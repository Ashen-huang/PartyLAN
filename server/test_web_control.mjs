import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const {
  decidePresenceJoin,
  mergePresence,
  canStartShare,
  isUnifiedSfuRoom,
  desiredSfuRole,
  runShareStart,
  runShareStop,
  webMediaContract,
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
  const live = { active: true, mediaMode: 'sfu', mediaProtocolVersion: 1, sharing: true, sharePhase: 'live', sfu: true };
  assert.equal(desiredSfuRole({ ...live, shareOwnerParticipantId: 'desktop' }, 'web', false), 'viewer');
  assert.equal(desiredSfuRole({ ...live, shareOwnerParticipantId: 'web' }, 'web', true), 'host');
  assert.equal(desiredSfuRole({ active: true, sharing: false, sfu: false }, 'web', false), '');
  assert.equal(desiredSfuRole({ ...live, sharePhase: 'starting', sfu: false }, 'web', false), '');
  assert.equal(desiredSfuRole({ ...live, sharePhase: 'stopping', sfu: false }, 'web', false), '');
});

test('unified web mode depends on the explicit room contract, not origin', () => {
  assert.equal(isUnifiedSfuRoom({ active: true, origin: 'web', mediaMode: 'sfu', mediaProtocolVersion: 1 }), true);
  assert.equal(isUnifiedSfuRoom({ active: true, origin: 'client', mediaMode: 'sfu', mediaProtocolVersion: 1 }), true);
  assert.equal(isUnifiedSfuRoom({ active: true, origin: 'client', mediaMode: 'legacy-p2p', mediaProtocolVersion: 1 }), false);
  assert.equal(isUnifiedSfuRoom({ active: true, origin: 'client', mediaMode: 'sfu', mediaProtocolVersion: 0 }), false);
});

test('web presence advertises the complete H264 and Opus SFU contract', () => {
  assert.deepEqual(webMediaContract(), {
    mediaProtocolVersion: 1,
    capabilities: { sfuViewer: true, sfuHost: true, h264: true, opus: true },
  });
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

test('unified share workflow marks live only after publishing and stops in phase order', async () => {
  const startCalls = [];
  await runShareStart({
    claim: async () => { startCalls.push('claim'); return { leaseId: 'lease-live' }; },
    capture: async () => { startCalls.push('capture'); return { id: 'stream' }; },
    publish: async () => { startCalls.push('publish'); return { ready: true }; },
    markLive: async () => { startCalls.push('live'); },
    release: async () => { startCalls.push('release'); },
  });
  assert.deepEqual(startCalls, ['claim', 'capture', 'publish', 'live']);

  const stopCalls = [];
  await runShareStop({
    markStopping: async () => { stopCalls.push('stopping'); },
    closeMedia: async () => { stopCalls.push('close'); },
    release: async () => { stopCalls.push('release'); },
  });
  assert.deepEqual(stopCalls, ['stopping', 'close', 'release']);
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
