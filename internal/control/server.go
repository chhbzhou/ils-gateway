package control

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chhbzhou/ils-gateway/internal/health"
)

const (
	maxMessage  = 256 << 10
	maxRequests = 32
	idleTimeout = 30 * time.Second
)

type Server struct {
	path        string
	stateDir    string
	requestOnly bool
	h           *health.State
	reload      func() error
	ln          net.Listener
	mu          sync.Mutex
}

func (s *Server) SetReload(reload func() error) { s.reload = reload }

func New(path string, h *health.State) *Server {
	return &Server{path: path, stateDir: filepath.Dir(path), h: h}
}

// NewWithState runs the daemon with a root-owned state directory. The daemon
// cannot write privileged markers there. It may request fail-open bypass, but
// it can never request bypass-off (which would grant traffic interception
// authority back to a non-root process).
func NewWithState(path, stateDir string, h *health.State) *Server {
	return &Server{path: path, stateDir: stateDir, requestOnly: true, h: h}
}

func (s *Server) Listen() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0750); err != nil {
		return err
	}
	_ = os.Remove(s.path)
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.path, 0660); err != nil {
		_ = ln.Close()
		return err
	}
	s.ln = ln
	go s.accept()
	return nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	err := s.ln.Close()
	s.ln = nil
	if removeErr := os.Remove(s.path); err == nil && !os.IsNotExist(removeErr) {
		err = removeErr
	}
	return err
}

func (s *Server) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

type request struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Action  string          `json:"action"` // backwards-compatible health probe
	Params  json.RawMessage `json:"params"`
}

type response struct {
	Version int         `json:"version"`
	ID      string      `json:"id,omitempty"`
	OK      bool        `json:"ok"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error"`
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	s.h.AddConnection()
	defer s.h.DoneConnection()
	r := bufio.NewReader(c)
	for count := 0; count < maxRequests; count++ {
		_ = c.SetDeadline(time.Now().Add(idleTimeout))
		body, err := readFrame(r)
		if err != nil {
			return
		}
		var q request
		if err := json.Unmarshal(body, &q); err != nil {
			_ = writeFrame(c, response{Version: 1, OK: false, Error: "invalid_json"})
			return
		}
		method := q.Method
		if method == "" {
			method = q.Action
		}
		var result interface{}
		var callErr interface{}
		switch method {
		case "status", "health":
			snapshot := s.h.Snapshot()
			bypassPath := filepath.Join(s.stateDir, "bypass")
			// The marker is the durable source of truth across daemon restarts;
			// the in-memory health flag is only a request-local cache.
			if !s.requestOnly && fileExists(bypassPath) {
				snapshot.Bypass = true
			}
			result = snapshot
		case "bypass_on":
			path := filepath.Join(s.stateDir, "bypass")
			if s.requestOnly {
				path = filepath.Join(filepath.Dir(s.path), "bypass_on.request")
			}
			if err := atomicMarker(path); err != nil {
				callErr = "bypass_marker_write_failed"
			} else {
				s.h.SetBypass(true)
				result = map[string]bool{"bypass": true}
			}
		case "reload":
			if s.reload == nil {
				callErr = "reload_unavailable"
			} else if err := s.reload(); err != nil {
				callErr = err.Error()
			} else {
				result = map[string]bool{"reloaded": true}
			}
		default:
			callErr = "unknown_method"
		}
		_ = writeFrame(c, response{Version: 1, ID: q.ID, OK: callErr == nil, Result: result, Error: callErr})
	}
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	if n == 0 || n > maxMessage {
		return nil, fmt.Errorf("invalid message length")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

func writeFrame(w io.Writer, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil || len(b) > maxMessage {
		return fmt.Errorf("response too large")
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(b))); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }

func atomicMarker(path string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".marker-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.WriteString("1\n")
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func Request(path, method string) (json.RawMessage, error) {
	return RequestWithParams(path, method, map[string]interface{}{})
}

func RequestWithParams(path, method string, params interface{}) (json.RawMessage, error) {
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	q := request{Version: 1, ID: "local", Method: method, Params: encoded}
	if err := writeFrame(c, q); err != nil {
		return nil, err
	}
	b, err := readFrame(bufio.NewReader(c))
	return b, err
}
