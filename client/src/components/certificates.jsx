// Fork addition: the certificates of this server, under Configuration >
// Domains. A card that says, per domain, who issued its certificate, what
// it covers, until when, when it is renewed, how the last order went and
// what to do about it, and per address whether a visitor's browser will
// trust it; "Renew now"; and the guided dialog "Add another domain" for a
// domain whose DNS is somewhere else. The server (src/certs) sends codes;
// the sentences are in utils/certificates.js.
import * as React from 'react';
import {
  Alert,
  Button,
  Chip,
  CircularProgress,
  Collapse,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  IconButton,
  Link,
  Stack,
  Tooltip,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  CopyOutlined,
  DownOutlined,
  InfoCircleOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  UpOutlined,
} from '@ant-design/icons';
import { useTheme } from '@mui/material/styles';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import MainCard from './MainCard';
import PermissionGuard from './permissionGuard';
import ChoiceChips from './choiceChips';
import ProposalBox from './proposalBox';
import SecretInput from './secretInput';
import { FormField, FormSelect } from './formField';
import { PERM_CONFIGURATION } from '../utils/permissions';
import {
  CHALLENGE_CHOICES, WAY_TEXT, caNotice, certificateState, challengeNote, cleanDomain, delegationText, domainProblem,
  failureSentences, homeAlerts, hostState, howFar, issuerSentence, methodSentence, missingForSave, proposalFor, providerFields,
  renewBlockedSentence, validitySentences, waitSentence, waysFor, zoneToSave,
} from '../utils/certificates';
import { FIX_LINKS } from '../utils/reachability';
import { Link as RouterLink } from 'react-router-dom';

// --- one status for the page, shared by the card and the dialogs
let store = { data: null, loading: false, failed: false };
const listeners = new Set();
const emit = () => listeners.forEach((listener) => listener({ ...store }));
let follow = null;

export const loadCertificates = async () => {
  store = { ...store, loading: true };
  emit();
  const res = await API.config.certificatesStatus();
  // an answer that is not the status (the server is restarting) keeps what was shown
  store = { data: res.ok ? res.data : store.data, loading: false, failed: !res.ok };
  emit();
  return res.ok ? res.data : null;
};

// After something was started the status is read again for a while: an
// order takes up to a few minutes, and a saved domain restarts the server.
export const followCertificates = (times = 40) => {
  clearTimeout(follow);
  let left = times;
  const again = async () => {
    const data = await loadCertificates();
    left -= 1;
    const busy = !data || (data.certificates || []).some((cert) => cert.running);
    if (left > 0 && (busy || left > times - 4)) {
      follow = setTimeout(again, 4000);
    }
  };
  follow = setTimeout(again, 1500);
};

export const useCertificates = () => {
  const [state, setState] = React.useState(store);
  React.useEffect(() => {
    listeners.add(setState);
    if (!store.data && !store.loading) loadCertificates();
    return () => { listeners.delete(setState); };
  }, []);
  return state;
};

const dateTime = (value) => {
  const date = new Date(value);
  return isNaN(date) ? '' : date.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
};

const LevelIcon = ({ level }) => {
  const theme = useTheme();
  const style = { marginTop: 3, flexShrink: 0 };
  if (level === 'ok') return <CheckCircleOutlined style={{ ...style, color: theme.palette.success.main }} />;
  if (level === 'problem') return <CloseCircleOutlined style={{ ...style, color: theme.palette.warning.main }} />;
  return <InfoCircleOutlined style={style} />;
};

const wrap = { overflowWrap: 'anywhere' };

// A value to copy into another provider's form.
const CopyValue = ({ label, value }) => {
  const { t } = useTranslation();
  const [copied, setCopied] = React.useState(false);
  const copy = () => {
    try {
      navigator.clipboard.writeText(value).then(() => setCopied(true), () => setCopied(false));
    } catch (e) {
      setCopied(false);
    }
  };
  return <Stack spacing={0.25} sx={{ minWidth: 0 }}>
    <Typography variant="caption" color="textSecondary">{label}</Typography>
    <Stack direction="row" spacing={0.5} alignItems="center" sx={{ minWidth: 0 }}>
      <Typography variant="body2" component="code" sx={{ ...wrap, fontFamily: 'monospace', userSelect: 'all', minWidth: 0 }}>{value}</Typography>
      <Tooltip title={copied ? t('mgmt.certs.copied', 'Copied') : t('mgmt.certs.copy', 'Copy')}>
        <IconButton size="small" onClick={copy} aria-label={t('mgmt.certs.copy', 'Copy')}><CopyOutlined /></IconButton>
      </Tooltip>
    </Stack>
  </Stack>;
};

