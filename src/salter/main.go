package main

import "fmt"
import "flag"
import "strings"
import "os"
import "sort"
import "syscall"
import "path"
import "log"
import "github.com/BurntSushi/ty/fun"

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
var G_DIR     string

func usage() {
	fmt.Println("usage: salter <options> <command>")
	flag.PrintDefaults()
	os.Exit(-1)
}

func init() {
	G_DIR = path.Join(os.ExpandEnv("$HOME"), ".salter")
	G_REGIONS = make(map[string]*Region)
}

func main() {
	var targets Targets
	var configFile string
	var all bool

	// Initialize data directory if it doesn't already exist
	os.Mkdir(G_DIR, 0700)

	// Setup logging subsystem
	logFilename := path.Join(G_DIR, "log")
	logFile, err := os.OpenFile(logFilename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("Could not open %s: %s\n", logFilename, err)
		os.Exit(1)
	}
	defer logFile.Close()

	// Direct all logging output to the log file
	log.SetOutput(logFile)

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

	// Special cases for -a / -n
	if flag.Arg(0) == "hosts" {
		all = true
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
	case "hosts":
		hosts()
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


func hosts() {
	// Update all the targets with latest instance info
	pForEachValue(G_CONFIG.Targets, (*Node).Update, 10)

	// Get a list of all the keys and sort them
	names := fun.Keys(G_CONFIG.Targets).([]string)
	sort.Strings(names)

	// Print each entry
	for _, name := range names {
		node := G_CONFIG.Targets[name]
		if node.Instance != nil {
			fmt.Printf("%s\t%s\n", node.Instance.IpAddress, node.Name)
		}
	}
}
