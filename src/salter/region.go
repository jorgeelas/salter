package main

import "fmt"
import "strings"
import "github.com/dizzyd/goamz/aws"
import "github.com/dizzyd/goamz/ec2"

type Region struct {
	Instances map[string]ec2.Instance // Canonical instance-id -> Instance
	Keys      map[string]Key	  // Key name -> Key (key.go)
	Conn      ec2.EC2

	instanceNames map[string]*ec2.Instance // Index of Tag:Name -> Instance

	dataDir   string
}

var REGION_CACHE map[string]Region

func GetRegion(name string, config Config) (Region, error) {
	if REGION_CACHE == nil {
		REGION_CACHE = make(map[string]Region)
	}

	region, found := REGION_CACHE[name]
	if !found {
		conn := ec2.New(config.AwsAuth, aws.Regions[name])
		region := Region{
			Conn: *conn,
			dataDir: config.DataDir,
		}
		err := region.Refresh()
		if err != nil {
			return region, err
		}

		REGION_CACHE[name] = region
	}

	return region, nil
}

func NewRegion(conn ec2.EC2, dataDir string) (r *Region, err error) {
	r = &Region{ Conn: conn, dataDir: dataDir }
	err = r.Refresh()
	if err != nil {
		r = nil
	}
	return
}

func (r *Region) Refresh() error {
	rInstances := make(map[string]ec2.Instance)
	rKeys := make(map[string]Key)
	rInstanceNames := make(map[string]*ec2.Instance)

	// Grab instances from this connection
	instanceResp, err := r.Conn.Instances(nil, nil)
	if err != nil {
		return err
	}

	// Traverse all the reservations
	for _, resv := range instanceResp.Reservations {
		for _, inst := range resv.Instances {
			name, hasName := findTag(inst.Tags, "Name")

			// Ignore terminated instances
			if inst.State.Name == "terminated" { continue }

			rInstances[inst.InstanceId] = inst
			if hasName { rInstanceNames[name] = &inst }
		}
	}

	// Grab keys from this connection
	keys, err := r.Conn.KeyPairs(nil, nil)
	if err != nil {
		return err
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
			fmt.Printf("Unable to load local copy of %s: %+v\n", keyPair.Name, err)
			continue
		}

		rKeys[keyPair.Name] = *keyPtr
	}

	// All data was pulled back successfully, overwrite current entries in
	// region
	r.Instances = rInstances
	r.Keys = rKeys
	r.instanceNames = rInstanceNames
	return nil
}

func (r *Region) IsInstanceRunning(name string) bool {
	instance, found := r.instanceNames[name]
	if !found {
		return false
	}
	// From docs, valid values for code: 0 (pending) | 16 (running) | 32
	// (shutting-down) | 48 (terminated) | 64 (stopping) | 80 (stopped)
	return (instance.State.Code < 32)
}

func (r *Region) KeyExists(name string) bool {
	_, found := r.Keys[name]
	return found
}

func findTag(tags []ec2.Tag, name string) (string, bool) {
	for _, tag := range tags {
		if tag.Key == name { return tag.Value, true }
	}
	return "", false
}