// The record that hands the proof of a name to another domain.
export const DelegationRecord = ({ record }) => {
  const { t } = useTranslation();
  return <Stack spacing={1} sx={{ minWidth: 0 }} data-certs-record={record.record}>
    <CopyValue label={t('mgmt.certs.record.type', 'Type')} value="CNAME" />
    <CopyValue label={t('mgmt.certs.record.name', 'Name (some providers want it without the domain at the end)')} value={record.record} />
    <CopyValue label={t('mgmt.certs.record.target', 'Target (also called "value" or "points to")')} value={record.target} />
  </Stack>;
};

// One domain and its certificate.
const DomainCertificate = ({ cert, hosts, now, onRenew, busy, notice }) => {
  const { t } = useTranslation();
  const tr = (sentence) => (sentence ? t(sentence[0], sentence[1], sentence[2]) : '');
  const state = certificateState(cert);
  const failed = cert.last && !cert.last.ok ? failureSentences(cert.last.code) : null;
  const wait = waitSentence(cert.wait, dateTime);
  const blocked = renewBlockedSentence(cert);
  const validity = validitySentences(cert, now, dateTime);
  const uncovered = cert.uncovered || [];

  return <Stack spacing={1} data-certs-domain={cert.domain} sx={{ minWidth: 0 }}>
    <Stack direction="row" spacing={1} alignItems="center" useFlexGap flexWrap="wrap">
      <Typography variant="subtitle1" sx={wrap}><SafetyCertificateOutlined /> {cert.domain}</Typography>
      <Chip size="small" color={state.level} variant={state.level === 'default' ? 'outlined' : 'filled'} label={tr(state.label)} data-certs-state={state.level} />
    </Stack>

    <Typography variant="body2" sx={wrap}>{tr(methodSentence(cert))}</Typography>

    {cert.present && <Typography variant="body2" sx={wrap}>
      {tr(issuerSentence(cert))} {validity.map((sentence) => tr(sentence)).join(' ')}
    </Typography>}

    {cert.present && (cert.names || []).length > 0 && <Typography variant="body2" color="textSecondary" sx={wrap}>
      {t('mgmt.certs.covers', 'It covers: {{names}}', { names: cert.names.join(', ') })}
    </Typography>}

    {cert.kind !== 'none' && cert.present && uncovered.length > 0 && <Alert severity="warning" sx={{ ...wrap, '& .MuiAlert-message': { minWidth: 0 } }}>
      {t('mgmt.certs.uncovered', 'Not covered by it: {{names}}. Visitors of these addresses get a warning until a certificate that covers them arrives.', { names: uncovered.join(', ') })}
    </Alert>}

    {failed && <Alert severity="error" sx={{ ...wrap, '& .MuiAlert-message': { minWidth: 0 } }} data-certs-failure={cert.last.code}>
      <Stack spacing={0.5}>
        <Typography variant="body2" sx={{ fontWeight: 600 }}>
          {t('mgmt.certs.last.failed', 'The last order, {{when}}, failed.', { when: tr(howFar(cert.last.at, now)) })} {tr(failed.what)}
        </Typography>
        <Typography variant="body2">{tr(failed.todo)}</Typography>
        {cert.last.detail && <Typography variant="caption" color="textSecondary" sx={wrap}>
          {t('mgmt.certs.last.detail', 'In the other side\'s words:')} {cert.last.detail}
        </Typography>}
      </Stack>
    </Alert>}

    {cert.last && cert.last.ok && cert.kind === 'acme' && <Typography variant="body2" color="textSecondary" sx={wrap}>
      {t('mgmt.certs.last.ok', 'Last order: {{when}}, it succeeded.', { when: tr(howFar(cert.last.at, now)) })}
    </Typography>}

    {wait && <Typography variant="body2" color="textSecondary" sx={wrap}>{tr(wait)}</Typography>}

    {cert.method === 'delegated' && (cert.records || []).length > 0 && (failed || !cert.present || uncovered.length > 0) && <Stack spacing={1}>
      <Typography variant="body2" sx={wrap}>
        {t('mgmt.certs.records.needed', 'The records this domain needs at its own DNS provider, so that its proof is found in {{via}}:', { via: cert.via })}
      </Typography>
      {cert.records.map((record) => <DelegationRecord key={record.record} record={record} />)}
    </Stack>}

    {hosts.length > 0 && <Stack spacing={0.5}>
      {hosts.map((host) => {
        const view = hostState(host, dateTime);
        return <Stack key={host.host} direction="row" spacing={1} alignItems="flex-start" data-certs-host={host.host} data-certs-trusted={host.trusted ? 'yes' : 'no'}>
          <LevelIcon level={view.level} />
          <Typography variant="body2" sx={{ ...wrap, minWidth: 0 }}>
            <strong>{host.host}</strong> · {tr(view.sentence)}{view.detail ? ' ' + tr(view.detail) : ''}
          </Typography>
        </Stack>;
      })}
    </Stack>}

    {notice && <Alert severity={notice.severity} sx={wrap}>{notice.text}</Alert>}

    {cert.kind === 'acme' && <PermissionGuard permission={PERM_CONFIGURATION}>
      <Stack direction="row" spacing={1} alignItems="center" useFlexGap flexWrap="wrap">
        <Tooltip title={cert.canRenew
          ? t('mgmt.certs.renewTip', 'Orders the certificate again now, without waiting for its renewal date or for the pause after a failed order.')
          : (blocked ? tr(blocked) : '')}>
          <span>
            <LoadingButton size="small" variant="outlined" startIcon={<ReloadOutlined />} disabled={!cert.canRenew} loading={busy || cert.running}
              onClick={() => onRenew(cert)} data-certs-renew={cert.domain}>
              {t('mgmt.certs.renewNow', 'Renew now')}
            </LoadingButton>
          </span>
        </Tooltip>
        {/* the same reason as the wait above is not said twice */}
        {!cert.canRenew && blocked && !(cert.wait && cert.wait.reason === cert.renewBlocked) && <Typography variant="caption" color="textSecondary" sx={wrap}>{tr(blocked)}</Typography>}
      </Stack>
    </PermissionGuard>}
  </Stack>;
};

