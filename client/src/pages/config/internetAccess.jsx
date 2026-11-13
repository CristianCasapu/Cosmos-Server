// Fork addition: "Internet access" card of the HTTP settings. It shows through
// which name Cosmos accepts requests and lets the router forward Cosmos' ports
// over UPnP, so no manual NAT rule is needed. See src/upnp on the server.
import * as React from 'react';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { Alert, Stack, Typography } from '@mui/material';
import { ReloadOutlined } from '@ant-design/icons';
import { LoadingButton } from '@mui/lab';
import PermissionGuard from '../../components/permissionGuard';
import { notify } from '../../utils/activity';
import { PERM_ADMIN } from '../../utils/permissions';
import { useTranslation } from 'react-i18next';
import { CosmosCheckbox, CosmosInputText } from './users/formShortcuts';
import { MappingsOverview, setRouterStatus } from '../../components/routerPorts';
import { hostWithPort, overviewRows } from '../../utils/routerPorts';

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
      .then((response) => {
        setStatus(response.data);
        setRouterStatus(response.data); // the other screens of the page show the same answer
        // say how it went: a failure when the page opens, either outcome after "Check now"
        const st = response.data || {};
        const rows = overviewRows(st);
        const failure = st.error || (rows.find((m) => m.state === 'failed' && m.error) || {}).error;
        if (failure || st.paused) {
          notify(t('mgmt.config.internet.status.retryFailed', 'Port forwarding failed: {{error}}', { error: failure || '' }), 'error');
        } else if (retry && rows.some((m) => m.active)) {
          notify(t('mgmt.config.internet.status.retryOk', 'The router forwards the port.'), 'success');
        }
      })
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

        <CosmosInputText
          label={t('mgmt.config.internet.reserved.label', 'External ports Cosmos never asks the router for')}
          name="UPnP_ReservedPorts"
          placeholder={((status && status.reserved && status.reserved.length && !values.UPnP_ReservedPorts) ? status.reserved : [25, 80, 110, 143, 443, 465, 587, 993, 995]).join(', ')}
          helperText={t('mgmt.config.internet.reserved.help',
            'Empty: the list shown (web and mail). A port forwarded in the router\'s own pages to another device is often invisible to Cosmos: list such ports here, and Cosmos never asks for them, neither for itself nor for an app. "none" for no port.')}
          formik={formik}
        />

        {/* 3. what the router is doing now (saved settings, not the form) */}
        <Stack spacing={1}>
          <Stack direction="row" spacing={1.5} alignItems="center" flexWrap="wrap" useFlexGap>
            <Typography variant="subtitle1">
              {t('mgmt.config.internet.status.title', 'Router status')}
            </Typography>
            <PermissionGuard permission={PERM_ADMIN}>
              <LoadingButton
                size="small"
                variant="outlined"
                loading={loading}
                startIcon={<ReloadOutlined />}
                onClick={() => load(true)}
              >
                {t('mgmt.config.internet.status.retry', 'Check now')}
              </LoadingButton>
            </PermissionGuard>
          </Stack>

          {loadError && <Alert severity="error">{loadError}</Alert>}

          {status && !status.enabled && !overviewRows(status).length && (
            <Typography variant="body2" color="text.secondary">
              {t('mgmt.config.internet.status.off', 'Automatic port forwarding is off.')}
            </Typography>
          )}

          {status && status.error && <Alert severity="error">{status.error}</Alert>}
          {status && status.paused && <Alert severity="warning">
            {t('mgmt.config.internet.status.paused',
              'Cosmos has stopped trying. Check that UPnP is enabled on the router, then press "Check now"; or turn automatic port forwarding off.')}
          </Alert>}
          {status && status.warning && <Alert severity="warning">{status.warning}</Alert>}

          {status && status.gatewayIP && (
            <Typography variant="body2" color="text.secondary" sx={{ overflowWrap: 'anywhere' }}>
              {t('mgmt.config.internet.status.router', 'Router: {{name}} ({{ip}})', { name: status.gateway || 'UPnP', ip: status.gatewayIP })}
              {status.externalIP
                ? ' · ' + t('mgmt.config.internet.status.externalIP', 'internet address: {{ip}}', { ip: status.externalIP })
                : ''}
            </Typography>
          )}

          <MappingsOverview status={status} onStatus={setStatus} formik={formik} />

          {status && httpsMapping && httpsMapping.active && hasDomain && (
            <Typography variant="body2" color="text.secondary" sx={{ overflowWrap: 'anywhere' }}>
              {t('mgmt.config.internet.status.reachable',
                'Once {{hostname}} points to {{ip}} in DNS, Cosmos is reachable at https://{{address}}',
                { hostname, ip: status.externalIP || '…', address: hostWithPort(hostname, publicPort, 'https') })}
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
