import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const injectPath = path.join(repoRoot, 'server', 'screego', 'deploy', 'inject.js');

class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  constructor(url, protocols) {
    this.url = url;
    this.protocols = protocols;
    this.readyState = FakeWebSocket.CONNECTING;
    this.closeCalls = [];
    this.listeners = new Map();
  }

  addEventListener(type, listener) {
    const listeners = this.listeners.get(type) || [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  close(code = 1000, reason = '') {
    this.closeCalls.push({ code, reason });
    this.readyState = FakeWebSocket.CLOSED;
    this.emit('close', { code, reason, wasClean: code === 1000 });
  }

  emit(type, event) {
    for (const listener of this.listeners.get(type) || []) {
      listener.call(this, event);
    }
    const handler = this['_' + type];
    if (typeof handler === 'function') {
      handler.call(this, event);
    }
  }
}

for (const property of ['onmessage', 'onclose', 'onerror']) {
  Object.defineProperty(FakeWebSocket.prototype, property, {
    configurable: true,
    enumerable: true,
    get() {
      return this['_' + property.slice(2)] || null;
    },
    set(handler) {
      this['_' + property.slice(2)] = handler;
    },
  });
}

function loadLifecycleHarness() {
  const source = fs.readFileSync(injectPath, 'utf8').replace(/\r\n/g, '\n');
  const marker = '  init();\n})();';
  assert.ok(source.includes(marker), 'inject script test marker is missing');
  const instrumented = source.replace(
    marker,
    [
      '  globalThis.__gcLifecycle = {',
      '    patchWebSocket: patchWebSocket,',
      '    activeRoomSocket: function () { return activeRoomSocket; }',
      '  };',
      '})();',
    ].join('\n'),
  );

  const storage = new Map();
  const context = {
    URL,
    Headers,
    Response,
    console: { log() {}, warn() {}, error() {} },
    clearInterval() {},
    clearTimeout() {},
    document: {
      getElementById() {
        return null;
      },
      querySelectorAll() {
        return [];
      },
    },
    fetch() {
      return new Promise(() => undefined);
    },
    location: {
      href: 'https://gamecast.test/',
      host: 'gamecast.test',
      hostname: 'gamecast.test',
      origin: 'https://gamecast.test',
      search: '',
    },
    localStorage: {
      getItem(key) {
        return storage.get(key) || null;
      },
      setItem(key, value) {
        storage.set(key, String(value));
      },
    },
    navigator: {},
    sessionStorage: {
      getItem(key) {
        return storage.get('session:' + key) || null;
      },
      setItem(key, value) {
        storage.set('session:' + key, String(value));
      },
    },
    setInterval() {
      return 1;
    },
    setTimeout() {
      return 1;
    },
    window: {
      WebSocket: FakeWebSocket,
    },
  };
  context.globalThis = context;
  vm.runInNewContext(instrumented, context, { filename: injectPath });
  context.__gcLifecycle.patchWebSocket();
  return {
    WebSocket: context.window.WebSocket,
    activeRoomSocket: context.__gcLifecycle.activeRoomSocket,
  };
}

test('pending room sockets do not replace or close each other before the room handshake', () => {
  const harness = loadLifecycleHarness();
  const first = new harness.WebSocket('wss://gamecast.test/stream');
  first.onmessage = () => undefined;
  const second = new harness.WebSocket('wss://gamecast.test/stream');
  second.onmessage = () => undefined;

  assert.equal(harness.activeRoomSocket(), null);
  assert.equal(first.closeCalls.length, 0);
  assert.equal(second.closeCalls.length, 0);
});

test('a room message promotes the new socket before retiring the previous active socket', () => {
  const harness = loadLifecycleHarness();
  const first = new harness.WebSocket('wss://gamecast.test/stream');
  first.onmessage = () => undefined;
  first.emit('message', { data: JSON.stringify({ type: 'room', payload: { id: 'first-room' } }) });

  assert.equal(harness.activeRoomSocket(), first);

  const second = new harness.WebSocket('wss://gamecast.test/stream');
  second.onmessage = () => undefined;
  assert.equal(harness.activeRoomSocket(), first);
  assert.equal(first.closeCalls.length, 0);

  second.emit('message', { data: JSON.stringify({ type: 'room', payload: { id: 'second-room' } }) });

  assert.equal(harness.activeRoomSocket(), second);
  assert.deepEqual(first.closeCalls, [{ code: 1000, reason: '' }]);

  first.emit('close', { code: 1000, reason: '', wasClean: true });
  assert.equal(harness.activeRoomSocket(), second);
});

test('non-stream sockets remain transparent to the room lifecycle', () => {
  const harness = loadLifecycleHarness();
  const socket = new harness.WebSocket('wss://gamecast.test/sfu-ws');

  assert.equal(socket instanceof FakeWebSocket, true);
  assert.equal(harness.activeRoomSocket(), null);
  assert.equal(socket.closeCalls.length, 0);
});
