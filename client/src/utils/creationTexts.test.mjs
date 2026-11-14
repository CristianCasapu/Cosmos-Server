// Fork addition: every text of the creation review and the forms it touches
// is in the English translation file, word for word. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
const files = [
  '../components/creationReview.jsx',
  '../pages/servapps/containers/newService.jsx',
  '../pages/servapps/containers/newServiceForm.jsx',
  '../pages/servapps/containers/docker-compose.jsx',
  '../pages/servapps/containers/network.jsx',
  '../pages/servapps/containers/setup.jsx',
  '../pages/market/listing.jsx',
];
const OWN = /^(mgmt\.servapps\.creation\.|mgmt\.servapps\.fork\.|navigation\.market\.archMismatch)/;
const pairsOf = (source) => [...source.matchAll(/\btr?\(\s*'([^']+)'\s*,\s*'((?:[^'\\]|\\.)*)'/gs)]
  .map((m) => [m[1], m[2].replace(/\\(.)/g, (whole, c) => (c === 'n' ? '\n' : c))]);

test('every text of the creation review is in the English translation file, word for word', () => {
  let checked = 0;
  for (const file of files) {
    for (const [key, text] of pairsOf(readFileSync(new URL(file, import.meta.url), 'utf8')).filter(([key]) => OWN.test(key))) {
      assert.equal(translations[key], text, `${key} (${file})`);
      checked += 1;
    }
  }
  const problems = readFileSync(new URL('./newContainer.js', import.meta.url), 'utf8');
  for (const m of problems.matchAll(/\['(mgmt\.servapps\.fork\.problem\.[^']+)', '((?:[^'\\]|\\.)*)'\]/g)) {
    assert.equal(translations[m[1]], m[2].replace(/\\(.)/g, '$1'), m[1]);
    checked += 1;
  }
  assert.ok(checked > 30, `only ${checked} texts checked`);
});
