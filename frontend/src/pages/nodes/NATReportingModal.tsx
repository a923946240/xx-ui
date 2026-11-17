import { useId, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Form, Input, Modal, Select, Space, Spin, Tabs, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { isNATHTTPSNode, natPoolSchema } from '@/api/nat-pool';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import { NATReportingViewSchema } from '@/generated/zod';
import { HttpUtil } from '@/utils';

const root = '/panel/api/nodes/nat';
const reportingKey = ['nodes', 'natReporting'];
const poolKey = ['nodes', 'natAddressPool'];

export default function NATReportingModal({
  nodes,
  onAddNode,
  onClose,
}: {
  nodes: NodeRecord[];
  onAddNode: () => void;
  onClose: () => void;
}) {
  const { t, i18n } = useTranslation();
  const poolText = (key: string) => t('pages.xray.natSync.pool.' + key);
  const id = useId();
  const client = useQueryClient();
  const [tab, setTab] = useState('reporting');
  const [relay, setRelay] = useState<number>();
  const [name, setName] = useState<string>();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const choices = nodes.filter(isNATHTTPSNode);
  const reporting = useQuery({
    queryKey: reportingKey,
    enabled: tab === 'reporting',
    queryFn: async ({ signal }) => {
      const response = await HttpUtil.get(root + '/reporting', undefined, { silent: true, signal });
      if (!response.success) throw new Error(response.msg);
      return NATReportingViewSchema.parse(response.obj);
    },
    refetchInterval: 10000,
    retry: false,
  });
  const pool = useQuery({
    queryKey: poolKey,
    enabled: tab === 'pool',
    queryFn: async ({ signal }) => {
      const response = await HttpUtil.get(root + '/pool', undefined, { silent: true, signal });
      if (!response.success) throw new Error(response.msg);
      return natPoolSchema.parse(response.obj);
    },
    refetchInterval: 10000,
    retry: false,
  });
  const cfg = reporting.data?.config;
  const relayNodeId = relay ?? (cfg?.relayNodeId || (choices.length === 1 ? choices[0].id : 0));
  const reportName = name ?? cfg?.name ?? '';
  const relayValid = choices.some((node) => node.id === relayNodeId);
  const lastReport = reporting.data?.lastReport;
  const time = (seconds: number) => new Date(seconds * 1000).toLocaleString(i18n.language);

  async function save(enabled: boolean) {
    if (!cfg || pending) return;
    setPending(true);
    setError('');
    try {
      const requested = enabled ? { relayNodeId, name: reportName, enabled } : { ...cfg, enabled };
      const response = await HttpUtil.post(root + '/reporting', requested, {
        silent: true,
        headers: { 'Content-Type': 'application/json' },
      });
      if (!response.success) {
        setError(response.msg || poolText('reportFailed'));
        return;
      }
      const view = NATReportingViewSchema.parse(response.obj);
      await client.cancelQueries({ queryKey: reportingKey });
      client.setQueryData(reportingKey, view);
      setRelay(undefined);
      setName(undefined);
    } catch {
      setError(poolText('reportFailed'));
    } finally {
      setPending(false);
    }
  }

  return (
    <Modal
      open
      title={poolText('title')}
      footer={null}
      onCancel={pending ? undefined : onClose}
      closable={!pending}
    >
      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          {
            key: 'reporting',
            label: poolText('reporting'),
            children: reporting.isPending ? (
              <Spin />
            ) : reporting.isError ? (
              <Alert showIcon type="error" title={poolText('loadFailed')} />
            ) : (
              <Space orientation="vertical" style={{ width: '100%' }}>
                <Typography.Paragraph>{poolText('reportHint')}</Typography.Paragraph>
                {!choices.length && (
                  <Alert
                    showIcon
                    type="info"
                    title={poolText('needNode')}
                    action={<Button onClick={onAddNode}>{t('pages.nodes.addNode')}</Button>}
                  />
                )}
                <Form layout="vertical" disabled={pending}>
                  <Form.Item
                    label={t('pages.xray.natSync.relayNode')}
                    htmlFor={id + 'relay'}
                    required
                  >
                    <Select
                      id={id + 'relay'}
                      value={relayNodeId || undefined}
                      onChange={setRelay}
                      options={choices.map((node) => ({
                        value: node.id,
                        label: node.name || '#' + node.id,
                      }))}
                    />
                  </Form.Item>
                  <Form.Item label={poolText('name')} htmlFor={id + 'name'}>
                    <Input
                      id={id + 'name'}
                      value={reportName}
                      onChange={(event) => setName(event.target.value)}
                    />
                  </Form.Item>
                  <Space wrap>
                    <Button
                      type="primary"
                      loading={pending}
                      disabled={
                        !relayValid || new TextEncoder().encode(reportName.trim()).length > 96
                      }
                      onClick={() => void save(true)}
                    >
                      {poolText('start')}
                    </Button>
                    {cfg?.enabled && (
                      <Button disabled={pending} onClick={() => void save(false)}>
                        {poolText('stop')}
                      </Button>
                    )}
                  </Space>
                </Form>
                {(error || reporting.data?.failed) && (
                  <Alert showIcon type="error" title={error || poolText('reportFailed')} />
                )}
                {lastReport && (
                  <Alert
                    showIcon
                    type="success"
                    title={lastReport.candidateIp}
                    description={t('pages.xray.natSync.reportedAt') + ' ' + time(lastReport.seenAt)}
                  />
                )}
              </Space>
            ),
          },
          {
            key: 'pool',
            label: poolText('title'),
            children: pool.isError ? (
              <Alert showIcon type="error" title={poolText('loadFailed')} />
            ) : pool.isLoading ? (
              <Spin />
            ) : pool.data?.length ? (
              <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
                {pool.data.map((entry) => (
                  <li key={entry.id} style={{ marginBlockEnd: 12 }}>
                    <Space wrap>
                      <Typography.Text strong>{entry.name}</Typography.Text>
                      <Typography.Text copyable>{entry.candidateIp}</Typography.Text>
                      <Typography.Text type="secondary">{time(entry.seenAt)}</Typography.Text>
                    </Space>
                  </li>
                ))}
              </ul>
            ) : (
              <Typography.Text type="secondary">{poolText('empty')}</Typography.Text>
            ),
          },
        ]}
      />
    </Modal>
  );
}
