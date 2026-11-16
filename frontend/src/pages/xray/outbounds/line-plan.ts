import type { XraySettingsValue } from '@/hooks/useXraySetting';
import type { RuleObject } from '@/schemas/routing';

type Outbound = NonNullable<XraySettingsValue['outbounds']>[number];
const finalProtocols = new Set(['shadowsocks', 'socks', 'http', 'vless', 'vmess', 'trojan']);

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

export function dialerOf(outbound: Outbound): string {
  const dialer = record(record(outbound.streamSettings).sockopt).dialerProxy;
  return typeof dialer === 'string' ? dialer : '';
}

export function canSetLine(outbound: Outbound): boolean {
  return !!outbound.tag && finalProtocols.has(outbound.protocol ?? '') && !outbound.proxySettings;
}

export function lineCopySource(tag: string | undefined): string | undefined {
  const encoded = tag?.match(/^xxui-line-[0-9a-z]+~(.+)$/)?.[1];
  if (!encoded) return undefined;
  try {
    return decodeURIComponent(encoded);
  } catch {
    return undefined;
  }
}

export function updateLineCopy(source: Outbound, copy: Outbound): Outbound {
  const stream = record(source.streamSettings);
  const sockopt = { ...record(stream.sockopt) };
  const upstream = dialerOf(copy);
  if (upstream) sockopt.dialerProxy = upstream;
  else delete sockopt.dialerProxy;
  return { ...source, tag: copy.tag, streamSettings: { ...stream, sockopt } };
}

export function linePath(
  config: XraySettingsValue,
  managedTags: string[],
  finalTag: string,
  upstream: string,
): string[] | null {
  const path = [finalTag];
  let next = upstream;
  while (next) {
    if (path.includes(next)) return null;
    const matches = config.outbounds?.filter((outbound) => outbound.tag === next) ?? [];
    const outbound = matches[0];
    if (
      matches.length > 1 ||
      (outbound && managedTags.includes(next)) ||
      (outbound && !canSetLine(outbound)) ||
      (!outbound && !managedTags.includes(next))
    ) {
      return null;
    }
    path.push(next);
    next = outbound ? dialerOf(outbound) : '';
  }
  return path.reverse();
}

export function isInboundBlock(rule: RuleObject, inbound: string): boolean {
  const matchKeys = new Set(['type', 'enabled', 'comment', 'ruleTag', 'inboundTag', 'outboundTag']);
  return (
    rule.enabled !== false &&
    rule.outboundTag === 'blocked' &&
    rule.inboundTag?.length === 1 &&
    rule.inboundTag[0] === inbound &&
    Object.entries(rule).every(([key, value]) => matchKeys.has(key) || value === undefined)
  );
}

export function applyLine(
  config: XraySettingsValue,
  managedTags: string[],
  finalTag: string,
  upstream: string,
  ruleIndex: number | undefined,
  inbound: string | undefined,
  blockUnmatched = false,
): XraySettingsValue | null {
  const outbounds = config.outbounds ?? [];
  const final = outbounds.find((outbound) => outbound.tag === finalTag);
  if (
    !final ||
    !canSetLine(final) ||
    managedTags.includes(finalTag) ||
    outbounds.filter((outbound) => outbound.tag === finalTag).length !== 1 ||
    !linePath(config, managedTags, finalTag, upstream)
  ) {
    return null;
  }
  const stream = record(final.streamSettings);
  const sockopt = { ...record(stream.sockopt) };
  if (upstream) sockopt.dialerProxy = upstream;
  else delete sockopt.dialerProxy;
  const next = {
    ...config,
    outbounds: outbounds.map((outbound) =>
      outbound === final ? { ...outbound, streamSettings: { ...stream, sockopt } } : outbound,
    ),
  };
  if (ruleIndex === undefined) return next;
  return routeLine(next, finalTag, undefined, ruleIndex, inbound, blockUnmatched);
}

export function inboundBlockConflicts(config: XraySettingsValue, inbound: string): number[] {
  const rules = config.routing?.rules ?? [];
  const cutoff = rules.findIndex((rule) => isInboundBlock(rule, inbound));
  const before = cutoff === -1 ? rules : rules.slice(0, cutoff);
  const blackholes = new Set(
    config.outbounds
      ?.filter((outbound) => outbound.protocol === 'blackhole')
      .map((outbound) => outbound.tag),
  );
  return before.flatMap((rule, index) =>
    rule.enabled !== false &&
    (!rule.inboundTag?.length || rule.inboundTag.includes(inbound)) &&
    !rule.user?.length &&
    (!blackholes.has(rule.outboundTag) || rule.balancerTag || rule.fallbackOutboundTag)
      ? [index + 1]
      : [],
  );
}

