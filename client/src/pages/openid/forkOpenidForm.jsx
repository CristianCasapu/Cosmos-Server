// Fork addition: the OpenID client dialog (Configuration > OpenID), guided.
// Hooked into upstream's openid-list.jsx in place of openid-edit.jsx's
// dialog and of the secret made in the browser. The app is picked from the
// server's URLs; its redirect URIs are proposed from what its documentation
// names (utils/openidForm.js) and never replace what was typed; the secret is
// made on the server and shown once, with every value the app needs ready to
// copy. The server checks the same rules (src/configapi/fork_openid.go).
import * as React from 'react';
import {
  Alert, Button, Chip, Dialog, DialogActions, DialogContent, DialogTitle, FormHelperText, IconButton,
  OutlinedInput, Stack, Tooltip, Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { CopyOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import ChoiceChips from '../../components/choiceChips';
import ProposalBox from '../../components/proposalBox';
import { FormField } from '../../components/formField';
import { keepOnBackdrop } from '../../components/formHelpers';
import {
  appChoices, proposeForApp, redirectProblem, redirectList, clientProblems, providerValues,
} from '../../utils/openidForm';

const tr = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');

// Where the panel is: the hostname from the settings, the port this page is open on.
export const panelPlace = (config) => {
  const http = (config && config.HTTPConfig) || {};
  return {
    hostname: http.Hostname || (typeof window !== 'undefined' ? window.location.hostname : ''),
    https: http.HTTPSCertificateMode !== 'DISABLED',
    port: typeof window !== 'undefined' ? window.location.port : '',
  };
};

const sameList = (a, b) => a.length === b.length && a.every((v, i) => v === b[i]);

export const ForkOpenidDialog = ({ clientId, config, onClose, onDone }) => {
  const { t } = useTranslation();
  const clients = (config && config.OpenIDClients) || [];
  const existing = clientId ? clients.find((c) => c.id === clientId) : null;
  const where = panelPlace(config);
  const choices = React.useMemo(() => appChoices(config, where), [config]);
  const takenIds = clients.map((c) => c.id).filter((id) => id !== clientId);

  const [choice, setChoice] = React.useState(existing ? 'keep' : '');
  const [id, setId] = React.useState(existing ? existing.id : '');
  const [redirects, setRedirects] = React.useState(existing ? redirectList(existing.redirect) : []);
  const [isPublic, setPublic] = React.useState(existing ? !!existing.public : false);
  const [typed, setTyped] = React.useState('');
  const [marks, setMarks] = React.useState({ id: null, redirects: null });
  const [proposal, setProposal] = React.useState(null);
  const [tried, setTried] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [failure, setFailure] = React.useState('');

  // a proposal fills what is empty or still holds the previous proposal
  const pick = (value) => {
    setChoice(value);
    const picked = choices.find((c) => c.host === value);
    const next = picked ? proposeForApp(picked, takenIds) : null;
    setProposal(next);
    if (!next) return;
    const nextMarks = { ...marks };
    if (!existing && (!id.trim() || id === marks.id)) { setId(next.id); nextMarks.id = next.id; }
    if (redirects.length === 0 || (marks.redirects && sameList(redirects, marks.redirects))) {
      setRedirects(next.redirects);
      nextMarks.redirects = next.redirects;
    }
    setMarks(nextMarks);
  };

  const typedProblem = typed.trim() ? redirectProblem(typed.trim()) : null;
  const add = () => {
    const value = typed.trim();
    if (!value || typedProblem || redirects.includes(value)) return;
    setRedirects([...redirects, value]);
    setTyped('');
  };
  const problems = clientProblems({ id: existing ? existing.id : id, redirects, takenIds });
  const proposalHeld = proposal && !sameList(redirects, proposal.redirects);

  const save = async () => {
    setTried(true);
    setFailure('');
    if (problems.length) return;
    setBusy(true);
    const body = { id: existing ? existing.id : id.trim(), redirect: redirects.join(','), public: isPublic, secret: '' };
    try {
      if (existing) {
        await API.openid.update(existing.id, body);
        let secret = '';
        // a client that stops being public needs a secret of its own
        if (existing.public && !isPublic) {
          const res = await API.openid.newSecret(existing.id);
          secret = res.data.secret;
        }
        onDone({ id: body.id, redirects, public: isPublic, secret, created: false });
      } else {
        const res = await API.openid.create(body);
        onDone({ id: body.id, redirects, public: isPublic, secret: isPublic ? '' : (res.data && res.data.secret) || '', created: true });
      }
    } catch (err) {
      setFailure(err.message || String(err));
      setBusy(false);
    }
  };

  const appOptions = [
    ...choices.map((c) => ({ value: c.host, label: c.app ? `${c.name} · ${c.app.name}` : c.name })),
    { value: 'other', label: t('mgmt.openId.fork.otherApp', 'Another app') },
  ];

  return <Dialog open onClose={keepOnBackdrop(onClose)} maxWidth="sm" fullWidth>
    <DialogTitle>{existing ? existing.id : t('mgmt.openid.newClientTitle')}</DialogTitle>
    <DialogContent>
      <Stack spacing={2.5} sx={{ mt: 1, overflowWrap: 'anywhere' }}>
        <Typography variant="body2" color="textSecondary">
          {t('mgmt.openId.fork.intro', 'An app registered here lets its users sign in with their Cosmos account. The app needs an ID, the addresses it is sent back to after signing in, and (for most apps) a secret that Cosmos makes.')}
        </Typography>

        {!where.https && <Alert severity="warning" sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>
          {t('mgmt.openId.fork.noHttps', 'This server serves its apps without HTTPS: an app cannot be sent back to a plain http address (only to one on the same device), so sign-in with Cosmos will not work until HTTPS is on (Configuration > Domains).')}
        </Alert>}
        {!existing && <ChoiceChips
          id="fork-openid-app"
          label={t('mgmt.openId.fork.whichApp', 'Which app')}
          value={choice}
          options={appOptions}
          onChange={pick}
          helperText={choices.length
            ? t('mgmt.openId.fork.whichAppHelp', 'The apps this server serves under a name of their own. Picking one proposes its ID and its addresses.')
            : t('mgmt.openId.fork.noApps', 'This server has no URL with a name of its own yet: enter the app\'s addresses below.')}
        />}
        {proposal && proposal.source && <FormHelperText sx={{ mt: -1.5 }}>{tr(t, proposal.source)}</FormHelperText>}

        {!existing
          ? <FormField
            id="fork-openid-id"
            label={t('mgmt.openId.fork.idLabel', 'Client ID')}
            value={id}
            required
            onChange={setId}
            error={tried && problems.find((p) => p[0].startsWith('mgmt.openId.fork.id')) ? tr(t, problems.find((p) => p[0].startsWith('mgmt.openId.fork.id'))) : ''}
            helperText={t('mgmt.openId.fork.idHelp', 'A short name the app sends to say who it is, for example immich. You enter the same in the app.')}
          />
          : <FormHelperText>{t('mgmt.openId.fork.idFixed', 'The client ID cannot be changed: the app knows it under this name. Delete the client and create another to rename it.')}</FormHelperText>}

        <Stack spacing={1}>
          <Typography variant="subtitle1" component="div">{t('mgmt.openId.fork.redirectsLabel', 'Addresses the app is sent back to')}</Typography>
          {redirects.length === 0
            ? <FormHelperText error={tried}>{t('mgmt.openId.fork.redirectsEmpty', 'None yet: pick the app above, or add the address its documentation names.')}</FormHelperText>
            : <Stack direction="row" useFlexGap flexWrap="wrap" spacing={1}>
              {redirects.map((uri) => <Chip
                key={uri}
                label={uri}
                variant="outlined"
                color={redirectProblem(uri) ? 'error' : 'default'}
                onDelete={() => setRedirects(redirects.filter((r) => r !== uri))}
                sx={{ maxWidth: '100%', height: 'auto', '& .MuiChip-label': { whiteSpace: 'normal', py: 0.5, overflowWrap: 'anywhere' } }}
              />)}
            </Stack>}
          <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} alignItems={{ xs: 'stretch', sm: 'flex-start' }}>
            <div style={{ flex: 1, minWidth: 0 }}>
              <FormField
                id="fork-openid-redirect"
                value={typed}
                placeholder="https://app.example.com/oauth/callback"
                onChange={setTyped}
                onEnter={add}
                error={typedProblem ? tr(t, typedProblem) : ''}
                helperText={t('mgmt.openId.fork.redirectHelp', 'Written out in full, exactly as the app sends it (its documentation calls it redirect URI or callback URL), then Add.')}
              />
            </div>
            <Button variant="outlined" disabled={!typed.trim() || Boolean(typedProblem)} onClick={add} sx={{ minHeight: 41 }}>
              {t('mgmt.openId.fork.add', 'Add')}
            </Button>
          </Stack>
          {tried && problems.filter((p) => p[0].startsWith('mgmt.openId.fork.redirect')).slice(0, 2).map((p) => <FormHelperText key={p[0]} error>{tr(t, p)}</FormHelperText>)}
          {proposalHeld && <ProposalBox
            title={t('mgmt.openId.fork.proposalTitle', 'Proposed: {{list}}', { list: proposal.redirects.join(', ') })}
            source={tr(t, proposal.source)}
            onUse={() => { setRedirects(proposal.redirects); setMarks({ ...marks, redirects: proposal.redirects }); }}
            useLabel={t('mgmt.openId.fork.useProposal', 'Use these addresses')}
          />}
        </Stack>

        <ChoiceChips
          id="fork-openid-kind"
          label={t('mgmt.openId.fork.kindLabel', 'Kind of app')}
          value={isPublic ? 'public' : 'server'}
          onChange={(value) => setPublic(value === 'public')}
          options={[
            { value: 'server', label: t('mgmt.openId.fork.kindServer', 'Has a server part (most apps)'), description: t('mgmt.openId.fork.kindServerHint', 'The app keeps a secret that Cosmos makes: Immich, Nextcloud, Grafana and most self-hosted apps.') },
            { value: 'public', label: t('mgmt.openId.fork.kindPublic', 'Runs only in a browser or on a phone'), description: t('mgmt.openId.fork.kindPublicHint', 'No secret: it could not be kept hidden there. The app proves itself with PKCE instead (the app\'s documentation says "public client").') },
          ]}
        />

        {failure && <Alert severity="error" sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>{failure}</Alert>}
      </Stack>
    </DialogContent>
    <DialogActions sx={{ flexWrap: 'wrap', gap: 1 }}>
      <Button onClick={onClose}>{t('global.cancelAction')}</Button>
      <LoadingButton variant="contained" loading={busy} onClick={save}>
        {existing ? t('global.saveAction') : t('global.createAction')}
      </LoadingButton>
    </DialogActions>
  </Dialog>;
};

// One value to copy, with what it is for.
const CopyRow = ({ label, value, sentence, secret }) => {
  const { t } = useTranslation();
  const [copied, setCopied] = React.useState(false);
  const input = React.useRef(null);
  const copy = () => {
    const done = () => { setCopied(true); setTimeout(() => setCopied(false), 2000); };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(value).then(done).catch(() => {});
    } else if (input.current) {
      // a page on plain http has no clipboard API: select it for the admin
      input.current.select();
      try { document.execCommand('copy'); done(); } catch (e) { /* selected at least */ }
    }
  };
  return <Stack spacing={0.5}>
    <Typography variant="subtitle2" component="div">{label}</Typography>
    <Stack direction="row" spacing={1} alignItems="center">
      <OutlinedInput
        inputRef={input}
        value={value}
        readOnly
        fullWidth
        size="small"
        inputProps={{ 'aria-label': label, spellCheck: false }}
        sx={{ fontFamily: 'monospace', ...(secret ? { fontWeight: 600 } : {}) }}
      />
      <Tooltip title={copied ? t('global.copied') : t('mgmt.openId.fork.copy', 'Copy {{what}}', { what: label })}>
        <IconButton onClick={copy} aria-label={t('mgmt.openId.fork.copy', 'Copy {{what}}', { what: label })}><CopyOutlined /></IconButton>
      </Tooltip>
    </Stack>
    {sentence && <FormHelperText sx={{ m: 0 }}>{sentence}</FormHelperText>}
  </Stack>;
};

