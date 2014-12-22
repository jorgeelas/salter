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

package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"
)

func FileExists(filename string) bool {
	_, err := os.Stat(filename)
	if err != nil {
		return false
	}
	if os.IsNotExist(err) {
		return false
	}
	return true
}

func HasKey(key string, m *map[string]interface{}) bool {
	_, hasKey := (*m)[key]
	return hasKey
}

func pForEachValue(m interface{}, f interface{}, concurrent int) {
	mVal := reflect.ValueOf(m)
	fVal := reflect.ValueOf(f)
	fType := fVal.Type()

	// If key is not already a pointer AND the function takes a pointer to
	// the value type we need to set a flag
	keyIsPointer := (mVal.Type().Key().Kind() == reflect.Ptr)
	passByRef := !keyIsPointer && (fType.In(0).Kind() == reflect.Ptr)

	runQueue := make(chan reflect.Value)
	doneQueue := make(chan bool)

	for i := 0; i < concurrent; i++ {
		go func() {
			for kVal := range runQueue {
				vVal := mVal.MapIndex(kVal)
				if passByRef {
					// Construct a pointer to the value
					ptr := reflect.New(vVal.Type())
					ptr.Elem().Set(vVal)
					fVal.Call([]reflect.Value{ptr})
					mVal.SetMapIndex(kVal, ptr.Elem()) // Make sure map has latest value
				} else {
					fVal.Call([]reflect.Value{vVal})
				}
			}

			doneQueue <- true
		}()
	}

	count := 0
	for _, kVal := range mVal.MapKeys() {
		runQueue <- kVal
		count++
	}

	close(runQueue)

	for i := 0; i < concurrent; i++ {
		<-doneQueue
	}

	close(doneQueue)
}

// Fields inherited from an old to a new node.
var inheritedFields map[string]bool = map[string]bool{
	"Ami":      true,
	"Flavor":   true,
	"KeyName":  true,
	"RegionId": true,
	"SGroup":   true,
	"Username": true,
	"Zone":     true,
}

func inheritFieldsIfEmpty(to interface{}, from interface{}) {
	toVal := reflect.ValueOf(to).Elem()
	fromVal := reflect.ValueOf(from).Elem()

	// Ensure that the types of the two objects are the same, otherwise
	// nothing in this function will work.
	if toVal.Type() != fromVal.Type() {
		panic(fmt.Errorf("%T and %T are not the same.", to, from))
	}

	// Walk through each field, if its name matches one of the ones in
	// the inheritedFields map then we check to see if the value is default
	// and if so we copy from the 'from' value into the 'to' value.
	for i := 0; i < toVal.NumField(); i++ {
		name := toVal.Type().Field(i).Name
		if _, ok := inheritedFields[name]; !ok {
			// Its not a field we care to copy.
			continue
		}
		toField := toVal.Field(i)

		// Check to see if the to field is empty (zero). If it is not empty
		// then something was set and as such no copy should be done.
		zero := reflect.Zero(toField.Type()).Interface()
		if toVal.Field(i).Interface() != zero {
			continue
		}

		// The field exists and is empty, copy the contents from the 'from'
		// value's field into the 'to' field.
		fromField := fromVal.FieldByName(name)
		toField.Set(fromField)
	}
}

// Contacts AWS and updates the data for all of the nodes in parallel.
// The number here is the number of concurrent operations that we should
// perform.
func updateNodes(nodes map[string]*Node, parallel int) (err error) {
	runQueue := make(chan *Node, len(nodes))
	wg := sync.WaitGroup{}
	wg.Add(parallel)
	for i := 0; i < parallel; i++ {
		go func() {
			defer wg.Done()
			for {
				if node, open := <-runQueue; !open {
					return
				} else if err2 := node.Update(); err2 != nil {
					err = err2
				}

			}
		}()
	}

	// Inject the nodes into the runQueue and then wait for the WaitGroup
	// so we know when the whole thing has finished.
	for _, node := range nodes {
		runQueue <- node
	}
	close(runQueue)
	wg.Wait()
	return err
}
