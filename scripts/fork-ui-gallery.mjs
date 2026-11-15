// Fork addition: a picture of every page of the panel, in light and dark mode
// and at three screen sizes, with a JSON summary of what a script can measure
// on each (script errors, sideways scroll, text that is cut off, overlapping
// controls, text below 12 px, WCAG contrast failures, buttons without a name,
// small touch targets). The file names are stable, so two runs (before and
// after a change) can be compared picture by picture.
//
// It needs a throwaway instance (CLAUDE.md, "Checking the UI in a browser")
// filled by scripts/fork-ui-gallery-seed.mjs and scripts/fork-ui-gallery-noise.sh.
// Never point it at the production instance.
//
//   node scripts/fork-ui-gallery.mjs
//     BASE=http://polish.test:18080   the instance (WebKit needs a name with a dot)
//     OUT=gallery                     where the pictures go: <OUT>/<engine>-<width>x<height>-<mode>/<view>.png + .json
//     ENGINES=chromium                chromium,firefox,webkit
//     VIEWPORTS=1920x1080,1366x768,390x844
//     MODES=light,dark
//     SET=all                         "small": the views marked core only (for the other engines)
//     ONLY=<regexp>                   only the views whose name matches
//     API_PER_MINUTE=420              the instance allows 500 API calls a minute per address; the script
//                                     counts its own and waits before it would go over
//     STANDALONE=1                    as the installed app: the page is told it runs in its own window
//                                     (display-mode: standalone, navigator.standalone), in every engine
//     SAFE_AREA=47,0,34,0             top,right,bottom,left in px: the parts of the screen a phone keeps for
//                                     itself (notch, home indicator). The skin's --ct-safe-* take these values
//                                     and the pictures show the two zones hatched
//     SERVICE_WORKERS=block           no service worker (Firefox and WebKit: the views' own answers then reach the page)
//     SHOT=viewport                   one screen per picture instead of the whole page (default: full), so
//                                     that bars fixed to the bottom are where a user sees them
//   node scripts/fork-ui-gallery.mjs summary     only rebuild <OUT>/summary.json from the .json files
//
// Each engine/viewport/mode combination is independent: run them as separate
// containers (their addresses differ, so each has its own API allowance) and
// the whole gallery takes minutes instead of an hour.
import { chromium, firefox, webkit } from 'playwright';
import { mkdirSync, writeFileSync, readdirSync, readFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';

const base = process.env.BASE || 'http://polish.test:18080';
const out = process.env.OUT || 'gallery';
const engineNames = (process.env.ENGINES || 'chromium').split(',');
const viewports = (process.env.VIEWPORTS || '1920x1080,1366x768,390x844').split(',').map((v) => {
  const [width, height] = v.split('x').map(Number);
  return { width, height };
});
const modes = (process.env.MODES || 'light,dark').split(',');
const smallSet = process.env.SET === 'small';
const only = process.env.ONLY ? new RegExp(process.env.ONLY) : null;
const apiPerMinute = Number(process.env.API_PER_MINUTE || 420);
const standalone = process.env.STANDALONE === '1';
const safeArea = process.env.SAFE_AREA ? process.env.SAFE_AREA.split(',').map(Number) : null;
const viewportShots = process.env.SHOT === 'viewport';
const admin = { nickname: 'admin', password: process.env.ADMIN_PASSWORD || 'Test-Passw0rd!x' };
const engines = { chromium, firefox, webkit };
const note = (...a) => console.log(...a);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---------------------------------------------------------------------------
// The views. One view is one page load; its captures are taken one after the
// other on that page (the page as loaded, then tabs and dialogs), each into
// <view>.png or <view>--<capture>.png.
//   tab: n          the n-th tab of the page's tab list (a select on a phone)
//   open: fn        do something first (open a dialog, hover, press keys)
//   at: selector    one screen with the first match (its card) just under the top bar, instead of the whole page;
//                   prev:<selector> the card before that one
//   close: fn       undo it (default after `open`: Escape)
//   core: true      part of the small set
//   mock: [[path, body, status, headers]]  answer the calls under `path` (any origin) with `body` instead of asking the server
//   patch: [[path, fn]]   ask the server, then change its JSON answer with fn(json, url) before the page gets it
// A step that cannot be done is reported in the summary, not fatal.
// ---------------------------------------------------------------------------
// the page's main action ("Create", "New ..."): the first filled button with a plus, above the list
const create = async (page) => { await page.locator('main button.MuiButton-contained:has(.anticon-plus-circle), main button.MuiButton-contained:has(.anticon-plus)').first().click({ timeout: 8000 }); };

const anonymousViews = [
  { id: 'auth-login', path: '/cosmos-ui/login', core: true, captures: [
    {},
    { id: 'empty-submit', open: async (page) => { await page.locator('button[type=submit]').first().click(); }, close: async () => {} },
    { id: 'keyboard-focus', open: async (page) => { await page.locator('#nickname-login').focus(); await page.keyboard.press('Tab'); await page.keyboard.press('Tab'); await page.keyboard.press('Tab'); }, close: async () => {} },
  ] },
  { id: 'auth-forgot-password', path: '/cosmos-ui/forgot-password' },
  { id: 'auth-register', path: '/cosmos-ui/register?t=0000000000000000&nickname=newuser' },
  // the links an invitation and a password reset e-mail open
  { id: 'auth-invite', path: '/cosmos-ui/register?t=2&nickname=newuser&key=0000000000000000' },
  { id: 'auth-reset-link', path: '/cosmos-ui/register?t=0&nickname=admin&key=0000000000000000' },
  // what the app gate and an ended session send a visitor to
  { id: 'auth-login-notlogged', path: '/cosmos-ui/login?notlogged=1&redirect=%2Fcosmos-ui%2Fservapps' },
  { id: 'auth-login-notadmin', path: '/cosmos-ui/login?notlogged=2' },
  { id: 'auth-login-invalid', path: '/cosmos-ui/login?invalid=1' },
  // the two-factor pages: the session waits for its code (the server answers "me" with a refusal then)
  { id: 'auth-loginmfa', path: '/cosmos-ui/loginmfa?redirect=%2Fcosmos-ui%2F', mock: [['/cosmos/api/me', { status: 'error', message: 'User requires MFA', code: 'HTTP006' }, 401]] },
  { id: 'auth-newmfa', path: '/cosmos-ui/newmfa', mock: [['/cosmos/api/me', { status: 'error', message: 'User requires MFA', code: 'HTTP006' }, 401], ['/cosmos/api/mfa', { status: 'OK', data: { key: 'otpauth://totp/Cosmos:admin?secret=JBSWY3DPEHPK3PXP&issuer=Cosmos' } }]] },
  // the first-run wizard, as a fresh server shows it
  { id: 'setup-wizard', path: '/cosmos-ui/newInstall', mock: [['/cosmos/api/status', { status: 'OK', data: { newInstall: true, database: true, docker: true, letsencrypt: false, hostmode: true, hostname: '0.0.0.0', containerized: false } }]], captures: [
    {},
    { id: 'step-2', open: async (page) => { await page.locator('main button, button').filter({ hasText: /^(Start|Next)/ }).last().click(); }, close: async () => {} },
  ] },
  // the panel when the server does not answer at all
  { id: 'server-unreachable', path: '/cosmos-ui/', abort: ['/cosmos/api/'], wait: 2500 },
  { id: 'offline-page', path: '/cosmos-ui/offline.html' },
  { id: 'not-found-anonymous', path: '/cosmos-ui/nothing-here' },
];

// the Market, from a stand-in answer (the test instance has no way out): pictures as SVG data: URLs,
// the install review with a name the server cannot look up (a server without DNS)
const svg = (a, b, text) => 'data:image/svg+xml;base64,' + Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="720"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="${a}"/><stop offset="1" stop-color="${b}"/></linearGradient></defs><rect width="1280" height="720" fill="url(#g)"/><rect x="60" y="60" width="1160" height="70" rx="12" fill="#fff" opacity=".35"/><rect x="60" y="170" width="360" height="480" rx="16" fill="#fff" opacity=".25"/><rect x="460" y="170" width="760" height="220" rx="16" fill="#fff" opacity=".25"/><rect x="460" y="430" width="760" height="220" rx="16" fill="#fff" opacity=".25"/><text x="80" y="110" font-family="sans-serif" font-size="36" fill="#fff">${text}</text></svg>`).toString('base64');
const icon = (c) => 'data:image/svg+xml;base64,' + Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" rx="14" fill="${c}"/><circle cx="32" cy="32" r="14" fill="#fff"/></svg>`).toString('base64');
// The compose files come from an HTTPS address answered by the browser (with the CORS header a market host
// sends): the panel's script policy lets it fetch https: addresses only, never data: ones
const composeBody = {
  'cosmos-installer': {},
  name: '{ServiceName}',
  services: { '{ServiceName}': { image: 'jellyfin/jellyfin:latest', container_name: '{ServiceName}', restart: 'unless-stopped',
    volumes: [{ type: 'volume', source: '{ServiceName}-config', target: '/config' }],
    routes: [{ name: '{ServiceName}', description: 'Web UI', useHost: true, target: 'http://{ServiceName}:8096', mode: 'SERVAPP', Timeout: 14400000, ThrottlePerMinute: 10000, BlockCommonBots: true, SmartShield: { Enabled: true } }] } },
  volumes: { '{ServiceName}-config': {} },
};
const compose = (name) => 'https://market.example.org/compose/' + encodeURIComponent(name.toLowerCase()) + '.json';
const marketApp = (name, colours, tags, description) => ({
  name, description, tags, icon: icon(colours[0]), compose: compose(name), supported_architectures: ['amd64', 'arm64'],
  screenshots: [svg(colours[0], colours[1], name + ' — library'), svg(colours[1], colours[0], name + ' — settings')],
  longDescription: '<p>' + description + ' It runs on your own server: your files stay at home, and every device in the house can use it.</p>',
  repository: 'https://example.org/' + name.toLowerCase(), image: 'https://example.org/r/' + name.toLowerCase(),
});
const marketApps = [
  marketApp('Jellyfin', ['#5b2a86', '#00a4dc'], ['media', 'films', 'music'], 'Your films, series and music, streamed to every screen.'),
  marketApp('Nextcloud', ['#0b5cad', '#38a3f1'], ['files', 'calendar', 'office'], 'Files, calendars and contacts, synchronised between your devices.'),
  marketApp('Vaultwarden', ['#175ddc', '#0b2a63'], ['passwords', 'security'], 'A password manager compatible with the Bitwarden apps.'),
  marketApp('Immich', ['#4250af', '#fa2921'], ['photos', 'backup'], 'Photos and videos from your phone, backed up automatically.'),
  marketApp('Home Assistant', ['#18bcf2', '#0b6a8c'], ['home', 'automation'], 'Lights, heating and sensors in one place, without a cloud account.'),
];
const marketMock = [['/cosmos/api/markets', { status: 'OK', data: { all: { 'cosmos-cloud': marketApps, 'community': marketApps.slice(0, 2).map((a) => ({ ...a, name: a.name })) }, showcase: marketApps.slice(0, 3) } }],
  ['/compose/', composeBody, 200, { 'Access-Control-Allow-Origin': '*' }]];
