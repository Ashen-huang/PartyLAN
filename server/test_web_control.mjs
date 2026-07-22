import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const {
  decidePresenceJoin,
  mergePresence,
  canStartShare,
  desiredSfuRole,
  runShareStart,
  canSendProtectedControl,
  shouldPostRoomState,
} = require('./web_control.js');

test('web creates an absent room but joins an existing client room as a member', () => {
  assert.deepEqual(decidePresenceJoin({ active: false }), { role: 'owner', origin: 'web' });
  assert.deepEqual(decidePresenceJoin({ active: true, origin: 'client' }), { role: 'member', origin: '' });
});

test('presence is deduplicated by participant id and keeps both client types', () => {
  const members = mergePresence([
    { participantId: 'desktop', name: 'Desktop', clientType: 'client', active: true },
    { participantId: 'web', name: 'Browser', clientType: 'web', active: true },
    { participantId: 'web', name: 'Duplicate', clientType: 'web', active: true },
  ]);
  assert.equal(members.length, 2);
  assert.equal(members[0].clientType, 'client');
  assert.equal(members[1].participantId, 'web');
});

test('another active lease blocks sharing while the current owner remains recognized', () => {
  assert.equal(canStartShare({ sharing: false }, 'web-self'), true);
  assert.equal(canStartShare({ sharing: true, shareOwnerParticipantId: 'desktop' }, 'web-self'), false);
  assert.equal(canStartShare({ sharing: true, shareOwnerParticipantId: 'web-self' }, 'web-self'), true);
});

test('cross-client room state selects the existing SFU role', () => {
  assert.equal(desiredSfuRole({ active: true, sharing: true, sfu: true, shareOwnerParticipantId: 'desktop' }, 'web', false), 'viewer');
  assert.equal(desiredSfuRole({ active: true, sharing: true, sfu: true, shareOwnerParticipantId: 'web' }, 'web', true), 'host');
  assert.equal(desiredSfuRole({ active: true, sharing: false, sfu: false }, 'web', false), '');
});

test('share workflow claims before capture and releases when capture is cancelled', async () => {
  const calls = [];
  await assert.rejects(
    () => runShareStart({
      claim: async () => { calls.push('claim'); return { leaseId: 'lease-1' }; },
      capture: async () => { calls.push('capture'); throw new Error('cancelled'); },
      publish: async () => { calls.push('publish'); },
      release: async () => { calls.push('release'); },
    }),
    /cancelled/,
  );
  assert.deepEqual(calls, ['claim', 'capture', 'release']);
});

test('share workflow never captures when claim fails and releases on publish failure', async () => {
  let captured = false;
  await assert.rejects(
    () => runShareStart({
      claim: async () => { throw new Error('share_locked'); },
      capture: async () => { captured = true; },
      publish: async () => undefined,
      release: async () => undefined,
    }),
    /share_locked/,
  );
  assert.equal(captured, false);

  const calls = [];
  await assert.rejects(
    () => runShareStart({
      claim: async () => ({ leaseId: 'lease-2' }),
      capture: async () => { calls.push('capture'); return {}; },
      publish: async () => { calls.push('publish'); throw new Error('publish failed'); },
      release: async () => { calls.push('release'); },
    }),
    /publish failed/,
  );
  assert.deepEqual(calls, ['capture', 'publish', 'release']);
});

test('protected control writes require an authenticated non-guest user', () => {
  assert.equal(canSendProtectedControl(true, 'admin'), true);
  assert.equal(canSendProtectedControl(false, 'admin'), false);
  assert.equal(canSendProtectedControl(true, 'guest'), false);
  assert.equal(canSendProtectedControl(true, ''), false);
});

test('room state posts are deduplicated inside the heartbeat interval', () => {
  const state = { room: 'room-a', active: true, sharing: true, sfu: false, audioMode: 'system' };
  assert.equal(shouldPostRoomState('', state, 1000, 0, 5000), true);
  assert.equal(shouldPostRoomState(JSON.stringify(state), state, 4500, 1000, 5000), false);
  assert.equal(shouldPostRoomState(JSON.stringify(state), state, 6000, 1000, 5000), true);
  assert.equal(shouldPostRoomState(JSON.stringify(state), {...state, sfu: true}, 2000, 1000, 5000), true);
});
