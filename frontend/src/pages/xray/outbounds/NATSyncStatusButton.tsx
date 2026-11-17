import { useId, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Descriptions, Form, Modal, Select, Space, Spin, Tag } from 'antd';
import { ReloadOutlined, SyncOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { z } from 'zod';
import { keys } from '@/api/queryKeys';
import { isNATHTTPSNode, natPoolSchema } from '@/api/nat-pool';
import { useNodesQuery, type NodeRecord } from '@/api/queries/useNodesQuery';
import { NATSyncOutboundCheckSchema, NATSyncWizardResultSchema } from '@/generated/zod';
import { NATSyncStatusSchema } from '@/schemas/xray';
import { HttpUtil } from '@/utils';
import './OutboundsTab.css';

const endpoint = '/panel/api/xray/nat-sync';
const queryKey = keys.xray.natSyncStatus();
const configSchema = z.object({
  enabled: z.boolean(),
  relayNodeId: z.number().int().nonnegative(),
  reporterNodeId: z.number().int().nonnegative(),
  poolEntryId: z.string().optional(),
  outboundTags: z
    .array(z.string())
    .nullish()
    .transform((tags) => tags ?? []),
});
const viewSchema = z.object({
  config: configSchema,
  status: NATSyncStatusSchema.nullish(),
});
const wizardResultSchema = NATSyncWizardResultSchema.extend({
  state: z.enum([
    'ready',
    'report_error',
    'config_error',
    'probe_error',
    'candidate_changed',
    'config_changed',
  ]),
  candidateIp: z.ipv4().optional(),
  seenAt: z.number().int().positive().optional(),
  checks: z.array(
    NATSyncOutboundCheckSchema.extend({
      port: z.number().int().positive(),
      state: z.enum(['passed', 'failed']),
    }),
  ),
});

type SyncConfig = z.infer<typeof configSchema>;
type WizardResult = z.infer<typeof wizardResultSchema>;
type Action = 'enable' | 'disable';

function nodeOptions(nodes: NodeRecord[], eligible: NodeRecord[], selected?: number) {
  const choices = eligible.map((node) => ({
    value: node.id,
    label: node.name || '#' + node.id,
    disabled: false,
  }));
  if (selected && !choices.some(({ value }) => value === selected)) {
    choices.push({
      value: selected,
      label: nodes.find((node) => node.id === selected)?.name || '#' + selected,
      disabled: true,
    });
  }
  return choices;
}

function settingsKey(config?: SyncConfig) {
  return (
    config &&
    JSON.stringify([
      config.relayNodeId,
      config.reporterNodeId,
      config.poolEntryId,
      config.outboundTags,
    ])
  );
}

export function NATSyncWizard({
  outboundTags,
  initialTag,
  onClose,
}: {
  outboundTags: string[];
  initialTag?: string;
  onClose: () => void;
}) {
  const [draft, setDraft] = useState<SyncConfig>();
  const [active, setActive] = useState<Action>();
  const [notice, setNotice] = useState<{ type: 'success' | 'error'; text: string }>();
  const [result, setResult] = useState<WizardResult>();
  const { t, i18n } = useTranslation();
  const uid = useId();
  const client = useQueryClient();
  const label = (key: string) => t('pages.xray.natSync.' + key);
  const wizard = (key: string, fields?: Record<string, string>) =>
    t('pages.xray.natSync.wizard.' + key, fields);
  const poolLabel = (key: string) => t('pages.xray.natSync.pool.' + key);

  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      const response = await HttpUtil.get(endpoint, undefined, { silent: true, signal });
      if (!response.success) throw new Error(response.msg);
      return viewSchema.parse(response.obj);
    },
    refetchOnWindowFocus: false,
    refetchInterval: (query) => (query.state.data?.config.enabled ? 5000 : false),
  });
  const {
    nodes,
    loading: nodesLoading,
    fetchError: nodesError,
    refetch: refetchNodes,
  } = useNodesQuery();
  const eligibleNodes = nodes.filter(isNATHTTPSNode);

  const saved = query.data?.config;
  const suggestedTag = initialTag ?? (outboundTags.length === 1 ? outboundTags[0] : undefined);
  const config =
    draft ??
    (saved &&
    suggestedTag &&
    (initialTag ? !saved.outboundTags.includes(initialTag) : !saved.outboundTags.length)
      ? { ...saved, outboundTags: [suggestedTag] }
      : saved);
  const status = query.data?.status;
  const changed = settingsKey(config) !== settingsKey(saved);
  const relayNodeId = config?.relayNodeId ?? 0;
  const hasRelay = eligibleNodes.some(({ id }) => id === relayNodeId);
  const pool = useQuery({
    queryKey: [...queryKey, 'pool', relayNodeId],
    enabled: hasRelay,
    queryFn: async ({ signal }) => {
      const response = await HttpUtil.get(
        endpoint + '/pool',
        { relayNodeId },
        { silent: true, signal },
      );
      if (!response.success) throw new Error(response.msg);
      return natPoolSchema.parse(response.obj);
    },
    retry: false,
    refetchInterval: 10000,
  });
  const selectedEntry = pool.data?.find((entry) => entry.id === config?.poolEntryId);
  const complete =
    !!config &&
    hasRelay &&
    !!selectedEntry &&
    !pool.isError &&
    config.outboundTags.length > 0 &&
    config.outboundTags.every((tag) => outboundTags.includes(tag));
  const nodesUnavailable = nodesLoading || !!nodesError;
  const busy = !!active || query.isPending;
  const time = (seconds?: number) =>
    seconds ? new Date(seconds * 1000).toLocaleString(i18n.language) : '—';

  function edit(fields: Partial<SyncConfig>) {
    if (!config) return;
    setDraft({ ...config, ...fields });
    setNotice(undefined);
    setResult(undefined);
  }

  async function perform(action: Action) {
    const disable = action === 'disable';
    const selection = disable ? saved : config;
    if (!selection || active) return;
    const requested = { ...selection, enabled: action === 'enable' };
    setActive(action);
    setNotice(undefined);
    setResult(undefined);
    try {
      const response = await HttpUtil.post(
        endpoint + (disable ? '/config' : '/wizard'),
        requested,
        {
          silent: true,
          headers: { 'Content-Type': 'application/json' },
        },
      );
      if (!response.success) {
        setNotice({ type: 'error', text: response.msg || label('requestFailed') });
        return;
      }
      if (!disable) {
        const outcome = wizardResultSchema.parse(response.obj);
        setResult(outcome);
        if (outcome.enabled) {
          await client.cancelQueries({ queryKey });
          client.setQueryData(queryKey, { config: requested });
          setDraft(undefined);
        }
        return;
      }

      const view = viewSchema.parse(response.obj);
      await client.cancelQueries({ queryKey });
      client.setQueryData(queryKey, view);
      setDraft(undefined);
      setNotice({ type: 'success', text: wizard('disabled') });
    } catch {
      setNotice({ type: 'error', text: label('requestFailed') });
    } finally {
      setActive(undefined);
    }
  }

  const options = Array.from(new Set([...outboundTags, ...(config?.outboundTags ?? [])])).map(
    (tag) => ({ value: tag, label: tag, disabled: !outboundTags.includes(tag) }),
  );

  return (
    <Modal
      title={wizard('title')}
      open
      width={620}
      style={{ top: 32 }}
      onCancel={() => {
        if (!active) onClose();
      }}
      footer={
        <Space wrap>
          <Button
            icon={<ReloadOutlined aria-hidden />}
            loading={query.isFetching || nodesLoading}
            disabled={!!active}
            onClick={() =>
              void Promise.all([query.refetch(), refetchNodes(), hasRelay && pool.refetch()])
            }
          >
            {label('refresh')}
          </Button>
          {saved?.enabled && (
            <Button
              loading={active === 'disable'}
              disabled={busy}
              onClick={() => void perform('disable')}
            >
              {wizard('disable')}
            </Button>
          )}
          <Button
            type="primary"
            loading={active === 'enable'}
            disabled={
              busy || query.isError || nodesUnavailable || !complete || (saved?.enabled && !changed)
            }
            onClick={() => void perform('enable')}
          >
            {wizard('enable')}
          </Button>
        </Space>
      }
    >
      {query.isPending ? (
        <Spin />
      ) : !config ? (
        <Alert type="error" showIcon title={label('loadFailed')} />
      ) : (
        <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
          {query.isError && <Alert type="error" showIcon title={label('loadFailed')} />}
          {nodesError ? (
            <Alert type="error" showIcon title={label('nodesLoadFailed')} />
          ) : !nodesLoading && !eligibleNodes.length ? (
            <Alert
              type="info"
              showIcon
              title={poolLabel('needNode')}
              description={<Link to="/nodes">{t('pages.nodes.addNode')}</Link>}
            />
          ) : null}
          {outboundTags.length === 0 && (
            <Alert type="info" showIcon title={wizard('needOutbounds')} />
          )}
          <Form className="nat-sync-form" layout="vertical" disabled={busy}>
            <Form.Item label={poolLabel('relay')} htmlFor={uid + 'relay'} required>
              <Select
                id={uid + 'relay'}
                loading={nodesLoading}
                disabled={busy || nodesUnavailable}
                allowClear
                options={nodeOptions(nodes, eligibleNodes, config.relayNodeId)}
                value={config.relayNodeId || undefined}
                onChange={(id) =>
                  edit({ relayNodeId: id ?? 0, reporterNodeId: 0, poolEntryId: undefined })
                }
              />
            </Form.Item>
            <Form.Item
              label={poolLabel('source')}
              htmlFor={uid + 'source'}
              extra={selectedEntry && label('reportedAt') + ' ' + time(selectedEntry.seenAt)}
              required
            >
              <Select
                id={uid + 'source'}
                loading={hasRelay && pool.isFetching}
                disabled={busy || !hasRelay || !pool.data?.length || pool.isError}
                options={pool.data?.map((entry) => ({
                  value: entry.id,
                  label: entry.name + ' · ' + entry.candidateIp,
                }))}
                value={selectedEntry?.id}
                onChange={(poolEntryId) => edit({ poolEntryId, reporterNodeId: 0 })}
              />
            </Form.Item>
            <Form.Item
              label={label('outboundTags')}
              htmlFor={uid + 'outbounds'}
              extra={label('outboundTagsHint')}
              required
            >
              <Select
                id={uid + 'outbounds'}
                mode="multiple"
                allowClear
                value={config.outboundTags}
                options={options}
                onChange={(tags) => edit({ outboundTags: tags })}
              />
            </Form.Item>
          </Form>
          {hasRelay && pool.isError && (
            <Alert type="error" showIcon title={poolLabel('loadFailed')} />
          )}
          {hasRelay && !pool.isFetching && !pool.isError && !pool.data?.length && (
            <Alert type="info" showIcon title={poolLabel('empty')} />
          )}
          {saved?.enabled && changed && (
            <Alert type="warning" showIcon title={wizard('replaceHint')} />
          )}
          {notice && <Alert type={notice.type} showIcon title={notice.text} />}
          {result?.candidateIp && (
            <Alert
              type="info"
              showIcon
              title={wizard('reported', { ip: result.candidateIp })}
              description={label('reportedAt') + ' ' + time(result.seenAt)}
            />
          )}
          {result && (
            <Alert
              type={result.state === 'ready' ? 'success' : 'error'}
              showIcon
              title={wizard(result.enabled ? 'activated' : result.state)}
              description={result.message}
            />
          )}
          {result?.checks.map((check) => (
            <Descriptions
              key={check.tag}
              size="small"
              column={1}
              title={
                <Space>
                  <span>{check.tag}</span>
                  <Tag color={check.state === 'passed' ? 'success' : 'error'}>
                    {label(check.state)}
                  </Tag>
                </Space>
              }
              items={[
                {
                  key: 'current',
                  label: wizard('currentEndpoint'),
                  children: check.currentAddress
                    ? formatEndpoint(check.currentAddress, check.port)
                    : '—',
                },
                {
                  key: 'candidate',
                  label: wizard('testedEndpoint'),
                  children: result.candidateIp && formatEndpoint(result.candidateIp, check.port),
                },
              ]}
            />
          ))}
          {status ? (
            <Descriptions
              column={{ xs: 1, sm: 2 }}
              size="small"
              items={[
                {
                  key: 'state',
                  label: label('state'),
                  children: (
                    <Tag color={status.state === 'in_sync' ? 'success' : 'warning'}>
                      {label(status.state)}
                    </Tag>
                  ),
                },
                { key: 'currentIp', label: label('currentIp'), children: status.currentIp ?? '—' },
                {
                  key: 'candidateIp',
                  label: label('candidateIp'),
                  children: status.candidateIp ?? '—',
                },
                {
                  key: 'probeResult',
                  label: label('probeResult'),
                  children: status.probeResult ? (
                    <Space size="small" wrap>
                      <Tag color={status.probeResult === 'passed' ? 'success' : 'error'}>
                        {label(status.probeResult)}
                      </Tag>
                      {status.probeIp}
                    </Space>
                  ) : (
                    '—'
                  ),
                },
                { key: 'probeAt', label: label('probeAt'), children: time(status.probeAt) },
                { key: 'appliedAt', label: label('appliedAt'), children: time(status.appliedAt) },
                { key: 'updatedAt', label: label('updatedAt'), children: time(status.updatedAt) },
              ]}
            />
          ) : (
            saved?.enabled && <Alert type="info" showIcon title={label('noStatus')} />
          )}
        </Space>
      )}
    </Modal>
  );
}

function formatEndpoint(address: string, port: number) {
  return (address.includes(':') ? `[${address}]` : address) + ':' + port;
}

export default function NATSyncStatusButton({ outboundTags }: { outboundTags: string[] }) {
  const [open, setOpen] = useState(false);
  const { t } = useTranslation();

  return (
    <>
      <Button icon={<SyncOutlined aria-hidden />} onClick={() => setOpen(true)}>
        {t('pages.xray.natSync.wizard.title')}
      </Button>
      {open && <NATSyncWizard outboundTags={outboundTags} onClose={() => setOpen(false)} />}
    </>
  );
}
