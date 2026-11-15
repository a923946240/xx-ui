package service

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/a923946240/xx-ui/v3/internal/database"
	"github.com/a923946240/xx-ui/v3/internal/database/model"
	"github.com/a923946240/xx-ui/v3/internal/util/json_util"
	"github.com/a923946240/xx-ui/v3/internal/xray"
)

func readFailoverJSON[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

type compiledFailoverRouter struct {
	Rules []struct {
		InboundTag  []string
		Domain      []string
		OutboundTag string
		BalancerTag string
	}
	Balancers []struct {
		Tag, FallbackTag string
		Selector         []string
		Strategy         struct{ Type string }
	}
	DomainStrategy string
}

type compiledFailoverExit struct {
	Tag, Protocol string
	Settings      struct{ InboundTag string }
}

func TestRouteFailoverExactPrimaryAndSharedProbes(t *testing.T) {
	const manualRule = `{"type":"field","domain":["full:ordinary.test"],"balancerTag":"manual"}`
	const manualBalancer = `{"tag":"manual","selector":["A"],"strategy":{"type":"random"}}`
	cfg := xray.Config{
		RouterConfig: json_util.RawMessage(`{"domainStrategy":"AsIs","rules":[` + manualRule + `,
			{"type":"field","domain":["full:one.test"],"outboundTag":"A","fallbackOutboundTag":"B"},
			{"type":"field","domain":["full:two.test"],"outboundTag":"A","fallbackOutboundTag":"B"},
			{"type":"field","domain":["full:three.test"],"outboundTag":"A","fallbackOutboundTag":"C"}],
			"balancers":[` + manualBalancer + `]}`),
		OutboundConfigs: json_util.RawMessage(`[{"tag":"A","protocol":"freedom"},{"tag":"A-shadow","protocol":"freedom"},{"tag":"B","protocol":"freedom"},{"tag":"C","protocol":"freedom"}]`),
		Observatory:     json_util.RawMessage(`{"subjectSelector":["A-shadow"],"probeURL":"https://health.example/test","probeInterval":"30s","enableConcurrency":false}`),
	}
	if err := compileRouteFailover(&cfg); err != nil {
		t.Fatal(err)
	}
	router := readFailoverJSON[compiledFailoverRouter](t, cfg.RouterConfig)
	if len(router.Rules) != 5 || len(router.Balancers) != 3 || router.DomainStrategy != "AsIs" || strings.Contains(string(cfg.RouterConfig), `"fallbackOutboundTag"`) {
		t.Fatalf("unexpected compiled router: %s", cfg.RouterConfig)
	}
	rawRouter := readFailoverJSON[struct{ Rules, Balancers []json.RawMessage }](t, cfg.RouterConfig)
	for _, fixture := range []struct {
		raw  json.RawMessage
		want string
	}{{rawRouter.Rules[1], manualRule}, {rawRouter.Balancers[0], manualBalancer}} {
		got := readFailoverJSON[map[string]any](t, fixture.raw)
		if !reflect.DeepEqual(got, readFailoverJSON[map[string]any](t, []byte(fixture.want))) {
			t.Fatalf("user configuration changed: %v, want %s", got, fixture.want)
		}
	}
	exits := readFailoverJSON[[]compiledFailoverExit](t, cfg.OutboundConfigs)
	if len(exits) != 5 || exits[0].Tag != "A" || exits[4].Protocol != "loopback" {
		t.Fatalf("unexpected exits: %s", cfg.OutboundConfigs)
	}
	alias := exits[4].Tag
	if alias == "" || exits[4].Settings.InboundTag == "" || !slices.Equal(router.Rules[0].InboundTag, []string{exits[4].Settings.InboundTag}) || router.Rules[0].OutboundTag != "A" {
		t.Fatalf("alias does not route exactly to A: %s", cfg.RouterConfig)
	}
	for _, exit := range exits[:4] {
		if strings.HasPrefix(exit.Tag, alias) {
			t.Fatalf("private selector %q also selects %q", alias, exit.Tag)
		}
	}
	if strings.HasPrefix(alias, "A") {
		t.Fatal("automatic route added an exit to the manual balancer")
	}
	for i, domain := range []string{"full:one.test", "full:two.test", "full:three.test"} {
		rule := router.Rules[i+2]
		if !slices.Equal(rule.Domain, []string{domain}) || rule.OutboundTag != "" || rule.BalancerTag == "" {
			t.Fatalf("unexpected route for %s: %+v", domain, rule)
		}
	}
	if got := []string{router.Rules[2].BalancerTag, router.Rules[3].BalancerTag, router.Rules[4].BalancerTag}; !slices.Equal(got, []string{router.Balancers[1].Tag, router.Balancers[1].Tag, router.Balancers[2].Tag}) || got[0] == got[2] {
		t.Fatalf("unexpected pair reuse: %v", got)
	}
	for i, backup := range []string{"B", "C"} {
		b := router.Balancers[i+1]
		if b.FallbackTag != backup || !slices.Equal(b.Selector, []string{alias}) || b.Strategy.Type != "roundRobin" {
			t.Fatalf("incorrect priority: %+v", b)
		}
	}
	obs := readFailoverJSON[map[string]any](t, cfg.Observatory)
	want := map[string]any{"subjectSelector": []any{"A-shadow", alias}, "probeURL": "https://health.example/test", "probeInterval": "30s", "enableConcurrency": false}
	if !reflect.DeepEqual(obs, want) {
		t.Fatalf("primary and preserved observer settings: %v", obs)
	}
}

func TestRouteFailoverRetainsBurstProbeSettings(t *testing.T) {
	const ping = `{"destination":"https://probe.example/test","interval":"45s","sampling":2,"timeout":"4s"}`
	cfg := xray.Config{
		RouterConfig:     json_util.RawMessage(`{"rules":[{"port":"443","outboundTag":"A","fallbackOutboundTag":"B"}]}`),
		OutboundConfigs:  json_util.RawMessage(`[{"tag":"A","protocol":"freedom"},{"tag":"B","protocol":"freedom"}]`),
		BurstObservatory: json_util.RawMessage(`{"subjectSelector":["existing"],"pingConfig":` + ping + `}`),
	}
	if err := compileRouteFailover(&cfg); err != nil {
		t.Fatal(err)
	}
	burst := readFailoverJSON[struct {
		SubjectSelector []string
		PingConfig      json.RawMessage
	}](t, cfg.BurstObservatory)
	router := readFailoverJSON[compiledFailoverRouter](t, cfg.RouterConfig)
	if len(cfg.Observatory) != 0 || len(router.Balancers) != 1 || !slices.Equal(burst.SubjectSelector, append([]string{"existing"}, router.Balancers[0].Selector...)) {
		t.Fatalf("unexpected burst probe subjects: %s", cfg.BurstObservatory)
	}
	if !reflect.DeepEqual(readFailoverJSON[map[string]any](t, burst.PingConfig), readFailoverJSON[map[string]any](t, []byte(ping))) {
		t.Fatalf("user burst ping settings changed: %s", burst.PingConfig)
	}
}

func TestRouteFailoverLegacyTemplateUntouched(t *testing.T) {
	// The property name itself is a valid legacy outbound tag.
	cfg := xray.Config{
		RouterConfig:    json_util.RawMessage(`{ "rules": [{"type":"field","port":"443","outboundTag":"fallbackOutboundTag"}], "balancers":[{"tag":"all","selector":[""]}] }`),
		OutboundConfigs: json_util.RawMessage(`[ {"tag":"fallbackOutboundTag","protocol":"freedom"} ]`),
	}
	before := string(cfg.RouterConfig)
	if err := compileRouteFailover(&cfg); err != nil || string(cfg.RouterConfig) != before || len(cfg.Observatory) != 0 {
		t.Fatalf("legacy route changed: %v %s", err, cfg.RouterConfig)
	}
}

func TestRouteFailoverValidation(t *testing.T) {
	setupExternalExitDB(t)
	const prefix = `{"outbounds":[{"tag":"A","protocol":"freedom"},{"tag":"A-shadow","protocol":"freedom"},{"tag":"B","protocol":"freedom"}],"routing":{"rules":[{"type":"field","port":"443",`
	for _, fields := range []string{
		`"fallbackOutboundTag":"B"`, `"outboundTag":"A","fallbackOutboundTag":"A"`,
		`"outboundTag":"A","fallbackOutboundTag":" "`, `"outboundTag":"A","fallbackOutboundTag":7`,
		`"outboundTag":"gone","fallbackOutboundTag":"B"`, `"outboundTag":"A","fallbackOutboundTag":"gone"`,
		`"outboundTag":"A","fallbackOutboundTag":"B","balancerTag":"manual"`,
		`"outboundTag":"A","fallbackOutboundTag":"B","ruleTag":"xui-dns-allow"`,
		`"outboundTag":"A","fallback\u004futboundTag":"gone"`,
	} {
		if err := (&XraySettingService{}).CheckXrayConfig(prefix + fields + `}]}}`); err == nil {
			t.Errorf("accepted %s", fields)
		}
	}
	valid := prefix + `"outboundTag":"A","fallbackOutboundTag":"B"}]`
	if err := (&XraySettingService{}).CheckXrayConfig(valid + `}}`); err != nil {
		t.Fatalf("valid configuration: %v", err)
	}
	for _, conflict := range []string{
		`,"balancers":[{"tag":"all","selector":[""]}]}}`,
		`},"observatory":{"subjectSelector":["A"]},"burstObservatory":{"subjectSelector":["B"]}}`,
	} {
		if err := (&XraySettingService{}).CheckXrayConfig(valid + conflict); err == nil {
			t.Errorf("accepted conflict %s", conflict)
		}
	}
}

func TestRouteFailoverExternalBackupGuardsSavedTemplate(t *testing.T) {
	setupExternalExitDB(t)
	external := &ExternalOutboundService{}
	v, err := external.Save(ExternalOutboundInput{Tag: "backup-ext", Protocol: "mieru", Host: "test.example", Port: 443, Username: "test", Password: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	settings := &XraySettingService{}
	const template = `{"outbounds":[{"tag":"primary","protocol":"freedom"}],"routing":{"rules":[{"type":"field","domain":["full:google.test"],"outboundTag":"primary","fallbackOutboundTag":"backup-ext"}]}}`
	if err := settings.SaveXraySetting(template); err != nil {
		t.Fatal(err)
	}
	before, err := settings.GetXrayConfigTemplate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before, `"fallbackOutboundTag"`) || strings.Contains(before, `"roundRobin"`) {
		t.Fatalf("runtime form persisted: %s", before)
	}
	if err := external.Delete(v.ID); err == nil || !strings.Contains(err.Error(), "referenced") {
		t.Fatalf("backup deletion: %v", err)
	}
	if err := settings.SaveXraySetting(strings.ReplaceAll(template, "backup-ext", "unknown")); err == nil {
		t.Fatal("saved unknown backup")
	}
	after, err := settings.GetXrayConfigTemplate()
	if err != nil || after != before {
		t.Fatalf("invalid save replaced retained template: %v", err)
	}
	if err := settings.SaveXraySetting(strings.ReplaceAll(template, `,"fallbackOutboundTag":"backup-ext"`, "")); err != nil {
		t.Fatal(err)
	}
	if err := external.Delete(v.ID); err != nil {
		t.Fatalf("unreferenced backup deletion: %v", err)
	}
}

func TestRouteFailoverDisabledRuleCanKeepRetiredSubscription(t *testing.T) {
	setupExternalExitDB(t)
	settings := &XraySettingService{}
	const disabled = `{"outbounds":[{"tag":"backup","protocol":"freedom"}],"routing":{"rules":[{"type":"field","port":"443","outboundTag":"retired","fallbackOutboundTag":"backup","enabled":false}]}}`
	if err := settings.SaveXraySetting(disabled); err != nil {
		t.Fatalf("save disabled route: %v", err)
	}
	stored, err := settings.GetXrayConfigTemplate()
	if err != nil || !strings.Contains(stored, `"retired"`) {
		t.Fatalf("disabled route was not retained: %v", err)
	}
	cfg, err := (&XrayService{}).GetXrayConfig()
	if err != nil || strings.Contains(string(cfg.RouterConfig), `"retired"`) || len(cfg.Observatory) != 0 {
		t.Fatalf("disabled rule affected the runtime: %v", err)
	}
	enabled := strings.Replace(disabled, `"enabled":false`, `"enabled":true`, 1)
	if err := settings.SaveXraySetting(enabled); err == nil {
		t.Fatal("re-enabled a route whose primary no longer exists")
	}
	current, err := settings.GetXrayConfigTemplate()
	if err != nil || current != stored {
		t.Fatalf("failed activation replaced disabled rule: %v", err)
	}
}

func TestRouteFailoverVanishedSubscriptionMembers(t *testing.T) {
	setupExternalExitDB(t)
	const primary = `{"tag":"sub-A","protocol":"freedom"}`
	const backup = `{"tag":"sub-B","protocol":"freedom"}`
	sub := model.OutboundSubscription{Enabled: true, LastFetchedOutbounds: "[" + primary + "," + backup + "]"}
	if err := database.GetDB().Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	const template = `{"outbounds":[{"tag":"default","protocol":"freedom"}],"routing":{"rules":[{"type":"field","domain":["full:google.test"],"outboundTag":"sub-A","fallbackOutboundTag":"sub-B"}]}}`
	if err := (&XraySettingService{}).SaveXraySetting(template); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, snapshot, want string
		viaBalancer          bool
	}{
		{"backup remains", "[" + backup + "]", "sub-B", false},
		{"primary remains", "[" + primary + "]", "blackhole", true},
		{"both gone", "[]", "blackhole", false},
		{"both restored", "[" + primary + "," + backup + "]", "sub-B", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := database.GetDB().Model(&sub).Update("last_fetched_outbounds", tc.snapshot).Error; err != nil {
				t.Fatal(err)
			}
			cfg, err := (&XrayService{}).GetXrayConfig()
			if err != nil {
				t.Fatal(err)
			}
			router := readFailoverJSON[compiledFailoverRouter](t, cfg.RouterConfig)
			var target string
			for _, rule := range router.Rules {
				if !slices.Contains(rule.Domain, "full:google.test") {
					continue
				}
				target = rule.OutboundTag
				if (rule.BalancerTag != "") != tc.viaBalancer {
					t.Fatalf("unexpected route choice: %+v", rule)
				}
				if tc.viaBalancer {
					for _, b := range router.Balancers {
						if b.Tag == rule.BalancerTag {
							target = b.FallbackTag
						}
					}
				}
			}
			for _, exit := range readFailoverJSON[[]compiledFailoverExit](t, cfg.OutboundConfigs) {
				if exit.Tag == target && ((tc.want == "blackhole" && exit.Protocol == "blackhole") || exit.Tag == tc.want) {
					return
				}
			}
			t.Fatalf("route target %q did not resolve to %s: %s", target, tc.want, cfg.OutboundConfigs)
		})
	}
}
