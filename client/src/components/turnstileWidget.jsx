// Fork addition: Cloudflare's Turnstile check on the panel's public forms
// (src/turnstile; logic and texts in utils/turnstile.js). One line in each
// sign-in page:
//
//   <TurnstileWidget form="login" />     (also "reset", "register")
//
// It shows nothing unless the server says the check applies to this visitor
// (GET /cosmos/api/can-send-email: never at home, never when it is off).
// Then it loads Cloudflare's script once (explicit rendering), renders the
// widget in the panel's light or dark mode and language, keeps the token in
// the store the API client takes it from (api/client.ts), and says in words
// what is going on: checking, passed, blocked by the browser, refused by the
// server. The form's button stays active: a refusal is explained, never a
// dead button.
//
// TurnstileBox is the bare widget, for the settings card's trial.
import * as React from 'react';
import { Alert, Box, Button, Stack, Typography } from '@mui/material';
import { useTheme } from '@mui/material/styles';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import {
  SCRIPT_URL, canRetry, clearToken, refusalText, renderOptions, setToken, statusText, subscribe, widgetErrorKind,
} from '../utils/turnstile';

const say = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');

// --- the script, loaded once per page; a failure can be tried again
let scriptPromise = null;
const SCRIPT_TIMEOUT = 15000;

export const loadTurnstile = () => {
  if (typeof window === 'undefined') return Promise.reject(new Error('blocked'));
  if (window.turnstile && typeof window.turnstile.render === 'function') return Promise.resolve(window.turnstile);
  if (scriptPromise) return scriptPromise;
  scriptPromise = new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = SCRIPT_URL;
    script.async = true;
    // no Referer beyond the origin, no cookies of ours: it is another site
    script.referrerPolicy = 'strict-origin';
    const timer = setTimeout(() => { scriptPromise = null; reject(new Error('blocked')); }, SCRIPT_TIMEOUT);
    script.onload = () => {
      clearTimeout(timer);
      if (window.turnstile && typeof window.turnstile.render === 'function') resolve(window.turnstile);
      else { scriptPromise = null; reject(new Error('blocked')); }
    };
    script.onerror = () => {
      clearTimeout(timer);
      scriptPromise = null;
      script.remove();
      reject(new Error('blocked'));
    };
    document.head.appendChild(script);
  });
  return scriptPromise;
};

// TurnstileBox renders one widget. onToken(token|null) follows its state;
// resetKey: a change makes it start again.
export const TurnstileBox = ({ siteKey, form, appearance, onToken, onState, resetKey }) => {
  const theme = useTheme();
  const { i18n } = useTranslation();
  const holder = React.useRef(null);
  const widget = React.useRef(null);
  const callbacks = React.useRef({ onToken, onState });
  callbacks.current = { onToken, onState };
  const mode = theme.palette.mode;
  const language = i18n && i18n.language;

  React.useEffect(() => {
    let gone = false;
    const state = (s) => { if (!gone && callbacks.current.onState) callbacks.current.onState(s); };
    const token = (tk) => { if (!gone && callbacks.current.onToken) callbacks.current.onToken(tk); };
    state('loading');
    loadTurnstile().then((turnstile) => {
      if (gone || !holder.current) return;
      try {
        widget.current = turnstile.render(holder.current, {
          ...renderOptions({ siteKey, form, mode, language, appearance }),
          callback: (value) => { token(value); state('ready'); },
          'expired-callback': () => { token(null); state('expired'); },
          'timeout-callback': () => { token(null); state('timeout'); },
          'unsupported-callback': () => { token(null); state('unsupported'); },
          'error-callback': (code) => { token(null); state(widgetErrorKind(code)); return true; },
          'before-interactive-callback': () => state('checking'),
        });
        state('checking');
      } catch (e) {
        state('failed');
      }
    }).catch(() => state('blocked'));
    return () => {
      gone = true;
      if (widget.current !== null && window.turnstile) {
        try { window.turnstile.remove(widget.current); } catch (e) { /* already gone */ }
      }
      widget.current = null;
    };
  }, [siteKey, form, mode, language, appearance, resetKey]);

  return <Box ref={holder} sx={{ width: '100%', maxWidth: '100%', overflow: 'hidden', '&:empty': { display: 'none' } }} data-turnstile-box={form} />;
};

// the public settings, read once per page
let publicPromise = null;
const readPublic = () => {
  if (!publicPromise) {
    publicPromise = Promise.resolve(API.config.canSendEmail())
      .then((answer) => (answer && answer.data && answer.data.turnstile) || null)
      .catch(() => null)
      .finally(() => { setTimeout(() => { publicPromise = null; }, 30000); });
  }
  return publicPromise;
};

const TurnstileWidget = ({ form }) => {
  const { t } = useTranslation();
  const [settings, setSettings] = React.useState(null);
  const [state, setState] = React.useState('loading');
  const [refusal, setRefusal] = React.useState('');
  const [resetKey, setResetKey] = React.useState(0);

  React.useEffect(() => {
    let gone = false;
    readPublic().then((value) => { if (!gone) setSettings(value); });
    return () => { gone = true; clearToken(form); };
  }, [form]);

  React.useEffect(() => subscribe((event) => {
    if (event.form !== form) return;
    if (event.type === 'spent') {
      // single use: the next attempt needs a new check
      setRefusal('');
      setState('checking');
      setResetKey((k) => k + 1);
    } else if (event.type === 'refused') {
      setRefusal(event.code);
      if (event.code === 'TS001' || event.code === 'TS002') setResetKey((k) => k + 1);
    }
  }), [form]);

  if (!settings || !settings.forms || !settings.forms[form]) return null;

  const status = statusText(state, typeof window !== 'undefined' ? window.location.hostname : '');
  const problem = !['loading', 'checking', 'ready'].includes(state);
  // "sent before the check was done" says nothing new when the line already says why the check is missing
  const refused = problem && refusal === 'TS001' ? null : refusalText(refusal);

  return <Stack spacing={1} sx={{ width: '100%', minWidth: 0 }} data-turnstile={form} data-turnstile-state={state}>
    <TurnstileBox
      siteKey={settings.siteKey}
      form={form}
      appearance={settings.appearance}
      resetKey={resetKey}
      onToken={(token) => (token ? setToken(form, token) : clearToken(form))}
      onState={setState}
    />
    <Typography
      variant="caption"
      color={problem ? 'error' : 'textSecondary'}
      role="status"
      aria-live="polite"
      sx={{ overflowWrap: 'anywhere' }}
    >
      {say(t, status)}
    </Typography>
    {canRetry(state) && <Button size="small" variant="outlined" sx={{ alignSelf: 'flex-start' }} onClick={() => setResetKey((k) => k + 1)}>
      {t('turnstile.widget.retry', 'Check again')}
    </Button>}
    {state === 'blocked' && <Button size="small" variant="outlined" sx={{ alignSelf: 'flex-start' }} onClick={() => setResetKey((k) => k + 1)}>
      {t('turnstile.widget.reload', 'Try to load it again')}
    </Button>}
    {refused && <Alert severity="warning" role="alert" sx={{ overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } }}>{say(t, refused)}</Alert>}
  </Stack>;
};

export default TurnstileWidget;
