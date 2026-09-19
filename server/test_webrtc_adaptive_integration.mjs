import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

const inject = await readFile(new URL('./screego/deploy/inject.js', import.meta.url), 'utf8');
const nginx = await readFile(new URL('./screego/deploy/nginx-screego.conf', import.meta.url), 'utf8');
const sourcePolicy = await readFile(new URL('./webrtc_adaptive_policy.js', import.meta.url), 'utf8');
const deployedPolicy = await readFile(new URL('./screego/deploy/webrtc-adaptive.js', import.meta.url), 'utf8');

test('source and deployed adaptive policies are byte-identical', () => {
  assert.equal(deployedPolicy, sourcePolicy);
});

test('adaptive policy loads before inject', () => {
  const replacement = nginx.match(/sub_filter '<\/head>' '([^']+)<\/head>'/);
  assert.ok(replacement);
  const adaptiveIndex = replacement[1].indexOf('/webrtc-adaptive.js');
  const injectIndex = replacement[1].indexOf('/inject.js');
  assert.ok(adaptiveIndex >= 0);
  assert.ok(injectIndex >= 0 && adaptiveIndex < injectIndex);
});

test('single-viewer sharing uses per-peer adaptive state', () => {
  assert.match(inject, /pc\._gcAdaptiveState\s*=\s*createCurrentAdaptiveState/);
  assert.match(inject, /function shouldUseAdaptiveStreaming\(pc\)/);
  assert.doesNotMatch(inject, /getConnectedVideoSenderPeerCount\(\) > 1/);
});