// How domains are proven on this server, and the door to change it.
const ChallengeSetting = ({ challenges }) => {
  const { t } = useTranslation();
  const tr = (sentence) => (sentence ? t(sentence[0], sentence[1], sentence[2]) : '');
  const [open, setOpen] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState('');
  const note = challengeNote(challenges);

  const choose = async (value) => {
    if (value === challenges.setting || busy) return;
    setBusy(true);
    setError('');
    const res = await API.config.certificatesSettings(value);
    setBusy(false);
    if (!res.ok) {
      setError(res.message || t('mgmt.certs.challenge.failed', 'The setting could not be saved.'));
      return;
    }
    followCertificates(8);
  };

  return <Stack spacing={1} data-certs-challenge={challenges.http}>
    {note && <Alert severity={note.level} sx={{ ...wrap, '& .MuiAlert-message': { minWidth: 0 } }}>{tr(note.sentence)}</Alert>}
    <PermissionGuard permission={PERM_CONFIGURATION}>
      <Stack spacing={1} alignItems="flex-start">
        <Button size="small" color="inherit" onClick={() => setOpen(!open)} endIcon={open ? <UpOutlined /> : <DownOutlined />} aria-expanded={open}>
          {t('mgmt.certs.challenge.door', 'Custom settings')}
        </Button>
        <Collapse in={open} sx={{ width: '100%' }}>
          <Stack spacing={1}>
            <ChoiceChips
              id="certs-http-challenge"
              label={t('mgmt.certs.challenge.label', 'Can a certificate authority reach this server on port 80 or 443?')}
              value={challenges.setting || ''}
              disabled={busy}
              onChange={choose}
              options={CHALLENGE_CHOICES.map((choice) => ({ value: choice.value, label: tr(choice.label), description: tr(choice.description) }))}
            />
            {error && <Alert severity="error" sx={wrap}>{error}</Alert>}
          </Stack>
        </Collapse>
      </Stack>
    </PermissionGuard>
  </Stack>;
};

