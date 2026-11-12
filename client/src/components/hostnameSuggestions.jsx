// Fork addition: proposes addresses for an app instead of leaving the hostname
// to be typed: one per domain configured under Configuration > HTTPS >
// Domains, a .local name for the local network, and the upstream default.
// Clicking a proposal fills the hostname field; it can still be edited.
import * as React from 'react';
import { Chip, Grid, Stack, Typography } from '@mui/material';
import { GlobalOutlined, HomeOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { getHostnameFromName } from '../utils/routes';

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

// The local address every app gets: a .local name when Cosmos publishes
// them, otherwise upstream's default (the server's address with a free port).
export const localAddress = (name, config) => {
  const label = hostLabel(name);
  if (label && config && config.HTTPConfig && config.HTTPConfig.PublishMDNS) {
    return label + '.local';
  }
  return getHostnameFromName(name, null, config);
};

// The external address of an app under one of the configured domains.
export const externalAddress = (name, domain) => hostLabel(name) + '.' + domain;

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

  const fallback = getHostnameFromName(name, null, config);
  if (fallback && !fallback.includes(' ')) {
    const bare = fallback.split(':')[0];
    add(fallback, bare.endsWith('.local') || /^\d+\.\d+\.\d+\.\d+$/.test(bare));
  }
  return suggestions;
};

const HostnameSuggestions = ({ name, value, config, onPick, disabled }) => {
  const { t } = useTranslation();
  const suggestions = React.useMemo(() => hostnameSuggestions(name, config), [name, config]);

  if (disabled || suggestions.length === 0) {
    return null;
  }

  const noDomain = configuredDomains(config).length === 0;

  return <Grid item xs={12}>
    <Stack spacing={0.75} style={{ paddingLeft: '20px' }}>
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
  </Grid>;
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
          label={externalAddress(name, domain) || domain}
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
  </Stack>;
};

export default HostnameSuggestions;
