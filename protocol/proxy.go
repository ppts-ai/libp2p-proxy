package protocol

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	logging "github.com/ipfs/go-log/v2"
	gostream "github.com/libp2p/go-libp2p-gostream"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	P2PHttpID   protocol.ID = "/http"
	ID          protocol.ID = "/p2pdao/libp2p-proxy/1.0.0"
	SSH_ID      protocol.ID = "/p2pdao/libp2p-ssh/1.0.0"
	ServiceName string      = "p2pdao.libp2p-proxy"
)

var Log = logging.Logger("libp2p-proxy")

type ProxyService struct {
	ctx              context.Context
	host             host.Host
	http             *http.Server
	p2pHost          string
	sshServerAddress string
}

func NewProxyService(ctx context.Context, h host.Host, p2pHost string, sshServer string) *ProxyService {
	ps := &ProxyService{ctx, h, nil, p2pHost, sshServer}
	h.SetStreamHandler(ID, ps.Handler)
	h.SetStreamHandler(SSH_ID, ps.Ssh_Handler)
	return ps
}

// Close terminates this listener. It will no longer handle any
// incoming streams
func (p *ProxyService) Close() error {
	if s := p.http; s != nil {
		c, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		p.http = nil
		s.Shutdown(c)
	}
	return p.host.Close()
}

func (p *ProxyService) Wait(fn func() error) error {
	<-p.ctx.Done()
	defer p.Close()

	if fn != nil {
		if err := fn(); err != nil {
			return err
		}
	}
	return p.ctx.Err()
}

func (p *ProxyService) Handler(s network.Stream) {
	if err := s.Scope().SetService(ServiceName); err != nil {
		Log.Errorf("error attaching stream to service: %s", err)
		s.Reset()
		return
	}

	p.handler(NewBufReaderStream(s))
}

func (p *ProxyService) Ssh_Handler(stream network.Stream) {
	defer stream.Close()

	// Connect to the real SSH server
	sshConn, err := net.Dial("tcp", p.sshServerAddress)
	if err != nil {
		log.Printf("Failed to connect to SSH server: %v", err)
		return
	}
	defer sshConn.Close()

	// Bidirectional copy between libp2p stream and SSH server
	go io.Copy(sshConn, stream)
	io.Copy(stream, sshConn)
}

func (p *ProxyService) handler(bs *BufReaderStream) {
	defer bs.Close()

	b, err := bs.Reader.Peek(1)
	if err != nil {
		if err == io.EOF {
			return
		}
		Log.Errorf("read stream error: %s", err)
		bs.Reset()
		return
	}

	if IsSocks5(b[0]) {
		p.socks5Handler(bs)
	} else {
		p.httpHandler(bs)
	}
}

func (p *ProxyService) ServeHTTP(handler http.Handler, s *http.Server) error {
	if p.http != nil {
		return fmt.Errorf("http.Server exists")
	}
	if handler == nil {
		return fmt.Errorf("http handler is nil")
	}
	l, err := gostream.Listen(p.host, P2PHttpID)
	if err != nil {
		return err
	}

	if s == nil {
		s = new(http.Server)
		s.ReadHeaderTimeout = 20 * time.Second
		s.ReadTimeout = 60 * time.Second
		s.WriteTimeout = 120 * time.Second
		s.IdleTimeout = 90 * time.Second
	}
	s.Handler = handler
	p.http = s

	go p.Wait(nil)
	return s.Serve(l)
}

func (p *ProxyService) isP2PHttp(host string) bool {
	return strings.HasPrefix(host, p.p2pHost)
}

func shouldLogError(err error) bool {
	return err != nil && err != io.EOF &&
		err != io.ErrUnexpectedEOF && err != syscall.ECONNRESET &&
		!strings.Contains(err.Error(), "timeout") &&
		!strings.Contains(err.Error(), "reset") &&
		!strings.Contains(err.Error(), "closed")
}
