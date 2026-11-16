import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ArrowRightOutlined, ImportOutlined, SwapOutlined, ToolOutlined } from '@ant-design/icons';

const entries = [
  {
    path: '/inbounds',
    icon: ImportOutlined,
    title: 'appearance.access',
    detail: 'appearance.accessEntry',
  },
  {
    path: '/outbound',
    icon: SwapOutlined,
    title: 'appearance.network',
    detail: 'appearance.networkEntry',
  },
  {
    path: '/settings',
    icon: ToolOutlined,
    title: 'appearance.system',
    detail: 'appearance.systemEntry',
  },
] as const;

export default function OverviewEntryPoints() {
  const { t } = useTranslation();
  return (
    <nav className="ov-workflows" aria-label={t('appearance.workspace')}>
      {entries.map(({ path, icon: Icon, title, detail }) => (
        <Link to={path} className="ov-workflow" key={path}>
          <span className="ov-workflow-icon">
            <Icon aria-hidden />
          </span>
          <span className="ov-workflow-copy">
            <strong>{t(title)}</strong>
            <span>{t(detail)}</span>
          </span>
          <ArrowRightOutlined aria-hidden />
        </Link>
      ))}
    </nav>
  );
}
