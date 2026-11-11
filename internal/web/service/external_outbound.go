package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/externalproxy"
	"github.com/mhsanaei/3x-ui/v3/internal/util/json_util"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

type ExternalOutboundInput struct {
	ID         int    `json:"id" example:"0"`
	Tag        string `json:"tag" example:"naive-exit"`
	Remark     string `json:"remark" example:"Remote exit"`
	Protocol   string `json:"protocol" example:"naive"`
	Host       string `json:"host" example:"proxy.example.com"`
	Port       int    `json:"port" example:"443"`
	Username   string `json:"username" example:"user"`
	Password   string `json:"password,omitempty" example:"replace-me"`
	ServerName string `json:"serverName" example:"proxy.example.com"`
	Transport  string `json:"transport" example:"tcp"`
	Enable     bool   `json:"enable" example:"true"`

	MieruMultiplexing  string `json:"mieruMultiplexing" example:"MULTIPLEXING_HIGH"`
	MieruHandshakeMode string `json:"mieruHandshakeMode" example:"HANDSHAKE_NO_WAIT"`
	MieruMtu           int    `json:"mieruMtu" example:"1400"`
}

type ExternalOutboundView struct {
	ID          int    `json:"id" example:"1"`
	Tag         string `json:"tag" example:"naive-exit"`
	Remark      string `json:"remark" example:"Remote exit"`
	Protocol    string `json:"protocol" example:"naive"`
	Host        string `json:"host" example:"proxy.example.com"`
	Port        int    `json:"port" example:"443"`
	Username    string `json:"username" example:"user"`
	ServerName  string `json:"serverName" example:"proxy.example.com"`
	Transport   string `json:"transport" example:"tcp"`
	Enable      bool   `json:"enable" example:"true"`
	PasswordSet bool   `json:"passwordSet" example:"true"`

	MieruMultiplexing  string `json:"mieruMultiplexing" example:"MULTIPLEXING_HIGH"`
	MieruHandshakeMode string `json:"mieruHandshakeMode" example:"HANDSHAKE_NO_WAIT"`
	MieruMtu           int    `json:"mieruMtu" example:"1400"`
	State              string `json:"state" example:"running"`
	Logs               string `json:"logs" example:"Client started"`
}

type ExternalOutboundProbe struct {
	Success    bool  `json:"success" example:"true"`
	Delay      int64 `json:"delay" example:"120"`
	HTTPStatus int   `json:"httpStatus" example:"204"`
}

type ExternalOutboundService struct{}

var (
	externalConfigMu sync.Mutex
	defaultExternal  struct {
		sync.Mutex
		dir, bin string
		manager  *externalproxy.Manager
	}
)

func externalOutboundManager() *externalproxy.Manager {
	defaultExternal.Lock()
	defer defaultExternal.Unlock()
	dir, _ := filepath.Abs(filepath.Join(config.GetDBFolderPath(), "external-outbounds"))
	bin, _ := filepath.Abs(filepath.Join(config.GetBinFolderPath(), "external-proxies"))
	if defaultExternal.manager == nil || defaultExternal.dir != dir || defaultExternal.bin != bin {
		if defaultExternal.manager != nil {
			defaultExternal.manager.StopAll()
		}
		defaultExternal.manager = externalproxy.NewManager(dir, bin)
		defaultExternal.dir, defaultExternal.bin = dir, bin
	}
	return defaultExternal.manager
}

func StopExternalOutbounds() { externalOutboundManager().StopAll() }

func externalSpec(row model.ExternalOutbound) externalproxy.Spec {
	return externalproxy.Spec{ID: row.ID, Tag: row.Tag, Protocol: row.Protocol, Host: row.Host, Port: row.Port, Username: row.Username, Password: row.Password, ServerName: row.ServerName, Transport: row.Transport, Enable: row.Enable, MieruMultiplexing: row.MieruMultiplexing, MieruHandshakeMode: row.MieruHandshakeMode, MieruMtu: row.MieruMtu}
}

func externalView(row model.ExternalOutbound) *ExternalOutboundView {
	status := externalOutboundManager().Status(row.Tag)
	return &ExternalOutboundView{ID: row.ID, Tag: row.Tag, Remark: row.Remark, Protocol: row.Protocol, Host: row.Host, Port: row.Port, Username: row.Username, ServerName: row.ServerName, Transport: row.Transport, Enable: row.Enable, PasswordSet: row.Password != "", State: status.State, Logs: status.Logs, MieruMultiplexing: row.MieruMultiplexing, MieruHandshakeMode: row.MieruHandshakeMode, MieruMtu: row.MieruMtu}
}

func (s *ExternalOutboundService) rows() ([]model.ExternalOutbound, error) {
	rows := []model.ExternalOutbound{}
	err := database.GetDB().Order("id ASC").Find(&rows).Error
	return rows, err
}

