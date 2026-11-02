package externalproxy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTrustTunnelRejectsLocalBypassTargets(t *testing.T) {
	r := &runner{spec: Spec{Protocol: "trusttunnel", Enable: true}, state: "running"}
	for _, address := range []string{"127.0.0.1:80", "127.2.3.4:80", "[::1]:80", "[::ffff:127.0.0.1]:80", "0.0.0.0:80", "[::]:80", "localhost:80", "a.localhost.:80"} {
		if _, err := r.dial(context.Background(), address); err == nil || !strings.Contains(err.Error(), "local bypass target") {
			t.Errorf("TrustTunnel did not reject %s before connecting: %v", address, err)
		}
	}
}

func TestClientLogRedactsEscapedSecretsAcrossWrites(t *testing.T) {
	s := Spec{Username: "alice", Password: `ab"cd\ef<&`}
	b, _ := json.Marshal(s.Password)
	escaped := string(b[1 : len(b)-1])
	l := newLogBuffer(s)
	line := `password="` + escaped + `"` + "\n"
	for _, c := range []byte(line) {
		_, _ = l.Write([]byte{c})
	}
	if got := l.String(); strings.Contains(got, escaped) || !strings.Contains(got, "[redacted]") {
		t.Fatalf("escaped secret was not redacted: %q", got)
	}
}
