import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import test from 'node:test';

const require = createRequire(import.meta.url);
const {
  POLICY_VERSION,
  createAdaptiveState,
  createReceiverBufferState,
  evaluateReceiverBufferSample,
  pauseAdaptiveState,
  evaluateAdaptiveSample,
  computeEncodingProfile,
  computeSfuHostUplinkProfile,
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
    maxLevel: 8, scaleResolutionDownBy: 1,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 1}), {
    level: 1, maxWidth: 1920, maxHeight: 1080, maxFramerate: 45,
    maxBitrate: 12000000, nominalBitrate: 9000000, degradeFloorBitrate: 5900000,
    maxLevel: 8, scaleResolutionDownBy: 1,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 2}), {
    level: 2, maxWidth: 1920, maxHeight: 1080, maxFramerate: 30,
    maxBitrate: 8000000, nominalBitrate: 6000000, degradeFloorBitrate: 3900000,
    maxLevel: 8, scaleResolutionDownBy: 1,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 3}), {
    level: 3, maxWidth: 1280, maxHeight: 720, maxFramerate: 60,
    maxBitrate: 8000000, nominalBitrate: 6000000, degradeFloorBitrate: 3900000,
    maxLevel: 8, scaleResolutionDownBy: 1.5,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 4}), {
    level: 4, maxWidth: 1280, maxHeight: 720, maxFramerate: 45,
    maxBitrate: 7000000, nominalBitrate: 5300000, degradeFloorBitrate: 3400000,
    maxLevel: 8, scaleResolutionDownBy: 1.5,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 5}), {
    level: 5, maxWidth: 1280, maxHeight: 720, maxFramerate: 30,
    maxBitrate: 6000000, nominalBitrate: 4500000, degradeFloorBitrate: 2900000,
    maxLevel: 8, scaleResolutionDownBy: 1.5,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 6}), {
    level: 6, maxWidth: 960, maxHeight: 540, maxFramerate: 60,
    maxBitrate: 5000000, nominalBitrate: 3800000, degradeFloorBitrate: 2500000,
    maxLevel: 8, scaleResolutionDownBy: 2,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 7}), {
    level: 7, maxWidth: 960, maxHeight: 540, maxFramerate: 45,
    maxBitrate: 4500000, nominalBitrate: 3400000, degradeFloorBitrate: 2200000,
    maxLevel: 8, scaleResolutionDownBy: 2,
  });
  assert.deepEqual(computeEncodingProfile({...options, level: 8}), {
    level: 8, maxWidth: 960, maxHeight: 540, maxFramerate: 30,
    maxBitrate: 4000000, nominalBitrate: 3000000, degradeFloorBitrate: 2000000,
    maxLevel: 8, scaleResolutionDownBy: 2,
  });
});

test('minimum adaptive protection profile is 540p30', () => {
  const profile = computeEncodingProfile({
    level: 8,
    sourceWidth: 1920,
    sourceHeight: 1080,
    configuredFps: 60,
    targetBitrate: 50000000,
    viewerCount: 1,
    serverSafeBitrate: 160000000,
  });
  assert.deepEqual(
    [profile.maxWidth, profile.maxHeight, profile.maxFramerate],
    [960, 540, 30],
  );
});

test('receiver buffer rises before a network-driven quality downshift and caps at 300ms', () => {
  const stressed = {
    targetFps: 60,
    decodedFps: 24,
    arrivalFps: 58,
    lossPercent: 3,
    jitterMs: 60,
    framesDroppedDelta: 3,
  };
  let result = evaluateReceiverBufferSample(createReceiverBufferState(), stressed);
  assert.equal(result.targetMs, 80);
  assert.equal(result.shouldDowngrade, false);
  result = evaluateReceiverBufferSample(result.state, stressed);
  assert.equal(result.targetMs, 150);
  result = evaluateReceiverBufferSample(result.state, stressed);
  assert.equal(result.targetMs, 220);
  result = evaluateReceiverBufferSample(result.state, stressed);
  assert.equal(result.targetMs, 300);
  assert.equal(result.shouldDowngrade, true);
});

test('static or sender-limited content does not accumulate receiver latency', () => {
  let state = createReceiverBufferState();
  for (let index = 0; index < 10; index += 1) {
    state = evaluateReceiverBufferSample(state, {
      targetFps: 60,
      decodedFps: 18,
      arrivalFps: 18,
      lossPercent: 0,
      jitterMs: 5,
      framesDroppedDelta: 0,
    }).state;
  }
  assert.equal(state.targetMs, 80);
  assert.equal(state.flowDeficit, false);
});

