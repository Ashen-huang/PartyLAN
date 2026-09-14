'use strict';

function decidePresenceJoin(roomState) {
  if (roomState && roomState.active) {
    return { role: 'member', origin: '' };
  }
  return { role: 'owner', origin: 'web' };
}

function mergePresence(participants) {
  var seen = Object.create(null);
  var result = [];
  (Array.isArray(participants) ? participants : []).forEach(function (participant) {
    var participantId = String(participant && participant.participantId || '').trim();
    if (!participantId || !participant.active || seen[participantId]) return;
    seen[participantId] = true;
    result.push({
      participantId: participantId,
      name: String(participant.name || participantId),
      clientType: participant.clientType === 'client' ? 'client' : 'web',
      role: String(participant.role || 'member'),
      active: true,
      sharing: !!participant.sharing
    });
  });
  return result;
}

function canStartShare(roomState, participantId) {
  if (!roomState || !roomState.sharing) return true;
  return !!participantId && roomState.shareOwnerParticipantId === participantId;
}

function isUnifiedSfuRoom(roomState) {
  return !!(
    roomState &&
    roomState.active &&
    roomState.mediaMode === 'sfu' &&
    Number(roomState.mediaProtocolVersion) === 1
  );
}

function webMediaContract() {
  return {
    mediaProtocolVersion: 1,
    capabilities: {
      sfuViewer: true,
      sfuHost: true,
      h264: true,
      opus: true
    }
  };
}

function desiredSfuRole(roomState, participantId, hasLocalStream) {
  if (
    !isUnifiedSfuRoom(roomState) ||
    !roomState.sharing ||
    roomState.sharePhase !== 'live' ||
    !roomState.sfu
  ) return '';
  if (hasLocalStream && roomState.shareOwnerParticipantId === participantId) return 'host';
  if (roomState.shareOwnerParticipantId !== participantId) return 'viewer';
  return '';
}

async function runShareStart(steps) {
  var lease = await steps.claim();
  var committed = false;
  try {
    var stream = await steps.capture(lease);
    var result = await steps.publish(stream, lease);
    if (steps.markLive) await steps.markLive(lease, stream, result);
    committed = true;
    return { lease: lease, stream: stream, result: result };
  } finally {
    if (!committed) await steps.release(lease);
  }
}

async function runShareStop(steps) {
  await steps.markStopping();
  await steps.closeMedia();
  await steps.release();
}

function canSendProtectedControl(loggedIn, user) {
  return !!loggedIn && !!String(user || '').trim() && String(user).trim() !== 'guest';
}

function shouldPostRoomState(previousSignature, state, now, lastPostAt, heartbeatMs) {
  var signature = JSON.stringify(state || {});
  return signature !== previousSignature || Number(now) - Number(lastPostAt || 0) >= Number(heartbeatMs || 0);
}

module.exports = {
  decidePresenceJoin: decidePresenceJoin,
  mergePresence: mergePresence,
  canStartShare: canStartShare,
  isUnifiedSfuRoom: isUnifiedSfuRoom,
  desiredSfuRole: desiredSfuRole,
  runShareStart: runShareStart,
  runShareStop: runShareStop,
  webMediaContract: webMediaContract,
  canSendProtectedControl: canSendProtectedControl,
  shouldPostRoomState: shouldPostRoomState
};
