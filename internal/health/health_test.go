package health

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotEffectiveEnabled(t *testing.T) {
	s := New()
	s.SetEnabled(true)
	s.SetHealthy(true)
	if s.Snapshot().EffectiveEnabled {
		t.Fatal("daemon health must not claim effective nft state")
	}
	if !s.Snapshot().DataPlaneHealthy {
		t.Fatal("expected data plane healthy")
	}
	s.SetBypass(true)
	if s.Snapshot().EffectiveEnabled {
		t.Fatal("bypass must not create effective state")
	}
}

func TestMetricsAreThreadSafeAndRedacted(t *testing.T) {
	s := New()
	const n = 100
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			s.RecordTargetRequest()
			s.RecordResponseModified("192.0.2.10")
			s.RecordRewriteCandidate("arpc", 1, []byte("secret payload"), []byte("rewritten"), "gzip")
			done <- struct{}{}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
	m := s.Snapshot()
	if m.TargetRequests != n || m.ResponseModified != n || m.LastEnvelope != "arpc" || m.LastInputSHA256 == "" || m.LastOutputSHA256 == "" {
		t.Fatalf("unexpected metrics: %+v", m)
	}
	if strings.Contains(m.LastInputSHA256, "secret") || strings.Contains(m.LastOutputSHA256, "rewritten") {
		t.Fatal("payload leaked into metrics")
	}
	if m.LastModifiedByIP["192.0.2.10"] == "" {
		t.Fatal("missing per-device modification time")
	}
}

func TestActivityPersistsAcrossStateRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.json")
	s := NewWithActivityFile(path)
	s.SetMACResolver(func(ip string) string {
		if ip == "192.0.2.25" {
			return "02:00:5E:10:00:25"
		}
		return ""
	})
	s.RecordResponseModified("192.0.2.25")
	s.RecordResponseModified("not-an-ip")

	reloaded := NewWithActivityFile(path).Snapshot()
	value := reloaded.LastModifiedByIP["192.0.2.25"]
	if value == "" {
		t.Fatal("persisted device activity was not restored")
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		t.Fatalf("invalid persisted timestamp: %v", err)
	}
	if _, found := reloaded.LastModifiedByIP["not-an-ip"]; found {
		t.Fatal("invalid client address was persisted")
	}
	if reloaded.LastModifiedByMAC["02:00:5e:10:00:25"] == "" {
		t.Fatal("persisted MAC activity was not restored")
	}
}

func TestLegacyActivityFileStillLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.json")
	if err := os.WriteFile(path, []byte(`{"192.0.2.8":"2026-08-16T12:30:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if NewWithActivityFile(path).Snapshot().LastModifiedByIP["192.0.2.8"] == "" {
		t.Fatal("legacy activity file was not loaded")
	}
}