test('healthy playback releases receiver buffering one step at a time', () => {
  let state = {...createReceiverBufferState(), targetMs: 300, actualMs: 300};
  for (let index = 0; index < 8; index += 1) {
    state = evaluateReceiverBufferSample(state, {
      targetFps: 60,
      decodedFps: 60,
      arrivalFps: 60,
      lossPercent: 0,
      jitterMs: 5,
      framesDroppedDelta: 0,
    }).state;
  }
  assert.equal(state.targetMs, 220);
});

test('network pressure cannot lower quality until receiver buffering reaches 300ms', () => {
  let result = evaluateMany(createAdaptiveState(0), [2000, 4000, 6000].map((now) => receiverBad(now, {
    receiverBufferTargetMs: 220,
    receiverBufferActualMs: 180,
    receiverBufferReason: 'network-buffering',
    receiverFlowDeficit: true,
  })));
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 0);
  assert.equal(result.state.status, 'receiver-buffering');

  result = evaluateMany(result.state, [8000, 10000].map((now) => receiverBad(now, {
    receiverBufferTargetMs: 300,
    receiverBufferActualMs: 290,
    receiverBufferReason: 'buffer-max',
    receiverFlowDeficit: true,
  })));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});
});

test('actual receiver delay stays truthful beyond the 300ms target ceiling', () => {
  const result = evaluateAdaptiveSample(createAdaptiveState(0), {
    valid: false,
    now: 1000,
    receiverBufferTargetMs: 300,
    receiverBufferActualMs: 842,
  });
  assert.equal(result.state.receiverBufferTargetMs, 300);
  assert.equal(result.state.receiverBufferActualMs, 842);
});

test('SFU host uplink profile preserves frame rate while splitting a constrained budget across active layers', () => {
  const moderateSingle = computeSfuHostUplinkProfile({
    availableKbps: 8000,
    requestedFps: 60,
    profileMaxBitrate: 16000000,
    activeLayerCount: 1,
  });
  assert.deepEqual(moderateSingle, {
    band: 'moderate',
    availableKbps: 8000,
    budgetKbps: 6240,
    fullMaxBitrate: 6240000,
    halfMaxBitrate: 0,
    maxFramerate: 60,
    halfMaxFramerate: 30,
    scaleResolutionDownBy: 1.25,
    halfScaleResolutionDownBy: 2.25,
  });

  const constrainedLayers = computeSfuHostUplinkProfile({
    availableKbps: 5000,
    requestedFps: 60,
    profileMaxBitrate: 16000000,
    activeLayerCount: 2,
  });
  assert.deepEqual(constrainedLayers, {
    band: 'constrained',
    availableKbps: 5000,
    budgetKbps: 3900,
    fullMaxBitrate: 2535000,
    halfMaxBitrate: 1365000,
    maxFramerate: 60,
    halfMaxFramerate: 30,
    scaleResolutionDownBy: 1.5,
    halfScaleResolutionDownBy: 2.25,
  });

  const critical = computeSfuHostUplinkProfile({
    availableKbps: 2500,
    requestedFps: 60,
    profileMaxBitrate: 16000000,
    activeLayerCount: 2,
  });
  assert.equal(critical.band, 'critical');
  assert.equal(critical.maxFramerate, 30);
  assert.equal(critical.scaleResolutionDownBy, 1.5);
  assert.ok(critical.fullMaxBitrate > 0);
  assert.ok(critical.halfMaxBitrate > 0);
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

test('adaptive profiles enforce the 30fps minimum tier', () => {
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
    maxFramerate: 30,
    maxBitrate: 6000000,
    nominalBitrate: 4500000,
    degradeFloorBitrate: 2900000,
    maxLevel: 1,
    scaleResolutionDownBy: 1,
  });
});

test('sustained pressure downshifts within six seconds', () => {
  const result = evaluateMany(createAdaptiveState(0), [2000, 4000, 6000].map(bad));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});
  assert.equal(result.state.level, 1);
});

