// -------------------------------------------------------------------
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
	"log"
	"os"
	"path"
)

// Logs a message to the debug log. These messages will NOT be displayed to the
// terminal so they can be verbose if necessary.
func debugf(f string, v ...interface{}) {
	log.Printf(f, v...)
}

// This returns the given error message on stderr. The message will NOT be
// included in the log file. This is used to inform the user of an error in
// the command line arguments or with the config file.
func errorf(f string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, f, v...)
}

// Logs a message to the screen as well as the debug and then exits with a non
// zero exit code.
func fatalf(f string, v ...interface{}) {
	log.Printf(f, v...)
	fmt.Printf(f, v...)
	os.Exit(1)
}

// Logs a message to the screen as well as the debug log.
func printf(f string, v ...interface{}) {
	log.Printf(f, v...)
	fmt.Printf(f, v...)
}

// Initializes the log file.
func setupLogging() {
	// Open the log file.
	logFilename := path.Join(G_DIR, "log")
	logFile, err := os.OpenFile(
		logFilename, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not open %s: %s\n", logFilename, err)
		os.Exit(1)
	}

	// Direct all logging output to the log file
	log.SetOutput(logFile)
}