// Every value the app needs, after a create or a new secret.
export const ForkOpenidValues = ({ result, config, onClose }) => {
  const { t } = useTranslation();
  const values = providerValues(panelPlace(config));
  return <Dialog open onClose={keepOnBackdrop(onClose)} maxWidth="sm" fullWidth>
    <DialogTitle>{t('mgmt.openId.fork.valuesTitle', 'What {{id}} needs', { id: result.id })}</DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ mt: 1, overflowWrap: 'anywhere' }}>
        {result.secret && <Alert severity="warning" sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>
          {t('mgmt.openId.fork.secretOnce', 'The secret is shown only now: copy it into the app before closing. Cosmos keeps only a fingerprint of it; if it is lost, "Reset secret" makes another one.')}
        </Alert>}
        <Typography variant="body2" color="textSecondary">
          {t('mgmt.openId.fork.valuesIntro', 'Enter these in the app\'s OpenID (or "OAuth", "SSO") settings. Most apps need the first three and the secret; they read the rest from the discovery address.')}
        </Typography>
        <CopyRow label={t('mgmt.openId.fork.discovery', 'Discovery address')} value={values.discovery}
          sentence={t('mgmt.openId.fork.discoveryHint', 'Apps call it "Discovery URL", "OpenID configuration URL" or "Well-known URL". It names every other address.')} />
        <CopyRow label={t('mgmt.openId.fork.issuer', 'Issuer')} value={values.issuer}
          sentence={t('mgmt.openId.fork.issuerHint', 'Apps call it "Issuer URL", "Provider URL" or "Authority": the discovery address without its end. It carries the port the panel is reached on.')} />
        <CopyRow label={t('mgmt.openId.fork.clientId', 'Client ID')} value={result.id}
          sentence={t('mgmt.openId.fork.clientIdHint', 'The name the app sends to say who it is.')} />
        {result.secret
          ? <CopyRow secret label={t('mgmt.openId.fork.secret', 'Client secret')} value={result.secret}
            sentence={t('mgmt.openId.fork.secretHint', 'Proves to Cosmos that the request comes from the app. Keep it in the app only.')} />
          : result.public && <FormHelperText>{t('mgmt.openId.fork.noSecret', 'No secret: this app signs in with PKCE (S256), which it does by itself when set up as a public client.')}</FormHelperText>}
        <CopyRow label={t('mgmt.openId.fork.scopes', 'Scopes')} value={values.scopes}
          sentence={t('mgmt.openId.fork.scopesHint', 'What the app may know about the user: the account, the name, the email address.')} />
        {(result.redirects || []).length > 0 && <CopyRow label={t('mgmt.openId.fork.redirects', 'Redirect URIs registered')} value={result.redirects.join(', ')}
          sentence={t('mgmt.openId.fork.redirectsHint', 'The app must send exactly one of these; a sign-in sent anywhere else is refused.')} />}
        <CopyRow label={t('mgmt.openId.fork.authorize', 'Sign-in page (authorization endpoint)')} value={values.authorize}
          sentence={t('mgmt.openId.fork.endpointsHint', 'Only for an app that asks for each address separately.')} />
        <CopyRow label={t('mgmt.openId.fork.token', 'Token endpoint')} value={values.token} />
        <CopyRow label={t('mgmt.openId.fork.userinfo', 'User info endpoint')} value={values.userinfo} />
      </Stack>
    </DialogContent>
    <DialogActions>
      <Button variant="contained" onClick={onClose}>{t('global.done')}</Button>
    </DialogActions>
  </Dialog>;
};

// "Reset secret" asks first: the app stops signing in until it has the new one.
export const ForkResetSecret = ({ id, onConfirm, onClose }) => {
  const { t } = useTranslation();
  return <Dialog open onClose={onClose} maxWidth="xs" fullWidth>
    <DialogTitle>{t('mgmt.openId.fork.resetTitle', 'Make a new secret for {{id}}?', { id })}</DialogTitle>
    <DialogContent>
      <Typography variant="body2">{t('mgmt.openId.fork.resetConfirm', 'A new secret replaces the one {{id}} has now: it cannot sign its users in until you enter the new one in it.', { id })}</Typography>
    </DialogContent>
    <DialogActions>
      <Button onClick={onClose}>{t('global.cancelAction')}</Button>
      <Button variant="contained" onClick={() => { onConfirm(); onClose(); }}>{t('mgmt.openId.fork.resetButton', 'Make a new secret')}</Button>
    </DialogActions>
  </Dialog>;
};
