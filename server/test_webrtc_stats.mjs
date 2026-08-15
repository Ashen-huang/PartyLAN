import assert from 'node:assert/strict';
import fs from 'node:fs';
import {createRequire} from 'node:module';
import test from 'node:test';

const require = createRequire(import.meta.url);
const {
  buildViewerQualityReport,
  buildWebRTCSample,
  createTelemetryReasonLatch,
  describeSelectedPair,
  recordTelemetryResetReasons,
  sendTelemetryReport,
} = require('./webrtc_stats.js');

const report = (entries) => new Map(entries.map((item) => [item.id, item]));
const connectedReport = (entries) => report([
  {
    id: 'pair',
    type: 'candidate-pair',
    nominated: true,
    state: 'succeeded',
    localCandidateId: 'local',
    remoteCandidateId: 'remote',
    currentRoundTripTime: 0.05,
  },
  {id: 'local', type: 'local-candidate', candidateType: 'host', protocol: 'udp', address: '10.0.0.2'},
  {id: 'remote', type: 'remote-candidate', candidateType: 'host', protocol: 'udp', address: '10.0.0.3'},
  ...entries,
]);

const deferred = () => {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return {promise, resolve};
};

test('selected candidates distinguish direct, TURN UDP, TURN TCP and address family', () => {
  const direct = report([
    {id: 'pair', type: 'candidate-pair', selected: true, state: 'succeeded', localCandidateId: 'l', remoteCandidateId: 'r'},
    {id: 'l', type: 'local-candidate', candidateType: 'srflx', protocol: 'udp', address: '100.64.0.2'},
    {id: 'r', type: 'remote-candidate', candidateType: 'prflx', protocol: 'udp', address: '2001:db8::1'},
  ]);
  assert.deepEqual(describeSelectedPair(direct), {
    pairId: 'pair',
    path: 'p2p',
    transport: 'udp',
    localCandidateType: 'srflx',
    remoteCandidateType: 'prflx',
    localAddressFamily: 'ipv4',
    remoteAddressFamily: 'ipv6',
  });

  const relay = report([
    {id: 'pair', type: 'candidate-pair', nominated: true, state: 'succeeded', localCandidateId: 'l', remoteCandidateId: 'r'},
    {id: 'l', type: 'local-candidate', candidateType: 'relay', protocol: 'udp', relayProtocol: 'tcp', address: '113.1.1.1'},
    {id: 'r', type: 'remote-candidate', candidateType: 'srflx', protocol: 'udp', address: '100.64.0.2'},
  ]);
  assert.equal(describeSelectedPair(relay).path, 'turn');
  assert.equal(describeSelectedPair(relay).transport, 'tcp');
});

test('transport selectedCandidatePairId wins over an older nominated pair', () => {
  const stats = report([
    {id: 'transport', type: 'transport', selectedCandidatePairId: 'new'},
    {id: 'old', type: 'candidate-pair', nominated: true, state: 'succeeded', localCandidateId: 'old-l', remoteCandidateId: 'old-r'},
    {id: 'new', type: 'candidate-pair', state: 'succeeded', localCandidateId: 'new-l', remoteCandidateId: 'new-r'},
    {id: 'old-l', type: 'local-candidate', candidateType: 'relay', protocol: 'udp', relayProtocol: 'tcp', address: '113.1.1.1'},
    {id: 'old-r', type: 'remote-candidate', candidateType: 'srflx', protocol: 'udp', address: '100.64.0.2'},
    {id: 'new-l', type: 'local-candidate', candidateType: 'srflx', protocol: 'udp', address: '100.64.0.3'},
    {id: 'new-r', type: 'remote-candidate', candidateType: 'prflx', protocol: 'udp', address: '100.64.0.4'},
  ]);
  assert.equal(describeSelectedPair(stats).pairId, 'new');
  assert.equal(describeSelectedPair(stats).path, 'p2p');
});

