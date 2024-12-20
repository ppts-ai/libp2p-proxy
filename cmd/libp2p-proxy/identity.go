package main

import (
	"crypto/sha256"
    "fmt"
	"net"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Custom Reader that returns deterministic data based on hash input
type macHashReader struct {
    hash []byte
    pos  int
}

// Implement the Read function to return bytes from the hash
func (r *macHashReader) Read(p []byte) (n int, err error) {
    for i := 0; i < len(p); i++ {
        if r.pos >= len(r.hash) {
            r.pos = 0 // Loop over the hash if p is larger
        }
        p[i] = r.hash[r.pos]
        r.pos++
    }
    return len(p), nil
}

// Function to get the MAC address of the first available network interface
func getMacAddress() (string, error) {
    interfaces, err := net.Interfaces()
    if err != nil {
        return "", err
    }
    for _, iface := range interfaces {
        if iface.HardwareAddr != nil {
            return iface.HardwareAddr.String(), nil
        }
    }
    return "", fmt.Errorf("no MAC address found")
}

func ReadPeerKey(peerKey string) (crypto.PrivKey, error) {
	bytes, err := crypto.ConfigDecodeKey(peerKey)
	if err != nil {
		return nil, err
	}

	return crypto.UnmarshalPrivateKey(bytes)
}

func GeneratePeerKey() (string, string, error) {
	// Step 1: Get MAC address
	macAddress, err := getMacAddress()
	if err != nil {
		fmt.Println("Error retrieving MAC address:", err)
		return "", "", err
	}

	// Step 2: Hash the MAC address
	hasher := sha256.New()
	hasher.Write([]byte(macAddress))
	macHash := hasher.Sum(nil)

	// Step 3: Create a deterministic reader based on the MAC address hash
	reader := &macHashReader{hash: macHash}
	privk, _, err := crypto.GenerateEd25519Key(reader)
	if err != nil {
		return "", "", err
	}

	bytes, err := crypto.MarshalPrivateKey(privk)
	if err != nil {
		return "", "", err
	}

	id, _ := peer.IDFromPrivateKey(privk)

	return crypto.ConfigEncodeKey(bytes), id.String(), nil
}
