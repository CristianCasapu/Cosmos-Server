// Fork addition: tests of utils/composeYaml.mjs (how a pasted compose file is read).
// node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { loadCompose } from './composeYaml.mjs';

test('merge keys are applied, as js-yaml 4 did', () => {
  const doc = loadCompose([
    'x-defaults: &defaults',
    '  restart: unless-stopped',
    '  environment:',
    '    TZ: Europe/Paris',
    'services:',
    '  web:',
    '    <<: *defaults',
    '    image: nginx:alpine',
  ].join('\n'));
  assert.equal(doc.services.web.restart, 'unless-stopped');
  assert.deepEqual(doc.services.web.environment, { TZ: 'Europe/Paris' });
  assert.equal(doc.services.web.image, 'nginx:alpine');
  assert.equal(Object.hasOwn(doc.services.web, '<<'), false);
});

test('a port mapping stays text (no YAML 1.1 base-60 numbers)', () => {
  const doc = loadCompose('services:\n  ssh:\n    ports:\n      - 22:22\n      - "8080:80"\n');
  assert.deepEqual(doc.services.ssh.ports, ['22:22', '8080:80']);
});

test('yes, no, on and off stay text; true and false are booleans', () => {
  const doc = loadCompose('a: yes\nb: off\nc: true\nd: false\n');
  assert.deepEqual(doc, { a: 'yes', b: 'off', c: true, d: false });
});

test('broken YAML throws, so the form can say what is wrong', () => {
  assert.throws(() => loadCompose('services:\n  web: [unclosed\n'));
});
