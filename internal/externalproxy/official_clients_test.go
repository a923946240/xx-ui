package externalproxy

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestOfficialClientConfigStartup(t *testing.T) {
	bin := os.Getenv("EXTERNAL_CLIENT_BIN_DIR")
	if bin == "" {
		t.Skip("set EXTERNAL_CLIENT_BIN_DIR to the pinned official client executables")
	}
	for i, protocol := range []string{"naive", "mieru", "trusttunnel"} {
		t.Run(protocol, func(t *testing.T) {
			m := NewManager(t.TempDir(), bin)
			defer m.StopAll()
			s := Spec{ID: i + 1, Tag: "official-" + protocol, Protocol: protocol, Host: "127.0.0.1", Port: 1, Username: "fixture-user", Password: "fixture-password", ServerName: "localhost", Transport: "tcp", Enable: true}
			if err := m.Reconcile([]Spec{s}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(20 * time.Second)
			for m.Status(s.Tag).State != "running" && time.Now().Before(deadline) {
				if protocol == "trusttunnel" && strings.Contains(m.Status(s.Tag).Logs, "Number of connection attempts exceeded") {
					if !strings.Contains(m.Status(s.Tag).Logs, "SOCKS5_LISTENER") {
						t.Fatal("TrustTunnel did not accept its SOCKS configuration")
					}
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			if status := m.Status(s.Tag); status.State != "running" {
				t.Fatalf("official client rejected generated configuration: %+v", status)
			}
		})
	}
}
