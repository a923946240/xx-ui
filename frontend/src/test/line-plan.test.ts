import { describe, expect, it } from 'vitest';
import type { XraySettingsValue } from '@/hooks/useXraySetting';
import {
  applyLine,
  applyLinePair,
  dialerOf,
  lineCopySource,
  linePath,
  updateLineCopy,
} from '@/pages/xray/outbounds/line-plan';

function config(): XraySettingsValue {
  return {
    outbounds: [
      { tag: 'direct', protocol: 'freedom' },
      { tag: 'blocked', protocol: 'blackhole' },
      { tag: 'hk', protocol: 'vless', streamSettings: { sockopt: { dialerProxy: 'IX-OUT' } } },
      { tag: 'tw', protocol: 'shadowsocks', streamSettings: { network: 'tcp' } },
      { tag: 'sg', protocol: 'vless' },
    ],
    routing: {
      rules: [
        {
          type: 'field',
          inboundTag: ['ss'],
          user: ['tw-user'],
          outboundTag: 'sg',
          fallbackOutboundTag: 'direct',
        },
        { type: 'field', inboundTag: ['ss'], user: ['sg-user'], outboundTag: 'sg' },
      ],
    },
  };
}

describe('dedicated line plans', () => {
  it('shows the dialer chain in traffic order and rejects missing or cyclic hops', () => {
    const current = config();
    expect(linePath(current, ['IX-OUT'], 'tw', 'hk')).toEqual(['IX-OUT', 'hk', 'tw']);
    expect(linePath(current, [], 'tw', 'hk')).toBeNull();
    expect(linePath(current, [], 'IX-OUT', 'hk')).toBeNull();
    expect(linePath(current, ['IX-OUT'], 'tw', 'tw')).toBeNull();
    expect(linePath(current, ['hk'], 'tw', 'hk')).toBeNull();
  });

  it('updates only the selected outbound and rule and appends an inbound block once', () => {
    const current = config();
    const updated = applyLine(current, ['IX-OUT'], 'tw', 'hk', 0, 'ss', true);
    expect(updated?.outbounds?.[3]?.streamSettings).toEqual({
      network: 'tcp',
      sockopt: { dialerProxy: 'hk' },
    });
    expect(updated?.outbounds?.[2]).toBe(current.outbounds?.[2]);
    expect(current.routing?.rules?.[0]?.outboundTag).toBe('sg');
    expect(updated?.routing?.rules).toEqual([
      { type: 'field', inboundTag: ['ss'], user: ['tw-user'], outboundTag: 'tw' },
      current.routing?.rules?.[1],
      { type: 'field', inboundTag: ['ss'], outboundTag: 'blocked' },
    ]);
    expect(applyLine(updated!, ['IX-OUT'], 'tw', 'hk', 0, 'ss', true)?.routing?.rules).toHaveLength(
      3,
    );
  });

  it('refuses to edit routing when a script takes over or a blackhole target is absent', () => {
    const current = config();
    current.routing!.script = 'route.lua';
    expect(applyLine(current, ['IX-OUT'], 'tw', 'hk', 0, 'ss', true)).toBeNull();
    delete current.routing!.script;
    current.outbounds = current.outbounds?.filter((outbound) => outbound.tag !== 'blocked');
    expect(applyLine(current, ['IX-OUT'], 'tw', 'hk', 0, 'ss', true)).toBeNull();
  });

  it('builds distinct primary and backup dialer chains using the same final endpoint', () => {
    const current = config();
    const final = current.outbounds!.find((outbound) => outbound.tag === 'tw')!;
    final.settings = { address: 'taiwan.example', port: 35974 };
    const next = applyLinePair(
      current,
      ['IX-OUT', 'IX-B'],
      'tw',
      ['IX-OUT', 'hk'],
      ['IX-B', 'sg'],
      0,
      'ss',
      true,
    );
    expect(next).not.toBeNull();
    const rule = next!.routing!.rules![0];
    const primary = next!.outbounds!.find((outbound) => outbound.tag === rule.outboundTag)!;
    const backup = next!.outbounds!.find((outbound) => outbound.tag === rule.fallbackOutboundTag)!;
    expect(primary.tag).not.toBe(backup.tag);
    expect(primary.settings).toEqual(final.settings);
    expect(backup.settings).toEqual(final.settings);
    expect(
      linePath(next!, ['IX-OUT', 'IX-B'], primary.tag!, dialerOf(primary))?.slice(0, 2),
    ).toEqual(['IX-OUT', 'hk']);
    expect(linePath(next!, ['IX-OUT', 'IX-B'], backup.tag!, dialerOf(backup))?.[0]).toBe('IX-B');
    expect(next!.outbounds!.slice(0, current.outbounds!.length)).toEqual(current.outbounds);
    expect(next!.routing!.rules!.at(-1)).toMatchObject({
      inboundTag: ['ss'],
      outboundTag: 'blocked',
    });
    expect(
      applyLinePair(
        next!,
        ['IX-OUT', 'IX-B'],
        'tw',
        ['IX-OUT', 'hk'],
        ['IX-B', 'sg'],
        0,
        'ss',
        true,
      ),
    ).toEqual(next);
    expect(lineCopySource(primary.tag)).toBe('tw');
    expect(lineCopySource(backup.tag)).toBe('tw');
    const refreshed = updateLineCopy(
      { ...final, settings: { address: 'new.example', port: 35975 } },
      backup,
    );
    expect(refreshed.settings).toEqual({ address: 'new.example', port: 35975 });
    expect(dialerOf(refreshed)).toBe(dialerOf(backup));
    expect(refreshed.tag).toBe(backup.tag);
  });

  it('keeps the inbound-wide block opt-in and detects routes that take precedence', () => {
    const current = config();
    expect(applyLine(current, ['IX-OUT'], 'tw', 'hk', 0, 'ss')?.routing?.rules).toHaveLength(2);
    current.routing!.rules!.push({
      type: 'field',
      inboundTag: ['ss'],
      network: 'tcp',
      outboundTag: 'direct',
    });
    expect(applyLine(current, ['IX-OUT'], 'tw', 'hk', 0, 'ss', true)).toBeNull();
    current.routing!.rules!.unshift({ type: 'field', inboundTag: ['ss'], outboundTag: 'direct' });
    expect(applyLine(current, ['IX-OUT'], 'tw', 'hk', 1, 'ss')).toBeNull();
  });

  it('rejects duplicate paths, misplaced external hops and conflicting balancer prefixes', () => {
    const current = config();
    expect(applyLinePair(current, ['IX-OUT'], 'tw', ['hk'], ['hk'], 0, 'ss')).toBeNull();
    expect(
      applyLinePair(current, ['IX-OUT'], 'tw', ['IX-OUT', 'hk'], ['sg', 'IX-OUT'], 0, 'ss'),
    ).toBeNull();
    expect(applyLinePair(current, ['IX-OUT'], 'tw', ['IX-OUT', 'hk'], ['tw'], 0, 'ss')).toBeNull();
    current.routing!.balancers = [{ tag: 'existing', selector: ['xxui-'] }];
    expect(applyLinePair(current, ['IX-OUT'], 'tw', ['IX-OUT', 'hk'], ['sg'], 0, 'ss')).toBeNull();
  });
});
