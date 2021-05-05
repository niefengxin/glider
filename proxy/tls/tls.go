package tls

import (
	stdtls "crypto/tls"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/nadoo/glider/log"
	"github.com/nadoo/glider/proxy"
)

// TLS struct.
type TLS struct {
	dialer proxy.Dialer
	proxy  proxy.Proxy
	addr   string

	config *stdtls.Config

	serverName string
	skipVerify bool

	certFile string
	keyFile  string

	server proxy.Server
}

func init() {
	proxy.RegisterDialer("tls", NewTLSDialer)
	proxy.RegisterServer("tls", NewTLSServer)
}

// NewTLS returns a tls struct.
func NewTLS(s string, d proxy.Dialer, p proxy.Proxy) (*TLS, error) {
	u, err := url.Parse(s)
	if err != nil {
		log.F("[tls] parse url err: %s", err)
		return nil, err
	}

	query := u.Query()
	t := &TLS{
		dialer:     d,
		proxy:      p,
		addr:       u.Host,
		serverName: query.Get("serverName"),
		skipVerify: query.Get("skipVerify") == "true",
		certFile:   query.Get("cert"),
		keyFile:    query.Get("key"),
	}

	if _, port, _ := net.SplitHostPort(t.addr); port == "" {
		t.addr = net.JoinHostPort(t.addr, "443")
	}

	if t.serverName == "" {
		t.serverName = t.addr[:strings.LastIndex(t.addr, ":")]
	}

	return t, nil
}

// NewTLSDialer returns a tls dialer.
func NewTLSDialer(s string, d proxy.Dialer) (proxy.Dialer, error) {
	p, err := NewTLS(s, d, nil)
	if err != nil {
		return nil, err
	}

	p.config = &stdtls.Config{
		ServerName:         p.serverName,
		InsecureSkipVerify: p.skipVerify,
		ClientSessionCache: stdtls.NewLRUClientSessionCache(64),
		MinVersion:         stdtls.VersionTLS12,
	}

	return p, err
}

// NewTLSServer returns a tls transport layer before the real server.
func NewTLSServer(s string, p proxy.Proxy) (proxy.Server, error) {
	server, chain := s, ""
	if idx := strings.IndexByte(s, ','); idx != -1 {
		server, chain = s[:idx], s[idx+1:]
	}

	t, err := NewTLS(server, nil, p)
	if err != nil {
		return nil, err
	}

	if t.certFile == "" || t.keyFile == "" {
		return nil, errors.New("[tls] cert and key file path must be spcified")
	}

	cert, err := stdtls.LoadX509KeyPair(t.certFile, t.keyFile)
	if err != nil {
		log.F("[tls] unable to load cert: %s, key %s", t.certFile, t.keyFile)
		return nil, err
	}

	t.config = &stdtls.Config{
		Certificates: []stdtls.Certificate{cert},
		MinVersion:   stdtls.VersionTLS12,
	}

	if chain != "" {
		t.server, err = proxy.ServerFromURL(chain, p)
		if err != nil {
			return nil, err
		}
	}

	return t, nil
}

// ListenAndServe listens on server's addr and serves connections.
func (s *TLS) ListenAndServe() {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		log.F("[tls] failed to listen on %s: %v", s.addr, err)
		return
	}
	defer l.Close()

	log.F("[tls] listening TCP on %s with TLS", s.addr)

	for {
		c, err := l.Accept()
		if err != nil {
			log.F("[tls] failed to accept: %v", err)
			continue
		}

		go s.Serve(c)
	}
}

// Serve serves a connection.
func (s *TLS) Serve(cc net.Conn) {
	c := stdtls.Server(cc, s.config)

	if s.server != nil {
		s.server.Serve(c)
		return
	}

	defer c.Close()

	rc, dialer, err := s.proxy.Dial("tcp", "")
	if err != nil {
		log.F("[tls] %s <-> %s via %s, error in dial: %v", c.RemoteAddr(), s.addr, dialer.Addr(), err)
		s.proxy.Record(dialer, false)
		return
	}
	defer rc.Close()

	log.F("[tls] %s <-> %s", c.RemoteAddr(), dialer.Addr())

	if err = proxy.Relay(c, rc); err != nil {
		log.F("[tls] %s <-> %s, relay error: %v", c.RemoteAddr(), dialer.Addr(), err)
		// record remote conn failure only
		if !strings.Contains(err.Error(), s.addr) {
			s.proxy.Record(dialer, false)
		}
	}
}

// Addr returns forwarder's address.
func (s *TLS) Addr() string {
	if s.addr == "" {
		return s.dialer.Addr()
	}
	return s.addr
}

// Dial connects to the address addr on the network net via the proxy.
func (s *TLS) Dial(network, addr string) (net.Conn, error) {
	cc, err := s.dialer.Dial("tcp", s.addr)
	if err != nil {
		log.F("[tls] dial to %s error: %s", s.addr, err)
		return nil, err
	}

	c := stdtls.Client(cc, s.config)
	err = c.Handshake()
	return c, err
}

// DialUDP connects to the given address via the proxy.
func (s *TLS) DialUDP(network, addr string) (net.PacketConn, net.Addr, error) {
	return nil, nil, proxy.ErrNotSupported
}
