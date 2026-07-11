import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

const inject = await readFile(new URL('./screego/deploy/inject.js', import.meta.url), 'utf8');
const nginx = await readFile(new URL('./screego/deploy/nginx-screego.conf', import.meta.url), 'utf8');

test('adaptive policy loads before inject', () => {
  const replacement = nginx.match(/sub_filter '<\/head>' '([^']+)<\/head>'/);
  assert.ok(replacement);
  const adaptiveIndex = replacement[1].indexOf('/webrtc-adaptive.js');
  const injectIndex = replacement[1].indexOf('/inject.js');
  assert.ok(adaptiveIndex >= 0);
  assert.ok(injectIndex >= 0 && adaptiveIndex < injectIndex);
});

test('single-viewer sharing uses per-peer adaptive state', () => {
  assert.match(inject, /pc\._gcAdaptiveState\s*=\s*GameCastWebRTCAdaptive\.createAdaptiveState/);
  assert.match(inject, /function shouldUseAdaptiveStreaming\(pc\)/);
  assert.doesNotMatch(inject, /getConnectedVideoSenderPeerCount\(\) > 1/);
});

test('monitor samples every connected video sender independently', () => {
  assert.match(inject, /getAdaptivePeerDescriptors\(\)\.forEach/);
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
