// Fork addition (plan 51): tests of monitoringStoreForm.js.
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  storeOf, storeMove, moveKey, moveSentence, maskedPasswordStale, hostChoices, pickedChoice,
  choiceValues, databaseRefusal, requestSaveAnyway, takeSaveAnyway, storeStateLine,
} from './monitoringStoreForm.js';

const pg = (host, database = 'cosmos', extra = {}) => ({ PostgresHost: host, PostgresDatabase: database, ...extra });

test('the store a setting names', () => {
  assert.equal(storeOf({}), null);
  assert.equal(storeOf({ PostgresHost: '  ' }), null);
  assert.deepEqual(storeOf(pg('192.168.1.10:5432', '')), { host: '192.168.1.10:5432', database: 'cosmos' });
});

test('a save moves the store only when the server or the database changes', () => {
  assert.equal(storeMove({}, {}), null);
  assert.equal(storeMove(pg('db:5432'), pg(' db:5432 ')), null);
  assert.equal(storeMove(pg('db:5432', ''), pg('db:5432', 'cosmos')), null, 'the default database is cosmos');
  assert.deepEqual(storeMove({}, pg('192.168.1.10:5432', 'monitoring')),
    { from: 'local', to: 'postgres', fromName: '', toName: 'monitoring at 192.168.1.10:5432' });
  assert.equal(storeMove(pg('db:5432'), {}).to, 'local');
  assert.equal(storeMove(pg('db:5432'), pg('db:5432', 'other')).to, 'postgres');
  assert.equal(moveKey(storeMove({}, pg('a:1'))), moveKey(storeMove({}, pg('b:1'))), 'a corrected server is not asked again');
  assert.notEqual(moveKey(storeMove({}, pg('a:1'))), moveKey(storeMove(pg('a:1'), {})));
  assert.equal(moveKey(null), '');
});

test('the history sentence says what happens to the records, in one sentence each way', () => {
  const [key1, text1, params1] = moveSentence(storeMove({}, pg('192.168.1.10:5432')));
  assert.match(key1, /toPostgres$/);
  assert.match(text1, /stay in the local file and are not copied now/);
  assert.match(text1, /charts start again from now/);
  assert.match(text1, /offers to copy the history/);
  assert.match(text1, /switching back to the local file shows it again/);
  assert.equal(params1.to, 'cosmos at 192.168.1.10:5432');
  const [, text2, params2] = moveSentence(storeMove(pg('db:5432'), {}));
  assert.match(text2, /stay there and are not copied/);
  assert.equal(params2.from, 'cosmos at db:5432');
  const [key3] = moveSentence(storeMove(pg('a:1'), pg('b:1')));
  assert.match(key3, /toOther$/);
  assert.equal(moveSentence(null), null);
});

test('the masked password next to a changed server, port, database or user', () => {
  const saved = pg('192.168.1.10:5432', 'cosmos', { PostgresUsername: 'cosmos' });
  assert.equal(maskedPasswordStale({ ...saved, PostgresPassword: '***' }, saved), false);
  for (const change of [{ PostgresHost: 'localhost:5432' }, { PostgresHost: '192.168.1.10:5433' }, { PostgresDatabase: 'other' }, { PostgresUsername: 'other' }]) {
    assert.equal(maskedPasswordStale({ ...saved, ...change, PostgresPassword: '***' }, saved), true, JSON.stringify(change));
    assert.equal(maskedPasswordStale({ ...saved, ...change, PostgresPassword: 'typed' }, saved), false);
  }
  assert.equal(maskedPasswordStale({ PostgresHost: '', PostgresPassword: '***' }, saved), false, 'back to the local file needs no password');
});

test('the host choices come from the proposals that have an address', () => {
  const proposals = [
    { container: 'postgresql', host: '192.168.1.10:5432', hostSource: 'published', reach: 'answers', database: 'cosmos', user: 'cosmos' },
    { container: 'other', host: '', hostSource: 'none' },
    { container: 'dup', host: '192.168.1.10:5432' },
    { container: 'old', host: '172.17.0.3:5432', hostSource: 'address', reach: 'silent' },
  ];
  const choices = hostChoices(proposals);
  assert.deepEqual(choices.map((c) => c.value), ['192.168.1.10:5432', '172.17.0.3:5432']);
  assert.equal(choices[1].reach, 'silent');
  assert.equal(pickedChoice(' 192.168.1.10:5432 ', choices), '192.168.1.10:5432');
  assert.equal(pickedChoice('localhost:5432', choices), 'custom');
  assert.deepEqual(choiceValues({ PostgresDatabase: '', PostgresUsername: 'mine' }, proposals[0]),
    { PostgresHost: '192.168.1.10:5432', PostgresDatabase: 'cosmos' }, 'a typed user is kept');
});

