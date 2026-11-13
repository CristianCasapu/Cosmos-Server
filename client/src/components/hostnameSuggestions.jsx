// Fork addition: proposes addresses for an app instead of leaving the hostname
// to be typed: one per domain configured under Configuration > HTTPS >
// Domains, a .local name for the local network, and the upstream default.
// Clicking a proposal fills the hostname field; it can still be edited.
import * as React from 'react';
import { Chip, Grid, Stack, Typography } from '@mui/material';
import { GlobalOutlined, HomeOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { getHostnameFromName, IsRouteSocketProxy } from '../utils/routes';
import { isLocalHost } from '../utils/appLinks';

// The app's name as a DNS label.
export const hostLabel = (name) => (name || '')
  .replace(/^\//, '')
  .toLowerCase()
  .replace(/[_\s.]+/g, '-')
  .replace(/[^a-z0-9-]/g, '')
  .replace(/^-+|-+$/g, '')
  .slice(0, 63);

// The domains the admin configured, most specific first.
export const configuredDomains = (config) => {
  const zones = (config && config.HTTPConfig && config.HTTPConfig.DNSZones) || [];
  return zones
    .map((zone) => (zone.Zone || '').trim().toLowerCase())
    .filter((zone) => zone && !zone.endsWith('.local'));
};

// The name an address is built from: the app's name, with the prefix and
// suffix an installer route asks for (hostPrefix / hostSuffix), if any.
export const addressName = (name, route) => ((route && route.hostPrefix) || '') + (name || '') + ((route && route.hostSuffix) || '');

// The local address every app gets: a .local name when Cosmos publishes
// them, otherwise upstream's default (the server's address with a free port).
// `route` is only given by the installer, for its hostPrefix / hostSuffix.
export const localAddress = (name, config, route) => {
  const label = hostLabel(addressName(name, route));
  if (label && config && config.HTTPConfig && config.HTTPConfig.PublishMDNS) {
    return label + '.local';
  }
  return getHostnameFromName(name, route || null, config);
};

// The external address of an app under one of the configured domains.
export const externalAddress = (name, domain) => hostLabel(name) + '.' + domain;

// True for an installer route (lower-case keys) that gets a hostname: it is
// reached by name, not by a port.
export const installerRouteHasAddress = (route) => !!(route && route.useHost)
  && !IsRouteSocketProxy({ Target: route.target || route.Target, Mode: route.mode || route.Mode });

// The routes an installer is about to create for the service `name`, plus one
// external copy (same target and settings) of each per attached domain.
export const withExternalRoutes = (routes, name, domains) => {
  if (!routes || !domains || domains.length === 0) {
    return routes;
  }
  const taken = routes.map((route) => route.host);
  const extras = [];
  routes.filter(installerRouteHasAddress).forEach((route) => {
    domains.forEach((domain) => {
      let host = externalAddress(addressName(name, route), domain);
      if (host === route.host) {
        return; // already picked as the route's own address
      }
      if (taken.includes(host)) {
        // two routes of one service: tell them apart by the route's name
        host = externalAddress(addressName(name, route) + '-' + route.name, domain);
      }
      if (taken.includes(host)) {
        return;
      }
      taken.push(host);
      extras.push({ ...route, name: route.name + '-' + domain, host });
    });
  });
  return [...routes, ...extras];
};

export const hostnameSuggestions = (name, config) => {
  const label = hostLabel(name);
  if (!label) {
    return [];
  }

  const suggestions = [];
  const add = (host, local) => {
    if (host && !suggestions.find((s) => s.host === host)) {
      suggestions.push({ host, local });
    }
  };

  // the local address first: every app has one; a domain adds an external one
  if (config && config.HTTPConfig && config.HTTPConfig.PublishMDNS) {
    add(label + '.local', true);
  }
  configuredDomains(config).forEach((domain) => add(label + '.' + domain, false));

  let fallback = '';
  try {
    fallback = getHostnameFromName(name, null, config);
  } catch (e) {
    // the configuration is not loaded yet: no default to propose
  }
  if (fallback && !fallback.includes(' ')) {
    add(fallback, isLocalHost(fallback));
  }
  return suggestions;
};

// `inline` renders the proposals without the grid cell and indent of the route form.
const HostnameSuggestions = ({ name, value, config, onPick, disabled, inline }) => {
  const { t } = useTranslation();
  const suggestions = React.useMemo(() => hostnameSuggestions(name, config), [name, config]);

  if (disabled || suggestions.length === 0) {
    return null;
  }

  const noDomain = configuredDomains(config).length === 0;

  const Cell = inline ? React.Fragment : Grid;
  const cellProps = inline ? {} : { item: true, xs: 12 };

  return <Cell {...cellProps}>
    <Stack spacing={0.75} style={inline ? undefined : { paddingLeft: '20px' }}>
      <Typography variant="caption" color="textSecondary">
        {t('mgmt.urls.edit.hostSuggestions.title', 'Suggested addresses (click to use one):')}
      </Typography>
      <Stack direction="row" useFlexGap flexWrap="wrap" spacing={1}>
        {suggestions.map((suggestion) => (
          <Chip
            key={suggestion.host}
            size="small"
            clickable
            color={value === suggestion.host ? 'primary' : 'default'}
            variant={value === suggestion.host ? 'filled' : 'outlined'}
            icon={suggestion.local ? <HomeOutlined /> : <GlobalOutlined />}
            label={suggestion.host}
            title={suggestion.local
              ? t('mgmt.urls.edit.hostSuggestions.local', 'Works on the local network only')
              : t('mgmt.urls.edit.hostSuggestions.domain', 'Uses a domain from Configuration > Domains')}
            onClick={() => onPick(suggestion.host)}
            sx={{ maxWidth: '100%' }}
          />
        ))}
      </Stack>
      {noDomain && <Typography variant="caption" color="textSecondary">
        {t('mgmt.urls.edit.hostSuggestions.noDomain',
          'To get addresses under your own domain, add the domain in Configuration > Domains.')}
      </Typography>}
    </Stack>
  </Cell>;
};

// One line on what the router has to do while Cosmos does not ask it to.
// `port` is the HTTPS port in use when the caller knows it (the environment
// can set another one than the settings hold).
export const PortForwardHint = ({ config, port }) => {
  const { t } = useTranslation();
  const http = (config && config.HTTPConfig) || {};
  if (http.UPnP && http.UPnP.Enabled) {
    return null;
  }
  return <Typography variant="caption" color="textSecondary">
    {t('mgmt.urls.external.forwardByHand',
      'Automatic port forwarding is off: forward port {{port}} (HTTPS) to this server on your router by hand, or switch it on under Configuration > HTTP > Internet access.',
      { port: port || http.HTTPSPort || '443' })}
  </Typography>;
};

// DomainPicker attaches domains to an app that is getting its local address:
// each selected domain adds an external address. None is selected by default.
export const DomainPicker = ({ name, config, selected, onChange }) => {
  const { t } = useTranslation();
  const domains = configuredDomains(config);

  if (domains.length === 0) {
    return <Typography variant="caption" color="textSecondary">
      {t('mgmt.urls.external.noDomain',
        'This address works on the local network. To also give the app an address reachable from the internet, add a domain in Configuration > Domains.')}
    </Typography>;
  }

  const toggle = (domain) => onChange(selected.includes(domain) ? selected.filter((d) => d !== domain) : [...selected, domain]);

  return <Stack spacing={1}>
    <Typography variant="subtitle2">
      {t('mgmt.urls.external.title', 'Also reachable from the internet through (optional):')}
    </Typography>
    <Stack direction="row" useFlexGap flexWrap="wrap" spacing={1}>
      {domains.map((domain) => (
        <Chip
          key={domain}
          clickable
          icon={<GlobalOutlined />}
          color={selected.includes(domain) ? 'primary' : 'default'}
          variant={selected.includes(domain) ? 'filled' : 'outlined'}
          label={hostLabel(name) ? externalAddress(name, domain) : domain}
          onClick={() => toggle(domain)}
          sx={{ maxWidth: '100%' }}
        />
      ))}
    </Stack>
    <Typography variant="caption" color="textSecondary">
      {selected.length === 0
        ? t('mgmt.urls.external.none', 'Nothing selected: the app gets its local address only.')
        : t('mgmt.urls.external.selected',
          'One more address is created per selected domain. For it to work from outside, the name must point to this server in DNS and the router must forward the port (Configuration > HTTP > Internet access).')}
    </Typography>
    {selected.length > 0 && <PortForwardHint config={config} />}
  </Stack>;
};

export default HostnameSuggestions;
