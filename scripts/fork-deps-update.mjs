#!/usr/bin/env node
// Fork addition: brings every dependency of the fork to its newest version
// that has been published for at least COOLDOWN_DAYS (7), on a clean copy of
// the repository, as commits (see /storage/dev/plans/41 and FORK.md):
//
//   1. the Go toolchain line of go.mod (patch releases of its minor);
//   2. every direct Go module, inside its line (`go get`, `go mod tidy`);
//   3. every npm package inside its major (package.json and the lock file,
//      root and scripts/fork-ci-smoke/; the transitive packages under npm's
//      own --before filter); the Playwright image follows its npm package;
//   4. the actions pinned in .github/actions/ (by commit, version in the
//      comment). Pins in .github/workflows/ are listed, not changed: the
//      workflow token may not push them;
//   5. the images golang and node named in scripts/;
//   6. then majors, one at a time: npm majors (with the packages that must
//      move together, see the holds file), Go 0.x lines, the Go minor line,
//      the Node line, action majors. Each is kept as a commit of its own only
//      when the quick checks pass; otherwise undone and recorded;
//   7. an empty commit that records what was tried, failed and left;
//   8. the advisory gate: scripts/fork-deps-check.sh must find nothing new.
//
// It never edits a source file, never runs an install script
// (--ignore-scripts everywhere), and never touches what
// scripts/fork-deps-holds.txt holds. Every program is started with a fixed
// argument list, never through a shell. Choosing versions uses only Node's
// standard library: no code of the dependencies runs until the quick checks.
//
// Environment:
//   COOLDOWN_DAYS=7        minimum age of a version, in days
//   FORK_DEPS_TODAY=YYYY-MM-DD  the day the cut-off is computed from (UTC; default today):
//                          same base, same day => the same commits, ids included
//   MAJORS=1               0: no majors
//   MAX_MAJORS=6           majors tried in one run
//   QUICK_CHECKS=sh scripts/fork-deps-quick.sh    run as "<cmd> go|npm" (empty: none)
//   ADVISORY_CHECK=sh scripts/fork-deps-check.sh  (empty: no gate)
//   SKIP_TRIED=<status.json>   a major that failed at the same version last time is not tried again
//   OUT=<file>             where the report (JSON) goes; default: fork-deps-report.json outside the tree
//   FORK_DEPS_RECORDED=<file>  answer every outside lookup from this file (tests)
//   FORK_DEPS_RECORD=<file>    write every outside lookup's answer to this file
//   GITHUB_TOKEN           used for api.github.com only (rate limit)
// Exit: 0 done (changed or not: see the report's "commits"), 1 the advisory
// gate or a step failed (report says which), 2 cannot run.
//
// By hand on a server, exactly as in CI, in a copy (never the working clone:
// the quick checks run `npm ci`), with Go copied out of the golang image:
//   S=/storage/dev/.cache/cosmos-autodeps-manual; mkdir -p $S
//   git clone -q --branch custom <repository> $S/repo
//   docker run --rm -v $S:/s golang:1.27 cp -a /usr/local/go /s/go
//   docker run --rm --name cosmos-autodeps-manual -v $S/repo:/src -v $S/go:/usr/local/go:ro -w /src \
//     -e PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin -e OUT=/src/../report.json node:26 \
//     sh -c 'git config --global --add safe.directory /src && node scripts/fork-deps-update.mjs'
// The commits are then in $S/repo; nothing is pushed.

import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync, existsSync, statSync } from 'node:fs';
import { join } from 'node:path';
import * as lib from './fork-deps-lib.mjs';

const ROOT = process.cwd();
const env = process.env;
const today = env.FORK_DEPS_TODAY || new Date().toISOString().slice(0, 10);
const cooldown = Number(env.COOLDOWN_DAYS ?? 7);
const cutoff = lib.cutoffFor(today, cooldown);
const cutoffIso = cutoff.toISOString();
const majorsOn = env.MAJORS !== '0';
const maxMajors = Number(env.MAX_MAJORS ?? 6);
const quickCmd = (env.QUICK_CHECKS ?? 'sh scripts/fork-deps-quick.sh').trim();
const advisoryCmd = (env.ADVISORY_CHECK ?? 'sh scripts/fork-deps-check.sh').trim();
const outPath = env.OUT || join(ROOT, '..', 'fork-deps-report.json');

const report = {
  schema: 1, today, cooldownDays: cooldown, cutoff: cutoffIso,
  changes: [], tried: [], left: [], held: [], notes: [], commits: [], failedStep: null, failure: null,
};

