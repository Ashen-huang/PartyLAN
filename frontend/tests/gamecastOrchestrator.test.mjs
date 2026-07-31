import test from 'node:test';
import assert from 'node:assert/strict';

import {
  GameCastViewerLifecycle,
  GAMECAST_HOST_SELF_PEER_ID,
  GAMECAST_VIEWER_SELF_PEER_ID,
  canStartGameCastSharing,
  gameCastControlErrorMessageKey,
  gameCastFailureMessageKey,
  gameCastRoomOriginLabelKey,
  mergeGameCastPresenceMembers,
  promoteGameCastViewerToHost,
  reduceGameCastViewerReconnectAttempt,
  shouldApplyGameCastShareRuntimeEvent,
  selectOwnGameCastRoomAction,
  shouldListGameCastRoom,
  selectGameCastSharePath,
  selectGameCastJoinAction,
  shouldReconnectGameCastViewer,
} from '../src/media/gamecastOrchestrator.ts';

test('viewer reconnect lifecycle permits only one in-flight attempt', () => {
  const lifecycle = new GameCastViewerLifecycle();

  const first = lifecycle.beginReconnect();
  assert.equal(typeof first, 'number');
  assert.equal(lifecycle.beginReconnect(), null);
  assert.equal(lifecycle.finishReconnect(first), true);

  const second = lifecycle.beginReconnect();
  assert.equal(typeof second, 'number');
  assert.notEqual(second, first);
});

test('viewer reconnect lifecycle rejects callbacks after invalidation', () => {
  const lifecycle = new GameCastViewerLifecycle();
  const stale = lifecycle.beginReconnect();

  lifecycle.invalidate();

  assert.equal(lifecycle.isCurrent(stale), false);
  assert.equal(lifecycle.finishReconnect(stale), false);
  assert.equal(typeof lifecycle.beginReconnect(), 'number');
});

test('viewer websocket completion preserves attempts until a new session or media arrives', () => {
  assert.equal(reduceGameCastViewerReconnectAttempt(2, 'connection-started'), 0);
  assert.equal(reduceGameCastViewerReconnectAttempt(1, 'websocket-connected'), 1);
  assert.equal(reduceGameCastViewerReconnectAttempt(1, 'media-connected'), 0);
  assert.equal(reduceGameCastViewerReconnectAttempt(2, 'sharing-stopped'), 0);
});

test('own active rooms route by their established origin', () => {
  assert.equal(
    selectOwnGameCastRoomAction('admin', { room: 'admin', active: true, origin: 'web' }),
    'open-browser',
  );
  assert.equal(
    selectOwnGameCastRoomAction('admin', { room: 'admin', active: true, origin: 'client' }),
    'enter-native',
  );
  assert.equal(selectOwnGameCastRoomAction('admin', null), 'create-client');
  assert.equal(
    selectOwnGameCastRoomAction('admin', { room: 'other', active: true, origin: 'web' }),
    'create-client',
  );
});

test('own web room stays visible while own client room is represented by create button', () => {
  assert.equal(shouldListGameCastRoom({ room: 'admin', active: true, origin: 'web' }, 'admin'), true);
  assert.equal(shouldListGameCastRoom({ room: 'admin', active: true, origin: 'client' }, 'admin'), false);
  assert.equal(shouldListGameCastRoom({ room: 'other', active: true, origin: 'client' }, 'admin'), true);
  assert.equal(shouldListGameCastRoom({ room: 'other', active: false, origin: 'client' }, 'admin'), false);
});

test('local host rooms choose native sharing', () => {
  assert.equal(
    selectGameCastSharePath({
      roomId: 'room-local',
      selfPeerId: GAMECAST_HOST_SELF_PEER_ID,
      roomEntry: 'local-created',
      roomOrigin: 'client',
      role: 'host',
      hostVideoCapable: true,
      sourceKind: 'display',
    }),
    'native',
  );
});

