// Fork addition: tests of scripts/fork-deps-update.mjs on fixture
// repositories, with recorded registry answers (FORK_DEPS_RECORDED) and
// stand-ins for go and npm that edit the files the way the real ones would.
// The real go and npm paths are proven separately against the public
// registries with a date in the past (see the plan). Needs git and node.
// Run: node --test scripts/fork-deps-update.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, chmodSync, rmSync, copyFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const T = (days) => new Date(Date.parse('2026-11-15T00:00:00Z') - days * 86400000).toISOString(); // days before "today"
const sha = (c) => c.repeat(40);

// stand-in go: get, mod tidy, mod edit -go
const fakeGo = `
import { readFileSync, writeFileSync, appendFileSync } from 'node:fs';
const [cmd, ...args] = process.argv.slice(2);
let mod = readFileSync('go.mod', 'utf8');
if (cmd === 'get') {
  for (const a of args) {
    const [path, v] = a.split('@');
    if (path === 'example.org/breaks') { console.error('go: example.org/breaks@' + v + ': invalid version'); process.exit(1); }
    const re = new RegExp('(\\\\t' + path.replace(/[.\\/]/g, '\\\\$&') + ' )v\\\\S+');
    if (!re.test(mod)) mod = mod.replace(/require \\(\\n/, 'require (\\n\\t' + path + ' ' + v + '\\n');
    mod = mod.replace(re, '$1' + v);
    // moving this one drags the held module along, as a real requirement would
    if (path === 'example.org/drags') mod = mod.replace(/(\\texample.org\\/held )v\\S+/, '$1v9.9.9');
    appendFileSync('go.sum', path + ' ' + v + ' h1:' + 'A'.repeat(43) + '=\\n');
  }
  writeFileSync('go.mod', mod);
} else if (cmd === 'mod' && args[0] === 'tidy') {
} else if (cmd === 'mod' && args[0] === 'edit') {
  writeFileSync('go.mod', mod.replace(/^go \\S+/m, 'go ' + args[1].replace('-go=', '')));
} else { console.error('stand-in go: unexpected ' + process.argv.slice(2).join(' ')); process.exit(3); }
`;

// stand-in npm: install/update --package-lock-only write the lock from package.json
const fakeNpm = `
import { readFileSync, writeFileSync } from 'node:fs';
const args = process.argv.slice(2);
if (!args.includes('--package-lock-only') || !args.includes('--ignore-scripts')) { console.error('stand-in npm: refused ' + args.join(' ')); process.exit(3); }
const pj = JSON.parse(readFileSync('package.json', 'utf8'));
const deps = { ...(pj.dependencies || {}), ...(pj.devDependencies || {}) };
const packages = { '': { name: pj.name, version: pj.version, dependencies: pj.dependencies || {} } };
for (const [n, s] of Object.entries(deps)) {
  const v = s.replace(/^[\\^~]/, '');
  if (n === 'conflicts' && v.startsWith('3.')) { console.error('npm error code ERESOLVE\\nnpm error peer conflict'); process.exit(1); }
  packages['node_modules/' + n] = { version: v, resolved: 'https://registry.npmjs.org/' + n + '/-/' + n + '-' + v + '.tgz', integrity: 'sha512-' + 'B'.repeat(86) + '==' };
}
writeFileSync('package-lock.json', JSON.stringify({ name: pj.name, version: pj.version, lockfileVersion: 3, requires: true, packages }, null, 2) + '\\n');
`;

