// Fork addition (plan 50): the guides' reader, values and facts.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  visibleInline, parseGuide, parseInline, codeBlocks, renderCode, guideFacts, visibleBlocks, visibleSections, fillValue,
  validBucket, validName, findGuideApp, guidePath, appGuide, VALUE_TYPES,
} from './guides.js';
import { groupApps } from './appInterfaces.js';

const s3 = readFileSync(new URL('../../../src/guides/s3.md', import.meta.url), 'utf8');
const PEM = '-----BEGIN CERTIFICATE-----\n' + 'MIIB'.repeat(16) + '\n' + 'QUJD'.repeat(10) + 'QQ==\n-----END CERTIFICATE-----';

const route = (Name, Host, Target, more = {}) => ({ Name, Host, UseHost: true, Target, Mode: 'SERVAPP', ...more });
const ownerRoutes = (more = {}) => [
  route('s3', 's3.local', 'http://rustfs:9000', { ForkRole: 'api', ForkGuide: 's3', ForkGuideRegion: 'us-east-1', ForkOwnCAHosts: ['s3.local'], ForkNetworks: ['media'], ForkImage: 'rustfs/rustfs@sha256:' + 'a'.repeat(64), ...more }),
  route('s3-internet', 's3.example.org', 'http://rustfs:9000', { ForkRole: 'api', ForkGuide: 's3', ForkShielded: true, ForkNetworks: ['media'] }),
  route('rustfs-console', 'rustfs.local', 'http://rustfs:9001', { ForkRole: 'admin', ForkGuide: 's3' }),
];
const loc = { protocol: 'https:', hostname: 'cosmos.example.org', port: '2053', origin: 'https://cosmos.example.org:2053' };
const ctxOf = (where, isAdmin = true) => ({ place: { where, sure: true }, location: loc, isAdmin });
const appOf = (routes) => groupApps(routes)[0];

test('the S3 guide reads: sections with ids, code blocks with ids and runners', () => {
  const guide = parseGuide(s3);
  assert.equal(guide.meta.kind, 's3');
  const ids = guide.sections.map((s) => s.id);
  for (const id of ['before', 'connect', 'linux', 'website', 'databases', 'docker', 'windows', 'macos', 'phones', 'nas', 'code', 'checking', 'trouble']) assert.ok(ids.includes(id), id);
  assert.equal(guide.sections.find((s) => s.id === 'before').admin, true);
  const blocks = codeBlocks(guide);
  const seen = new Set();
  blocks.forEach((b) => {
    assert.ok(b.id, 'every code block has an id: ' + b.lines[0]);
    assert.ok(!seen.has(b.id), 'ids are unique: ' + b.id);
    seen.add(b.id);
  });
  assert.ok(blocks.length >= 25);
});

