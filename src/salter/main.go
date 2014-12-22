// -------------------------------------------------------------------
//
// salter: Tool for bootstrap salt clusters in EC2
//
// Copyright (c) 2013-2014 Orchestrate, Inc. All Rights Reserved.
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
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path"
	"sort"
	"strings"
	"syscall"

	"github.com/BurntSushi/ty/fun"
)

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

type TagMap map[string]string

func (t *TagMap) String() string {
	return fmt.Sprint(*t)
}

func (t *TagMap) Set(value string) error {
	for _, s := range strings.Split(value, ",") {
		kv := strings.Split(s, "=")
		(*t)[kv[0]] = kv[1]
	}
	return nil
}

func (t *TagMap) Merge(otherTags TagMap) {
	for k, v := range otherTags {
		if k != "Name" {
			(*t)[k] = v
		}
	}
}

type Command struct {
	// Function called to execute the command.
	Fn func() error

	// The usage string that should be displayed for the command.
	Usage string

	// Does this command use nodes? (-g -n/-r -n/-a)
	Nodes bool

	// Does this command use salt targets? (-t)
	Target bool

	// Does this command use tags?
	Tags bool

	// If this is true then -a is default if no other arguments are
	// passed.
	DefaultAll bool
}

var G_CONFIG *Config
var G_TARGETS map[string]*Node
var G_DIR string
var G_COMMANDS map[string]Command

var ARG_TARGETS Targets
var ARG_CONFIG_FILE string
var ARG_ALL bool
var ARG_SALT_TARGETS string
var ARG_SALT_TARGETS_DEFAULT string = "*"
var ARG_TAGS TagMap
var ARG_GLOB bool
var ARG_REGEX bool
var ARG_PARALLEL int = 10

// Displays usage information for the flags library.
func usage() error {
	errorf("usage: salter <options> <command>\n")
	errorf(" options:\n")
	flag.PrintDefaults()
	errorf(" commands:\n")

	cmds := fun.Keys(G_COMMANDS).([]string)
	sort.Strings(cmds)

	for _, cmd := range cmds {
		errorf("  * %-12s %s\n", cmd, G_COMMANDS[cmd].Usage)
	}

	return nil
}

func init() {
	ARG_TAGS = make(map[string]string)

	// Get the current users home directory so we can set the default path
	// for salter config/log files.
	if usr, err := user.Current(); err != nil {
		fatalf("Unable to obtain the current users home directory: %s", err)
		os.Exit(1)
	} else {
		G_DIR = path.Join(usr.HomeDir, ".salter")
	}

	// Setup the map of sub commands.
	G_COMMANDS = map[string]Command{
		"bootstrap": Command{
			Fn:    bootstrap,
			Usage: "Upload Salt configuration and highstate master.",
			Nodes: true,
		},
		"csshx": Command{
			Fn:    csshx,
			Usage: "open a series of SSH sessions to EC2 instances via csshX",
			Nodes: true,
		},
		"dump": Command{
			Fn:         dump,
			Usage:      "dump generated node definitions",
			Nodes:      true,
			DefaultAll: true,
		},
		"help": Command{
			Fn:    usage,
			Usage: "display help",
		},
		"highstate": Command{
			Fn:     highstate,
			Usage:  "invoke Salt highstate on the Salt master",
			Target: true,
		},
		"hosts": Command{
			Fn:         hosts,
			Usage:      "generate a list of live nodes on EC2",
			Nodes:      true,
			DefaultAll: true,
		},
		"info": Command{
			Fn:         info,
			Usage:      "display internal/external IP addresses for nodes",
			Nodes:      true,
			DefaultAll: true,
		},
		"launch": Command{
			Fn:    launch,
			Usage: "launch instances on EC2",
			Nodes: true,
		},
		"sgroups": Command{
			Fn:    sgroups,
			Usage: "generate security groups from configuration",
			Nodes: true,
		},
		"ssh": Command{
			Fn:    sshto,
			Usage: "open a SSH session to a EC2 instance",
			Nodes: true,
		},
		"tag": Command{
			Fn:    tag,
			Usage: "(re)apply tags to each AWS node",
			Nodes: true,
			Tags:  true,
		},
		"teardown": Command{
			Fn:    teardown,
			Usage: "terminates instances on EC2",
			Nodes: true,
		},
		"upload": Command{
			Fn:    upload,
			Usage: "upload Salt configuration to the Salt master",
		},
	}

}