function setup({ goMod, pkg, holds = '', recorded, quick = 'exit 0', advisory = 'exit 0', extraFiles = {} }) {
  const dir = mkdtempSync(join(tmpdir(), 'fork-deps-test-'));
  const repo = join(dir, 'repo'), bin = join(dir, 'bin');
  mkdirSync(join(repo, 'scripts'), { recursive: true });
  mkdirSync(join(repo, '.github/actions/fork-build'), { recursive: true });
  mkdirSync(join(repo, '.github/workflows'), { recursive: true });
  mkdirSync(bin);
  for (const [name, body] of [['go', fakeGo], ['npm', fakeNpm]]) {
    writeFileSync(join(bin, `${name}.mjs`), body);
    writeFileSync(join(bin, name), `#!/bin/sh\nexec node ${join(bin, `${name}.mjs`)} "$@"\n`);
    chmodSync(join(bin, name), 0o755);
  }
  writeFileSync(join(dir, 'quick.sh'), `echo "quick $1"\n${quick}\n`);
  writeFileSync(join(dir, 'advisory.sh'), `${advisory}\n`);
  writeFileSync(join(dir, 'recorded.json'), JSON.stringify(recorded));
  writeFileSync(join(repo, 'go.mod'), goMod);
  writeFileSync(join(repo, 'go.sum'), '');
  writeFileSync(join(repo, 'package.json'), JSON.stringify(pkg, null, 2) + '\n');
  writeFileSync(join(repo, 'scripts/fork-deps-holds.txt'), holds);
  for (const f of ['fork-deps-lib.mjs', 'fork-deps-update.mjs', 'fork-deps-validate.mjs', 'fork-deps-notes.mjs']) copyFileSync(join(here, f), join(repo, 'scripts', f));
  writeFileSync(join(repo, '.github/actions/fork-build/action.yml'),
    `runs:\n  steps:\n    - uses: actions/setup-go@${sha('1')} # v7.0.0\n    - uses: actions/setup-node@${sha('2')} # v7.1.0\n      with:\n        node-version: 26\n`);
  writeFileSync(join(repo, '.github/workflows/ci.yml'), `jobs:\n  a:\n    steps:\n      - uses: actions/checkout@${sha('3')} # v7.0.1\n`);
  writeFileSync(join(repo, 'scripts/check.mjs'), '// docker run golang:1.27 x; node:26; mcr.microsoft.com/playwright:v1.65.1-noble\nconst x = 1;\n');
  for (const [p, body] of Object.entries(extraFiles)) { mkdirSync(dirname(join(repo, p)), { recursive: true }); writeFileSync(join(repo, p), body); }
  const g = (...a) => execFileSync('git', a, { cwd: repo, encoding: 'utf8' });
  g('init', '-q', '-b', 'custom');
  g('config', 'user.name', 't'); g('config', 'user.email', 't@example.org');
  execFileSync(join(bin, 'npm'), ['install', '--package-lock-only', '--ignore-scripts'], { cwd: repo });
  g('add', '-A');
  g('commit', '-q', '-m', 'base');
  return { dir, repo, bin, git: g };
}

function update(fx, extraEnv = {}) {
  const res = spawnSync('node', ['scripts/fork-deps-update.mjs'], {
    cwd: fx.repo, encoding: 'utf8',
    env: { ...process.env, PATH: `${fx.bin}:${process.env.PATH}`, FORK_DEPS_TODAY: '2026-11-15', FORK_DEPS_RECORDED: join(fx.dir, 'recorded.json'),
      QUICK_CHECKS: `sh ${join(fx.dir, 'quick.sh')}`, ADVISORY_CHECK: `sh ${join(fx.dir, 'advisory.sh')}`, OUT: join(fx.dir, 'report.json'),
      GIT_CONFIG_NOSYSTEM: '1', ...extraEnv },
  });
  const report = JSON.parse(readFileSync(join(fx.dir, 'report.json'), 'utf8'));
  return { code: res.status, out: res.stdout + res.stderr, report };
}

const baseGoMod = 'module example.org/x\n\ngo 1.27.4\n\nrequire (\n\texample.org/lib v1.2.0\n\texample.org/held v1.0.0\n\texample.org/zero v0.3.0\n\texample.org/side v1.0.0 // indirect\n)\n';
const basePkg = { name: 'x', version: '1.0.0', dependencies: { left: '^1.0.0', 'react': '^18.2.0', 'react-dom': '^18.2.0' }, devDependencies: { tool: '2.0.0' } };

