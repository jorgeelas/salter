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

import "os"
import "reflect"
import "fmt"

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


func pForEachValue(m interface{}, f interface{}, concurrent int) {
	mVal := reflect.ValueOf(m)
	fVal := reflect.ValueOf(f)
	fType:= fVal.Type()

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
					fVal.Call([]reflect.Value { ptr })
					mVal.SetMapIndex(kVal, ptr.Elem()) // Make sure map has latest value
				} else {
					fVal.Call([]reflect.Value { vVal })
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
		<- doneQueue
	}

	close(doneQueue)
}


func inheritFieldsIfEmpty(to interface{}, from interface{}, fieldNames []string) {
	toVal := reflect.Indirect(reflect.ValueOf(to))
	fromVal := reflect.ValueOf(from)

	for _, field := range fieldNames {
		toField := toVal.FieldByName(field)
		if isEmpty(toField) {
			fromField := fromVal.FieldByName(field)
			toField.Set(fromField)
		}
	}
}

func isEmpty(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}

	switch v.Kind() {
        case reflect.Int:
		return v.Int() == 0
        case reflect.Int8:
		return v.Int() == 0
        case reflect.Int16:
		return v.Int() == 0
        case reflect.Int32:
		return v.Int() == 0
        case reflect.Int64:
		return v.Int() == 0
        case reflect.Uint:
		return v.Uint() == 0
        case reflect.Uint8:
		return v.Uint() == 0
        case reflect.Uint16:
		return v.Uint() == 0
        case reflect.Uint32:
		return v.Uint() == 0
        case reflect.Uint64:
		return v.Uint() == 0
        case reflect.Float32:
		return v.Int() == 0.0
        case reflect.Float64:
		return v.Int() == 0.0
        case reflect.Complex64:
		return v.Int() == 0.0
        case reflect.Complex128:
		return v.Int() == 0.0
        case reflect.Array:
		return v.IsNil()
        case reflect.Chan:
		return v.IsNil()
        case reflect.Func:
		return v.IsNil()
        case reflect.Interface:
		return v.IsNil()
        case reflect.Map:
		return v.IsNil()
        case reflect.Ptr:
		return v.IsNil()
        case reflect.Slice:
		return v.IsNil()
        case reflect.String:
		return v.Len() == 0
	default:
		panic(fmt.Sprintf("IsEmpty() unexpected value kind: %+v\n", v))
	}
}
