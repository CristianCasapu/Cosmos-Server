// Fork addition: the pure part of the automatic dependency update
// (scripts/fork-deps-update.mjs): version arithmetic, the cool-down, the
// holds, the file edits, the commit trailers and the checks of what an update
// may change. Nothing here reads the network or runs a program, so all of it
// is tested with recorded answers (fork-deps-lib.test.mjs). Standard library
// only: choosing versions must not run any code of the dependencies.

// ---------------------------------------------------------------- versions

// parseVersion reads 1.2.3, v1.2.3, 1.2.3-beta.4, v26.1.5+incompatible.
// Returns null for anything else (ranges, tags, Go pseudo-versions are
// recognised separately by isPseudo).
export function parseVersion(text) {
  const m = /^v?(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})(?:-([0-9A-Za-z.-]{1,64}))?(\+[0-9A-Za-z.-]{1,64})?$/.exec(String(text));
  if (!m) return null;
  return { major: +m[1], minor: +m[2], patch: +m[3], pre: m[4] ? m[4].split('.') : [], build: m[5] || '', raw: String(text) };
}

// A Go pseudo-version (v0.0.0-20260913233941-486c28b93357 and its forms).
export function isPseudo(text) {
  return /^v\d+\.\d+\.\d+-(?:0\.|[0-9A-Za-z.-]*\.0\.)?\d{14}-[0-9a-f]{12}(\+incompatible)?$/.test(String(text));
}

function comparePre(a, b) {
  if (!a.length && !b.length) return 0;
  if (!a.length) return 1; // a release is above its pre-releases
  if (!b.length) return -1;
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    if (i >= a.length) return -1;
    if (i >= b.length) return 1;
    const x = a[i], y = b[i];
    const nx = /^\d+$/.test(x), ny = /^\d+$/.test(y);
    if (nx && ny) { if (+x !== +y) return +x < +y ? -1 : 1; continue; }
    if (nx !== ny) return nx ? -1 : 1;
    if (x !== y) return x < y ? -1 : 1;
  }
  return 0;
}

// compareVersions orders two parsed versions (-1, 0, 1).
export function compareVersions(a, b) {
  for (const k of ['major', 'minor', 'patch']) if (a[k] !== b[k]) return a[k] < b[k] ? -1 : 1;
  return comparePre(a.pre, b.pre);
}

// line is what "the same major" means: 1.x.y for >= 1, 0.y.x for 0.y (semver
// lets 0.y to 0.z break, and npm's caret agrees).
export function lineOf(v) {
  return v.major > 0 ? `${v.major}` : `0.${v.minor}`;
}

// kindOf says whether going from one version to another stays in the line.
export function kindOf(from, to) {
  return lineOf(from) === lineOf(to) ? 'minor' : 'major';
}

// ceilingAllows: '-' never, '1' stays in major 1, '1.5' stays at or below 1.5.x.
export function ceilingAllows(ceiling, v) {
  if (ceiling === undefined || ceiling === null || ceiling === '') return true;
  if (ceiling === '-') return false;
  const parts = ceiling.split('.').map(Number);
  if (parts.some((n) => !Number.isInteger(n))) return false;
  if (v.major !== parts[0]) return v.major < parts[0];
  if (parts.length > 1 && v.minor !== parts[1]) return v.minor < parts[1];
  if (parts.length > 2 && v.patch !== parts[2]) return v.patch < parts[2];
  return true;
}

// ---------------------------------------------------------------- the cool-down

// cutoffFor: the start of `today` (UTC) minus `days`. A version counts only
// when it was published before this moment, so the choice is the same all day.
export function cutoffFor(today, days) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(today)) throw new Error(`not a day: ${today}`);
  if (!Number.isInteger(days) || days < 0 || days > 365) throw new Error(`not a cool-down: ${days}`);
  const start = Date.parse(`${today}T00:00:00Z`);
  if (Number.isNaN(start)) throw new Error(`not a day: ${today}`);
  return new Date(start - days * 86400000);
}

export function ageDays(published, today) {
  return Math.floor((Date.parse(`${today}T00:00:00Z`) - Date.parse(published)) / 86400000);
}