const noDnsMock = [['/cosmos/api/dns', { status: 'error', message: 'Internal server error: lookup jellyfin.polish.test on 127.0.0.11:53: server misbehaving', code: 'DNS001' }, 500],
  ['/cosmos/api/dns-check', { status: 'error', message: 'DNS Check error: lookup jellyfin.polish.test on 127.0.0.11:53: server misbehaving', code: 'DNS002' }, 500]];

// the OpenID client the gallery makes and removes again: a name of its own per run (runs share an instance)
const openidClient = 'gallery-' + Math.random().toString(36).slice(2, 8);
// the sign-in check of Cloudflare failing in both ways the server reports (src/turnstile/watch.go)
const turnstileProblems = [
  { reason: 'key_refused', since: '2026-11-14T08:10:00Z', last: '2026-11-14T09:40:00Z', count: 3 },
  { reason: 'unreachable', since: '2026-11-14T09:00:00Z', last: '2026-11-14T09:41:00Z', count: 12 },
];
// an app reached under its local address and two names of a domain: one tile on the home page
const withExtraAddresses = (j) => {
  const routes = (((j || {}).data || {}).HTTPConfig || {}).ProxyConfig ? j.data.HTTPConfig.ProxyConfig.Routes || [] : null;
  if (!routes) return j;
  const jellyfin = routes.find((r) => r.Name === 'Jellyfin');
  if (!jellyfin || routes.some((r) => r.Name === 'Jellyfin-example.org')) return j;
  const extra = [
    { ...jellyfin, Name: 'Jellyfin-example.org', Host: 'jellyfin.example.org' },
    { ...jellyfin, Name: 'Jellyfin-media', Host: 'media.example.org' },
  ];
  return { ...j, data: { ...j.data, HTTPConfig: { ...j.data.HTTPConfig, ProxyConfig: { ...j.data.HTTPConfig.ProxyConfig, Routes: [...routes, ...extra] } } } };
};
// the storage dialogs' lists (as scripts/fork-ui-check-forms4.mjs and -settings-forms.mjs answer them)
const TB = 1e12;
const storageMock = [
  ['/cosmos/api/storage/candidates', { status: 'OK', data: {
    devices: [
      { device: '/dev/sdb1', disk: '/dev/sdb', type: 'part', fstype: 'ext4', label: 'Media', uuid: '1b2c3d4e-0000-4000-8000-000000000001', model: 'WDC WD40', size: 4 * TB, mountable: true, use: { code: '', detail: '' } },
      { device: '/dev/sdc1', disk: '/dev/sdc', type: 'part', fstype: 'ext4', label: '', size: 2 * TB, mountable: false, use: { code: 'mounted', detail: '/mnt/d1' } },
    ],
    mounts: [
      { path: '/', device: '/dev/nvme0n1p2', disk: '/dev/nvme0n1', fstype: 'ext4', size: TB / 2, free: TB / 4, permanent: true, kind: 'system' },
      { path: '/mnt/d1', device: '/dev/sdc1', disk: '/dev/sdc', fstype: 'ext4', size: 2 * TB, free: TB, permanent: true, kind: 'data' },
      { path: '/mnt/d2', device: '/dev/sdf1', disk: '/dev/sdf', fstype: 'xfs', size: 4 * TB, free: 3 * TB, permanent: true, kind: 'data' },
      { path: '/mnt/p2', device: '/dev/sdh1', disk: '/dev/sdh', fstype: 'ext4', size: 6 * TB, free: 6 * TB, permanent: true, kind: 'data' },
    ],
    base: '/mnt', inContainer: false,
  } }],
  ['/cosmos/api/mounts', { status: 'OK', data: [{ device: '/dev/sdc1', path: '/mnt/d1', type: 'ext4', opts: ['rw'], permenant: true }, { device: '/dev/sdf1', path: '/mnt/d2', type: 'xfs', opts: ['rw'], permenant: true }] }],
  ['/cosmos/api/storage/unmount-check', { status: 'OK', data: { mountPoint: '/mnt/d1', device: '/dev/sdc1', permanent: true, system: false, users: [{ pid: 4242, name: 'jellyfin', how: 'files' }] } }],
  ['/cosmos/api/disks', { status: 'OK', data: [
    { name: '/dev/sdz', type: 'disk', model: 'Test USB', size: 64000000000, rota: false, children: [{ name: '/dev/sdz1', type: 'part', fstype: 'exfat', size: 64000000000, children: [] }] },
    { name: '/dev/sdy', type: 'disk', model: 'System', size: 500000000000, rota: false, children: [{ name: '/dev/sdy1', type: 'part', fstype: 'ext4', mountpoint: '/', size: 500000000000, children: [] }] },
  ] }],
  ['/cosmos/api/snapraid', { status: 'OK', data: [] }],
  ['/cosmos/api/smart-def', { status: 'OK', data: {} }],
];

