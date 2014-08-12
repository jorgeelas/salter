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

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"text/template"

	"github.com/BurntSushi/toml"
	"github.com/mitchellh/goamz/aws"
)

type Config struct {
	Aws     AwsConfig
	DataDir string
	SGroups map[string]SGroupConfig
	Salt    SaltConfig
	Tags    map[string]TagMap
	Targets map[string]*Node

	// This is the lsit of raw "Node" configuration elements in the config
	// file. If a user defines a node named "node" and a count of 5 then
	// this would contain a single element "node" while the field Nodes
	// would contain 5 elements, "node1" -> "node5".
	RawNodes map[string]*Node `toml:"nodes"`

	// None of the following values are provided by the user and as such
	// we disable tomls ability to populate them.

	// This is populated from environment variables.
	AwsAuth aws.Auth `toml:"-"`

	// This is the raw data returned from the toml parser.
	metaData toml.MetaData `toml:"-"`

	// This is the template used when setting up the AWS UserData element
	// for the cloud-init package.
	UserDataTemplate template.Template `toml:"-"`

	// This will contain a list of all defined nodes, exploded based on
	// the count variable.
	Nodes map[string]*Node `toml:"-"`
}

type AwsConfig struct {
	Ami      string `toml:"ami"`
	Flavor   string `toml:"flavor"`
	KeyName  string `toml:"keyname"`
	RegionId string `toml:"region"`
	SGroup   string `toml:"sgroup"`
	Username string `toml:"ssh_username"`
	Zone     string `toml:"zone"`
}

type SGroupConfig struct {
	Rules []string
}

type SaltConfig struct {
	RootDir      string `toml:"root"`
	Grains       map[string]string
	Timeout      int    `toml:"timeout"`
	UserDataFile string `toml:"userdata"`
}

// Loads the configuration from filename.
func LoadConfig(filename string) (config *Config, err error) {
	// This is the value we will return on success.
	config = &Config{}

	// Attempt to load the AWS auth data provided in AWS_ACCESS_KEY and
	// AWS_SECRET_KEY for use later. Not having these environment variables
	// is an error at the moment, though in the future this may not be
	// strictly required.
	if config.AwsAuth, err = aws.EnvAuth(); err != nil {
		return nil, err
	}

	// Next we attempt to parse the config file into our configuration
	// structure using the TOML library.
	if config.metaData, err = toml.DecodeFile(filename, config); err != nil {
		return nil, err
	}

	// Convert the RawNodes field into the Nodes field by expanding each
	// node definition out into a fully exploded list of all nodes that are
	// configured.
	config.Nodes = make(map[string]*Node, len(config.RawNodes))
	for id, node := range config.RawNodes {
		// The easiest case here is that the node has no count. In this case
		// the node name matches the id and there is no expansion.
		if node.Count == 0 {
			// If the node has already been defined then we are likely
			// processing a "child" of a clustered node definition. In this
			// case the clustered definition will always win.
			if _, exist := config.Nodes[id]; exist {
				continue
			}

			// Otherwise we assume that this is a stand alone node, so we
			// add it using the simple method.
			nodeData := new(Node)
			*nodeData = *node
			nodeData.Name = id

			// We also copy any of the AWS specific configuration into the
			// node so it can pick up default values like key, or flavor.. etc.
			inheritFieldsIfEmpty(&nodeData.AwsConfig, &config.Aws)

			// Add the Tags for this node.
			node.Tags = config.Tags[id]
			if node.Tags == nil {
				node.Tags = make(TagMap)
			}

			// Add the Node to the map.
			config.Nodes[id] = nodeData
			continue
		}

		// In this situation we need to iterate through the count and add each
		// node to the Nodes map if its not already present. Since a node can
		// actually be defined as part of a count, AND as a stand alone item
		// we need to be safe about not overwriting data here.
		for i := uint(1); i <= node.Count; i++ {
			name := fmt.Sprintf("%s%d", id, i)
			nodeData := new(Node)
			childData, exist := config.RawNodes[name]
			if exist {
				// This node has specific configuration. We need to ensure
				// that data in the specific config is prioritized over data
				// in the parent node definition we are processing.
				*nodeData = *childData
				inheritFieldsIfEmpty(nodeData, node)
			} else {
				// Otherwise we can make a copy of the node being processed.
				*nodeData = *node
			}

			// Next we populate the Tags field from the config, selecting the
			// value specific to this node first, and if that doesn't exist
			// we select the parent value, and barring that we select an empty
			// TagMap.
			if tags, exist := config.Tags[name]; exist {
				nodeData.Tags = tags
			} else if tags, exist = config.Tags[id]; exist {
				nodeData.Tags = tags
			} else {
				nodeData.Tags = make(TagMap)
			}

			// We also copy any of the AWS specific configuration into the
			// node so it can pick up default values like key, or flavor.. etc.
			inheritFieldsIfEmpty(&nodeData.AwsConfig, &config.Aws)

			// And finally we add it to the map of defined nodes.
			nodeData.Name = name
			nodeData.Count = 0
			config.Nodes[name] = nodeData
		}
	}

	// Setup default salter configurations.
	if config.Salt.Timeout == 0 {
		config.Salt.Timeout = 60
	}
	if config.Salt.UserDataFile == "" {
		config.Salt.UserDataFile = "bootstrap/user.data"
	}

	// FIXME

	// If a user-data file is specified, use that to construct a template
	userDataTemplate, err := template.ParseFiles(config.Salt.UserDataFile)
	if err != nil {
		return nil, err
	}
	config.UserDataTemplate = *userDataTemplate

	// FIXME ^^^^

	return
}