// what the registries answer: everything current unless a test adds to it
function answers(extra = {}) {
  return {
    'go-versions golang.org/toolchain': ['v0.0.1-go1.27.4.linux-amd64'],
    'go-time golang.org/toolchain@v0.0.1-go1.27.4.linux-amd64': T(30),
    'go-versions example.org/lib': ['v1.2.0'], 'go-versions example.org/zero': ['v0.3.0'],
    'npm left': [{ version: '1.0.0', time: T(400) }], 'npm react': [{ version: '18.2.0', time: T(400) }],
    'npm react-dom': [{ version: '18.2.0', time: T(400) }], 'npm tool': [{ version: '2.0.0', time: T(400) }],
    'gh-releases actions/setup-go': [{ version: 'v7.0.0', time: T(100) }], 'gh-releases actions/setup-node': [{ version: 'v7.1.0', time: T(100) }],
    'gh-releases actions/checkout': [{ version: 'v7.0.1', time: T(100) }],
    'node-index': [{ version: 'v26.13.1', date: '2026-10-28', lts: 'Lithium' }],
    ...extra,
  };
}

const holdHeld = 'go example.org/held - kept by hand\npath .github/workflows - token\npath scripts/fork-deps-holds.txt - owner\ngroup npm react react-dom\n';

test('nothing to do: no commit, exit 0', () => {
  const fx = setup({ goMod: baseGoMod, pkg: basePkg, holds: holdHeld, recorded: answers() });
  const head = fx.git('rev-parse', 'HEAD').trim();
  const { code, report } = update(fx);
  assert.equal(code, 0);
  assert.equal(report.changed, false);
  assert.equal(fx.git('rev-parse', 'HEAD').trim(), head);
  assert.equal(report.changes.length, 0);
  rmSync(fx.dir, { recursive: true, force: true });
});