// ---------------------------------------------------------------- running programs

function run(cmd, args, opts = {}) {
  const childEnv = { ...env, ...(opts.env || {}) };
  // the quick checks run the dependencies' code: they get no token
  if (opts.untrusted) for (const k of Object.keys(childEnv)) if (/TOKEN|SECRET|ACTIONS_/.test(k)) delete childEnv[k];
  try {
    return execFileSync(cmd, args, {
      cwd: opts.cwd || ROOT, encoding: 'utf8', maxBuffer: 256 << 20,
      stdio: ['ignore', 'pipe', 'pipe'], timeout: opts.timeout || 30 * 60 * 1000,
      env: childEnv,
    });
  } catch (err) {
    const out = `${err.stdout || ''}\n${err.stderr || ''}`.trim();
    const e = new Error(`${cmd} ${args.join(' ')} failed: ${out.split('\n').slice(-40).join('\n')}`);
    e.output = out;
    throw e;
  }
}

// the quick checks: "<QUICK_CHECKS> go|npm"; returns null or the first lines of the failure
function quickCheck(what) {
  if (!quickCmd || what === 'none') return null;
  const [cmd, ...args] = quickCmd.split(/\s+/);
  try {
    run(cmd, [...args, what], { timeout: 60 * 60 * 1000, untrusted: true });
    return null;
  } catch (err) {
    const lines = (err.output || err.message).split('\n').filter((l) => /\S/.test(l));
    const firstError = lines.findIndex((l) => /error|fail|FAIL|panic|not ok|Error/.test(l));
    const from = firstError < 0 ? Math.max(0, lines.length - 20) : firstError;
    return lines.slice(from, from + 20).map((l) => lib.safeText(l, 240)).join('\n');
  }
}

const git = (...args) => run('git', args);
const read = (p) => readFileSync(join(ROOT, p), 'utf8');
const write = (p, text) => writeFileSync(join(ROOT, p), text);

function commitDate() {
  return `${today}T00:00:00Z`;
}

// git commit -F - reads the message from standard input
function commitWithMessage(message, allowEmpty) {
  execFileSync('git', ['commit', '-q', ...(allowEmpty ? ['--allow-empty'] : []), '-F', '-'], {
    cwd: ROOT, input: message, encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'],
    env: { ...env, GIT_AUTHOR_DATE: commitDate(), GIT_COMMITTER_DATE: commitDate() },
  });
  return git('rev-parse', 'HEAD').trim();
}

function commitAll(subject, body, trailers, allowEmpty = false) {
  git('add', '-A');
  if (!allowEmpty && !git('diff', '--cached', '--name-only').trim()) return null;
  const message = [subject, '', body.trim(), '', ...trailers].join('\n').replace(/\n{3,}/g, '\n\n') + '\n';
  const sha = commitWithMessage(message, allowEmpty);
  report.commits.push({ sha, subject });
  return sha;
}

function undo() {
  git('reset', '-q', '--hard', 'HEAD');
  git('clean', '-q', '-fd', '--', '.');
}

// ---------------------------------------------------------------- outside lookups

const recorded = env.FORK_DEPS_RECORDED ? JSON.parse(readFileSync(env.FORK_DEPS_RECORDED, 'utf8')) : null;
const recording = env.FORK_DEPS_RECORD ? {} : null;

async function lookup(key, fetcher) {
  if (recorded) {
    if (!(key in recorded)) throw new Error(`no recorded answer for ${key}`);
    const answer = recorded[key];
    // a recorded failure of the registry (tests): {"__error__": "HTTP 403"}
    if (answer && typeof answer === 'object' && !Array.isArray(answer) && answer.__error__) throw Object.assign(new Error(`${key}: ${answer.__error__}`), { lookup: true });
    return answer;
  }
  let value;
  try {
    value = await fetcher();
  } catch (err) {
    // a registry that cannot be read skips the step that needed it, not the run
    err.lookup = true;
    throw err;
  }
  if (recording) recording[key] = value;
  return value;
}

