package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxProxyBody = 4 << 20

// Upstream timeouts are deliberately bounded.  A transparent gateway must
// fail open on a stalled upstream instead of retaining a connection slot and
// its body-budget reservation indefinitely.
const (
	upstreamDialTimeout           = 10 * time.Second
	upstreamTLSHandshakeTimeout   = 10 * time.Second
	upstreamResponseHeaderTimeout = 15 * time.Second
	upstreamExpectContinueTimeout = 1 * time.Second
	upstreamIdleConnTimeout       = 30 * time.Second
)

var errBodyLimit = errors.New("body exceeds configured limit")

// Server is the first usable data-plane implementation. It terminates TLS
// only for an explicit SNI allowlist and forwards requests to the same HTTPS
// host. The CA is injectable so production can replace the ephemeral CA with
// the persisted, root-owned CA helper described by the design.
type Server struct {
	addr            string
	allowSNI        map[string]bool
	transport       *http.Transport
	tlsConfig       *tls.Config
	http            *http.Server
	lns             []net.Listener
	rewrite         func([]byte) ([]byte, bool)
	rewriteDetailed func([]byte) ([]byte, RewriteMetadata, error)
	observer        Observer
	ready           chan error
	bodyLimit       int64
	workSlots       chan struct{}
	connSlots       chan struct{}
	connReleases    sync.Map
	stopping        atomic.Bool
	mu              sync.Mutex
	resolver        func(net.Conn) (net.Addr, error)
	dial            func(string, string) (net.Conn, error)
	budget          *byteBudget
	budgetMu        sync.Mutex
}

// RewriteMetadata is returned by the production WLoc decoder for evidence.
type RewriteMetadata struct {
	Envelope    string
	Locations   int
	Unsupported bool
}

// Observer is intentionally small so the proxy does not depend on the control protocol.
type Observer interface {
	RecordTargetRequest()
	RecordResponseModified(string)
	RecordResponsePassthrough(string)
	RecordRewriteUnsupported()
	RecordRewriteFailed()
	RecordGzipDecodeFailed()
	RecordGzipEncodeFailed()
	RecordUpstreamFailed()
	RecordRewriteCandidate(string, int, []byte, []byte, string)
}

func requestClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	if net.ParseIP(r.RemoteAddr) != nil {
		return r.RemoteAddr
	}
	return ""
}

func (s *Server) reportError(message string) {
	if o, ok := s.observer.(interface{ SetLastError(string) }); ok {
		o.SetLastError(message)
	}
}

type byteBudget struct {
	mu                  sync.Mutex
	available, capacity int64
	cond                *sync.Cond
}

func newByteBudget(n int64) *byteBudget {
	b := &byteBudget{available: n, capacity: n}
	b.cond = sync.NewCond(&b.mu)
	return b
}
func (b *byteBudget) acquire(n int64) bool {
	if n <= 0 || n > b.capacity {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for b.available < n {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		timer := time.AfterFunc(remaining, func() {
			b.mu.Lock()
			b.cond.Broadcast()
			b.mu.Unlock()
		})
		b.cond.Wait()
		timer.Stop()
	}
	b.available -= n
	return true
}
func (b *byteBudget) release(n int64) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	b.available += n
	if b.available > b.capacity {
		b.available = b.capacity
	}
	b.cond.Broadcast()
	b.mu.Unlock()
}

// NewServerWithLeaves constructs a data plane without access to the CA key.
// leafDir must contain PEM files named after the normalized SNI (for example
// gsp-ssl.ls.apple.com.pem), each containing certificate and private key.
func NewServerWithLeaves(addr string, allowSNI map[string]bool, rewrite func([]byte) ([]byte, bool), leafDir string) (*Server, error) {
	s := newServerWithLeafLoader(addr, allowSNI, rewrite, leafDir)
	for host := range s.allowSNI {
		if _, err := tls.LoadX509KeyPair(filepath.Join(leafDir, host+".pem"), filepath.Join(leafDir, host+".pem")); err != nil {
			return nil, fmt.Errorf("missing leaf certificate for %s: %w", host, err)
		}
	}
	return s, nil
}