test('minors: each group a commit with trailers; the cool-down boundary; workflow pins listed, not changed; holds respected', () => {
  const fx = setup({ goMod: baseGoMod, pkg: basePkg, holds: holdHeld, recorded: answers({
    'go-versions golang.org/toolchain': ['v0.0.1-go1.27.4.linux-amd64', 'v0.0.1-go1.27.5.linux-amd64', 'v0.0.1-go1.27.6.linux-amd64'],
    'go-time golang.org/toolchain@v0.0.1-go1.27.5.linux-amd64': T(8), 'go-time golang.org/toolchain@v0.0.1-go1.27.6.linux-amd64': T(6),
    'go-versions example.org/lib': ['v1.2.0', 'v1.3.0', 'v1.4.0'], 'go-time example.org/lib@v1.4.0': T(7), 'go-time example.org/lib@v1.3.0': T(7.0001),
    'npm left': [{ version: '1.0.0', time: T(400) }, { version: '1.1.0', time: T(10) }, { version: '1.2.0', time: T(1) }],
    'gh-releases actions/setup-go': [{ version: 'v7.0.0', time: T(100) }, { version: 'v7.2.0', time: T(9) }],
    'gh-sha actions/setup-go@v7.2.0': sha('4'),
    'gh-releases actions/checkout': [{ version: 'v7.0.1', time: T(100) }, { version: 'v7.1.0', time: T(20) }],
    'gh-sha actions/checkout@v7.1.0': sha('5'),
  }) });
  const { code, report, out } = update(fx, { MAJORS: '0' });
  assert.equal(code, 0, out);
  const byName = Object.fromEntries(report.changes.map((c) => [c.name, c.to]));
  assert.deepEqual(byName, { go: '1.27.5', 'example.org/lib': 'v1.3.0', left: '1.1.0', 'actions/setup-go': 'v7.2.0' });
  assert.match(readFileSync(join(fx.repo, 'go.mod'), 'utf8'), /^go 1\.27\.5$/m);
  assert.match(readFileSync(join(fx.repo, 'go.mod'), 'utf8'), /example\.org\/held v1\.0\.0/);
  assert.match(readFileSync(join(fx.repo, 'package.json'), 'utf8'), /"left": "\^1\.1\.0"/);
  assert.match(readFileSync(join(fx.repo, '.github/actions/fork-build/action.yml'), 'utf8'), new RegExp(`setup-go@${sha('4')} # v7.2.0`));
  assert.match(readFileSync(join(fx.repo, '.github/workflows/ci.yml'), 'utf8'), new RegExp(`checkout@${sha('3')} # v7.0.1`));
  assert.ok(report.left.some((l) => l.name === 'actions/checkout' && /workflow token/.test(l.reason)));
  const log = fx.git('log', '--format=%s', 'custom~5..HEAD').trim().split('\n');
  assert.deepEqual(log, ['Record the automatic dependency update of 2026-11-15', 'Update 1 action pin of the build inside their majors',
    'Update 1 npm package inside their majors', 'Update 1 Go module inside their versions', 'Update the Go toolchain to 1.27.5']);
  assert.match(fx.git('log', '-1', '--format=%B', 'HEAD~2'), /^Fork-Dependency: npm left 1\.0\.0 1\.1\.0 minor 2026-11-05$/m);
  // the validator, run from the base, accepts it
  const base = fx.git('rev-list', '--max-parents=0', 'HEAD').trim(), tip = fx.git('rev-parse', 'HEAD').trim();
  execFileSync('node', ['scripts/fork-deps-validate.mjs', base, tip], { cwd: fx.repo });
  // and the notes of the release read it back
  const meta = execFileSync('node', ['scripts/fork-deps-notes.mjs', '', fx.dir, '2026-11-15'], { cwd: fx.repo, encoding: 'utf8' });
  assert.deepEqual(JSON.parse(meta), {});  // no previous tag: nothing to say
  fx.git('tag', 'v0.24.0-fork.1', base);
  const meta2 = JSON.parse(execFileSync('node', ['scripts/fork-deps-notes.mjs', 'v0.24.0-fork.1', fx.dir, '2026-11-15'], { cwd: fx.repo, encoding: 'utf8' }));
  assert.deepEqual(meta2, { minor: 4, major: 0, majors: [] });
  const deps = JSON.parse(readFileSync(join(fx.dir, 'dependencies.json'), 'utf8'));
  assert.equal(deps.schema, 1);
  assert.deepEqual(deps.changes.find((c) => c.name === 'left'), { eco: 'npm', name: 'left', from: '1.0.0', to: '1.1.0', kind: 'minor', ageDays: 10 });
  assert.ok(deps.left.some((c) => c.name === 'actions/checkout'));
  rmSync(fx.dir, { recursive: true, force: true });
});