test('browser congestion-control baseline is scoped to the test room', () => {
  assert.equal(POLICY_VERSION, 'adaptive-per-peer-v11-buffered-45fps');
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

test('sustained degradation descends one level at a time through all nine levels', () => {
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

  result = evaluateMany(result.state, [86000, 88000, 90000].map((now) => bad(now, {profileKbps: 1800})));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 6, toLevel: 7});
  assert.equal(result.state.level, 7);

  result = evaluateMany(result.state, [100000, 102000, 104000].map((now) => bad(now, {profileKbps: 1600})));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 7, toLevel: 8});
  assert.equal(result.state.level, 8);

  result = evaluateMany(result.state, [114000, 116000, 118000].map((now) => bad(now, {profileKbps: 1400})));
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 8);
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
    maxLevel: 8,
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
  const throttled = computeSenderDeltas(
    {bytesSent: 8500000, packetsSent: 2200, packetsLost: 20, now: 62000},
    {bytesSent: 1000000, packetsSent: 1000, packetsLost: 10, now: 2000},
  );
  assert.equal(throttled.valid, true);
  assert.equal(throttled.outboundKbps, 1000);
  assert.deepEqual(
    computeSenderDeltas(
      {bytesSent: 8500000, packetsSent: 2200, packetsLost: 20, now: 73001},
      {bytesSent: 1000000, packetsSent: 1000, packetsLost: 10, now: 2000},
    ),
    {valid: false, invalidReason: 'sample-gap'},
  );
});

test('720p sources try 60, 45 and 30 fps before the 540p floor', () => {
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
      [960, 540, 60, 5000000],
      [960, 540, 45, 4500000],
      [960, 540, 30, 4000000],
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

test('active constrained content is classified as network pressure when sender and receiver collapse together', () => {
  const floors = [7800, 5900, 3900, 2900, 2000, 1200, 800];
  const result = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000].map((now) => receiverGood(now, {
      levelDegradeFloorsKbps: floors,
      nominalKbps: 12000,
      degradeFloorKbps: 7800,
      senderCapturedFps: 60,
      senderEncodedFps: 3,
      senderSentFps: 3,
      receiverDecodedFps: 3,
      receiverIncomingKbps: 800,
      outboundKbps: 900,
      availableOutgoingBitrate: 1000000,
      qualityLimitationReason: 'bandwidth',
      senderKbitsPerFrame: 300,
      senderPacketSendDelayMs: 80,
    })),
  );
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});
  assert.equal(result.state.pressure, 'network');
  assert.equal(result.state.lastDownshiftReceiverDriven, true);
});

test('dynamic content does not downshift when receiver delivery and capacity remain healthy', () => {
  const result = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000, 6000].map((now) => receiverGood(now, {
      profileKbps: 16000,
      nominalKbps: 12000,
      degradeFloorKbps: 7800,
      senderCapturedFps: 60,
      senderEncodedFps: 60,
      senderSentFps: 60,
      receiverDecodedFps: 58,
      receiverIncomingKbps: 9000,
      outboundKbps: 9000,
      availableOutgoingBitrate: 12000000,
      senderKbitsPerFrame: 150,
      senderPacketSendDelayMs: 45,
      bandwidthLimitedMsDelta: 900,
      qualityLimitationReason: 'bandwidth',
    })),
  );
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 0);
  assert.equal(result.state.pressure, 'healthy');
});

test('static low-bitrate content does not become network pressure or start a recovery probe', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 2,
    connectedAt: 0,
    lastDownshiftPressure: 'network',
  };
  const result = evaluateMany(
    state,
    [2000, 4000, 6000, 8000, 10000, 12000, 14000].map((now) => receiverGood(now, {
      profileKbps: 8000,
      nominalKbps: 6000,
      degradeFloorKbps: 3900,
      nextProfileKbps: 12000,
      nextNominalKbps: 9000,
      senderCapturedFps: 60,
      senderEncodedFps: 60,
      senderSentFps: 60,
      receiverDecodedFps: 60,
      receiverIncomingKbps: 320,
      outboundKbps: 320,
      availableOutgoingBitrate: 12000000,
      senderKbitsPerFrame: 5,
      senderPacketSendDelayMs: 2,
    })),
  );
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 2);
  assert.notEqual(result.state.status, 'probing');
});

