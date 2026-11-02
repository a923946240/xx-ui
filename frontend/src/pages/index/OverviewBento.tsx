import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Alert, Card, Spin, Tag, theme } from 'antd';
import {
  ApiOutlined,
  ArrowDownOutlined,
  ArrowUpOutlined,
  DashboardOutlined,
} from '@ant-design/icons';

import { externalOutboundsKey, listExternalOutbounds } from '@/api/external-outbounds';
import { Sparkline } from '@/components/viz';
import { CPUFormatter, SizeFormatter } from '@/utils';
import type { Status } from '@/models/status';
import type { OverviewHistory } from './useOverviewHistory';
import ThroughputCard from './ThroughputCard';
import ConnectionsCard from './ConnectionsCard';
import SystemStrip from './SystemStrip';
import './OverviewBento.css';

interface Props {
  status: Status;
  history: OverviewHistory;
  isMobile: boolean;
  showIp: boolean;
  onToggleIp: () => void;
}

export default function OverviewBento({ status, history, isMobile, showIp, onToggleIp }: Props) {
  const { t } = useTranslation();
  const { token } = theme.useToken();
  const exits = useQuery({
    queryKey: externalOutboundsKey,
    queryFn: listExternalOutbounds,
    refetchInterval: 5000,
  });
  const total = status.netTraffic.sent + status.netTraffic.recv;
  const sentShare = total > 0 ? Math.round((status.netTraffic.sent / total) * 100) : 0;
  const resources = [
    { key: 'memory', metric: 'mem', value: status.mem, color: token.colorPrimary },
    { key: 'storage', metric: 'disk', value: status.disk, color: token.colorWarning },
    { key: 'swap', metric: 'swap', value: status.swap, color: '#a58add' },
  ] as const;

  return (
    <div className="overview-bento">
      <div className="overview-main">
        <Card className="overview-traffic">
          <div className="overview-card-heading">
            <h2>{t('appearance.totalTraffic')}</h2>
            <span className="overview-period">
              {t('pages.index.sent')} / {t('pages.index.received')}
            </span>
          </div>
          <div className="overview-total">{SizeFormatter.sizeFormat(total)}</div>
          <p className="panel-caption">{t('appearance.collectedTraffic')}</p>
          <div className="overview-traffic-inset">
            <h3>{t('appearance.trafficSplit')}</h3>
            <div className="overview-traffic-values">
              <div>
                <span>
                  <ArrowUpOutlined aria-hidden /> {t('pages.index.upload')}
                </span>
                <strong>{SizeFormatter.sizeFormat(status.netTraffic.sent)}</strong>
              </div>
              <div>
                <span>
                  <ArrowDownOutlined aria-hidden /> {t('pages.index.download')}
                </span>
                <strong>{SizeFormatter.sizeFormat(status.netTraffic.recv)}</strong>
              </div>
            </div>
            <div
              className="overview-traffic-bar"
              role="img"
              aria-label={`${t('pages.index.upload')} ${sentShare}% / ${t('pages.index.download')} ${total ? 100 - sentShare : 0}%`}
            >
              <i style={{ width: `${sentShare}%`, background: token.colorPrimary }} />
              <i
                style={{ width: `${total ? 100 - sentShare : 0}%`, background: token.colorSuccess }}
              />
            </div>
          </div>
        </Card>
        <ThroughputCard
          status={status}
          up={history.series.netUp}
          down={history.series.netDown}
          labels={history.labels}
          isMobile={isMobile}
        />
        <Card className="overview-resources">
          <div className="overview-card-heading">
            <h2>{t('appearance.resources')}</h2>
            <span className="panel-eyebrow">
              {status.resourceScope === 'container' ? 'CONTAINER' : 'HOST'}
            </span>
          </div>
          <div className="overview-resource-grid">
            <div className="overview-cpu">
              <span className="panel-caption">
                <DashboardOutlined aria-hidden /> {t('pages.index.cpu')}
              </span>
              <strong>
                {status.metricAvailable('cpu') ? `${status.cpu.percent.toFixed(0)}%` : '—'}
              </strong>
              <span className="panel-caption">
                {status.resourceScope === 'container'
                  ? `${status.logicalPro} vCPU`
                  : `${CPUFormatter.cpuCoreFormat(status.cpuCores)} / ${status.logicalPro}T · ${CPUFormatter.cpuSpeedFormat(status.cpuSpeedMhz)}`}
              </span>
              <Sparkline
                data={history.series.cpu}
                height={76}
                stroke={token.colorPrimary}
                strokeWidth={2}
                fillOpacity={0.18}
                showGrid={false}
                showAxes={false}
                showMarker={false}
                valueMax={100}
              />
            </div>
            <div className="overview-resource-inset">
              {resources.map(({ key, metric, value, color }) => (
                <div key={key} className="overview-resource-row">
                  <div>
                    <span>{t(`pages.index.${key}`)}</span>
                    <span>
                      {status.metricAvailable(metric)
                        ? `${SizeFormatter.sizeFormat(value.current)} / ${SizeFormatter.sizeFormat(value.total)}`
                        : '—'}
                    </span>
                    <strong>
                      {status.metricAvailable(metric) ? `${value.percent.toFixed(0)}%` : '—'}
                    </strong>
                  </div>
                  <div className="overview-resource-track">
                    <i
                      style={{
                        width: `${Math.min(100, Math.max(0, value.percent))}%`,
                        background: value.percent >= 90 ? token.colorError : color,
                      }}
                    />
                  </div>
                </div>
              ))}
            </div>
          </div>
        </Card>
        <div className="overview-runtime">
          <SystemStrip status={status} showIp={showIp} onToggleIp={onToggleIp} />
        </div>
      </div>
      <div className="overview-side">
        <ConnectionsCard
          status={status}
          tcp={history.series.tcpCount}
          udp={history.series.udpCount}
          labels={history.labels}
          isMobile={isMobile}
          compact
        />
        <Card
          className="overview-exits"
          title={t('pages.xray.external.title')}
          extra={
            <Link to="/outbound" aria-label={t('menu.outbounds')}>
              <ArrowUpOutlined aria-hidden style={{ transform: 'rotate(45deg)' }} />
            </Link>
          }
        >
          {exits.isLoading ? (
            <Spin />
          ) : exits.error ? (
            <Alert type="error" title={exits.error.message} />
          ) : !exits.data?.length ? (
            <div className="overview-empty">
              <ApiOutlined aria-hidden />
              <p>{t('pages.xray.external.empty')}</p>
              <Link to="/outbound">{t('pages.xray.external.add')} →</Link>
            </div>
          ) : (
            <>
              {exits.data.slice(0, 5).map((exit) => (
                <Link className="overview-exit" key={exit.id} to="/outbound">
                  <span className="overview-exit-icon">
                    <ApiOutlined aria-hidden />
                  </span>
                  <div>
                    <strong>
                      {exit.remark ||
                        (exit.protocol === 'naive'
                          ? 'NaiveProxy'
                          : exit.protocol === 'trusttunnel'
                            ? 'TrustTunnel'
                            : 'mieru')}
                    </strong>
                    <span>{exit.host}</span>
                    <Tag
                      color={
                        exit.state === 'running' ? 'success' : exit.enable ? 'warning' : 'default'
                      }
                    >
                      {t(`pages.xray.external.states.${exit.state}`, { defaultValue: exit.state })}
                    </Tag>
                  </div>
                </Link>
              ))}
              {exits.data.length > 5 && (
                <Link to="/outbound">
                  {t('menu.outbounds')} ({exits.data.length}) →
                </Link>
              )}
            </>
          )}
          <p className="overview-exits-foot">
            <span className="panel-status-dot" />
            TCP · {t('appearance.clientStatus')}
          </p>
        </Card>
      </div>
    </div>
  );
}
