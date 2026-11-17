import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeSetupCode, setupCodeProblem, formatSetupCode, setupCodeAnswer, keepSetupCode, heldSetupCode, forgetSetupCode, setupCodeHeaders, SETUP_CODE_HEADER } from './setupCode.js';

test('a pasted code is taken with or without its separators', () => {
  for (const typed of ['ABCD-EFGH-JKMN-PQRS-TVWX', 'abcd efgh jkmn pqrs tvwx', 'ABCDEFGHJKMNPQRSTVWX', ' abcd_efgh-jkmn pqrs-tvwx\n']) {
    assert.equal(normalizeSetupCode(typed), 'ABCDEFGHJKMNPQRSTVWX');
  }
  assert.equal(formatSetupCode('abcdefghjkmnpqrstvwx'), 'ABCD-EFGH-JKMN-PQRS-TVWX');
});

test('what is wrong with a typed code is said', () => {
  assert.equal(setupCodeProblem('')[0], 'newInstall.setupCode.empty');
  assert.equal(setupCodeProblem('ABCD-EFGH-JKMN-PQRS-TVW0')[0], 'newInstall.setupCode.lookAlike');
  assert.equal(setupCodeProblem('ABCD-EFGH-JKMN-PQRS-TVW!')[0], 'newInstall.setupCode.badSign');
  assert.equal(setupCodeProblem('ABCD-EFGH')[0], 'newInstall.setupCode.length');
  assert.equal(setupCodeProblem('ABCD-EFGH-JKMN-PQRS-TVWX'), null);
  assert.equal(normalizeSetupCode('ABCD-EFGH-JKMN-PQRS-TVW0'), '');
});

test('the answers of the server in words', () => {
  assert.equal(setupCodeAnswer(403, 'SC002')[0], 'newInstall.setupCode.wrong');
  assert.deepEqual(setupCodeAnswer(429, 'SC003', '17')[2], { seconds: '17' });
  assert.equal(setupCodeAnswer(503, 'SC004')[0], 'newInstall.setupCode.noFile');
  assert.equal(setupCodeAnswer(0, undefined)[0], 'newInstall.setupCode.noAnswer');
});

test('the code goes only to Cosmos\' API, and only while held', () => {
  forgetSetupCode();
  assert.deepEqual(setupCodeHeaders('/cosmos/api/newInstall'), {});
  keepSetupCode('abcd-efgh-jkmn-pqrs-tvwx');
  assert.equal(heldSetupCode(), 'ABCDEFGHJKMNPQRSTVWX');
  assert.deepEqual(setupCodeHeaders('/cosmos/api/setup'), { [SETUP_CODE_HEADER]: 'ABCDEFGHJKMNPQRSTVWX' });
  assert.deepEqual(setupCodeHeaders('https://example.org/cosmos/api/setup'), {});
  assert.deepEqual(setupCodeHeaders('/cosmos-ui/x'), {});
  forgetSetupCode();
  assert.equal(heldSetupCode(), '');
});
