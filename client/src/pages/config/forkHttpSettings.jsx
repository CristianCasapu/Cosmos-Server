// Fork addition: the HTTP settings, guided (Configuration > HTTP). Hooked into
// upstream's configHTTP.jsx in place of the two port fields and the admin
// whitelist, and with a "what saving does" note at the top of the tab. The
// logic is in utils/httpSettings.js; the server refuses the same mistakes on
// save (src/configapi/fork_http_settings.go) and answers the check this form
// shows (POST /api/config/http-check: the address you are on, the server's
// addresses, whether the two ports are free; nothing saved, nothing contacted).
import * as React from 'react';
import * as Yup from 'yup';
import { Alert, Button, Chip, FormHelperText, Grid, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import { ChoiceRow } from '../../components/formHelpers';
import { FormField } from '../../components/formField';
import ProposalBox from '../../components/proposalBox';
import {
  portChoice, portsOf, portProblems, portStateText, saveEffects, cleanList, joinList,
  isAddressEntry, isProxyEntry, invalidEntries, whitelistProposals, locksOut,
  hostnameProblem, ownCountriesRefused, portNumber, proxyProposals, cloudflareProxyNote,
} from '../../utils/httpSettings';
import { ownCountries } from '../../utils/geoDefaults';

const tr = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');

const httpsOn = (values) => values.HTTPSCertificateMode && values.HTTPSCertificateMode !== 'DISABLED';

const snapshot = (values) => ({
  hostname: values.Hostname,
  httpPort: values.HTTPPort,
  httpsPort: values.HTTPSPort,
  https: httpsOn(values),
  whitelist: cleanList(values.AdminWhitelistIPs),
  constellationOnly: Boolean(values.AdminConstellationOnly),
});

// Asks the server what the ports and the whitelist being edited would do, when
// the tab opens and a moment after one of them changed. The check is local to
// the server: it contacts nothing.
export const useHttpCheck = (formik) => {
  const [check, setCheck] = React.useState(null);
  const { HTTPPort, HTTPSPort, AdminWhitelistIPs, AdminConstellationOnly } = formik.values;
  React.useEffect(() => {
    let gone = false;
    const timer = setTimeout(() => {
      API.config.httpCheck({
        httpPort: String(HTTPPort || ''),
        httpsPort: String(HTTPSPort || ''),
        adminWhitelist: cleanList(AdminWhitelistIPs).slice(0, 64),
        constellationOnly: Boolean(AdminConstellationOnly),
      }).then((res) => { if (!gone) setCheck(res.data); }).catch(() => { if (!gone) setCheck(null); });
    }, check ? 700 : 0);
    return () => { gone = true; clearTimeout(timer); };
  }, [HTTPPort, HTTPSPort, AdminWhitelistIPs, AdminConstellationOnly]);
  return check;
};

// Rules for the settings form (pages/config/index.jsx): only a value that
// changed is judged, so an old odd value never blocks saving something else.
export const httpSettingsValidation = (t, config) => {
  const saved = (config && config.HTTPConfig) || {};
  const unchanged = (value, stored) => String(value === undefined || value === null ? '' : value).trim() === String(stored || '').trim();
  const savedList = (list) => joinList(list || []);
  return {
    // upstream's rule (required, at most 255) and the server's syntax
    Hostname: Yup.string().max(255).required(t('mgmt.config.http.hostnameInput.HostnameValidation')).test('fork-hostname', function (value) {
      if (unchanged(value, saved.Hostname)) return true;
      const problem = hostnameProblem(value);
      return problem ? this.createError({ message: tr(t, problem) }) : true;
    }),
    HTTPPort: Yup.string().test('fork-port', function (value) {
      if (unchanged(value, saved.HTTPPort) && unchanged(this.parent.HTTPSPort, saved.HTTPSPort)) return true;
      const problem = portProblems(value, this.parent.HTTPSPort).http || (String(value || '').trim() === '' && ['mgmt.config.http.fork.portMissing', 'Enter a port, for example 80']);
      return problem ? this.createError({ message: tr(t, problem) }) : true;
    }),
    HTTPSPort: Yup.string().test('fork-port', function (value) {
      if (unchanged(value, saved.HTTPSPort) && unchanged(this.parent.HTTPPort, saved.HTTPPort)) return true;
      const problem = portProblems(this.parent.HTTPPort, value).https || (String(value || '').trim() === '' && ['mgmt.config.http.fork.portMissingHttps', 'Enter a port, for example 443']);
      return problem ? this.createError({ message: tr(t, problem) }) : true;
    }),
    AdminWhitelistIPs: Yup.string().nullable().test('fork-list', function (value) {
      if (joinList(value) === savedList(config && config.AdminWhitelistIPs)) return true;
      const bad = invalidEntries(value, isAddressEntry);
      return bad.length ? this.createError({ message: t('mgmt.config.http.fork.listInvalid', 'Not an address or a range: {{entries}}. For example 192.168.1.20 or 192.168.1.0/24', { entries: bad.join(', ') }) }) : true;
    }),
    TrustedProxies: Yup.string().nullable().test('fork-proxies', function (value) {
      if (joinList(value) === savedList(saved.TrustedProxies)) return true;
      const bad = invalidEntries(value, isProxyEntry);
      return bad.length ? this.createError({ message: t('mgmt.config.http.fork.proxiesInvalid', 'Not an address, a range or a URL: {{entries}}. For example 172.17.0.1, 10.0.0.0/8 or https://www.cloudflare.com/ips-v4', { entries: bad.join(', ') }) }) : true;
    }),
  };
};

// What saving this tab will do, said before Save is pressed.
export const ForkHttpBeforeSave = ({ formik, check }) => {
  const { t } = useTranslation();
  const before = snapshot(formik.initialValues);
  const after = snapshot(formik.values);
  const proxiesChanged = joinList(formik.values.TrustedProxies) !== joinList(formik.initialValues.TrustedProxies);
  const changed = JSON.stringify(before) !== JSON.stringify(after) || proxiesChanged;
  if (!changed) return null;
  const effects = saveEffects(before, after, check);
  const worst = effects.some((e) => e.severity === 'error') ? 'error' : effects.some((e) => e.severity === 'warning') ? 'warning' : 'info';
  return <Alert severity={worst} sx={{ '& .MuiAlert-message': { minWidth: 0, overflowWrap: 'anywhere' } }}>
    <Typography variant="subtitle1" component="div" sx={{ fontWeight: 600 }}>{t('mgmt.config.http.fork.beforeSave', 'When you save these HTTP settings')}</Typography>
    <ul style={{ margin: '4px 0 0', paddingLeft: '20px' }}>
      {effects.map((effect) => <li key={effect.text[0]}>{tr(t, effect.text)}</li>)}
    </ul>
  </Alert>;
};

// The server's address: what is wrong with it, and the name this page is open on.
export const ForkHostnameHint = ({ formik }) => {
  const { t } = useTranslation();
  const value = String(formik.values.Hostname || '').trim();
  const page = typeof window !== 'undefined' ? window.location.hostname : '';
  const pagePort = typeof window !== 'undefined' ? window.location.port : '';
  const pageHost = page && pagePort ? page + ':' + pagePort : page;
  const problem = value !== String(formik.initialValues.Hostname || '').trim() ? hostnameProblem(value) : null;
  const differs = page && value && value.toLowerCase() !== page.toLowerCase() && value.toLowerCase() !== pageHost.toLowerCase() && !hostnameProblem(page);
  return <>
    {!problem && <FormHelperText>{t('mgmt.config.http.fork.hostnameHelp', 'The name or IP address the panel is opened with, without http://, for example cosmos.example.com or 192.168.1.10. The apps\' addresses are built on it too.')}</FormHelperText>}
    {differs && <ProposalBox
      title={t('mgmt.config.http.fork.pageHostTitle', 'This page is open on {{name}}', { name: page })}
      source={t('mgmt.config.http.fork.pageHostSource', 'The setting holds {{saved}}. Both work while they lead to this server; the setting is the one Cosmos gives out in links and certificates.', { saved: value })}
      onUse={() => formik.setFieldValue('Hostname', page)}
      useLabel={t('mgmt.config.http.fork.useName', 'Use {{name}}', { name: page })}
    />}
  </>;
};

const PortFinding = ({ finding, changed }) => {
  const { t } = useTranslation();
  const said = changed && portStateText(finding);
  if (!said) return null;
  return <FormHelperText error={said.severity === 'error'} sx={{ overflowWrap: 'anywhere' }}>{tr(t, said.text)}</FormHelperText>;
};

// The two ports as one choice, with what the check found for a changed one.
export const ForkHttpPorts = ({ formik, check }) => {
  const { t } = useTranslation();
  const { HTTPPort, HTTPSPort } = formik.values;
  const [other, setOther] = React.useState(portChoice(HTTPPort, HTTPSPort) === 'other');
  const choice = other ? 'other' : portChoice(HTTPPort, HTTPSPort);
  const problems = portProblems(HTTPPort, HTTPSPort);
  const ports = (check && check.ports) || [];
  const changedHttp = String(HTTPPort || '').trim() !== String(formik.initialValues.HTTPPort || '').trim();
  const changedHttps = String(HTTPSPort || '').trim() !== String(formik.initialValues.HTTPSPort || '').trim();
  const sameAsCheck = (finding, value) => finding && finding.port === portNumber(value);
  const unusual = choice !== 'standard';
  const error = (name, local) => (formik.touched[name] && formik.errors[name]) || (local && tr(t, local));

  return <ChoiceRow
    name="fork-http-ports"
    label={t('mgmt.config.http.fork.portsLabel', 'Ports of the web server')}
    value={choice}
    onChange={(value) => {
      if (value === 'other') {
        setOther(true);
        return;
      }
      setOther(false);
      const pair = portsOf(value);
      formik.setValues({ ...formik.values, HTTPPort: pair.http, HTTPSPort: pair.https });
    }}
    options={[
      { value: 'standard', label: t('mgmt.config.http.fork.portsStandard', '80 and 443 (usual)'), hint: t('mgmt.config.http.fork.portsStandardHint', 'The usual ports: addresses need no port number, and certificates from Let\'s Encrypt work without anything more. Keep these unless another program on this server needs them.') },
      { value: 'alternate', label: t('mgmt.config.http.fork.portsAlternate', '8080 and 8443'), hint: t('mgmt.config.http.fork.portsAlternateHint', 'When 80 and 443 belong to another program on this server (another web server or proxy). Addresses then carry the port, for example https://cosmos.example.com:8443.') },
      { value: 'other', label: t('mgmt.config.http.fork.portsOther', 'Other ports'), hint: t('mgmt.config.http.fork.portsOtherHint', 'Two ports of your choice, from 1 to 65535.') },
    ]}
  >
    {choice === 'other' && <Grid container spacing={2}>
      <Grid size={{ xs: 12, sm: 6 }}>
        <FormField
          id="HTTPPort-login"
          label={t('mgmt.config.http.fork.httpPort', 'HTTP port')}
          value={HTTPPort}
          inputMode="numeric"
          onChange={(text) => formik.setFieldValue('HTTPPort', text)}
          onBlur={() => formik.setFieldTouched('HTTPPort', true, false)}
          error={error('HTTPPort', problems.http)}
          helperText={t('mgmt.config.http.fork.httpPortHelp', 'Plain HTTP: it sends visitors on to HTTPS. Usually 80.')}
        />
        <PortFinding finding={ports[0]} changed={changedHttp && sameAsCheck(ports[0], HTTPPort)} />
      </Grid>
      <Grid size={{ xs: 12, sm: 6 }}>
        <FormField
          id="HTTPSPort-login"
          label={t('mgmt.config.http.fork.httpsPort', 'HTTPS port')}
          value={HTTPSPort}
          inputMode="numeric"
          onChange={(text) => formik.setFieldValue('HTTPSPort', text)}
          onBlur={() => formik.setFieldTouched('HTTPSPort', true, false)}
          error={error('HTTPSPort', problems.https)}
          helperText={t('mgmt.config.http.fork.httpsPortHelp', 'The encrypted address of the panel and the apps. Usually 443.')}
        />
        <PortFinding finding={ports[1]} changed={changedHttps && sameAsCheck(ports[1], HTTPSPort)} />
      </Grid>
    </Grid>}
    {choice !== 'other' && <>
      <PortFinding finding={ports[0]} changed={changedHttp && sameAsCheck(ports[0], HTTPPort)} />
      <PortFinding finding={ports[1]} changed={changedHttps && sameAsCheck(ports[1], HTTPSPort)} />
      {!changedHttp && !changedHttps && formik.errors.HTTPPort && <FormHelperText error>{formik.errors.HTTPPort}</FormHelperText>}
    </>}
    {unusual && <FormHelperText>{t('mgmt.config.http.fork.portsUnusual', 'From the internet, Let\'s Encrypt checks a domain on port 80 and browsers expect 443: forward those two from your router to these ports, or get certificates through DNS (Configuration > Domains).')}</FormHelperText>}
  </ChoiceRow>;
};

// The admin whitelist as a list of entries, with the address in use and its
// network proposed, and a refusal said before the server makes it.
export const ForkAdminWhitelist = ({ formik, check }) => {
  const { t } = useTranslation();
  const [typed, setTyped] = React.useState('');
  const list = cleanList(formik.values.AdminWhitelistIPs);
  const set = (next) => {
    formik.setFieldTouched('AdminWhitelistIPs', true, false);
    formik.setFieldValue('AdminWhitelistIPs', joinList(next));
  };
  const add = (entry) => {
    const value = String(entry || '').trim();
    if (!value || list.includes(value)) return;
    set([...list, value]);
  };
  const typedProblem = typed.trim() && !isAddressEntry(typed) ? t('mgmt.config.http.fork.entryInvalid', 'Enter an address such as 192.168.1.20 or a range such as 192.168.1.0/24') : '';
  const proposals = whitelistProposals(check, list);
  const changed = joinList(formik.values.AdminWhitelistIPs) !== joinList(formik.initialValues.AdminWhitelistIPs)
    || Boolean(formik.values.AdminConstellationOnly) !== Boolean(formik.initialValues.AdminConstellationOnly);
  const you = check && check.you && check.you.address;

  return <Grid size={{ xs: 12 }}>
    <Stack spacing={1}>
      <Typography variant="subtitle1" component="div">{t('mgmt.config.http.fork.whitelistLabel', 'Addresses administrators may use')}</Typography>
      {list.length === 0
        ? <FormHelperText>{t('mgmt.config.http.fork.whitelistEmpty', 'No list: administrators can work from any address. With a list, admin actions from any other address are refused, signing in included.')}</FormHelperText>
        : <Stack direction="row" useFlexGap sx={{ flexWrap: 'wrap' }} spacing={1}>
          {list.map((entry) => <Chip
            key={entry}
            label={entry}
            color={isAddressEntry(entry) ? 'default' : 'error'}
            variant="outlined"
            onDelete={() => set(list.filter((e) => e !== entry))}
            sx={{ maxWidth: '100%' }}
          />)}
        </Stack>}
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { xs: 'stretch', sm: 'flex-start' } }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <FormField
            id="fork-whitelist-add"
            value={typed}
            placeholder="192.168.1.0/24"
            onChange={setTyped}
            onEnter={() => { if (!typedProblem) { add(typed); setTyped(''); } }}
            error={typedProblem}
            helperText={t('mgmt.config.http.fork.whitelistAddHelp', 'An address or a range, for example 192.168.1.20 or 192.168.1.0/24, then Add.')}
          />
        </div>
        <Button variant="outlined" disabled={!typed.trim() || Boolean(typedProblem)} onClick={() => { add(typed); setTyped(''); }} sx={{ minHeight: 41 }}>
          {t('mgmt.config.http.fork.add', 'Add')}
        </Button>
      </Stack>
      {proposals.length > 0 && <Stack spacing={1}>
        {proposals.map((proposal) => <Stack key={proposal.value} direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap' }} useFlexGap>
          <Button size="small" variant="outlined" onClick={() => add(proposal.value)}>{t('mgmt.config.http.fork.addValue', 'Add {{value}}', { value: proposal.value })}</Button>
          <FormHelperText sx={{ m: 0, overflowWrap: 'anywhere' }}>{tr(t, proposal.source)}</FormHelperText>
        </Stack>)}
      </Stack>}
      {formik.touched.AdminWhitelistIPs && formik.errors.AdminWhitelistIPs && <FormHelperText error>{formik.errors.AdminWhitelistIPs}</FormHelperText>}
      {changed && locksOut(check) && <Alert severity="error" sx={{ '& .MuiAlert-message': { minWidth: 0, overflowWrap: 'anywhere' } }}>
        {t('mgmt.config.http.fork.lockOut', 'This list would lock you out: you are connected from {{address}}, which it does not allow. Saving is refused until it does: add the address or its network.', { address: you || '?' })}
      </Alert>}
      {list.length > 0 && check && check.whitelist && check.whitelist.passes && you && <FormHelperText>
        {t('mgmt.config.http.fork.youPass', 'You are connected from {{address}}: the list allows it.', { address: you })}
      </FormHelperText>}
    </Stack>
  </Grid>;
};

