package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/a923946240/xx-ui/v3/internal/database"
)

const natSyncSettingKey = "natSSHSync"

type NATSyncConfig struct {
	Enabled            bool     `json:"enabled"`
	RelayHost          string   `json:"relayHost"`
	RelayPort          int      `json:"relayPort"`
	RelayUser          string   `json:"relayUser"`
	HostKeyFingerprint string   `json:"hostKeyFingerprint"`
	OutboundTags       []string `json:"outboundTags"`
}

type NATSyncReport struct {
	CandidateIP string `json:"candidateIp"`
	SeenAt      int64  `json:"seenAt,omitempty"`
}

type NATSyncStatus struct {
	State       string `json:"state"`
	CurrentIP   string `json:"currentIp,omitempty"`
	CandidateIP string `json:"candidateIp,omitempty"`
	ProbeIP     string `json:"probeIp,omitempty"`
	ProbeResult string `json:"probeResult,omitempty"`
	ProbeAt     int64  `json:"probeAt,omitempty"`
	AppliedAt   int64  `json:"appliedAt,omitempty"`
	UpdatedAt   int64  `json:"updatedAt"`
}

type NATSyncView struct {
	Config       NATSyncConfig  `json:"config"`
	PublicKey    string         `json:"publicKey,omitempty"`
	Status       *NATSyncStatus `json:"status,omitempty"`
	LegacyStatus bool           `json:"legacyStatus,omitempty"`
}

// NATSyncService coordinates the single panel-managed job and its settings.
type NATSyncService struct {
	runMu    sync.Mutex
	statusMu sync.Mutex
	status   *NATSyncStatus
}

var PanelNATSync NATSyncService

func loadNATSyncConfig() (NATSyncConfig, error) {
	cfg := NATSyncConfig{RelayPort: 22, RelayUser: "nat-sync", OutboundTags: []string{}}
	setting, err := (&SettingService{}).getSetting(natSyncSettingKey)
	if database.IsNotFound(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, errors.New("could not read NAT sync settings")
	}
	if err := json.Unmarshal([]byte(setting.Value), &cfg); err != nil {
		return cfg, errors.New("could not decode NAT sync settings")
	}
	return cfg, nil
}

func (s *NATSyncService) View(createKey bool) (NATSyncView, error) {
	cfg, err := loadNATSyncConfig()
	if err != nil {
		return NATSyncView{}, err
	}
	key, err := natSyncPublicKey(createKey)
	if err != nil {
		return NATSyncView{}, err
	}
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	view := NATSyncView{Config: cfg, PublicKey: key}
	if cfg.Enabled && s.status != nil {
		status := *s.status
		view.Status = &status
	}
	return view, nil
}

func (s *NATSyncService) Test(ctx context.Context, cfg NATSyncConfig) (NATSyncReport, error) {
	return readNATSyncRelay(ctx, normalizeNATSyncConfig(cfg))
}

func normalizeNATSyncConfig(cfg NATSyncConfig) NATSyncConfig {
	cfg.RelayHost = strings.TrimSpace(cfg.RelayHost)
	cfg.RelayUser = strings.TrimSpace(cfg.RelayUser)
	cfg.HostKeyFingerprint = strings.TrimSpace(cfg.HostKeyFingerprint)
	if cfg.OutboundTags == nil {
		cfg.OutboundTags = []string{}
	}
	return cfg
}

func (s *NATSyncService) Save(ctx context.Context, cfg NATSyncConfig) error {
	cfg = normalizeNATSyncConfig(cfg)
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	if cfg.Enabled {
		if err := validateNATSyncRelay(cfg); err != nil {
			return err
		}
		if err := validateNATSyncTags(cfg.OutboundTags); err != nil {
			return err
		}
		if _, err := natSyncTestURL(); err != nil {
			return err
		}
		if _, err := s.Test(ctx, cfg); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := (&SettingService{}).saveSetting(natSyncSettingKey, string(data)); err != nil {
		return errors.New("could not save NAT sync settings")
	}

	s.statusMu.Lock()
	s.status = nil
	s.statusMu.Unlock()
	return nil
}

// Run is scheduled by the panel. If the previous check is still running, skip this tick.
func (s *NATSyncService) Run(ctx context.Context) {
	if !s.runMu.TryLock() {
		return
	}
	defer s.runMu.Unlock()
	cfg, err := loadNATSyncConfig()
	if err != nil || !cfg.Enabled || ctx.Err() != nil {
		return
	}
	s.statusMu.Lock()
	var status NATSyncStatus
	if s.status != nil {
		status = *s.status
	}
	s.statusMu.Unlock()
	defer func() {
		status.UpdatedAt = time.Now().Unix()
		s.statusMu.Lock()
		s.status = &status
		s.statusMu.Unlock()
	}()

	report, err := readNATSyncRelay(ctx, cfg)
	if err != nil {
		status.State = "relay_error"
		return
	}
	status.CandidateIP = report.CandidateIP
	_ = runNATSyncXray(cfg.OutboundTags, report.CandidateIP, func() (string, error) {
		report, err := readNATSyncRelay(ctx, cfg)
		return report.CandidateIP, err
	}, &status)
}
