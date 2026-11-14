// Fork addition: the decisions of the panel's side of the installable app,
// as plain functions (tested in pwaClient.test.mjs): when to try the server
// again from the offline page, whether it is back, and which way of
// installing this browser offers. The browser calls are in
// components/pwa.jsx and offline/offline.js.

// Seconds before the next automatic try from the offline page: soon at
// first, then every 30 s (a restarting server is back within a minute; a
// server that is off should not be asked in a loop).
export const retryDelay = (attempt) => {
  const steps = [5, 10, 15, 30];
  const n = Number.isInteger(attempt) && attempt > 0 ? attempt : 0;
  return steps[Math.min(n, steps.length - 1)];
};

// Did the server answer? Any answer of Cosmos itself counts (a sign-in page,
// a refusal); what a proxy in front of it says when it is not there does not
// (502, 503, 504, Cloudflare's 52x and 530).
export const serverBack = (status) => {
  if (!Number.isInteger(status) || status < 100) return false;
  if (status === 502 || status === 503 || status === 504) return false;
  if (status >= 520 && status <= 530) return false;
  return true;
};

const ios = (ua, touchPoints) => /\b(iPhone|iPad|iPod)\b/.test(ua) || (/\bMacintosh\b/.test(ua) && touchPoints > 1);
const macSafari = (ua, touchPoints) => /\bMacintosh\b/.test(ua) && touchPoints <= 1
  && /\bVersion\/\d+.*\bSafari\//.test(ua) && !/\b(Chrome|Chromium|CriOS|Edg|EdgiOS|Firefox|FxiOS|OPR)\//.test(ua);

// What the panel offers for installing it:
//   'prompt'      the browser can install it at the press of a button (Chromium: beforeinstallprompt)
//   'ios'         iPhone and iPad: Share, then "Add to Home Screen" (every browser there)
//   'mac-safari'  Safari on a Mac: File, then "Add to Dock"
//   null          already installed, switched off, dismissed for now, or this browser cannot install
// env: { ua, touchPoints, standalone, prompt (bool), enabled, dismissedUntil (ms), now (ms) }
export const installOffer = (env) => {
  if (!env || !env.enabled || env.standalone) return null;
  if (Number(env.dismissedUntil) > Number(env.now)) return null;
  if (env.prompt) return 'prompt';
  const ua = String(env.ua || '');
  const touchPoints = Number(env.touchPoints) || 0;
  if (ios(ua, touchPoints)) return 'ios';
  if (macSafari(ua, touchPoints)) return 'mac-safari';
  return null;
};

// What "Not now" means: the offer comes back after 30 days.
export const DISMISS_DAYS = 30;
export const dismissedUntil = (now) => Number(now) + DISMISS_DAYS * 24 * 60 * 60 * 1000;

// Reads what was stored by "Not now", tolerating anything else in its place.
export const readDismissed = (stored) => {
  const n = Number(stored);
  return Number.isFinite(n) && n > 0 ? n : 0;
};

// How often the panel asks the server whether there is a new worker (a new release).
export const UPDATE_CHECK_MS = 30 * 60 * 1000;
