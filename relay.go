package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	multiaddr "github.com/multiformats/go-multiaddr"
)

func main() {
	// Create a context that can be canceled
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load or generate a fixed private key
	privKey := loadOrGeneratePrivateKey()

	// Create a libp2p host with a fixed port and private key
	host, err := libp2p.New(
		libp2p.ListenAddrs(multiaddr.StringCast("/ip4/0.0.0.0/tcp/4001")), // Fixed port 4001
		libp2p.Identity(privKey), // Fixed Peer ID
	)
	if err != nil {
		panic(fmt.Sprintf("Failed to create libp2p host: %s", err))
	}

	// Set up the relay server on the host
	_, err = relayv2.New(host)
	if err != nil {
		panic(fmt.Sprintf("Failed to enable relay: %s", err))
	}

	// Print host's listening addresses
	fmt.Println("Relay server is running!")
	fmt.Println("Peer ID:", host.ID())
	fmt.Println("Listening on:")

	for _, addr := range host.Addrs() {
		fullAddr := addr.Encapsulate(multiaddr.StringCast(fmt.Sprintf("/p2p/%s", host.ID())))
		fmt.Println(fullAddr)
	}

	// Wait for termination signal (e.g., Ctrl+C)
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	<-signalChan

	fmt.Println("\nShutting down relay server...")
	if err := host.Close(); err != nil {
		fmt.Printf("Error shutting down host: %s\n", err)
	}
}

// loadOrGeneratePrivateKey loads a fixed private key or generates a new one if not found
func loadOrGeneratePrivateKey() crypto.PrivKey {
	keyFile := "peerkey.base64"

	// Try to load the key from a file
	if keyData, err := os.ReadFile(keyFile); err == nil {
		privKeyBytes, err := base64.StdEncoding.DecodeString(string(keyData))
		if err != nil {
			panic(fmt.Sprintf("Failed to decode private key: %s", err))
		}
		privKey, err := crypto.UnmarshalEd25519PrivateKey(privKeyBytes)
		if err != nil {
			panic(fmt.Sprintf("Failed to unmarshal private key: %s", err))
		}
		return privKey
	}

	// Generate a new private key if not found
	_, privKeyBytes, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(fmt.Sprintf("Failed to generate private key: %s", err))
	}
	privKey, err := crypto.UnmarshalEd25519PrivateKey(privKeyBytes)
	if err != nil {
		panic(fmt.Sprintf("Failed to unmarshal generated private key: %s", err))
	}

	// Save the private key to a file
	if err := os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(privKeyBytes)), 0600); err != nil {
		panic(fmt.Sprintf("Failed to save private key: %s", err))
	}

	return privKey
}
