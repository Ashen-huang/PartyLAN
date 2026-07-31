import test from 'node:test';
import assert from 'node:assert/strict';

import { GameCastHost } from '../src/media/gamecastHost.ts';

class FakeTrack {
  constructor(kind, id) {
    this.kind = kind;
    this.id = id;
    this.stopped = false;
    this.listeners = new Map();
  }

  addEventListener(name, listener) {
    this.listeners.set(name, listener);
  }

  async applyConstraints() {}

  getSettings() {
    return this.kind === 'video' ? { width: 1280, height: 720, frameRate: 30 } : {};
  }

  stop() {
    this.stopped = true;
  }
}

class FakeMediaStream {
  constructor(tracks = []) {
    this.tracks = tracks;
  }

  getTracks() {
    return this.tracks.slice();
  }

  getVideoTracks() {
    return this.tracks.filter((track) => track.kind === 'video');
  }

  removeTrack(track) {
    this.tracks = this.tracks.filter((current) => current !== track);
  }
}

class FakeSender {
  parameters = { encodings: [{}] };

  getParameters() {
    return this.parameters;
  }

  async setParameters(parameters) {
    this.parameters = parameters;
  }
}

class FakePeerConnection {
  static instances = [];

  remoteDescription = null;
  localDescription = null;
  connectionState = 'new';
  addedICE = [];
  tracks = [];
  closed = false;
  onicecandidate = null;
  onconnectionstatechange = null;

  constructor() {
    FakePeerConnection.instances.push(this);
  }

  addTrack(track) {
    this.tracks.push(track);
    return new FakeSender();
  }

  async createOffer() {
    return { type: 'offer', sdp: 'v=0\r\n' };
  }

  async setLocalDescription(description) {
    this.localDescription = description;
  }

  async setRemoteDescription(description) {
    this.remoteDescription = description;
  }

  async addIceCandidate(candidate) {
    this.addedICE.push(candidate);
  }

  close() {
    this.closed = true;
    this.connectionState = 'closed';
  }
}

function installWebRTCFakes(stream) {
  FakePeerConnection.instances = [];
  let displayConstraints;
  globalThis.RTCPeerConnection = FakePeerConnection;
  Object.defineProperty(globalThis, 'navigator', {
    configurable: true,
    value: {
      mediaDevices: {
        getDisplayMedia: async (constraints) => {
          displayConstraints = constraints;
          return stream;
        },
      },
    },
  });
  return () => displayConstraints;
}

function shareSettings() {
  return {
    width: 1280,
    height: 720,
    fps: 30,
    sourceId: 'window:1',
    sourceKind: 'window',
    sourceLabel: 'Fixture',
    audioMode: 'process',
    audioProcessName: 'fixture.exe',
  };
}

test('compat host discards unexpected display audio and publishes video only', async () => {
  const video = new FakeTrack('video', 'video-1');
  const audio = new FakeTrack('audio', 'audio-1');
  const stream = new FakeMediaStream([video, audio]);
  const getDisplayConstraints = installWebRTCFakes(stream);
  const outbound = [];
  const host = new GameCastHost({
    serverBaseUrl: 'https://gamecast.test',
    onSignal: (signal) => outbound.push(signal),
  });

  await host.capture(shareSettings());
  assert.equal(getDisplayConstraints().audio, false);
  assert.equal(audio.stopped, true);
  assert.deepEqual(stream.getTracks(), [video]);
  await host.publish();

  assert.deepEqual(FakePeerConnection.instances[0].tracks, [video]);
  assert.equal(outbound[0].type, 'offer');
});

test('compat host queues ICE until the SFU answer arrives', async () => {
  const stream = new FakeMediaStream([new FakeTrack('video', 'video-1')]);
  installWebRTCFakes(stream);
  const host = new GameCastHost({
    serverBaseUrl: 'https://gamecast.test',
    onSignal: () => undefined,
  });
  await host.capture(shareSettings());
  await host.publish();

  const candidate = { candidate: 'candidate:1 1 udp 1 127.0.0.1 9000 typ host' };
  await host.handleSignal({ type: 'ice-candidate', ice: candidate });
  assert.deepEqual(FakePeerConnection.instances[0].addedICE, []);

  await host.handleSignal({ type: 'answer', sdp: 'v=0\r\n' });
  assert.deepEqual(FakePeerConnection.instances[0].addedICE, [candidate]);
});

test('compat host stop releases peer and all capture tracks without leaving the room', async () => {
  const video = new FakeTrack('video', 'video-1');
  const audio = new FakeTrack('audio', 'audio-1');
  const stream = new FakeMediaStream([video, audio]);
  installWebRTCFakes(stream);
  const states = [];
  const host = new GameCastHost({
    serverBaseUrl: 'https://gamecast.test',
    onSignal: () => undefined,
    onStateChange: (state) => states.push(state),
  });
  await host.capture(shareSettings());
  await host.publish();
  const pc = FakePeerConnection.instances[0];

  host.stop('test complete');

  assert.equal(pc.closed, true);
  assert.equal(video.stopped, true);
  assert.equal(audio.stopped, true);
  assert.equal(states.at(-1), 'closed');
});
