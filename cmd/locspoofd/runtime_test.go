package main

import (
	"bytes"
	"github.com/chhbzhou/ils-gateway/internal/config"
	"strings"
	"testing"
)

type runtimeHealthStub struct{ lastError string }

func (h *runtimeHealthStub) SetLastError(value string) { h.lastError = value }

func validWLocBody() []byte {
	// bare -> record(field 2) -> location(field 1/2)
	return []byte{0x12, 0x06, 0x12, 0x04, 0x08, 0x01, 0x10, 0x02}
}

func TestDynamicRuntimePolicyAndARPResolution(t *testing.T) {
	h := &runtimeHealthStub{}
	cfg := config.Default()
	cfg.Enabled = true
	cfg.ProfileSet = true
	cfg.Profile.Latitude = 1
	cfg.Profile.Longitude = 2
	policy := newRuntimePolicy(cfg)
	first, _, err := policy.DetailedRewrite(h)(validWLocBody())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profile.Latitude = 3
	policy.Store(cfg)
	second, _, err := policy.DetailedRewrite(h)(validWLocBody())
	if err != nil || bytes.Equal(first, second) {
		t.Fatal("runtime profile did not update without recreating the callback")
	}
	arp := "IP address HW type Flags HW address Mask Device\n192.0.2.25 0x1 0x2 02:00:5E:10:00:25 * br-lan\n"
	if got := resolveMACFromARPReader(strings.NewReader(arp), "192.0.2.25"); got != "02:00:5e:10:00:25" {
		t.Fatalf("unexpected ARP MAC: %q", got)
	}
}

func TestRuntimePolicyBuilders(t *testing.T) {
	c := config.Default()
	c.WLocHosts = []string{"GSP-SSL.LS.APPLE.COM"}
	if !buildAllowlist(c)["gsp-ssl.ls.apple.com"] {
		t.Fatal("host normalization failed")
	}
	if buildRewrite(c) != nil {
		t.Fatal("disabled config must not rewrite")
	}
	c.Enabled = true
	c.ProfileSet = true
	if buildRewrite(c) == nil {
		t.Fatal("enabled profile missing rewrite")
	}
}

func TestBuildDetailedRewriteDisabledAndIncomplete(t *testing.T) {
	h := &runtimeHealthStub{}
	c := config.Default()
	if buildDetailedRewrite(c, h) != nil {
		t.Fatal("disabled config must not install a detailed rewrite")
	}
	c.Enabled = true
	if buildDetailedRewrite(c, h) != nil {
		t.Fatal("enabled config without a profile must remain fail-open")
	}
}

func TestBuildDetailedRewriteSuccessAndErrorMetadata(t *testing.T) {
	h := &runtimeHealthStub{}
	c := config.Default()
	c.Enabled = true
	c.ProfileSet = true
	c.Profile.Latitude = 35.5
	c.Profile.Longitude = -139.7
	c.Profile.HorizontalAccuracy = 10
	c.Profile.VerticalAccuracy = 20
	fn := buildDetailedRewrite(c, h)
	if fn == nil {
		t.Fatal("valid profile did not install detailed rewrite")
	}
	out, meta, err := fn(validWLocBody())
	if err != nil || len(out) == 0 || bytes.Equal(out, validWLocBody()) {
		t.Fatalf("successful detailed rewrite: len=%d metadata=%+v err=%v", len(out), meta, err)
	}
	if meta.Envelope != "bare" || meta.Locations != 1 || meta.Unsupported {
		t.Fatalf("unexpected success metadata: %+v", meta)
	}
	if h.lastError != "" {
		t.Fatalf("success should not set an error: %q", h.lastError)
	}

	if _, meta, err := fn([]byte{0}); err == nil || !meta.Unsupported {
		t.Fatalf("unsupported body classification: metadata=%+v err=%v", meta, err)
	}
	if h.lastError == "" {
		t.Fatal("unsupported rewrite did not record last_error")
	}
	if _, meta, err := fn([]byte{0x12, 0x05, 0x01}); err == nil {
		t.Fatalf("malformed body unexpectedly succeeded: metadata=%+v", meta)
	}
	if h.lastError == "" {
		t.Fatal("malformed rewrite did not record last_error")
	}
}
