// Fork addition (plan 50): runs every code block of the S3 guide as the panel
// shows it, from each place, against the test Cosmos and RustFS set up by
// scripts/fork-guide-examples.sh (read its header). It runs inside the
// Docker in Docker, with its Docker socket, on the home network (gx-lan).
//
// For each place (home, internet, container): the facts come from the real
// GET /api/dashboard and GET /api/status of the test Cosmos through
// client/src/utils/guides.js, exactly as the guide's page builds them; the
// guide's own administration blocks make the bucket and the key; then each
// chain of blocks (attribute chain=) runs in a fresh container of its kind
// (attribute runner=), in order, with the secret markers replaced by the
// test's values. A block passes when its commands exit 0. Then the limits are
// proven: one request of 150 MB through the stand-in is refused (413), the
// same through the home address passes.
import { readFileSync, writeFileSync, mkdirSync, appendFileSync } from 'node:fs';
import { execFileSync, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { parseGuide, codeBlocks, renderCode, guideFacts, visibleSections } from '../client/src/utils/guides.js';
import { groupApps, mergeDashboard } from '../client/src/utils/appInterfaces.js';

const OUT = process.env.OUT || '/scratch/out';
const ONLY = (process.env.ONLY || '').split(',').filter(Boolean);
const PLACES = (process.env.PLACES || 'home,internet,container').split(',').filter(Boolean);
const BASE = 'https://cosmos.example.org:2053';
const admin = { nickname: 'admin', password: 'Gx-test-password-0420' };
mkdirSync(OUT, { recursive: true });
const LOG = OUT + '/examples.log';
writeFileSync(LOG, '');
const log = (...a) => { const line = a.join(' '); console.log(line); appendFileSync(LOG, line + '\n'); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const results = [];

// ---------------------------------------------------------------------------
// the test Cosmos

let cookie = '';
const call = async (method, path, data) => {
  try {
    const r = await fetch(BASE + '/cosmos/api/' + path, {
      method, headers: { 'content-type': 'application/json', ...(cookie ? { cookie } : {}) }, body: data === undefined ? undefined : JSON.stringify(data),
    });
    const set = r.headers.getSetCookie ? r.headers.getSetCookie() : [];
    if (set.length) cookie = set.map((c) => c.split(';')[0]).join('; ');
    let body = null;
    try { body = await r.json(); } catch (e) { /* not JSON */ }
    return { status: r.status, body };
  } catch (e) { return { status: 0, body: null }; }
};
const waitUp = async () => {
  for (let i = 0; i < 120; i++) {
    if ((await call('GET', 'status')).status > 0) return;
    await sleep(1000);
  }
  throw new Error('the test Cosmos does not answer');
};

const route = (Name, Host, Target) => ({
  Name, Description: '', UseHost: true, Host, Target, Mode: 'SERVAPP', UsePathPrefix: false, PathPrefix: '', StripPathPrefix: false,
  AuthEnabled: false, Timeout: 14400000, ThrottlePerMinute: 0, CORSOrigin: '', SmartShield: { Enabled: false },
});

const setUp = async () => {
  await waitUp();
  if ((await call('POST', 'newInstall', { step: '2' })).status === 200) {
    log('wizard', (await call('POST', 'newInstall', { step: '3', httpsCertificateMode: 'SELFSIGNED', hostname: 'cosmos.example.org' })).status);
    await sleep(1500);
    await call('POST', 'newInstall', { step: '4', ...admin, email: 'admin@example.org' });
    await call('POST', 'newInstall', { step: '5' });
    await sleep(6000);
    await waitUp();
  }
  log('login', (await call('POST', 'login', admin)).status, 'sudo', (await call('POST', 'sudo', { password: admin.password })).status);
  const config = (await call('GET', 'config')).body.data;
  const wanted = ['s3', 's3-internet', 'rustfs-console'];
  const have = (config.HTTPConfig.ProxyConfig.Routes || []).map((r) => r.Name);
  if (!wanted.every((n) => have.includes(n)) || !(config.HTTPConfig.Cloudflare && (config.HTTPConfig.Cloudflare.Shielded || []).includes('s3.example.org'))) {
    config.HTTPConfig.ProxyConfig.Routes = [
      route('s3', 's3.local', 'http://rustfs:9000'),
      route('s3-internet', 's3.example.org', 'http://rustfs:9000'),
      route('rustfs-console', 'rustfs.local', 'http://rustfs:9001'),
    ];
    config.HTTPConfig.Cloudflare = { ...(config.HTTPConfig.Cloudflare || {}), Shielded: ['s3.example.org'], TrustRanges: true };
    log('save the URLs', (await call('PUT', 'config', config)).status);
    await sleep(5000);
    await waitUp();
    await call('POST', 'login', admin);
  }
};

// ---------------------------------------------------------------------------
// running

const docker = (...args) => spawnSync('docker', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });

const PLACE_NET = {
  home: ['--network', 'gx-lan', '--add-host', 's3.local:192.168.60.1', '--add-host', 'rustfs.local:192.168.60.1'],
  internet: ['--network', 'gx-inet', '--add-host', 's3.example.org:203.0.113.2'],
  container: ['--network', 'storage-net'],
};

// what a runner starts before the blocks (a database to back up), as root
const SETUP = {
  linux: 'service mariadb start >/dev/null 2>&1; mariadb -e "create database if not exists shop; create table if not exists shop.orders (id int); insert into shop.orders values (1),(2),(3);"',
  pg: 'su postgres -c "initdb -D /tmp/pg >/dev/null && pg_ctl -D /tmp/pg -o \\"-k /var/run/postgresql\\" -l /tmp/pg.log start >/dev/null" && sleep 2 && createdb shop && psql -q -d shop -c "create table orders (id int); insert into orders values (1),(2),(3);"',
  maria: '(MARIADB_ALLOW_EMPTY_ROOT_PASSWORD=1 docker-entrypoint.sh mariadbd >/tmp/db.log 2>&1 &) ; for i in $(seq 1 60); do mariadb -e "select 1" >/dev/null 2>&1 && break; sleep 1; done; mariadb -e "create database if not exists shop; create table if not exists shop.orders (id int); insert into shop.orders values (1),(2),(3);"',
  mongo: '(mongod --bind_ip 127.0.0.1 --dbpath /tmp --fork --logpath /tmp/mongo.log >/dev/null) && mongosh --quiet --eval "db.getSiblingDB(\\"shop\\").orders.insertMany([{a:1},{a:2},{a:3}])" >/dev/null',
  php: 'service mariadb start >/dev/null 2>&1; mariadb -e "create database if not exists site; create user if not exists site@localhost identified by \'$DBPASS\'; grant all on site.* to site@localhost; create table if not exists site.posts (id int, title text); insert into site.posts values (1, \'Hello\'), (2, \'Here\');"',
};
const IMAGE = { linux: 'gx-linux', pg: 'gx-pg', maria: 'gx-maria', mongo: 'gx-mongo', aws: 'gx-aws', python: 'gx-python', node: 'gx-node', php: 'gx-php', go: 'gx-go', curl: 'gx-curl', pwsh: 'gx-pwsh', docker: 'gx-docker' };
// chains that read the Linux section's settings file first
const NEEDS_ENV = new Set(['pg', 'maria', 'mongo']);
// where the certificate file goes on each kind of machine
const CA_PATH = { pwsh: '/root/cosmos-ca.pem', docker: '/work/cosmos-ca.pem' };

const secrets = (place) => ({
  'PASTE-ACCESS-KEY-HERE': place.name + '-backup',
  'PASTE-SECRET-KEY-HERE': place.secret,
  'CHOOSE-A-SECRET-KEY-HERE': place.secret,
  'CHOOSE-A-REPOSITORY-PASSWORD-HERE': place.repoPassword,
  'PASTE-DATABASE-PASSWORD-HERE': place.dbPassword,
});
const unmark = (text, place) => Object.entries(secrets(place)).reduce((t, [k, v]) => t.split(k).join(v), text);

const shellQuote = (s) => "'" + String(s).replace(/'/g, "'\\''") + "'";

// one chain in one fresh container: returns per block { id, ok, output }
const runChain = (chain, blocks, place, facts) => {
  const runner = blocks[0].runner;
  const dir = '/scratch/out/run/' + place.key + '-' + chain;
  execFileSync('rm', ['-rf', dir]);
  mkdirSync(dir, { recursive: true });
  const steps = [];
  const pre = [];
  if (runner !== 'rustfs' && facts.flags.ca && facts.values.ca_pem) {
    const path = CA_PATH[runner] || '/etc/ssl/cosmos-ca.pem';
    pre.push('mkdir -p ' + path.replace(/\/[^/]+$/, '') + ' && cat > ' + path + " <<'PEM'\n" + facts.values.ca_pem + '\nPEM');
  }
  if (NEEDS_ENV.has(runner)) {
    const env = codeBlocks(place.guide).find((b) => b.id === 'linux-env');
    pre.push(unmark(renderCode(env, facts).text, place));
    if (runner === 'mongo') pre.push("echo 'MONGO_URI=mongodb://127.0.0.1:27017' >> /root/.s3-backup.env");
  }
  if (SETUP[runner]) pre.unshift(SETUP[runner]);
  writeFileSync(dir + '/00-pre.sh', 'set -e\n' + pre.join('\n') + '\n');
  blocks.forEach((b, i) => {
    const rendered = renderCode(b, facts);
    const text = unmark(rendered.text, place);
    const n = String(i + 1).padStart(2, '0');
    if (b.attrs.file) writeFileSync(dir + '/' + n + '.file', text + '\n');
    else writeFileSync(dir + '/' + n + (b.lang === 'powershell' ? '.ps1' : '.sh'), text + '\n');
    steps.push({ b, n });
  });
  // the driver: each block in turn, in one shell (sh blocks share their environment, as in one terminal)
  const driver = ['set -e', 'cd /work', 'echo "=== pre"', '. /steps/00-pre.sh', 'echo "=== pre ok"'];
  steps.forEach(({ b, n }) => {
    driver.push('echo "=== step ' + b.id + '"');
    if (b.attrs.file) driver.push('cp /steps/' + n + '.file /work/' + b.attrs.file);
    else if (b.lang === 'powershell') driver.push('pwsh -NoProfile -NonInteractive -File /steps/' + n + '.ps1');
    else driver.push('. /steps/' + n + '.sh');
    driver.push('echo "=== ok ' + b.id + '"');
  });
  writeFileSync(dir + '/driver.sh', driver.join('\n') + '\n');
  let res;
  if (runner === 'rustfs') {
    // the app's Terminal: a shell inside the app's own container
    docker('exec', 'rustfs', 'rm', '-rf', '/tmp/steps');
    docker('cp', dir, 'rustfs:/tmp/steps');
    const script = readFileSync(dir + '/driver.sh', 'utf8').replace(/\/steps\//g, '/tmp/steps/').replace('cd /work', 'cd /tmp');
    res = docker('exec', 'rustfs', 'sh', '-c', script);
  } else {
    const name = 'gx-run-' + place.key + '-' + chain;
    docker('rm', '-f', '-v', name);
    const extra = [];
    if (runner === 'docker') extra.push('-v', '/var/run/docker.sock:/var/run/docker.sock');
    if (runner === 'php') extra.push('-e', 'DBPASS=' + place.dbPassword, '-e', 'DB_HOST=localhost');
    // the steps are copied in (not mounted), so that a run cannot write into the scratch folder's other files
    docker('create', '--name', name, ...PLACE_NET[place.key], '--entrypoint', 'sh', '-w', '/work', ...extra, IMAGE[runner], '-c', 'mkdir -p /work && sh /steps/driver.sh');
    docker('cp', dir, name + ':/steps');
    res = docker('start', '-a', name);
    docker('rm', '-f', '-v', name);
  }
  const output = (res.stdout || '') + (res.stderr || '');
  writeFileSync(dir + '/output.txt', output);
  const done = new Set([...output.matchAll(/^=== ok (\S+)$/gm)].map((m) => m[1]));
  const preOk = /^=== pre ok$/m.test(output);
  return blocks.map((b) => ({ id: b.id, ok: preOk && done.has(b.id), output: output.slice(-1500) }));
};

// ---------------------------------------------------------------------------

const guide = parseGuide(readFileSync('/repo/src/guides/s3.md', 'utf8'));
await setUp();
const dashboard = (await call('GET', 'dashboard')).body.data;
const status = (await call('GET', 'status')).body.data;
const configRoutes = (await call('GET', 'config')).body.data.HTTPConfig.ProxyConfig.Routes;
log('dashboard', JSON.stringify(dashboard.map((r) => ({ n: r.Name, role: r.ForkRole, guide: r.ForkGuide, src: r.ForkGuideSource, cf: r.ForkShielded, ca: r.ForkOwnCAHosts, net: r.ForkNetworks }))));
const app = groupApps(mergeDashboard(configRoutes, dashboard)).find((a) => a.container === 'rustfs');
if (!app) { log('FAIL the RustFS app is not in the dashboard'); process.exit(1); }

for (const key of PLACES) {
  const where = key === 'internet' ? 'away' : 'home';
  const name = { home: 'home1', internet: 'inet1', container: 'ctr1' }[key];
  const place = { key, name, guide, secret: randomBytes(15).toString('hex'), repoPassword: 'Repo-' + randomBytes(9).toString('hex'), dbPassword: 'Db-' + randomBytes(9).toString('hex') };
  const ctx = { place: { where, sure: true }, location: { protocol: 'https:', hostname: 'cosmos.example.org', port: '2053', origin: BASE }, isAdmin: true };
  const facts = guideFacts(app, ctx, status, key, { name, bucket: 'backups-' + name });
  log('\n## place', key, 'endpoint', facts.values.endpoint, 'cloudflare', facts.flags.cloudflare, 'own CA', facts.flags.ca, 'rustfs', facts.flags.rustfs);
  if (facts.choice !== key) { log('FAIL the place', key, 'has no address'); results.push({ place: key, id: '(place)', ok: false }); continue; }
  const sections = visibleSections(guide, facts.flags).map((s) => s.id);
  const blocks = codeBlocks(guide).filter((b) => (!b.section || sections.includes(b.section)) && b.conds.every((c) => (c.startsWith('!') ? !facts.flags[c.slice(1)] : facts.flags[c])));
  const chains = new Map();
  blocks.forEach((b) => {
    if (!b.runner) { results.push({ place: key, id: b.id, ok: null, note: 'not run: no runner (Windows only)' }); return; }
    if (b.runner === 'docker' && key !== 'container') { results.push({ place: key, id: b.id, ok: null, note: 'run for the container place only' }); return; }
    const chain = b.attrs.chain || b.id;
    if (!chains.has(chain)) chains.set(chain, []);
    chains.get(chain).push(b);
  });
  // the administration first: it makes the bucket and the key the others use
  const order = ['admin', ...[...chains.keys()].filter((c) => c !== 'admin')];
  for (const chain of order) {
    const list = chains.get(chain);
    if (!list) continue;
    if (ONLY.length && chain !== 'admin' && !list.some((b) => ONLY.includes(b.id))) continue;
    if (chain === 'docker') docker('run', '--rm', '-v', 'app-data:/d', 'alpine:3.22', 'sh', '-c', 'echo "app data" > /d/state.txt');
    const started = Date.now();
    const out = runChain(chain, list, place, facts);
    out.forEach((r) => {
      results.push({ place: key, chain, ...r });
      log((r.ok ? 'ok   ' : 'FAIL ') + key + ' ' + r.id + ' (' + list[0].runner + ', ' + Math.round((Date.now() - started) / 1000) + ' s)');
    });
    if (out.some((r) => !r.ok)) log('---- output of ' + key + '/' + chain + ' (end):\n' + out[0].output + '\n----');
  }
}

// the limits, proven: one request of 150 MB through the stand-in is refused, through the home address it passes
const proof = (net, endpoint, extra) => docker('run', '--rm', ...net, ...extra, 'gx-curl', 'sh', '-c',
  'head -c 157286400 /dev/zero > /tmp/big && curl -sS -o /dev/null -w "%{http_code}" --aws-sigv4 "aws:amz:us-east-1:s3" --user "gx-admin:Gx-test-admin-secret-0420" --upload-file /tmp/big ' + endpoint + '/backups-home1/proof/big.bin');
if (PLACES.includes('internet')) {
  const r = proof(PLACE_NET.internet, 'https://s3.example.org:2053', []);
  const ok = r.stdout.trim() === '413';
  results.push({ place: 'internet', id: 'limit-150mb-single-request', ok, note: 'HTTP ' + r.stdout.trim() });
  log((ok ? 'ok   ' : 'FAIL ') + 'internet: one request of 150 MB through the stand-in answers ' + r.stdout.trim() + ' (413 expected)');
}
if (PLACES.includes('home')) {
  const ca = '/scratch/out/cosmos-ca.pem';
  writeFileSync(ca, String(status.CACert || '').trim() + '\n');
  const r = proof(PLACE_NET.home, 'https://s3.local:2053', ['-v', ca + ':/ca.pem:ro', '-e', 'CURL_CA_BUNDLE=/ca.pem']);
  const ok = r.stdout.trim() === '200';
  results.push({ place: 'home', id: 'limit-150mb-home', ok, note: 'HTTP ' + r.stdout.trim() });
  log((ok ? 'ok   ' : 'FAIL ') + 'home: one request of 150 MB through the home address answers ' + r.stdout.trim() + ' (200 expected)');
}

writeFileSync(OUT + '/results.json', JSON.stringify(results.map(({ output, ...r }) => r), null, 1));
const failed = results.filter((r) => r.ok === false);
log('\n' + results.filter((r) => r.ok).length + ' passed, ' + failed.length + ' failed, ' + results.filter((r) => r.ok === null).length + ' not run');
process.exit(failed.length ? 1 : 0);
