package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/a923946240/xx-ui/v3/internal/database"
	"github.com/a923946240/xx-ui/v3/internal/web/runtime"
)

const (
	natPoolKey       = "natAddressPool"
	natPoolReportKey = "natAddressReporting"
	natPoolFreshness = 2 * time.Minute
	natPoolRetention = 24 * time.Hour
)

type natPoolRecord struct {
	ReporterID string
	Entry      runtime.NATPoolEntry
}

var natPoolMu sync.Mutex

func loadNATPool() (map[string]natPoolRecord, error) {
	rows := make(map[string]natPoolRecord)
	setting, err := (&SettingService{}).getSetting(natPoolKey)
	if database.IsNotFound(err) {
		return rows, nil
	}
	if err != nil || json.Unmarshal([]byte(setting.Value), &rows) != nil || rows == nil {
		return nil, errors.New("could not read the NAT address pool")
	}
	return rows, nil
}

func validNATReportName(name string) bool {
	return len(name) <= 96 && strings.IndexFunc(name, unicode.IsControl) < 0
}

func RecordNATPoolReport(principal, defaultName string, report runtime.NATPoolReport, peer netip.Addr) (runtime.NATPoolEntry, error) {
	if strings.TrimSpace(report.Name) == "" {
		report.Name = defaultName
	}
	return recordNATPoolReport(principal, report, peer, time.Now())
}

func recordNATPoolReport(principal string, report runtime.NATPoolReport, peer netip.Addr, now time.Time) (runtime.NATPoolEntry, error) {
	id, err := uuid.Parse(report.ReporterID)
	name := strings.TrimSpace(report.Name)
	peer = peer.Unmap()
	if principal == "" || err != nil || id == uuid.Nil || !validNATReportName(name) || !runtime.IsPublicNATIPv4(peer) {
		return runtime.NATPoolEntry{}, errors.New("invalid NAT address report")
	}
	if name == "" {
		name = id.String()[:8]
	}
	owner := sha256.Sum256([]byte(principal))
	key := hex.EncodeToString(owner[:])
	entryID := sha256.Sum256([]byte(key + ":" + id.String()))
	entry := runtime.NATPoolEntry{
		ID: hex.EncodeToString(entryID[:]), Name: name, CandidateIP: peer.String(), SeenAt: now.Unix(),
	}

	natPoolMu.Lock()
	defer natPoolMu.Unlock()
	rows, err := loadNATPool()
	if err != nil {
		return runtime.NATPoolEntry{}, err
	}
	for currentOwner, row := range rows {
		if now.Sub(time.Unix(row.Entry.SeenAt, 0)) > natPoolRetention {
			delete(rows, currentOwner)
			continue
		}
		if (currentOwner == key) != (row.ReporterID == id.String()) {
			return runtime.NATPoolEntry{}, errors.New("the reporting credential is already bound to a different node")
		}
	}
	if _, exists := rows[key]; !exists && len(rows) >= runtime.NATPoolLimit {
		return runtime.NATPoolEntry{}, errors.New("the NAT address pool is full")
	}
	if previous, exists := rows[key]; exists && previous.Entry.SeenAt > entry.SeenAt {
		return previous.Entry, nil
	}
	rows[key] = natPoolRecord{ReporterID: id.String(), Entry: entry}
	data, err := json.Marshal(rows)
	if err != nil {
		return runtime.NATPoolEntry{}, err
	}
	if err := (&SettingService{}).saveSetting(natPoolKey, string(data)); err != nil {
		return runtime.NATPoolEntry{}, errors.New("could not save the NAT address report")
	}
	return entry, nil
}

func ListNATPool() ([]runtime.NATPoolEntry, error) {
	return listNATPool(time.Now())
}

func listNATPool(now time.Time) ([]runtime.NATPoolEntry, error) {
	rows, err := loadNATPool()
	if err != nil {
		return nil, err
	}
	entries := make([]runtime.NATPoolEntry, 0, len(rows))
	for _, row := range rows {
		if freshNATPoolEntry(row.Entry, now) {
			entries = append(entries, row.Entry)
		}
	}
	slices.SortFunc(entries, func(a, b runtime.NATPoolEntry) int {
		if a.Name != b.Name {
			return strings.Compare(a.Name, b.Name)
		}
		return strings.Compare(a.ID, b.ID)
	})
	return entries, nil
}

func freshNATPoolEntry(entry runtime.NATPoolEntry, now time.Time) bool {
	age := now.Sub(time.Unix(entry.SeenAt, 0))
	return age >= -time.Minute && age <= natPoolFreshness
}

func ReadRemoteNATPool(ctx context.Context, relayID int) ([]runtime.NATPoolEntry, error) {
	node, err := natSyncNode(relayID)
	if err != nil {
		return nil, err
	}
	manager := runtime.GetManager()
	if manager == nil {
		return nil, errors.New("the node connection manager is unavailable")
	}
	remote, err := manager.RemoteFor(node)
	if err != nil {
		return nil, errors.New("cannot load the address pool node connection")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := remote.NATPool(ctx)
	if err != nil {
		return nil, errors.New("could not read the node's NAT address pool; check its version and permissions")
	}
	now := time.Now()
	return slices.DeleteFunc(rows, func(entry runtime.NATPoolEntry) bool {
		return !freshNATPoolEntry(entry, now)
	}), nil
}
