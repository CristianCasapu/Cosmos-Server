// Fork addition: tests of hostCheck.js. Run: node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { hostCheckText, skipLookup } from './hostCheck.js';

test('a server without a working resolver is said in plain words', () => {
  const [key, text, values] = hostCheckText('jellyfin.example.org', 'Internal server error: lookup jellyfin.example.org on 127.0.0.11:53: server misbehaving');
  assert.equal(key, 'hostCheck.noResolver');
  assert.equal(values.name, 'jellyfin.example.org');
  assert.doesNotMatch(text, /Internal server error|lookup/);
  assert.equal(hostCheckText('a.example.org', 'DNS Check error: lookup a.example.org: i/o timeout')[0], 'hostCheck.noResolver');
  assert.equal(hostCheckText('a.example.org', 'dial udp 1.1.1.1:53: connect: network is unreachable')[0], 'hostCheck.noResolver');
});

test('a name that does not exist is told apart from a resolver that does not answer', () => {
  assert.equal(hostCheckText('nope.example.org:443', 'Internal server error: lookup nope.example.org on 1.1.1.1:53: no such host')[0], 'hostCheck.noSuchName');
  assert.equal(hostCheckText('nope.example.org:443', 'x: no such host')[2].name, 'nope.example.org');
});

test('anything else keeps a short reason, never the whole text', () => {
  const [key, , values] = hostCheckText('a.example.org', 'Internal server error: ' + 'x'.repeat(500));
  assert.equal(key, 'hostCheck.other');
  assert.equal(values.reason.length, 160);
  assert.equal(hostCheckText('', 'Internal server error: No URL requested')[0], 'hostCheck.empty');
});

test('.local names are not looked up', () => {
  assert.equal(skipLookup('jellyfin.local'), true);
  assert.equal(skipLookup('jellyfin.local:8080'), true);
  assert.equal(skipLookup('JELLYFIN.LOCAL.'), true);
  assert.equal(skipLookup('local.example.org'), false);
  assert.equal(skipLookup(''), false);
});
