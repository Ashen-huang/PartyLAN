import test from 'node:test';
import assert from 'node:assert/strict';

import { GameCastReceiver, computeGameCastVideoStats } from '../src/media/gamecastReceiver.ts';

class FakeMediaStream {
  tracks = [];

  addTrack(track) {
    this.tracks.push(track);
  }

  getTracks() {
    return this.tracks.slice();
  }
}

class FakePeerConnection {
  static instances = [];

  connectionState = 'new';
  remoteDescription = null;
  localDescription = null;
  addedICE = [];
  closed = false;
  onicecandidate = null;
  ontrack = null;
  onconnectionstatechange = null;

  configuration = null;

  constructor(configuration) {
    this.configuration = configuration;
    FakePeerConnection.instances.push(this);
  }

  async setRemoteDescription(description) {
    this.remoteDescription = description;
  }

  async createAnswer() {
    return { type: 'answer', sdp: 'v=0\r\n' };
  }

  async setLocalDescription(description) {
    this.localDescription = description;
  }

  async addIceCandidate(candidate) {
    this.addedICE.push(candidate);
  }

  async getStats() {
    return new Map();
  }

  close() {
    this.closed = true;
    this.connectionState = 'closed';
  }
}

function installWebRTCFakes() {
  FakePeerConnection.instances = [];
  globalThis.MediaStream = FakeMediaStream;
  globalThis.RTCPeerConnection = FakePeerConnection;
}

test('viewer preserves ICE candidates received before the offer', async () => {
  installWebRTCFakes();
  const outbound = [];
  const receiver = new GameCastReceiver({
    serverBaseUrl: 'https://gamecast.test',
    onSignal: (signal) => outbound.push(signal),
  });

  const candidate = { candidate: 'candidate:1 1 udp 1 127.0.0.1 9000 typ host' };
  await receiver.handleSignal({ type: 'ice-candidate', ice: candidate });
  await receiver.handleSignal({ type: 'offer', sdp: 'v=0\r\n' });

  assert.equal(FakePeerConnection.instances.length, 1);
  assert.deepEqual(FakePeerConnection.instances[0].addedICE, [candidate]);
  assert.equal(outbound.length, 1);
  assert.equal(outbound[0].type, 'answer');
});

test('viewer uses Screego session ICE servers and echoes the session id', async () => {
  installWebRTCFakes();
  const outbound = [];
  const receiver = new GameCastReceiver({
    serverBaseUrl: 'https://gamecast.test',
    onSignal: (signal) => outbound.push(signal),
  });
  const iceServers = [{
    urls: ['turn:8.134.126.54:3478?transport=udp'],
    username: 'temporary-user',
    credential: 'temporary-secret',
  }];

  await receiver.handleSignal({
    type: 'offer',
    sessionId: 'session-1',
    sdp: { type: 'offer', sdp: 'v=0\r\n' },
    iceServers,
  });

  const pc = FakePeerConnection.instances[0];
  assert.deepEqual(pc.configuration, { iceServers });
  assert.deepEqual(outbound[0], {
    type: 'answer',
    sessionId: 'session-1',
    sdp: 'v=0\r\n',
  });

  pc.onicecandidate({
    candidate: { toJSON: () => ({ candidate: 'candidate:viewer-1' }) },
  });
  assert.deepEqual(outbound[1], {
    type: 'ice-candidate',
    sessionId: 'session-1',
    ice: { candidate: 'candidate:viewer-1' },
  });
});

test('viewer replaces Screego sessions and ignores stale ICE and endshare', async () => {
  installWebRTCFakes();
  const receiver = new GameCastReceiver({
    serverBaseUrl: 'https://gamecast.test',
    onSignal: () => undefined,
  });

  await receiver.handleSignal({ type: 'offer', sessionId: 'session-1', sdp: 'v=0\r\n', iceServers: [] });
  const first = FakePeerConnection.instances[0];
  await receiver.handleSignal({ type: 'offer', sessionId: 'session-2', sdp: 'v=0\r\n', iceServers: [] });
  const second = FakePeerConnection.instances[1];

  assert.equal(first.closed, true);
  await receiver.handleSignal({
    type: 'ice-candidate',
    sessionId: 'session-1',
    ice: { candidate: 'candidate:stale' },
  });
  assert.deepEqual(second.addedICE, []);

  await receiver.handleSignal({ type: 'endshare', sessionId: 'session-1' });
  assert.equal(second.closed, false);
  await receiver.handleSignal({ type: 'endshare', sessionId: 'session-2' });
  assert.equal(second.closed, true);
});

