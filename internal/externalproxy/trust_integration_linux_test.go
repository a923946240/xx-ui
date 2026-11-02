//go:build linux

package externalproxy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOfficialTrustTunnelDisconnectDoesNotBypass(t *testing.T) {
	bin, endpoint := os.Getenv("EXTERNAL_CLIENT_BIN_DIR"), os.Getenv("EXTERNAL_TRUST_ENDPOINT_BINARY")
	if bin == "" || endpoint == "" {
		t.Skip("set pinned TrustTunnel client and endpoint fixture binaries")
	}
	ip := fixtureNonLoopbackIP(t)
	dir := t.TempDir()
	cert, key := fixtureCertificate(t)
	for name, data := range map[string][]byte{"cert.pem": cert, "key.pem": key} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	port := fixturePort(t)
	vpn := fmt.Sprintf("listen_address = %q\nallow_private_network_connections = true\ncredentials_file = %q\n[listen_protocols.http2]\n[forward_protocol]\ndirect = {}\n", fmt.Sprintf("127.0.0.1:%d", port), filepath.Join(dir, "credentials.toml"))
	hosts := fmt.Sprintf("[[main_hosts]]\nhostname = %q\ncert_chain_path = %q\nprivate_key_path = %q\n", "fixture.test", filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	for name, data := range map[string]string{"vpn.toml": vpn, "hosts.toml": hosts, "credentials.toml": "[[client]]\nusername = \"fixture\"\npassword = \"synthetic-fixture-password\"\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	server := exec.Command(endpoint, filepath.Join(dir, "vpn.toml"), filepath.Join(dir, "hosts.toml"), "--jobs", "1")
	startFixture(t, server)
	waitFixturePort(t, port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &runner{ctx: ctx, spec: Spec{ID: 1, Tag: "trust-fixture", Protocol: "trusttunnel", Host: "127.0.0.1", ServerName: "fixture.test", Port: port, Username: "fixture", Password: "synthetic-fixture-password", Enable: true}, binary: filepath.Join(bin, "trusttunnel"), dir: filepath.Join(dir, "client")}
	r.logs = newLogBuffer(r.spec)
	clientCommand, err := r.command()
	if err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(r.dir, "client.toml")
	config, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	// Fixture certificate pin preserves TLS verification without changing the host CA store.
	config = []byte(strings.Replace(string(config), "[endpoint]\n", "[endpoint]\ncertificate = '''"+string(cert)+"'''\n", 1))
	if err := os.WriteFile(configFile, config, 0o600); err != nil {
		t.Fatal(err)
	}
	startFixture(t, clientCommand)
	r.pid = clientCommand.Process.Pid
	for deadline := time.Now().Add(10 * time.Second); !ownsListener(r.pid, r.backend) && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if !ownsListener(r.pid, r.backend) {
		t.Fatalf("TrustTunnel listener unavailable: %s", r.logs.String())
	}
	r.state = "running"
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(r.logs.String(), "VPN_SS_CONNECTED") && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(r.logs.String(), "VPN_SS_CONNECTED") {
		t.Fatalf("TrustTunnel endpoint did not connect: %s", r.logs.String())
	}
	var reached atomic.Int64
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		_, _ = io.WriteString(w, "through-trusttunnel")
	}))
	l, err := net.Listen("tcp4", net.JoinHostPort(ip, "0"))
	if err != nil {
		t.Fatal(err)
	}
	_ = target.Listener.Close()
	target.Listener = l
	target.StartTLS()
	defer target.Close()
	transport := target.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.TLSClientConfig.ServerName = target.Certificate().DNSNames[0]
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) { return r.dial(ctx, address) }
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("official TrustTunnel HTTPS fixture: %v; client logs: %s", err, r.logs.String())
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "through-trusttunnel" || reached.Load() != 1 {
		t.Fatalf("unexpected response %q err=%v requests=%d", body, err, reached.Load())
	}
	if err := server.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if response, err := client.Get(target.URL); err == nil {
			response.Body.Close()
			t.Fatal("disconnected TrustTunnel bypassed the endpoint")
		}
	}
	if reached.Load() != 1 {
		t.Fatal("disconnected TrustTunnel reached the destination directly")
	}
}

func fixtureNonLoopbackIP(t *testing.T) string {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range addresses {
		if a, ok := address.(*net.IPNet); ok && a.IP.To4() != nil && !a.IP.IsLoopback() && !a.IP.IsUnspecified() {
			return a.IP.String()
		}
	}
	t.Skip("fixture needs a non-loopback IPv4 interface")
	return ""
}

func fixtureCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture.test"}, DNSNames: []string{"fixture.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}
