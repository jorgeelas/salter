package main

import "os"

func FileExists(filename string) bool {
	_, err := os.Stat(filename)
	if err != nil { return false }
	if os.IsNotExist(err) { return false }
	return true
}

func HasKey(key string, m *map[string]interface{}) bool {
	_, hasKey := (*m)[key]
	return hasKey
}
