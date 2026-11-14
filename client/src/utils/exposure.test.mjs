// node --test client/src/utils/exposure.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  RISKY_PORTS, TEXT, accessSentence, directName, exposeRequest, exposureDomains, isRiskyPort, nameProblem, portPlan,
  choiceProblems, exposureChanges, initialChoices, proposeAccess, proposeThrough, throughSentence, webPlan, withRoutes, withdrawRequest,
} from './exposure.js';

const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8');

const config = (extra = {}) => ({
  HTTPConfig: {
    Hostname: '192.168.1.10',
    HTTPSPort: '2053',
    DNSZones: [{ Zone: 'example.org' }, { Zone: 'example.net' }],
    ProxyConfig: {
      Routes: [
        { Name: 'sonarr', Mode: 'SERVAPP', UseHost: true, Host: 'sonarr.local', Target: 'http://sonarr:8989' },
        { Name: 'radarr', Mode: 'SERVAPP', UseHost: true, Host: 'radarr.local', Target: 'http://radarr:7878' },
        { Name: 'radarr-example.org', Mode: 'SERVAPP', UseHost: true, Host: 'radarr.example.org', Target: 'http://radarr:7878' },
        { Name: 'minecraft', Mode: 'SERVAPP', UseHost: true, Host: 'minecraft.local', Target: 'http://minecraft:8123' },
      ],
    },
    UPnP: { AppPorts: [] },
    Cloudflare: { Shielded: ['radarr.example.org'] },
    ...extra,
  },
});
const cloudflare = (portOK = true, extra = {}) => ({
  portOK, publicPort: portOK ? 2053 : 4443, allowedPorts: [443, 2053, 2083, 2087, 2096, 8443],
  zones: [{ name: 'example.org', hasToken: true, manageRecords: true, wildcardRecord: false, ...extra }],
});

test('the risky list is the server\'s', () => {
  const go = read('../../../src/cloudflare/expose.go');
  const block = go.slice(go.indexOf('var RiskyPorts'), go.indexOf('}\n', go.indexOf('var RiskyPorts')) + 1);
  const pairs = [...block.matchAll(/\{(\d+), (\d+)\}/g)].map((m) => [Number(m[1]), Number(m[2])]);
  assert.deepEqual(pairs, RISKY_PORTS);
  assert.ok(isRiskyPort(5432) && isRiskyPort(2375) && isRiskyPort(22) && isRiskyPort(27018));
  assert.ok(!isRiskyPort(25565) && !isRiskyPort(1883) && !isRiskyPort(51413));
});

test('what each domain allows', () => {
  const domains = exposureDomains(config(), cloudflare());
  assert.deepEqual(domains.map((d) => [d.zone, d.managed, d.canShield]), [['example.org', true, true], ['example.net', false, false]]);
  // not on a port Cloudflare proxies: no shield anywhere, the records stay Cosmos'
  assert.equal(exposureDomains(config(), cloudflare(false))[0].canShield, false);
  assert.equal(exposureDomains(config(), cloudflare(true, { hasToken: false }))[0].managed, false);
});

test('how a web address goes out, with the reason', () => {
  const [org, net] = exposureDomains(config(), cloudflare());
  assert.deepEqual(proposeThrough(org, cloudflare(), 'lscr.io/linuxserver/sonarr'), { through: 'cloudflare', reason: 'cloudflare' });
  assert.deepEqual(proposeThrough(org, cloudflare(), 'jellyfin/jellyfin'), { through: 'direct', reason: 'media' });
  assert.deepEqual(proposeThrough(net, cloudflare(), 'sonarr'), { through: 'direct', reason: 'notCloudflare' });
  const [notProxied] = exposureDomains(config(), cloudflare(false));
  assert.deepEqual(proposeThrough(notProxied, cloudflare(false), 'sonarr'), { through: 'direct', reason: 'port' });
  const s = throughSentence({ reason: 'port' }, cloudflare(false), 'sonarr');
  assert.equal(s.params.port, 4443);
  assert.match(s.params.ports, /2053/);
});

test('who may open it: Cosmos\' sign-in for tools that control the server or have none', () => {
  assert.deepEqual(proposeAccess('portainer/portainer-ce:latest'), { access: 'admins', source: 'control' });
  assert.deepEqual(proposeAccess('amir20/dozzle'), { access: 'admins', source: 'control' });
  assert.deepEqual(proposeAccess('lscr.io/linuxserver/transmission'), { access: 'users', source: 'noSignIn' });
  assert.deepEqual(proposeAccess('lscr.io/linuxserver/sonarr'), { access: 'anyone', source: 'own' });
  assert.deepEqual(proposeAccess('sonarr', { AuthEnabled: true, AdminOnly: true }), { access: 'admins', source: 'route' });
  assert.equal(accessSentence('control', 'portainer').params.name, 'portainer');
});

