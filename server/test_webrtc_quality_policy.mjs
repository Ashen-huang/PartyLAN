import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import test from 'node:test';

const require = createRequire(import.meta.url);
const {createQualityState, evaluateQualitySample, POLICY_VERSION} = require('./webrtc_quality_policy.js');

const good = (now, overrides = {}) => ({
  now,
  valid: true,
  path: 'turn',
  lossPercent: 0.2,
  jitterMs: 10,
  qualityLimitationReason: 'none',
  availableOutgoingBitrate: 8000000,
  targetBitrate: 9000000,
  framesDecodedDelta: 30,
  packetsReceivedDelta: 100,
  pairId: 'pair-a',
  generation: 0,
  ...overrides,
});

const bad = (now, overrides = {}) => good(now, {
  lossPercent: 8,
  jitterMs: 150,
  qualityLimitationReason: 'bandwidth',
  availableOutgoingBitrate: 2000000,
  framesDecodedDelta: 0,
  ...overrides,
});

test('the first 15 seconds are observation only', () => {
  let state = createQualityState(0);
  for (let now = 2000; now <= 14000; now += 2000) state = evaluateQualitySample(state, bad(now)).state;
  assert.equal(state.action, null);
  assert.equal(state.status, 'observing');
  state = evaluateQualitySample(state, bad(16000)).state;
  assert.equal(state.action, 'protect_and_restart_ice');
  assert.equal(state.recoveryCount, 1);
  assert.equal(state.policyVersion, POLICY_VERSION);
});

test('short spikes and non-consecutive bad samples do not trigger', () => {
  let state = createQualityState(0);
  state = evaluateQualitySample(state, good(16000)).state;
  for (const sample of [bad(18000), bad(20000), bad(22000), good(24000), good(26000), good(28000), good(30000)]) {
    state = evaluateQualitySample(state, sample).state;
  }
  assert.equal(state.action, null);

  for (const sample of [bad(32000), bad(34000), good(36000), bad(38000), bad(40000), good(42000), bad(44000)]) {
    state = evaluateQualitySample(state, sample).state;
  }
  assert.equal(state.action, null);
});

test('five of seven bad samples with the last three consecutive protects and restarts once', () => {
  let state = createQualityState(0);
  state = evaluateQualitySample(state, good(16000)).state;
  const samples = [bad(18000), good(20000), good(22000), bad(24000), bad(26000), bad(28000), bad(30000)];
  for (const sample of samples) state = evaluateQualitySample(state, sample).state;
  assert.equal(state.action, 'protect_and_restart_ice');
  assert.equal(state.status, 'restarting');
  assert.equal(state.recoveryCount, 1);
  assert.equal(state.badCount, 0);
});

test('a repeatedly degraded session remains protected without a second restart', () => {
  let state = createQualityState(0);
  for (const now of [16000, 18000, 20000, 22000, 24000, 26000, 28000]) {
    state = evaluateQualitySample(state, bad(now)).state;
  }
  assert.equal(state.action, 'protect_and_restart_ice');

  state = {...state, connectedAt: 70000, cooldownUntil: 0};
  for (const now of [86000, 88000, 90000, 92000, 94000, 96000, 98000]) {
    state = evaluateQualitySample(state, bad(now)).state;
  }
  assert.equal(state.action, 'protect_bitrate');
  assert.equal(state.status, 'protected');
  assert.equal(state.recoveryCount, 1);
});

test('low bitrate, low fps, high RTT, direct paths, hidden pages and insufficient packets do not trigger alone', () => {
  let state = createQualityState(0);
  const harmless = bad(16000, {lossPercent: 0, jitterMs: 150, framesDecodedDelta: 0, availableOutgoingBitrate: 100000});
  for (let index = 0; index < 8; index += 1) state = evaluateQualitySample(state, {...harmless, now: 16000 + index * 2000}).state;
  assert.equal(state.action, null);

  state = createQualityState(0);
  for (let index = 0; index < 8; index += 1) state = evaluateQualitySample(state, bad(16000 + index * 2000, {path: 'p2p'})).state;
  assert.equal(state.action, null);

  state = createQualityState(0);
  for (let index = 0; index < 8; index += 1) state = evaluateQualitySample(state, bad(16000 + index * 2000, {valid: false, invalidReason: 'page-hidden'})).state;
  assert.equal(state.action, null);
});

test('pair changes reset observation and cooldown suppresses repeat decisions', () => {
  let state = createQualityState(0);
  state = evaluateQualitySample(state, good(16000)).state;
  state = evaluateQualitySample(state, bad(18000, {pairId: 'pair-b'})).state;
  assert.equal(state.status, 'observing');
  assert.equal(state.window.length, 0);

  state = {...state, connectedAt: 0, cooldownUntil: 90000};
  for (let now = 70000; now <= 84000; now += 2000) state = evaluateQualitySample(state, bad(now, {pairId: 'pair-b'})).state;
  assert.equal(state.action, null);
  assert.equal(state.status, 'cooldown');
});

test('an invalid sample is skipped without erasing the prior quality window', () => {
  let state = createQualityState(0);
  for (const sample of [bad(2000), bad(4000), bad(6000)]) {
    state = evaluateQualitySample(state, sample).state;
  }
  const before = state.window.slice();
  state = evaluateQualitySample(state, bad(8000, {valid: false, invalidReason: 'stats-timeout'})).state;
  assert.deepEqual(state.window, before);
});

test('twenty stable seconds request one bounded bitrate recovery step', () => {
  let state = createQualityState(0);
  state = {...state, status: 'protected', recoveryCount: 1, stableSince: null};
  for (let now = 20000; now <= 40000; now += 2000) state = evaluateQualitySample(state, good(now)).state;
  assert.equal(state.status, 'recovering');
  assert.equal(state.stable, true);
  assert.equal(state.action, 'recover_bitrate');
  assert.equal(state.recoveryStep, 1);
});