test('viewer keeps video and shared audio tracks in one remote stream', async () => {
  installWebRTCFakes();
  const streams = [];
  const receiver = new GameCastReceiver({
    serverBaseUrl: 'https://gamecast.test',
    onSignal: () => undefined,
    onRemoteStream: (stream) => streams.push(stream),
  });

  await receiver.handleSignal({ type: 'offer', sdp: 'v=0\r\n' });
  const pc = FakePeerConnection.instances[0];
  const video = { id: 'video-1', kind: 'video' };
  const audio = { id: 'audio-1', kind: 'audio' };
  pc.ontrack({ track: video });
  pc.ontrack({ track: audio });

  assert.equal(streams.length, 2);
  assert.deepEqual(streams.at(-1).getTracks(), [video, audio]);
});

test('viewer close releases its peer connection', async () => {
  installWebRTCFakes();
  const states = [];
  const receiver = new GameCastReceiver({
    serverBaseUrl: 'https://gamecast.test',
    onSignal: () => undefined,
    onStateChange: (state) => states.push(state),
  });

  await receiver.handleSignal({ type: 'offer', sdp: 'v=0\r\n' });
  const pc = FakePeerConnection.instances[0];
  receiver.close('test complete');

  assert.equal(pc.closed, true);
  assert.equal(states.at(-1), 'closed');
});

test('viewer ignores a remote track delivered after close', async () => {
  installWebRTCFakes();
  const streams = [];
  const receiver = new GameCastReceiver({
    serverBaseUrl: 'https://gamecast.test',
    onSignal: () => undefined,
    onRemoteStream: (stream) => streams.push(stream),
  });

  await receiver.handleSignal({ type: 'offer', sdp: 'v=0\r\n' });
  const pc = FakePeerConnection.instances[0];
  receiver.close('test complete');
  pc.ontrack({ track: { id: 'late-video', kind: 'video' } });

  assert.equal(streams.length, 0);
});

test('viewer ignores an in-flight stats result after close', async () => {
  installWebRTCFakes();
  const stats = [];
  let resolveStats;
  const receiver = new GameCastReceiver({
    serverBaseUrl: 'https://gamecast.test',
    onSignal: () => undefined,
    onStats: (sample) => stats.push(sample),
  });

  await receiver.handleSignal({ type: 'offer', sdp: 'v=0\r\n' });
  const pc = FakePeerConnection.instances[0];
  pc.getStats = () => new Promise((resolve) => {
    resolveStats = resolve;
  });

  const pending = receiver.emitStats();
  receiver.close('test complete');
  resolveStats(new Map());
  await pending;

  assert.equal(stats.length, 0);
});

test('viewer stats convert cumulative WebRTC counters into valid deltas', () => {
  const previous = {
    bytesReceived: 100000,
    packetsReceived: 100,
    packetsLost: 2,
    framesDecoded: 50,
    framesDropped: 1,
    timestamp: 1000,
  };
  const report = new Map([
    ['transport', { id: 'transport', type: 'transport', selectedCandidatePairId: 'pair-1' }],
    ['video', {
      id: 'video', type: 'inbound-rtp', kind: 'video',
      bytesReceived: 300000, packetsReceived: 290, packetsLost: 12,
      framesDecoded: 98, framesDropped: 4, jitter: 0.12, timestamp: 3000,
    }],
  ]);

  const result = computeGameCastVideoStats(report, previous, 'generation-1');
  assert.equal(result.stats.valid, true);
  assert.equal(result.stats.generation, 'generation-1');
  assert.equal(result.stats.pairId, 'pair-1');
  assert.equal(result.stats.packetsReceivedDelta, 190);
  assert.equal(result.stats.packetsLostDelta, 10);
  assert.equal(result.stats.framesDroppedDelta, 3);
  assert.equal(result.stats.decodedFps, 24);
  assert.equal(result.stats.incomingBitrateKbps, 800);
});

test('viewer stats invalidate a sample when cumulative counters reset', () => {
  const previous = {
    bytesReceived: 300000,
    packetsReceived: 290,
    packetsLost: 12,
    framesDecoded: 98,
    framesDropped: 4,
    timestamp: 3000,
  };
  const report = new Map([
    ['video', {
      id: 'video', type: 'inbound-rtp', kind: 'video',
      bytesReceived: 1000, packetsReceived: 2, packetsLost: 0,
      framesDecoded: 1, framesDropped: 0, jitter: 0.01, timestamp: 4000,
    }],
  ]);

  const result = computeGameCastVideoStats(report, previous, 'generation-2');
  assert.equal(result.stats.valid, false);
  assert.equal(result.stats.invalidReason, 'counter-reset');
  assert.equal(result.stats.packetsReceivedDelta, null);
  assert.equal(result.stats.packetsLostDelta, null);
});
