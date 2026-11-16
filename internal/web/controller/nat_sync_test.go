package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/a923946240/xx-ui/v3/internal/database/dbtest"
	"github.com/a923946240/xx-ui/v3/internal/web/locale"
)

type natSyncTestResponse struct {
	Success bool
	Obj     struct {
		Config struct {
			Enabled   bool
			RelayHost string
			RelayPort int
		}
		PublicKey string
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
		if strings.Contains(response.Body.String(), "PRIVATE KEY") {
			t.Fatal("NAT sync response exposed a private key")
		}
		var got natSyncTestResponse
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
}

func TestNATSyncStartsDisabledWithoutGeneratingKeys(t *testing.T) {
	request := natSyncTestAPI(t)
	got := request(http.MethodGet, "", "")
	if !got.Success || got.Obj.Config.Enabled || got.Obj.Config.RelayPort != 22 || got.Obj.PublicKey != "" {
		t.Fatalf("unexpected initial NAT sync view: %+v", got)
	}
}

func TestNATSyncSavesDisabledDraftAndRejectsInvalidEnable(t *testing.T) {
	request := natSyncTestAPI(t)
	saved := request(http.MethodPost, "/config", `{"relayHost":"relay.example","relayPort":2222}`)
	if !saved.Success || saved.Obj.Config.Enabled || saved.Obj.Config.RelayHost != "relay.example" {
		t.Fatalf("disabled configuration failed: %+v", saved)
	}
	enabled := request(http.MethodPost, "/config", `{"enabled":true,"relayHost":"another.example","relayPort":2222}`)
	if enabled.Success {
		t.Fatal("configuration was enabled without SSH host verification or outbound tags")
	}
	got := request(http.MethodGet, "", "")
	if !got.Success || got.Obj.Config != saved.Obj.Config {
		t.Fatalf("failed enable changed existing configuration: %+v", got)
	}
}

func TestNATSyncKeyGenerationIsIdempotent(t *testing.T) {
	request := natSyncTestAPI(t)
	first := request(http.MethodPost, "/key", "")
	second := request(http.MethodPost, "/key", "")
	if !first.Success || !second.Success || !strings.HasPrefix(first.Obj.PublicKey, "ssh-ed25519 ") ||
		first.Obj.PublicKey != second.Obj.PublicKey {
		t.Fatal("NAT sync did not return the same Ed25519 public key")
	}
}
