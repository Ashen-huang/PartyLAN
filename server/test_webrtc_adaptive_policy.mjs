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
  resolveAdaptiveMode,
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

test('web sender ladder exposes burst nominal and degrade values for all high-source levels', () => {
  const options = {
    sourceWidth: 1920,
    sourceHeight: 1080,
    configuredFps: 60,
    targetBitrate: 50000000,
    viewerCount: 1,
    serverSafeBitrate: 160000000,
  };
  assert.deepEqual(computeEncodingProfile({...options, level: 0}), {
    level: 0, maxWidth: 1920, maxHeight: 1080, maxFramerate: 60,
    maxBitrate: 16000000, nominalBitrate: 12000000, degradeFloorBitrate: 7800000,
    maxLevel: 6, scaleResolutionDownBy: 1,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 1}), {
    level: 1, maxWidth: 1920, maxHeight: 1080, maxFramerate: 45,
    maxBitrate: 12000000, nominalBitrate: 9000000, degradeFloorBitrate: 5900000,
    maxLevel: 6, scaleResolutionDownBy: 1,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 2}), {
    level: 2, maxWidth: 1920, maxHeight: 1080, maxFramerate: 30,
    maxBitrate: 8000000, nominalBitrate: 6000000, degradeFloorBitrate: 3900000,
    maxLevel: 6, scaleResolutionDownBy: 1,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 3}), {
    level: 3, maxWidth: 1280, maxHeight: 720, maxFramerate: 30,
    maxBitrate: 6000000, nominalBitrate: 4500000, degradeFloorBitrate: 2900000,
    maxLevel: 6, scaleResolutionDownBy: 1.5,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 4}), {
    level: 4, maxWidth: 1280, maxHeight: 720, maxFramerate: 21,
    maxBitrate: 4000000, nominalBitrate: 3000000, degradeFloorBitrate: 2000000,
    maxLevel: 6, scaleResolutionDownBy: 1.5,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 5}), {
    level: 5, maxWidth: 960, maxHeight: 540, maxFramerate: 21,
    maxBitrate: 2500000, nominalBitrate: 1900000, degradeFloorBitrate: 1200000,
    maxLevel: 6, scaleResolutionDownBy: 2,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 6}), {
    level: 6, maxWidth: 640, maxHeight: 360, maxFramerate: 18,
    maxBitrate: 800000, nominalBitrate: 600000, degradeFloorBitrate: 400000,
    maxLevel: 6, scaleResolutionDownBy: 3,
  });
});

test('native source levels use the same 1440p and 4K bitrate caps as the client', () => {
  for (const source of [
    {width: 2560, height: 1440, bitrates: [24000000, 18000000, 12000000]},
    {width: 3840, height: 2160, bitrates: [40000000, 30000000, 20000000]},
  ]) {
    const options = {
      sourceWidth: source.width,
      sourceHeight: source.height,
      configuredFps: 60,
      targetBitrate: 50000000,
      viewerCount: 1,
      serverSafeBitrate: 160000000,
    };
    for (let level = 0; level < 3; level += 1) {
      const profile = computeEncodingProfile({...options, level});
      assert.equal(profile.maxWidth, source.width);
      assert.equal(profile.maxHeight, source.height);
      assert.equal(profile.maxBitrate, source.bitrates[level]);
    }
  }
});

test('native source levels never increase a low configured frame rate', () => {
  const profile = computeEncodingProfile({
    level: 0,
    sourceWidth: 1280,
    sourceHeight: 720,
    configuredFps: 24,
    targetBitrate: 20000000,
    viewerCount: 1,
    serverSafeBitrate: 20000000,
  });
  assert.deepEqual(profile, {
    level: 0,
    maxWidth: 1280,
    maxHeight: 720,
    maxFramerate: 24,
    maxBitrate: 4000000,
    nominalBitrate: 3000000,
    degradeFloorBitrate: 2000000,
    maxLevel: 3,
    scaleResolutionDownBy: 1,
  });
});

test('sustained pressure downshifts within six seconds', () => {
  const result = evaluateMany(createAdaptiveState(0), [2000, 4000, 6000].map(bad));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});
  assert.equal(result.state.level, 1);
});

test('browser congestion-control baseline is scoped to the test room', () => {
  assert.equal(resolveAdaptiveMode('test', '?adaptive=baseline'), 'browser-bwe-baseline-v1');
  assert.equal(resolveAdaptiveMode('other', '?adaptive=baseline'), POLICY_VERSION);
  assert.equal(resolveAdaptiveMode('test', '?adaptive=custom'), POLICY_VERSION);
});

test('observation and a short spike do not downshift', () => {
  let state = createAdaptiveState(0);
  let result = evaluateMany(state, [
    bad(2000), good(4000), bad(6000),
  ]);
  assert.equal(result.state.level, 0);
  assert.equal(result.decision, null);

  state = createAdaptiveState(0);
  result = evaluateMany(state, [
    good(16000), bad(18000), good(20000), bad(22000), good(24000), bad(26000), good(28000),
  ]);
  assert.equal(result.state.level, 0);
  assert.equal(result.decision, null);
});