const views = [
  { id: 'home', path: '/cosmos-ui/', core: true, captures: [
    {},
    { id: 'keyboard-focus', open: async (page) => { for (let i = 0; i < 4; i++) await page.keyboard.press('Tab'); }, close: async () => {} },
    { id: 'menu', phoneOnly: true, open: async (page) => { await page.locator('header button').first().click(); }, close: async (page) => { await page.keyboard.press('Escape'); } },
    { id: 'notifications', open: async (page) => { await page.locator('header button:has(.anticon-bell)').first().click(); } },
    // the phone header's "More" menu (terminal, restarts)
    { id: 'more', phoneOnly: true, open: async (page) => { await page.locator('header button:has(.anticon-more)').first().click(); } },
    { id: 'tooltip', desktopOnly: true, open: async (page) => { await page.locator('header button').nth(1).hover(); await page.waitForTimeout(900); }, close: async (page) => { await page.mouse.move(600, 500); } },
  ] },
  { id: 'servapps', path: '/cosmos-ui/servapps', core: true, wait: 1200, captures: [
    {},
    { id: 'keyboard-focus', open: async (page) => { for (let i = 0; i < 9; i++) await page.keyboard.press('Tab'); }, close: async () => {} },
    { id: 'new-url', open: async (page) => { await page.locator('main .MuiChip-clickable.MuiChip-colorPrimary').first().click({ timeout: 8000 }); } },
    { id: 'volumes', tab: 1 },
    { id: 'networks', tab: 2 },
  ] },
  { id: 'servapps-stack', path: '/cosmos-ui/servapps/stack/immich', wait: 800 },
  { id: 'servapps-new', path: '/cosmos-ui/servapps/new-service' },
  { id: 'container', path: '/cosmos-ui/servapps/containers/jellyfin', core: true, wait: 800, captures: [
    {},
    { id: 'logs', tab: 1 },
    { id: 'monitoring', tab: 2, wait: 1500 },
    { id: 'events', tab: 3 },
    { id: 'terminal', tab: 4 },
    { id: 'compose', tab: 5 },
    { id: 'docker', tab: 6 },
    { id: 'network', tab: 7 },
    { id: 'storage', tab: 8 },
  ] },
  { id: 'container-stopped', path: '/cosmos-ui/servapps/containers/pihole', wait: 800 },
  { id: 'container-long-name', path: '/cosmos-ui/servapps/containers/nextcloud-aio-mastercontainer-with-a-rather-long-name', wait: 800 },
  { id: 'container-missing', path: '/cosmos-ui/servapps/containers/no-such-container', wait: 800 },
  { id: 'monitoring', path: '/cosmos-ui/monitoring', core: true, wait: 2000, captures: [
    {},
    { id: 'proxy', tab: 1, wait: 1500 },
    { id: 'events', tab: 2, wait: 800 },
    { id: 'alerts', tab: 3 },
    { id: 'alert-editor', open: async (page) => { await page.locator('[role=tabpanel]:not([hidden]) tbody tr').first().locator('td').nth(1).click(); } },
  ] },
  { id: 'market', path: '/cosmos-ui/market-listing', wait: 2500 },
  { id: 'trust', path: '/cosmos-ui/trust' },
  { id: 'urls', path: '/cosmos-ui/config-url', core: true, captures: [
    {},
    { id: 'new', open: create },
    { id: 'no-match', open: async (page) => { await page.locator('main input[placeholder]').first().fill('zzzz-nothing'); }, close: async (page) => { await page.locator('main input[placeholder]').first().fill(''); } },
    { id: 'shield', tab: 1 },
    { id: 'whitelist', tab: 2 },
  ] },
  { id: 'url', path: '/cosmos-ui/config-url/Jellyfin', captures: [
    {},
    { id: 'setup', tab: 1 },
    { id: 'security', tab: 2 },
    { id: 'monitoring', tab: 3, wait: 1500 },
    { id: 'events', tab: 4 },
  ] },
  { id: 'url-missing', path: '/cosmos-ui/config-url/No-Such-URL' },
  { id: 'storage', path: '/cosmos-ui/storage', captures: [
    {},
    { id: 'mounts', tab: 1 },
    { id: 'merge', tab: 2 },
    { id: 'parity', tab: 3 },
    { id: 'raid', tab: 4 },
  ] },
  { id: 'users', path: '/cosmos-ui/config-users', core: true, captures: [{}, { id: 'new', open: create }] },
  { id: 'openid', path: '/cosmos-ui/openid-manage', captures: [{}, { id: 'new', open: create }] },
  { id: 'cron', path: '/cosmos-ui/cron', captures: [{}, { id: 'new', open: create }] },
  { id: 'config', path: '/cosmos-ui/config-general/general', core: true, captures: [
    {},
    { id: 'unsaved', open: async (page) => { const f = page.locator('input[name=MaintenanceTime]'); await f.scrollIntoViewIfNeeded(); await f.fill('03:30'); await f.blur(); }, close: async () => {} },
    { id: 'http', tab: 1 },
    { id: 'https', tab: 2 },
    { id: 'appearance', tab: 3 },
    { id: 'external', tab: 4 },
    { id: 'api', tab: 5 },
  ] },
  { id: 'security', path: '/cosmos-ui/security', core: true, wait: 1500, captures: [
    {},
    { id: 'incident', open: async (page) => { await page.locator('main [role=button]:has(> .MuiAlert-root)').first().click(); } },
  ] },
  { id: 'security-audit', path: '/cosmos-ui/security-audit', core: true, captures: [
    {},
    { id: 'row', open: async (page) => { await page.locator('main [role=button].MuiButtonBase-root').first().click(); } },
  ] },
  { id: 'security-audit-user', path: '/cosmos-ui/security-audit?user=admin' },
  { id: 'not-found', path: '/cosmos-ui/nothing-here' },
  // a server that has measured nothing yet (a fresh install, the first minute after a start):
  // the seeded instance is past that, so the answers are replaced on their way to the page
  { id: 'home-no-metrics', path: '/cosmos-ui/', core: true, wait: 1200, mock: [['/cosmos/api/metrics', { status: 'OK', data: null }], ['/cosmos/api/dashboard', { status: 'OK', data: null }]] },
  { id: 'home-empty-metrics', path: '/cosmos-ui/', wait: 1200, mock: [['/cosmos/api/metrics', { status: 'OK', data: [] }], ['/cosmos/api/dashboard', { status: 'OK', data: null }]] },
  { id: 'monitoring-no-metrics', path: '/cosmos-ui/monitoring', wait: 1500, mock: [['/cosmos/api/metrics', { status: 'OK', data: null }]], captures: [{}, { id: 'proxy', tab: 1, wait: 800 }] },
  { id: 'monitoring-empty-metrics', path: '/cosmos-ui/monitoring', wait: 1500, mock: [['/cosmos/api/metrics', { status: 'OK', data: [] }]], captures: [{}, { id: 'proxy', tab: 1, wait: 800 }] },
  { id: 'container-no-metrics', path: '/cosmos-ui/servapps/containers/jellyfin', wait: 800, mock: [['/cosmos/api/metrics', { status: 'OK', data: null }]], captures: [{ id: 'monitoring', tab: 2, wait: 1200 }] },
  // the home page's health card: every kind of problem at once, and all well
  { id: 'home-health', path: '/cosmos-ui/', core: true, wait: 1500, patch: [
    ['/cosmos/api/list-metrics', (j) => ({ ...j, data: { ...(j.data || {}), 'cosmos.system.disk./storage': 'Disk /storage' } })],
    ['/cosmos/api/metrics', (j, url) => (/cosmos\.system\.disk\./.test(decodeURIComponent(url)) ? { status: 'OK', data: [{ Key: 'cosmos.system.disk./storage', Max: 4000e9, Values: [{ Value: 3880e9 }] }] } : j)],
    ['/cosmos/api/snapshots', () => ({ status: 'OK', data: { enabled: true, sources: [{ path: '/storage/photos', lastError: 'The folder was empty: is the disk mounted?' }] } })],
    ['/cosmos/api/jobs', (j) => ({ ...j, data: { ...(j.data || {}), Custom: { ...((j.data || {}).Custom || {}), 'nightly-cleanup': { Name: 'nightly-cleanup', Scheduler: 'Custom', LastRun: '2026-11-14T03:00:00Z', LastRunSuccess: false } } } })],
  ] },
  { id: 'home-calm', path: '/cosmos-ui/', wait: 1500, patch: [
    ['/cosmos/api/jobs', () => ({ status: 'OK', data: { Custom: {} } })],
    ['/cosmos/api/servapps', (j) => ({ ...j, data: (j.data || []).filter((c) => c.State !== 'exited' || !/Exited \((?!0|137|143)\d+\)/.test(c.Status)) })],
  ] },
  // the "updates are available" line on ServApps (the test images have no newer version)
  { id: 'servapps-updates', path: '/cosmos-ui/servapps', wait: 1200, patch: [['/cosmos/api/config', (j) => ({ ...j, updates: { '/jellyfin': true, '/radarr': true } })]] },
  // every ServApps notice at once: updates, leftovers of interrupted edits, apps without an address
  { id: 'servapps-notices', path: '/cosmos-ui/servapps', core: true, wait: 1500, patch: [['/cosmos/api/config', (j) => ({ ...j, updates: { '/jellyfin': true, '/radarr': true } })]], mock: [['/cosmos/api/servapps-leftovers', { status: 'OK', data: [
    { name: 'jellyfin-cosmos-old-0123456789ab', app: 'jellyfin', finished: '2026-11-14T09:12:00Z', running: false, appExists: true, appRunning: true },
    { name: 'sonarr-cosmos-old-ba9876543210', app: 'sonarr', finished: '2026-11-13T21:40:00Z', running: false, appExists: false, appRunning: false },
  ] }]], captures: [
    {},
    { id: 'expanded', open: async (page) => { await page.locator('main [data-notices] button[aria-expanded=false]').first().click(); }, close: async () => {} },
  ] },
  // the home page while the server throttles some answers: what could not be checked is said, with a retry
  { id: 'home-partial', path: '/cosmos-ui/', core: true, wait: 1500, mock: [['/cosmos/api/jobs', { status: 'error', message: 'Too many requests', code: 'HTTP429' }, 429]], patch: [
    ['/cosmos/api/servapps', (j) => ({ ...j, data: (j.data || []).filter((c) => c.State !== 'exited' || !/Exited \((?!0|137|143)\d+\)/.test(c.Status)) })],
  ] },
  { id: 'market-showcase', path: '/cosmos-ui/market-listing', core: true, wait: 1500, mock: marketMock, captures: [
    {},
    { id: 'search', open: async (page) => { await page.locator('main input[placeholder^="Search"]').first().fill('nextcloud'); }, close: async (page) => { await page.locator('main input[placeholder^="Search"]').first().fill(''); } },
    { id: 'sources', open: async (page) => { await page.locator('main button').filter({ has: page.locator('.anticon-file-text, .anticon-container, .anticon-profile') }).first().click(); } },
  ] },
  { id: 'market-app', path: '/cosmos-ui/market-listing/cosmos-cloud/Jellyfin', core: true, wait: 1500, mock: [...marketMock, ...noDnsMock], captures: [
    {},
    { id: 'review', wait: 2500, open: async (page) => { await page.getByRole('button', { name: /Install$/ }).first().click(); /* the app's page is drawn over the list: its button comes first */ } },
    // an address under a domain on a server that cannot look names up (the stand-in answers like Go's resolver)
    { id: 'review-no-dns', wait: 2500, open: async (page) => { await page.getByRole('button', { name: /Install$/ }).first().click(); await page.locator('[role=dialog] .MuiChip-root', { hasText: new URL(base).hostname }).first().click(); }, close: async (page) => { await page.keyboard.press('Escape'); } },
  ] },
  { id: 'new-container-steps', path: '/cosmos-ui/servapps/new-service', captures: [
    {},
    { id: 'step-2', open: async (page) => { await page.locator('input[name=name]').first().fill('demo'); await page.locator('input[name=image]').first().fill('busybox:1.36'); await page.locator('button').filter({ hasText: /^Next$/ }).last().click(); }, close: async () => {} },
  ] },
  // the home page with several notices at once (a restart needed, the sign-in check of Cloudflare failing)
  // and an app reached under several addresses (one tile per app, utils/homeTiles.js)
  { id: 'home-notices', path: '/cosmos-ui/', core: true, wait: 1500, patch: [
    ['/cosmos/api/status', (j) => ({ ...j, data: { ...(j.data || {}), needsRestart: true, domain: true } })],
    ['/cosmos/api/turnstile', (j) => ({ ...j, data: { ...(j.data || {}), enabled: true, consoleOff: false, problems: turnstileProblems } })],
    ['/cosmos/api/config', (j) => withExtraAddresses(j)],
  ], captures: [
    {},
    { id: 'expanded', open: async (page) => { await page.locator('main [data-notices] button[aria-expanded=false]').first().click(); }, close: async () => {} },
  ] },
  { id: 'home-turnstile-console', path: '/cosmos-ui/', wait: 1500, patch: [
    ['/cosmos/api/turnstile', (j) => ({ ...j, data: { ...(j.data || {}), enabled: true, consoleOff: true, problems: [] } })],
  ] },
  // Configuration > Domains: the certificates, home names, Cloudflare and Turnstile cards; the Turnstile card in its states
  { id: 'domains-cards', path: '/cosmos-ui/config-general/https', wait: 2000, captures: [
    { id: 'cloudflare-turnstile', at: 'prev:[data-turnstile-card]' },
    { id: 'turnstile', at: '[data-turnstile-card]' },
  ] },
  { id: 'domains-turnstile-problems', path: '/cosmos-ui/config-general/https', wait: 2000, patch: [
    ['/cosmos/api/turnstile', (j) => ({ ...j, data: { ...(j.data || {}), enabled: true, active: true, consoleOff: false, siteKey: '1x00000000000000000000AA', secretKey: '***', confirmed: true, problems: turnstileProblems } })],
  ], captures: [{ at: '[data-turnstile-card]' }] },
  { id: 'domains-turnstile-console', path: '/cosmos-ui/config-general/https', wait: 2000, patch: [
    ['/cosmos/api/turnstile', (j) => ({ ...j, data: { ...(j.data || {}), enabled: true, active: false, consoleOff: true, siteKey: '1x00000000000000000000AA', secretKey: '***', confirmed: true, problems: [] } })],
  ], captures: [{ at: '[data-turnstile-card]' }] },
  // the OpenID client form, its values to copy, and the new secret
  { id: 'openid-client', path: '/cosmos-ui/openid-manage', wait: 800, captures: [
    { id: 'form', open: async (page) => {
      await create(page);
      await page.locator('#fork-openid-id').fill(openidClient);
      await page.locator('#fork-openid-redirect').fill(new URL(base).origin.replace('://', '://jellyfin.') + '/auth/callback');
      await page.locator('[role=dialog]').getByRole('button', { name: 'Add', exact: true }).click();
    }, close: async () => {} },
    { id: 'values', wait: 1500, open: async (page) => { await page.locator('[role=dialog]').getByRole('button', { name: 'Create' }).click(); }, close: async (page) => { await page.locator('[role=dialog]').getByRole('button', { name: 'Done' }).click(); } },
    { id: 'reset-secret', open: async (page) => { await page.getByRole('button', { name: 'Reset Secret' }).first().click(); },
      close: async (page) => { await page.keyboard.press('Escape'); await page.evaluate((id) => fetch('/cosmos/api/openid/' + id, { method: 'DELETE' }), openidClient); } },
  ] },
  // the storage dialogs, answered by the browser (no disk is ever touched): mount, unmount, merge, parity, format
  { id: 'storage-dialogs', path: '/cosmos-ui/storage', wait: 1500, mock: storageMock, patch: [['/cosmos/api/status', (j) => ({ ...j, data: { ...(j.data || {}), containerized: false } })]], captures: [
    { id: 'format', open: async (page) => { await page.getByRole('button', { name: 'Format' }).first().click(); } },
    { id: 'mount', tab: 1, open: async (page) => { await page.getByRole('button', { name: 'New Mount' }).first().click(); await page.locator('[role=dialog]').getByRole('button', { name: /sdb1/ }).click(); } },
    { id: 'unmount', open: async (page) => { await page.getByRole('button', { name: 'More actions' }).first().click(); await page.getByRole('menuitem', { name: 'Unmount' }).first().click(); await page.locator('#fork-storage-confirm').fill('/mnt/d1'); },
      close: async (page) => { await page.keyboard.press('Escape'); await page.waitForTimeout(300); await page.keyboard.press('Escape'); } },
    { id: 'merge', tab: 2, open: async (page) => { await page.getByRole('button', { name: 'Create Merge' }).first().click(); await page.locator('[role=dialog]').getByRole('checkbox', { name: '/mnt/d1' }).check(); await page.locator('[role=dialog]').getByRole('checkbox', { name: '/mnt/d2' }).check(); } },
    { id: 'parity', tab: 3, open: async (page) => { await page.getByRole('button', { name: 'New Parity Disks' }).first().click(); await page.locator('[role=dialog]').getByRole('checkbox', { name: '/mnt/d1' }).first().check(); /* the next one is proposed as the parity disk */ } },
  ] },
  // the small forms: a new network, a new volume, a Market source, a Shield whitelist entry
  { id: 'small-forms', path: '/cosmos-ui/servapps', wait: 1200, captures: [
    { id: 'network', tab: 2, open: async (page) => { await page.getByRole('button', { name: 'New Network' }).first().click(); await page.locator('#name').fill('lan-apps'); } },
    { id: 'volume', tab: 1, open: async (page) => { await page.getByRole('button', { name: /New Volume/ }).first().click(); await page.locator('#name').fill('photos-cache'); } },
  ] },
  { id: 'market-source', path: '/cosmos-ui/market-listing', wait: 1500, mock: marketMock, captures: [
    { open: async (page) => { await page.getByRole('button', { name: 'Sources' }).first().click(); await page.locator('[role=dialog]').getByRole('button', { name: 'Add Source' }).click(); await page.locator('[role=dialog] input[name$=".Url"]').last().fill('https://apps.example.org/servapps.json'); } },
  ] },
  { id: 'whitelist-entry', path: '/cosmos-ui/config-url/whitelist', wait: 1200, captures: [
    { open: async (page) => { await page.getByRole('button', { name: 'Add IP' }).first().click(); } },
  ] },
  // upstream's licence-only pages: one picture each, for the record
  { id: 'paid-backups', path: '/cosmos-ui/backups' },
  { id: 'paid-constellation', path: '/cosmos-ui/constellation' },
];

