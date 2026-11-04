package service

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/externalproxy"
	"github.com/mhsanaei/3x-ui/v3/internal/util/netproxy"
)

type ExternalClientRequest struct {
	Protocol string `json:"protocol" example:"mieru"`
}

type ExternalClientView struct {
	Protocol        string `json:"protocol" example:"mieru"`
	Name            string `json:"name" example:"mieru"`
	Installed       bool   `json:"installed" example:"true"`
	Version         string `json:"version" example:"3.39.0"`
	Release         string `json:"release" example:"v3.39.0"`
	LatestVersion   string `json:"latestVersion" example:"v3.39.0"`
	UpdateAvailable bool   `json:"updateAvailable" example:"false"`
	Supported       bool   `json:"supported" example:"true"`
	State           string `json:"state" example:"idle"`
	Error           string `json:"error" example:""`
}

var externalClientManager struct {
	sync.Mutex
	bin       string
	installer *externalproxy.ClientInstaller
}

func clientInstaller() *externalproxy.ClientInstaller {
	externalClientManager.Lock()
	defer externalClientManager.Unlock()
	bin, _ := filepath.Abs(filepath.Join(config.GetBinFolderPath(), "external-proxies"))
	if externalClientManager.installer == nil || externalClientManager.bin != bin {
		externalClientManager.bin = bin
		externalClientManager.installer = externalproxy.NewClientInstaller(bin, externalClientHTTPClient, func(ctx context.Context, protocol string, replace func() error) error {
			externalConfigMu.Lock()
			defer externalConfigMu.Unlock()
			if err := replace(); err != nil {
				return err
			}
			return externalOutboundManager().RestartProtocol(ctx, protocol)
		})
	}
	return externalClientManager.installer
}

func externalClientHTTPClient() (*http.Client, error) {
	proxyURL := strings.TrimSpace(os.Getenv("XUI_EXTERNAL_CLIENT_PROXY"))
	if proxyURL == "" {
		return (&SettingService{}).NewProxiedHTTPClient(5 * time.Minute), nil
	}
	client, err := netproxy.NewHTTPClient(proxyURL, 5*time.Minute)
	if err != nil {
		return nil, errors.New("invalid external client download proxy configuration")
	}
	return client, nil
}

func (*ExternalOutboundService) Clients() []ExternalClientView {
	rows := clientInstaller().List()
	views := make([]ExternalClientView, 0, len(rows))
	for _, r := range rows {
		views = append(views, ExternalClientView{Protocol: r.Protocol, Name: r.Name, Installed: r.Installed, Version: r.Version, Release: r.Release, LatestVersion: r.LatestVersion, UpdateAvailable: r.UpdateAvailable, Supported: r.Supported, State: r.State, Error: r.Error})
	}
	return views
}

func (*ExternalOutboundService) StartClientOperation(protocol string, install bool) error {
	return clientInstaller().Start(protocol, install)
}
