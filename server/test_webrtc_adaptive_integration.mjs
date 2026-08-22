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
  assert.match(inject, /maxLevel:\s*adaptiveProfile/);
});

test('share settings expose 30 45 and 60 fps and migrate the old 35 fps default', () => {
  assert.match(inject, /var FPS_OPTIONS\s*=\s*\[30,\s*45,\s*60\]/);
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

test('native peer sessions bind receiver reports to the exact sender connection', () => {
  assert.match(inject, /__PARTYLAN_REGISTER_PEER_SESSION__/);
  assert.match(inject, /QUALITY_SNAPSHOT_URL/);
  assert.match(inject, /screegoSessionId/);
  assert.match(inject, /receiverReportAgeMs/);
  assert.match(inject, /receiverDecodedFps/);
  assert.match(inject, /receiverIncomingKbps/);
  assert.match(inject, /senderCapturedFps/);
  assert.match(inject, /senderEncodedFps/);
  assert.doesNotMatch(inject, /receiverImpaired:\s*false/);
});

test('a peer profile applies bitrate, frame rate and resolution scaling only to that sender', () => {
  assert.match(inject, /encoding\.scaleResolutionDownBy\s*=\s*profile\.scaleResolutionDownBy/);
  assert.match(inject, /encoding\.maxFramerate\s*=\s*profile\.maxFramerate/);
  assert.match(inject, /encoding\.maxBitrate\s*=\s*profile\.maxBitrate/);
  assert.match(inject, /applyAdaptiveProfile\(pc,/);
  assert.doesNotMatch(inject, /function applyAdaptiveStreamingProfile\(\)[\s\S]*?applyLiveConstraints\(\)/);
});

test('adaptive decisions are reported as edge events with profile metadata', () => {
  assert.match(inject, /adaptiveDecision:\s*adaptiveDecision/);
  assert.match(inject, /adaptiveLevel:\s*adaptiveState\.level/);
  assert.match(inject, /adaptiveScale:\s*adaptiveProfile\.scaleResolutionDownBy/);
  assert.match(inject, /adaptiveNominalBitrate:\s*adaptiveProfile\.nominalBitrate/);
  assert.match(inject, /adaptiveDegradeFloorBitrate:\s*adaptiveProfile\.degradeFloorBitrate/);
});

test('adaptive budget uses the Guangzhou server profile and only relayed paths', () => {
  assert.match(inject, /var SERVER_PROFILE\s*=\s*{\s*cores:\s*2,\s*ramGB:\s*2,\s*netMbps:\s*200,\s*safeNetMbps:\s*160\s*}/);
  assert.match(inject, /function isServerBudgetedAdaptivePeer\(descriptor\)/);
  assert.match(inject, /descriptor\.mode\s*===\s*'sfu-host'/);
  assert.match(inject, /_gcAdaptiveLastSample\.path\s*===\s*'turn'/);
  assert.match(inject, /applyServerBudget:\s*applyServerBudget/);
  assert.match(inject, /viewerCount:\s*getServerBudgetedAdaptivePeerCount\(\)/);
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
