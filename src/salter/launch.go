package main

import "fmt"
import "time"
import "code.google.com/p/go.crypto/ssh"

func launch() bool {
	// Make sure a master running
	masterNode := ensureMaster()
	if masterNode == nil {
		return false
	}

	// Remove the master node from targets; we're assured it's already
	// running
	delete(G_CONFIG.Targets, masterNode.Name)

	// Spin up SSH to the master node -- we'll need this to accept the salt
	// key from minions
	err := masterNode.SshOpen()
	if err != nil {
		fmt.Printf("Unable to open SSH connection to master: %+v\n",
			err)
		return false
	}

	// Setup a channel for queuing up nodes to launch and another
	// for shutdown notification
	launchQueue := make(chan Node)
	shutdownQueue := make(chan bool)

	// Spin up goroutines to start launching nodes; we limit
	// total number of goroutines to be nice to AWS
	for i := 0; i < G_CONFIG.MaxConcurrent; i++ {
		go func() {
			for node := range launchQueue {
				launchNode(&node, masterNode)
			}

			shutdownQueue <- true
		}()
	}

	// Start enqueuing targets to launch
	for _, node := range G_CONFIG.Targets {
		launchQueue <- node
	}

	// All done launching
	close(launchQueue)

	// Wait for each of the goroutines to shutdown
	for i := 0; i < G_CONFIG.MaxConcurrent; i++ {
		<- shutdownQueue
	}

	return true
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
		fmt.Printf("None of the nodes are associated with a saltmaster role!\n")
		return nil
	}

	// Grab latest state of master node
	err := masterNode.Update()
	if err != nil {
		fmt.Printf("Unable to update state of %s node from AWS: %+v\n",
			masterNode.Name, err)
		return nil
	}

	// Track whether or not we are starting the master node for the first
	// time
	started := false

	if !masterNode.IsRunning() {
		// Not yet running, start it (designating master as localhost)
		err := masterNode.Start("127.0.0.1")
		if err != nil {
			fmt.Printf("Unable to start node %s: %+v\n", masterNode.Name, err)
			return nil
		}
		started = true
	}

	// Wait for master node be up and ready
	err = waitForRunning(masterNode)
	if err != nil {
		return nil
	}

	// Make sure the minion key on the master has been accepted
	if started {
		distributeKeys(masterNode, masterNode)
	}

	return masterNode
}


func launchNode(node *Node, masterNode *Node) {
	err := node.Update()
	if err != nil {
		fmt.Printf("Unable to update status of %s node from AWS: %+v\n",
			node.Name, err)
		return
	}

	err = node.Start(masterNode.Instance.PrivateIpAddress)
	if err != nil {
		fmt.Printf("Failed to start %s: %+v\n", node.Name, err)
		return
	}

	// Wait for the node to move to running state, ssh to come up and
	// cloud-init to finish
	err = waitForRunning(node)
	if err != nil {
		fmt.Printf("Failed to launch %s: %+v\n", node.Name, err)
		return
	}

	// Finally, distribute the keys
	distributeKeys(node, masterNode)
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
	for {
		err := node.SshOpen()
		if err == nil {
			defer node.SshClose()
			return waitForCloudInit(node)
		}

		// Wait for 7 seconds
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
	master.SshUpload("/etc/salt/pki/master/minions_pre/" + node.Name, pubKey)
	master.SshRun("/usr/bin/sudo /usr/bin/salt-key -y -a " + node.Name)

	// Write the pub & private keys to node
	node.SshUpload("/etc/salt/pki/minion/minion.pub", pubKey)
	node.SshUpload("/etc/salt/pki/minion/minion.pem", privKey)

	// Finally, restart minion
	node.SshRun("/usr/bin/sudo start salt-minion")
}

