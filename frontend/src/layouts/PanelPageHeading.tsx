import { useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { PANEL_NAV_GROUPS, PANEL_TITLE_KEYS } from '@/routes/panelRoutes';

export default function PanelPageHeading() {
  const { pathname } = useLocation();
  const { t } = useTranslation();
  const titleKey = PANEL_TITLE_KEYS[pathname];
  if (!titleKey) return null;
  const group = PANEL_NAV_GROUPS.find((entry) => entry.paths.includes(pathname));
  return (
    <header className="panel-page-heading">
      <div>
        {group && <div className="panel-eyebrow">{t(group.titleKey)}</div>}
        <h1>{t(titleKey)}</h1>
      </div>
    </header>
  );
}
