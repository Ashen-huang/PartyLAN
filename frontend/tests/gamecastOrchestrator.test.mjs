import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

import {
  GameCastViewerLifecycle,
  GAMECAST_HOST_SELF_PEER_ID,
  GAMECAST_VIEWER_SELF_PEER_ID,
  canStartGameCastSharing,
  gameCastControlErrorMessageKey,
  gameCastFailureMessageKey,
  gameCastRoomOriginLabelKey,
  isGameCastRoomLiveForViewer,
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

const appSource = await readFile(new URL('../src/App.tsx', import.meta.url), 'utf8');

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

test('own active rooms always enter the native room regardless of origin', () => {
  assert.equal(
    selectOwnGameCastRoomAction('admin', { room: 'admin', active: true, origin: 'web' }),
    'enter-native',
  );
  assert.equal(
    selectOwnGameCastRoomAction('admin', { room: 'admin', active: true, origin: 'client' }),
    'enter-native',
  );
  assert.equal(
    selectOwnGameCastRoomAction('admin', { room: 'admin', active: true }),
    'enter-native',
  );
  assert.equal(selectOwnGameCastRoomAction('admin', null), 'create-client');
  assert.equal(
    selectOwnGameCastRoomAction('admin', { room: 'other', active: true, origin: 'web' }),
    'create-client',
  );
});

test('own active room is represented by the create or enter button regardless of origin', () => {
  assert.equal(shouldListGameCastRoom({ room: 'admin', active: true, origin: 'web' }, 'admin'), false);
  assert.equal(shouldListGameCastRoom({ room: 'admin', active: true, origin: 'client' }, 'admin'), false);
  assert.equal(shouldListGameCastRoom({ room: 'admin', active: true }, 'admin'), false);
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
      mediaMode: 'sfu',
      mediaProtocolVersion: 1,
      role: 'host',
      hostVideoCapable: true,
      sourceKind: 'display',
    }),
    'native-sfu',
  );
});

test('web rooms expose the same native sharing path under the unified media contract', () => {
  assert.equal(
    selectGameCastSharePath({
      roomId: 'room-web',
      selfPeerId: GAMECAST_VIEWER_SELF_PEER_ID,
      roomEntry: 'web-joined-viewer',
      roomOrigin: 'web',
      mediaMode: 'sfu',
      mediaProtocolVersion: 1,
      role: 'viewer',
      hostVideoCapable: true,
      sourceKind: 'window',
    }),
    'native-sfu',
  );
});

test('client rooms choose native sharing even after the owner switched to viewer', () => {
  assert.equal(
    selectGameCastSharePath({
      roomId: 'room-client',
      selfPeerId: GAMECAST_VIEWER_SELF_PEER_ID,
      roomEntry: 'client-joined-viewer',
      roomOrigin: 'client',
      mediaMode: 'sfu',
      mediaProtocolVersion: 1,
      role: 'viewer',
      hostVideoCapable: true,
      sourceKind: 'display',
    }),
    'native-sfu',
  );
});

test('native capability and the unified media contract gate client sharing', () => {
  const unified = { mediaMode: 'sfu', mediaProtocolVersion: 1 };
  assert.equal(canStartGameCastSharing(GAMECAST_VIEWER_SELF_PEER_ID, false, unified), false);
  assert.equal(canStartGameCastSharing(GAMECAST_VIEWER_SELF_PEER_ID, true, unified), true);
  assert.equal(canStartGameCastSharing(GAMECAST_HOST_SELF_PEER_ID, true, unified), true);
  assert.equal(canStartGameCastSharing(GAMECAST_HOST_SELF_PEER_ID, true, { mediaMode: 'legacy-p2p', mediaProtocolVersion: 0 }), true);
});

test('legacy P2P rooms select native P2P sharing and viewing without SFU flags', () => {
  const room = {
    active: true,
    sharing: true,
    mediaMode: 'legacy-p2p',
    mediaProtocolVersion: 0,
    sharePhase: 'live',
    sfu: false,
  };
  assert.equal(
    selectGameCastSharePath({
      roomId: 'legacy-room',
      selfPeerId: GAMECAST_VIEWER_SELF_PEER_ID,
      roomEntry: 'web-joined-viewer',
      mediaMode: room.mediaMode,
      mediaProtocolVersion: room.mediaProtocolVersion,
      role: 'viewer',
      hostVideoCapable: true,
      sourceKind: 'display',
    }),
    'native-p2p',
  );
  assert.equal(isGameCastRoomLiveForViewer(room), true);
  assert.equal(isGameCastRoomLiveForViewer({ ...room, mediaMode: 'unknown' }), false);
});

