package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/net/swarm"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	"github.com/multiformats/go-multiaddr"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/p2pdao/libp2p-proxy/config"
	"github.com/p2pdao/libp2p-proxy/protocol"
)

const usage = `
libp2p-proxy creates a http and socks5 proxy service using two libp2p peers.
Generate two peer keys for server and client:
    ./libp2p-proxy -key
Update server.json with server peer key and start remote peer first with:
    ./libp2p-proxy -config server.json
Then update client.json with server peer multiaddress and start the local peer with:
    ./libp2p-proxy -config client.json

Then you can do something like:
    export http_proxy=http://127.0.0.1:1082 https_proxy=http://127.0.0.1:1082
or:
    export http_proxy=socks5://127.0.0.1:1082 https_proxy=socks5://127.0.0.1:1082
then:
    curl "https://github.com"
-------------------------------------------------------
Command flags:
`

func main() {
	// Parse some flags
	cfgPath := flag.String("config", "", "json configuration file; empty uses the default configuration")
	peerID := flag.String("peer", "", "proxy server peer address")
	proxyAddr := flag.String("addr", "", "proxy client address, default is 127.0.0.1:1082")
	help := flag.Bool("help", false, "show help info")
	genKey := flag.Bool("key", false, "generate a new peer private key")
	// version := flag.Bool("version", false, "show version info")
	flag.Parse()

	if *help {
		fmt.Println(usage)
		flag.PrintDefaults()
		os.Exit(0)
	}

	if *genKey {
		_, peerID, err := GeneratePeerKey()
		if err != nil {
			protocol.Log.Fatal(err)
		}
		//fmt.Printf("Private Peer Key: %s\n", privKey)
		fmt.Printf("%s\n", peerID)
		os.Exit(0)
	}

	flag.Parse()
	cfg, err := config.LoadConfig(*cfgPath)
	if err != nil {
		protocol.Log.Fatal(err)
	}

	if peerID != nil && *peerID != "" {
		if strings.HasPrefix(*peerID, "/") {
			if cfg.Proxy == nil {
				cfg.Proxy = &config.ProxyConfig{}
			}
			cfg.Proxy.ServerPeer = *peerID
			if cfg.Proxy.Addr == "" {
				cfg.Proxy.Addr = "127.0.0.1:1082"
			}
			if proxyAddr != nil && *proxyAddr != "" {
				cfg.Proxy.Addr = *proxyAddr
			}
		} else {
			cfg.PeerKey = *peerID
		}

	}

	if cfg.PeerKey == "" {
		cfg.PeerKey, _, _ = GeneratePeerKey()
	}

	if cfg.P2PHost == "" {
		cfg.P2PHost = "p2p.to"
	}

	ctx := ContextWithSignal(context.Background())
	privk, err := ReadPeerKey(cfg.PeerKey)
	if err != nil {
		fmt.Println("Error retrieving secret:", err)
	}

	var opts []libp2p.Option = []libp2p.Option{
		libp2p.Identity(privk),
		libp2p.UserAgent(protocol.ServiceName),
		libp2p.EnableRelay(),
	}

	//acl, err := protocol.NewACL(cfg.ACL)
	//if err != nil {
	//protocol.Log.Fatal(err)
	//}
	//opts = append(opts, libp2p.ConnectionGater(acl))

	// The multiaddress string
	multiAddrStr := "/ip4/64.176.227.5/tcp/4001/p2p/12D3KooWLzi9E1oaHLhWrgTPnPa3aUjNkM8vvC8nYZp1gk9RjTV1"

	// Parse the multiaddress
	multiAddr, err := multiaddr.NewMultiaddr(multiAddrStr)
	if err != nil {
		log.Fatalf("Failed to parse multiaddress: %v", err)
	}

	// Extract AddrInfo from the multiaddress
	relay1info, err := peer.AddrInfoFromP2pAddr(multiAddr)
	if err != nil {
		log.Fatalf("Failed to extract AddrInfo: %v", err)
	}



		if cfg.Network.EnableNAT {
			opts = append(opts,
				libp2p.NATPortMap(),
				libp2p.EnableNATService(),
			)
		}

		host, err := libp2p.New(opts...)
		if err != nil {
			protocol.Log.Fatal(err)
		}

		// Connect both unreachable1 and unreachable2 to relay1
		if err := host.Connect(ctx, *relay1info); err != nil {
			log.Printf("Failed to connect unreachable1 and relay1: %v", err)
			return
		}

		fmt.Printf("Peer ID: %s\n", host.ID())
		fmt.Printf("Peer Addresses:\n")
		for _, addr := range host.Addrs() {
			fmt.Printf("\t%s/p2p/%s\n", addr, host.ID())
		}

		ping.NewPingService(host)
		proxy := protocol.NewProxyService(ctx, host, cfg.P2PHost, host.ID())

		// Hosts that want to have messages relayed on their behalf need to reserve a slot
		// with the circuit relay service host
		// As we will open a stream to unreachable2, unreachable2 needs to make the
		// reservation
		_, err = client.Reserve(ctx, host, *relay1info)
		if err != nil {
			log.Printf("unreachable2 failed to receive a relay reservation from relay1. %v", err)
			return
		}

		// Register a connection notification handler
		host.Network().Notify(&network.NotifyBundle{
			ConnectedF: func(n network.Network, conn network.Conn) {
				addr := conn.RemoteMultiaddr()

				if addr.String() == "" {
					fmt.Println("No multiaddr found for connection.")
					return
				}

				// Check if the connection is using a relay
				if isRelayConnection(addr.String()) {
					fmt.Printf("Connected via relay: %s\n", addr)
				} else {
					fmt.Printf("Direct P2P connection: %s\n", addr)
				}
			},
			DisconnectedF: func(_ network.Network, conn network.Conn) {
				if conn.RemotePeer() == relay1info.ID {
					fmt.Println("Lost connection to relay. Reconnecting...")
					go reconnectToRelay(host, relay1info, proxy.GetRemotePeer())
				}
			},
		})

		go func() {
			if err := proxy.Serve("0.0.0.0:1082"); err != nil {
				protocol.Log.Fatal(err)
			}
		}()
		go func() {
			if err := proxy.ServeSsh("0.0.0.0:2222"); err != nil {
				protocol.Log.Fatal(err)
			}
		}()

		// Define a handler function
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusInternalServerError)
				return
			}
			defer r.Body.Close() // Ensure the body is closed
			bodyStr := string(body)
			//fmt.Fprintln(w, "Hello, World!", bodyStr)
			peerID,err := peer.Decode(bodyStr)
			if err != nil {
				protocol.Log.Fatal(err)
			}

			if host.ID() == peerID {
				fmt.Fprintln(w, "switch off proxy")
				proxy.SetRemotePeer(peerID)
			}else {

			serverPeer, err := ma.NewMultiaddr("/p2p/" + relay1info.ID.String() + "/p2p-circuit/p2p/" + bodyStr)
			if err != nil {
				http.Error(w, "Failed to create peer address", http.StatusInternalServerError)
				return
			} else {
				log.Println(serverPeer)
			}

			host.Network().(*swarm.Swarm).Backoff().Clear(peerID)

			log.Println("Now let's attempt to connect the hosts via the relay node")

			// Open a connection to the previously unreachable host via the relay address
			unreachable2relayinfo := peer.AddrInfo{
				ID:    peerID,
				Addrs: []ma.Multiaddr{serverPeer},
			}
			// host.Peerstore().AddAddrs(serverPeer.ID, serverPeer.Addrs, peerstore.PermanentAddrTTL)
			ctxt, cancel := context.WithTimeout(ctx, time.Second*5)

			if err := host.Connect(context.Background(), unreachable2relayinfo); err != nil {
				log.Printf("Unexpected error here. Failed to connect unreachable1 and unreachable2: %v", err)
				http.Error(w, "Failed to connect to remote peer", http.StatusInternalServerError)
				return
			}

			log.Println("Yep, that worked!")
			
			res := <-ping.Ping(ctxt, host, peerID)
			if res.Error != nil {
				protocol.Log.Warnf("ping error: %v", res.Error)
				http.Error(w, "ping error", http.StatusInternalServerError)
				return
			} else {
				protocol.Log.Infof("ping RTT: %s", res.RTT)
			}
			cancel()
			host.ConnManager().Protect(peerID, "proxy")
			proxy.SetRemotePeer(peerID)
			}
		})

		go func() {
			// Start the HTTP server
			fmt.Println("Starting server on :8080...")
			err = http.ListenAndServe(":8080", nil)
			if err != nil {
				fmt.Println("Error starting server:", err)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				// Exit the loop if the context is canceled or times out
				return
			default:
				// Create a new context with a 5-second timeout for each ping
				ctxt, cancel := context.WithTimeout(ctx, time.Second*5)

				res := <-ping.Ping(ctxt, host, relay1info.ID)
				if res.Error != nil {
					protocol.Log.Fatalf("ping error: %v", res.Error)
				} else {
					protocol.Log.Infof("ping RTT: %s", res.RTT)
				}
				cancel()

				// Wait for 30 seconds before the next iteration
				time.Sleep(30 * time.Second)
			}
		}
}