export function shadowedBy(config: XraySettingsValue, index: number, inbound: string): number[] {
  const rules = config.routing?.rules ?? [];
  const user = rules[index]?.user ?? [];
  const routingFields = new Set([
    'type',
    'enabled',
    'comment',
    'ruleTag',
    'outboundTag',
    'fallbackOutboundTag',
    'balancerTag',
    'webhook',
    'inboundTag',
    'user',
  ]);
  return rules
    .slice(0, index)
    .flatMap((rule, earlier) =>
      rule.enabled !== false &&
      (!rule.inboundTag?.length || rule.inboundTag.includes(inbound)) &&
      (!rule.user?.length ||
        (user.length > 0 && user.every((value) => rule.user?.includes(value)))) &&
      Object.entries(rule).every(([key, value]) => routingFields.has(key) || value === undefined)
        ? [earlier + 1]
        : [],
    );
}

function routeLine(
  config: XraySettingsValue,
  finalTag: string,
  backupTag: string | undefined,
  ruleIndex: number,
  inbound: string | undefined,
  blockUnmatched: boolean,
): XraySettingsValue | null {
  const rules = config.routing?.rules ?? [];
  const rule = rules[ruleIndex];
  if (
    !rule ||
    !inbound ||
    config.routing?.script?.trim() ||
    rule.ruleTag === 'xui-dns-allow' ||
    rule.enabled === false ||
    rule.inboundTag?.length !== 1 ||
    rule.inboundTag[0] !== inbound ||
    shadowedBy(config, ruleIndex, inbound).length > 0 ||
    (blockUnmatched &&
      !config.outbounds?.some(
        (outbound) => outbound.tag === 'blocked' && outbound.protocol === 'blackhole',
      ))
  ) {
    return null;
  }
  const updated = { ...rule, outboundTag: finalTag };
  delete updated.balancerTag;
  if (backupTag) updated.fallbackOutboundTag = backupTag;
  else delete updated.fallbackOutboundTag;
  const nextRules = rules.map((value, index) => (index === ruleIndex ? updated : value));
  const next = { ...config, routing: { ...config.routing, rules: nextRules } };
  if (blockUnmatched && inboundBlockConflicts(next, inbound).length) return null;
  if (blockUnmatched && !nextRules.some((value) => isInboundBlock(value, inbound))) {
    nextRules.push({ type: 'field', inboundTag: [inbound], outboundTag: 'blocked' });
  }
  return next;
}

export function applyLinePair(
  config: XraySettingsValue,
  managedTags: string[],
  finalTag: string,
  primaryHops: string[],
  backupHops: string[],
  ruleIndex: number,
  inbound: string | undefined,
  blockUnmatched = false,
): XraySettingsValue | null {
  const original = config.outbounds ?? [];
  const outbounds = [...original];
  if (
    !finalTag ||
    primaryHops.length === 0 ||
    backupHops.length === 0 ||
    JSON.stringify(primaryHops) === JSON.stringify(backupHops)
  )
    return null;

  const occupied = new Set([...managedTags, ...original.map((outbound) => outbound.tag ?? '')]);
  const prefixes = config.routing?.balancers?.flatMap((balancer) => balancer.selector) ?? [];
  let serial = 0;
  function compose(hops: string[]) {
    const path = [...hops, finalTag];
    if (new Set(path).size !== path.length) return null;
    let before = '';
    for (const [index, tag] of path.entries()) {
      const matches = original.filter((outbound) => outbound.tag === tag);
      if (matches.length === 0 && index === 0 && managedTags.includes(tag)) {
        before = tag;
        continue;
      }
      const base = matches[0];
      if (!base || matches.length !== 1 || managedTags.includes(tag) || !canSetLine(base))
        return null;
      if (dialerOf(base) === before) {
        before = tag;
        continue;
      }
      const stream = record(base.streamSettings);
      const sockopt = { ...record(stream.sockopt) };
      if (before) sockopt.dialerProxy = before;
      else delete sockopt.dialerProxy;
      const source = lineCopySource(base.tag) ?? tag;
      const clone = { ...base, tag: undefined, streamSettings: { ...stream, sockopt } };
      const existing = outbounds.find(
        (outbound) =>
          lineCopySource(outbound.tag) === source &&
          outbound.tag &&
          !managedTags.includes(outbound.tag) &&
          JSON.stringify({ ...outbound, tag: undefined }) === JSON.stringify(clone),
      );
      if (existing?.tag) {
        before = existing.tag;
        continue;
      }
      // Balancers match by tag prefix. Do not add a copy under a prefix they own.
      if (prefixes.some((prefix) => 'xxui-line-'.startsWith(prefix))) return null;
      let name: string;
      do {
        if (serial >= 4096) return null;
        name = 'xxui-line-' + (++serial).toString(36) + '~' + encodeURIComponent(source);
      } while (occupied.has(name) || prefixes.some((prefix) => name.startsWith(prefix)));
      occupied.add(name);
      outbounds.push({ ...clone, tag: name });
      before = name;
    }
    return before;
  }
  const primary = compose(primaryHops);
  const backup = compose(backupHops);
  if (!primary || !backup || primary === backup) return null;
  return routeLine({ ...config, outbounds }, primary, backup, ruleIndex, inbound, blockUnmatched);
}
