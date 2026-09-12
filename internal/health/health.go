package health

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type Snapshot struct {
	Healthy          bool `json:"healthy"`
	DataPlaneHealthy bool `json:"data_plane_healthy"`
	Enabled          bool `json:"enabled"`
	Bypass           bool `json:"bypass"`
	// EffectiveEnabled is intentionally not serialized.  The authoritative
	// network state is locspoofctl's exact nft verification, not this daemon
	// health snapshot.
	EffectiveEnabled      bool              `json:"-"`
	ActiveConnections     int64             `json:"active_connections"`
	LastError             string            `json:"last_error,omitempty"`
	TargetRequests        uint64            `json:"target_requests"`
	ResponseModified      uint64            `json:"response_modified"`
	ResponsePassthrough   uint64            `json:"response_passthrough"`
	RewriteUnsupported    uint64            `json:"rewrite_unsupported"`
	RewriteFailed         uint64            `json:"rewrite_failed"`
	GzipDecodeFailed      uint64            `json:"gzip_decode_failed"`
	GzipEncodeFailed      uint64            `json:"gzip_encode_failed"`
	UpstreamFailed        uint64            `json:"upstream_failed"`
	LastRewriteTime       string            `json:"last_rewrite_time,omitempty"`
	LastEnvelope          string            `json:"last_envelope,omitempty"`
	LastLocationsModified int               `json:"last_locations_modified,omitempty"`
	LastContentEncoding   string            `json:"last_content_encoding,omitempty"`
	LastInputSHA256       string            `json:"last_input_sha256,omitempty"`
	LastOutputSHA256      string            `json:"last_output_sha256,omitempty"`
	LastModifiedByIP      map[string]string `json:"last_modified_by_ip,omitempty"`
	LastModifiedByMAC     map[string]string `json:"last_modified_by_mac,omitempty"`
}
type State struct {
	healthy, enabled, bypass atomic.Bool
	active                   atomic.Int64
	lastError                atomic.Value
	activityPath             string
	macResolver              func(string) string
	metricsMu                sync.RWMutex
	metrics                  Metrics
}

type Metrics struct {
	TargetRequests, ResponseModified, ResponsePassthrough                                 uint64
	RewriteUnsupported, RewriteFailed, GzipDecodeFailed, GzipEncodeFailed, UpstreamFailed uint64
	LastRewriteTime                                                                       time.Time
	LastEnvelope, LastContentEncoding, LastInputSHA256, LastOutputSHA256                  string
	LastLocationsModified                                                                 int
	LastModifiedByIP                                                                      map[string]time.Time
	LastModifiedByMAC                                                                     map[string]time.Time
}

func New() *State {
	s := &State{}
	s.metrics.LastModifiedByIP = make(map[string]time.Time)
	s.metrics.LastModifiedByMAC = make(map[string]time.Time)
	s.healthy.Store(false)
	return s
}

func NewWithActivityFile(path string) *State {
	s := New()
	s.activityPath = path
	s.loadActivity()
	return s
}