test('test-room baseline bypasses custom sender parameters but keeps policy telemetry', () => {
  assert.match(inject, /resolveAdaptiveMode\(currentRoomId\(\), location\.search/);
  assert.match(inject, /function browserBWEBaselineEnabled\(\)/);
  const applyBitrate = inject.slice(
    inject.indexOf('function applyBitrate(pc)'),
    inject.indexOf('function requestPeerIceRecovery'),
  );
  assert.match(applyBitrate, /if \(browserBWEBaselineEnabled\(\)\) return Promise\.resolve\(false\)/);
  assert.match(inject, /state\.policyVersion\s*=\s*currentAdaptivePolicyMode\(\)/);
  assert.match(inject, /adaptivePolicyVersion:\s*adaptiveState\.policyVersion/);
});

test('recovery samples include the next higher profile bitrate', () => {
  assert.match(inject, /computePeerAdaptiveProfile\(pc, adaptiveState\.level - 1\)/);
  assert.match(inject, /nextProfileKbps:\s*nextAdaptiveProfile/);
  assert.match(inject, /nominalKbps:\s*adaptiveProfile/);
  assert.match(inject, /degradeFloorKbps:\s*adaptiveProfile/);
  assert.match(inject, /nextNominalKbps:\s*nextAdaptiveProfile/);
  assert.match(inject, /nextDegradeFloorKbps:\s*nextAdaptiveProfile/);
  assert.match(inject, /maxLevel:\s*adaptiveProfile/);
});

test('adaptive sampling supplies content demand and transport congestion signals', () => {
  const sampler = inject.slice(
    inject.indexOf('function sampleAdaptiveStreamingPeer'),
    inject.indexOf('function pauseAdaptiveStreamingEvidence()'),
  );
  for (const field of [
    'totalPacketSendDelay',
    'retransmittedPacketsSent',
    'qualityLimitationDurations',
    'qpSum',
    'totalEncodeTime',
    'senderKbitsPerFrame',
    'senderPacketSendDelayMs',
    'senderRetransmitPercent',
    'bandwidthLimitedMsDelta',
    'senderQpPerFrame',
    'senderEncodeMsPerFrame',
  ]) {
    assert.match(sampler, new RegExp(field));
  }
  assert.match(sampler, /profileMaxFramerate:\s*adaptiveProfile/);
});

test('adaptive sampler reads the peer mode from its descriptor', () => {
  const sampler = inject.slice(
    inject.indexOf('function sampleAdaptiveStreamingPeer'),
    inject.indexOf('function pauseAdaptiveStreamingEvidence()'),
  );
  assert.match(
    sampler,
    /function sampleAdaptiveStreamingPeer\(descriptor\)\s*\{\s*var pc = descriptor && descriptor\.pc;\s*var mode = descriptor && descriptor\.mode \|\| ['"]native-host['"];\s*/,
  );
  assert.match(sampler, /recoveryReceiverOptional:\s*mode === ['"]sfu-host['"] && !receiverQuality/);
});

test('soft protection is per-peer bitrate-only and exposes transition telemetry', () => {
  const applyBitrate = inject.slice(
    inject.indexOf('function applyBitrate(pc)'),
    inject.indexOf('function requestPeerIceRecovery', inject.indexOf('function applyBitrate(pc)')),
  );
  assert.match(applyBitrate, /softProtectionKbps/);
  assert.match(applyBitrate, /softProtectionBitrate/);
  assert.match(applyBitrate, /if \(softOnly\)/);
  assert.match(applyBitrate, /encoding\.maxBitrate\s*=\s*softProtectionBitrate/);
  const sampler = inject.slice(
    inject.indexOf('function sampleAdaptiveStreamingPeer'),
    inject.indexOf('function pauseAdaptiveStreamingEvidence()'),
  );
  assert.match(sampler, /previousSoftProtectionKbps/);
  assert.match(sampler, /applyAdaptiveProfile\(pc, 'soft:'/);
  const telemetry = [
    'adaptiveTargetBitrateKbps',
    'adaptiveSoftProtectionKbps',
    'adaptiveSoftProtectionUntil',
    'adaptiveLastTransitionAt',
    'adaptiveTargetLevel',
    'adaptiveTransitionReason',
  ];
  for (const field of telemetry) assert.match(inject, new RegExp(field));
});

test('share settings expose the 30, 45 and 60 fps ladder and migrate legacy values', () => {
  assert.match(inject, /var FPS_OPTIONS\s*=\s*\[30,\s*45,\s*60\]/);
  assert.match(inject, /FPS_OPTIONS\.reduce/);
  assert.match(inject, /s\.framerate\s*=\s*30/);
  assert.doesNotMatch(inject, /s\.framerate\s*=\s*35/);
});

test('the adaptive ladder is the only bitrate ceiling', () => {
  assert.match(inject, /function computeTargetBitrate\(\)\s*{\s*return maxBitrate\(\);\s*}/);
  assert.doesNotMatch(inject, /bitrateBaseForResolution/);
});

test('monitor samples every connected video sender independently', () => {
  assert.match(inject, /var descriptors\s*=\s*getAdaptivePeerDescriptors\(\)/);
  assert.match(inject, /descriptors\.forEach\(sampleAdaptiveStreamingPeer\)/);
  assert.match(inject, /setInterval\(sampleAdaptiveStreaming, 2000\)/);
  assert.match(inject, /evaluateAdaptiveSample\(adaptiveState, adaptiveSample\)/);
});

test('hidden share pages keep receiver polling and sender sampling active', () => {
  const sampler = inject.slice(
    inject.indexOf('function pauseAdaptiveStreamingEvidence()'),
    inject.indexOf('function startAdaptiveStreamingMonitor()'),
  );
  assert.doesNotMatch(sampler, /document\.visibilityState\s*===\s*'hidden'/);
  const refreshIndex = sampler.indexOf('refreshAdaptiveQualitySnapshot()');
  const sampleIndex = sampler.indexOf('descriptors.forEach(sampleAdaptiveStreamingPeer)');
  assert.ok(refreshIndex >= 0, 'receiver quality snapshots must keep refreshing');
  assert.ok(sampleIndex > refreshIndex, 'sender stats must be sampled after the snapshot refresh');
});

test('native peer sessions bind receiver reports to the exact sender connection', () => {
  assert.match(inject, /__PARTYLAN_REGISTER_PEER_SESSION__/);
  assert.match(inject, /QUALITY_SNAPSHOT_URL/);
  assert.match(inject, /screegoSessionId/);
  assert.match(inject, /receiverReportAgeMs/);
  assert.match(inject, /receiverReportId/);
  assert.match(inject, /receiverDecisionFresh/);
  assert.match(inject, /reportSeq/);
  assert.match(inject, /receiverDecodedFps/);
  assert.match(inject, /receiverIncomingKbps/);
  assert.match(inject, /receiverBufferTargetMs/);
  assert.match(inject, /receiverBufferActualMs/);
  assert.match(inject, /receiverFlowDeficit/);
  assert.match(inject, /senderCapturedFps/);
  assert.match(inject, /senderEncodedFps/);
  assert.doesNotMatch(inject, /receiverImpaired:\s*false/);
});

test('native viewers apply bounded jitter buffering before sender quality downshift', () => {
  assert.match(inject, /function ensureReceiverBufferTarget\(pc\)/);
  assert.match(inject, /receiver\.jitterBufferTarget\s*=\s*targetMs/);
  assert.match(inject, /receiver\.playoutDelayHint\s*=\s*targetMs\s*\/\s*1000/);
  assert.match(inject, /evaluateReceiverBufferSample\(state, bufferSample\)/);
  assert.match(inject, /bufferTargetMs:\s*effectiveReceiverBuffer/);
  assert.match(sourcePolicy, /RECEIVER_BUFFER_STEPS_MS\s*=\s*\[80, 150, 220, 300\]/);
  assert.match(sourcePolicy, /receiverBufferBlocksDownshift\(sample\)/);
  assert.match(sourcePolicy, /state\.status\s*=\s*'receiver-buffering'/);
});

test('native viewer keeps actual buffer telemetry distinct from the 300ms target cap', () => {
  assert.match(inject, /Math\.min\(60000, Number\(sample\.bufferActualMs/);
  assert.match(inject, /mode === 'sfu-viewer' \|\| mode === 'sfu-host'[\s\S]*媒体连接: 已建立/);
  assert.match(inject, /Number\(rttNum \|\| 0\) \/ 2/);
  assert.doesNotMatch(inject, /compactDelay \+= ' \(\+'/);
});

test('SFU viewer quality reports use the session-level identifier when available', () => {
  assert.match(inject, /getSessionId\(\)/);
  assert.match(inject, /mode\s*===\s*['"]sfu-viewer['"][\s\S]{0,220}getSessionId/);
  assert.match(inject, /participantId:\s*getWebParticipantId\(\)/);
});

test('a peer profile applies bitrate, frame rate and resolution scaling only to that sender', () => {
  assert.match(inject, /encoding\.scaleResolutionDownBy\s*=\s*profile\.scaleResolutionDownBy/);
  assert.match(inject, /encoding\.maxFramerate\s*=\s*profile\.maxFramerate/);
  assert.match(inject, /encoding\.maxBitrate\s*=\s*profile\.maxBitrate/);
  assert.match(inject, /applyAdaptiveProfile\(pc,/);
  assert.doesNotMatch(inject, /function applyAdaptiveStreamingProfile\(\)[\s\S]*?applyLiveConstraints\(\)/);
});

test('the selected sharing mode controls browser degradation preference', () => {
  const applyBitrate = inject.slice(
    inject.indexOf('function applyBitrate(pc)'),
    inject.indexOf('function requestPeerIceRecovery'),
  );
  assert.match(applyBitrate, /var qualityMode\s*=\s*QUALITY\[c\.qm\]\s*\|\|\s*QUALITY\.balanced/);
  assert.match(applyBitrate, /p\.degradationPreference\s*=\s*qualityMode\.pref/);
  assert.doesNotMatch(applyBitrate, /p\.degradationPreference\s*=\s*'maintain-framerate'/);
});

test('adaptive decisions are reported as edge events with profile metadata', () => {
  assert.match(inject, /adaptiveDecision:\s*adaptiveDecision/);
  assert.match(inject, /adaptiveLevel:\s*adaptiveState\.level/);
  assert.match(inject, /adaptiveScale:\s*adaptiveProfile\.scaleResolutionDownBy/);
  assert.match(inject, /adaptiveNominalBitrate:\s*adaptiveProfile\.nominalBitrate/);
  assert.match(inject, /adaptiveDegradeFloorBitrate:\s*adaptiveProfile\.degradeFloorBitrate/);
  assert.match(inject, /adaptiveConfiguredFps:\s*extCfg\(\)\.fps/);
  assert.match(inject, /visible:\s*document\.visibilityState\s*!==\s*'hidden'/);
  assert.match(inject, /adaptiveSafeCapacityKbps:\s*adaptiveState\.safeCapacityKbps/);
  assert.match(inject, /adaptiveRecoveryCapacityKbps:\s*adaptiveState\.recoveryCapacityKbps/);
  assert.match(inject, /adaptiveRecoveryHealthySamples:\s*adaptiveState\.recoveryHealthySamples/);
  assert.match(inject, /adaptiveRecoveryGateReason:\s*adaptiveState\.recoveryGateReason/);
  assert.match(inject, /adaptiveRecoveryTargetFloorKbps:\s*adaptiveState\.recoveryTargetFloorKbps/);
  assert.match(inject, /adaptiveContentState:\s*adaptiveState\.contentState/);
});

test('capture constraints are reapplied after display capture and stats summary is compact', () => {
  assert.match(inject, /applyCaptureConstraintsWithFallback\(activeVideoTrack, c\)/);
  assert.match(inject, /width:\s*\{ ideal: p\.w, max: p\.w \}/);
  assert.match(inject, /height:\s*\{ ideal: p\.h, max: p\.h \}/);
  const activation = inject.slice(
    inject.indexOf('function activateCapturedDisplayStream('),
    inject.indexOf('function primeDisplayMediaRequestForShare('),
  );
  assert.match(inject, /applyLiveConstraints\(\)/);
  const stats = inject.slice(
    inject.indexOf('function updateStats('),
    inject.indexOf('function getConnectionStatsDescriptors'),
  );
  assert.match(
    stats,
    /publishConnectionStats\(\s*statsText,\s*resolution \+ ' · ' \+ fps \+ ' · ' \+ compactDelay \+ ' · ' \+ formatKbps\(downKbpsValue\)/,
  );
  const compactSummary = stats.slice(
    stats.indexOf('publishConnectionStats(\n        statsText,'),
    stats.indexOf('pc._gcStatsReportCounter', stats.indexOf('publishConnectionStats(\n        statsText,')),
  );
  assert.doesNotMatch(compactSummary, /分辨率|帧数|延迟|下行/);
});

test('capture telemetry records requested and actual settings and fallback order', () => {
  assert.match(inject, /actualWidth/);
  assert.match(inject, /actualHeight/);
  assert.match(inject, /actualFrameRate/);
  assert.match(inject, /requestedWidth/);
  assert.match(inject, /requestedHeight/);
  assert.match(inject, /requestedFrameRate/);
  assert.match(inject, /captureConstraintStatus/);
  assert.match(inject, /1080p45/);
  assert.match(inject, /720p45/);
  assert.match(inject, /540p45/);
  assert.match(inject, /1080p30/);
  assert.match(inject, /720p60/);
  assert.match(inject, /720p30/);
  assert.match(inject, /getSettings\(\)/);
  assert.doesNotMatch(inject, /actualFrameRate\s*:\s*extCfg\(\)\.fps/);
});

test('relay load diagnostics use the active adaptive ceiling instead of the configured target', () => {
  const diagnostics = inject.slice(
    inject.indexOf('var adaptiveTargetKbpsValue'),
    inject.indexOf('var serverLoadRatio', inject.indexOf('var adaptiveTargetKbpsValue')),
  );
  assert.match(
    diagnostics,
    /currentFlowKbps\s*=\s*Math\.max\(adaptiveTargetKbpsValue,\s*upKbpsValue\s*\|\|\s*0,\s*downKbpsValue\s*\|\|\s*0\)/,
  );
  assert.doesNotMatch(diagnostics, /Math\.max\(targetKbpsValue,/);
});

test('adaptive budget uses the Guangzhou server profile and only relayed paths', () => {
  assert.match(inject, /var SERVER_PROFILE\s*=\s*{\s*cores:\s*2,\s*ramGB:\s*2,\s*netMbps:\s*200,\s*safeNetMbps:\s*160\s*}/);
  assert.match(inject, /function isServerBudgetedAdaptivePeer\(descriptor\)/);
  assert.match(inject, /descriptor\.mode\s*===\s*'sfu-host'/);
  assert.match(inject, /_gcAdaptiveLastSample\.path\s*===\s*'turn'/);
  assert.match(inject, /applyServerBudget:\s*applyServerBudget/);
  assert.match(inject, /viewerCount:\s*getServerBudgetedAdaptivePeerCount\(\)/);
});

test('SFU host sender parameters are owned by the flow-first uplink controller', () => {
  const applyBitrate = inject.slice(
    inject.indexOf('function applyBitrate(pc)'),
    inject.indexOf('function requestPeerIceRecovery', inject.indexOf('function applyBitrate(pc)')),
  );
  assert.match(applyBitrate, /pc\s*===\s*getSfuPeerConnection\(\)/);
  assert.match(applyBitrate, /GC_SFU\.sampleHostUplink\(true\)/);
  assert.ok(
    applyBitrate.indexOf('GC_SFU.sampleHostUplink(true)') < applyBitrate.indexOf('sender.getParameters()'),
    'the generic adaptive writer must delegate before touching SFU sender encodings',
  );
});

test('first known or changed path reapplies the peer profile', () => {
  assert.match(inject, /previousAdaptivePath\s*!==\s*adaptiveSample\.path/);
  assert.match(inject, /applyAdaptiveProfile\(pc,\s*'path:'/);
});

test('server budget count changes reapply every relayed peer profile', () => {
  assert.match(inject, /var adaptiveServerBudgetPeerCount\s*=\s*0/);
  assert.match(inject, /function countServerBudgetedAdaptivePeers\(descriptors\)/);
  const sampler = inject.slice(
    inject.indexOf('function sampleAdaptiveStreaming()'),
    inject.indexOf('function startAdaptiveStreamingMonitor()'),
  );
  assert.match(sampler, /budgetedPeerCount\s*!==\s*adaptiveServerBudgetPeerCount/);
  assert.match(sampler, /isServerBudgetedAdaptivePeer\(descriptor\)/);
  assert.match(sampler, /applyAdaptiveProfile\(\s*descriptor\.pc,\s*'server-budget:'/);
});

test('share lifecycle reset clears quality and ICE protection state', () => {
  const reset = inject.slice(
    inject.indexOf('function clearPeerQualityState'),
    inject.indexOf('function getSfuPeerConnection'),
  );
  assert.match(reset, /pc\._gcQualityState\s*=\s*null/);
  assert.match(reset, /pc\._gcProtectionMaxBitrate\s*=\s*0/);
  assert.match(reset, /pc\._gcProtectionMaxWidth\s*=\s*0/);
  assert.match(reset, /pc\._gcProtectionMaxHeight\s*=\s*0/);
  assert.match(reset, /pc\._gcProtectionRecoveryStep\s*=\s*0/);
  assert.match(inject, /closeActivePeerConnections\(\s*'share-stop'\s*\)/);
});

test('peer lifecycle removes replaced, disconnected and orphaned sessions individually', () => {
  assert.match(inject, /function cleanupPeerConnectionState\(pc,/);
  assert.match(inject, /cleanupPeerConnectionState\(existingPeer,\s*true,\s*'session-replaced'\)/);
  assert.match(inject, /cleanupPeerConnectionState\(pc,\s*false,\s*'session-close'\)/);
  assert.match(inject, /PEER_DISCONNECTED_GRACE_MS/);
  assert.match(inject, /_gcOrphanedSince/);
  assert.match(inject, /reconcileOrphanedPeerConnections\(\)/);
});

test('sharing presence loss self-heals before stopping media', () => {
  assert.match(inject, /function recoverWebPresence\(roomId\)/);
  assert.match(inject, /activeDisplayStream\s*&&[\s\S]*!hasWebPresenceParticipant/);
  assert.match(inject, /recoverWebPresence\(roomId\)/);
  assert.match(inject, /PRESENCE_RECOVERY_MAX_FAILURES/);
  assert.match(inject, /stopWebShare\(false\)/);
  assert.match(inject, /webPresenceRecoveryPromise/);
  assert.match(inject, /!activeDisplayStream\s*\|\|\s*webPresenceRecoveryPromise/);
});

test('expired share leases stop media and clear the session lifecycle', () => {
  assert.match(inject, /share_lease_expired[\s\S]*share_lease_invalid/);
  assert.match(
    inject,
    /function invalidateWebShareState\(\)[\s\S]*stopWebShare\(false,\s*['"]share-control-invalid['"],\s*true\)/,
  );
  assert.match(inject, /closeActivePeerConnections\('share-stop'\)/);
});

test('legacy room-state writes are restricted to the owner and suppressed during sharing', () => {
  const ownership = inject.slice(
    inject.indexOf('function ownsCurrentRoomState('),
    inject.indexOf('function currentSfuMode('),
  );
  assert.match(ownership, /webPresenceRole\s*===\s*['"]owner['"]/);
  assert.match(ownership, /webShareLease/);
  assert.match(ownership, /sameRoomId/);

  const roomState = inject.slice(
    inject.indexOf('function postRoomState('),
    inject.indexOf('function sendRoomStateBeacon('),
  );
  assert.match(roomState, /canMaintainRoomState\(roomId\)/);
  assert.match(roomState, /webShareLease/);
});

test('invalid share control state tears down every local media session', () => {
  const heartbeat = inject.slice(
    inject.indexOf('function heartbeatWebShare('),
    inject.indexOf('function startWebShareHeartbeat('),
  );
  assert.match(heartbeat, /participant_not_found/);
  assert.match(heartbeat, /invalidateWebShareState\(\)/);
  assert.match(inject, /function invalidateWebShareState\(\)/);
  assert.match(inject, /stopWebShare\(false,\s*['"]share-control-invalid['"]\s*,\s*true\)/);
  assert.match(inject, /closeReason\s*===\s*['"]share-control-invalid['"][\s\S]*notifyDisplayStreamEnded\(stream\)/);
});
