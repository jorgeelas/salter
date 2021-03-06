// -------------------------------------------------------------------
//
// salter: Tool for bootstrap salt clusters in EC2
//
// Copyright (c) 2013-2014 Orchestrate, Inc. All Rights Reserved.
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

// +build darwin linux

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"syscall"
)

// Closes all the file descriptors from a given number up.
func closeFrom(i int) {
	fd, err := os.Open("/dev/fd")
	if err != nil {
		panic(err)
	}
	defer fd.Close()

	// Walk each item in the directory.
	for {
		names, err := fd.Readdirnames(1)
		if err == io.EOF {
			return
		} else if err != nil {
			panic(err)
		} else if len(names) != 1 {
			panic(fmt.Errorf("This shouldn't happen."))
		}

		if fd, err := strconv.ParseInt(names[0], 10, 63); err != nil {
			// This shouldn't ever happen, its means that a fd is not an int
			// so we just ignore it. =/
			continue
		} else if fd >= int64(i) {
			// Note that we can not close here due to ordering issues with
			// KQUEUE file descriptors.
			syscall.CloseOnExec(int(fd))
		}
	}
}
