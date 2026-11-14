// Fork addition: the section of the Cloudflare card about "Authenticated
// Origin Pulls": the certificate Cloudflare's proxy presents to this server,
// which shows that a request from Cloudflare's addresses really was made by
// Cloudflare's proxy (see src/cloudflare/originpulls.go). Off until the
// administrator switches it on, in steps: choose whose certificate, set it up
// at Cloudflare, test (count, refuse nothing), enforce, with a way back at
// every step. The sentences and decisions are in utils/originPulls.js.
import * as React from 'react';
import {
  Alert,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  InputLabel,
  OutlinedInput,
  Stack,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import ChoiceChips from './choiceChips';
import { FormField } from './formField';
import PermissionGuard from './permissionGuard';
import { PERM_CONFIGURATION } from '../utils/permissions';
import {
  KINDS, certState, expiryWarning, nextStep, refusalText, sameState, showFingerprint, stateText, stepsAtCloudflare,
  uploadFailureText, verdict, waitingCert,
} from '../utils/originPulls';

const wrap = { overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } };

// A text the administrator copies into Cloudflare's dashboard.
const CopyBox = ({ id, label, value }) => {
  const { t } = useTranslation();
  const [copied, setCopied] = React.useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
    } catch (e) {
      // no clipboard here (an old browser, a page without HTTPS): select it for copying by hand
      const field = document.getElementById(id);
      if (field) { field.focus(); field.select(); }
    }
  };
  return <Stack spacing={0.5}>
    <InputLabel htmlFor={id}>{label}</InputLabel>
    <OutlinedInput id={id} value={value} fullWidth multiline minRows={3} maxRows={6}
      inputProps={{ readOnly: true, spellCheck: false, style: { fontFamily: 'monospace', fontSize: '0.8rem' } }} />
    <Stack direction="row" spacing={1} alignItems="center">
      <Button size="small" variant="outlined" onClick={copy}>{t('mgmt.cloudflare.pulls.copy', 'Copy')}</Button>
      {copied && <Typography variant="caption" color="textSecondary">{t('mgmt.cloudflare.pulls.copied', 'Copied.')}</Typography>}
    </Stack>
  </Stack>;
};