test('persistent constrained motion moves through every tier after its settle window', () => {
  const floors = [7800, 3900, 3900, 2900, 2500, 2000];
  const constrained = (now) => receiverBad(now, {
    levelDegradeFloorsKbps: floors,
    senderCapturedFps: 60,
    senderEncodedFps: 3,
    senderSentFps: 3,
    receiverDecodedFps: 3,
    receiverIncomingKbps: 800,
    outboundKbps: 900,
    availableOutgoingBitrate: 1000000,
    qualityLimitationReason: 'bandwidth',
    senderKbitsPerFrame: 300,
    senderPacketSendDelayMs: 80,
  });
  let result = evaluateMany(createAdaptiveState(0), [2000, 4000].map(constrained));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});

  result = evaluateAdaptiveSample(result.state, constrained(6000));
  assert.equal(result.decision, null);
  result = evaluateAdaptiveSample(result.state, constrained(8000));
  assert.equal(result.decision, null);
  result = evaluateAdaptiveSample(result.state, constrained(10000));
  assert.deepEqual(result.decision, {type: 'downshift-fast', fromLevel: 1, toLevel: 2});
  result = evaluateAdaptiveSample(result.state, constrained(16000));
  assert.deepEqual(result.decision, {type: 'downshift-fast', fromLevel: 2, toLevel: 3});
  result = evaluateAdaptiveSample(result.state, constrained(22000));
  assert.deepEqual(result.decision, {type: 'downshift-fast', fromLevel: 3, toLevel: 4});
  result = evaluateAdaptiveSample(result.state, constrained(28000));
  assert.deepEqual(result.decision, {type: 'downshift-fast', fromLevel: 4, toLevel: 5});
  assert.equal(result.state.level, 5);
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

test('bound severe receiver impairment still descends only one tier at a time', () => {
  const floors = [7800, 3900, 3900, 2900, 2500, 2000];
  let result = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000].map((now) => receiverBad(now, {
      levelDegradeFloorsKbps: floors,
      availableOutgoingBitrate: 1050000,
    })),
  );
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});

  result = evaluateAdaptiveSample(result.state, receiverBad(10000, {
    profileKbps: 8000,
    nominalKbps: 6000,
    degradeFloorKbps: 3900,
    levelDegradeFloorsKbps: floors,
    availableOutgoingBitrate: 1050000,
  }));
  assert.deepEqual(result.decision, {type: 'downshift-fast', fromLevel: 1, toLevel: 2});
  assert.equal(result.state.level, 2);
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

test('missing receiver feedback still permits explicit sender CPU downshift', () => {
  const result = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000].map((now) => good(now, {
      receiverBound: false,
      senderCapturedFps: 30,
      senderEncodedFps: 30,
      senderSentFps: 30,
      qualityLimitationReason: 'cpu',
    })),
  );
  assert.deepEqual(result.decision, {type: 'downshift-cpu', fromLevel: 0, toLevel: 1});
  assert.equal(result.state.status, 'degraded-cpu');
  assert.equal(result.state.receiverStatus, 'unavailable');
  assert.equal(result.state.lastDownshiftReceiverDriven, false);
  assert.equal(result.state.fastDownshiftReadyAt, 0);
});

test('missing receiver feedback permits confirmed capture-to-sender collapse only', () => {
  const collapsed = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000].map((now) => good(now, {
      receiverBound: false,
      senderCapturedFps: 30,
      senderEncodedFps: 3,
      senderSentFps: 3,
      qualityLimitationReason: 'none',
    })),
  );
  assert.deepEqual(collapsed.decision, {type: 'downshift-cpu', fromLevel: 0, toLevel: 1});
  assert.equal(collapsed.state.pressure, 'cpu');

  const staticSource = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000, 6000].map((now) => good(now, {
      receiverBound: false,
      senderCapturedFps: 1,
      senderEncodedFps: 1,
      senderSentFps: 1,
      outboundKbps: 40,
      qualityLimitationReason: 'none',
    })),
  );
  assert.equal(staticSource.decision, null);
  assert.equal(staticSource.state.level, 0);
  assert.equal(staticSource.state.status, 'sender-observing');

  const bandwidthLabelOnly = evaluateMany(
    createAdaptiveState(0),
    [2000, 4000, 6000].map((now) => good(now, {
      receiverBound: false,
      senderCapturedFps: 30,
      senderEncodedFps: 30,
      senderSentFps: 30,
      outboundKbps: 200,
      availableOutgoingBitrate: 200000,
      qualityLimitationReason: 'bandwidth',
    })),
  );
  assert.equal(bandwidthLabelOnly.decision, null);
  assert.equal(bandwidthLabelOnly.state.level, 0);
  assert.equal(bandwidthLabelOnly.state.pressure, 'unavailable');
});

