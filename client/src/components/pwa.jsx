// Fork addition: the panel's side of the installable app (the worker is in
// client/sw/, the server's side in src/fork_pwa.go, the decisions in
// utils/pwaClient.js).
//
//   startPWA()          registers the worker when the server offers the app (its
//                       manifest exists on this address), removes it when not
//   <PWAUpdateNotice>   "a new version is ready: Reload", when a new build's worker waits
//   <PWAInstallButton>  in the header (in the menu's footer on phones), only while
//                       this browser can install the panel and it is not installed
//   <PWASettings>       the card in Configuration > Appearance: the switch, and how
//                       to install on each kind of device
import * as React from 'react';
import {
  Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Grid, IconButton, Link,
  Snackbar, Stack, Tooltip, Typography,
} from '@mui/material';
import { useTranslation } from 'react-i18next';

import MainCard from './MainCard';
import { CosmosCheckbox } from '../pages/config/users/formShortcuts';
import {
  installOffer, dismissedUntil, readDismissed, UPDATE_CHECK_MS,
} from '../utils/pwaClient';
import { PWA_CACHE_PREFIX, PWA_SCOPE } from '../utils/pwaPolicy';

const WORKER = PWA_SCOPE + 'sw.js';
const MANIFEST = PWA_SCOPE + 'manifest.webmanifest';
const DISMISS_KEY = 'ct-pwa-not-now';

const standalone = () => {
  try {
    return (window.matchMedia && (window.matchMedia('(display-mode: standalone)').matches
      || window.matchMedia('(display-mode: fullscreen)').matches))
      || window.navigator.standalone === true;
  } catch (e) {
    return false;
  }
};

const readStore = () => {
  try { return readDismissed(window.localStorage.getItem(DISMISS_KEY)); } catch (e) { return 0; }
};

// One state for the whole page, shared by the components below.
const state = {
  enabled: null,       // the server offers the app on this address (null: not known yet)
  prompt: null,        // Chromium's install event, kept for the button
  updateReady: false,  // a new build's worker is waiting
  workerError: null,   // why the worker could not be registered (an untrusted certificate, mostly)
  standalone: false,
  dismissedUntil: 0,
};
const listeners = new Set();
const publish = (change) => {
  Object.assign(state, change);
  listeners.forEach((notify) => notify({ ...state }));
};

if (typeof window !== 'undefined') {
  state.standalone = standalone();
  state.dismissedUntil = readStore();
  // Fired early, possibly before the panel is drawn: kept for the button,
  // instead of the browser's own banner.
  window.addEventListener('beforeinstallprompt', (event) => {
    event.preventDefault();
    publish({ prompt: event });
  });
  window.addEventListener('appinstalled', () => publish({ prompt: null }));
}

export const usePWA = () => {
  const [current, setCurrent] = React.useState({ ...state });
  React.useEffect(() => {
    listeners.add(setCurrent);
    setCurrent({ ...state });
    return () => { listeners.delete(setCurrent); };
  }, []);
  return current;
};

const supported = () => typeof window !== 'undefined' && window.isSecureContext
  && typeof navigator !== 'undefined' && 'serviceWorker' in navigator;

// The panel's worker and caches, gone (the switch is off, or this is not the
// panel's own address). Another program's worker or caches are not touched.
const removeWorker = async () => {
  try {
    const registrations = await navigator.serviceWorker.getRegistrations();
    const own = window.location.origin + PWA_SCOPE;
    await Promise.all(registrations.filter((r) => r.scope === own).map((r) => r.unregister()));
  } catch (e) { /* nothing registered */ }
  try {
    if (window.caches) {
      const names = await window.caches.keys();
      await Promise.all(names.filter((n) => n.startsWith(PWA_CACHE_PREFIX)).map((n) => window.caches.delete(n)));
    }
  } catch (e) { /* storage blocked */ }
};

let registration = null;

const watch = (reg) => {
  registration = reg;
  const ready = () => {
    // a worker that waits while another controls the page: a new build
    if (reg.waiting && navigator.serviceWorker.controller) publish({ updateReady: true });
  };
  ready();
  reg.addEventListener('updatefound', () => {
    const next = reg.installing;
    if (next) next.addEventListener('statechange', () => { if (next.state === 'installed') ready(); });
  });
  // ask the server for a new build now and then, and when the app comes back to the front
  const check = () => reg.update().catch(() => {});
  setInterval(check, UPDATE_CHECK_MS);
  let last = Date.now();
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible' && Date.now() - last > 60 * 1000) {
      last = Date.now();
      check();
    }
  });
};

