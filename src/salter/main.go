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

import "fmt"
import "flag"
import "strings"
import "os"
import "sort"
import "syscall"
import "path"
import "log"
import "os/exec"
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
	Fn func() error
	Usage string
}

var G_CONFIG   Config
var G_REGIONS  map[string]*Region
var G_DIR      string
var G_LOG      *os.File
var G_COMMANDS map[string]Command

var ARG_TARGETS Targets
var ARG_CONFIG_FILE string
var ARG_ALL bool
var ARG_SALT_TARGETS string
var ARG_TAGS TagMap

func usage() error {
	fmt.Println("usage: salter <options> <command>")
	fmt.Printf(" options:\n")
	flag.PrintDefaults()
	fmt.Printf(" commands:\n")

	cmds := fun.Keys(G_COMMANDS).([]string)
	sort.Strings(cmds)

	for _, cmd := range cmds {
		fmt.Printf("  * %-12s %s\n", cmd, G_COMMANDS[cmd].Usage)
	}

	return nil
}

func init() {
	ARG_TAGS = make(map[string]string)

	G_DIR = path.Join(os.ExpandEnv("$HOME"), ".salter")
	G_REGIONS = make(map[string]*Region)
	G_COMMANDS = make(map[string]Command)
	G_COMMANDS["launch"] = Command{ Fn: launch, Usage: "launch instances on EC2"}
	G_COMMANDS["teardown"] = Command{ Fn: teardown, Usage: "terminates instances on EC2"}
	G_COMMANDS["ssh"] = Command{ Fn: sshto, Usage: "open a SSH session to a EC2 instance"}
	G_COMMANDS["csshx"] = Command { Fn: csshx, Usage: "open a series of SSH sessions to EC2 instances via csshX" }
	G_COMMANDS["hosts"] = Command{ Fn: hosts, Usage: "generate a list of live nodes on EC2"}
	G_COMMANDS["upload"] = Command{ Fn: upload, Usage: "upload Salt configuration to the Salt master"}
	G_COMMANDS["highstate"] = Command{ Fn: highstate, Usage: "invoke Salt highstate on the Salt master"}
	G_COMMANDS["sgroups"] = Command{ Fn: sgroups, Usage: "generate security groups from configuration"}
	G_COMMANDS["help"] = Command{ Fn: usage, Usage: "display help"}
	G_COMMANDS["dump"] = Command{ Fn: dump, Usage: "dump generated node definitions"}
	G_COMMANDS["bootstrap"] = Command{ Fn: bootstrap,
		Usage: "Upload Salt configuration and highstate master."}
	G_COMMANDS["info"] = Command{ Fn: info, Usage:"display internal/external IP addresses for nodes"}
	G_COMMANDS["tag"] = Command{ Fn: tag, Usage:"(re)apply tags to each AWS node"}
}

