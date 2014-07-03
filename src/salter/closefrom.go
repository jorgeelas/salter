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