test('sustained degradation descends one level at a time through all seven levels', () => {
  let state = createAdaptiveState(0);
  let result = evaluateMany(state, [2000, 4000, 6000].map(bad));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});
  assert.equal(result.state.level, 1);
  assert.equal(result.state.policyVersion, POLICY_VERSION);

  result = evaluateMany(result.state, [16000, 18000, 20000].map((now) => bad(now, {profileKbps: 8500})));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 1, toLevel: 2});
  assert.equal(result.state.level, 2);

  result = evaluateMany(result.state, [30000, 32000, 34000].map((now) => bad(now, {profileKbps: 6500})));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 2, toLevel: 3});
  assert.equal(result.state.level, 3);

  result = evaluateMany(result.state, [44000, 46000, 48000].map((now) => bad(now, {profileKbps: 4000})));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 3, toLevel: 4});
  assert.equal(result.state.level, 4);

  result = evaluateMany(result.state, [58000, 60000, 62000].map((now) => bad(now, {profileKbps: 3000})));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 4, toLevel: 5});
  assert.equal(result.state.level, 5);

  result = evaluateMany(result.state, [72000, 74000, 76000].map((now) => bad(now, {profileKbps: 2000})));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 5, toLevel: 6});
  assert.equal(result.state.level, 6);
});

test('low available bitrate requires matching low throughput without high loss', () => {
  const result = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000, 6000].map((now) => good(now, {
      lossPercent: 0.2,
      jitterMs: 10,
      outboundKbps: 3000,
      profileKbps: 6000,
      availableOutgoingBitrate: 3000000,
      qualityLimitationReason: 'none',
    })),
  );
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});
});

test('browser bandwidth limitation alone does not trigger without high loss', () => {
  const result = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000, 6000].map((now) => good(now, {
      lossPercent: 0.2,
      jitterMs: 10,
      outboundKbps: 5000,
      profileKbps: 6000,
      availableOutgoingBitrate: 8000000,
      qualityLimitationReason: 'bandwidth',
    })),
  );
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 0);
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

test('states are independent for a good and a bad viewer', () => {
  const badResult = evaluateMany(createAdaptiveState(0), [16000, 18000, 20000, 22000, 24000, 26000, 28000].map(bad));
  const goodResult = evaluateMany(createAdaptiveState(0), [16000, 18000, 20000, 22000, 24000, 26000, 28000].map(good));
  assert.equal(badResult.state.level, 1);
  assert.equal(goodResult.state.level, 0);
});

test('encoding profiles cap resolution, frame rate, bitrate and server budget', () => {
  const lowSource = computeEncodingProfile({
    level: 1,
    sourceWidth: 640,
    sourceHeight: 360,
    configuredFps: 35,
    targetBitrate: 1500000,
    viewerCount: 1,
    serverSafeBitrate: 16000000,
  });
  assert.equal(lowSource.maxWidth, 640);
  assert.equal(lowSource.maxHeight, 360);
  assert.equal(lowSource.scaleResolutionDownBy, 1);
  assert.equal(lowSource.maxBitrate, 1500000);

  const oneP2pViewer = computeEncodingProfile({
    level: 0,
    sourceWidth: 1920,
    sourceHeight: 1080,
    configuredFps: 60,
    targetBitrate: 10800000,
    viewerCount: 1,
    serverSafeBitrate: 160000000,
    applyServerBudget: false,
  });
  const twentyP2pViewers = computeEncodingProfile({
    level: 0,
    sourceWidth: 1920,
    sourceHeight: 1080,
    configuredFps: 60,
    targetBitrate: 10800000,
    viewerCount: 20,
    serverSafeBitrate: 160000000,
    applyServerBudget: false,
  });
  const twentyRelayedViewers = computeEncodingProfile({
    level: 0,
    sourceWidth: 1920,
    sourceHeight: 1080,
    configuredFps: 60,
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
    nominalBitrate: 2300000,
    degradeFloorBitrate: 1500000,
    maxLevel: 6,
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

test('720p sources use 60 45 30 21 fps bands before lower resolutions', () => {
  const options = {
    sourceWidth: 1280,
    sourceHeight: 720,
    configuredFps: 60,
    targetBitrate: 50000000,
    viewerCount: 1,
    serverSafeBitrate: 160000000,
  };
  const profiles = [0, 1, 2, 3, 4, 5].map((level) => computeEncodingProfile({...options, level}));
  assert.deepEqual(
    profiles.map(({maxWidth, maxHeight, maxFramerate, maxBitrate}) => [maxWidth, maxHeight, maxFramerate, maxBitrate]),
    [
      [1280, 720, 60, 12000000],
      [1280, 720, 45, 9000000],
      [1280, 720, 30, 6000000],
      [1280, 720, 21, 4000000],
      [960, 540, 21, 2500000],
      [640, 360, 18, 800000],
    ],
  );
  assert.ok(profiles.every((profile) => profile.maxLevel === 5));
});

test('high resolution source caps leave burst headroom for complex scenes', () => {
  for (const source of [
    {width: 1920, height: 1080, bitrates: [16000000, 12000000, 8000000]},
    {width: 2560, height: 1440, bitrates: [24000000, 18000000, 12000000]},
    {width: 3840, height: 2160, bitrates: [40000000, 30000000, 20000000]},
  ]) {
    const profiles = source.bitrates.map((_, level) => computeEncodingProfile({
      level,
      sourceWidth: source.width,
      sourceHeight: source.height,
      configuredFps: 60,
      targetBitrate: 50000000,
      viewerCount: 1,
      serverSafeBitrate: 160000000,
    }));
    assert.deepEqual(profiles.map((profile) => profile.maxBitrate), source.bitrates);
    assert.deepEqual(profiles.map((profile) => profile.maxFramerate), [60, 45, 30]);
  }
});

test('bandwidth limitation alone and healthy complex-scene throughput do not downshift', () => {
  const bandwidthOnly = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000, 6000].map((now) => good(now, {
      outboundKbps: 11000,
      profileKbps: 16000,
      nominalKbps: 12000,
      degradeFloorKbps: 7800,
      availableOutgoingBitrate: 7000000,
      qualityLimitationReason: 'bandwidth',
    })),
  );
  assert.equal(bandwidthOnly.decision, null);
  assert.equal(bandwidthOnly.state.level, 0);
});

test('post-downshift estimator tracking cannot cascade without further degradation', () => {
  let result = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000, 6000].map((now) => good(now, {
      outboundKbps: 5000,
      profileKbps: 16000,
      nominalKbps: 12000,
      degradeFloorKbps: 7800,
      availableOutgoingBitrate: 6000000,
      qualityLimitationReason: 'bandwidth',
    })),
  );
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});

  result = evaluateMany(
    result.state,
    [16000, 18000, 20000].map((now) => good(now, {
      outboundKbps: 4800,
      profileKbps: 12000,
      nominalKbps: 9000,
      degradeFloorKbps: 5900,
      availableOutgoingBitrate: 5500000,
      qualityLimitationReason: 'bandwidth',
    })),
  );
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 1);
});

