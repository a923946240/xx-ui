package runtime

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"

	"github.com/a923946240/xx-ui/v3/internal/database/model"
)

const (
	natPoolPath       = "/panel/api/nodes/nat/pool"
	natPoolReportPath = natPoolPath + "/report"
	NATPoolLimit      = 128
)

type NATPoolReport struct {
	ReporterID string `json:"reporterId"`
	Name       string `json:"name"`
}

type NATPoolEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CandidateIP string `json:"candidateIp"`
	SeenAt      int64  `json:"seenAt"`
}

func ValidNATPoolEntryID(id string) bool {
	check, err := hex.DecodeString(id)
	return err == nil && len(check) == 32
}

func validNATPoolEntry(entry NATPoolEntry) bool {
	ip, err := netip.ParseAddr(entry.CandidateIP)
	return err == nil && IsPublicNATIPv4(ip) && ValidNATPoolEntryID(entry.ID) &&
		entry.SeenAt > 0 && len(entry.Name) <= 96
}

func (r *Remote) NATPool(ctx context.Context) ([]NATPoolEntry, error) {
	if err := r.requireTrustedNATNode(); err != nil {
		return nil, err
	}
	env, err := r.doRequest(ctx, http.MethodGet, natPoolPath, nil, true)
	if err != nil {
		return nil, err
	}
	var rows []NATPoolEntry
	if json.Unmarshal(env.Obj, &rows) != nil || rows == nil || len(rows) > NATPoolLimit {
		return nil, errors.New("invalid NAT address pool")
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if !validNATPoolEntry(row) || seen[row.ID] {
			return nil, errors.New("invalid NAT address pool entry")
		}
		seen[row.ID] = true
	}
	return rows, nil
}

func PushNATPool(ctx context.Context, node *model.Node, report NATPoolReport) (NATPoolEntry, error) {
	remote := NewRemote(node, nil)
	if err := remote.requireTrustedNATNode(); err != nil {
		return NATPoolEntry{}, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if node.TlsVerifyMode == "pin" || node.TlsVerifyMode == "mtls" {
		var err error
		tlsConfig, err = tlsConfigForNode(node)
		if err != nil {
			return NATPoolEntry{}, err
		}
		tlsConfig.MinVersion = tls.VersionTLS12
	}
	remote.clientOnce.Do(func() {
		remote.client = &http.Client{Transport: &http.Transport{
			DialContext: dialPublicNATRelayIPv4, TLSClientConfig: tlsConfig,
			DisableKeepAlives: true, MaxResponseHeaderBytes: 8 << 10,
		}}
	})
	env, err := remote.natNodePost(ctx, natPoolReportPath, report)
	if err != nil {
		return NATPoolEntry{}, err
	}
	var entry NATPoolEntry
	if json.Unmarshal(env.Obj, &entry) != nil || !validNATPoolEntry(entry) {
		return NATPoolEntry{}, errors.New("invalid NAT pool report receipt")
	}
	return entry, nil
}
