// Fork addition: names for use at home under the owner's domain
// (<app>.<label>.<domain> at the server's private address; the server side
// is src/utils/fork_home_names.go and src/cloudflare/homenames.go). What the
// screens say about them, as [key, English, variables] for t(). Nothing here
// imports the UI, so it can be exercised with `node --test`.

// The label as the server accepts it: letters, digits, inner hyphens, 1 to
// 63 characters (ValidHomeLabel).
export const validLabel = (label) => /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(String(label || ''));

// What a typed label becomes: lower case, spaces and dots as hyphens.
export const cleanLabel = (text) => String(text || '').trim().toLowerCase().replace(/[\s._]+/g, '-');

export const DEFAULT_LABEL = 'home';

// The example name the switch shows.
export const exampleName = (label, zone, app) => (app || 'radarr') + '.' + (validLabel(label) ? label : DEFAULT_LABEL) + '.' + zone;

const PROBLEMS = {
  no_zone: ['mgmt.homeNames.problem.no_zone', 'This domain is no longer configured.'],
  not_cloudflare: ['mgmt.homeNames.problem.not_cloudflare', 'Only for a domain connected to Cloudflare: Cosmos writes the record there.'],
  no_token: ['mgmt.homeNames.problem.no_token', 'No Cloudflare token is stored for this domain. Connect it again.'],
  records: ['mgmt.homeNames.problem.records', 'Cosmos does not manage this domain\'s records ("manage records" is off).'],
  certificate: ['mgmt.homeNames.problem.certificate', 'The domain does not get a Let\'s Encrypt certificate through DNS, so browsers would not trust the names.'],
  too_long: ['mgmt.homeNames.problem.too_long', 'The label and the domain together leave no room for an app\'s name.'],
  label: ['mgmt.homeNames.problem.label', 'The label can hold letters, digits and hyphens (not at the start or end), 63 at most.'],
  in_use: ['mgmt.homeNames.problem.in_use', 'Addresses under this label are already in use. Choose another label.'],
  too_many: ['mgmt.homeNames.problem.too_many', 'Names for use at home are possible on at most 4 domains.'],
  zone: ['mgmt.homeNames.problem.zone', 'This domain cannot have names for use at home.'],
};
export const PROBLEM_CODES = Object.keys(PROBLEMS);

export const problemText = (code) => PROBLEMS[code] || ['mgmt.homeNames.problem.other', 'This did not work.'];

// The state of a domain's record, as [level, text].
export const recordState = (row) => {
  if (!row || !row.active) return null;
  switch (row.record) {
    case 'ok':
      return ['success', row.recordAddress
        ? ['mgmt.homeNames.record.okAt', 'In public DNS: {{wildcard}} points at {{address}} (DNS only).', { wildcard: row.wildcard, address: row.recordAddress }]
        : ['mgmt.homeNames.record.ok', 'In public DNS: {{wildcard}} points at the server\'s private address (DNS only).', { wildcard: row.wildcard }]];
    case 'conflict':
      return ['error', ['mgmt.homeNames.record.conflict', 'A record Cosmos did not create is at {{wildcard}}: it is left as it is. Delete it at Cloudflare, or tick it for replacement in the Cloudflare card.', { wildcard: row.wildcard }]];
    case 'error':
      return ['error', ['mgmt.homeNames.record.error', 'Cloudflare refused or could not be reached: {{message}}', { message: row.recordMessage || '' }]];
    case 'no_address':
      return ['warning', ['mgmt.homeNames.record.noAddress', 'The server\'s private address on the home network is not known (no network interface with one), so nothing is written yet.']];
    default:
      return ['info', ['mgmt.homeNames.record.pending', 'The record {{wildcard}} is being written; this takes up to a minute.', { wildcard: row.wildcard }]];
  }
};

// The certificate of the names, as [level, text].
export const certificateState = (row) => {
  if (!row || !row.active) return null;
  if (row.certificateCovered && row.certificateSource !== 'selfsigned') {
    return ['success', ['mgmt.homeNames.cert.ok', 'Certificate: {{wildcard}} is on the domain\'s Let\'s Encrypt certificate: no warning in any browser.', { wildcard: row.wildcard }]];
  }
  return ['info', ['mgmt.homeNames.cert.pending', 'Certificate: {{wildcard}} is being added to the domain\'s certificate (one order, see the Certificates card). Until then browsers warn.', { wildcard: row.wildcard }]];
};

