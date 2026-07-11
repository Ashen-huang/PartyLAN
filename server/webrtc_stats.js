(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.GameCastWebRTCStats = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  function number(value) {
    return typeof value === 'number' && Number.isFinite(value) ? value : 0;
  }

  function addressFamily(candidate) {
    var address = String(candidate && (candidate.address || candidate.ip) || '');
    if (!address) return 'unknown';
    return address.indexOf(':') >= 0 ? 'ipv6' : 'ipv4';
  }

  function selectedPair(stats) {
    var selected = null;
    stats.forEach(function (item) {
      if (item.type === 'transport' && item.selectedCandidatePairId) {
        selected = stats.get(item.selectedCandidatePairId) || selected;
      }
    });
    if (selected) return selected;
    stats.forEach(function (item) {
      if (!selected && item.type === 'candidate-pair' && item.state === 'succeeded' && item.selected) {
        selected = item;
      }
    });
    if (selected) return selected;
    stats.forEach(function (item) {
      if (
        !selected &&
        item.type === 'candidate-pair' &&
        item.state === 'succeeded' &&
        item.nominated
      ) {
        selected = item;
      }
    });
    return selected;
  }

  function describeSelectedPair(stats) {
    var pair = selectedPair(stats);
    if (!pair) {
      return {
        pairId: '',
        path: 'unknown',
        transport: 'unknown',
        localCandidateType: 'unknown',
        remoteCandidateType: 'unknown',
        localAddressFamily: 'unknown',
        remoteAddressFamily: 'unknown'
      };
    }
    var local = stats.get(pair.localCandidateId) || {};
    var remote = stats.get(pair.remoteCandidateId) || {};
    var relay = local.candidateType === 'relay' || remote.candidateType === 'relay';
    var relayCandidate = local.candidateType === 'relay' ? local : remote;
    var transport = relay
      ? relayCandidate.relayProtocol || relayCandidate.protocol || 'unknown'
      : local.protocol || remote.protocol || 'unknown';
    return {
      pairId: pair.id || '',
      path: relay ? 'turn' : 'p2p',
      transport: String(transport).toLowerCase(),
      localCandidateType: local.candidateType || 'unknown',
      remoteCandidateType: remote.candidateType || 'unknown',
      localAddressFamily: addressFamily(local),
      remoteAddressFamily: addressFamily(remote)
    };
  }

  function totals(stats) {
    var result = {
      packetsReceived: 0,
      packetsLost: 0,
      packetsSent: 0,
      bytesReceived: 0,
      bytesSent: 0,
      framesDecoded: 0,
      framesDropped: 0,
      framesEncoded: 0,
      jitterMs: 0,
      qualityLimitationReason: 'none'
    };
    stats.forEach(function (item) {
      if (item.type === 'inbound-rtp' && (item.kind === 'video' || item.mediaType === 'video')) {
        result.packetsReceived += number(item.packetsReceived);
        result.packetsLost += number(item.packetsLost);
        result.bytesReceived += number(item.bytesReceived);
        result.framesDecoded += number(item.framesDecoded);
        result.framesDropped += number(item.framesDropped);
        result.jitterMs = Math.max(result.jitterMs, number(item.jitter) * 1000);
      }
      if (item.type === 'outbound-rtp' && (item.kind === 'video' || item.mediaType === 'video')) {
        result.packetsSent += number(item.packetsSent);
        result.bytesSent += number(item.bytesSent);
        result.framesEncoded += number(item.framesEncoded);
        if (item.qualityLimitationReason) {
          result.qualityLimitationReason = item.qualityLimitationReason;
        }
      }
    });
    return result;
  }

  function delta(current, previous, name) {
    var value = current[name] - previous[name];
    return value >= 0 ? value : null;
  }

  function buildWebRTCSample(stats, previous, context) {
    context = context || {};
    var now = number(context.now) || Date.now();
    var pairDescription = describeSelectedPair(stats);
    var pair = pairDescription.pairId ? stats.get(pairDescription.pairId) || {} : {};
    var currentTotals = totals(stats);
    var snapshot = Object.assign({}, currentTotals, {
      now: now,
      pairId: pairDescription.pairId,
      generation: number(context.generation)
    });
    var sample = Object.assign({}, pairDescription, {
      now: now,
      generation: number(context.generation),
      targetBitrate: number(context.targetBitrate),
      availableOutgoingBitrate: number(pair.availableOutgoingBitrate),
      rttMs: number(pair.currentRoundTripTime) * 1000,
      jitterMs: currentTotals.jitterMs,
      qualityLimitationReason: currentTotals.qualityLimitationReason,
      snapshot: snapshot,
      valid: false,
      invalidReason: 'baseline'
    });

    if (context.visible === false) sample.invalidReason = 'page-hidden';
    else if (context.sourceChanged) sample.invalidReason = 'source-changed';
    else if (context.resolutionChanged) sample.invalidReason = 'resolution-changed';
    else if (!pairDescription.pairId) sample.invalidReason = 'no-selected-pair';
    else if (!previous) sample.invalidReason = 'baseline';
    else if (previous.pairId !== pairDescription.pairId) sample.invalidReason = 'pair-changed';
    else if (number(previous.generation) !== number(context.generation)) {
      sample.invalidReason = 'generation-changed';
    } else {
      var elapsedMs = now - previous.now;
      var received = delta(currentTotals, previous, 'packetsReceived');
      var lost = delta(currentTotals, previous, 'packetsLost');
      var sent = delta(currentTotals, previous, 'packetsSent');
      var recvBytes = delta(currentTotals, previous, 'bytesReceived');
      var sentBytes = delta(currentTotals, previous, 'bytesSent');
      var decoded = delta(currentTotals, previous, 'framesDecoded');
      var dropped = delta(currentTotals, previous, 'framesDropped');
      var encoded = delta(currentTotals, previous, 'framesEncoded');
      if (
        elapsedMs <= 0 ||
        received === null ||
        lost === null ||
        sent === null ||
        recvBytes === null ||
        sentBytes === null ||
        decoded === null ||
        dropped === null ||
        encoded === null
      ) {
        sample.invalidReason = 'counter-reset';
      } else {
        sample.packetsReceivedDelta = received;
        sample.packetsLostDelta = lost;
        sample.packetsSentDelta = sent;
        sample.framesDecodedDelta = decoded;
        sample.framesDroppedDelta = dropped;
        sample.framesEncodedDelta = encoded;
        sample.lossPercent = received + lost > 0 ? lost * 100 / (received + lost) : 0;
        sample.downKbps = recvBytes * 8 / elapsedMs;
        sample.upKbps = sentBytes * 8 / elapsedMs;
        if (received + lost < 20 && sent < 20) {
          sample.invalidReason = 'insufficient-packets';
        } else {
          sample.valid = true;
          sample.invalidReason = '';
        }
      }
    }
    return sample;
  }

  return {
    describeSelectedPair: describeSelectedPair,
    buildWebRTCSample: buildWebRTCSample
  };
});
