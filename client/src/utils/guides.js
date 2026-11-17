// Fork addition (plan 50): the "How to use it" guides of apps.
//
// A guide is a file of src/guides (one per kind of app: s3.md), written in a
// small subset of Markdown that this module reads into a tree; the panel
// renders that tree with React elements (never as HTML), and the examples
// test (scripts/fork-guide-examples.mjs) runs every code block exactly as the
// panel shows it. Nothing here imports the UI (node --test).
//
// The subset: "## Title {#id}" starts a section ("{#id admin}" for
// administrators only); "### " and "#### " headings; paragraphs; "- " and
// "1. " lists; "> " notes; "| a | b |" tables; fenced code with an info
// string ("```sh id=linux-restic runner=restic"); inline `code`, **bold**,
// [text](https://…) (any other kind of link stays text) and {{placeholders}}.
// A line "{{if cond}}" … "{{end}}" (also inside code; "{{if !cond}}" for
// not) keeps what it holds only when the condition holds. There is no raw
// HTML: "<" is text like any other character.
//
// The values come from the app's interfaces (utils/appInterfaces.js, the one
// rule), never from a rule of their own. Every value has a type and is
// checked against it when it is put in: the alphabets allowed cannot close a
// shell quote, a string of any language or a code fence, so a value is safe
// by construction or the example is refused ("could not be built").

import { appInterfaces, parseHost, safeURL } from './appInterfaces.js';

// ---------------------------------------------------------------------------
// reading a guide

const HEADER = /^---\n([\s\S]*?)\n---\n?/;

