// Fork addition: an app's links, grouped (the local ones under a house, the
// external ones under a globe), and the "Domains" action that attaches or
// detaches a domain later without filling in a route by hand.
import * as React from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  Stack,
  Tooltip,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { GlobalOutlined, HomeOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import HostChip from './hostChip';
import { ExternalPortNote, useCosmosPorts } from './routerPorts'; // the port the addresses are reached on (src/upnp)
import { shownRoute } from '../utils/routerPorts';
import PermissionGuard from './permissionGuard';
import { PERM_RESOURCES } from '../utils/permissions';
import { IsRouteSocketProxy, ValidateRoute, getContainersRoutes } from '../utils/routes';
import { PortForwardHint, configuredDomains, externalAddress, hostLabel } from './hostnameSuggestions';
import { baseIsUnderDomain, baseRoute, domainRoutes, groupAppRoutes, routeHost, serverBack } from '../utils/appLinks';

// The chips of an app's routes, local links first. In a row of chips each
// group starts with its icon; with `rows` each group gets a line of its own,
// headed by the icon and what it stands for.
const AppLinks = ({ routes, chipStyle, rows }) => {
  const { t } = useTranslation();
  // a host is stored without a port; its chip shows and opens it with the port it is reached on
  const ports = useCosmosPorts(null);
  const scheme = window.location.protocol === 'http:' ? 'http' : 'https';

  return groupAppRoutes(routes, window.location.host).map((group) => {
    const Icon = group.local ? HomeOutlined : GlobalOutlined;
    const label = group.local
      ? t('mgmt.urls.links.local', 'Local network')
      : t('mgmt.urls.links.external', 'Internet');
    const chips = group.routes.map((route) => <HostChip key={route.Name} route={shownRoute(route, ports, scheme)} settings style={chipStyle} />);

    if (rows) {
      return <div key={label}>
        <Typography variant="caption" color="textSecondary" component="div" style={chipStyle}>
          <Icon /> {label}
        </Typography>
        {chips}
      </div>;
    }
    return <React.Fragment key={label}>
      <Tooltip title={label}>
        <Icon aria-label={label} style={{ flexShrink: 0 }} />
      </Tooltip>
      {chips}
    </React.Fragment>;
  });
};

// What the dialog shows for one domain. `routes` are the app's external
// routes under it, `own` tells that the base route itself is that address.
// Routes reached through a port of their own have no address to copy.
const domainRows = (config, containerName) => {
  const routes = getContainersRoutes(config, containerName).filter((route) => !IsRouteSocketProxy(route));
  const domains = configuredDomains(config);
  const base = baseRoute(routes, domains);
  if (!base) {
    return { base, rows: [] };
  }
  const labels = [...new Set([hostLabel(base.Name), hostLabel(containerName)])];
  return {
    base,
    rows: domains.map((domain) => {
      const attached = domainRoutes(routes, base, domain, labels);
      const own = baseIsUnderDomain(base, domain, labels);
      return {
        domain,
        own,
        routes: attached,
        attached: own || attached.length > 0,
        host: attached.length > 0 ? attached.map(routeHost).join(', ') : own ? routeHost(base) : externalAddress(base.Name, domain),
      };
    }),
  };
};

// The "Domains" chip and its dialog. Offered only when the app has a route
// to copy and a domain is configured. `refresh` reloads the configuration.
export const AppDomains = ({ config, containerName, refresh, size, style }) => {
  const { t } = useTranslation();
  const [checked, setChecked] = React.useState(null); // { domain: bool } while the dialog is open
  const [saving, setSaving] = React.useState(false);
  const [errors, setErrors] = React.useState([]);

  const { base, rows } = domainRows(config, containerName);
  if (!base || rows.length === 0) {
    return null;
  }

  const open = () => {
    setErrors([]);
    setChecked(Object.fromEntries(rows.map((row) => [row.domain, row.attached])));
  };
  const close = () => { if (!saving) setChecked(null); };

  const toAdd = checked ? rows.filter((row) => checked[row.domain] && !row.attached) : [];
  // only the app's external routes under an unticked domain, never its base route
  const toRemove = checked ? rows.filter((row) => !checked[row.domain] && !row.own).flatMap((row) => row.routes) : [];

  const save = async () => {
    // one more route per attached domain: the base route with another name and host
    const copies = toAdd.map((row) => ({
      ...base,
      Name: base.Name + '-' + row.domain,
      Host: externalAddress(base.Name, row.domain),
      UseHost: true,
    }));
    const all = (config.HTTPConfig.ProxyConfig.Routes || []);
    const problems = copies.flatMap((route) => [
      ...(ValidateRoute(route, config) || []),
      ...(all.some((other) => routeHost(other) === route.Host)
        ? [t('mgmt.urls.domains.hostTaken', '{{host}} is already the address of another URL.', { host: route.Host })]
        : []),
    ]);
    if (problems.length > 0) {
      setErrors([...new Set(problems)]);
      return;
    }

    setErrors([]);
    setSaving(true);
    let done = false;
    try {
      for (const route of copies) {
        await API.config.addRoute(route);
        // the server reloads after each change: wait until it answers again
        await serverBack();
      }
      for (const route of toRemove) {
        await API.config.deleteRoute(route.Name);
        await serverBack();
      }
      done = true;
    } catch (e) {
      setErrors([e.message || String(e)]);
    }
    setSaving(false);
    if (done) {
      setChecked(null);
    }
    refresh && refresh();
  };

  return <span style={{ display: 'inline-flex', ...style }}>
    <PermissionGuard permission={PERM_RESOURCES}>
      <Chip
        label={t('mgmt.urls.domains.button', 'Domains')}
        color="primary"
        variant="outlined"
        size={size}
        icon={<GlobalOutlined />}
        onClick={open}
      />
    </PermissionGuard>

    {checked && <Dialog open onClose={close} fullWidth maxWidth="xs">
      <DialogTitle sx={{ overflowWrap: 'anywhere' }}>
        {t('mgmt.urls.domains.title', 'Domains of {{name}}', { name: containerName })}
      </DialogTitle>
      <DialogContent>
        <Stack spacing={1}>
          <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.urls.domains.text',
              'Each ticked domain gives the app one more address, reachable from the internet: a copy of its URL {{name}}, with the same target and settings. Unticking a domain deletes that address.',
              { name: base.Name })}
          </Typography>
          {rows.map((row) => (
            <FormControlLabel
              key={row.domain}
              control={<Checkbox
                checked={Boolean(checked[row.domain])}
                disabled={saving || row.own}
                onChange={(event) => setChecked({ ...checked, [row.domain]: event.target.checked })}
              />}
              label={<Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
                <GlobalOutlined /> {row.host}
              </Typography>}
            />
          ))}
          {rows.some((row) => row.own) && <Typography variant="caption" color="textSecondary">
            {t('mgmt.urls.domains.own',
              'A domain that cannot be unticked is the address of the URL itself: change or delete that URL from its settings.')}
          </Typography>}
          {rows.some((row) => checked[row.domain]) && <PortForwardHint config={config} />}
          {errors.length > 0 && <Alert severity="error" sx={{ overflowWrap: 'anywhere' }}>
            {errors.map((problem, index) => <div key={index}>{problem}</div>)}
          </Alert>}
          <ExternalPortNote config={config} example={(rows[0] || {}).host} />
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={close} disabled={saving}>{t('global.cancelAction')}</Button>
        <LoadingButton
          loading={saving}
          disabled={toAdd.length === 0 && toRemove.length === 0}
          onClick={save}
        >
          {t('global.saveAction')}
        </LoadingButton>
      </DialogActions>
    </Dialog>}
  </span>;
};

export default AppLinks;