// What the server's own resolver answered, as [level, text].
export const serverCheckText = (check, address) => {
  if (!check) return null;
  const vars = { name: check.name, address: address || '', answer: (check.addresses || []).join(', ') };
  switch (check.result) {
    case 'ok':
      return ['success', ['mgmt.homeNames.check.server.ok', 'The server\'s own resolver finds {{name}} at the server\'s private address. This is the server\'s view: a device asks its router, which may still drop the answer.', vars]];
    case 'other':
      return ['warning', ['mgmt.homeNames.check.server.other', 'The server\'s own resolver finds {{name}} at another address ({{answer}}). The record may not be out yet, or a record of someone else\'s answers.', vars]];
    case 'none':
      return ['warning', ['mgmt.homeNames.check.server.none', 'The server\'s own resolver does not find {{name}}. Right after switching on this is normal for a few minutes; if it stays, the resolver the server uses (often the router) drops answers with private addresses: see "The name does not resolve at home" below.', vars]];
    case 'no_address':
      return ['warning', ['mgmt.homeNames.check.server.noAddress', 'The server\'s private address is not known, so there is nothing to compare with.', vars]];
    default:
      return ['error', ['mgmt.homeNames.check.server.error', 'The server could not ask its resolver.', vars]];
  }
};

// What the browser could do, as [level, text]. The browser cannot look a
// name up: it can only try to open the address, without reading the answer.
export const browserCheckText = (result, name) => {
  if (!result) return null;
  if (result === 'opened') {
    return ['success', ['mgmt.homeNames.check.browser.ok', 'This device opened {{name}}: the name resolves here and the certificate is trusted.', { name }]];
  }
  return ['warning', ['mgmt.homeNames.check.browser.failed', 'This device could not open {{name}}. Outside the home network that is expected. At home it means the name does not resolve here (the router\'s rebinding protection, or the record is not out yet) or the browser refused the certificate or the access to a local device.', { name }]];
};

// The address the browser check opens: the panel's home name with the port
// the panel is used on, and a path that answers without any side effect.
export const probeURL = (panel, location) => {
  const loc = location || {};
  const scheme = String(loc.protocol || 'https:').replace(/:$/, '');
  const standard = scheme === 'http' ? '80' : '443';
  const port = loc.port && String(loc.port) !== standard ? ':' + loc.port : '';
  return scheme + '://' + panel + port + '/_health';
};

// The domain to allow in a rebinding protection: the home suffix, the
// narrowest name that covers every home name.
export const rebindDomain = (row) => (row && row.suffix) || '';