test('samples use counter deltas and reject pair changes or counter resets', () => {
  const firstStats = report([
    {id: 'pair-a', type: 'candidate-pair', nominated: true, state: 'succeeded', localCandidateId: 'l', remoteCandidateId: 'r', currentRoundTripTime: 0.05},
    {id: 'l', type: 'local-candidate', candidateType: 'relay', protocol: 'udp', relayProtocol: 'udp', address: '113.1.1.1'},
    {id: 'r', type: 'remote-candidate', candidateType: 'srflx', protocol: 'udp', address: '100.64.0.2'},
    {id: 'in', type: 'inbound-rtp', kind: 'video', packetsReceived: 100, packetsLost: 2, bytesReceived: 100000, framesDecoded: 60, framesDropped: 1, jitter: 0.02},
    {id: 'out', type: 'outbound-rtp', kind: 'video', packetsSent: 90, bytesSent: 90000, framesEncoded: 55, qualityLimitationReason: 'none'},
  ]);
  const first = buildWebRTCSample(firstStats, null, {now: 1000, targetBitrate: 4000000, visible: true});
  assert.equal(first.valid, false);

  const nextStats = report([
    {id: 'pair-a', type: 'candidate-pair', nominated: true, state: 'succeeded', localCandidateId: 'l', remoteCandidateId: 'r', currentRoundTripTime: 0.06, availableOutgoingBitrate: 3000000},
    {id: 'l', type: 'local-candidate', candidateType: 'relay', protocol: 'udp', relayProtocol: 'udp', address: '113.1.1.1'},
    {id: 'r', type: 'remote-candidate', candidateType: 'srflx', protocol: 'udp', address: '100.64.0.2'},
    {id: 'in', type: 'inbound-rtp', kind: 'video', packetsReceived: 190, packetsLost: 12, bytesReceived: 220000, framesDecoded: 95, framesDropped: 3, jitter: 0.12},
    {id: 'out', type: 'outbound-rtp', kind: 'video', packetsSent: 180, bytesSent: 190000, framesEncoded: 100, qualityLimitationReason: 'bandwidth'},
  ]);
  const next = buildWebRTCSample(nextStats, first.snapshot, {now: 3000, targetBitrate: 4000000, visible: true});
  assert.equal(next.valid, true);
  assert.equal(next.packetsReceivedDelta, 90);
  assert.equal(next.packetsLostDelta, 10);
  assert.equal(next.lossPercent, 10);
  assert.equal(next.downKbps, 480);
  assert.equal(next.jitterMs, 120);
  assert.equal(next.qualityLimitationReason, 'bandwidth');
  assert.equal(next.localAddress, undefined);

  const changed = buildWebRTCSample(nextStats, {...next.snapshot, pairId: 'pair-b'}, {now: 5000, targetBitrate: 4000000, visible: true});
  assert.equal(changed.valid, false);
  assert.equal(changed.invalidReason, 'pair-changed');
});

test('background and source changes invalidate a sample without manufacturing loss', () => {
  const stats = report([
    {id: 'pair', type: 'candidate-pair', nominated: true, state: 'succeeded', localCandidateId: 'l', remoteCandidateId: 'r'},
    {id: 'l', type: 'local-candidate', candidateType: 'host', protocol: 'udp', address: '10.0.0.2'},
    {id: 'r', type: 'remote-candidate', candidateType: 'host', protocol: 'udp', address: '10.0.0.3'},
  ]);
  assert.equal(buildWebRTCSample(stats, null, {now: 1000, visible: false}).invalidReason, 'page-hidden');
  assert.equal(buildWebRTCSample(stats, null, {now: 1000, visible: true, sourceChanged: true}).invalidReason, 'source-changed');
});

test('valid inbound samples produce bounded native viewer quality reports', () => {
  const quality = buildViewerQualityReport({
    valid: true,
    pairId: 'pair-1',
    generation: 2,
    elapsedMs: 2000,
    packetsReceivedDelta: 190,
    packetsLostDelta: 10,
    framesDecodedDelta: 48,
    framesDroppedDelta: 3,
    downKbps: 800,
    jitterMs: 120,
  }, {
    room: 'room-1',
    participantId: 'web-1',
    screegoSessionId: 'session-1',
    mode: 'native-viewer',
  });
  assert.deepEqual(quality, {
    room: 'room-1',
    participantId: 'web-1',
    screegoSessionId: 'session-1',
    generation: '2',
    pairId: 'pair-1',
    packetsReceivedDelta: 190,
    packetsLostDelta: 10,
    framesDroppedDelta: 3,
    decodedFps: 24,
    incomingBitrateKbps: 800,
    jitterMs: 120,
  });
  assert.equal(buildViewerQualityReport({...quality, valid: false}, {
    room: 'room-1', participantId: 'web-1', mode: 'native-viewer'
  }), null);
  assert.equal(buildViewerQualityReport({valid: true}, {
    room: 'room-1', participantId: 'web-1', mode: 'native-host'
  }), null);
});

