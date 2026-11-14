import { useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { zodResolver } from '@hookform/resolvers/zod';
import { FormProvider, useForm, useWatch } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Switch,
  Tag,
  Typography,
  Spin,
} from 'antd';

import { ApiOutlined, EditOutlined, FileTextOutlined, PlusOutlined } from '@ant-design/icons';
import './ExternalOutbounds.css';
import ExternalClients from './ExternalClients';

import { FormField } from '@/components/form/rhf';
import { keys } from '@/api/queryKeys';
import {
  externalOutboundsKey,
  listExternalOutbounds,
  saveExternalOutbound,
  deleteExternalOutbound,
  probeExternalOutbound,
} from '@/api/external-outbounds';
import { ExternalOutboundInputSchema } from '@/schemas/external-outbound';
import type { ExternalOutboundInput, ExternalOutboundView } from '@/schemas/external-outbound';

const defaults: ExternalOutboundInput = {
  id: 0,
  tag: '',
  remark: '',
  protocol: 'naive',
  host: '',
  port: 443,
  username: '',
  password: '',
  serverName: '',
  transport: 'tcp',
  enable: true,
  mieruMultiplexing: '',
  mieruHandshakeMode: '',
  mieruMtu: 0,
};

const mieruMultiplexingLevels = [
  'MULTIPLEXING_OFF',
  'MULTIPLEXING_LOW',
  'MULTIPLEXING_MIDDLE',
  'MULTIPLEXING_HIGH',
] as const;