// The card under the domains.
export const CertificatesOverview = () => {
  const { t } = useTranslation();
  const tr = (sentence) => (sentence ? t(sentence[0], sentence[1], sentence[2]) : '');
  const state = useCertificates();
  const [busy, setBusy] = React.useState('');
  const [notices, setNotices] = React.useState({});
  const data = state.data;

  React.useEffect(() => () => clearTimeout(follow), []);

  if (!data) {
    return <MainCard title={t('mgmt.certs.title', 'Certificates')}>
      {state.failed
        ? <Typography variant="body2" color="textSecondary">{t('mgmt.certs.noAnswer', 'The server did not answer. It may be restarting: this card fills in by itself.')}</Typography>
        : <center><CircularProgress color="inherit" size={20} /></center>}
    </MainCard>;
  }

  const renew = async (cert) => {
    setBusy(cert.domain);
    setNotices({ ...notices, [cert.domain]: null });
    const res = await API.config.certificatesRenew(cert.domain);
    setBusy('');
    if (res.ok) {
      setNotices({ ...notices, [cert.domain]: { severity: 'info', text: t('mgmt.certs.renewStarted', 'The order has started. It takes a minute or two; this card shows how it ends.') } });
      followCertificates();
    } else {
      setNotices({ ...notices, [cert.domain]: { severity: 'warning', text: res.message || t('mgmt.certs.renewRefused', 'The order could not be started.') } });
      loadCertificates();
    }
  };

  const now = new Date();
  const ca = caNotice(data.ca);
  const certificates = data.certificates || [];

  return <MainCard title={t('mgmt.certs.title', 'Certificates')}>
    <Stack spacing={2} data-certs-overview>
      <Typography variant="body2" sx={wrap}>
        {t('mgmt.certs.intro', 'A certificate is what lets a browser show the padlock for an address. Cosmos gets them from Let\'s Encrypt, free and trusted by every browser, and renews them by itself. An address served through Cloudflare shows Cloudflare\'s certificate to visitors; an address served directly shows the one below.')}
      </Typography>
      {ca && <Alert severity="warning" sx={wrap} data-certs-ca={data.ca.kind}>{tr(ca)}</Alert>}
      <ChallengeSetting challenges={data.challenges} />

      {certificates.length === 0
        ? <Typography variant="body2" color="textSecondary">{t('mgmt.certs.none', 'No domain is served yet.')}</Typography>
        : <Stack spacing={2} divider={<Divider flexItem />}>
          {certificates.map((cert) => <DomainCertificate
            key={cert.domain + (cert.derived ? ':derived' : '')}
            cert={cert}
            hosts={(data.hosts || []).filter((host) => host.domain === cert.domain)}
            now={now}
            busy={busy === cert.domain}
            notice={notices[cert.domain]}
            onRenew={renew}
          />)}
        </Stack>}

      <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
        <Button size="small" variant="outlined" startIcon={<ReloadOutlined />} onClick={() => loadCertificates()} disabled={state.loading}>
          {t('mgmt.certs.refresh', 'Read again')}
        </Button>
      </Stack>
    </Stack>
  </MainCard>;
};

// In upstream's "add a domain by hand" form, where Let's Encrypt without a
// DNS provider used to be offered as if it worked everywhere.
export const HttpChallengeNote = () => {
  const { t } = useTranslation();
  const state = useCertificates();
  const note = state.data ? challengeNote(state.data.challenges) : null;
  if (!note || state.data.challenges.http !== 'impossible') return null;
  return <Alert severity="warning" sx={wrap} data-certs-no-http>
    {t(note.sentence[0], note.sentence[1], note.sentence[2])}{' '}
    {t('mgmt.certs.challenge.needsProvider', 'Without a DNS provider this domain gets no certificate from Let\'s Encrypt here: choose its provider below, or use "Add another domain".')}
  </Alert>;
};