async function fetchJson(url, { allow404 = false } = {}) {
  const headers = { 'User-Agent': 'cosmos-fork-deps-update', Accept: 'application/json' };
  if (url.startsWith('https://api.github.com/') && env.GITHUB_TOKEN) headers.Authorization = `Bearer ${env.GITHUB_TOKEN}`;
  for (let attempt = 1; ; attempt++) {
    const res = await fetch(url, { headers, signal: AbortSignal.timeout(60000) });
    if (allow404 && res.status === 404) return null;
    // a registry answering 5xx or limiting the rate is retried twice: they are transient
    if ((res.status >= 500 || res.status === 429) && attempt < 3) { await new Promise((r) => setTimeout(r, 5000 * attempt)); continue; }
    if (!res.ok) throw new Error(`${url}: HTTP ${res.status}`);
    const text = await res.text();
    if (text.length > 64 << 20) throw new Error(`${url}: answer too large`);
    return JSON.parse(text);
  }
}

const sources = {
  // npm: every version with its publish time, deprecated ones left out
  async npm(name) {
    return lookup(`npm ${name}`, async () => {
      const doc = await fetchJson(`https://registry.npmjs.org/${name.replace('/', '%2f')}`);
      const out = [];
      for (const [version, meta] of Object.entries(doc.versions || {})) {
        if (meta && meta.deprecated) continue;
        if (doc.time && doc.time[version]) out.push({ version, time: doc.time[version] });
      }
      return out;
    });
  },
  // Go: the module's versions (retracted ones left out by the go command)
  async goVersions(mod) {
    return lookup(`go-versions ${mod}`, async () => {
      const info = JSON.parse(run('go', ['list', '-m', '-versions', '-json', mod], { env: { GOFLAGS: '-mod=mod' } }));
      return info.Versions || [];
    });
  },
  async goTime(mod, version) {
    return lookup(`go-time ${mod}@${version}`, async () => {
      const info = JSON.parse(run('go', ['list', '-m', '-json', `${mod}@${version}`], { env: { GOFLAGS: '-mod=mod' } }));
      return info.Time || null;
    });
  },
  async githubReleases(repo) {
    return lookup(`gh-releases ${repo}`, async () => {
      const list = await fetchJson(`https://api.github.com/repos/${repo}/releases?per_page=50`);
      return list.filter((r) => !r.draft && !r.prerelease && r.published_at).map((r) => ({ version: r.tag_name, time: r.published_at }));
    });
  },
  async githubTagSha(repo, tag) {
    return lookup(`gh-sha ${repo}@${tag}`, async () => {
      const c = await fetchJson(`https://api.github.com/repos/${repo}/commits/${encodeURIComponent(tag)}`);
      return /^[0-9a-f]{40}$/.test(c.sha || '') ? c.sha : null;
    });
  },
  async nodeIndex() {
    return lookup('node-index', async () => (await fetchJson('https://nodejs.org/dist/index.json')).map((r) => ({ version: r.version, date: r.date, lts: r.lts })));
  },
  async imageTag(image, tag) {
    return lookup(`image ${image}:${tag}`, async () => {
      if (image === 'mcr.microsoft.com/playwright') {
        const list = await fetchJson('https://mcr.microsoft.com/v2/playwright/tags/list');
        return (list.tags || []).includes(tag);
      }
      const answer = await fetchJson(`https://hub.docker.com/v2/namespaces/library/repositories/${image}/tags/${encodeURIComponent(tag)}`, { allow404: true });
      return !!answer;
    });
  },
};

// ---------------------------------------------------------------- the steps

const holds = lib.parseHolds(read('scripts/fork-deps-holds.txt'));
const triedBefore = new Set();
if (env.SKIP_TRIED && existsSync(env.SKIP_TRIED)) {
  try {
    const last = JSON.parse(readFileSync(env.SKIP_TRIED, 'utf8'));
    for (const t of [...(last.tried || []), ...(last.dropped || [])]) triedBefore.add(`${t.eco} ${t.name} ${t.to}`);
  } catch { /* an unreadable status only means everything is tried */ }
}
const majors = []; // {eco, label, members:[{eco,name,from,to,time}], apply(), check}

function held(eco, name) {
  const h = lib.holdOf(holds, eco, name);
  if (h && h.ceiling === '-') report.held.push({ eco, name, reason: h.reason });
  return h;
}

