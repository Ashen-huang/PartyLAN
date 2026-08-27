(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.GameCastWebRTCAdaptive = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  var POLICY_VERSION = 'adaptive-per-peer-v7';
  var BASELINE_POLICY_VERSION = 'browser-bwe-baseline-v1';
  var OBSERVATION_MS = 4000;
  var WINDOW_SIZE = 3;
  var REQUIRED_BAD = 2;
  var REQUIRED_CONSECUTIVE = 2;
  var DOWNSHIFT_COOLDOWN_MS = 6000;
  var FAST_DOWNSHIFT_SETTLE_MS = 2000;
  var RECOVERY_STABLE_MS = 6000;
  var RECOVERY_PROBE_MS = 6000;
  var RECOVERY_COOLDOWN_MS = 8000;
  var RECOVERY_HEADROOM = 1.05;
  var CASCADE_DEGRADATION_RATIO = 0.75;
  var RECEIVER_REPORT_MAX_AGE_MS = 6000;
  var RECEIVER_ACTIVE_FPS = 8;
  var RECEIVER_DECODE_RATIO = 0.5;
  var RECEIVER_HEALTHY_RATIO = 0.75;
  var RECEIVER_JITTER_LIMIT_MS = 100;
  var DEFAULT_MAX_LEVEL = 6;
  var NOMINAL_BITRATE_RATIO = 0.75;
  var DEGRADE_FLOOR_RATIO = 0.65;
  var MAX_SAMPLE_GAP_SECONDS = 70;

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
      windowPressure: '',
      badCount: 0,
      pairId: '',
      generation: 0,
      cooldownUntil: 0,
      stableSince: null,
      maxLevel: DEFAULT_MAX_LEVEL,
      lastDownshiftAvailableBitrate: 0,
      lastDownshiftReceiverDriven: false,
      lastDownshiftPressure: '',
      fastDownshiftReadyAt: 0,
      receiverJitterEwma: 0,
      receiverStatus: 'unavailable',
      receiverReason: 'receiver-unavailable',
      lastReceiverReportId: '',
      probeFromLevel: null,
      probeStartedAt: 0,
      probeUntil: 0,
      probeBadCount: 0,
      probeValidCount: 0,
      probeImprovedCount: 0,
      probeBaselineDecodedFps: 0,
      probeBaselineIncomingKbps: 0,
      probeReceiverRequired: false,
      probePressure: '',
      probeFailureCount: 0,
      pressure: 'unavailable',
      lastDecision: null
    };
  }

  function resetForPath(state, sample) {
    return Object.assign({}, state, {
      connectedAt: sample.now,
      status: 'observing',
      window: [],
      windowPressure: '',
      badCount: 0,
      pairId: sample.pairId || '',
      generation: sample.generation || 0,
      cooldownUntil: 0,
      stableSince: null,
      lastDownshiftAvailableBitrate: 0,
      lastDownshiftReceiverDriven: false,
      lastDownshiftPressure: '',
      fastDownshiftReadyAt: 0,
      receiverJitterEwma: 0,
      receiverStatus: 'unavailable',
      receiverReason: 'receiver-unavailable',
      lastReceiverReportId: '',
      probeFromLevel: null,
      probeStartedAt: 0,
      probeUntil: 0,
      probeBadCount: 0,
      probeValidCount: 0,
      probeImprovedCount: 0,
      probeBaselineDecodedFps: 0,
      probeBaselineIncomingKbps: 0,
      probeReceiverRequired: false,
      probePressure: '',
      probeFailureCount: 0,
      pressure: 'unavailable',
      lastDecision: null
    });
  }

  function pauseAdaptiveState(current) {
    var state = Object.assign({}, current, {
      status: current && current.probeFromLevel !== null && current.probeFromLevel !== undefined
        ? 'probing'
        : 'feedback-paused',
      window: [],
      windowPressure: '',
      badCount: 0,
      stableSince: null,
      receiverJitterEwma: 0,
      receiverStatus: 'unavailable',
      receiverReason: 'receiver-unavailable',
      probeBadCount: 0,
      probeValidCount: 0,
      probeImprovedCount: 0,
      pressure: 'unavailable',
      lastDecision: null
    });
    return state;
  }

  function finiteNumber(value) {
    var result = Number(value);
    return Number.isFinite(result) ? result : null;
  }

  function assessReceiver(sample) {
    var ageMs = finiteNumber(sample.receiverReportAgeMs);
    var decodedFps = finiteNumber(sample.receiverDecodedFps);
    var incomingKbps = finiteNumber(sample.receiverIncomingKbps);
    var senderFps = Math.max(
      0,
      finiteNumber(sample.senderSentFps) || 0,
      finiteNumber(sample.senderEncodedFps) || 0
    );
    var sourceFps = Math.max(
      senderFps,
      finiteNumber(sample.senderCapturedFps) || 0
    );
    var bound = sample.receiverBound === true && ageMs !== null &&
      ageMs >= 0 && ageMs <= RECEIVER_REPORT_MAX_AGE_MS && decodedFps !== null;
    var sourceActive = sourceFps >= RECEIVER_ACTIVE_FPS;
    var senderDeliveryCollapsed = sourceActive &&
      senderFps < Math.max(RECEIVER_ACTIVE_FPS, sourceFps * RECEIVER_DECODE_RATIO);
    if (!bound) {
      return {
        bound: false,
        impaired: false,
        healthy: false,
        reason: 'receiver-unavailable',
        senderFps: senderFps,
        sourceFps: sourceFps,
        senderCollapsed: senderDeliveryCollapsed,
        decodedFps: decodedFps || 0,
        incomingKbps: incomingKbps || 0
      };
    }

    var senderActive = senderFps >= RECEIVER_ACTIVE_FPS;
    var decodeRatio = senderFps > 0 ? decodedFps / senderFps : 1;
    var receiverLoss = Math.max(0, finiteNumber(sample.receiverLossPercent) || 0);
    var receiverJitter = Math.max(
      0,
      finiteNumber(sample.receiverSmoothedJitterMs) ||
        finiteNumber(sample.receiverJitterMs) || 0
    );
    var outboundKbps = Math.max(0, finiteNumber(sample.outboundKbps) || 0);
    var throughputGap = senderActive && outboundKbps > 0 && incomingKbps !== null &&
      incomingKbps < outboundKbps * 0.5;
    var decodeImpaired = senderActive && (
      decodedFps < RECEIVER_ACTIVE_FPS || decodeRatio < RECEIVER_DECODE_RATIO
    );
    var transportImpaired = throughputGap &&
      (receiverLoss > 5 || receiverJitter > RECEIVER_JITTER_LIMIT_MS);
    var impaired = decodeImpaired || transportImpaired;
    var receiverFollowsSender = senderFps <= 0 || decodeRatio >= RECEIVER_HEALTHY_RATIO;
    var healthyDecode = !sourceActive || receiverFollowsSender;
    var healthy = healthyDecode && receiverLoss < 3 &&
      receiverJitter <= RECEIVER_JITTER_LIMIT_MS;
    var reason = 'receiver-healthy';
    if (decodeImpaired) reason = decodedFps < 1 ? 'receiver-no-frames' : 'receiver-low-fps';
    else if (transportImpaired) reason = 'receiver-throughput-gap';
    else if (!healthy) reason = 'receiver-unstable';
    return {
      bound: true,
      impaired: impaired,
      healthy: healthy,
      reason: reason,
      senderFps: senderFps,
      sourceFps: sourceFps,
      senderCollapsed: senderDeliveryCollapsed,
      receiverFollowsSender: receiverFollowsSender,
      decodedFps: decodedFps,
      incomingKbps: incomingKbps || 0,
      lossPercent: receiverLoss,
      jitterMs: receiverJitter
    };
  }

  function hasSenderCpuPressure(sample, receiver) {
    return receiver.sourceFps >= RECEIVER_ACTIVE_FPS && (
      String(sample.qualityLimitationReason || '').toLowerCase() === 'cpu' ||
      receiver.senderCollapsed
    );
  }

  function classifyPressure(sample, receiver) {
    if (!sample.valid) return 'unavailable';
    if (!receiver.bound) {
      return hasSenderCpuPressure(sample, receiver) ? 'cpu' : 'unavailable';
    }
    if (receiver.impaired || sample.receiverImpaired === true) return 'network';
    if (!receiver.healthy) return 'unavailable';
    if (
      receiver.receiverFollowsSender && (
        hasSenderCpuPressure(sample, receiver)
      )
    ) return 'cpu';
    return 'healthy';
  }

  function isBadSample(sample) {
    if (!sample.valid) return false;
    var receiver = assessReceiver(sample);
    var pressure = classifyPressure(sample, receiver);
    if (receiver.bound || sample.receiverBound === false || sample.receiverBound === true) {
      return pressure === 'cpu' || pressure === 'network';
    }
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
    var receiver = assessReceiver(sample);
    if (receiver.bound && classifyPressure(sample, receiver) !== 'healthy') return false;
    if (sample.receiverBound === false) return false;
    var nominalKbps = Number(sample.nominalKbps) || Number(sample.profileKbps) || 0;
    var degradeFloorKbps = Number(sample.degradeFloorKbps) || nominalKbps * DEGRADE_FLOOR_RATIO;
    var nextNominalKbps = Number(sample.nextNominalKbps) || Number(sample.nextProfileKbps) || nominalKbps;
    var availableOutgoingBitrate = Number(sample.availableOutgoingBitrate) || 0;
    var currentDeliveryHealthy = Number(sample.outboundKbps) >= degradeFloorKbps;
    var advertisedHeadroom = availableOutgoingBitrate >= nextNominalKbps * 1000 * RECOVERY_HEADROOM;
    return (
      sample.valid &&
      Number(sample.lossPercent) < 2 &&
      Number(sample.jitterMs) < (receiver.bound ? 120 : 60) &&
      sample.receiverImpaired !== true &&
      nominalKbps > 0 &&
      nextNominalKbps > 0 &&
      (advertisedHeadroom || currentDeliveryHealthy)
    );
  }

  function hasIndependentImpairment(sample) {
    var receiver = assessReceiver(sample);
    return classifyPressure(sample, receiver) === 'network' ||
      Number(sample.lossPercent) > 5 || Number(sample.jitterMs) > 100;
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
    state.probeImprovedCount = 0;
    state.probeBaselineDecodedFps = 0;
    state.probeBaselineIncomingKbps = 0;
    state.probeReceiverRequired = false;
    state.probePressure = '';
  }

  function sampleEffectiveCapacityKbps(sample) {
    var advertised = Math.max(0, Number(sample.availableOutgoingBitrate) || 0) / 1000;
    var receiver = assessReceiver(sample);
    var delivered = receiver.bound ? Math.max(0, receiver.incomingKbps) * 1.2 : 0;
    if (advertised > 0 && delivered > 0) return Math.min(advertised, delivered);
    return advertised || delivered || Math.max(0, Number(sample.outboundKbps) || 0);
  }

  function sustainableLevel(currentLevel, maxLevel, sample) {
    var floors = Array.isArray(sample.levelDegradeFloorsKbps)
      ? sample.levelDegradeFloorsKbps
      : [];
    var capacity = sampleEffectiveCapacityKbps(sample);
    if (!(capacity > 0) || floors.length <= currentLevel + 1) {
      return Math.min(maxLevel, currentLevel + 1);
    }
    var sustainableCapacity = capacity / RECOVERY_HEADROOM;
    for (var level = currentLevel + 1; level <= maxLevel; level += 1) {
      var floor = Number(floors[level]) || 0;
      if (floor > 0 && floor <= sustainableCapacity) return level;
    }
    return maxLevel;
  }

  function rollbackProbe(state, sample) {
    var rollbackLevel = state.probeFromLevel;
    var rollbackDecision = {
      type: 'probe-rollback',
      fromLevel: state.level,
      toLevel: rollbackLevel
    };
    state.level = rollbackLevel;
    state.status = 'probe-rollback';
    state.window = [];
    state.windowPressure = '';
    state.badCount = 0;
    state.stableSince = null;
    state.probeFailureCount = Math.min(3, (state.probeFailureCount || 0) + 1);
    state.cooldownUntil = sample.now + RECOVERY_COOLDOWN_MS * state.probeFailureCount;
    state.lastDecision = rollbackDecision;
    clearProbe(state);
    return {state: state, decision: rollbackDecision};
  }

  function evaluateAdaptiveSample(current, sample) {
    var state = Object.assign({
      maxLevel: DEFAULT_MAX_LEVEL,
      lastDownshiftAvailableBitrate: 0,
      lastDownshiftReceiverDriven: false,
      lastDownshiftPressure: '',
      fastDownshiftReadyAt: 0,
      receiverJitterEwma: 0,
      receiverStatus: 'unavailable',
      receiverReason: 'receiver-unavailable',
      lastReceiverReportId: '',
      probeFromLevel: null,
      probeStartedAt: 0,
      probeUntil: 0,
      probeBadCount: 0,
      probeValidCount: 0,
      probeImprovedCount: 0,
      probeBaselineDecodedFps: 0,
      probeBaselineIncomingKbps: 0,
      probeReceiverRequired: false,
      probePressure: '',
      probeFailureCount: 0,
      windowPressure: ''
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
    sample = Object.assign({}, sample);
    if (sample.receiverBound === true && Number.isFinite(Number(sample.receiverJitterMs))) {
      var receiverJitter = Math.max(0, Number(sample.receiverJitterMs));
      state.receiverJitterEwma = state.receiverJitterEwma > 0
        ? state.receiverJitterEwma * 0.7 + receiverJitter * 0.3
        : receiverJitter;
      sample.receiverSmoothedJitterMs = state.receiverJitterEwma;
    }
    var receiver = assessReceiver(sample);
    var pressure = classifyPressure(sample, receiver);
    var receiverReportId = receiver.bound
      ? String(sample.receiverReportedAtUnix || '')
      : '';
    var receiverFresh = !receiverReportId || receiverReportId !== state.lastReceiverReportId;
    if (receiverReportId && receiverFresh) state.lastReceiverReportId = receiverReportId;
    state.receiverStatus = !receiver.bound
      ? 'unavailable'
      : (pressure === 'network'
        ? 'impaired'
        : (pressure === 'cpu' || pressure === 'healthy' ? 'healthy' : 'unavailable'));
    state.receiverReason = receiver.reason;
    state.pressure = pressure;
    if (!sample.valid) {
      return {state: pauseAdaptiveState(state), decision: null};
    }

    if (state.probeFromLevel !== null) {
      if (!receiver.bound || !receiverFresh || !receiverReportId) {
        state.status = 'probing';
        return {state: state, decision: null};
      }
      state.probeValidCount += 1;
      var probePressure = state.probePressure || 'network';
      if (pressure === probePressure) state.probeBadCount += 1;
      if (pressure === 'healthy') state.probeImprovedCount += 1;
      if (state.probeBadCount >= 2) {
        return rollbackProbe(state, sample);
      }
      if (sample.now >= state.probeUntil) {
        if (state.probeValidCount < 2 || state.probeImprovedCount < 2) {
          state.status = 'probing';
          return {state: state, decision: null};
        }
        var committedFromLevel = state.probeFromLevel;
        var committedPressure = probePressure;
        var commitDecision = {
          type: 'probe-commit',
          fromLevel: committedFromLevel,
          toLevel: state.level
        };
        state.status = state.level === 0 ? 'normal' : 'recovering';
        state.window = [];
        state.windowPressure = '';
        state.badCount = 0;
        state.stableSince = null;
        state.cooldownUntil = sample.now + RECOVERY_STABLE_MS;
        state.lastDownshiftAvailableBitrate = 0;
        state.lastDownshiftReceiverDriven = false;
        state.fastDownshiftReadyAt = 0;
        state.lastDownshiftPressure = state.level === 0 ? '' : committedPressure;
        state.probeFailureCount = 0;
        state.lastDecision = commitDecision;
        clearProbe(state);
        return {state: state, decision: commitDecision};
      }
      state.status = 'probing';
      return {state: state, decision: null};
    }

    var receiverExplicitlyUnavailable = sample.receiverBound === false ||
      (sample.receiverBound === true && !receiver.bound);
    if (receiverExplicitlyUnavailable && pressure !== 'cpu') {
      state.status = 'sender-observing';
      state.window = [];
      state.windowPressure = '';
      state.badCount = 0;
      state.stableSince = null;
      return {state: state, decision: null};
    }

    var bad = isBadSample(sample);
    if (
      pressure === 'network' && receiverFresh && state.lastDownshiftReceiverDriven &&
      sample.now >= state.fastDownshiftReadyAt && state.level < state.maxLevel
    ) {
      var fastLevel = Math.min(
        sustainableLevel(state.level, state.maxLevel, sample),
        state.level + 2
      );
      if (fastLevel > state.level) {
        var fastDecision = {
          type: 'downshift-fast',
          fromLevel: state.level,
          toLevel: fastLevel
        };
        state.level = fastLevel;
        state.status = fastLevel >= state.maxLevel ? 'floor-protected' : 'degraded-fast';
        state.window = [];
        state.windowPressure = '';
        state.badCount = 0;
        state.stableSince = null;
        state.cooldownUntil = sample.now + DOWNSHIFT_COOLDOWN_MS;
        state.lastDownshiftAvailableBitrate = sampleCapacityBitrate(sample);
        state.lastDownshiftReceiverDriven = false;
        state.lastDownshiftPressure = 'network';
        state.fastDownshiftReadyAt = 0;
        state.lastDecision = fastDecision;
        return {state: state, decision: fastDecision};
      }
    }
    if (receiver.bound && !receiverFresh) {
      state.status = receiver.impaired ? 'receiver-waiting' : state.status;
      return {state: state, decision: null};
    }
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
        state.probeImprovedCount = 0;
        state.probeBaselineDecodedFps = receiver.bound ? receiver.decodedFps : 0;
        state.probeBaselineIncomingKbps = receiver.bound ? receiver.incomingKbps : 0;
        state.probeReceiverRequired = receiver.bound;
        state.probePressure = state.lastDownshiftPressure || 'network';
        state.level = probedLevel;
        state.status = 'probing';
        state.window = [];
        state.windowPressure = '';
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

    var downshiftBad = pressure === 'cpu'
      ? bad
      : cascadeAllowsDownshift(state, sample, bad);
    var evidencePressure = pressure === 'cpu' || pressure === 'network'
      ? pressure
      : (downshiftBad ? 'legacy' : '');
    if (
      downshiftBad && state.windowPressure &&
      state.windowPressure !== evidencePressure
    ) {
      state.window = [];
      state.badCount = 0;
    }
    if (downshiftBad) state.windowPressure = evidencePressure;
    state.window = state.window.concat([downshiftBad]).slice(-WINDOW_SIZE);
    state.badCount = state.window.filter(Boolean).length;
    if (sample.now - state.connectedAt < OBSERVATION_MS) {
      state.status = 'observing';
      return {state: state, decision: null};
    }

    var trailing = state.window.slice(-REQUIRED_CONSECUTIVE);
    var confirmed =
      state.badCount >= REQUIRED_BAD &&
      trailing.length === REQUIRED_CONSECUTIVE &&
      trailing.every(Boolean) &&
      (pressure === 'cpu' || pressure === 'network' || state.window.length === WINDOW_SIZE);

    if (confirmed && state.level < state.maxLevel) {
      var nextLevel = state.level + 1;
      var downshiftDecision = {
        type: pressure === 'cpu' ? 'downshift-cpu' : 'downshift',
        fromLevel: state.level,
        toLevel: nextLevel
      };
      state.level = nextLevel;
      state.status = pressure === 'cpu' ? 'degraded-cpu' : 'degraded-confirmed';
      state.window = [];
      state.windowPressure = '';
      state.badCount = 0;
      state.stableSince = null;
      state.cooldownUntil = sample.now + DOWNSHIFT_COOLDOWN_MS;
      state.lastDownshiftAvailableBitrate = sampleCapacityBitrate(sample);
      state.lastDownshiftReceiverDriven = pressure === 'network';
      state.lastDownshiftPressure = pressure;
      state.fastDownshiftReadyAt = pressure === 'network'
        ? sample.now + FAST_DOWNSHIFT_SETTLE_MS
        : 0;
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
    if (elapsedSeconds > MAX_SAMPLE_GAP_SECONDS) {
      return {valid: false, invalidReason: 'sample-gap'};
    }
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
    pauseAdaptiveState: pauseAdaptiveState,
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
      fastDownshiftSettleMs: FAST_DOWNSHIFT_SETTLE_MS,
      recoveryStableMs: RECOVERY_STABLE_MS,
      recoveryProbeMs: RECOVERY_PROBE_MS,
      recoveryCooldownMs: RECOVERY_COOLDOWN_MS,
      recoveryHeadroom: RECOVERY_HEADROOM,
      cascadeDegradationRatio: CASCADE_DEGRADATION_RATIO,
      receiverReportMaxAgeMs: RECEIVER_REPORT_MAX_AGE_MS,
      receiverJitterLimitMs: RECEIVER_JITTER_LIMIT_MS
    }
  };
});
