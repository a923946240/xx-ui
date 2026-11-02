package externalproxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
)

type Status struct {
	State string `json:"state"`
	Logs  string `json:"logs"`
}

type Manager struct {
	mu                  sync.Mutex
	stateDir, binaryDir string
	runners             map[string]*runner
	initErr             error
}

func NewManager(stateDir, binaryDir string) *Manager {
	state, err := filepath.Abs(stateDir)
	if err != nil {
		return &Manager{initErr: err, runners: make(map[string]*runner)}
	}
	bin, err := filepath.Abs(binaryDir)
	return &Manager{stateDir: state, binaryDir: bin, initErr: err, runners: make(map[string]*runner)}
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (m *Manager) Reconcile(specs []Spec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.initErr != nil {
		return m.initErr
	}
	wanted := make(map[string]Spec, len(specs))
	for _, s := range specs {
		if err := s.Validate(); err != nil {
			return err
		}
		if s.ID <= 0 {
			return errors.New("invalid external outbound id")
		}
		if _, exists := wanted[s.Tag]; exists {
			return errors.New("duplicate external outbound tag")
		}
		wanted[s.Tag] = s
	}
	for tag, old := range m.runners {
		if s, exists := wanted[tag]; !exists || s != old.spec {
			old.stop()
			delete(m.runners, tag)
		}
	}
	for tag, s := range wanted {
		if _, exists := m.runners[tag]; exists {
			continue
		}
		r := &runner{spec: s, binary: filepath.Join(m.binaryDir, s.Protocol), dir: filepath.Join(m.stateDir, strconv.Itoa(s.ID)), state: "disabled", logs: newLogBuffer(s)}
		m.runners[tag] = r
		if !s.Enable {
			continue
		}
		if runtime.GOOS != "linux" {
			r.setState("unsupported-platform")
			continue
		}
		token, err := randomToken()
		if err != nil {
			return err
		}
		r.localPassword = token
		r.ctx, r.cancel = context.WithCancel(context.Background())
		r.gate, err = newGate("xray", token, r.dial)
		if err != nil {
			r.setState("listener-error")
			r.logs.add("Could not reserve private SOCKS listener")
			r.cancel()
			continue
		}
		r.done = make(chan struct{})
		r.setState("starting")
		if !executableExists(r.binary) {
			r.setState("missing-binary")
		}
		go r.supervise()
	}
	return nil
}

func (m *Manager) Outbounds() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	tags := make([]string, 0, len(m.runners))
	for tag := range m.runners {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	result := make([]map[string]any, 0, len(tags))
	for _, tag := range tags {
		r := m.runners[tag]
		out := map[string]any{"tag": tag, "protocol": "blackhole", "settings": map[string]any{}}
		if r.spec.Enable && r.gate != nil {
			_, p, _ := net.SplitHostPort(r.gate.Address())
			port, _ := strconv.Atoi(p)
			out["protocol"] = "socks"
			out["settings"] = map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": port, "users": []any{map[string]string{"user": "xray", "pass": r.localPassword}}}}}
		}
		result = append(result, out)
	}
	return result
}

func (m *Manager) Status(tag string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.runners[tag]; r != nil {
		return Status{State: r.getState(), Logs: r.logs.String()}
	}
	return Status{State: "stopped"}
}

func (m *Manager) DialContext(ctx context.Context, tag, address string) (net.Conn, error) {
	m.mu.Lock()
	r := m.runners[tag]
	m.mu.Unlock()
	if r == nil {
		return nil, errors.New("external outbound is unavailable")
	}
	return r.dial(ctx, address)
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.runners {
		r.stop()
	}
	clear(m.runners)
}