func (s *ExternalOutboundService) List() ([]*ExternalOutboundView, error) {
	rows, err := s.rows()
	if err != nil {
		return nil, err
	}
	views := make([]*ExternalOutboundView, 0, len(rows))
	for _, row := range rows {
		views = append(views, externalView(row))
	}
	return views, nil
}

func (s *ExternalOutboundService) Save(in ExternalOutboundInput) (*ExternalOutboundView, error) {
	externalConfigMu.Lock()
	defer externalConfigMu.Unlock()
	row := model.ExternalOutbound{}
	if in.ID != 0 {
		if err := database.GetDB().First(&row, in.ID).Error; err != nil {
			return nil, err
		}
		if row.Tag != strings.TrimSpace(in.Tag) {
			return nil, errors.New("outbound tags cannot be changed; create a new exit")
		}
	}
	password := in.Password
	if password == "" {
		password = row.Password
	}
	row = model.ExternalOutbound{ID: in.ID, Tag: strings.TrimSpace(in.Tag), Remark: strings.TrimSpace(in.Remark), Protocol: in.Protocol, Host: strings.TrimSpace(in.Host), Port: in.Port, Username: in.Username, Password: password, ServerName: strings.TrimSpace(in.ServerName), Transport: in.Transport, Enable: in.Enable, MieruMultiplexing: in.MieruMultiplexing, MieruHandshakeMode: in.MieruHandshakeMode, MieruMtu: in.MieruMtu}
	if row.Transport == "" {
		row.Transport = "tcp"
	}
	if len(row.Remark) > 256 {
		return nil, errors.New("remark must be at most 256 bytes")
	}
	if err := externalSpec(row).Validate(); err != nil {
		return nil, err
	}
	if err := externalTagAvailable(row.Tag, row.ID); err != nil {
		return nil, err
	}
	if err := database.GetDB().Session(&gorm.Session{Logger: gormlogger.Discard}).Save(&row).Error; err != nil {
		return nil, errors.New("could not save external outbound")
	}
	return externalView(row), nil
}

func externalTagAvailable(tag string, id int) error {
	if strings.HasPrefix(tag, "amneziawg-v6-") {
		return errors.New("outbound tag is reserved for generated handlers")
	}
	var count int64
	if err := database.GetDB().Model(&model.ExternalOutbound{}).Where("tag = ? AND id <> ?", tag, id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("outbound tag already exists")
	}
	template, err := (&SettingService{}).GetXrayConfigTemplate()
	if err != nil {
		return err
	}
	if templateDefinesTag([]byte(template), tag) {
		return errors.New("outbound tag already exists")
	}
	var subscriptions []model.OutboundSubscription
	if err := database.GetDB().Find(&subscriptions).Error; err != nil {
		return err
	}
	for _, subscription := range subscriptions {
		if templateDefinesTag([]byte(`{"outbounds":`+subscription.LastFetchedOutbounds+`}`), tag) {
			return errors.New("outbound tag already exists")
		}
	}
	return nil
}

