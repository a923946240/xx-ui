package service

import (
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/a923946240/xx-ui/v3/internal/database/dbtest"
	"github.com/a923946240/xx-ui/v3/internal/web/runtime"
)

func TestNATPoolBindsReporterAndOnlyListsFreshObservedIPs(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	now := time.Unix(1800000000, 0)
	report := runtime.NATPoolReport{ReporterID: "0cbdb040-4896-4c84-9047-baca1f432875", Name: "Taiwan"}
	first, err := recordNATPoolReport("token:taiwan", report, netip.MustParseAddr("1.1.1.1"), now)
	if err != nil {
		t.Fatal(err)
	}
	next, err := recordNATPoolReport("token:taiwan", report, netip.MustParseAddr("8.8.8.8"), now.Add(time.Second))
	if err != nil || next.ID != first.ID || next.CandidateIP != "8.8.8.8" {
		t.Fatalf("a new observation must update the same source: %+v, %v", next, err)
	}
	other := runtime.NATPoolReport{ReporterID: "3ac2f382-7306-48be-9392-48defba17378", Name: "Other"}
	if _, err := recordNATPoolReport("token:taiwan", other, netip.MustParseAddr("4.2.2.2"), now); err == nil {
		t.Fatal("the same credential could write a different reporter")
	}
	if _, err := recordNATPoolReport("token:other", report, netip.MustParseAddr("4.2.2.2"), now); err == nil {
		t.Fatal("another credential could claim the same reporter")
	}
	if _, err := recordNATPoolReport("token:other", other, netip.MustParseAddr("192.168.0.5"), now); err == nil {
		t.Fatal("private peer was accepted")
	}
	listed, err := listNATPool(now.Add(2 * time.Second))
	if err != nil || len(listed) != 1 || listed[0] != next {
		t.Fatalf("wrong fresh pool contents: %+v, %v", listed, err)
	}
	listed, err = listNATPool(now.Add(5 * time.Minute))
	if err != nil || len(listed) != 0 {
		t.Fatalf("stale entry remained selectable: %+v, %v", listed, err)
	}
}
