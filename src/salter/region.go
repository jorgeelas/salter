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

import "strings"
import "sync"
import "github.com/mitchellh/goamz/aws"
import "github.com/mitchellh/goamz/ec2"
import "log"

type Region struct {
	Keys      map[string]Key	  // Key name -> Key (key.go)
	Conn      ec2.EC2

	SGroups   map[string]RegionalSGroup // SG name -> Info

	dataDir   string
}

type RegionalSGroup struct {
	ec2.SecurityGroupInfo
	RegionId string
}

type regionCacheRequest struct {
	Name   string
	Region *Region
	Chan   chan error
}

var G_REGION_CACHE chan *regionCacheRequest

func init() {
	G_REGION_CACHE = make(chan *regionCacheRequest)
}

func StartRegionCache(config *Config) {
	go regionCacheLoop(config.AwsAuth, config.DataDir)
}

func GetRegion(name string) (*Region, error) {
	req := regionCacheRequest{
		Name: name,
		Chan: make(chan error),
	}
	defer close(req.Chan)

	G_REGION_CACHE <- &req

	err := <- req.Chan
	return req.Region, err
}

func regionCacheLoop(awsAuth aws.Auth, dataDir string) {
	regions := make(map[string]*Region)
	for {
		req := <-G_REGION_CACHE
		region, found := regions[req.Name]
		if !found {
			conn := ec2.New(awsAuth, aws.Regions[req.Name])
			region = &Region{
				Conn: *conn,
				dataDir: dataDir,
			}

			err := region.Refresh()
			if err != nil {
				req.Chan <- err
				continue
			}

			regions[req.Name] = region
		}

		req.Region = region
		req.Chan <- nil
	}
}

func RegionKeyExists(name string, regionId string) bool {
	region, _ := GetRegion(regionId)
	_, found := region.Keys[name]
	return found
}

func RegionKey(name string, regionId string) Key {
	region, _ := GetRegion(regionId)
	key, _ := region.Keys[name]
	return key
}

func RegionSGExists(name string, regionId string) bool {
	region, _ := GetRegion(regionId)
	_, found := region.SGroups[name]
	return found
}

func RegionSG(name string, regionId string) RegionalSGroup {
	region, _ := GetRegion(regionId)
	sg := region.SGroups[name]
	return sg
}

func RegionSGEnsureExists(name string, regionId string) (*RegionalSGroup, error) {
	region, _ := GetRegion(regionId)
	var sg RegionalSGroup
	sg, found := region.SGroups[name]
	if !found {
		// Create the SG
		// TODO: Need to add support for VPC here?
		sg.Name = name
		sg.SecurityGroup.Description = name
		sgResp, err := region.Conn.CreateSecurityGroup(sg.SecurityGroup)
		if err != nil {
			return nil, err
		}

		log.Printf("Created security group %s-%s\n", regionId, name)

		sg.RegionId = regionId
		sg.SecurityGroup = sgResp.SecurityGroup
		region.SGroups[name] = sg
		return &sg, nil
	}
	return &sg, nil
}

func (r *Region) Refresh() error {
	rKeys := make(map[string]Key)
	rSgroups := make(map[string]RegionalSGroup)

	var lastErr *error = nil

	wg := new(sync.WaitGroup)

	// Grab keys from this connection
	wg.Add(1)
	go func() {
		defer wg.Done()
		keys, err := r.Conn.KeyPairs(nil, nil)
		if err != nil {
			lastErr = &err
			return
		}

		for _, keyPair := range keys.Keys {
			// Normalize the fingerprint by removing colons
			fingerprint := strings.Replace(keyPair.Fingerprint, ":", "", -1)

			// If the key is already present in r.Keys with the same
			// fingerprint, we can re-use that entry and avoid a bunch of
			// parsing/hashing
			key, found := r.Keys[keyPair.Name]
			if found && key.Fingerprint == fingerprint {
				rKeys[keyPair.Name] = key
				continue
			}

			// Load the local portion of the key, if it's present
			keyPtr, err := LoadKey(keyPair.Name, r.dataDir, fingerprint)
			if err != nil {
				log.Printf("Unable to load local copy of %s: %+v\n",
					keyPair.Name, err)
				continue
			}

			rKeys[keyPair.Name] = *keyPtr
		}
	}()

	// Grab all security groups
	wg.Add(1)
	go func() {
		defer wg.Done()
		sgroupResp, err := r.Conn.SecurityGroups(nil, nil)
		if err != nil {
			lastErr = &err
			return
		}

		for _, group := range sgroupResp.Groups {
			// If the group is associated with a VPC we ignore it
			if group.VpcId != "" {
				continue
			}
			rSgroups[group.Name] = RegionalSGroup{ group, r.Conn.Region.Name }
		}

		// Insert amazon-elb as a valid group
		amazonElbSg := RegionalSGroup{ RegionId: r.Conn.Region.Name }
		amazonElbSg.Name = "amazon-elb-sg"
		amazonElbSg.OwnerId = "amazon-elb"
		rSgroups["amazon-elb-sg"] = amazonElbSg
	}()

	wg.Wait()
	if lastErr != nil {
		return *lastErr
	}

	// All data was pulled back successfully, overwrite current entries in
	// region
	r.Keys = rKeys
	r.SGroups = rSgroups
	return nil
}

func findTag(tags []ec2.Tag, name string) (string, bool) {
	for _, tag := range tags {
		if tag.Key == name { return tag.Value, true }
	}
	return "", false
}
