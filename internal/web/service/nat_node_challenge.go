package service

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/netip"
	"sync"
	"time"

	"github.com/a923946240/xx-ui/v3/internal/web/runtime"
)

const (
	natNodeChallengeLifetime = 30 * time.Second
	natNodeChallengeLimit    = 128
)

var (
	natNodeChallenges       natNodeChallengeStore
	errNATResultUnavailable = errors.New("NAT observation is unavailable or expired")
)

type natNodeChallengeEntry struct {
	expiresAt time.Time
	report    runtime.NATNodeReport
}

// natNodeChallengeStore accepts one observation and one retrieval per short-lived challenge.
type natNodeChallengeStore struct {
	mu      sync.Mutex
	entries map[string]natNodeChallengeEntry
}

func IssueNATNodeChallenge() (runtime.NATNodeChallenge, error) {
	return natNodeChallenges.issue(time.Now())
}

func RecordNATNodeReport(challenge string, peer netip.Addr) bool {
	return natNodeChallenges.record(challenge, peer.Unmap(), time.Now())
}

func TakeNATNodeResult(challenge string) (runtime.NATNodeReport, error) {
	return natNodeChallenges.result(challenge, time.Now())
}

func (s *natNodeChallengeStore) issue(now time.Time) (runtime.NATNodeChallenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for challenge, entry := range s.entries {
		if !now.Before(entry.expiresAt) {
			delete(s.entries, challenge)
		}
	}
	if len(s.entries) >= natNodeChallengeLimit {
		return runtime.NATNodeChallenge{}, errors.New("too many pending NAT observations")
	}

	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return runtime.NATNodeChallenge{}, errors.New("could not create NAT observation challenge")
	}
	challenge := base64.RawURLEncoding.EncodeToString(secret[:])
	if _, exists := s.entries[challenge]; exists {
		return runtime.NATNodeChallenge{}, errors.New("could not create NAT observation challenge")
	}
	if s.entries == nil {
		s.entries = make(map[string]natNodeChallengeEntry)
	}
	s.entries[challenge] = natNodeChallengeEntry{expiresAt: now.Add(natNodeChallengeLifetime)}
	return runtime.NATNodeChallenge{Challenge: challenge}, nil
}

func (s *natNodeChallengeStore) record(challenge string, peer netip.Addr, now time.Time) bool {
	if !runtime.IsPublicNATIPv4(peer) {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[challenge]
	if !exists || !now.Before(entry.expiresAt) || entry.report.CandidateIP != "" {
		return false
	}
	entry.report = runtime.NATNodeReport{CandidateIP: peer.String(), SeenAt: now.Unix()}
	s.entries[challenge] = entry
	return true
}

func (s *natNodeChallengeStore) result(challenge string, now time.Time) (runtime.NATNodeReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[challenge]
	if !exists || !now.Before(entry.expiresAt) || entry.report.CandidateIP == "" {
		return runtime.NATNodeReport{}, errNATResultUnavailable
	}
	delete(s.entries, challenge)
	return entry.report, nil
}
