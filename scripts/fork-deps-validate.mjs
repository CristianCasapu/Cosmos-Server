#!/usr/bin/env node
// Fork addition: what an automatic dependency update may change, checked
// before its commits are pushed anywhere.
//
//   node scripts/fork-deps-validate.mjs <base> <tip>
//
// The job that pushes runs this from its own checkout of the base (custom,
// trusted), never from the candidate: the update itself ran dependency code
// (its quick checks), so its result is data. The commits from base to tip
// must be a straight line on top of base and may change only dependency
// versions: go.mod's go line and require versions, package.json versions of
// packages already listed, a lock file whose every package comes from
// registry.npmjs.org with an integrity, action pins and the Node line in
// .github/actions, and image names in scripts. Nothing under the held paths,
// no source file, no workflow, no document. Exit 0: fine; 1: refused (the
// reason is printed); 2: cannot run.

import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import * as lib from './fork-deps-lib.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const [base, tip] = process.argv.slice(2);
const git = (...args) => execFileSync('git', args, { encoding: 'utf8', maxBuffer: 256 << 20 });

function refuse(reason) {
  console.error(`Refused: ${lib.safeText(reason, 500)}`);
  process.exit(1);
}

if (!/^[0-9a-f]{40}$/.test(base || '') || !/^[0-9a-f]{40}$/.test(tip || '')) {
  console.error('usage: fork-deps-validate.mjs <base sha> <tip sha>');
  process.exit(2);
}

// the holds come from this (trusted) checkout, not from the candidate
const holds = lib.parseHolds(readFileSync(join(here, 'fork-deps-holds.txt'), 'utf8'));

try { git('merge-base', '--is-ancestor', base, tip); } catch { refuse('the update is not on top of the commit it started from'); }
if (git('rev-list', '--merges', `${base}..${tip}`).trim()) refuse('the update contains a merge');
const count = git('rev-list', '--count', `${base}..${tip}`).trim();
if (Number(count) > 60) refuse(`too many commits (${count})`);

const show = (rev, path) => {
  try { return git('show', `${rev}:${path}`); } catch { return null; }
};

const changed = git('diff', '--name-status', '--no-renames', base, tip).split('\n').filter(Boolean);
for (const line of changed) {
  const [status, path] = line.split('\t');
  if (status !== 'M') refuse(`${path}: files may not be added or removed (${status})`);
  if (!lib.allowedPath(path, holds)) refuse(`${path}: not a file the update may change`);
  const before = show(base, path), after = show(tip, path);
  let problem = null;
  if (path === 'go.mod') problem = lib.goModChangeAllowed(before, after, holds);
  else if (path === 'go.sum') problem = after.split('\n').every((l) => l === '' || /^\S+ v\S+ h1:[A-Za-z0-9+/=]{44}$/.test(l)) ? null : 'go.sum: a line is not a checksum';
  else if (path.endsWith('package.json')) problem = lib.packageJsonChangeAllowed(before, after);
  else if (path.endsWith('package-lock.json')) problem = lib.lockAllowed(after);
  else problem = lib.changedLinesAllowed(path, before, after);
  if (problem) refuse(problem);
}

// the mode of every file stays (no file becomes executable)
for (const line of git('diff', '--summary', base, tip).split('\n')) {
  if (/mode change/.test(line)) refuse(`a file mode changed: ${line.trim()}`);
}
console.log(`The update from ${base.slice(0, 12)} to ${tip.slice(0, 12)} changes only dependency versions (${changed.length} files, ${count} commits).`);
