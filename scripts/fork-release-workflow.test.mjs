// Fork addition (plan 47): the release workflow signs only what the build
// made: publish (and e2e) take the build's artifact by its ID, and publish
// checks every file against the build's SHA-256s (scripts/fork-release-
// handover.sh) before anything is signed, in a step whose failure fails the
// job. Read from .github/workflows/fork-release.yml as text (the layout the
// workflow is written in: jobs at two spaces, steps at six).
// node --test scripts/fork-release-workflow.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const workflow = readFileSync(join(here, '..', '.github', 'workflows', 'fork-release.yml'), 'utf8');

// jobs: name -> text; steps of a job: list of texts
const jobs = (text) => {
  const lines = text.split('\n');
  const start = lines.findIndex((l) => l === 'jobs:');
  const out = {};
  let name = null;
  for (const line of lines.slice(start + 1)) {
    const m = line.match(/^ {2}([a-z0-9-]+):\s*$/);
    if (m) { name = m[1]; out[name] = ''; continue; }
    if (name) out[name] += line + '\n';
  }
  return out;
};
const steps = (job) => job.split('\n').reduce((acc, l) => { if (l.startsWith('      - ')) acc.push(l + '\n'); else if (acc.length) acc[acc.length - 1] += l + '\n'; return acc; }, []);

const all = jobs(workflow);

test('the build gives the artifact ID and the SHA-256 of every file as outputs', () => {
  assert.match(all.build, /\n {6}artifact-id: \$\{\{ steps\.upload\.outputs\.artifact-id \}\}/);
  assert.match(all.build, /\n {6}sums: \$\{\{ steps\.sums\.outputs\.sums \}\}/);
  const upload = steps(all.build).find((s) => s.includes('actions/upload-artifact'));
  assert.ok(upload && /\n {8}id: upload\n/.test(upload), 'the upload step is the one the output names');
});

for (const name of ['e2e', 'publish']) {
  test(`${name} downloads the build's artifact by its ID, never by name`, () => {
    const downloads = steps(all[name]).filter((s) => s.includes('actions/download-artifact'));
    assert.ok(downloads.length > 0);
    for (const d of downloads) {
      assert.match(d, /artifact-ids: \$\{\{ needs\.build\.outputs\.artifact-id \}\}/);
      assert.doesNotMatch(d, /\n {10}name:/);
    }
  });
}

test('publish checks the files against the build before anything is signed, and fails when the check fails', () => {
  const list = steps(all.publish);
  const check = list.findIndex((s) => s.includes('fork-release-handover.sh release'));
  const download = list.findIndex((s) => s.includes('actions/download-artifact'));
  const sign = list.findIndex((s) => /- name: Sign\n/.test(s));
  const write = list.findIndex((s) => /- name: Write what is to be signed\n/.test(s));
  assert.ok(check > download && download >= 0, 'the check runs on what was downloaded');
  assert.ok(sign > check && write > check, 'the check runs before the signing');
  const step = list[check];
  assert.match(step, /SUMS: \$\{\{ needs\.build\.outputs\.sums \}\}/);
  assert.match(step, /set -euo pipefail/);
  assert.doesNotMatch(step, /continue-on-error|\|\| *true|\n {8}if:/);
  // the script it runs is this commit's, from a checkout that holds nothing else
  const checkout = list.find((s) => s.includes('actions/checkout'));
  assert.match(checkout, /sparse-checkout: scripts\/fork-release-handover\.sh/);
  assert.match(checkout, /persist-credentials: false/);
  assert.doesNotMatch(all.publish, /continue-on-error/);
});

test('publish needs the build and the e2e job', () => {
  assert.match(all.publish, /needs: \[build, e2e\]/);
});
