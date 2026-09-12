package proxy

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const maxClientHello = 64 << 10

type queueListener struct {
	conns chan net.Conn
	done  chan struct{}
}

func newQueueListener() *queueListener {
	return &queueListener{conns: make(chan net.Conn), done: make(chan struct{})}
}
func (l *queueListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		if c == nil {
			return nil, net.ErrClosed
		}
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *queueListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}
func (l *queueListener) Addr() net.Addr { return transparentAddr("transparent") }

type transparentAddr string

func (a transparentAddr) Network() string { return string(a) }
func (a transparentAddr) String() string  { return string(a) }

type prefixedConn struct {
	net.Conn
	reader io.Reader
}

func (c *prefixedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (s *Server) serveTransparent(raw ...net.Listener) error {
	ql := newQueueListener()
	defer ql.Close()
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.http.Serve(ql) }()
	acceptErr := make(chan error, len(raw))
	for _, ln := range raw {
		go func(l net.Listener) {
			for {
				conn, err := l.Accept()
				if err != nil {
					acceptErr <- err
					return
				}
				go s.routeTransparent(conn, ql)
			}
		}(ln)
	}
	select {
	case err := <-acceptErr:
		for _, ln := range raw {
			_ = ln.Close()
		}
		_ = s.http.Close()
		if s.stopping.Load() {
			return http.ErrServerClosed
		}
		return err
	case err := <-serveErr:
		for _, ln := range raw {
			_ = ln.Close()
		}
		if s.stopping.Load() && err != nil {
			return http.ErrServerClosed
		}
		return err
	}
}

func (s *Server) routeTransparent(conn net.Conn, ql *queueListener) {
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			<-s.connSlots
		})
	}
	select {
	case s.connSlots <- struct{}{}:
	default:
		_ = conn.Close()
		return
	}
	dst, err := s.resolver(conn)
	if err != nil {
		// Without the pre-NAT address there is no safe upstream target. Close
		// rather than guessing; callers can install a resolver that falls back
		// to an explicit policy destination when transparent mode is unavailable.
		_ = conn.Close()
		release()
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf, decision := readAndClassify(conn, dst, s.allowSNI)
	_ = conn.SetReadDeadline(time.Time{})
	if decision == MITM {
		tlsConn := tlsServerWithPrefix(conn, buf, s.tlsConfig)
		s.connReleases.Store(tlsConn, release)
		select {
		case ql.conns <- tlsConn:
		case <-ql.done:
			_ = tlsConn.Close()
			if fn, ok := s.connReleases.LoadAndDelete(tlsConn); ok {
				fn.(func())()
			}
		}
		return
	}
	defer release()
	_ = s.relayPrefixed(conn, dst, buf)
}

func readAndClassify(conn net.Conn, dst net.Addr, allow map[string]bool) ([]byte, Decision) {
	var buf []byte
	chunk := make([]byte, 2048)
	port := 0
	if tcp, ok := dst.(*net.TCPAddr); ok {
		port = tcp.Port
	}
	for len(buf) < maxClientHello {
		n, err := conn.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		ip := net.IP(nil)
		if tcp, ok := dst.(*net.TCPAddr); ok {
			ip = tcp.IP
		}
		result := Classify(buf, ip, port, Policy{MaxHello: maxClientHello, AllowSNI: allow, AllowDestination: map[string]bool{"*": true}})
		if result.Decision == MITM {
			return buf, MITM
		}
		if len(buf) >= 5 && buf[0] == 22 && ((int(buf[3])<<8)|int(buf[4]))+5 <= len(buf) {
			return buf, Passthrough
		}
		if len(buf) >= maxClientHello || err != nil {
			return buf, Passthrough
		}
		if len(buf) >= 5 && buf[0] != 22 {
			return buf, Passthrough
		}
	}
	return buf, Passthrough
}

func tlsServerWithPrefix(conn net.Conn, prefix []byte, cfg *tls.Config) net.Conn {
	return tls.Server(&prefixedConn{Conn: conn, reader: io.MultiReader(bytes.NewReader(prefix), conn)}, cfg)
}

func relayPrefixed(conn net.Conn, dst net.Addr, prefix []byte) error {
	upstream, err := net.DialTimeout(dst.Network(), dst.String(), 10*time.Second)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer upstream.Close()
	defer conn.Close()
	if _, err := upstream.Write(prefix); err != nil {
		return err
	}
	result := make(chan error, 2)
	go func() { _, e := io.Copy(upstream, conn); result <- e }()
	go func() { _, e := io.Copy(conn, upstream); result <- e }()
	return <-result
}

func (s *Server) relayPrefixed(conn net.Conn, dst net.Addr, prefix []byte) error {
	if s.dial == nil {
		return relayPrefixed(conn, dst, prefix)
	}
	upstream, err := s.dial(dst.Network(), dst.String())
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer upstream.Close()
	defer conn.Close()
	if _, err := upstream.Write(prefix); err != nil {
		return err
	}
	result := make(chan error, 2)
	go func() { _, e := io.Copy(upstream, conn); result <- e }()
	go func() { _, e := io.Copy(conn, upstream); result <- e }()
	return <-result
}

func (s *Server) SetOriginalDestinationResolver(fn func(net.Conn) (net.Addr, error)) {
	if fn != nil {
		s.resolver = fn
	}
}

func (s *Server) SetOriginalDestinationDialer(fn func(string, string) (net.Conn, error)) {
	if fn != nil {
		s.dial = fn
	}
}
