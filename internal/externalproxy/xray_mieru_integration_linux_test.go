//go:build linux

package externalproxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

func TestRealXrayMieruFinalExitAndDisable(t *testing.T) {
	bin := os.Getenv("EXTERNAL_CLIENT_BIN_DIR")
	mita := os.Getenv("EXTERNAL_MITA_BINARY")
	xray := os.Getenv("XRAY_E2E_BINARY")
	if bin == "" || mita == "" || xray == "" {
		t.Skip("set official client, mita and Xray fixture binaries")
	}
	if _, err := os.Stat("/var/lib/mita"); err == nil {
		t.Skip("refuse to share an existing mita system state directory")
	}
	dir, err := os.MkdirTemp("", "external-mieru-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	port := fixturePort(t)
	config := map[string]any{
		"listenIPAddress": "127.0.0.1",
		"portBindings":    []any{map[string]any{"port": port, "protocol": "TCP"}},
		"users":           []any{map[string]any{"name": "fixture", "password": "synthetic-fixture-password", "allowLoopbackIP": true, "allowPrivateIP": true}},
		"loggingLevel":    "ERROR",
	}
	configPath := filepath.Join(dir, "mita.json")
	writeFixtureJSON(t, configPath, config)
	mitaCommand := exec.Command(mita, "run")
	mitaCommand.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "MITA_CONFIG_JSON_FILE=" + configPath, "MITA_UDS_PATH=" + filepath.Join(dir, "mita.sock"), "MITA_INSECURE_UDS=1"}
	if os.Geteuid() == 0 {
		if err := os.Chown(dir, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(configPath, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		mitaCommand.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534, NoSetGroups: true}}
	}
	startFixture(t, mitaCommand)
	waitFixturePort(t, port)
	m := NewManager(t.TempDir(), bin)
	defer m.StopAll()
	s := Spec{ID: 1, Tag: "mieru-fixture", Protocol: "mieru", Host: "127.0.0.1", Port: port, Username: "fixture", Password: "synthetic-fixture-password", Enable: true}
	if err := m.Reconcile([]Spec{s}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for m.Status(s.Tag).State != "running" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if status := m.Status(s.Tag); status.State != "running" {
		t.Fatalf("mieru client unavailable: %+v", status)
	}
	xrayPort := fixturePort(t)
	xrayConfig := map[string]any{
		"log":       map[string]string{"loglevel": "error"},
		"inbounds":  []any{map[string]any{"tag": "entry", "listen": "127.0.0.1", "port": xrayPort, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": false}}},
		"outbounds": m.Outbounds(),
		"routing":   map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"entry"}, "outboundTag": s.Tag}}},
	}
	xrayConfigPath := filepath.Join(t.TempDir(), "xray.json")
	writeFixtureJSON(t, xrayConfigPath, xrayConfig)
	startFixture(t, exec.Command(xray, "run", "-c", xrayConfigPath))
	waitFixturePort(t, xrayPort)
	var reached atomic.Int64
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		_, _ = io.WriteString(w, "through-real-mieru")
	}))
	defer target.Close()
	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(xrayPort)), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	transport := target.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.(proxy.ContextDialer).DialContext(ctx, network, address)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("real Xray → gate → mieru → mita → HTTPS fixture: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "through-real-mieru" || reached.Load() != 1 {
		t.Fatalf("unexpected tunneled response %q, requests=%d, err=%v", body, reached.Load(), err)
	}
	transport.CloseIdleConnections()
	s.Enable = false
	if err := m.Reconcile([]Spec{s}); err != nil {
		t.Fatal(err)
	}
	if response, err := client.Get(target.URL); err == nil {
		response.Body.Close()
		t.Fatal("disabled exit fell back to a working path")
	}
	if reached.Load() != 1 {
		t.Fatal("disabled exit reached the destination")
	}
}

func fixturePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func writeFixtureJSON(t *testing.T, path string, data any) {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func startFixture(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
}

func waitFixturePort(t *testing.T, port int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fixture listener %d did not start", port)
}
