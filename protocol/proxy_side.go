package protocol

import (
	"net"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

func (p *ProxyService) Serve(proxyAddr string) error {
	ln, err := net.Listen("tcp", proxyAddr)
	if err != nil {
		return err
	}

	go p.Wait(ln.Close)

	for {
		conn, err := ln.Accept()
		if err := p.ctx.Err(); err != nil {
			return err
		}

		if err != nil {
			return err
		}
		go p.sideHandler(conn, p.remotePeer)
	}
}

func (p *ProxyService) ServeSsh(proxyAddr string) error {
	ln, err := net.Listen("tcp", proxyAddr)
	if err != nil {
		return err
	}

	go p.Wait(ln.Close)

	for {
		conn, err := ln.Accept()
		if err := p.ctx.Err(); err != nil {
			return err
		}

		if err != nil {
			return err
		}
		go p.sideHandlerSsh(conn, p.remotePeer)
	}
}

func (p *ProxyService) sideHandler(conn net.Conn, remotePeer peer.ID) {
	defer conn.Close()
	if remotePeer == "" {
		return
	}
	// standalone mode
	if remotePeer == p.host.ID() {
		p.handler(NewBufReaderStream(conn))
		return
	}

	s, err := p.host.NewStream(network.WithAllowLimitedConn(p.ctx, "/p2pdao/libp2p-proxy/1.0.0"), remotePeer, ID)
	if err != nil {
		Log.Errorf("creating stream to %s error: %v", remotePeer, err)
		return
	}

	defer s.Close()
	if err := tunneling(s, conn); shouldLogError(err) {
		Log.Warn(err)
	}
}

func (p *ProxyService) sideHandlerSsh(conn net.Conn, remotePeer peer.ID) {
	defer conn.Close()
	if remotePeer == "" {
		return
	}
	// standalone mode
	if remotePeer == p.host.ID() {
		p.handler(NewBufReaderStream(conn))
		return
	}

	s, err := p.host.NewStream(network.WithAllowLimitedConn(p.ctx, "/p2pdao/libp2p-ssh/1.0.0"), remotePeer, SSH_ID)
	if err != nil {
		Log.Errorf("creating stream to %s error: %v", remotePeer, err)
		return
	}

	defer s.Close()
	if err := tunneling(s, conn); shouldLogError(err) {
		Log.Warn(err)
	}
}
