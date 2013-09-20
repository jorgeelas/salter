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

var G_CONFIG  Config
var G_REGIONS map[string]*Region
var G_DIR     string
var G_LOG     *os.File

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
	var saltTargets string

	// Initialize data directory if it doesn't already exist
	os.Mkdir(G_DIR, 0700)

	// Setup logging subsystem
	logFilename := path.Join(G_DIR, "log")
	G_LOG, err := os.OpenFile(logFilename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("Could not open %s: %s\n", logFilename, err)
		os.Exit(1)
	}
	defer G_LOG.Close()

	// Direct all logging output to the log file
	log.SetOutput(G_LOG)

	// Setup command line flags
	flag.StringVar(&configFile, "c", "salter.cfg", "Configuration file")
	flag.BoolVar(&all, "a", false, "Apply operations to all nodes")
	flag.Var(&targets, "n", "Target nodes for the operation (overrides -a flag)")
	flag.StringVar(&saltTargets, "s", "", "Targets for salt-related operations")

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

	// Walk all the nodes, caching info about their regions
	for _, node := range G_CONFIG.Nodes {
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
	case "upload":
		upload()
	case "highstate":
		highstate(saltTargets)
	}
}


func sshto() {
	target := fun.Keys(G_CONFIG.Targets).([]string)
	if len(target) != 1 {
		fmt.Printf("Only one node may be used with the ssh command.\n")
		return
	}

	node := G_CONFIG.Targets[target[0]]
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

func upload() {
	// Find the master node
	node := G_CONFIG.findNodeByRole("saltmaster")
	if node == nil {
		fmt.Printf("Could not find a node with saltmaster role!\n")
		return
	}

	// Get latest info from AWS
	err := node.Update()
	if err != nil {
		fmt.Printf("Failed to update info for %s: %+v\n", node.Name, err)
		return
	}

	// If the node isn't running, bail
	if !node.IsRunning() {
		fmt.Printf("%s is not running.\n", node.Name)
		return
	}

	// Lookup key for the node/region
	key := RegionKey(node.KeyName, node.RegionId)

	// Generate SSH sub-command
	sshCmd := fmt.Sprintf("ssh -i %s -o LogLevel=FATAL -o StrictHostKeyChecking=no "+
		"-o UserKnownHostsFile=/dev/null", key.Filename)

	// Run rsync
	rsync := exec.Command("rsync", "-auvz", "--delete",
		"--rsync-path=sudo rsync",
		"-e", sshCmd,
		G_CONFIG.Salt.RootDir + "/",
		fmt.Sprintf("%s@%s:/srv/salt", G_CONFIG.Aws.Username, node.Instance.DNSName))
	rsync.Stdout = os.Stdout
	rsync.Stderr = os.Stdout
	fmt.Printf("Uploading %s to %s:/srv/salt...\n", G_CONFIG.Salt.RootDir, node.Instance.IpAddress)
	err = rsync.Run()
	if err != nil {
		fmt.Printf("Rsync failed: %+v\n", err)
		return
	}

	// Sync all nodes
	err = node.SshRun("sudo salt '*' --output=txt saltutil.sync_all")
	if err != nil {
		fmt.Printf("Failed to run saltutil.sync_all: %+v\n", err)
		return
	}

	// Update mine functions
	err = node.SshRun("sudo salt '*' --output=txt mine.update")
	if err != nil {
		fmt.Printf("Failed to run mine.update: %+v\n", err)
		return
	}

}

func highstate(saltTargets string) {
	// Find the master node
	node := G_CONFIG.findNodeByRole("saltmaster")
	if node == nil {
		fmt.Printf("Could not find a node with saltmaster role!\n")
		return
	}

	// Get latest info from AWS
	err := node.Update()
	if err != nil {
		fmt.Printf("Failed to update info for %s: %+v\n", node.Name, err)
		return
	}

	// If the node isn't running, bail
	if !node.IsRunning() {
		fmt.Printf("%s is not running.\n", node.Name)
		return
	}

	// Run the high state
	saltHighstate(node, saltTargets)
}

// For each target node, ensure that the security group exists in the
// appropriate region and that the rules in local definition are present.
func sgroups() {
	// Setup a cache to track security groups we need to work on
	groups := make(map[string]RegionalSGroup)

	// First, create any sgroups that need to exist
	for _, node := range G_CONFIG.Targets {
		sg, err := RegionSGEnsureExists(node.SGroup, node.RegionId)
		if err != nil {
			fmt.Printf("%s: Failed to create security group %s: %+v\n",
				node.Name, node.SGroup, err)
			return
		}
		groups[node.RegionId + "/" + node.SGroup] = *sg
	}

	// Now, for each of the groups, make sure any and all locally-defined
	// rules are present
	for _, sg := range groups {
		// If the sg is not defined in our config, noop
		_, found := G_CONFIG.SGroups[sg.Name]
		if !found {
			// Warn that this group was not found in local config
			fmt.Printf("%s: Security group %s is not defined in local config file.\n", sg.RegionId, sg.Name)
			continue
		}

		// 
	}


}
