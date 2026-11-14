// Fork addition: what the Domains dialog filled by hand proposes
// (pages/config/forkZoneMode.jsx): the certificate mode from the name, in
// words. Plain functions: see zoneMode.test.mjs. Texts are ['key', 'English', vars].

// Names that no public certificate authority will certify: only used at home.
const LOCAL_SUFFIXES = ['local', 'lan', 'home', 'internal', 'localdomain', 'home.arpa', 'intranet', 'corp', 'private', 'test', 'localhost'];

export const cleanZone = (text) => String(text || '').trim().toLowerCase().replace(/^https?:\/\//, '').replace(/[/:].*$/, '').replace(/\.$/, '');

export const isAddressName = (name) => /^\d{1,3}(\.\d{1,3}){3}$/.test(name) || name.includes(':');

export const isLocalZone = (text) => {
  const name = cleanZone(text);
  if (!name) return false;
  if (isAddressName(name)) return true;
  return LOCAL_SUFFIXES.some((suffix) => name === suffix || name.endsWith('.' + suffix));
};

export const isZoneName = (text) => /^[a-z0-9-]+(\.[a-z0-9-]+)+$/i.test(cleanZone(text));

export const MODES = ['LETSENCRYPT', 'SELFSIGNED', 'PROVIDED', 'DISABLED'];

// The mode proposed for a name, with the reason; null while there is no name.
export const proposeMode = (text) => {
  const name = cleanZone(text);
  if (!isZoneName(name)) return null;
  if (isLocalZone(name)) {
    return { mode: 'SELFSIGNED', source: ['mgmt.config.zones.fork.proposeLocal', '{{name}} is a name for use at home: no public authority certifies it, so Cosmos makes its own certificate. Devices show a warning until they trust Cosmos\' certificate authority (Domains > Certificates).', { name }] };
  }
  return { mode: 'LETSENCRYPT', source: ['mgmt.config.zones.fork.proposePublic', '{{name}} is a public name: Let\'s Encrypt certifies it for free, renewed by Cosmos.', { name }] };
};

// One sentence per mode, for the chips.
export const modeHint = (mode) => ({
  LETSENCRYPT: ['mgmt.config.zones.fork.hintLetsEncrypt', 'A free certificate every browser trusts, ordered and renewed by Cosmos. Needs a public domain, and either DNS access (below) or the domain reaching this server on port 80.'],
  SELFSIGNED: ['mgmt.config.zones.fork.hintSelfSigned', 'Cosmos makes the certificate itself. Works for any name, also at home; each device must trust Cosmos\' certificate authority once, or it shows a warning.'],
  PROVIDED: ['mgmt.config.zones.fork.hintProvided', 'A certificate you already have (from your provider or your company): paste it and its private key below. You renew it yourself.'],
  DISABLED: ['mgmt.config.zones.fork.hintDisabled', 'No certificate for this domain: its apps are served by plain HTTP only. Passwords then cross the network readable.'],
}[mode] || null);
