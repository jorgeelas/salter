package main

import "os"

func FileExists(filename string) bool {
	_, err := os.Stat(filename)
	if err != nil { return false }
	if os.IsNotExist(err) { return false }
	return true
}
