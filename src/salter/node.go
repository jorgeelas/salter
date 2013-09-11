package main

import "fmt"
import "log"
import "github.com/dizzyd/goamz/aws"
import "github.com/dizzyd/goamz/ec2"
import "code.google.com/p/go.crypto/ssh"
import "crypto/x509"
import "encoding/pem"
import "crypto/rand"
import "crypto/rsa"
import "bytes"

type Node struct {
	Name      string
	Roles     []string
	Count     int
	Flavor    string
	RegionId  string `toml:"region"`
	Zone      string
	Ami       string
	SGroup    string
	KeyName   string
	Tags      map[string]string

	Config    *Config
	Instance  *ec2.Instance
	SshClient *ssh.ClientConn
}

func (node *Node) Conn() *ec2.EC2 {
	return ec2.New(G_CONFIG.AwsAuth, aws.Regions[node.RegionId])
}

// Retrieve instance information from AWS
func (node *Node) Update() error {
	// Clear out current instance info
	node.Instance = nil

	// Use the node name as our primary filter
	filter := ec2.NewFilter()
	filter.Add("tag:Name", node.Name)
	filter.Add("instance-state-code", "0", "16")
	response, err := node.Conn().Instances(nil, filter)
	if err != nil {
		return err
	}

	if len(response.Reservations) == 0 {
		// Nothing was returned in the list; it's not running
		return nil
	}

	if len(response.Reservations) > 1 || len(response.Reservations[0].Instances) > 1 {
		// More than one reservation or instances that match our filter;
		// this means something is bjorked and manual intervention is required
		return fmt.Errorf("Unexpected number of reservations/instances for %s",
			node.Name)
	}

	node.Instance = &(response.Reservations[0].Instances[0])
	return nil
}

func (node *Node) IsRunning() bool {
	// Determine if the node is live on AWS and running or pending
	if node.Instance == nil {
		return false
	} else {
		return node.Instance.State.Code < 32
	}
}

// Start the node on AWS
func (node *Node) Start(masterIp string) error {
	// If node is already running, noop
	if node.IsRunning() {
		return fmt.Errorf("already running")
	}

	// Verify that we have a key available to this node
	if !RegionKeyExists(node.KeyName, node.RegionId) {
		return fmt.Errorf("key %s is not available locally",
			node.KeyName)
	}

	// Generate the userdata script for this node
	userData, err := G_CONFIG.generateUserData(node.Name, node.Roles, masterIp)
	if err != nil {
		return err
	}

	runInst := ec2.RunInstances {
		ImageId: node.Ami,
		KeyName: node.KeyName,
		InstanceType: node.Flavor,
		UserData: userData }
	runResp, err := node.Conn().RunInstances(&runInst)
	if err != nil {
		return fmt.Errorf("launch failed: %+v\n", err)
	}

	node.Instance = &(runResp.Instances[0])

	fmt.Printf("Launching %s (%s)\n", node.Name, node.Instance.InstanceId)

	// Instance is now running; apply any tags
	_, err = node.Conn().CreateTags([]string { node.Instance.InstanceId }, node.ec2Tags())
	if err != nil {
		return fmt.Errorf("Failed to apply tags to %s: %+v\n", node.Name, err)
	}

	return nil
}

func (node *Node) Terminate() error {
	// Terminate the node on AWS
	if !node.IsRunning() {
		return fmt.Errorf("node not running")
	}

	_, err := node.Conn().TerminateInstances([]string { node.Instance.InstanceId })
	if err != nil {
		return err
	}
	fmt.Printf("Terminated %s (%s)\n", node.Name, node.Instance.InstanceId)
	node.Instance = nil
	return nil
}

func (node *Node) SshOpen() error {
	if !node.IsRunning() {
		return fmt.Errorf("node not running")
	}

	if node.SshClient == nil {
		config := ssh.ClientConfig{
			User: G_CONFIG.Aws.Username,
			Auth: PublicKeyAuth(RegionKey(node.KeyName, node.RegionId)),
		}

		client, err := ssh.Dial("tcp", node.Instance.DNSName + ":22", &config)
		if err != nil {
			return err
		}

		node.SshClient = client
	}

	return nil
}

func (node *Node) SshClose() {
	if node.SshClient != nil {
		node.SshClient.Close()
		node.SshClient = nil
	}
}

func (node *Node) SshRun(cmd string) error {
	if node.SshClient == nil {
		err := node.SshOpen()
		if err != nil {
			return err
		}
	}

	session, err := node.SshClient.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session - %+v", err)
	}

	defer session.Close()
	output, err := session.Output(cmd)
	log.Printf("%s: output for %s:\n%s\n", node.Name, cmd, output)
	return err
}

func (node *Node) SshUpload(remoteFilename string, data []byte) error {
	if node.SshClient == nil {
		err := node.SshOpen()
		if err != nil {
			return err
		}
	}

	session, err := node.SshClient.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session - %+v", err)
	}

	defer session.Close()

	session.Stdin = bytes.NewReader(data)
	cmd := fmt.Sprintf("/usr/bin/sudo sh -c '/bin/cat > %s'", remoteFilename)
	err = session.Run(cmd)
	log.Printf("%s: uploaded data to %s; error: %+v\n", node.Name, remoteFilename, err)
	return err
}

func (node *Node) ec2Tags() []ec2.Tag {
	result := []ec2.Tag{ ec2.Tag{ Key: "Name", Value: node.Name }}
	for key, value := range node.Tags {
		result = append(result, ec2.Tag{ Key: key, Value: value })
	}
	return result
}

func (node *Node) GenSaltKey(bits int) ([]byte, []byte, error) {
	privKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, nil, err
	}

	// Encode private key as PKCS-1 PEM
	privKeyStr := PemEncode(x509.MarshalPKCS1PrivateKey(privKey), "RSA PRIVATE KEY")

	// Encode public key as PKIX PEM
	pubKeyBin, _ := x509.MarshalPKIXPublicKey(&(privKey.PublicKey))
	pubKeyStr := PemEncode(pubKeyBin, "PUBLIC KEY")
	return pubKeyStr, privKeyStr, nil
}

func PemEncode(data []byte, header string) []byte {
	b := pem.Block{ Type: header, Bytes: data }
	return pem.EncodeToMemory(&b)
}
