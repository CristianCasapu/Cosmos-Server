import * as React from 'react';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { Formik } from 'formik';
import * as Yup from 'yup';
import {
  Alert,
  Button,
  Grid,
  Stack,
  FormHelperText,
} from '@mui/material';
import RestartModal from './users/restart';
import { ArrowDownOutlined, DeleteOutlined, SyncOutlined } from '@ant-design/icons';
import { LoadingButton } from '@mui/lab';
import { useClientInfos } from '../../utils/hooks';
import { PERM_CONFIGURATION, PERM_ADMIN_READ } from '../../utils/permissions';
import ConfirmModal from '../../components/confirmModal';
import PermissionGuard from '../../components/permissionGuard';
import PrettyTabbedView from '../../components/tabbedView/tabbedView';

import { useTranslation } from 'react-i18next';

// Fork addition: an empty value or a port number between 1 and 65535.
// Fork addition: a folder on the server, checked again (and created if missing) by the server on save.
const hostDirectory = (t) => Yup.string().test(
  'host-directory',
  t('mgmt.config.pathValidation', 'Enter an absolute path such as /storage/backups, without ".." and without spaces at either end'),
  (value) => !value || (/^\/[^\0\r\n]*$/.test(value) && !value.split('/').includes('..') && value.trim() === value),
);

const optionalPort = (t) => Yup.string().test(
  'port',
  t('mgmt.config.internet.upnp.portValidation', 'Enter a port between 1 and 65535, or leave empty'),
  (value) => {
    const text = String(value || '').trim();
    return text === '' || (/^\d{1,5}$/.test(text) && Number(text) >= 1 && Number(text) <= 65535);
  },
);

import ConfigGeneral from './configGeneral';
import { snapshotsInitialValues, snapshotsToConfig, snapshotsValidation } from './snapshots'; // fork addition
import { telegramInitialValues, telegramToConfig, telegramValidation } from './telegram'; // fork addition
import ConfigHTTP from './configHTTP';
import ConfigHTTPS from './configHTTPS';
import ConfigExternal from './configExternal';
import ConfigAppearance from './configAppearance';
import ConfigAPITokens from './configAPITokens';

const SaveButton = ({ formik, saveLabel }) => {
  return (
    <PermissionGuard permission={PERM_CONFIGURATION}>
    <MainCard>
      {formik.errors.submit && (
        <Grid item xs={12}>
          <FormHelperText error>{formik.errors.submit}</FormHelperText>
        </Grid>
      )}
      <Grid item xs={12}>
        <LoadingButton
          disableElevation
          loading={formik.isSubmitting}
          fullWidth
          size="large"
          type="submit"
          variant="contained"
          color="primary"
        >
          {saveLabel}
        </LoadingButton>
      </Grid>
    </MainCard>
    </PermissionGuard>
  );
};

