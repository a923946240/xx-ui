package controller

import (
	"encoding/json"
	"io"
	"net/netip"
	"os"
)

const natSyncStatusPath = "/var/lib/xx-ui/nat-sync/status.json"

type natSyncStatus struct {
	State       string `json:"state"`
	CurrentIP   string `json:"currentIp,omitempty"`
	CandidateIP string `json:"candidateIp,omitempty"`
	ProbeIP     string `json:"probeIp,omitempty"`
	ProbeResult string `json:"probeResult,omitempty"`
	ProbeAt     int64  `json:"probeAt,omitempty"`
	AppliedAt   int64  `json:"appliedAt,omitempty"`
	UpdatedAt   int64  `json:"updatedAt"`
}

func readNATSyncStatus(path string) *natSyncStatus {
	const maxSize = 4096
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSize {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil || len(body) > maxSize {
		return nil
	}
	var status natSyncStatus
	if json.Unmarshal(body, &status) != nil || status.UpdatedAt <= 0 ||
		status.ProbeAt < 0 || status.AppliedAt < 0 {
		return nil
	}
	switch status.State {
	case "in_sync", "relay_error", "config_error", "probe_error", "candidate_changed", "apply_error":
	default:
		return nil
	}
	switch status.ProbeResult {
	case "", "passed", "failed":
	default:
		return nil
	}
	for _, value := range []string{status.CurrentIP, status.CandidateIP, status.ProbeIP} {
		if value == "" {
			continue
		}
		ip, err := netip.ParseAddr(value)
		if err != nil || !ip.Is4() {
			return nil
		}
	}
	return &status
}
