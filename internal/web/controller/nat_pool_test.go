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
	"github.com/a923946240/xx-ui/v3/internal/web/locale"
	"github.com/a923946240/xx-ui/v3/internal/web/runtime"
)

func TestNATPoolReportUsesAuthorizedDirectPeer(t *testing.T) {
	engine, auth := newAPIAuthTestEngine(t)
	api := engine.Group("/panel/api", auth.checkAPIAuth, auth.enforceTokenScope)
	api.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
		c.Next()
	})
	api.POST("/nodes/nat/pool/report", natPoolReport)
	api.GET("/nodes/nat/pool", natPoolList)
	api.POST("/nodes/nat/reporting", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, scope := range []string{model.ApiScopeNodeSync, model.ApiScopeMonitor} {
		if err := database.GetDB().Create(&model.ApiToken{
			Name: scope, Token: crypto.HashTokenSHA256(scope + "-test-token"), Scope: scope, Enabled: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	const report = `{"reporterId":"0cbdb040-4896-4c84-9047-baca1f432875","name":"Taiwan"}`
	send := func(method, scheme, path, scope, body, forwarded string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, scheme+"://receiver.test/panel/api/nodes/nat/"+path, strings.NewReader(body))
		req.RemoteAddr = "1.1.1.1:45120"
		req.Header.Set("Content-Type", "application/json")
		if scope != "" {
			req.Header.Set("Authorization", "Bearer "+scope+"-test-token")
		}
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		return response
	}
	for _, attempt := range []struct {
		scheme, scope, forwarded string
		status                   int
	}{
		{"http", model.ApiScopeNodeSync, "", http.StatusNotFound},
		{"https", model.ApiScopeNodeSync, "8.8.8.8", http.StatusNotFound},
		{"https", model.ApiScopeMonitor, "", http.StatusForbidden},
		{"https", "", "", http.StatusNotFound},
	} {
		if got := send(http.MethodPost, attempt.scheme, "pool/report", attempt.scope, report, attempt.forwarded).Code; got != attempt.status {
			t.Fatalf("untrusted report status = %d, want %d", got, attempt.status)
		}
	}
	if got := send(http.MethodPost, "https", "reporting", model.ApiScopeNodeSync, report, "").Code; got != http.StatusForbidden {
		t.Fatalf("reporter credential could change settings: %d", got)
	}
	spoofed := strings.TrimSuffix(report, "}") + `,"candidateIp":"8.8.8.8"}`
	var invalid struct{ Success bool }
	response := send(http.MethodPost, "https", "pool/report", model.ApiScopeNodeSync, spoofed, "")
	if err := json.Unmarshal(response.Body.Bytes(), &invalid); err != nil || invalid.Success {
		t.Fatalf("report could set its own IP: %+v, %v", invalid, err)
	}
	if got := send(http.MethodPost, "https", "pool/report", model.ApiScopeNodeSync, report, "").Code; got != http.StatusOK {
		t.Fatalf("valid report status = %d", got)
	}
	var list struct {
		Success bool
		Obj     []runtime.NATPoolEntry
	}
	response = send(http.MethodGet, "https", "pool", model.ApiScopeNodeSync, "", "")
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if !list.Success || len(list.Obj) != 1 || list.Obj[0].CandidateIP != "1.1.1.1" {
		t.Fatalf("list did not return the observed peer: %+v", list)
	}
}
