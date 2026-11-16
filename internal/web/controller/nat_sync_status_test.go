package controller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadNATSyncStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	if readNATSyncStatus(path) != nil {
		t.Fatal("a missing status file should be omitted from the panel response")
	}
	save := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	save(`{"state":"probe_error","currentIp":"1.1.1.1","candidateIp":"8.8.8.8","probeIp":"8.8.8.8","probeResult":"failed","probeAt":10,"appliedAt":1,"updatedAt":11,"extra":"must-stay-local"}`)
	status := readNATSyncStatus(path)
	if status == nil || status.CurrentIP != "1.1.1.1" || status.CandidateIP != "8.8.8.8" ||
		status.ProbeResult != "failed" || status.AppliedAt != 1 {
		t.Fatalf("unexpected projected status: %+v", status)
	}
	body, err := json.Marshal(status)
	if err != nil || strings.Contains(string(body), "must-stay-local") {
		t.Fatalf("status projection failed: %v", err)
	}
	for _, invalid := range []string{
		`{"state":"in_sync","currentIp":"invalid","updatedAt":11}`,
		`{"state":"unknown","updatedAt":11}`,
		strings.Repeat(" ", 4097),
	} {
		save(invalid)
		if readNATSyncStatus(path) != nil {
			t.Fatal("invalid status file was accepted")
		}
	}
}