// Geo-blocking: the countries the admin seems to be in that the list would refuse:
// the one of the address the server sees first, then the browser's hints.
export const ForkGeoOwnCountry = ({ formik, check }) => {
  const { t } = useTranslation();
  const seen = check && check.seen && check.seen.country;
  const local = check && check.you && check.you.local;
  const own = ownCountries({
    seen,
    serverCountry: formik.values.ServerCountry,
    zones: [Intl.DateTimeFormat().resolvedOptions().timeZone, formik.values.Timezone],
    languages: typeof navigator !== 'undefined' ? navigator.languages : [],
  });
  const refused = ownCountriesRefused(formik.values.GeoBlocking, formik.values.CountryBlacklistIsWhitelist, own);
  const seenRefused = seen && refused.includes(String(seen).toUpperCase());
  const hasList = (formik.values.GeoBlocking || []).length > 0;
  return <>
    {refused.length > 0 && <Grid size={{ xs: 12 }}>
      <Alert severity={seenRefused ? 'error' : 'warning'} sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>
        {seenRefused
          ? t('mgmt.config.http.fork.geoOwnSeen', 'Visitors from {{country}} would be refused, and you are there: the address the server sees you at ({{address}}) is in {{country}}, according to the location database on this server. Saving this list locks you out of every app and of the panel from where you are now.', { country: seen, address: check.you.address })
          : t('mgmt.config.http.fork.geoOwn', 'Visitors from {{countries}} would be refused, and you or this server seem to be there (from the time zone and the languages of this browser). From the internet you would be locked out too.', { countries: refused.join(', ') })}
      </Alert>
    </Grid>}
    {hasList && local && <Grid size={{ xs: 12 }}>
      <FormHelperText>{t('mgmt.config.http.fork.geoLocal', 'You are on the local network ({{address}}): geo-blocking never applies there, so this list cannot lock you out at home. It does apply when you connect from the internet.', { address: check.you.address })}</FormHelperText>
    </Grid>}
  </>;
};