test('the web plan of an app: proposed names, and what is exposed already', () => {
  const plan = webPlan({ config: config(), cloudflare: cloudflare(), containerName: 'sonarr', image: 'linuxserver/sonarr' });
  assert.equal(plan.available, true);
  assert.equal(plan.route.Name, 'sonarr');
  assert.deepEqual(plan.domains.map((d) => [d.zone, d.host, d.through, d.exposed]), [
    ['example.org', 'sonarr.example.org', 'cloudflare', null],
    ['example.net', 'sonarr.example.net', 'direct', null],
  ]);
  const radarr = webPlan({ config: config(), cloudflare: cloudflare(), containerName: 'radarr', image: 'radarr' });
  assert.deepEqual(radarr.domains[0].exposed, { host: 'radarr.example.org', name: 'radarr-example.org', own: false, through: 'cloudflare', access: 'anyone' });
  // an app without a web URL has no web side
  assert.equal(webPlan({ config: config(), cloudflare: cloudflare(), containerName: 'qbit', image: '' }).available, false);
  // a name another app has is not free
  const taken = config();
  taken.HTTPConfig.ProxyConfig.Routes.push({ Name: 'x', Mode: 'SERVAPP', UseHost: true, Host: 'sonarr.example.org', Target: 'http://other:80' });
  assert.equal(webPlan({ config: taken, cloudflare: cloudflare(), containerName: 'sonarr', image: '' }).domains[0].free, false);
});

test('direct names: their own beside a web interface, the plain one without, the web one when that is direct', () => {
  const taken = new Map();
  assert.equal(directName({ containerName: 'minecraft', zone: 'example.org', hasWeb: true, taken }), 'minecraft-direct.example.org');
  assert.equal(directName({ containerName: 'Minecraft_Server', zone: 'example.org', hasWeb: false, taken }), 'minecraft-server.example.org');
  assert.equal(directName({ containerName: 'minecraft', zone: 'example.org', hasWeb: true, webDirect: 'minecraft.example.org', taken }), 'minecraft.example.org');
  taken.set('minecraft-direct.example.org', { container: 'other' });
  assert.equal(directName({ containerName: 'minecraft', zone: 'example.org', hasWeb: true, taken, own: (h) => h.container === 'minecraft' }), 'minecraft-direct-2.example.org');
});

test('the other ports: the web port goes through Cosmos, dangerous and reserved ones behind the door', () => {
  const ports = [
    { port: 8123, protocol: 'tcp', hostPort: 8123 }, // its web interface (a URL targets it)
    { port: 25565, protocol: 'tcp', hostPort: 25565 },
    { port: 25565, protocol: 'udp', hostPort: 25565 },
    { port: 5432, protocol: 'tcp', hostPort: 5432 },
    { port: 80, protocol: 'tcp', hostPort: 443 },
    { port: 9000, protocol: 'tcp', hostPort: 9000, hostIp: '127.0.0.1' },
  ];
  const plan = portPlan({ config: config(), cloudflare: cloudflare(), router: { reserved: [80, 443] }, containerName: 'minecraft', ports });
  assert.deepEqual(plan.map((p) => [p.key, p.door, p.hostname]), [
    ['minecraft:80/tcp', true, 'minecraft-direct.example.org'],
    ['minecraft:5432/tcp', true, 'minecraft-direct.example.org'],
    ['minecraft:25565/tcp', false, 'minecraft-direct.example.org'],
    ['minecraft:25565/udp', false, 'minecraft-direct.example.org'],
  ]);
  // without a domain whose records Cosmos manages, no name
  assert.equal(portPlan({ config: config(), cloudflare: cloudflare(true, { hasToken: false }), router: {}, containerName: 'minecraft', ports })[0].hostname, '');
  // an exposed port shows its choice
  const exposed = config({ UPnP: { AppPorts: [{ Container: 'minecraft', Port: 25565, Protocol: 'tcp', Hostname: 'mc.example.org' }] } });
  const one = portPlan({ config: exposed, cloudflare: cloudflare(), router: {}, containerName: 'minecraft', ports }).find((p) => p.key === 'minecraft:25565/tcp');
  assert.equal(one.hostname, 'mc.example.org');
  assert.ok(one.exposed);
});

test('an installation is planned on its URLs to come', () => {
  const installing = withRoutes(config(), [{ Name: 'gitea', Mode: 'SERVAPP', UseHost: true, Host: 'gitea.local', Target: 'http://gitea:3000' }]);
  const plan = webPlan({ config: installing, cloudflare: cloudflare(), containerName: 'gitea', image: 'gitea/gitea' });
  assert.equal(plan.route.Name, 'gitea');
  assert.equal(plan.domains[0].host, 'gitea.example.org');
  const ports = portPlan({ config: installing, cloudflare: cloudflare(), router: {}, containerName: 'gitea', ports: [{ port: 3000, protocol: 'tcp', hostPort: 3000 }, { port: 22, protocol: 'tcp', hostPort: 2222 }] });
  assert.deepEqual(ports.map((p) => [p.port, p.door, p.hostname]), [[22, true, 'gitea-direct.example.org']]);
});