// ---------------------------------------------------------------------------
// What a script can measure on a rendered page. Runs inside the page.
// ---------------------------------------------------------------------------
const audit = () => {
  const vw = window.innerWidth;
  const MAX = 12; // samples kept per finding
  const result = {
    title: document.title,
    sidewaysScroll: document.documentElement.scrollWidth > vw + 1,
    pageHeight: document.documentElement.scrollHeight,
    smallText: { count: 0, samples: [] },
    contrast: { count: 0, checked: 0, unknown: 0, samples: [] },
    cutOff: { count: 0, samples: [] },
    truncated: { count: 0, samples: [] },
    overlaps: { count: 0, samples: [] },
    unnamed: { count: 0, samples: [] },
    targets: { below24: 0, below44: 0, samples: [] },
  };
  const add = (bucket, sample) => { bucket.count++; if (bucket.samples.length < MAX) bucket.samples.push(sample); };

  const describe = (el) => {
    const parts = [];
    for (let e = el; e && e !== document.body && parts.length < 4; e = e.parentElement) {
      let name = e.tagName.toLowerCase();
      if (e.id) name += '#' + e.id;
      const cls = [...e.classList].filter((c) => /^(Mui|ct-|apexcharts)/.test(c) && !/^Mui.*-root$/.test(c) || /^Mui(Button|Chip|Tab|Alert|Card|Paper|TableCell|Typography|ListItemButton|IconButton|MenuItem|InputBase|FormLabel|Link|Badge)-root$/.test(c)).slice(0, 2);
      if (cls.length) name += '.' + cls.join('.');
      parts.unshift(name);
    }
    return parts.join(' > ');
  };
  const short = (text) => text.replace(/\s+/g, ' ').trim().slice(0, 60);

  // --- colours
  const parseColour = (text) => {
    const m = /rgba?\(([^)]+)\)/.exec(text || '');
    if (!m) return null;
    const p = m[1].split(/[\s,/]+/).filter(Boolean).map(parseFloat);
    if (p.length < 3 || p.some(Number.isNaN)) return null;
    return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 };
  };
  const over = (top, under) => ({
    r: top.r * top.a + under.r * (1 - top.a),
    g: top.g * top.a + under.g * (1 - top.a),
    b: top.b * top.a + under.b * (1 - top.a),
    a: 1,
  });
  const luminance = (c) => {
    const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
    return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
  };
  const ratio = (a, b) => {
    const la = luminance(a), lb = luminance(b);
    return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
  };
  const hex = (c) => '#' + [c.r, c.g, c.b].map((v) => Math.round(v).toString(16).padStart(2, '0')).join('');
  const pageColour = (() => {
    const html = parseColour(getComputedStyle(document.documentElement).backgroundColor);
    const body = parseColour(getComputedStyle(document.body).backgroundColor);
    const white = { r: 255, g: 255, b: 255, a: 1 };
    return over(body || white, over(html || white, white));
  })();
  // The colours behind an element: every layer up to the first opaque one. A
  // gradient counts with each of its colour stops (the worst one decides); a
  // picture behind the text cannot be judged and gives null.
  const backgrounds = (el) => {
    const layers = []; // from the element upwards; each layer is a list of alternative colours
    for (let e = el; e; e = e.parentElement) {
      const style = getComputedStyle(e);
      const image = style.backgroundImage;
      if (e !== document.body && e !== document.documentElement && image && image !== 'none') {
        // several pictures: the first is on top, and what lies under an opaque one is not seen
        // (the buttons draw their border as a gradient under a plain fill)
        let opaque = false;
        for (const layer of image.split(/,(?![^(]*\))(?=\s*[a-z-]+\()/)) {
          if (/url\(/.test(layer)) return null;
          const stops = (layer.match(/rgba?\([^)]+\)/g) || []).map(parseColour).filter(Boolean);
          if (!stops.length) continue;
          layers.push(stops);
          if (stops.every((s) => s.a >= 0.99)) { opaque = true; break; }
        }
        if (opaque) break;
        // the picture layers lie over the element's own colour: put that under them
        const under = parseColour(style.backgroundColor);
        if (under && under.a > 0) { layers.push([under]); if (under.a >= 0.99) break; }
        continue;
      }
      const colour = parseColour(style.backgroundColor);
      if (colour && colour.a > 0) {
        layers.push([colour]);
        if (colour.a >= 0.99) break;
      }
    }
    let results = [pageColour];
    for (let i = layers.length - 1; i >= 0; i--) {
      const next = [];
      for (const under of results) for (const top of layers[i]) next.push(over(top, under));
      results = next.slice(0, 16);
    }
    return results;
  };
  const opacityOf = (el) => {
    let o = 1;
    for (let e = el; e && e !== document.documentElement; e = e.parentElement) o *= parseFloat(getComputedStyle(e).opacity || '1');
    return o;
  };
  const disabled = (el) => !!el.closest('[disabled], [aria-disabled="true"], .Mui-disabled');
  const visible = (el) => {
    const style = getComputedStyle(el);
    if (style.visibility === 'hidden' || style.display === 'none') return false;
    const r = el.getBoundingClientRect();
    if (!(r.width > 1 && r.height > 1 && r.bottom > 0 && r.right > 0)) return false;
    // inside a box that shows nothing of it (the closed side menu is zero pixels wide, not removed)
    let left = r.left, right = r.right, top = r.top, bottom = r.bottom;
    for (let e = el.parentElement; e && e !== document.body; e = e.parentElement) {
      const s = getComputedStyle(e);
      if (s.overflowX === 'visible' && s.overflowY === 'visible') continue;
      const b = e.getBoundingClientRect();
      if (s.overflowX !== 'visible') { left = Math.max(left, b.left); right = Math.min(right, b.right); }
      if (s.overflowY !== 'visible') { top = Math.max(top, b.top); bottom = Math.min(bottom, b.bottom); }
      if (right - left < 2 || bottom - top < 2) return false;
    }
    return true;
  };
  const clipper = (el) => {
    for (let e = el.parentElement; e && e !== document.body; e = e.parentElement) {
      const o = getComputedStyle(e).overflowX;
      if (o === 'hidden' || o === 'clip' || o === 'auto' || o === 'scroll') return e;
    }
    return null;
  };

  // --- every element that holds text of its own
  const seen = new Set();
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.nodeValue;
    if (!text || !text.trim()) continue;
    const el = node.parentElement;
    if (!el || seen.has(el) || /^(SCRIPT|STYLE|NOSCRIPT|OPTION)$/.test(el.tagName)) continue;
    seen.add(el);
    if (!visible(el) || el.closest('[aria-hidden="true"]')) continue;
    const opacity = opacityOf(el);
    if (opacity < 0.05) continue;
    const style = getComputedStyle(el);
    const size = parseFloat(style.fontSize);
    const rect = el.getBoundingClientRect();
    // text hidden from sight but kept for screen readers
    if (rect.width <= 2 || rect.height <= 2 || style.clipPath === 'inset(50%)' || style.clip === 'rect(0px, 0px, 0px, 0px)') continue;

    if (size < 11.95) add(result.smallText, { text: short(text), px: Math.round(size * 10) / 10, at: describe(el) });

    // contrast (WCAG 2.x: 4.5:1, or 3:1 for text of 24 px, or 18.66 px bold, and up)
    if (!disabled(el)) {
      const colour = parseColour(style.color);
      // text painted with a picture of its own (the logo's second word) cannot be judged
      const painted = style.webkitBackgroundClip === 'text' || style.backgroundClip === 'text';
      const behind = painted ? null : backgrounds(el);
      if (!colour || !behind) result.contrast.unknown++;
      else {
        result.contrast.checked++;
        const weight = parseInt(style.fontWeight, 10) || 400;
        const need = size >= 24 || (size >= 18.66 && weight >= 700) ? 3 : 4.5;
        let worst = Infinity, worstBg = null, shown = null;
        for (const bg of behind) {
          const fg = over({ ...colour, a: colour.a * opacity }, bg);
          const r = ratio(fg, bg);
          if (r < worst) { worst = r; worstBg = bg; shown = fg; }
        }
        if (worst < need - 0.05) add(result.contrast, { text: short(text), ratio: Math.round(worst * 100) / 100, need, colour: hex(shown), on: hex(worstBg), px: Math.round(size), at: describe(el) });
      }
    }

    // cut off: wider than what it may show, or running out of the box that clips it
    const ellipsis = style.textOverflow === 'ellipsis';
    const hides = style.overflowX === 'hidden' || style.overflowX === 'clip';
    if (hides && el.scrollWidth > el.clientWidth + 1) {
      const whole = el.getAttribute('title') || el.getAttribute('aria-label') || (el.closest('[title], [aria-label]') || {}).title;
      if (ellipsis && !whole) add(result.truncated, { text: short(text), at: describe(el) });
      else if (!ellipsis) add(result.cutOff, { text: short(text), why: 'wider than its box', at: describe(el) });
    } else {
      const box = clipper(el);
      if (box) {
        const b = box.getBoundingClientRect();
        const scrolls = /auto|scroll/.test(getComputedStyle(box).overflowX);
        if (!scrolls && (rect.right > b.right + 2 || rect.left < b.left - 2) && rect.width > 4) add(result.cutOff, { text: short(text), why: 'runs out of the box that clips it', at: describe(el) });
      } else if (rect.right > vw + 2) {
        add(result.cutOff, { text: short(text), why: 'runs out of the window', at: describe(el) });
      }
    }
  }

  // --- controls: a name for assistive technology, their size on a phone, overlaps
  const controls = [...document.querySelectorAll('button, a[href], [role=button], [role=tab], [role=menuitem], input:not([type=hidden]), select, textarea, [role=checkbox], [role=switch]')]
    .filter((el) => visible(el) && !el.closest('[aria-hidden="true"]'));
  const named = (el) => {
    if ((el.innerText || '').trim()) return true;
    if (el.getAttribute('aria-label') || el.getAttribute('title') || el.getAttribute('aria-labelledby')) return true;
    if (el.querySelector('img[alt]:not([alt=""]), svg title')) return true;
    if (/^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName)) {
      if (el.id && document.querySelector('label[for="' + CSS.escape(el.id) + '"]')) return true;
      if (el.closest('label') || el.getAttribute('placeholder')) return true;
    }
    return false;
  };
  const boxes = [];
  for (const el of controls) {
    const r = el.getBoundingClientRect();
    if (!named(el)) add(result.unnamed, { at: describe(el) });
    // a checkbox or radio is an invisible input over its picture: the label is the target
    const hiddenInput = el.tagName === 'INPUT' && parseFloat(getComputedStyle(el).opacity) === 0;
    if (!hiddenInput && !disabled(el) && (/^(BUTTON|A)$/.test(el.tagName) || el.getAttribute('role') === 'button')) {
      const inline = el.tagName === 'A' && getComputedStyle(el).display === 'inline';
      if (!inline) {
        const least = Math.min(r.width, r.height);
        if (least < 23.5) result.targets.below24++;
        if (least < 43.5) {
          result.targets.below44++;
          if (result.targets.samples.length < MAX) result.targets.samples.push({ size: Math.round(r.width) + 'x' + Math.round(r.height), text: short(el.innerText || el.getAttribute('aria-label') || ''), at: describe(el) });
        }
      }
    }
    if (!hiddenInput && boxes.length < 400) boxes.push({ el, r });
  }
  for (let i = 0; i < boxes.length; i++) {
    for (let j = i + 1; j < boxes.length; j++) {
      const a = boxes[i], b = boxes[j];
      if (a.el.contains(b.el) || b.el.contains(a.el)) continue;
      const w = Math.min(a.r.right, b.r.right) - Math.max(a.r.left, b.r.left);
      const h = Math.min(a.r.bottom, b.r.bottom) - Math.max(a.r.top, b.r.top);
      if (w <= 2 || h <= 2) continue;
      const smaller = Math.min(a.r.width * a.r.height, b.r.width * b.r.height);
      if (w * h < 0.3 * smaller) continue;
      // one drawn over the other on purpose (a fixed bar over the scrolled page) is not an overlap
      const fixed = (el) => { for (let e = el; e && e !== document.body; e = e.parentElement) { const p = getComputedStyle(e).position; if (p === 'fixed' || p === 'sticky') return true; } return false; };
      if (fixed(a.el) !== fixed(b.el)) continue;
      add(result.overlaps, { a: describe(a.el), b: describe(b.el) });
    }
  }

  // --- one Tab stop per control: a focusable element inside another (a button in a link) is two
  const focusable = 'a[href], button:not([disabled]), input:not([type=hidden]):not([disabled]), select, textarea, [tabindex]:not([tabindex="-1"])';
  result.nestedFocus = { count: 0, samples: [] };
  for (const el of document.querySelectorAll(focusable)) {
    if (!visible(el) || el.closest('[aria-hidden="true"]')) continue;
    const inner = [...el.querySelectorAll(focusable)].find((child) => visible(child) && child.tabIndex >= 0);
    if (el.tabIndex >= 0 && inner) add(result.nestedFocus, { text: short(el.innerText || el.getAttribute('aria-label') || ''), at: describe(inner) });
  }

  // --- heading levels: a jump of more than one level down (h1 -> h4) loses the outline
  result.headingSkips = { count: 0, samples: [] };
  let previous = 0;
  for (const h of document.querySelectorAll('h1, h2, h3, h4, h5, h6, [role=heading][aria-level]')) {
    if (!visible(h) || h.closest('[aria-hidden="true"]')) continue;
    const level = h.getAttribute('aria-level') ? Number(h.getAttribute('aria-level')) : Number(h.tagName.slice(1));
    if (previous && level > previous + 1) add(result.headingSkips, { text: short(h.innerText || ''), from: previous, to: level, at: describe(h) });
    previous = level;
  }

  // --- the header's controls on a phone: size and the gap between neighbours
  const bar = document.querySelector('header.MuiAppBar-root .MuiToolbar-root');
  if (bar) {
    const items = [...bar.querySelectorAll('button, a[href]')].filter((el) => visible(el) && !el.parentElement.closest('button, a[href]'))
      .map((el) => ({ r: el.getBoundingClientRect(), name: short(el.innerText || el.getAttribute('aria-label') || '') }))
      .sort((a, b) => a.r.left - b.r.left);
    let minGap = Infinity;
    for (let i = 1; i < items.length; i++) minGap = Math.min(minGap, items[i].r.left - items[i - 1].r.right);
    result.header = {
      controls: items.length,
      smallest: items.length ? Math.round(Math.min(...items.map((i) => Math.min(i.r.width, i.r.height)))) : null,
      minGap: Number.isFinite(minGap) ? Math.round(minGap) : null,
      fitsWindow: items.every((i) => i.r.right <= vw + 1 && i.r.left >= -1),
      names: items.map((i) => i.name),
    };
  }
  return result;
};