// This function will automatically select all nodes that match a given
// glob expression.
func (c *Config) Glob(targets []string) (map[string]*Node, error) {
	matches := make(map[string]*Node)
	for name, node := range c.Nodes {
		for _, target := range targets {
			if match, err := filepath.Match(target, name); err != nil {
				// The only error here that can happen is if the pattern is bad.
				return nil, err
			} else if match {
				matches[name] = node
			}
		}
	}
	return matches, nil
}

// Selects all nodes that match a given regular expression.
func (c *Config) Regex(targets []string) (map[string]*Node, error) {
	// Compile the regular expression first.
	// Now Match the nodes.
	matches := make(map[string]*Node)
	for _, target := range targets {
		regex, err := regexp.Compile(target)
		if err != nil {
			return nil, err
		}
		for name, node := range c.Nodes {

			if regex.MatchString(name) {
				matches[name] = node
			}
		}
	}
	return matches, nil
}

type userDataVars struct {
	Hostname     string
	SaltMasterIP string
	Roles        []string
	IsMaster     bool
	Grains       map[string]string
	Environment  string
}

func (config *Config) generateUserData(host string, roles []string, masterIp string) ([]byte, error) {
	var userDataBuf bytes.Buffer
	err := config.UserDataTemplate.Execute(&userDataBuf,
		userDataVars{
			Hostname:     host,
			SaltMasterIP: masterIp,
			Roles:        roles,
			IsMaster:     (masterIp == "127.0.0.1"),
			Grains:       config.Salt.Grains,
			Environment:  "test",
		})
	if err != nil {
		errorf("Failed to generate user-data for %s: %+v\n", host, err)
		return nil, err
	}
	return userDataBuf.Bytes(), nil
}

func (config *Config) findNodeByRole(role string) *Node {
	for _, node := range config.Nodes {
		for _, r := range node.Roles {
			if r == role {
				return node
			}
		}
	}
	return nil
}

// Initializes the directory that stores the AWS key used for connecting to
// nodes. Each directory is a hash of AWS_ACCESS_KEY and AWS_SECRET_KEY in
// order to ensure that individual accounts don't tromp all over each other.
// The provided directory is the location that configuration data should
// be kept.
func (config *Config) InitDataDir(baseDir string) error {
	// Generate the hash for the two keys.
	hash := md5.New()
	io.WriteString(hash, config.AwsAuth.AccessKey)
	io.WriteString(hash, config.AwsAuth.SecretKey)
	hashStr := fmt.Sprintf("%x", hash.Sum(nil))

	// Make a data directory for this specific pairing under the
	// base directory.
	config.DataDir = filepath.Join(baseDir, "data", hashStr)

	// Make the data directory if necessary,
	if err := os.MkdirAll(config.DataDir, 0700); err != nil {
		return err
	}

	// Write the directory that is being used for this instance to the debug
	// logs.
	debugf("Using data dir: %s\n", config.DataDir)
	return nil
}
