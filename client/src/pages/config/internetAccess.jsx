// Fork addition: "Internet access" card of the HTTP settings. It shows through
// which name Cosmos accepts requests and lets the router forward Cosmos' ports
// over UPnP, so no manual NAT rule is needed. See src/upnp on the server.
import * as React from 'react';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { Alert, Chip, Stack, Typography } from '@mui/material';
import { CheckCircleOutlined, CloseCircleOutlined, ReloadOutlined } from '@ant-design/icons';
import { LoadingButton } from '@mui/lab';
import { useTranslation } from 'react-i18next';
import { CosmosCheckbox, CosmosInputText } from './users/formShortcuts';

// A name a browser on the internet can resolve: not an IP address, not a
// single-label host.
const looksLikeDomain = (host) =>
  /^(?=.*[a-z])([a-z0-9-]+\.)+[a-z]{2,}$/i.test((host || '').trim());

const InternetAccess = ({ formik }) => {
  const { t } = useTranslation();
  const [status, setStatus] = React.useState(null);
  const [loading, setLoading] = React.useState(false);
  const [loadError, setLoadError] = React.useState('');

  const values = formik.values;
  const hostname = (values.Hostname || '').trim();
  const hasDomain = looksLikeDomain(hostname);
  const httpsOn = values.HTTPSCertificateMode !== 'DISABLED';

  const load = (retry) => {
    setLoading(true);
    setLoadError('');
    (retry ? API.config.upnpRetry() : API.config.upnpStatus())
      .then((response) => setStatus(response.data))
      .catch((error) => setLoadError(error.message || String(error)))
      .finally(() => setLoading(false));
  };

  React.useEffect(() => { load(false); }, []);

  const httpsMapping = status && (status.mappings || []).find((m) => m.name === 'HTTPS');
  const publicPort = httpsMapping ? httpsMapping.externalPort : (values.UPnP_ExternalHTTPSPort || values.HTTPSPort);

  return (
    <MainCard title={t('mgmt.config.internet.title', 'Internet access')}>
      <Stack spacing={2}>
        {/* 1. the name Cosmos answers to */}
        {hasDomain ? (
          <Alert severity={httpsOn ? 'info' : 'warning'}>
            {t('mgmt.config.internet.domain.set',
              'Cosmos accepts requests for {{hostname}} only; any other name is refused.', { hostname })}
            {' '}
            {httpsOn
              ? t('mgmt.config.internet.domain.https', 'HTTPS is enforced.')
              : t('mgmt.config.internet.domain.noHttps',
                'HTTPS is disabled: do not expose Cosmos to the internet like this. Choose a certificate mode in the HTTPS tab.')}
          </Alert>
        ) : (
          <Alert severity="warning">
            {t('mgmt.config.internet.domain.missing',
              'The Hostname above is not a domain name, so Cosmos cannot be reached by name from the internet. Set it to the domain you want to allow (for example cosmos.example.com): requests for any other name are refused.')}
          </Alert>
        )}

        {/* 2. router port forwarding */}
        <CosmosCheckbox
          label={t('mgmt.config.internet.upnp.enableLabel', 'Open the port on my router automatically (UPnP)')}
          name="UPnP_Enabled"
          formik={formik}
        />

        {values.UPnP_Enabled && (<>
          <Alert severity="warning">
            {t('mgmt.config.internet.upnp.warning',
              'This makes Cosmos reachable from the internet. Use a domain with HTTPS, strong passwords and two-factor authentication. UPnP must be enabled on the router.')}
          </Alert>

          <CosmosInputText
            label={t('mgmt.config.internet.upnp.externalHttpsLabel', 'External HTTPS port (empty: same as the HTTPS port)')}
            name="UPnP_ExternalHTTPSPort"
            placeholder={String(values.HTTPSPort || '')}
            inputProps={{ inputMode: 'numeric' }}
            formik={formik}
          />

          <CosmosCheckbox
            label={t('mgmt.config.internet.upnp.forwardHttpLabel', 'Also forward the HTTP port (not recommended)')}
            name="UPnP_ForwardHTTP"
            formik={formik}
          />

          {values.UPnP_ForwardHTTP && (
            <CosmosInputText
              label={t('mgmt.config.internet.upnp.externalHttpLabel', 'External HTTP port (empty: same as the HTTP port)')}
              name="UPnP_ExternalHTTPPort"
              placeholder={String(values.HTTPPort || '')}
              inputProps={{ inputMode: 'numeric' }}
              formik={formik}
            />
          )}
        </>)}

        {/* 3. what the router is doing now (saved settings, not the form) */}
        <Stack spacing={1}>
          <Stack direction="row" spacing={1.5} alignItems="center" flexWrap="wrap" useFlexGap>
            <Typography variant="subtitle1">
              {t('mgmt.config.internet.status.title', 'Router status')}
            </Typography>
            <LoadingButton
              size="small"
              variant="outlined"
              loading={loading}
              startIcon={<ReloadOutlined />}
              onClick={() => load(true)}
            >
              {t('mgmt.config.internet.status.retry', 'Check now')}
            </LoadingButton>
          </Stack>

          {loadError && <Alert severity="error">{loadError}</Alert>}

          {status && !status.enabled && !(status.mappings || []).length && (
            <Typography variant="body2" color="text.secondary">
              {t('mgmt.config.internet.status.off', 'Automatic port forwarding is off.')}
            </Typography>
          )}

          {status && status.error && <Alert severity="error">{status.error}</Alert>}
          {status && status.warning && <Alert severity="warning">{status.warning}</Alert>}

          {status && status.gatewayIP && (
            <Typography variant="body2" color="text.secondary" sx={{ overflowWrap: 'anywhere' }}>
              {t('mgmt.config.internet.status.router', 'Router: {{name}} ({{ip}})', { name: status.gateway || 'UPnP', ip: status.gatewayIP })}
              {status.externalIP
                ? ' · ' + t('mgmt.config.internet.status.externalIP', 'internet address: {{ip}}', { ip: status.externalIP })
                : ''}
            </Typography>
          )}

          {status && (status.mappings || []).map((m) => (
            <Stack key={m.name} direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
              <Chip
                size="small"
                color={m.active ? 'success' : 'error'}
                icon={m.active ? <CheckCircleOutlined /> : <CloseCircleOutlined />}
                label={m.name}
              />
              <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
                {m.active
                  ? t('mgmt.config.internet.status.forwarded', 'port {{external}} on the router goes to port {{internal}} here',
                    { external: m.externalPort, internal: m.internalPort })
                  : (m.error || t('mgmt.config.internet.status.notForwarded', 'not forwarded'))}
              </Typography>
            </Stack>
          ))}

          {status && httpsMapping && httpsMapping.active && hasDomain && (
            <Typography variant="body2" color="text.secondary" sx={{ overflowWrap: 'anywhere' }}>
              {t('mgmt.config.internet.status.reachable',
                'Once {{hostname}} points to {{ip}} in DNS, Cosmos is reachable at https://{{hostname}}:{{port}}',
                { hostname, ip: status.externalIP || '…', port: publicPort })}
            </Typography>
          )}

          <Typography variant="caption" color="text.secondary">
            {t('mgmt.config.internet.status.note',
              'The status reflects the saved settings. The router keeps a port open for at most one hour after Cosmos stops.')}
          </Typography>
        </Stack>
      </Stack>
    </MainCard>
  );
};

export default InternetAccess;
