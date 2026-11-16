import { useId, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Collapse,
  Descriptions,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Spin,
  Switch,
  Tag,
  Typography,
} from 'antd';
import { ReloadOutlined, SyncOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import { keys } from '@/api/queryKeys';
import { NATSyncStatusSchema } from '@/schemas/xray';
import { HttpUtil } from '@/utils';
import { onNumber } from '@/utils/onNumber';
import './OutboundsTab.css';

const endpoint = '/panel/api/xray/nat-sync';
const queryKey = keys.xray.natSyncStatus();
const configSchema = z.object({
  enabled: z.boolean(),
  relayHost: z.string(),
  relayPort: z.number().int(),
  relayUser: z.string(),
  hostKeyFingerprint: z.string(),
  outboundTags: z
    .array(z.string())
    .nullish()
    .transform((tags) => tags ?? []),
});
const viewSchema = z.object({
  config: configSchema,
  publicKey: z.string().optional(),
  status: NATSyncStatusSchema.nullish(),
  legacyStatus: z.boolean().optional(),
});
const reportSchema = z.object({
  candidateIp: z.ipv4(),
  seenAt: z.number().int().nonnegative().optional(),
});

type SyncConfig = z.infer<typeof configSchema>;
type SyncReport = z.infer<typeof reportSchema>;
type Action = 'key' | 'test' | 'config';

const readAuthorization =
  'restrict,command="/usr/bin/python3 /usr/local/libexec/nat-outbound-sync.py read /var/lib/nat-outbound-sync/taiwan.json --max-age 60 --json"';
const reportAuthorization =
  'restrict,command="/usr/bin/python3 /usr/local/libexec/nat-outbound-sync.py report /var/lib/nat-outbound-sync/taiwan.json"';
const dnsName =
  /^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*$/;
const relayUser = /^[A-Za-z_][A-Za-z0-9_.-]{0,63}$/;
const fingerprint = /^SHA256:[A-Za-z0-9+/]{43}$/;

function shellQuote(value: string): string {
  return "'" + value.replaceAll("'", "'\\''") + "'";
}

function CopyCommand({ text }: { text: string }) {
  return (
    <Typography.Paragraph
      code
      copyable={{ text }}
      style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', marginBottom: 0 }}
    >
      {text}
    </Typography.Paragraph>
  );
}

export default function NATSyncStatusButton({ outboundTags }: { outboundTags: string[] }) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<SyncConfig>();
  const [active, setActive] = useState<Action>();
  const [notice, setNotice] = useState<{ type: 'success' | 'error'; text: string }>();
  const [report, setReport] = useState<SyncReport>();
  const [testedConfig, setTestedConfig] = useState('');
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
    enabled: open,
    refetchOnWindowFocus: false,
  });

  const saved = query.data?.config;
  const displayed = draft ?? saved;
  const config = displayed && {
    ...displayed,
    relayHost: displayed.relayHost.trim(),
    relayUser: displayed.relayUser.trim(),
    hostKeyFingerprint: displayed.hostKeyFingerprint.trim(),
  };
  const status = query.data?.status;
  const publicKey = query.data?.publicKey;
  const relayUsesHostName = !!(
    config &&
    config.relayHost.length <= 253 &&
    dnsName.test(config.relayHost)
  );
  const validSSHPortAndUser = !!(
    config &&
    config.relayPort > 0 &&
    config.relayPort <= 65535 &&
    relayUser.test(config.relayUser)
  );
  const complete = !!(
    config &&
    (relayUsesHostName ||
      z.ipv6().safeParse(config.relayHost.replace(/^\[(.*)\]$/, '$1')).success) &&
    validSSHPortAndUser &&
    fingerprint.test(config.hostKeyFingerprint) &&
    config.outboundTags.length
  );
  const connection = JSON.stringify({ ...config, enabled: false });
  const savedConnection = JSON.stringify({ ...saved, enabled: false });
  const changed = !!config && JSON.stringify(config) !== JSON.stringify(saved);
  const requiresTest =
    config?.enabled &&
    (!saved?.enabled || connection !== savedConnection) &&
    connection !== testedConfig;
  const time = (seconds?: number) =>
    seconds ? new Date(seconds * 1000).toLocaleString(i18n.language) : '—';
  const busy = !!active || query.isFetching || query.isError;

  function edit(fields: Partial<SyncConfig>) {
    if (!displayed) return;
    setDraft({ ...displayed, ...fields });
    setNotice(undefined);
  }

  function close() {
    if (active) return;
    setOpen(false);
    setDraft(undefined);
    setNotice(undefined);
    setReport(undefined);
    setTestedConfig('');
  }

  async function perform(action: Action) {
    setActive(action);
    setNotice(undefined);
    if (action === 'test') {
      setReport(undefined);
      setTestedConfig('');
    }
    try {
      const response = await HttpUtil.post(
        endpoint + '/' + action,
        action === 'key' ? undefined : config,
        { silent: true, headers: { 'Content-Type': 'application/json' } },
      );
      if (!response.success) {
        setNotice({ type: 'error', text: response.msg || label('requestFailed') });
        return;
      }
      if (action === 'test') {
        setReport(reportSchema.parse(response.obj));
        setTestedConfig(connection);
        return;
      }

      const nextView = viewSchema.parse(response.obj);
      client.setQueryData(queryKey, nextView);
      if (action === 'config') {
        setDraft(undefined);
        setReport(undefined);
        setTestedConfig('');
        setNotice({ type: 'success', text: label('saved') });
      }
    } catch {
      setNotice({ type: 'error', text: label('requestFailed') });
    } finally {
      setActive(undefined);
    }
  }

  const options = Array.from(new Set([...outboundTags, ...(config?.outboundTags ?? [])])).map(
    (tag) => ({ value: tag, label: tag, disabled: !outboundTags.includes(tag) }),
  );
  const reportCommand =
    config && relayUsesHostName && validSSHPortAndUser
      ? 'ssh -4 -n -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 -o ConnectTimeout=4 ' +
        '-i "$HOME/.ssh/xx-ui-nat-report" -p ' +
        config.relayPort +
        ' -l ' +
        shellQuote(config.relayUser) +
        ' -- ' +
        shellQuote(config.relayHost)
      : undefined;
  const reportNotice = report && connection === testedConfig;

  return (
    <>
      <Button icon={<SyncOutlined aria-hidden />} onClick={() => setOpen(true)}>
        {label('title')}
      </Button>
      {open && (
        <Modal
          title={label('title')}
          open
          width={680}
          style={{ top: 32 }}
          onCancel={close}
          footer={
            <Space wrap>
              <Button
                icon={<ReloadOutlined aria-hidden />}
                loading={query.isFetching}
                disabled={!!active}
                onClick={() => void query.refetch()}
              >
                {label('refresh')}
              </Button>
              <Button
                loading={active === 'test'}
                disabled={busy || !complete || !publicKey}
                onClick={() => void perform('test')}
              >
                {label('testConnection')}
              </Button>
              <Button
                type="primary"
                loading={active === 'config'}
                disabled={busy || !changed || requiresTest || (config?.enabled && !complete)}
                onClick={() => void perform('config')}
              >
                {t('save')}
              </Button>
            </Space>
          }
        >
          {query.isPending ? (
            <Spin />
          ) : query.isError || !displayed ? (
            <Alert type="error" showIcon title={label('loadFailed')} />
          ) : (
            <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
              <Form className="nat-sync-form" layout="vertical" disabled={busy}>
                <Form.Item label={label('relayHost')} htmlFor={uid + 'host'} required>
                  <Input
                    id={uid + 'host'}
                    value={displayed.relayHost}
                    onChange={(event) => edit({ relayHost: event.target.value })}
                  />
                </Form.Item>
                <Space wrap size="middle">
                  <Form.Item label={label('relayPort')} htmlFor={uid + 'port'} required>
                    <InputNumber
                      id={uid + 'port'}
                      min={1}
                      max={65535}
                      precision={0}
                      value={displayed.relayPort || null}
                      onChange={onNumber((relayPort) => edit({ relayPort }))}
                    />
                  </Form.Item>
                  <Form.Item label={label('relayUser')} htmlFor={uid + 'user'} required>
                    <Input
                      id={uid + 'user'}
                      value={displayed.relayUser}
                      onChange={(event) => edit({ relayUser: event.target.value })}
                    />
                  </Form.Item>
                </Space>
                <Form.Item label={label('hostKeyFingerprint')} htmlFor={uid + 'pin'} required>
                  <Input
                    id={uid + 'pin'}
                    placeholder="SHA256:…"
                    value={displayed.hostKeyFingerprint}
                    onChange={(event) => edit({ hostKeyFingerprint: event.target.value })}
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
                    value={displayed.outboundTags}
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
                    checked={displayed.enabled}
                    onChange={(enabled) => edit({ enabled })}
                  />
                </Form.Item>
              </Form>
              <Collapse
                defaultActiveKey={publicKey ? [] : ['setup']}
                items={[
                  {
                    key: 'setup',
                    label: label('setup'),
                    children: (
                      <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
                        <Typography.Text>{label('relaySetup')}</Typography.Text>
                        <CopyCommand text="ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256" />
                        {publicKey ? (
                          <>
                            <Typography.Text strong>{label('readAuthorization')}</Typography.Text>
                            <CopyCommand text={readAuthorization + ' ' + publicKey.trim()} />
                          </>
                        ) : (
                          <Button
                            loading={active === 'key'}
                            disabled={busy}
                            onClick={() => void perform('key')}
                          >
                            {label('generateKey')}
                          </Button>
                        )}
                        <Typography.Text strong>{label('taiwanKey')}</Typography.Text>
                        <CopyCommand
                          text={
                            'install -d -m 700 "$HOME/.ssh"\n' +
                            'ssh-keygen -t ed25519 -N \'\' -f "$HOME/.ssh/xx-ui-nat-report"\n' +
                            'cat "$HOME/.ssh/xx-ui-nat-report.pub"'
                          }
                        />
                        <Typography.Text>{label('reportAuthorization')}</Typography.Text>
                        <CopyCommand text={reportAuthorization + ' ' + label('taiwanPublicKey')} />
                        {reportCommand && (
                          <>
                            <Typography.Text>{label('reportCommand')}</Typography.Text>
                            <CopyCommand text={reportCommand} />
                          </>
                        )}
                        {config?.relayHost && !relayUsesHostName && (
                          <Typography.Text>{label('v4ReportHost')}</Typography.Text>
                        )}
                        <Typography.Link
                          href="https://github.com/a923946240/xx-ui/blob/main/scripts/nat-outbound-sync.md"
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          {label('setupGuide')}
                        </Typography.Link>
                      </Space>
                    ),
                  },
                ]}
              />
              {requiresTest && <Alert type="info" showIcon title={label('testFirst')} />}
              {notice && <Alert type={notice.type} showIcon title={notice.text} />}
              {reportNotice && (
                <Alert
                  type="success"
                  showIcon
                  title={label('connectionPassed') + ' · ' + report.candidateIp}
                  description={
                    report.seenAt ? label('reportedAt') + ' ' + time(report.seenAt) : undefined
                  }
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
                        <Space size="small" wrap>
                          <Tag color={status.state === 'in_sync' ? 'success' : 'warning'}>
                            {label(status.state)}
                          </Tag>
                          {query.data?.legacyStatus && <Tag>{label('legacyStatus')}</Tag>}
                        </Space>
                      ),
                    },
                    {
                      key: 'currentIp',
                      label: label('currentIp'),
                      children: status.currentIp ?? '—',
                    },
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
                    {
                      key: 'appliedAt',
                      label: label('appliedAt'),
                      children: time(status.appliedAt),
                    },
                    {
                      key: 'updatedAt',
                      label: label('updatedAt'),
                      children: time(status.updatedAt),
                    },
                  ]}
                />
              ) : (
                <Alert type="info" showIcon title={label('noStatus')} />
              )}
            </Space>
          )}
        </Modal>
      )}
    </>
  );
}