test('sender-only fallback never enters receiver-driven fast downshift', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 1,
    lastDownshiftReceiverDriven: true,
    fastDownshiftReadyAt: 0,
  };
  const result = evaluateMany(
    state,
    [6000, 8000].map((now) => good(now, {
      receiverBound: false,
      senderCapturedFps: 60,
      senderEncodedFps: 3,
      senderSentFps: 3,
      qualityLimitationReason: 'cpu',
    })),
  );
  assert.deepEqual(result.decision, {type: 'downshift-cpu', fromLevel: 1, toLevel: 2});
  assert.equal(result.state.lastDownshiftReceiverDriven, false);
});

test('receiver and sender-only evidence require separate confirmation windows', () => {
  let result = evaluateAdaptiveSample(createAdaptiveState(0), receiverBad(2000));
  result = evaluateAdaptiveSample(result.state, good(4000, {
    receiverBound: false,
    senderCapturedFps: 30,
    senderEncodedFps: 3,
    senderSentFps: 3,
    qualityLimitationReason: 'cpu',
  }));
  assert.equal(result.decision, null);
  assert.equal(result.state.badCount, 1);

  result = evaluateAdaptiveSample(result.state, good(6000, {
    receiverBound: false,
    senderCapturedFps: 30,
    senderEncodedFps: 3,
    senderSentFps: 3,
    qualityLimitationReason: 'cpu',
  }));
  assert.deepEqual(result.decision, {type: 'downshift-cpu', fromLevel: 0, toLevel: 1});
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

test('the 540p30 floor freezes repeated downshift decisions', () => {
  const state = {...createAdaptiveState(0), level: 5, connectedAt: 0};
  const result = evaluateMany(
    state,
    [2000, 4000, 6000].map((now) => bad(now, {
      maxLevel: 5,
      profileKbps: 800,
      nominalKbps: 600,
      degradeFloorKbps: 400,
      outboundKbps: 100,
      availableOutgoingBitrate: 100000,
    })),
  );
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 5);
  assert.equal(result.state.status, 'floor-protected');
});

test('sustained demanding delivery starts a recovery probe only after target-floor headroom is verified', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 2,
    connectedAt: 0,
    lastDownshiftPressure: 'network',
  };
  const samples = [2000, 4000, 6000, 8000, 10000, 12000, 14000].map((now) => receiverGood(now, {
    profileKbps: 8000,
    nominalKbps: 6000,
    degradeFloorKbps: 3900,
    nextProfileKbps: 12000,
    nextNominalKbps: 9000,
    nextDegradeFloorKbps: 5900,
    outboundKbps: 6500,
    availableOutgoingBitrate: 7000000,
  }));
  const result = evaluateMany(state, samples);
  assert.deepEqual(result.decision, {type: 'probe', fromLevel: 2, toLevel: 1});
  assert.equal(result.state.level, 1);
  assert.equal(result.state.probeFromLevel, 2);
  assert.equal(result.state.probePressure, 'network');
});

test('a recovery probe commits only with fresh healthy target-capacity evidence', () => {
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
  result = evaluateMany(baseState, [unchanged(12000), unchanged(14000)]);
  assert.deepEqual(result.decision, {type: 'probe-rollback', fromLevel: 1, toLevel: 2});
  assert.equal(result.state.level, 2);
  assert.equal(result.state.probeFromLevel, null);
});

test('stability lab blocks recovery while retransmission or sender queue remains elevated', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 2,
    connectedAt: 0,
    lastDownshiftPressure: 'network',
  };
  const sample = (now, overrides = {}) => receiverGood(now, {
    stabilityLab: true,
    profileKbps: 8000,
    nominalKbps: 6000,
    degradeFloorKbps: 3900,
    nextProfileKbps: 12000,
    nextNominalKbps: 9000,
    nextDegradeFloorKbps: 5900,
    outboundKbps: 7000,
    availableOutgoingBitrate: 12000000,
    senderKbitsPerFrame: 180,
    ...overrides,
  });

  let result = evaluateMany(state, [2000, 6000, 10000, 14000, 18000, 22000].map((now) => sample(now, {
    senderRetransmitPercent: 8,
    senderPacketSendDelayMs: 4,
  })));
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 2);

  result = evaluateMany(state, [2000, 6000, 10000, 14000, 18000, 22000].map((now) => sample(now, {
    senderRetransmitPercent: 1,
    senderPacketSendDelayMs: 45,
  })));
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 2);
});

