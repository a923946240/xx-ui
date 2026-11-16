import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Checkbox, Form, Modal, Select, Space, Switch, Tag, Typography } from 'antd';
import { ArrowDownOutlined, ArrowUpOutlined, CloseOutlined } from '@ant-design/icons';
import type { OutboundTestState, SetTemplate, XraySettingsValue } from '@/hooks/useXraySetting';
import {
  applyLine,
  applyLinePair,
  canSetLine,
  dialerOf,
  inboundBlockConflicts,
  lineCopySource,
  linePath,
  shadowedBy,
} from './line-plan';

interface Props {
  config: XraySettingsValue;
  managedTags: string[];
  setConfig: SetTemplate;
  testStates: Record<number, OutboundTestState>;
  onTest: (index: number, mode: string) => void;
  onClose: () => void;
}

export default function LineWizard({
  config,
  managedTags,
  setConfig,
  testStates,
  onTest,
  onClose,
}: Props) {
  const { t } = useTranslation();
  const uid = useId();
  const [finalTag, setFinalTag] = useState('');
  const [upstream, setUpstream] = useState('');
  const [failover, setFailover] = useState(false);
  const [primaryHops, setPrimaryHops] = useState<string[]>([]);
  const [backupHops, setBackupHops] = useState<string[]>([]);
  const [ruleIndex, setRuleIndex] = useState<number>();
  const [blockUnmatched, setBlockUnmatched] = useState(false);
  const [probe, setProbe] = useState<{ config: XraySettingsValue; index: number }>();
  const outbounds = config.outbounds ?? [];
  const rules = config.routing?.rules ?? [];
  const finalIndex = outbounds.findIndex((outbound) => outbound.tag === finalTag);
  const final = outbounds[finalIndex];
  const selected = ruleIndex === undefined ? undefined : rules[ruleIndex];
  const inbound = selected?.inboundTag?.[0];
  const available = outbounds.filter(canSetLine);
  const options = available.map((outbound) => ({
    label: lineCopySource(outbound.tag)
      ? t('pages.xray.lineWizard.copyLabel', {
          tag: outbound.tag,
          source: lineCopySource(outbound.tag),
        })
      : `${outbound.tag} · ${outbound.protocol}`,
    value: outbound.tag!,
  }));
  const path = final ? linePath(config, managedTags, finalTag, upstream) : undefined;
  const canRoute = !config.routing?.script?.trim();
  const blockAvailable = outbounds.some(
    (outbound) => outbound.tag === 'blocked' && outbound.protocol === 'blackhole',
  );
  const shadow = inbound && ruleIndex !== undefined ? shadowedBy(config, ruleIndex, inbound) : [];
  const nextRuleConfig =
    selected && ruleIndex !== undefined
      ? {
          ...config,
          routing: {
            ...config.routing,
            rules: rules.map((rule, index) =>
              index === ruleIndex
                ? {
                    ...rule,
                    outboundTag: finalTag,
                    fallbackOutboundTag: undefined,
                    balancerTag: undefined,
                  }
                : rule,
            ),
          },
        }
      : config;
  const conflicts = inbound ? inboundBlockConflicts(nextRuleConfig, inbound) : [];
  const next = failover
    ? ruleIndex === undefined
      ? null
      : applyLinePair(
          config,
          managedTags,
          finalTag,
          primaryHops,
          backupHops,
          ruleIndex,
          inbound,
          blockUnmatched,
        )
    : final && (ruleIndex === undefined || selected)
      ? applyLine(config, managedTags, finalTag, upstream, ruleIndex, inbound, blockUnmatched)
      : null;
  const draft = !!final && dialerOf(final) !== upstream;
  const probeState =
    probe?.config === config && probe.index === finalIndex ? testStates[finalIndex] : undefined;
  const result = probeState?.result;

  function selectFinal(tag: string) {
    setFinalTag(tag);
    const outbound = outbounds.find((value) => value.tag === tag);
    const previous = outbound ? dialerOf(outbound) : '';
    setUpstream(previous);
    setPrimaryHops(linePath(config, managedTags, tag, previous)?.slice(0, -1) ?? []);
    setBackupHops([]);
    setProbe(undefined);
  }

  const hopOptions = [
    ...options.filter((option) => option.value !== finalTag && !managedTags.includes(option.value)),
    ...[...new Set(managedTags)]
      .filter((tag) => tag !== finalTag && !outbounds.some((outbound) => outbound.tag === tag))
      .map((tag) => ({ label: tag, value: tag })),
  ];

  function pathEditor(
    label: string,
    id: string,
    hops: string[],
    setHops: (next: string[]) => void,
  ) {
    return (
      <section className="line-wizard-lane" aria-labelledby={id + '-title'}>
        <Typography.Text strong id={id + '-title'}>
          {label}
        </Typography.Text>
        <ol className="line-wizard-steps">
          {hops.map((hop, index) => {
            function move(offset: number) {
              const target = index + offset;
              if (target < 0 || target >= hops.length) return;
              const updated = [...hops];
              [updated[index], updated[target]] = [updated[target], updated[index]];
              setHops(updated);
            }
            return (
              <li key={hop} className="line-wizard-step">
                <span className="line-wizard-step-number" aria-hidden>
                  {index + 1}
                </span>
                <span className="line-wizard-step-name" title={hop}>
                  {hop}
                </span>
                <Space size={2}>
                  <Button
                    size="small"
                    icon={<ArrowUpOutlined aria-hidden />}
                    disabled={index === 0}
                    aria-label={t('pages.xray.lineWizard.moveEarlier', { hop })}
                    onClick={() => move(-1)}
                  />
                  <Button
                    size="small"
                    icon={<ArrowDownOutlined aria-hidden />}
                    disabled={index === hops.length - 1}
                    aria-label={t('pages.xray.lineWizard.moveLater', { hop })}
                    onClick={() => move(1)}
                  />
                  <Button
                    size="small"
                    type="text"
                    icon={<CloseOutlined aria-hidden />}
                    aria-label={t('pages.xray.lineWizard.removeHop', { hop })}
                    onClick={() => setHops(hops.filter((value) => value !== hop))}
                  />
                </Space>
              </li>
            );
          })}
        </ol>
        <Select<string>
          id={id}
          aria-label={label + ' · ' + t('pages.xray.lineWizard.addHop')}
          showSearch
          optionFilterProp="label"
          disabled={!final}
          value={null}
          placeholder={t('pages.xray.lineWizard.addHop')}
          options={hopOptions.filter((option) => !hops.includes(option.value))}
          onChange={(hop) => setHops([...hops, hop])}
        />
      </section>
    );
  }

  return (
    <Modal
      open
      width={failover ? 760 : 640}
      style={{ top: 24 }}
      styles={{ body: { maxHeight: 'calc(100dvh - 150px)', overflowY: 'auto', paddingInline: 4 } }}
      title={t('pages.xray.lineWizard.title')}
      onCancel={onClose}
      cancelText={t('cancel')}
      okText={t('pages.xray.lineWizard.stage')}
      okButtonProps={{ disabled: !next || (!failover && !draft && ruleIndex === undefined) }}
      onOk={() => {
        if (!next) return;
        setConfig((current) => (current === config ? next : current));
        onClose();
      }}
    >
      <Form layout="vertical" className="line-wizard-form">
        <Form.Item label={t('pages.xray.lineWizard.final')} htmlFor={uid + '-final'}>
          <Select
            id={uid + '-final'}
            showSearch
            optionFilterProp="label"
            options={options}
            value={finalTag || undefined}
            onChange={selectFinal}
            placeholder={t('pages.xray.lineWizard.finalPlaceholder')}
          />
        </Form.Item>
        <Form.Item label={t('pages.xray.lineWizard.autoFailover')} htmlFor={uid + '-failover'}>
          <Switch
            id={uid + '-failover'}
            checked={failover}
            onChange={(checked) => {
              if (checked) setPrimaryHops(path?.slice(0, -1) ?? []);
              setFailover(checked);
              setProbe(undefined);
            }}
          />
        </Form.Item>
        {failover ? (
          <div className="line-wizard-topology">
            <div className="line-wizard-pair">
              {pathEditor(
                t('pages.xray.lineWizard.primaryPath'),
                uid + '-primary',
                primaryHops,
                setPrimaryHops,
              )}
              {pathEditor(
                t('pages.xray.lineWizard.backupPath'),
                uid + '-backup',
                backupHops,
                setBackupHops,
              )}
            </div>
            {final && (
              <div className="line-wizard-destination">
                <ArrowDownOutlined aria-hidden />
                <Typography.Text type="secondary">
                  {t('pages.xray.lineWizard.final')}
                </Typography.Text>
                <Typography.Text strong>{finalTag}</Typography.Text>
              </div>
            )}
          </div>
        ) : (
          <Form.Item label={t('pages.xray.lineWizard.upstream')} htmlFor={uid + '-upstream'}>
            <Select
              id={uid + '-upstream'}
              disabled={!final}
              showSearch
              optionFilterProp="label"
              value={upstream}
              options={[
                { label: t('pages.xray.lineWizard.noUpstream'), value: '' },
                ...options.filter(
                  (option) => option.value !== finalTag && !managedTags.includes(option.value),
                ),
                ...[...new Set(managedTags)]
                  .filter(
                    (tag) =>
                      tag !== finalTag && !outbounds.some((outbound) => outbound.tag === tag),
                  )
                  .map((tag) => ({ label: tag, value: tag })),
              ]}
              onChange={(tag) => {
                setUpstream(tag);
                setProbe(undefined);
              }}
            />
          </Form.Item>
        )}
        <Form.Item
          label={t(failover ? 'pages.xray.lineWizard.ruleRequired' : 'pages.xray.lineWizard.rule')}
          required={failover}
          htmlFor={uid + '-rule'}
        >
          <Select
            id={uid + '-rule'}
            allowClear
            showSearch
            optionFilterProp="label"
            disabled={!canRoute}
            value={ruleIndex}
            placeholder={
              failover
                ? t('pages.xray.lineWizard.ruleRequired')
                : t('pages.xray.lineWizard.rulePlaceholder')
            }
            options={rules.flatMap((rule, index) =>
              rule.enabled !== false &&
              rule.ruleTag !== 'xui-dns-allow' &&
              rule.inboundTag?.length === 1 &&
              rule.inboundTag[0] !== 'api' &&
              (rule.outboundTag || rule.balancerTag) &&
              rule.outboundTag !== 'blocked'
                ? [
                    {
                      value: index,
                      label: `#${index + 1} · ${rule.inboundTag[0]} · ${rule.user?.join(', ') || rule.comment || rule.outboundTag || rule.balancerTag}`,
                    },
                  ]
                : [],
            )}
            onChange={(index) => {
              setRuleIndex(index);
              setBlockUnmatched(false);
            }}
          />
        </Form.Item>
        {inbound && (
          <Form.Item extra={t('pages.xray.lineWizard.blockImpact', { inbound })}>
            <Checkbox
              checked={blockUnmatched}
              disabled={!blockAvailable || conflicts.length > 0}
              onChange={(event) => setBlockUnmatched(event.target.checked)}
            >
              {t('pages.xray.lineWizard.blockUnmatched')}
            </Checkbox>
          </Form.Item>
        )}
      </Form>
      <Space orientation="vertical" style={{ width: '100%' }} size="middle">
        {shadow.length > 0 && (
          <Alert
            type="error"
            title={t('pages.xray.lineWizard.shadowRules', { rules: shadow.join(', ') })}
          />
        )}
        {conflicts.length > 0 && (
          <Alert
            type="warning"
            title={t('pages.xray.lineWizard.blockConflict', { rules: conflicts.join(', ') })}
          />
        )}
        {!failover && path === null && (
          <Alert type="error" title={t('pages.xray.lineWizard.invalid')} />
        )}
        {failover && (
          <Alert
            type={next ? 'info' : 'warning'}
            title={t('pages.xray.lineWizard.failoverEffect')}
            description={next ? undefined : t('pages.xray.lineWizard.failoverRequirements')}
          />
        )}
        {!failover && path && (
          <section aria-label={t('pages.xray.lineWizard.path')}>
            <Typography.Text strong>{t('pages.xray.lineWizard.path')}</Typography.Text>
            <div className="line-wizard-path">
              <div>{inbound || t('pages.xray.lineWizard.panelSource')}</div>
              {path.map((tag, index) => (
                <div key={tag} className="line-wizard-hop">
                  <ArrowDownOutlined aria-hidden />
                  <div>
                    <Typography.Text strong>{tag}</Typography.Text>{' '}
                    {index === path.length - 1 && (
                      <Tag color="blue">{t('pages.xray.lineWizard.final')}</Tag>
                    )}
                  </div>
                </div>
              ))}
            </div>
          </section>
        )}
        {!failover && selected && (
          <Alert type="warning" title={t('pages.xray.lineWizard.routeEffect')} />
        )}
        {!failover && path && !draft && (
          <Space wrap>
            <Button
              loading={!!probeState?.testing}
              onClick={() => {
                setProbe({ config, index: finalIndex });
                onTest(finalIndex, 'http');
              }}
            >
              {t('pages.xray.lineWizard.probe')}
            </Button>
            {result && (
              <Tag color={result.success ? 'success' : 'error'}>
                {t(
                  result.success
                    ? 'pages.xray.lineWizard.probeSuccess'
                    : 'pages.xray.lineWizard.probeFailure',
                )}
                {result.success && result.delay !== undefined ? ` · ${result.delay} ms` : ''}
                {result.egress?.ipv4 || result.egress?.ipv6
                  ? ` · ${result.egress.ipv4 || result.egress.ipv6}`
                  : ''}
              </Tag>
            )}
          </Space>
        )}
        <Typography.Text type="secondary">{t('pages.xray.lineWizard.scope')}</Typography.Text>
      </Space>
    </Modal>
  );
}