test('diagnostic telemetry records cumulative, per-object delta and derived values', () => {
  const firstStats = connectedReport([
    {id: 'source-shared', type: 'media-source', kind: 'video', frames: 100},
    {
      id: 'out-a', type: 'outbound-rtp', kind: 'video', active: true, mediaSourceId: 'source-shared',
      packetsSent: 100, bytesSent: 100000, framesEncoded: 60, framesSent: 58,
      keyFramesEncoded: 2, pliCount: 1, nackCount: 2,
      retransmittedPacketsSent: 3, retransmittedBytesSent: 3000, totalPacketSendDelay: 0.2,
    },
    {
      id: 'out-b', type: 'outbound-rtp', mediaType: 'video', mediaSourceId: 'source-shared',
      packetsSent: 50, bytesSent: 50000, framesEncoded: 30, framesSent: 29,
      keyFramesEncoded: 1, pliCount: 2, nackCount: 3,
      retransmittedPacketsSent: 2, retransmittedBytesSent: 2000, totalPacketSendDelay: 0.1,
    },
    {
      id: 'out-paused', type: 'outbound-rtp', kind: 'video', active: false,
      packetsSent: 1000, bytesSent: 1000000, framesEncoded: 1000,
    },
    {
      id: 'in-a', type: 'inbound-rtp', mediaType: 'video',
      packetsReceived: 100, packetsLost: 0, bytesReceived: 100000,
      framesDecoded: 50, framesDropped: 0, jitter: 0.01, pliCount: 4, nackCount: 5,
    },
  ]);
  const first = buildWebRTCSample(firstStats, null, {now: 1000, visible: true});

  assert.equal(first.framesCaptured, 100);
  assert.equal(first.framesEncoded, 90);
  assert.equal(first.framesSent, 87);
  assert.equal(first.keyFramesEncoded, 3);
  assert.equal(first.outboundPliCount, 3);
  assert.equal(first.outboundNackCount, 5);
  assert.equal(first.inboundPliCount, 4);
  assert.equal(first.inboundNackCount, 5);
  assert.equal(first.retransmittedPacketsSent, 5);
  assert.equal(first.retransmittedBytesSent, 5000);
  assert.equal(first.totalPacketSendDelayMs, 300);
  assert.equal(first.framesCapturedDelta, undefined);

  const nextStats = connectedReport([
    {id: 'source-shared', type: 'media-source', kind: 'video', frames: 160},
    {
      id: 'out-a', type: 'outbound-rtp', kind: 'video', active: true, mediaSourceId: 'source-shared',
      packetsSent: 160, bytesSent: 160000, framesEncoded: 90, framesSent: 86,
      keyFramesEncoded: 3, pliCount: 3, nackCount: 6,
      retransmittedPacketsSent: 5, retransmittedBytesSent: 5000, totalPacketSendDelay: 0.38,
    },
    {
      id: 'out-b', type: 'outbound-rtp', mediaType: 'video', mediaSourceId: 'source-shared',
      packetsSent: 90, bytesSent: 90000, framesEncoded: 50, framesSent: 48,
      keyFramesEncoded: 2, pliCount: 3, nackCount: 5,
      retransmittedPacketsSent: 3, retransmittedBytesSent: 3200, totalPacketSendDelay: 0.18,
    },
    {
      id: 'out-paused', type: 'outbound-rtp', kind: 'video', active: false,
      packetsSent: 1020, bytesSent: 1020000, framesEncoded: 1010,
    },
    {
      id: 'in-a', type: 'inbound-rtp', mediaType: 'video',
      packetsReceived: 180, packetsLost: 2, bytesReceived: 180000,
      framesDecoded: 90, framesDropped: 1, jitter: 0.02, pliCount: 7, nackCount: 9,
    },
  ]);
  const next = buildWebRTCSample(nextStats, first.snapshot, {now: 3000, visible: true});

  assert.equal(next.valid, true);
  assert.equal(next.packetsSentDelta, 120);
  assert.equal(next.framesCaptured, 160);
  assert.equal(next.framesCapturedDelta, 60);
  assert.equal(next.capturedFps, 30);
  assert.equal(next.framesEncoded, 140);
  assert.equal(next.framesEncodedDelta, 50);
  assert.equal(next.encodedFps, 25);
  assert.equal(next.framesSent, 134);
  assert.equal(next.framesSentDelta, 47);
  assert.equal(next.sentFps, 23.5);
  assert.equal(next.keyFramesEncoded, 5);
  assert.equal(next.keyFramesEncodedDelta, 2);
  assert.equal(next.outboundPliCount, 6);
  assert.equal(next.outboundPliCountDelta, 3);
  assert.equal(next.outboundNackCount, 11);
  assert.equal(next.outboundNackCountDelta, 6);
  assert.equal(next.inboundPliCount, 7);
  assert.equal(next.inboundPliCountDelta, 3);
  assert.equal(next.inboundNackCount, 9);
  assert.equal(next.inboundNackCountDelta, 4);
  assert.equal(next.retransmittedPacketsSent, 8);
  assert.equal(next.retransmittedPacketsSentDelta, 3);
  assert.equal(next.retransmittedBytesSent, 8200);
  assert.equal(next.retransmittedBytesSentDelta, 3200);
  assert.equal(next.totalPacketSendDelayMs, 560);
  assert.equal(next.totalPacketSendDelayDeltaMs, 260);
  assert.equal(next.averagePacketSendDelayMs, 2.6);
  assert.equal(next.diagnosticPacketsSentDelta, undefined);
  assert.equal(next.telemetryResetReason, undefined);
});