func (s *State) SetHealthy(v bool)                          { s.healthy.Store(v) }
func (s *State) SetEnabled(v bool)                          { s.enabled.Store(v) }
func (s *State) SetBypass(v bool)                           { s.bypass.Store(v) }
func (s *State) SetLastError(v string)                      { s.lastError.Store(v) }
func (s *State) SetMACResolver(resolve func(string) string) { s.macResolver = resolve }
func (s *State) AddConnection()                             { s.active.Add(1) }
func (s *State) DoneConnection()                            { s.active.Add(-1) }
func (s *State) RecordTargetRequest() {
	s.metricsMu.Lock()
	s.metrics.TargetRequests++
	s.metricsMu.Unlock()
}
func (s *State) RecordResponsePassthrough(encoding string) {
	s.metricsMu.Lock()
	s.metrics.ResponsePassthrough++
	s.metrics.LastContentEncoding = encoding
	s.metricsMu.Unlock()
}
func (s *State) RecordRewriteUnsupported() {
	s.metricsMu.Lock()
	s.metrics.RewriteUnsupported++
	s.metricsMu.Unlock()
}
func (s *State) RecordRewriteFailed() {
	s.metricsMu.Lock()
	s.metrics.RewriteFailed++
	s.metricsMu.Unlock()
}
func (s *State) RecordGzipDecodeFailed() {
	s.metricsMu.Lock()
	s.metrics.GzipDecodeFailed++
	s.metricsMu.Unlock()
}
func (s *State) RecordGzipEncodeFailed() {
	s.metricsMu.Lock()
	s.metrics.GzipEncodeFailed++
	s.metricsMu.Unlock()
}
func (s *State) RecordUpstreamFailed() {
	s.metricsMu.Lock()
	s.metrics.UpstreamFailed++
	s.metricsMu.Unlock()
}
func (s *State) RecordRewriteCandidate(envelope string, locations int, input, output []byte, encoding string) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	s.metrics.LastRewriteTime = time.Now().UTC()
	s.metrics.LastEnvelope = envelope
	s.metrics.LastLocationsModified = locations
	s.metrics.LastContentEncoding = encoding
	in := sha256.Sum256(input)
	out := sha256.Sum256(output)
	s.metrics.LastInputSHA256 = hex.EncodeToString(in[:])
	s.metrics.LastOutputSHA256 = hex.EncodeToString(out[:])
}
func (s *State) RecordResponseModified(clientIP string) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	s.metrics.ResponseModified++
	if ip := net.ParseIP(clientIP); ip != nil {
		now := time.Now().UTC()
		normalizedIP := ip.String()
		s.metrics.LastModifiedByIP[normalizedIP] = now
		if s.macResolver != nil {
			if mac := normalizeMAC(s.macResolver(normalizedIP)); mac != "" {
				s.metrics.LastModifiedByMAC[mac] = now
			}
		}
		s.persistActivityLocked()
	}
}
func (s *State) Snapshot() Snapshot {
	e := ""
	if v := s.lastError.Load(); v != nil {
		e = v.(string)
	}
	healthy, enabled, bypass := s.healthy.Load(), s.enabled.Load(), s.bypass.Load()
	s.metricsMu.RLock()
	m := s.metrics
	modifiedByIP := make(map[string]string, len(m.LastModifiedByIP))
	for ip, modified := range m.LastModifiedByIP {
		modifiedByIP[ip] = modified.Format(time.RFC3339Nano)
	}
	modifiedByMAC := make(map[string]string, len(m.LastModifiedByMAC))
	for mac, modified := range m.LastModifiedByMAC {
		modifiedByMAC[mac] = modified.Format(time.RFC3339Nano)
	}
	s.metricsMu.RUnlock()
	out := Snapshot{Healthy: healthy, DataPlaneHealthy: healthy, Enabled: enabled, Bypass: bypass, ActiveConnections: s.active.Load(), LastError: e,
		TargetRequests: m.TargetRequests, ResponseModified: m.ResponseModified, ResponsePassthrough: m.ResponsePassthrough,
		RewriteUnsupported: m.RewriteUnsupported, RewriteFailed: m.RewriteFailed, GzipDecodeFailed: m.GzipDecodeFailed, GzipEncodeFailed: m.GzipEncodeFailed, UpstreamFailed: m.UpstreamFailed,
		LastEnvelope: m.LastEnvelope, LastLocationsModified: m.LastLocationsModified, LastContentEncoding: m.LastContentEncoding, LastInputSHA256: m.LastInputSHA256, LastOutputSHA256: m.LastOutputSHA256}
	if len(modifiedByIP) > 0 {
		out.LastModifiedByIP = modifiedByIP
	}
	if len(modifiedByMAC) > 0 {
		out.LastModifiedByMAC = modifiedByMAC
	}
	if !m.LastRewriteTime.IsZero() {
		out.LastRewriteTime = m.LastRewriteTime.Format(time.RFC3339Nano)
	}
	return out
}

func (s *State) loadActivity() {
	if s.activityPath == "" {
		return
	}
	body, err := os.ReadFile(s.activityPath)
	if err != nil || len(body) > 64<<10 {
		return
	}
	var stored persistedActivity
	if err := json.Unmarshal(body, &stored); err != nil || stored.Version == 0 {
		var legacy map[string]string
		if json.Unmarshal(body, &legacy) != nil {
			return
		}
		stored = persistedActivity{Version: 1, ByIP: legacy}
	}
	for rawIP, rawTime := range stored.ByIP {
		ip := net.ParseIP(rawIP)
		modified, err := time.Parse(time.RFC3339Nano, rawTime)
		if ip != nil && err == nil {
			s.metrics.LastModifiedByIP[ip.String()] = modified.UTC()
		}
	}
	for rawMAC, rawTime := range stored.ByMAC {
		mac := normalizeMAC(rawMAC)
		modified, err := time.Parse(time.RFC3339Nano, rawTime)
		if mac != "" && err == nil {
			s.metrics.LastModifiedByMAC[mac] = modified.UTC()
		}
	}
}

func (s *State) persistActivityLocked() {
	if s.activityPath == "" {
		return
	}
	stored := persistedActivity{Version: 2, ByIP: make(map[string]string, len(s.metrics.LastModifiedByIP)), ByMAC: make(map[string]string, len(s.metrics.LastModifiedByMAC))}
	for ip, modified := range s.metrics.LastModifiedByIP {
		stored.ByIP[ip] = modified.Format(time.RFC3339Nano)
	}
	for mac, modified := range s.metrics.LastModifiedByMAC {
		stored.ByMAC[mac] = modified.Format(time.RFC3339Nano)
	}
	body, err := json.Marshal(stored)
	if err != nil {
		return
	}
	dir := filepath.Dir(s.activityPath)
	tmp, err := os.CreateTemp(dir, ".activity-")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(body)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		_ = os.Rename(tmpName, s.activityPath)
	}
}

type persistedActivity struct {
	Version int               `json:"version"`
	ByIP    map[string]string `json:"by_ip,omitempty"`
	ByMAC   map[string]string `json:"by_mac,omitempty"`
}

func normalizeMAC(raw string) string {
	mac, err := net.ParseMAC(raw)
	if err != nil || len(mac) != 6 {
		return ""
	}
	return mac.String()
}