export const parseMeta = (text) => {
  const m = String(text || '').replace(/\r\n?/g, '\n').match(HEADER);
  const meta = {};
  if (!m) return { meta, body: String(text || '').replace(/\r\n?/g, '\n') };
  m[1].split('\n').forEach((line) => {
    const i = line.indexOf(':');
    if (i > 0) meta[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  });
  return { meta, body: String(text).replace(/\r\n?/g, '\n').slice(m[0].length) };
};

const ID = /^[a-z0-9][a-z0-9-]{0,47}$/;
const COND = /^\{\{(if (!?[a-z_]+)|end)\}\}$/;

// inline text: [{t:'text'|'code'|'strong'|'link'|'value', text, href, name}]
export const parseInline = (text) => {
  const out = [];
  const s = String(text || '');
  const re = /`([^`\n]+)`|\*\*([^*\n]+)\*\*|\[([^\]\n]+)\]\(([^)\s]+)\)|\{\{([a-z_]+)\}\}|\{\{if (!?[a-z_]+)\}\}/g;
  let last = 0;
  let m;
  while ((m = re.exec(s)) !== null) {
    if (m.index > last) out.push({ t: 'text', text: s.slice(last, m.index) });
    if (m[1] !== undefined) out.push({ t: 'code', text: m[1] });
    else if (m[2] !== undefined) out.push({ t: 'strong', text: m[2] });
    else if (m[3] !== undefined) {
      const href = /^https?:\/\//i.test(m[4]) ? safeURL(m[4]) : '';
      out.push(href ? { t: 'link', text: m[3], href } : { t: 'text', text: m[0] });
    } else if (m[5] === 'end') out.push({ t: 'end' });
    else if (m[5] !== undefined) out.push({ t: 'value', name: m[5] });
    else out.push({ t: 'if', cond: m[6] });
    last = re.lastIndex;
  }
  if (last < s.length) out.push({ t: 'text', text: s.slice(last) });
  return out;
};

const tableCells = (line) => line.trim().replace(/^\|/, '').replace(/\|$/, '').split('|').map((c) => parseInline(c.trim()));

// The body as blocks: [{type, …}]; conditions as {type:'if', cond, blocks}.
const parseBlocks = (lines) => {
  const root = [];
  const stack = [{ blocks: root }];
  const push = (block) => stack[stack.length - 1].blocks.push(block);
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    const trimmed = line.trim();
    const cond = trimmed.match(COND);
    if (cond) {
      if (cond[1] === 'end') {
        if (stack.length > 1) stack.pop();
      } else {
        const block = { type: 'if', cond: cond[2], blocks: [] };
        push(block);
        stack.push(block);
      }
      i++;
      continue;
    }
    if (trimmed === '') { i++; continue; }
    const fence = line.match(/^```([a-z0-9]*)(.*)$/);
    if (fence) {
      const attrs = {};
      fence[2].trim().split(/\s+/).filter(Boolean).forEach((pair) => {
        const [k, v] = pair.split('=');
        if (/^[a-z]+$/.test(k) && v && /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$/.test(v)) attrs[k] = v;
      });
      const code = [];
      i++;
      while (i < lines.length && !/^```\s*$/.test(lines[i])) { code.push(lines[i]); i++; }
      i++; // the closing fence (or the end)
      push({ type: 'code', lang: fence[1] || 'text', id: ID.test(attrs.id || '') ? attrs.id : '', runner: attrs.runner || '', attrs, lines: code });
      continue;
    }
    const heading = line.match(/^(#{3,4}) (.+)$/);
    if (heading) {
      push({ type: 'heading', level: heading[1].length, inline: parseInline(heading[2].trim()) });
      i++;
      continue;
    }
    if (/^> /.test(line) || line === '>') {
      const text = [];
      while (i < lines.length && (/^> /.test(lines[i]) || lines[i] === '>')) { text.push(lines[i].replace(/^> ?/, '')); i++; }
      const joined = text.join(' ').trim();
      const kind = /^\*\*(Warning|Careful)/.test(joined) ? 'warning' : 'info';
      push({ type: 'note', kind, inline: parseInline(joined) });
      continue;
    }
    if (/^\|/.test(trimmed)) {
      const rows = [];
      while (i < lines.length && /^\|/.test(lines[i].trim())) { rows.push(lines[i]); i++; }
      const body = rows.filter((r) => !/^\|[\s:|-]+\|$/.test(r.trim()));
      push({ type: 'table', head: tableCells(body[0] || ''), rows: body.slice(1).map(tableCells) });
      continue;
    }
    const item = line.match(/^(- |\d+\. )(.*)$/);
    if (item) {
      const ordered = /\d/.test(item[1]);
      const items = [];
      while (i < lines.length) {
        const m = lines[i].match(/^(- |\d+\. )(.*)$/);
        if (m && /\d/.test(m[1]) === ordered) { items.push(m[2]); i++; continue; }
        if (items.length && /^ {2,}\S/.test(lines[i])) { items[items.length - 1] += ' ' + lines[i].trim(); i++; continue; }
        break;
      }
      push({ type: 'list', ordered, items: items.map(parseInline) });
      continue;
    }
    const para = [];
    while (i < lines.length && lines[i].trim() !== '' && !COND.test(lines[i].trim()) && !/^(```|#{2,4} |> |\||- |\d+\. )/.test(lines[i])) { para.push(lines[i].trim()); i++; }
    if (para.length === 0) { para.push(line.trim()); i++; }
    push({ type: 'para', inline: parseInline(para.join(' ')) });
  }
  return root;
};