func main() {
	// Initialize data directory if it doesn't already exist
	os.Mkdir(G_DIR, 0700)

	// Initialize the log file.
	setupLogging()

	// Log run info
	debugf("--- %s ---\n", os.Args)
	cwd, _ := os.Getwd()
	debugf("Cwd: %s\n", cwd)

	//
	// Argument Parsing
	//

	// Setup command line flags
	flag.StringVar(&ARG_CONFIG_FILE, "c", "salter.cfg",
		"Configuration file")
	flag.BoolVar(&ARG_ALL, "a", false,
		"Apply operations to all nodes")
	flag.Var(&ARG_TARGETS, "n",
		"Target nodes for the operation (overrides -a flag)")
	flag.StringVar(&ARG_SALT_TARGETS, "s", ARG_SALT_TARGETS_DEFAULT,
		"Targets for salt-related operations")
	flag.Var(&ARG_TAGS, "t", "Tags to apply")
	flag.BoolVar(&ARG_GLOB, "g", false,
		"Use globbing with the -n parameter to select nodes")
	flag.BoolVar(&ARG_REGEX, "r", false,
		"Use regexes with the -n parameter to select nodes")

	// Parse it up
	flag.Parse()

	// If parse failed, bail
	if !flag.Parsed() || flag.NArg() != 1 {
		usage()
		os.Exit(1)
	}

	// Find the command the user is invoking
	cmdName := flag.Arg(0)
	cmd, found := G_COMMANDS[cmdName]
	if !found {
		errorf("ERROR: unknown command '%s'\n", flag.Arg(0))
		usage()
		os.Exit(-1)
	}

	// See if the -s flag was used properly.
	if cmd.Target && ARG_SALT_TARGETS == "" {
		fatalf("-s can not contain an empty string.\n")
	} else if !cmd.Target && ARG_SALT_TARGETS != ARG_SALT_TARGETS_DEFAULT {
		fatalf("-s is not valid with %s.\n", cmdName)
	}

	// See if the -a/-n/-g/-r flags were used properly.
	if cmd.Nodes && ARG_ALL && len(ARG_TARGETS) != 0 {
		fatalf("-a and -n are mutually exclusive.\n")
	} else if cmd.Nodes && ARG_ALL && ARG_REGEX {
		fatalf("-a and -r are mutually exclusive.\n")
	} else if cmd.Nodes && ARG_ALL && ARG_GLOB {
		fatalf("-a and -g are mutually exclusive.\n")
	} else if cmd.Nodes && !ARG_ALL && len(ARG_TARGETS) == 0 {
		if cmd.DefaultAll {
			ARG_ALL = true
		} else {
			fatalf("%s requires either -a or a target (-n).\n", cmdName)
		}
	} else if cmd.Nodes && !ARG_ALL && ARG_REGEX && ARG_GLOB {
		fatalf("-g and -r are mutually exclusive.\n")
	} else if cmd.Nodes && !ARG_ALL && !ARG_REGEX && !ARG_GLOB {
		fatalf("-n requires one of -g or -r.\n")
	} else if !cmd.Nodes && ARG_ALL {
		fatalf("-a is not valid with %s.\n", cmdName)
	} else if !cmd.Nodes && len(ARG_TARGETS) != 0 {
		fatalf("-n is not valid with %s.\n", cmdName)
	} else if !cmd.Nodes && ARG_REGEX {
		fatalf("-r is not valid with %s.\n", cmdName)
	} else if !cmd.Nodes && ARG_GLOB {
		fatalf("-g is not valid with %s.\n", cmdName)
	}

	// See if the -t flag was used properly.
	if cmd.Tags && len(ARG_TAGS) == 0 {
		fatalf("%s requires tags to add (-t).\n", cmdName)
	} else if len(ARG_TAGS) != 0 {
		fatalf("-t is not valid with %s.\n", cmdName)
	}

	//
	// End Argument Parsing
	//

	// Initialize the config file
	var err error
	if G_CONFIG, err = LoadConfig(ARG_CONFIG_FILE); err != nil {
		fatalf("Failed to load config from %s: %s\n", ARG_CONFIG_FILE, err)
		os.Exit(1)
	}

	// Create the data directory for this cluster.
	if err := G_CONFIG.InitDataDir(G_DIR); err != nil {
		fatalf("Failed to initialize the data directory: %s\n", err)
		os.Exit(1)
	}

	// Setup the targets for the configuration. This uses the arguments that
	// were passed in to select a subset of the nodes loaded via LoadConfig.
	if ARG_GLOB {
		// -g -n
		if G_TARGETS, err = G_CONFIG.Glob(ARG_TARGETS); err != nil {
			fatalf("%s", err)
		}
	} else if ARG_REGEX {
		// -r -n
		if G_TARGETS, err = G_CONFIG.Regex(ARG_TARGETS); err != nil {
			fatalf("%s", err)
		}
	} else if ARG_ALL {
		// -a
		G_TARGETS = G_CONFIG.Nodes
	}

	// Start region cache
	StartRegionCache(G_CONFIG)

	// Walk all the nodes, caching info about their regions
	for _, node := range G_CONFIG.Nodes {
		_, err := GetRegion(node.RegionId)
		if err != nil {
			panic(fmt.Sprintf("Failed to load region for %s: %+v\n",
				node.RegionId, err))
		}
	}

	cmd.Fn()
}

