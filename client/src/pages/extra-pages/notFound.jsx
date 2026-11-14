// Fork addition: an address under /cosmos-ui/ that is no page of the panel.
// It used to give an empty window; now the panel stays and says so.
import * as React from 'react';
import { Button } from '@mui/material';
import { Link, useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { CompassOutlined, HomeOutlined } from '@ant-design/icons';
import EmptyState from '../../components/emptyState';

const NotFound = () => {
  const { t } = useTranslation();
  const location = useLocation();
  // the address is shown as text, cut to a line; it is never used as a link
  const asked = String(location.pathname || '').slice(0, 120);
  return <EmptyState
    icon={<CompassOutlined />}
    title={t('notFound.title', 'This page does not exist')}
    action={<Button component={Link} to="/cosmos-ui/" variant="contained" startIcon={<HomeOutlined />}>{t('notFound.home', 'Go to the home page')}</Button>}
  >
    {t('notFound.text', 'There is nothing at {{address}}. The address may be mistyped, or the page was renamed. The menu lists every page of the panel.', { address: asked })}
  </EmptyState>;
};

export default NotFound;