test('stability lab requires twenty seconds of clean recovery before probing', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 2,
    connectedAt: 0,
    lastDownshiftPressure: 'network',
  };
  const clean = (now) => receiverGood(now, {
    stabilityLab: true,
    profileKbps: 8000,
    nominalKbps: 6000,
    degradeFloorKbps: 3900,
    nextProfileKbps: 12000,
    nextNominalKbps: 9000,
    nextDegradeFloorKbps: 5900,
    outboundKbps: 7000,
    availableOutgoingBitrate: 12000000,
    senderKbitsPerFrame: 180,
    senderRetransmitPercent: 1,
    senderPacketSendDelayMs: 4,
  });
  let result = evaluateMany(state, [2000, 6000, 10000, 14000, 18000].map(clean));
  assert.equal(result.decision, null);
  result = evaluateAdaptiveSample(result.state, clean(22000));
  assert.deepEqual(result.decision, {type: 'probe', fromLevel: 2, toLevel: 1});
});

test('bandwidth recovery escapes the floor without rebuilding adaptive state', () => {
  const floors = [7800, 3900, 3900, 2900, 2500, 2000];
  const constrained = (now) => receiverBad(now, {
    stabilityLab: true,
    levelDegradeFloorsKbps: floors,
    senderCapturedFps: 30,
    senderEncodedFps: 3,
    senderSentFps: 3,
    receiverDecodedFps: 3,
    receiverIncomingKbps: 260,
    outboundKbps: 280,
    availableOutgoingBitrate: 600000,
    qualityLimitationReason: 'bandwidth',
    senderKbitsPerFrame: 90,
    senderPacketSendDelayMs: 60,
  });
  let result = evaluateMany(createAdaptiveState(0), [2000, 4000].map(constrained));
  assert.deepEqual(result.decision, {type: 'downshift', fromLevel: 0, toLevel: 1});
  result = evaluateAdaptiveSample(result.state, constrained(6000));
  assert.equal(result.decision, null);
  result = evaluateAdaptiveSample(result.state, constrained(8000));
  assert.equal(result.decision, null);
  result = evaluateAdaptiveSample(result.state, constrained(10000));
  assert.deepEqual(result.decision, {type: 'downshift-fast', fromLevel: 1, toLevel: 2});
  result = evaluateAdaptiveSample(result.state, constrained(16000));
  assert.deepEqual(result.decision, {type: 'downshift-fast', fromLevel: 2, toLevel: 3});
  result = evaluateAdaptiveSample(result.state, constrained(22000));
  assert.deepEqual(result.decision, {type: 'downshift-fast', fromLevel: 3, toLevel: 4});
  result = evaluateAdaptiveSample(result.state, constrained(28000));
  assert.deepEqual(result.decision, {type: 'downshift-fast', fromLevel: 4, toLevel: 5});
  assert.equal(result.state.level, 5);

  const recovered = (now, availableOutgoingBitrate, receiverIncomingKbps) => receiverGood(now, {
    stabilityLab: true,
    levelDegradeFloorsKbps: floors,
    profileKbps: 800,
    nominalKbps: 600,
    degradeFloorKbps: 400,
    nextProfileKbps: 2500,
    nextNominalKbps: 1900,
    nextDegradeFloorKbps: 1200,
    profileMaxFramerate: 18,
    senderCapturedFps: 18,
    senderEncodedFps: 18,
    senderSentFps: 18,
    receiverDecodedFps: 18,
    receiverIncomingKbps,
    outboundKbps: 300,
    availableOutgoingBitrate,
    senderKbitsPerFrame: 20,
    senderRetransmitPercent: 0.5,
    senderPacketSendDelayMs: 3,
  });
  let recoveryDecision = null;
  for (const [now, available, incoming] of [
    [30000, 1800000, 280],
    [34000, 2400000, 300],
    [38000, 3200000, 310],
    [42000, 4200000, 320],
    [46000, 5200000, 330],
    [50000, 6200000, 340],
    [54000, 6800000, 350],
    [58000, 7200000, 360],
  ]) {
    result = evaluateAdaptiveSample(result.state, recovered(now, available, incoming));
    if (result.decision) {
      recoveryDecision = result.decision;
      break;
    }
  }
  assert.deepEqual(recoveryDecision, {type: 'probe', fromLevel: 5, toLevel: 4});
  assert.equal(result.state.level, 4);

  const probeStartedAt = result.state.probeStartedAt;
  result = evaluateAdaptiveSample(result.state, recovered(probeStartedAt + 4000, 6500000, 1250));
  assert.equal(result.decision, null);
  result = evaluateAdaptiveSample(result.state, recovered(probeStartedAt + 10000, 6800000, 1500));
  assert.deepEqual(result.decision, {type: 'probe-commit', fromLevel: 5, toLevel: 4});
  assert.equal(result.state.level, 4);
});

