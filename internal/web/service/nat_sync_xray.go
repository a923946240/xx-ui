package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/a923946240/xx-ui/v3/internal/web/runtime"
	"github.com/a923946240/xx-ui/v3/internal/web/service/outbound"
)

type natSyncOutbound = map[string]any

type natSyncProbe struct {
	tag            string
	port           int64
	currentAddress string
	chain          []natSyncOutbound
}

type natSyncPlan struct {
	template  string
	probes    []natSyncProbe
	currentIP string
	changed   bool
}

func decodeNATSyncJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(out)
}

func natSyncTemplate(template string) (map[string]json.RawMessage, []natSyncOutbound, error) {
	var fields map[string]json.RawMessage
	var outbounds []natSyncOutbound
	if json.Unmarshal([]byte(UnwrapXrayTemplateConfig(template)), &fields) != nil ||
		decodeNATSyncJSON(fields["outbounds"], &outbounds) != nil {
		return nil, nil, errors.New("cannot read Xray template outbounds")
	}
	return fields, outbounds, nil
}

func findNATSyncOutbound(outbounds []natSyncOutbound, tag string) (natSyncOutbound, error) {
	var match natSyncOutbound
	for _, item := range outbounds {
		if item["tag"] == tag {
			if match != nil {
				return nil, errors.New("synchronization selected an ambiguous outbound tag")
			}
			match = item
		}
	}
	return match, nil
}

func natSyncEndpoint(target natSyncOutbound) (natSyncOutbound, int64, error) {
	key := ""
	protocol, _ := target["protocol"].(string)
	switch strings.ToLower(protocol) {
	case "shadowsocks", "socks", "trojan":
		key = "servers"
	case "vless", "vmess":
		key = "vnext"
	default:
		return nil, 0, errors.New("sync requires a supported proxy outbound")
	}
	settings, _ := target["settings"].(map[string]any)
	// Xray uses the flat endpoint when a settings.address is present,
	// including VLESS outbounds written by the panel form.
	server := settings
	if settings["address"] == nil {
		servers, _ := settings[key].([]any)
		if len(servers) != 1 {
			return nil, 0, errors.New("sync requires exactly one server per outbound")
		}
		server, _ = servers[0].(map[string]any)
	}
	portValue, _ := server["port"].(json.Number)
	port, err := portValue.Int64()
	if err != nil || port < 1 || port > 65535 {
		return nil, 0, errors.New("sync requires a valid fixed outbound port")
	}
	if address, _ := server["address"].(string); address == "" {
		return nil, 0, errors.New("sync requires an outbound server address")
	}
	return server, port, nil
}

func natSyncDialer(outbound natSyncOutbound) string {
	stream, _ := outbound["streamSettings"].(map[string]any)
	sockopt, _ := stream["sockopt"].(map[string]any)
	tag, _ := sockopt["dialerProxy"].(string)
	return tag
}

