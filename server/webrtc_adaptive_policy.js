(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.GameCastWebRTCAdaptive = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  var POLICY_VERSION = 'adaptive-per-peer-v3';
  var BASELINE_POLICY_VERSION = 'browser-bwe-baseline-v1';
  var OBSERVATION_MS = 4000;
  var WINDOW_SIZE = 3;
  var REQUIRED_BAD = 2;
  var REQUIRED_CONSECUTIVE = 2;
  var DOWNSHIFT_COOLDOWN_MS = 8000;
  var RECOVERY_STABLE_MS = 10000;
  var RECOVERY_COOLDOWN_MS = 12000;
  var RECOVERY_HEADROOM = 1.2;
  var MAX_LEVEL = 5;
  var PROFILES = {
    0: {preserveSource: true, maxFramerate: 60, maxBitrate: 10000000},
    1: {preserveSource: true, maxFramerate: 45, maxBitrate: 8000000},
    2: {preserveSource: true, maxFramerate: 30, maxBitrate: 5000000},
    3: {maxWidth: 1280, maxHeight: 720, maxFramerate: 30, maxBitrate: 3000000},
    4: {maxWidth: 960, maxHeight: 540, maxFramerate: 24, maxBitrate: 1700000},
    5: {maxWidth: 640, maxHeight: 360, maxFramerate: 18, maxBitrate: 600000}
  };

  function resolveAdaptiveMode(roomId, search) {
    if (String(roomId || '').toLowerCase() !== 'test') return POLICY_VERSION;
    var query = String(search || '').replace(/^\?/, '').split('&');
    for (var index = 0; index < query.length; index++) {
      var parts = query[index].split('=');
      var key = decodeURIComponent(parts[0] || '');
      var value = decodeURIComponent(parts.slice(1).join('=') || '');
      if (key === 'adaptive' && value === 'baseline') return BASELINE_POLICY_VERSION;
    }
    return POLICY_VERSION;
  }

  function createAdaptiveState(connectedAt) {
    return {
      policyVersion: POLICY_VERSION,
      connectedAt: connectedAt || 0,
      level: 0,
      status: 'observing',
      window: [],
      badCount: 0,
      pairId: '',
      generation: 0,
      cooldownUntil: 0,
      stableSince: null,
      lastDecision: null
    };
  }

  function resetForPath(state, sample) {
    return Object.assign({}, state, {
      connectedAt: sample.now,
      status: 'observing',
      window: [],
      badCount: 0,
      pairId: sample.pairId || '',
      generation: sample.generation || 0,
      cooldownUntil: 0,
      stableSince: null,
      lastDecision: null
    });
  }

  function isBadSample(sample) {
    if (!sample.valid) return false;
    var profileKbps = Number(sample.profileKbps) || 0;
    var lowThroughput =
      profileKbps > 0 &&
      Number(sample.outboundKbps) >= 0 &&
      Number(sample.outboundKbps) < profileKbps * 0.8;
    var lowAvailable =
      profileKbps > 0 &&
      Number(sample.availableOutgoingBitrate) > 0 &&
      Number(sample.availableOutgoingBitrate) < profileKbps * 1000 * 0.8;
    var highLoss = Number(sample.lossPercent) > 5;
    var bandwidthLimited = sample.qualityLimitationReason === 'bandwidth';
    return bandwidthLimited || lowAvailable || (
      highLoss && (lowThroughput || Number(sample.jitterMs) > 100)
    );
  }

  function isStableSample(sample) {
    var profileKbps = Number(sample.profileKbps) || 0;
    var nextProfileKbps = Number(sample.nextProfileKbps) || profileKbps;
    var availableOutgoingBitrate = Number(sample.availableOutgoingBitrate) || 0;
    return (
      sample.valid &&
      Number(sample.lossPercent) < 1 &&
      Number(sample.jitterMs) < 40 &&
      sample.qualityLimitationReason !== 'bandwidth' &&
      profileKbps > 0 &&
      nextProfileKbps > 0 &&
      availableOutgoingBitrate >= nextProfileKbps * 1000 * RECOVERY_HEADROOM
    );
  }

  function evaluateAdaptiveSample(current, sample) {
    var state = Object.assign({}, current, {lastDecision: null});
    if (!state.pairId && sample.pairId) {
      state.pairId = sample.pairId;
      state.generation = sample.generation || 0;
    } else if (
      sample.pairId &&
      (sample.pairId !== state.pairId || (sample.generation || 0) !== state.generation)
    ) {
      return {state: resetForPath(state, sample), decision: null};
    }

    if (!sample.valid) return {state: state, decision: null};

    var bad = isBadSample(sample);
    if (bad) {
      state.stableSince = null;
    } else if (isStableSample(sample) && state.level > 0) {
      if (state.stableSince === null) state.stableSince = sample.now;
      if (
        sample.now >= state.cooldownUntil &&
        sample.now - state.stableSince >= RECOVERY_STABLE_MS
      ) {
        var recoveredLevel = Math.max(0, state.level - 1);
        var recoveryDecision = {
          type: 'recover',
          fromLevel: state.level,
          toLevel: recoveredLevel
        };
        state.level = recoveredLevel;
        state.status = recoveredLevel === 0 ? 'normal' : 'recovering';
        state.window = [];
        state.badCount = 0;
        state.stableSince = null;
        state.cooldownUntil = sample.now + RECOVERY_COOLDOWN_MS;
        state.lastDecision = recoveryDecision;
        return {state: state, decision: recoveryDecision};
      }
    } else {
      state.stableSince = null;
    }

    if (sample.now < state.cooldownUntil) {
      state.status = 'cooldown';
      return {state: state, decision: null};
    }

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

    if (confirmed && state.level < MAX_LEVEL) {
      var nextLevel = state.level + 1;
      var downshiftDecision = {
        type: 'downshift',
        fromLevel: state.level,
        toLevel: nextLevel
      };
      state.level = nextLevel;
      state.status = 'degraded-confirmed';
      state.window = [];
      state.badCount = 0;
      state.stableSince = null;
      state.cooldownUntil = sample.now + DOWNSHIFT_COOLDOWN_MS;
      state.lastDecision = downshiftDecision;
      return {state: state, decision: downshiftDecision};
    }

    if (confirmed) state.status = 'protected';
    else state.status = bad ? 'degraded-suspected' : 'normal';
    return {state: state, decision: null};
  }

  function roundScale(value) {
    return Math.round(value * 1000) / 1000;
  }

  function computeSenderDeltas(current, previous) {
    if (!current || !previous) return {valid: false, invalidReason: 'baseline'};
    var elapsedSeconds = (Number(current.now) - Number(previous.now)) / 1000;
    var bytesSentDelta = Number(current.bytesSent) - Number(previous.bytesSent);
    var packetsSentDelta = Number(current.packetsSent) - Number(previous.packetsSent);
    var packetsLostDelta = Number(current.packetsLost) - Number(previous.packetsLost);
    if (elapsedSeconds > 10) return {valid: false, invalidReason: 'sample-gap'};
    if (
      !(elapsedSeconds > 0) ||
      bytesSentDelta < 0 ||
      packetsSentDelta < 0 ||
      packetsLostDelta < 0
    ) {
      return {valid: false, invalidReason: 'counter-reset'};
    }
    if (packetsSentDelta < 20) return {valid: false, invalidReason: 'insufficient-packets'};
    return {
      valid: true,
      invalidReason: '',
      outboundKbps: bytesSentDelta * 8 / elapsedSeconds / 1000,
      lossPercent: packetsLostDelta * 100 / Math.max(1, packetsSentDelta)
    };
  }

  function computeEncodingProfile(options) {
    var level = Math.max(0, Math.min(MAX_LEVEL, Number(options.level) || 0));
    var sourceWidth = Math.max(1, Number(options.sourceWidth) || 1);
    var sourceHeight = Math.max(1, Number(options.sourceHeight) || 1);
    var configuredFps = Math.max(1, Number(options.configuredFps) || 24);
    var targetBitrate = Math.max(1, Number(options.targetBitrate) || 1);
    var viewerCount = Math.max(1, Number(options.viewerCount) || 1);
    var serverSafeBitrate = Math.max(1, Number(options.serverSafeBitrate) || targetBitrate);
    var profile = PROFILES[level] || PROFILES[0];
    var maxWidth = profile.preserveSource ? sourceWidth : Math.min(sourceWidth, profile.maxWidth);
    var maxHeight = profile.preserveSource ? sourceHeight : Math.min(sourceHeight, profile.maxHeight);
    var maxFramerate = Math.min(configuredFps, profile.maxFramerate);
    var profileBitrate = profile.maxBitrate;
    var applyServerBudget = options.applyServerBudget === true;
    var protectionMaxWidth = Math.max(0, Number(options.protectionMaxWidth) || 0);
    var protectionMaxHeight = Math.max(0, Number(options.protectionMaxHeight) || 0);
    var protectionMaxBitrate = Math.max(0, Number(options.protectionMaxBitrate) || 0);
    if (protectionMaxWidth > 0) maxWidth = Math.min(maxWidth, protectionMaxWidth);
    if (protectionMaxHeight > 0) maxHeight = Math.min(maxHeight, protectionMaxHeight);
    if (protectionMaxBitrate > 0) profileBitrate = Math.min(profileBitrate, protectionMaxBitrate);
    var budgetBitrate = applyServerBudget ? serverSafeBitrate / viewerCount : targetBitrate;
    var maxBitrate = Math.round(Math.min(targetBitrate, profileBitrate, budgetBitrate));
    var scaleResolutionDownBy = roundScale(Math.max(
      1,
      sourceWidth / Math.max(1, maxWidth),
      sourceHeight / Math.max(1, maxHeight)
    ));
    return {
      level: level,
      maxWidth: Math.round(maxWidth),
      maxHeight: Math.round(maxHeight),
      maxFramerate: Math.round(maxFramerate),
      maxBitrate: maxBitrate,
      scaleResolutionDownBy: scaleResolutionDownBy
    };
  }

  return {
    POLICY_VERSION: POLICY_VERSION,
    BASELINE_POLICY_VERSION: BASELINE_POLICY_VERSION,
    resolveAdaptiveMode: resolveAdaptiveMode,
    createAdaptiveState: createAdaptiveState,
    evaluateAdaptiveSample: evaluateAdaptiveSample,
    computeEncodingProfile: computeEncodingProfile,
    computeSenderDeltas: computeSenderDeltas,
    isBadSample: isBadSample,
    thresholds: {
      observationMs: OBSERVATION_MS,
      windowSize: WINDOW_SIZE,
      requiredBad: REQUIRED_BAD,
      requiredConsecutive: REQUIRED_CONSECUTIVE,
      downshiftCooldownMs: DOWNSHIFT_COOLDOWN_MS,
      recoveryStableMs: RECOVERY_STABLE_MS,
      recoveryCooldownMs: RECOVERY_COOLDOWN_MS,
      recoveryHeadroom: RECOVERY_HEADROOM
    }
  };
});
