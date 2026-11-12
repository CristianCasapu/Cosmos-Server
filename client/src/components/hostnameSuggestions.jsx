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

  configuredDomains(config).forEach((domain) => add(label + '.' + domain, false));
  if (config && config.HTTPConfig && config.HTTPConfig.PublishMDNS) {
    add(label + '.local', true);
  }

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

export default HostnameSuggestions;
