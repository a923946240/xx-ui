package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/util/json_util"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

const failoverField = "fallbackOutboundTag"

// Legacy templates need no rewrite. Escaped JSON keys must also be recognized.
func hasRouteFailover(raw json_util.RawMessage) bool {
	if !bytes.Contains(raw, []byte(`"fallbackOutboundTag"`)) && bytes.IndexByte(raw, '\\') < 0 {
		return false
	}
	var routing struct{ Rules []map[string]json.RawMessage }
	_ = json.Unmarshal(raw, &routing)
	for _, rule := range routing.Rules {
		if _, ok := rule[failoverField]; ok {
			return true
		}
	}
	return false
}

// Validate against the proposed template and the same dynamic exits the UI can select.
func validateRouteFailover(cfg *xray.Config) error {
	if !hasRouteFailover(cfg.RouterConfig) {
		return nil
	}
	scratch := *cfg
	// A disabled rule may reference a retired subscription member. It can be
	// saved or switched off, but re-enabling it must validate current exits.
	scratch.RouterConfig = stripDisabledRules(scratch.RouterConfig)
	if !hasRouteFailover(scratch.RouterConfig) {
		return nil
	}
	var dynamic []string
	if database.GetDB() != nil {
		subs, err := (&OutboundSubscriptionService{}).AllActiveOutboundTags()
		if err != nil {
			return err
		}
		external, err := (&ExternalOutboundService{}).Tags()
		if err != nil {
			return err
		}
		dynamic = append(subs, external...)
	}
	return buildRouteFailover(&scratch, dynamic, true)
}

func compileRouteFailover(cfg *xray.Config) error {
	if !hasRouteFailover(cfg.RouterConfig) {
		return nil
	}
	return buildRouteFailover(cfg, nil, false)
}

type failoverBalancer struct {
	Tag      string            `json:"tag"`
	Selector []string          `json:"selector"`
	Fallback string            `json:"fallbackTag"`
	Strategy map[string]string `json:"strategy"`
}

// A balancer selector is a prefix. Give each primary an isolated loopback whose
// leading inbound rule dispatches to the exact original tag, even beside A-extra.
func buildRouteFailover(cfg *xray.Config, dynamic []string, strict bool) error {
	var router map[string]json.RawMessage
	if err := json.Unmarshal(cfg.RouterConfig, &router); err != nil {
		return err
	}
	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(router["rules"], &rules); err != nil {
		return err
	}
	var outbounds, balancers []json.RawMessage
	if len(cfg.OutboundConfigs) > 0 {
		if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
			return err
		}
	}
	if raw := router["balancers"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &balancers); err != nil {
			return err
		}
	}
	known := make(map[string]bool)
	for _, tag := range dynamic {
		known[tag] = true
	}
	for _, raw := range outbounds {
		var out struct{ Tag string }
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		known[out.Tag] = true
	}
	prefix, err := privateFailoverPrefix(cfg, dynamic, balancers)
	if err != nil {
		return err
	}
	aliasFor := make(map[string]string)
	balancerFor := make(map[[2]string]string)
	var selectors []string
	var internalRules []map[string]json.RawMessage
	blocked := prefix + "unavailable"
	needsBlock := false
	for i, rule := range rules {
		raw, ok := rule[failoverField]
		if !ok {
			continue
		}
		var ruleTag string
		_ = json.Unmarshal(rule["ruleTag"], &ruleTag)
		if ruleTag == dnsAllowRuleTag {
			return fmt.Errorf("routing rule %d: automatic failover is unavailable on the panel-managed DNS allow rule", i+1)
		}
		var primary, backup, other string
		_ = json.Unmarshal(rule["outboundTag"], &primary)
		balancerTag, hasBalancer := rule["balancerTag"]
		badBalancer := hasBalancer && (json.Unmarshal(balancerTag, &other) != nil || other != "")
		if json.Unmarshal(raw, &backup) != nil || strings.TrimSpace(backup) == "" ||
			strings.TrimSpace(primary) == "" || backup == primary || badBalancer {
			return fmt.Errorf("routing rule %d: choose two different outbound tags for automatic failover, without a balancer", i+1)
		}
		if strict && (!known[primary] || !known[backup]) {
			return fmt.Errorf("routing rule %d: primary or backup outbound no longer exists; reload outbounds", i+1)
		}
		delete(rule, failoverField)
		// A subscription member may disappear after a valid save. Never dispatch
		// to a missing handler: use the remaining backup or an explicit block.
		if !known[backup] {
			backup = blocked
			needsBlock = true
		}
		if !known[primary] {
			rule["outboundTag"] = failoverJSON(backup)
			continue
		}
		alias, exists := aliasFor[primary]
		if !exists {
			alias = failoverTag(prefix+"via-", primary)
			aliasFor[primary] = alias
			selectors = append(selectors, alias)
			outbounds = append(outbounds, failoverJSON(map[string]any{
				"tag": alias, "protocol": "loopback", "settings": map[string]string{"inboundTag": alias},
			}))
			internalRules = append(internalRules, map[string]json.RawMessage{
				"type": failoverJSON("field"), "inboundTag": failoverJSON([]string{alias}), "outboundTag": failoverJSON(primary),
			})
		}
		pair := [2]string{primary, backup}
		balancer, exists := balancerFor[pair]
		if !exists {
			balancer = failoverTag(prefix+"pair-", primary, backup)
			balancerFor[pair] = balancer
			// With a sole candidate, roundRobin prefers the primary even
			// before its first probe. A failed observation selects the backup.
			balancers = append(balancers, failoverJSON(failoverBalancer{
				Tag: balancer, Selector: []string{alias}, Fallback: backup, Strategy: map[string]string{"type": "roundRobin"},
			}))
		}
		delete(rule, "outboundTag")
		rule["balancerTag"] = failoverJSON(balancer)
	}
	if needsBlock {
		outbounds = append(outbounds, failoverJSON(map[string]string{"tag": blocked, "protocol": "blackhole"}))
	}
	if len(selectors) != 0 {
		if err := addFailoverProbes(cfg, selectors); err != nil {
			return err
		}
		router["balancers"] = failoverJSON(balancers)
		cfg.FailoverPrimaries = make(map[string]string, len(aliasFor))
		for primary, alias := range aliasFor {
			cfg.FailoverPrimaries[alias] = primary
		}
	}
	router["rules"] = failoverJSON(append(internalRules, rules...))
	cfg.RouterConfig = json_util.RawMessage(failoverJSON(router))
	cfg.OutboundConfigs = json_util.RawMessage(failoverJSON(outbounds))
	return nil
}

