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
import "crypto/md5"
import "path/filepath"
import "io"
import "crypto"
import "code.google.com/p/go.crypto/ssh"

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

var asn1TagNull       = 5
var oidPublicKeyRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
var rsaAlgorithmIdentifier = pkix.AlgorithmIdentifier {
	Algorithm: oidPublicKeyRSA,
	Parameters: asn1.RawValue{Tag: asn1TagNull},
}

// Primitive PKCS-8 encoder for a RSA private key
func marshalToPKCS8(privKey *rsa.PrivateKey) []byte {
	pk1 := pkcs8 { Algo: rsaAlgorithmIdentifier,
		       PrivateKey: x509.MarshalPKCS1PrivateKey(privKey) }
	b, _ := asn1.Marshal(pk1)
	return b
}


func LoadKey(keyName, keyDir, remoteFingerprint string) (*Key, error) {
	filename := filepath.Join(keyDir, keyName + ".pem")
	if FileExists(filename) {
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
		return &Key{ Name: keyName, Key: *privKey, Filename: filename }, nil
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
	block := pem.Block { Type: "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey) }
	return ioutil.WriteFile(filename, pem.EncodeToMemory(&block), 0600)
}


// Construct a public key authentictor suitable for using in a ssh.ClientConfig
func PublicKeyAuth(k Key) []ssh.ClientAuth {
	kc := new(keychain)
	kc.keys = append(kc.keys, &(k.Key))
	return []ssh.ClientAuth { ssh.ClientAuthKeyring(kc) }
}

//
// SSH-specific interface for performing client auth
//
type keychain struct {
	keys []interface{}
}

func (k *keychain) Key(i int) (interface{}, error) {
	if i < 0 || i >= len(k.keys) {
		return nil, nil
	}

	switch key := k.keys[i].(type) {
	case *rsa.PrivateKey:
		return &key.PublicKey, nil
	default:
		return nil, fmt.Errorf("keychain.Key: unsupported key type - %+v\n",
			k.keys[i])
	}
}

func (k *keychain) Sign(i int, rand io.Reader, data []byte) (sig []byte, err error) {
	hashFunc := crypto.SHA1
	h := hashFunc.New()
	h.Write(data)
	digest := h.Sum(nil)
	switch key := k.keys[i].(type) {
	case *rsa.PrivateKey:
		return rsa.SignPKCS1v15(rand, key, hashFunc, digest)
	}
	return nil, fmt.Errorf("keychain.Sign: unsupported key type - %+v\n",
		k.keys[i])
}