// ---------------------------------------------------------------------------
// One engine/viewport/mode run
// ---------------------------------------------------------------------------
const run = async (engineName, viewport, mode) => {
  const label = `${engineName}-${viewport.width}x${viewport.height}-${mode}`;
  const dir = join(out, label);
  mkdirSync(dir, { recursive: true });
  const phone = viewport.width < 600;

  const browser = await engines[engineName].launch();
  // SERVICE_WORKERS=block: the panel's worker would fetch past the answers a view gives itself (Firefox, WebKit)
  const context = await browser.newContext({ viewport, colorScheme: mode, reducedMotion: 'reduce', ignoreHTTPSErrors: true, locale: 'en-GB', timezoneId: 'Europe/Bucharest', serviceWorkers: process.env.SERVICE_WORKERS === 'block' ? 'block' : 'allow' });
  // the skin's own switch (client/public/skin/mode.js) reads this
  await context.addInitScript((m) => { try { window.localStorage.setItem('ct-mode', m); } catch (e) { /* no storage */ } }, mode);
  // the installed app: the page asks the browser "am I in a window of my own?" (client/src/utils/pwaInstall.js)
  if (standalone) {
    await context.addInitScript(() => {
      const real = window.matchMedia ? window.matchMedia.bind(window) : null;
      if (real) {
        window.matchMedia = (query) => {
          if (!/display-mode\s*:\s*standalone/i.test(String(query))) return real(query);
          const noop = () => {};
          return { matches: true, media: String(query), onchange: null, addListener: noop, removeListener: noop, addEventListener: noop, removeEventListener: noop, dispatchEvent: () => false };
        };
      }
      try { Object.defineProperty(window.navigator, 'standalone', { value: true, configurable: true }); } catch (e) { /* read-only here */ }
    });
  }
  // the screen's reserved zones: the skin reads them from env(safe-area-inset-*) through --ct-safe-*
  if (safeArea) {
    await context.addInitScript(([top, right, bottom, left, hatch]) => {
      const apply = () => {
        const root = document.documentElement;
        if (!root) return;
        root.style.setProperty('--ct-safe-top', top + 'px');
        root.style.setProperty('--ct-safe-right', right + 'px');
        root.style.setProperty('--ct-safe-bottom', bottom + 'px');
        root.style.setProperty('--ct-safe-left', left + 'px');
        if (!hatch || !document.body || document.getElementById('gallery-safe-top')) return;
        const zone = (id, css) => {
          const el = document.createElement('div');
          el.id = id;
          el.setAttribute('aria-hidden', 'true');
          el.style.cssText = 'position:fixed;left:0;right:0;z-index:2147483647;pointer-events:none;' +
            'background:repeating-linear-gradient(45deg,rgba(255,0,90,.38) 0 5px,rgba(255,0,90,0) 5px 10px);' + css;
          document.body.appendChild(el);
        };
        if (top) zone('gallery-safe-top', 'top:0;height:' + top + 'px');
        if (bottom) zone('gallery-safe-bottom', 'bottom:0;height:' + bottom + 'px');
      };
      apply();
      document.addEventListener('DOMContentLoaded', apply);
      window.addEventListener('load', apply);
    }, [...safeArea, viewportShots]);
  }
  const page = await context.newPage();
  if (standalone && engineName === 'chromium') {
    // Chromium can be told for real, so that CSS media queries agree with the script above
    try {
      const cdp = await context.newCDPSession(page);
      await cdp.send('Emulation.setEmulatedMedia', { features: [{ name: 'display-mode', value: 'standalone' }] });
    } catch (e) { note(label, 'display-mode could not be emulated through the browser:', e.message.slice(0, 120)); }
  }

  // the API calls of the last minute, and those still running
  const stamps = [];
  const running = new Set();
  let lastActivity = Date.now();
  const isApi = (url) => /\/cosmos\/(api|rclone|_logs)/.test(url);
  page.on('request', (r) => { if (isApi(r.url())) { stamps.push(Date.now()); if (!/terminal|listen/.test(r.url())) running.add(r); lastActivity = Date.now(); } });
  const done = (r) => { if (running.delete(r)) lastActivity = Date.now(); };
  page.on('requestfinished', done);
  page.on('requestfailed', done);
  let throttled = 0;
  page.on('response', (r) => { if (r.status() === 429) throttled++; });
  let perLoad = 90; // grows to the largest page load seen
  const room = async (need) => {
    for (;;) {
      const now = Date.now();
      while (stamps.length && now - stamps[0] > 61000) stamps.shift();
      if (stamps.length + need <= apiPerMinute) return;
      await sleep(1000);
    }
  };
  const settle = async (extra = 0) => {
    const start = Date.now();
    while (Date.now() - start < 12000) {
      if (running.size === 0 && Date.now() - lastActivity > 500) break;
      await page.waitForTimeout(100);
    }
    await page.evaluate(() => (document.fonts ? document.fonts.ready.then(() => true) : true)).catch(() => {});
    await page.waitForTimeout(350 + extra);
  };

  let errors = [];
  page.on('pageerror', (e) => errors.push('error: ' + e.message.slice(0, 300)));
  page.on('console', (m) => { if (m.type() === 'error' && !/Failed to load resource|status of (4|5)\d\d|net::ERR|WebSocket|favicon/i.test(m.text())) errors.push('console: ' + m.text().slice(0, 300)); });

  // the page's tab list: tabs on a desktop, a select on a phone
  const openTab = async (index) => {
    // (a tablet in portrait gets the select too: ask the page which of the two it shows)
    const tabs = page.locator('main [role=tablist]').first();
    if (!phone && await tabs.isVisible().catch(() => false)) { await tabs.locator('[role=tab]').nth(index).click(); return; }
    const select = await page.evaluateHandle(() => {
      for (const s of document.querySelectorAll('main .MuiSelect-select')) {
        const root = s.closest('.MuiInputBase-root');
        if (root && root.parentElement && root.parentElement.querySelector(':scope > [role=tabpanel]')) return s;
      }
      return null;
    });
    const element = select.asElement();
    if (!element) throw new Error('no tab select on this page');
    await element.click();
    await page.locator('[role=listbox] li').nth(index).click();
  };

  const capture = async (name, problems, at) => {
    const file = join(dir, name + '.png');
    // a full-page picture taken on a scrolled page draws the fixed top bar in the middle of it
    // (`at`: one screen, scrolled so that the first element matching the selector is just under the bar)
    if (at) {
      const found = await page.evaluate((selector) => {
        // "prev:<selector>": the card before the one that holds the match (two cards one under the other)
        const previous = selector.startsWith('prev:');
        const el = document.querySelector(previous ? selector.slice(5) : selector);
        if (!el) return false;
        let card = el.closest('.MuiCard-root, .MuiPaper-root') || el;
        if (previous) {
          let up = card;
          while (up && !up.previousElementSibling) up = up.parentElement;
          if (up && up.previousElementSibling) card = up.previousElementSibling;
        }
        const bar = document.querySelector('header.MuiAppBar-root');
        window.scrollTo(0, Math.max(0, card.getBoundingClientRect().top + window.scrollY - (bar ? bar.getBoundingClientRect().height : 0) - 12));
        return true;
      }, at).catch(() => false);
      if (!found) problems.push('step: nothing matches ' + at);
    } else await page.evaluate(() => window.scrollTo(0, 0)).catch(() => {});
    await page.waitForTimeout(120);
    const height = await page.evaluate(() => document.documentElement.scrollHeight).catch(() => viewport.height);
    const limit = viewport.height * (phone ? 6 : 4);
    try {
      if (viewportShots || at) await page.screenshot({ path: file });
      else if (height > limit) await page.screenshot({ path: file, fullPage: true, clip: { x: 0, y: 0, width: viewport.width, height: limit } });
      else await page.screenshot({ path: file, fullPage: true });
    } catch (e) { problems.push('screenshot: ' + String(e.message).slice(0, 200)); }
    let measured = null;
    try { measured = await page.evaluate(audit); } catch (e) { problems.push('audit: ' + String(e.message).slice(0, 200)); }
    const summary = {
      view: name, engine: engineName, viewport: `${viewport.width}x${viewport.height}`, mode,
      url: page.url().replace(base, ''), file: `${label}/${name}.png`,
      errors: [...new Set(errors)], problems, ...(measured || {}),
    };
    errors = [];
    writeFileSync(join(dir, name + '.json'), JSON.stringify(summary, null, 1));
    const m = measured || {};
    note(label, name.padEnd(34),
      summary.errors.length ? `errors ${summary.errors.length}` : '',
      m.sidewaysScroll ? 'SIDEWAYS' : '',
      m.contrast && m.contrast.count ? `contrast ${m.contrast.count}` : '',
      m.smallText && m.smallText.count ? `small ${m.smallText.count}` : '',
      m.cutOff && m.cutOff.count ? `cut ${m.cutOff.count}` : '',
      m.overlaps && m.overlaps.count ? `overlap ${m.overlaps.count}` : '',
      problems.length ? 'PROBLEM ' + problems.join(' | ') : '');
  };

  const visit = async (view) => {
    if (only && !only.test(view.id)) return;
    if (smallSet && !view.core) return;
    await room(perLoad);
    const before = stamps.length;
    for (const [part, body, status, headers] of view.mock || []) await page.route((url) => url.pathname.startsWith(part), (route) => route.fulfill({ status: status || 200, json: body, headers }));
    for (const part of view.abort || []) await page.route((url) => url.pathname.startsWith(part), (route) => route.abort('connectionrefused'));
    for (const [part, change] of view.patch || []) {
      await page.route((url) => url.pathname === part || url.pathname.startsWith(part + '?'), async (route) => {
        try {
          const answer = await route.fetch();
          let json = null;
          try { json = await answer.json(); } catch (e) { /* not JSON: passed on */ }
          if (json === null) return route.fulfill({ response: answer });
          return route.fulfill({ response: answer, json: change(json, route.request().url()) });
        } catch (e) { return route.continue().catch(() => {}); }
      });
    }
    try { await page.goto(base + view.path, { waitUntil: 'domcontentloaded' }); } catch (e) { note(label, view.id, 'did not load:', e.message.slice(0, 120)); return; }
    await settle(view.wait || 0);
    perLoad = Math.max(perLoad, Math.min(stamps.length - before + 20, 260));
    for (const step of view.captures || [{}]) {
      if ((step.phoneOnly && !phone) || (step.desktopOnly && phone)) continue;
      const name = step.id ? `${view.id}--${step.id}` : view.id;
      const problems = [];
      try {
        if (step.tab !== undefined) { await room(60); await openTab(step.tab); }
        if (step.open) { await room(40); await step.open(page); }
        await settle(step.wait || 0);
      } catch (e) { problems.push('step: ' + String(e.message).split('\n')[0].slice(0, 200)); }
      await capture(name, problems, step.at);
      try {
        if (step.open) {
          if (step.close) await step.close(page); else await page.keyboard.press('Escape');
          await page.waitForTimeout(400);
        }
      } catch (e) { /* the next step reports what is in its way */ }
    }
    if (view.mock || view.patch || view.abort) await page.unrouteAll({ behavior: 'ignoreErrors' });
  };

  // the pages that need no session, then the login through its form (with a pause between them: the
  // server limits the requests of a visitor without a session, and a refused style sheet spoils a picture)
  const anonPause = Number(process.env.ANON_PAUSE || 6000);
  for (const view of anonymousViews) { await visit(view); if (!only || only.test(view.id)) await sleep(anonPause); }
  await room(perLoad);
  await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
  await page.locator('#nickname-login').fill(admin.nickname);
  await page.locator('#-password-login').fill(admin.password);
  await page.locator('button[type=submit]').first().click();
  await page.waitForURL((url) => !/\/login/.test(url.pathname), { timeout: 20000 }).catch(() => note(label, 'LOGIN DID NOT LEAVE THE FORM'));
  await settle();
  // most admin pages ask for the password a second time; answer it from a panel page
  const sudo = await page.evaluate((password) => fetch('/cosmos/api/sudo', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password }) }).then((r) => r.status), admin.password).catch((e) => String(e));
  note(label, 'logged in through the form, password confirmed:', sudo);
  errors = [];

  for (const view of views) await visit(view);
  if (throttled) note(label, 'THROTTLED RESPONSES:', throttled, '(pages may be incomplete: lower API_PER_MINUTE)');
  await browser.close();
};