test('unsupported optional telemetry is omitted while supported zero remains zero', () => {
  const unsupported = buildWebRTCSample(connectedReport([
    {
      id: 'out', type: 'outbound-rtp', kind: 'video',
      packetsSent: 30, bytesSent: 30000, framesEncoded: 20,
    },
  ]), null, {now: 1000, visible: true});
  assert.equal(unsupported.framesCaptured, undefined);
  assert.equal(unsupported.framesSent, undefined);
  assert.equal(unsupported.keyFramesEncoded, undefined);
  assert.equal(unsupported.outboundPliCount, undefined);
  assert.equal(unsupported.totalPacketSendDelayMs, undefined);

  const supportedZero = buildWebRTCSample(connectedReport([
    {id: 'source', type: 'media-source', mediaType: 'video', frames: 0},
    {
      id: 'out', type: 'outbound-rtp', mediaType: 'video', mediaSourceId: 'source',
      packetsSent: 0, bytesSent: 0, framesEncoded: 0, framesSent: 0,
      keyFramesEncoded: 0, pliCount: 0, nackCount: 0,
      retransmittedPacketsSent: 0, retransmittedBytesSent: 0, totalPacketSendDelay: 0,
    },
    {
      id: 'in', type: 'inbound-rtp', mediaType: 'video',
      packetsReceived: 0, packetsLost: 0, bytesReceived: 0,
      framesDecoded: 0, framesDropped: 0, pliCount: 0, nackCount: 0,
    },
  ]), null, {now: 1000, visible: true});
  assert.equal(supportedZero.framesCaptured, 0);
  assert.equal(supportedZero.framesEncoded, 0);
  assert.equal(supportedZero.framesSent, 0);
  assert.equal(supportedZero.keyFramesEncoded, 0);
  assert.equal(supportedZero.outboundPliCount, 0);
  assert.equal(supportedZero.inboundNackCount, 0);
  assert.equal(supportedZero.retransmittedPacketsSent, 0);
  assert.equal(supportedZero.totalPacketSendDelayMs, 0);

  const partiallyUnsupported = buildWebRTCSample(connectedReport([
    {
      id: 'out-a', type: 'outbound-rtp', kind: 'video',
      packetsSent: 30, bytesSent: 30000, framesEncoded: 20, framesSent: 18,
    },
    {
      id: 'out-b', type: 'outbound-rtp', kind: 'video',
      packetsSent: 20, bytesSent: 20000, framesEncoded: 10,
      framesSent: Number.NaN, pliCount: Number.POSITIVE_INFINITY, nackCount: -1,
    },
  ]), null, {now: 1000, visible: true});
  assert.equal(partiallyUnsupported.framesSent, undefined);
  assert.equal(partiallyUnsupported.outboundPliCount, undefined);
  assert.equal(partiallyUnsupported.outboundNackCount, undefined);
  assert.equal(partiallyUnsupported.framesEncoded, 30);
});