test('sender-side recovery can probe after receiver feedback expires', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 5,
    connectedAt: 0,
    lastDownshiftPressure: 'network',
  };
  const recovered = (now) => good(now, {
    stabilityLab: true,
    profileKbps: 2500,
    nominalKbps: 1900,
    degradeFloorKbps: 1200,
    nextProfileKbps: 4000,
    nextNominalKbps: 3000,
    nextDegradeFloorKbps: 2000,
    profileMaxFramerate: 21,
    senderCapturedFps: 21,
    senderEncodedFps: 21,
    senderSentFps: 21,
    senderKbitsPerFrame: 110,
    outboundKbps: 2100,
    availableOutgoingBitrate: 9000000,
    receiverBound: false,
    recoveryReceiverOptional: true,
    receiverDecodedFps: 0,
    senderPacketSendDelayMs: 3,
    senderRetransmitPercent: 0.5,
  });
  let result = {state, decision: null};
  let decision = null;
  for (const now of [12000, 16000, 20000, 24000, 28000, 32000, 36000]) {
    result = evaluateAdaptiveSample(result.state, recovered(now));
    if (result.decision) {
      decision = result.decision;
      break;
    }
  }
  assert.deepEqual(decision, {type: 'probe', fromLevel: 5, toLevel: 4});
});

test('healthy repeated receiver reports do not freeze recovery progress', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 5,
    connectedAt: 0,
    lastDownshiftPressure: 'network',
    lastReceiverReportId: 'same-report',
  };
  const sample = (now) => good(now, {
    profileKbps: 2500,
    nominalKbps: 1900,
    degradeFloorKbps: 1200,
    nextProfileKbps: 4000,
    nextNominalKbps: 3000,
    nextDegradeFloorKbps: 2000,
    profileMaxFramerate: 21,
    senderCapturedFps: 21,
    senderEncodedFps: 21,
    senderSentFps: 21,
    senderKbitsPerFrame: 110,
    outboundKbps: 2100,
    availableOutgoingBitrate: 9000000,
    receiverBound: true,
    receiverReportAgeMs: 1200,
    receiverReportedAtUnix: 123,
    receiverDecodedFps: 21,
    receiverIncomingKbps: 2100,
    receiverLossPercent: 0.1,
    receiverJitterMs: 10,
    senderPacketSendDelayMs: 3,
    senderRetransmitPercent: 0.5,
  });
  let result = evaluateAdaptiveSample(state, sample(12000));
  result = evaluateAdaptiveSample(result.state, sample(16000));
  assert.equal(result.state.recoveryHealthySamples, 2);
  assert.equal(result.state.recoveryGateReason, 'ready');
});

test('production recovery also waits for retransmission and sender queue to clear', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 2,
    connectedAt: 0,
    lastDownshiftPressure: 'network',
  };
  const sample = (now) => receiverGood(now, {
    profileKbps: 8000,
    nominalKbps: 6000,
    degradeFloorKbps: 3900,
    nextProfileKbps: 12000,
    nextNominalKbps: 9000,
    nextDegradeFloorKbps: 5900,
    outboundKbps: 7000,
    availableOutgoingBitrate: 12000000,
    senderKbitsPerFrame: 180,
    senderRetransmitPercent: 8,
    senderPacketSendDelayMs: 45,
  });
  const result = evaluateMany(state, [2000, 6000, 10000, 14000, 18000, 22000].map(sample));
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 2);
});

test('healthy receiver retransmission pressure does not trigger fast cascade', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 1,
    connectedAt: 0,
    lastDownshiftReceiverDriven: true,
    lastDownshiftPressure: 'network',
    fastDownshiftReadyAt: 0,
  };
  const result = evaluateAdaptiveSample(state, receiverGood(10000, {
    profileKbps: 12000,
    nominalKbps: 9000,
    degradeFloorKbps: 5900,
    nextProfileKbps: 8000,
    nextNominalKbps: 6000,
    nextDegradeFloorKbps: 3900,
    outboundKbps: 5600,
    availableOutgoingBitrate: 5000000,
    senderRetransmitPercent: 8,
    senderPacketSendDelayMs: 4,
  }));
  assert.notEqual(result.decision && result.decision.type, 'downshift-fast');
});

