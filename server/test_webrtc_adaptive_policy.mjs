import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import test from 'node:test';

const require = createRequire(import.meta.url);
const {
  POLICY_VERSION,
  createAdaptiveState,
  pauseAdaptiveState,
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

const receiverBad = (now, overrides = {}) => bad(now, {
  receiverBound: true,
  receiverReportAgeMs: 500,
  receiverReportedAtUnix: now / 1000,
  senderCapturedFps: 60,
  senderEncodedFps: 30,
  senderSentFps: 30,
  receiverDecodedFps: 2,
  receiverIncomingKbps: 1000,
  receiverLossPercent: 8,
  receiverJitterMs: 140,
  ...overrides,
});

const receiverGood = (now, overrides = {}) => good(now, {
  receiverBound: true,
  receiverReportAgeMs: 500,
  receiverReportedAtUnix: now / 1000,
  senderCapturedFps: 30,
  senderEncodedFps: 30,
  senderSentFps: 30,
  receiverDecodedFps: 29,
  receiverIncomingKbps: 5000,
  receiverLossPercent: 0.2,
  receiverJitterMs: 15,
  ...overrides,
});

function evaluateMany(state, samples) {
  let result = {state, decision: null};
  for (const sample of samples) result = evaluateAdaptiveSample(result.state, sample);
  return result;
}

test('feedback pause clears accumulated evidence without changing the selected level', () => {
  const paused = pauseAdaptiveState({
    ...createAdaptiveState(0),
    level: 2,
    status: 'degraded-suspected',
    window: [true],
    badCount: 1,
    stableSince: 4000,
    lastReceiverReportId: 'old-report',
    probeFromLevel: 3,
    probeUntil: 16000,
    probeBadCount: 1,
    probeValidCount: 1,
    probeImprovedCount: 1,
    probePressure: 'network',
  });
  assert.equal(paused.level, 2);
  assert.equal(paused.probeFromLevel, 3);
  assert.equal(paused.probePressure, 'network');
  assert.equal(paused.status, 'probing');
  assert.deepEqual(paused.window, []);
  assert.equal(paused.badCount, 0);
  assert.equal(paused.stableSince, null);
  assert.equal(paused.lastReceiverReportId, 'old-report');
  assert.equal(paused.probeBadCount, 0);
  assert.equal(paused.probeValidCount, 0);
  assert.equal(paused.probeImprovedCount, 0);
});

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
  assert.equal(POLICY_VERSION, 'adaptive-per-peer-v6');
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

test('bound severe receiver impairment descends quickly by at most two levels', () => {
  const floors = [7800, 5900, 3900, 2900, 2000, 1200, 400];
  let result = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000].map((now) => receiverBad(now, {
      levelDegradeFloorsKbps: floors,
      availableOutgoingBitrate: 1050000,
    })),
  );
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});

  result = evaluateAdaptiveSample(result.state, receiverBad(6000, {
    profileKbps: 12000,
    nominalKbps: 9000,
    degradeFloorKbps: 5900,
    levelDegradeFloorsKbps: floors,
    availableOutgoingBitrate: 1050000,
  }));
  assert.deepEqual(result.decision, {type: 'downshift-fast', fromLevel: 1, toLevel: 3});
  assert.equal(result.state.level, 3);
});

test('low decoded fps is not receiver impairment when the sender source is idle', () => {
  const result = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000].map((now) => receiverBad(now, {
      senderSentFps: 1,
      senderCapturedFps: 1,
      senderEncodedFps: 1,
      receiverDecodedFps: 1,
      receiverIncomingKbps: 50,
      receiverLossPercent: 0,
      receiverJitterMs: 10,
      outboundKbps: 50,
      availableOutgoingBitrate: 8000000,
      qualityLimitationReason: 'none',
    })),
  );
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 0);
});