test('diagnostic deltas remain available when only the legacy quality window is invalid', () => {
  const makeStats = (second) => connectedReport([
    {id: 'source', type: 'media-source', kind: 'video', frames: second ? 140 : 100},
    {
      id: 'out', type: 'outbound-rtp', kind: 'video', mediaSourceId: 'source',
      packetsSent: second ? 140 : 100, bytesSent: second ? 140000 : 100000,
      framesEncoded: second ? 90 : 60, framesSent: second ? 86 : 58,
      keyFramesEncoded: second ? 3 : 2, pliCount: second ? 3 : 1,
      nackCount: second ? 5 : 2, retransmittedPacketsSent: second ? 7 : 3,
      retransmittedBytesSent: second ? 7000 : 3000,
      totalPacketSendDelay: second ? 0.32 : 0.2,
    },
    {
      id: 'in', type: 'inbound-rtp', kind: 'video',
      packetsReceived: second ? 140 : 100, packetsLost: second ? 1 : 0,
      bytesReceived: second ? 140000 : 100000,
      framesDecoded: second ? 90 : 60, framesDropped: second ? 1 : 0,
      pliCount: second ? 6 : 4, nackCount: second ? 8 : 5,
    },
  ]);
  const first = buildWebRTCSample(makeStats(false), null, {now: 1000, visible: true});
  const next = buildWebRTCSample(makeStats(true), first.snapshot, {
    now: 3000,
    visible: true,
    resolutionChanged: true,
  });

  assert.equal(next.valid, false);
  assert.equal(next.invalidReason, 'resolution-changed');
  assert.equal(next.framesCapturedDelta, 40);
  assert.equal(next.framesEncodedDelta, 30);
  assert.equal(next.framesSentDelta, 28);
  assert.equal(next.outboundPliCountDelta, 2);
  assert.equal(next.inboundNackCountDelta, 3);
  assert.equal(next.retransmittedPacketsSentDelta, 4);
  assert.equal(next.totalPacketSendDelayDeltaMs, 120);
  assert.equal(next.averagePacketSendDelayMs, 3);
});

test('diagnostic identity changes reset only telemetry baselines', () => {
  const first = buildWebRTCSample(connectedReport([
    {id: 'source-a', type: 'media-source', kind: 'video', frames: 100},
    {
      id: 'out-a', type: 'outbound-rtp', kind: 'video', mediaSourceId: 'source-a',
      packetsSent: 100, bytesSent: 100000, framesEncoded: 60, framesSent: 58,
      keyFramesEncoded: 2, pliCount: 1, nackCount: 2,
      retransmittedPacketsSent: 3, retransmittedBytesSent: 3000, totalPacketSendDelay: 0.2,
    },
    {
      id: 'in-a', type: 'inbound-rtp', kind: 'video',
      packetsReceived: 100, packetsLost: 0, bytesReceived: 100000,
      framesDecoded: 50, framesDropped: 0, pliCount: 4, nackCount: 5,
    },
  ]), null, {now: 1000, visible: true});

  const changed = buildWebRTCSample(connectedReport([
    {id: 'source-b', type: 'media-source', kind: 'video', frames: 160},
    {
      id: 'out-b', type: 'outbound-rtp', kind: 'video', mediaSourceId: 'source-b',
      packetsSent: 180, bytesSent: 180000, framesEncoded: 100, framesSent: 96,
      keyFramesEncoded: 4, pliCount: 3, nackCount: 5,
      retransmittedPacketsSent: 5, retransmittedBytesSent: 5000, totalPacketSendDelay: 0.4,
    },
    {
      id: 'in-b', type: 'inbound-rtp', kind: 'video',
      packetsReceived: 180, packetsLost: 2, bytesReceived: 180000,
      framesDecoded: 90, framesDropped: 1, pliCount: 7, nackCount: 9,
    },
  ]), first.snapshot, {now: 3000, visible: true});

  assert.equal(changed.valid, true);
  assert.equal(changed.invalidReason, '');
  assert.equal(
    changed.telemetryResetReason,
    'outbound-identity-changed,inbound-identity-changed,media-source-identity-changed',
  );
  assert.equal(changed.framesCapturedDelta, undefined);
  assert.equal(changed.framesEncodedDelta, undefined);
  assert.equal(changed.framesSentDelta, undefined);
  assert.equal(changed.inboundPliCountDelta, undefined);
  assert.equal(changed.averagePacketSendDelayMs, undefined);
});

