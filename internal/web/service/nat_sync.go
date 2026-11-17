package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/a923946240/xx-ui/v3/internal/database"
	"github.com/a923946240/xx-ui/v3/internal/database/model"
	"github.com/a923946240/xx-ui/v3/internal/web/runtime"
)

const natSyncSettingKey = "natNodeSync"

type NATSyncConfig struct {
	Enabled        bool     `json:"enabled"`
	RelayNodeID    int      `json:"relayNodeId"`
	ReporterNodeID int      `json:"reporterNodeId"`
	OutboundTags   []string `json:"outboundTags"`
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
	Config NATSyncConfig  `json:"config"`
	Status *NATSyncStatus `json:"status,omitempty"`
}

// NATSyncService coordinates the single panel-managed job and its settings.
type NATSyncService struct {
	runMu    sync.Mutex
	statusMu sync.Mutex
	status   *NATSyncStatus
}

var PanelNATSync NATSyncService

func loadNATSyncConfig() (NATSyncConfig, error) {
	cfg := NATSyncConfig{OutboundTags: []string{}}
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

func (s *NATSyncService) Status() *NATSyncStatus {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.status == nil {
		return nil
	}
	status := *s.status
	return &status
}

func (s *NATSyncService) View() (NATSyncView, error) {
	cfg, err := loadNATSyncConfig()
	if err != nil {
		return NATSyncView{}, err
	}
	view := NATSyncView{Config: cfg}
	if cfg.Enabled {
		view.Status = s.Status()
	}
	return view, nil
}

func validateNATNodeSelection(cfg NATSyncConfig) error {
	if cfg.RelayNodeID <= 0 || cfg.ReporterNodeID <= 0 || cfg.RelayNodeID == cfg.ReporterNodeID {
		return errors.New("select two distinct NAT sync nodes")
	}
	return nil
}

func natSyncNode(id int) (*model.Node, error) {
	node, err := (&NodeService{}).GetById(id)
	if err != nil || !node.Enable {
		return nil, errors.New("select an enabled NAT sync node")
	}
	if node.Scheme != "" && node.Scheme != "https" {
		return nil, errors.New("NAT sync nodes require HTTPS with certificate verification")
	}
	switch node.TlsVerifyMode {
	case "", "verify", "pin", "mtls":
		return node, nil
	default:
		return nil, errors.New("invalid NAT sync node certificate verification mode")
	}
}

func (s *NATSyncService) Test(ctx context.Context, cfg NATSyncConfig) (runtime.NATNodeReport, error) {
	var report runtime.NATNodeReport
	if err := validateNATNodeSelection(cfg); err != nil {
		return report, err
	}
	relayNode, err := natSyncNode(cfg.RelayNodeID)
	if err != nil {
		return report, err
	}
	reporterNode, err := natSyncNode(cfg.ReporterNodeID)
	if err != nil {
		return report, err
	}
	if relayNode.Guid != "" && relayNode.Guid == reporterNode.Guid {
		return report, errors.New("the relay and reporter resolve to the same xx-ui node")
	}

	manager := runtime.GetManager()
	if manager == nil {
		return report, errors.New("the node connection manager is unavailable")
	}
	relay, err := manager.RemoteFor(relayNode)
	if err != nil {
		return report, errors.New("cannot load the NAT relay node connection")
	}
	reporter, err := manager.RemoteFor(reporterNode)
	if err != nil {
		return report, errors.New("cannot load the NAT reporter node connection")
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	challenge, err := relay.NATChallenge(ctx)
	if err != nil {
		return report, errors.New("NAT relay node could not issue a challenge; check its panel version and node permissions")
	}
	target, err := relay.NATObservationTarget(challenge.Challenge)
	if err != nil {
		return report, errors.New("NAT relay requires a public HTTPS endpoint with a trusted certificate")
	}
	if err := reporter.NATObserve(ctx, target); err != nil {
		return report, errors.New("NAT reporter could not reach the relay directly over IPv4 HTTPS; check both node versions and permissions")
	}
	report, err = relay.NATResult(ctx, challenge)
	if err != nil || !natSyncIPv4(report.CandidateIP) || report.SeenAt <= 0 {
		return runtime.NATNodeReport{}, errors.New("NAT relay did not confirm a public IPv4 report")
	}
	return report, nil
}

func (s *NATSyncService) Save(ctx context.Context, cfg NATSyncConfig) error {
	if cfg.OutboundTags == nil {
		cfg.OutboundTags = []string{}
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	if cfg.Enabled {
		if err := validateNATNodeSelection(cfg); err != nil {
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
	var status NATSyncStatus
	if previous := s.Status(); previous != nil {
		status = *previous
	}
	defer func() {
		status.UpdatedAt = time.Now().Unix()
		s.statusMu.Lock()
		s.status = &status
		s.statusMu.Unlock()
	}()

	report, err := s.Test(ctx, cfg)
	if err != nil {
		status.State = "relay_error"
		return
	}
	status.CandidateIP = report.CandidateIP
	_ = runNATSyncXray(cfg.OutboundTags, report.CandidateIP, func() (string, error) {
		report, err := s.Test(ctx, cfg)
		return report.CandidateIP, err
	}, &status)
}