test('the 360p18 floor freezes repeated downshift decisions', () => {
  const state = {...createAdaptiveState(0), level: 6, connectedAt: 0};
  const result = evaluateMany(
    state,
    [2000, 4000, 6000].map((now) => bad(now, {
      maxLevel: 6,
      profileKbps: 800,
      nominalKbps: 600,
      degradeFloorKbps: 400,
      outboundKbps: 100,
      availableOutgoingBitrate: 100000,
    })),
  );
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 6);
  assert.equal(result.state.status, 'floor-protected');
});

test('healthy delivery starts a recovery probe without 120 percent BWE headroom', () => {
  const state = {...createAdaptiveState(0), level: 2, connectedAt: 0};
  const samples = [2000, 4000, 6000, 8000, 10000].map((now) => good(now, {
    profileKbps: 8000,
    nominalKbps: 6000,
    degradeFloorKbps: 3900,
    nextProfileKbps: 12000,
    nextNominalKbps: 9000,
    outboundKbps: 6500,
    availableOutgoingBitrate: 7000000,
  }));
  const result = evaluateMany(state, samples);
  assert.deepEqual(result.decision, {type: 'probe', fromLevel: 2, toLevel: 1});
  assert.equal(result.state.level, 1);
  assert.equal(result.state.probeFromLevel, 2);
});

test('a recovery probe commits after six healthy seconds and rolls back after two bad samples', () => {
  const baseState = {
    ...createAdaptiveState(0),
    level: 1,
    connectedAt: 0,
    probeFromLevel: 2,
    probeStartedAt: 10000,
    probeUntil: 16000,
    probeBadCount: 0,
    probeValidCount: 0,
  };
  const healthy = (now) => good(now, {
    profileKbps: 12000,
    nominalKbps: 9000,
    degradeFloorKbps: 5900,
    outboundKbps: 7000,
    availableOutgoingBitrate: 8000000,
  });
  let result = evaluateMany(baseState, [healthy(12000), healthy(14000), healthy(16000)]);
  assert.deepEqual(result.decision, {type: 'probe-commit', fromLevel: 2, toLevel: 1});
  assert.equal(result.state.level, 1);
  assert.equal(result.state.probeFromLevel, null);

  result = evaluateMany(baseState, [
    bad(12000, {profileKbps: 12000, nominalKbps: 9000, degradeFloorKbps: 5900}),
    bad(14000, {profileKbps: 12000, nominalKbps: 9000, degradeFloorKbps: 5900}),
  ]);
  assert.deepEqual(result.decision, {type: 'probe-rollback', fromLevel: 1, toLevel: 2});
  assert.equal(result.state.level, 2);
  assert.equal(result.state.probeFromLevel, null);
});
