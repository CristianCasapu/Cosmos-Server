// Fork addition: fills a throwaway Cosmos instance so that it looks like a
// small home server, for scripts/fork-ui-gallery.mjs. Never point it at the
// production instance: it finishes the setup wizard and writes settings.
//
// It expects (see CLAUDE.md, "Checking the UI in a browser"):
//   - the instance at BASE (default http://polish.test:18080), fresh or
//     already seeded (every step is skipped when its result exists);
//   - a Docker of the instance's own (docker:27-dind) holding a handful of
//     containers named jellyfin, radarr, sonarr, qbittorrent, vaultwarden,
//     pihole (the last two stopped) and one with a long name.
//
// Run it from the Playwright image on the instance's network:
//   node scripts/fork-ui-gallery-seed.mjs
// HOME_BACKGROUND=<file> sets the home page's background from a file holding
// a data: URL; THEME_PRIMARY / THEME_SECONDARY set the accent colours.
//
// The security pages get their rows from scripts/fork-ui-gallery-noise.sh,
// which must run from other addresses than this script's.
import { request } from 'playwright';
import { readFileSync } from 'node:fs';

const base = process.env.BASE || 'http://polish.test:18080';
const host = new URL(base).hostname;
const admin = { nickname: 'admin', password: process.env.ADMIN_PASSWORD || 'Test-Passw0rd!x' };
const note = (...a) => console.log(...a);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const api = await request.newContext({ baseURL: base });
const call = async (method, path, data) => {
  let r;
  // the last step of the wizard and a save of the settings end the process: no answer then
  try { r = await api.fetch('/cosmos/api/' + path, { method, data, failOnStatusCode: false }); } catch (e) { return { status: 0, body: null }; }
  let body = null;
  try { body = await r.json(); } catch (e) { /* not JSON */ }
  return { status: r.status(), body };
};
const waitUp = async () => {
  for (let i = 0; i < 60; i++) {
    if ((await call('GET', 'status')).status > 0) return; // 401 once the wizard is done: it answers, that is enough
    await sleep(1000);
  }
  throw new Error('the instance did not come back');
};

// --- the setup wizard (the server answers 403 to it once it is done)
await waitUp();
const first = await call('POST', 'newInstall', { step: '2' });
if (first.status === 200) {
  note('wizard: 2', first.status);
  note('wizard: 3', (await call('POST', 'newInstall', { step: '3', httpsCertificateMode: 'DISABLED', hostname: host })).status);
  await sleep(1500);
  note('wizard: 4', (await call('POST', 'newInstall', { step: '4', ...admin, email: 'admin@example.org' })).status);
  note('wizard: 5', (await call('POST', 'newInstall', { step: '5' })).status);
  await sleep(6000);
  await waitUp();
}

const login = async () => {
  const l = await call('POST', 'login', admin);
  const s = await call('POST', 'sudo', { password: admin.password });
  note('login', l.status, 'sudo', s.status);
};
await login();

// --- settings: colours, home background, three URLs
let config = (await call('GET', 'config')).body.data;
const routes = config.HTTPConfig.ProxyConfig.Routes || [];
const wanted = [
  { Name: 'Jellyfin', Description: 'Films and series', UseHost: true, Host: 'jellyfin.' + host, Target: 'http://jellyfin:8096', Mode: 'SERVAPP', AuthEnabled: true },
  { Name: 'Vaultwarden', Description: 'Passwords', UseHost: true, Host: 'vault.' + host, Target: 'http://vaultwarden:80', Mode: 'SERVAPP', AuthEnabled: false, BlockCommonBots: true },
  { Name: 'Router', Description: 'The router\'s own page', UseHost: true, Host: 'router.' + host, Target: 'http://192.168.1.1', Mode: 'PROXY', AuthEnabled: true, AdminOnly: true },
  // answered for real by a container's published port, so that the proxy charts have traffic
  { Name: 'Sonarr', Description: 'Series', UseHost: true, Host: 'sonarr.' + host, Target: process.env.LIVE_TARGET || 'http://cosmos-polish-dind:8989', Mode: 'PROXY', AuthEnabled: false },
];
let changed = false;
for (const route of wanted) {
  if (routes.some((r) => r.Name === route.Name)) continue;
  routes.push({ Timeout: 14400000, ThrottlePerMinute: 10000, SmartShield: { Enabled: true }, ...route });
  changed = true;
}
config.HTTPConfig.ProxyConfig.Routes = routes;
const primary = process.env.THEME_PRIMARY || 'rgba(64, 132, 255, 1)';
const secondary = process.env.THEME_SECONDARY || 'rgba(14, 150, 184, 1)';
if (!config.ThemeConfig || config.ThemeConfig.PrimaryColor !== primary) {
  config.ThemeConfig = { ...(config.ThemeConfig || {}), PrimaryColor: primary, SecondaryColor: secondary };
  changed = true;
}
if (process.env.HOME_BACKGROUND && !(config.HomepageConfig && config.HomepageConfig.Background)) {
  config.HomepageConfig = { ...(config.HomepageConfig || {}), Background: readFileSync(process.env.HOME_BACKGROUND, 'utf8').trim() };
  changed = true;
}
if (changed) {
  note('settings saved:', (await call('PUT', 'config', config)).status);
  await sleep(5000);
  await waitUp();
  await login();
}

// --- a second user who finished the invitation, a third who did not
const users = ((await call('GET', 'users')).body.data || []).map((u) => u.nickname || u.Nickname);
for (const [nickname, email, register] of [['maria', 'maria@example.org', true], ['bunica', 'bunica@example.org', false]]) {
  if (users.includes(nickname)) continue;
  const made = await call('POST', 'users', { nickname, email });
  const key = made.body && made.body.data && (made.body.data.registerKey || made.body.data.RegisterKey);
  note('user', nickname, made.status, key ? 'invited' : JSON.stringify(made.body).slice(0, 200));
  if (register && key) {
    const visitor = await request.newContext({ baseURL: base });
    const r = await visitor.post('/cosmos/api/register', { data: { nickname, registerKey: key, password: 'Other-Passw0rd!y' }, failOnStatusCode: false });
    note('  registered', r.status());
    const l = await visitor.post('/cosmos/api/login', { data: { nickname, password: 'Other-Passw0rd!y' }, failOnStatusCode: false });
    note('  logged in once', l.status());
    await visitor.dispose();
  }
}

// --- a scheduled job and an API token, so that those lists are not empty
const cron = (await call('GET', 'cron')).body;
note('cron list', JSON.stringify(cron).slice(0, 120));

// --- a few container actions, for the events and the audit page
for (const [name, action] of [['radarr', 'restart'], ['pihole', 'start'], ['pihole', 'stop']]) {
  note('container', name, action, (await call('GET', `servapps/${name}/manage/${action}`)).status);
  await sleep(800);
}

await api.dispose();
note('seeded');
