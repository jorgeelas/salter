package main

import "fmt"
import "github.com/dizzyd/goamz/ec2"

type TeardownInfo struct {
	conn ec2.EC2
	name string
	id   string
}

func teardown(config Config) {
	// Setup a channel for queuing requests for teardown and another
	// for shutdown notification
	teardownQueue := make(chan TeardownInfo)
	shutdownQueue := make(chan bool)

	// Spin up goroutines to start tearing down nodes; we limit
	// total number of goroutines to be nice to AWS
	for i := 0; i < config.MaxConcurrent; i++ {
		go func() {
			for info := range teardownQueue {
				teardownNode(info)
			}
			shutdownQueue <- true
		}()
	}

	// Walk all the target nodes, queuing up teardown requests
	for _, node := range config.Targets {
		region, _ := GetRegion(node.Region, config)
		if region.IsInstanceRunning(node.Name) {
			instance := region.instanceNames[node.Name]
			teardownQueue <- TeardownInfo{ conn: region.Conn, name: node.Name, id: instance.InstanceId }
		} else {
			fmt.Printf("Skipping %s; not running.\n", node.Name)
		}
	}

	// All done queuing
	close(teardownQueue)

	// Wait for each of the goroutines to shutdown
	for i := 0; i < config.MaxConcurrent; i++ {
		<- shutdownQueue
	}
}

func teardownNode(info TeardownInfo) {
	_, err := info.conn.TerminateInstances([]string { info.id })
	if err != nil {
		fmt.Printf("Failed to teardown %s (%s): %+v\n", info.name, info.id, err)
	}

	fmt.Printf("Terminated %s (%s)\n", info.name, info.id)
}

