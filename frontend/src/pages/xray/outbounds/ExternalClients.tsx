import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Popconfirm, Space, Spin, Tag, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import {
  externalClientsKey,
  listExternalClients,
  manageExternalClient,
} from '@/api/external-outbounds';
import './ExternalClients.css';

const busyStates = ['checking', 'downloading', 'verifying', 'installing'];

export default function ExternalClients({ disabled = false }: { disabled?: boolean }) {
  const { t } = useTranslation();
  const cache = useQueryClient();
  const [error, setError] = useState('');
  const query = useQuery({
    queryKey: externalClientsKey,
    queryFn: listExternalClients,
    refetchInterval: 2000,
  });
  const operation = useMutation({
    mutationFn: manageExternalClient,
    onSuccess: () => cache.invalidateQueries({ queryKey: externalClientsKey }),
    onError: (e: Error) => setError(e.message),
  });
  const rows = query.data ?? [];
  const busy = operation.isPending || rows.some((row) => busyStates.includes(row.state));
  function stateLabel(state: string, installed: boolean) {
    switch (state) {
      case 'checking':
        return t('pages.xray.external.clients.checking');
      case 'downloading':
        return t('pages.xray.external.clients.downloading');
      case 'verifying':
        return t('pages.xray.external.clients.verifying');
      case 'installing':
        return t('pages.xray.external.clients.installing');
      case 'error':
        return t('pages.xray.external.clients.failed');
      default:
        return installed
          ? t('pages.xray.external.clients.installed')
          : t('pages.xray.external.clients.notInstalled');
    }
  }
  function start(protocol: string, install: boolean) {
    setError('');
    operation.mutate({ protocol, install });
  }
  return (
    <section aria-label={t('pages.xray.external.clients.title')} className="external-software">
      <Card title={t('pages.xray.external.clients.title')}>
        <Typography.Paragraph type="secondary">
          {t('pages.xray.external.clients.helpText')}
        </Typography.Paragraph>
        {query.isLoading ? <Spin /> : null}
        {error || query.error ? (
          <Alert type="error" title={error || t('pages.xray.external.clients.loadFailed')} />
        ) : null}
        <div className="external-software-grid">
          {rows.map((row) => (
            <div className="external-software-item" key={row.protocol}>
              <Space wrap>
                <Typography.Text strong>{row.name}</Typography.Text>
                <Tag
                  color={
                    row.state === 'error'
                      ? 'error'
                      : busyStates.includes(row.state)
                        ? 'processing'
                        : 'default'
                  }
                >
                  {stateLabel(row.state, row.installed)}
                </Tag>
              </Space>
              <div className="external-software-version">
                <span>{t('pages.xray.external.clients.current')}</span>
                <Typography.Text code>
                  {row.installed
                    ? row.release || row.version || t('pages.xray.external.clients.unknown')
                    : t('pages.xray.external.clients.notInstalled')}
                </Typography.Text>
              </div>
              <div className="external-software-version">
                <span>{t('pages.xray.external.clients.latest')}</span>
                <Typography.Text code>{row.latestVersion || '—'}</Typography.Text>
              </div>
              {!row.supported ? (
                <Typography.Text type="warning">
                  {t('pages.xray.external.clients.unsupported')}
                </Typography.Text>
              ) : null}
              {row.error ? <Alert type="error" title={row.error} /> : null}
              <Space wrap>
                <Button
                  size="small"
                  aria-label={`${t('pages.xray.external.clients.check')} ${row.name}`}
                  disabled={disabled || busy || !row.supported}
                  onClick={() => start(row.protocol, false)}
                >
                  {t('pages.xray.external.clients.check')}
                </Button>
                <Popconfirm
                  title={t('pages.xray.external.clients.confirmTitle', { name: row.name })}
                  description={t('pages.xray.external.clients.confirmDescription')}
                  okText={t('pages.xray.external.clients.confirm')}
                  cancelText={t('cancel')}
                  onConfirm={() => start(row.protocol, true)}
                >
                  <Button
                    type="primary"
                    size="small"
                    aria-label={`${t(row.installed ? 'pages.xray.external.clients.update' : 'pages.xray.external.clients.install')} ${row.name}`}
                    disabled={
                      disabled ||
                      busy ||
                      !row.supported ||
                      (row.installed && row.latestVersion !== '' && !row.updateAvailable)
                    }
                  >
                    {t(
                      row.installed
                        ? 'pages.xray.external.clients.update'
                        : 'pages.xray.external.clients.install',
                    )}
                  </Button>
                </Popconfirm>
              </Space>
            </div>
          ))}
        </div>
      </Card>
    </section>
  );
}
