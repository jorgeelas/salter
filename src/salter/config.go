package main

import "io"
import "fmt"
import "os"
import "path/filepath"
import "crypto/md5"
import "github.com/BurntSushi/toml"
import "github.com/dizzyd/goamz/aws"

type Config struct {
	Nodes     map[string]NodeConfig
	Tags      map[string]TagConfig
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
	Tags    map[string]string
}

type TagConfig map[string]string

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
			// Multiple instances of this type of node have been
			// requested. It's possible in the config file to
			// override individual specifications for nodes, so as
			// we go through and dynamically generate nodes, see if
			// an override already exists and use it
			for i := 0; i < node.Count; i++ {
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

				resolvedNodes[key] = n
				fmt.Printf("%s = %+v\n", key, n)
			}
		} else {
			node.applyAwsDefaults(config.Aws)
			node.Tags = config.Tags[id]
			resolvedNodes[id] = node
			fmt.Printf("%s = %+v\n", id, node)
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
