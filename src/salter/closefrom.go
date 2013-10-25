// +build darwin linux

package main

import (
	"io/ioutil"
	"strconv"
	"syscall"
)

// Closes all the file descriptors from a given number up.
func closeFrom(i int) {
	items, err := ioutil.ReadDir("/dev/fd")
	if err != nil {
		panic(err)
	}

	for _, item := range items {
		if fd, err := strconv.ParseInt(item.Name(), 10, 63); err != nil {
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
