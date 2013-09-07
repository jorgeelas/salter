package main

import "fmt"
import "time"
import "github.com/dizzyd/goamz/aws"
import "github.com/dizzyd/goamz/ec2"

func launch(config Config) bool {
	// Setup a channel for queuing up nodes to launch and another
	// for shutdown notification
	launchQueue := make(chan NodeConfig)
	shutdownQueue := make(chan bool)

	// Spin up goroutines to start launching nodes; we limit
	// total number of goroutines to be nice to AWS
	for i := 0; i < config.MaxConcurrent; i++ {
		go func() {
			for node := range launchQueue {
				region, _ := GetRegion(node.Region, config)
				launchNode(node, config, region)
			}

			shutdownQueue <- true
		}()
	}

	// Start enqueuing targets to launch
	for _, node := range config.Targets {
		launchQueue <- node
	}

	// All done launching
	close(launchQueue)

	// Wait for each of the goroutines to shutdown
	for i := 0; i < config.MaxConcurrent; i++ {
		<- shutdownQueue
	}

	return true
}


func launchNode(node NodeConfig, config Config, region Region) {
	// If the instance is already running, skip it
	if region.IsInstanceRunning(node.Name) {
		fmt.Printf("Not launching %s; already running.\n", node.Name)
		return
	}

	// If the key for this node is unavailable, skip it
	if !region.KeyExists(node.KeyName) {
		fmt.Printf("Not launching %s: key %s is not available locally!\n",
			node.Name, node.KeyName)
		return
	}

	fmt.Printf("Launching %s = %+v\n", node.Name, node)

	runInst := ec2.RunInstances {
		ImageId: node.Ami,
		KeyName: node.KeyName,
		InstanceType: node.Flavor }
	conn := ec2.New(config.AwsAuth, aws.Regions[node.Region])
	runResp, err := conn.RunInstances(&runInst)
	if err != nil {
		fmt.Printf("%s: %+v\n", node.Name, err)
		return
	}

	// Extract instanceId
	instanceId := runResp.Instances[0].InstanceId

	// Instance is now running; apply any tags
	_, err = conn.CreateTags([]string { instanceId }, node.ec2Tags())
	if err != nil {
		fmt.Printf("Failed to apply tags to %s: %+v\n", node.Name, err)
		return
	}

	// Poll the instance ID, waiting for it to either start running or fail
	for {
		// Wait for 7 seconds
		time.Sleep(7 * time.Second)

		// Check the status
		statusResp, err := conn.Instances([]string { instanceId }, nil)
		if err != nil {
			fmt.Printf("Instances list failed: %+v\n", err)
			return
		}

		state := statusResp.Reservations[0].Instances[0].State.Name
		fmt.Printf("%s: %s\n", node.Name, state)
		if state == "running" {
			break
		} else if state == "pending" {
			continue
		} else {
			// Other state; indicative of a failed launch
			fmt.Printf("Failed to launch instance: %s = %d\n", node.Name, state)
			return
		}
	}


}