// `status` is the answer of GET /api/cloudflare; `reload` reads it again.
const OriginPulls = ({ status, reload }) => {
  const { t } = useTranslation();
  const say = (pair) => (pair ? t(pair[0], pair[1], pair[2]) : '');
  const [busy, setBusy] = React.useState('');
  const [notice, setNotice] = React.useState(null); // { severity, text }
  const [needToken, setNeedToken] = React.useState(false);
  const [token, setToken] = React.useState('');
  const [zone, setZone] = React.useState('');
  const [revealed, setRevealed] = React.useState(null); // { certificate, privateKey }
  const [askAnyway, setAskAnyway] = React.useState(false);
  const [fresh, setFresh] = React.useState(null); // the state an action answered with, until the status is read again

  const pulls = fresh || (status && status.originPulls);
  const shielded = ((status && status.hosts) || []).filter((host) => host.shielded).map((host) => host.host);
  const mode = (pulls && pulls.mode) || '';
  const confirming = React.useRef(false);

  // What an action answered is shown at once. The status is then read again
  // until it says the same: some actions make the server take its
  // connections down for a moment, and what is read meanwhile is the state
  // from before.
  const settle = async (expected) => {
    for (let i = 0; i < 10; i++) {
      const data = await reload();
      if (!expected || sameState(data && data.originPulls, expected)) break;
      await new Promise((resolve) => setTimeout(resolve, 1500));
    }
    setFresh(null);
  };

  // while certificates are asked for, the counts move: look again every few seconds
  React.useEffect(() => {
    if (!mode) return undefined;
    const timer = setInterval(() => { reload(); }, 5000);
    return () => clearInterval(timer);
  }, [mode]);

  // Enforcing waits for a request from the administrator's browser made
  // after it began: this one. If it cannot be made (this browser came
  // through Cloudflare and is now refused), Cosmos stops enforcing by itself.
  React.useEffect(() => {
    if (!pulls || !pulls.unconfirmed || confirming.current) return;
    confirming.current = true;
    API.config.cloudflareOriginPulls({ action: 'confirm' }).then((res) => {
      confirming.current = false;
      if (res.ok && res.data) {
        setFresh(res.data.originPulls);
        settle(res.data.originPulls);
      } else {
        settle(null);
      }
    });
  }, [pulls && pulls.unconfirmed, pulls && pulls.confirmSeconds]);

  if (!pulls || shielded.length === 0) {
    return null;
  }

  const run = async (name, body, done) => {
    setBusy(name);
    setNotice(null);
    const res = await API.config.cloudflareOriginPulls(body);
    setBusy('');
    if (res.ok && res.data) {
      if (res.data.originPulls) setFresh(res.data.originPulls);
      if (done) done(res.data);
      settle(res.data.originPulls);
      return res.data;
    }
    if (body.action === 'upload' && res.status === 502) {
      if (res.reason === 'forbidden' || res.reason === 'invalid_token' || res.reason === 'malformed_token') setNeedToken(true);
      setNotice({ severity: res.reason === 'forbidden' && !body.token ? 'info' : 'error', text: say(uploadFailureText(res.reason)) + (res.detail ? ' (' + res.detail + ')' : '') });
    } else if (res.status === 0) {
      setNotice({ severity: 'warning', text: t('mgmt.cloudflare.pulls.noAnswer', 'Cosmos did not answer. If you reach this page through Cloudflare, wait a moment and reload it.') });
    } else {
      setNotice({ severity: 'error', text: res.reason ? say(refusalText(res.reason)) : (res.message || say(refusalText(''))) });
    }
    settle(null);
    return null;
  };

  const state = stateText(pulls);
  const expiry = expiryWarning(pulls);
  const step = nextStep(pulls);
  const waiting = waitingCert(pulls);
  const result = mode ? verdict(pulls, shielded, say) : null;
  const zones = pulls.zones || [];
  const target = zone || zones[0] || '';
  const enforcing = mode === 'enforce';
  const kind = KINDS.find((entry) => entry.value === pulls.kind);

  const upload = () => run('upload', { action: 'upload', zone: target, token: needToken ? token.trim() : '' }, (data) => {
    setNeedToken(false);
    setToken('');
    setNotice({
      severity: data.enabled ? 'success' : 'warning',
      text: data.enabled
        ? t('mgmt.cloudflare.pulls.upload.done', 'Cloudflare has the certificate for {{zone}} and "Zone-level" is switched on there. Cosmos no longer holds the key. Start the test below.', { zone: target })
        : t('mgmt.cloudflare.pulls.upload.notEnabled', 'Cloudflare has the certificate for {{zone}}, but Cosmos could not switch "Zone-level" on there: do that at Cloudflare (SSL/TLS > Origin Server > Authenticated Origin Pulls). Cosmos no longer holds the key.', { zone: target }),
    });
  });

  return <Stack spacing={1.5} data-origin-pulls={mode || 'off'} sx={{ minWidth: 0 }}>
    <Typography variant="subtitle1">{t('mgmt.cloudflare.pulls.title', 'Proof that a request came through Cloudflare')}</Typography>
    <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.cloudflare.pulls.intro',
        'Cloudflare\'s addresses are shared by all its customers, so a request from one of them has not necessarily passed your domain there. Cloudflare\'s proxy can present a certificate to this server with every connection (Cloudflare calls this "Authenticated Origin Pulls"), and Cosmos can require it for the addresses served through Cloudflare. Your local network never needs it: the panel stays reachable at home whatever is set here.')}
    </Typography>

    <Alert severity={state[0]} sx={wrap} data-origin-pulls-state>{t(state[1], state[2], state[3])}</Alert>
    {expiry && <Alert severity={expiry[0]} sx={wrap}>{t(expiry[1], expiry[2], expiry[3])}</Alert>}

    <PermissionGuard permission={PERM_CONFIGURATION}>
      <Stack spacing={1.5} sx={{ minWidth: 0 }}>
        <ChoiceChips
          id="origin-pulls-kind"
          label={t('mgmt.cloudflare.pulls.kind.label', 'Which certificate Cloudflare presents')}
          value={pulls.kind || ''}
          disabled={enforcing || busy !== ''}
          options={KINDS.map((entry) => ({ value: entry.value, label: say(entry.label), description: say(entry.description) }))}
          helperText={enforcing
            ? t('mgmt.cloudflare.pulls.kind.locked', 'Stop enforcing to change this.')
            : t('mgmt.cloudflare.pulls.kind.help', 'Choose one to see what it proves and what to do at Cloudflare.')}
          onChange={(value) => value !== pulls.kind && run('kind', { action: 'kind', kind: value })}
        />
        {pulls.kind === 'shared' && pulls.shared && <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
          {t('mgmt.cloudflare.pulls.shared.built', 'Cloudflare\'s certificate as built into this version of Cosmos: valid until {{date}}, SHA-256 fingerprint {{fingerprint}}. It is never fetched from the internet: you can compare it with the one Cloudflare publishes.',
            { date: String(pulls.shared.notAfter).slice(0, 10), fingerprint: showFingerprint(pulls.shared.fingerprint) })}
        </Typography>}

        {pulls.kind === 'own' && <Stack spacing={1} sx={{ minWidth: 0 }}>
          {(pulls.own || []).map((cert) => {
            const words = certState(cert);
            return <Stack key={cert.fingerprint} spacing={0.25} sx={{ minWidth: 0 }} data-origin-pull-cert={cert.waiting ? 'waiting' : (cert.handedOver ? 'handed' : 'unused')}>
              <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
                {t('mgmt.cloudflare.pulls.cert.line', 'Certificate valid until {{date}} ({{days}} days left)', { date: String(cert.notAfter).slice(0, 10), days: cert.daysLeft })}
              </Typography>
              <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
                {t('mgmt.cloudflare.pulls.cert.fingerprint', 'SHA-256 fingerprint {{fingerprint}}', { fingerprint: showFingerprint(cert.fingerprint) })}
              </Typography>
              <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>{t(words[0], words[1], words[2])}</Typography>
              {!cert.waiting && (pulls.own || []).length > 1 && <Stack direction="row">
                <LoadingButton size="small" loading={busy === 'retire' + cert.fingerprint} disabled={busy !== ''}
                  onClick={() => run('retire' + cert.fingerprint, { action: 'retire', fingerprint: cert.fingerprint })}>
                  {t('mgmt.cloudflare.pulls.cert.retire', 'Stop accepting this certificate')}
                </LoadingButton>
              </Stack>}
            </Stack>;
          })}

          {waiting && <Stack spacing={1} sx={{ minWidth: 0 }} data-origin-pull-handover>
            <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
              {t('mgmt.cloudflare.pulls.handover.text',
                'Cloudflare needs this certificate and its private key to present it. Cosmos can upload both, or show them to you once. After either, Cosmos no longer has the key.')}
            </Typography>
            {zones.length > 1 && <ChoiceChips
              id="origin-pulls-zone"
              label={t('mgmt.cloudflare.pulls.handover.zone', 'For which domain')}
              value={target}
              options={zones.map((name) => ({ value: name, label: name }))}
              onChange={setZone}
            />}
            {needToken && <FormField
              id="origin-pulls-token"
              label={t('mgmt.cloudflare.pulls.handover.token', 'One-time token')}
              type="password"
              autoComplete="off"
              value={token}
              onChange={setToken}
              helperText={t('mgmt.cloudflare.pulls.handover.tokenHelp', 'A Cloudflare API token with the permission Zone > SSL and Certificates > Edit for {{zone}}. Used for this upload, not kept. You can delete it at Cloudflare afterwards.', { zone: target })}
            />}
            <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
              <LoadingButton size="small" variant="contained" loading={busy === 'upload'} disabled={busy !== '' || !target || (needToken && !token.trim())} onClick={upload}>
                {needToken
                  ? t('mgmt.cloudflare.pulls.handover.uploadWith', 'Upload with this token')
                  : t('mgmt.cloudflare.pulls.handover.upload', 'Upload to Cloudflare')}
              </LoadingButton>
              <LoadingButton size="small" variant="outlined" loading={busy === 'reveal'} disabled={busy !== ''}
                onClick={() => run('reveal', { action: 'reveal' }, (data) => setRevealed({ certificate: data.certificate, privateKey: data.privateKey }))}>
                {t('mgmt.cloudflare.pulls.handover.reveal', 'Show the certificate and key')}
              </LoadingButton>
              <LoadingButton size="small" loading={busy === 'discard'} disabled={busy !== ''} onClick={() => run('discard', { action: 'discard' })}>
                {t('mgmt.cloudflare.pulls.handover.discard', 'Discard it')}
              </LoadingButton>
            </Stack>
          </Stack>}

          {!waiting && <Stack direction="row">
            <LoadingButton size="small" variant="outlined" loading={busy === 'generate'} disabled={busy !== ''}
              onClick={() => run('generate', { action: 'generate' }, () => setNeedToken(false))}>
              {(pulls.own || []).length === 0
                ? t('mgmt.cloudflare.pulls.cert.make', 'Make a certificate')
                : t('mgmt.cloudflare.pulls.cert.makeNew', 'Make a new certificate')}
            </LoadingButton>
          </Stack>}
        </Stack>}

        {kind && !enforcing && <Stack spacing={0.5} sx={{ minWidth: 0 }}>
          <Typography variant="body2">
            {pulls.kind === 'own'
              ? t('mgmt.cloudflare.pulls.steps.titleOwn', 'At Cloudflare, when you upload the certificate yourself:')
              : t('mgmt.cloudflare.pulls.steps.titleShared', 'At Cloudflare:')}
          </Typography>
          <Typography component="ol" variant="body2" color="textSecondary" sx={{ m: 0, pl: 2.5, overflowWrap: 'anywhere' }}>
            {stepsAtCloudflare(pulls.kind).map((entry) => <li key={entry[0]}>{say(entry)}</li>)}
          </Typography>
        </Stack>}

        {result && <Alert severity={result[0]} sx={wrap} data-origin-pulls-verdict={result[1].split('.').pop()}>
          {t(result[1], result[2], result[3])}
        </Alert>}
        {!mode && step.why && pulls.kind && <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>{say(step.why)}</Typography>}
        {mode === 'test' && step.why && <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>{say(step.why)}</Typography>}
        {notice && <Alert severity={notice.severity} sx={wrap} data-origin-pulls-notice>{notice.text}</Alert>}

        <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
          {!mode && <LoadingButton size="small" variant="contained" loading={busy === 'test'} disabled={busy !== '' || !step.test}
            onClick={() => run('test', { action: 'mode', mode: 'test' })}>
            {t('mgmt.cloudflare.pulls.action.test', 'Start the test')}
          </LoadingButton>}
          {mode === 'test' && <LoadingButton size="small" variant="contained" loading={busy === 'enforce'} disabled={busy !== '' || !step.enforce}
            onClick={() => (step.anyway ? setAskAnyway(true) : run('enforce', { action: 'mode', mode: 'enforce' }))}>
            {step.anyway
              ? t('mgmt.cloudflare.pulls.action.enforceAnyway', 'Enforce anyway')
              : t('mgmt.cloudflare.pulls.action.enforce', 'Enforce')}
          </LoadingButton>}
          {enforcing && <LoadingButton size="small" variant="contained" color="warning" loading={busy === 'stop'} disabled={busy !== ''}
            onClick={() => run('stop', { action: 'mode', mode: 'test' })}>
            {t('mgmt.cloudflare.pulls.action.stop', 'Stop enforcing')}
          </LoadingButton>}
          {mode && <LoadingButton size="small" variant="outlined" loading={busy === 'off'} disabled={busy !== ''}
            onClick={() => run('off', { action: 'mode', mode: 'off' })}>
            {t('mgmt.cloudflare.pulls.action.off', 'Switch off')}
          </LoadingButton>}
        </Stack>
      </Stack>
    </PermissionGuard>

    {askAnyway && <Dialog open onClose={() => setAskAnyway(false)} fullWidth maxWidth="xs">
      <DialogTitle>{t('mgmt.cloudflare.pulls.anyway.title', 'Enforce although some requests have no certificate?')}</DialogTitle>
      <DialogContent>
        <Stack spacing={1.5}>
          {result && <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{t(result[1], result[2], result[3])}</Typography>}
          <Typography variant="body2">
            {t('mgmt.cloudflare.pulls.anyway.text',
              'From now on such requests are refused. Requests with the certificate, and everything from your local network, are answered as before. If this browser is cut off, Cosmos goes back to testing by itself after three minutes.')}
          </Typography>
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={() => setAskAnyway(false)}>{t('global.cancelAction')}</Button>
        <Button onClick={() => { setAskAnyway(false); run('enforce', { action: 'mode', mode: 'enforce', anyway: true }); }}>
          {t('mgmt.cloudflare.pulls.action.enforceAnyway', 'Enforce anyway')}
        </Button>
      </DialogActions>
    </Dialog>}

    {revealed && <Dialog open onClose={() => {}} fullWidth maxWidth="sm">
      <DialogTitle>{t('mgmt.cloudflare.pulls.reveal.title', 'The certificate and its private key')}</DialogTitle>
      <DialogContent>
        <Stack spacing={1.5} data-origin-pull-revealed>
          <Alert severity="warning" sx={wrap}>
            {t('mgmt.cloudflare.pulls.reveal.once',
              'This is shown once: Cosmos no longer has the key. Whoever has it can pass as Cloudflare for your domain, so paste it at Cloudflare and keep it nowhere else. If it gets lost before it is uploaded, make a new certificate.')}
          </Alert>
          <CopyBox id="origin-pulls-certificate" label={t('mgmt.cloudflare.pulls.reveal.certificate', 'Certificate')} value={revealed.certificate} />
          <CopyBox id="origin-pulls-key" label={t('mgmt.cloudflare.pulls.reveal.key', 'Private key')} value={revealed.privateKey} />
          <Typography component="ol" variant="body2" color="textSecondary" sx={{ m: 0, pl: 2.5, overflowWrap: 'anywhere' }}>
            {stepsAtCloudflare('own').map((entry) => <li key={entry[0]}>{say(entry)}</li>)}
          </Typography>
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={() => setRevealed(null)}>{t('mgmt.cloudflare.pulls.reveal.done', 'I have uploaded it: close')}</Button>
      </DialogActions>
    </Dialog>}
  </Stack>;
};

export default OriginPulls;