let started = false;
export function startPWA() {
  if (started) return;
  started = true;
  if (import.meta.env && import.meta.env.DEV) return;
  if (!supported()) {
    publish({ workerError: typeof window !== 'undefined' && !window.isSecureContext ? 'insecure' : 'unsupported' });
    return;
  }
  // The server says whether it offers the app here: no manifest, no app.
  fetch(MANIFEST, { method: 'HEAD', cache: 'no-store', credentials: 'same-origin' }).then((answer) => {
    if (answer.status === 404) {
      publish({ enabled: false });
      return removeWorker();
    }
    if (!answer.ok) return null;
    publish({ enabled: true });
    return navigator.serviceWorker.register(WORKER, { scope: PWA_SCOPE, updateViaCache: 'none' })
      .then(watch)
      .catch((error) => publish({ workerError: (error && error.name) || 'error' }));
  }).catch(() => { /* the server did not answer: nothing to decide now */ });
}

// "Reload" on the notice: the waiting worker takes over, then the page loads
// once more from the server.
export function applyUpdate() {
  let done = false;
  const reload = () => { if (!done) { done = true; window.location.reload(); } };
  if (registration && registration.waiting) {
    navigator.serviceWorker.addEventListener('controllerchange', reload);
    registration.waiting.postMessage({ type: 'SKIP_WAITING' });
    setTimeout(reload, 4000);
  } else {
    reload();
  }
}

const offerFor = (current) => installOffer({
  ua: typeof navigator !== 'undefined' ? navigator.userAgent : '',
  touchPoints: typeof navigator !== 'undefined' ? navigator.maxTouchPoints : 0,
  standalone: current.standalone,
  prompt: !!current.prompt,
  enabled: current.enabled === true,
  dismissedUntil: current.dismissedUntil,
  now: Date.now(),
});

const notNow = () => {
  const until = dismissedUntil(Date.now());
  try { window.localStorage.setItem(DISMISS_KEY, String(until)); } catch (e) { /* kept for this page only */ }
  publish({ dismissedUntil: until });
};

const askBrowser = async (prompt) => {
  if (!prompt) return;
  try {
    await prompt.prompt();
    const choice = await prompt.userChoice;
    publish({ prompt: null });
    if (choice && choice.outcome === 'dismissed') notNow();
  } catch (e) {
    publish({ prompt: null });
  }
};

export const PWAUpdateNotice = () => {
  const { t } = useTranslation();
  const { updateReady } = usePWA();
  const [later, setLater] = React.useState(false);

  React.useEffect(() => { startPWA(); }, []);

  return (
    <Snackbar open={updateReady && !later} anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }} className="ct-pwa-update">
      <Alert
        severity="info"
        variant="outlined"
        sx={{ alignItems: 'center', bgcolor: 'background.paper' }}
        action={(
          <Stack direction="row" spacing={1}>
            <Button size="small" onClick={() => setLater(true)}>{t('fork.pwa.later', 'Later')}</Button>
            <Button size="small" variant="contained" onClick={applyUpdate}>{t('fork.pwa.reload', 'Reload')}</Button>
          </Stack>
        )}
      >
        {t('fork.pwa.updateReady', 'A new version of Cosmos is ready.')}
      </Alert>
    </Snackbar>
  );
};

// A screen with an arrow into it: "put this on the device".
const InstallPicture = () => (
  <svg xmlns="http://www.w3.org/2000/svg" width="1em" height="1em" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ fontSize: '1.15em' }}>
    <rect x="3" y="4" width="18" height="13" rx="2" />
    <path d="M8 21h8M12 17v4M12 7.5v6M9.5 11l2.5 2.5 2.5-2.5" />
  </svg>
);

const Steps = ({ offer }) => {
  const { t } = useTranslation();
  const steps = offer === 'ios' ? [
    t('fork.pwa.ios.step1', 'Tap the Share button of the browser (a square with an arrow pointing up). On an iPhone it is at the bottom of the screen, on an iPad at the top.'),
    t('fork.pwa.ios.step2', 'Choose "Add to Home Screen". Scroll down the list if you do not see it.'),
    t('fork.pwa.ios.step3', 'Tap "Add". Cosmos then opens from its own icon, in a window of its own.'),
  ] : [
    t('fork.pwa.mac.step1', 'In the menu bar, choose File, then "Add to Dock".'),
    t('fork.pwa.mac.step2', 'Click "Add". Cosmos then opens from the Dock, in a window of its own.'),
  ];
  return (
    <Box component="ol" sx={{ pl: 3, my: 1 }}>
      {steps.map((step) => <Typography component="li" key={step} sx={{ mb: 1 }}>{step}</Typography>)}
    </Box>
  );
};

export const PWAInstallDialog = ({ open, offer, onClose }) => {
  const { t } = useTranslation();
  return (
    <Dialog open={open} onClose={onClose} maxWidth="xs" fullWidth>
      <DialogTitle>{t('fork.pwa.dialogTitle', 'Install Cosmos as an app')}</DialogTitle>
      <DialogContent>
        <Typography sx={{ mb: 1 }}>
          {offer === 'ios'
            ? t('fork.pwa.ios.intro', 'iPhone and iPad install apps from a web page through the Share menu:')
            : t('fork.pwa.mac.intro', 'Safari on a Mac adds web apps to the Dock:')}
        </Typography>
        <Steps offer={offer} />
      </DialogContent>
      <DialogActions>
        <Button onClick={() => { notNow(); onClose(); }}>{t('fork.pwa.notNow', 'Not now')}</Button>
        <Button variant="contained" onClick={onClose}>{t('global.close', 'Close')}</Button>
      </DialogActions>
    </Dialog>
  );
};

