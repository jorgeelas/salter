package main

import "io"
import "fmt"
import "os"
import "path/filepath"
import "crypto/md5"
import "github.com/BurntSushi/toml"
import "launchpad.net/goamz/aws"

type Config struct {
	Nodes     map[string]NodeConfig
	Aws       AwsConfig
	Raw       interface{}
	Targets   map[string]NodeConfig
	SGroups   map[string]SGroupConfig
	AwsAuth   aws.Auth
	DataDir   string

	MaxConcurrent int
}

type NodeConfig struct {
	Name    string
	Roles   []string
	Count   int
	Flavor  string
	Region  string
	Zone    string
	Ami     string
	SGroup  string
	Keyname string
}

type AwsConfig struct {
	Username string `toml: "ssh_username"`
	Flavor   string
	Region   string
	Ami      string
	SGroup   string
	Keyname  string
}

type SGroupConfig struct {
	Rules []string
}

func NewConfig(filename string, targets []string, all bool) (config Config, err error) {
	raw, err := toml.DecodeFile(filename, &config)
	if err != nil {
		return
	}

	// Expand the list of nodes so that we have an entry per individual node
	resolvedNodes := make(map[string]NodeConfig)
	for id, node := range config.Nodes {
		node.Name = id
		if node.Count > 0 {
			for i := 0; i < node.Count; i++ {
				n := node
				n.applyAwsDefaults(config.Aws)
				n.Count = 0
				key := fmt.Sprintf("%s%d", id, i)
				resolvedNodes[key] = n
			}
		} else {
			node.applyAwsDefaults(config.Aws)
			resolvedNodes[id] = node
		}
	}

	// Load AWS auth info from environment
	auth, err := aws.EnvAuth()
	if err != nil {
		fmt.Printf("Failed to load AWS auth from environment: %s\n", err)
		return
	}


	// Initialize our result
	config.AwsAuth = auth
	config.MaxConcurrent = 2
	config.Nodes = resolvedNodes
	config.Raw = raw
	config.Targets = make(map[string]NodeConfig)

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

func (node *NodeConfig) applyAwsDefaults(aws AwsConfig) {
	if node.Flavor  == "" { node.Flavor = aws.Flavor }
	if node.Ami     == "" { node.Ami = aws.Ami }
	if node.Region  == "" { node.Region = aws.Region }
	if node.SGroup  == "" { node.SGroup = aws.SGroup }
	if node.Keyname == "" { node.Keyname = aws.Keyname }
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
