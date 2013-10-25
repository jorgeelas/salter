// +build (NOT darwin) AND (NOT linux)

package main

// Does nothing for platforms where there isn't a closefrom option.
func closeFrom(i int) {
}