func main() {
	// Initialize data directory if it doesn't already exist
	os.Mkdir(G_DIR, 0700)

	// Setup logging subsystem
	logFilename := path.Join(G_DIR, "log")
	G_LOG, err := os.OpenFile(logFilename, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		fmt.Printf("Could not open %s: %s\n", logFilename, err)
		os.Exit(1)
	}
	defer G_LOG.Close()

	// Direct all logging output to the log file
	log.SetOutput(G_LOG)

	// Log run info
	log.Printf("--- %s ---\n", os.Args)
	cwd, _ := os.Getwd()
	log.Printf("Cwd: %s\n", cwd)

	// Setup command line flags
	flag.StringVar(&ARG_CONFIG_FILE, "c", "salter.cfg", "Configuration file")
	flag.BoolVar(&ARG_ALL, "a", false, "Apply operations to all nodes")
	flag.Var(&ARG_TARGETS, "n", "Target nodes for the operation (overrides -a flag)")
	flag.StringVar(&ARG_SALT_TARGETS, "s", "'*'", "Targets for salt-related operations")
	flag.Var(&ARG_TAGS, "t", "Tags to apply")

	// Parse it up
	flag.Parse()

	// If parse failed, bail
	if !flag.Parsed() || flag.NArg() != 1 {
		usage()
		os.Exit(-1)
	}

	// Special cases for -a / -n
	if flag.Arg(0) == "hosts" {
		ARG_ALL = true
	}

	// Find the command the user is invoking
	cmd, found := G_COMMANDS[flag.Arg(0)]
	if !found {
		fmt.Printf("ERROR: unknown command '%s'\n", flag.Arg(0))
		usage()
		os.Exit(-1)
	}

	// Initialize the config file
	config, err := NewConfig(ARG_CONFIG_FILE, ARG_TARGETS, ARG_ALL)
	if err != nil {
		fmt.Printf("Failed to load config from %s: %+v\n", ARG_CONFIG_FILE, err)
		os.Exit(-1)
	}

	// Make config globally available
	G_CONFIG = config

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
	target := fun.Keys(G_CONFIG.Targets).([]string)
	if len(target) != 1 {
		fmt.Printf("Only one node may be used with the ssh command.\n")
		return fmt.Errorf("More than one target")
	}

	node := G_CONFIG.Targets[target[0]]
	err := node.Update()
	if err != nil {
		fmt.Printf("Unable to retrieve status of %s from AWS: %+v\n",
			node.Name, err)
		return err
	}

	if !node.IsRunning() {
		fmt.Printf("Node %s is not running.\n", node.Name)
		return fmt.Errorf("%s is not running", node.Name)
	}

	key := RegionKey(node.KeyName, node.RegionId)

	args := []string {
		"ssh",
		"-i", key.Filename,
		"-o", "LogLevel=FATAL",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ForwardAgent=yes",
		"-l", G_CONFIG.Aws.Username,
		node.Instance.DNSName,
    }

	env := []string {
		"TERM=" + os.Getenv("TERM"),
	}


	fmt.Printf("Connecting to %s (%s)...\n", node.Name, node.Instance.InstanceId)
	closeFrom(3)
	syscall.Exec("/usr/bin/ssh", args, env)
	return nil
}

func csshx() error {
	// Make sure we can find an instance of csshX on the path
	csshPath, err := exec.LookPath("csshX")
	if err != nil {
		fmt.Printf("Unable to find csshX on your path.\n")
		return err
	}

	// Get a list of all the keys and sort them
	names := fun.Keys(G_CONFIG.Targets).([]string)
	sort.Strings(names)
	if len(names) < 1 {
		fmt.Printf("You must specify one or more targets!\n")
		return fmt.Errorf("At least one target must be specified")
	}

	// Update all the targets with latest instance info
	pForEachValue(G_CONFIG.Targets, (*Node).Update, 10)

	// If any of the nodes are not running, bail with error
	allRunning := true
	for _, node := range G_CONFIG.Targets {
		if !node.IsRunning() {
			allRunning = false
			fmt.Printf("%s is not running\n", node.Name)
		}
	}

	if !allRunning {
		fmt.Printf("Some target nodes are not running on AWS; aborting.\n")
		return fmt.Errorf("Some target nodes are not running")
	}

	key := RegionKey(G_CONFIG.Targets[names[0]].KeyName, G_CONFIG.Targets[names[0]].RegionId)

	sshArgs := fmt.Sprintf("-i %s -o LogLevel=FATAL -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ForwardAgent=yes",
		key.Filename)

	args := []string {
		csshPath, "--ssh_args", sshArgs,
		"-l", G_CONFIG.Aws.Username,
	}

	fmt.Printf("Connecting to:\n")

	for _, name := range names {
		fmt.Printf(" * %s\n", name)
		args = append(args, G_CONFIG.Targets[name].Instance.PublicIpAddress)
	}

	env := []string {
		"HOME=" + os.Getenv("HOME"),
		"TERM=" + os.Getenv("TERM"),
	}

	syscall.Exec("/usr/local/bin/csshX", args, env)
	return nil
}



func hosts() error {
	// Update all the targets with latest instance info
	pForEachValue(G_CONFIG.Targets, (*Node).Update, 10)

	// Get a list of all the keys and sort them
	names := fun.Keys(G_CONFIG.Targets).([]string)
	sort.Strings(names)

	// Print each entry
	for _, name := range names {
		node := G_CONFIG.Targets[name]
		if node.Instance != nil {
			fmt.Printf("%s\t%s\n", node.Instance.PublicIpAddress, node.Name)
		}
	}

	return nil
}

