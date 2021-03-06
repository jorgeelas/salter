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
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

type Key struct {
	Name        string
	Key         rsa.PrivateKey
	Fingerprint string
	Filename    string
}

// Structs/constants from Go crypto/x509/pkcs8.go
type pkcs8 struct {
	Version    int
	Algo       pkix.AlgorithmIdentifier
	PrivateKey []byte
}

var asn1TagNull = 5
var oidPublicKeyRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
var rsaAlgorithmIdentifier = pkix.AlgorithmIdentifier{
	Algorithm:  oidPublicKeyRSA,
	Parameters: asn1.RawValue{Tag: asn1TagNull},
}

// Primitive PKCS-8 encoder for a RSA private key
func marshalToPKCS8(privKey *rsa.PrivateKey) []byte {
	pk1 := pkcs8{Algo: rsaAlgorithmIdentifier,
		PrivateKey: x509.MarshalPKCS1PrivateKey(privKey)}
	b, _ := asn1.Marshal(pk1)
	return b
}

func LoadKey(keyName, keyDir, remoteFingerprint string) (*Key, error) {
	filename := filepath.Join(keyDir, keyName+".pem")
	if FileExists(filename) {
		// Ensure the key file is not og accessible
		info, _ := os.Stat(filename)
		if info.Mode().Perm() != 0600 {
			errorf("WARNING: incorrect perms on key %s - %s\n",
				filename, info.Mode().Perm())
		}

		// Read the whole PEM file
		data, err := ioutil.ReadFile(filename)
		if err != nil {
			return nil, err
		}

		// Decode from PEM
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("Failed to decode key from %s: %s",
				filename, err)
		}

		// Decode from DER
		privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}

		// If the key was generated by AWS, it will return a SHA1 fingeprint of
		// PRIVATE key (40 bytes long); otherwise it will return a MD5 fingerprint
		// of the PUBLIC key.
		if len(remoteFingerprint) > 32 {
			// Validate SHA-1 fingerprint of private key (PKCS-8 encoded)
			h := sha1.New()
			h.Write(marshalToPKCS8(privKey))
			privFingerprint := fmt.Sprintf("%x", h.Sum(nil))
			if privFingerprint != remoteFingerprint {
				return nil, fmt.Errorf("Mismatched private fingerprint for %s: %s != %s",
					keyName, privFingerprint, remoteFingerprint)
			}
		} else {
			// Validate MD-5 fingerprint of public key (PKIX encoded)
			pub, _ := x509.MarshalPKIXPublicKey(&(privKey.PublicKey))
			h := md5.New()
			h.Write(pub)
			pubFingerprint := fmt.Sprintf("%x", h.Sum(nil))
			if pubFingerprint != remoteFingerprint {
				return nil, fmt.Errorf("Mismatched public fingerprint for %s: %s != %s",
					keyName, pubFingerprint, remoteFingerprint)

			}
		}

		// Successfully validated key fingerprint; return the key
		return &Key{Name: keyName, Key: *privKey, Filename: filename}, nil
	} else {
		return nil, fmt.Errorf("Missing file for %s: %s", filename, remoteFingerprint)
	}
}

// Generate a new RSA key, serializing to a PKCS-1 PEM file
func generateKey(filename string, bits int) error {
	privKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return err
	}

	// Save the private key to local disk
	block := pem.Block{Type: "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey)}
	return ioutil.WriteFile(filename, pem.EncodeToMemory(&block), 0600)
}

// Construct a public key authentictor suitable for using in a ssh.ClientConfig
func PublicKeyAuth(k Key) []ssh.AuthMethod {
	authKey, err := ssh.NewSignerFromKey(&(k.Key))
	if err != nil {
		errorf("Failed to construct public key auth struct from %s: %+v\n",
			k.Name, err)
		return nil
	}
	return []ssh.AuthMethod{ssh.PublicKeys(authKey)}
}
