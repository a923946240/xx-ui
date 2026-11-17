package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/a923946240/xx-ui/v3/internal/database/model"
)

const testNATChallenge = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestNATObservationTargetReusesNodeTLSAndBasePath(t *testing.T) {
	const certPin = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	relay := &model.Node{
		Scheme: "https", Address: "receiver.example.com", Port: 8443, BasePath: "/secret-panel",
		TlsVerifyMode: "pin", PinnedCertSha256: certPin, ApiToken: "test-token",
	}
	remote := NewRemote(relay, nil)
	target, err := remote.NATObservationTarget(testNATChallenge)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://receiver.example.com:8443/secret-panel/panel/api/nodes/nat/report"; target.RelayURL != want {
		t.Fatalf("relay URL = %q, want %q", target.RelayURL, want)
	}
	if target.PinnedCertSHA256 != certPin || target.Challenge != testNATChallenge {
		t.Fatal("reporter lost the receiver certificate pin or challenge")
	}
	relay.TlsVerifyMode = "skip"
	if _, err := remote.NATObservationTarget(testNATChallenge); err == nil {
		t.Fatal("accepted unverified TLS for the relay")
	}
}

func TestNATObserveNeverFollowsRedirect(t *testing.T) {
	var forwarded atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/panel/api/nodes/nat/observe" {
			http.Redirect(w, req, "/redirected", http.StatusTemporaryRedirect)
			return
		}
		forwarded.Store(true)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	sha := sha256.Sum256(srv.Certificate().Raw)
	node := nodeForServer(t, srv, "pin", hex.EncodeToString(sha[:]))
	node.ApiToken = "test-token"
	remote := NewRemote(node, nil)

	err := remote.NATObserve(context.Background(), NATNodeObserveRequest{
		RelayURL: "https://receiver.example.com/panel/api/nodes/nat/report", Challenge: testNATChallenge,
	})
	if err == nil || forwarded.Load() {
		t.Fatalf("redirect handled: error=%v, second request=%v", err, forwarded.Load())
	}
}

func TestSendNATObservationRejectsUnsafeDestinations(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	for _, url := range []string{
		srv.URL + "/panel/api/nodes/nat/report",
		"https://169.254.169.254/panel/api/nodes/nat/report",
		"https://receiver.example.com/panel/api/nodes/nat/observe",
		"https://receiver.example.com/ignored/../panel/api/nodes/nat/report",
		"https://receiver.example.com/panel/api/nodes/nat/report?redirect=/anything",
		"http://receiver.example.com/panel/api/nodes/nat/report",
	} {
		t.Run(url, func(t *testing.T) {
			if err := SendNATObservation(context.Background(), NATNodeObserveRequest{RelayURL: url, Challenge: testNATChallenge}); err == nil {
				t.Fatal("accepted unsafe relay target")
			}
		})
	}
	if called.Load() {
		t.Fatal("relay reporter contacted a loopback listener")
	}
}

func TestNATPublicIPv4AndChallengeValidation(t *testing.T) {
	for _, addr := range []string{"0.1.2.3", "10.0.0.2", "100.64.0.2", "127.0.0.1", "169.254.169.254", "192.0.2.8", "192.168.1.2", "198.18.0.1", "203.0.113.4", "224.0.0.5", "255.255.255.255", "2001:4860:4860::8888"} {
		if IsPublicNATIPv4(netip.MustParseAddr(addr)) {
			t.Errorf("accepted non-public IPv4 %q", addr)
		}
	}
	if !IsPublicNATIPv4(netip.MustParseAddr("8.8.4.4")) {
		t.Fatal("rejected public IPv4")
	}
	if !ValidNATNodeChallenge(testNATChallenge) {
		t.Fatal("rejected challenge encoding")
	}
	for _, challenge := range []string{"", testNATChallenge + "=", strings.Repeat("A", 42) + "B", strings.Repeat("A", 44)} {
		if ValidNATNodeChallenge(challenge) {
			t.Errorf("accepted invalid challenge encoding %q", challenge)
		}
	}
}

func TestNATNodeRPCUsesVerifiedPanelRoutes(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("node RPC did not use the configured node authentication")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch req.URL.Path {
		case "/panel/api/nodes/nat/challenge":
			_, _ = w.Write([]byte(`{"success":true,"obj":{"challenge":"` + testNATChallenge + `"}}`))
		case "/panel/api/nodes/nat/observe":
			var observation NATNodeObserveRequest
			if err := json.NewDecoder(req.Body).Decode(&observation); err != nil ||
				observation.RelayURL != "https://receiver.example.com/panel/api/nodes/nat/report" ||
				observation.Challenge != testNATChallenge {
				t.Error("reporter RPC did not receive the one-time observation request")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"success":true}`))
		case "/panel/api/nodes/nat/result":
			var challenge NATNodeChallenge
			if err := json.NewDecoder(req.Body).Decode(&challenge); err != nil || challenge.Challenge != testNATChallenge {
				t.Error("result RPC did not receive the original challenge")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"obj":{"candidateIp":"8.8.4.4","seenAt":1790000000}}`))
		default:
			t.Errorf("unexpected node endpoint: %s", req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	sha := sha256.Sum256(srv.Certificate().Raw)
	node := nodeForServer(t, srv, "pin", hex.EncodeToString(sha[:]))
	node.ApiToken = "test-token"
	remote := NewRemote(node, nil)
	ctx := context.Background()

	challenge, err := remote.NATChallenge(ctx)
	if err != nil || challenge.Challenge != testNATChallenge {
		t.Fatalf("node challenge: error=%v", err)
	}
	if err := remote.NATObserve(ctx, NATNodeObserveRequest{
		RelayURL: "https://receiver.example.com/panel/api/nodes/nat/report", Challenge: challenge.Challenge,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := remote.NATResult(ctx, challenge)
	if err != nil || result.CandidateIP != "8.8.4.4" || result.SeenAt != 1790000000 {
		t.Fatalf("node result: error=%v, result=%+v", err, result)
	}
}