// The guided dialog for a domain whose DNS is not at Cloudflare.
// `zones`: the domains as /api/zones lists them. onClose(changed).
export const AnotherDomain = ({ zones, onClose, onCloudflare, onManual }) => {
  const { t } = useTranslation();
  const tr = (sentence) => (sentence ? t(sentence[0], sentence[1], sentence[2]) : '');
  const [domain, setDomain] = React.useState('');
  const [info, setInfo] = React.useState(null);
  const [looking, setLooking] = React.useState(false);
  const [lookupError, setLookupError] = React.useState('');
  const [way, setWay] = React.useState('');
  const [catalogue, setCatalogue] = React.useState(null);
  const [provider, setProvider] = React.useState('');
  const [variables, setVariables] = React.useState({});
  const [showCustom, setShowCustom] = React.useState(false);
  const [via, setVia] = React.useState('');
  const [check, setCheck] = React.useState(null);
  const [checking, setChecking] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [saveError, setSaveError] = React.useState('');
  const [done, setDone] = React.useState(false);

  const name = cleanDomain(domain);
  const problem = domainProblem(domain, zones);
  const proposal = proposalFor(info);
  const ways = info ? waysFor(info) : [];
  const offers = (info && info.canAnswer) || [];
  const offer = offers.find((entry) => entry.via === via) || null;

  React.useEffect(() => {
    API.config.certificatesProviders().then((res) => setCatalogue(res.ok ? res.data : {}));
  }, []);

  // what was found belongs to the name it was found for
  const typed = (text) => {
    setDomain(text);
    if (info && cleanDomain(text) !== info.domain) {
      setInfo(null);
      setWay('');
      setCheck(null);
      setLookupError('');
    }
  };

  const lookUp = async () => {
    if (!name || problem || looking || (info && info.domain === name)) return;
    setLooking(true);
    setLookupError('');
    const res = await API.config.certificatesDomain(name);
    setLooking(false);
    if (!res.ok) {
      setLookupError(res.status === 429
        ? t('mgmt.certs.add.tooMany', 'Too many lookups in the last hour: try again later.')
        : (res.message || t('mgmt.certs.add.lookupError', 'The domain could not be looked up.')));
      return;
    }
    setInfo(res.data);
    const first = waysFor(res.data)[0];
    setWay(first);
    setProvider(res.data.provider && res.data.provider !== 'cloudflare' ? res.data.provider : '');
    setVariables({});
    const answering = res.data.canAnswer || [];
    setVia(answering.length > 0 ? answering[0].via : '');
    setCheck(null);
  };

  const checkRecord = async () => {
    setChecking(true);
    const res = await API.config.certificatesDelegation(name, via);
    setChecking(false);
    setCheck(res.ok ? res.data : { state: 'failed', record: offer ? offer.record : '', target: offer ? offer.target : '' });
  };

  const fields = providerFields(catalogue && provider ? catalogue[provider] : null);
  const checked = check && check.state === 'ok';
  const missing = info ? missingForSave({ way, provider, fields: fields.main, variables, via, checked }) : null;

  const save = async () => {
    setSaving(true);
    setSaveError('');
    try {
      await API.zones.set(name, zoneToSave({ way, provider, variables, via }));
      setDone(true);
      followCertificates();
    } catch (err) {
      setSaveError(err && err.message ? err.message : t('mgmt.certs.add.saveError', 'The domain could not be saved.'));
    }
    setSaving(false);
  };

  const providerOptions = Object.entries(catalogue || {})
    .filter(([code]) => code !== 'cloudflare')
    .map(([code, entry]) => [code, entry.name || code])
    .sort((a, b) => a[1].localeCompare(b[1]));
  const delegationResult = check ? delegationText(check) : null;

  return <Dialog open onClose={() => !saving && onClose(done)} fullWidth maxWidth="sm" data-certs-add={done ? 'done' : (info ? 'way' : 'domain')}>
    <DialogTitle>{t('mgmt.certs.add.title', 'Add another domain')}</DialogTitle>
    <DialogContent>
      {done
        ? <Stack spacing={2} sx={{ pt: 1 }}>
          <Alert severity="success" sx={wrap}>
            {t('mgmt.certs.add.done', '{{domain}} is added. Cosmos is ordering its certificate now: the "Certificates" card shows how it goes. Give an application an address under it with "Domains" on its card (ServApps); its DNS record is yours to create at the domain\'s provider, pointing at this server.', { domain: name })}
          </Alert>
        </Stack>
        : <Stack spacing={2} sx={{ pt: 1 }}>
          <Typography variant="body2" sx={wrap}>
            {t('mgmt.certs.add.intro', 'For a domain that is not at Cloudflare. Cosmos gets it a certificate every browser trusts, for the domain and every name directly under it, without any port being open.')}
          </Typography>
          <FormField
            id="certs-add-domain"
            label={t('mgmt.certs.add.domain', 'Domain')}
            value={domain}
            onChange={typed}
            onBlur={lookUp}
            onEnter={lookUp}
            placeholder="example.net"
            error={problem ? tr(problem) : (lookupError || '')}
            helperText={t('mgmt.certs.add.domainHelp', 'The domain as you bought it, for example example.net. Cosmos looks up where its DNS is kept when you leave this field.')}
            inputProps={{ autoCapitalize: 'none', spellCheck: false }}
            endAdornment={looking ? <CircularProgress color="inherit" size={16} /> : null}
          />

          {proposal && proposal.kind === 'cloudflare' && <ProposalBox
            title={t('mgmt.certs.add.cloudflareTitle', 'This domain is at Cloudflare')}
            source={tr(proposal.source)}
            status={t('mgmt.certs.add.cloudflareStatus', '"Connect Cloudflare" sets it up with one token, including the records of your applications.')}
            onUse={onCloudflare}
            useLabel={t('mgmt.cloudflare.connect.button', 'Connect Cloudflare')}
          />}

          {proposal && proposal.kind !== 'cloudflare' && <Typography variant="body2" color="textSecondary" sx={wrap} data-certs-source={proposal.kind}>
            {tr(proposal.source)}
          </Typography>}

          {info && (!proposal || proposal.kind !== 'cloudflare') && <>
            {ways.length > 1
              ? <ChoiceChips
                id="certs-add-way"
                label={t('mgmt.certs.add.wayLabel', 'How Cosmos proves that the domain is yours')}
                value={way}
                onChange={(value) => { setWay(value); setSaveError(''); }}
                options={ways.map((value) => ({ value, label: tr(WAY_TEXT[value].label), description: tr(WAY_TEXT[value].description) }))}
              />
              : <Typography variant="body2" sx={wrap}>{tr(WAY_TEXT.access.description)} {t('mgmt.certs.add.noDelegate', 'Once one domain of this server has DNS access, any other domain can be added with a single record instead.')}</Typography>}

            {way === 'access' && <Stack spacing={2}>
              <FormSelect
                id="certs-add-provider"
                label={t('mgmt.certs.add.provider', 'DNS provider')}
                value={provider}
                onChange={(value) => { setProvider(value); setVariables({}); }}
                options={providerOptions}
                emptyLabel={t('mgmt.certs.add.providerChoose', 'Choose…')}
                helperText={info.provider && info.provider === provider
                  ? t('mgmt.certs.add.providerProposed', 'Proposed from the domain\'s name servers.')
                  : t('mgmt.certs.add.providerHelp', 'The company where the DNS records of this domain are edited (often the one the domain was bought from).')}
              />
              {provider && fields.main.map((field) => <SecretInput
                key={field.name}
                id={'certs-add-' + field.name}
                label={field.text || field.name}
                value={variables[field.name] || ''}
                stored={false}
                onChange={(value) => setVariables({ ...variables, [field.name]: value })}
                helperText={t('mgmt.certs.add.variableHelp', 'The provider\'s name for this setting: {{name}}. Leave empty what your account does not use.', { name: field.name })}
              />)}
              {provider && fields.custom.length > 0 && <Stack spacing={1} alignItems="flex-start">
                <Button size="small" color="inherit" onClick={() => setShowCustom(!showCustom)} endIcon={showCustom ? <UpOutlined /> : <DownOutlined />} aria-expanded={showCustom}>
                  {t('mgmt.certs.challenge.door', 'Custom settings')}
                </Button>
                <Collapse in={showCustom} sx={{ width: '100%' }}>
                  <Stack spacing={2}>
                    {fields.custom.map((field) => <FormField
                      key={field.name}
                      id={'certs-add-' + field.name}
                      label={field.name}
                      value={variables[field.name] || ''}
                      onChange={(value) => setVariables({ ...variables, [field.name]: value })}
                      helperText={field.text}
                    />)}
                  </Stack>
                </Collapse>
              </Stack>}
              {provider && <Typography variant="caption" color="textSecondary" sx={wrap}>
                {t('mgmt.certs.add.accessNote', 'What you enter is stored on this server and used only to write the record that proves the domain. Give the token no more than the right to edit this domain\'s DNS.')}
              </Typography>}
            </Stack>}

            {way === 'delegate' && <Stack spacing={2}>
              {offers.length > 1 && <FormSelect
                id="certs-add-via"
                label={t('mgmt.certs.add.via', 'Domain the proof is written in')}
                value={via}
                onChange={(value) => { setVia(value); setCheck(null); }}
                options={offers.map((entry) => [entry.via, entry.via])}
              />}
              {offer && <>
                <Typography variant="body2" sx={wrap}>
                  {t('mgmt.certs.add.recordIntro', 'Create this record where the DNS of {{domain}} is edited. It is the only thing to do there, once; it lets certificate authorities find the proof for {{domain}} in {{via}}, where Cosmos writes it.', { domain: name, via })}
                </Typography>
                <DelegationRecord record={offer} />
                <Stack direction="row" spacing={1} alignItems="center" useFlexGap flexWrap="wrap">
                  <LoadingButton size="small" variant="outlined" loading={checking} onClick={checkRecord} data-certs-check>
                    {t('mgmt.certs.add.check', 'Check the record')}
                  </LoadingButton>
                </Stack>
                {delegationResult && <Alert severity={delegationResult.ok ? 'success' : 'warning'} sx={{ ...wrap, '& .MuiAlert-message': { minWidth: 0 } }} data-certs-delegation={check.state}>
                  {tr(delegationResult.sentence)}
                </Alert>}
              </>}
            </Stack>}

            <Typography variant="caption" color="textSecondary" sx={wrap}>
              {t('mgmt.certs.add.noHttp', 'Not on offer: proving a name by answering on port 80 or 443 (HTTP-01, TLS-ALPN-01). See "Custom settings" on the Certificates card if this server is reached on those ports.')}
            </Typography>
          </>}

          {saveError && <Alert severity="error" sx={wrap}>{saveError}</Alert>}
        </Stack>}
    </DialogContent>
    <DialogActions sx={{ flexWrap: 'wrap', gap: 1 }}>
      {!done && missing && info && (!proposal || proposal.kind !== 'cloudflare') && <Typography variant="caption" color="textSecondary" sx={{ ...wrap, flex: '1 1 200px', px: 1 }}>
        {tr(missing)}
      </Typography>}
      {!done && <Button color="inherit" onClick={onManual}>{t('mgmt.cloudflare.connect.byHand', 'Add a domain by hand')}</Button>}
      <Button onClick={() => onClose(done)} disabled={saving}>{done ? t('global.close', 'Close') : t('global.cancelAction', 'Cancel')}</Button>
      {!done && <LoadingButton variant="contained" loading={saving} disabled={!info || !!missing || !!problem || (proposal && proposal.kind === 'cloudflare')} onClick={save} data-certs-save>
        {t('mgmt.certs.add.save', 'Add the domain')}
      </LoadingButton>}
    </DialogActions>
  </Dialog>;
};

