package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSOCKS5DomainDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, _ := ln.Accept()
		defer c.Close()
		buf := make([]byte, 3)
		_, _ = io.ReadFull(c, buf)
		_, _ = c.Write([]byte{5, 0})
		h := make([]byte, 5)
		_, _ = io.ReadFull(c, h)
		name := make([]byte, int(h[4]))
		_, _ = io.ReadFull(c, name)
		var port [2]byte
		_, _ = io.ReadFull(c, port[:])
		got <- string(name)
		_, _ = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
	}()
	dial, err := newSOCKS5Dialer(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c, err := dial(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	select {
	case name := <-got:
		if name != "example.com" {
			t.Fatal(name)
		}
	case <-time.After(time.Second):
		t.Fatal("no request")
	}
}

func TestSOCKS5RejectsInvalidPort(t *testing.T) {
	if _, err := newSOCKS5Dialer("127.0.0.1:0"); err == nil {
		t.Fatal("accepted port 0")
	}
}

func TestSetUpstreamSocks5ConfiguresTransport(t *testing.T) {
	s := &Server{transport: &http.Transport{}}
	if err := s.SetUpstreamSocks5("127.0.0.1:1080"); err != nil {
		t.Fatal(err)
	}
	if s.transport.DialContext == nil {
		t.Fatal("SOCKS5 dialer was not installed")
	}
	if s.transport.TLSHandshakeTimeout <= 0 || s.transport.ResponseHeaderTimeout <= 0 || s.transport.ExpectContinueTimeout <= 0 || s.transport.IdleConnTimeout <= 0 {
		t.Fatalf("SOCKS5 transport has unbounded phase timeout: %+v", s.transport)
	}
	if err := s.SetUpstreamSocks5("127.0.0.1:0"); err == nil {
		t.Fatal("accepted invalid SOCKS5 port")
	}
}

func TestSOCKS5HandshakeUsesContextDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, e := ln.Accept()
		if e == nil {
			defer c.Close()
			// Never complete the greeting: cancellation must interrupt the blocked
			// SOCKS5 read without waiting for the 10s fallback.
			_, _ = io.ReadFull(c, make([]byte, 4))
		}
	}()
	dial, err := newSOCKS5Dialer(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	if c, err := dial(ctx, "tcp", "example.com:443"); err == nil {
		_ = c.Close()
		t.Fatal("SOCKS5 handshake unexpectedly succeeded")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("SOCKS5 cancellation took %s", elapsed)
	}
}

func TestSOCKS5IPv4AndIPv6AddressTypes(t *testing.T) {
	for _, target := range []string{"192.0.2.1:443", "[2001:db8::1]:443"} {
		t.Run(target, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			seen := make(chan byte, 1)
			go func() {
				c, e := ln.Accept()
				if e != nil {
					return
				}
				defer c.Close()
				greeting := make([]byte, 3)
				_, _ = io.ReadFull(c, greeting)
				_, _ = c.Write([]byte{5, 0})
				header := make([]byte, 4)
				_, _ = io.ReadFull(c, header)
				seen <- header[3]
				var n int
				switch header[3] {
				case 1:
					n = 4
				case 4:
					n = 16
				}
				buf := make([]byte, n+2)
				_, _ = io.ReadFull(c, buf)
				_, _ = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
			}()
			dial, err := newSOCKS5Dialer(ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			c, err := dial(context.Background(), "tcp", target)
			if err != nil {
				t.Fatal(err)
			}
			_ = c.Close()
			want := byte(1)
			if strings.HasPrefix(target, "[") {
				want = 4
			}
			if got := <-seen; got != want {
				t.Fatalf("ATYP=%d want %d", got, want)
			}
		})
	}
}

func TestSOCKS5RejectAndContextCancel(t *testing.T) {
	server, client := net.Pipe()
	go func() {
		buf := make([]byte, 3)
		_, _ = io.ReadFull(server, buf)
		_, _ = server.Write([]byte{5, 0})
		request := make([]byte, 10)
		_, _ = io.ReadFull(server, request)
		_, _ = server.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
	}()
	if err := socks5Connect(client, "192.0.2.1:443"); err == nil {
		t.Fatal("accepted SOCKS5 rejection")
	}
	_ = server.Close()
	_ = client.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, _ := ln.Accept()
		if c != nil {
			defer c.Close()
			time.Sleep(time.Second)
		}
	}()
	dial, err := newSOCKS5Dialer(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := dial(ctx, "tcp", "example.com:443"); err == nil {
		t.Fatal("expected context timeout")
	}
	if time.Since(started) > time.Second {
		t.Fatal("SOCKS5 cancellation exceeded deadline")
	}
}
