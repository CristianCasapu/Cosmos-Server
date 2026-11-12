import * as React from 'react';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { decodeJwt, importSPKI, jwtVerify } from 'jose';
import {
  Alert,
  Button,
  Grid,
  IconButton,
  InputLabel,
  Stack,
  FormHelperText,
  TextField,
  MenuItem,
  Tooltip,
} from '@mui/material';
import { CopyOutlined, EyeInvisibleOutlined, EyeOutlined, WarningFilled } from '@ant-design/icons';
import { CosmosCheckbox, CosmosInputPassword, CosmosInputText, CosmosSelect } from './users/formShortcuts';
import { LoadingButton } from '@mui/lab';
import { useTranslation } from 'react-i18next';
import { FilePickerButton } from '../../components/filePicker';
import PermissionGuard from '../../components/permissionGuard';
import { PERM_ADMIN, PERM_CREDENTIALS_READ } from '../../utils/permissions';
import { useClientInfos } from '../../utils/hooks';
import EmailTest from './emailTest';
import ServerUpdate from '../../components/serverUpdate';

// License issuer's public key for signature verification
const LICENSE_PUBLIC_KEY = `-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE8QolLbFdVfU3XPkC01NwsS94bv1W
Ijy+/SYjyHfakFQm7JDhKpbNPC5oc+e4uM6Y9UyC0686toqpTYBSzbgaQw==
-----END PUBLIC KEY-----`;

const validateAndDecodeJWT = (token) => {
  try {
    const payload = decodeJwt(token);
    return { error: null, data: payload };
  } catch (error) {
    console.error('Failed to decode JWT:', error);
    return { error: 'parse', data: null };
  }
};

const verifyJWTSignature = async (token) => {
  if (typeof crypto === 'undefined' || !crypto.subtle) {
    // crypto.subtle unavailable (e.g. HTTP context), skip verification
    return true;
  }
  try {
    const publicKey = await importSPKI(LICENSE_PUBLIC_KEY, 'ES256');
    await jwtVerify(token, publicKey);
    return true;
  } catch (error) {
    console.error('Failed to verify JWT signature:', error);
    return false;
  }
};

