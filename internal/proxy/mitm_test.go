package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/chhbzhou/ils-gateway/internal/health"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMITMHandleRewriteAndLargePassthrough(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "large") {
			_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
			return
		}
		if strings.Contains(r.URL.Path, "corrupt") {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write([]byte("not-gzip"))
			return
		}
		if strings.Contains(r.URL.Path, "unknown") {
			w.Header().Set("Content-Encoding", "br")
			_, _ = w.Write([]byte("brotli-bytes"))
			return
		}
		if strings.Contains(r.URL.Path, "oversized-gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			_, _ = gz.Write([]byte(strings.Repeat("z", 2048)))
			_ = gz.Close()
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("ETag", "stale")
		w.Header().Set("Content-MD5", "stale")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte("compressed"))
		_ = gz.Close()
	}))
	defer upstream.Close()
	s := &Server{allowSNI: map[string]bool{"gsp-ssl.ls.apple.com": true}, rewrite: func(b []byte) ([]byte, bool) { return append(b, '!'), true }, bodyLimit: 1024, workSlots: make(chan struct{}, 4)}
	s.transport = upstream.Client().Transport.(*http.Transport).Clone()
	s.transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	s.transport.DisableCompression = true
	s.transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	for _, path := range []string{"/clls/wloc/large", "/clls/wloc", "/clls/wloc/corrupt", "/clls/wloc/unknown", "/clls/wloc/oversized-gzip"} {
		r := httptest.NewRequest("GET", "https://gsp-ssl.ls.apple.com"+path, nil)
		r.Host = "gsp-ssl.ls.apple.com"
		r.TLS = &tls.ConnectionState{ServerName: "gsp-ssl.ls.apple.com"}
		w := httptest.NewRecorder()
		s.handle(w, r)
		if path == "/clls/wloc/large" {
			if w.Body.Len() != 2048 {
				t.Fatalf("large body=%d", w.Body.Len())
			}
		} else if path == "/clls/wloc" {
			if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("ETag") != "" || w.Header().Get("Content-MD5") != "" {
				t.Fatal("gzip rewrite headers were not updated")
			}
			gz, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			decoded, _ := io.ReadAll(gz)
			_ = gz.Close()
			if string(decoded) != "compressed!" {
				t.Fatalf("gzip body=%q", decoded)
			}
		} else if path == "/clls/wloc/corrupt" || path == "/clls/wloc/unknown" || path == "/clls/wloc/oversized-gzip" {
			if w.Header().Get("Content-Encoding") == "" || w.Body.Len() == 0 {
				t.Fatal("failed compressed response was not preserved")
			}
			if path == "/clls/wloc/corrupt" && string(w.Body.Bytes()) != "not-gzip" {
				t.Fatal("corrupt gzip body was changed")
			}
			if path == "/clls/wloc/unknown" && string(w.Body.Bytes()) != "brotli-bytes" {
				t.Fatal("unknown encoding body was changed")
			}
		}
	}
}

