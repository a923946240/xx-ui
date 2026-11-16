package service

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	natSyncTestTarget = `{"tag":"tw","protocol":"shadowsocks","settings":{"servers":[{"address":"8.8.8.8","port":35974,"extra":9007199254740993}]},"streamSettings":{"sockopt":{"dialerProxy":"hk"}}}`
	natSyncTestRelay  = `{"tag":"hk","protocol":"socks","settings":{"servers":[{"address":"127.0.0.1","port":1080}]}}`
)

func TestPrepareNATSyncUsesRunningManagedRelayAndPreservesTemplate(t *testing.T) {
	template := `{"outbounds":[` + natSyncTestTarget + `],"someExtension":{"id":9007199254740993}}`
	live := `[` + natSyncTestTarget + "," + natSyncTestRelay + `]`
	plan, err := prepareNATSyncXray(template, []byte(live), []string{"tw"}, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.changed || plan.currentIP != "8.8.8.8" || len(plan.probes) != 1 {
		t.Fatalf("wrong switch plan: changed=%v current=%q probes=%d", plan.changed, plan.currentIP, len(plan.probes))
	}
	if !strings.Contains(plan.template, `"address":"1.1.1.1"`) ||
		!strings.Contains(plan.template, `"port":35974`) ||
		!strings.Contains(plan.template, `"extra":9007199254740993`) ||
		!strings.Contains(plan.template, `"someExtension":{"id":9007199254740993}`) ||
		strings.Contains(plan.template, `"tag":"hk"`) {
		t.Fatalf("candidate template lost unrelated settings or persisted a managed relay: %s", plan.template)
	}
	var chain []struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(plan.probes[0].chain, &chain); err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 || chain[0].Tag != "tw" || chain[1].Tag != "hk" {
		t.Fatalf("wrong probe chain: %+v", chain)
	}
}

func TestPrepareNATSyncRejectsUnsafeOrChangedChain(t *testing.T) {
	validTemplate := `{"outbounds":[` + natSyncTestTarget + `]}`
	cases := []struct{ name, template, live string }{
		{"ambiguous target", `{"outbounds":[` + natSyncTestTarget + "," + natSyncTestTarget + `]}`, `[` + natSyncTestTarget + `]`},
		{"port drift", validTemplate, `[` + strings.Replace(natSyncTestTarget, "35974", "1080", 1) + "," + natSyncTestRelay + `]`},
		{"direct relay", validTemplate, `[` + natSyncTestTarget + `,{"tag":"hk","protocol":"freedom"}]`},
		{"missing relay", validTemplate, `[` + natSyncTestTarget + `]`},
		{"relay cycle", validTemplate, `[` + natSyncTestTarget + `,{"tag":"hk","protocol":"socks","streamSettings":{"sockopt":{"dialerProxy":"tw"}}}]`},
		{"bad chain on unchanged address", validTemplate, `[` + natSyncTestTarget + `,{"tag":"hk","protocol":"freedom"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := "1.1.1.1"
			if tc.name == "bad chain on unchanged address" {
				candidate = "8.8.8.8"
			}
			if _, err := prepareNATSyncXray(tc.template, []byte(tc.live), []string{"tw"}, candidate); err == nil {
				t.Fatal("unsafe candidate accepted")
			}
		})
	}
}

func TestPrepareNATSyncUpdatesEffectiveFlatEndpoint(t *testing.T) {
	target := `{"tag":"tw","protocol":"vless","settings":{"address":"8.8.8.8","port":443,"vnext":[{"address":"4.4.4.4","port":5000}]},"streamSettings":{"sockopt":{"dialerProxy":"hk"}}}`
	template := `{"outbounds":[` + target + `]}`
	live := `[` + target + "," + natSyncTestRelay + `]`
	plan, err := prepareNATSyncXray(template, []byte(live), []string{"tw"}, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if plan.currentIP != "8.8.8.8" || !strings.Contains(plan.template, `"address":"1.1.1.1","port":443`) ||
		!strings.Contains(plan.template, `"vnext":[{"address":"4.4.4.4","port":5000}]`) {
		t.Fatalf("updated the wrong endpoint in %s; recorded current address %q", plan.template, plan.currentIP)
	}
}