// choose picks, from candidates [{version, time}], the newest version in the
// current one's line (kind minor) and the newest of a newer line (kind major),
// each published before the cut-off and allowed by the ceiling. A current
// pre-release (@mui/lab's alphas) may move to a later pre-release of the same
// x.y.z or to a release of its line. Pre-releases are never chosen otherwise.
export function choose(current, candidates, cutoff, ceiling) {
  const cur = parseVersion(current);
  if (!cur) return { minor: null, major: null };
  let minor = null, major = null;
  for (const c of candidates) {
    const v = parseVersion(c.version);
    if (!v || !c.time) continue;
    const t = Date.parse(c.time);
    if (Number.isNaN(t) || t >= cutoff.getTime()) continue;
    if (compareVersions(v, cur) <= 0) continue;
    if (!ceilingAllows(ceiling, v)) continue;
    if (v.build && !cur.build) continue; // +incompatible only after +incompatible
    if (v.pre.length) {
      const sameBase = v.major === cur.major && v.minor === cur.minor && v.patch === cur.patch;
      if (!(cur.pre.length && sameBase && v.pre[0] === cur.pre[0])) continue;
    }
    const entry = { version: c.version, time: c.time, parsed: v };
    if (lineOf(v) === lineOf(cur)) {
      if (!minor || compareVersions(v, minor.parsed) > 0) minor = entry;
    } else if (!v.pre.length) {
      if (!major || compareVersions(v, major.parsed) > 0) major = entry;
    }
  }
  const strip = (e) => (e ? { version: e.version, time: e.time } : null);
  return { minor: strip(minor), major: strip(major) };
}

// chooseLazy is choose for sources that give the publish time one version
// at a time (the Go module proxy): it asks for the times of the newest
// versions first and stops as soon as both answers are known.
export async function chooseLazy(current, versions, timeOf, cutoff, ceiling) {
  const cur = parseVersion(current);
  if (!cur) return { minor: null, major: null };
  const ordered = versions
    .map((version) => ({ version, parsed: parseVersion(version) }))
    .filter((c) => c.parsed && !c.parsed.pre.length && compareVersions(c.parsed, cur) > 0 && ceilingAllows(ceiling, c.parsed))
    .filter((c) => !c.parsed.build || cur.build)
    .sort((a, b) => compareVersions(b.parsed, a.parsed));
  const found = [];
  let haveMinor = false, haveMajor = false;
  for (const c of ordered) {
    const inLine = lineOf(c.parsed) === lineOf(cur);
    if ((inLine && haveMinor) || (!inLine && haveMajor)) continue;
    const time = await timeOf(c.version);
    if (!time || Date.parse(time) >= cutoff.getTime()) continue;
    found.push({ version: c.version, time });
    if (inLine) haveMinor = true; else haveMajor = true;
    if (haveMinor && haveMajor) break;
  }
  return choose(current, found, cutoff, ceiling);
}

// ---------------------------------------------------------------- holds