// 1. the Go toolchain line
async function goToolchain() {
  const mod = lib.parseGoMod(read('go.mod'));
  const cur = lib.parseVersion(mod.go);
  if (!cur || held('toolchain', 'go')?.ceiling === '-') return;
  const list = lib.toolchainVersions(await sources.goVersions('golang.org/toolchain'));
  const timeOf = async (v) => sources.goTime('golang.org/toolchain', list.find((x) => x.version === v).module);
  // within the minor: 1.27.4 -> 1.27.5 (lineOf groups by major, so compare minors here)
  const sameMinor = list.map((x) => x.version).filter((v) => lib.parseVersion(v).minor === cur.minor && lib.parseVersion(v).major === cur.major);
  const patch = await lib.chooseLazy(mod.go, sameMinor, timeOf, cutoff);
  if (patch.minor) {
    run('go', ['mod', 'edit', `-go=${patch.minor.version}`]);
    const c = { eco: 'toolchain', name: 'go', from: mod.go, to: patch.minor.version, kind: 'minor', time: patch.minor.time };
    const failure = quickCheck('go');
    if (failure) { undo(); report.tried.push({ ...c, reason: `quick checks: ${failure.split('\n')[0]}`, failure }); }
    else { report.changes.push(c); commitAll(`Update the Go toolchain to ${c.to}`, `go.mod: go ${c.from} -> ${c.to} (published ${c.time.slice(0, 10)}).`, [lib.trailer(c)]); }
  }
  // a newer minor line (1.28) is a major-class change, once its image exists
  const others = list.map((x) => x.version).filter((v) => { const p = lib.parseVersion(v); return p.major === cur.major && p.minor > cur.minor; });
  let newest = null;
  for (const v of others.sort((a, b) => lib.compareVersions(lib.parseVersion(b), lib.parseVersion(a)))) {
    const t = await timeOf(v);
    if (t && Date.parse(t) < cutoff.getTime()) { newest = { version: v, time: t }; break; }
  }
  if (newest) {
    const p = lib.parseVersion(newest.version);
    const line = `${p.major}.${p.minor}`;
    const oldLine = `${cur.major}.${cur.minor}`;
    if (!(await sources.imageTag('golang', line))) {
      report.left.push({ eco: 'toolchain', name: 'go', from: mod.go, to: newest.version, reason: `the image golang:${line} does not exist yet` });
    } else {
      majors.push({
        eco: 'toolchain', label: `the Go line to ${line}`, check: 'go',
        members: [{ eco: 'toolchain', name: 'go', from: lib.parseGoMod(read('go.mod')).go, to: newest.version, time: newest.time }],
        apply() {
          run('go', ['mod', 'edit', `-go=${newest.version}`]);
          replaceImagesInScripts('golang', oldLine, line);
        },
      });
    }
  }
}

// 2. direct Go modules
async function goModules() {
  const before = read('go.mod');
  const mod = lib.parseGoMod(before);
  const heldVersions = new Map();
  for (const r of mod.requires) if (lib.holdOf(holds, 'go', r.path)?.ceiling === '-') heldVersions.set(r.path, r.version);
  const planned = [];
  for (const r of mod.requires) {
    if (r.indirect) continue;
    if (mod.replaced.has(r.path)) { report.held.push({ eco: 'go', name: r.path, reason: 'replaced in go.mod' }); continue; }
    const h = held('go', r.path);
    if (h && h.ceiling === '-') continue;
    if (lib.isPseudo(r.version)) {
      const tagged = (await sources.goVersions(r.path)).filter((v) => lib.parseVersion(v) && !lib.parseVersion(v).pre.length);
      if (tagged.length) report.left.push({ eco: 'go', name: r.path, from: r.version, to: tagged[tagged.length - 1], reason: 'an untagged version is in use: by hand' });
      continue;
    }
    const versions = await sources.goVersions(r.path);
    const pick = await lib.chooseLazy(r.version, versions, (v) => sources.goTime(r.path, v), cutoff, h?.ceiling);
    if (pick.minor) planned.push({ eco: 'go', name: r.path, from: r.version, to: pick.minor.version, kind: 'minor', time: pick.minor.time });
    if (pick.major && majorsOn) {
      const m = { eco: 'go', name: r.path, from: r.version, to: pick.major.version, time: pick.major.time };
      majors.push({ eco: 'go', label: `${r.path} ${pick.major.version}`, check: 'go', members: [m], apply: () => goGet([m], heldVersions) });
    }
  }
  if (!planned.length) return;
  const kept = [];
  for (const c of planned) {
    const snapshot = [read('go.mod'), read('go.sum')];
    const problem = goGet([c], heldVersions);
    if (problem) {
      write('go.mod', snapshot[0]); write('go.sum', snapshot[1]);
      report.tried.push({ ...c, reason: problem });
    } else kept.push(c);
  }
  if (!kept.length) return;
  const failure = quickCheck('go');
  if (failure) {
    undo();
    for (const c of kept) report.tried.push({ ...c, reason: 'quick checks failed with the Go modules updated together', failure });
    return;
  }
  report.changes.push(...kept);
  commitAll(`Update ${kept.length} Go module${kept.length > 1 ? 's' : ''} inside their versions`,
    kept.map((c) => `- ${c.name} ${c.from} -> ${c.to} (published ${c.time.slice(0, 10)})`).join('\n'), kept.map(lib.trailer));
}

