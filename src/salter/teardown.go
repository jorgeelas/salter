package main

import "fmt"

func teardown() {
	// Setup a channel for queuing requests for teardown and another
	// for shutdown notification
	teardownQueue := make(chan Node)
	shutdownQueue := make(chan bool)

	// Spin up goroutines to start tearing down nodes; we limit
	// total number of goroutines to be nice to AWS
	for i := 0; i < G_CONFIG.MaxConcurrent; i++ {
		go func() {
			for node := range teardownQueue {
				teardownNode(&node)
			}
			shutdownQueue <- true
		}()
	}

	// Walk all the target nodes, queuing up teardown requests
	for _, node := range G_CONFIG.Targets {
		teardownQueue <- node
	}

	// All done queuing
	close(teardownQueue)

	// Wait for each of the goroutines to shutdown
	for i := 0; i < G_CONFIG.MaxConcurrent; i++ {
		<- shutdownQueue
	}
}

func teardownNode(node *Node) {
	err := node.Update()
	if err != nil {
		fmt.Printf("Failed to teardown %s: %+v\n", node.Name, err)
		return
	}

	err = node.Terminate()
	if err != nil {
		fmt.Printf("Failed to teardown %s: %+v\n", node.Name, err)
		return
	}

	// TODO: Revoke key from master
}

