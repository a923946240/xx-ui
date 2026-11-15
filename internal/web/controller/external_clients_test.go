package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/a923946240/xx-ui/v3/internal/database"
	"github.com/a923946240/xx-ui/v3/internal/database/model"
	"github.com/a923946240/xx-ui/v3/internal/util/crypto"
)

func TestExternalClientsListsMissingExecutables(t *testing.T) {
	t.Setenv("XUI_BIN_FOLDER", t.TempDir())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewExternalOutboundController(router.Group("/external"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/external/clients", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("client management returned HTTP %d, want 200", response.Code)
	}
	var body struct {
		Success bool `json:"success"`
		Obj     []struct {
			Protocol  string `json:"protocol"`
			Installed bool   `json:"installed"`
			Version   string `json:"version"`
		} `json:"obj"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success || len(body.Obj) != 3 {
		t.Fatalf("want three client statuses, got %s", response.Body.String())
	}
	for i, protocol := range []string{"naive", "mieru", "trusttunnel"} {
		if body.Obj[i].Protocol != protocol || body.Obj[i].Installed || body.Obj[i].Version != "" {
			t.Fatalf("unexpected missing-client status: %+v", body.Obj[i])
		}
	}
}

func TestExternalClientsRequireFullAdminAuthority(t *testing.T) {
	engine, a := newAPIAuthTestEngine(t)
	t.Setenv("XUI_BIN_FOLDER", t.TempDir())
	group := engine.Group("/panel/api/external-outbounds")
	group.Use(a.checkAPIAuth, a.enforceTokenScope)
	NewExternalOutboundController(group)
	for _, scope := range []string{model.ApiScopeMonitor, model.ApiScopeNodeSync, model.ApiScopeNodeAdmin, model.ApiScopeAdmin} {
		plaintext := "external-client-test-" + scope
		if err := database.GetDB().Create(&model.ApiToken{Name: scope, Token: crypto.HashTokenSHA256(plaintext), Enabled: true, Scope: scope}).Error; err != nil {
			t.Fatal(err)
		}
		for _, endpoint := range []string{"/clients", "/clients/check", "/clients/install"} {
			method := http.MethodPost
			if endpoint == "/clients" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, "/panel/api/external-outbounds"+endpoint, strings.NewReader(`{"protocol":"../not-a-client"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+plaintext)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, req)
			if scope != model.ApiScopeAdmin {
				if response.Code != http.StatusForbidden {
					t.Fatalf("scope %s could access %s: %d", scope, endpoint, response.Code)
				}
				continue
			}
			if response.Code != http.StatusOK {
				t.Fatalf("admin request returned %d", response.Code)
			}
			if method == http.MethodPost && !strings.Contains(response.Body.String(), `"success":false`) {
				t.Fatalf("arbitrary executable accepted: %s", response.Body.String())
			}
		}
	}
	for _, path := range []string{"/clients", "/clients/install"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "install") {
			method = http.MethodPost
		}
		response := httptest.NewRecorder()
		request := httptest.NewRequest(method, "/panel/api/external-outbounds"+path, nil)
		request.Header.Set("X-Requested-With", "XMLHttpRequest")
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous request reached client management: %d", response.Code)
		}
	}
}

func TestExternalClientsRejectUnknownFieldsAndOversizedRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewExternalOutboundController(engine.Group("/external"))
	for _, body := range []string{
		`{"protocol":"naive","url":"https://attacker.invalid/client"}`,
		`{"protocol":"naive"}{"protocol":"mieru"}`,
		`{"protocol":"` + strings.Repeat("x", 3000) + `"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/external/clients/install", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if !strings.Contains(response.Body.String(), `"success":false`) || !strings.Contains(response.Body.String(), "invalid external client request") {
			t.Fatalf("unexpected client request accepted: %s", response.Body.String())
		}
	}
}
