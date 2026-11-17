// Fork addition (plan 47): the one-time setup code of a new installation
// (src/fork_setup_code.go). The wizard asks for it first; while it is held,
// every request to the API carries it (api/client.ts), and the server
// refuses the setup routes without it. Pure but for the small keeper below,
// tested by setupCode.test.mjs.

export const SETUP_CODE_HEADER = 'X-Cosmos-Setup-Code';
const ALPHABET = 'ABCDEFGHJKMNPQRSTVWXYZ23456789';
const LENGTH = 20;
const KEY = 'cosmos-setup-code';

// What someone typed or pasted, as the bare code (upper case, no spaces,
// dashes or underscores), or '' when it cannot be one. The look-alikes the
// code never holds are named, so the error can say which sign is wrong.
export const normalizeSetupCode = (input) => {
  const text = String(input || '').toUpperCase().replace(/[\s_-]+/g, '');
  if (text.length !== LENGTH) return '';
  for (const c of text) if (!ALPHABET.includes(c)) return '';
  return text;
};

// The reason a typed code is not one, as [key, English] for t(), or null.
export const setupCodeProblem = (input) => {
  const text = String(input || '').toUpperCase().replace(/[\s_-]+/g, '');
  if (!text) return ['newInstall.setupCode.empty', 'Enter the setup code.'];
  const bad = [...text].find((c) => !ALPHABET.includes(c));
  if (bad) {
    if ('01ILOU'.includes(bad)) return ['newInstall.setupCode.lookAlike', 'The code never holds 0, 1, I, L, O or U: look again at the sign that looks like one of them.'];
    return ['newInstall.setupCode.badSign', 'The code holds letters and digits only.'];
  }
  if (text.length !== LENGTH) return ['newInstall.setupCode.length', 'The code has 20 letters and digits (five groups of four).'];
  return null;
};

export const formatSetupCode = (code) => (normalizeSetupCode(code).match(/.{1,4}/g) || []).join('-');

// The answer of the server to a check of the code, as [key, English, values].
export const setupCodeAnswer = (status, code, retryAfter) => {
  if (code === 'SC002') return ['newInstall.setupCode.wrong', 'This is not the setup code of this server.', {}];
  if (code === 'SC003' || status === 429) return ['newInstall.setupCode.wait', 'Too many tries: wait {{seconds}} seconds and try again.', { seconds: String(Math.max(1, parseInt(retryAfter, 10) || 60)) }];
  if (code === 'SC004') return ['newInstall.setupCode.noFile', 'The server cannot read its setup code file. Restart Cosmos on the server (sudo systemctl restart CosmosCloud) to make a new one.', {}];
  if (code === 'NI001' || status === 403) return ['newInstall.setupCode.done', 'This server is already set up. Sign in instead.', {}];
  return ['newInstall.setupCode.noAnswer', 'The server did not answer. Check that Cosmos runs, then try again.', {}];
};

// The keeper: the code lives in this tab only (sessionStorage), and only
// until the setup is finished.
let held = '';
const store = () => { try { return typeof sessionStorage !== 'undefined' ? sessionStorage : null; } catch { return null; } };

export const keepSetupCode = (code) => {
  held = normalizeSetupCode(code);
  try { const s = store(); if (s) { if (held) s.setItem(KEY, held); else s.removeItem(KEY); } } catch { /* private window */ }
};
export const heldSetupCode = () => {
  if (held) return held;
  try { const s = store(); held = normalizeSetupCode(s ? s.getItem(KEY) : ''); } catch { held = ''; }
  return held;
};
export const forgetSetupCode = () => keepSetupCode('');

// The header for a request: only to Cosmos' own API, only while a code is held.
export const setupCodeHeaders = (path) => {
  const code = heldSetupCode();
  return code && /^\/cosmos\/api\//.test(String(path || '')) ? { [SETUP_CODE_HEADER]: code } : {};
};