test('web rooms never expose a client sharing path', () => {
  assert.equal(
    selectGameCastSharePath({
      roomId: 'room-web',
      selfPeerId: GAMECAST_VIEWER_SELF_PEER_ID,
      roomEntry: 'web-joined-viewer',
      roomOrigin: 'web',
      role: 'viewer',
      hostVideoCapable: true,
      sourceKind: 'window',
    }),
    null,
  );
});

test('client rooms choose native sharing even after the owner switched to viewer', () => {
  assert.equal(
    selectGameCastSharePath({
      roomId: 'room-client',
      selfPeerId: GAMECAST_VIEWER_SELF_PEER_ID,
      roomEntry: 'client-joined-viewer',
      roomOrigin: 'client',
      role: 'viewer',
      hostVideoCapable: true,
      sourceKind: 'display',
    }),
    'native',
  );
});

test('only client rooms with native capability expose client sharing', () => {
  assert.equal(canStartGameCastSharing(GAMECAST_VIEWER_SELF_PEER_ID, false, true, 'client'), false);
  assert.equal(canStartGameCastSharing(GAMECAST_VIEWER_SELF_PEER_ID, true, false, 'client'), true);
  assert.equal(canStartGameCastSharing(GAMECAST_HOST_SELF_PEER_ID, true, false, 'web'), false);
  assert.equal(canStartGameCastSharing(GAMECAST_HOST_SELF_PEER_ID, true, false, 'client'), true);
});

test('room origin routes web and unknown rooms to the browser', () => {
  assert.equal(selectGameCastJoinAction('web'), 'open-browser');
  assert.equal(selectGameCastJoinAction(''), 'open-browser');
  assert.equal(selectGameCastJoinAction(undefined), 'open-browser');
  assert.equal(selectGameCastJoinAction('client'), 'native-viewer');
  assert.equal(gameCastRoomOriginLabelKey('web'), 'gamecast.originWeb');
  assert.equal(gameCastRoomOriginLabelKey('client'), 'gamecast.originClient');
  assert.equal(gameCastRoomOriginLabelKey(''), 'gamecast.originCompat');
});

test('control presence maps both clients and web tabs without duplicating self', () => {
  const members = mergeGameCastPresenceMembers(
    [
      { participantId: 'client-self', name: 'Desktop', clientType: 'client', role: 'owner', active: true, sharing: false },
      { participantId: 'web-1', name: 'Browser', clientType: 'web', role: 'member', active: true, sharing: true },
      { participantId: 'web-1', name: 'Browser duplicate', clientType: 'web', role: 'member', active: true, sharing: true },
    ],
    'client-self',
    GAMECAST_HOST_SELF_PEER_ID,
  );
  assert.equal(members.length, 2);
  assert.deepEqual(members[0], {
    id: GAMECAST_HOST_SELF_PEER_ID,
    name: 'Desktop',
    online: true,
    muted: false,
    sharing: false,
    clientType: 'client',
    role: 'owner',
  });
  assert.equal(members[1].id, 'gamecast-participant-web-1');
  assert.equal(members[1].clientType, 'web');
  assert.equal(members[1].sharing, true);
});

test('control errors map to local translation keys', () => {
  assert.equal(gameCastControlErrorMessageKey('share_locked'), 'gamecast.errorShareLocked');
  assert.equal(gameCastControlErrorMessageKey('share_lease_expired'), 'gamecast.errorShareLeaseExpired');
  assert.equal(gameCastControlErrorMessageKey('unknown'), 'gamecast.errorControlUnknown');
});

test('compat sharing promotes the joined viewer websocket to the host role after capture', async () => {
  const calls = [];

  await promoteGameCastViewerToHost(' room-web ', async (roomId) => {
    calls.push(roomId);
  });

  assert.deepEqual(calls, ['room-web']);
});