// ---------------------------------------------------------------------------
// summary.json: every capture's counts, and the totals per run
// ---------------------------------------------------------------------------
const summarise = () => {
  const rows = [];
  for (const run of existsSync(out) ? readdirSync(out, { withFileTypes: true }).filter((d) => d.isDirectory()).map((d) => d.name).sort() : []) {
    for (const file of readdirSync(join(out, run)).filter((f) => f.endsWith('.json')).sort()) {
      const s = JSON.parse(readFileSync(join(out, run, file), 'utf8'));
      rows.push({
        run, view: s.view, title: s.title, errors: s.errors.length, problems: s.problems.length, sidewaysScroll: !!s.sidewaysScroll,
        contrast: s.contrast ? s.contrast.count : null, smallText: s.smallText ? s.smallText.count : null,
        cutOff: s.cutOff ? s.cutOff.count : null, truncated: s.truncated ? s.truncated.count : null,
        overlaps: s.overlaps ? s.overlaps.count : null, unnamed: s.unnamed ? s.unnamed.count : null,
        targetsBelow24: s.targets ? s.targets.below24 : null, targetsBelow44: s.targets ? s.targets.below44 : null,
        nestedFocus: s.nestedFocus ? s.nestedFocus.count : null, headingSkips: s.headingSkips ? s.headingSkips.count : null,
        headerMinGap: s.header ? s.header.minGap : null, headerSmallest: s.header ? s.header.smallest : null,
      });
    }
  }
  const totals = {};
  for (const row of rows) {
    const t = totals[row.run] || (totals[row.run] = { captures: 0, errors: 0, problems: 0, sidewaysScroll: 0, contrast: 0, smallText: 0, cutOff: 0, truncated: 0, overlaps: 0, unnamed: 0, targetsBelow24: 0, targetsBelow44: 0, nestedFocus: 0, headingSkips: 0, headerMinGap: null, headerSmallest: null });
    t.captures++;
    for (const key of Object.keys(t)) if (key !== 'captures' && key !== 'headerMinGap' && key !== 'headerSmallest') t[key] += Number(row[key]) || 0;
    // the header: the worst seen in the run
    for (const key of ['headerMinGap', 'headerSmallest']) if (row[key] !== null && (t[key] === null || row[key] < t[key])) t[key] = row[key];
  }
  writeFileSync(join(out, 'summary.json'), JSON.stringify({ totals, captures: rows }, null, 1));
  note('\nsummary:', join(out, 'summary.json'));
  for (const [run, t] of Object.entries(totals)) note(' ', run.padEnd(28), JSON.stringify(t));
};

if (process.argv[2] !== 'summary') {
  for (const engineName of engineNames) for (const viewport of viewports) for (const mode of modes) await run(engineName, viewport, mode);
}
summarise();