export const PWAInstallButton = ({ sx, size }) => {
  const { t } = useTranslation();
  const current = usePWA();
  const [open, setOpen] = React.useState(false);
  const offer = offerFor(current);
  if (!offer && !open) return null;

  const label = t('fork.pwa.install', 'Install Cosmos as an app');
  return (
    <>
      <Tooltip title={label}>
        <IconButton
          disableRipple
          color="secondary"
          size={size}
          aria-label={label}
          sx={{ color: 'text.primary', ...(sx || {}) }}
          onClick={() => (offer === 'prompt' ? askBrowser(current.prompt) : setOpen(true))}
        >
          <InstallPicture />
        </IconButton>
      </Tooltip>
      <PWAInstallDialog open={open} offer={offer || 'ios'} onClose={() => setOpen(false)} />
    </>
  );
};

// Configuration > Appearance. The switch is the server's: off, the browsers
// that installed the panel remove its worker at their next visit.
export const PWASettings = ({ formik }) => {
  const { t } = useTranslation();
  const current = usePWA();
  const offer = offerFor({ ...current, dismissedUntil: 0 });
  const [open, setOpen] = React.useState(false);

  let here = null;
  if (current.standalone) {
    here = <Alert severity="success">{t('fork.pwa.here.installed', 'You are using the installed app.')}</Alert>;
  } else if (current.workerError === 'SecurityError' || current.workerError === 'insecure') {
    here = (
      <Alert severity="warning">
        {t('fork.pwa.here.certificate', 'This browser does not trust the certificate of this address, so the app cannot show its page for "server unreachable" and may not install. Use an address with a trusted certificate, or install the Cosmos certificate on this device:')}
        {' '}<Link href="/cosmos-ui/trust">{t('fork.pwa.here.trustLink', 'how to trust the certificate')}</Link>
      </Alert>
    );
  } else if (offer === 'prompt') {
    here = (
      <Alert severity="info" action={<Button size="small" variant="contained" onClick={() => askBrowser(current.prompt)}>{t('fork.pwa.installNow', 'Install')}</Button>}>
        {t('fork.pwa.here.prompt', 'This browser can install the panel now.')}
      </Alert>
    );
  } else if (offer === 'ios' || offer === 'mac-safari') {
    here = (
      <Alert severity="info" action={<Button size="small" onClick={() => setOpen(true)}>{t('fork.pwa.howTo', 'How')}</Button>}>
        {t('fork.pwa.here.manual', 'This browser installs the panel from its own menu.')}
      </Alert>
    );
  }

  return (
    <MainCard title={t('fork.pwa.title', 'App on your devices')}>
      <Grid container spacing={2}>
        <CosmosCheckbox
          name="OfferWebApp"
          formik={formik}
          label={t('fork.pwa.switchLabel', 'Offer the panel as an installable app')}
          helperText={t('fork.pwa.switchHelp', 'The panel can then be installed on computers, phones and tablets and opens in a window of its own. It keeps only its own program files on the device, never your data, and shows a "server unreachable" page instead of an error when the server cannot be reached. Switched off, devices that installed it remove those files at their next visit.')}
        />
        {here && <Grid item xs={12}>{here}</Grid>}
        <Grid item xs={12}>
          <Typography variant="h6" sx={{ mb: 1 }}>{t('fork.pwa.howTitle', 'How to install it')}</Typography>
          <Box component="ul" sx={{ pl: 3, m: 0, '& li': { mb: 0.75 } }}>
            <Typography component="li">{t('fork.pwa.how.chromium', 'Chrome and Edge, on a computer or Android: the install button in the address bar or in the panel\'s header, or the browser menu, "Install Cosmos".')}</Typography>
            <Typography component="li">{t('fork.pwa.how.ios', 'iPhone and iPad (any browser): Share, then "Add to Home Screen".')}</Typography>
            <Typography component="li">{t('fork.pwa.how.mac', 'Safari on a Mac: File, then "Add to Dock".')}</Typography>
            <Typography component="li">{t('fork.pwa.how.firefox', 'Firefox: on Android, the menu, "Add to home screen"; on a computer Firefox does not install web apps, use the panel in a tab.')}</Typography>
            <Typography component="li">{t('fork.pwa.how.certificate', 'Every browser needs a certificate it trusts for the app to work fully: a public one, or the Cosmos certificate installed on the device.')}</Typography>
          </Box>
        </Grid>
      </Grid>
      <PWAInstallDialog open={open} offer={offer === 'mac-safari' ? 'mac-safari' : 'ios'} onClose={() => setOpen(false)} />
    </MainCard>
  );
};