// The whole guide: { meta, intro: blocks, sections: [{ id, title, admin, blocks }] }.
export const parseGuide = (text) => {
  const { meta, body } = parseMeta(text);
  const lines = body.split('\n');
  const sections = [];
  let intro = [];
  let current = null;
  let chunk = [];
  const flush = () => {
    const blocks = parseBlocks(chunk);
    if (current) current.blocks = blocks; else intro = blocks;
    chunk = [];
  };
  lines.forEach((line) => {
    const m = line.match(/^## (.+?)(?:\s+\{#([a-z0-9-]+)( admin)?\})?\s*$/);
    if (m) {
      flush();
      const id = m[2] && ID.test(m[2]) ? m[2] : 'section-' + (sections.length + 1);
      current = { id, title: parseInline(m[1]), admin: !!m[3], blocks: [] };
      sections.push(current);
    } else chunk.push(line);
  });
  flush();
  return { meta, intro, sections };
};

// Every code block of a guide, in order, with the section it is in.
export const codeBlocks = (guide) => {
  const out = [];
  const walk = (blocks, section, conds) => blocks.forEach((b) => {
    if (b.type === 'code') out.push({ ...b, section, conds });
    else if (b.type === 'if') walk(b.blocks, section, [...conds, b.cond]);
  });
  walk(guide.intro, '', []);
  guide.sections.forEach((s) => walk(s.blocks, s.id, s.admin ? ['admin'] : []));
  return out;
};

// ---------------------------------------------------------------------------
// values

// What each placeholder may hold. All alphabets here are free of quotes,
// spaces, backslashes, "$", "`", "{", "}", "<", ">" and new lines.
const SAFE_URL = /^https?:\/\/[a-z0-9._-]+(:\d{1,5})?$/;
const SAFE_HOST = /^[a-z0-9._-]+(:\d{1,5})?$/;
export const VALUE_TYPES = {
  endpoint: (v) => SAFE_URL.test(v) && v.length <= 300,
  endpoint_host: (v) => SAFE_HOST.test(v) && v.length <= 300,
  endpoint_scheme: (v) => v === 'https' || v === 'http',
  console: (v) => /^https?:\/\/[a-z0-9._-]+(:\d{1,5})?(\/[A-Za-z0-9._~/-]*)?$/.test(v) && v.length <= 400,
  region: (v) => /^[a-z0-9-]{1,32}$/.test(v),
  bucket: (v) => validBucket(v),
  name: (v) => validName(v),
  network: (v) => /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/.test(v),
  container: (v) => /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/.test(v),
  port: (v) => /^\d{1,5}$/.test(v) && Number(v) >= 1 && Number(v) <= 65535,
  ca_pem: (v) => /^-----BEGIN CERTIFICATE-----\n[A-Za-z0-9+/=\n]{40,8000}\n-----END CERTIFICATE-----$/.test(v),
};

// An S3 bucket name (AWS' rules, without dots: a dot breaks TLS names and
// virtual-host addressing).
export const validBucket = (v) => typeof v === 'string' && /^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$/.test(v) && !/--/.test(v) && !/^xn--|^sthree-|-s3alias$/.test(v);

// The name of a machine or a site in the examples.
export const validName = (v) => typeof v === 'string' && /^[a-z0-9][a-z0-9-]{0,30}$/.test(v);

// Secrets are never values: the guide shows these markers, which the reader
// replaces (and the examples test replaces with its own test keys).
export const SECRET_MARKER = /\b(PASTE|CHOOSE)-[A-Z0-9-]+-HERE\b/g;

// ---------------------------------------------------------------------------
// the facts of one app, from its interfaces

const HOME_KINDS = ['home', 'local', 'lan'];

const targetOf = (route) => {
  const m = String((route && route.Target) || '').match(/^(https?):\/\/([a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}):(\d{1,5})\/?$/);
  return m ? { scheme: m[1], container: m[2], port: m[3] } : null;
};

const plainEndpoint = (url) => {
  const safe = safeURL(url);
  if (!safe) return '';
  const u = new URL(safe);
  if (u.pathname !== '/' || u.search || u.hash) return ''; // an S3 API under a path does not work
  return u.protocol + '//' + u.host;
};

// The places a reader may connect from.
export const PLACES = ['home', 'internet', 'container'];

// facts: { kind, places: {home, internet, container} (the address from each,
// '' when none), choice, values: {...}, flags: {...}, notes: [...] }.
//   app: { key, container, routes } (as appInterfaces takes it), the routes
//        merged with GET /api/dashboard (ForkGuide, ForkShielded, …)
//   ctx: { place, location, isAdmin, ports } (useOpenContext)
//   status: GET /api/status (CACert)
//   choice: 'home' | 'internet' | 'container' | '' (the default for the place)
//   typed: { bucket, name } as the reader typed them
export const guideFacts = (app, ctx, status, choice, typed) => {
  const c = ctx || {};
  const routes = (app && app.routes) || [];
  const byName = new Map(routes.map((r) => [r.Name, r]));
  const kind = (routes.find((r) => r.ForkGuide) || {}).ForkGuide || '';
  const interfaces = appInterfaces(app, { ...c, isAdmin: false });
  const guided = interfaces.filter((i) => i.web && byName.get(i.route) && byName.get(i.route).ForkGuide === kind);
  const api = guided.filter((i) => i.role === 'api');
  const pool = api.length ? api : guided.filter((i) => i.role !== 'admin');
  const pick = (kinds) => {
    for (const k of kinds) {
      const found = pool.find((i) => i.kind === k && plainEndpoint(i.url));
      if (found) return found;
    }
    return null;
  };
  const homeItem = pick(HOME_KINDS);
  const netItem = pick(['public']);
  // the console (the app's administration) is named to administrators only
  const consoleItem = !c.isAdmin ? null : interfaces.find((i) => i.web && i.role === 'admin' && !i.derived && (i.kind === 'local' || i.kind === 'home' || i.kind === 'lan'))
    || interfaces.find((i) => i.web && i.role === 'admin');
  const apiRoute = (homeItem && byName.get(homeItem.route)) || (netItem && byName.get(netItem.route)) || routes.find((r) => r.ForkGuide && targetOf(r)) || null;
  const target = apiRoute && apiRoute.Mode === 'SERVAPP' ? targetOf(apiRoute) : null;
  const networks = ((apiRoute && apiRoute.ForkNetworks) || []).filter((n) => !['bridge', 'host', 'none'].includes(n) && VALUE_TYPES.network(String(n)));
  const containerURL = c.isAdmin && target && networks.length ? target.scheme + '://' + target.container + ':' + target.port : '';
  const places = {
    home: homeItem ? plainEndpoint(homeItem.url) : '',
    internet: netItem ? plainEndpoint(netItem.url) : '',
    container: containerURL,
  };
  const where = (c.place && c.place.where) || 'away';
  let chosen = PLACES.includes(choice) && places[choice] ? choice : '';
  if (!chosen) chosen = where === 'home' ? (places.home ? 'home' : places.internet ? 'internet' : '') : (places.internet ? 'internet' : places.home ? 'home' : '');
  if (!chosen && places.container) chosen = 'container';
  const item = chosen === 'home' ? homeItem : chosen === 'internet' ? netItem : null;
  const itemRoute = item ? byName.get(item.route) : null;
  const endpoint = places[chosen] || '';
  const hostOf = (url) => (url ? new URL(url).host : '');
  const ownCA = !!(item && itemRoute && (itemRoute.ForkOwnCAHosts || []).some((h) => {
    const p = parseHost(h);
    return p && p.name === new URL(item.url).hostname;
  }));
  const shielded = !!(netItem && byName.get(netItem.route) && byName.get(netItem.route).ForkShielded && !netItem.homeName);
  const image = String((routes.find((r) => r.ForkImage) || {}).ForkImage || '');
  const caPem = String((status && status.CACert) || '').replace(/\r\n?/g, '\n').trim();
  const t = typed || {};
  const values = {
    endpoint,
    endpoint_host: hostOf(endpoint),
    endpoint_scheme: endpoint ? new URL(endpoint).protocol.replace(':', '') : '',
    console: consoleItem ? consoleItem.url : '',
    region: String((apiRoute && apiRoute.ForkGuideRegion) || (routes.find((r) => r.ForkGuideRegion) || {}).ForkGuideRegion || 'us-east-1'),
    bucket: validBucket(t.bucket) ? t.bucket : 'backups-' + (validName(t.name) ? t.name : 'server1'),
    name: validName(t.name) ? t.name : 'server1',
    network: networks[0] || '',
    container: target ? target.container : '',
    port: target ? target.port : '',
    ca_pem: VALUE_TYPES.ca_pem(caPem) ? caPem : '',
  };
  if (!validBucket(values.bucket)) values.bucket = 'backups-server1';
  const flags = {
    home: chosen === 'home',
    internet: chosen === 'internet',
    container: chosen === 'container',
    cloudflare: chosen === 'internet' && shielded,
    any_cloudflare: shielded,
    ca: chosen !== 'container' && ownCA,
    ca_known: !!values.ca_pem,
    http: values.endpoint_scheme === 'http',
    admin: !!c.isAdmin,
    console: !!values.console,
    has_home: !!places.home,
    has_internet: !!places.internet,
    no_internet: !places.internet,
    has_container: !!places.container,
    endpoint: !!endpoint,
    rustfs: !!c.isAdmin && /^(docker\.io\/)?rustfs\/rustfs([:@]|$)/.test(image),
  };
  return { kind, places, choice: chosen, values, flags };
};

// ---------------------------------------------------------------------------
// filling

const condHolds = (cond, flags) => {
  const not = cond.startsWith('!');
  const name = not ? cond.slice(1) : cond;
  const value = !!(flags && flags[name]);
  return not ? !value : value;
};

// Keeps the parts of a line whose conditions ("{{if x}} … {{end}}" inside a
// sentence) hold.
export const visibleInline = (items, flags) => {
  const out = [];
  const stack = [];
  (items || []).forEach((x) => {
    if (x.t === 'if') stack.push(x.cond);
    else if (x.t === 'end') stack.pop();
    else if (stack.every((cond) => condHolds(cond, flags))) out.push(x);
  });
  return out;
};

// Keeps the blocks whose conditions hold.
export const visibleBlocks = (blocks, flags) => {
  const out = [];
  (blocks || []).forEach((b) => {
    if (b.type === 'if') {
      if (condHolds(b.cond, flags)) visibleBlocks(b.blocks, flags).forEach((x) => out.push(x));
    } else out.push(b);
  });
  return out;
};

// A value put into text: '' with a problem when it is not of its type.
export const fillValue = (name, values) => {
  const check = VALUE_TYPES[name];
  if (!check) return { ok: false, problem: 'unknown value ' + name };
  const value = values && values[name] !== undefined ? String(values[name]) : '';
  if (value === '') return { ok: false, problem: 'missing ' + name };
  if (!check(value)) return { ok: false, problem: 'not plain: ' + name };
  return { ok: true, value };
};

// A code block as the reader gets it: { ok, text, parts: [{text, kind}],
// problem }. kind: 'plain', 'value' (a fact of this server) or 'secret' (a
// marker the reader replaces).
export const renderCode = (block, facts) => {
  const flags = (facts && facts.flags) || {};
  const values = (facts && facts.values) || {};
  const keep = [];
  const stack = [];
  (block.lines || []).forEach((line) => {
    const m = line.trim().match(COND);
    if (m) {
      if (m[1] === 'end') stack.pop(); else stack.push(m[2]);
      return;
    }
    if (stack.every((cond) => condHolds(cond, flags))) keep.push(line);
  });
  const parts = [];
  let problem = '';
  const text = keep.join('\n');
  const re = /\{\{([a-z_]+)\}\}/g;
  let last = 0;
  let m;
  const plain = (s) => {
    let at = 0;
    let x;
    SECRET_MARKER.lastIndex = 0;
    while ((x = SECRET_MARKER.exec(s)) !== null) {
      if (x.index > at) parts.push({ text: s.slice(at, x.index), kind: 'plain' });
      parts.push({ text: x[0], kind: 'secret' });
      at = SECRET_MARKER.lastIndex;
    }
    if (at < s.length) parts.push({ text: s.slice(at), kind: 'plain' });
  };
  while ((m = re.exec(text)) !== null) {
    plain(text.slice(last, m.index));
    const filled = fillValue(m[1], values);
    if (!filled.ok) { problem = problem || filled.problem; parts.push({ text: m[0], kind: 'value' }); } else parts.push({ text: filled.value, kind: 'value' });
    last = re.lastIndex;
  }
  plain(text.slice(last));
  if (problem) return { ok: false, text: '', parts, problem };
  return { ok: true, text: parts.map((p) => p.text).join(''), parts, problem: '' };
};

// A value in prose: the text, or the placeholder itself when it cannot be filled.
export const inlineValue = (name, facts) => {
  const filled = fillValue(name, (facts && facts.values) || {});
  return filled.ok ? filled.value : '';
};

// The sections a reader gets: administrators all, others those not marked admin.
export const visibleSections = (guide, flags) => (guide.sections || []).filter((s) => !s.admin || (flags && flags.admin));

// The app of a page address (/cosmos-ui/guide/<key>): a container's name or a URL's name.
export const findGuideApp = (apps, key) => {
  const wanted = String(key || '').toLowerCase();
  if (!wanted) return null;
  return (apps || []).find((a) => a.container && a.container === wanted)
    || (apps || []).find((a) => a.routes.some((r) => String(r.Name || '').toLowerCase() === wanted)) || null;
};

// The page address of an app's guide (its container's name, else its first URL's name).
export const guidePath = (app, section) => {
  const key = app && (app.container || (app.routes[0] && app.routes[0].Name));
  if (!key) return '';
  return '/cosmos-ui/guide/' + encodeURIComponent(key) + (section && ID.test(section) ? '#' + section : '');
};

// The kind of guide an app has ('' for none).
export const appGuide = (app) => ((app && app.routes) || []).map((r) => r.ForkGuide).find(Boolean) || '';
