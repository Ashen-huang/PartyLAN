(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.GameCastWebRTCAdaptive = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  var POLICY_VERSION = 'adaptive-per-peer-v4';
  var BASELINE_POLICY_VERSION = 'browser-bwe-baseline-v1';
  var OBSERVATION_MS = 4000;
  var WINDOW_SIZE = 3;
  var REQUIRED_BAD = 2;
  var REQUIRED_CONSECUTIVE = 2;
  var DOWNSHIFT_COOLDOWN_MS = 8000;
  var RECOVERY_STABLE_MS = 8000;
  var RECOVERY_PROBE_MS = 6000;
  var RECOVERY_COOLDOWN_MS = 12000;
  var RECOVERY_HEADROOM = 1.05;
  var CASCADE_DEGRADATION_RATIO = 0.75;
  var DEFAULT_MAX_LEVEL = 6;
  var NOMINAL_BITRATE_RATIO = 0.75;
  var DEGRADE_FLOOR_RATIO = 0.65;

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
      maxLevel: DEFAULT_MAX_LEVEL,
      lastDownshiftAvailableBitrate: 0,
      probeFromLevel: null,
      probeStartedAt: 0,
      probeUntil: 0,
      probeBadCount: 0,
      probeValidCount: 0,
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
      lastDownshiftAvailableBitrate: 0,
      probeFromLevel: null,
      probeStartedAt: 0,
      probeUntil: 0,
      probeBadCount: 0,
      probeValidCount: 0,
      lastDecision: null
    });
  }

  function isBadSample(sample) {
    if (!sample.valid) return false;
    var nominalKbps = Number(sample.nominalKbps) || Number(sample.profileKbps) || 0;
    var degradeFloorKbps = Number(sample.degradeFloorKbps) || nominalKbps * DEGRADE_FLOOR_RATIO;
    var lowThroughput =
      degradeFloorKbps > 0 &&
      Number(sample.outboundKbps) > 0 &&
      Number(sample.outboundKbps) < degradeFloorKbps;
    var lowAvailable =
      degradeFloorKbps > 0 &&
      Number(sample.availableOutgoingBitrate) > 0 &&
      Number(sample.availableOutgoingBitrate) < degradeFloorKbps * 1000;
    var highLoss = Number(sample.lossPercent) > 5;
    return sample.receiverImpaired === true || (lowAvailable && lowThroughput) || (
      highLoss && (lowThroughput || Number(sample.jitterMs) > 100)
    );
  }

  function isStableSample(sample) {
    var nominalKbps = Number(sample.nominalKbps) || Number(sample.profileKbps) || 0;
    var degradeFloorKbps = Number(sample.degradeFloorKbps) || nominalKbps * DEGRADE_FLOOR_RATIO;
    var nextNominalKbps = Number(sample.nextNominalKbps) || Number(sample.nextProfileKbps) || nominalKbps;
    var availableOutgoingBitrate = Number(sample.availableOutgoingBitrate) || 0;
    var currentDeliveryHealthy = Number(sample.outboundKbps) >= degradeFloorKbps;
    var advertisedHeadroom = availableOutgoingBitrate >= nextNominalKbps * 1000 * RECOVERY_HEADROOM;
    return (
      sample.valid &&
      Number(sample.lossPercent) < 2 &&
      Number(sample.jitterMs) < 60 &&
      sample.receiverImpaired !== true &&
      nominalKbps > 0 &&
      nextNominalKbps > 0 &&
      (advertisedHeadroom || currentDeliveryHealthy)
    );
  }

  function hasIndependentImpairment(sample) {
    return sample.receiverImpaired === true || Number(sample.lossPercent) > 5 || Number(sample.jitterMs) > 100;
  }

  function sampleCapacityBitrate(sample) {
    var available = Number(sample.availableOutgoingBitrate) || 0;
    if (available > 0) return available;
    var outboundKbps = Number(sample.outboundKbps) || 0;
    return outboundKbps > 0 ? outboundKbps * 1000 : 0;
  }

  function cascadeAllowsDownshift(state, sample, bad) {
    if (!bad || !(state.lastDownshiftAvailableBitrate > 0)) return bad;
    if (hasIndependentImpairment(sample)) return true;
    var capacity = sampleCapacityBitrate(sample);
    return capacity > 0 && capacity <= state.lastDownshiftAvailableBitrate * CASCADE_DEGRADATION_RATIO;
  }

  function clearProbe(state) {
    state.probeFromLevel = null;
    state.probeStartedAt = 0;
    state.probeUntil = 0;
    state.probeBadCount = 0;
    state.probeValidCount = 0;
  }

  function evaluateAdaptiveSample(current, sample) {
    var state = Object.assign({
      maxLevel: DEFAULT_MAX_LEVEL,
      lastDownshiftAvailableBitrate: 0,
      probeFromLevel: null,
      probeStartedAt: 0,
      probeUntil: 0,
      probeBadCount: 0,
      probeValidCount: 0
    }, current, {lastDecision: null});
    if (!state.pairId && sample.pairId) {
      state.pairId = sample.pairId;
      state.generation = sample.generation || 0;
    } else if (
      sample.pairId &&
      (sample.pairId !== state.pairId || (sample.generation || 0) !== state.generation)
    ) {
      return {state: resetForPath(state, sample), decision: null};
    }

    if (Number.isFinite(Number(sample.maxLevel))) {
      state.maxLevel = Math.max(0, Number(sample.maxLevel));
    }
    if (!sample.valid) {
      if (state.probeFromLevel !== null) state.status = 'probing';
      return {state: state, decision: null};
    }

    if (state.probeFromLevel !== null) {
      state.probeValidCount += 1;
      if (isBadSample(sample)) state.probeBadCount += 1;
      if (state.probeBadCount >= 2) {
        var rollbackLevel = state.probeFromLevel;
        var rollbackDecision = {
          type: 'probe-rollback',
          fromLevel: state.level,
          toLevel: rollbackLevel
        };
        state.level = rollbackLevel;
        state.status = 'probe-rollback';
        state.window = [];
        state.badCount = 0;
        state.stableSince = null;
        state.cooldownUntil = sample.now + RECOVERY_COOLDOWN_MS;
        state.lastDecision = rollbackDecision;
        clearProbe(state);
        return {state: state, decision: rollbackDecision};
      }
      if (sample.now >= state.probeUntil && state.probeValidCount >= 2) {
        var committedFromLevel = state.probeFromLevel;
        var commitDecision = {
          type: 'probe-commit',
          fromLevel: committedFromLevel,
          toLevel: state.level
        };
        state.status = state.level === 0 ? 'normal' : 'recovering';
        state.window = [];
        state.badCount = 0;
        state.stableSince = null;
        state.cooldownUntil = sample.now + RECOVERY_COOLDOWN_MS;
        state.lastDownshiftAvailableBitrate = 0;
        state.lastDecision = commitDecision;
        clearProbe(state);
        return {state: state, decision: commitDecision};
      }
      state.status = 'probing';
      return {state: state, decision: null};
    }

    var bad = isBadSample(sample);
    if (bad) {
      state.stableSince = null;
    } else if (isStableSample(sample) && state.level > 0) {
      if (state.stableSince === null) state.stableSince = sample.now;
      if (
        sample.now >= state.cooldownUntil &&
        sample.now - state.stableSince >= RECOVERY_STABLE_MS
      ) {
        var probedLevel = Math.max(0, state.level - 1);
        var recoveryDecision = {
          type: 'probe',
          fromLevel: state.level,
          toLevel: probedLevel
        };
        state.probeFromLevel = state.level;
        state.probeStartedAt = sample.now;
        state.probeUntil = sample.now + RECOVERY_PROBE_MS;
        state.probeBadCount = 0;
        state.probeValidCount = 0;
        state.level = probedLevel;
        state.status = 'probing';
        state.window = [];
        state.badCount = 0;
        state.stableSince = null;
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

    var downshiftBad = cascadeAllowsDownshift(state, sample, bad);
    state.window = state.window.concat([downshiftBad]).slice(-WINDOW_SIZE);
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

    if (confirmed && state.level < state.maxLevel) {
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
      state.lastDownshiftAvailableBitrate = sampleCapacityBitrate(sample);
      state.lastDecision = downshiftDecision;
      return {state: state, decision: downshiftDecision};
    }

    if (confirmed && state.level >= state.maxLevel) state.status = 'floor-protected';
    else if (confirmed) state.status = 'protected';
    else state.status = bad ? 'degraded-suspected' : 'normal';
    return {state: state, decision: null};
  }

  function roundScale(value) {
    return Math.round(value * 1000) / 1000;
  }

  function sourceBitrateForResolution(width, height) {
    if (width <= 1280 && height <= 720) return 12000000;
    if (width <= 1920 && height <= 1080) return 16000000;
    if (width <= 2560 && height <= 1440) return 24000000;
    return 40000000;
  }

  function sourceFPSRatio(fps) {
    if (fps >= 60) return 1;
    if (fps >= 45) return 0.75;
    if (fps >= 30) return 0.5;
    if (fps >= 21) return 1 / 3;
    if (fps > 0) return (1 / 3) * fps / 21;
    return 1 / 3;
  }

  function sourceBitrateForFPS(baseBitrate, fps) {
    return Math.max(800000, Math.round(baseBitrate * sourceFPSRatio(fps) / 100000) * 100000);
  }

  function addEncodingBand(bands, sourceWidth, sourceHeight, configuredFps, candidate) {
    var maxWidth = Math.min(sourceWidth, candidate.maxWidth || sourceWidth);
    var maxHeight = Math.min(sourceHeight, candidate.maxHeight || sourceHeight);
    var maxFramerate = Math.min(configuredFps, candidate.maxFramerate);
    if (!(maxFramerate > 0)) return;
    var previous = bands[bands.length - 1];
    if (
      previous &&
      previous.maxWidth === maxWidth &&
      previous.maxHeight === maxHeight &&
      previous.maxFramerate === maxFramerate
    ) return;
    bands.push({
      maxWidth: maxWidth,
      maxHeight: maxHeight,
      maxFramerate: maxFramerate,
      maxBitrate: candidate.maxBitrate
    });
  }

  function buildEncodingBands(sourceWidth, sourceHeight, configuredFps) {
    var bands = [];
    var highResolutionSource = sourceWidth > 1280 || sourceHeight > 720;
    var sourceBitrate = sourceBitrateForResolution(sourceWidth, sourceHeight);
    var sourceFpsBands = highResolutionSource ? [configuredFps, 45, 30] : [configuredFps, 45, 30, 21];
    sourceFpsBands.forEach(function (fps) {
      if (fps > configuredFps) return;
      addEncodingBand(bands, sourceWidth, sourceHeight, configuredFps, {
        maxFramerate: fps,
        maxBitrate: sourceBitrateForFPS(sourceBitrate, fps)
      });
    });
    if (highResolutionSource) {
      addEncodingBand(bands, sourceWidth, sourceHeight, configuredFps, {
        maxWidth: 1280, maxHeight: 720, maxFramerate: 30, maxBitrate: 6000000
      });
    }
    addEncodingBand(bands, sourceWidth, sourceHeight, configuredFps, {
      maxWidth: 1280, maxHeight: 720, maxFramerate: 21, maxBitrate: 4000000
    });
    addEncodingBand(bands, sourceWidth, sourceHeight, configuredFps, {
      maxWidth: 960, maxHeight: 540, maxFramerate: 21, maxBitrate: 2500000
    });
    addEncodingBand(bands, sourceWidth, sourceHeight, configuredFps, {
      maxWidth: 640, maxHeight: 360, maxFramerate: 18, maxBitrate: 800000
    });
    return bands;
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
    var sourceWidth = Math.max(1, Number(options.sourceWidth) || 1);
    var sourceHeight = Math.max(1, Number(options.sourceHeight) || 1);
    var configuredFps = Math.max(1, Number(options.configuredFps) || 24);
    var profiles = buildEncodingBands(sourceWidth, sourceHeight, configuredFps);
    var maxLevel = Math.max(0, profiles.length - 1);
    var level = Math.max(0, Math.min(maxLevel, Number(options.level) || 0));
    var targetBitrate = Math.max(1, Number(options.targetBitrate) || 1);
    var viewerCount = Math.max(1, Number(options.viewerCount) || 1);
    var serverSafeBitrate = Math.max(1, Number(options.serverSafeBitrate) || targetBitrate);
    var profile = profiles[level] || profiles[0];
    var maxWidth = profile.maxWidth;
    var maxHeight = profile.maxHeight;
    var maxFramerate = profile.maxFramerate;
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
    var nominalBitrate = Math.min(maxBitrate, Math.round(profileBitrate * NOMINAL_BITRATE_RATIO / 100000) * 100000);
    var degradeFloorBitrate = Math.max(
      100000,
      Math.min(maxBitrate, Math.round(nominalBitrate * DEGRADE_FLOOR_RATIO / 100000) * 100000)
    );
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
      nominalBitrate: nominalBitrate,
      degradeFloorBitrate: degradeFloorBitrate,
      maxLevel: maxLevel,
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
      recoveryProbeMs: RECOVERY_PROBE_MS,
      recoveryCooldownMs: RECOVERY_COOLDOWN_MS,
      recoveryHeadroom: RECOVERY_HEADROOM,
      cascadeDegradationRatio: CASCADE_DEGRADATION_RATIO
    }
  };
});