// go get one or more module@version, tidy, and refuse what moves a held module or the go line
function goGet(list, heldVersions) {
  const goLine = lib.parseGoMod(read('go.mod')).go;
  try {
    run('go', ['get', ...list.map((c) => `${c.name}@${c.to}`)], { env: { GOFLAGS: '-mod=mod', GOTOOLCHAIN: 'auto' } });
    run('go', ['mod', 'tidy'], { env: { GOFLAGS: '-mod=mod', GOTOOLCHAIN: 'auto' } });
  } catch (err) {
    return `go get failed: ${lib.safeText(err.message.split('\n').slice(0, 3).join(' '), 200)}`;
  }
  const after = lib.parseGoMod(read('go.mod'));
  if (after.go !== goLine) return `would raise the go line to ${after.go}`;
  if (after.toolchain) return 'would add a toolchain line';
  for (const r of after.requires) {
    if (heldVersions.has(r.path) && heldVersions.get(r.path) !== r.version) return `would move the held module ${r.path}`;
  }
  return null;
}

// 3. npm, in one folder (root, scripts/fork-ci-smoke)
const npmArgs = ['--package-lock-only', '--ignore-scripts', '--no-audit', '--no-fund', `--before=${cutoffIso}`];

function npm(dir, args) {
  return run('npm', [...args], { cwd: join(ROOT, dir) });
}