// Trusted proxies as a list of entries, with the proxy in front of this
// request, the reverse-proxy containers and Cloudflare proposed.
export const ForkTrustedProxies = ({ formik, check }) => {
  const { t } = useTranslation();
  const [typed, setTyped] = React.useState('');
  const list = cleanList(formik.values.TrustedProxies);
  const set = (next) => {
    formik.setFieldTouched('TrustedProxies', true, false);
    formik.setFieldValue('TrustedProxies', joinList(next));
  };
  const add = (entries) => {
    const fresh = (Array.isArray(entries) ? entries : [entries]).map((e) => String(e || '').trim()).filter((e) => e && !list.includes(e));
    if (fresh.length) set([...list, ...fresh]);
  };
  const typedProblem = typed.trim() && !isProxyEntry(typed) ? t('mgmt.config.http.fork.proxyEntryInvalid', 'Enter an address such as 172.17.0.1, a range such as 10.0.0.0/8, or the address of a list such as https://www.cloudflare.com/ips-v4') : '';
  const proposals = proxyProposals(check, list);
  const cloudflare = cloudflareProxyNote(check, list);
  const seen = check && check.seen;

  return <Grid size={{ xs: 12 }}>
    <Stack spacing={1}>
      <Typography variant="subtitle1" component="div">{t('mgmt.config.http.fork.proxiesLabel', 'Trusted proxies')}</Typography>
      <FormHelperText sx={{ m: 0 }}>{t('mgmt.config.http.fork.proxiesHelp', 'Programs that pass visitors on to Cosmos (a reverse proxy, a tunnel). Cosmos believes them about the visitor\'s real address; anyone else saying so is ignored. Leave the list empty when visitors reach Cosmos directly.')}</FormHelperText>
      {list.length > 0 && <Stack direction="row" useFlexGap sx={{ flexWrap: 'wrap' }} spacing={1}>
        {list.map((entry) => <Chip
          key={entry}
          label={entry}
          color={isProxyEntry(entry) ? 'default' : 'error'}
          variant="outlined"
          onDelete={() => set(list.filter((e) => e !== entry))}
          sx={{ maxWidth: '100%' }}
        />)}
      </Stack>}
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { xs: 'stretch', sm: 'flex-start' } }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <FormField
            id="fork-proxies-add"
            value={typed}
            placeholder="172.17.0.1"
            onChange={setTyped}
            onEnter={() => { if (!typedProblem) { add(typed); setTyped(''); } }}
            error={typedProblem}
            helperText={t('mgmt.config.http.fork.proxiesAddHelp', 'An address, a range, or the address of a published list, then Add.')}
          />
        </div>
        <Button variant="outlined" disabled={!typed.trim() || Boolean(typedProblem)} onClick={() => { add(typed); setTyped(''); }} sx={{ minHeight: 41 }}>
          {t('mgmt.config.http.fork.add', 'Add')}
        </Button>
      </Stack>
      {proposals.map((proposal) => <Stack key={proposal.value} direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap' }} useFlexGap>
        <Button size="small" variant="outlined" onClick={() => add(proposal.value)}>{t('mgmt.config.http.fork.addValue', 'Add {{value}}', { value: proposal.value })}</Button>
        <FormHelperText sx={{ m: 0, overflowWrap: 'anywhere', flex: '1 1 240px' }}>{tr(t, proposal.source)}</FormHelperText>
      </Stack>)}
      {seen && seen.via && seen.viaTrusted && <FormHelperText>{t('mgmt.config.http.fork.proxyTrusted', 'Your request came through {{ip}}, which is trusted: Cosmos sees you at {{address}}.', { ip: seen.via, address: check.you.address })}</FormHelperText>}
      {cloudflare && <Alert severity="info" sx={{ '& .MuiAlert-message': { minWidth: 0, overflowWrap: 'anywhere' } }}>
        <Stack spacing={1} sx={{ alignItems: 'flex-start' }}>
          <span>{cloudflare.trusted
            ? t('mgmt.config.http.fork.cloudflareTrusted', 'A domain of this server is at Cloudflare, and Cosmos already believes Cloudflare\'s addresses for the apps served through Cloudflare (Domains > Cloudflare). They need not be listed here. Listing them here makes Cosmos believe them for every name, also those served directly; any Cloudflare customer can send traffic from those addresses.')
            : t('mgmt.config.http.fork.cloudflareUntrusted', 'A domain of this server is at Cloudflare. If visitors reach apps through Cloudflare\'s proxy, switch on "Serve through Cloudflare" for those apps (Domains > Cloudflare): Cosmos then believes Cloudflare only for them. Listing Cloudflare\'s ranges here makes Cosmos believe them for every name.')}</span>
          {cloudflare.listed
            ? <span>{t('mgmt.config.http.fork.cloudflareListed', 'Cloudflare\'s {{count}} ranges are in the list.', { count: cloudflare.ranges.length })}</span>
            : cloudflare.missing.length > 0 && <Button size="small" variant="outlined" color="inherit" onClick={() => add(cloudflare.missing)}>
              {t('mgmt.config.http.fork.cloudflareAdd', 'Add Cloudflare\'s {{count}} ranges anyway', { count: cloudflare.missing.length })}
            </Button>}
        </Stack>
      </Alert>}
      {formik.touched.TrustedProxies && formik.errors.TrustedProxies && <FormHelperText error>{formik.errors.TrustedProxies}</FormHelperText>}
    </Stack>
  </Grid>;
};
