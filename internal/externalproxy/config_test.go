package externalproxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProtocolConfigsUsePrivateSocksAndVerifiedTLS(t *testing.T) {
	for _, protocol := range []string{"naive", "mieru", "trusttunnel"} {
		t.Run(protocol, func(t *testing.T) {
			s := Spec{ID: 1, Tag: "exit", Protocol: protocol, Host: "proxy.example", Port: 443, Username: "alice", Password: "p@ss:\"word", Transport: "tcp"}
			if err := s.Validate(); err != nil {
				t.Fatal(err)
			}
			b, err := s.Render(32123)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "0.0.0.0") || strings.Contains(string(b), "skip_verification = true") {
				t.Fatalf("unsafe listener or TLS setting: %s", b)
			}
			if protocol == "trusttunnel" {
				for _, want := range []string{`address = "127.0.0.1:32123"`, `skip_verification = false`, `upstream_protocol = "http2"`, `killswitch_enabled = true`} {
					if !strings.Contains(string(b), want) {
						t.Fatalf("missing %q in %s", want, b)
					}
				}
				return
			}
			var cfg map[string]any
			if err := json.Unmarshal(b, &cfg); err != nil {
				t.Fatal(err)
			}
			if protocol == "naive" && (cfg["listen"] != "socks://127.0.0.1:32123" || cfg["proxy"] != "https://alice:p%40ss%3A%22word@proxy.example:443") {
				t.Fatalf("unexpected naive config %s", b)
			}
			if protocol == "mieru" && (cfg["socks5Port"] != float64(32123) || cfg["rpcPort"] != float64(0) || cfg["socks5ListenLAN"] != false) {
				t.Fatalf("unexpected mieru listeners: %s", b)
			}
		})
	}
}

func TestConfigRejectsMalformedEndpointsAndCredentials(t *testing.T) {
	base := Spec{Tag: "exit", Protocol: "naive", Host: "proxy.example", Port: 443, Username: "alice", Password: "secret", Transport: "tcp"}
	for _, tc := range []struct {
		name   string
		mutate func(*Spec)
		want   string
	}{
		{"unknown", func(s *Spec) { s.Protocol = "shell" }, "unsupported protocol"},
		{"url", func(s *Spec) { s.Host = "https://proxy.example/path" }, "invalid server host"},
		{"port", func(s *Spec) { s.Port = 65536 }, "port must be between 1 and 65535"},
		{"control", func(s *Spec) { s.Password = "secret\nlog" }, "credentials contain control characters"},
		{"tag", func(s *Spec) { s.Tag = "exit space" }, "invalid outbound tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.mutate(&s)
			if err := s.Validate(); err == nil || err.Error() != tc.want {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
