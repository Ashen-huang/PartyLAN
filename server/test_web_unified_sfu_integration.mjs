import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const serverDir = path.dirname(fileURLToPath(import.meta.url));
const injectPath = path.join(serverDir, 'screego', 'deploy', 'inject.js');
const injectSource = fs.readFileSync(injectPath, 'utf8').replace(/\r\n/g, '\n');
const sfuClientSource = fs.readFileSync(path.join(serverDir, 'screego', 'deploy', 'sfu-client.js'), 'utf8');
const nginxSource = fs.readFileSync(path.join(serverDir, 'screego', 'deploy', 'nginx-screego.conf'), 'utf8');

class FakeElement {
  constructor(tagName = 'div') {
    this.tagName = tagName.toUpperCase();
    this.children = [];
    this.dataset = {};
    this.style = {};
    this.attributes = new Map();
    this.parentNode = null;
    this.isConnected = true;
    this._textContent = '';
  }

  get textContent() {
    return this._textContent + this.children.map((child) => child.textContent).join('');
  }

  set textContent(value) {
    this._textContent = String(value || '');
    this.children = [];
  }

  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  replaceChildren(...children) {
    this.children.forEach((child) => { child.parentNode = null; });
    this.children = [];
    this._textContent = '';
    children.forEach((child) => this.appendChild(child));
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
    if (name === 'data-gc-presence-member') this.dataset.gcPresenceMember = String(value);
  }

  getAttribute(name) {
    return this.attributes.get(name) || null;
  }

  removeAttribute(name) {
    this.attributes.delete(name);
  }

  remove() {
    if (!this.parentNode) return;
    this.parentNode.children = this.parentNode.children.filter((child) => child !== this);
    this.parentNode = null;
  }

  closest() {
    return null;
  }

  querySelector() {
    return null;
  }

  querySelectorAll() {
    return [];
  }
}

function createDocument() {
  let anchor = new FakeElement('div');
  const legacyRoster = new FakeElement('div');
  const body = new FakeElement('body');

  return {
    body,
    createElement(tagName) {
      return new FakeElement(tagName);
    },
    getElementById(id) {
      return id === 'partylan-presence-roster' ? anchor : null;
    },
    querySelectorAll(selector) {
      if (selector === '[data-gc-presence-member="1"]') {
        return anchor.children.filter((child) => child.dataset.gcPresenceMember === '1');
      }
      if (selector === '[data-partylan-legacy-roster]') return [legacyRoster];
      return [];
    },
    replaceAnchor() {
      anchor.isConnected = false;
      anchor = new FakeElement('div');
      return anchor;
    },
    get anchor() {
      return anchor;
    },
    legacyRoster,
  };
}

function jsonResponse(data, ok = true) {
  return {
    ok,
    status: ok ? 200 : 400,
    json: async () => data,
  };
}

