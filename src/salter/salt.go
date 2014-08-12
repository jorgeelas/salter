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
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// This is the JSON structure returned for each host after a highstate.
type HighstateHost map[string]HighstateEntry

// Walks through all the results in a HighstateHost result and summarize them
// into simple counts.
func (h HighstateHost) Summarize(host string) (states, changes, errors int) {
	states = len(h)
	for id, entry := range h {
		if !entry.Result {
			errors += 1
			debugf("%s: Highstate error in '%s': %s\n", host, id, entry.Comment)
		} else if len(entry.Changes) > 0 {
			changes += len(entry.Changes)
			debugf("%s: Highstate change '%s': %s\n", host, id, entry.Comment)
		}
	}
	return
}

// This is a specific item from a host's highstate report. This structure
// is defined by the salt API. Each state is represented in a HighstateEntry.
type HighstateEntry struct {
	// A string comment about the state.
	Comment string `json:"comment"`

	// A map of all the changes made by this state.
	Changes map[string]interface{} `json:"changes"`

	// True if the state executed successfully.
	Result bool `jason:"result"`
}

// Attempts to SSH into 'master' in order to highstate the given targets.
func saltHighstate(master *Node, targets string) error {
	if targets == "" {
		// If the -s argument was not specified then we need to report the
		// error then terminate.
		fatalf("No salt targets (-s) specified for highstate operation.\n")
		os.Exit(1)
	}

	// Execute the SSH command.
	cmd := fmt.Sprintf(
		"sudo salt '%s' -t %d --output=json --static state.highstate",
		targets, G_CONFIG.Salt.Timeout)
	out, err := master.SshRunOutput(cmd)
	if err != nil {
		debugf("Error during highstate process: %s\nMaster: %s\nCommand: %s\n",
			err, master.Name, cmd)
		return err
	}

	// Attempt to unmarshal the returned JSON data.
	var hosts map[string]json.RawMessage
	if err = json.Unmarshal(out, &hosts); err != nil {
		debugf("Error parsing JSON returned from salt: %s\nCommand: %s\n"+
			"Raw data: %#v\n", err, cmd, out)
		return err
	}

	// This is the reporting output, indexed by host name.
	report := make(map[string]string, 0)

	for host, raw := range hosts {
		// First step is to try and parse the individual node response into
		// a HighstateEntry item. If this succeeds then the result is a
		// successful highstate, otherwise the response is likely a string
		// error message.
		var items HighstateHost
		if err := json.Unmarshal(raw, &items); err != nil {
			if msg, err := json.MarshalIndent(raw, "", "\t"); err != nil {
				// Inform the user.
				debugf("Error highstating %s: %s\n", host, raw)

				// Add a line to the report.
				report[host] = "Error while highstating."
			} else {
				// Inform the user.
				debugf("Bad JSON highstate reply while highstating %s:\n%s\n",
					host, msg)

				// Add a line to the report.
				report[host] = "Error while highstating."
			}
			continue
		}

		// If we get here then the message was a valid HighStateHost
		// structure and therefor we can use it to count changes/errors/etc.
		states, changes, errors := items.Summarize(host)
		debugf("%s Highstate results: %d errors, %d changes, %d states.\n",
			host, errors, changes, states)

		// Add the results to the map we will display to the user.
		report[host] = fmt.Sprintf("%d errors, %d changes, %d states.",
			errors, changes, states)
	}

	// Walk through the keys (hostnames) calculating the longest name in the
	// group and adding them to a list that we can use for sorting.
	longestName := 0
	keys := make([]string, 0, len(report))
	for host, _ := range hosts {
		runes := utf8.RuneCountInString(host)
		if runes > longestName {
			longestName = runes
		}
		keys = append(keys, host)
	}
	sort.Strings(keys)
	longestName += 2

	// Walk through the results and display them in sorted order.
	for _, host := range keys {
		hostLen := utf8.RuneCountInString(host)
		pad := strings.Repeat(" ", longestName-hostLen)
		printf("%s:%s%s\n", host, pad, report[host])
	}

	// Success
	return nil
}