func info() error {
	// Update all the targets with latest instance info
	pForEachValue(G_CONFIG.Targets, (*Node).Update, 10)

	// Get a list of all the keys and sort them
	names := fun.Keys(G_CONFIG.Targets).([]string)
	sort.Strings(names)

	// Print each entry
	for _, name := range names {
		node := G_CONFIG.Targets[name]
		if node.Instance != nil {
			fmt.Printf("%s\t%s\t%s\n", node.Name,
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
		fmt.Printf("Could not find a node with saltmaster role!\n")
		return fmt.Errorf("no saltmaster role")
	}

	// Get latest info from AWS
	err := node.Update()
	if err != nil {
		fmt.Printf("Failed to update info for %s: %+v\n", node.Name, err)
		return err
	}

	// If the node isn't running, bail
	if !node.IsRunning() {
		fmt.Printf("%s is not running.\n", node.Name)
		return fmt.Errorf("master isn't running")
	}

	// Lookup key for the node/region
	key := RegionKey(node.KeyName, node.RegionId)

	// Generate SSH sub-command
	sshCmd := fmt.Sprintf("ssh -l %s -i %s -o LogLevel=FATAL " +
		"-o StrictHostKeyChecking=no "+
		"-o UserKnownHostsFile=/dev/null", G_CONFIG.Aws.Username, key.Filename)

	// Run rsync
	rsync := exec.Command("rsync", "-rlptDuvz", "--delete",
		"--rsync-path=sudo rsync",
		"-e", sshCmd,
		G_CONFIG.Salt.RootDir + "/",
		fmt.Sprintf("%s@%s:/srv/salt", G_CONFIG.Aws.Username, node.Instance.DNSName))
	rsync.Stdout = os.Stdout
	rsync.Stderr = os.Stdout
	fmt.Printf("Uploading %s to %s:/srv/salt...\n", G_CONFIG.Salt.RootDir, node.Instance.PublicIpAddress)
	err = rsync.Run()
	if err != nil {
		fmt.Printf("Rsync failed: %+v\n", err)
		return err
	}

	// Sync all nodes
	fmt.Printf("Running saltutil.sync_all...\n")
	err = node.SshRun("sudo salt '*' --output=txt saltutil.sync_all")
	if err != nil {
		fmt.Printf("Failed to run saltutil.sync_all: %+v\n", err)
		return err
	}

	// Update mine functions
	fmt.Printf("Running mine.update...\n")
	err = node.SshRun("sudo salt '*' --output=txt mine.update")
	if err != nil {
		fmt.Printf("Failed to run mine.update: %+v\n", err)
		return err
	}

	// Ensure that all pillars are up to date
	fmt.Printf("Running saltutil.refresh_pillar...\n")
	err = node.SshRun("sudo salt '*' --output=txt saltutil.refresh_pillar")
	if err != nil {
		fmt.Printf("Failed to run saltutil.refresh_pillar: %+v\n", err)
		return err
	}

	return nil
}

func highstate() error {
	// Find the master node
	node := G_CONFIG.findNodeByRole("saltmaster")
	if node == nil {
		fmt.Printf("Could not find a node with saltmaster role!\n")
		return fmt.Errorf("no master")
	}

	// Get latest info from AWS
	err := node.Update()
	if err != nil {
		fmt.Printf("Failed to update info for %s: %+v\n", node.Name, err)
		return err
	}

	// If the node isn't running, bail
	if !node.IsRunning() {
		fmt.Printf("%s is not running.\n", node.Name)
		return fmt.Errorf("%s is not running", node.Name)
	}

	// Run the high state
	return saltHighstate(node, ARG_SALT_TARGETS)
}

func dump() error {
	// Get a list of all the keys and sort them
	names := fun.Keys(G_CONFIG.Targets).([]string)
	sort.Strings(names)

	// Print each entry
	for _, name := range names {
		fmt.Printf("%s: %+v\n", name, G_CONFIG.Targets[name])
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
	fmt.Printf("Highstating %s...\n", node.Name)
	ARG_SALT_TARGETS = node.Name
	return highstate()
}

func tag() error {
	pForEachValue(G_CONFIG.Targets, func(n *Node) error {
		err := n.Update()
		if err != nil {
			return err
		}
		n.Tags.Merge(ARG_TAGS)
		fmt.Printf("Tagging %s: %s\n", n.Name, n.Tags)
		return n.ApplyTags()
	}, 10);
	return nil
}