export default function ExternalOutbounds({
  disabled = false,
  children,
}: {
  disabled?: boolean;
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: externalOutboundsKey,
    queryFn: listExternalOutbounds,
    refetchInterval: 5000,
  });
  const [editing, setEditing] = useState(false);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [logsTag, setLogsTag] = useState<string | null>(null);
  const form = useForm<ExternalOutboundInput>({
    defaultValues: defaults,
    resolver: zodResolver(ExternalOutboundInputSchema),
  });
  const protocol = useWatch({ control: form.control, name: 'protocol' });
  const id = useWatch({ control: form.control, name: 'id' });
  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: externalOutboundsKey }),
      queryClient.invalidateQueries({ queryKey: keys.xray.config() }),
    ]);
  };
  const save = useMutation({
    mutationFn: saveExternalOutbound,
    onSuccess: async () => {
      setEditing(false);
      setError('');
      await invalidate();
    },
    onError: (e: Error) => setError(e.message),
  });
  const remove = useMutation({
    mutationFn: deleteExternalOutbound,
    onSuccess: invalidate,
    onError: (e: Error) => setError(e.message),
  });
  const probe = useMutation({
    mutationFn: probeExternalOutbound,
    onSuccess: (result) =>
      setNotice(
        `${result.success ? t('pages.xray.external.reachable') : t('pages.xray.external.failed')} · HTTP ${result.httpStatus} · ${result.delay} ms`,
      ),
    onError: (e: Error) => setError(e.message),
  });

  function edit(row?: ExternalOutboundView) {
    setError('');
    if (row) setSelectedId(row.id);
    form.reset(row ? ExternalOutboundInputSchema.parse({ ...row, password: '' }) : { ...defaults });
    setEditing(true);
  }

  const rows = query.data ?? [];
  const selected = rows.find((row) => row.id === selectedId) ?? rows[0];
  const busy = disabled || save.isPending || editing;
  const logs = rows.find((row) => row.tag === logsTag)?.logs || t('pages.xray.external.noLogs');
  const protocolLabel = (value: string) =>
    value === 'naive' ? 'NaiveProxy' : value === 'trusttunnel' ? 'TrustTunnel' : 'mieru';
  const editor = (
    <FormProvider {...form}>
      <Form
        layout="vertical"
        component="form"
        onFinish={form.handleSubmit((value) => {
          if (!disabled && !save.isPending) save.mutate(value);
        })}
      >
        {error ? <Alert type="error" title={error} style={{ marginBottom: 16 }} /> : null}
        <FormField
          name="protocol"
          label={t('pages.xray.external.protocol')}
          onAfterChange={() => {
            form.setValue('transport', 'tcp');
            form.setValue('mieruMultiplexing', '');
            form.setValue('mieruHandshakeMode', '');
            form.setValue('mieruMtu', 0);
          }}
        >
          <Select
            options={['naive', 'mieru', 'trusttunnel'].map((value) => ({
              value,
              label:
                value === 'naive'
                  ? 'NaiveProxy'
                  : value === 'trusttunnel'
                    ? 'TrustTunnel'
                    : 'mieru',
            }))}
          />
        </FormField>
        <FormField name="tag" label={t('pages.xray.external.tag')}>
          <Input disabled={id !== 0} autoComplete="off" />
        </FormField>
        <FormField name="remark" label={t('pages.xray.external.remark')}>
          <Input autoComplete="off" />
        </FormField>
        <FormField name="host" label={t('pages.xray.external.host')}>
          <Input autoComplete="off" />
        </FormField>
        <FormField name="port" label={t('pages.xray.external.port')}>
          <InputNumber min={1} max={65535} style={{ width: '100%' }} />
        </FormField>
        <FormField name="username" label={t('pages.xray.external.username')}>
          <Input autoComplete="off" />
        </FormField>
        <FormField
          name="password"
          label={t('pages.xray.external.password')}
          extra={id !== 0 ? t('pages.xray.external.retainPassword') : undefined}
        >
          <Input.Password autoComplete="new-password" />
        </FormField>
        {protocol !== 'mieru' ? (
          <>
            <FormField name="serverName" label={t('pages.xray.external.serverName')}>
              <Input autoComplete="off" />
            </FormField>
            <FormField name="transport" label={t('pages.xray.external.transport')}>
              <Select
                options={[
                  { value: 'tcp', label: 'TCP / HTTPS' },
                  { value: 'quic', label: 'QUIC / HTTP3' },
                ]}
              />
            </FormField>
          </>
        ) : (
          <>
            <FormField name="mieruMultiplexing" label={t('pages.xray.external.mieruMultiplexing')}>
              <Select
                options={[
                  { value: '', label: t('pages.xray.external.clientDefault') },
                  ...mieruMultiplexingLevels.map((value) => ({ value, label: value })),
                ]}
              />
            </FormField>
            <FormField
              name="mieruHandshakeMode"
              label={t('pages.xray.external.mieruHandshakeMode')}
            >
              <Select
                options={[
                  { value: '', label: t('pages.xray.external.clientDefault') },
                  { value: 'HANDSHAKE_STANDARD', label: 'HANDSHAKE_STANDARD' },
                  { value: 'HANDSHAKE_NO_WAIT', label: 'HANDSHAKE_NO_WAIT' },
                ]}
              />
            </FormField>
            <FormField
              name="mieruMtu"
              label={t('pages.xray.external.mieruMtu')}
              extra={t('pages.xray.external.mieruMtuHint')}
            >
              <InputNumber min={0} max={1400} style={{ width: '100%' }} />
            </FormField>
          </>
        )}
        <FormField name="enable" label={t('pages.xray.external.enabled')} valueProp="checked">
          <Switch />
        </FormField>
        <div className="external-form-actions">
          <Button htmlType="submit" type="primary" loading={save.isPending} disabled={disabled}>
            {t('pages.xray.external.save')}
          </Button>
          <Button
            disabled={save.isPending}
            onClick={() => {
              setEditing(false);
              setError('');
              form.reset(defaults);
            }}
          >
            {t('cancel')}
          </Button>
        </div>
      </Form>
    </FormProvider>
  );
  return (
    <>
      <ExternalClients disabled={disabled} />
      <div className="external-workspace">
        <div className="external-main">
          <div className="external-toolbar">
            <p className="external-help">{t('pages.xray.external.helpText')}</p>
            <Button
              type="primary"
              icon={<PlusOutlined aria-hidden />}
              onClick={() => edit()}
              disabled={busy}
            >
              {t('pages.xray.external.add')}
            </Button>
          </div>
          <div className="external-summary">
            <Card>
              <span className="panel-eyebrow">{t('pages.xray.external.title')}</span>
              <strong>
                {query.isLoading || query.error ? '—' : String(rows.length).padStart(2, '0')}
              </strong>
              <div className="external-protocol-bars" aria-hidden="true">
                <i />
                <i />
                <i />
              </div>
            </Card>
            <Card>
              <span className="panel-eyebrow">
                <span className="panel-status-dot" />
                {t('pages.xray.external.enabled')}
              </span>
              <strong>
                {query.isLoading || query.error
                  ? '—'
                  : String(rows.filter((row) => row.enable).length).padStart(2, '0')}
              </strong>
              <span className="panel-caption">{t('appearance.clientStatus')}</span>
            </Card>
            <Card>
              <span className="panel-eyebrow">{t('appearance.forwarding')}</span>
              <strong>TCP</strong>
              <span className="panel-caption">{t('pages.xray.external.title')}</span>
            </Card>
          </div>
          <section aria-label={t('pages.xray.external.title')}>
            <Card className="external-list" title={t('pages.xray.external.title')}>
              {disabled ? <Alert type="warning" title={t('pages.xray.external.pending')} /> : null}
              {query.error ? <Alert type="error" title={query.error.message} /> : null}
              {error && !editing ? (
                <Alert type="error" title={error} closable onClose={() => setError('')} />
              ) : null}
              {notice ? (
                <Alert type="info" title={notice} closable onClose={() => setNotice('')} />
              ) : null}
              {query.isLoading ? <Spin /> : null}
              {!query.isLoading && !query.error && rows.length === 0 ? (
                <Typography.Text type="secondary">{t('pages.xray.external.empty')}</Typography.Text>
              ) : null}
              {rows.map((row) => (
                <div
                  key={row.id}
                  className={`external-exit-row${selected?.id === row.id ? ' is-selected' : ''}`}
                >
                  <button
                    type="button"
                    className={`external-exit-select protocol-${row.protocol}`}
                    disabled={editing}
                    onClick={() => setSelectedId(row.id)}
                    aria-pressed={selected?.id === row.id}
                    aria-label={`${t('appearance.selectExit')} ${row.tag}`}
                  >
                    <ApiOutlined aria-hidden />
                  </button>
                  <div className="external-exit-name">
                    <div>
                      <strong>{row.remark || row.tag}</strong>
                      <Tag>{row.tag}</Tag>
                    </div>
                    <span>
                      {protocolLabel(row.protocol)} · {row.host}:{row.port}
                    </span>
                  </div>
                  <Tag
                    className="external-state"
                    color={row.state === 'running' ? 'success' : row.enable ? 'warning' : 'default'}
                  >
                    {t(`pages.xray.external.states.${row.state}`, { defaultValue: row.state })}
                  </Tag>
                  <Switch
                    aria-label={`${t('pages.xray.external.enabled')} ${row.tag}`}
                    checked={row.enable}
                    disabled={busy}
                    onChange={(enable) =>
                      save.mutate(
                        ExternalOutboundInputSchema.parse({ ...row, password: '', enable }),
                      )
                    }
                  />
                  <div className="external-row-actions">
                    <Button
                      type="text"
                      icon={<EditOutlined aria-hidden />}
                      aria-label={`${t('pages.xray.external.edit')} ${row.tag}`}
                      disabled={busy}
                      onClick={() => edit(row)}
                    >
                      {t('pages.xray.external.edit')}
                    </Button>
                    <Button
                      type="text"
                      disabled={probe.isPending || !row.enable}
                      loading={probe.isPending && probe.variables === row.id}
                      onClick={() => {
                        setError('');
                        setNotice('');
                        probe.mutate(row.id);
                      }}
                    >
                      {t('pages.xray.external.probe')}
                    </Button>
                    <Button
                      type="text"
                      icon={<FileTextOutlined aria-hidden />}
                      onClick={() => setLogsTag(row.tag)}
                    >
                      {t('pages.xray.external.logs')}
                    </Button>
                    <Popconfirm
                      title={t('pages.xray.external.deleteConfirm')}
                      onConfirm={() => remove.mutateAsync(row.id)}
                    >
                      <Button type="text" danger disabled={busy || remove.isPending}>
                        {t('pages.xray.external.delete')}
                      </Button>
                    </Popconfirm>
                  </div>
                </div>
              ))}
            </Card>
          </section>
          {children}
        </div>
        <aside className="external-inspector">
          <section aria-label={t('appearance.exitConfiguration')}>
            <Card title={t('appearance.exitConfiguration')}>
              {editing && id !== 0 ? (
                editor
              ) : selected ? (
                <>
                  <p className="panel-caption">{selected.tag}</p>
                  <dl className="external-details">
                    {[
                      [t('pages.xray.external.protocol'), protocolLabel(selected.protocol)],
                      [t('pages.xray.external.tag'), selected.tag],
                      [t('pages.xray.external.host'), selected.host],
                      [t('pages.xray.external.port'), String(selected.port)],
                      [t('pages.xray.external.username'), selected.username],
                      [t('pages.xray.external.serverName'), selected.serverName || '—'],
                      [t('pages.xray.external.transport'), selected.transport.toUpperCase()],
                      ...(selected.protocol === 'mieru'
                        ? [
                            [
                              t('pages.xray.external.mieruMultiplexing'),
                              selected.mieruMultiplexing || t('pages.xray.external.clientDefault'),
                            ],
                            [
                              t('pages.xray.external.mieruHandshakeMode'),
                              selected.mieruHandshakeMode || t('pages.xray.external.clientDefault'),
                            ],
                            [
                              t('pages.xray.external.mieruMtu'),
                              selected.mieruMtu
                                ? String(selected.mieruMtu)
                                : t('pages.xray.external.clientDefault'),
                            ],
                          ]
                        : []),
                    ].map(([label, value]) => (
                      <div key={label}>
                        <dt>{label}</dt>
                        <dd>{value}</dd>
                      </div>
                    ))}
                  </dl>
                  <Button block type="primary" disabled={busy} onClick={() => edit(selected)}>
                    {t('pages.xray.external.edit')}
                  </Button>
                </>
              ) : (
                <Typography.Text type="secondary">{t('pages.xray.external.empty')}</Typography.Text>
              )}
            </Card>
          </section>
          <Card title={t('appearance.clientStatus')} className="external-client-card">
            {selected ? (
              <>
                <div className="external-client-heading">
                  <span className={`external-exit-select protocol-${selected.protocol}`}>
                    <ApiOutlined aria-hidden />
                  </span>
                  <div>
                    <strong>{protocolLabel(selected.protocol)}</strong>
                    <p className="panel-caption">
                      {t(`pages.xray.external.states.${selected.state}`, {
                        defaultValue: selected.state,
                      })}
                    </p>
                  </div>
                </div>
                <Button
                  type="link"
                  icon={<FileTextOutlined aria-hidden />}
                  onClick={() => setLogsTag(selected.tag)}
                >
                  {t('pages.xray.external.logs')}
                </Button>
              </>
            ) : (
              <Typography.Text type="secondary">{t('pages.xray.external.empty')}</Typography.Text>
            )}
          </Card>
        </aside>
        <Modal
          title={t('pages.xray.external.add')}
          open={editing && id === 0}
          onCancel={() => {
            if (!save.isPending) {
              setEditing(false);
              setError('');
              form.reset(defaults);
            }
          }}
          footer={null}
          destroyOnHidden
          closable={!save.isPending}
          mask={{ closable: !save.isPending }}
          keyboard={!save.isPending}
        >
          {editing && id === 0 ? editor : null}
        </Modal>
        <Modal
          title={t('pages.xray.external.logs')}
          open={logsTag !== null}
          onCancel={() => setLogsTag(null)}
          footer={null}
        >
          <pre
            style={{
              maxHeight: 360,
              overflow: 'auto',
              whiteSpace: 'pre-wrap',
              overflowWrap: 'anywhere',
            }}
          >
            {logs}
          </pre>
        </Modal>
      </div>
    </>
  );
}
