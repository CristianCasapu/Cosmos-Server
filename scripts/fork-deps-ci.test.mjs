// Fork addition: tests of scripts/fork-deps-ci.sh, the steps of
// fork-deps-update.yml, against a bare repository standing in for GitHub and
// a stand-in gh that records what it is asked. Needs git, bash, jq, node.
// Run: node --test scripts/fork-deps-ci.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, chmodSync, rmSync, copyFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));

// stand-in gh: answers from FAKE_GH_* and appends every call to FAKE_GH_LOG
const fakeGh = `#!/bin/sh
printf '%s\\n' "$*" >> "$FAKE_GH_LOG"
case "$1 $2" in
  "run list") case "$*" in *"$FAKE_GH_BUSY"*) echo 1 ;; *) echo 0 ;; esac ;;
  "issue list") cat "$FAKE_GH_ISSUES" 2>/dev/null || true ;;
  "issue create"|"issue comment")
    while [ $# -gt 0 ]; do [ "$1" = "--body-file" ] && cat "$2" >> "$FAKE_GH_LOG"; shift; done
    case "$*" in *) ;; esac
    [ -n "$FAKE_GH_CREATED" ] && echo "https://github.com/owner/repo/issues/42" ;;
  "issue close") ;;
  "release view") echo false ;;
  *) echo "stand-in gh: $*" >&2; exit 1 ;;
esac
`;

function setup() {
  const dir = mkdtempSync(join(tmpdir(), 'fork-deps-ci-'));
  const remote = join(dir, 'remote.git'), work = join(dir, 'work'), bin = join(dir, 'bin');
  mkdirSync(bin);
  writeFileSync(join(bin, 'gh'), fakeGh); chmodSync(join(bin, 'gh'), 0o755);
  execFileSync('git', ['init', '-q', '--bare', '-b', 'custom', remote]);
  mkdirSync(join(work, 'scripts'), { recursive: true });
  const g = (...a) => execFileSync('git', a, { cwd: work, encoding: 'utf8' }).trim();
  g('init', '-q', '-b', 'custom'); g('config', 'user.name', 't'); g('config', 'user.email', 't@example.org');
  for (const f of ['fork-deps-ci.sh', 'fork-deps-lib.mjs', 'fork-deps-validate.mjs', 'fork-deps-notes.mjs', 'fork-deps-holds.txt']) copyFileSync(join(here, f), join(work, 'scripts', f));
  writeFileSync(join(work, 'package.json'), '{\n  "name": "x",\n  "dependencies": {\n    "a": "^1.0.0",\n    "b": "^1.0.0"\n  }\n}\n');
  writeFileSync(join(work, 'go.mod'), 'module x\n\ngo 1.27.4\n');
  mkdirSync(join(work, 'src'));
  writeFileSync(join(work, 'src/main.go'), 'package main\n');
  g('add', '-A'); g('commit', '-q', '-m', 'base');
  g('remote', 'add', 'origin', remote);
  g('push', '-q', 'origin', 'custom', 'custom:production');
  const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, GITHUB_REPOSITORY: 'owner/repo', GITHUB_RUN_ID: '1234',
    FAKE_GH_LOG: join(dir, 'gh.log'), FAKE_GH_CREATED: '1', FAKE_GH_ISSUES: join(dir, 'issues.txt'), FAKE_GH_BUSY: 'none-busy' };
  const ci = (args, extra = {}) => spawnSync('bash', ['scripts/fork-deps-ci.sh', ...args], { cwd: work, encoding: 'utf8', env: { ...env, ...extra } });
  return { dir, work, remote, g, ci, env };
}

// commits as the update makes them: minors, then majors one per commit, then the record
function updateCommits(fx, majors = 2) {
  const { g, work } = fx;
  const pj = (a, b) => writeFileSync(join(work, 'package.json'), `{\n  "name": "x",\n  "dependencies": {\n    "a": "${a}",\n    "b": "${b}"\n  }\n}\n`);
  pj('^1.1.0', '^1.0.0'); g('commit', '-q', '-am', 'Update 1 npm package\n\nFork-Dependency: npm a 1.0.0 1.1.0 minor 2026-11-01');
  if (majors >= 1) { pj('^2.0.0', '^1.0.0'); g('commit', '-q', '-am', 'Update a 2.0.0 (major)\n\nFork-Dependency: npm a 1.1.0 2.0.0 major 2026-10-01'); }
  if (majors >= 2) { pj('^2.0.0', '^3.0.0'); g('commit', '-q', '-am', 'Update b 3.0.0 (major)\n\nFork-Dependency: npm b 1.0.0 3.0.0 major 2026-10-02'); }
  g('commit', '-q', '--allow-empty', '-m', 'Record the automatic dependency update of 2026-11-15\n\nx\n\nFork-Dependency-Run: 2026-11-15 cutoff 2026-11-08\nFork-Dependency-Left: action actions/checkout v7.0.1 v7.1.0 workflow file');
  return g('rev-parse', 'HEAD');
}