test('a single-layer reset is not hidden by growth in another layer', () => {
  const makeStats = (second) => connectedReport([
    {id: 'source', type: 'media-source', kind: 'video', frames: second ? 160 : 100},
    {
      id: 'out-a', type: 'outbound-rtp', kind: 'video', mediaSourceId: 'source',
      packetsSent: second ? 0 : 100, bytesSent: second ? 120000 : 100000,
      framesEncoded: second ? 0 : 60, framesSent: second ? 0 : 100,
      keyFramesEncoded: second ? 3 : 2, pliCount: second ? 2 : 1, nackCount: second ? 3 : 2,
      retransmittedPacketsSent: second ? 4 : 3,
      retransmittedBytesSent: second ? 4000 : 3000,
      totalPacketSendDelay: second ? 0.3 : 0.2,
    },
    {
      id: 'out-b', type: 'outbound-rtp', kind: 'video', mediaSourceId: 'source',
      packetsSent: second ? 260 : 100, bytesSent: second ? 280000 : 100000,
      framesEncoded: second ? 170 : 60, framesSent: second ? 250 : 100,
      keyFramesEncoded: second ? 12 : 10, pliCount: second ? 4 : 2, nackCount: second ? 6 : 3,
      retransmittedPacketsSent: second ? 7 : 5,
      retransmittedBytesSent: second ? 7000 : 5000,
      totalPacketSendDelay: second ? 0.5 : 0.4,
    },
  ]);
  const first = buildWebRTCSample(makeStats(false), null, {now: 1000, visible: true});
  const next = buildWebRTCSample(makeStats(true), first.snapshot, {now: 3000, visible: true});

  assert.equal(next.valid, true);
  assert.equal(next.packetsSentDelta, 60);
  assert.equal(next.framesSent, 250);
  assert.equal(next.framesSentDelta, undefined);
  assert.equal(next.sentFps, undefined);
  assert.equal(next.framesEncoded, 170);
  assert.equal(next.framesEncodedDelta, undefined);
  assert.equal(next.encodedFps, undefined);
  assert.equal(next.keyFramesEncodedDelta, 3);
  assert.equal(next.totalPacketSendDelayDeltaMs, 200);
  assert.equal(next.averagePacketSendDelayMs, undefined);
  assert.equal(next.telemetryResetReason, 'optional-counter-reset');
});

test('queue delay omits averages for zero packets or incomplete delay counters', () => {
  const makeStats = (packetCount, includeSecondDelay) => connectedReport([
    {
      id: 'out-a', type: 'outbound-rtp', kind: 'video',
      packetsSent: packetCount, bytesSent: packetCount * 1000, framesEncoded: packetCount,
      totalPacketSendDelay: packetCount === 100 ? 0.2 : 0.3,
    },
    {
      id: 'out-b', type: 'outbound-rtp', kind: 'video',
      packetsSent: packetCount, bytesSent: packetCount * 1000, framesEncoded: packetCount,
      ...(includeSecondDelay ? {totalPacketSendDelay: packetCount === 100 ? 0.1 : 0.2} : {}),
    },
  ]);
  const first = buildWebRTCSample(makeStats(100, true), null, {now: 1000, visible: true});
  const zeroPackets = buildWebRTCSample(makeStats(100, true), first.snapshot, {
    now: 3000,
    visible: true,
  });
  assert.equal(zeroPackets.packetsSentDelta, 0);
  assert.equal(zeroPackets.averagePacketSendDelayMs, undefined);

  const missingDelay = buildWebRTCSample(makeStats(120, false), first.snapshot, {
    now: 3000,
    visible: true,
  });
  assert.equal(missingDelay.framesEncodedDelta, 40);
  assert.equal(missingDelay.totalPacketSendDelayMs, undefined);
  assert.equal(missingDelay.totalPacketSendDelayDeltaMs, undefined);
  assert.equal(missingDelay.averagePacketSendDelayMs, undefined);
});

