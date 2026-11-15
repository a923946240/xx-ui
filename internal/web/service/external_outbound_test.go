package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/a923946240/xx-ui/v3/internal/database"
	"github.com/a923946240/xx-ui/v3/internal/database/dbtest"
	"github.com/a923946240/xx-ui/v3/internal/database/model"
)

func setupExternalExitDB(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	t.Setenv("XUI_BIN_FOLDER", dir)
	dbtest.InitDB(t, filepath.Join(dir, "x-ui.db"))
	t.Cleanup(StopExternalOutbounds)
}

func TestExternalSubscriptionCollisionPreservesPreviousSnapshot(t *testing.T) {
	setupExternalExitDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`vless://00000000-0000-4000-8000-000000000000@1.2.3.4:443?type=tcp&security=tls&sni=proxy.example#exit`))
	}))
	defer server.Close()
	sub := model.OutboundSubscription{Url: server.URL, TagPrefix: "test-", AllowPrivate: true, Enabled: true}
	if err := database.GetDB().Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	s := &OutboundSubscriptionService{}
	outbounds, err := s.Refresh(sub.Id)
	if err != nil || len(outbounds) != 1 {
		t.Fatalf("fixture refresh: %v %v", outbounds, err)
	}
	var previous model.OutboundSubscription
	if err := database.GetDB().First(&previous, sub.Id).Error; err != nil {
		t.Fatal(err)
	}
	tag := outbounds[0].(map[string]any)["tag"].(string)
	if err := database.GetDB().Model(&sub).Update("last_fetched_outbounds", "[]").Error; err != nil {
		t.Fatal(err)
	}
	_, err = (&ExternalOutboundService{}).Save(ExternalOutboundInput{Tag: tag, Protocol: "mieru", Host: "exit.example", Port: 443, Username: "user", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(sub.Id); err == nil {
		t.Fatal("refresh overwrote the external tag")
	}
	var got model.OutboundSubscription
	if err := database.GetDB().First(&got, sub.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.LastFetchedOutbounds != "[]" || got.LastUpdated != previous.LastUpdated {
		t.Fatal("failed refresh replaced the valid snapshot")
	}
}

func TestExternalDeletionAndPanelReferenceAreAtomic(t *testing.T) {
	setupExternalExitDB(t)
	s := &ExternalOutboundService{}
	settings := &SettingService{}
	for i := 0; i < 12; i++ {
		v, err := s.Save(ExternalOutboundInput{Tag: "race-exit", Protocol: "mieru", Host: "exit.example", Port: 443, Username: "user", Password: "secret"})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _ = s.Delete(v.ID) }()
		go func() { defer wg.Done(); <-start; _ = settings.SetPanelOutbound(v.Tag) }()
		close(start)
		wg.Wait()
		tag, err := settings.GetPanelOutbound()
		if err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := database.GetDB().Model(&model.ExternalOutbound{}).Where("tag = ?", v.Tag).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if tag == v.Tag && count == 0 {
			t.Fatal("dangling panel reference after concurrent delete")
		}
		if err := settings.SetPanelOutbound(""); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			if err := s.Delete(v.ID); err != nil {
				t.Fatal(err)
			}
		}
		if err := settings.SetPanelOutbound(v.Tag); err == nil {
			t.Fatal("stale form resurrected a deleted reference")
		}
		if err := (&NodeService{}).UpdateFromRequest(1, &NodeMutationRequest{OutboundTag: v.Tag}); err == nil || !strings.Contains(err.Error(), "target no longer exists") {
			t.Fatalf("stale node reference: %v", err)
		}
	}
}

func TestExternalSavePreservesSecretAndRefusesInvalidEdit(t *testing.T) {
	setupExternalExitDB(t)
	s := &ExternalOutboundService{}
	in := ExternalOutboundInput{Tag: "naive-exit", Protocol: "naive", Host: "exit.example", Port: 443, Username: "alice", Password: "only-write-this", Enable: true}
	v, err := s.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	in.ID, in.Password, in.Remark = v.ID, "", "renamed display"
	if _, err = s.Save(in); err != nil {
		t.Fatal(err)
	}
	in.Host = "https://invalid.example/path"
	if _, err = s.Save(in); err == nil || err.Error() != "invalid server host" {
		t.Fatalf("invalid edit: %v", err)
	}
	var row model.ExternalOutbound
	if err := database.GetDB().First(&row, v.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Password != "only-write-this" || row.Host != "exit.example" || row.Remark != "renamed display" {
		t.Fatalf("saved configuration was not preserved: host=%s remark=%s", row.Host, row.Remark)
	}
	rows, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(rows)
	if strings.Contains(string(b), "only-write-this") || strings.Contains(string(b), `"password":`) {
		t.Fatal("list returned a stored password")
	}
	if !rows[0].PasswordSet {
		t.Fatal("edit form cannot distinguish retained password")
	}
	cfg, err := (&XrayService{}).GetXrayConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg.OutboundConfigs), `"tag":"naive-exit"`) || strings.Contains(string(cfg.OutboundConfigs), "only-write-this") {
		t.Fatal("runtime config omitted the exit or leaked its remote password")
	}
}