test('sender CPU collapse is not receiver impairment and only downshifts one level', () => {
  let result = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000].map((now) => receiverBad(now, {
      senderCapturedFps: 60,
      senderEncodedFps: 3,
      senderSentFps: 3,
      receiverDecodedFps: 3,
      receiverIncomingKbps: 900,
      receiverLossPercent: 0,
      receiverJitterMs: 10,
      lossPercent: 0,
      jitterMs: 10,
      outboundKbps: 900,
      availableOutgoingBitrate: 8000000,
      qualityLimitationReason: 'cpu',
    })),
  );
  assert.deepEqual(result.decision, {type: 'downshift-cpu', fromLevel: 0, toLevel: 1});
  assert.equal(result.state.receiverStatus, 'healthy');
  assert.equal(result.state.lastDownshiftReceiverDriven, false);
  assert.equal(result.state.fastDownshiftReadyAt, 0);

  result = evaluateMany(
    result.state,
    [10000, 12000].map((now) => receiverBad(now, {
      profileKbps: 12000,
      nominalKbps: 9000,
      degradeFloorKbps: 5900,
      senderCapturedFps: 60,
      senderEncodedFps: 3,
      senderSentFps: 3,
      receiverDecodedFps: 3,
      receiverIncomingKbps: 900,
      receiverLossPercent: 0,
      receiverJitterMs: 10,
      lossPercent: 0,
      jitterMs: 10,
      outboundKbps: 900,
      availableOutgoingBitrate: 8000000,
      qualityLimitationReason: 'cpu',
    })),
  );
  assert.deepEqual(result.decision, {type: 'downshift-cpu', fromLevel: 1, toLevel: 2});
  assert.equal(result.state.level, 2);
});

test('a collapsed sender is CPU-limited only when the receiver follows the sent frames', () => {
  const result = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000].map((now) => receiverBad(now, {
      senderCapturedFps: 60,
      senderEncodedFps: 3,
      senderSentFps: 3,
      receiverDecodedFps: 1,
      receiverIncomingKbps: 300,
      receiverLossPercent: 0,
      receiverJitterMs: 10,
      lossPercent: 0,
      jitterMs: 10,
      outboundKbps: 900,
      availableOutgoingBitrate: 8000000,
      qualityLimitationReason: 'cpu',
    })),
  );
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 0);
  assert.equal(result.state.pressure, 'unavailable');
  assert.equal(result.state.receiverStatus, 'unavailable');
});

