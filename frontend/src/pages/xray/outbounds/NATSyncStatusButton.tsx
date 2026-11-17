import { useId, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Descriptions, Form, Modal, Select, Space, Spin, Switch, Tag } from 'antd';
import { ReloadOutlined, SyncOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { z } from 'zod';
import { keys } from '@/api/queryKeys';
import { useNodesQuery, type NodeRecord } from '@/api/queries/useNodesQuery';
import { NATSyncStatusSchema } from '@/schemas/xray';
import { HttpUtil } from '@/utils';
import './OutboundsTab.css';

const endpoint = '/panel/api/xray/nat-sync';
const queryKey = keys.xray.natSyncStatus();
const configSchema = z.object({
  enabled: z.boolean(),
  relayNodeId: z.number().int().nonnegative(),
  reporterNodeId: z.number().int().nonnegative(),
  outboundTags: z
    .array(z.string())
    .nullish()
    .transform((tags) => tags ?? []),
});
const viewSchema = z.object({
  config: configSchema,
  status: NATSyncStatusSchema.nullish(),
});
const reportSchema = z.object({
  candidateIp: z.ipv4(),
  seenAt: z.number().int().positive(),
});

type SyncConfig = z.infer<typeof configSchema>;
type SyncReport = z.infer<typeof reportSchema>;
type Action = 'test' | 'config';

function nodeOptions(
  nodes: NodeRecord[],
  eligible: NodeRecord[],
  selected?: number,
  other?: number,
) {
  const choices = eligible.map((node) => ({
    value: node.id,
    label: node.name || '#' + node.id,
    disabled: node.id === other,
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
  return config && JSON.stringify([config.relayNodeId, config.reporterNodeId, config.outboundTags]);
}

function NATSyncModal({ outboundTags, onClose }: { outboundTags: string[]; onClose: () => void }) {
  const [draft, setDraft] = useState<SyncConfig>();
  const [active, setActive] = useState<Action>();
  const [notice, setNotice] = useState<{ type: 'success' | 'error'; text: string }>();
  const [tested, setTested] = useState<{ settings: string; report: SyncReport }>();
  const { t, i18n } = useTranslation();
  const uid = useId();
  const client = useQueryClient();
  const label = (key: string) => t('pages.xray.natSync.' + key);

  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      const response = await HttpUtil.get(endpoint, undefined, { silent: true, signal });
      if (!response.success) throw new Error(response.msg);
      return viewSchema.parse(response.obj);
    },
    refetchOnWindowFocus: false,
  });
  const {
    nodes,
    loading: nodesLoading,
    fetchError: nodesError,
    refetch: refetchNodes,
  } = useNodesQuery();
  const eligibleNodes = nodes.filter(
    (node) =>
      node.enable &&
      !node.transitive &&
      (!node.scheme || node.scheme === 'https') &&
      node.tlsVerifyMode !== 'skip',
  );

  const saved = query.data?.config;
  const config = draft ?? saved;
  const status = query.data?.status;
  const settings = settingsKey(config);
  const changed = !!config && JSON.stringify(config) !== JSON.stringify(saved);
  const report = tested && tested.settings === settings ? tested.report : undefined;
  const requiresTest =
    config?.enabled && (!saved?.enabled || settings !== settingsKey(saved)) && !report;
  const complete =
    !!config &&
    config.relayNodeId !== config.reporterNodeId &&
    eligibleNodes.some(({ id }) => id === config.relayNodeId) &&
    eligibleNodes.some(({ id }) => id === config.reporterNodeId) &&
    config.outboundTags.length > 0 &&
    config.outboundTags.every((tag) => outboundTags.includes(tag));
  const nodesUnavailable = nodesLoading || !!nodesError;
  const busy = !!active || query.isFetching;
  const time = (seconds?: number) =>
    seconds ? new Date(seconds * 1000).toLocaleString(i18n.language) : '—';

  function edit(fields: Partial<SyncConfig>) {
    if (!config) return;
    setDraft({ ...config, ...fields });
    setNotice(undefined);
  }

  async function perform(action: Action) {
    if (!config || !settings) return;
    setActive(action);
    setNotice(undefined);
    if (action === 'test') setTested(undefined);
    try {
      const response = await HttpUtil.post(endpoint + '/' + action, config, {
        silent: true,
        headers: { 'Content-Type': 'application/json' },
      });
      if (!response.success) {
        setNotice({ type: 'error', text: response.msg || label('requestFailed') });
        return;
      }
      if (action === 'test') {
        setTested({ settings, report: reportSchema.parse(response.obj) });
        return;
      }

      client.setQueryData(queryKey, viewSchema.parse(response.obj));
      setDraft(undefined);
      setTested(undefined);
      setNotice({ type: 'success', text: label('saved') });
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
      title={label('title')}
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
            onClick={() => void Promise.all([query.refetch(), refetchNodes()])}
          >
            {label('refresh')}
          </Button>
          <Button
            loading={active === 'test'}
            disabled={busy || query.isError || nodesUnavailable || !complete}
            onClick={() => void perform('test')}
          >
            {label('testConnection')}
          </Button>
          <Button
            type="primary"
            loading={active === 'config'}
            disabled={
              busy ||
              !changed ||
              (config?.enabled && (query.isError || nodesUnavailable || !complete || requiresTest))
            }
            onClick={() => void perform('config')}
          >
            {t('save')}
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
          ) : !nodesLoading && eligibleNodes.length < 2 ? (
            <Alert
              type="info"
              showIcon
              title={label('needNodes')}
              description={<Link to="/nodes">{t('pages.nodes.addNode')}</Link>}
            />
          ) : null}
          <Form className="nat-sync-form" layout="vertical" disabled={busy}>
            <Form.Item
              label={label('relayNode')}
              htmlFor={uid + 'relay'}
              extra={label('relayNodeHint')}
              required
            >
              <Select
                id={uid + 'relay'}
                loading={nodesLoading}
                disabled={busy || nodesUnavailable}
                allowClear
                options={nodeOptions(
                  nodes,
                  eligibleNodes,
                  config.relayNodeId,
                  config.reporterNodeId,
                )}
                value={config.relayNodeId || undefined}
                onChange={(relayNodeId) => edit({ relayNodeId: relayNodeId ?? 0 })}
              />
            </Form.Item>
            <Form.Item label={label('reporterNode')} htmlFor={uid + 'reporter'} required>
              <Select
                id={uid + 'reporter'}
                loading={nodesLoading}
                disabled={busy || nodesUnavailable}
                allowClear
                options={nodeOptions(
                  nodes,
                  eligibleNodes,
                  config.reporterNodeId,
                  config.relayNodeId,
                )}
                value={config.reporterNodeId || undefined}
                onChange={(reporterNodeId) => edit({ reporterNodeId: reporterNodeId ?? 0 })}
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
            <Form.Item
              label={label('enabled')}
              htmlFor={uid + 'enabled'}
              style={{ marginBottom: 0 }}
            >
              <Switch
                aria-label={label('enabled')}
                id={uid + 'enabled'}
                checked={config.enabled}
                onChange={(enabled) => edit({ enabled })}
              />
            </Form.Item>
          </Form>
          {requiresTest && <Alert type="info" showIcon title={label('testFirst')} />}
          {notice && <Alert type={notice.type} showIcon title={notice.text} />}
          {report && (
            <Alert
              type="success"
              showIcon
              title={label('connectionPassed') + ' · ' + report.candidateIp}
              description={label('reportedAt') + ' ' + time(report.seenAt)}
            />
          )}
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
            <Alert type="info" showIcon title={label('noStatus')} />
          )}
        </Space>
      )}
    </Modal>
  );
}

export default function NATSyncStatusButton({ outboundTags }: { outboundTags: string[] }) {
  const [open, setOpen] = useState(false);
  const { t } = useTranslation();

  return (
    <>
      <Button icon={<SyncOutlined aria-hidden />} onClick={() => setOpen(true)}>
        {t('pages.xray.natSync.title')}
      </Button>
      {open && <NATSyncModal outboundTags={outboundTags} onClose={() => setOpen(false)} />}
    </>
  );
}
