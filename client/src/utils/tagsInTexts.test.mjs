// Fork addition: a text that carries an HTML tag must be read through
// <Trans>, never through t(): t() returns the text as it is, and React shows
// the tag to the reader ("…the configuration. <br/>This page…").
// Placeholders written with angle brackets (<token>, <app>) are not tags.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { readOnlyReason } from './readOnly.js';

const root = join(fileURLToPath(new URL('.', import.meta.url)), '..');
const texts = JSON.parse(readFileSync(join(root, 'utils/locales/en/translation.json'), 'utf8'));
const TAG = /<\/?(br|strong|b|i|em|u|p|pre|code|a|ul|ol|li|span|div|small|h[1-6])(\s[^>]*)?\/?>/i;

const sources = [];
const walk = (dir) => {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) {
      if (name !== 'locales' && name !== 'node_modules') walk(path);
    } else if (/\.(jsx?|tsx?)$/.test(name) && !/\.test\./.test(name)) {
      sources.push([path.slice(root.length + 1), readFileSync(path, 'utf8')]);
    }
  }
};
walk(root);

test('a text with an HTML tag is never read through t()', () => {
  const tagged = Object.keys(texts).filter((key) => typeof texts[key] === 'string' && TAG.test(texts[key]));
  assert.ok(tagged.length > 5, 'the translation file has texts with tags');
  const wrong = [];
  for (const [file, source] of sources) {
    for (const key of tagged) {
      // t('key'   t("key"   t(`key`   (but not i18nKey="key")
      const call = new RegExp('\\bt\\(\\s*[\'"`]' + key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '[\'"`]');
      if (call.test(source)) wrong.push(file + ': t(' + JSON.stringify(key) + ') shows its tag as text: use <Trans i18nKey=…>');
    }
  }
  assert.deepEqual(wrong, []);
});

test('a fallback text given to t() carries no HTML tag', () => {
  const wrong = [];
  const call = /\bt\(\s*(['"`])([\w.:-]+)\1\s*,\s*(['"`])((?:\\.|(?!\3)[^\\])*)\3/g;
  for (const [file, source] of sources) {
    for (const match of source.matchAll(call)) {
      if (TAG.test(match[4])) wrong.push(file + ': the fallback of ' + match[2] + ' has a tag');
    }
  }
  assert.deepEqual(wrong, []);
});

test('why a page of settings is read-only', () => {
  assert.equal(readOnlyReason(true, true), 'none');
  assert.equal(readOnlyReason(false, true), 'password', 'an administrator whose confirmation ran out is not told they are no administrator');
  assert.equal(readOnlyReason(false, false), 'role');
  assert.equal(readOnlyReason(true, false), 'none');
  for (const key of ['readOnly.confirm', 'readOnly.password', 'readOnly.role']) {
    assert.ok(texts[key] && !TAG.test(texts[key]), key + ' is in the translation file, without a tag');
  }
});