async function npmFolder(dir) {
  const pjPath = join(dir, 'package.json');
  if (!existsSync(join(ROOT, pjPath))) return;
  const lockPath = join(dir, 'package-lock.json');
  const pj = JSON.parse(read(pjPath));
  const lockBefore = JSON.parse(read(lockPath));
  const installed = lib.lockVersions(lockBefore);
  const planned = [];
  let text = read(pjPath);
  const groupsDone = new Set();
  for (const section of ['dependencies', 'devDependencies']) {
    for (const [name, spec] of Object.entries(pj[section] || {})) {
      const h = held('npm', name);
      if (h && h.ceiling === '-') continue;
      const parts = lib.specParts(spec);
      if (!parts) { report.left.push({ eco: 'npm', name, from: spec, to: spec, reason: 'a version range the machine does not edit' }); continue; }
      const current = installed.get(`node_modules/${name}`) || parts.version;
      const pick = lib.choose(current, await sources.npm(name), cutoff, h?.ceiling);
      if (pick.minor) {
        planned.push({ eco: 'npm', name, from: current, to: pick.minor.version, kind: 'minor', time: pick.minor.time, section, spec: parts.op + pick.minor.version, was: spec });
        text = lib.setSpec(text, section, name, spec, parts.op + pick.minor.version);
      }
      if (pick.major && majorsOn && !groupsDone.has(name)) {
        const group = holds.groups.find((g) => g.eco === 'npm' && g.names.includes(name));
        const members = [];
        for (const member of group ? group.names : [name]) {
          groupsDone.add(member);
          const memberSection = ['dependencies', 'devDependencies'].find((s) => pj[s] && pj[s][member]);
          if (!memberSection) continue;
          const mh = lib.holdOf(holds, 'npm', member);
          if (mh && mh.ceiling === '-') continue;
          const mParts = lib.specParts(pj[memberSection][member]);
          if (!mParts) continue;
          const mCur = installed.get(`node_modules/${member}`) || mParts.version;
          const mPick = member === name ? pick : lib.choose(mCur, await sources.npm(member), cutoff, mh?.ceiling);
          if (mPick.major) members.push({ eco: 'npm', name: member, from: mCur, to: mPick.major.version, time: mPick.major.time, section: memberSection, op: mParts.op, dir });
        }
        if (members.length) {
          majors.push({
            eco: 'npm', label: members.map((m) => `${m.name} ${m.to}`).join(', '), check: 'npm', members,
            apply() {
              let t = read(pjPath);
              const now = JSON.parse(t);
              for (const m of members) t = lib.setSpec(t, m.section, m.name, now[m.section][m.name], m.op + m.to);
              write(pjPath, t);
              npm(dir, ['install', ...npmArgs]);
              return null;
            },
          });
        }
      }
    }
  }
  // the Playwright image goes with the npm package of the smoke check
  const pw = planned.find((c) => c.name === 'playwright' && dir === 'scripts/fork-ci-smoke');
  if (pw && !(await sources.imageTag('mcr.microsoft.com/playwright', `v${pw.to}-noble`))) {
    report.left.push({ ...pw, eco: 'npm', reason: `the image mcr.microsoft.com/playwright:v${pw.to}-noble does not exist yet` });
    planned.splice(planned.indexOf(pw), 1);
    text = lib.setSpec(text, pw.section, 'playwright', pw.spec, pw.was);
  }
  write(pjPath, text);

  // the transitive packages inside their ranges, under npm's own age filter
  let refreshed = true;
  try {
    npm(dir, ['update', '--no-save', ...npmArgs]);
    const down = lib.downgrades(installed, lib.lockVersions(JSON.parse(read(lockPath))));
    if (down.length) throw new Error(`it would go back to older versions (${down.slice(0, 5).join(', ')}): some were installed by hand less than ${cooldown} days ago`);
  } catch (err) {
    refreshed = false;
    write(lockPath, JSON.stringify(lockBefore, null, 2) + '\n');
    report.notes.push(`${dir || '.'}: the packages below the direct ones were not refreshed this week: ${lib.safeText(err.message.split('\n').find((l) => /notarget|older versions|ERESOLVE/.test(l)) || err.message.split('\n')[0], 300)}`);
  }
  try {
    npm(dir, ['install', ...npmArgs]);
  } catch (err) {
    undo();
    for (const c of planned) report.tried.push({ ...c, reason: `npm could not resolve the lock file: ${lib.safeText(err.message.split('\n').find((l) => /ERESOLVE|notarget|error/.test(l)) || '', 200)}` });
    return;
  }
  if (pw && planned.includes(pw)) replaceImagesInScripts('mcr.microsoft.com/playwright', pw.from, pw.to);
  const lockAfter = read(lockPath);
  const lockChanged = JSON.stringify(lockBefore) !== JSON.stringify(JSON.parse(lockAfter));
  if (!planned.length && !lockChanged) return;
  const failure = quickCheck(dir === '' ? 'npm' : 'none');
  if (failure) {
    undo();
    for (const c of planned) report.tried.push({ ...c, reason: 'quick checks failed with the npm packages updated together', failure });
    return;
  }
  report.changes.push(...planned.map(({ section, spec, was, ...c }) => c));
  const where = dir ? ` in ${dir}` : '';
  commitAll(planned.length ? `Update ${planned.length} npm package${planned.length > 1 ? 's' : ''}${where} inside their majors` : `Refresh the npm lock file${where}`,
    [...planned.map((c) => `- ${c.name} ${c.from} -> ${c.to} (published ${c.time.slice(0, 10)})`),
      refreshed ? `The packages they bring were refreshed inside their ranges, each published before ${cutoffIso.slice(0, 10)}.` : 'The packages they bring were not refreshed (see the run).'].join('\n'),
    planned.map(lib.trailer));
}

// v7 -> v7.0.0: a pin's comment may name only the major
function fullTag(tag) {
  const parts = tag.replace(/^v/, '').split('.');
  while (parts.length < 3) parts.push('0');
  return `v${parts.join('.')}`;
}

// 4. actions in .github/actions (pins in workflows are listed only)
async function actions() {
  const files = run('git', ['ls-files', '.github']).split('\n').filter((f) => /\.ya?ml$/.test(f));
  const planned = [];
  let changedFiles = new Map();
  for (const file of files) {
    const text = read(file);
    for (const u of lib.usesLines(text)) {
      const h = held('action', u.repo);
      if (h && h.ceiling === '-') continue;
      const releases = await sources.githubReleases(u.repo);
      const pick = lib.choose(fullTag(u.version), releases.filter((r) => lib.parseVersion(r.version)), cutoff, h?.ceiling);
      const inWorkflows = file.startsWith('.github/workflows/');
      for (const [kind, p] of [['minor', pick.minor], ['major', pick.major]]) {
        if (!p) continue;
        const sha = await sources.githubTagSha(u.repo, p.version);
        if (!sha) continue;
        const c = { eco: 'action', name: u.repo, from: u.version, to: p.version, kind, time: p.time, sha, file, fromSha: u.sha };
        if (inWorkflows) {
          report.left.push({ eco: 'action', name: u.repo, from: u.version, to: p.version, reason: `pinned in ${file}: the workflow token may not change workflow files` });
        } else if (kind === 'minor') {
          planned.push(c);
          changedFiles.set(file, lib.setUses(changedFiles.get(file) || text, u.repo, u.sha, sha, p.version));
        } else if (majorsOn) {
          majors.push({ eco: 'action', label: `${u.repo} ${p.version}`, check: 'none', members: [c],
            apply() { write(file, lib.setUses(read(file), u.repo, u.sha, sha, p.version)); return null; } });
        }
      }
    }
  }
  // the same action listed twice (several files) is reported once
  report.left = [...new Map(report.left.map((l) => [`${l.eco} ${l.name} ${l.to} ${l.reason}`, l])).values()];
  if (!planned.length) return;
  for (const [file, text] of changedFiles) write(file, text);
  report.changes.push(...planned.map(({ sha, file, fromSha, ...c }) => c));
  commitAll(`Update ${planned.length} action pin${planned.length > 1 ? 's' : ''} of the build inside their majors`,
    planned.map((c) => `- ${c.name} ${c.from} -> ${c.to} (${c.sha}, published ${c.time.slice(0, 10)})`).join('\n'), planned.map(lib.trailer));
}

