package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ipfs/go-datastore"
	leveldb "github.com/ipfs/go-ds-leveldb"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p-peerstore/pstoreds"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
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
		privKey, peerID, err := GeneratePeerKey()
		if err != nil {
			protocol.Log.Fatal(err)
		}
		fmt.Printf("Private Peer Key: %s\n", privKey)
		fmt.Printf("Public Peer ID: %s\n", peerID)
		os.Exit(0)
	}

	flag.Parse()
	cfg, err := config.LoadConfig(*cfgPath)
	if err != nil {
		protocol.Log.Fatal(err)
	}

	if peerID != nil && *peerID != "" {
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
		protocol.Log.Fatal(err)
	}

	var opts []libp2p.Option = []libp2p.Option{
		libp2p.Identity(privk),
		libp2p.UserAgent(protocol.ServiceName),
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(),
		libp2p.WithDialTimeout(time.Second * 60),
	}

	// if len(cfg.Network.Relays) > 0 {
	// 	relays := make([]peer.AddrInfo, 0, len(cfg.Network.Relays))
	// 	for _, addr := range cfg.Network.Relays {
	// 		pi, err := peer.AddrInfoFromString(addr)
	// 		if err != nil {
	// 			protocol.Log.Fatal(fmt.Sprintf("failed to initialize default static relays: %s", err))
	// 		}
	// 		relays = append(relays, *pi)
	// 	}
	// 	opts = append(opts,
	// 		libp2p.EnableAutoRelay(),
	// 		libp2p.StaticRelays(relays),
	// 	)
	// }

	acl, err := protocol.NewACL(cfg.ACL)
	if err != nil {
		protocol.Log.Fatal(err)
	}
	opts = append(opts, libp2p.ConnectionGater(acl))

	if cfg.Proxy == nil || cfg.Proxy.ServerPeer == "" {
		// run DHT client for server side
		var ds datastore.Batching
		if cfg.DHT.DatastorePath != "" {
			ds, err = leveldb.NewDatastore(cfg.DHT.DatastorePath, nil)
			if err != nil {
				protocol.Log.Fatal(err)
			}
		}
		if ds != nil {
			pds, err := pstoreds.NewPeerstore(ctx, ds, pstoreds.DefaultOpts())
			if err != nil {
				protocol.Log.Fatal(err)
			}
			opts = append(opts, libp2p.Peerstore(pds))
		}

		opts = append(opts,
			libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
				dhtpeers := dht.GetDefaultBootstrapPeerAddrInfos()
				if len(cfg.DHT.BootstrapPeers) > 0 {
					for _, s := range cfg.DHT.BootstrapPeers {
						if peer, err := peer.AddrInfoFromString(s); err == nil {
							dhtpeers = append(dhtpeers, *peer)
						}
					}
				}

				dhtopts := []dht.Option{
					dht.Mode(dht.ModeClient),
					dht.BootstrapPeers(dhtpeers...),
				}
				if ds != nil {
					dhtopts = append(dhtopts, dht.Datastore(ds))
				}

				idht, err := dht.New(ctx, h, dhtopts...)
				if err == nil {
					idht.Bootstrap(ctx)
				}
				return idht, err
			}),
		)
	}

	// The multiaddress string
	multiAddrStr := "/ip4/104.131.131.82/tcp/4001/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ"

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

	if cfg.Proxy == nil {
		opts = append(opts,
			libp2p.ListenAddrStrings(cfg.Network.ListenAddrs...),
		)

		if cfg.Network.EnableNAT {
			opts = append(opts,
				libp2p.NATPortMap(),
				libp2p.EnableNATService(),
			)
		}

		if len(cfg.Network.ExternalAddrs) > 0 {
			emas := make([]ma.Multiaddr, 0, len(cfg.Network.ExternalAddrs))
			for _, ad := range cfg.Network.ExternalAddrs {
				if addr, err := ma.NewMultiaddr(ad); err == nil {
					emas = append(emas, addr)
				}
			}

			if len(emas) > 0 {
				opts = append(opts,
					libp2p.ForceReachabilityPublic(),
					libp2p.AddrsFactory(func(_ []ma.Multiaddr) []ma.Multiaddr {
						return emas
					}),
				)
			}
		}

		host, err := libp2p.New(opts...)
		if err != nil {
			protocol.Log.Fatal(err)
		}

		// Connect both unreachable1 and unreachable2 to relay1
		if err := host.Connect(context.Background(), *relay1info); err != nil {
			log.Printf("Failed to connect unreachable1 and relay1: %v", err)
			return
		}

		fmt.Printf("Peer ID: %s\n", host.ID())
		fmt.Printf("Peer Addresses:\n")
		for _, addr := range host.Addrs() {
			fmt.Printf("\t%s/p2p/%s\n", addr, host.ID())
		}

		ping.NewPingService(host)
		proxy := protocol.NewProxyService(ctx, host, cfg.P2PHost)

		// Hosts that want to have messages relayed on their behalf need to reserve a slot
		// with the circuit relay service host
		// As we will open a stream to unreachable2, unreachable2 needs to make the
		// reservation
		_, err = client.Reserve(context.Background(), host, *relay1info)
		if err != nil {
			log.Printf("unreachable2 failed to receive a relay reservation from relay1. %v", err)
			return
		}

		if cfg.ServePath != "" {
			ss := newStatic(cfg.ServePath)
			fmt.Printf("Serve HTTP static: %s\n", ss)
			if err := proxy.ServeHTTP(ss, nil); err != nil {
				protocol.Log.Fatal(err)
			}
		} else {
			if err := proxy.Wait(nil); err != nil {
				protocol.Log.Fatal(err)
			}
		}

	} else {
		opts = append(opts,
			libp2p.NoListenAddrs,
		)
		host, err := libp2p.New(opts...)
		if err != nil {
			protocol.Log.Fatal(err)
		}

		// Connect both unreachable1 and unreachable2 to relay1
		if err := host.Connect(context.Background(), *relay1info); err != nil {
			log.Printf("Failed to connect unreachable1 and relay1: %v", err)
			return
		}

		fmt.Printf("Peer ID: %s\n", host.ID())
		serverPeer := &peer.AddrInfo{ID: host.ID()}
		if cfg.Proxy.ServerPeer != "" {
			serverPeer1, err := peer.AddrInfoFromString(cfg.Proxy.ServerPeer)
			if err != nil {
				protocol.Log.Fatal(err)
			}

			// Now create a new address for unreachable2 that specifies to communicate via
			// relay1 using a circuit relay
			serverPeer, err := ma.NewMultiaddr("/p2p/" + relay1info.ID.String() + "/p2p-circuit/p2p/" + serverPeer1.ID.String())
			if err != nil {
				log.Println(err)
				return
			} else {
				log.Println(serverPeer)
			}

			// Since we just tried and failed to dial, the dialer system will, by default
			// prevent us from redialing again so quickly. Since we know what we're doing, we
			// can use this ugly hack (it's on our TODO list to make it a little cleaner)
			// to tell the dialer "no, its okay, let's try this again"
			host.Network().(*swarm.Swarm).Backoff().Clear(serverPeer1.ID)

			log.Println("Now let's attempt to connect the hosts via the relay node")

			// Open a connection to the previously unreachable host via the relay address
			unreachable2relayinfo := peer.AddrInfo{
				ID:    serverPeer1.ID,
				Addrs: []ma.Multiaddr{serverPeer},
			}
			// host.Peerstore().AddAddrs(serverPeer.ID, serverPeer.Addrs, peerstore.PermanentAddrTTL)
			ctxt, cancel := context.WithTimeout(ctx, time.Second*5)

			if err := host.Connect(context.Background(), unreachable2relayinfo); err != nil {
				log.Printf("Unexpected error here. Failed to connect unreachable1 and unreachable2: %v", err)
				return
			}

			log.Println("Yep, that worked!")

			res := <-ping.Ping(ctxt, host, serverPeer1.ID)
			if res.Error != nil {
				protocol.Log.Fatalf("ping error: %v", res.Error)
			} else {
				protocol.Log.Infof("ping RTT: %s", res.RTT)
			}
			cancel()
			host.ConnManager().Protect(serverPeer1.ID, "proxy")
		}

		proxy := protocol.NewProxyService(ctx, host, cfg.P2PHost)
		fmt.Printf("Proxy Address: %s\n", cfg.Proxy.Addr)
		if err := proxy.Serve(cfg.Proxy.Addr, serverPeer.ID); err != nil {
			protocol.Log.Fatal(err)
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
