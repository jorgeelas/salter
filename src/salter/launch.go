package main

import "fmt"
import "launchpad.net/goamz/aws"
import "launchpad.net/goamz/ec2"

type ConnCache map[string]ec2.EC2      // region -> EC2 conn
type InstCache map[string]ec2.Instance // tag:Name -> Instance

type KeyPairMap map[string]string     // keyname -> fingerprint
type KeyCache  map[string]KeyPairMap  // region -> KeyPairMap


func (kcache KeyCache) cacheRegion(ec2 *ec2.EC2) {
	_, found := kcache[ec2.Name]
	if !found {
		keys, err := ec2.KeyPairs(nil, nil)
		if err != nil {
			panic(fmt.Sprintf("Failed to list keypairs for %s: %s\n",
				ec2.Name, err))
		}

		keyPairs := make(KeyPairMap)
		for _, keyPair := range keys.Keys {
			// Normalize the fingerprint by removing colons
			fingerprint := strings.Replace(keyPair.Fingerprint, ":", "", -1)
			keyPairs[keyPair.Name] = fingerprint
		}

		kcache[ec2.Name] = keyPairs
	}
}

func (icache InstCache) cacheRegion(ec2 *ec2.EC2) {
	_, found := icache[ec2.Name]
	if !found {
		instanceResp, err := ec2.Instances(nil, nil)
		if err != nil {
			panic(fmt.Sprintf("Failed to retrieve instances for %s: %s\n",
				ec2.Name, err))
		}

		for _, resv := range instanceResp.Reservations {
			for _, inst := range resv.Instances {
				name, hasName := findTag(inst.Tags, "Name")

				// Ignore instances without a Name tag
				if !hasName  { continue }

				// Ignore instances that are not running
				if inst.State.Name != "running" { continue }

				// Check for conflicts
				oldInst, found := icache[name]
				if found {
					// We've found an existing instance with the same
					// name (probably in another region!!). This is
					// a major configuration problem that needs to
					// be resolved by hand
					panic(fmt.Sprintf("%s (%s, %s) conflicts with (%s, %s). " +
						"Please resolve this conflict by shutting down " +
						"one of these instances.",
						name, oldInst.InstanceId, oldInst.AvailZone,
						inst.InstanceId, inst.AvailZone))
				}

				// Cache the instance
				icache[name] = inst
			}
		}
	}
}

func findTag(tags []ec2.Tag, name string) (string, bool) {
	for _, tag := range tags {
		if tag.Key == name { return tag.Value, true }
	}
	return "", false
}

func loadKey(filename string) (*rsa.PrivateKey, error) {
	if os.IsExist(filename) {
		// Read the whole PEM file
		data, err := ioutil.ReadFile(filepath.join(dataDir, keyname + ".pem"))
		if err != nil {
			return nil, err
		}

		// Decode from PEM
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("Failed to decode key from %s: %s\n",
				filename, err)
		}

		// Parse out the private key from DER format
		return x509.ParsePKCS1PrivateKey()
	} else {
		return nil, nil
	}
}

func keyFingerprint(filename string) string {

	privKey, err := loadKey(filename)
	if err != nil {
		panic(fmt.Sprintf("Failed to load key %s: %s\n", [filename, err]))
	}

	if privKey == nil {
		// No key file exists; empty string as a fingerprint
		return ""
	}

	// Key exists, cast it into public form and marshal into DER for hashing
	derPubKey := x509.MarshalPKIXPublicKey(privKey.(rsa.PublicKey))
	return fmt.Sprintf("%x", md5.New().Sum(derPubKey))
}

func generateKeyPair(keyname string, conn *ec2.EC2, keys *KeyPairMap, dataDir string) {
	filename := filepath.join(dataDir, keyname + ".pem")

	// Try to load and compute the key from local storage
	localFingerprint := keyFingerprint(filename)

	remoteFingerprint, existsOnAws := keys[keyname]

	// If the key exists on AWS and the fingerprints match, nothing more to do
	if existsOnAws && localFingerprint == remoteFingerprint {
		return
	}

	// If the key exists on AWS and not locally, manual intervention is required
	if existsOnAws && localFingerprint == "" {

	} else if existsOnAws && remoteFingerprint != localFingerprint {
		// Key exists on AWS, but does not match local fingerprint
	} else if 
	

	// Recoverable states:
	// - Key does not exist on AWS, but does exist locally -> Import to AWS
	// - Key does not exist anywhere -> Create + Import
}

func launch(config Config) bool {
	// Spin up a cache of connections for the nodes we're launching
	instances := make(InstCache)
	keypairs  := make(KeyCache)

	// Walk all the target nodes, caching info from their region and also
	// setting up keypairs
	for _, node := range config.Targets {
		conn := ec2.New(config.AwsAuth, aws.Regions[node.Region])
		instances.cacheRegion(conn)
		keypairs.cacheRegion(conn)

		generateKeyPair(node.KeyName, conn, keypairs[conn.Name], config.DataDir)
	}

	// Setup a channel for queuing up nodes to launch and another
	// for shutdown notification
	launchQueue := make(chan NodeConfig)
	shutdownQueue := make(chan bool)

	// Spin up goroutines to start launching nodes; we limit
	// total number of goroutines to be nice to AWS
	for i := 0; i < config.MaxConcurrent; i++ {
		go func() {
			for node := range launchQueue {
				launchNode(node, config, instances)
			}

			shutdownQueue <- true
		}()
	}

	// Start enqueuing targets to launch
	for _, node := range config.Targets {
		launchQueue <- node
	}

	// All done launching
	close(launchQueue)

	// Wait for each of the goroutines to shutdown
	for i := 0; i < config.MaxConcurrent; i++ {
		<- shutdownQueue
	}

	fmt.Printf("All done!\n")

	return true
}


func launchNode(node NodeConfig, config Config, instCache InstCache) {
	// If the instance is already running, skip it
	_, running := instCache[node.Name]
	if running {
		fmt.Printf("Not launching %s; already running.\n", node.Name)
		return
	}

	// Open the connection
	//conn := ec2.New(config.AwsAuth, aws.Regions[node.Region])

	fmt.Printf("Launching %s\n", node.Name)


}
