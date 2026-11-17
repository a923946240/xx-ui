package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/a923946240/xx-ui/v3/internal/database/model"
	"github.com/a923946240/xx-ui/v3/internal/util/netsafe"
)

const (
	natNodeChallengePath = "/panel/api/nodes/nat/challenge"
	natNodeObservePath   = "/panel/api/nodes/nat/observe"
	natNodeReportPath    = "/panel/api/nodes/nat/report"
	natNodeResultPath    = "/panel/api/nodes/nat/result"
)

type NATNodeChallenge struct {
	Challenge string `json:"challenge"`
}

type NATNodeObserveRequest struct {
	RelayURL         string `json:"relayUrl"`
	Challenge        string `json:"challenge"`
	PinnedCertSHA256 string `json:"pinnedCertSha256,omitempty"`
}

type NATNodeReport struct {
	CandidateIP string `json:"candidateIp"`
	SeenAt      int64  `json:"seenAt"`
}

func ValidNATNodeChallenge(challenge string) bool {
	if len(challenge) != base64.RawURLEncoding.EncodedLen(32) {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(challenge)
	return err == nil && len(raw) == 32
}

func (r *Remote) requireTrustedNATNode() error {
	if r.node.Scheme != "" && r.node.Scheme != "https" {
		return errors.New("NAT sync requires HTTPS on both nodes")
	}
	switch r.node.TlsVerifyMode {
	case "", "verify", "mtls":
		return nil
	case "pin":
		_, err := DecodeCertPin(r.node.PinnedCertSha256)
		return err
	default:
		return errors.New("NAT sync requires verified HTTPS or a pinned node certificate")
	}
}

func (r *Remote) NATObservationTarget(challenge string) (NATNodeObserveRequest, error) {
	if err := r.requireTrustedNATNode(); err != nil {
		return NATNodeObserveRequest{}, err
	}
	base, err := r.baseURL()
	if err != nil {
		return NATNodeObserveRequest{}, err
	}
	req := NATNodeObserveRequest{
		RelayURL:  base + strings.TrimPrefix(natNodeReportPath, "/"),
		Challenge: challenge,
	}
	if r.node.TlsVerifyMode == "pin" {
		req.PinnedCertSHA256 = r.node.PinnedCertSha256
	}
	return req, validateNATObservationTarget(req)
}

func (r *Remote) natNodePost(ctx context.Context, endpoint string, payload any) (*envelope, error) {
	if err := r.requireTrustedNATNode(); err != nil {
		return nil, err
	}
	return r.doRequest(ctx, http.MethodPost, endpoint, payload, true)
}

func (r *Remote) NATChallenge(ctx context.Context) (NATNodeChallenge, error) {
	var result NATNodeChallenge
	env, err := r.natNodePost(ctx, natNodeChallengePath, nil)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(env.Obj, &result); err != nil || !ValidNATNodeChallenge(result.Challenge) {
		return NATNodeChallenge{}, errors.New("invalid NAT challenge from relay node")
	}
	return result, nil
}

func (r *Remote) NATObserve(ctx context.Context, req NATNodeObserveRequest) error {
	if err := validateNATObservationTarget(req); err != nil {
		return err
	}
	_, err := r.natNodePost(ctx, natNodeObservePath, req)
	return err
}

func (r *Remote) NATResult(ctx context.Context, req NATNodeChallenge) (NATNodeReport, error) {
	var report NATNodeReport
	if !ValidNATNodeChallenge(req.Challenge) {
		return report, errors.New("invalid NAT sync challenge")
	}
	env, err := r.natNodePost(ctx, natNodeResultPath, req)
	if err != nil {
		return report, err
	}
	if err := json.Unmarshal(env.Obj, &report); err != nil || report.SeenAt <= 0 {
		return NATNodeReport{}, errors.New("invalid NAT result from relay node")
	}
	addr, err := netip.ParseAddr(report.CandidateIP)
	if err != nil || !IsPublicNATIPv4(addr) {
		return NATNodeReport{}, errors.New("NAT relay did not report a public IPv4 address")
	}
	return report, nil
}

func validateNATObservationTarget(req NATNodeObserveRequest) error {
	if !ValidNATNodeChallenge(req.Challenge) {
		return errors.New("invalid NAT sync challenge")
	}
	if len(req.RelayURL) > 2048 {
		return errors.New("invalid NAT relay report URL")
	}
	target, err := url.Parse(req.RelayURL)
	if err != nil || target.Scheme != "https" || target.Opaque != "" || target.User != nil ||
		target.RawQuery != "" || target.ForceQuery || strings.Contains(req.RelayURL, "#") ||
		target.RawPath != "" || target.Path != path.Clean(target.Path) ||
		!strings.HasSuffix(target.Path, natNodeReportPath) {
		return errors.New("invalid NAT relay report URL")
	}
	host, err := netsafe.NormalizeHost(target.Hostname())
	if err != nil {
		return errors.New("invalid NAT relay report host")
	}
	if addr, err := netip.ParseAddr(host); err == nil && !IsPublicNATIPv4(addr) {
		return errors.New("NAT relay report requires a public IPv4 address")
	}
	if port := target.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("invalid NAT relay report port")
		}
	}
	if req.PinnedCertSHA256 != "" {
		if _, err := DecodeCertPin(req.PinnedCertSHA256); err != nil {
			return err
		}
	}
	return nil
}