test('the refusal of a database that does not answer offers "Save anyway", once', () => {
  assert.equal(databaseRefusal(null), null);
  assert.equal(databaseRefusal({ code: 'UC046', message: 'x' }), null);
  assert.deepEqual(databaseRefusal({ code: 'UC047', message: 'Monitoring database: Nothing answers at localhost:5432. Nothing was saved.' }),
    { message: 'Nothing answers at localhost:5432. Nothing was saved.' });
  assert.equal(takeSaveAnyway(), false);
  requestSaveAnyway();
  assert.equal(takeSaveAnyway(), true);
  assert.equal(takeSaveAnyway(), false);
});

test('the line about the store in use', () => {
  assert.equal(storeStateLine(null), null);
  assert.equal(storeStateLine({ fallback: false }), null);
  const [, text, params] = storeStateLine({ fallback: true, host: 'localhost:5432', since: 'T', message: 'Nothing answers.' }, (d) => 'at ' + d);
  assert.match(text, /does not answer since \{\{since\}\}/);
  assert.match(text, /Records are kept in the local file/);
  assert.deepEqual(params, { host: 'localhost:5432', since: 'at T', reason: 'Nothing answers.' });
  assert.match(storeStateLine({ fallback: true, since: 'T' })[0], /NoHost$/);
  assert.equal(storeStateLine({ fallback: true, host: 'h', since: 'T', message: 'Not reached.', advice: 'Use 192.168.1.10:5432.' })[2].reason, 'Not reached. Use 192.168.1.10:5432.');
});

test('the records of another server\'s outage that stay in the local file (review round 3, M8)', async () => {
  const { asideLines } = await import('./monitoringStoreForm.js');
  assert.deepEqual(asideLines(null), []);
  assert.deepEqual(asideLines({ aside: [] }), []);
  const lines = asideLines({ aside: [{ host: '192.0.2.5:5432', database: 'cosmos', from: 'A', to: 'B' }] }, (d) => 'at ' + d);
  assert.equal(lines.length, 1);
  assert.match(lines[0][1], /holds the records written while \{\{host\}\} did not answer/);
  assert.match(lines[0][1], /They stay there/);
  assert.deepEqual(lines[0][2], { host: '192.0.2.5:5432', from: 'at A', to: 'at B' });
});

test('a container\'s own address on Docker\'s network: works now, said, with the address that stays', async () => {
  const { containerAddressWarning, containerAddressSentence } = await import('./monitoringStoreForm.js');
  const proposals = [
    { container: 'postgresql', host: '192.168.1.10:5432', hostSource: 'published', addresses: ['172.17.0.2'] },
    { container: 'pgofficial', host: '172.17.0.3:5432', hostSource: 'address', addresses: ['172.17.0.3'] },
  ];
  assert.deepEqual(containerAddressWarning('172.17.0.2:5432', proposals), { container: 'postgresql', address: '172.17.0.2', prefer: '192.168.1.10:5432' });
  assert.match(containerAddressSentence(containerAddressWarning('172.17.0.2:5432', proposals))[1], /works now, but it changes when the container is made again/);
  assert.match(containerAddressSentence(containerAddressWarning('172.17.0.2:5432', proposals))[0], /bridgePrefer$/);
  assert.equal(containerAddressWarning('172.17.0.3:5432', proposals).prefer, '', 'nothing published: publish it');
  assert.match(containerAddressSentence(containerAddressWarning('172.17.0.3', proposals))[0], /bridgePublish$/);
  assert.equal(containerAddressWarning('192.168.1.10:5432', proposals), null);
  assert.equal(containerAddressWarning('', proposals), null);
  assert.equal(containerAddressSentence(null), null);
});
