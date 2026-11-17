package service

import (
	"net/netip"
	"testing"
	"time"
)

func TestNATNodeChallengeSingleUse(t *testing.T) {
	var store natNodeChallengeStore
	now := time.Unix(1700000000, 0)
	challenge, err := store.issue(now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.result(challenge.Challenge, now); err == nil {
		t.Fatal("result was available before the reporter arrived")
	}
	if store.record(challenge.Challenge, netip.MustParseAddr("192.168.1.5"), now) {
		t.Fatal("private address accepted as a report")
	}

	observedAt := now.Add(time.Second)
	if !store.record(challenge.Challenge, netip.MustParseAddr("1.1.1.1"), observedAt) {
		t.Fatal("first valid report was rejected")
	}
	if store.record(challenge.Challenge, netip.MustParseAddr("8.8.8.8"), now.Add(2*time.Second)) {
		t.Fatal("a repeated report replaced the observed address")
	}
	report, err := store.result(challenge.Challenge, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if report.CandidateIP != "1.1.1.1" || report.SeenAt != observedAt.Unix() {
		t.Fatalf("wrong report: %+v", report)
	}
	if _, err := store.result(challenge.Challenge, now.Add(3*time.Second)); err == nil {
		t.Fatal("a consumed report was returned again")
	}
}

func TestNATNodeChallengeExpiresAndReleasesCapacity(t *testing.T) {
	var store natNodeChallengeStore
	now := time.Unix(1700000000, 0)
	reported, err := store.issue(now)
	if err != nil {
		t.Fatal(err)
	}
	if !store.record(reported.Challenge, netip.MustParseAddr("1.1.1.1"), now) {
		t.Fatal("report was rejected")
	}
	unreported, err := store.issue(now)
	if err != nil {
		t.Fatal(err)
	}
	for i := 2; i < natNodeChallengeLimit; i++ {
		if _, err := store.issue(now); err != nil {
			t.Fatalf("issue challenge %d within capacity: %v", i, err)
		}
	}
	if _, err := store.issue(now); err == nil {
		t.Fatal("issuing beyond capacity succeeded")
	}
	later := now.Add(time.Minute)
	if _, err := store.result(reported.Challenge, later); err == nil {
		t.Fatal("an expired report was returned")
	}
	if store.record(unreported.Challenge, netip.MustParseAddr("1.1.1.1"), later) {
		t.Fatal("an expired challenge accepted a report")
	}
	if _, err := store.issue(later); err != nil {
		t.Fatalf("expired entries did not release capacity: %v", err)
	}
}