// SendNATObservation contacts the relay directly from the reporter over IPv4.
// Only the one-time challenge crosses this connection.
func SendNATObservation(ctx context.Context, req NATNodeObserveRequest) error {
	if err := validateNATObservationTarget(req); err != nil {
		return err
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if req.PinnedCertSHA256 != "" {
		cfg, err := tlsConfigForNode(&model.Node{TlsVerifyMode: "pin", PinnedCertSha256: req.PinnedCertSHA256})
		if err != nil {
			return err
		}
		cfg.MinVersion = tls.VersionTLS12
		tlsCfg = cfg
	}
	transport := &http.Transport{
		DialContext:            dialPublicNATRelayIPv4,
		TLSClientConfig:        tlsCfg,
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 8 << 10,
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       6 * time.Second,
		CheckRedirect: stopNodeRedirect,
	}
	// A validated challenge consists of URL-safe ASCII and still goes through
	// the JSON encoder so both ends share the wire type.
	payload, err := json.Marshal(NATNodeChallenge{Challenge: req.Challenge})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, req.RelayURL, bytes.NewReader(payload))
	if err != nil {
		return errors.New("could not create NAT relay report request")
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(request)
	if err != nil {
		return errors.New("could not reach the NAT relay directly over verified HTTPS and IPv4")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("NAT relay rejected the report (HTTP %d)", resp.StatusCode)
	}
	return nil
}

func dialPublicNATRelayIPv4(ctx context.Context, _, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		return nil, err
	}
	ctx = netsafe.ContextWithAllowPrivate(ctx, false)
	for _, ip := range ips {
		ip = ip.Unmap()
		if !IsPublicNATIPv4(ip) {
			continue
		}
		// Dial the numeric address already checked above, never re-resolve the hostname.
		conn, err := netsafe.SSRFGuardedDialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("NAT relay requires a reachable public IPv4 address")
}

var nonPublicNATIPv4 = [...]netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

func IsPublicNATIPv4(addr netip.Addr) bool {
	if !addr.Is4() || !addr.IsGlobalUnicast() || netsafe.IsBlockedIP(net.IP(addr.AsSlice())) {
		return false
	}
	if addr == netip.AddrFrom4([4]byte{192, 0, 0, 9}) || addr == netip.AddrFrom4([4]byte{192, 0, 0, 10}) {
		return true
	}
	for _, prefix := range nonPublicNATIPv4 {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