function loadHarness(fetchImpl = async () => jsonResponse({ ok: false })) {
  const marker = '  init();\n})();';
  assert.ok(injectSource.includes(marker), 'inject script test marker is missing');
  const instrumented = injectSource.replace(
    marker,
    [
      '  globalThis.__gcUnified = {',
      '    setAuth: function (user) { currentUser = user; lastKnownAuthUser = user; lastKnownLoggedIn = true; },',
      '    setRoomState: function (state) { lastRoomState = state; },',
      '    setPresenceMembers: function (participants) { webPresenceMembers = mergeWebPresence(participants); },',
      '    currentSfuMode: currentSfuMode,',
      '    shouldDropNativePeerMessage: shouldDropNativePeerMessage,',
      '    renderWebPresenceMembers: renderWebPresenceMembers,',
      '    ensureWebPresence: ensureWebPresence,',
      '    leaveWebPresence: leaveWebPresence,',
      '    fetchRoomState: fetchRoomState,',
      '    syncViewerSfuState: syncViewerSfuState,',
      '    claimWebShare: claimWebShare,',
      '    heartbeatWebShare: heartbeatWebShare,',
      '    releaseWebShare: releaseWebShare,',
      '    requestDisplayMediaWithCompatibilityFallback: requestDisplayMediaWithCompatibilityFallback,',
      '    buildCompatibilityDisplayMediaRequest: buildCompatibilityDisplayMediaRequest,',
      '    finishShareSetupCapture: finishShareSetupCapture',
      '  };',
      '})();',
    ].join('\n'),
  );

  class NativePeerConnection {}
  class FakeWebSocket {
    static CONNECTING = 0;
    static OPEN = 1;
    static CLOSING = 2;
    static CLOSED = 3;
  }

  const storage = new Map([['gcWebParticipantId', 'web-self']]);
  const intervals = [];
  const document = createDocument();
  const sfuCalls = [];
  const GC_SFU = {
    connect(room, role) {
      sfuCalls.push({ type: 'connect', room, role });
      this.connected = true;
      this.role = role;
    },
    disconnect() {
      sfuCalls.push({ type: 'disconnect' });
      this.connected = false;
      this.role = '';
    },
    isConnected() {
      return !!this.connected;
    },
    isMediaReady() {
      return !!this.mediaReady;
    },
    getRole() {
      return this.role || '';
    },
    getPeerConnection() {
      return null;
    },
  };

  const context = {
    URL,
    Headers,
    Response,
    GC_SFU,
    console: { log() {}, warn() {}, error() {} },
    clearInterval() {},
    clearTimeout() {},
    document,
    fetch: fetchImpl,
    location: {
      href: 'https://partylanapp.com/?room=room-a',
      host: 'partylanapp.com',
      hostname: 'partylanapp.com',
      origin: 'https://partylanapp.com',
      search: '?room=room-a',
    },
    localStorage: {
      getItem(key) {
        return storage.get('local:' + key) || null;
      },
      setItem(key, value) {
        storage.set('local:' + key, String(value));
      },
    },
    navigator: {
      sendBeacon() {
        return true;
      },
    },
    sessionStorage: {
      getItem(key) {
        return storage.get(key) || null;
      },
      setItem(key, value) {
        storage.set(key, String(value));
      },
    },
    setInterval(callback, delay) {
      intervals.push({ callback, delay });
      return intervals.length;
    },
    setTimeout() {
      return 1;
    },
    window: {
      WebSocket: FakeWebSocket,
      RTCPeerConnection: NativePeerConnection,
      addEventListener() {},
      crypto: { randomUUID: () => 'generated-id' },
    },
  };
  context.globalThis = context;
  vm.runInNewContext(instrumented, context, { filename: injectPath });
  context.__gcUnified.setAuth('web-user');
  return { context, document, intervals, sfuCalls, api: context.__gcUnified, NativePeerConnection };
}

const flushPromises = () => new Promise((resolve) => setImmediate(resolve));

test('inject preserves the native RTC constructor for the SFU before wrapping Screego peers', () => {
  const harness = loadHarness();
  assert.equal(
    harness.context.window.__PARTYLAN_NATIVE_RTC_PEER_CONNECTION__,
    harness.NativePeerConnection,
  );
  assert.match(sfuClientSource, /window\.__PARTYLAN_NATIVE_RTC_PEER_CONNECTION__\s*\|\|/);
});