const ConfigGeneral = ({ formik, config, status, isAdmin }) => {
  const { t } = useTranslation();
  const { hasPermission } = useClientInfos();
  const canReadCredentials = hasPermission(PERM_CREDENTIALS_READ);
  const [isCheckingUpdate, setIsCheckingUpdate] = React.useState(false);
  const [updateCheck, setUpdateCheck] = React.useState(null);
  const [licenseValidation, setLicenseValidation] = React.useState({ checked: false, valid: null });
  const [showInternalBackupPwd, setShowInternalBackupPwd] = React.useState(false);

  const rawInternalBackupPassword = config?.Backup?.Backups?.['Cosmos Internal Backup']?.Password;
  const internalBackupPasswordMasked = rawInternalBackupPassword === '***';
  const internalBackupPassword = internalBackupPasswordMasked ? '' : rawInternalBackupPassword;

  return (
    <Stack spacing={3}>
      <MainCard title={t('mgmt.config.generalTitle')}>
        <Grid container spacing={3}>
          <Grid item xs={12}>
            <Alert severity="info">{t('mgmt.config.general.configFileInfo')}</Alert>
          </Grid>

          {status && !status.containerized && <>
            {/* Fork addition: update status, "check" without installing, "update now" (upstream: one button that did both) */}
            <ServerUpdate />

            <CosmosCheckbox
              label={t('mgmt.config.general.autoupdates')}
              name="AutoUpdate"
              formik={formik}
            />

            {/* Fork addition */}
            <CosmosInputText
              label={t('mgmt.config.update.timeLabel', 'Time of the nightly maintenance and automatic update (server time)')}
              name="MaintenanceTime"
              type="time"
              formik={formik}
            />

            <CosmosCheckbox
              label={t('mgmt.config.general.betaupdate')}
              name="BetaUpdates"
              formik={formik}
            /></>}

          <CosmosCheckbox
            label={t('mgmt.config.general.forceMfaCheckbox.forceMfaLabel')}
            name="RequireMFA"
            formik={formik}
          />

          <Grid item xs={12}>
            <Stack spacing={1}>
              <InputLabel htmlFor="LoggingLevel-login">{t('mgmt.config.general.logLevelInput')}</InputLabel>
              <TextField
                className="px-2 my-2"
                variant="outlined"
                name="LoggingLevel"
                id="LoggingLevel"
                select
                value={formik.values.LoggingLevel}
                onChange={formik.handleChange}
                error={
                  formik.touched.LoggingLevel &&
                  Boolean(formik.errors.LoggingLevel)
                }
                helperText={
                  formik.touched.LoggingLevel && formik.errors.LoggingLevel
                }
              >
                <MenuItem key={"DEBUG"} value={"DEBUG"}>
                  DEBUG
                </MenuItem>
                <MenuItem key={"INFO"} value={"INFO"}>
                  INFO
                </MenuItem>
                <MenuItem key={"WARNING"} value={"WARNING"}>
                  WARNING
                </MenuItem>
                <MenuItem key={"ERROR"} value={"ERROR"}>
                  ERROR
                </MenuItem>
              </TextField>
            </Stack>
          </Grid>

          <CosmosCheckbox
            label={t('mgmt.config.general.monitoringCheckbox.monitoringLabel')}
            name="MonitoringEnabled"
            formik={formik}
          />
        </Grid>
      </MainCard>

      <MainCard title={t('mgmt.config.general.backupTitle')}>
        <Grid container spacing={3}>
          <Grid item xs={12}>
            <Alert severity="info">{t('mgmt.config.general.backupInfo2')}</Alert>
          </Grid>
          <Grid item xs={12}>
            <Stack direction={"row"} spacing={2} alignItems="flex-end">
                <FilePickerButton canCreate onPick={(path) => {
                  if(path)
                      formik.setFieldValue('BackupOutputDir', path);
                }} size="150%" select="folder" />
              <CosmosInputText
                label={t('mgmt.config.general.backupDirInput.backupDirLabel')}
                name="BackupOutputDir"
                formik={formik}
                helperText={t('mgmt.config.general.backupDirInput.backupDirHelperText')}
              />
            </Stack>
          </Grid>

          {/* Fork addition: when the configuration backup runs */}
          <CosmosSelect
            name="BackupFrequency"
            label={t('mgmt.config.backupSchedule.frequency', 'How often the configuration is backed up')}
            formik={formik}
            options={[
              ['2h', t('mgmt.config.backupSchedule.2h', 'Every two hours')],
              ['6h', t('mgmt.config.backupSchedule.6h', 'Every six hours')],
              ['12h', t('mgmt.config.backupSchedule.12h', 'Every twelve hours')],
              ['daily', t('mgmt.config.backupSchedule.daily', 'Once a day')],
              ['weekly', t('mgmt.config.backupSchedule.weekly', 'Once a week')],
            ]}
          />
          {formik.values.BackupFrequency === 'weekly' && <CosmosSelect
            name="BackupWeekday"
            label={t('mgmt.config.backupSchedule.weekday', 'Day of the weekly backup')}
            formik={formik}
            options={[1, 2, 3, 4, 5, 6, 0].map((day) => [day,
              new Date(2026, 10, 15 + day).toLocaleDateString(undefined, { weekday: 'long' })])}
          />}
          <CosmosInputText
            label={['daily', 'weekly'].includes(formik.values.BackupFrequency)
              ? t('mgmt.config.backupSchedule.timeAt', 'Time of the backup (server time)')
              : t('mgmt.config.backupSchedule.timeFrom', 'Counted from this time of day (server time)')}
            name="BackupTime"
            type="time"
            formik={formik}
          />

          <Grid item xs={12}>
            <Stack direction={"row"} spacing={2} alignItems="flex-end">
                {status && status.Licence && <FilePickerButton canCreate onPick={(path) => {
                  if(path)
                      formik.setFieldValue('IncrBackupOutputDir', path);
                }} size="150%" select="folder" />}
              <CosmosInputText
                label={t('mgmt.config.general.backupDirInput.incrBackupDirLabel')}
                name="IncrBackupOutputDir"
                disabled={!status || !status.Licence}
                formik={formik}
                helperText={t('mgmt.config.general.backupDirInput.backupDirHelperText')}
              />
            </Stack>
          </Grid>

          {isAdmin && canReadCredentials && internalBackupPassword && (
            <Grid item xs={12}>
              <Stack spacing={1}>
                <InputLabel htmlFor="internal-backup-password">
                  {t('mgmt.config.general.internalBackupPasswordLabel')}
                </InputLabel>
                <Stack direction="row" spacing={1} alignItems="center">
                  <TextField
                    id="internal-backup-password"
                    fullWidth
                    variant="outlined"
                    type={showInternalBackupPwd ? 'text' : 'password'}
                    value={internalBackupPassword}
                    InputProps={{ readOnly: true, style: { fontFamily: 'monospace' } }}
                  />
                  <Tooltip title={showInternalBackupPwd ? t('global.hide') : t('global.show')}>
                    <IconButton onClick={() => setShowInternalBackupPwd((s) => !s)}>
                      {showInternalBackupPwd ? <EyeInvisibleOutlined /> : <EyeOutlined />}
                    </IconButton>
                  </Tooltip>
                  <Tooltip title={t('global.copy')}>
                    <IconButton onClick={() => navigator.clipboard.writeText(internalBackupPassword)}>
                      <CopyOutlined />
                    </IconButton>
                  </Tooltip>
                </Stack>
                <FormHelperText>
                  {t('mgmt.config.general.internalBackupPasswordHelper')}
                </FormHelperText>
              </Stack>
            </Grid>
          )}
        </Grid>
      </MainCard>

      <MainCard title={t('mgmt.config.general.licenceInput.licenceLabel')}>
        <Grid container spacing={3}>
          <CosmosInputText
            label={t('mgmt.config.general.licenceInput.licenceLabel')}
            name="Licence"
            formik={formik}
            multiline
            autoComplete="off"
            inputProps={{ 'data-1p-ignore': true, 'data-lpignore': 'true', 'data-bwignore': true, style: { fontFamily: 'monospace' } }}
            onChange={(e) => {
              formik.setFieldValue("ServerToken", "");
            }}
          />
          {(() => {
            const isMasked = formik.values.Licence && /^\*+$/.test(formik.values.Licence);
            const licenseResult = React.useMemo(() => {
              if (!formik.values.Licence || isMasked) return null;
              return validateAndDecodeJWT(formik.values.Licence);
            }, [formik.values.Licence]);

            React.useEffect(() => {
              if (licenseResult && !licenseResult.error) {
                setLicenseValidation({ checked: false, valid: null });
                verifyJWTSignature(formik.values.Licence).then((isValid) => {
                  setLicenseValidation({ checked: true, valid: isValid });
                });
              }
            }, [formik.values.Licence]);

            if (!licenseResult) return null;

            if (licenseResult.error === 'parse') {
              return (
                <Grid item xs={12}>
                  <Alert severity="error">
                    {t('mgmt.config.general.licenceInput.licenseInvalid')}
                  </Alert>
                </Grid>
              );
            }

            if (licenseValidation.checked && !licenseValidation.valid) {
              return (
                <Grid item xs={12}>
                  <Alert severity="error">
                    {t('mgmt.config.general.licenceInput.licenseSignatureInvalid')}
                  </Alert>
                </Grid>
              );
            }

            const licenseData = licenseResult.data;
            const isExpired = licenseData.lifetime === false &&
              licenseData.expiresAt &&
              new Date(licenseData.expiresAt * 1000) < new Date();

            return (
              <>
                {licenseData.email && (
                  <Grid item xs={12}>
                    <Alert severity="info">
                      {t('mgmt.config.general.licenceInput.licenseEmail', { email: licenseData.email })} <br/>
                      {t('mgmt.config.general.licenceInput.licenseType', { type: licenseData.subscriptionType })}
                    </Alert>
                  </Grid>
                )}

                {licenseData.lifetime === false && isExpired && (
                  <Grid item xs={12}>
                    <Alert severity="warning" icon={<WarningFilled />}>
                      {t('mgmt.config.general.licenceInput.licenseExpired', {
                        date: new Date(licenseData.expiresAt * 1000).toLocaleDateString()
                      })}
                    </Alert>
                  </Grid>
                )}

                {licenseData.lifetime === false && (
                  <Grid item xs={12}>
                    <Stack spacing={1}>
                      <Button
                        href='https://billing.stripe.com/p/login/28obMlc7X1jgeqY144'
                        target="_blank"
                        variant="outlined">
                        {t('mgmt.config.general.licenceInput.manageLicenceButton')}
                      </Button>
                    </Stack>
                  </Grid>
                )}
              </>
            );
          })()}
        </Grid>
      </MainCard>

      <MainCard title="Emails - SMTP">
        <Stack spacing={2}>
          <Alert severity="info">{t('mgmt.config.email.inbobox.label')}.</Alert>

          <CosmosCheckbox
            label={t('mgmt.config.email.enableCheckbox.enableLabel')}
            name="Email_Enabled"
            formik={formik}
          />

          {formik.values.Email_Enabled && (<>
            <CosmosInputText
              label="SMTP Host"
              name="Email_Host"
              formik={formik}
              helperText="SMTP Host"
            />

            <CosmosInputText
              label="SMTP Port"
              name="Email_Port"
              formik={formik}
              helperText="SMTP Port"
            />

            <CosmosInputText
              label={t('mgmt.config.email.usernameInput.usernameLabel')}
              name="Email_Username"
              formik={formik}
              helperText={t('mgmt.config.email.usernameInput.usernameHelperText')}
            />

            <CosmosInputPassword
              label={t('mgmt.config.email.passwordInput.passwordLabel')}
              name="Email_Password"
              autoComplete='new-password'
              formik={formik}
              helperText={t('mgmt.config.email.passwordInput.passwordHelperText')}
              noStrength
            />

            <CosmosInputText
              label={t('mgmt.config.email.senderInput.senderLabel')}
              name="Email_From"
              formik={formik}
              helperText={t('mgmt.config.email.senderInput.senderHelperText')}
            />

            <CosmosCheckbox
              label={t('mgmt.config.email.tlsCheckbox.tlsLabel')}
              name="Email_UseTLS"
              formik={formik}
            />

            {formik.values.Email_UseTLS && (
              <CosmosCheckbox
                label={t('mgmt.config.email.selfSignedCheckbox.SelfSignedLabel')}
                name="Email_AllowInsecureTLS"
                formik={formik}
              />
            )}

            <CosmosCheckbox
              label={t('mgmt.config.email.notifyLoginCheckbox.notifyLoginLabel')}
              name="Email_NotifyLogin"
              formik={formik}
            />

            <EmailTest formik={formik} />
          </>)}
        </Stack>
      </MainCard>
    </Stack>
  );
};

export default ConfigGeneral;
