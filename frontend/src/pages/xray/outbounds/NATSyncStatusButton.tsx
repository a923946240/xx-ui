import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Descriptions, Modal, Space, Spin, Tag } from 'antd';
import { ReloadOutlined, SyncOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { keys } from '@/api/queryKeys';
import { fetchXrayConfig } from '@/hooks/useXraySetting';
import { NATSyncStatusSchema } from '@/schemas/xray';

export default function NATSyncStatusButton() {
  const [open, setOpen] = useState(false);
  const { t, i18n } = useTranslation();
  const query = useQuery({
    queryKey: keys.xray.natSyncStatus(),
    queryFn: async () => {
      const { natSyncStatus } = await fetchXrayConfig();
      return natSyncStatus ? NATSyncStatusSchema.parse(natSyncStatus) : null;
    },
    enabled: open,
  });
  const status = query.data;
  const label = (key: string) => t(`pages.xray.natSync.${key}`);
  const time = (seconds?: number) =>
    seconds ? new Date(seconds * 1000).toLocaleString(i18n.language) : '—';

  return (
    <>
      <Button icon={<SyncOutlined aria-hidden />} onClick={() => setOpen(true)}>
        {label('title')}
      </Button>
      {open && (
        <Modal
          title={label('title')}
          open
          onCancel={() => setOpen(false)}
          footer={
            <Button
              icon={<ReloadOutlined aria-hidden />}
              loading={query.isFetching}
              onClick={() => void query.refetch()}
            >
              {label('refresh')}
            </Button>
          }
        >
          {query.isPending ? (
            <Spin />
          ) : query.isError ? (
            <Alert type="error" showIcon title={label('loadFailed')} />
          ) : status ? (
            <Descriptions
              column={1}
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
            <Alert type="info" showIcon title={label('notReporting')} />
          )}
        </Modal>
      )}
    </>
  );
}
