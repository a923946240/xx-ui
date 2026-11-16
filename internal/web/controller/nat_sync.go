package controller

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/a923946240/xx-ui/v3/internal/web/service"
)

func (a *XraySettingController) natSyncView(c *gin.Context) {
	writeNATSyncView(c, false)
}

func (a *XraySettingController) natSyncKey(c *gin.Context) {
	writeNATSyncView(c, true)
}

func (a *XraySettingController) natSyncSave(c *gin.Context) {
	cfg, ok := bindNATSyncConfig(c)
	if !ok {
		return
	}
	if err := service.PanelNATSync.Save(c.Request.Context(), cfg); err != nil {
		jsonObj(c, nil, err)
		return
	}
	writeNATSyncView(c, false)
}

func (a *XraySettingController) natSyncTest(c *gin.Context) {
	cfg, ok := bindNATSyncConfig(c)
	if !ok {
		return
	}
	report, err := service.PanelNATSync.Test(c.Request.Context(), cfg)
	jsonObj(c, report, err)
}

func bindNATSyncConfig(c *gin.Context) (service.NATSyncConfig, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var cfg service.NATSyncConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		jsonObj(c, nil, errors.New("invalid NAT sync configuration"))
		return cfg, false
	}
	return cfg, true
}

func writeNATSyncView(c *gin.Context, createKey bool) {
	view, err := service.PanelNATSync.View(createKey)
	if err == nil && !view.Config.Enabled {
		view.Status = readNATSyncStatus(natSyncStatusPath)
		view.LegacyStatus = view.Status != nil
	}
	jsonObj(c, view, err)
}
