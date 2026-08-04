import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import test from 'node:test';

const require = createRequire(import.meta.url);
const {
  POLICY_VERSION,
  createAdaptiveState,
  evaluateAdaptiveSample,
  computeEncodingProfile,
  computeSenderDeltas,
} = require('./webrtc_adaptive_policy.js');

const good = (now, overrides = {}) => ({
  now,
  valid: true,
  pairId: 'pair-a',
  generation: 0,
  lossPercent: 0.2,
  jitterMs: 10,
  outboundKbps: 5000,
  profileKbps: 6000,
  availableOutgoingBitrate: 8000000,
  qualityLimitationReason: 'none',
  ...overrides,
});

const bad = (now, overrides = {}) => good(now, {
  lossPercent: 7,
  jitterMs: 140,
  outboundKbps: 1800,
  availableOutgoingBitrate: 1600000,
  qualityLimitationReason: 'bandwidth',
  ...overrides,
});

function evaluateMany(state, samples) {
  let result = {state, decision: null};
  for (const sample of samples) result = evaluateAdaptiveSample(result.state, sample);
  return result;
}

test('observation and a short spike do not downshift', () => {
  let state = createAdaptiveState(0);
  let result = evaluateMany(state, [
    bad(2000), bad(4000), bad(6000), bad(8000), bad(10000), bad(12000), bad(14000),
  ]);
  assert.equal(result.state.level, 0);
  assert.equal(result.decision, null);

  state = createAdaptiveState(0);
  result = evaluateMany(state, [
    good(16000), bad(18000), bad(20000), good(22000), good(24000), bad(26000), good(28000),
  ]);
  assert.equal(result.state.level, 0);
  assert.equal(result.decision, null);
});

test('sustained degradation descends one level at a time through 360p', () => {
  let state = createAdaptiveState(0);
  let result = evaluateMany(state, [16000, 18000, 20000, 22000, 24000, 26000, 28000].map(bad));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});
  assert.equal(result.state.level, 1);
  assert.equal(result.state.policyVersion, POLICY_VERSION);

  result = evaluateMany(result.state, [50000, 52000, 54000, 56000, 58000, 60000, 62000].map((now) => bad(now, {profileKbps: 3000})));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 1, toLevel: 2});
  assert.equal(result.state.level, 2);

  result = evaluateMany(result.state, [84000, 86000, 88000, 90000, 92000, 94000, 96000].map((now) => bad(now, {profileKbps: 1700})));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 2, toLevel: 3});
  assert.equal(result.state.level, 3);

  result = evaluateMany(result.state, [118000, 120000, 122000, 124000, 126000, 128000, 130000].map((now) => bad(now, {profileKbps: 1000})));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 3, toLevel: 4});
  assert.equal(result.state.level, 4);
});

test('low available bitrate triggers without high loss', () => {
  const result = evaluateMany(
    createAdaptiveState(0),
    [16000, 18000, 20000, 22000, 24000, 26000, 28000].map((now) => good(now, {
      lossPercent: 0.2,
      jitterMs: 10,
      outboundKbps: 5000,
      profileKbps: 6000,
      availableOutgoingBitrate: 3000000,
      qualityLimitationReason: 'none',
    })),
  );
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});
});

test('browser bandwidth limitation triggers without high loss', () => {
  const result = evaluateMany(
    createAdaptiveState(0),
    [16000, 18000, 20000, 22000, 24000, 26000, 28000].map((now) => good(now, {
      lossPercent: 0.2,
      jitterMs: 10,
      outboundKbps: 5000,
      profileKbps: 6000,
      availableOutgoingBitrate: 8000000,
      qualityLimitationReason: 'bandwidth',
    })),
  );
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});
});

test('invalid samples and low throughput alone do not downshift', () => {
  let state = createAdaptiveState(0);
  let result = evaluateMany(state, [16000, 18000, 20000, 22000, 24000, 26000, 28000].map((now) => bad(now, {
    valid: false,
    invalidReason: 'page-hidden',
  })));
  assert.equal(result.state.level, 0);
  assert.equal(result.state.window.length, 0);

  state = createAdaptiveState(0);
  result = evaluateMany(state, [16000, 18000, 20000, 22000, 24000, 26000, 28000].map((now) => good(now, {
    outboundKbps: 100,
    profileKbps: 6000,
    availableOutgoingBitrate: 8000000,
    qualityLimitationReason: 'none',
  })));
  assert.equal(result.state.level, 0);
  assert.equal(result.decision, null);
});

test('stable quality recovers only one level after thirty seconds', () => {
  let state = {...createAdaptiveState(0), level: 2, connectedAt: 0};
  let result = evaluateMany(state, [20000, 30000, 40000, 49999].map(good));
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 2);

  result = evaluateAdaptiveSample(result.state, good(50000));
  assert.deepEqual(result.decision, {type: 'recover', fromLevel: 2, toLevel: 1});
  assert.equal(result.state.level, 1);
});

test('recovery waits until available bitrate reaches the current profile', () => {
  const state = {...createAdaptiveState(0), level: 2, connectedAt: 0};
  const result = evaluateMany(
    state,
    [20000, 30000, 40000, 50000, 60000].map((now) => good(now, {
      profileKbps: 1700,
      availableOutgoingBitrate: 1200000,
    })),
  );
  assert.equal(result.state.level, 2);
  assert.equal(result.decision, null);
});

