package main

import "crypto/rsa"
import "crypto/sha1"
import "crypto/x509"
import "crypto/x509/pkix"
import "encoding/asn1"
import "encoding/pem"
import "fmt"
import "io/ioutil"
import "crypto/rand"
import "encoding/base64"
import "crypto/md5"

// Structs/constants from Go crypto/x509/pkcs8.go
type pkcs8 struct {
	Version    int
	Algo       pkix.AlgorithmIdentifier
	PrivateKey []byte
}

var asn1TagNull       = 5
var oidPublicKeyRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
var rsaAlgorithmIdentifier = pkix.AlgorithmIdentifier { Algorithm: oidPublicKeyRSA, Parameters: asn1.RawValue{Tag: asn1TagNull}}

// Primitive PKCS-8 encoder for a RSA private key
func marshalToPKCS8(privKey *rsa.PrivateKey) []byte {
	pk1 := pkcs8 { Algo: rsaAlgorithmIdentifier,
		       PrivateKey: x509.MarshalPKCS1PrivateKey(privKey) }
	b, _ := asn1.Marshal(pk1)
	return b
}

// Load a private key from a PKCS-1 DER file
func loadKey(filename string) (*rsa.PrivateKey, error) {
	if FileExists(filename) {
		// Read the whole PEM file
		data, err := ioutil.ReadFile(filename)
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
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	} else {
		return nil, nil
	}
}

func keyFingerprint(filename string) (string, string) {
	privKey, err := loadKey(filename)
	if err != nil {
		panic(fmt.Sprintf("Failed to load key %s: %s\n", filename, err))
	}

	if privKey == nil {
		// No key file exists; empty string as a fingerprint
		return "", ""
	}

	// Calculate the SHA-1 hash of private key portion (PKCS-8 encoded)
	h := sha1.New()
	h.Write(marshalToPKCS8(privKey))
	privFingerprint := fmt.Sprintf("%x", h.Sum(nil))

	// Calculate the MD-5 hash of public key portion (PKIX encoded)
	pub, _ := x509.MarshalPKIXPublicKey(&(privKey.PublicKey))
	h = md5.New()
	h.Write(pub)
	pubFingerprint := fmt.Sprintf("%x", h.Sum(nil))

	return pubFingerprint, privFingerprint
}

// Generate a new RSA key, store in PKCS-1 PEM file
func generateKey(filename string, bits int) error {
	privKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return err
	}

	// Save the private key to local disk
	block := pem.Block { Type: "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey) }
	return ioutil.WriteFile(filename, pem.EncodeToMemory(&block), 0600)
}


// Load a PKCS-1 private key from disk and extract the public portion of it
func loadPublicKey(filename string) (string, error) {
	privKey, err := loadKey(filename)
	if err != nil {
		return "", err
	}

	// PEM-encode the public key portion of the data and return in
	pubBytes, err := x509.MarshalPKIXPublicKey(&(privKey.PublicKey))

	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(pubBytes), nil
}