const ConfigManagement = () => {
  const { t } = useTranslation();
  const [config, setConfig] = React.useState(null);
  const [serverCertificate, setServerCertificate] = React.useState("");
  const [status, setStatus] = React.useState(null);
  const [openResartModal, setOpenRestartModal] = React.useState(false);
  const [saveLabel, setSaveLabel] = React.useState(t('global.saveAction'));
  const { hasPermission } = useClientInfos();
  const isAdmin = hasPermission(PERM_CONFIGURATION);
  const canViewAdmin = hasPermission(PERM_ADMIN_READ);

  // Fork addition: `retries`. Saving makes the server restart its listener, which refuses the
  // reload that follows the save; the page then kept showing the settings from before the save
  // (and an uncaught error). After a save the reload is tried again, once a second.
  function refresh(retries = 0) {
    let again = false;
    const retry = (error) => {
      // only when the server did not answer at all: an error it answered with has been shown
      if (retries > 0 && !again && !(error && error.status)) {
        again = true;
        setTimeout(() => refresh(retries - 1), 1000);
      }
    };

    API.config.get().then((res) => {
      setConfig(res.data);
      setServerCertificate(res.serverCertificate || "");
    }).catch(retry);

    API.getStatus().then((res) => {
      setStatus(res.data);
    }).catch(retry);
  }

  React.useEffect(() => {
    refresh();
  }, []);

  const wrapTab = (formik, content) => (
    <div style={{ maxWidth: "1000px", margin: "auto" }}>
      <Stack spacing={3}>
        {/* Fork addition: useFlexGap flexWrap. On a phone the three buttons were squeezed into one line, their text broken and the icons pushed out of them */}
        <Stack direction="row" spacing={2} useFlexGap flexWrap="wrap">
          <Button variant="contained" color="primary" startIcon={<SyncOutlined />} onClick={() => {
              refresh();
          }}>{t('mgmt.config.header.refreshButton.refreshLabel')}</Button>

          {canViewAdmin && <Button variant="outlined" color="primary"
            startIcon={<ArrowDownOutlined />} href={"/cosmos/_logs"}>
            {t('mgmt.config.header.downloadLogsButton.downloadLogsLabel')}
          </Button>}

          <PermissionGuard permission={PERM_CONFIGURATION}>
            <ConfirmModal variant="outlined" color="warning" startIcon={<DeleteOutlined />} callback={() => {
                API.metrics.reset().then(() => {
                  refresh();
                });
            }}
            label={t('mgmt.config.header.purgeMetricsButton.purgeMetricsLabel')}
            content={t('mgmt.config.header.purgeMetricsButton.purgeMetricsPopUp.cofirmAction')} />
          </PermissionGuard>
        </Stack>

        {!isAdmin && <div>
          <Alert severity="warning">{t('mgmt.config.general.notAdminWarning')}
          </Alert>
        </div>}

        <SaveButton formik={formik} saveLabel={saveLabel} />
        {content}
        <SaveButton formik={formik} saveLabel={saveLabel} />
      </Stack>
    </div>
  );

  return <div>
    {config && <>
      <RestartModal openModal={openResartModal} setOpenModal={setOpenRestartModal} />

      <Formik
        initialValues={{
          LoggingLevel: config.LoggingLevel,
          RequireMFA: config.RequireMFA,
          GeoBlocking: config.BlockedCountries,
          CountryBlacklistIsWhitelist: config.CountryBlacklistIsWhitelist,
          AutoUpdate: config.AutoUpdate,
          BetaUpdates: config.BetaUpdates,
          MaintenanceTime: config.MaintenanceTime || '02:00', // fork addition
          Timezone: config.Timezone || '', // fork addition

          Licence: config.Licence,
          ServerToken: config.ServerToken,

          Hostname: config.HTTPConfig.Hostname,
          GenerateMissingTLSCert: config.HTTPConfig.GenerateMissingTLSCert,
          GenerateMissingAuthCert: config.HTTPConfig.GenerateMissingAuthCert,
          HTTPPort: config.HTTPConfig.HTTPPort,
          HTTPSPort: config.HTTPConfig.HTTPSPort,
          SSLEmail: config.HTTPConfig.SSLEmail,
          // a server only chooses between HTTPS and none: how is up to each domain
          HTTPSCertificateMode: (!config.HTTPConfig.HTTPSCertificateMode || config.HTTPConfig.HTTPSCertificateMode === "DISABLED") ? "DISABLED" : "LETSENCRYPT",
          ForceHTTPSCertificateRenewal: config.HTTPConfig.ForceHTTPSCertificateRenewal,
          AdvertisedAddress: config.HTTPConfig.AdvertisedAddress || "",
          UseForwardedFor: config.HTTPConfig.UseForwardedFor,
          TrustedProxies: config.HTTPConfig.TrustedProxies && config.HTTPConfig.TrustedProxies.join(', '),
          AllowSearchEngine: config.HTTPConfig.AllowSearchEngine,
          AllowHTTPLocalIPAccess: config.HTTPConfig.AllowHTTPLocalIPAccess,
          PublishMDNS: config.HTTPConfig.PublishMDNS,
          UPnP_Enabled: Boolean(config.HTTPConfig.UPnP && config.HTTPConfig.UPnP.Enabled),
          UPnP_ForwardHTTP: Boolean(config.HTTPConfig.UPnP && config.HTTPConfig.UPnP.ForwardHTTP),
          UPnP_ExternalHTTPSPort: (config.HTTPConfig.UPnP && config.HTTPConfig.UPnP.ExternalHTTPSPort) || '',
          UPnP_ExternalHTTPPort: (config.HTTPConfig.UPnP && config.HTTPConfig.UPnP.ExternalHTTPPort) || '',
          UPnP_ReservedPorts: (config.HTTPConfig.UPnP && config.HTTPConfig.UPnP.ReservedPorts) || '',

          Email_Enabled: config.EmailConfig.Enabled,
          Email_Host: config.EmailConfig.Host,
          Email_Port: config.EmailConfig.Port,
          Email_Username: config.EmailConfig.Username,
          Email_Password: config.EmailConfig.Password,
          Email_From: config.EmailConfig.From,
          Email_UseTLS : config.EmailConfig.UseTLS,
          Email_AllowInsecureTLS : config.EmailConfig.AllowInsecureTLS,
          Email_NotifyLogin: config.EmailConfig.NotifyLogin,

          SkipPruneNetwork: config.DockerConfig.SkipPruneNetwork,
          SkipPruneImages: config.DockerConfig.SkipPruneImages,
          DefaultDataPath: config.DockerConfig.DefaultDataPath || "/cosmos-storage",

          Background: config && config.HomepageConfig && config.HomepageConfig.Background,
          Expanded: config && config.HomepageConfig && config.HomepageConfig.Expanded,
          PrimaryColor: config && config.ThemeConfig && config.ThemeConfig.PrimaryColor,
          SecondaryColor: config && config.ThemeConfig && config.ThemeConfig.SecondaryColor,

          MonitoringEnabled: !config.MonitoringDisabled,

          BackupOutputDir: config.BackupOutputDir,
          // fork addition: when the configuration backup runs
          BackupFrequency: config.BackupFrequency || '2h',
          BackupTime: config.BackupTime || '00:00',
          BackupWeekday: String(config.BackupWeekday || 0), // text: the select treats the number 0 as empty
          ...snapshotsInitialValues(config), // fork addition: Snapshots_*, the incremental backup
          ...telegramInitialValues(config), // fork addition: Telegram_*, the notifications; never the token
          IncrBackupOutputDir: config.Backup && config.Backup.Backups && config.Backup.Backups["Cosmos Internal Backup"] ? config.Backup.Backups["Cosmos Internal Backup"].Repository : "",

          AdminWhitelistIPs: config.AdminWhitelistIPs && config.AdminWhitelistIPs.join(', '),
          AdminConstellationOnly: config.AdminConstellationOnly,

          PostgresHost: config.Database.PostgresHost,
          PostgresDatabase: config.Database.PostgresDatabase,
          PostgresUsername: config.Database.PostgresUsername,
          PostgresPassword: config.Database.PostgresPassword,
          MetricsNodeName: config.Database.NodeName,
        }}

        validationSchema={Yup.object().shape({
          Hostname: Yup.string().max(255).required(t('mgmt.config.http.hostnameInput.HostnameValidation')),
          LoggingLevel: Yup.string().max(255).required(t('mgmt.config.general.logLevelInput.logLevelValidation')),
          BackupOutputDir: hostDirectory(t),
          BackupTime: Yup.string().matches(/^([01]\d|2[0-3]):[0-5]\d$/,
            t('mgmt.config.update.timeValidation', 'Enter a time as HH:MM, for example 02:00')),
          DefaultDataPath: hostDirectory(t),
          MaintenanceTime: Yup.string().matches(/^([01]\d|2[0-3]):[0-5]\d$/,
            t('mgmt.config.update.timeValidation', 'Enter a time as HH:MM, for example 02:00')),
          // Fork addition: the server decides whether the zone exists
          Timezone: Yup.string().trim().matches(/^[A-Za-z0-9_+-]+(\/[A-Za-z0-9_+-]+)*$/, {
            message: t('mgmt.config.timezone.validation', 'Enter a time zone name such as Europe/Bucharest, or leave empty'),
            excludeEmptyString: true,
          }),
          UPnP_ExternalHTTPSPort: optionalPort(t),
          UPnP_ExternalHTTPPort: optionalPort(t),
          UPnP_ReservedPorts: Yup.string().max(200).matches(/^\s*(none|(\d{1,5}[\s,;]*)*)\s*$/i,
            t('mgmt.config.internet.reserved.validation', 'Enter port numbers separated by commas, "none", or leave it empty for the usual list')),
          ...snapshotsValidation(t), // fork addition
          ...telegramValidation(t, config), // fork addition
        })}

        onSubmit={async (values, { setSubmitting }) => {
          setSubmitting(true);

          if(!values.IncrBackupOutputDir && config.Backup.Backups && config.Backup.Backups["Cosmos Internal Backup"]) {
            delete config.Backup.Backups["Cosmos Internal Backup"];
          }

          let toSave = {
            ...config,
            Licence: values.Licence,
            ServerToken: values.ServerToken,
            Database: {
              ...config.Database,
              PostgresHost: values.PostgresHost,
              PostgresDatabase: values.PostgresDatabase,
              PostgresUsername: values.PostgresUsername,
              PostgresPassword: values.PostgresPassword,
              NodeName: values.MetricsNodeName,
            },
            LoggingLevel: values.LoggingLevel,
            RequireMFA: values.RequireMFA,
            AutoUpdate: values.AutoUpdate,
            BetaUpdates: values.BetaUpdates,
            MaintenanceTime: values.MaintenanceTime, // fork addition
            Timezone: (values.Timezone || '').trim(), // fork addition
            BlockedCountries: values.GeoBlocking,
            CountryBlacklistIsWhitelist: values.CountryBlacklistIsWhitelist,
            MonitoringDisabled: !values.MonitoringEnabled,
            BackupOutputDir: values.BackupOutputDir,
            BackupFrequency: values.BackupFrequency,
            BackupTime: values.BackupTime,
            BackupWeekday: Number(values.BackupWeekday) || 0,
            Snapshots: snapshotsToConfig(values), // fork addition
            Telegram: telegramToConfig(values, config), // fork addition: a token left empty keeps the stored one
            AdminConstellationOnly: values.AdminConstellationOnly,
            AdminWhitelistIPs: (values.AdminWhitelistIPs && values.AdminWhitelistIPs != "") ?
              values.AdminWhitelistIPs.split(',').map((x) => x.trim()) : [],
            HTTPConfig: {
              ...config.HTTPConfig,
              Hostname: values.Hostname,
              GenerateMissingAuthCert: values.GenerateMissingAuthCert,
              HTTPPort: values.HTTPPort,
              HTTPSPort: values.HTTPSPort,
              SSLEmail: values.SSLEmail,
              HTTPSCertificateMode: values.HTTPSCertificateMode,
              ForceHTTPSCertificateRenewal: values.ForceHTTPSCertificateRenewal,
              AdvertisedAddress: values.AdvertisedAddress,
              UseForwardedFor: values.UseForwardedFor,
              TrustedProxies: (values.TrustedProxies && values.TrustedProxies != "") ?
                values.TrustedProxies.split(',').map((x) => x.trim()) : [],
              AllowSearchEngine: values.AllowSearchEngine,
              AllowHTTPLocalIPAccess: values.AllowHTTPLocalIPAccess,
              PublishMDNS: values.PublishMDNS,
              UPnP: {
                Enabled: Boolean(values.UPnP_Enabled),
                ForwardHTTP: Boolean(values.UPnP_ForwardHTTP),
                ExternalHTTPSPort: String(values.UPnP_ExternalHTTPSPort || '').trim(),
                ExternalHTTPPort: String(values.UPnP_ExternalHTTPPort || '').trim(),
                ReservedPorts: String(values.UPnP_ReservedPorts || '').trim(),
                // AppPorts (the ports of apps open on the router) are not sent: the server keeps its own list
              },
            },
            EmailConfig: {
              ...config.EmailConfig,
              Enabled: values.Email_Enabled,
              Host: values.Email_Host,
              Port: values.Email_Port,
              Username: values.Email_Username,
              Password: values.Email_Password,
              From: values.Email_From,
              UseTLS: values.Email_UseTLS,
              AllowInsecureTLS: values.Email_AllowInsecureTLS,
              NotifyLogin: values.Email_NotifyLogin,
            },
            DockerConfig: {
              ...config.DockerConfig,
              SkipPruneNetwork: values.SkipPruneNetwork,
              SkipPruneImages: values.SkipPruneImages,
              DefaultDataPath: values.DefaultDataPath
            },
            HomepageConfig: {
              ...config.HomepageConfig,
              Background: values.Background,
              Expanded: values.Expanded
            },
            ThemeConfig: {
              ...config.ThemeConfig,
              PrimaryColor: values.PrimaryColor,
              SecondaryColor: values.SecondaryColor
            },
            Backup: {
              ...config.Backup,
              Backups: values.IncrBackupOutputDir ? {
                ...(config.Backup.Backups || {}),
                "Cosmos Internal Backup": {
                  ...(config.Backup.Backups ? config.Backup.Backups["Cosmos Internal Backup"] : {}),
                  "Crontab": "0 0 4 * * *",
                  "CrontabForget": "0 0 12 * * *",
                  "Source": status && status.ConfigFolder,
                  "Password": "",
                  "Name": "Cosmos Internal Backup",
                  Repository: values.IncrBackupOutputDir
                }
              } : (config.Backup.Backups || {})
            }
          }

          return API.config.set(toSave).then(() => {
            refresh(15); // fork addition: the server may be restarting its listener
            setSaveLabel(t('global.savedConfirmation'));
            setTimeout(() => {
              setSaveLabel(t('global.saveAction'));
            }, 3000);
          }).catch(() => {
            setSaveLabel(t('global.savedError'));
            setTimeout(() => {
              setSaveLabel(t('global.saveAction'));
            }, 3000);
          });
        }}
      >
        {(formik) => (
          <form noValidate onSubmit={formik.handleSubmit}>
            <PrettyTabbedView
              rootURL="/cosmos-ui/config-general"
              tabs={[
                {
                  title: t('mgmt.config.generalTitle'),
                  children: wrapTab(formik, <ConfigGeneral formik={formik} config={config} status={status} isAdmin={isAdmin} />),
                  url: "/general",
                },
                {
                  title: "HTTP",
                  children: wrapTab(formik, <ConfigHTTP formik={formik} />),
                  url: "/http-server",
                },
                {
                  title: t('mgmt.config.zones.title'),
                  children: wrapTab(formik, <ConfigHTTPS formik={formik} config={config} serverCertificate={serverCertificate} />),
                  url: "/https",
                },
                {
                  title: t('mgmt.config.appearanceTitle'),
                  children: wrapTab(formik, <ConfigAppearance formik={formik} />),
                  url: "/appearance",
                },
                {
                  title: t('mgmt.config.externalTitle'),
                  children: wrapTab(formik, <ConfigExternal formik={formik} />),
                  url: "/external",
                },
                {
                  title: "API",
                  children: <div style={{ maxWidth: "1000px", margin: "auto" }}>
                    <ConfigAPITokens />
                  </div>,
                  url: "/api",
                },
              ]}
            />
          </form>
        )}
      </Formik>
    </>}
  </div>;
}

export default ConfigManagement;
