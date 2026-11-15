// Fork addition: the home page's health lines, for administrators (shown in the
// home page's notice area, components/homeNotices.jsx; this was the separate
// health card): failed scheduled jobs, apps stopped by an error or killed for
// lack of memory, disks nearly full, snapshots that went wrong. Built from
// answers the API already gives (utils/health.js says which); read once per
// visit and on "Check again". Three states: every check made and nothing found
// (one calm line), something needs attention, or not everything could be
// checked (said, with what and why, never "all is well" then).
import * as React from 'react';
import { Button, Link } from '@mui/material';
import { ReloadOutlined } from '@ant-design/icons';
import { Link as RouterLink } from 'react-router';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import {
  diskKeys, healthLines, calmLine, healthState, isThrottled, oomCandidates, oomKilled, unreadLine,
} from '../utils/health';

// each source: its data, or { failed: error } when it could not be read
const read = (promise) => promise.then((r) => ({ data: r && r.data !== undefined ? r.data : null })).catch((error) => ({ failed: error || {} }));

const readSources = async (docker) => {
  const [jobs, containers, names, snapshots] = await Promise.all([
    read(API.cron.list()),
    docker ? read(API.docker.list()) : Promise.resolve({ failed: {} }),
    read(API.metrics.list()),
    read(API.config.snapshotsStatus()),
  ]);
  const keys = names.failed ? [] : diskKeys(names.data);
  const disks = names.failed ? names : (keys.length ? await read(API.metrics.get(keys)) : { data: [] });
  // apps that exited with 137: a stop by hand, or the memory killer (one inspect each, at most five)
  const candidates = containers.failed ? [] : oomCandidates(containers.data);
  const inspected = Object.fromEntries(await Promise.all(candidates.map(async (name) => {
    const answer = await read(API.docker.get(name));
    return [name, answer.failed ? undefined : answer.data];
  })));
  return { jobs, containers, disks, snapshots, oom: oomKilled(inspected) };
};

export const useHealthNotices = ({ docker = true, enabled = true }) => {
  const { t } = useTranslation();
  const [sources, setSources] = React.useState(null);
  const [round, setRound] = React.useState(0);
  const [checking, setChecking] = React.useState(false);

  React.useEffect(() => {
    if (!enabled) return undefined;
    let live = true;
    setChecking(true);
    readSources(docker).then((answer) => { if (live) { setSources(answer); setChecking(false); } });
    return () => { live = false; };
  }, [docker, enabled, round]);

  if (!sources) return { items: [], state: null, calm: null }; // nothing is said before it is known

  const value = (source) => (source.failed ? undefined : source.data);
  const all = {
    jobs: value(sources.jobs), containers: value(sources.containers), disks: value(sources.disks),
    snapshots: value(sources.snapshots), oom: sources.oom,
  };
  const lines = healthLines(all);
  const named = [
    ['jobs', t('health.source.jobs', 'scheduled jobs')],
    ['containers', t('health.source.apps', 'apps')],
    ['disks', t('health.source.disks', 'disks')],
    ['snapshots', t('health.source.snapshots', 'snapshots')],
  ];
  const unread = named.filter(([key]) => sources[key].failed).map(([, name]) => name);
  const throttled = named.some(([key]) => sources[key].failed && isThrottled(sources[key].failed));
  const tr = (text) => t(text[0], text[1], { ...(text[2] || {}), interpolation: { escapeValue: false } });
  const state = healthState(lines, unread);

  const items = lines.map((line) => ({
    id: 'health:' + line.id,
    severity: line.severity,
    attrs: { 'data-health': line.id },
    text: tr(line.text),
    actions: <Link component={RouterLink} to={line.link} className="ct-notice-link">{t('health.open', 'Open')}</Link>,
  }));
  if (unread.length) {
    items.push({
      id: 'health:unread',
      severity: 'unknown',
      attrs: { 'data-health': 'unread' },
      text: tr(unreadLine(unread, throttled)),
      actions: <Button size="small" variant="outlined" color="inherit" startIcon={<ReloadOutlined />} disabled={checking}
        onClick={() => setRound((n) => n + 1)}>{t('health.retry', 'Check again')}</Button>,
    });
  }
  return { items, state, calm: state === 'ok' ? tr(calmLine(all)) : null };
};

export default useHealthNotices;
