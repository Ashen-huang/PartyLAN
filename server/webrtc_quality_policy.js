(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.GameCastWebRTCQuality = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  var POLICY_VERSION = 'ice-recovery-v1';
  var OBSERVATION_MS = 15000;
  var WINDOW_SIZE = 7;
  var REQUIRED_BAD = 5;
  var REQUIRED_CONSECUTIVE = 3;
  var COOLDOWN_MS = 60000;
  var STABLE_MS = 20000;
  var MAX_AUTO_RESTARTS = 1;
  var MAX_RECOVERY_STEPS = 3;

  function createQualityState(connectedAt) {
    return {
      policyVersion: POLICY_VERSION,
      connectedAt: connectedAt || 0,
      status: 'observing',
      action: null,
      window: [],
      pairId: '',
      generation: 0,
      badCount: 0,
      recoveryCount: 0,
      recoveryStep: 0,
      cooldownUntil: 0,
      stableSince: null,
      stable: false
    };
  }

  function isSecondarySignal(sample) {
    var bitrateConstrained =
      sample.targetBitrate > 0 &&
      sample.availableOutgoingBitrate > 0 &&
      sample.availableOutgoingBitrate < sample.targetBitrate * 0.45;
    return (
      sample.jitterMs > 100 ||
      sample.qualityLimitationReason === 'bandwidth' ||
      bitrateConstrained ||
      sample.framesDecodedDelta === 0
    );
  }

  function isBadSample(sample) {
    return sample.lossPercent > 5 && isSecondarySignal(sample);
  }

  function stableSample(sample) {
    return sample.valid && sample.lossPercent < 1 && sample.jitterMs < 40;
  }

  function resetForPath(state, sample) {
    return Object.assign({}, state, {
      connectedAt: sample.now,
      status: 'observing',
      action: null,
      window: [],
      pairId: sample.pairId,
      generation: sample.generation,
      badCount: 0,
      stableSince: null,
      stable: false
    });
  }

  function evaluateQualitySample(current, sample) {
    var state = Object.assign({}, current, {action: null, stable: false});
    if (!state.pairId && sample.pairId) {
      state.pairId = sample.pairId;
      state.generation = sample.generation;
    } else if (
      sample.pairId &&
      (sample.pairId !== state.pairId || sample.generation !== state.generation)
    ) {
      state = resetForPath(state, sample);
      return {state: state, decision: null};
    }

    if (!sample.valid) {
      return {state: state, decision: null};
    }
    if (sample.path !== 'turn') {
      state.window = [];
      state.badCount = 0;
      if (sample.path !== 'turn' && sample.valid) state.status = 'normal';
      return {state: state, decision: null};
    }

    if (stableSample(sample)) {
      if (state.stableSince === null) state.stableSince = sample.now;
      if (sample.now - state.stableSince >= STABLE_MS) {
        state.stable = true;
        state.window = [];
        state.badCount = 0;
        if (state.recoveryCount > 0 && state.recoveryStep < MAX_RECOVERY_STEPS) {
          state.recoveryStep += 1;
          state.status = 'recovering';
          state.action = 'recover_bitrate';
          state.stableSince = sample.now;
          return {state: state, decision: state.action};
        }
        state.status = 'normal';
      }
    } else {
      state.stableSince = null;
    }

    if (sample.now < state.cooldownUntil) {
      state.status = state.recoveryCount > 0 ? 'protected' : 'cooldown';
      return {state: state, decision: null};
    }

    var bad = isBadSample(sample);
    state.window = state.window.concat([bad]).slice(-WINDOW_SIZE);
    state.badCount = state.window.filter(Boolean).length;
    if (sample.now - state.connectedAt < OBSERVATION_MS) {
      state.status = 'observing';
      return {state: state, decision: null};
    }
    var trailing = state.window.slice(-REQUIRED_CONSECUTIVE);
    var confirmed =
      state.window.length === WINDOW_SIZE &&
      state.badCount >= REQUIRED_BAD &&
      trailing.length === REQUIRED_CONSECUTIVE &&
      trailing.every(Boolean);

    if (confirmed) {
      state.window = [];
      state.badCount = 0;
      state.stableSince = null;
      state.recoveryStep = 0;
      state.cooldownUntil = sample.now + COOLDOWN_MS;
      if (state.recoveryCount < MAX_AUTO_RESTARTS) {
        state.recoveryCount += 1;
        state.status = 'restarting';
        state.action = 'protect_and_restart_ice';
      } else {
        state.status = 'protected';
        state.action = 'protect_bitrate';
      }
      return {state: state, decision: state.action};
    }
    state.status = bad ? 'degraded-suspected' : state.status === 'protected' ? 'protected' : 'normal';
    return {state: state, decision: null};
  }

  return {
    POLICY_VERSION: POLICY_VERSION,
    createQualityState: createQualityState,
    evaluateQualitySample: evaluateQualitySample,
    isBadSample: isBadSample,
    thresholds: {
      observationMs: OBSERVATION_MS,
      windowSize: WINDOW_SIZE,
      requiredBad: REQUIRED_BAD,
      requiredConsecutive: REQUIRED_CONSECUTIVE,
      cooldownMs: COOLDOWN_MS,
      stableMs: STABLE_MS
    }
  };
});
