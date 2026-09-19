(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.GameCastWebRTCAdaptive = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  var POLICY_VERSION = 'adaptive-per-peer-v11-buffered-45fps';
  var BASELINE_POLICY_VERSION = 'browser-bwe-baseline-v1';
  var OBSERVATION_MS = 4000;
  var WINDOW_SIZE = 3;
  var REQUIRED_BAD = 2;
  var REQUIRED_CONSECUTIVE = 2;
  var DOWNSHIFT_COOLDOWN_MS = 6000;
  var FAST_DOWNSHIFT_SETTLE_MS = 6000;
  var RECOVERY_STABLE_MS = 12000;
  var RECOVERY_PROBE_MS = 8000;
  var STABILITY_LAB_RECOVERY_STABLE_MS = 20000;
  var STABILITY_LAB_RECOVERY_PROBE_MS = 10000;
  var RECOVERY_COOLDOWN_MS = 8000;
  var RECOVERY_HEADROOM = 1.15;
  var CASCADE_DEGRADATION_RATIO = 0.75;
  var RECEIVER_REPORT_MAX_AGE_MS = 6000;
  var RECEIVER_ACTIVE_FPS = 8;
  var RECEIVER_DECODE_RATIO = 0.5;
  var RECEIVER_HEALTHY_RATIO = 0.75;
  var RECEIVER_JITTER_LIMIT_MS = 100;
  var DEFAULT_MAX_LEVEL = 8;
  var RECEIVER_BUFFER_MIN_MS = 80;
  var RECEIVER_BUFFER_MAX_MS = 300;
  var RECEIVER_BUFFER_STEPS_MS = [80, 150, 220, 300];
  var RECEIVER_BUFFER_STRESS_SAMPLES = 2;
  var RECEIVER_BUFFER_RECOVERY_SAMPLES = 8;
  var NOMINAL_BITRATE_RATIO = 0.75;
  var DEGRADE_FLOOR_RATIO = 0.65;
  var MAX_SAMPLE_GAP_SECONDS = 70;
  var CAPACITY_PRESSURE_RATIO = 0.85;
  var CAPACITY_RISE_RATIO = 1.25;
  var CONTENT_FRAME_MIN_KBITS = 12;
  var CONTENT_FRAME_RATIO = 0.25;
  var PACKET_SEND_DELAY_CONGESTED_MS = 30;
  var RETRANSMIT_CONGESTED_PERCENT = 3;
  var BANDWIDTH_LIMITED_MIN_MS = 700;
  var SOFT_PROTECTION_RATIO = 0.8;
  var SOFT_PROTECTION_RECOVERY_RATIO = 1.12;
  var SOFT_PROTECTION_MAX_MS = 6000;
  var SOFT_PROTECTION_REQUIRED_SAMPLES = 2;
  var TIER_TRANSITION_DWELL_MS = 4000;

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
      safeCapacityKbps: 0,
      recoveryCapacityKbps: 0,
      recoveryHealthySamples: 0,
      recoveryCapacityAt: 0,
      recoveryGateReason: 'not-started',
      currentDegradeFloorKbps: 0,
      recoveryTargetFloorKbps: 0,
      lastDemandAt: 0,
      contentState: 'idle',
      receiverJitterEwma: 0,
      receiverStatus: 'unavailable',
      receiverReason: 'receiver-unavailable',
      receiverBufferTargetMs: RECEIVER_BUFFER_MIN_MS,
      receiverBufferActualMs: RECEIVER_BUFFER_MIN_MS,
      receiverBufferReason: 'baseline',
      receiverFlowDeficit: false,
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
      probeTargetFloorKbps: 0,
      probePressure: '',
      probeFailureCount: 0,
      pressure: 'unavailable',
      softProtectionKbps: 0,
      softProtectionUntil: 0,
      softBadCount: 0,
      softHealthyCount: 0,
      softProtectionExpiredAt: 0,
      lastTransitionAt: 0,
      transitionReason: '',
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
      safeCapacityKbps: 0,
      recoveryCapacityKbps: 0,
      recoveryHealthySamples: 0,
      recoveryCapacityAt: 0,
      recoveryGateReason: 'path-reset',
      currentDegradeFloorKbps: 0,
      recoveryTargetFloorKbps: 0,
      lastDemandAt: 0,
      contentState: 'idle',
      receiverJitterEwma: 0,
      receiverStatus: 'unavailable',
      receiverReason: 'receiver-unavailable',
      receiverBufferTargetMs: RECEIVER_BUFFER_MIN_MS,
      receiverBufferActualMs: RECEIVER_BUFFER_MIN_MS,
      receiverBufferReason: 'path-reset',
      receiverFlowDeficit: false,
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
      probeTargetFloorKbps: 0,
      probePressure: '',
      probeFailureCount: 0,
      pressure: 'unavailable',
      softProtectionKbps: 0,
      softProtectionUntil: 0,
      softBadCount: 0,
      softHealthyCount: 0,
      softProtectionExpiredAt: 0,
      lastTransitionAt: 0,
      transitionReason: 'path-reset',
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
      receiverBufferReason: 'paused',
      receiverFlowDeficit: false,
      probeBadCount: 0,
      probeValidCount: 0,
      probeImprovedCount: 0,
      pressure: 'unavailable',
      softBadCount: 0,
      softHealthyCount: 0,
      softProtectionExpiredAt: 0,
      transitionReason: 'paused',
      lastDecision: null
    });
    return state;
  }

  function finiteNumber(value) {
    var result = Number(value);
    return Number.isFinite(result) ? result : null;
  }

  function normalizeReceiverBufferTarget(value) {
    var numeric = finiteNumber(value);
    if (numeric === null) return RECEIVER_BUFFER_MIN_MS;
    numeric = Math.max(RECEIVER_BUFFER_MIN_MS, Math.min(RECEIVER_BUFFER_MAX_MS, numeric));
    var closest = RECEIVER_BUFFER_STEPS_MS[0];
    var distance = Math.abs(numeric - closest);
    for (var index = 1; index < RECEIVER_BUFFER_STEPS_MS.length; index += 1) {
      var candidate = RECEIVER_BUFFER_STEPS_MS[index];
      var candidateDistance = Math.abs(numeric - candidate);
      if (candidateDistance < distance) {
        closest = candidate;
        distance = candidateDistance;
      }
    }
    return closest;
  }

  function createReceiverBufferState() {
    return {
      targetMs: RECEIVER_BUFFER_MIN_MS,
      actualMs: 0,
      actualMeasured: false,
      stressCount: 0,
      healthyCount: 0,
      flowDeficit: false,
      reason: 'baseline'
    };
  }

  function nextReceiverBufferTarget(value) {
    var current = normalizeReceiverBufferTarget(value);
    for (var index = 0; index < RECEIVER_BUFFER_STEPS_MS.length; index += 1) {
      if (RECEIVER_BUFFER_STEPS_MS[index] > current) return RECEIVER_BUFFER_STEPS_MS[index];
    }
    return RECEIVER_BUFFER_MAX_MS;
  }

  function previousReceiverBufferTarget(value) {
    var current = normalizeReceiverBufferTarget(value);
    for (var index = RECEIVER_BUFFER_STEPS_MS.length - 1; index >= 0; index -= 1) {
      if (RECEIVER_BUFFER_STEPS_MS[index] < current) return RECEIVER_BUFFER_STEPS_MS[index];
    }
    return RECEIVER_BUFFER_MIN_MS;
  }

  // Buffering is deliberately driven by transport evidence plus a frame deficit.
  // A static/CPU-limited source must not accumulate latency just because its FPS is low.
  function evaluateReceiverBufferSample(current, sample) {
    var state = Object.assign(createReceiverBufferState(), current || {});
    sample = sample || {};
    state.targetMs = normalizeReceiverBufferTarget(state.targetMs);
    state.actualMs = Math.max(0, finiteNumber(state.actualMs) || 0);
    var targetFps = Math.max(0, finiteNumber(sample.targetFps) || 0);
    var decodedFps = finiteNumber(sample.decodedFps);
    var arrivalFps = finiteNumber(sample.arrivalFps);
    var lossPercent = Math.max(0, finiteNumber(sample.lossPercent) || 0);
    var jitterMs = Math.max(0, finiteNumber(sample.jitterMs) || 0);
    var dropped = Math.max(0, finiteNumber(sample.framesDroppedDelta) || 0);
    var transportEvidence = sample.networkEvidence === true || lossPercent >= 1 ||
      jitterMs >= 35 || dropped >= 1 || (
        arrivalFps !== null && decodedFps !== null && targetFps > 0 &&
        arrivalFps >= Math.max(RECEIVER_ACTIVE_FPS, targetFps * 0.8) &&
        decodedFps < arrivalFps * 0.85
      );
    var flowDeficit = targetFps > 0 && decodedFps !== null &&
      decodedFps < targetFps * 0.85;
    var stressed = flowDeficit && transportEvidence;
    var healthy = !stressed && lossPercent < 1 && jitterMs < 25 && dropped < 1 && (
      targetFps <= 0 || decodedFps === null || decodedFps >= targetFps * 0.9
    );
    if (stressed) {
      state.stressCount = Math.min(RECEIVER_BUFFER_STRESS_SAMPLES, (state.stressCount || 0) + 1);
      state.healthyCount = 0;
      state.flowDeficit = true;
      if (state.stressCount >= RECEIVER_BUFFER_STRESS_SAMPLES) {
        state.targetMs = nextReceiverBufferTarget(state.targetMs);
      }
      state.reason = state.targetMs >= RECEIVER_BUFFER_MAX_MS ? 'buffer-max' : 'network-buffering';
    } else if (healthy) {
      state.stressCount = 0;
      state.healthyCount = Math.min(RECEIVER_BUFFER_RECOVERY_SAMPLES, (state.healthyCount || 0) + 1);
      state.flowDeficit = false;
      if (state.healthyCount >= RECEIVER_BUFFER_RECOVERY_SAMPLES && state.targetMs > RECEIVER_BUFFER_MIN_MS) {
        state.targetMs = previousReceiverBufferTarget(state.targetMs);
        state.healthyCount = 0;
        state.reason = 'buffer-recovered';
      } else if (state.targetMs > RECEIVER_BUFFER_MIN_MS) {
        state.reason = 'buffer-stabilizing';
      } else {
        state.reason = 'baseline';
      }
    } else {
      state.stressCount = Math.max(0, (state.stressCount || 0) - 1);
      state.healthyCount = 0;
      state.flowDeficit = false;
      state.reason = state.targetMs > RECEIVER_BUFFER_MIN_MS ? 'buffer-holding' : 'baseline';
    }
    var actualMs = finiteNumber(sample.actualMs);
    if (actualMs !== null && actualMs >= 0) {
      state.actualMs = Math.max(0, Math.min(60000, actualMs));
      state.actualMeasured = true;
    } else if (state.actualMeasured !== true) {
      state.actualMs = 0;
    }
    return {
      state: state,
      targetMs: state.targetMs,
      actualMs: state.actualMs,
      actualMeasured: state.actualMeasured === true,
      flowDeficit: state.flowDeficit,
      shouldDowngrade: state.flowDeficit && state.targetMs >= RECEIVER_BUFFER_MAX_MS,
      reason: state.reason
    };
  }

  function receiverBufferBlocksDownshift(sample) {
    var targetMs = finiteNumber(sample && sample.receiverBufferTargetMs);
    return sample && sample.receiverBound === true &&
      sample.receiverFlowDeficit === true && targetMs !== null &&
      targetMs < RECEIVER_BUFFER_MAX_MS;
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
    var expectedSenderFps = Math.min(
      sourceFps,
      Math.max(RECEIVER_ACTIVE_FPS, finiteNumber(sample.profileMaxFramerate) || sourceFps)
    );
    var senderDeliveryCollapsed = sourceActive &&
      senderFps < Math.max(RECEIVER_ACTIVE_FPS, expectedSenderFps * RECEIVER_DECODE_RATIO);
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

  function currentDegradeFloorKbps(sample) {
    var nominalKbps = Number(sample.nominalKbps) || Number(sample.profileKbps) || 0;
    return Number(sample.degradeFloorKbps) || nominalKbps * DEGRADE_FLOOR_RATIO;
  }

  function nextDegradeFloorKbps(sample) {
    var floor = Number(sample.nextDegradeFloorKbps) || 0;
    if (floor > 0) return floor;
    var nextNominalKbps = Number(sample.nextNominalKbps) || Number(sample.nextProfileKbps) || 0;
    return nextNominalKbps > 0 ? nextNominalKbps * DEGRADE_FLOOR_RATIO : 0;
  }

  function assessContentStress(sample, receiver) {
    var sourceActive = receiver.sourceFps >= RECEIVER_ACTIVE_FPS;
    if (!sourceActive) {
      return {
        active: false,
        demanding: false,
        driving: false,
        queueing: false,
        retransmitting: false,
        bandwidthLimited: false,
        state: 'idle'
      };
    }
    var nominalKbps = Number(sample.nominalKbps) || Number(sample.profileKbps) || 0;
    var profileFps = Math.max(
      RECEIVER_ACTIVE_FPS,
      Number(sample.profileMaxFramerate) || receiver.sourceFps || RECEIVER_ACTIVE_FPS
    );
    var frameBudgetKbits = nominalKbps > 0
      ? nominalKbps / Math.max(RECEIVER_ACTIVE_FPS, Math.min(receiver.sourceFps, profileFps))
      : 0;
    var frameKbits = Number(sample.senderKbitsPerFrame) || 0;
    if (!(frameKbits > 0) && receiver.senderFps > 0) {
      frameKbits = Math.max(0, Number(sample.outboundKbps) || 0) / receiver.senderFps;
    }
    var queueing = Number(sample.senderPacketSendDelayMs) >= PACKET_SEND_DELAY_CONGESTED_MS;
    var retransmitting = Number(sample.senderRetransmitPercent) >= RETRANSMIT_CONGESTED_PERCENT;
    var bandwidthLimited = Number(sample.bandwidthLimitedMsDelta) >= BANDWIDTH_LIMITED_MIN_MS;
    var receiverDrops = Number(sample.receiverFramesDroppedDelta) >= 2;
    var highFrameDemand = frameKbits >= Math.max(
      CONTENT_FRAME_MIN_KBITS,
      frameBudgetKbits * CONTENT_FRAME_RATIO
    );
    var availableKbps = Math.max(0, Number(sample.availableOutgoingBitrate) || 0) / 1000;
    var outboundKbps = Math.max(0, Number(sample.outboundKbps) || 0);
    var floorKbps = currentDegradeFloorKbps(sample);
    var driving = outboundKbps > 0 && (
      availableKbps > 0
        ? outboundKbps >= availableKbps * 0.4
        : outboundKbps >= floorKbps * 0.6
    );
    var demanding = highFrameDemand || receiver.senderCollapsed || queueing ||
      retransmitting || receiverDrops || (bandwidthLimited && driving);
    return {
      active: true,
      demanding: demanding,
      driving: driving,
      queueing: queueing,
      retransmitting: retransmitting,
      bandwidthLimited: bandwidthLimited,
      state: demanding ? 'demanding' : 'steady'
    };
  }

  function hasExplicitCpuPressure(sample) {
    return String(sample.qualityLimitationReason || '').toLowerCase() === 'cpu';
  }

  function hasSenderCpuPressure(sample, receiver) {
    var targetFps = Math.min(
      receiver.sourceFps,
      Math.max(RECEIVER_ACTIVE_FPS, Number(sample.profileMaxFramerate) || receiver.sourceFps)
    );
    var senderUnderTarget = targetFps > 0 &&
      receiver.senderFps < Math.max(RECEIVER_ACTIVE_FPS, targetFps * 0.55);
    return receiver.sourceFps >= RECEIVER_ACTIVE_FPS && (
      receiver.senderCollapsed || hasExplicitCpuPressure(sample) &&
      (!receiver.bound || senderUnderTarget)
    );
  }

  function assessCapacityPressure(sample, receiver, content) {
    if (!sample.valid || !content.active || !content.demanding) return false;
    var floorKbps = currentDegradeFloorKbps(sample);
    if (!(floorKbps > 0)) return false;
    var availableKbps = Math.max(0, Number(sample.availableOutgoingBitrate) || 0) / 1000;
    var incomingKbps = receiver.bound ? Math.max(0, receiver.incomingKbps) : 0;
    var lowAvailable = availableKbps > 0 && availableKbps < floorKbps * CAPACITY_PRESSURE_RATIO;
    var lowDelivery = incomingKbps > 0 && incomingKbps < floorKbps * CAPACITY_PRESSURE_RATIO;
    var targetFps = Math.min(
      receiver.sourceFps,
      Math.max(RECEIVER_ACTIVE_FPS, Number(sample.profileMaxFramerate) || receiver.sourceFps)
    );
    var senderFpsHealthy = targetFps <= 0 ||
      receiver.senderFps >= Math.max(RECEIVER_ACTIVE_FPS, targetFps * 0.55);
    var playbackHealthy = receiver.bound && receiver.healthy &&
      receiver.receiverFollowsSender && senderFpsHealthy;
    if (playbackHealthy) return false;
    var transportSignal = content.queueing || content.retransmitting ||
      content.bandwidthLimited || receiver.impaired ||
      String(sample.qualityLimitationReason || '').toLowerCase() === 'bandwidth';
    return (lowAvailable && (content.driving || lowDelivery || transportSignal)) ||
      (lowDelivery && transportSignal);
  }

  function classifyPressure(sample, receiver, content) {
    if (!sample.valid) return 'unavailable';
    content = content || assessContentStress(sample, receiver);
    if (receiver.impaired || sample.receiverImpaired === true) return 'network';
    if (assessCapacityPressure(sample, receiver, content)) return 'network';
    if (hasSenderCpuPressure(sample, receiver) && (!receiver.bound || receiver.receiverFollowsSender)) {
      return 'cpu';
    }
    if (!receiver.bound) {
      return hasSenderCpuPressure(sample, receiver) ? 'cpu' : 'unavailable';
    }
    if (!receiver.healthy) return 'unavailable';
    if (receiver.receiverFollowsSender && hasSenderCpuPressure(sample, receiver)) return 'cpu';
    return 'healthy';
  }

  function isBadSample(sample) {
    if (!sample.valid) return false;
    var receiver = assessReceiver(sample);
    var content = assessContentStress(sample, receiver);
    var pressure = classifyPressure(sample, receiver, content);
    if (receiver.bound || sample.receiverBound === false || sample.receiverBound === true) {
      return pressure === 'cpu' || pressure === 'network';
    }
    var degradeFloorKbps = currentDegradeFloorKbps(sample);
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

  function capacityForDecisionKbps(sample) {
    var safeCapacityKbps = Number(sample.safeCapacityKbps) || 0;
    if (safeCapacityKbps > 0) return safeCapacityKbps;
    var availableKbps = Math.max(0, Number(sample.availableOutgoingBitrate) || 0) / 1000;
    if (availableKbps > 0) return availableKbps;
    var receiver = assessReceiver(sample);
    if (receiver.bound && receiver.incomingKbps > 0) return receiver.incomingKbps * 1.2;
    return Math.max(0, Number(sample.outboundKbps) || 0);
  }

  function recoveryCapacityForDecisionKbps(sample) {
    var recoveryCapacityKbps = Number(sample.recoveryCapacityKbps) || 0;
    if (recoveryCapacityKbps > 0) return recoveryCapacityKbps;
    return capacityForDecisionKbps(sample);
  }

  function isStableSample(sample) {
    var receiver = assessReceiver(sample);
    var content = assessContentStress(sample, receiver);
    if (!content.demanding) return false;
    var pressure = classifyPressure(sample, receiver, content);
    var senderRecovery = sample.recoveryReceiverOptional === true && !receiver.bound &&
      pressure === 'unavailable' &&
      Number(sample.senderSentFps || sample.senderEncodedFps || 0) >=
        Math.max(RECEIVER_ACTIVE_FPS, Number(sample.profileMaxFramerate || 0) * 0.75) &&
      String(sample.qualityLimitationReason || '').toLowerCase() !== 'bandwidth';
    if (!senderRecovery && receiver.bound && pressure !== 'healthy') return false;
    if (!senderRecovery && sample.receiverBound === false) return false;
    var nominalKbps = Number(sample.nominalKbps) || Number(sample.profileKbps) || 0;
    var targetFloorKbps = nextDegradeFloorKbps(sample);
    var capacityKbps = recoveryCapacityForDecisionKbps(sample);
    return (
      sample.valid &&
      transportRecoveryReady(sample) &&
      Number(sample.lossPercent) < 2 &&
      Number(sample.jitterMs) < (receiver.bound ? 120 : 60) &&
      sample.receiverImpaired !== true &&
      nominalKbps > 0 &&
      targetFloorKbps > 0 &&
      capacityKbps >= targetFloorKbps * RECOVERY_HEADROOM
    );
  }

  function transportRecoveryReady(sample) {
    var retransmit = finiteNumber(sample.senderRetransmitPercent);
    var queueDelay = finiteNumber(sample.senderPacketSendDelayMs);
    return (retransmit === null || retransmit < 3) &&
      (queueDelay === null || queueDelay < 20);
  }

  function receiverRecoveryUsable(sample, receiver, receiverFresh) {
    if (!receiver.bound) return false;
    if (receiverFresh) return true;
    return receiver.healthy && Number(sample.receiverReportAgeMs) >= 0 &&
      Number(sample.receiverReportAgeMs) < 3000;
  }

  function hasIndependentImpairment(sample) {
    var receiver = assessReceiver(sample);
    var content = assessContentStress(sample, receiver);
    return classifyPressure(sample, receiver, content) === 'network' ||
      Number(sample.lossPercent) > 5 || Number(sample.jitterMs) > 100;
  }

  function sampleCapacityBitrate(sample) {
    var capacityKbps = capacityForDecisionKbps(sample);
    return capacityKbps > 0 ? capacityKbps * 1000 : 0;
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
    state.probeTargetFloorKbps = 0;
    state.probePressure = '';
  }

  function observedCapacityKbps(sample, receiver, content) {
    if (!content.demanding) return 0;
    var advertised = Math.max(0, Number(sample.availableOutgoingBitrate) || 0) / 1000;
    var delivered = receiver.bound ? Math.max(0, receiver.incomingKbps) * 1.2 : 0;
    var outboundKbps = Math.max(0, Number(sample.outboundKbps) || 0);
    var floorKbps = currentDegradeFloorKbps(sample);
    var currentProfileIsSustained = outboundKbps >= floorKbps * CAPACITY_PRESSURE_RATIO;
    if (advertised > 0 && (currentProfileIsSustained || !(delivered > 0))) return advertised;
    if (advertised > 0 && delivered > 0) return Math.min(advertised, delivered);
    return delivered || outboundKbps;
  }

  function updateSafeCapacity(state, sample, receiver, content, pressure) {
    if (!sample.valid || !content.demanding) return state.safeCapacityKbps || 0;
    var observed = observedCapacityKbps(sample, receiver, content);
    if (!(observed > 0)) return state.safeCapacityKbps || 0;
    var current = Math.max(0, Number(state.safeCapacityKbps) || 0);
    if (!(current > 0) || pressure === 'network' || observed < current) {
      state.safeCapacityKbps = observed;
    } else {
      state.safeCapacityKbps = Math.min(observed, current * CAPACITY_RISE_RATIO);
    }
    state.lastDemandAt = sample.now;
    return state.safeCapacityKbps;
  }

  function observedRecoveryCapacityKbps(sample, receiver) {
    var advertised = Math.max(0, Number(sample.availableOutgoingBitrate) || 0) / 1000;
    var delivered = receiver.bound ? Math.max(0, receiver.incomingKbps) * 1.2 : 0;
    var outboundKbps = Math.max(0, Number(sample.outboundKbps) || 0);
    return Math.max(advertised, delivered, outboundKbps);
  }

  function updateRecoveryCapacity(state, sample, receiver, pressure, receiverFresh) {
    state.currentDegradeFloorKbps = currentDegradeFloorKbps(sample);
    state.recoveryTargetFloorKbps = nextDegradeFloorKbps(sample);
    if (!sample.valid) {
      state.recoveryGateReason = 'sample-unavailable';
      return state.recoveryCapacityKbps || 0;
    }
    var senderRecovery = sample.recoveryReceiverOptional === true && !receiver.bound &&
      pressure === 'unavailable';
    if (!receiver.bound && !senderRecovery) {
      state.recoveryGateReason = 'receiver-unavailable';
      return state.recoveryCapacityKbps || 0;
    }
    if (!senderRecovery && !receiverRecoveryUsable(sample, receiver, receiverFresh)) {
      state.recoveryGateReason = 'receiver-repeat';
      return state.recoveryCapacityKbps || 0;
    }
    if (pressure !== 'healthy' && !senderRecovery) {
      state.recoveryHealthySamples = 0;
      if (pressure === 'network') state.recoveryCapacityKbps = 0;
      state.recoveryGateReason = pressure + '-pressure';
      return state.recoveryCapacityKbps || 0;
    }
    if (!transportRecoveryReady(sample)) {
      state.recoveryHealthySamples = 0;
      state.recoveryGateReason = 'transport-not-ready';
      return state.recoveryCapacityKbps || 0;
    }
    var observed = observedRecoveryCapacityKbps(sample, receiver);
    if (!(observed > 0)) {
      state.recoveryHealthySamples = 0;
      state.recoveryGateReason = 'capacity-unavailable';
      return state.recoveryCapacityKbps || 0;
    }
    var current = Math.max(0, Number(state.recoveryCapacityKbps) || 0);
    state.recoveryCapacityKbps = current > 0
      ? (observed < current ? current * 0.5 + observed * 0.5 : current * 0.6 + observed * 0.4)
      : observed;
    state.recoveryHealthySamples = Math.min(1000, (state.recoveryHealthySamples || 0) + 1);
    state.recoveryCapacityAt = sample.now;
    var target = state.recoveryTargetFloorKbps * RECOVERY_HEADROOM;
    if (state.recoveryHealthySamples < 2) state.recoveryGateReason = 'warming';
    else if (target > 0 && state.recoveryCapacityKbps < target) state.recoveryGateReason = 'capacity-below-target';
    else state.recoveryGateReason = 'ready';
    return state.recoveryCapacityKbps;
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
    state.recoveryHealthySamples = 0;
    state.recoveryGateReason = 'probe-rollback';
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
      safeCapacityKbps: 0,
      recoveryCapacityKbps: 0,
      recoveryHealthySamples: 0,
      recoveryCapacityAt: 0,
      recoveryGateReason: 'not-started',
      currentDegradeFloorKbps: 0,
      recoveryTargetFloorKbps: 0,
      lastDemandAt: 0,
      contentState: 'idle',
      receiverJitterEwma: 0,
      receiverStatus: 'unavailable',
      receiverReason: 'receiver-unavailable',
      receiverBufferTargetMs: RECEIVER_BUFFER_MIN_MS,
      receiverBufferActualMs: RECEIVER_BUFFER_MIN_MS,
      receiverBufferReason: 'baseline',
      receiverFlowDeficit: false,
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
      probeTargetFloorKbps: 0,
      probePressure: '',
      probeFailureCount: 0,
      windowPressure: ''
      ,softProtectionKbps: 0
      ,softProtectionUntil: 0
      ,softBadCount: 0
      ,softHealthyCount: 0
      ,softProtectionExpiredAt: 0
      ,lastTransitionAt: 0
      ,transitionReason: ''
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
    var content = assessContentStress(sample, receiver);
    var pressure = classifyPressure(sample, receiver, content);
    if (sample.valid) {
      sample.safeCapacityKbps = updateSafeCapacity(state, sample, receiver, content, pressure);
      state.contentState = content.state;
    } else {
      sample.safeCapacityKbps = state.safeCapacityKbps || 0;
    }
    var receiverReportId = receiver.bound
      ? String(sample.receiverReportId || sample.receiverReportedAtUnix || '')
      : '';
    var receiverFresh = receiver.bound && !!receiverReportId &&
      receiverReportId !== state.lastReceiverReportId;
    if (receiverReportId && receiverFresh) state.lastReceiverReportId = receiverReportId;
    sample.recoveryCapacityKbps = updateRecoveryCapacity(
      state, sample, receiver, pressure, receiverFresh
    );
    sample.recoveryHealthySamples = state.recoveryHealthySamples || 0;
    state.receiverStatus = !receiver.bound
      ? 'unavailable'
      : (pressure === 'network'
        ? 'impaired'
        : (pressure === 'cpu' || pressure === 'healthy' ? 'healthy' : 'unavailable'));
    state.receiverReason = pressure === 'network' && receiver.reason === 'receiver-healthy'
      ? 'capacity-underflow'
      : receiver.reason;
    state.receiverBufferTargetMs = normalizeReceiverBufferTarget(sample.receiverBufferTargetMs);
    state.receiverBufferActualMs = Math.max(
      0,
      Math.min(60000, finiteNumber(sample.receiverBufferActualMs) || state.receiverBufferTargetMs)
    );
    state.receiverBufferReason = String(sample.receiverBufferReason || 'baseline');
    state.receiverFlowDeficit = sample.receiverFlowDeficit === true;
    state.pressure = pressure;
    if (!sample.valid) {
      return {state: pauseAdaptiveState(state), decision: null};
    }

    var softPressure = receiver.bound && content.active && pressure === 'network' &&
      sample.receiverFlowDeficit === true &&
      (content.queueing || content.retransmitting);
    if (softPressure && state.softProtectionExpiredAt > 0 &&
        sample.now >= state.softProtectionExpiredAt &&
        sample.now - state.softProtectionExpiredAt <= TIER_TRANSITION_DWELL_MS) {
      softPressure = false;
    }
    var softExpired = false;
    if (softPressure && state.probeFromLevel === null) {
      if (state.softProtectionKbps > 0 && state.softProtectionUntil > 0 &&
          sample.now >= state.softProtectionUntil) {
        state.softProtectionKbps = 0;
        state.softProtectionUntil = 0;
        state.softBadCount = 0;
        state.softHealthyCount = 0;
        state.softProtectionExpiredAt = sample.now;
        state.transitionReason = 'soft-expired';
        softExpired = true;
      }
      if (softExpired) {
        // Let the normal sustained-pressure window evaluate this sample.
      } else {
      state.softHealthyCount = 0;
      state.softBadCount = Math.min(
        SOFT_PROTECTION_REQUIRED_SAMPLES,
        (state.softBadCount || 0) + 1
      );
      var softTargetKbps = Math.max(
        100,
        Number(sample.profileKbps) || Number(sample.nominalKbps) || 0
      );
      var softFloorKbps = Math.max(100, currentDegradeFloorKbps(sample));
      var currentSoftKbps = Number(state.softProtectionKbps) > 0
        ? Number(state.softProtectionKbps)
        : softTargetKbps;
      state.softProtectionKbps = Math.max(
        softFloorKbps,
        Math.min(softTargetKbps, Math.round(currentSoftKbps * SOFT_PROTECTION_RATIO))
      );
      if (!(state.softProtectionUntil > sample.now)) {
        state.softProtectionUntil = sample.now + SOFT_PROTECTION_MAX_MS;
      }
      state.transitionReason = 'soft-protect';
      if (state.softBadCount < SOFT_PROTECTION_REQUIRED_SAMPLES || sample.now < state.softProtectionUntil) {
        state.status = 'soft-protected';
        state.window = [];
        state.windowPressure = '';
        state.badCount = 0;
        return {state: state, decision: null};
      }
      state.softProtectionKbps = 0;
      state.softProtectionUntil = 0;
      state.softBadCount = 0;
      state.softProtectionExpiredAt = sample.now;
      state.transitionReason = 'soft-expired';
      }
    } else if (!softPressure && state.softProtectionKbps > 0 && receiver.bound && pressure === 'healthy') {
      state.softBadCount = 0;
      state.softHealthyCount = Math.min(
        SOFT_PROTECTION_REQUIRED_SAMPLES,
        (state.softHealthyCount || 0) + 1
      );
      var recoveryTargetKbps = Math.max(
        100,
        Number(sample.profileKbps) || Number(sample.nominalKbps) || state.softProtectionKbps
      );
      if (state.softHealthyCount >= SOFT_PROTECTION_REQUIRED_SAMPLES) {
        var recoveredSoftKbps = Math.min(
          recoveryTargetKbps,
          Math.max(state.softProtectionKbps, Math.round(state.softProtectionKbps * SOFT_PROTECTION_RECOVERY_RATIO))
        );
        state.softProtectionKbps = recoveredSoftKbps >= recoveryTargetKbps * 0.98
          ? 0
          : recoveredSoftKbps;
        state.softHealthyCount = 0;
        state.transitionReason = state.softProtectionKbps > 0 ? 'soft-recover' : 'soft-recovered';
      }
      if (state.softProtectionKbps > 0) {
        state.status = 'soft-recovering';
        return {state: state, decision: null};
      }
    } else if (state.softProtectionUntil && sample.now >= state.softProtectionUntil) {
      state.softProtectionKbps = 0;
      state.softBadCount = 0;
      state.softHealthyCount = 0;
      state.transitionReason = 'soft-expired';
    }

    if (state.probeFromLevel !== null) {
      if (state.probeReceiverRequired &&
          (!receiverRecoveryUsable(sample, receiver, receiverFresh) || !receiverReportId)) {
        state.status = 'probing';
        return {state: state, decision: null};
      }
      state.probeValidCount += 1;
      var probePressure = state.probePressure || 'network';
      var probeTargetFloorKbps = Number(state.probeTargetFloorKbps) ||
        currentDegradeFloorKbps(sample);
      var currentFloorKbps = currentDegradeFloorKbps(sample);
      var currentFloorSustained = state.level >= state.maxLevel || currentFloorKbps <= 0 ||
        Number(sample.safeCapacityKbps) >= currentFloorKbps * CAPACITY_PRESSURE_RATIO;
      var senderProbeRecovery = state.probeReceiverRequired === false &&
        sample.recoveryReceiverOptional === true && !receiver.bound &&
        pressure === 'unavailable' &&
        Number(sample.senderSentFps || sample.senderEncodedFps || 0) >=
          Math.max(RECEIVER_ACTIVE_FPS, Number(sample.profileMaxFramerate || 0) * 0.75);
      var probeCapacitySufficient = content.demanding && (pressure === 'healthy' || senderProbeRecovery) &&
        transportRecoveryReady(sample) &&
        currentFloorSustained &&
        probeTargetFloorKbps > 0 &&
        recoveryCapacityForDecisionKbps(sample) >= probeTargetFloorKbps * RECOVERY_HEADROOM;
      if (!probeCapacitySufficient) state.probeBadCount += 1;
      else state.probeImprovedCount += 1;
      if (state.probeBadCount >= 2) {
        return rollbackProbe(state, sample);
      }
      if (sample.now >= state.probeUntil) {
        if (state.probeValidCount < 2 || state.probeImprovedCount < 2) {
          return rollbackProbe(state, sample);
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
        state.cooldownUntil = sample.now + (
          sample.stabilityLab === true ? STABILITY_LAB_RECOVERY_STABLE_MS : RECOVERY_STABLE_MS
        );
        state.lastDownshiftAvailableBitrate = 0;
        state.lastDownshiftReceiverDriven = false;
        state.fastDownshiftReadyAt = 0;
        state.lastDownshiftPressure = state.level === 0 ? '' : committedPressure;
        state.probeFailureCount = 0;
        state.recoveryHealthySamples = 0;
        state.recoveryGateReason = 'probe-committed';
        state.lastDecision = commitDecision;
        clearProbe(state);
        return {state: state, decision: commitDecision};
      }
      state.status = 'probing';
      return {state: state, decision: null};
    }

    var receiverExplicitlyUnavailable = sample.receiverBound === false ||
      (sample.receiverBound === true && !receiver.bound);
    if (receiverExplicitlyUnavailable && !sample.recoveryReceiverOptional &&
        pressure !== 'cpu' && pressure !== 'network') {
      state.status = 'sender-observing';
      state.window = [];
      state.windowPressure = '';
      state.badCount = 0;
      state.stableSince = null;
      return {state: state, decision: null};
    }

    var bad = isBadSample(sample);
    if (
      pressure === 'network' && !receiverBufferBlocksDownshift(sample) && receiverFresh && state.lastDownshiftReceiverDriven &&
      (!state.lastTransitionAt || sample.now - state.lastTransitionAt >= TIER_TRANSITION_DWELL_MS) &&
      sample.now >= state.fastDownshiftReadyAt && state.level < state.maxLevel
    ) {
      var fastLevel = Math.min(state.maxLevel, state.level + 1);
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
        state.lastDownshiftReceiverDriven = fastLevel < state.maxLevel;
        state.lastDownshiftPressure = 'network';
        state.lastTransitionAt = sample.now;
        state.softProtectionKbps = 0;
        state.softProtectionUntil = 0;
        state.softBadCount = 0;
        state.softHealthyCount = 0;
        state.softProtectionExpiredAt = 0;
        state.transitionReason = 'downshift-fast';
        state.recoveryCapacityKbps = 0;
        state.recoveryHealthySamples = 0;
        state.recoveryGateReason = 'downshift';
        state.fastDownshiftReadyAt = fastLevel < state.maxLevel
          ? sample.now + FAST_DOWNSHIFT_SETTLE_MS
          : 0;
        state.lastDecision = fastDecision;
        return {state: state, decision: fastDecision};
      }
    }
    if (receiver.bound && !receiverRecoveryUsable(sample, receiver, receiverFresh)) {
      state.status = receiver.impaired ? 'receiver-waiting' : state.status;
      return {state: state, decision: null};
    }
    if (pressure === 'network' && receiverBufferBlocksDownshift(sample)) {
      state.status = 'receiver-buffering';
      state.recoveryGateReason = 'receiver-buffering';
      state.window = [];
      state.windowPressure = '';
      state.badCount = 0;
      state.stableSince = null;
      return {state: state, decision: null};
    }
    if (bad) {
      state.stableSince = null;
    } else if (isStableSample(sample) && state.level > 0) {
      if (state.stableSince === null) state.stableSince = sample.now;
      if (
        state.recoveryHealthySamples >= 2 &&
        sample.now >= state.cooldownUntil &&
        sample.now - state.stableSince >= (
          sample.stabilityLab === true ? STABILITY_LAB_RECOVERY_STABLE_MS : RECOVERY_STABLE_MS
        )
      ) {
        var probedLevel = Math.max(0, state.level - 1);
        var recoveryDecision = {
          type: 'probe',
          fromLevel: state.level,
          toLevel: probedLevel
        };
        state.probeFromLevel = state.level;
        state.probeStartedAt = sample.now;
        state.probeUntil = sample.now + (
          sample.stabilityLab === true ? STABILITY_LAB_RECOVERY_PROBE_MS : RECOVERY_PROBE_MS
        );
        state.probeBadCount = 0;
        state.probeValidCount = 0;
        state.probeImprovedCount = 0;
        state.probeBaselineDecodedFps = receiver.bound ? receiver.decodedFps : 0;
        state.probeBaselineIncomingKbps = receiver.bound ? receiver.incomingKbps : 0;
        state.probeReceiverRequired = receiver.bound;
        state.probeTargetFloorKbps = nextDegradeFloorKbps(sample);
        state.probePressure = state.lastDownshiftPressure || 'network';
        state.level = probedLevel;
        state.status = 'probing';
        state.window = [];
        state.windowPressure = '';
        state.badCount = 0;
        state.stableSince = null;
        state.recoveryGateReason = 'probing';
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
      (pressure === 'cpu' || pressure === 'network' || state.window.length === WINDOW_SIZE) &&
      (!state.lastTransitionAt || sample.now - state.lastTransitionAt >= TIER_TRANSITION_DWELL_MS);

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
      state.lastTransitionAt = sample.now;
      state.softProtectionKbps = 0;
      state.softProtectionUntil = 0;
      state.softBadCount = 0;
      state.softHealthyCount = 0;
      state.softProtectionExpiredAt = 0;
      state.transitionReason = pressure === 'cpu' ? 'downshift-cpu' : 'downshift';
      state.recoveryCapacityKbps = 0;
      state.recoveryHealthySamples = 0;
      state.recoveryGateReason = 'downshift';
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
    var requestedFps = Math.min(60, Math.max(30, configuredFps));
    var fpsTiers = [60, 45, 30].filter(function (fps) { return fps <= requestedFps; });
    function addResolutionTier(maxWidth, maxHeight, bitrates) {
      fpsTiers.forEach(function (fps) {
        addEncodingBand(bands, sourceWidth, sourceHeight, configuredFps, {
          maxWidth: maxWidth,
          maxHeight: maxHeight,
          maxFramerate: fps,
          maxBitrate: bitrates[fps]
        });
      });
    }
    addResolutionTier(sourceWidth, sourceHeight, {
      60: sourceBitrateForFPS(sourceBitrate, 60),
      45: sourceBitrateForFPS(sourceBitrate, 45),
      30: sourceBitrateForFPS(sourceBitrate, 30)
    });
    if (highResolutionSource) addResolutionTier(1280, 720, {
      60: 8000000,
      45: 7000000,
      30: 6000000
    });
    if (sourceWidth > 960 || sourceHeight > 540) {
      addResolutionTier(960, 540, {
        60: 5000000,
        45: 4500000,
        30: 4000000
      });
    }
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
    var configuredFps = Math.min(60, Math.max(30, Number(options.configuredFps) || 30));
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

  // Split one congestion-controlled SFU uplink budget across the active
  // simulcast encoders. Keeping per-layer maxima independent can make a
  // 16 Mbps full layer plus an 8 Mbps protection layer compete on a link that
  // only has 5-10 Mbps available, which collapses the encoder frame rate.
  function computeSfuHostUplinkProfile(options) {
    options = options || {};
    var availableKbps = Math.max(0, Math.round(Number(options.availableKbps) || 0));
    var requestedFps = Math.max(24, Math.min(60, Math.round(Number(options.requestedFps) || 30)));
    var profileMaxKbps = Math.max(800, Math.round((Number(options.profileMaxBitrate) || 16000000) / 1000));
    var activeLayerCount = Math.max(1, Math.min(2, Math.round(Number(options.activeLayerCount) || 1)));
    var budgetKbps = availableKbps > 0
      ? Math.max(800, Math.min(profileMaxKbps, Math.round(availableKbps * 0.78)))
      : Math.min(profileMaxKbps, 8000);
    var band = 'healthy';
    var scaleResolutionDownBy = 1;
    var maxFramerate = requestedFps;
    if (budgetKbps < 2800) {
      band = 'critical';
      scaleResolutionDownBy = 1.5;
      maxFramerate = Math.min(30, requestedFps);
    } else if (budgetKbps < 5000) {
      band = 'constrained';
      scaleResolutionDownBy = 1.5;
    } else if (budgetKbps < 9000) {
      band = 'moderate';
      scaleResolutionDownBy = 1.25;
    }
    var fullShare = activeLayerCount > 1 ? 0.65 : 1;
    var fullMaxBitrate = Math.max(500000, Math.round(budgetKbps * fullShare) * 1000);
    var halfMaxBitrate = activeLayerCount > 1
      ? Math.max(300000, Math.round(budgetKbps * (1 - fullShare)) * 1000)
      : 0;
    return {
      band: band,
      availableKbps: availableKbps,
      budgetKbps: budgetKbps,
      fullMaxBitrate: fullMaxBitrate,
      halfMaxBitrate: halfMaxBitrate,
      maxFramerate: maxFramerate,
      halfMaxFramerate: Math.min(30, requestedFps),
      scaleResolutionDownBy: scaleResolutionDownBy,
      halfScaleResolutionDownBy: Math.max(2.25, roundScale(scaleResolutionDownBy * 1.5))
    };
  }

  return {
    POLICY_VERSION: POLICY_VERSION,
    BASELINE_POLICY_VERSION: BASELINE_POLICY_VERSION,
    resolveAdaptiveMode: resolveAdaptiveMode,
    createAdaptiveState: createAdaptiveState,
    createReceiverBufferState: createReceiverBufferState,
    evaluateReceiverBufferSample: evaluateReceiverBufferSample,
    pauseAdaptiveState: pauseAdaptiveState,
    evaluateAdaptiveSample: evaluateAdaptiveSample,
    computeEncodingProfile: computeEncodingProfile,
    computeSfuHostUplinkProfile: computeSfuHostUplinkProfile,
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
      receiverJitterLimitMs: RECEIVER_JITTER_LIMIT_MS,
      receiverBufferMinMs: RECEIVER_BUFFER_MIN_MS,
      receiverBufferMaxMs: RECEIVER_BUFFER_MAX_MS,
      receiverBufferStepsMs: RECEIVER_BUFFER_STEPS_MS.slice()
    }
  };
});