test('guard: the kill switch pauses, a sync or rollback under way holds the update back', () => {
  const fx = setup();
  assert.equal(fx.ci(['guard'], { FORK_DEPS_UPDATE: 'off' }).stdout.trim(), 'paused');
  assert.equal(fx.ci(['guard'], { FAKE_GH_BUSY: 'fork-sync.yml' }).stdout.trim(), 'busy fork-sync.yml');
  assert.equal(fx.ci(['guard'], { FAKE_GH_BUSY: 'fork-rollback.yml' }).stdout.trim(), 'busy fork-rollback.yml');
  assert.equal(fx.ci(['guard']).stdout.trim(), 'go');
  rmSync(fx.dir, { recursive: true, force: true });
});

test('receive: the validated bundle becomes deps/<day>; a bundle that touches code is refused and nothing is pushed', () => {
  const fx = setup();
  const base = fx.g('rev-parse', 'HEAD');
  const tip = updateCommits(fx, 1);
  fx.g('bundle', 'create', '-q', join(fx.dir, 'good.bundle'), `${base}..HEAD`);
  fx.g('checkout', '-q', '--detach', base);
  let res = fx.ci(['receive', join(fx.dir, 'good.bundle'), base, '2026-11-15']);
  assert.equal(res.status, 0, res.stderr);
  assert.equal(res.stdout.trim(), tip);
  assert.equal(execFileSync('git', ['--git-dir', fx.remote, 'rev-parse', 'deps/2026-11-15'], { encoding: 'utf8' }).trim(), tip);
  // a bundle that changes a source file
  writeFileSync(join(fx.work, 'src/main.go'), 'package main\n// injected\n');
  fx.g('commit', '-q', '-am', 'Update 1 npm package');
  fx.g('bundle', 'create', '-q', join(fx.dir, 'evil.bundle'), `${base}..HEAD`);
  fx.g('checkout', '-q', '--detach', base);
  res = fx.ci(['receive', join(fx.dir, 'evil.bundle'), base, '2026-11-16']);
  assert.notEqual(res.status, 0);
  assert.match(res.stderr, /src\/main\.go: not a file the update may change/);
  assert.throws(() => execFileSync('git', ['--git-dir', fx.remote, 'rev-parse', '--verify', '-q', 'deps/2026-11-16'], { stdio: 'pipe' }));
  rmSync(fx.dir, { recursive: true, force: true });
});

test('promote: both branches move in one push; if custom moved or production is ahead, neither moves', () => {
  const fx = setup();
  const base = fx.g('rev-parse', 'HEAD');
  const tip = updateCommits(fx, 1);
  fx.g('push', '-q', 'origin', `${tip}:refs/heads/deps/2026-11-15`);
  const remoteRev = (b) => execFileSync('git', ['--git-dir', fx.remote, 'rev-parse', b], { encoding: 'utf8' }).trim();

  // a human pushed to custom meanwhile
  fx.g('checkout', '-q', '--detach', base);
  writeFileSync(join(fx.work, 'human.txt'), 'x'); fx.g('add', 'human.txt'); fx.g('commit', '-q', '-m', 'human');
  const human = fx.g('rev-parse', 'HEAD');
  fx.g('push', '-q', 'origin', `${human}:refs/heads/custom`);
  assert.equal(fx.ci(['promote', tip, base]).stdout.trim(), 'custom-moved');
  assert.equal(remoteRev('custom'), human);
  assert.equal(remoteRev('production'), base);

  // production ahead of custom (a sync)
  fx.g('push', '-q', '--force', 'origin', `${base}:refs/heads/custom`);
  fx.g('push', '-q', 'origin', `${human}:refs/heads/production`);
  assert.equal(fx.ci(['promote', tip, base]).stdout.trim(), 'production-ahead');
  assert.equal(remoteRev('custom'), base);

  // the normal case
  fx.g('push', '-q', '--force', 'origin', `${base}:refs/heads/production`);
  assert.equal(fx.ci(['promote', tip, base]).stdout.trim(), 'promoted');
  assert.equal(remoteRev('custom'), tip);
  assert.equal(remoteRev('production'), tip);
  rmSync(fx.dir, { recursive: true, force: true });
});

