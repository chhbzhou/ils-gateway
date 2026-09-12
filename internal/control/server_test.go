package control

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chhbzhou/ils-gateway/internal/health"
)

func TestStatusAndBypassPersistMarker(t *testing.T) {
	p := filepath.Join(t.TempDir(), "control.sock")
	h := health.New()
	s := New(p, h)
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := Request(p, "bypass_on")
	if err != nil {
		t.Fatal(err)
	}
	var resp response
	if err := json.Unmarshal(b, &resp); err != nil || !resp.OK {
		t.Fatalf("%s %v", b, err)
	}
	if _, err := net.DialTimeout("unix", p, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := Request(p, "status"); err != nil {
		t.Fatal(err)
	}
	b, err = Request(p, "health")
	if err != nil {
		t.Fatal(err)
	}
	var healthResp struct {
		Result struct {
			Bypass           bool `json:"bypass"`
			EffectiveEnabled bool `json:"effective_enabled"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &healthResp); err != nil {
		t.Fatal(err)
	}
	if !healthResp.Result.Bypass || healthResp.Result.EffectiveEnabled {
		t.Fatalf("marker not reflected: %s", b)
	}
}

func TestBypassMarkerWriteFailure(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "missing", "control.sock"), health.New())
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	done := make(chan struct{})
	go func() { s.handle(server); close(done) }()
	q := request{Version: 1, ID: "x", Method: "bypass_on", Params: json.RawMessage(`{}`)}
	if err := writeFrame(client, q); err != nil {
		t.Fatal(err)
	}
	b, err := readFrame(bufio.NewReader(client))
	if err != nil {
		t.Fatal(err)
	}
	var resp response
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("marker failure reported success")
	}
	_ = client.Close()
	<-done
}

func TestRequestOnlyBypassCanOnlyRequestFailOpen(t *testing.T) {
	d := t.TempDir()
	runDir := os.TempDir()
	p := filepath.Join(runDir, "ils-request.sock")
	_ = os.Remove(p)
	t.Cleanup(func() {
		_ = os.Remove(p)
		_ = os.Remove(filepath.Join(runDir, "bypass_on.request"))
	})
	s := NewWithState(p, filepath.Join(d, "state"), health.New())
	if err := os.MkdirAll(runDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := Request(p, "bypass_on")
	if err != nil {
		t.Fatal(err)
	}
	var resp response
	if err := json.Unmarshal(b, &resp); err != nil || !resp.OK {
		t.Fatalf("bypass_on response=%s err=%v", b, err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "bypass_on.request")); err != nil {
		t.Fatalf("bypass_on request missing: %v", err)
	}

	b, err = Request(p, "bypass_off")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK || resp.Error != "unknown_method" {
		t.Fatalf("bypass_off must be rejected by non-root daemon: %s", b)
	}
	if _, err := os.Stat(filepath.Join(runDir, "bypass_off.request")); !os.IsNotExist(err) {
		t.Fatalf("daemon created bypass_off request: %v", err)
	}
}

func TestRequestOnlyHealthDoesNotReadRootState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix control socket path semantics are not available on Windows")
	}
	d := t.TempDir()
	state := filepath.Join(d, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "bypass"), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d, "control.sock")
	s := NewWithState(p, state, health.New())
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := Request(p, "health")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			Bypass bool `json:"bypass"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Result.Bypass {
		t.Fatal("request-only daemon reported root bypass marker as authoritative")
	}
}

func TestReloadCallback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "control.sock")
	s := New(p, health.New())
	called := false
	s.SetReload(func() error { called = true; return nil })
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := Request(p, "reload")
	if err != nil {
		t.Fatal(err)
	}
	var resp response
	if err := json.Unmarshal(b, &resp); err != nil || !resp.OK || !called {
		t.Fatalf("reload response=%s called=%v err=%v", b, called, err)
	}
}