func TestExternalTagsCannotCollideWithTemplatesOrBeDeletedWhileReferenced(t *testing.T) {
	setupExternalExitDB(t)
	s := &ExternalOutboundService{}
	in := ExternalOutboundInput{Tag: "direct", Protocol: "mieru", Host: "exit.example", Port: 443, Username: "alice", Password: "secret", Enable: true}
	if _, err := s.Save(in); err == nil || err.Error() != "outbound tag already exists" {
		t.Fatalf("collision: %v", err)
	}
	in.Tag = "api"
	if _, err := s.Save(in); err == nil || err.Error() != "outbound tag already exists" {
		t.Fatalf("API handler collision: %v", err)
	}
	in.Tag = "tunnel-exit"
	v, err := s.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	settings := &SettingService{}
	tpl, err := settings.GetXrayConfigTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(tpl), &cfg); err != nil {
		t.Fatal(err)
	}
	api := cfg["api"].(map[string]any)
	api["tag"] = in.Tag
	apiCollision, _ := json.Marshal(cfg)
	if err := (&XraySettingService{}).SaveXraySetting(string(apiCollision)); err == nil || err.Error() != "outbound tag already exists" {
		t.Fatalf("template API handler collision: %v", err)
	}
	api["tag"] = "api"
	cfg["routing"] = map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"entry"}, "outboundTag": in.Tag}}}
	b, _ := json.Marshal(cfg)
	if err := settings.saveSetting("xrayTemplateConfig", string(b)); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(v.ID); err == nil || err.Error() != "outbound is referenced; update routing before deleting it" {
		t.Fatalf("referenced delete: %v", err)
	}
	in.ID, in.Enable, in.Password = v.ID, false, ""
	if _, err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	if err := s.Reconcile(); err != nil {
		t.Fatal(err)
	}
	obs := externalOutboundManager().Outbounds()
	if len(obs) != 1 || obs[0]["protocol"] != "blackhole" || obs[0]["tag"] != "tunnel-exit" {
		t.Fatalf("disabled exit lost its block tag: %#v", obs)
	}
	runtimeConfig, err := (&XrayService{}).GetXrayConfig()
	if err != nil {
		t.Fatal(err)
	}
	var runtimeOutbounds []map[string]any
	if err := json.Unmarshal(runtimeConfig.OutboundConfigs, &runtimeOutbounds); err != nil {
		t.Fatal(err)
	}
	foundBlocked := false
	for _, out := range runtimeOutbounds {
		if out["tag"] == "tunnel-exit" && out["protocol"] == "blackhole" {
			foundBlocked = true
		}
	}
	if !foundBlocked {
		t.Fatal("GetXrayConfig did not inject the disabled block tag")
	}
	cfg["outbounds"] = append(cfg["outbounds"].([]any), map[string]any{"tag": in.Tag, "protocol": "freedom", "settings": map[string]any{}})
	b, _ = json.Marshal(cfg)
	if err := (&XraySettingService{}).SaveXraySetting(string(b)); err == nil || err.Error() != "outbound tag already exists" {
		t.Fatalf("template bypassed collision check: %v", err)
	}
}

func TestExternalReservedHandlerTags(t *testing.T) {
	for _, raw := range []string{`{"api":{"tag":"exit"}}`, `{"metrics":{"tag":"exit"}}`, `{"reverse":{"bridges":[{"tag":"exit"}]}}`, `{"reverse":{"portals":[{"tag":"exit"}]}}`} {
		if !templateDefinesTag([]byte(raw), "exit") {
			t.Errorf("missed handler tag in %s", raw)
		}
	}
}

func TestExternalReferenceDetectionIncludesBalancerPrefixes(t *testing.T) {
	for _, raw := range []string{
		`{"routing":{"rules":[{"outboundTag":"exit"}]}}`,
		`{"routing":{"balancers":[{"selector":["ex"]}]}}`,
		`{"outbounds":[{"streamSettings":{"sockopt":{"dialerProxy":"exit"}}}]}`,
		`{"outbounds":[{"proxySettings":{"tag":"exit"}}]}`,
	} {
		if !referencesExternalTag([]byte(raw), "exit") {
			t.Fatalf("missed reference in %s", raw)
		}
	}
	if referencesExternalTag([]byte(`{"outbounds":[{"tag":"unrelated"}]}`), "exit") {
		t.Fatal("unrelated tag was treated as a reference")
	}
}

// A saved mieru exit must keep its link tuning through the row, the view and
// the rendered client; dropping it left IX-OUT on per-session underlays.
func TestExternalMieruTuningSurvivesSaveAndRender(t *testing.T) {
	setupExternalExitDB(t)
	s := &ExternalOutboundService{}
	in := ExternalOutboundInput{
		Tag: "ix-out", Protocol: "mieru", Host: "192.0.2.10", Port: 24988, Username: "alice", Password: "secret",
		MieruMultiplexing: "MULTIPLEXING_HIGH", MieruHandshakeMode: "HANDSHAKE_NO_WAIT", MieruMtu: 1400,
	}
	v, err := s.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	if v.MieruMultiplexing != "MULTIPLEXING_HIGH" || v.MieruHandshakeMode != "HANDSHAKE_NO_WAIT" || v.MieruMtu != 1400 {
		t.Fatalf("view dropped the tuning: %+v", v)
	}
	var row model.ExternalOutbound
	if err := database.GetDB().First(&row, v.ID).Error; err != nil {
		t.Fatal(err)
	}
	b, err := externalSpec(row).Render(32123)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"level":"MULTIPLEXING_HIGH"`, `"handshakeMode":"HANDSHAKE_NO_WAIT"`, `"mtu":1400`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("rendered client lacks %s: %s", want, b)
		}
	}
	in.ID, in.Password, in.MieruMtu = v.ID, "", 1500
	if _, err := s.Save(in); err == nil || err.Error() != "mieru MTU must be between 1280 and 1400" {
		t.Fatalf("invalid MTU accepted: %v", err)
	}
}