func TestEnrollmentFingerprintAndErrors(t *testing.T) {
	f := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(f, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/fingerprint", nil)
	w := httptest.NewRecorder()
	EnrollmentHandler(f).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d", w.Code)
	}
	dir := t.TempDir()
	host := "gsp-ssl.ls.apple.com"
	writeTestLeaf(t, dir, host)
	b, _ := os.ReadFile(filepath.Join(dir, host+".pem"))
	block, _ := pem.Decode(b)
	ca := filepath.Join(dir, "ca.pem")
	_ = os.WriteFile(ca, pem.EncodeToMemory(block), 0600)
	signedProfile := []byte("signed-mobileconfig")
	_ = os.WriteFile(filepath.Join(dir, "ca.mobileconfig"), signedProfile, 0600)
	w = httptest.NewRecorder()
	EnrollmentHandler(ca).ServeHTTP(w, httptest.NewRequest("GET", "/fingerprint", nil))
	cert, _ := x509.ParseCertificate(block.Bytes)
	sum := sha256.Sum256(cert.Raw)
	if strings.TrimSpace(w.Body.String()) != hex.EncodeToString(sum[:]) && strings.TrimSpace(w.Body.String()) != strings.ToUpper(hex.EncodeToString(sum[:])) {
		t.Fatal("fingerprint mismatch")
	}
	w = httptest.NewRecorder()
	EnrollmentHandler(ca).ServeHTTP(w, httptest.NewRequest("GET", "/ca.pem", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Disposition") == "" || w.Body.Len() == 0 {
		t.Fatalf("CA download failed: code=%d headers=%v", w.Code, w.Header())
	}
	w = httptest.NewRecorder()
	EnrollmentHandler(ca).ServeHTTP(w, httptest.NewRequest("GET", "/ca.cer", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/x-x509-ca-cert" || !bytes.Equal(w.Body.Bytes(), cert.Raw) {
		t.Fatalf("CER download failed: code=%d headers=%v", w.Code, w.Header())
	}
	w = httptest.NewRecorder()
	EnrollmentHandler(ca).ServeHTTP(w, httptest.NewRequest("GET", "/ca.mobileconfig", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/x-apple-aspen-config" ||
		!bytes.Equal(w.Body.Bytes(), signedProfile) {
		t.Fatalf("mobileconfig download failed: code=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}
	_ = os.Remove(filepath.Join(dir, "ca.mobileconfig"))
	w = httptest.NewRecorder()
	EnrollmentHandler(ca).ServeHTTP(w, httptest.NewRequest("GET", "/ca.mobileconfig", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing mobileconfig status = %d", w.Code)
	}
	w = httptest.NewRecorder()
	EnrollmentHandler(ca).ServeHTTP(w, httptest.NewRequest("GET", "/other", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unexpected unknown endpoint status: %d", w.Code)
	}
}

func TestMITMHostSNIMismatch(t *testing.T) {
	s := &Server{allowSNI: map[string]bool{"gsp-ssl.ls.apple.com": true}, workSlots: make(chan struct{}, 1)}
	r := httptest.NewRequest("GET", "https://example.com/", nil)
	r.Host = "example.com"
	r.TLS = &tls.ConnectionState{ServerName: "gsp-ssl.ls.apple.com"}
	w := httptest.NewRecorder()
	s.handle(w, r)
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("code=%d", w.Code)
	}
}

func TestByteBudgetBlocksAndReleases(t *testing.T) {
	b := newByteBudget(8)
	if !b.acquire(8) {
		t.Fatal("initial budget acquire failed")
	}
	done := make(chan struct{})
	go func() { b.acquire(8); close(done) }()
	select {
	case <-done:
		t.Fatal("second acquire should wait")
	case <-time.After(20 * time.Millisecond):
	}
	b.release(8)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("budget was not released")
	}
	b.release(8)
}

func TestReadBoundedReturnsPrefixWithoutGrowing(t *testing.T) {
	body := bytes.Repeat([]byte{'x'}, 4097)
	got, err := readBounded(bytes.NewReader(body), 1024)
	if !errors.Is(err, errBodyLimit) {
		t.Fatalf("expected body limit, got %v", err)
	}
	if len(got) != 1025 || !bytes.Equal(got, body[:1025]) {
		t.Fatalf("unexpected bounded prefix: len=%d", len(got))
	}
	if _, err := readBounded(bytes.NewReader([]byte("ok")), 2); err != nil {
		t.Fatalf("exact body should fit: %v", err)
	}
}

func TestGzipBoundedOutputRejectsExpansion(t *testing.T) {
	if out, ok := gzipBounded(bytes.Repeat([]byte{'x'}, 4096), 8); ok || out != nil {
		t.Fatal("gzip output exceeding budget was accepted")
	}
	out, ok := gzipBounded([]byte("small"), 128)
	if !ok || len(out) > 128 {
		t.Fatalf("small gzip output rejected or exceeded budget: ok=%v len=%d", ok, len(out))
	}
}

func TestByteBudgetConcurrentReservationsDoNotLeak(t *testing.T) {
	b := newByteBudget(32)
	const workers = 64
	done := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		go func() {
			if b.acquire(8) {
				time.Sleep(time.Millisecond)
				b.release(8)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < workers; i++ {
		<-done
	}
	if !b.acquire(32) {
		t.Fatal("budget did not return to full capacity")
	}
	b.release(32)
}

func TestObserverCountsOnlyCommittedRewrite(t *testing.T) {
	s := &Server{allowSNI: map[string]bool{"gsp-ssl.ls.apple.com": true}, rewrite: func(b []byte) ([]byte, bool) { return append(append([]byte(nil), b...), '!'), true }, bodyLimit: 1024, workSlots: make(chan struct{}, 1)}
	h := health.New()
	s.SetObserver(h)
	s.transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableCompression: true, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) { return nil, fmt.Errorf("unused") }}
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("body")) }))
	defer up.Close()
	s.transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, up.Listener.Addr().String())
	}
	r := httptest.NewRequest("GET", "https://gsp-ssl.ls.apple.com/clls/wloc", nil)
	r.Host = "gsp-ssl.ls.apple.com"
	r.TLS = &tls.ConnectionState{ServerName: "gsp-ssl.ls.apple.com"}
	r.RemoteAddr = "192.0.2.25:49152"
	w := httptest.NewRecorder()
	s.handle(w, r)
	m := h.Snapshot()
	if m.TargetRequests != 1 || m.ResponseModified != 1 || m.ResponsePassthrough != 0 {
		t.Fatalf("metrics=%+v", m)
	}
	if m.LastModifiedByIP["192.0.2.25"] == "" {
		t.Fatalf("missing client modification time: %+v", m)
	}
}