test('the requests', () => {
  assert.deepEqual(exposeRequest('/sonarr', [{ route: 'sonarr', hostname: 'Sonarr.Example.org', through: 'cloudflare', access: 'users' }], []), {
    action: 'expose', container: 'sonarr', confirm: 'sonarr',
    web: [{ route: 'sonarr', hostname: 'sonarr.example.org', through: 'cloudflare', access: 'users' }], ports: [],
  });
  assert.deepEqual(exposeRequest('mc', [], [{ port: '25565', protocol: 'UDP', hostname: 'mc.example.org', externalPort: '', risky: false }]).ports,
    [{ port: 25565, protocol: 'udp', hostname: 'mc.example.org', externalPort: 0, scheme: '', risky: false }]);
  assert.deepEqual(withdrawRequest('mc', ['mc.example.org'], [{ port: 25565, protocol: 'tcp' }]), {
    action: 'withdraw', container: 'mc', web: [{ hostname: 'mc.example.org' }], ports: [{ port: 25565, protocol: 'tcp' }],
  });
});

test('typed names', () => {
  assert.equal(nameProblem('', 'example.org'), 'empty');
  assert.equal(nameProblem('app_1.example.org', 'example.org'), 'invalid');
  assert.equal(nameProblem('app.example.com', 'example.org'), 'outside');
  assert.equal(nameProblem('example.org', 'example.org'), 'apex');
  assert.equal(nameProblem('App.Example.org', 'example.org'), '');
});

test('every sentence is in the translation file, word for word', () => {
  const translations = JSON.parse(read('./locales/en/translation.json'));
  for (const [id, english] of Object.entries(TEXT)) {
    assert.equal(translations['mgmt.expose.' + id], english, 'mgmt.expose.' + id);
  }
});

test('the dialog sends only what changed', () => {
  const cfg = config({ UPnP: { AppPorts: [{ Container: 'radarr', Port: 6881, Protocol: 'udp', Hostname: 'radarr-direct.example.org' }] } });
  const web = webPlan({ config: cfg, cloudflare: cloudflare(), containerName: 'radarr', image: 'radarr' });
  const ports = portPlan({ config: cfg, cloudflare: cloudflare(), router: {}, containerName: 'radarr', ports: [{ port: 7878, protocol: 'tcp', hostPort: 7878 }, { port: 6881, protocol: 'udp', hostPort: 6881 }, { port: 6881, protocol: 'tcp', hostPort: 6881 }] });
  const before = initialChoices(web, ports);
  assert.deepEqual(before.web['example.org'], { on: true, host: 'radarr.example.org', through: 'cloudflare', access: 'anyone' });
  assert.equal(before.web['example.net'].on, false);
  assert.equal(before.ports['radarr:6881/udp'].on, true);
  // nothing changed: nothing to send
  assert.deepEqual(exposureChanges('radarr', web, ports, before, before), { expose: null, withdraw: null });

  const after = JSON.parse(JSON.stringify(before));
  after.web['example.org'].through = 'direct';
  after.ports['radarr:6881/udp'].on = false;
  after.ports['radarr:6881/tcp'].on = true;
  const changes = exposureChanges('radarr', web, ports, before, after);
  assert.deepEqual(changes.expose.web, [{ route: 'radarr', hostname: 'radarr.example.org', through: 'direct', access: 'anyone' }]);
  assert.deepEqual(changes.expose.ports.map((p) => [p.port, p.protocol, p.hostname]), [[6881, 'tcp', 'radarr-direct.example.org']]);
  assert.deepEqual(changes.withdraw, { action: 'withdraw', container: 'radarr', web: [], ports: [{ port: 6881, protocol: 'udp' }] });

  // switching the web address off withdraws it; renaming withdraws the old one
  const off = JSON.parse(JSON.stringify(before));
  off.web['example.org'].on = false;
  assert.deepEqual(exposureChanges('radarr', web, ports, before, off).withdraw.web, [{ hostname: 'radarr.example.org' }]);
  const renamed = JSON.parse(JSON.stringify(before));
  renamed.web['example.org'].host = 'films.example.org';
  const r = exposureChanges('radarr', web, ports, before, renamed);
  assert.equal(r.expose.web[0].hostname, 'films.example.org');
  assert.deepEqual(r.withdraw.web, [{ hostname: 'radarr.example.org' }]);
});

test('problems of the choices', () => {
  const cfg = config();
  const web = webPlan({ config: cfg, cloudflare: cloudflare(), containerName: 'sonarr', image: '' });
  const ports = portPlan({ config: cfg, cloudflare: cloudflare(), router: {}, containerName: 'sonarr', ports: [{ port: 6881, protocol: 'tcp', hostPort: 6881 }] });
  const choices = initialChoices(web, ports);
  choices.web['example.org'] = { ...choices.web['example.org'], on: true, host: 'radarr.example.org' };
  choices.ports['sonarr:6881/tcp'] = { on: true, hostname: 'x.example.com', externalPort: '70000' };
  assert.deepEqual(choiceProblems(web, ports, choices, cfg, 'sonarr'), {
    'web:example.org': 'taken', 'port:sonarr:6881/tcp': 'outside', 'port:sonarr:6881/tcp:external': 'externalPort',
  });
});
