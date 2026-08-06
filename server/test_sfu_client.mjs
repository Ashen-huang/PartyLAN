import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const serverDir = path.dirname(fileURLToPath(import.meta.url));
const source = fs.readFileSync(path.join(serverDir, 'screego', 'deploy', 'sfu-client.js'), 'utf8');

function loadClient(options = {}) {
  const resizeHandlers = [];
  const sockets = [];
  const nativePeers = [];
  const wrappedPeers = [];
  const capabilities = options.capabilities || {
    video: [
      { mimeType: 'video/VP8', clockRate: 90000 },
      { mimeType: 'video/H264', clockRate: 90000, sdpFmtpLine: 'profile-level-id=42e01f' },
    ],
    audio: [
      { mimeType: 'audio/PCMU', clockRate: 8000, channels: 1 },
      { mimeType: 'audio/opus', clockRate: 48000, channels: 2 },
    ],
  };

  class FakeMediaStream {
    constructor(tracks = []) {
      this.tracks = [...tracks];
    }
    getTracks() { return [...this.tracks]; }
    getVideoTracks() { return this.tracks.filter((track) => track.kind === 'video'); }
    getAudioTracks() { return this.tracks.filter((track) => track.kind === 'audio'); }
    addTrack(track) { this.tracks.push(track); }
  }

  class FakePeerConnection {
    constructor() {
      this.connectionState = 'new';
      this.remoteDescription = null;
      this.transceivers = [];
      this.codecPreferences = { video: [], audio: [] };
    }
    addTransceiver(trackOrKind, init) {
      const track = typeof trackOrKind === 'string' ? null : trackOrKind;
      const kind = track ? track.kind : trackOrKind;
      const transceiver = {
        sender: { track },
        receiver: { track: null },
        direction: init?.direction,
        setCodecPreferences: (codecs) => {
          this.codecPreferences[kind] = [...codecs];
        },
      };
      this.transceivers.push(transceiver);
      return transceiver;
    }
    addTrack(track) {
      this.transceivers.push({ sender: { track }, receiver: { track: null } });
      return { track };
    }
    getTransceivers() { return [...this.transceivers]; }
    async createOffer() {
      const names = (kind) => {
        const preferred = this.codecPreferences[kind];
        const codecs = preferred.length ? preferred : capabilities[kind];
        return codecs.map((codec) => codec.mimeType.split('/')[1]).join(',');
      };
      return { type: 'offer', sdp: `v=0\r\na=x-video-codecs:${names('video')}\r\na=x-audio-codecs:${names('audio')}\r\n` };
    }
    async createAnswer() { return { type: 'answer', sdp: 'v=0\r\n' }; }
    async setLocalDescription(description) { this.localDescription = description; }
    async setRemoteDescription(description) {
      this.remoteDescription = description;
    }
    async addIceCandidate() {}
    close() { this.connectionState = 'closed'; }
  }

  class NativePeerConnection extends FakePeerConnection {
    constructor(config) {
      super(config);
      nativePeers.push(this);
    }
  }

  class WrappedPeerConnection extends FakePeerConnection {
    constructor(config) {
      super(config);
      wrappedPeers.push(this);
    }
  }

  class FakeWebSocket {
    static OPEN = 1;
    constructor(url) {
      this.url = url;
      this.readyState = FakeWebSocket.OPEN;
      this.sent = [];
      sockets.push(this);
    }
    open() { this.onopen?.(); }
    send(payload) { this.sent.push(JSON.parse(payload)); }
    close() { this.readyState = 3; }
  }

  const context = {
    console: { log() {} },
    location: { host: 'partylanapp.com', hostname: 'partylanapp.com' },
    setTimeout,
    clearTimeout,
    MediaStream: FakeMediaStream,
    WebSocket: FakeWebSocket,
    RTCSessionDescription: class {
      constructor(description) { Object.assign(this, description); }
    },
    RTCRtpSender: {
      getCapabilities(kind) { return { codecs: capabilities[kind] || [] }; },
    },
    RTCRtpReceiver: {
      getCapabilities(kind) { return { codecs: capabilities[kind] || [] }; },
    },
    window: {
      RTCPeerConnection: WrappedPeerConnection,
      __PARTYLAN_NATIVE_RTC_PEER_CONNECTION__: options.exposeNativeRTC === false
        ? undefined
        : NativePeerConnection,
      innerWidth: 1920,
      innerHeight: 1080,
      screen: { width: 1920, height: 1080 },
      addEventListener(type, handler) {
        if (type === 'resize') resizeHandlers.push(handler);
      },
    },
  };
  vm.createContext(context);
  vm.runInContext(source, context, { filename: 'sfu-client.js' });
  return {
    client: context.GC_SFU,
    resizeHandlers,
    sockets,
    nativePeers,
    wrappedPeers,
    FakeMediaStream,
  };
}