func TestUpstreamResponseHeaderTimeoutReleasesSlots(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Keep the handler from producing response headers.  The transport's
		// bounded ResponseHeaderTimeout must terminate this request quickly.
		<-r.Context().Done()
	}))
	defer up.Close()
	h := health.New()
	s := &Server{
		allowSNI:  map[string]bool{"gsp-ssl.ls.apple.com": true},
		bodyLimit: 1024,
		workSlots: make(chan struct{}, 1),
	}
	s.SetObserver(h)
	s.transport = up.Client().Transport.(*http.Transport).Clone()
	s.transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	s.transport.DisableCompression = true
	s.transport.ResponseHeaderTimeout = 40 * time.Millisecond
	s.transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, up.Listener.Addr().String())
	}
	r := httptest.NewRequest("GET", "https://gsp-ssl.ls.apple.com/clls/wloc", nil)
	r.Host = "gsp-ssl.ls.apple.com"
	r.TLS = &tls.ConnectionState{ServerName: "gsp-ssl.ls.apple.com"}
	start := time.Now()
	w := httptest.NewRecorder()
	s.handle(w, r)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("upstream timeout took %s", elapsed)
	}
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := h.Snapshot().UpstreamFailed; got != 1 {
		t.Fatalf("upstream_failed=%d", got)
	}
	// A second request must not be rejected as busy: the work slot was
	// released on the timeout path.
	w = httptest.NewRecorder()
	s.handle(w, r)
	if w.Code == http.StatusServiceUnavailable && strings.Contains(w.Body.String(), "proxy busy") {
		t.Fatal("work slot leaked after upstream timeout")
	}
}

func TestUpstreamTLSHandshakeTimeoutIsBounded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, e := ln.Accept()
		if e == nil {
			// Consume the ClientHello but never send a ServerHello. The client
			// transport must close the connection at its TLS deadline.
			_, _ = io.Copy(io.Discard, c)
			_ = c.Close()
		}
	}()
	h := health.New()
	s := &Server{
		allowSNI:  map[string]bool{"gsp-ssl.ls.apple.com": true},
		bodyLimit: 1024,
		workSlots: make(chan struct{}, 1),
	}
	s.SetObserver(h)
	s.transport = &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
		DisableCompression:    true,
		TLSHandshakeTimeout:   40 * time.Millisecond,
		ResponseHeaderTimeout: 100 * time.Millisecond,
		ExpectContinueTimeout: 40 * time.Millisecond,
		IdleConnTimeout:       100 * time.Millisecond,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, ln.Addr().String())
		},
	}
	r := httptest.NewRequest("GET", "https://gsp-ssl.ls.apple.com/clls/wloc", nil)
	r.Host = "gsp-ssl.ls.apple.com"
	r.TLS = &tls.ConnectionState{ServerName: "gsp-ssl.ls.apple.com"}
	start := time.Now()
	w := httptest.NewRecorder()
	s.handle(w, r)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("TLS handshake timeout took %s", elapsed)
	}
	if w.Code != http.StatusBadGateway || h.Snapshot().UpstreamFailed != 1 {
		t.Fatalf("TLS timeout status/metrics: status=%d metrics=%+v", w.Code, h.Snapshot())
	}
}

func TestGzipMetricsHashDecodedPayloadAndClearError(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte("decoded-location"))
		_ = gz.Close()
	}))
	defer up.Close()
	h := health.New()
	h.SetLastError("stale failure")
	s := &Server{
		allowSNI:  map[string]bool{"gsp-ssl.ls.apple.com": true},
		bodyLimit: 1024,
		workSlots: make(chan struct{}, 1),
		rewriteDetailed: func(in []byte) ([]byte, RewriteMetadata, error) {
			return append(append([]byte(nil), in...), '!'), RewriteMetadata{Envelope: "arpc", Locations: 1}, nil
		},
	}
	s.SetObserver(h)
	s.transport = up.Client().Transport.(*http.Transport).Clone()
	s.transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	s.transport.DisableCompression = true
	s.transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, up.Listener.Addr().String())
	}
	r := httptest.NewRequest("GET", "https://gsp-ssl.ls.apple.com/clls/wloc", nil)
	r.Host = "gsp-ssl.ls.apple.com"
	r.TLS = &tls.ConnectionState{ServerName: "gsp-ssl.ls.apple.com"}
	w := httptest.NewRecorder()
	s.handle(w, r)
	m := h.Snapshot()
	in := sha256.Sum256([]byte("decoded-location"))
	out := sha256.Sum256([]byte("decoded-location!"))
	if m.ResponseModified != 1 || m.ResponsePassthrough != 0 {
		t.Fatalf("unexpected response metrics: %+v", m)
	}
	if m.LastInputSHA256 != hex.EncodeToString(in[:]) || m.LastOutputSHA256 != hex.EncodeToString(out[:]) {
		t.Fatalf("gzip hashes must use decoded payloads: %+v", m)
	}
	if m.LastError != "" {
		t.Fatalf("successful rewrite retained stale last_error: %q", m.LastError)
	}
}