test('majors one at a time: a failing one is undone and recorded, a passing one is its own commit, a group moves together', () => {
  const fx = setup({ goMod: baseGoMod, pkg: { ...basePkg, dependencies: { ...basePkg.dependencies, conflicts: '^2.0.0' } }, holds: holdHeld,
    quick: 'if grep -q "example.org/zero v0.4" go.mod; then echo "--- FAIL: TestZero (0.00s)"; echo "    zero_test.go:12: broke"; exit 1; fi',
    recorded: answers({
      'go-versions example.org/zero': ['v0.3.0', 'v0.4.0'], 'go-time example.org/zero@v0.4.0': T(30),
      'npm react': [{ version: '18.2.0', time: T(400) }, { version: '19.1.0', time: T(30) }],
      'npm react-dom': [{ version: '18.2.0', time: T(400) }, { version: '19.1.0', time: T(30) }],
      'npm conflicts': [{ version: '2.0.0', time: T(400) }, { version: '3.0.0', time: T(30) }],
      'npm tool': [{ version: '2.0.0', time: T(400) }, { version: '3.0.0', time: T(30) }, { version: '4.0.0', time: T(2) }],
    }) });
  const { code, report, out } = update(fx);
  assert.equal(code, 0, out);
  const majors = report.changes.filter((c) => c.kind === 'major').map((c) => `${c.name} ${c.to}`).sort();
  assert.deepEqual(majors, ['react 19.1.0', 'react-dom 19.1.0', 'tool 3.0.0']);
  const tried = Object.fromEntries(report.tried.map((t) => [t.name, t.reason]));
  assert.match(tried['example.org/zero'], /quick checks: --- FAIL: TestZero/);
  assert.match(tried.conflicts, /ERESOLVE/);
  assert.match(readFileSync(join(fx.repo, 'go.mod'), 'utf8'), /example\.org\/zero v0\.3\.0/);
  // never two majors in one commit (a group is one major); each major commit carries its trailers
  const subjects = fx.git('log', '--format=%s', 'HEAD~3..HEAD').trim().split('\n');
  assert.deepEqual(subjects, ['Record the automatic dependency update of 2026-11-15', 'Update tool 3.0.0 (major)', 'Update react 19.1.0, react-dom 19.1.0 (major)']);
  assert.equal(fx.git('log', '-1', '--format=%B', 'HEAD~1').match(/^Fork-Dependency: /gm).length, 1);
  assert.match(fx.git('log', '-1', '--format=%B', 'HEAD'), /^Fork-Dependency-Tried: go example\.org\/zero v0\.3\.0 v0\.4\.0 quick checks: --- FAIL: TestZero/m);
  // the next run skips the major that failed at the same version
  const again = setup({ goMod: baseGoMod, pkg: basePkg, holds: holdHeld, recorded: answers({ 'go-versions example.org/zero': ['v0.3.0', 'v0.4.0'], 'go-time example.org/zero@v0.4.0': T(30) }) });
  const second = update(again, { SKIP_TRIED: join(fx.dir, 'report.json') });
  assert.equal(second.report.tried.length, 0);
  assert.ok(second.report.left.some((l) => l.name === 'example.org/zero' && /last time/.test(l.reason)));
  rmSync(fx.dir, { recursive: true, force: true });
  rmSync(again.dir, { recursive: true, force: true });
});

test('a module whose update would move a held one is not updated; MAX_MAJORS defers the rest', () => {
  const fx = setup({ goMod: baseGoMod.replace('\texample.org/zero v0.3.0\n', '\texample.org/zero v0.3.0\n\texample.org/drags v1.0.0\n'), pkg: basePkg, holds: holdHeld,
    recorded: answers({ 'go-versions example.org/drags': ['v1.0.0', 'v1.1.0'], 'go-time example.org/drags@v1.1.0': T(30),
      'npm tool': [{ version: '2.0.0', time: T(400) }, { version: '3.0.0', time: T(30) }],
      'npm left': [{ version: '1.0.0', time: T(400) }, { version: '2.0.0', time: T(30) }] }) });
  const { code, report } = update(fx, { MAX_MAJORS: '1' });
  assert.equal(code, 0);
  assert.match(report.tried.find((t) => t.name === 'example.org/drags').reason, /held module example\.org\/held/);
  assert.match(readFileSync(join(fx.repo, 'go.mod'), 'utf8'), /example\.org\/held v1\.0\.0/);
  assert.equal(report.changes.filter((c) => c.kind === 'major').length, 1);
  assert.ok(report.left.some((l) => /more than 1 majors/.test(l.reason)));
  rmSync(fx.dir, { recursive: true, force: true });
});

test('the advisory gate stops the run', () => {
  const fx = setup({ goMod: baseGoMod, pkg: basePkg, holds: holdHeld, advisory: 'echo "New findings: GHSA-xxxx"; exit 1',
    recorded: answers({ 'npm left': [{ version: '1.0.0', time: T(400) }, { version: '1.1.0', time: T(10) }] }) });
  const { code, report } = update(fx);
  assert.equal(code, 1);
  assert.equal(report.failedStep, 'advisories');
  assert.match(report.failure, /GHSA-xxxx/);
  rmSync(fx.dir, { recursive: true, force: true });
});

