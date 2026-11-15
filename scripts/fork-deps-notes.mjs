#!/usr/bin/env node
// Fork addition: what a release changes in its dependencies, read from the
// "Fork-Dependency:" lines the automatic update writes into its commits
// (scripts/fork-deps-update.mjs), for the release workflow:
//
//   node scripts/fork-deps-notes.mjs <previous release tag or ""> <out dir> [YYYY-MM-DD]
//
// writes <out>/dependencies.json (schema 1: release changes with from, to,
// kind, age in days; majors tried and dropped; what was left for a session),
// <out>/dependencies.md (a section for the release notes, empty when nothing
// changed) and prints the summary that goes into meta.json
// ({"minor": n, "major": m, "majors": [...]}; {} when nothing changed).

import { execFileSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import * as lib from './fork-deps-lib.mjs';

const [previous = '', out = '.', day] = process.argv.slice(2);
const today = day || new Date().toISOString().slice(0, 10);
if (previous && !/^v\d+\.\d+\.\d+-fork\.\d+$/.test(previous)) {
  console.error(`not a fork release tag: ${previous}`);
  process.exit(2);
}

const range = previous ? [`${previous}..HEAD`] : ['-n', '0', 'HEAD'];
const log = execFileSync('git', ['log', '--reverse', '--no-merges', '--format=%B%x00', ...range], { encoding: 'utf8', maxBuffer: 64 << 20 });
const parsed = log.split('\0').map(lib.parseTrailers);
const summary = lib.summarise(parsed, today);

const doc = {
  schema: 1,
  previous: previous || null,
  changes: summary.changes.slice(0, 300),
  dropped: summary.dropped.slice(0, 50),
  tried: summary.tried.slice(0, 50),
  left: summary.left.slice(0, 50),
};
writeFileSync(join(out, 'dependencies.json'), JSON.stringify(doc, null, 2) + '\n');
writeFileSync(join(out, 'dependencies.md'), lib.notesSection(summary));
console.log(JSON.stringify(summary.changes.length ? lib.metaSummary(summary) : {}));
