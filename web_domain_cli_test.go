package main

import (
	"testing"

	"github.com/a923946240/xx-ui/v3/internal/config"
	"github.com/a923946240/xx-ui/v3/internal/database/dbtest"
	"github.com/a923946240/xx-ui/v3/internal/web/service"
)

func TestUpdateSettingRepairsOnlyTheWebDomain(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, config.GetDBPath())
	settings := &service.SettingService{}
	if err := settings.SetPort(45007); err != nil {
		t.Fatal(err)
	}
	if err := settings.SetBasePath("/original-panel/"); err != nil {
		t.Fatal(err)
	}
	domain := "Panel.Example.NET."
	if err := updateSetting(0, "", "", "", "", false, &domain); err != nil {
		t.Fatal(err)
	}
	if got, err := settings.GetWebDomain(); err != nil || got != "panel.example.net" {
		t.Fatalf("web domain = %q, %v", got, err)
	}
	invalid := "https://another.example.net:443/panel/"
	if err := updateSetting(0, "", "", "", "", false, &invalid); err == nil {
		t.Fatal("URL was accepted as a listening domain")
	}
	if got, err := settings.GetWebDomain(); err != nil || got != "panel.example.net" {
		t.Fatalf("invalid update changed the domain to %q, %v", got, err)
	}
	if err := updateSetting(0, "", "", "", "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := settings.GetWebDomain(); err != nil || got != "panel.example.net" {
		t.Fatalf("omitting the domain changed it to %q, %v", got, err)
	}
	clear := ""
	if err := updateSetting(0, "", "", "", "", false, &clear); err != nil {
		t.Fatal(err)
	}
	if got, err := settings.GetWebDomain(); err != nil || got != "" {
		t.Fatalf("web domain after clear = %q, %v", got, err)
	}
	if got, err := settings.GetPort(); err != nil || got != 45007 {
		t.Fatalf("panel port changed to %d, %v", got, err)
	}
	if got, err := settings.GetBasePath(); err != nil || got != "/original-panel/" {
		t.Fatalf("panel path changed to %q, %v", got, err)
	}
}