func templateDefinesTag(data []byte, tag string) bool {
	var cfg struct {
		API struct {
			Tag string `json:"tag"`
		} `json:"api"`
		Metrics struct {
			Tag string `json:"tag"`
		} `json:"metrics"`
		Reverse struct {
			Bridges []struct {
				Tag string `json:"tag"`
			} `json:"bridges"`
			Portals []struct {
				Tag string `json:"tag"`
			} `json:"portals"`
		} `json:"reverse"`
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
		Routing struct {
			Balancers []struct {
				Tag string `json:"tag"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return false
	}
	if cfg.API.Tag == tag || cfg.Metrics.Tag == tag {
		return true
	}
	for _, bridge := range cfg.Reverse.Bridges {
		if bridge.Tag == tag {
			return true
		}
	}
	for _, portal := range cfg.Reverse.Portals {
		if portal.Tag == tag {
			return true
		}
	}
	for _, outbound := range cfg.Outbounds {
		if outbound.Tag == tag {
			return true
		}
	}
	for _, balancer := range cfg.Routing.Balancers {
		if balancer.Tag == tag {
			return true
		}
	}
	return false
}

func referencesExternalTag(data []byte, tag string) bool {
	var root any
	if json.Unmarshal(data, &root) != nil {
		return true
	}
	var walk func(any, string) bool
	walk = func(value any, parent string) bool {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if text, ok := child.(string); ok && text == tag && (key == "outboundTag" || key == failoverField || key == "dialerProxy" || key == "fallbackTag" || parent == "proxySettings" && key == "tag") {
					return true
				}
				if key == "selector" {
					if selectors, ok := child.([]any); ok {
						for _, selector := range selectors {
							if prefix, ok := selector.(string); ok && strings.HasPrefix(tag, prefix) {
								return true
							}
						}
					}
				}
				if walk(child, key) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if walk(child, parent) {
					return true
				}
			}
		}
		return false
	}
	return walk(root, "")
}

func (s *ExternalOutboundService) Delete(id int) error {
	externalConfigMu.Lock()
	defer externalConfigMu.Unlock()
	var row model.ExternalOutbound
	if err := database.GetDB().First(&row, id).Error; err != nil {
		return err
	}
	template, err := (&SettingService{}).GetXrayConfigTemplate()
	if err != nil {
		return err
	}
	referenced := referencesExternalTag([]byte(template), row.Tag)
	var inbounds []model.Inbound
	if err := database.GetDB().Find(&inbounds).Error; err != nil {
		return err
	}
	for _, inbound := range inbounds {
		if inbound.Settings != "" && referencesExternalTag([]byte(inbound.Settings), row.Tag) {
			referenced = true
		}
	}
	var nodes []model.Node
	if err := database.GetDB().Find(&nodes).Error; err != nil {
		return err
	}
	for _, node := range nodes {
		if node.OutboundTag == row.Tag {
			referenced = true
		}
	}
	panelTag, err := (&SettingService{}).GetPanelOutbound()
	if err != nil {
		return err
	}
	if panelTag == row.Tag {
		referenced = true
	}
	if referenced {
		return errors.New("outbound is referenced; update routing before deleting it")
	}
	return database.GetDB().Delete(&row).Error
}

func checkExternalTemplateTags(raw string) error {
	if database.GetDB() == nil {
		return nil
	}
	var rows []model.ExternalOutbound
	if err := database.GetDB().Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if templateDefinesTag([]byte(raw), row.Tag) {
			return errors.New("outbound tag already exists")
		}
	}
	return nil
}

// Call while holding externalConfigMu, including the subsequent reference write.
func validateEgressTarget(tag string) error {
	if tag == "" {
		return nil
	}
	var count int64
	if err := database.GetDB().Model(&model.ExternalOutbound{}).Where("tag = ?", tag).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	template, err := (&SettingService{}).GetXrayConfigTemplate()
	if err != nil {
		return err
	}
	if templateDefinesTag([]byte(template), tag) {
		return nil
	}
	var subscriptions []model.OutboundSubscription
	if err := database.GetDB().Find(&subscriptions).Error; err != nil {
		return err
	}
	for _, sub := range subscriptions {
		if sub.Enabled && templateDefinesTag([]byte(`{"outbounds":`+sub.LastFetchedOutbounds+`}`), tag) {
			return nil
		}
	}
	return errors.New("outbound target no longer exists; reload routing before saving")
}

func validateInboundEgress(inbound *model.Inbound) error {
	if inbound.Protocol != model.MTProto {
		return nil
	}
	var settings struct {
		OutboundTag string `json:"outboundTag"`
	}
	if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
		return err
	}
	return validateEgressTarget(settings.OutboundTag)
}

func (s *ExternalOutboundService) Reconcile() error {
	externalConfigMu.Lock()
	defer externalConfigMu.Unlock()
	rows, err := s.rows()
	if err != nil {
		return err
	}
	specs := make([]externalproxy.Spec, 0, len(rows))
	for _, row := range rows {
		specs = append(specs, externalSpec(row))
	}
	return externalOutboundManager().Reconcile(specs)
}

func (s *ExternalOutboundService) Inject(cfg *xray.Config) error {
	if err := s.Reconcile(); err != nil {
		return err
	}
	var outbounds []map[string]any
	if len(cfg.OutboundConfigs) > 0 {
		if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
			return err
		}
	}
	managed := externalOutboundManager().Outbounds()
	for _, extra := range managed {
		for _, existing := range outbounds {
			if existing["tag"] == extra["tag"] {
				return errors.New("external outbound tag collides with another outbound")
			}
		}
		outbounds = append(outbounds, extra)
	}
	if len(managed) > 0 {
		b, err := json.Marshal(outbounds)
		if err != nil {
			return err
		}
		cfg.OutboundConfigs = json_util.RawMessage(b)
	}
	return nil
}

func (s *ExternalOutboundService) Tags() ([]string, error) {
	tags := []string{}
	err := database.GetDB().Model(&model.ExternalOutbound{}).Order("id ASC").Pluck("tag", &tags).Error
	return tags, err
}

func (s *ExternalOutboundService) Probe(ctx context.Context, id int) (*ExternalOutboundProbe, error) {
	var row model.ExternalOutbound
	if err := database.GetDB().First(&row, id).Error; err != nil {
		return nil, err
	}
	if !row.Enable {
		return nil, errors.New("external outbound is disabled")
	}
	if err := s.Reconcile(); err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 8 * time.Second}
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		return externalOutboundManager().DialContext(ctx, row.Tag, address)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.google.com/generate_204", nil)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("tunnel HTTPS probe failed; inspect client status")
	}
	_ = resp.Body.Close()
	return &ExternalOutboundProbe{Success: resp.StatusCode >= 200 && resp.StatusCode < 400, Delay: time.Since(start).Milliseconds(), HTTPStatus: resp.StatusCode}, nil
}
