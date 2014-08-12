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

func teardown() error {
	// Setup a channel for queuing requests for teardown and another
	// for shutdown notification
	teardownQueue := make(chan *Node)
	shutdownQueue := make(chan bool)

	// Spin up goroutines to start tearing down nodes; we limit
	// total number of goroutines to be nice to AWS
	for i := 0; i < ARG_PARALLEL; i++ {
		go func() {
			for node := range teardownQueue {
				teardownNode(node)
			}
			shutdownQueue <- true
		}()
	}

	// Walk all the target nodes, queuing up teardown requests
	for _, node := range G_TARGETS {
		teardownQueue <- node
	}

	// All done queuing
	close(teardownQueue)

	// Wait for each of the goroutines to shutdown
	for i := 0; i < ARG_PARALLEL; i++ {
		<-shutdownQueue
	}

	return nil
}

func teardownNode(node *Node) {
	err := node.Update()
	if err != nil {
		printf("%s: not terminated; %+v\n", node.Name, err)
		return
	}

	err = node.Terminate()
	if err != nil {
		printf("%s: not terminated; %+v\n", node.Name, err)
		return
	}

	// TODO: Revoke key from master
}