// images in scripts/*.mjs|*.sh
function scriptFiles() {
  return run('git', ['ls-files', 'scripts']).split('\n').filter((f) => /^scripts\/[^/]+\.(mjs|sh)$/.test(f));
}

function replaceImagesInScripts(image, from, to) {
  for (const f of scriptFiles()) {
    const text = read(f);
    const next = lib.replaceImage(text, image, from, to);
    if (next !== text) write(f, next);
  }
}

// 5. the Node line (node-version in .github/actions and node:N in scripts)
async function nodeLine() {
  const actionFile = '.github/actions/fork-build/action.yml';
  const m = /node-version:\s*(\d{2,3})\s*$/m.exec(read(actionFile));
  if (!m || held('node', 'node')?.ceiling === '-') return;
  const current = Number(m[1]);
  const lines = lib.nodeLtsLines(await sources.nodeIndex());
  const pick = lib.choose(`${current}.0.0`, lines, cutoff, lib.holdOf(holds, 'node', 'node')?.ceiling);
  if (!pick.major || !majorsOn) return;
  const next = lib.parseVersion(pick.major.version).major;
  if (!(await sources.imageTag('node', String(next)))) {
    report.left.push({ eco: 'node', name: 'node', from: String(current), to: String(next), reason: `the image node:${next} does not exist yet` });
    return;
  }
  majors.push({ eco: 'node', label: `the Node line to ${next}`, check: 'none',
    members: [{ eco: 'node', name: 'node', from: String(current), to: String(next), time: pick.major.time }],
    apply() {
      write(actionFile, read(actionFile).replace(/(node-version:\s*)\d{2,3}(\s*)$/m, `$1${next}$2`));
      replaceImagesInScripts('node', String(current), String(next));
      return null;
    } });
}

// what is in use now, for a major's "from"
function currentVersion(x) {
  if (x.eco === 'go') return lib.parseGoMod(read('go.mod')).requires.find((r) => r.path === x.name)?.version;
  if (x.eco === 'toolchain') return lib.parseGoMod(read('go.mod')).go;
  if (x.eco === 'npm') {
    const lock = JSON.parse(read(join(x.dir || '', 'package-lock.json')));
    return lock.packages?.[`node_modules/${x.name}`]?.version;
  }
  return null;
}

// 6. majors, one at a time
async function tryMajors() {
  let tried = 0;
  for (const m of majors) {
    const key = m.members.map((x) => `${x.eco} ${x.name} ${x.to}`);
    if (key.every((k) => triedBefore.has(k))) {
      for (const x of m.members) report.left.push({ ...x, reason: 'failed at this version last time: tried again when a newer one appears' });
      continue;
    }
    if (tried >= maxMajors) {
      for (const x of m.members) report.left.push({ ...x, reason: `more than ${maxMajors} majors this week: next week` });
      continue;
    }
    tried++;
    // the minors of this run may have moved a member since it was planned
    for (const x of m.members) x.from = currentVersion(x) || x.from;
    let problem = null;
    try { problem = m.apply(); } catch (err) { problem = lib.safeText(err.message.split('\n').find((l) => /ERESOLVE|notarget|error|failed/.test(l)) || err.message, 240); }
    let failure = null;
    if (!problem) {
      failure = quickCheck(m.check);
      if (failure) problem = `quick checks: ${failure.split('\n')[0]}`;
    }
    if (problem) {
      undo();
      for (const x of m.members) report.tried.push({ eco: x.eco, name: x.name, from: x.from, to: x.to, reason: problem, failure });
      continue;
    }
    const changes = m.members.map((x) => ({ eco: x.eco, name: x.name, from: x.from, to: x.to, kind: 'major', time: x.time }));
    report.changes.push(...changes);
    const sha = commitAll(`Update ${m.label} (major)`,
      changes.map((c) => `- ${c.name} ${c.from} -> ${c.to} (published ${c.time.slice(0, 10)})`).join('\n') +
      (m.check === 'none' ? '\n\nNot covered by the quick checks: only the full checks exercise it.' : '\n\nThe quick checks passed with it.'),
      changes.map(lib.trailer));
    if (sha) report.commits[report.commits.length - 1].major = true;
  }
}

