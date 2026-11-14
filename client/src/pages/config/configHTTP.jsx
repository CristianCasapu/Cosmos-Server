import * as React from 'react';
import MainCard from '../../components/MainCard';
import {
  Alert,
  Button,
  Grid,
  InputLabel,
  OutlinedInput,
  Stack,
  FormHelperText,
  Tooltip,
} from '@mui/material';
import { QuestionCircleOutlined } from '@ant-design/icons';
import { CosmosCheckbox, CosmosFormDivider, CosmosInputText } from './users/formShortcuts';
import CountrySelect from '../../components/countrySelect';
import { isDomain } from '../../utils/indexs';
import { Trans, useTranslation } from 'react-i18next';
import InternetAccess from './internetAccess';
import { PaidOnly } from '../../components/paidVisibility'; // Fork addition
import { ownCountries, safeDefaultBlocked } from '../../utils/geoDefaults'; // Fork addition
import { PanelReach } from '../../components/reachability'; // Fork addition
import { LocalNamesSettings } from '../../components/localNames'; // Fork addition
import { useHttpCheck, ForkHttpBeforeSave, ForkHostnameHint, ForkHttpPorts, ForkAdminWhitelist, ForkGeoOwnCountry } from './forkHttpSettings'; // Fork addition

const ConfigHTTP = ({ formik }) => {
  const [geoLeftOut, setGeoLeftOut] = React.useState(null); // Fork addition: what the default country list left out
  const { t } = useTranslation();
  const check = useHttpCheck(formik); // Fork addition: the ports and the admin whitelist as the server sees them, see forkHttpSettings.jsx

  return (
    <Stack spacing={3}>
      <ForkHttpBeforeSave formik={formik} check={check} />{/* Fork addition: what saving this tab will do */}
      <MainCard title="HTTP">
        <Grid container spacing={3}>
          <Grid item xs={12}>
            <Stack spacing={1}>
              <InputLabel htmlFor="Hostname-login">{t('mgmt.config.http.hostnameInput.HostnameLabel')}</InputLabel>
              <OutlinedInput
                id="Hostname-login"
                type="text"
                value={formik.values.Hostname}
                name="Hostname"
                onBlur={formik.handleBlur}
                onChange={formik.handleChange}
                placeholder="Hostname"
                fullWidth
                error={Boolean(formik.touched.Hostname && formik.errors.Hostname)}
              />
              {formik.touched.Hostname && formik.errors.Hostname && (
                <FormHelperText error id="standard-weight-helper-text-Hostname-login">
                  {formik.errors.Hostname}
                </FormHelperText>
              )}
              <ForkHostnameHint formik={formik} />{/* Fork addition: what to enter, and the name this page is open on */}
              <PanelReach />{/* Fork addition: the saved address as the internet sees it, see components/reachability.jsx */}
            </Stack>
          </Grid>

          {(formik.values.HTTPSCertificateMode != "DISABLED" || isDomain(formik.values.Hostname)) ? (
          <Grid item xs={12}>
              <CosmosCheckbox
                label={<span>{t('mgmt.config.http.allowInsecureLocalAccessCheckbox.allowInsecureLocalAccessLabel')} &nbsp;
                  <Tooltip title={<span style={{fontSize:'110%'}}><Trans i18nKey="mgmt.config.http.allowInsecureLocalAccessCheckbox.allowInsecureLocalAccessTooltip" /></span>}>
                        <QuestionCircleOutlined size={'large'} />
                    </Tooltip></span>}
                name="AllowHTTPLocalIPAccess"
                formik={formik}
              />
              {formik.values.AllowHTTPLocalIPAccess /* Fork addition: was allowHTTPLocalIPAccess, so the warning never showed */ && <Alert severity="warning"><Trans i18nKey="mgmt.config.http.allowInsecureLocalAccessCheckbox.allowInsecureLocalAccessWarning" /></Alert>}
          </Grid>) : ""}

          {/* Fork addition: the two ports as one choice, checked against this server (forkHttpSettings.jsx); upstream had two free text fields */}
          <ForkHttpPorts formik={formik} check={check} />

          <Grid item xs={12}>
            <Alert severity="info">
              {t('mgmt.config.http.allowSearchIndexCheckbox')}<br />
            </Alert>
            <CosmosCheckbox
              label={t('mgmt.config.http.allowSearchIndexCheckbox.allowSearchIndexLabel')}
              name="AllowSearchEngine"
              formik={formik}
            />
          </Grid>
          <Grid item xs={12}>
            <Alert severity="info">
              {t('mgmt.config.http.publishMDNSCheckbox')}
            </Alert>
            <CosmosCheckbox
              label="Publish .local domains on your local network with mDNS"
              name="PublishMDNS"
              formik={formik}
            />
            <LocalNamesSettings formik={formik} />{/* Fork addition: are the names published and answered, which devices can use them */}
          </Grid>
        </Grid>
      </MainCard>

      <InternetAccess formik={formik} />

      <MainCard title={t('global.securityTitle')}>
        <Grid container spacing={3}>
          <CosmosInputText
            label={t('mgmt.config.security.trustedProxiesInput.trustedProxiesLabel')}
            name="TrustedProxies"
            formik={formik}
            helperText={t('mgmt.config.security.trustedProxiesInput.trustedProxiesHelperText')}
          />

          <CosmosFormDivider title='Geo-Blocking' />

          <CosmosCheckbox
            label={t('mgmt.config.security.invertBlacklistCheckbox.invertBlacklistLabel')}
            name="CountryBlacklistIsWhitelist"
            formik={formik}
          />

          <Grid item xs={12}>
              <InputLabel htmlFor="GeoBlocking">
                {t('mgmt.config.security.geoBlockSelection.geoBlockLabel', {blockAllow: formik.values.CountryBlacklistIsWhitelist ? t('mgmt.config.security.geoBlockSelection.geoBlockLabel.varAllow') : t('mgmt.config.security.geoBlockSelection.geoBlockLabel.varBlock')
                  })}
              </InputLabel>
          </Grid>

          <CountrySelect name="GeoBlocking" label={t('mgmt.config.security.geoBlockSelection', { blockAllow: formik.values.CountryBlacklistIsWhitelist ? t('mgmt.config.security.geoBlockSelection.varAllow') : t('mgmt.config.security.geoBlockSelection.varBlock') })} formik={formik} />
          <ForkGeoOwnCountry formik={formik} />{/* Fork addition: the country the admin is in, refused by the list */}

          <Grid item xs={12}>
            <Button onClick={() => {
              // Fork addition: the default list without the country the admin or the server is in (utils/geoDefaults.js); upstream's list blocks sixteen countries, the admin's own included
              const { list, leftOut } = safeDefaultBlocked(ownCountries({
                serverCountry: formik.values.ServerCountry,
                zones: [Intl.DateTimeFormat().resolvedOptions().timeZone, formik.values.Timezone],
                languages: navigator.languages,
              }));
              formik.setFieldValue("GeoBlocking", list)
              formik.setFieldValue("CountryBlacklistIsWhitelist", false)
              setGeoLeftOut(leftOut);
            }} variant="outlined">{t('mgmt.config.security.geoblock.resetToDefaultButton')}</Button>
            {/* Fork addition: say which country was left out, and that the list is the admin's to check */}
            {geoLeftOut && <FormHelperText>
              {geoLeftOut.length > 0
                ? t('mgmt.config.security.geoblock.leftOut', '{{countries}} was left off the list: you or this server seem to be there, and blocking it would lock you out. Check that no other country you connect from is on the list before you save.', { countries: geoLeftOut.join(', ') })
                : t('mgmt.config.security.geoblock.checkOwn', 'Check that no country you connect from is on the list before you save: visitors from a listed country are refused, you included.')}
            </FormHelperText>}
          </Grid>

          <CosmosFormDivider title={t('mgmt.config.security.adminRestrictionsTitle')} />

          <Grid item xs={12}>
            <Alert severity="info">{t('mgmt.config.security.adminRestrictions.adminRestrictionsInfo')}</Alert>
          </Grid>

          {/* Fork addition: a list with the address in use proposed and a lock-out said before saving (forkHttpSettings.jsx); upstream had a free text field */}
          <ForkAdminWhitelist formik={formik} check={check} />

          <PaidOnly keep={formik.initialValues.AdminConstellationOnly}>{/* Fork addition: needs Constellation, hence a licence */}
          <CosmosCheckbox
            label={t('mgmt.config.security.adminRestrictions.adminConstellationCheckbox.adminConstellationLabel')}
            name="AdminConstellationOnly"
            formik={formik}
          />
          </PaidOnly>
        </Grid>
      </MainCard>
    </Stack>
  );
};

export default ConfigHTTP;
