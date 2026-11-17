package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWaitForNATSyncProbeReturnsWhenTheRequestIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	go func() {
		<-started
		cancel()
	}()
	err := waitForNATSyncProbe(ctx, func() error {
		close(started)
		<-release
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled probe returned %v", err)
	}
}

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
	chain := plan.probes[0].chain
	if len(chain) != 2 || chain[0]["tag"] != "tw" || chain[1]["tag"] != "hk" {
		t.Fatalf("wrong probe chain: %+v", chain)
	}
}

func TestPrepareNATSyncRejectsUnsafeOrChangedChain(t *testing.T) {
	validTemplate := `{"outbounds":[` + natSyncTestTarget + `]}`
	cases := []struct{ name, template, live string }{
		{"ambiguous target", `{"outbounds":[` + natSyncTestTarget + "," + natSyncTestTarget + `]}`, `[` + natSyncTestTarget + `]`},
		{"port drift", validTemplate, `[` + strings.Replace(natSyncTestTarget, "35974", "1080", 1) + "," + natSyncTestRelay + `]`},
		{"target dialer drift", validTemplate, `[` + strings.Replace(natSyncTestTarget, "hk", "other", 1) + "," + natSyncTestRelay + `]`},
		{"upstream dialer drift", `{"outbounds":[` + natSyncTestTarget + "," + natSyncTestRelay + `]}`, `[` + natSyncTestTarget + `,{"tag":"hk","protocol":"socks","streamSettings":{"sockopt":{"dialerProxy":"other"}}}]`},
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

func TestPrepareNATSyncCanProbeTheAlreadyReportedAddress(t *testing.T) {
	template := `{"outbounds":[` + natSyncTestTarget + `]}`
	live := []byte(`[` + natSyncTestTarget + "," + natSyncTestRelay + `]`)
	plan, err := prepareNATSyncXray(template, live, []string{"tw"}, "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	check := plan.probes[0]
	if plan.changed || check.currentAddress != "8.8.8.8" || check.port != 35974 || len(check.chain) != 2 {
		t.Fatalf("an unchanged outbound must still be testable in the wizard: %+v", check)
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