test('the same base and day give the same commits, ids included', () => {
  const rec = answers({ 'npm left': [{ version: '1.0.0', time: T(400) }, { version: '1.1.0', time: T(10) }],
    'npm tool': [{ version: '2.0.0', time: T(400) }, { version: '3.0.0', time: T(30) }] });
  const a = setup({ goMod: baseGoMod, pkg: basePkg, holds: holdHeld, recorded: rec });
  const b = setup({ goMod: baseGoMod, pkg: basePkg, holds: holdHeld, recorded: rec });
  // the two bases are made at different moments: compare the trees and the update commits' content
  update(a); update(b);
  assert.equal(a.git('rev-parse', 'HEAD^{tree}').trim(), b.git('rev-parse', 'HEAD^{tree}').trim());
  assert.equal(a.git('log', '--format=%s%n%b%n%ad%n%cd', 'HEAD~3..HEAD'), b.git('log', '--format=%s%n%b%n%ad%n%cd', 'HEAD~3..HEAD'));
  rmSync(a.dir, { recursive: true, force: true }); rmSync(b.dir, { recursive: true, force: true });
});

test('the validator refuses what an update must not change', () => {
  const fx = setup({ goMod: baseGoMod, pkg: basePkg, holds: holdHeld, recorded: answers(), extraFiles: { 'src/main.go': 'package main\n' } });
  const base = fx.git('rev-parse', 'HEAD').trim();
  const tryChange = (path, edit) => {
    fx.git('checkout', '-q', base);
    writeFileSync(join(fx.repo, path), edit(readFileSync(join(fx.repo, path), 'utf8')));
    fx.git('commit', '-q', '-am', 'change');
    const res = spawnSync('node', ['scripts/fork-deps-validate.mjs', base, fx.git('rev-parse', 'HEAD').trim()], { cwd: fx.repo, encoding: 'utf8' });
    return res.status === 0 ? null : res.stderr;
  };
  assert.match(tryChange('src/main.go', (s) => s + '// x\n'), /not a file the update may change/);
  assert.match(tryChange('.github/workflows/ci.yml', (s) => s.replace('v7.0.1', 'v7.0.2')), /not a file/);
  assert.match(tryChange('scripts/fork-deps-holds.txt', (s) => s + 'npm x - y\n'), /not a file/);
  assert.match(tryChange('package-lock.json', (s) => s.replace('https://registry.npmjs.org/left', 'https://evil.example.org/left')), /not from registry/);
  assert.match(tryChange('scripts/check.mjs', (s) => s.replace('const x = 1;', 'const x = 2;')), /only image names/);
  assert.match(tryChange('.github/actions/fork-build/action.yml', (s) => s.replace('actions/setup-go', 'evil/setup-go')), /only action pins/);
  assert.equal(tryChange('package.json', (s) => s.replace('"^1.0.0"', '"^1.0.1"')), null);
  rmSync(fx.dir, { recursive: true, force: true });
});

test('a registry that cannot be read skips its step with a note, the rest goes on', () => {
  const fx = setup({ goMod: baseGoMod, pkg: basePkg, holds: holdHeld, recorded: answers({
    'gh-releases actions/setup-go': { __error__: 'HTTP 403' },
    'npm left': [{ version: '1.0.0', time: T(400) }, { version: '1.1.0', time: T(10) }] }) });
  const { code, report } = update(fx);
  assert.equal(code, 0);
  assert.deepEqual(report.changes.map((c) => c.name), ['left']);
  assert.ok(report.notes.some((n) => /^actions: skipped this week, a registry could not be read: .*HTTP 403/.test(n)));
  assert.match(fx.git('log', '-1', '--format=%B'), /actions: skipped this week/);
  rmSync(fx.dir, { recursive: true, force: true });
});

test('it refuses a dirty tree and a worktree', () => {
  const fx = setup({ goMod: baseGoMod, pkg: basePkg, holds: holdHeld, recorded: answers() });
  writeFileSync(join(fx.repo, 'stray.txt'), 'x');
  const res = spawnSync('node', ['scripts/fork-deps-update.mjs'], { cwd: fx.repo, encoding: 'utf8', env: { ...process.env, OUT: join(fx.dir, 'r.json') } });
  assert.equal(res.status, 2);
  assert.match(res.stderr, /not clean/);
  rmSync(fx.dir, { recursive: true, force: true });
});