test('every block renders for each place, with no placeholder left', () => {
  const guide = parseGuide(s3);
  for (const where of ['home', 'away']) {
    for (const choice of ['home', 'internet', 'container']) {
      const facts = guideFacts(appOf(ownerRoutes()), ctxOf(where), { CACert: PEM }, choice, { bucket: 'backups-web1', name: 'web1' });
      assert.equal(facts.choice, choice);
      codeBlocks(guide).forEach((b) => {
        const r = renderCode(b, facts);
        assert.ok(r.ok, b.id + ' ' + choice + ': ' + r.problem);
        assert.ok(!/\{\{/.test(r.text), b.id + ' keeps no condition or placeholder');
      });
    }
  }
});

test('the facts follow the interfaces: home, internet, container; Cloudflare and own CA', () => {
  const app = appOf(ownerRoutes());
  const home = guideFacts(app, ctxOf('home'), { CACert: PEM }, '', {});
  assert.equal(home.choice, 'home');
  assert.equal(home.values.endpoint, 'https://s3.local:2053');
  assert.equal(home.values.console, 'https://rustfs.local:2053');
  assert.equal(home.flags.ca, true);
  assert.equal(home.flags.cloudflare, false);
  assert.equal(home.flags.rustfs, true);
  const away = guideFacts(app, ctxOf('away'), { CACert: PEM }, '', {});
  assert.equal(away.choice, 'internet');
  assert.equal(away.values.endpoint, 'https://s3.example.org:2053');
  assert.equal(away.values.endpoint_host, 's3.example.org:2053');
  assert.equal(away.flags.cloudflare, true);
  assert.equal(away.flags.ca, false);
  const inside = guideFacts(app, ctxOf('home'), {}, 'container', {});
  assert.equal(inside.values.endpoint, 'http://rustfs:9000');
  assert.equal(inside.values.network, 'media');
  assert.equal(inside.flags.ca, false);
  // a plain user: no containers, no RustFS terminal
  const user = guideFacts(app, ctxOf('home', false), {}, 'container', {});
  assert.equal(user.places.container, '');
  assert.notEqual(user.choice, 'container');
  assert.equal(user.flags.rustfs, false);
});

test('an app without an internet address says so, and away falls back to home', () => {
  const routes = ownerRoutes().filter((r) => r.Name !== 's3-internet');
  const f = guideFacts(appOf(routes), ctxOf('away'), {}, '', {});
  assert.equal(f.flags.no_internet, true);
  assert.equal(f.choice, 'home');
  const intro = visibleBlocks(parseGuide(s3).intro, f.flags);
  assert.ok(intro.some((b) => b.type === 'note' && JSON.stringify(b.inline).includes('no internet address')));
  const g = guideFacts(appOf(ownerRoutes()), ctxOf('away'), {}, '', {});
  assert.ok(!visibleBlocks(parseGuide(s3).intro, g.flags).some((b) => b.type === 'note'));
});

test('Cloudflare part sizes appear only on a Cloudflare address', () => {
  const guide = parseGuide(s3);
  const rclone = codeBlocks(guide).find((b) => b.id === 'linux-env');
  const app = appOf(ownerRoutes());
  assert.match(renderCode(rclone, guideFacts(app, ctxOf('away'), {}, 'internet', {})).text, /UPLOAD_CUTOFF=64M/);
  assert.doesNotMatch(renderCode(rclone, guideFacts(app, ctxOf('home'), {}, 'home', {})).text, /UPLOAD_CUTOFF/);
  const direct = appOf(ownerRoutes().map((r) => ({ ...r, ForkShielded: false })));
  assert.doesNotMatch(renderCode(rclone, guideFacts(direct, ctxOf('away'), {}, 'internet', {})).text, /UPLOAD_CUTOFF/);
});

test('administrators get the console steps, others do not', () => {
  const guide = parseGuide(s3);
  const admin = guideFacts(appOf(ownerRoutes()), ctxOf('home'), {}, '', {});
  const user = guideFacts(appOf(ownerRoutes()), ctxOf('home', false), {}, '', {});
  assert.ok(visibleSections(guide, admin.flags).some((s) => s.id === 'before'));
  assert.ok(!visibleSections(guide, user.flags).some((s) => s.id === 'before'));
});

test('hostile values never reach a command', () => {
  const hostile = ['s3.local;rm -rf /', "s3'.local", 's3.local$(id)', 's3.local`id`', 's3 .local', 's3.local\nx', 's3.local"', '<b>s3</b>.local'];
  const guide = parseGuide(s3);
  const env = codeBlocks(guide).find((b) => b.id === 'linux-env');
  hostile.forEach((host) => {
    const routes = ownerRoutes().map((r) => (r.Name === 's3' ? { ...r, Host: host, ForkOwnCAHosts: [host] } : r)).filter((r) => r.Name !== 's3-internet');
    const f = guideFacts(appOf(routes), ctxOf('home'), {}, 'home', {});
    const r = renderCode(env, f);
    assert.ok(!r.ok || !/[;'$`"<\n ]/.test(f.values.endpoint), 'host ' + JSON.stringify(host) + ' gave ' + f.values.endpoint);
  });
  // a hostile network or region is refused, not quoted
  const net = guideFacts(appOf(ownerRoutes({ ForkNetworks: ['media; rm -rf /'] })), ctxOf('home'), {}, 'container', {});
  assert.equal(net.places.container, '');
  const region = guideFacts(appOf(ownerRoutes({ ForkGuideRegion: "us'east" })), ctxOf('home'), {}, 'home', {});
  assert.equal(renderCode(codeBlocks(guide).find((b) => b.id === 'linux-env'), region).ok, false);
  // typed values: refused unless plain
  ['web1;id', 'Web1', "a'b", 'x'.repeat(40), '', '-x'].forEach((name) => assert.equal(validName(name), false, name));
  ['a', 'UPPER', 'has space', 'a..b', 'with.dot', 'xn--a', "a'b", 'a$(id)'].forEach((b) => assert.equal(validBucket(b), false, b));
  // an invalid typed value withholds the blocks that use it, never a default in its place
  const typed = guideFacts(appOf(ownerRoutes()), ctxOf('home'), {}, 'home', { bucket: 'x$(id)', name: 'a;b' });
  assert.equal(typed.values.bucket, '');
  assert.equal(typed.values.name, '');
  const envBlock = codeBlocks(guide).find((b) => b.id === 'linux-env');
  assert.equal(renderCode(envBlock, typed).ok, false);
  const fine = guideFacts(appOf(ownerRoutes()), ctxOf('home'), {}, 'home', { name: 'web1' });
  assert.equal(fine.values.bucket, 'backups-web1');
  // the endpoint check is the barrier for a host that reached the facts: it refuses shell and markup
  ['https://s3.local$(id):2053', 'https://s3.local;id', "https://s3'.local", 'https://s3.local`id`', 'https://s3.local:2053/x', 'https://S3.local', 'https://s3.local:2053 ', 'javascript:alert(1)', 'https://s3.local\nid']
    .forEach((v) => assert.equal(VALUE_TYPES.endpoint(v), false, v));
  assert.equal(VALUE_TYPES.endpoint('https://s3.local:2053'), true);
  ['s3.local:2053;id', 's3.local $(id)', "s3.local'"].forEach((v) => assert.equal(VALUE_TYPES.endpoint_host(v), false, v));
  // a certificate that is not a plain PEM is left out
  // a PEM must have PEM's layout: a line that closes a heredoc never passes
  const lines = (n) => Array.from({ length: n }, () => 'A'.repeat(64)).join('\n');
  const goodPem = '-----BEGIN CERTIFICATE-----\n' + lines(3) + '\nQUJD\n-----END CERTIFICATE-----';
  assert.equal(VALUE_TYPES.ca_pem(goodPem), true);
  assert.equal(VALUE_TYPES.ca_pem('-----BEGIN CERTIFICATE-----\n' + lines(2) + '\nEOF\nreboot\n-----END CERTIFICATE-----'), false);
  assert.equal(VALUE_TYPES.ca_pem('-----BEGIN CERTIFICATE-----\nQUJD\nEOF\n-----END CERTIFICATE-----'), false);
  assert.equal(VALUE_TYPES.ca_pem('-----BEGIN CERTIFICATE-----\n' + 'A'.repeat(65) + '\n-----END CERTIFICATE-----'), false);
  const pem = guideFacts(appOf(ownerRoutes()), ctxOf('home'), { CACert: PEM + "\nEOF\nrm -rf /" }, 'home', {});
  assert.equal(pem.values.ca_pem, '');
  assert.equal(renderCode(codeBlocks(guide).find((b) => b.id === 'ca-file'), pem).ok, false);
  // every value type refuses quotes, spaces, new lines, $ and backticks
  Object.entries(VALUE_TYPES).forEach(([name, check]) => {
    ["a'b", 'a b', 'a\nb', 'a$b', 'a`b', 'a"b', 'a\\b', 'a{b'].forEach((v) => assert.equal(check(v), false, name + ' accepts ' + JSON.stringify(v)));
  });
});

test('the reader: no HTML, links http/https only, conditions, unknown values refused', () => {
  const g = parseGuide('---\nkind: x\ntitle: X\n---\n<script>alert(1)</script>\n\n## A {#a}\n[x](javascript:alert(1)) [y](https://example.org/a) **b** `c`\n{{if ca}}\nsecret\n{{end}}\n```sh id=one\n{{if !ca}}\nplain {{nothing}}\n{{end}}\n```\n```sh id=two\nopen fence');
  assert.equal(g.intro[0].type, 'para');
  assert.equal(g.intro[0].inline[0].t, 'text'); // the tag is text
  const inline = g.sections[0].blocks[0].inline;
  assert.ok(!inline.some((x) => x.t === 'link' && /javascript/.test(x.href)));
  assert.ok(inline.some((x) => x.t === 'link' && x.href === 'https://example.org/a'));
  assert.equal(visibleBlocks(g.sections[0].blocks, { ca: false }).some((b) => b.type === 'para' && JSON.stringify(b).includes('secret')), false);
  const one = codeBlocks(g).find((b) => b.id === 'one');
  assert.equal(renderCode(one, { flags: { ca: true }, values: {} }).text, '');
  assert.equal(renderCode(one, { flags: { ca: false }, values: {} }).ok, false);
  assert.equal(fillValue('nothing', {}).ok, false);
  assert.equal(codeBlocks(g).find((b) => b.id === 'two').lines[0], 'open fence');
  assert.deepEqual(parseInline('{{endpoint}}')[0], { t: 'value', name: 'endpoint' });
});

test('the secret markers are marked, never filled', () => {
  const guide = parseGuide(s3);
  const env = codeBlocks(guide).find((b) => b.id === 'linux-env');
  const r = renderCode(env, guideFacts(appOf(ownerRoutes()), ctxOf('home'), {}, 'home', {}));
  assert.ok(r.parts.some((p) => p.kind === 'secret' && p.text === 'PASTE-SECRET-KEY-HERE'));
});

test('pages of guides: found by container or URL name; a guide path is plain', () => {
  const apps = groupApps(ownerRoutes());
  assert.equal(findGuideApp(apps, 'rustfs'), apps[0]);
  assert.equal(findGuideApp(apps, 's3-internet'), apps[0]);
  assert.equal(findGuideApp(apps, 'nothing'), null);
  assert.equal(guidePath(apps[0], 'linux'), '/cosmos-ui/guide/rustfs#linux');
  assert.equal(guidePath(apps[0], 'x#y'), '/cosmos-ui/guide/rustfs');
  assert.equal(appGuide(apps[0]), 's3');
});

test('conditions inside a sentence, and no condition or placeholder left in any text', () => {
  const items = parseInline('a{{if ca}} b{{end}}{{if !ca}} c{{end}} d');
  assert.equal(visibleInline(items, { ca: true }).map((x) => x.text).join(''), 'a b d');
  assert.equal(visibleInline(items, { ca: false }).map((x) => x.text).join(''), 'a c d');
  const guide = parseGuide(s3);
  const texts = [];
  const walk = (blocks) => blocks.forEach((b) => {
    if (b.type === 'if') walk(b.blocks);
    [b.inline, ...(b.items || []), ...(b.head || []), ...((b.rows || []).flat())].filter(Boolean).forEach((inline) => inline.forEach((x) => texts.push(x)));
  });
  walk(guide.intro);
  guide.sections.forEach((sec) => walk(sec.blocks));
  texts.filter((x) => x.t === 'text').forEach((x) => assert.ok(!/\{\{/.test(x.text), 'left in the text: ' + x.text));
  texts.filter((x) => x.t === 'code').forEach((x) => [...x.text.matchAll(/\{\{([^}]*)\}\}/g)].forEach((m) => assert.ok(VALUE_TYPES[m[1]], 'in code: ' + m[0])));
  texts.filter((x) => x.t === 'value').forEach((x) => assert.ok(VALUE_TYPES[x.name], 'unknown value ' + x.name));
});

test('what Cosmos limits on the chosen address: none, some, the container place, and only for administrators', async () => {
  const { limitParts, isStorageAPI, API_ROUTE_SETTINGS } = await import('./guides.js');
  const open = { Shield: false, TimeoutMs: 0 };
  const limited = { Shield: true, ShieldRequestsPerHour: 36000, ShieldBytesPerHour: 429496729600, ShieldSimultaneous: 3000, ThrottlePerMinute: 10000, TimeoutMs: 14400000, BlockBots: true, BlockAPIAbuse: true };
  const routes = ownerRoutes({ ForkLimits: open }).map((r) => (r.Name === 's3-internet' ? { ...r, ForkLimits: limited } : r));
  const home = guideFacts(appOf(routes), ctxOf('home'), {}, 'home', {});
  assert.equal(home.limits.route, 's3');
  assert.deepEqual(limitParts(home.limits), []);
  const away = guideFacts(appOf(routes), ctxOf('away'), {}, 'internet', {});
  assert.deepEqual(limitParts(away.limits).map((p) => p.what), ['requests', 'bytes', 'simultaneous', 'throttle', 'timeout', 'bots', 'referer']);
  assert.equal(guideFacts(appOf(routes), ctxOf('home'), {}, 'container', {}).limits.direct, true);
  // a plain user's dashboard has no ForkLimits: nothing is said
  const plain = ownerRoutes().map(({ ForkLimits, ...r }) => r);
  assert.equal(guideFacts(appOf(plain), ctxOf('home', false), {}, 'home', {}).limits, null);
  // the URL form's proposal: the API of a storage app
  const dash = [{ ForkGuide: 's3', ForkContainer: 'rustfs', ForkServicePort: 9000, ForkRole: 'api', Target: 'http://rustfs:9000' }];
  assert.equal(isStorageAPI('', 'rustfs', 9000, dash), true);
  assert.equal(isStorageAPI('', 'rustfs', 9001, dash), false);
  assert.equal(isStorageAPI('api', 'other', 80, []), true);
  assert.equal(isStorageAPI('', 'web', 80, dash), false);
  assert.equal(API_ROUTE_SETTINGS.SmartShieldEnabled, false);
  assert.equal(API_ROUTE_SETTINGS.Timeout, 86400000);
});

test('a widget line is read as a widget, not text', () => {
  const g = parseGuide('---\nkind: x\ntitle: X\n---\n## A {#a}\n{{show limits}}\ntext');
  assert.equal(g.sections[0].blocks[0].type, 'widget');
  assert.equal(g.sections[0].blocks[0].name, 'limits');
});
