package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/a923946240/xx-ui/v3/internal/database"
	"github.com/a923946240/xx-ui/v3/internal/database/dbtest"
	"github.com/a923946240/xx-ui/v3/internal/database/model"
	"github.com/a923946240/xx-ui/v3/internal/web/locale"
)

type natSyncTestResponse struct {
	Success bool
	Obj     struct {
		Config struct {
			Enabled        bool
			RelayNodeID    *int
			ReporterNodeID *int
		}
	}
}

func natSyncTestAPI(t *testing.T) func(method, path, body string) natSyncTestResponse {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	dbtest.InitDB(t, filepath.Join(dir, "x-ui.db"))
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
		c.Next()
	})
	NewXraySettingController(engine.Group("/panel/api"))

	return func(method, path, body string) natSyncTestResponse {
		t.Helper()
		req := httptest.NewRequest(method, "/panel/api/xray/nat-sync"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("NAT sync %s %s status = %d", method, path, response.Code)
		}
		var got natSyncTestResponse
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
}

func TestNATSyncDoesNotActivateOldSSHConfiguration(t *testing.T) {
	request := natSyncTestAPI(t)
	legacy := model.Setting{
		Key: "natSSHSync", Value: `{"enabled":true,"relayHost":"example.com","outboundTags":["tw"]}`,
	}
	if err := database.GetDB().Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	got := request(http.MethodGet, "", "")
	cfg := got.Obj.Config
	if !got.Success || cfg.Enabled || cfg.RelayNodeID == nil || cfg.ReporterNodeID == nil ||
		*cfg.RelayNodeID != 0 || *cfg.ReporterNodeID != 0 {
		t.Fatalf("unexpected initial NAT node sync view: %+v", got)
	}
}

func TestNATSyncSavesDisabledDraftAndRejectsDuplicateNodes(t *testing.T) {
	request := natSyncTestAPI(t)
	saved := request(http.MethodPost, "/config", `{"relayNodeId":27,"reporterNodeId":31}`)
	if !saved.Success || saved.Obj.Config.Enabled || saved.Obj.Config.RelayNodeID == nil ||
		*saved.Obj.Config.RelayNodeID != 27 || saved.Obj.Config.ReporterNodeID == nil ||
		*saved.Obj.Config.ReporterNodeID != 31 {
		t.Fatalf("disabled configuration failed: %+v", saved)
	}
	enabled := request(http.MethodPost, "/config", `{"enabled":true,"relayNodeId":27,"reporterNodeId":27}`)
	if enabled.Success {
		t.Fatal("configuration was enabled with equal relay and reporter")
	}
	got := request(http.MethodGet, "", "")
	if !got.Success || got.Obj.Config.Enabled || *got.Obj.Config.ReporterNodeID != 31 {
		t.Fatalf("failed enable changed existing configuration: %+v", got)
	}
}