test('explicit receiver feedback loss clears pre-gap degradation evidence', () => {
  let result = evaluateAdaptiveSample(createAdaptiveState(0), receiverBad(2000));
  assert.equal(result.decision, null);
  assert.equal(result.state.badCount, 1);

  result = evaluateAdaptiveSample(result.state, good(3000, {
    valid: false,
    invalidReason: 'page-hidden',
    receiverBound: false,
  }));
  assert.deepEqual(result.state.window, []);
  assert.equal(result.state.badCount, 0);

  result = evaluateAdaptiveSample(result.state, receiverBad(4000, {
    receiverReportedAtUnix: 2,
  }));
  assert.equal(result.decision, null);
  assert.equal(result.state.badCount, 0);

  result = evaluateAdaptiveSample(result.state, receiverBad(6000));
  assert.equal(result.decision, null);
  assert.equal(result.state.badCount, 1);
  result = evaluateAdaptiveSample(result.state, receiverBad(8000));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});
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
  const state = {
    ...createAdaptiveState(0),
    level: 2,
    connectedAt: 0,
    lastDownshiftPressure: 'network',
  };
  const samples = [2000, 4000, 6000, 8000].map((now) => receiverGood(now, {
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
  assert.equal(result.state.probePressure, 'network');
});

test('a recovery probe commits after fresh healthy reports without fixed fps or bitrate growth', () => {
  const baseState = {
    ...createAdaptiveState(0),
    level: 1,
    connectedAt: 0,
    probeFromLevel: 2,
    probeStartedAt: 10000,
    probeUntil: 16000,
    probeBadCount: 0,
    probeValidCount: 0,
    probeBaselineDecodedFps: 18,
    probeBaselineIncomingKbps: 3000,
    probeReceiverRequired: true,
    probePressure: 'network',
  };
  const improved = (now) => receiverGood(now, {
    profileKbps: 12000,
    nominalKbps: 9000,
    degradeFloorKbps: 5900,
    outboundKbps: 7000,
    availableOutgoingBitrate: 8000000,
    receiverDecodedFps: 28,
    receiverIncomingKbps: 5000,
  });
  let result = evaluateMany(baseState, [improved(12000), improved(14000), improved(16000)]);
  assert.deepEqual(result.decision, {type: 'probe-commit', fromLevel: 2, toLevel: 1});
  assert.equal(result.state.level, 1);
  assert.equal(result.state.probeFromLevel, null);

  const unchanged = (now) => receiverGood(now, {
    profileKbps: 12000,
    nominalKbps: 9000,
    degradeFloorKbps: 5900,
    outboundKbps: 4000,
    availableOutgoingBitrate: 8000000,
    senderCapturedFps: 18,
    senderEncodedFps: 18,
    senderSentFps: 18,
    receiverDecodedFps: 18,
    receiverIncomingKbps: 3000,
  });
  result = evaluateMany(baseState, [unchanged(12000), unchanged(14000), unchanged(16000)]);
  assert.deepEqual(result.decision, {type: 'probe-commit', fromLevel: 2, toLevel: 1});
  assert.equal(result.state.level, 1);
  assert.equal(result.state.probeFromLevel, null);
});

test('a receiver-validated probe freezes when its report expires', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 1,
    probeFromLevel: 2,
    probeStartedAt: 10000,
    probeUntil: 16000,
    probeReceiverRequired: true,
    probePressure: 'network',
    probeBaselineDecodedFps: 18,
    probeBaselineIncomingKbps: 3000,
  };
  let result = evaluateAdaptiveSample(state, good(14000, {
    valid: false,
    receiverBound: false,
  }));
  assert.equal(result.decision, null);
  result = evaluateAdaptiveSample(result.state, good(16000, {
    valid: false,
    receiverBound: false,
  }));
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 1);
  assert.equal(result.state.probeFromLevel, 2);
  assert.equal(result.state.probeBadCount, 0);
});

test('a repeated receiver report freezes instead of committing or rolling back a probe', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 1,
    probeFromLevel: 2,
    probeStartedAt: 10000,
    probeUntil: 16000,
    probeReceiverRequired: true,
    probePressure: 'network',
    probeBaselineDecodedFps: 18,
    probeBaselineIncomingKbps: 3000,
  };
  const repeated = (now) => receiverGood(now, {
    receiverReportedAtUnix: 12,
    receiverDecodedFps: 30,
    receiverIncomingKbps: 5000,
  });
  const result = evaluateMany(state, [repeated(12000), repeated(14000), repeated(16000)]);
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 1);
  assert.equal(result.state.probeFromLevel, 2);
  assert.equal(result.state.probeValidCount, 1);
});

test('a CPU recovery probe commits only after CPU pressure clears', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 1,
    probeFromLevel: 2,
    probeStartedAt: 10000,
    probeUntil: 16000,
    probeReceiverRequired: true,
    probePressure: 'cpu',
  };
  const recovered = (now) => receiverGood(now, {
    senderCapturedFps: 30,
    senderEncodedFps: 30,
    senderSentFps: 30,
    receiverDecodedFps: 29,
    qualityLimitationReason: 'none',
  });
  let result = evaluateMany(state, [recovered(12000), recovered(14000), recovered(16000)]);
  assert.deepEqual(result.decision, {type: 'probe-commit', fromLevel: 2, toLevel: 1});

  const stillCpuLimited = (now) => receiverGood(now, {
    senderCapturedFps: 60,
    senderEncodedFps: 3,
    senderSentFps: 3,
    receiverDecodedFps: 3,
    receiverIncomingKbps: 900,
    qualityLimitationReason: 'cpu',
  });
  result = evaluateMany(state, [stillCpuLimited(12000), stillCpuLimited(14000)]);
  assert.deepEqual(result.decision, {type: 'probe-rollback', fromLevel: 1, toLevel: 2});
});