func newServerWithLeafLoader(addr string, allowSNI map[string]bool, rewrite func([]byte) ([]byte, bool), leafDir string) *Server {
	s := &Server{addr: addr, allowSNI: normalizeAllowlist(allowSNI), rewrite: rewrite, ready: make(chan error, 1)}
	s.bodyLimit = maxProxyBody
	s.workSlots = make(chan struct{}, 32)
	s.connSlots = make(chan struct{}, 32)
	s.budget = newByteBudget(32 * maxProxyBody * 4)
	s.transport = newUpstreamTransport()
	s.tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}
	s.tlsConfig.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		host := NormalizeSNI(hello.ServerName)
		if !s.allowSNI[host] {
			return nil, fmt.Errorf("SNI is not allowed: %s", host)
		}
		cert, err := tls.LoadX509KeyPair(filepath.Join(leafDir, host+".pem"), filepath.Join(leafDir, host+".pem"))
		if err != nil {
			return nil, err
		}
		return &cert, nil
	}
	s.http = &http.Server{Handler: http.HandlerFunc(s.handle), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second}
	s.http.ConnState = func(conn net.Conn, state http.ConnState) {
		if state != http.StateClosed && state != http.StateHijacked {
			return
		}
		if release, ok := s.connReleases.LoadAndDelete(conn); ok {
			release.(func())()
		}
	}
	s.resolver = OriginalDestination
	s.dial = func(network, address string) (net.Conn, error) {
		return net.DialTimeout(network, address, upstreamDialTimeout)
	}
	return s
}

func newUpstreamTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: upstreamDialTimeout, KeepAlive: 30 * time.Second}
	return &http.Transport{
		ForceAttemptHTTP2:     true,
		DisableCompression:    true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   upstreamTLSHandshakeTimeout,
		ResponseHeaderTimeout: upstreamResponseHeaderTimeout,
		ExpectContinueTimeout: upstreamExpectContinueTimeout,
		IdleConnTimeout:       upstreamIdleConnTimeout,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
	}
}

// applyUpstreamTimeoutDefaults protects callers that inject a transport (for
// example the SOCKS5 setup) from accidentally restoring unbounded defaults.
// Existing non-zero values are preserved so tests and deployments can tune
// the limits lower than the production defaults.
func applyUpstreamTimeoutDefaults(t *http.Transport) {
	if t == nil {
		return
	}
	if t.DialContext == nil {
		d := &net.Dialer{Timeout: upstreamDialTimeout, KeepAlive: 30 * time.Second}
		t.DialContext = d.DialContext
	}
	if t.TLSHandshakeTimeout == 0 {
		t.TLSHandshakeTimeout = upstreamTLSHandshakeTimeout
	}
	if t.ResponseHeaderTimeout == 0 {
		t.ResponseHeaderTimeout = upstreamResponseHeaderTimeout
	}
	if t.ExpectContinueTimeout == 0 {
		t.ExpectContinueTimeout = upstreamExpectContinueTimeout
	}
	if t.IdleConnTimeout == 0 {
		t.IdleConnTimeout = upstreamIdleConnTimeout
	}
}

func normalizeAllowlist(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for host, enabled := range in {
		if enabled {
			out[NormalizeSNI(host)] = true
		}
	}
	return out
}

func (s *Server) ListenAndServe() error {
	s.stopping.Store(false)
	lns, err := listenDual(s.addr)
	if err != nil {
		s.ready <- err
		return err
	}
	s.mu.Lock()
	s.lns = lns
	s.mu.Unlock()
	s.ready <- nil
	return s.serveTransparent(lns...)
}

func (s *Server) Ready() <-chan error { return s.ready }

