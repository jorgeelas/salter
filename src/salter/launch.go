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
	"fmt"
	"time"

	"code.google.com/p/go.crypto/ssh"
)

func launch() error {
	// Make sure a master running
	masterNode := ensureMaster()
	if masterNode == nil {
		return fmt.Errorf("missing master node")
	}

	// Remove the master node from targets; we're assured it's already
	// running
	delete(G_TARGETS, masterNode.Name)

	// Spin up SSH to the master node -- we'll need this to accept the salt
	// key from minions
	err := masterNode.SshOpen()
	if err != nil {
		errorf("Unable to open SSH connection to master: %+v\n", err)
		return err
	}

	// Setup a channel for queuing up nodes to launch and another
	// for shutdown notification
	launchQueue := make(chan *Node)
	shutdownQueue := make(chan bool)

	// Spin up goroutines to start launching nodes; we limit
	// total number of goroutines to be nice to AWS
	for i := 0; i < ARG_PARALLEL; i++ {
		go func() {
			for node := range launchQueue {
				launchNode(node, masterNode)
			}

			shutdownQueue <- true
		}()
	}

	// Start enqueuing targets to launch
	for _, node := range G_TARGETS {
		launchQueue <- node
	}

	// All done launching
	close(launchQueue)

	// Wait for each of the goroutines to shutdown
	for i := 0; i < ARG_PARALLEL; i++ {
		<-shutdownQueue
	}

	return nil
}

func ensureMaster() *Node {
	// Special case for handling saltmaster. The master needs to be up and
	// running so that we have an IP to put into the minion's /etc/hosts
	// file. Thus, we need to verify that a node is already up with the
	// appropriate role, or we are planning to start that node before
	// continuing.
	masterNode := G_CONFIG.findNodeByRole("saltmaster")
	if masterNode == nil {
		// No saltmaster role defined; we can't setup a cluster without it!
		errorf("None of the nodes are associated with a saltmaster role!\n")
		return nil
	}

	// Grab latest state of master node
	err := masterNode.Update()
	if err != nil {
		errorf("Unable to update state of %s node from AWS: %+v\n",
			masterNode.Name, err)
		return nil
	}

	if !masterNode.IsRunning() {
		// Not yet running, start it (designating master as localhost)
		err := masterNode.Start("127.0.0.1")
		if err != nil {
			errorf("Unable to start node %s: %+v\n", masterNode.Name, err)
			return nil
		}
	}

	// Wait for master node be up and ready
	err = waitForRunning(masterNode)
	if err != nil {
		return nil
	}

	// Make sure the minion key on the master has been accepted
	distributeKeys(masterNode, masterNode)

	displayNodeInfo(masterNode)

	return masterNode
}

func launchNode(node *Node, masterNode *Node) {
	err := node.Update()
	if err != nil {
		errorf("Unable to update status of %s node from AWS: %+v\n",
			node.Name, err)
		return
	}

	err = node.Start(masterNode.Instance.PrivateIpAddress)
	if err != nil {
		errorf("Failed to start %s: %+v\n", node.Name, err)
		return
	}

	// Wait for the node to move to running state, ssh to come up and
	// cloud-init to finish
	err = waitForRunning(node)
	if err != nil {
		errorf("Failed to launch %s: %+v\n", node.Name, err)
		return
	}

	// Finally, distribute the keys
	distributeKeys(node, masterNode)

	displayNodeInfo(node)
}

func displayNodeInfo(node *Node) {
	// Get the uptime from the node for display purposes
	uptime, err := node.SshRunOutput("uptime")
	if err != nil {
		debugf("Failed to get uptime for %s: %+v\n", node.Name, err)
	}

	// Display launch info
	printf("%s (%s): running %s\n", node.Name, node.Instance.PublicIpAddress,
		uptime)
}

func waitForRunning(node *Node) error {
	for {
		err := node.Update()
		if err != nil {
			return fmt.Errorf("AWS status update failed - %+v", err)
		}

		switch node.Instance.State.Name {
		case "running":
			return waitForSsh(node)
		case "pending":
			continue
		default:
			// Not running or pending; indicates a failed launch
			return fmt.Errorf("unexpected instance state - %s",
				node.Instance.State.Name)
		}

		time.Sleep(5 * time.Second)
	}
}

func waitForSsh(node *Node) error {
	counter := 10
	for {
		err := node.SshOpen()
		if err == nil {
			defer node.SshClose()
			return waitForCloudInit(node)
		}

		counter--

		if counter < 0 {
			return fmt.Errorf("wait for SSH timed out: %s",
				err)
		}

		// Wait for 5 seconds
		time.Sleep(5 * time.Second)
	}
}

func waitForCloudInit(node *Node) error {
	cmd := "/bin/bash -c 'test -f /var/lib/cloud/instance/boot-finished'"
	for {
		err := node.SshRun(cmd)
		if err == nil {
			return nil
		}

		switch err.(type) {
		case *ssh.ExitError:
			time.Sleep(5 * time.Second)
		default:
			return err
		}
	}
}

func distributeKeys(node *Node, master *Node) {
	// Generate a pub/priv keypair
	pubKey, privKey, _ := node.GenSaltKey(2048)

	// Write the pub key to the master and accept it
	master.SshUpload("/etc/salt/pki/master/minions_pre/"+node.Name, pubKey)
	master.SshRun("/usr/bin/sudo /usr/bin/salt-key -y -a " + node.Name)

	// Write the pub & private keys to node
	node.SshUpload("/etc/salt/pki/minion/minion.pub", pubKey)
	node.SshUpload("/etc/salt/pki/minion/minion.pem", privKey)

	// Finally, restart minion
	node.SshRun("/usr/bin/sudo start salt-minion")
}