func natSyncTargets(template []natSyncOutbound, tags []string) ([]natSyncOutbound, error) {
	if len(tags) == 0 {
		return nil, errors.New("select at least one outbound to synchronize")
	}
	seen := make(map[string]bool, len(tags))
	targets := make([]natSyncOutbound, 0, len(tags))
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" || seen[tag] {
			return nil, errors.New("select distinct outbound tags")
		}
		seen[tag] = true
		target, err := findNATSyncOutbound(template, tag)
		if err != nil {
			return nil, err
		}
		if target == nil {
			return nil, errors.New("selected outbound is missing from the saved Xray template")
		}
		if _, _, err := natSyncEndpoint(target); err != nil {
			return nil, err
		}
		if natSyncDialer(target) == "" {
			return nil, errors.New("selected outbound must use a dialer proxy")
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func validateNATSyncTags(tags []string) error {
	template, err := (&SettingService{}).GetXrayConfigTemplate()
	if err != nil {
		return errors.New("cannot load the saved Xray template")
	}
	_, outbounds, err := natSyncTemplate(template)
	if err != nil {
		return err
	}
	_, err = natSyncTargets(outbounds, tags)
	return err
}

func natSyncChain(target natSyncOutbound, template, live []natSyncOutbound) ([]natSyncOutbound, error) {
	chain := []natSyncOutbound{target}
	tag, _ := target["tag"].(string)
	seen := map[string]bool{tag: true}
	for tag := natSyncDialer(target); tag != ""; tag = natSyncDialer(target) {
		if seen[tag] {
			return nil, errors.New("dialer proxy chain contains a cycle")
		}
		seen[tag] = true
		next, err := findNATSyncOutbound(template, tag)
		if err != nil {
			return nil, err
		}
		if next == nil {
			next, err = findNATSyncOutbound(live, tag)
			if err != nil {
				return nil, err
			}
		}
		if next == nil {
			return nil, errors.New("dialer proxy is missing from the template and running core")
		}
		protocol, _ := next["protocol"].(string)
		switch strings.ToLower(protocol) {
		case "", "freedom", "blackhole", "dns", "loopback":
			return nil, errors.New("dialer proxy chain must contain only proxy outbounds")
		}
		chain = append(chain, next)
		target = next
	}
	return chain, nil
}

func natSyncIPv4(value string) bool {
	ip, err := netip.ParseAddr(value)
	return err == nil && runtime.IsPublicNATIPv4(ip)
}

func prepareNATSyncXray(template string, liveJSON []byte, tags []string, candidate string) (*natSyncPlan, error) {
	if !natSyncIPv4(candidate) {
		return nil, errors.New("candidate must be a public IPv4 address")
	}
	fields, outbounds, err := natSyncTemplate(template)
	if err != nil {
		return nil, err
	}
	targets, err := natSyncTargets(outbounds, tags)
	if err != nil {
		return nil, err
	}
	var live []natSyncOutbound
	if decodeNATSyncJSON(liveJSON, &live) != nil {
		return nil, errors.New("cannot read running Xray outbounds")
	}

	plan := &natSyncPlan{probes: make([]natSyncProbe, len(tags))}
	for i, target := range targets {
		tag := tags[i]
		active, err := findNATSyncOutbound(live, tag)
		if err != nil {
			return nil, err
		}
		nextEndpoint, fixedPort, _ := natSyncEndpoint(target)
		activeEndpoint, activePort, err := natSyncEndpoint(active)
		if err != nil || fixedPort != activePort {
			return nil, errors.New("selected outbound and running core must agree on the fixed port")
		}
		activeIP, _ := activeEndpoint["address"].(string)
		if i == 0 && natSyncIPv4(activeIP) {
			plan.currentIP = activeIP
		} else if activeIP != plan.currentIP {
			plan.currentIP = ""
		}
		if nextEndpoint["address"] != candidate || activeIP != candidate {
			plan.changed = true
		}
		nextEndpoint["address"] = candidate
		plan.probes[i] = natSyncProbe{tag: tag, port: fixedPort, currentAddress: activeIP}
	}
	// Resolve all chains after substituting every selected address. A selected
	// outbound may also appear upstream of another selected outbound.
	for i, target := range targets {
		chain, err := natSyncChain(target, outbounds, live)
		if err != nil {
			return nil, err
		}
		for _, hop := range chain {
			tag, _ := hop["tag"].(string)
			active, err := findNATSyncOutbound(live, tag)
			if err != nil || active == nil || active["protocol"] != hop["protocol"] ||
				natSyncDialer(active) != natSyncDialer(hop) {
				return nil, errors.New("saved and running dialer chains differ; apply the Xray configuration first")
			}
		}
		plan.probes[i].chain = chain
	}
	if !plan.changed {
		return plan, nil
	}
	fields["outbounds"], err = json.Marshal(outbounds)
	if err != nil {
		return nil, errors.New("cannot encode candidate outbounds")
	}
	updated, err := json.Marshal(fields)
	if err != nil {
		return nil, errors.New("cannot encode candidate Xray template")
	}
	plan.template = string(updated)
	return plan, nil
}

func runningNATSyncOutbounds() ([]byte, error) {
	process := currentXrayProcess()
	if process == nil || !process.IsRunning() {
		return nil, errors.New("Xray must be running to synchronize NAT addresses")
	}
	cfg := process.GetConfig()
	if cfg == nil {
		return nil, errors.New("running Xray configuration is unavailable")
	}
	return cfg.OutboundConfigs, nil
}

func natSyncTestURL() (string, error) {
	testURL, err := (&SettingService{}).GetXrayOutboundTestUrl()
	parsed, parseErr := url.Parse(testURL)
	if err != nil || parseErr != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return "", errors.New("configure an HTTPS outbound test URL")
	}
	return testURL, nil
}

