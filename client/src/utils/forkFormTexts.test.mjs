// Fork addition: every text of the reworked forms is in the English
// translation file, word for word (other languages fall back to it, and a
// text that differs there from the one in the code shows the wrong one).
// The same check the email form makes for its own texts. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));

// [file, the prefixes of the keys that are the fork's own in it]
const files = [
  ['../pages/config/routes/forkRouteGuide.jsx', ['mgmt.urls.guide.']],
  ['../pages/servapps/exposeModal.jsx', ['mgmt.urls.guide.']],
  ['../components/localAddressesForAll.jsx', ['mgmt.urls.all.']],
  ['../components/portChips.jsx', ['mgmt.servApps.port.']],
  ['../components/throttledNotice.jsx', ['throttled.']],
  ['../pages/config/users/forkUserDialogs.jsx', ['mgmt.usermgmt.fork.']],
  ['./activity.js', ['activity.throttled']],
];

// t('key', 'English text' …) and tr('key', 'English text' …), the text in single quotes
const pairsOf = (source) => [...source.matchAll(/\btr?\(\s*'([^']+)'\s*,\s*'((?:[^'\\]|\\.)*)'/gs)]
  .map((m) => [m[1], m[2].replace(/\\(.)/g, (whole, c) => (c === 'n' ? '\n' : c))]);

test('every text of the reworked forms is in the English translation file, word for word', () => {
  let checked = 0;
  for (const [file, prefixes] of files) {
    const source = readFileSync(new URL(file, import.meta.url), 'utf8');
    const own = pairsOf(source).filter(([key]) => prefixes.some((prefix) => key.startsWith(prefix)));
    assert.ok(own.length > 0, `${file}: no text found`);
    for (const [key, text] of own) {
      assert.equal(translations[key], text, `translation.json: ${key} (${file})`);
      checked += 1;
    }
  }
  assert.ok(checked > 110, `only ${checked} texts checked`);
});

test('a text with a value in it names the value the code passes', () => {
  // {{name}} in a text of these forms without a variable of that name would be shown as it is
  for (const [file, prefixes] of files) {
    const source = readFileSync(new URL(file, import.meta.url), 'utf8');
    for (const m of source.matchAll(/\btr?\(\s*'([^']+)'\s*,\s*'((?:[^'\\]|\\.)*)'\s*(,\s*\{([^}]*)\})?/gs)) {
      if (!prefixes.some((prefix) => m[1].startsWith(prefix))) continue;
      const wanted = [...m[2].matchAll(/\{\{(\w+)\}\}/g)].map((v) => v[1]);
      const given = m[4] || '';
      for (const name of wanted) {
        assert.ok(new RegExp('\\b' + name + '\\b').test(given), `${m[1]}: {{${name}}} is not passed (${file})`);
      }
    }
  }
});