func (s *Server) SetLimits(maxConnections int, maxBodyBytes, maxBufferedBytes int64) {
	if maxConnections > 0 {
		s.workSlots = make(chan struct{}, maxConnections)
		s.connSlots = make(chan struct{}, maxConnections)
	}
	if maxBodyBytes > 0 {
		s.bodyLimit = maxBodyBytes
	}
	if maxBufferedBytes > 0 && s.bodyLimit > 0 && int64(cap(s.workSlots))*s.bodyLimit > maxBufferedBytes {
		slots := int(maxBufferedBytes / s.bodyLimit)
		if slots < 1 {
			slots = 1
		}
		s.workSlots = make(chan struct{}, slots)
	}
	if maxBufferedBytes > 0 {
		s.budget = newByteBudget(maxBufferedBytes)
	}
}

func (s *Server) SetObserver(o Observer) { s.observer = o }

func (s *Server) SetDetailedRewrite(fn func([]byte) ([]byte, RewriteMetadata, error)) {
	s.rewriteDetailed = fn
}
func (s *Server) getBudget() *byteBudget {
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	if s.bodyLimit <= 0 {
		s.bodyLimit = maxProxyBody
	}
	if s.budget == nil {
		reservation, ok := bufferReservation(s.bodyLimit)
		if !ok {
			reservation = 0
		}
		s.budget = newByteBudget(reservation)
	}
	return s.budget
}

func (s *Server) SetUpstreamSocks5(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	dialer, err := newSOCKS5Dialer(endpoint)
	if err != nil {
		return err
	}
	if s.transport == nil {
		s.transport = newUpstreamTransport()
	}
	s.transport.DialContext = dialer
	applyUpstreamTimeoutDefaults(s.transport)
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.stopping.Store(true)
	s.mu.Lock()
	for _, ln := range s.lns {
		_ = ln.Close()
	}
	s.lns = nil
	s.mu.Unlock()
	return s.http.Shutdown(ctx)
}