test('legacy aggregate framesEncoded reset still invalidates the core sample', () => {
  const makeStats = (second) => connectedReport([
    {
      id: 'out-a', type: 'outbound-rtp', kind: 'video',
      packetsSent: second ? 140 : 100, bytesSent: second ? 140000 : 100000,
      framesEncoded: second ? 30 : 60,
    },
    {
      id: 'out-b', type: 'outbound-rtp', kind: 'video', active: false,
      packetsSent: second ? 120 : 100, bytesSent: second ? 120000 : 100000,
      framesEncoded: second ? 20 : 60,
    },
  ]);
  const first = buildWebRTCSample(makeStats(false), null, {now: 1000, visible: true});
  const next = buildWebRTCSample(makeStats(true), first.snapshot, {now: 3000, visible: true});

  assert.equal(next.valid, false);
  assert.equal(next.invalidReason, 'counter-reset');
  assert.equal(next.framesEncoded, 30);
  assert.equal(next.framesEncodedDelta, undefined);
  assert.equal(next.telemetryResetReason, 'optional-counter-reset');
});

test('telemetry reason latch filters, deduplicates and orders tokens', async () => {
  const latch = createTelemetryReasonLatch();
  recordTelemetryResetReasons(
    latch,
    'optional-counter-reset,unknown,outbound-identity-changed,' +
      'media-source-identity-changed,outbound-identity-changed,inbound-identity-changed',
  );
  let sentPayload;

  const sent = await sendTelemetryReport(
    latch,
    {event: 'webrtc_sample', telemetryResetReason: 'untrusted'},
    async (payload) => {
      sentPayload = payload;
      return true;
    },
  );

  assert.equal(sent, true);
  assert.equal(
    sentPayload.telemetryResetReason,
    'outbound-identity-changed,inbound-identity-changed,' +
      'media-source-identity-changed,optional-counter-reset',
  );

  let clearedPayload;
  assert.equal(await sendTelemetryReport(latch, {event: 'next'}, (payload) => {
    clearedPayload = payload;
    return true;
  }), true);
  assert.equal(clearedPayload.telemetryResetReason, undefined);
});

test('telemetry failures retain reasons and normalize false, reject and throw', async () => {
  const latch = createTelemetryReasonLatch();
  recordTelemetryResetReasons(latch, 'optional-counter-reset');

  assert.equal(await sendTelemetryReport(latch, {}, () => false), false);
  assert.equal(await sendTelemetryReport(latch, {}, () => Promise.reject(new Error('offline'))), false);
  assert.equal(await sendTelemetryReport(latch, {}, () => {
    throw new Error('sync failure');
  }), false);

  let retryPayload;
  assert.equal(await sendTelemetryReport(latch, {}, (payload) => {
    retryPayload = payload;
    return true;
  }), true);
  assert.equal(retryPayload.telemetryResetReason, 'optional-counter-reset');
});

test('telemetry success cannot clear newer different or same-token reasons', async () => {
  const latch = createTelemetryReasonLatch();
  const firstResponse = deferred();
  recordTelemetryResetReasons(latch, 'outbound-identity-changed');
  let firstPayload;
  const firstRequest = sendTelemetryReport(latch, {request: 1}, (payload) => {
    firstPayload = payload;
    return firstResponse.promise;
  });
  await Promise.resolve();

  recordTelemetryResetReasons(
    latch,
    'inbound-identity-changed,outbound-identity-changed',
  );
  firstResponse.resolve(true);
  assert.equal(await firstRequest, true);
  assert.equal(firstPayload.telemetryResetReason, 'outbound-identity-changed');

  let retryPayload;
  assert.equal(await sendTelemetryReport(latch, {request: 2}, (payload) => {
    retryPayload = payload;
    return true;
  }), true);
  assert.equal(
    retryPayload.telemetryResetReason,
    'outbound-identity-changed,inbound-identity-changed',
  );

  recordTelemetryResetReasons(latch, 'media-source-identity-changed');
  let reappearedPayload;
  assert.equal(await sendTelemetryReport(latch, {request: 3}, (payload) => {
    reappearedPayload = payload;
    return true;
  }), true);
  assert.equal(reappearedPayload.telemetryResetReason, 'media-source-identity-changed');
});

test('source and deployed WebRTC stats modules stay byte-identical', () => {
  const source = fs.readFileSync(new URL('./webrtc_stats.js', import.meta.url));
  const deployed = fs.readFileSync(new URL('./screego/deploy/webrtc-stats.js', import.meta.url));
  assert.deepEqual(deployed, source);
});