// 7. the record of the run
function record() {
  const trailers = [
    `Fork-Dependency-Run: ${today} cutoff ${cutoffIso.slice(0, 10)}`,
    ...report.tried.map((t) => lib.otherTrailer('Tried', t)),
    ...report.left.map((t) => lib.otherTrailer('Left', t)),
  ];
  const summary = `${report.changes.length} update${report.changes.length === 1 ? '' : 's'} (${report.changes.filter((c) => c.kind === 'major').length} major), ` +
    `${report.tried.length} tried and failed, ${report.left.length} left for later or a session.`;
  commitAll(`Record the automatic dependency update of ${today}`, `${summary}\n\n${report.notes.join('\n')}`, trailers, true);
}

// ---------------------------------------------------------------- main

async function main() {
  if (!existsSync(join(ROOT, 'go.mod')) || !existsSync(join(ROOT, 'package.json'))) {
    console.error('Run this from the root of the repository.'); process.exit(2);
  }
  if (statSync(join(ROOT, '.git')).isFile() && env.FORK_DEPS_ALLOW_WORKTREE !== '1') {
    console.error('This is a git worktree: run the update in a clone of its own (the quick checks replace node_modules).'); process.exit(2);
  }
  if (git('status', '--porcelain').trim()) { console.error('The tree is not clean.'); process.exit(2); }
  git('config', 'user.name', 'github-actions[bot]');
  git('config', 'user.email', '41898282+github-actions[bot]@users.noreply.github.com');
  const base = git('rev-parse', 'HEAD').trim();
  report.base = base;
  console.log(`Dependencies published before ${cutoffIso} (${cooldown} days before ${today}), on ${base}`);

  const steps = [['toolchain', goToolchain], ['go', goModules], ['npm', () => npmFolder('')],
    ['npm-smoke', () => npmFolder('scripts/fork-ci-smoke')], ['actions', actions], ['node', nodeLine], ['majors', tryMajors]];
  for (const [name, step] of steps) {
    if (name === 'majors' && !majorsOn) continue;
    try {
      await step();
      console.log(`- ${name}: done`);
    } catch (err) {
      undo();
      if (err.lookup) {
        report.notes.push(`${name}: skipped this week, a registry could not be read: ${lib.safeText(err.message, 200)}`);
        console.error(`- ${name}: skipped (${err.message})`);
        continue;
      }
      report.failedStep = 'update';
      report.failure = lib.safeText(`${name}: ${err.message}`, 1500);
      console.error(`- ${name}: ${err.message}`);
      break;
    }
  }
  if (!report.failedStep) {
    if (report.changes.length || report.tried.length) record();
    if (advisoryCmd && report.changes.length) {
      const [cmd, ...args] = advisoryCmd.split(/\s+/);
      try { console.log(run(cmd, args, { timeout: 30 * 60 * 1000, untrusted: true }).split('\n').slice(-20).join('\n')); }
      catch (err) {
        report.failedStep = 'advisories';
        report.failure = lib.safeText(err.output || err.message, 1500);
      }
    }
  }
  report.head = git('rev-parse', 'HEAD').trim();
  report.changed = report.head !== base;
  writeFileSync(outPath, JSON.stringify(report, null, 2) + '\n');
  if (recording) writeFileSync(env.FORK_DEPS_RECORD, JSON.stringify(recording, null, 1) + '\n');
  console.log(`${report.changes.length} updated, ${report.tried.length} tried and failed, ${report.left.length} left; report in ${outPath}`);
  process.exit(report.failedStep ? 1 : 0);
}

main().catch((err) => { console.error(err.stack || err.message); process.exit(2); });
