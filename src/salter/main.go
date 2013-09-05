package main

// Action
// - launch
// - teardown
// - cmd
// - ssh
// - highstate

import "fmt"
import "flag"
import "strings"
import "os"

type Targets []string

func (t *Targets) String() string {
	return fmt.Sprint(*t)
}

func (t *Targets) Set(value string) error {
	for _, s := range strings.Split(value, ",") {
		*t = append(*t, s)
	}
	return nil
}

var config Config
var configFile string
var targets Targets
var all bool

func usage() {
	fmt.Println("usage: salter <options> <command>")
	flag.PrintDefaults()
	os.Exit(-1)
}

func main() {
	// Setup command line flags
	flag.StringVar(&configFile, "f", "salter.cfg", "Configuration file")
	flag.BoolVar(&all, "a", false, "Apply operations to all nodes")
	flag.Var(&targets, "n", "Target nodes for the operation (overrides -a flag)")

	// Parse it up
	flag.Parse()

	// If parse failed, bail
	if !flag.Parsed() || flag.NArg() != 1 {
		usage()
	}

	// Initialize the config file
	config, err := NewConfig(configFile, targets, all)
	if err != nil {
		fmt.Printf("Failed to load config from %s: %+v\n", configFile, err)
		os.Exit(-1)
	}

	// Walk all the target nodes, caching info about their regions
	for _, node := range config.Targets {
		_, err := GetRegion(node.Region, config)
		if err != nil {
			panic(fmt.Sprintf("Failed to load region for %s: %+v\n", node.Region, err))
		}
	}

	// fmt.Printf("Config file: %+v\n", configFile)
	// fmt.Printf("Targets: %+v\n", targets)
	// fmt.Printf("All: %+v\n", all)
	// fmt.Printf("Command: %+v\n", flag.Arg(0))
	// fmt.Printf("Config: %+v\n", config)
	// fmt.Printf("---\nTargetNodes: %+v\n", config.SGroups)

	switch flag.Arg(0) {
	case "launch":
		launch(config)
	case "teardown":
		teardown(config)
	}
}
