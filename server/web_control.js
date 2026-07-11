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

function desiredSfuRole(roomState, participantId, hasLocalStream) {
  if (!roomState || !roomState.active || !roomState.sharing || !roomState.sfu) return '';
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
    committed = true;
    return { lease: lease, stream: stream, result: result };
  } finally {
    if (!committed) await steps.release(lease);
  }
}

module.exports = {
  decidePresenceJoin: decidePresenceJoin,
  mergePresence: mergePresence,
  canStartShare: canStartShare,
  desiredSfuRole: desiredSfuRole,
  runShareStart: runShareStart
};