// parseHolds reads scripts/fork-deps-holds.txt:
//   <eco> <name> <ceiling> <reason...>     a hold (ceiling '-', '1' or '1.5')
//   group <eco> <name> <name>...           packages whose majors move together
//   # comment
export function parseHolds(text) {
  const holds = new Map(), groups = [], paths = [];
  const ecos = new Set(['go', 'npm', 'action', 'image', 'toolchain', 'node', 'bundled', 'path']);
  text.split('\n').forEach((raw, i) => {
    const line = raw.replace(/#.*/, '').trim();
    if (!line) return;
    const f = line.split(/\s+/);
    if (f[0] === 'group') {
      if (f.length < 4 || !ecos.has(f[1])) throw new Error(`holds line ${i + 1}: a group needs an ecosystem and two names`);
      groups.push({ eco: f[1], names: f.slice(2) });
      return;
    }
    if (!ecos.has(f[0]) || f.length < 4) throw new Error(`holds line ${i + 1}: expected <eco> <name> <ceiling> <reason>`);
    if (!/^(-|\d+(\.\d+){0,2})$/.test(f[2])) throw new Error(`holds line ${i + 1}: ceiling must be -, N, N.N or N.N.N`);
    const reason = raw.trim().split(/\s+/).slice(3).join(' ');
    if (f[0] === 'path') paths.push(f[1]);
    holds.set(`${f[0]} ${f[1]}`, { ceiling: f[2], reason });
  });
  return { holds, groups, paths };
}

export function holdOf(holds, eco, name) {
  return holds.holds.get(`${eco} ${name}`) || null;
}

// ---------------------------------------------------------------- go.mod

export function parseGoMod(text) {
  const out = { go: null, toolchain: null, requires: [], replaced: new Set() };
  let block = null;
  for (const raw of text.split('\n')) {
    const line = raw.trim();
    if (block) {
      if (line === ')') { block = null; continue; }
      const body = line.replace(/\/\/.*$/, '').trim();
      if (!body) continue;
      if (block === 'require') {
        const [path, version] = body.split(/\s+/);
        out.requires.push({ path, version, indirect: /\/\/\s*indirect/.test(line) });
      } else if (block === 'replace') {
        out.replaced.add(body.split(/\s+/)[0]);
      }
      continue;
    }
    let m;
    if ((m = /^go\s+(\S+)/.exec(line))) out.go = m[1];
    else if ((m = /^toolchain\s+(\S+)/.exec(line))) out.toolchain = m[1];
    else if (/^(require|replace)\s*\($/.test(line)) block = line.startsWith('require') ? 'require' : 'replace';
    else if ((m = /^require\s+(\S+)\s+(\S+)/.exec(line))) out.requires.push({ path: m[1], version: m[2], indirect: /\/\/\s*indirect/.test(line) });
    else if ((m = /^replace\s+(\S+)/.exec(line))) out.replaced.add(m[1]);
  }
  return out;
}

// The Go toolchain versions on the module proxy: v0.0.1-go1.27.5.linux-amd64.
export function toolchainVersions(list) {
  const out = [];
  for (const v of list) {
    const m = /^v0\.0\.1-go(\d+)\.(\d+)\.(\d+)\.linux-amd64$/.exec(v);
    if (m) out.push({ module: v, version: `${m[1]}.${m[2]}.${m[3]}` });
  }
  return out;
}

// ---------------------------------------------------------------- package.json

// specParts splits ^1.2.3 / ~1.2.3 / 1.2.3. Anything else (ranges, tags,
// URLs) is not touched by the machine.
export function specParts(spec) {
  const m = /^([\^~]?)(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$/.exec(String(spec));
  return m ? { op: m[1], version: m[2] } : null;
}

// setSpec changes one entry of one dependency section in package.json's text,
// leaving every other byte as it is. Throws unless it finds it exactly once.
export function setSpec(text, section, name, from, to) {
  const start = text.indexOf(`"${section}": {`);
  if (start < 0) throw new Error(`package.json has no ${section}`);
  const end = text.indexOf('}', start);
  const body = text.slice(start, end);
  const needle = `${JSON.stringify(name)}: ${JSON.stringify(from)}`;
  const at = body.indexOf(needle);
  if (at < 0 || body.indexOf(needle, at + 1) >= 0) throw new Error(`${name} ${from} not found once in ${section}`);
  return text.slice(0, start + at) + `${JSON.stringify(name)}: ${JSON.stringify(to)}` + text.slice(start + at + needle.length);
}

// lockVersions: path -> version for every package of a lock file v3.
export function lockVersions(lock) {
  const out = new Map();
  for (const [path, entry] of Object.entries(lock.packages || {})) if (path && entry && entry.version) out.set(path, entry.version);
  return out;
}

// downgrades lists the lock entries whose version went down.
export function downgrades(before, after) {
  const out = [];
  for (const [path, version] of after) {
    const was = before.get(path);
    if (!was || was === version) continue;
    const a = parseVersion(was), b = parseVersion(version);
    if (a && b && compareVersions(b, a) < 0) out.push(`${path.replace(/^.*node_modules\//, '')} ${was} -> ${version}`);
  }
  return out;
}

// ---------------------------------------------------------------- actions and images

// usesLines finds `uses: owner/repo[/path]@<sha> # vX.Y.Z` in a file.
export function usesLines(text) {
  const out = [];
  text.split('\n').forEach((line, i) => {
    const m = /^(\s*-?\s*uses:\s*)([A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+)((?:\/[A-Za-z0-9_.-]+)*)@([0-9a-f]{40})\s+#\s*(v?\d+(?:\.\d+){0,2})\s*$/.exec(line);
    if (m) out.push({ line: i, prefix: m[1], repo: m[2], sub: m[3], sha: m[4], version: m[5] });
  });
  return out;
}

export function setUses(text, repo, fromSha, toSha, toVersion) {
  let n = 0;
  const out = text.split('\n').map((line) => {
    const m = /^(\s*-?\s*uses:\s*)([A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+)((?:\/[A-Za-z0-9_.-]+)*)@([0-9a-f]{40})\s+#.*$/.exec(line);
    if (!m || m[2] !== repo || m[4] !== fromSha) return line;
    n++;
    return `${m[1]}${m[2]}${m[3]}@${toSha} # ${toVersion}`;
  });
  if (!n) throw new Error(`no pin of ${repo} at ${fromSha}`);
  return out.join('\n');
}

// The images the machine follows, with what their version is read from.
export const IMAGES = [
  { name: 'golang', re: /\bgolang:(\d+\.\d+)\b/g },
  { name: 'node', re: /\bnode:(\d{2,3})(?![\w.:-])/g },
  { name: 'mcr.microsoft.com/playwright', re: /\bmcr\.microsoft\.com\/playwright:v(\d+\.\d+\.\d+)-noble\b/g },
];

export function imageRefs(text) {
  const out = [];
  for (const img of IMAGES) for (const m of text.matchAll(img.re)) out.push({ image: img.name, tag: m[1] });
  return out;
}

// replaceImage changes one image's tag everywhere in a text; with it the
// playwright@<version> that goes with the Playwright image.
export function replaceImage(text, image, from, to) {
  const esc = (s) => s.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&');
  switch (image) {
    case 'golang': return text.replace(new RegExp(`\\bgolang:${esc(from)}\\b`, 'g'), `golang:${to}`);
    case 'node': return text.replace(new RegExp(`\\bnode:${esc(from)}(?![\\w.:-])`, 'g'), `node:${to}`);
    case 'mcr.microsoft.com/playwright':
      return text
        .replace(new RegExp(`\\bmcr\\.microsoft\\.com/playwright:v${esc(from)}-noble\\b`, 'g'), `mcr.microsoft.com/playwright:v${to}-noble`)
        .replace(new RegExp(`\\bplaywright@${esc(from)}\\b`, 'g'), `playwright@${to}`);
    default: throw new Error(`not an image the machine follows: ${image}`);
  }
}

// nodeLines reads nodejs.org/dist/index.json: for each major that has an LTS
// release, its first LTS release (the day the line became LTS).
export function nodeLtsLines(index) {
  const first = new Map();
  for (const r of index) {
    const m = /^v(\d+)\.\d+\.\d+$/.exec(r.version || '');
    if (!m || !r.lts || !r.date) continue;
    const major = +m[1];
    const was = first.get(major);
    if (!was || r.date < was) first.set(major, r.date);
  }
  return [...first].map(([major, date]) => ({ version: `${major}.0.0`, time: `${date}T00:00:00Z` }));
}

// ---------------------------------------------------------------- trailers

// One line per changed package in the commit message, read back by
// fork-deps-notes.mjs for the release's dependencies.json.
const SAFE_NAME = /^[A-Za-z0-9@/._+-]{1,214}$/;
const SAFE_VERSION = /^[A-Za-z0-9.+_-]{1,64}$/;
const ECOS = new Set(['go', 'npm', 'action', 'image', 'toolchain', 'node']);

export function safeText(text, max = 300) {
  return String(text).replace(/[\u0000-\u001f\u007f-\u009f]/g, ' ').replace(/\s+/g, ' ').trim().slice(0, max);
}

export function trailer(c) {
  for (const [k, re] of [['name', SAFE_NAME], ['from', SAFE_VERSION], ['to', SAFE_VERSION]]) {
    if (!re.test(c[k])) throw new Error(`unsafe ${k}: ${c[k]}`);
  }
  if (!ECOS.has(c.eco)) throw new Error(`unknown ecosystem ${c.eco}`);
  if (c.kind !== 'minor' && c.kind !== 'major') throw new Error(`unknown kind ${c.kind}`);
  const day = /^\d{4}-\d{2}-\d{2}/.exec(c.time || '');
  return `Fork-Dependency: ${c.eco} ${c.name} ${c.from} ${c.to} ${c.kind} ${day ? day[0] : '-'}`;
}

export function otherTrailer(word, c) {
  if (!['Tried', 'Left', 'Dropped', 'Held'].includes(word)) throw new Error(`unknown trailer ${word}`);
  const name = SAFE_NAME.test(c.name) ? c.name : 'unknown';
  const from = SAFE_VERSION.test(c.from || '') ? c.from : '-';
  const to = SAFE_VERSION.test(c.to || '') ? c.to : '-';
  const eco = ECOS.has(c.eco) ? c.eco : 'npm';
  return `Fork-Dependency-${word}: ${eco} ${name} ${from} ${to} ${safeText(c.reason || '', 200)}`;
}

export function parseTrailers(message) {
  const out = { changes: [], tried: [], left: [], dropped: [] };
  for (const line of String(message).split('\n')) {
    let m = /^Fork-Dependency: (\S+) (\S+) (\S+) (\S+) (minor|major) (\S+)$/.exec(line);
    if (m && ECOS.has(m[1]) && SAFE_NAME.test(m[2]) && SAFE_VERSION.test(m[3]) && SAFE_VERSION.test(m[4])) {
      out.changes.push({ eco: m[1], name: m[2], from: m[3], to: m[4], kind: m[5], published: m[6] === '-' ? null : m[6] });
      continue;
    }
    m = /^Fork-Dependency-(Tried|Left|Dropped): (\S+) (\S+) (\S+) (\S+) ?(.*)$/.exec(line);
    if (m && ECOS.has(m[2]) && SAFE_NAME.test(m[3])) {
      out[m[1].toLowerCase()].push({ eco: m[2], name: m[3], from: m[4], to: m[5], reason: safeText(m[6], 200) });
    }
  }
  return out;
}

// summarise merges the trailers of several commits (oldest first) into one
// list: a package changed twice keeps its first "from" and last "to".
export function summarise(parsedList, today) {
  const byKey = new Map();
  const extra = { tried: [], left: [], dropped: [] };
  for (const p of parsedList) {
    for (const c of p.changes) {
      const key = `${c.eco} ${c.name}`;
      const was = byKey.get(key);
      byKey.set(key, was ? { ...c, from: was.from, kind: was.kind === 'major' || c.kind === 'major' ? 'major' : 'minor' } : { ...c });
    }
    for (const k of Object.keys(extra)) extra[k].push(...p[k]);
  }
  const changes = [...byKey.values()].map((c) => ({
    eco: c.eco, name: c.name, from: c.from, to: c.to, kind: c.kind,
    ageDays: c.published ? Math.max(0, ageDays(`${c.published}T00:00:00Z`, today)) : null,
  }));
  const dedupe = (list) => [...new Map(list.map((x) => [`${x.eco} ${x.name} ${x.to}`, x])).values()];
  return { changes, tried: dedupe(extra.tried), left: dedupe(extra.left), dropped: dedupe(extra.dropped) };
}

export function metaSummary(summary) {
  const majors = summary.changes.filter((c) => c.kind === 'major');
  return {
    minor: summary.changes.length - majors.length,
    major: majors.length,
    majors: majors.slice(0, 10).map((c) => safeText(`${c.name} ${c.to}`, 80)),
  };
}

export function notesSection(summary) {
  if (!summary.changes.length && !summary.dropped.length && !summary.tried.length) return '';
  const lines = ['', '### Dependencies updated automatically', ''];
  const md = (s) => String(s).replace(/[`*_[\]<>|@\\]/g, (ch) => `\\${ch}`);
  for (const c of summary.changes) {
    lines.push(`- ${md(c.eco)} ${md(c.name)} ${md(c.from)} → ${md(c.to)} (${c.kind}${c.ageDays !== null ? `, ${c.ageDays} days old` : ''})`);
  }
  for (const [title, list] of [['Majors tried and dropped', summary.dropped], ['Majors tried, failed the quick checks', summary.tried]]) {
    if (!list.length) continue;
    lines.push('', `${title}:`, '');
    for (const c of list) lines.push(`- ${md(c.eco)} ${md(c.name)} ${md(c.from)} → ${md(c.to)}: ${md(c.reason)}`);
  }
  return lines.join('\n') + '\n';
}

// ---------------------------------------------------------------- what an update may change

const ALLOWED_FILES = new Set(['go.mod', 'go.sum', 'package.json', 'package-lock.json',
  'scripts/fork-ci-smoke/package.json', 'scripts/fork-ci-smoke/package-lock.json']);

// allowedPath: may the machine change this file at all?
export function allowedPath(path, holds) {
  if (holds && holds.paths.some((p) => path === p || path.startsWith(p.replace(/\/?$/, '/')))) return false;
  if (ALLOWED_FILES.has(path)) return true;
  if (/^\.github\/actions\/[A-Za-z0-9_.-]+\/action\.ya?ml$/.test(path)) return true;
  if (/^scripts\/[A-Za-z0-9_.-]+\.(mjs|sh)$/.test(path)) return true;
  return false;
}

// lineChangeAllowed: in action files and scripts only lines that pin an
// action, name the Node line or name an image may differ.
export function changedLinesAllowed(path, before, after) {
  const a = before.split('\n'), b = after.split('\n');
  if (a.length !== b.length) return `${path}: lines were added or removed`;
  const action = path.startsWith('.github/');
  for (let i = 0; i < a.length; i++) {
    if (a[i] === b[i]) continue;
    if (action) {
      const pin = /^\s*-?\s*uses:\s*[A-Za-z0-9_.-]+\/[A-Za-z0-9_.\/-]+@[0-9a-f]{40} # v\d+(\.\d+){0,2}$/;
      const nodeLine = /^\s*node-version:\s*\d{2,3}$/;
      // the same action from the same repository: only its commit and version
      const repo = (s) => s.replace(/@[0-9a-f]{40} # v\S+$/, '');
      if (pin.test(a[i]) && pin.test(b[i]) && repo(a[i]) === repo(b[i])) continue;
      if (nodeLine.test(a[i]) && nodeLine.test(b[i]) && a[i].replace(/\d+$/, '') === b[i].replace(/\d+$/, '')) continue;
      return `${path}:${i + 1}: only action pins and the Node line may change`;
    }
    // scripts: the line must be the same once image names are blanked out
    const blank = (s) => s
      .replace(/\bgolang:\d+\.\d+\b/g, 'golang:X').replace(/\bnode:\d{2,3}(?![\w.:-])/g, 'node:X')
      .replace(/\bmcr\.microsoft\.com\/playwright:v\d+\.\d+\.\d+-noble\b/g, 'PW').replace(/\bplaywright@\d+\.\d+\.\d+\b/g, 'pw@X');
    if (blank(a[i]) !== blank(b[i])) return `${path}:${i + 1}: only image names may change in a script`;
  }
  return null;
}

// goModChangeAllowed: only the go line and require versions; the module, the
// replace and exclude directives, a toolchain line, and the held modules stay.
export function goModChangeAllowed(before, after, holds) {
  const a = parseGoMod(before), b = parseGoMod(after);
  if (b.toolchain !== a.toolchain) return 'go.mod: the toolchain line changed';
  const strip = (t) => t.split('\n').filter((l) => !/^\s*(go\s+\d|[A-Za-z0-9.\/_~-]+\s+v\S+(\s*\/\/\s*indirect)?\s*$|require\s+\S+\s+v\S+)/.test(l.trim()) && l.trim() !== '' && !/^\s*(require\s*\(|\))\s*$/.test(l)).join('\n');
  if (strip(before) !== strip(after)) return 'go.mod: something other than versions changed';
  const was = new Map(a.requires.map((r) => [r.path, r.version]));
  for (const r of b.requires) {
    const h = holdOf(holds, 'go', r.path);
    if (h && h.ceiling === '-' && was.get(r.path) !== r.version) return `go.mod: held module ${r.path} changed`;
  }
  return null;
}

// packageJsonChangeAllowed: the same packages, the same everything, only the
// version of existing entries may change.
export function packageJsonChangeAllowed(before, after) {
  const a = JSON.parse(before), b = JSON.parse(after);
  const sections = ['dependencies', 'devDependencies', 'optionalDependencies', 'peerDependencies'];
  const rest = (p) => JSON.stringify(Object.fromEntries(Object.entries(p).filter(([k]) => !sections.includes(k))));
  if (rest(a) !== rest(b)) return 'package.json: something other than dependency versions changed';
  for (const s of sections) {
    const x = a[s] || {}, y = b[s] || {};
    if (JSON.stringify(Object.keys(x)) !== JSON.stringify(Object.keys(y))) return `package.json: the ${s} list changed`;
    for (const k of Object.keys(y)) if (x[k] !== y[k] && !specParts(y[k])) return `package.json: ${k} has an unusual version ${y[k]}`;
  }
  return null;
}

// lockAllowed: every package comes from the public npm registry with an integrity.
export function lockAllowed(text) {
  let lock;
  try { lock = JSON.parse(text); } catch { return 'package-lock.json: not JSON'; }
  for (const [path, e] of Object.entries(lock.packages || {})) {
    if (!path || e.link) continue;
    if (e.inBundle) continue;
    if (!e.resolved || !e.resolved.startsWith('https://registry.npmjs.org/')) return `package-lock.json: ${path} is not from registry.npmjs.org`;
    if (!/^sha(512|384|256)-[A-Za-z0-9+/=]{40,}$/.test(e.integrity || '')) return `package-lock.json: ${path} has no integrity`;
  }
  return null;
}
