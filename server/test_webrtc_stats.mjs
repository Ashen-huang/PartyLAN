import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import test from 'node:test';

const require = createRequire(import.meta.url);
const {buildViewerQualityReport, buildWebRTCSample, describeSelectedPair} = require('./webrtc_stats.js');

const report = (entries) => new Map(entries.map((item) => [item.id, item]));

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
    mode: 'native-viewer',
  });
  assert.deepEqual(quality, {
    room: 'room-1',
    participantId: 'web-1',
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
