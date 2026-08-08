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
});

test('monitor samples every connected video sender independently', () => {
  assert.match(inject, /var descriptors\s*=\s*getAdaptivePeerDescriptors\(\)/);
  assert.match(inject, /descriptors\.forEach\(sampleAdaptiveStreamingPeer\)/);
  assert.match(inject, /setInterval\(sampleAdaptiveStreaming, 2000\)/);
  assert.match(inject, /evaluateAdaptiveSample\(adaptiveState, adaptiveSample\)/);
});

test('a peer profile applies bitrate, 24 fps and resolution scaling only to that sender', () => {
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
