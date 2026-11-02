package controller

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/locale"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func TestExternalAPIHidesPasswordAndRestrictsMonitorTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	t.Setenv("XUI_BIN_FOLDER", dir)
	dbtest.InitDB(t, filepath.Join(dir, "x-ui.db"))
	t.Cleanup(service.StopExternalOutbounds)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
		if c.GetHeader("X-Test-Monitor") == "1" {
			c.Set("api_token_scope", model.ApiScopeMonitor)
		}
		c.Next()
	})
	api := &APIController{}
	group := engine.Group("/panel/api/external-outbounds", api.enforceTokenScope)
	NewExternalOutboundController(group)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/panel/api/external-outbounds/save", strings.NewReader(`{"tag":"exit","protocol":"naive","host":"proxy.example","port":443,"username":"alice","password":"never-return","enable":false}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"success":true`) || strings.Contains(w.Body.String(), "never-return") {
		t.Fatalf("save response %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/panel/api/external-outbounds/list", nil))
	if !strings.Contains(w.Body.String(), `"passwordSet":true`) || strings.Contains(w.Body.String(), `"password":`) || strings.Contains(w.Body.String(), "never-return") {
		t.Fatalf("read exposed secret or lost presence: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/panel/api/external-outbounds/list", nil)
	req.Header.Set("X-Test-Monitor", "1")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("monitor could read credentials metadata: %d", w.Code)
	}
}
