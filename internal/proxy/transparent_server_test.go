package proxy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRouteTransparentResolverFailureReleasesSlot(t *testing.T) {
	const maxConnections = 4
	var attempts int
	s := &Server{connSlots: make(chan struct{}, maxConnections), resolver: func(net.Conn) (net.Addr, error) {
		attempts++
		return nil, net.ErrClosed
	}}
	q := newQueueListener()
	defer q.Close()
	for i := 0; i < maxConnections+3; i++ {
		a, b := net.Pipe()
		s.routeTransparent(a, q)
		_ = b.Close()
	}
	if attempts != maxConnections+3 {
		t.Fatalf("resolver attempts=%d, want %d", attempts, maxConnections+3)
	}

	upstreamClient, upstreamServer := net.Pipe()
	defer upstreamServer.Close()
	go func() { _, _ = io.ReadAll(upstreamServer) }()
	s.dial = func(string, string) (net.Conn, error) { return upstreamClient, nil }
	s.resolver = func(net.Conn) (net.Addr, error) {
		return &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 443}, nil
	}
	ingress, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		s.routeTransparent(ingress, q)
		close(done)
	}()
	_, _ = client.Write([]byte("not tls"))
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recovered connection did not complete")
	}
	select {
	case s.connSlots <- struct{}{}:
	default:
		t.Fatal("connection slot leaked after resolver failures/recovery")
	}
}

func writeTestLeaf(t *testing.T, dir, host string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	tpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: host}, DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, _ := x509.MarshalECPrivateKey(key)
	b := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	b = append(b, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})...)
	if err := os.WriteFile(filepath.Join(dir, host+".pem"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRouteTransparentNonTargetPreservesPrefixAndPayload(t *testing.T) {
	ingress, client := net.Pipe()
	defer client.Close()
	defer ingress.Close()
	upstreamClient, upstreamServer := net.Pipe()
	defer upstreamServer.Close()
	defer upstreamClient.Close()
	queued := newQueueListener()
	s := &Server{allowSNI: map[string]bool{"gsp-ssl.ls.apple.com": true}, connSlots: make(chan struct{}, 1), dial: func(string, string) (net.Conn, error) { return upstreamClient, nil }}
	s.resolver = func(net.Conn) (net.Addr, error) { return &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 443}, nil }
	upstreamRead := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(upstreamServer)
		upstreamRead <- b
	}()
	go s.routeTransparent(ingress, queued)
	prefix := []byte{22, 3, 3, 0, 5, 'h', 'e', 'l', 'l', 'o'}
	payload := []byte("after-client-hello")
	if _, err := client.Write(append(prefix, payload...)); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	got := <-upstreamRead
	if !bytes.Equal(got, append(prefix, payload...)) {
		t.Fatalf("upstream bytes changed: got %x want %x", got, append(prefix, payload...))
	}
}

func TestRouteTransparentAllowedTLSUsesPrefixedServer(t *testing.T) {
	dir := t.TempDir()
	host := "gsp-ssl.ls.apple.com"
	writeTestLeaf(t, dir, host)
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, host+".pem"), filepath.Join(dir, host+".pem"))
	if err != nil {
		t.Fatal(err)
	}
	ingress, client := net.Pipe()
	defer client.Close()
	defer ingress.Close()
	queued := newQueueListener()
	s := &Server{allowSNI: map[string]bool{host: true}, connSlots: make(chan struct{}, 1), tlsConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	s.resolver = func(net.Conn) (net.Addr, error) { return &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 443}, nil }
	go s.routeTransparent(ingress, queued)
	clientTLS := tls.Client(client, &tls.Config{ServerName: host, InsecureSkipVerify: true})
	clientDone := make(chan error, 1)
	go func() { clientDone <- clientTLS.Handshake() }()
	serverConn, err := queued.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()
	serverTLS := serverConn.(*tls.Conn)
	serverDone := make(chan error, 1)
	go func() { serverDone <- serverTLS.Handshake() }()
	if err := <-clientDone; err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
}