func ContextWithSignal(ctx context.Context) context.Context {
	newCtx, cancel := context.WithCancel(ctx)
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		cancel()
	}()
	return newCtx
}

type static string

func newStatic(root string) static {
	root = filepath.FromSlash(root)
	if root[0] == '.' {
		wd, err := os.Getwd()
		if err != nil {
			protocol.Log.Fatal(err)
		}
		root = filepath.Join(wd, root)
	}
	info, _ := os.Stat(root)
	if info == nil || !info.IsDir() {
		protocol.Log.Fatalf("invalid root path: %s", root)
	}
	return static(root)
}

func (s static) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := filepath.FromSlash(r.URL.Path)
	name := filepath.Join(string(s), path)
	if !strings.HasPrefix(name, string(s)) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "invalid request path: %s", path)
		return
	}
	defer protocol.Log.Infof("serve file: %s", name)
	http.ServeFile(w, r, name)
}

// Helper function to check if the multiaddr uses a relay
func isRelayConnection(multiaddr string) bool {
	return contains(multiaddr, "/p2p-circuit")
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (len(substr) == 0 || (s != "" && (s[0:len(substr)] == substr || contains(s[1:], substr))))
}

func reconnectToRelay(host host.Host, relayInfo *peer.AddrInfo, peer peer.ID) {
	for {
		host.Network().(*swarm.Swarm).Backoff().Clear(peer)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err := host.Connect(ctx, *relayInfo)
		if err == nil {
			fmt.Println("Reconnected to relay.")
			return
		}

		fmt.Printf("Retrying connection to relay: %v\n", err)
		time.Sleep(5 * time.Second) // Retry delay
	}
}