func sshto() error {
	target := fun.Keys(G_TARGETS).([]string)
	if len(target) != 1 {
		errorf("Only one node may be used with the ssh command.\n")
		return fmt.Errorf("More than one target")
	}

	node := G_TARGETS[target[0]]
	err := node.Update()
	if err != nil {
		errorf("Unable to retrieve status of %s from AWS: %+v\n",
			node.Name, err)
		return err
	}

	if !node.IsRunning() {
		errorf("Node %s is not running.\n", node.Name)
		return fmt.Errorf("%s is not running", node.Name)
	}

	key := RegionKey(node.KeyName, node.RegionId)

	args := []string{
		"ssh",
		"-i", key.Filename,
		"-o", "LogLevel=FATAL",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ForwardAgent=yes",
		"-l", G_CONFIG.Aws.Username,
		node.Instance.DNSName,
	}

	env := []string{
		"TERM=" + os.Getenv("TERM"),
	}

	printf("Connecting to %s (%s)...\n", node.Name, node.Instance.InstanceId)
	closeFrom(3)
	err = syscall.Exec("/usr/bin/ssh", args, env)
	fmt.Println("Failed to execute: %s", err)
	syscall.Exit(1)
	return nil
}

func csshx() error {
	// Make sure we can find an instance of csshX on the path
	csshPath, err := exec.LookPath("csshX")
	if err != nil {
		errorf("Unable to find csshX on your path.\n")
		return err
	}

	// Get a list of all the keys and sort them
	names := fun.Keys(G_TARGETS).([]string)
	sort.Strings(names)
	if len(names) < 1 {
		errorf("You must specify one or more targets!\n")
		return fmt.Errorf("At least one target must be specified")
	}

	// Update all the targets with latest instance info
	updateNodes(G_TARGETS, ARG_PARALLEL)

	// If any of the nodes are not running, bail with error
	allRunning := true
	for _, node := range G_TARGETS {
		if !node.IsRunning() {
			allRunning = false
			printf("%s is not running\n", node.Name)
		}
	}

	if !allRunning {
		printf("Some target nodes are not running on AWS; aborting.\n")
		return fmt.Errorf("Some target nodes are not running")
	}

	key := RegionKey(G_TARGETS[names[0]].KeyName, G_TARGETS[names[0]].RegionId)

	sshArgs := fmt.Sprintf("-i %s -o LogLevel=FATAL -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ForwardAgent=yes",
		key.Filename)

	args := []string{
		csshPath,
		"--ssh_args", sshArgs,
		"-l", G_CONFIG.Aws.Username,
	}

	printf("Connecting to:\n")

	for _, name := range names {
		printf(" * %s\n", name)
		args = append(args, G_TARGETS[name].Instance.PublicIpAddress)
	}

	env := []string{
		"HOME=" + os.Getenv("HOME"),
		"TERM=" + os.Getenv("TERM"),
	}

	err = syscall.Exec(csshPath, args, env)
	fmt.Println("Failed to execute: %s", err)
	syscall.Exit(1)
	return nil
}

func hosts() error {
	// Update all the targets with latest instance info
	updateNodes(G_TARGETS, ARG_PARALLEL)

	// Get a list of all the keys and sort them
	names := fun.Keys(G_TARGETS).([]string)
	sort.Strings(names)

	// Print each entry
	for _, name := range names {
		node := G_TARGETS[name]
		if node.Instance != nil {
			printf("%s\t%s\n", node.Instance.PublicIpAddress, node.Name)
		}
	}

	return nil
}