func failoverTag(prefix string, parts ...string) string {
	sum := sha256.Sum256(failoverJSON(parts))
	return fmt.Sprintf("%s%x", prefix, sum[:16])
}

// Only JSON primitives and already-decoded raw fields are passed here.
func failoverJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func privateFailoverPrefix(cfg *xray.Config, dynamic []string, balancers []json.RawMessage) (string, error) {
	var prefixes []string
	for _, raw := range balancers {
		var b struct{ Selector []string }
		if err := json.Unmarshal(raw, &b); err != nil {
			return "", err
		}
		prefixes = append(prefixes, b.Selector...)
	}
	// Decode string escapes before checking for collisions, including names in
	// reverse and API handlers, configured loopback inbound tags and DB exits.
	var names any
	dec := json.NewDecoder(bytes.NewReader(failoverJSON(cfg)))
	dec.UseNumber()
	if err := dec.Decode(&names); err != nil {
		return "", err
	}
	all := failoverJSON([]any{names, dynamic})
	for _, p := range []string{"xui-fo-", "@xui-fo-", "_xui-fo-", "~xui-fo-"} {
		if !bytes.Contains(all, []byte(p)) && !slices.ContainsFunc(prefixes, func(s string) bool { return strings.HasPrefix(p, s) }) {
			return p, nil
		}
	}
	return "", errors.New("routing failover: existing outbound selectors or tags cover the private failover namespace; narrow the other balancer selectors")
}

func addFailoverProbes(cfg *xray.Config, tags []string) error {
	dest := &cfg.Observatory
	present := func(v json_util.RawMessage) bool { return len(v) != 0 && string(v) != "null" }
	if present(cfg.BurstObservatory) {
		if present(cfg.Observatory) {
			return errors.New("routing failover: choose a single observatory or burstObservatory")
		}
		dest = &cfg.BurstObservatory
	}
	settings := map[string]json.RawMessage{}
	if present(*dest) {
		if err := json.Unmarshal(*dest, &settings); err != nil {
			return err
		}
	} else {
		settings["probeURL"] = failoverJSON("https://www.google.com/generate_204")
		settings["probeInterval"] = failoverJSON("15s")
		settings["enableConcurrency"] = failoverJSON(true)
	}
	var subjects []string
	if raw := settings["subjectSelector"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &subjects); err != nil {
			return err
		}
	}
	settings["subjectSelector"] = failoverJSON(append(subjects, tags...))
	*dest = json_util.RawMessage(failoverJSON(settings))
	return nil
}
