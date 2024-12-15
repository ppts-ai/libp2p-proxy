package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	libp2p "github.com/libp2p/go-libp2p"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	multiaddr "github.com/multiformats/go-multiaddr"
)

func main() {
	// Create a context that can be canceled
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a libp2p host
	host, err := libp2p.New()
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