test('states are independent for a good and a bad viewer', () => {
  const badResult = evaluateMany(createAdaptiveState(0), [16000, 18000, 20000, 22000, 24000, 26000, 28000].map(bad));
  const goodResult = evaluateMany(createAdaptiveState(0), [16000, 18000, 20000, 22000, 24000, 26000, 28000].map(good));
  assert.equal(badResult.state.level, 1);
  assert.equal(goodResult.state.level, 0);
});

test('encoding profiles cap resolution, frame rate, bitrate and server budget', () => {
  assert.deepEqual(
    computeEncodingProfile({
      level: 1,
      sourceWidth: 1920,
      sourceHeight: 1080,
      configuredFps: 60,
      targetBitrate: 10800000,
      viewerCount: 1,
      serverSafeBitrate: 16000000,
    }),
    {level: 1, maxWidth: 1280, maxHeight: 720, maxFramerate: 24, maxBitrate: 3000000, scaleResolutionDownBy: 1.5},
  );
  assert.deepEqual(
    computeEncodingProfile({
      level: 2,
      sourceWidth: 1920,
      sourceHeight: 1080,
      configuredFps: 60,
      targetBitrate: 10800000,
      viewerCount: 1,
      serverSafeBitrate: 16000000,
    }),
    {level: 2, maxWidth: 960, maxHeight: 540, maxFramerate: 24, maxBitrate: 1700000, scaleResolutionDownBy: 2},
  );
  assert.deepEqual(
    computeEncodingProfile({
      level: 3,
      sourceWidth: 1920,
      sourceHeight: 1080,
      configuredFps: 60,
      targetBitrate: 10800000,
      viewerCount: 1,
      serverSafeBitrate: 160000000,
    }),
    {level: 3, maxWidth: 854, maxHeight: 480, maxFramerate: 20, maxBitrate: 1000000, scaleResolutionDownBy: 2.25},
  );
  assert.deepEqual(
    computeEncodingProfile({
      level: 4,
      sourceWidth: 1920,
      sourceHeight: 1080,
      configuredFps: 60,
      targetBitrate: 10800000,
      viewerCount: 1,
      serverSafeBitrate: 160000000,
    }),
    {level: 4, maxWidth: 640, maxHeight: 360, maxFramerate: 15, maxBitrate: 600000, scaleResolutionDownBy: 3},
  );

  const lowSource = computeEncodingProfile({
    level: 1,
    sourceWidth: 640,
    sourceHeight: 360,
    configuredFps: 35,
    targetBitrate: 1500000,
    viewerCount: 1,
    serverSafeBitrate: 16000000,
  });
  assert.equal(lowSource.scaleResolutionDownBy, 1);
  assert.equal(lowSource.maxBitrate, 1500000);

  const oneP2pViewer = computeEncodingProfile({
    level: 0,
    sourceWidth: 1920,
    sourceHeight: 1080,
    configuredFps: 35,
    targetBitrate: 10800000,
    viewerCount: 1,
    serverSafeBitrate: 160000000,
    applyServerBudget: false,
  });
  const twentyP2pViewers = computeEncodingProfile({
    level: 0,
    sourceWidth: 1920,
    sourceHeight: 1080,
    configuredFps: 35,
    targetBitrate: 10800000,
    viewerCount: 20,
    serverSafeBitrate: 160000000,
    applyServerBudget: false,
  });
  const twentyRelayedViewers = computeEncodingProfile({
    level: 0,
    sourceWidth: 1920,
    sourceHeight: 1080,
    configuredFps: 35,
    targetBitrate: 10800000,
    viewerCount: 20,
    serverSafeBitrate: 160000000,
    applyServerBudget: true,
  });
  assert.equal(oneP2pViewer.maxBitrate, 10800000);
  assert.equal(twentyP2pViewers.maxBitrate, 10800000);
  assert.equal(twentyRelayedViewers.maxBitrate, 8000000);

  const protectedViewer = computeEncodingProfile({
    level: 0,
    sourceWidth: 1920,
    sourceHeight: 1080,
    configuredFps: 60,
    targetBitrate: 10800000,
    viewerCount: 1,
    serverSafeBitrate: 16000000,
    protectionMaxWidth: 1280,
    protectionMaxHeight: 720,
    protectionMaxBitrate: 3000000,
  });
  assert.deepEqual(protectedViewer, {
    level: 0,
    maxWidth: 1280,
    maxHeight: 720,
    maxFramerate: 60,
    maxBitrate: 3000000,
    scaleResolutionDownBy: 1.5,
  });
});

test('sender deltas derive loss from packets sent when remote received is absent', () => {
  const result = computeSenderDeltas(
    {bytesSent: 1500000, packetsSent: 1100, packetsLost: 17, now: 4000},
    {bytesSent: 1000000, packetsSent: 1000, packetsLost: 10, now: 2000},
  );
  assert.equal(result.valid, true);
  assert.equal(result.outboundKbps, 2000);
  assert.equal(result.lossPercent, 7);

  assert.deepEqual(
    computeSenderDeltas(
      {bytesSent: 900, packetsSent: 90, packetsLost: 2, now: 4000},
      {bytesSent: 1000, packetsSent: 100, packetsLost: 3, now: 2000},
    ),
    {valid: false, invalidReason: 'counter-reset'},
  );
  assert.deepEqual(
    computeSenderDeltas(
      {bytesSent: 1500000, packetsSent: 1100, packetsLost: 17, now: 14000},
      {bytesSent: 1000000, packetsSent: 1000, packetsLost: 10, now: 2000},
    ),
    {valid: false, invalidReason: 'sample-gap'},
  );
});