func info() error {
	// Update all the targets with latest instance info
	updateNodes(G_TARGETS, ARG_PARALLEL)

	// Get a list of all the keys and sort them
	names := fun.Keys(G_TARGETS).([]string)
	sort.Strings(names)

	// Print each entry
	for _, name := range names {
		node := G_TARGETS[name]
		if node.Instance != nil {
			printf("%s\t%s\t%s\n", node.Name,
				node.Instance.PublicIpAddress,
				node.Instance.PrivateIpAddress)
		}
	}

	return nil
}

func upload() error {
	// Find the master node
	node := G_CONFIG.findNodeByRole("saltmaster")
	if node == nil {
		errorf("Could not find a node with saltmaster role!\n")
		return fmt.Errorf("no saltmaster role")
	}

	// Get latest info from AWS
	err := node.Update()
	if err != nil {
		errorf("Failed to update info for %s: %+v\n", node.Name, err)
		return err
	}

	// If the node isn't running, bail
	if !node.IsRunning() {
		errorf("%s is not running.\n", node.Name)
		return fmt.Errorf("master isn't running")
	}

	// Lookup key for the node/region
	key := RegionKey(node.KeyName, node.RegionId)

	// Generate SSH sub-command
	sshCmd := fmt.Sprintf("ssh -l %s -i %s -o LogLevel=FATAL "+
		"-o StrictHostKeyChecking=no "+
		"-o UserKnownHostsFile=/dev/null", G_CONFIG.Aws.Username, key.Filename)

	// Run rsync
	rsync := exec.Command("rsync", "-rlptDuvz", "--delete",
		"--rsync-path=sudo rsync",
		"-e", sshCmd,
		G_CONFIG.Salt.RootDir+"/",
		fmt.Sprintf("%s@%s:/srv/salt", G_CONFIG.Aws.Username, node.Instance.DNSName))
	rsync.Stdout = os.Stdout
	rsync.Stderr = os.Stdout
	printf("Uploading %s to %s:/srv/salt...\n", G_CONFIG.Salt.RootDir, node.Instance.PublicIpAddress)
	err = rsync.Run()
	if err != nil {
		errorf("Rsync failed: %+v\n", err)
		return err
	}

	// Sync all nodes
	printf("Running saltutil.sync_all...\n")
	err = node.SshRun("sudo salt '*' --output=txt saltutil.sync_all")
	if err != nil {
		errorf("Failed to run saltutil.sync_all: %+v\n", err)
		return err
	}

	// Update mine functions
	printf("Running mine.update...\n")
	err = node.SshRun("sudo salt '*' --output=txt mine.update")
	if err != nil {
		errorf("Failed to run mine.update: %+v\n", err)
		return err
	}

	// Ensure that all pillars are up to date
	printf("Running saltutil.refresh_pillar...\n")
	err = node.SshRun("sudo salt '*' --output=txt saltutil.refresh_pillar")
	if err != nil {
		errorf("Failed to run saltutil.refresh_pillar: %+v\n", err)
		return err
	}

	return nil
}

func highstate() error {
	// Find the master node
	node := G_CONFIG.findNodeByRole("saltmaster")
	if node == nil {
		errorf("Could not find a node with saltmaster role!\n")
		return fmt.Errorf("no master")
	}

	// Get latest info from AWS
	err := node.Update()
	if err != nil {
		errorf("Failed to update info for %s: %+v\n", node.Name, err)
		return err
	}

	// If the node isn't running, bail
	if !node.IsRunning() {
		errorf("%s is not running.\n", node.Name)
		return fmt.Errorf("%s is not running", node.Name)
	}

	// Run the high state
	return saltHighstate(node, ARG_SALT_TARGETS)
}

func dump() error {
	// Get a list of all the keys and sort them
	names := fun.Keys(G_TARGETS).([]string)
	sort.Strings(names)

	// Print each entry
	for _, name := range names {
		printf("%s: %+v\n", name, G_TARGETS[name])
	}

	return nil
}

func bootstrap() error {
	// Upload data to master
	err := upload()
	if err != nil {
		return err
	}

	// Find the master node
	node := G_CONFIG.findNodeByRole("saltmaster")

	// Highstate just the master
	printf("Highstating %s...\n", node.Name)
	ARG_SALT_TARGETS = node.Name
	return highstate()
}

func tag() error {
	pForEachValue(G_TARGETS, func(n *Node) error {
		err := n.Update()
		if err != nil {
			return err
		}
		n.Tags.Merge(ARG_TAGS)
		printf("Tagging %s: %s\n", n.Name, n.Tags)
		return n.ApplyTags()
	}, 10)
	return nil
}