test('drop: the newest major goes first, the record says so, until no major is left', () => {
  const fx = setup();
  const base = fx.g('rev-parse', 'HEAD');
  const tip = updateCommits(fx, 2);
  assert.equal(fx.ci(['majors', tip, base]).stdout.trim(), '2');
  const first = fx.ci(['drop', tip, base, 'full checks failed in run 1234']);
  assert.equal(first.status, 0, first.stderr);
  const t1 = first.stdout.trim().split('\n').pop();
  assert.equal(fx.ci(['majors', t1, base]).stdout.trim(), '1');
  assert.match(fx.g('show', `${t1}:package.json`), /"b": "\^1\.0\.0"/);
  assert.match(fx.g('show', `${t1}:package.json`), /"a": "\^2\.0\.0"/);
  assert.match(fx.g('log', '-1', '--format=%B', t1), /^Fork-Dependency-Dropped: npm b 1\.0\.0 3\.0\.0 full checks failed in run 1234$/m);
  assert.match(fx.g('log', '-1', '--format=%B', t1), /^Fork-Dependency-Left: action actions\/checkout/m);
  const t2 = fx.ci(['drop', t1, base, 'full checks failed in run 1235']).stdout.trim().split('\n').pop();
  assert.equal(fx.ci(['majors', t2, base]).stdout.trim(), '0');
  assert.match(fx.g('show', `${t2}:package.json`), /"a": "\^1\.1\.0"/);
  const body = fx.g('log', '-1', '--format=%B', t2);
  assert.match(body, /Dropped: npm b .* run 1234/);
  assert.match(body, /Dropped: npm a 1\.1\.0 2\.0\.0 .* run 1235/);
  assert.notEqual(fx.ci(['drop', t2, base, 'again']).status, 0);
  rmSync(fx.dir, { recursive: true, force: true });
});

test('status: one file on its own branch, a commit per run, the list from the trailers, quietSince carried, text cleaned', () => {
  const fx = setup();
  const base = fx.g('rev-parse', 'HEAD');
  const tip = updateCommits(fx, 1);
  const read = () => JSON.parse(execFileSync('git', ['--git-dir', fx.remote, 'show', 'fork-deps-status:status.json'], { encoding: 'utf8' }));
  let res = fx.ci(['status', 'nothing']);
  assert.equal(res.status, 0, res.stderr);
  const s1 = read();
  assert.equal(s1.schema, 1); assert.equal(s1.outcome, 'nothing'); assert.equal(s1.run, 1234);
  assert.match(s1.quietSince, /^\d{4}-\d{2}-\d{2}$/);
  fx.ci(['status', 'nothing'], { GITHUB_RUN_ID: '1235' });
  assert.equal(read().quietSince, s1.quietSince);
  res = fx.ci(['status', 'updated'], { BASE: base, SHA: tip, RELEASE: '0.24.0-fork.31', GITHUB_RUN_ID: '1236' });
  assert.equal(res.status, 0, res.stderr);
  const s3 = read();
  assert.equal(s3.release, '0.24.0-fork.31');
  assert.equal(s3.quietSince, undefined);
  assert.deepEqual(s3.changes.map((c) => `${c.name} ${c.from} ${c.to} ${c.kind}`), ['a 1.0.0 2.0.0 major']);
  assert.equal(s3.left[0].name, 'actions/checkout');
  writeFileSync(join(fx.dir, 'failure.txt'), 'npm error \u001b[31mboom\u001b[0m @someone ```\nline two\n');
  fx.ci(['status', 'failed', 'checks', join(fx.dir, 'failure.txt')], { ISSUE: '42', RELEASE: 'not a version', GITHUB_RUN_ID: '1237' });
  const s4 = read();
  assert.equal(s4.outcome, 'failed'); assert.equal(s4.failedStep, 'checks'); assert.equal(s4.issue, 42);
  assert.equal(s4.release, undefined);
  assert.equal(s4.failure, 'npm error boom _someone ___\nline two');
  assert.doesNotMatch(s4.failure, /[@`\u001b]/);
  const log = execFileSync('git', ['--git-dir', fx.remote, 'log', '--format=%s', 'fork-deps-status'], { encoding: 'utf8' }).trim().split('\n');
  assert.equal(log.length, 4);
  assert.equal(execFileSync('git', ['--git-dir', fx.remote, 'ls-tree', '--name-only', 'fork-deps-status'], { encoding: 'utf8' }).trim(), 'status.json');
  assert.notEqual(fx.ci(['status', 'exploded']).status, 0);
  rmSync(fx.dir, { recursive: true, force: true });
});

test('issue: opened once, then commented; the text cannot mention anyone or break out of its block', () => {
  const fx = setup();
  writeFileSync(join(fx.dir, 'failure.txt'), 'FAIL @owner ```\n');
  let res = fx.ci(['issue', join(fx.dir, 'failure.txt')], { CANDIDATE: 'deps/2026-11-15' });
  assert.equal(res.status, 0, res.stderr);
  assert.equal(res.stdout.trim(), '42');
  writeFileSync(join(fx.dir, 'issues.txt'), '7\n');
  res = fx.ci(['issue', join(fx.dir, 'failure.txt')]);
  assert.equal(res.stdout.trim(), '7');
  const calls = readFileSync(join(fx.dir, 'gh.log'), 'utf8');
  assert.match(calls, /issue create --repo owner\/repo --title Automatic dependency update needs a session/);
  assert.match(calls, /issue comment 7 --repo owner\/repo/);
  assert.match(calls, /FAIL _owner ___/);
  assert.doesNotMatch(calls.split('What failed:')[1], /@owner|```\n```/);
  assert.match(calls, /Candidate branch: `deps\/2026-11-15`/);
  rmSync(fx.dir, { recursive: true, force: true });
});