// How to let the names through a router's or resolver's DNS rebinding
// protection. {{domain}} is rebindDomain.
export const REBIND_HELP = [
  ['fritzbox', 'Fritz!Box', ['mgmt.homeNames.rebind.fritzbox', 'Home Network > Network > Network Settings > DNS rebind protection: add {{domain}} to the host name exceptions and apply.']],
  ['openwrt', 'OpenWrt', ['mgmt.homeNames.rebind.openwrt', 'Network > DHCP and DNS > Filter: add {{domain}} to "Domain whitelist" and save; or uci add_list dhcp.@dnsmasq[0].rebind_domain=\'{{domain}}\' then uci commit dhcp and /etc/init.d/dnsmasq restart. Some releases want the form /{{domain}}/.']],
  ['dnsmasq', 'dnsmasq', ['mgmt.homeNames.rebind.dnsmasq', 'With stop-dns-rebind on, add the line rebind-domain-ok=/{{domain}}/ and restart dnsmasq.']],
  ['pihole', 'Pi-hole', ['mgmt.homeNames.rebind.pihole', 'Pi-hole does not filter these answers unless rebinding protection was switched on in its dnsmasq settings; then add rebind-domain-ok=/{{domain}}/ (version 6: Settings > All settings > misc.dnsmasq_lines) and restart its DNS.']],
  ['adguard', 'AdGuard Home', ['mgmt.homeNames.rebind.adguard', 'AdGuard Home does not filter private answers by default. If a blocklist does, add the custom rule @@||{{domain}}^ under Filters > Custom filtering rules.']],
  ['unbound', 'Unbound', ['mgmt.homeNames.rebind.unbound', 'With private-address: lines in unbound.conf, add private-domain: "{{domain}}" to the server: section and reload Unbound.']],
  ['opnsense', 'OPNsense', ['mgmt.homeNames.rebind.opnsense', 'Services > Unbound DNS > Advanced: add {{domain}} to "Private Domains" and apply.']],
  ['pfsense', 'pfSense', ['mgmt.homeNames.rebind.pfsense', 'Services > DNS Resolver > Custom options: add the lines server: and private-domain: "{{domain}}", save and apply.']],
  ['firefox', 'Firefox', ['mgmt.homeNames.rebind.firefox', 'With "DNS over HTTPS" on, Firefox ignores private answers from its DNS provider and asks the system instead; at "Max protection" the names never resolve: add {{domain}} under Settings > Privacy & Security > DNS over HTTPS > Manage exceptions.']],
  ['provider', '', ['mgmt.homeNames.rebind.provider', 'A provider\'s router without an exception list: point the devices (or the router\'s DNS setting) at a resolver that passes private answers, such as the one of your ISP or a public one. Some DNS services (NextDNS, for example) have a rebinding block of their own to switch off for {{domain}}.']],
];

// An app's home names among its routes: one per name, as a copy of the
// route it belongs to (same name, so its settings link stays the route's),
// with the home name as the host. `status` is GET /api/home-names.
export const homeRoutesFor = (status, routes) => {
  const names = (status && status.names) || [];
  const out = [];
  const seen = new Set();
  (routes || []).forEach((route) => {
    const host = String((route && route.UseHost && route.Host) || '').trim().toLowerCase();
    names.forEach((name) => {
      if (name.route === route.Name && name.source === host && !seen.has(name.host)) {
        seen.add(name.host);
        out.push({ ...route, Host: name.host, homeName: true });
      }
    });
  });
  return out;
};

// The suffixes ("home.example.org") in force.
export const activeSuffixes = (status) => ((status && status.zones) || []).filter((row) => row.active && row.suffix).map((row) => row.suffix);

// True for a host under a home suffix in force.
export const isHomeName = (status, host) => {
  const name = String(host || '').trim().toLowerCase().replace(/:\d+$/, '').replace(/\.$/, '');
  return activeSuffixes(status).some((suffix) => name.endsWith('.' + suffix));
};

// What a route's home name will be, for a new URL: the first label of its
// address (or of the app's name) under each suffix in force.
export const proposedHomeNames = (status, label) => {
  const text = String(label || '').trim().toLowerCase();
  // an address or a URL with a port of its own gets no home name (the server's rule)
  if (text.includes(':') || /^\d+\.\d+\.\d+\.\d+$/.test(text)) return [];
  const first = text.split('.')[0];
  if (!validLabel(first)) return [];
  return activeSuffixes(status).map((suffix) => first + '.' + suffix);
};

// Every text this module can return, for the translation test.
export const allTexts = () => {
  const texts = PROBLEM_CODES.map(problemText).concat([problemText('x')]);
  const row = { active: true, wildcard: '*.home.example.org', recordAddress: '192.168.1.2' };
  ['ok', 'conflict', 'error', 'no_address', 'pending'].forEach((record) => {
    texts.push(recordState({ ...row, record })[1]);
  });
  texts.push(recordState({ ...row, record: 'ok', recordAddress: '' })[1]);
  texts.push(certificateState({ ...row, certificateCovered: true, certificateSource: 'zone' })[1]);
  texts.push(certificateState({ ...row, certificateCovered: false })[1]);
  ['ok', 'other', 'none', 'no_address', 'error'].forEach((result) => texts.push(serverCheckText({ result, name: 'x' })[1]));
  texts.push(browserCheckText('opened', 'x')[1], browserCheckText('failed', 'x')[1]);
  REBIND_HELP.forEach((entry) => texts.push(entry[2]));
  return texts;
};
