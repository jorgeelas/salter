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
import "log"
import "encoding/json"

type HighstateHost map[string]HighstateEntry

type HighstateEntry struct {
	Comment string
	Changes map[string]interface{}
	Result  bool
}


func saltHighstate(master *Node, targets string) error {
	if targets == "" {
		fmt.Printf("No salt targets (-s) specified for highstate operation.\n")
		return nil
	}


	cmd := fmt.Sprintf("sudo salt %s -t %d --output=json --static state.highstate",
		targets,
		G_CONFIG.Salt.Timeout)
	out, err := master.SshRunOutput(cmd)
	if err != nil {
		log.Printf("%s: %s failed - %+v\n", master.Name, cmd, err)
		return err
	}

	var raw interface{}
	err = json.Unmarshal(out, &raw)

	if err != nil {
		fmt.Printf("JSON parse error: %s\nRaw output: %s\n", err, out)
		return err
	}

	hosts := raw.(map[string]interface{})
	for host, info := range hosts {
		// Attempt to convert the raw info into highstate host; if it fails, we assume that
		// salt is giving some error message and report accordingly
		entries := toHighstateHost(info)
		if entries == nil {
			formatted, _ := json.MarshalIndent(info, "", "\t")
			fmt.Printf("%s: %s\n", host, formatted)
			continue
		}

		// Check each of the entries and look for any errors
		changes := 0
		errors := 0
		for id, entry := range *entries {
			entryChanges := len(entry.Changes)
			changes += entryChanges
			if !entry.Result { errors += 1}
			if entryChanges > 0 || !entry.Result {
				log.Printf("%s.%s: %s\n", host, id, entry.Comment)
			}
		}
		fmt.Printf("%s: summary: %d errors, %d changes, %d states.\n", host, errors, changes, len(*entries))
		log.Printf("%s: summary: %d errors, %d changes, %d states.\n", host, errors, changes, len(*entries))
	}
	return nil
}

// Attempt to take a chunk of generic JSON and convert into a HighstateHost
//
// It's unfortunate that the JSON library doesn't deal with unexpected data more
// gracefully. This unmarshal-to-generic, remarshal-attempt-unmarshal dance is
// lame.
func toHighstateHost(data interface{}) *HighstateHost {
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}

	var result HighstateHost
	err = json.Unmarshal(bytes, &result)
	if err != nil {
		return nil
	}
	return &result
}
