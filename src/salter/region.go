package main

import "strings"
import "sync"
import "github.com/dizzyd/goamz/aws"
import "github.com/dizzyd/goamz/ec2"
import "log"

type Region struct {
	Keys      map[string]Key	  // Key name -> Key (key.go)
	Conn      ec2.EC2

	SGroups   map[string]ec2.SecurityGroupInfo // SG name -> Info

	dataDir   string
}

func GetRegion(name string) (*Region, error) {
	region, found := G_REGIONS[name]
	if !found {
		conn := ec2.New(G_CONFIG.AwsAuth, aws.Regions[name])
		region := &Region{
			Conn: *conn,
			dataDir: G_CONFIG.DataDir,
		}

		err := region.Refresh()
		if err != nil {
			return region, err
		}

		G_REGIONS[name] = region
	}

	return region, nil
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

func (r *Region) Refresh() error {
	rKeys := make(map[string]Key)
	rSgroups := make(map[string]ec2.SecurityGroupInfo)

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
			rSgroups[group.Name] = group
		}
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