func TestRelayPrefixedUsesInjectedDialer(t *testing.T) {
	ingress, client := net.Pipe()
	defer client.Close()
	defer ingress.Close()
	upstreamClient, upstreamServer := net.Pipe()
	defer upstreamServer.Close()
	s := &Server{dial: func(string, string) (net.Conn, error) { return upstreamClient, nil }}
	done := make(chan error, 1)
	go func() {
		done <- s.relayPrefixed(ingress, &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 443}, []byte("prefix"))
	}()
	got := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(upstreamServer); got <- b }()
	if _, err := client.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	if b := <-got; string(b) != "prefixpayload" {
		t.Fatalf("got %q", b)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRelayPrefixedDialerForFreeFunction(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan []byte, 1)
	go func() {
		c, e := ln.Accept()
		if e != nil {
			return
		}
		b, _ := io.ReadAll(c)
		accepted <- b
		_ = c.Close()
	}()
	client, ingress := net.Pipe()
	defer client.Close()
	defer ingress.Close()
	done := make(chan error, 1)
	go func() { done <- relayPrefixed(ingress, ln.Addr(), []byte("prefix")) }()
	_, _ = client.Write([]byte("payload"))
	_ = client.Close()
	if got := <-accepted; string(got) != "prefixpayload" {
		t.Fatalf("got %q", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSetOriginalDestinationDialer(t *testing.T) {
	s := &Server{}
	s.SetOriginalDestinationDialer(func(string, string) (net.Conn, error) { return nil, net.ErrClosed })
	if s.dial == nil {
		t.Fatal("dialer was not installed")
	}
}

func TestReadAndClassifyFragmentedClientHello(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		c := tls.Client(client, &tls.Config{ServerName: "gsp-ssl.ls.apple.com", InsecureSkipVerify: true})
		_ = c.Handshake()
	}()
	server.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf, decision := readAndClassify(server, &net.TCPAddr{IP: net.ParseIP("17.0.0.1"), Port: 443}, map[string]bool{"gsp-ssl.ls.apple.com": true})
	if decision != MITM || len(buf) < 5 {
		t.Fatalf("decision=%s bytes=%d", decision, len(buf))
	}
}

func TestReadAndClassifyNonTargetPassthrough(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		c := tls.Client(client, &tls.Config{ServerName: "example.com", InsecureSkipVerify: true})
		_ = c.Handshake()
	}()
	server.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, decision := readAndClassify(server, &net.TCPAddr{IP: net.ParseIP("17.0.0.1"), Port: 443}, map[string]bool{"gsp-ssl.ls.apple.com": true})
	if decision != Passthrough {
		t.Fatalf("decision=%s", decision)
	}
}

func TestListenDual(t *testing.T) {
	lns, err := listenDual("0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, ln := range lns {
			_ = ln.Close()
		}
	}()
	if len(lns) != 2 {
		t.Fatalf("listeners=%d", len(lns))
	}
	if lns[0].Addr().(*net.TCPAddr).IP.To4() == nil || lns[1].Addr().(*net.TCPAddr).IP.To4() != nil {
		t.Fatalf("unexpected addresses: %v %v", lns[0].Addr(), lns[1].Addr())
	}
	if lns[0].Addr().(*net.TCPAddr).Port != lns[1].Addr().(*net.TCPAddr).Port {
		t.Fatal("listeners use different ports")
	}
}

func TestServerReadyAndShutdownDual(t *testing.T) {
	dir := t.TempDir()
	host := "gsp-ssl.ls.apple.com"
	writeTestLeaf(t, dir, host)
	s, err := NewServerWithLeaves("0.0.0.0:0", map[string]bool{host: true}, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	s.SetOriginalDestinationResolver(func(net.Conn) (net.Addr, error) { return &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 443}, nil })
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.ListenAndServe() }()
	select {
	case err := <-s.Ready():
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("not ready")
	}
	s.mu.Lock()
	count := len(s.lns)
	s.mu.Unlock()
	if count != 2 {
		t.Fatalf("listeners=%d", count)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = s.Shutdown(ctx)
	select {
	case <-serveErr:
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not stop")
	}
}
