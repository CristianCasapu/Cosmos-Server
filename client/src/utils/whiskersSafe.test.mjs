// Fork addition: the compose installer's templates read without `new Function`
// (whiskersSafe.js) give what whiskers 0.4 gives, on the templates the Market
// uses and on random ones.
// node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
import { render } from './whiskersSafe.js';

const require = createRequire(import.meta.url);
const whiskers = require('whiskers');

// both on a copy of the context: loops change it, and that is compared too
const both = (template, context) => {
  const a = structuredClone(context);
  const b = structuredClone(context);
  let want, got, wantError = null, gotError = null;
  const warn = console.warn;
  console.warn = () => {};
  try {
    try { want = whiskers.render(template, a); } catch (e) { wantError = e; }
    try { got = render(template, b); } catch (e) { gotError = e; }
  } finally {
    console.warn = warn;
  }
  whiskers.cache = {};
  return { want, got, wantError, gotError, a, b };
};

const same = (template, context, label = template) => {
  const r = both(template, context);
  if (r.wantError || r.gotError) {
    assert.ok(r.wantError && r.gotError, `${JSON.stringify(label)}: whiskers ${r.wantError ? 'throws ' + r.wantError.message : 'gives ' + JSON.stringify(r.want)}, this ${r.gotError ? 'throws ' + r.gotError.message : 'gives ' + JSON.stringify(r.got)}`);
    return;
  }
  assert.equal(r.got, r.want, JSON.stringify(label));
  assert.deepEqual(r.b, r.a, `${JSON.stringify(label)}: the context after rendering`);
};

const context = {
  ServiceName: 'jellyfin',
  Hostnames: { jellyfin: { main: { host: 'jellyfin.example.com' } } },
  Context: { port: 8096, enabled: true, off: false, empty: '', zero: 0, list: ['a', 'b'], none: [], obj: {}, nested: { x: 'y' } },
  Passwords: ['p4ss\'w"rd\\1', 'second'],
  CPU_ARCH: 'amd64',
  CPU_AVX: true,
  DefaultDataPath: '/cosmos-storage',
  RootHostname: 'example.com',
  RootProtocol: 'https',
  items: [{ name: 'one' }, { name: 'two' }],
  word: 'abc',
  partial: 'P[{ServiceName}]',
  partialLoop: '{for i in items}<{i.name}>{/for}',
};

test('the tags the Market templates use', () => {
  for (const template of [
    '',
    'plain text with \'quotes\' "double" \\ backslash \\n not a newline',
    'line one\nline two\r\nline three\r',
    '{ServiceName}-{CPU_ARCH} on {RootProtocol}://{RootHostname}',
    '{Hostnames.jellyfin.main.host} {Hostnames.missing.main.host}|{Context.nested.x}',
    '{Passwords.0} {Passwords.1} {Passwords.2}',
    '{Context.port} {Context.zero} {Context.off} {Context.empty} {Context.none} {Context.obj} {Context.list} {Context.nested}',
    '{if CPU_AVX}avx{else}no avx{/if} {if not CPU_AVX}x{else}y{/if}',
    '{if Context.off}a{/if}{if Context.zero}zero{else}nothing{/if}',
    '{for i in items}- {i.name}\n{/for}after: {i}',
    '{for i in Context.none}x{else}empty list{/for}',
    '{for i in Context.list}{i}{else}never{/for}{i}',
    '{for c in word}[{c}]{/for}',
    '{>partial} and {>partialLoop} and {>missing}',
    '{!a comment {ServiceName}!}after',
    '\\{ServiceName} \\\\{ServiceName} \\\\\\{ServiceName} \\{!kept!}',
    '{unknown tag here} { ServiceName } {Service Name} {}',
    'extra {/if} and {else} and {/for}',
    '{if CPU_AVX}open if {for i in items}open loop {i.name}',
    '{for item-name in items}{item-name.name}{/for}{item-name}',
    '{for 1x in items}{1x}{/for}',
    '{for a@b in items}x{/for}',
    '{for a-b-c in items}x{/for}',
    '{for i in items}{for i in items}{i.name}{/for}|{i.name}{/for}{i}',
    '{for ServiceName in items}{ServiceName.name}{/for}{ServiceName}',
    '{for Context in Context.list}{Context}{/for}{Context.port}',
    '{for word in word}{word}{/for}{word}',
    'services:\n  {ServiceName}:\n    image: x\n    environment:\n      - PASS={Passwords.0}\n      - URL={RootProtocol}://{Hostnames.jellyfin.main.host}\n',
  ]) same(template, context);
});

test('a Market listing installer template', () => {
  // the shape of the official Market's compose files (cosmos-servapps-official)
  const template = `{
  "cosmos-installer": { "form": [{ "name": "admin", "label": "Admin", "initialValue": "admin", "type": "text" }] },
  "name": "{ServiceName}",
  "services": {
    "{ServiceName}": {
      "image": "example/app:latest{if CPU_AVX}-avx{/if}",
      "environment": ["ADMIN={Context.admin}", "PASSWORD={Passwords.0}", "DB={ServiceName}-db"],
      "routes": [{ "name": "{ServiceName}", "target": "http://{ServiceName}:8080", "useHost": true, "host": "{Hostnames.{ServiceName}.{ServiceName}.host}" }],
      "volumes": [{ "source": "{DefaultDataPath}/{ServiceName}", "target": "/data", "type": "bind" }]
    }
  }
}`;
  same(template, context);
});

// random templates from the pieces the syntax is made of
test('random templates', () => {
  const pieces = ['{', '}', '{ServiceName}', '{Context.port}', '{Context.none}', '{Passwords.0}', '{if CPU_AVX}', '{if not Context.off}', '{if Context.zero}',
    '{else}', '{/if}', '{for i in items}', '{for i in Context.list}', '{for i in Context.none}', '{for c in word}', '{for word in word}', '{i.name}', '{i}', '{c}', '{/for}',
    '{>partial}', '{>partialLoop}', '{!', '!}', '\\', '\\\\', "'", '"', '\n', '\r', ' ', 'text', 'for', 'if', '>', '/', '.', '-', '@', '{for i-j in items}', '{i-j}', '{for 9 in items}'];
  let seed = 20261114;
  const random = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
  for (let n = 0; n < 4000; n++) {
    let template = '';
    const length = 1 + Math.floor(random() * 24);
    for (let i = 0; i < length; i++) template += pieces[Math.floor(random() * pieces.length)];
    same(template, context);
  }
});

test('the installer uses this, not the library', () => {
  const source = readFileSync(new URL('../pages/servapps/containers/docker-compose.jsx', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /from ['"]whiskers['"]/, 'docker-compose.jsx must not import whiskers (it compiles with new Function)');
  assert.match(source, /from ['"][./]*utils\/whiskersSafe['"]/);
});
