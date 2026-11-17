package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"

	"github.com/a923946240/xx-ui/v3/internal/web/global"
	"github.com/a923946240/xx-ui/v3/internal/web/locale"
	"github.com/a923946240/xx-ui/v3/internal/web/service"
)

func natNodeTestRouter() *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
		c.Next()
	})
	router.POST("/challenge", natNodeChallenge)
	router.POST("/observe", natNodeObserve)
	router.POST("/result", natNodeResult)
	router.POST("/report", natNodeReport)
	return router
}

func TestNATNodeReportUsesDirectTLSPeer(t *testing.T) {
	router := natNodeTestRouter()
	send := func(scheme, path, body, peer string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, scheme+"://relay.example"+path, strings.NewReader(body))
		req.RemoteAddr = peer
		req.Header.Set("Content-Type", "application/json")
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}

	newChallenge := send("https", "/challenge", "", "8.8.8.8:52800", nil)
	var issued struct {
		Success bool
		Obj     struct {
			Challenge string
		}
	}
	if err := json.Unmarshal(newChallenge.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if newChallenge.Code != http.StatusOK || !issued.Success || len(issued.Obj.Challenge) != 43 {
		t.Fatal("relay challenge was not issued")
	}
	if h := newChallenge.Header().Get("Cache-Control"); h != "no-store" {
		t.Errorf("challenge cache control = %q; want no-store", h)
	}
	body, err := json.Marshal(map[string]string{"challenge": issued.Obj.Challenge})
	if err != nil {
		t.Fatal(err)
	}

	for _, attempt := range []struct {
		name    string
		scheme  string
		peer    string
		body    string
		headers map[string]string
	}{
		{name: "clear text", scheme: "http", peer: "1.1.1.1:55000", body: string(body)},
		{
			name: "private peer with spoofed IP", scheme: "https", peer: "192.168.1.5:55000",
			body: string(body), headers: map[string]string{"X-Forwarded-For": "1.1.1.1"},
		},
		{
			name: "forwarding header on public peer", scheme: "https", peer: "8.8.8.8:55000",
			body: string(body), headers: map[string]string{"Forwarded": "for=1.1.1.1"},
		},
		{
			name: "non-JSON content type", scheme: "https", peer: "8.8.8.8:55000",
			body: string(body), headers: map[string]string{"Content-Type": "text/plain"},
		},
		{name: "oversize body", scheme: "https", peer: "1.1.1.1:55000", body: string(body) + strings.Repeat(" ", 4096)},
		{name: "trailing JSON", scheme: "https", peer: "1.1.1.1:55000", body: string(body) + "{}"},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			resp := send(attempt.scheme, "/report", attempt.body, attempt.peer, attempt.headers)
			if resp.Code != http.StatusNotFound {
				t.Fatalf("invalid report status = %d; want 404", resp.Code)
			}
		})
	}

	if got := send("https", "/report", string(body), "1.1.1.1:55000", nil).Code; got != http.StatusNoContent {
		t.Fatalf("direct report status = %d; want 204", got)
	}
	if got := send("https", "/report", string(body), "8.8.8.8:55000", nil).Code; got != http.StatusNotFound {
		t.Fatalf("repeated report status = %d; want 404", got)
	}

	result := send("https", "/result", string(body), "8.8.8.8:52800", nil)
	var parsed struct {
		Success bool
		Obj     struct {
			CandidateIP string
			SeenAt      int64
		}
	}
	if err := json.Unmarshal(result.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if !parsed.Success || parsed.Obj.CandidateIP != "1.1.1.1" || parsed.Obj.SeenAt <= 0 {
		t.Fatalf("result did not return the direct peer: %+v", parsed)
	}
}

type natNodeMountTestServer struct {
	global.WebServer
	scheduler *cron.Cron
}

func (s natNodeMountTestServer) GetCron() *cron.Cron { return s.scheduler }

// Exercise the real API registration and a real TLS listener. A synthetic
// public remote address represents the Internet peer in this loopback test.
func TestNATNodeReportMountedOutsidePanelLogin(t *testing.T) {
	oldServer := global.GetWebServer()
	global.SetWebServer(natNodeMountTestServer{scheduler: cron.New()})
	t.Cleanup(func() { global.SetWebServer(oldServer) })

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request.RemoteAddr = "1.1.1.1:55000"
		c.Next()
	})
	NewAPIController(router.Group("/custom"))
	srv := httptest.NewTLSServer(router)
	defer srv.Close()

	challenge, err := service.IssueNATNodeChallenge()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(challenge)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Post(srv.URL+"/custom/panel/api/nodes/nat/report", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("public challenge report status = %d; want 204", resp.StatusCode)
	}
	got, err := service.TakeNATNodeResult(challenge.Challenge)
	if err != nil || got.CandidateIP != "1.1.1.1" {
		t.Fatalf("mounted route did not capture the direct TLS peer: %+v, %v", got, err)
	}
}