func EnrollmentHandler(certPath string) http.Handler {
	profilePath := filepath.Join(filepath.Dir(certPath), "ca.mobileconfig")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ca.pem" && r.URL.Path != "/ca.cer" && r.URL.Path != "/ca.mobileconfig" && r.URL.Path != "/fingerprint" {
			http.NotFound(w, r)
			return
		}
		cert, err := os.ReadFile(certPath)
		if err != nil {
			http.Error(w, "CA unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/ca.pem" {
			w.Header().Set("Content-Type", "application/x-pem-file")
			w.Header().Set("Content-Disposition", `attachment; filename="ils-gateway-ca.pem"`)
			_, _ = w.Write(cert)
			return
		}
		block, _ := pem.Decode(cert)
		if block == nil {
			http.Error(w, "invalid CA", http.StatusServiceUnavailable)
			return
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			http.Error(w, "invalid CA", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/fingerprint":
			sum := sha256.Sum256(parsed.Raw)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = fmt.Fprintf(w, "%X\n", sum[:])
		case "/ca.cer":
			w.Header().Set("Content-Type", "application/x-x509-ca-cert")
			w.Header().Set("Content-Disposition", `inline; filename="ils-gateway-ca.cer"`)
			_, _ = w.Write(parsed.Raw)
		case "/ca.mobileconfig":
			profile, err := os.ReadFile(profilePath)
			if err != nil || len(profile) == 0 {
				http.Error(w, "configuration profile unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/x-apple-aspen-config")
			w.Header().Set("Content-Disposition", `attachment; filename="ils-gateway-ca.mobileconfig"`)
			_, _ = w.Write(profile)
		}
	})
}

func EnrollmentMobileConfig(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	payloadUUID := fmt.Sprintf("%X-%X-%X-%X-%X", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	profileUUID := fmt.Sprintf("%X-%X-%X-%X-%X", sum[16:20], sum[20:22], sum[22:24], sum[24:26], sum[26:32])
	identifier := fmt.Sprintf("com.ils-gateway.ca.%x", sum[:8])
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
<key>PayloadContent</key><array><dict>
<key>PayloadCertificateFileName</key><string>iLS Gateway CA</string>
<key>PayloadContent</key><data>%s</data>
<key>PayloadDescription</key><string>iLS Gateway 定位服务根证书</string>
<key>PayloadDisplayName</key><string>iLS Gateway CA</string>
<key>PayloadIdentifier</key><string>%s.root</string>
<key>PayloadType</key><string>com.apple.security.root</string>
<key>PayloadUUID</key><string>%s</string>
<key>PayloadVersion</key><integer>1</integer>
</dict></array>
<key>PayloadDescription</key><string>为授权设备安装 iLS Gateway 根证书</string>
<key>PayloadDisplayName</key><string>iLS Gateway CA</string>
<key>PayloadIdentifier</key><string>%s</string>
<key>PayloadOrganization</key><string>iLS Gateway</string>
<key>PayloadRemovalDisallowed</key><false/>
<key>PayloadType</key><string>Configuration</string>
<key>PayloadUUID</key><string>%s</string>
<key>PayloadVersion</key><integer>1</integer>
</dict>
</plist>
`, base64.StdEncoding.EncodeToString(cert.Raw), identifier, payloadUUID, identifier, profileUUID)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	select {
	case s.workSlots <- struct{}{}:
		defer func() { <-s.workSlots }()
	default:
		http.Error(w, "proxy busy", http.StatusServiceUnavailable)
		return
	}
	budget := s.getBudget()
	limit := s.bodyLimit
	if limit <= 0 {
		limit = maxProxyBody
	}
	reservation, ok := bufferReservation(limit)
	if !ok {
		s.reportError("invalid proxy buffer limit")
		http.Error(w, "invalid proxy buffer limit", http.StatusServiceUnavailable)
		return
	}
	if !budget.acquire(reservation) {
		s.reportError("proxy buffer budget exhausted")
		http.Error(w, "proxy buffer budget exhausted", http.StatusServiceUnavailable)
		return
	}
	defer budget.release(reservation)
	if r.TLS == nil || !s.allowSNI[NormalizeSNI(r.TLS.ServerName)] {
		http.Error(w, "SNI not allowed", http.StatusMisdirectedRequest)
		return
	}
	if s.observer != nil && r.URL.Path == "/clls/wloc" {
		s.observer.RecordTargetRequest()
	}
	if r.Host == "" || NormalizeSNI(strings.Split(r.Host, ":")[0]) != NormalizeSNI(r.TLS.ServerName) {
		http.Error(w, "host/SNI mismatch", http.StatusMisdirectedRequest)
		return
	}
	r.URL.Scheme = "https"
	r.URL.Host = r.Host
	r.RequestURI = ""
	r.Header.Del("Proxy-Connection")
	resp, err := s.transport.RoundTrip(r)
	if err != nil {
		if s.observer != nil {
			s.observer.RecordUpstreamFailed()
		}
		s.reportError("upstream unavailable")
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, err := readBounded(resp.Body, limit)
	if err != nil {
		if errors.Is(err, errBodyLimit) {
			copyHeaders(w, resp.Header, false, "")
			w.Header().Del("Content-Length")
			w.WriteHeader(resp.StatusCode)
			_, writeErr := w.Write(body)
			if writeErr == nil {
				_, writeErr = io.Copy(w, resp.Body)
			}
			if writeErr != nil {
				if s.observer != nil {
					s.observer.RecordUpstreamFailed()
				}
				s.reportError("upstream stream failed")
			}
			if s.observer != nil && r.URL.Path == "/clls/wloc" {
				s.observer.RecordResponsePassthrough(resp.Header.Get("Content-Encoding"))
			}
			return
		}
		if s.observer != nil {
			s.observer.RecordUpstreamFailed()
		}
		s.reportError("upstream read failed")
		http.Error(w, "upstream read failed", http.StatusBadGateway)
		return
	}
	if int64(len(body)) > limit {
		copyHeaders(w, resp.Header, false, "")
		// The bounded prefix is only a probe. Remove the upstream framing
		// length before streaming the remainder so a large fail-open response
		// cannot be truncated by a stale Content-Length header.
		w.Header().Del("Content-Length")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		if _, copyErr := io.Copy(w, resp.Body); copyErr != nil {
			if s.observer != nil {
				s.observer.RecordUpstreamFailed()
			}
			s.reportError("upstream stream failed")
		}
		if s.observer != nil && r.URL.Path == "/clls/wloc" {
			s.observer.RecordResponsePassthrough(resp.Header.Get("Content-Encoding"))
		}
		return
	}
	rewritten := false
	outputEncoding := ""
	rewriteEnvelope := ""
	rewriteLocations := 0
	var rewriteInput, rewriteOutput []byte
	if (s.rewrite != nil || s.rewriteDetailed != nil) && r.URL.Path == "/clls/wloc" {
		encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
		switch encoding {
		case "":
			if s.rewriteDetailed != nil {
				changed, meta, rewriteErr := s.rewriteDetailed(body)
				if rewriteErr == nil && int64(len(changed)) <= limit && !bytes.Equal(changed, body) {
					rewriteEnvelope, rewriteLocations, rewriteInput, rewriteOutput = meta.Envelope, meta.Locations, body, changed
					body = changed
					rewritten = true
				} else if s.observer != nil {
					if rewriteErr != nil {
						if meta.Unsupported {
							s.observer.RecordRewriteUnsupported()
						} else {
							s.observer.RecordRewriteFailed()
						}
					} else if !bytes.Equal(changed, body) && int64(len(changed)) > limit {
						s.reportError("rewrite output exceeds body limit")
						s.observer.RecordRewriteFailed()
					} else if bytes.Equal(changed, body) {
						s.observer.RecordRewriteUnsupported()
					}
				}
			} else if changed, ok := s.rewrite(body); ok {
				switch {
				case bytes.Equal(changed, body):
					if s.observer != nil {
						s.observer.RecordRewriteUnsupported()
					}
				case int64(len(changed)) > limit:
					s.reportError("rewrite output exceeds body limit")
					if s.observer != nil {
						s.observer.RecordRewriteFailed()
					}
				default:
					body = changed
					rewritten = true
				}
			} else if s.observer != nil {
				s.reportError("rewrite failed")
				s.observer.RecordRewriteFailed()
			}
		case "gzip":
			if decoded, ok := gunzipBounded(body, limit); ok {
				if s.rewriteDetailed != nil {
					changed, meta, rewriteErr := s.rewriteDetailed(decoded)
					if rewriteErr == nil && !bytes.Equal(changed, decoded) {
						if encoded, encodeOK := gzipBounded(changed, limit); encodeOK {
							// Hashes are always over the decoded protobuf, even when the
							// response sent to the client is gzip encoded.
							rewriteEnvelope, rewriteLocations, rewriteInput, rewriteOutput = meta.Envelope, meta.Locations, decoded, changed
							body = encoded
							rewritten = true
							outputEncoding = "gzip"
						} else if s.observer != nil {
							s.reportError("gzip output exceeds body limit")
							s.observer.RecordGzipEncodeFailed()
						}
					} else if s.observer != nil {
						if rewriteErr != nil {
							if meta.Unsupported {
								s.observer.RecordRewriteUnsupported()
							} else {
								s.observer.RecordRewriteFailed()
							}
						} else {
							s.observer.RecordRewriteUnsupported()
						}
					}
				} else if changed, changedOK := s.rewrite(decoded); changedOK {
					if bytes.Equal(changed, decoded) {
						if s.observer != nil {
							s.observer.RecordRewriteUnsupported()
						}
					} else if encoded, encodeOK := gzipBounded(changed, limit); encodeOK {
						body = encoded
						rewritten = true
						outputEncoding = "gzip"
					} else if s.observer != nil {
						s.reportError("gzip output exceeds body limit")
						s.observer.RecordGzipEncodeFailed()
					}
				} else if s.observer != nil {
					s.reportError("rewrite failed")
					s.observer.RecordRewriteFailed()
				}
			} else if s.observer != nil {
				s.reportError("gzip response decode failed")
				s.observer.RecordGzipDecodeFailed()
			}
		default:
			// Unknown encodings are deliberately fail-open: preserve the exact
			// upstream bytes and Content-Encoding header.
			if s.observer != nil {
				s.observer.RecordRewriteUnsupported()
			}
		}
	}
	copyHeaders(w, resp.Header, rewritten, outputEncoding)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(resp.StatusCode)
	written, writeErr := w.Write(body)
	if s.observer != nil && r.URL.Path == "/clls/wloc" {
		if rewritten && writeErr == nil && written == len(body) {
			clearLastError(s.observer)
			if rewriteInput != nil {
				s.observer.RecordRewriteCandidate(rewriteEnvelope, rewriteLocations, rewriteInput, rewriteOutput, outputEncoding)
			}
			s.observer.RecordResponseModified(requestClientIP(r))
		} else {
			s.observer.RecordResponsePassthrough(resp.Header.Get("Content-Encoding"))
		}
	}
}

func copyHeaders(w http.ResponseWriter, headers http.Header, rewritten bool, outputEncoding string) {
	hopByHop := map[string]bool{"connection": true, "keep-alive": true, "proxy-authenticate": true, "proxy-authorization": true, "te": true, "trailer": true, "transfer-encoding": true, "upgrade": true}
	for key, values := range headers {
		if hopByHop[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			if rewritten && (strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Content-Encoding") || strings.EqualFold(key, "ETag") || strings.EqualFold(key, "Content-MD5")) {
				continue
			}
			w.Header().Add(key, value)
		}
	}
	if rewritten && outputEncoding != "" {
		w.Header().Set("Content-Encoding", outputEncoding)
	}
}

func gunzipBounded(body []byte, limit int64) ([]byte, bool) {
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	defer r.Close()
	decoded, err := readBounded(r, limit)
	if err != nil || decoded == nil {
		return nil, false
	}
	return decoded, true
}

func gzipBounded(body []byte, limit int64) ([]byte, bool) {
	if limit <= 0 || limit > int64(maxInt()) || int64(len(body)) > limit {
		return nil, false
	}
	out := &boundedBuffer{buf: make([]byte, 0, int(limit)), limit: limit}
	w := gzip.NewWriter(out)
	if _, err := w.Write(body); err != nil {
		_ = w.Close()
		return nil, false
	}
	if err := w.Close(); err != nil || int64(len(out.buf)) > limit {
		return nil, false
	}
	return out.buf, true
}

// readBounded performs a bounded read without io.ReadAll's geometric capacity
// growth. The caller reserves one body slot before entering the data path, so
// this allocation is part of the configured buffer budget.
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	if limit < 0 || limit >= int64(maxInt()) {
		return nil, fmt.Errorf("invalid body limit")
	}
	buf := make([]byte, int(limit)+1)
	used := 0
	for used < len(buf) {
		n, err := r.Read(buf[used:])
		if n > 0 {
			used += n
			if used == len(buf) {
				return buf[:used], errBodyLimit
			}
		}
		if err != nil {
			if err == io.EOF {
				return buf[:used], nil
			}
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
	return buf[:used], errBodyLimit
}

func bufferReservation(limit int64) (int64, bool) {
	if limit < 0 || limit > ((1<<63-1)-4)/4 {
		return 0, false
	}
	return 4 * (limit + 1), true
}

type boundedBuffer struct {
	buf   []byte
	limit int64
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if int64(len(b.buf))+int64(len(p)) > b.limit {
		return 0, fmt.Errorf("buffer limit exceeded")
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

func clearLastError(o Observer) {
	if h, ok := o.(interface{ SetLastError(string) }); ok {
		h.SetLastError("")
	}
}
