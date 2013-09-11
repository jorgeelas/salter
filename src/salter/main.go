package main

import "fmt"
import "flag"
import "strings"
import "os"
import "syscall"

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

var G_CONFIG  Config
var G_REGIONS map[string]*Region

func usage() {
	fmt.Println("usage: salter <options> <command>")
	flag.PrintDefaults()
	os.Exit(-1)
}

func init() {
	G_REGIONS = make(map[string]*Region)
}

func main() {
	var targets Targets
	var configFile string
	var all bool

	// Setup command line flags
	flag.StringVar(&configFile, "c", "salter.cfg", "Configuration file")
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

	// Make config globally available
	G_CONFIG = config

	// Walk all the target nodes, caching info about their regions
	for _, node := range G_CONFIG.Targets {
		_, err := GetRegion(node.RegionId)
		if err != nil {
			panic(fmt.Sprintf("Failed to load region for %s: %+v\n",
				node.RegionId, err))
		}
	}

	switch flag.Arg(0) {
	case "launch":
		launch()
	case "teardown":
		teardown()
	case "ssh":
		sshto()
	}
}


func sshto() {
	count := len(G_CONFIG.Targets)
	if count > 1 || count < 1 {
		fmt.Printf("Only one node may be used with the ssh command.\n")
		return
	}

	for _, node := range G_CONFIG.Targets {
		err := node.Update()
		if err != nil {
			fmt.Printf("Unable to retrieve status of %s from AWS: %+v\n",
				node.Name, err)
			return
		}

		if !node.IsRunning() {
			fmt.Printf("Node %s is not running.\n", node.Name)
			return
		}

		key := RegionKey(node.KeyName, node.RegionId)

		args := []string {
			"ssh", "-i", key.Filename,
			"-o", "LogLevel=FATAL",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "ForwardAgent=yes",
			"-l", G_CONFIG.Aws.Username,
			node.Instance.DNSName }

		env := []string {
			"TERM=" + os.Getenv("TERM"),
		}

		fmt.Printf("Connecting to %s (%s)...\n", node.Name, node.Instance.InstanceId)
		syscall.Exec("/usr/bin/ssh", args, env)
	}
}
