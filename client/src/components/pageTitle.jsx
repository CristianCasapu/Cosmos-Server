// Fork addition: the browser tab says which page is open ("jellyfin ·
// ServApps · Cosmos" instead of "Cosmos" everywhere). Renders nothing.
import { useEffect } from 'react';
import { useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { menuEntries, tabTitleFor } from '../utils/wayfinding';

const PageTitle = ({ navigation }) => {
  const location = useLocation();
  const { t } = useTranslation();
  useEffect(() => {
    document.title = tabTitleFor(location.pathname, menuEntries(navigation), t);
  }, [location.pathname, navigation, t]);
  return null;
};

export default PageTitle;