test('compat host promotion rejects an empty room before touching signaling', async () => {
  await assert.rejects(
    () => promoteGameCastViewerToHost('   ', async () => {
      throw new Error('must not connect');
    }),
    /room id is required/,
  );
});

test('missing room or unsupported host returns no share path', () => {
  assert.equal(
    selectGameCastSharePath({
      roomId: '',
      selfPeerId: GAMECAST_HOST_SELF_PEER_ID,
      roomEntry: 'local-created',
      roomOrigin: 'client',
      role: 'host',
      hostVideoCapable: true,
      sourceKind: 'display',
    }),
    null,
  );
  assert.equal(
    selectGameCastSharePath({
      roomId: 'room-local',
      selfPeerId: GAMECAST_HOST_SELF_PEER_ID,
      roomEntry: 'local-created',
      roomOrigin: 'client',
      role: 'host',
      hostVideoCapable: false,
      sourceKind: 'display',
    }),
    null,
  );
});

test('viewer reconnect stays disabled while compat sharing owns the media path', () => {
  assert.equal(
    shouldReconnectGameCastViewer({
      roomId: 'room-web',
      selfPeerId: GAMECAST_VIEWER_SELF_PEER_ID,
      role: 'viewer',
      roomActive: true,
      roomSharing: true,
      activeSharePath: 'web-compat',
      statusState: 'connected',
      receiverState: 'error',
      attempt: 0,
    }),
    false,
  );
});

test('viewer reconnect is bounded and only runs for an active remote share', () => {
  const base = {
    roomId: 'room-web',
    selfPeerId: GAMECAST_VIEWER_SELF_PEER_ID,
    role: 'viewer',
    roomActive: true,
    roomSharing: true,
    activeSharePath: null,
    statusState: 'error',
    receiverState: 'error',
  };

  assert.equal(shouldReconnectGameCastViewer({ ...base, attempt: 0 }), true);
  assert.equal(shouldReconnectGameCastViewer({ ...base, attempt: 3 }), false);
  assert.equal(shouldReconnectGameCastViewer({ ...base, attempt: 0, roomSharing: false }), false);
});

test('viewer reconnects once sharing starts after the original signaling wait expired', () => {
  const viewerBeforeHost = {
    roomId: 'late-host-room',
    selfPeerId: GAMECAST_VIEWER_SELF_PEER_ID,
    role: 'viewer',
    roomActive: true,
    roomSharing: false,
    activeSharePath: null,
    statusState: 'connected',
    receiverState: 'idle',
    attempt: 0,
  };

  assert.equal(shouldReconnectGameCastViewer(viewerBeforeHost), false);
  assert.equal(shouldReconnectGameCastViewer({ ...viewerBeforeHost, roomSharing: true }), true);
  assert.equal(
    shouldReconnectGameCastViewer({
      ...viewerBeforeHost,
      roomSharing: true,
      statusState: 'connecting',
      receiverState: 'connecting',
    }),
    false,
  );
});

test('failure kinds have distinct user-facing message keys', () => {
  assert.notEqual(gameCastFailureMessageKey('host-switch'), gameCastFailureMessageKey('native-share-start'));
  assert.notEqual(gameCastFailureMessageKey('compat-share-start'), gameCastFailureMessageKey('viewer-reconnect'));
});

test('native runtime events only apply to the current native room', () => {
  assert.equal(
    shouldApplyGameCastShareRuntimeEvent({
      activeSharePath: 'native',
      currentRoomId: 'room-a',
      eventRoomId: 'room-a',
    }),
    true,
  );
  assert.equal(
    shouldApplyGameCastShareRuntimeEvent({
      activeSharePath: 'web-compat',
      currentRoomId: 'room-a',
      eventRoomId: 'room-a',
    }),
    false,
  );
  assert.equal(
    shouldApplyGameCastShareRuntimeEvent({
      activeSharePath: 'native',
      currentRoomId: 'room-b',
      eventRoomId: 'room-a',
    }),
    false,
  );
});
