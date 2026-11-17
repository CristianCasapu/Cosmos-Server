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
import { parseGuide, codeBlocks, renderCode, guideFacts, visibleSections, limitParts } from '../client/src/utils/guides.js';
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
let base = BASE;
const call = async (method, path, data) => {
  try {
    const r = await fetch(base + '/cosmos/api/' + path, {
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

const route = (Name, Host, Target, more = {}) => ({
  Name, Description: '', UseHost: true, Host, Target, Mode: 'SERVAPP', UsePathPrefix: false, PathPrefix: '', StripPathPrefix: false,
  AuthEnabled: false, Timeout: 14400000, ThrottlePerMinute: 0, CORSOrigin: '', SmartShield: { Enabled: false }, ...more,
});

const setUp = async () => {
  // before the setup wizard the server answers on plain HTTP only
  base = 'http://cosmos.example.org:2052';
  await waitUp();
  if ((await call('POST', 'newInstall', { step: '2' })).status === 200) {
    log('wizard', (await call('POST', 'newInstall', { step: '3', httpsCertificateMode: 'SELFSIGNED', hostname: 'cosmos.example.org' })).status);
    await sleep(1500);
    await call('POST', 'newInstall', { step: '4', ...admin, email: 'admin@example.org' });
    await call('POST', 'newInstall', { step: '5' });
    await sleep(6000);
  }
  base = BASE;
  await waitUp();
  log('login', (await call('POST', 'login', admin)).status, 'sudo', (await call('POST', 'sudo', { password: admin.password })).status);
  const config = (await call('GET', 'config')).body.data;
  const wanted = ['s3', 's3-internet', 'rustfs-console', 's3-shield', 's3-timeout'];
  const have = (config.HTTPConfig.ProxyConfig.Routes || []).map((r) => r.Name);
  if (!wanted.every((n) => have.includes(n))) {
    config.HTTPConfig.ProxyConfig.Routes = [
      // the home address as the guide proposes it for backups: no shield, no throttle, no timeout
      route('s3', 's3.local', 'http://rustfs:9000', { Timeout: 0 }),
      route('s3-internet', 's3.example.org', 'http://rustfs:9000'),
      route('rustfs-console', 'rustfs.local', 'http://rustfs:9001'),
      // the same storage behind Cosmos' shield (a small budget, to see it act) and behind a short timeout
      route('s3-shield', 's3s.local', 'http://rustfs:9000', { SmartShield: { Enabled: true, PerUserRequestLimit: 20, PolicyStrictness: 1 } }),
      route('s3-timeout', 's3t.local', 'http://rustfs:9000', { Timeout: 20000 }),
    ];
    log('save the URLs', (await call('PUT', 'config', config)).status);
    await sleep(5000);
    await waitUp();
    await call('POST', 'login', admin);
  }
};

// ---------------------------------------------------------------------------
// running

// every call is bounded: a chain that hangs (a client retrying for ever) fails after 12 minutes
const docker = (...args) => spawnSync('docker', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, timeout: 12 * 60 * 1000 });

const PLACE_NET = {
  home: ['--network', 'gx-lan', '--add-host', 's3.local:192.168.60.1', '--add-host', 'rustfs.local:192.168.60.1'],
  internet: ['--network', 'gx-inet', '--add-host', 's3.example.org:203.0.113.2'],
  container: ['--network', 'storage-net'],
};

// what a runner starts before the blocks (a database to back up), as root
const MARIA_DATA = 'create database if not exists shop; create table if not exists shop.orders (id int); insert into shop.orders values (1),(2),(3);';
const SETUP = {
  linux: 'service mariadb start >/dev/null 2>&1; mariadb -e "' + MARIA_DATA + '"',
  pg: 'su postgres -c "initdb -D /tmp/pg >/dev/null && pg_ctl -D /tmp/pg -o \\"-k /var/run/postgresql\\" -l /tmp/pg.log start >/dev/null" && sleep 2 && runuser -u postgres -- createdb shop && runuser -u postgres -- psql -q -d shop -c "create table orders (id int); insert into orders values (1),(2),(3);"',
  maria: '(MARIADB_ALLOW_EMPTY_ROOT_PASSWORD=1 docker-entrypoint.sh mariadbd >/tmp/db.log 2>&1 &) ; for i in $(seq 1 90); do grep -q "port: 3306" /tmp/db.log && mariadb -e "select 1" >/dev/null 2>&1 && break; sleep 1; done; mariadb -e "' + MARIA_DATA + '"',
  // MongoDB with its access control on: the guide's tools sign in with the config file's address
  mongo: 'mongod --bind_ip 127.0.0.1 --dbpath /tmp --fork --logpath /tmp/mongo.log --auth >/dev/null && mongosh --quiet admin --eval "db.createUser({user: \\"backup\\", pwd: \\"$DBPASS\\", roles: [\\"root\\"]})" >/dev/null && mongosh --quiet -u backup -p "$DBPASS" --authenticationDatabase admin --eval "db.getSiblingDB(\\"shop\\").orders.insertMany([{a:1},{a:2},{a:3}])" >/dev/null',
  // a site database with what a plain dump gets wrong: foreign keys (child created first), a view,
  // a trigger, a routine, utf8mb4 text, binary data and a table of 200 MB
  php: 'service mariadb start >/dev/null 2>&1; mariadb < /usr/local/share/site.sql && mariadb -e "create user if not exists site@localhost identified by \'$DBPASS\'; grant all on site.* to site@localhost; grant all on site_restored.* to site@localhost; grant all on site_restored2.* to site@localhost;"',
};
const IMAGE = { linux: 'gx-linux', pg: 'gx-pg', maria: 'gx-maria', mongo: 'gx-mongo', aws: 'gx-aws', python: 'gx-python', node: 'gx-node', php: 'gx-php', go: 'gx-go', curl: 'gx-curl', pwsh: 'gx-pwsh', docker: 'gx-docker' };
// chains that read the Linux section's settings file (and restic's password) first, as the guide says
const NEEDS_ENV = new Set(['pg', 'maria', 'mongo', 'aws', 'python', 'node', 'go']);
// where the certificate file goes on each kind of machine (the guide's own places)
const CA_PATH = { pwsh: '/work/cosmos-ca.pem', docker: '/work/cosmos-ca.pem', php: '/work/cosmos-ca.pem' };

// checks run after a chain, in the same container (the test's own, not the guide's): each must exit 0
const POST = {
  rustfs: [
    ['the administrator\'s key is not left in rc\'s settings', 'rc alias list | grep -q adm && exit 1; ! grep -q "secret" "$HOME/.config/rc/config.toml"'],
  ],
  linux: [
    ['secret files are mode 600', 'test "$(stat -c %a /root/.s3-backup.env)" = 600 && test "$(stat -c %a /root/.restic-password)" = 600'],
    ['the timer\'s units are valid', 'systemd-analyze verify /etc/systemd/system/s3-backup.service /etc/systemd/system/s3-backup.timer 2>&1 | grep -v -E "^$|Failed to (connect|open)|Cannot (add|access)|Unit is bound to inactive" | grep . && exit 1 || true'],
    ['a dump that fails halfway saves no snapshot and fails the command',
      'set -a; . /root/.s3-backup.env; set +a; before=$(restic snapshots --json | grep -o "\\"id\\"" | wc -l); mkdir -p /tmp/shim; printf "#!/bin/sh\\necho \\"CREATE TABLE half (\\"\\nexit 2\\n" > /tmp/shim/mariadb-dump; chmod 755 /tmp/shim/mariadb-dump; if PATH=/tmp/shim:$PATH bash -e /steps/DB_STEP; then echo "the failed dump made the command succeed"; exit 1; fi; after=$(restic snapshots --json | grep -o "\\"id\\"" | wc -l); test "$before" = "$after" || { echo "a snapshot was saved ($before -> $after)"; exit 1; }'],
  ],
  php: [
    ['the dump (mysqldump) restores into an empty database, the same', 'sh /usr/local/share/site-compare.sh site_restored'],
    ['the PHP dumper too', 'sed -i "s/\'dumper\'   => \'auto\'/\'dumper\'   => \'php\'/" backup-config.php && grep -q "=> \'php\'" backup-config.php && php backup.php && php list.php && sh /usr/local/share/site-compare.sh site_restored2'],
    ['40 runs keep two objects (the rest are versions the bucket\'s rule clears), and the dump of three runs ago comes back', 'sh /usr/local/share/php-40-runs.sh'],
    ['nothing is left in the working folder but the downloaded dump', 'rm -f backup-work/restored.sql; test -z "$(ls -A backup-work)"'],
    ['the script refuses to back up a folder that holds its settings', 'sed -i "s#\'site\'     => \'/home/you/public_html\'#\'site\'     => \'/\'#" backup-config.php && grep -q "=> \'/\'," backup-config.php && if php backup.php; then exit 1; fi; cp /tmp/full-config.php backup-config.php'],
    ['the script refuses a working folder inside the web folder', 'sed -i "s#\'web_root\' => \'/home/you/public_html\'#\'web_root\' => \'/work\'#" backup-config.php && if php backup.php; then exit 1; fi'],
  ],
};

const secrets = (place) => ({
  'PASTE-ACCESS-KEY-HERE': place.name + '-backup',
  'PASTE-SECRET-KEY-HERE': place.secret,
  'CHOOSE-A-REPOSITORY-PASSWORD-HERE': place.repoPassword,
  'PASTE-DATABASE-PASSWORD-HERE': place.dbPassword,
  'PASTE-FOLDER-TO-BACK-UP-HERE': '/root/Documents',
  'PASTE-TIME-BEFORE-DAMAGE-HERE': place.before || '',
});
const unmark = (text, place) => Object.entries(secrets(place)).reduce((t, [k, v]) => t.split(k).join(v), text);

// what a step printed, between its markers
const stepOutput = (output, id) => {
  const m = output.match(new RegExp('^=== step ' + id + '\\n([\\s\\S]*?)^=== ok ' + id + '$', 'm'));
  return m ? m[1] : '';
};

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
    pre.push('mkdir -p ' + path.replace(/\/[^/]+$/, '') + ' && cat > ' + path + " <<'COSMOS_CA_END'\n" + facts.values.ca_pem + '\nCOSMOS_CA_END');
  }
  if (NEEDS_ENV.has(runner) || chain === 'recover' || chain === 'check') {
    // the Linux section's settings file and restic's password, as that section made them
    const env = codeBlocks(place.guide).find((b) => b.id === 'linux-env');
    pre.push("install -m 600 /dev/null /root/.s3-backup.env && cat > /root/.s3-backup.env <<'SETTINGS_END'\n" + unmark(renderCode(env, facts).text, place) + '\nSETTINGS_END');
    pre.push("install -m 600 /dev/null /root/.restic-password && printf '%s\\n' '" + place.repoPassword + "' > /root/.restic-password");
  }
  if (SETUP[runner]) pre.unshift(SETUP[runner]);
  writeFileSync(dir + '/00-pre.sh', 'set -e\n' + pre.join('\n') + '\n');
  let dbStep = '';
  blocks.forEach((b, i) => {
    const rendered = renderCode(b, facts);
    const text = unmark(rendered.text, place);
    const n = String(i + 1).padStart(2, '0');
    if (b.attrs.file) writeFileSync(dir + '/' + n + '.file', text + '\n');
    else writeFileSync(dir + '/' + n + (b.lang === 'powershell' ? '.ps1' : '.sh'), text + '\n');
    if (b.id === 'linux-restic-db') dbStep = n + '.sh';
    steps.push({ b, n });
  });
  // the driver: each block in turn, in one shell (blocks share their environment, as in one terminal);
  // a file block is pasted into its file (an existing file keeps its mode, as an editor does)
  const driver = ['set -e', 'cd /work', 'echo "=== pre"', '. /steps/00-pre.sh', 'echo "=== pre ok"'];
  steps.forEach(({ b, n }) => {
    driver.push('echo "=== step ' + b.id + '"');
    if (b.attrs.file) driver.push('cp /steps/' + n + '.file ' + (b.attrs.file.startsWith('/') ? b.attrs.file : '/work/' + b.attrs.file));
    else if (b.lang === 'powershell') driver.push('pwsh -NoProfile -NonInteractive -File /steps/' + n + '.ps1');
    else driver.push('. /steps/' + n + '.sh');
    driver.push('echo "=== ok ' + b.id + '"');
  });
  (POST[runner] || []).forEach(([what, script], i) => {
    if (runner === 'linux' && chain !== 'linux') return;
    const file = 'post' + i + '.sh';
    writeFileSync(dir + '/' + file, script.replace('DB_STEP', dbStep) + '\n');
    driver.push('echo "=== check ' + i + '"', '("$(command -v bash || echo sh)" -e /steps/' + file + ') && echo "=== check ok ' + i + '" || echo "=== check failed ' + i + '"');
  });
  writeFileSync(dir + '/driver.sh', driver.join('\n') + '\n');
  let res;
  if (runner === 'rustfs') {
    // the app's Terminal: a shell inside the app's own container
    const into = '/tmp/steps-' + place.key + '-' + Date.now();
    docker('cp', dir, 'rustfs:' + into);
    const script = readFileSync(dir + '/driver.sh', 'utf8').replace(/\/steps\//g, into + '/').replace('cd /work', 'cd /tmp');
    res = docker('exec', 'rustfs', 'sh', '-c', script);
  } else {
    const name = 'gx-run-' + place.key + '-' + chain;
    docker('rm', '-f', '-v', name);
    const extra = ['-e', 'DBPASS=' + place.dbPassword, '-e', 'ENDPOINT=' + facts.values.endpoint];
    if (runner === 'docker') extra.push('-v', '/var/run/docker.sock:/var/run/docker.sock');
    // the steps are copied in (not mounted), so that a run cannot write into the scratch folder's other files;
    // bash where the machine has it (as a person's terminal), else sh
    docker('create', '--name', name, ...PLACE_NET[place.key], '--entrypoint', 'sh', '-w', '/work', ...extra, IMAGE[runner], '-c',
      'mkdir -p /work && if command -v bash >/dev/null; then bash /steps/driver.sh; else sh /steps/driver.sh; fi');
    docker('cp', dir, name + ':/steps');
    res = docker('start', '-a', name);
    if (res.error) res = { stdout: (res.stdout || '') + '\n(the chain did not end within 12 minutes)\n', stderr: res.stderr || '' };
    docker('rm', '-f', '-v', name);
  }
  const output = (res.stdout || '') + (res.stderr || '');
  writeFileSync(dir + '/output.txt', output);
  const done = new Set([...output.matchAll(/^=== ok (\S+)$/gm)].map((m) => m[1]));
  const preOk = /^=== pre ok$/m.test(output);
  const started = new Set([...output.matchAll(/^=== step (\S+)$/gm)].map((m) => m[1]));
  const out = blocks.map((b) => ({ id: b.id, ok: preOk && done.has(b.id), note: done.has(b.id) ? '' : started.has(b.id) ? 'failed' : 'not reached', output: output.slice(-1500) }));
  (POST[runner] || []).forEach(([what], i) => {
    if (runner === 'linux' && chain !== 'linux') return;
    const ok = new RegExp('^=== check ok ' + i + '$', 'm').test(output);
    out.push({ id: 'check: ' + what, ok, note: ok ? '' : 'failed', output: output.slice(-1500) });
  });
  return { out, output };
};

// the test's attack, with the machine's own key: every file of the bucket deleted, restic's
// repository configuration overwritten with garbage (what a broken-into machine can do)
const attack = (place, facts) => {
  const env = codeBlocks(place.guide).find((b) => b.id === 'linux-env');
  const script = ['set -e',
    "install -m 600 /dev/null /root/.s3-backup.env && cat > /root/.s3-backup.env <<'SETTINGS_END'\n" + unmark(renderCode(env, facts).text, place) + '\nSETTINGS_END',
    facts.flags.ca ? "mkdir -p /etc/ssl && cat > /etc/ssl/cosmos-ca.pem <<'COSMOS_CA_END'\n" + facts.values.ca_pem + '\nCOSMOS_CA_END' : 'true',
    'set -a; . /root/.s3-backup.env; set +a',
    'echo garbage | rclone rcat store:' + facts.values.bucket + '/restic/config',
    'rclone delete store:' + facts.values.bucket + '/www',
    'rclone delete store:' + facts.values.bucket + '/restic/index',
    // garbage until the bucket's quota is full (the test set it just above what the bucket holds)
    'for i in 1 2 3 4 5 6; do head -c 30000000 /dev/urandom | rclone rcat store:' + facts.values.bucket + '/restic/data/zz/garbage$i 2>/dev/null || { echo "the bucket is full after $i pieces of garbage"; break; }; done',
    'echo attacked'].join('\n');
  return docker('run', '--rm', ...PLACE_NET[place.key], '--entrypoint', 'bash', 'gx-linux', '-c', script);
};

// ---------------------------------------------------------------------------

const guide = parseGuide(readFileSync('/repo/src/guides/s3.md', 'utf8'));
await setUp();
if (process.env.SETUP_ONLY) process.exit(0);
const dashboard = (await call('GET', 'dashboard')).body.data;
const status = (await call('GET', 'status')).body.data;
const configRoutes = (await call('GET', 'config')).body.data.HTTPConfig.ProxyConfig.Routes;
log('dashboard', JSON.stringify(dashboard.map((r) => ({ n: r.Name, role: r.ForkRole, guide: r.ForkGuide, src: r.ForkGuideSource, cf: r.ForkShielded, ca: r.ForkOwnCAHosts, net: r.ForkNetworks }))));
const app = groupApps(mergeDashboard(configRoutes, dashboard)).find((a) => a.container === 'rustfs');
if (!app) { log('FAIL the RustFS app is not in the dashboard'); process.exit(1); }

let homePlace = null;
for (const key of PLACES) {
  const where = key === 'internet' ? 'away' : 'home';
  const name = { home: 'home1', internet: 'inet1', container: 'ctr1' }[key];
  const place = { key, name, guide, secret: '', repoPassword: 'Repo-' + randomBytes(9).toString('hex'), dbPassword: 'Db-' + randomBytes(9).toString('hex') };
  const ctx = { place: { where, sure: true }, location: { protocol: 'https:', hostname: 'cosmos.example.org', port: '2053', origin: BASE }, isAdmin: true };
  const facts = guideFacts(app, ctx, status, key, { name, bucket: 'backups-' + name });
  log('app', JSON.stringify(app.routes.map((r) => [r.Name, r.Host, r.UseHost, r.Target, r.Mode, r.ForkRole, r.ForkGuide, r.UsePathPrefix, r.PathPrefix])), JSON.stringify(facts.places));
  if (key !== 'container') {
    const parts = limitParts(facts.limits).map((p) => p.what).join(',');
    const want = key === 'home' ? '' : 'timeout';
    const okLimits = facts.limits && parts === want;
    results.push({ place: key, id: 'limits-of-the-address', ok: !!okLimits, note: parts });
    log((okLimits ? 'ok   ' : 'FAIL ') + key + ': what Cosmos limits on ' + facts.values.endpoint + ': [' + parts + '] (expected [' + want + '])');
  }
  if (key === 'home') homePlace = { place, facts };
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
    if (chain === 'docker') {
      // the app of the Docker section: a running PostgreSQL on the volume app-db, with a table
      docker('rm', '-f', '-v', 'app-db', 'app-db-check', 'app-db-new');
      docker('volume', 'rm', '-f', 'app-db', 'app-db-restored');
      docker('run', '-d', '--name', 'app-db', '-e', 'POSTGRES_USER=app', '-e', 'POSTGRES_PASSWORD=' + place.dbPassword, '-e', 'POSTGRES_DB=app', '-v', 'app-db:/var/lib/postgresql', 'postgres:18');
      const ready = docker('run', '--rm', '-v', '/var/run/docker.sock:/var/run/docker.sock', 'gx-docker', 'sh', '-c',
        'for i in $(seq 1 60); do docker exec app-db psql -U app -d app -c "select 1" >/dev/null 2>&1 && break; sleep 1; done; sleep 2; docker exec app-db psql -U app -d app -c "create table orders (id int); insert into orders values (1),(2),(3);"');
      log('the Docker section\'s app:', (ready.stdout || '').trim(), (ready.stderr || '').trim().slice(-200));
    }
    if (chain === 'check') {
      // a moment before the damage (UTC), then the damage
      await sleep(2000);
      place.before = new Date().toISOString().replace(/\.\d+Z$/, 'Z');
      await sleep(2000);
      // the bucket's quota just above what it holds now, so that the attack fills it
      const quota = docker('exec', 'rustfs', 'sh', '-c', 'umask 077; printf \'{"schema_version":1,"aliases":[{"name":"t","endpoint":"http://127.0.0.1:9000","region":"us-east-1","signature":"v4","bucket_lookup":"auto","credentials":{"access_key":"%s","secret_key":"%s"}}]}\' "$RUSTFS_ACCESS_KEY" "$RUSTFS_SECRET_KEY" > /tmp/t.json && rc alias import /tmp/t.json >/dev/null && rm -f /tmp/t.json && used=$(rc bucket quota info t/' + facts.values.bucket + ' --json | sed -n "s/.*\\"usage\\"[^0-9]*\\([0-9]*\\).*/\\1/p" | head -1) && rc bucket quota set t/' + facts.values.bucket + ' $((used + 50000000)) >/dev/null && rc alias remove t >/dev/null && echo "quota set to $((used + 50000000)) bytes"');
      log('before the attack:', (quota.stdout || '').trim(), (quota.stderr || '').trim().slice(-200));
      const hit = attack(place, facts);
      log('attack at', place.before, '+2 s:', (hit.stdout || '').trim().split('\n').join(' / '));
    }
    const started = Date.now();
    const { out, output } = runChain(chain, list, place, facts);
    if (chain === 'linux') {
      // restic's password, made at random by the guide's own block and shown once
      const pw = stepOutput(output, 'linux-password').trim().split('\n').pop();
      if (pw) place.repoPassword = pw; else log('FAIL the password block printed no password');
    }
    if (chain === 'admin' || chain === 'recover-key') {
      // the key's secret, made by the guide's own block and printed once
      const m = stepOutput(output, chain === 'admin' ? 'admin-key' : 'recover-key').match(/^Secret key: ([A-Za-z0-9]{20,})$/m);
      const before = place.secret;
      if (m) place.secret = m[1]; else log('FAIL the block printed no secret key');
      if (chain === 'recover-key' && m) {
        // the attacker's copy of the old secret no longer works
        const old = docker('run', '--rm', ...PLACE_NET[place.key], 'gx-curl', 'sh', '-c', (facts.flags.ca ? "cat > /tmp/ca.pem <<'COSMOS_CA_END'\n" + facts.values.ca_pem + "\nCOSMOS_CA_END\nexport CURL_CA_BUNDLE=/tmp/ca.pem\n" : '')
          + 'curl -s -o /dev/null -w "%{http_code}" --aws-sigv4 "aws:amz:us-east-1:s3" --user "' + place.name + '-backup:' + before + '" "' + facts.values.endpoint + '/' + facts.values.bucket + '?list-type=2"');
        const refused = (old.stdout || '').trim() === '403';
        results.push({ place: key, id: 'check: the old secret is refused after the new one', ok: refused, note: 'HTTP ' + (old.stdout || '').trim() });
        log((refused ? 'ok   ' : 'FAIL ') + key + ': the old secret is refused after the new one (' + (old.stdout || '').trim() + ')');
      }
    }
    out.forEach((r) => {
      results.push({ place: key, chain, ...r });
      log((r.ok ? 'ok   ' : 'FAIL ') + key + ' ' + r.id + (r.note ? ' [' + r.note + ']' : '') + ' (' + list[0].runner + ', ' + Math.round((Date.now() - started) / 1000) + ' s)');
    });
    if (out.some((r) => !r.ok)) log('---- output of ' + key + '/' + chain + ' (end):\n' + out[0].output + '\n----');
  }
}

// the limits, proven: one request of 150 MB through the stand-in is refused, through the home address it passes
// (a RustFS just started may answer 503 for a moment: up to three tries, the last answer counts)
const proof = (net, endpoint, extra) => docker('run', '--rm', ...net, ...extra, 'gx-curl', 'sh', '-c',
  'head -c 157286400 /dev/zero > /tmp/big && for i in 1 2 3; do c=$(curl -sS -o /dev/null -w "%{http_code}" --aws-sigv4 "aws:amz:us-east-1:s3" --user "gx-admin:Gx-test-admin-secret-0420" --upload-file /tmp/big ' + endpoint + '/backups-home1/proof/big.bin); [ "$c" = 503 ] || break; sleep 5; done; printf %s "$c"');
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

// Cosmos' own limits, for real: the guide's rclone example through a URL with the shield on is
// refused (429), the same through the home URL without limits passes; one long request (40 MB at
// 1 MB/s) through a URL with a 20 s timeout is cut, through the URL without a timeout it is not
if (homePlace) {
  const { place, facts } = homePlace;
  const env = unmark(renderCode(codeBlocks(guide).find((b) => b.id === 'linux-env'), facts).text, place);
  const run = (host, script) => docker('run', '--rm', ...PLACE_NET.home, '--add-host', host + ':192.168.60.1', '--entrypoint', 'bash', 'gx-linux', '-c', [
    'set -e',
    "install -m 600 /dev/null /root/.s3-backup.env && cat > /root/.s3-backup.env <<'SETTINGS_END'\n" + env.split('s3.local').join(host) + '\nSETTINGS_END',
    "cat > /etc/ssl/cosmos-ca.pem <<'COSMOS_CA_END'\n" + facts.values.ca_pem + '\nCOSMOS_CA_END',
    'set -a; . /root/.s3-backup.env; set +a', script].join('\n'));
  const many = 'rclone copy --retries 1 --low-level-retries 1 /usr/share/doc store:' + facts.values.bucket + '/limits-$RANDOM 2>&1 | tail -3';
  // the example fails, and the answers it gets are Cosmos' 429 (a client already over its budget is
  // refused from its first request; rclone then reports what it made of that answer)
  const shielded = run('s3s.local', many + '; status=${PIPESTATUS[0]}; for i in $(seq 1 30); do curl -s -o /dev/null -w "%{http_code}\\n" --cacert /etc/ssl/cosmos-ca.pem --aws-sigv4 "aws:amz:us-east-1:s3" --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY" "$AWS_ENDPOINT_URL/' + facts.values.bucket + '?list-type=2"; done | sort | uniq -c; exit $status');
  const refused = shielded.status !== 0 && /\d+ 429/.test(shielded.stdout || '');
  results.push({ place: 'home', id: 'shield-on-refuses-a-backup', ok: refused, note: (shielded.stdout || '').trim().slice(-160) });
  log((refused ? 'ok   ' : 'FAIL ') + 'home: the rclone example through a URL with Cosmos\' shield on is refused with 429');
  const free = run('s3.local', many + '; exit ${PIPESTATUS[0]}');
  results.push({ place: 'home', id: 'no-limits-the-backup-passes', ok: free.status === 0, note: (free.stdout || '').trim().slice(-160) });
  log((free.status === 0 ? 'ok   ' : 'FAIL ') + 'home: the same through the URL without Cosmos\' limits passes');
  const slow = 'head -c 40000000 /dev/urandom > /tmp/slow.bin && curl -sS -o /dev/null -w "%{http_code} %{time_total}" --cacert /etc/ssl/cosmos-ca.pem --limit-rate 1M --aws-sigv4 "aws:amz:us-east-1:s3" --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY" --upload-file /tmp/slow.bin "$AWS_ENDPOINT_URL/' + facts.values.bucket + '/limits/slow.bin" || true';
  const cut = run('s3t.local', slow);
  const cutOk = /^\d{3} /.test((cut.stdout || '').trim()) && !/^200 /.test((cut.stdout || '').trim()) && Number((cut.stdout || '').trim().split(' ')[1]) < 35;
  results.push({ place: 'home', id: 'a-timeout-cuts-a-long-request', ok: cutOk, note: (cut.stdout || '').trim() });
  log((cutOk ? 'ok   ' : 'FAIL ') + 'home: a 40-second upload through a URL with a 20 s timeout is cut (' + (cut.stdout || '').trim() + ')');
  const whole = run('s3.local', slow);
  const wholeOk = /^200 /.test((whole.stdout || '').trim()) && Number((whole.stdout || '').trim().split(' ')[1]) > 30;
  results.push({ place: 'home', id: 'no-timeout-a-long-request-passes', ok: wholeOk, note: (whole.stdout || '').trim() });
  log((wholeOk ? 'ok   ' : 'FAIL ') + 'home: the same through the URL without a timeout passes (' + (whole.stdout || '').trim() + ')');
}

writeFileSync(OUT + '/results.json', JSON.stringify(results.map(({ output, ...r }) => r), null, 1));
const failed = results.filter((r) => r.ok === false);
log('\n' + results.filter((r) => r.ok).length + ' passed, ' + failed.length + ' failed, ' + results.filter((r) => r.ok === null).length + ' not run');
process.exit(failed.length ? 1 : 0);
