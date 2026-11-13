// Fork addition: tells the admin in the form that a host port is already
// taken, and by whom, before Docker refuses it: by another app, by Cosmos
// itself, or by another row of the same form. Built on what the UI already
// loads (the container list, the configuration, the status); the checks are
// in utils/portConflicts.js.
import * as React from 'react';
import { Alert, FormHelperText, Grid } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import {
  containerHolders, cosmosHolders, findPortConflicts, hasBlockingPortConflict,
  composePortRows, composeContainerNames,
} from '../utils/portConflicts';

export { hasBlockingPortConflict };

// The edit form hands a range to Docker as it is, which picks one free port of it.
export const EDIT_FORM = { rangePicksOne: true };

// What is loaded to know who holds which port; `given` (from a parent that
// already loaded it) saves the requests. Each part may be missing: an admin
// without access to the configuration still gets the containers' ports.
export const usePortSources = (given) => {
  const [sources, setSources] = React.useState({ containers: [], config: null, status: null });

  React.useEffect(() => {
    if (given) return undefined;
    let live = true;
    const keep = (part) => (value) => {
      if (live) setSources((old) => ({ ...old, [part]: value }));
    };
    API.docker.list().then((res) => keep('containers')((res && res.data) || [])).catch(() => {});
    API.config.get().then((res) => keep('config')(res && res.data)).catch(() => {});
    API.getStatus().then((res) => keep('status')(res && res.data)).catch(() => {});
    return () => { live = false; };
  }, []);

  return given || sources;
};

// The ports that are taken, for a form that replaces the containers named in `skip`.
export const portHolders = (sources, skip) => [
  ...containerHolders(sources && sources.containers, skip),
  ...cosmosHolders(sources && sources.config, sources && sources.status),
];

export const usePortHolders = (skip, given) => {
  const sources = usePortSources(given);
  const skipped = Array.isArray(skip) ? skip.join('/') : (skip || '');
  return React.useMemo(() => portHolders(sources, skip), [sources, skipped]);
};

const sentence = (problem, t) => {
  const vars = { port: problem.port, protocol: (problem.protocol || '').toUpperCase(), name: problem.holder && problem.holder.name };
  if (problem.invalid) {
    return t('mgmt.servApps.ports.invalid', 'Enter a port number from 1 to 65535, or leave it empty to keep this port closed on the server.');
  }
  if (problem.holder.kind === 'container') {
    return problem.certain
      ? t('mgmt.servApps.ports.usedByApp', 'Port {{port}} ({{protocol}}) is already used by the app "{{name}}". Choose another host port.', vars)
      : t('mgmt.servApps.ports.maybeUsedByApp', 'Port {{port}} ({{protocol}}) is used by the app "{{name}}". This may not work.', vars);
  }
  if (problem.holder.kind === 'cosmos') {
    const https = problem.holder.role === 'https';
    if (problem.certain) {
      return https
        ? t('mgmt.servApps.ports.usedByCosmosHttps', 'Port {{port}} is used by Cosmos itself (its HTTPS port). Choose another host port.', vars)
        : t('mgmt.servApps.ports.usedByCosmosHttp', 'Port {{port}} is used by Cosmos itself (its HTTP port). Choose another host port.', vars);
    }
    return https
      ? t('mgmt.servApps.ports.maybeUsedByCosmosHttps', 'Port {{port}} is the HTTPS port in Cosmos\' settings. If Cosmos uses it on this server, the app will not start.', vars)
      : t('mgmt.servApps.ports.maybeUsedByCosmosHttp', 'Port {{port}} is the HTTP port in Cosmos\' settings. If Cosmos uses it on this server, the app will not start.', vars);
  }
  if (problem.holder.name) {
    return t('mgmt.servApps.ports.usedByService', 'Port {{port}} ({{protocol}}) is also asked for by "{{name}}". Only one of them can have it.', vars);
  }
  return problem.certain
    ? t('mgmt.servApps.ports.twice', 'Port {{port}} ({{protocol}}) is in this list twice. Each host port can be used once.', vars)
    : t('mgmt.servApps.ports.maybeTwice', 'Port {{port}} ({{protocol}}) may be asked for twice in this list.', vars);
};

// Under one row of the port list: what is wrong with its host port, if anything.
export const PortConflictHint = ({ ports, index, holders, options, style }) => {
  const { t } = useTranslation();
  const problems = findPortConflicts(ports, holders, options)[index] || [];
  if (problems.length === 0) return null;

  return <Grid item xs={12} style={style}>
    {problems.map((problem, i) => (
      <FormHelperText key={i} error={!!(problem.invalid || problem.certain)} sx={{ mt: 0 }}>
        {sentence(problem, t)}
      </FormHelperText>
    ))}
  </Grid>;
};

// Renders nothing: tells the form around the port list (the steps of the new
// container form) whether a row cannot work, each time that changes.
export const PortConflictReport = ({ ports, holders, options, onBlocked }) => {
  const blocked = hasBlockingPortConflict(ports, holders, options);
  React.useEffect(() => {
    if (onBlocked) onBlocked(blocked);
  }, [blocked]);
  return null;
};

// For a compose definition about to be created (the review step of the new
// container form, the market installer, an imported compose file): the host
// ports in it that are taken. A warning only: the text can still be edited.
export const ComposePortConflicts = ({ service, sources }) => {
  const { t } = useTranslation();
  const names = composeContainerNames(service);
  const holders = usePortHolders(names, sources);
  const rows = composePortRows(service);
  const found = findPortConflicts(rows, holders);

  const lines = [];
  found.forEach((problems, index) => {
    problems.filter((problem) => !problem.invalid).forEach((problem) => {
      // one line per pair of services, not one for each side
      if (problem.holder.kind === 'form' && problem.holder.row < index) return;
      const text = (names.length > 1 ? rows[index].service + ': ' : '') + sentence(problem, t);
      if (!lines.includes(text)) lines.push(text);
    });
  });
  if (lines.length === 0) return null;

  return <Alert severity="warning">
    {lines.map((line) => <div key={line}>{line}</div>)}
  </Alert>;
};
