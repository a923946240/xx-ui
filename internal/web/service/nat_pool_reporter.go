package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/a923946240/xx-ui/v3/internal/database"
	"github.com/a923946240/xx-ui/v3/internal/web/runtime"
)

type NATReportingConfig struct {
	Enabled     bool   `json:"enabled"`
	RelayNodeID int    `json:"relayNodeId"`
	Name        string `json:"name"`
}

type NATReportingView struct {
	Config     NATReportingConfig    `json:"config"`
	LastReport *runtime.NATPoolEntry `json:"lastReport,omitempty"`
	Failed     bool                  `json:"failed"`
}

type NATReportingService struct {
	runMu  sync.Mutex
	mu     sync.Mutex
	last   *runtime.NATPoolEntry
	failed bool
}

var PanelNATReporting NATReportingService

func loadNATReportingConfig() (NATReportingConfig, error) {
	var cfg NATReportingConfig
	setting, err := (&SettingService{}).getSetting(natPoolReportKey)
	if database.IsNotFound(err) {
		return cfg, nil
	}
	if err != nil || json.Unmarshal([]byte(setting.Value), &cfg) != nil {
		return NATReportingConfig{}, errors.New("could not read NAT reporting settings")
	}
	return cfg, nil
}

func (s *NATReportingService) View() (NATReportingView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := loadNATReportingConfig()
	return NATReportingView{Config: cfg, LastReport: s.last, Failed: s.failed}, err
}

func (s *NATReportingService) Save(ctx context.Context, cfg NATReportingConfig) (NATReportingView, error) {
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.RelayNodeID < 0 || !validNATReportName(cfg.Name) {
		return NATReportingView{}, errors.New("invalid NAT reporting settings")
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	var report *runtime.NATPoolEntry
	if cfg.Enabled {
		entry, err := sendNATPoolReport(ctx, cfg)
		if err != nil {
			return NATReportingView{}, err
		}
		report = &entry
	}
	if err := ctx.Err(); err != nil {
		return NATReportingView{}, err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return NATReportingView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := (&SettingService{}).saveSetting(natPoolReportKey, string(data)); err != nil {
		return NATReportingView{}, errors.New("could not save NAT reporting settings")
	}
	s.last, s.failed = report, false
	return NATReportingView{Config: cfg, LastReport: report}, nil
}

func (s *NATReportingService) Run(ctx context.Context) {
	if !s.runMu.TryLock() {
		return
	}
	defer s.runMu.Unlock()
	cfg, err := loadNATReportingConfig()
	if err != nil || !cfg.Enabled || ctx.Err() != nil {
		return
	}
	entry, err := sendNATPoolReport(ctx, cfg)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = err != nil
	if err == nil {
		s.last = &entry
	}
}

func sendNATPoolReport(ctx context.Context, cfg NATReportingConfig) (runtime.NATPoolEntry, error) {
	node, err := natSyncNode(cfg.RelayNodeID)
	if err != nil {
		return runtime.NATPoolEntry{}, err
	}
	guid, err := (&SettingService{}).GetPanelGuid()
	if err != nil || guid == "" || guid == node.Guid {
		return runtime.NATPoolEntry{}, errors.New("select a different panel to receive the NAT report")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	entry, err := runtime.PushNATPool(ctx, node, runtime.NATPoolReport{ReporterID: guid, Name: cfg.Name})
	if err != nil {
		return runtime.NATPoolEntry{}, errors.New("NAT report was not accepted; check the receiving panel's version, HTTPS, and reporting credentials")
	}
	return entry, nil
}
