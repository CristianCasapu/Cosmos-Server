// Fork addition: the home page's health card, for administrators. One line
// per thing that needs attention (failed scheduled jobs, apps stopped by an
// error, disks nearly full, snapshots that went wrong, certificate problems),
// each a link to where it is fixed; one calm line when all is well. Built from
// answers the API already gives (utils/health.js says which); read once per
// visit of the home page.
import * as React from 'react';
import { Box, Link, Stack, Typography } from '@mui/material';
import { CheckCircleOutlined, CloseCircleOutlined, ExclamationCircleOutlined } from '@ant-design/icons';
import { Link as RouterLink } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import { useCertificates } from './certificates';
import { homeAlerts } from '../utils/certificates';
import { diskKeys, healthLines, calmLine } from '../utils/health';

const ICONS = {
  error: <CloseCircleOutlined className="ct-health-icon ct-health-error" aria-hidden="true" />,
  warning: <ExclamationCircleOutlined className="ct-health-icon ct-health-warning" aria-hidden="true" />,
  ok: <CheckCircleOutlined className="ct-health-icon ct-health-ok" aria-hidden="true" />,
};

const readSources = async (docker) => {
  const quiet = (promise) => promise.then((r) => (r && r.data !== undefined ? r.data : null)).catch(() => undefined);
  const [jobs, containers, names, snapshots] = await Promise.all([
    quiet(API.cron.list()),
    docker ? quiet(API.docker.list()) : Promise.resolve(undefined),
    quiet(API.metrics.list()),
    quiet(API.config.snapshotsStatus()),
  ]);
  const keys = diskKeys(names);
  const disks = names === undefined ? undefined : (keys.length ? await quiet(API.metrics.get(keys)) : []);
  return { jobs, containers, disks, snapshots };
};

const HealthCard = ({ docker = true }) => {
  const { t } = useTranslation();
  const [sources, setSources] = React.useState(null);
  const certificates = useCertificates();

  React.useEffect(() => {
    let live = true;
    readSources(docker).then((read) => { if (live) setSources(read); });
    return () => { live = false; };
  }, [docker]);

  if (!sources) return null; // nothing is said before it is known
  const certificateAlerts = homeAlerts(certificates.data, (v) => v);
  const all = { ...sources, certificateAlerts };
  const lines = healthLines(all);
  const unread = [
    sources.jobs === undefined && t('health.source.jobs', 'scheduled jobs'),
    sources.containers === undefined && t('health.source.apps', 'apps'),
    sources.disks === undefined && t('health.source.disks', 'disks'),
    sources.snapshots === undefined && t('health.source.snapshots', 'snapshots'),
  ].filter(Boolean);
  const tr = (text) => t(text[0], text[1], { ...(text[2] || {}), interpolation: { escapeValue: false } });

  return <Box className={'ct-health' + (lines.length ? ' ct-health-attention' : ' ct-health-calm')} component="section"
    aria-label={t('health.title', 'Server health')}>
    <Stack spacing={0.75}>
      {lines.length === 0 && <Typography variant="body2" className="ct-health-line">
        {ICONS.ok}<span>{tr(calmLine(all))}</span>
      </Typography>}
      {lines.map((line) => <Typography key={line.id} variant="body2" className="ct-health-line" data-health={line.id}>
        {ICONS[line.severity]}
        <span className="ct-visually-hidden">{line.severity === 'error' ? t('health.severity.error', 'Problem:') : t('health.severity.warning', 'Warning:')} </span>
        <span>{tr(line.text)} <Link component={RouterLink} to={line.link} className="ct-health-link">{t('health.open', 'Open')}</Link></span>
      </Typography>)}
      {unread.length > 0 && <Typography variant="body2" color="textSecondary" className="ct-health-line ct-health-unread">
        <span>{t('health.unread', 'Could not be checked just now: {{what}}.', { what: unread.join(', ') })}</span>
      </Typography>}
    </Stack>
  </Box>;
};

export default HealthCard;