const liveTrack = (kind, id) => ({ kind, id, readyState: 'live' });

const flushPromises = () => new Promise((resolve) => setImmediate(resolve));

test('production SFU client loads without network side effects and exposes its lifecycle API', () => {
  const { client, resizeHandlers } = loadClient();
  assert.ok(client);
  for (const method of [
    'connect',
    'disconnect',
    'shareScreen',
    'validateMediaCodecs',
    'waitUntilMediaReady',
    'maybeRefreshViewerLayer',
    'isConnected',
    'isMediaReady',
    'getPeerConnection',
    'getRole',
  ]) {
    assert.equal(typeof client[method], 'function', method);
  }
  assert.equal(resizeHandlers.length, 1);
  assert.equal(client.isConnected(), null);
  assert.equal(client.isMediaReady(), false);
});

test('SFU media uses the native RTC constructor saved before injection patches', async () => {
  const env = loadClient();
  const stream = new env.FakeMediaStream([liveTrack('video', 'video-1'), liveTrack('audio', 'audio-1')]);
  env.client.shareScreen(stream);
  env.client.connect('room-native', 'host', {});
  env.sockets[0].open();
  await flushPromises();

  assert.equal(env.nativePeers.length, 1);
  assert.equal(env.wrappedPeers.length, 0);
});

test('host offer is restricted to H264 video and Opus stereo audio', async () => {
  const env = loadClient();
  const stream = new env.FakeMediaStream([liveTrack('video', 'video-1'), liveTrack('audio', 'audio-1')]);
  env.client.shareScreen(stream);
  env.client.connect('room-codecs', 'host', {});
  env.sockets[0].open();
  await flushPromises();

  const peer = env.nativePeers[0];
  assert.deepEqual(peer.codecPreferences.video.map((codec) => codec.mimeType), ['video/H264']);
  assert.deepEqual(peer.codecPreferences.audio.map((codec) => codec.mimeType), ['audio/opus']);
  const offer = env.sockets[0].sent.find((message) => message.type === 'offer');
  assert.match(offer.sdp, /x-video-codecs:H264/);
  assert.match(offer.sdp, /x-audio-codecs:opus/);
  assert.doesNotMatch(offer.sdp, /VP8|PCMU/);
});

test('media readiness resolves only after the host peer connection is connected', async () => {
  const env = loadClient();
  const stream = new env.FakeMediaStream([liveTrack('video', 'video-1'), liveTrack('audio', 'audio-1')]);
  env.client.shareScreen(stream);
  env.client.connect('room-ready', 'host', {});
  const ready = env.client.waitUntilMediaReady(1000);
  env.sockets[0].open();
  await flushPromises();

  const peer = env.nativePeers[0];
  let settled = false;
  ready.then(() => { settled = true; });
  await flushPromises();
  assert.equal(settled, false);

  peer.connectionState = 'connected';
  peer.onconnectionstatechange();
  assert.equal(await ready, peer);
  assert.equal(env.client.isMediaReady(), true);
});

test('sharing fails with codec_unavailable before signaling when H264 or Opus is missing', () => {
  for (const capabilities of [
    {
      video: [{ mimeType: 'video/VP8', clockRate: 90000 }],
      audio: [{ mimeType: 'audio/opus', clockRate: 48000, channels: 2 }],
    },
    {
      video: [{ mimeType: 'video/H264', clockRate: 90000 }],
      audio: [{ mimeType: 'audio/PCMU', clockRate: 8000, channels: 1 }],
    },
  ]) {
    const env = loadClient({ capabilities });
    const stream = new env.FakeMediaStream([liveTrack('video', 'video-1')]);
    assert.throws(
      () => env.client.shareScreen(stream),
      (error) => error?.code === 'codec_unavailable',
    );
    assert.equal(env.sockets.length, 0);
  }
});

test('SFU reconnects are bounded and server errors are reduced to stable codes', () => {
  assert.match(source, /MAX_RECONNECT_ATTEMPTS\s*=\s*5/);
  assert.match(source, /reconnectAttempts\s*>=\s*MAX_RECONNECT_ATTEMPTS/);
  assert.match(source, /setStatus\('错误: sfu_unavailable'\)/);
  assert.doesNotMatch(source, /(?:log|setStatus)\('错误: '\s*\+\s*msg\.error/);
});
