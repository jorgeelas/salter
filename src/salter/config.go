package main

import "io"
import "fmt"
import "os"
import "path/filepath"
import "crypto/md5"
import "github.com/BurntSushi/toml"
import "github.com/dizzyd/goamz/aws"
import "text/template"
import "bytes"

type Config struct {
	Nodes        map[string]Node
	Tags         map[string]TagConfig
	Aws          AwsConfig
	Salt         SaltConfig
	Raw          interface{}
	Targets      map[string]Node
	SGroups      map[string]SGroupConfig
	AwsAuth      aws.Auth
	DataDir      string
	UserDataFile string

	UserDataTemplate template.Template
	MaxConcurrent    int
}

type TagConfig map[string]string

type AwsConfig struct {
	Username string `toml:"ssh_username"`
	Flavor   string
	RegionId string `toml:"region"`
	Ami      string
	SGroup   string
	KeyName  string
}

type SGroupConfig struct {
	Rules []string
}

type SaltConfig struct {
	RootDir string `toml:"root"`
}

func NewConfig(filename string, targets []string, all bool) (config Config, err error) {
	raw, err := toml.DecodeFile(filename, &config)
	if err != nil {
		return
	}

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
				if !exists { n = node }

				// Get the tags specific to this generated node,
				// or fallback to generic identifier
				tags, exists := config.Tags[key]
				if !exists { tags = config.Tags[id] }

				n.applyAwsDefaults(config.Aws)
				n.Name = key
				n.Count = 0
				n.Tags = tags
				n.Config = &config

				resolvedNodes[key] = n
			}
		} else {
			node.applyAwsDefaults(config.Aws)
			node.Config = &config
			node.Tags = config.Tags[id]
			resolvedNodes[id] = node
		}
	}

	// Load AWS auth info from environment
	auth, err := aws.EnvAuth()
	if err != nil {
		fmt.Printf("Failed to load AWS auth from environment: %s\n", err)
		return
	}

	// If a user-data file is specified, use that to construct a template
	userDataTemplate, err := template.ParseFiles("bootstrap/user.data")
	if err != nil {
		fmt.Printf("Failed to load user data template from %s: %+v\n",
			config.UserDataFile, err)
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
			n, ok := config.Nodes[t]
			if ok { config.Targets[t] = n }
		}
	case all == true:
		config.Targets = config.Nodes
	}

	// Initialize data directory
	config.initDataDir()

	return
}

func (node *Node) applyAwsDefaults(aws AwsConfig) {
	if node.Flavor    == "" { node.Flavor = aws.Flavor }
	if node.Ami       == "" { node.Ami = aws.Ami }
	if node.RegionId  == "" { node.RegionId = aws.RegionId }
	if node.SGroup    == "" { node.SGroup = aws.SGroup }
	if node.KeyName   == "" { node.KeyName = aws.KeyName }
}


type UserDataVars struct {
	Hostname string
	SaltMasterIP string
	Roles []string
	IsMaster bool
}

func (config *Config) generateUserData(host string, roles []string, masterIp string) ([]byte, error) {
	var userDataBuf bytes.Buffer
	err := config.UserDataTemplate.Execute(&userDataBuf,
		UserDataVars{
			Hostname: host,
			SaltMasterIP: masterIp,
			Roles: roles,
			IsMaster: (masterIp == "127.0.0.1"),
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