test('transient transport pressure uses per-peer soft protection before changing the tier', () => {
  const transient = (now, overrides = {}) => receiverBad(now, {
    receiverReportedAtUnix: now / 1000,
    receiverBufferTargetMs: 300,
    receiverFlowDeficit: true,
    senderCapturedFps: 60,
    senderEncodedFps: 45,
    senderSentFps: 45,
    receiverDecodedFps: 12,
    receiverIncomingKbps: 1500,
    outboundKbps: 1500,
    availableOutgoingBitrate: 1800000,
    senderPacketSendDelayMs: 55,
    senderRetransmitPercent: 6,
    ...overrides,
  });
  let state = createAdaptiveState(0);
  state = evaluateAdaptiveSample(state, receiverGood(20000, {
    receiverReportedAtUnix: 20,
    receiverBufferTargetMs: 80,
  })).state;
  let result = evaluateAdaptiveSample(state, transient(22000));
  result = evaluateAdaptiveSample(result.state, transient(24000));
  assert.equal(result.state.level, 0);
  assert.ok(result.state.softProtectionKbps > 0);
  assert.equal(result.state.transitionReason, 'soft-protect');
  assert.notEqual(result.decision && result.decision.type, 'downshift-fast');
});

test('tier transitions are single-step and have a dwell timestamp', () => {
  const stress = (now) => receiverBad(now, {
    receiverReportedAtUnix: now / 1000,
    receiverBufferTargetMs: 300,
    receiverFlowDeficit: true,
    senderCapturedFps: 60,
    senderEncodedFps: 12,
    senderSentFps: 12,
    receiverDecodedFps: 4,
    receiverIncomingKbps: 900,
    outboundKbps: 900,
    availableOutgoingBitrate: 1000000,
    senderPacketSendDelayMs: 80,
    senderRetransmitPercent: 5,
  });
  let state = createAdaptiveState(0);
  let result = {state, decision: null};
  for (const now of [20000, 22000, 24000, 26000, 28000, 30000]) result = evaluateAdaptiveSample(result.state, stress(now));
  assert.equal(result.state.level, 1);
  assert.equal(result.state.lastTransitionAt, 28000);
  result = evaluateAdaptiveSample(result.state, stress(32000));
  assert.equal(result.state.level, 1);
});

test('H264 cpu limitation does not downshift when current profile fps remains usable', () => {
  const samples = [2000, 4000, 6000].map((now) => receiverGood(now, {
    profileKbps: 6000,
    nominalKbps: 4500,
    degradeFloorKbps: 2900,
    profileMaxFramerate: 30,
    senderCapturedFps: 60,
    senderEncodedFps: 20,
    senderSentFps: 20,
    receiverDecodedFps: 19,
    receiverIncomingKbps: 3800,
    outboundKbps: 4000,
    availableOutgoingBitrate: 6000000,
    qualityLimitationReason: 'cpu',
  }));
  const result = evaluateMany(createAdaptiveState(0), samples);
  assert.equal(result.decision, null);
  assert.equal(result.state.level, 0);
});

test('a probe rolls back when dynamic content cannot sustain the target profile capacity', () => {
  const state = {
    ...createAdaptiveState(0),
    level: 1,
    connectedAt: 0,
    probeFromLevel: 2,
    probeStartedAt: 10000,
    probeUntil: 16000,
    probePressure: 'network',
  };
  const constrained = (now) => receiverGood(now, {
    profileKbps: 12000,
    nominalKbps: 9000,
    degradeFloorKbps: 5900,
    senderCapturedFps: 45,
    senderEncodedFps: 45,
    senderSentFps: 45,
    receiverDecodedFps: 44,
    receiverIncomingKbps: 1080,
    outboundKbps: 1200,
    availableOutgoingBitrate: 4700000,
    qualityLimitationReason: 'bandwidth',
    senderKbitsPerFrame: 140,
    senderPacketSendDelayMs: 60,
  });
  const result = evaluateMany(state, [constrained(12000), constrained(14000)]);
  assert.deepEqual(result.decision, {type: 'probe-rollback', fromLevel: 1, toLevel: 2});
  assert.equal(result.state.level, 2);
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

test('an old repeated receiver report freezes instead of committing or rolling back a probe', () => {
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
    receiverReportAgeMs: 5000,
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