test('explicit unified room contract immediately suppresses Screego P2P messages', async () => {
  const modeDecision = injectSource.slice(
    injectSource.indexOf('function currentSfuMode('),
    injectSource.indexOf('function roomStateSfuActive('),
  );
  assert.doesNotMatch(modeDecision, /origin|viewerIntent|participant|autoSfu/);
  assert.doesNotMatch(injectSource, /gc-sfu-toggle|允许多人时自动启用 SFU|fetchViewerIntent\(/);

  const states = [
    {
      ok: true,
      state: {
        room: 'room-a', active: true, origin: 'web', mediaMode: 'sfu', mediaProtocolVersion: 1,
        sharing: true, sharePhase: 'starting', sfu: false,
      },
    },
    {
      ok: true,
      state: {
        room: 'room-a', active: true, origin: 'client', mediaMode: 'legacy-p2p', mediaProtocolVersion: 1,
        sharing: true, sharePhase: 'live', sfu: false,
      },
    },
  ];
  const harness = loadHarness(async () => jsonResponse(states.shift()));

  await harness.api.fetchRoomState('room-a', true);
  assert.equal(harness.api.currentSfuMode(), true);
  assert.equal(harness.context.window.__PARTYLAN_SFU_ONLY__, true);
  assert.equal(harness.api.shouldDropNativePeerMessage({ data: '{"type":"hostsession"}' }), true);
  assert.equal(harness.api.shouldDropNativePeerMessage({ data: '{"type":"clientsession"}' }), true);

  await harness.api.fetchRoomState('room-a', true);
  assert.equal(harness.api.currentSfuMode(), false);
  assert.equal(harness.context.window.__PARTYLAN_SFU_ONLY__, false);
  assert.equal(harness.api.shouldDropNativePeerMessage({ data: '{"type":"hostsession"}' }), false);
});

test('web Presence advertises the unified contract and owns the stable complete roster', async () => {
  const requests = [];
  const room = {
    room: 'room-a', active: true, mediaMode: 'sfu', mediaProtocolVersion: 1,
    sharing: true, sharePhase: 'live', sfu: true, shareOwnerParticipantId: 'desktop',
  };
  const harness = loadHarness(async (url, options = {}) => {
    requests.push({ url: String(url), options });
    if (String(url).startsWith('/api/room-state?')) return jsonResponse({ ok: true, state: room });
    if (url === '/api/presence/join') {
      return jsonResponse({ ok: true, participant: { participantId: 'web-self', role: 'member' }, room });
    }
    if (String(url).startsWith('/api/presence?')) {
      return jsonResponse({
        ok: true,
        room,
        participants: [
          { participantId: 'web-self', name: 'Web User', clientType: 'web', role: 'member', active: true },
          { participantId: 'desktop', name: 'Desktop User', clientType: 'client', role: 'owner', active: true, sharing: true },
          { participantId: 'desktop', name: 'Duplicate', clientType: 'client', role: 'member', active: true },
        ],
      });
    }
    return jsonResponse({ ok: true });
  });

  await harness.api.ensureWebPresence('room-a');
  await flushPromises();

  const join = requests.find((request) => request.url === '/api/presence/join');
  assert.ok(join, 'Presence join must be sent');
  const payload = JSON.parse(join.options.body);
  assert.equal(payload.mediaProtocolVersion, 1);
  assert.deepEqual(payload.capabilities, {
    sfuViewer: true,
    sfuHost: true,
    h264: true,
    opus: true,
  });
  assert.ok(harness.intervals.some((timer) => timer.delay === 5000), 'Presence heartbeat must run every 5 seconds');
  assert.ok(harness.intervals.some((timer) => timer.delay === 3000), 'Presence roster must refresh every 3 seconds');

  assert.equal(harness.document.anchor.children.length, 2);
  const rosterText = harness.document.anchor.textContent;
  assert.match(rosterText, /Web User/);
  assert.match(rosterText, /网页端/);
  assert.match(rosterText, /当前/);
  assert.match(rosterText, /Desktop User/);
  assert.match(rosterText, /客户端/);
  assert.match(rosterText, /房主/);
  assert.match(rosterText, /共享中/);
  assert.equal(harness.document.legacyRoster.style.display, 'none');

  harness.document.replaceAnchor();
  harness.api.renderWebPresenceMembers();
  assert.equal(harness.document.anchor.children.length, 2, 'React redraw must not lose the Presence roster');
});

test('web Presence activates unified mode without a room query and clears it on leave', async () => {
  const room = {
    room: 'room-a', active: true, mediaMode: 'sfu', mediaProtocolVersion: 1,
    sharing: false, sharePhase: 'idle', sfu: false,
  };
  const harness = loadHarness(async (url) => {
    const pathname = String(url);
    if (pathname.startsWith('/api/room-state?')) return jsonResponse({ ok: true, state: room });
    if (pathname === '/api/presence/join') {
      return jsonResponse({ ok: true, participant: { participantId: 'web-self', role: 'member' }, room });
    }
    if (pathname.startsWith('/api/presence?')) {
      return jsonResponse({ ok: true, room, participants: [] });
    }
    return jsonResponse({ ok: true });
  });
  harness.context.location.href = 'https://partylanapp.com/';
  harness.context.location.search = '';

  await harness.api.ensureWebPresence('room-a');
  await flushPromises();

  assert.equal(harness.context.window.__PARTYLAN_SFU_ONLY__, true);

  await harness.api.leaveWebPresence(false);
  assert.equal(harness.context.window.__PARTYLAN_SFU_ONLY__, false);
});

test('viewer connects only for a compatible live SFU lease and disconnects on stopping', async () => {
  const harness = loadHarness();
  const base = {
    room: 'room-a', active: true, mediaMode: 'sfu', mediaProtocolVersion: 1,
    sharing: true, shareOwnerParticipantId: 'desktop',
  };

  harness.api.setRoomState({ ...base, sharePhase: 'starting', sfu: true });
  await harness.api.syncViewerSfuState('room-a', false);
  assert.equal(harness.sfuCalls.length, 0);

  harness.api.setRoomState({ ...base, sharePhase: 'live', sfu: true });
  await harness.api.syncViewerSfuState('room-a', false);
  assert.deepEqual(harness.sfuCalls[0], { type: 'connect', room: 'room-a', role: 'viewer' });

  harness.api.setRoomState({ ...base, sharePhase: 'stopping', sfu: false });
  await harness.api.syncViewerSfuState('room-a', false);
  assert.deepEqual(harness.sfuCalls[1], { type: 'disconnect' });

  harness.api.setRoomState({ ...base, mediaMode: 'legacy-p2p', sharePhase: 'live', sfu: true });
  await harness.api.syncViewerSfuState('room-a', false);
  assert.equal(harness.sfuCalls.length, 2);
});

test('web share control writes carry starting, live, stopping and release in order', async () => {
  const writes = [];
  let phase = 'idle';
  const room = () => ({
    room: 'room-a', active: true, mediaMode: 'sfu', mediaProtocolVersion: 1,
    sharing: phase !== 'idle', sharePhase: phase, sfu: phase === 'live',
    shareOwnerParticipantId: phase === 'idle' ? '' : 'web-self',
  });
  const harness = loadHarness(async (url, options = {}) => {
    const pathname = String(url);
    if (options.method === 'POST') writes.push({ pathname, body: JSON.parse(options.body) });
    if (pathname.startsWith('/api/room-state?')) return jsonResponse({ ok: true, state: room() });
    if (pathname === '/api/presence/join') {
      return jsonResponse({ ok: true, participant: { participantId: 'web-self', role: 'member' }, room: room() });
    }
    if (pathname.startsWith('/api/presence?')) {
      return jsonResponse({ ok: true, participants: [], room: room() });
    }
    if (pathname === '/api/share/claim') {
      phase = 'starting';
      return jsonResponse({
        ok: true,
        lease: { room: 'room-a', leaseId: 'lease-1', mediaMode: 'sfu', phase },
        room: room(),
      });
    }
    if (pathname === '/api/share/heartbeat') {
      phase = JSON.parse(options.body).phase;
      return jsonResponse({
        ok: true,
        lease: { room: 'room-a', leaseId: 'lease-1', mediaMode: 'sfu', phase },
        room: room(),
      });
    }
    if (pathname === '/api/share/release') {
      phase = 'idle';
      return jsonResponse({ ok: true, lease: { active: false }, room: room() });
    }
    return jsonResponse({ ok: true });
  });

  await harness.api.claimWebShare();
  await harness.api.heartbeatWebShare('live');
  await harness.api.heartbeatWebShare('stopping');
  await harness.api.releaseWebShare(false);

  const shareWrites = writes.filter((request) => request.pathname.startsWith('/api/share/'));
  assert.deepEqual(shareWrites.map((request) => request.pathname), [
    '/api/share/claim',
    '/api/share/heartbeat',
    '/api/share/heartbeat',
    '/api/share/release',
  ]);
  assert.equal(shareWrites[0].body.mediaMode, 'sfu');
  assert.equal(shareWrites[0].body.phase, 'starting');
  assert.equal(shareWrites[1].body.phase, 'live');
  assert.equal(shareWrites[2].body.phase, 'stopping');
});

test('web host claim, readiness and normal stop follow the four-phase lease contract', () => {
  const claim = injectSource.slice(
    injectSource.indexOf('function claimWebShare('),
    injectSource.indexOf('function releaseWebShare('),
  );
  assert.match(claim, /request\.mediaMode\s*=\s*'sfu'/);
  assert.match(claim, /request\.phase\s*=\s*'starting'/);

  const heartbeat = injectSource.slice(
    injectSource.indexOf('function heartbeatWebShare('),
    injectSource.indexOf('function startWebShareHeartbeat('),
  );
  assert.match(heartbeat, /phase:\s*phase/);

  assert.match(sfuClientSource, /waitUntilMediaReady:/);
  const activation = injectSource.slice(
    injectSource.indexOf('function activateCapturedDisplayStream('),
    injectSource.indexOf('function primeDisplayMediaRequestForShare('),
  );
  const validateCodecs = activation.indexOf('GC_SFU.validateMediaCodecs()');
  const publishCapturedStream = activation.indexOf('activeDisplayStream = stream');
  assert.ok(validateCodecs >= 0 && publishCapturedStream > validateCodecs);
  const hostConnect = injectSource.slice(
    injectSource.indexOf('function sfuConnect('),
    injectSource.indexOf('function sfuDisconnect('),
  );
  assert.match(hostConnect, /waitUntilMediaReady\(/);
  assert.match(hostConnect, /heartbeatWebShare\('live'\)/);

  const stop = injectSource.slice(
    injectSource.indexOf('function stopWebShare('),
    injectSource.indexOf('function syncShareButtonAvailability('),
  );
  const stopping = stop.indexOf("heartbeatWebShare('stopping')");
  const close = stop.indexOf('sfuDisconnect()');
  const release = stop.indexOf('releaseWebShare(');
  assert.ok(stopping >= 0 && close > stopping && release > close);
});

test('display capture has a compatibility request and retries only recoverable picker errors', () => {
  const capture = injectSource.slice(
    injectSource.indexOf('function buildDisplayMediaRequest('),
    injectSource.indexOf('function showWebAudioCaptureNotice('),
  );
  assert.match(capture, /function buildCompatibilityDisplayMediaRequest\(/);
  assert.match(capture, /name === 'TypeError'[\s\S]{0,180}name === 'OverconstrainedError'[\s\S]{0,180}name === 'NotReadableError'/);
  assert.doesNotMatch(capture, /name === 'NotAllowedError'|name === 'AbortError'|name === 'NotFoundError'|name === 'InvalidStateError'/);
  assert.match(capture, /compatibility/i);
});

test('web sharing keeps 720p30 as the default without overwriting saved settings', () => {
  const defaults = injectSource.slice(
    injectSource.indexOf('function ensureDefaultShareSettings('),
    injectSource.indexOf('function normalizeResolutionKey('),
  );
  assert.match(defaults, /s\.resolution = '1280x720'/);
  assert.match(defaults, /s\.framerate = 30/);
  assert.match(defaults, /if \(!s\.resolution\)/);
  assert.match(defaults, /if \(!s\.framerate\)/);
});

test('display capture fallback drops browser-specific constraints once and preserves audio intent', async () => {
  const harness = loadHarness();
  const calls = [];
  const stream = { id: 'compat-stream' };
  const request = (constraints) => {
    calls.push(constraints);
    if (calls.length === 1) {
      return Promise.reject(Object.assign(new Error('window constraints rejected'), { name: 'NotReadableError' }));
    }
    return Promise.resolve(stream);
  };

  const result = await harness.api.requestDisplayMediaWithCompatibilityFallback(
    { video: { width: { ideal: 1280 } }, audio: true, windowAudio: 'window' },
    { audio: 'app' },
    request,
  );
  assert.equal(result, stream);
  assert.equal(calls.length, 2);
  assert.equal(calls[1].video, true);
  assert.equal(calls[1].audio, true);
  assert.equal(Object.keys(calls[1]).length, 2);

  calls.length = 0;
  await assert.rejects(
    harness.api.requestDisplayMediaWithCompatibilityFallback(
      { video: {}, audio: true },
      { audio: 'app' },
      () => {
        calls.push({});
        return Promise.reject(Object.assign(new Error('cancelled'), { name: 'NotAllowedError' }));
      },
    ),
    { name: 'NotAllowedError' },
  );
  assert.equal(calls.length, 1);
});

test('display capture fallback also catches a synchronous browser TypeError', async () => {
  const harness = loadHarness();
  let attempts = 0;
  const result = await harness.api.requestDisplayMediaWithCompatibilityFallback(
    { video: {}, audio: false },
    { audio: 'off' },
    () => {
      attempts += 1;
      if (attempts === 1) throw Object.assign(new Error('invalid display options'), { name: 'TypeError' });
      return Promise.resolve({ id: 'sync-compatible-stream' });
    },
  );
  assert.equal(result.id, 'sync-compatible-stream');
  assert.equal(attempts, 2);
});

test('share setup waits for the capture promise before clicking the current start button', () => {
  const setup = injectSource.slice(
    injectSource.indexOf('function createShareSetupModal('),
    injectSource.indexOf('function collectShareStartButtons('),
  );
  assert.match(setup, /finishShareSetupCapture\(capturePromise, findShareStartButton/);
  assert.doesNotMatch(
    setup,
    /primeDisplayMediaRequestForShare\(\);[\s\S]{0,260}closeShareSetupModal\(true\);[\s\S]{0,120}btn\.click\(\)/,
  );
});

test('share setup completion does not click until capture resolves', async () => {
  const harness = loadHarness();
  let resolveCapture;
  const capturePromise = new Promise((resolve) => { resolveCapture = resolve; });
  const events = [];
  const button = { isConnected: true };

  const completion = harness.api.finishShareSetupCapture(
    capturePromise,
    () => button,
    (currentButton) => {
      events.push('ready');
      currentButton.clicked = true;
    },
    () => events.push('error'),
  );
  assert.deepEqual(events, []);
  assert.equal(button.clicked, undefined);
  resolveCapture({ id: 'stream' });
  await completion;
  assert.deepEqual(events, ['ready']);
  assert.equal(button.clicked, true);
});

test('web hover tools stay outside the viewing layout and preserve diagnostics', () => {
  assert.match(injectSource, /root\.id = 'gc-hover-tools'/);
  assert.match(injectSource, /id=\"gc-web-presence-roster\"/);
  assert.match(injectSource, /id=\"gc-hover-share-start\"/);
  assert.match(injectSource, /function syncWebHoverToolsVisibility\(\)/);
  assert.match(injectSource, /function destroyWebHoverTools\(\)/);
  assert.match(injectSource, /position:fixed; inset:0; z-index:9990/);
  assert.match(injectSource, /statsOverlayId = 'gc-stats-overlay'/);
  assert.match(injectSource, /matchMedia\('\(hover: none\), \(pointer: coarse\)'\)/);
  assert.match(injectSource, /justify-content:flex-end;padding:18px 24px/);
});

test('versioned assets load inject before the SFU client without exposing sensitive material', () => {
  const replacement = nginxSource.match(/sub_filter '<\/head>' '([^']+)<\/head>'/);
  assert.ok(replacement);
  assert.ok(replacement[1].indexOf('/inject.js?v=6.30') >= 0);
  assert.ok(replacement[1].indexOf('/sfu-client.js?v=1.9') >= 0);
  assert.ok(replacement[1].indexOf('/inject.js') < replacement[1].indexOf('/sfu-client.js'));

  const lowered = injectSource.toLowerCase();
  assert.doesNotMatch(lowered, /console\.(?:log|warn|error)\([^\n]*(?:authorization|token|sdp|candidate)/);
});
