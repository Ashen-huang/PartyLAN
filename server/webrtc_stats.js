(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.GameCastWebRTCStats = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  var TELEMETRY_RESET_REASON_ORDER = [
    'outbound-identity-changed',
    'inbound-identity-changed',
    'media-source-identity-changed',
    'optional-counter-reset'
  ];

  function number(value) {
    return typeof value === 'number' && Number.isFinite(value) ? value : 0;
  }

  function finiteCounter(value) {
    return typeof value === 'number' && Number.isFinite(value) && value >= 0
      ? value
      : undefined;
  }

  function positiveNumber(value) {
    return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0;
  }

  function captureSettings(track) {
    if (!track || typeof track.getSettings !== 'function') return {};
    try {
      var settings = track.getSettings();
      return settings && typeof settings === 'object' ? settings : {};
    } catch (error) {
      return {};
    }
  }

  function captureMatchesRequest(snapshot) {
    if (!snapshot) return false;
    if (!(snapshot.actualWidth > 0) || !(snapshot.actualHeight > 0)) return false;
    var sizeMatches = snapshot.actualWidth >= snapshot.requestedWidth * 0.95 &&
      snapshot.actualHeight >= snapshot.requestedHeight * 0.95;
    var fpsMatches = !(snapshot.requestedFrameRate > 0) ||
      (snapshot.actualFrameRate > 0 && snapshot.actualFrameRate >= snapshot.requestedFrameRate * 0.9);
    return sizeMatches && fpsMatches;
  }

  function buildCaptureSettingsSnapshot(track, requested, status) {
    requested = requested && typeof requested === 'object' ? requested : {};
    var settings = captureSettings(track);
    var snapshot = {
      requestedWidth: positiveNumber(Number(requested.width)),
      requestedHeight: positiveNumber(Number(requested.height)),
      requestedFrameRate: positiveNumber(Number(requested.frameRate)),
      actualWidth: positiveNumber(Number(settings.width)),
      actualHeight: positiveNumber(Number(settings.height)),
      actualFrameRate: positiveNumber(Number(settings.frameRate)),
      captureDisplaySurface: typeof settings.displaySurface === 'string'
        ? settings.displaySurface.toLowerCase()
        : '',
      captureConstraintStatus: String(status || '')
    };
    if (!snapshot.captureConstraintStatus) {
      if (!snapshot.actualWidth || !snapshot.actualHeight) {
        snapshot.captureConstraintStatus = 'settings-unavailable';
      } else {
        snapshot.captureConstraintStatus = captureMatchesRequest(snapshot)
          ? 'matched'
          : 'browser-limited';
      }
    }
    return snapshot;
  }

  function buildCaptureFallbackProfiles(requested) {
    requested = requested && typeof requested === 'object' ? requested : {};
    var width = positiveNumber(Number(requested.width)) || 1280;
    var height = positiveNumber(Number(requested.height)) || 720;
    var fps = Math.max(1, Math.min(60, positiveNumber(Number(requested.frameRate)) || 30));
    var profiles = [];
    function add(name, profileWidth, profileHeight, profileFps) {
      if (profileWidth > width || profileHeight > height || profileFps > fps) return;
      if (profiles.some(function (profile) {
        return profile.width === profileWidth && profile.height === profileHeight &&
          profile.frameRate === profileFps;
      })) return;
      profiles.push({name: name, width: profileWidth, height: profileHeight, frameRate: profileFps});
    }
    var fpsTiers = [60, 45, 30].filter(function (candidate) { return candidate <= fps; });
    if (!fpsTiers.length) fpsTiers.push(fps);
    function addResolutionTiers(label, profileWidth, profileHeight) {
      fpsTiers.forEach(function (profileFps) {
        add(label + 'p' + Math.round(profileFps), profileWidth, profileHeight, profileFps);
      });
    }
    if (width >= 1920 && height >= 1080) {
      addResolutionTiers('1080', 1920, 1080);
      addResolutionTiers('720', 1280, 720);
      addResolutionTiers('540', 960, 540);
    } else if (width >= 1280 && height >= 720) {
      addResolutionTiers('720', 1280, 720);
      addResolutionTiers('540', 960, 540);
    } else if (width >= 960 && height >= 540) {
      addResolutionTiers('540', 960, 540);
    } else {
      add(Math.round(height) + 'p' + Math.round(fps), width, height, fps);
    }
    if (!profiles.length) add(Math.round(height) + 'p' + Math.round(fps), width, height, fps);
    return profiles;
  }

  function createTelemetryReasonLatch() {
    return {nextVersion: 0, reasons: Object.create(null)};
  }

  function recordTelemetryResetReasons(latch, reasonText) {
    if (!latch || typeof latch !== 'object') return false;
    if (!latch.reasons || typeof latch.reasons !== 'object') {
      latch.reasons = Object.create(null);
    }
    if (!Number.isFinite(latch.nextVersion) || latch.nextVersion < 0) {
      latch.nextVersion = 0;
    }
    var recorded = false;
    String(reasonText || '').split(',').forEach(function (reason) {
      var token = reason.trim();
      if (TELEMETRY_RESET_REASON_ORDER.indexOf(token) < 0) return;
      latch.nextVersion += 1;
      latch.reasons[token] = latch.nextVersion;
      recorded = true;
    });
    return recorded;
  }

  function telemetryReasonBatch(latch) {
    var versions = Object.create(null);
    var reasons = [];
    var source = latch && latch.reasons && typeof latch.reasons === 'object'
      ? latch.reasons
      : {};
    TELEMETRY_RESET_REASON_ORDER.forEach(function (reason) {
      var version = source[reason];
      if (typeof version !== 'number' || !Number.isFinite(version)) return;
      versions[reason] = version;
      reasons.push(reason);
    });
    return {versions: versions, text: reasons.join(',')};
  }

  function acknowledgeTelemetryReasonBatch(latch, batch) {
    if (!latch || !latch.reasons) return;
    Object.keys(batch.versions).forEach(function (reason) {
      if (latch.reasons[reason] === batch.versions[reason]) {
        delete latch.reasons[reason];
      }
    });
  }

  function sendTelemetryReport(latch, payload, send) {
    var batch = telemetryReasonBatch(latch);
    var reportPayload = Object.assign({}, payload || {});
    delete reportPayload.telemetryResetReason;
    if (batch.text) reportPayload.telemetryResetReason = batch.text;

    return Promise.resolve().then(function () {
      if (typeof send !== 'function') return false;
      return send(reportPayload);
    }).then(function (sent) {
      if (sent !== true) return false;
      acknowledgeTelemetryReasonBatch(latch, batch);
      return true;
    }, function () {
      return false;
    });
  }

  function isDiagnosticVideoRtp(item, type) {
    return !!item &&
      item.type === type &&
      (item.kind === 'video' || item.mediaType === 'video') &&
      item.active !== false;
  }

  function videoRtpReports(stats, type) {
    return allVideoRtpReports(stats, type).filter(function (item) {
      return item.active !== false;
    });
  }

  function allVideoRtpReports(stats, type) {
    var reports = [];
    if (!stats || typeof stats.forEach !== 'function') return reports;
    stats.forEach(function (item) {
      if (item && item.type === type && item.isRemote !== true &&
          (item.kind === 'video' || item.mediaType === 'video')) reports.push(item);
    });
    return reports;
  }

  function statsEntries(stats) {
    var entries = [];
    if (!stats || typeof stats.forEach !== 'function') return entries;
    try {
      stats.forEach(function (item) {
        if (item && typeof item === 'object') entries.push(item);
      });
    } catch (error) {}
    return entries;
  }

  function statsGet(stats, id) {
    if (!stats || id === undefined || id === null || id === '') return null;
    try {
      if (typeof stats.get === 'function') {
        var direct = stats.get(id);
        if (direct) return direct;
      }
    } catch (error) {}
    var wanted = String(id);
    var found = null;
    if (typeof stats.forEach === 'function') {
      try {
        stats.forEach(function (item) {
          if (!found && item && String(item.id || '') === wanted) found = item;
        });
      } catch (error2) {}
    }
    return found;
  }

  function codecNameFromValue(value) {
    var text = String(value || '').trim();
    if (!text) return '';
    var lower = text.toLowerCase();
    if (lower.indexOf('audio/') === 0) return '';
    var slash = text.lastIndexOf('/');
    if (slash >= 0) text = text.slice(slash + 1);
    text = text.split(/[\s;/]/)[0].trim();
    lower = text.toLowerCase();
    if (lower === 'rtx' || lower === 'red' || lower === 'ulpfec' ||
        lower === 'flexfec-03' || lower === 'telephone-event') return '';
    if (lower === 'h264' || lower === 'h.264' || lower === 'avc1' || lower.indexOf('h264') === 0) return 'H264';
    if (lower === 'vp8' || lower.indexOf('vp8') === 0) return 'VP8';
    if (lower === 'vp9' || lower.indexOf('vp9') === 0) return 'VP9';
    if (lower === 'av1' || lower.indexOf('av1') === 0) return 'AV1';
    return '';
  }

  function codecNameFromReport(report) {
    if (!report || typeof report !== 'object') return '';
    return codecNameFromValue(report.mimeType || report.mime || report.codec || '');
  }

  function describeCodec(stats, report, sdp) {
    report = report && typeof report === 'object' ? report : {};
    var codecId = report.codecId;
    var payloadType = report.payloadType;
    var direct = statsGet(stats, codecId);
    var directName = codecNameFromReport(direct);
    if (directName) return directName;

    var entries = statsEntries(stats);
    var bestName = '';
    var bestScore = -1;
    entries.forEach(function (item) {
      if (!item || item.type !== 'codec') return;
      var name = codecNameFromReport(item);
      if (!name) return;
      var score = 1;
      if (codecId !== undefined && codecId !== null &&
          String(item.id || '') === String(codecId)) score += 100;
      if (payloadType !== undefined && payloadType !== null &&
          Number(item.payloadType) === Number(payloadType)) score += 50;
      if (score > bestScore) {
        bestScore = score;
        bestName = name;
      }
    });
    if (bestName) return bestName;

    var reportName = codecNameFromReport(report);
    if (reportName) return reportName;

    var text = String(sdp || '');
    var matcher = /a=rtpmap:(\d+)\s+([^\s\/]+)/ig;
    var firstVideoCodec = '';
    var match;
    while ((match = matcher.exec(text))) {
      var sdpName = codecNameFromValue(match[2]);
      if (!sdpName) continue;
      if (payloadType !== undefined && payloadType !== null &&
          String(match[1]) === String(payloadType)) return sdpName;
      if (!firstVideoCodec) firstVideoCodec = sdpName;
    }
    return payloadType === undefined || payloadType === null ? firstVideoCodec : '';
  }

  function counterOrMinusOne(report, key) {
    var value = finiteCounter(report && report[key]);
    return value === undefined ? -1 : value;
  }

  function comparePrimaryVideoRtp(left, right, type) {
    // Inbound reports survive a simulcast/decoder switch for a while. Their
    // old resolution and cumulative byte counters can look healthier than the
    // report that is actually decoding. Prefer live decoder/packet activity
    // before falling back to resolution and RID ordering.
    if (type === 'inbound-rtp') {
      var leftFps = positiveNumber(Number(left.framesPerSecond));
      var rightFps = positiveNumber(Number(right.framesPerSecond));
      if (leftFps !== rightFps) return leftFps - rightFps;
      var leftLastPacket = counterOrMinusOne(left, 'lastPacketReceivedTimestamp');
      var rightLastPacket = counterOrMinusOne(right, 'lastPacketReceivedTimestamp');
      if ((leftLastPacket > 0) !== (rightLastPacket > 0)) {
        return leftLastPacket > 0 ? 1 : -1;
      }
      if (leftLastPacket !== rightLastPacket) return leftLastPacket - rightLastPacket;
      var leftDecoded = counterOrMinusOne(left, 'framesDecoded');
      var rightDecoded = counterOrMinusOne(right, 'framesDecoded');
      if ((leftDecoded > 0) !== (rightDecoded > 0)) return leftDecoded > 0 ? 1 : -1;
      var leftReceived = counterOrMinusOne(left, 'framesReceived');
      var rightReceived = counterOrMinusOne(right, 'framesReceived');
      if ((leftReceived > 0) !== (rightReceived > 0)) return leftReceived > 0 ? 1 : -1;
      var leftEmitted = counterOrMinusOne(left, 'jitterBufferEmittedCount');
      var rightEmitted = counterOrMinusOne(right, 'jitterBufferEmittedCount');
      if ((leftEmitted > 0) !== (rightEmitted > 0)) return leftEmitted > 0 ? 1 : -1;
      if (leftEmitted !== rightEmitted) return leftEmitted - rightEmitted;
      if (leftDecoded !== rightDecoded) return leftDecoded - rightDecoded;
      if (leftReceived !== rightReceived) return leftReceived - rightReceived;
    }
    var leftPixels = positiveNumber(Number(left.frameWidth || left.width)) *
      positiveNumber(Number(left.frameHeight || left.height));
    var rightPixels = positiveNumber(Number(right.frameWidth || right.width)) *
      positiveNumber(Number(right.frameHeight || right.height));
    if (leftPixels !== rightPixels) return leftPixels - rightPixels;
    var layerRank = {q: 1, h: 2, f: 3};
    var leftRank = layerRank[String(left.rid || '').toLowerCase()] || 0;
    var rightRank = layerRank[String(right.rid || '').toLowerCase()] || 0;
    if (leftRank !== rightRank) return leftRank - rightRank;
    var leftFps = positiveNumber(Number(left.framesPerSecond));
    var rightFps = positiveNumber(Number(right.framesPerSecond));
    if (leftFps !== rightFps) return leftFps - rightFps;
    return String(right.id || '').localeCompare(String(left.id || ''));
  }

  function selectPrimaryVideoRtp(stats, type, preferredId) {
    var reports = videoRtpReports(stats, type);
    if (!reports.length) return null;
    return reports.reduce(function (selected, report) {
      if (!selected) return report;
      var comparison = comparePrimaryVideoRtp(report, selected, type);
      if (comparison !== 0) return comparison > 0 ? report : selected;
      if (preferredId && String(report.id || '') === String(preferredId)) return report;
      return selected;
    }, null);
  }

  function describeVideoRtpLayers(stats, type) {
    return allVideoRtpReports(stats, type).map(function (report) {
      return {
        id: String(report.id || ''),
        rid: String(report.rid || ''),
        active: report.active !== false,
        width: positiveNumber(Number(report.frameWidth)),
        height: positiveNumber(Number(report.frameHeight)),
        fps: positiveNumber(Number(report.framesPerSecond)),
        bytes: finiteCounter(type === 'outbound-rtp' ? report.bytesSent : report.bytesReceived) || 0,
        frames: finiteCounter(type === 'outbound-rtp' ? report.framesEncoded : report.framesDecoded) || 0,
        qualityLimitationReason: String(report.qualityLimitationReason || '')
      };
    }).sort(function (left, right) {
      return comparePrimaryVideoRtp(left, right, type) > 0 ? -1 : 1;
    });
  }

  function sortedIds(items) {
    return items.map(function (item) { return String(item.id || ''); }).sort();
  }

  function sameIds(left, right) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) {
      return false;
    }
    for (var index = 0; index < left.length; index += 1) {
      if (left[index] !== right[index]) return false;
    }
    return true;
  }

  function collectMetric(items, field, scale) {
    if (!items.length) return undefined;
    var multiplier = scale || 1;
    var byId = Object.create(null);
    var total = 0;
    for (var index = 0; index < items.length; index += 1) {
      var item = items[index];
      var value = finiteCounter(item[field]);
      var id = String(item.id || '');
      if (value === undefined || !id) return undefined;
      value *= multiplier;
      byId[id] = value;
      total += value;
    }
    return {total: total, byId: byId};
  }

  function collectMediaSource(stats, outbound) {
    if (!outbound.length) return {ids: [], framesCaptured: undefined};
    var seen = Object.create(null);
    var ids = [];
    for (var index = 0; index < outbound.length; index += 1) {
      var sourceId = String(outbound[index].mediaSourceId || '');
      if (!sourceId) return {ids: [], framesCaptured: undefined};
      if (!seen[sourceId]) {
        seen[sourceId] = true;
        ids.push(sourceId);
      }
    }
    ids.sort();

    var byId = Object.create(null);
    var total = 0;
    for (var sourceIndex = 0; sourceIndex < ids.length; sourceIndex += 1) {
      var id = ids[sourceIndex];
      var source = stats.get(id);
      var frames = finiteCounter(source && source.frames);
      if (
        !source || source.type !== 'media-source' ||
        (source.kind !== 'video' && source.mediaType !== 'video') ||
        frames === undefined
      ) {
        return {ids: ids, framesCaptured: undefined};
      }
      byId[id] = frames;
      total += frames;
    }
    return {ids: ids, framesCaptured: {total: total, byId: byId}};
  }

  function collectDiagnosticSnapshot(stats) {
    var outbound = [];
    var inbound = [];
    stats.forEach(function (item) {
      if (isDiagnosticVideoRtp(item, 'outbound-rtp')) outbound.push(item);
      if (isDiagnosticVideoRtp(item, 'inbound-rtp')) inbound.push(item);
    });
    outbound.sort(function (left, right) {
      return String(left.id || '').localeCompare(String(right.id || ''));
    });
    inbound.sort(function (left, right) {
      return String(left.id || '').localeCompare(String(right.id || ''));
    });

    var mediaSource = collectMediaSource(stats, outbound);
    return {
      outboundIds: sortedIds(outbound),
      inboundIds: sortedIds(inbound),
      mediaSourceIds: mediaSource.ids,
      outbound: {
        framesEncoded: collectMetric(outbound, 'framesEncoded'),
        framesSent: collectMetric(outbound, 'framesSent'),
        keyFramesEncoded: collectMetric(outbound, 'keyFramesEncoded'),
        pliCount: collectMetric(outbound, 'pliCount'),
        nackCount: collectMetric(outbound, 'nackCount'),
        retransmittedPacketsSent: collectMetric(outbound, 'retransmittedPacketsSent'),
        retransmittedBytesSent: collectMetric(outbound, 'retransmittedBytesSent'),
        totalPacketSendDelayMs: collectMetric(outbound, 'totalPacketSendDelay', 1000),
        packetsSent: collectMetric(outbound, 'packetsSent')
      },
      inbound: {
        pliCount: collectMetric(inbound, 'pliCount'),
        nackCount: collectMetric(inbound, 'nackCount'),
        framesReceived: collectMetric(inbound, 'framesReceived'),
        jitterBufferDelayMs: collectMetric(inbound, 'jitterBufferDelay', 1000),
        jitterBufferEmittedCount: collectMetric(inbound, 'jitterBufferEmittedCount')
      },
      mediaSource: {
        framesCaptured: mediaSource.framesCaptured
      }
    };
  }

  function deltaMetric(current, previous) {
    if (!current || !previous) return {available: false, reset: false};
    var currentIds = Object.keys(current.byId).sort();
    var previousIds = Object.keys(previous.byId).sort();
    if (!sameIds(currentIds, previousIds)) return {available: false, reset: false};
    var total = 0;
    for (var index = 0; index < currentIds.length; index += 1) {
      var id = currentIds[index];
      var value = current.byId[id] - previous.byId[id];
      if (value < 0) return {available: false, reset: true};
      total += value;
    }
    return {available: true, reset: false, value: total};
  }

  function setMetricTotal(sample, metric, field) {
    if (metric) sample[field] = metric.total;
  }

  function applyMetricDelta(sample, current, previous, field, fpsField, elapsedMs, allowed, reasons) {
    var result = deltaMetric(current, previous);
    if (result.reset) reasons['optional-counter-reset'] = true;
    if (!allowed || !result.available) return result;
    sample[field] = result.value;
    if (fpsField) sample[fpsField] = result.value * 1000 / elapsedMs;
    return result;
  }

  function applyDiagnosticTelemetry(sample, current, previous, elapsedMs) {
    var currentOutbound = current.outbound;
    var currentInbound = current.inbound;
    var currentMediaSource = current.mediaSource;

    setMetricTotal(sample, currentMediaSource.framesCaptured, 'framesCaptured');
    setMetricTotal(sample, currentOutbound.framesEncoded, 'framesEncoded');
    setMetricTotal(sample, currentOutbound.framesSent, 'framesSent');
    setMetricTotal(sample, currentOutbound.keyFramesEncoded, 'keyFramesEncoded');
    setMetricTotal(sample, currentOutbound.pliCount, 'outboundPliCount');
    setMetricTotal(sample, currentOutbound.nackCount, 'outboundNackCount');
    setMetricTotal(sample, currentInbound.pliCount, 'inboundPliCount');
    setMetricTotal(sample, currentInbound.nackCount, 'inboundNackCount');
    setMetricTotal(sample, currentInbound.jitterBufferDelayMs, 'jitterBufferDelayMs');
    setMetricTotal(sample, currentInbound.jitterBufferEmittedCount, 'jitterBufferEmittedCount');
    setMetricTotal(sample, currentOutbound.retransmittedPacketsSent, 'retransmittedPacketsSent');
    setMetricTotal(sample, currentOutbound.retransmittedBytesSent, 'retransmittedBytesSent');
    setMetricTotal(sample, currentOutbound.totalPacketSendDelayMs, 'totalPacketSendDelayMs');

    if (!previous || !previous.diagnostic) return;
    var previousDiagnostic = previous.diagnostic;
    var outboundSame = sameIds(current.outboundIds, previousDiagnostic.outboundIds);
    var inboundSame = sameIds(current.inboundIds, previousDiagnostic.inboundIds);
    var mediaSourceSame = sameIds(current.mediaSourceIds, previousDiagnostic.mediaSourceIds);
    var reasons = Object.create(null);
    if (!outboundSame) reasons['outbound-identity-changed'] = true;
    if (!inboundSame) reasons['inbound-identity-changed'] = true;
    if (!mediaSourceSame) reasons['media-source-identity-changed'] = true;

    var previousOutbound = previousDiagnostic.outbound || {};
    var previousInbound = previousDiagnostic.inbound || {};
    var previousMediaSource = previousDiagnostic.mediaSource || {};
    var hasElapsedWindow = elapsedMs > 0;
    var canUseOutbound = hasElapsedWindow && outboundSame;
    var canUseInbound = hasElapsedWindow && inboundSame;
    var canUseMediaSource = hasElapsedWindow && mediaSourceSame;

    applyMetricDelta(
      sample,
      currentMediaSource.framesCaptured,
      previousMediaSource.framesCaptured,
      'framesCapturedDelta',
      'capturedFps',
      elapsedMs,
      canUseMediaSource,
      reasons
    );
    applyMetricDelta(
      sample,
      currentOutbound.framesEncoded,
      previousOutbound.framesEncoded,
      'framesEncodedDelta',
      'encodedFps',
      elapsedMs,
      canUseOutbound,
      reasons
    );
    applyMetricDelta(
      sample,
      currentOutbound.framesSent,
      previousOutbound.framesSent,
      'framesSentDelta',
      'sentFps',
      elapsedMs,
      canUseOutbound,
      reasons
    );
    applyMetricDelta(
      sample,
      currentOutbound.keyFramesEncoded,
      previousOutbound.keyFramesEncoded,
      'keyFramesEncodedDelta',
      '',
      elapsedMs,
      canUseOutbound,
      reasons
    );
    applyMetricDelta(
      sample,
      currentOutbound.pliCount,
      previousOutbound.pliCount,
      'outboundPliCountDelta',
      '',
      elapsedMs,
      canUseOutbound,
      reasons
    );
    applyMetricDelta(
      sample,
      currentOutbound.nackCount,
      previousOutbound.nackCount,
      'outboundNackCountDelta',
      '',
      elapsedMs,
      canUseOutbound,
      reasons
    );
    applyMetricDelta(
      sample,
      currentInbound.pliCount,
      previousInbound.pliCount,
      'inboundPliCountDelta',
      '',
      elapsedMs,
      canUseInbound,
      reasons
    );
    applyMetricDelta(
      sample,
      currentInbound.nackCount,
      previousInbound.nackCount,
      'inboundNackCountDelta',
      '',
      elapsedMs,
      canUseInbound,
      reasons
    );
    applyMetricDelta(
      sample,
      currentInbound.framesReceived,
      previousInbound.framesReceived,
      'framesReceivedDelta',
      'arrivalFps',
      elapsedMs,
      canUseInbound,
      reasons
    );
    var jitterBufferDelay = applyMetricDelta(
      sample,
      currentInbound.jitterBufferDelayMs,
      previousInbound.jitterBufferDelayMs,
      'jitterBufferDelayDeltaMs',
      '',
      elapsedMs,
      canUseInbound,
      reasons
    );
    var jitterBufferEmitted = applyMetricDelta(
      sample,
      currentInbound.jitterBufferEmittedCount,
      previousInbound.jitterBufferEmittedCount,
      'jitterBufferEmittedCountDelta',
      '',
      elapsedMs,
      canUseInbound,
      reasons
    );
    if (jitterBufferDelay.available && jitterBufferEmitted.available &&
        jitterBufferEmitted.value > 0) {
      sample.jitterBufferActualMs = jitterBufferDelay.value / jitterBufferEmitted.value;
    }
    applyMetricDelta(
      sample,
      currentOutbound.retransmittedPacketsSent,
      previousOutbound.retransmittedPacketsSent,
      'retransmittedPacketsSentDelta',
      '',
      elapsedMs,
      canUseOutbound,
      reasons
    );
    applyMetricDelta(
      sample,
      currentOutbound.retransmittedBytesSent,
      previousOutbound.retransmittedBytesSent,
      'retransmittedBytesSentDelta',
      '',
      elapsedMs,
      canUseOutbound,
      reasons
    );
    var delay = applyMetricDelta(
      sample,
      currentOutbound.totalPacketSendDelayMs,
      previousOutbound.totalPacketSendDelayMs,
      'totalPacketSendDelayDeltaMs',
      '',
      elapsedMs,
      canUseOutbound,
      reasons
    );
    var sentPackets = deltaMetric(currentOutbound.packetsSent, previousOutbound.packetsSent);
    if (sentPackets.reset) reasons['optional-counter-reset'] = true;
    if (
      canUseOutbound && delay.available && sentPackets.available &&
      sentPackets.value > 0
    ) {
      sample.averagePacketSendDelayMs = delay.value / sentPackets.value;
    }

    var resetReasons = TELEMETRY_RESET_REASON_ORDER.filter(function (reason) {
      return reasons[reason];
    });
    if (resetReasons.length) sample.telemetryResetReason = resetReasons.join(',');
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
    var primaryInbound = selectPrimaryVideoRtp(stats, 'inbound-rtp');
    stats.forEach(function (item) {
      if (item.type === 'inbound-rtp' && (item.kind === 'video' || item.mediaType === 'video') &&
          primaryInbound && item.id === primaryInbound.id) {
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
    var diagnostic = collectDiagnosticSnapshot(stats);
    var snapshot = Object.assign({}, currentTotals, {
      now: now,
      pairId: pairDescription.pairId,
      generation: number(context.generation),
      diagnostic: diagnostic
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
    sample.outboundLayers = describeVideoRtpLayers(stats, 'outbound-rtp');
    sample.inboundLayers = describeVideoRtpLayers(stats, 'inbound-rtp');

    sample.visibilityState = context.visible === false ? 'hidden' : 'visible';
    if (context.sourceChanged) sample.invalidReason = 'source-changed';
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
        sample.elapsedMs = elapsedMs;
        sample.packetsReceivedDelta = received;
        sample.packetsLostDelta = lost;
        sample.packetsSentDelta = sent;
        sample.framesDecodedDelta = decoded;
        sample.framesDroppedDelta = dropped;
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
    var diagnosticElapsedMs = previous && typeof previous.now === 'number'
      ? now - previous.now
      : 0;
    applyDiagnosticTelemetry(
      sample,
      diagnostic,
      previous,
      diagnosticElapsedMs
    );
    return sample;
  }

  function buildViewerQualityReport(sample, context) {
    context = context || {};
    var mode = String(context.mode || '');
    var room = String(context.room || '').trim();
    var participantId = String(context.participantId || '').trim();
    var screegoSessionId = String(context.screegoSessionId || '').trim();
    var totalPackets = number(sample && sample.packetsReceivedDelta) + number(sample && sample.packetsLostDelta);
    if (
      !sample || !sample.valid ||
      (mode !== 'native-viewer' && mode !== 'sfu-viewer') ||
      !room || !participantId || (mode === 'native-viewer' && !screegoSessionId) || !sample.pairId ||
      !(number(sample.elapsedMs) > 0) ||
      totalPackets < 20
    ) return null;
    var report = {
      room: room,
      participantId: participantId,
      generation: String(sample.generation || 0),
      pairId: String(sample.pairId),
      packetsReceivedDelta: Math.max(0, Math.round(number(sample.packetsReceivedDelta))),
      packetsLostDelta: Math.max(0, Math.round(number(sample.packetsLostDelta))),
      framesDroppedDelta: Math.max(0, Math.round(number(sample.framesDroppedDelta))),
      decodedFps: Math.max(0, number(sample.framesDecodedDelta) * 1000 / number(sample.elapsedMs)),
      incomingBitrateKbps: Math.max(0, number(sample.downKbps)),
      jitterMs: Math.max(0, number(sample.jitterMs))
    };
    if (screegoSessionId) report.screegoSessionId = screegoSessionId;
    if (sample.bufferTargetMs !== undefined) {
      report.bufferTargetMs = Math.max(0, Math.min(300, number(sample.bufferTargetMs)));
    }
    if (sample.bufferActualMs !== undefined) {
      report.bufferActualMs = Math.max(0, Math.min(60000, number(sample.bufferActualMs)));
    }
    if (sample.bufferReason !== undefined) {
      report.bufferReason = String(sample.bufferReason || 'baseline').slice(0, 64);
    }
    if (sample.flowDeficit !== undefined) report.flowDeficit = sample.flowDeficit === true;
    if (context.protocolVersion === 2 && Number(context.reportSeq) > 0) {
      report.protocolVersion = 2;
      report.reportSeq = Math.floor(Number(context.reportSeq));
      report.sampleDurationMs = Math.max(0, Math.min(4000, Math.round(number(sample.elapsedMs))));
    }
    var layer = String(context.layer || '').trim().toLowerCase();
    if (layer === 'h' || layer === 'f') report.layer = layer;
    return report;
  }

  return {
    describeSelectedPair: describeSelectedPair,
    buildWebRTCSample: buildWebRTCSample,
    buildViewerQualityReport: buildViewerQualityReport,
    createTelemetryReasonLatch: createTelemetryReasonLatch,
    recordTelemetryResetReasons: recordTelemetryResetReasons,
    sendTelemetryReport: sendTelemetryReport,
    buildCaptureSettingsSnapshot: buildCaptureSettingsSnapshot,
    buildCaptureFallbackProfiles: buildCaptureFallbackProfiles,
    captureMatchesRequest: captureMatchesRequest,
    selectPrimaryVideoRtp: selectPrimaryVideoRtp,
    describeVideoRtpLayers: describeVideoRtpLayers,
    describeCodec: describeCodec
  };
});