func TestDetailedRewriteUnsupportedIsCountedOnce(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte("unsupported"))
		_ = gz.Close()
	}))
	defer up.Close()
	h := health.New()
	s := &Server{
		allowSNI:  map[string]bool{"gsp-ssl.ls.apple.com": true},
		bodyLimit: 1024,
		workSlots: make(chan struct{}, 1),
		rewriteDetailed: func([]byte) ([]byte, RewriteMetadata, error) {
			return nil, RewriteMetadata{Unsupported: true}, errors.New("unsupported envelope")
		},
	}
	s.SetObserver(h)
	s.transport = up.Client().Transport.(*http.Transport).Clone()
	s.transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	s.transport.DisableCompression = true
	s.transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, up.Listener.Addr().String())
	}
	r := httptest.NewRequest("GET", "https://gsp-ssl.ls.apple.com/clls/wloc", nil)
	r.Host = "gsp-ssl.ls.apple.com"
	r.TLS = &tls.ConnectionState{ServerName: "gsp-ssl.ls.apple.com"}
	w := httptest.NewRecorder()
	s.handle(w, r)
	m := h.Snapshot()
	if m.RewriteUnsupported != 1 || m.RewriteFailed != 0 || m.ResponseModified != 0 || m.ResponsePassthrough != 1 {
		t.Fatalf("unexpected unsupported metrics: %+v", m)
	}
}

type shortResponseWriter struct {
	header http.Header
	status int
}

func (w *shortResponseWriter) Header() http.Header    { return w.header }
func (w *shortResponseWriter) WriteHeader(status int) { w.status = status }
func (w *shortResponseWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

func TestResponseModifiedRequiresCompleteWrite(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("body")) }))
	defer up.Close()
	h := health.New()
	h.SetLastError("stale failure")
	s := &Server{
		allowSNI:  map[string]bool{"gsp-ssl.ls.apple.com": true},
		bodyLimit: 1024,
		workSlots: make(chan struct{}, 1),
		rewrite:   func(in []byte) ([]byte, bool) { return append(append([]byte(nil), in...), '!'), true },
	}
	s.SetObserver(h)
	s.transport = up.Client().Transport.(*http.Transport).Clone()
	s.transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	s.transport.DisableCompression = true
	s.transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, up.Listener.Addr().String())
	}
	r := httptest.NewRequest("GET", "https://gsp-ssl.ls.apple.com/clls/wloc", nil)
	r.Host = "gsp-ssl.ls.apple.com"
	r.TLS = &tls.ConnectionState{ServerName: "gsp-ssl.ls.apple.com"}
	w := &shortResponseWriter{header: make(http.Header)}
	s.handle(w, r)
	m := h.Snapshot()
	if m.ResponseModified != 0 || m.ResponsePassthrough != 1 {
		t.Fatalf("partial write must be passthrough: %+v", m)
	}
	if m.LastError != "stale failure" {
		t.Fatalf("partial write must not clear last_error: %q", m.LastError)
	}
}

func TestTransparentMITMEndToEndRewritesWLocBody(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "gsp-ssl.ls.apple.com" {
			http.Error(w, "bad host", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("original-location"))
	}))
	defer upstream.Close()
	dir := t.TempDir()
	host := "gsp-ssl.ls.apple.com"
	writeTestLeaf(t, dir, host)
	s, err := NewServerWithLeaves("0.0.0.0:0", map[string]bool{host: true}, func(b []byte) ([]byte, bool) {
		return bytes.ReplaceAll(b, []byte("original"), []byte("rewritten")), true
	}, dir)
	if err != nil {
		t.Fatal(err)
	}
	s.transport = upstream.Client().Transport.(*http.Transport).Clone()
	s.transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
	s.transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	s.SetOriginalDestinationResolver(func(net.Conn) (net.Addr, error) {
		return &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 443}, nil
	})
	raw := newQueueListener()
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.serveTransparent(raw) }()
	ingress, client := net.Pipe()
	raw.conns <- ingress
	tlsClient := tls.Client(client, &tls.Config{ServerName: host, InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}})
	if err := tlsClient.Handshake(); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(tlsClient, "GET /clls/wloc HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(bufio.NewReader(tlsClient))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response, []byte("rewritten-location")) || bytes.Contains(response, []byte("original-location")) {
		t.Fatalf("unexpected response: %s", response)
	}
	_ = client.Close()
	_ = raw.Close()
	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("transparent server did not stop")
	}
}