test('room origin never routes normal joins to the browser', () => {
  assert.equal(selectGameCastJoinAction('web'), 'native-viewer');
  assert.equal(selectGameCastJoinAction(''), 'native-viewer');
  assert.equal(selectGameCastJoinAction(undefined), 'native-viewer');
  assert.equal(selectGameCastJoinAction('client'), 'native-viewer');
  assert.equal(gameCastRoomOriginLabelKey('web'), 'gamecast.originWeb');
  assert.equal(gameCastRoomOriginLabelKey('client'), 'gamecast.originClient');
  assert.equal(gameCastRoomOriginLabelKey(''), 'gamecast.originCompat');
});

test('control presence keeps same-account client and web participants distinct', () => {
  const members = mergeGameCastPresenceMembers(
    [
      { participantId: 'client-self', name: 'Ashen', clientType: 'client', role: 'owner', active: true, sharing: false },
      { participantId: 'web-1', name: 'Ashen', clientType: 'web', role: 'member', active: true, sharing: true },
      { participantId: 'web-1', name: 'Ashen', clientType: 'web', role: 'member', active: true, sharing: true },
    ],
    'client-self',
    GAMECAST_HOST_SELF_PEER_ID,
  );
  assert.equal(members.length, 2);
  assert.deepEqual(members[0], {
    id: GAMECAST_HOST_SELF_PEER_ID,
    name: 'Ashen',
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

test('normal room joins never call BrowserOpenURL and Presence polling is origin agnostic', () => {
  const joinStart = appSource.indexOf('const handleJoinGameCastRoom');
  const joinEnd = appSource.indexOf('const handleDisconnectGameCast', joinStart);
  const joinSource = appSource.slice(joinStart, joinEnd);
  assert.ok(joinStart >= 0 && joinEnd > joinStart);
  assert.match(joinSource, /handleJoinGameCastViewer/);
  assert.doesNotMatch(joinSource, /BrowserOpenURL/);

  const presenceStart = appSource.indexOf('async function pollPresence');
  const presenceEnd = appSource.indexOf('\n  useEffect(', presenceStart);
  const presenceSource = appSource.slice(presenceStart, presenceEnd);
  assert.ok(presenceStart >= 0 && presenceEnd > presenceStart);
  assert.match(presenceSource, /GetGameCastPresence\(roomId\)/);
  assert.match(presenceSource, /}, 3000\)/);
  assert.doesNotMatch(presenceSource, /origin\s*!==\s*['"]client['"]/);
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
    roomMediaMode: 'sfu',
    roomMediaProtocolVersion: 1,
    roomSharePhase: 'live',
    roomSFU: true,
    activeSharePath: null,
    statusState: 'error',
    receiverState: 'error',
  };

  assert.equal(shouldReconnectGameCastViewer({ ...base, attempt: 0 }), true);
  assert.equal(shouldReconnectGameCastViewer({ ...base, attempt: 3 }), false);
  assert.equal(shouldReconnectGameCastViewer({ ...base, attempt: 0, roomSharing: false }), false);
  assert.equal(shouldReconnectGameCastViewer({ ...base, attempt: 0, roomSharePhase: 'starting', roomSFU: false }), false);
  assert.equal(shouldReconnectGameCastViewer({ ...base, attempt: 0, roomSharePhase: 'stopping', roomSFU: false }), false);
  assert.equal(shouldReconnectGameCastViewer({ ...base, attempt: 0, roomMediaMode: 'legacy-p2p' }), false);
});

test('viewer reconnects once sharing starts after the original signaling wait expired', () => {
  const viewerBeforeHost = {
    roomId: 'late-host-room',
    selfPeerId: GAMECAST_VIEWER_SELF_PEER_ID,
    role: 'viewer',
    roomActive: true,
    roomSharing: false,
    roomMediaMode: 'sfu',
    roomMediaProtocolVersion: 1,
    roomSharePhase: 'idle',
    roomSFU: false,
    activeSharePath: null,
    statusState: 'connected',
    receiverState: 'idle',
    attempt: 0,
  };

  assert.equal(shouldReconnectGameCastViewer(viewerBeforeHost), false);
  assert.equal(
    shouldReconnectGameCastViewer({
      ...viewerBeforeHost,
      roomSharing: true,
      roomSharePhase: 'live',
      roomSFU: true,
    }),
    true,
  );
  assert.equal(
    shouldReconnectGameCastViewer({
      ...viewerBeforeHost,
      roomSharing: true,
      roomSharePhase: 'live',
      roomSFU: true,
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
      activeSharePath: 'native-sfu',
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
      activeSharePath: 'native-p2p',
      currentRoomId: 'room-b',
      eventRoomId: 'room-a',
    }),
    false,
  );
});
