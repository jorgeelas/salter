// -------------------------------------------------------------------
//
// salter: Tool for bootstrap salt clusters in EC2
//
// Copyright (c) 2013 David Smith (dizzyd@dizzyd.com). All Rights Reserved.
//
// This file is provided to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file
// except in compliance with the License.  You may obtain
// a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.
//
// -------------------------------------------------------------------
package main

import "io"
import "fmt"
import "os"
import "path/filepath"
import "crypto/md5"
import "github.com/BurntSushi/toml"
import "github.com/mitchellh/goamz/aws"
import "text/template"
import "bytes"
import "regexp"

type Config struct {
	Nodes        map[string]Node
	Tags         map[string]TagMap
	Aws          AwsConfig
	Salt         SaltConfig
	Raw          interface{}
	Targets      map[string]Node
	SGroups      map[string]SGroupConfig
	AwsAuth      aws.Auth
	DataDir      string

	UserDataTemplate template.Template
	MaxConcurrent    int
}

type AwsConfig struct {
	Username string `toml:"ssh_username"`
	Flavor   string
	RegionId string `toml:"region"`
	Zone     string `toml:"zone"`
	Ami      string
	SGroup   string
	KeyName  string
}

type SGroupConfig struct {
	Rules []string
}

type SaltConfig struct {
	RootDir string `toml:"root"`
	Grains map[string]string
	Timeout int
	UserDataFile string `toml:"userdata"`
}

func NewConfig(filename string, targets []string, all bool) (config Config, err error) {
	raw, err := toml.DecodeFile(filename, &config)
	if err != nil {
		return
	}

	// Inherited fields for a node
	inheritedFields := []string {"Username", "Flavor", "RegionId", "Ami", "SGroup", "KeyName", "Zone"}

	// Expand the list of nodes so that we have an entry per individual node
	resolvedNodes := make(map[string]Node)
	for id, node := range config.Nodes {
		node.Name = id
		if node.Count > 0 {
			// Multiple instances of this type of node have been
			// requested. It's possible in the config file to
			// override individual specifications for nodes, so as
			// we go through and dynamically generate nodes, see if
			// an override already exists and use it
			for i := 1; i <= node.Count; i++ {
				key := fmt.Sprintf("%s%d", id, i)

				// Get the config specific to this key, or fallback
				// to template base
				n, exists := config.Nodes[key]
				if exists {
					inheritFieldsIfEmpty(&n, node, inheritedFields)
				} else {
					n = node
				}

				// Get the tags specific to this generated node,
				// or fallback to generic identifier
				tags, exists := config.Tags[key]
				if !exists { tags = config.Tags[id] }

				if tags == nil {
					tags = make(TagMap)
				}

				inheritFieldsIfEmpty(&n, config.Aws, inheritedFields)
				n.Name = key
				n.Count = 0
				n.Tags = tags
				n.Config = &config

				resolvedNodes[key] = n
			}
		} else {
			inheritFieldsIfEmpty(&node, config.Aws, inheritedFields)
			node.Config = &config
			node.Tags = config.Tags[id]
			if node.Tags == nil {
				node.Tags = make(TagMap)
			}
			resolvedNodes[id] = node
		}
	}

	// Setup core salt config values
	if config.Salt.Timeout == 0 {
		config.Salt.Timeout = 60
	}

	if config.Salt.UserDataFile == "" {
		config.Salt.UserDataFile = "bootstrap/user.data"
	}


	// Load AWS auth info from environment
	auth, err := aws.EnvAuth()
	if err != nil {
		err = fmt.Errorf("Failed to load AWS auth from environment: %s\n", err)
		return
	}

	// If a user-data file is specified, use that to construct a template
	userDataTemplate, err := template.ParseFiles(config.Salt.UserDataFile)
	if err != nil {
		err = fmt.Errorf("Failed to load user data template from %s: %+v\n",
			config.Salt.UserDataFile, err)
		return
	}
	config.UserDataTemplate = *userDataTemplate


	// Initialize our result
	config.AwsAuth = auth
	config.MaxConcurrent = 5
	config.Nodes = resolvedNodes
	config.Raw = raw
	config.Targets = make(map[string]Node)

	// Identify all targeted nodes
	switch {
	case len(targets) > 0:
		for _, t := range targets {
			selectNodes(t, config.Nodes, &(config.Targets))
		}

		// If no nodes were actually selected; warn and bail
		if len(config.Targets) < 1 {
			err = fmt.Errorf("No nodes matched the provided names!")
			return
		}
	case all == true:
		config.Targets = config.Nodes
	}

	// Initialize data directory
	config.initDataDir()

	return
}

func selectNodes(target string, nodes map[string]Node, matched *map[string]Node) {
	if ARG_REGEX {
		// Try to compile the target into a regex
		regex, err := regexp.Compile(target)
		if err != nil {
			// Not a regex; warn and bail
			fmt.Printf("%s is not a valid regex: %+v\n", target, err)
			return
		}

		// Find all the node names that match our regex
		for name, node := range nodes {
			if regex.MatchString(name) {
				(*matched)[name] = node
			}
		}
	} else {
		// Find all the node names that match our glob
		for name, node := range nodes {
			matches, _ := filepath.Match(target, name)
			if matches {
				(*matched)[name] = node
			}
		}
	}
}

type UserDataVars struct {
	Hostname string
	SaltMasterIP string
	Roles []string
	IsMaster bool
	Grains map[string]string
	Environment string
}

func (config *Config) generateUserData(host string, roles []string, masterIp string) ([]byte, error) {
	var userDataBuf bytes.Buffer
	err := config.UserDataTemplate.Execute(&userDataBuf,
		UserDataVars{
			Hostname: host,
			SaltMasterIP: masterIp,
			Roles: roles,
			IsMaster: (masterIp == "127.0.0.1"),
			Grains: config.Salt.Grains,
			Environment: "test",
		})
	if err != nil {
		fmt.Printf("Failed to generate user-data for %s: %+v\n", host, err)
		return nil, err
	}
	return userDataBuf.Bytes(), nil
}


func (config *Config) findNodeByRole(role string) *Node {
	for _, node := range config.Nodes {
		for _, r := range node.Roles {
			if r == role {
				return &node
			}
		}
	}
	return nil
}


func (config* Config) initDataDir() {
	// The data directory is always suffixed with a hash of the AWS access
	// key + secret to ensure that individual accounts don't tromp all over
	// each other.
	hash := md5.New()
	io.WriteString(hash, config.AwsAuth.AccessKey)
	io.WriteString(hash, config.AwsAuth.SecretKey)
	hashStr := fmt.Sprintf("%x", hash.Sum(nil))

	config.DataDir = filepath.Join(os.ExpandEnv("$HOME/.salter/data"), hashStr)

	err := os.MkdirAll(config.DataDir, 0700)
	if err != nil {
		panic(fmt.Sprintf("Failed to initialize data dir %s: %s\n",
			config.DataDir, err))
	}

	fmt.Printf("Using data dir: %s\n", config.DataDir)
}