// On the home page, for the administrators: what is wrong with a
// certificate, for which domain, and what to do. Nothing when nothing is
// (this replaces upstream's "provide an email address" and its list of raw
// errors since the last restart, see pages/home/index.jsx).
export const CertificateAlerts = () => {
  const { t } = useTranslation();
  const tr = (sentence) => (sentence ? t(sentence[0], sentence[1], sentence[2]) : '');
  const state = useCertificates();
  // read again at every visit of the home page: what was read on another page may be old
  React.useEffect(() => { loadCertificates(); }, []);
  const alerts = homeAlerts(state.data, dateTime);
  if (alerts.length === 0) return null;
  return <>
    {alerts.map((alert, index) => <Alert key={alert.domain + ':' + index} severity={alert.severity} sx={{ ...wrap, '& .MuiAlert-message': { minWidth: 0 } }} data-certs-home={alert.domain || 'ca'}>
      <Stack spacing={0.5}>
        <Typography variant="body2" sx={wrap}>{alert.what.map(tr).join(' ')}</Typography>
        {alert.todo && <Typography variant="body2" sx={wrap}>{tr(alert.todo)}</Typography>}
        <Typography variant="body2"><Link component={RouterLink} to={FIX_LINKS.domains()}>{t('mgmt.certs.home.open', 'Open the certificates')}</Link></Typography>
      </Stack>
    </Alert>)}
  </>;
};

export default CertificatesOverview;