func loadNATSyncPlan(tags []string, candidate string) (string, []byte, *natSyncPlan, error) {
	template, err := (&XraySettingService{}).GetXrayConfigTemplate()
	if err != nil {
		return "", nil, nil, errors.New("cannot load the saved Xray template")
	}
	live, err := runningNATSyncOutbounds()
	if err != nil {
		return "", nil, nil, err
	}
	plan, err := prepareNATSyncXray(template, live, tags, candidate)
	return template, live, plan, err
}

func probeNATSyncOutbound(probe natSyncProbe, testURL string) error {
	target, err := json.Marshal(probe.chain[:1])
	if err != nil {
		return errors.New("cannot encode candidate outbound")
	}
	chain, err := json.Marshal(probe.chain)
	if err != nil {
		return errors.New("cannot encode candidate dialer chain")
	}
	result, err := (&outbound.OutboundService{}).TestOutbounds(string(target), testURL, string(chain), "http")
	if err != nil || len(result) != 1 || result[0] == nil || result[0].Tag != probe.tag ||
		!result[0].Success || result[0].HTTPStatus < 200 || result[0].HTTPStatus >= 400 {
		return errors.New("candidate outbound chain did not pass the HTTPS probe")
	}
	return nil
}

func waitForNATSyncProbe(ctx context.Context, probe func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- probe() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func runNATSyncXray(tags []string, candidate string, reread func() (string, error), status *NATSyncStatus) error {
	status.State = "config_error"
	status.CurrentIP = ""
	template, live, plan, err := loadNATSyncPlan(tags, candidate)
	if err != nil {
		return err
	}
	status.CurrentIP = plan.currentIP
	if !plan.changed {
		status.State = "in_sync"
		return nil
	}
	testURL, err := natSyncTestURL()
	if err != nil {
		return err
	}

	status.State = "probe_error"
	status.ProbeIP = candidate
	status.ProbeAt = time.Now().Unix()
	status.ProbeResult = "failed"
	for _, probe := range plan.probes {
		err := probeNATSyncOutbound(probe, testURL)
		status.ProbeAt = time.Now().Unix()
		if err != nil {
			return err
		}
	}
	status.ProbeResult = "passed"
	newIP, err := reread()
	if err != nil {
		status.State = "relay_error"
		return errors.New("cannot recheck the reported NAT address")
	}
	if newIP != candidate {
		status.State = "candidate_changed"
		return errors.New("the reported NAT address changed during the probe")
	}

	status.State = "apply_error"
	latest, err := runningNATSyncOutbounds()
	if err != nil {
		return err
	}
	if !bytes.Equal(live, latest) {
		return errors.New("running Xray outbounds changed during the probe")
	}
	core := &XrayService{}
	settings := &XraySettingService{}
	if err := settings.SaveXraySettingIfMatch(plan.template, XrayTemplateETag(template)); err != nil {
		if errors.Is(err, ErrXrayTemplateChanged) {
			return ErrXrayTemplateChanged
		}
		return errors.New("cannot save the candidate Xray template")
	}
	if err := core.RestartXray(false); err != nil {
		return errors.New("cannot apply the candidate address to Xray")
	}
	live, err = runningNATSyncOutbounds()
	if err != nil {
		return err
	}
	var applied []natSyncOutbound
	if decodeNATSyncJSON(live, &applied) != nil {
		return errors.New("cannot verify the running Xray address")
	}
	for _, probe := range plan.probes {
		active, lookupErr := findNATSyncOutbound(applied, probe.tag)
		endpoint, port, endpointErr := natSyncEndpoint(active)
		if lookupErr != nil || endpointErr != nil || port != probe.port || endpoint["address"] != candidate {
			return errors.New("running Xray has not applied the candidate address")
		}
	}
	status.State = "in_sync"
	status.CurrentIP = candidate
	status.AppliedAt = time.Now().Unix()
	return nil
}
