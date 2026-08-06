import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const serverDir = path.dirname(fileURLToPath(import.meta.url));
const source = fs.readFileSync(path.join(serverDir, 'screego', 'deploy', 'sfu-client.js'), 'utf8');

function loadClient() {
  const resizeHandlers = [];
  const context = {
    console: { log() {} },
    location: { host: 'partylanapp.com', hostname: 'partylanapp.com' },
    setTimeout,
    clearTimeout,
    MediaStream: class {
      getTracks() { return []; }
      addTrack() {}
    },
    window: {
      RTCPeerConnection: class {},
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
  return { client: context.GC_SFU, resizeHandlers };
}

test('production SFU client loads without network side effects and exposes its lifecycle API', () => {
  const { client, resizeHandlers } = loadClient();
  assert.ok(client);
  for (const method of [
    'connect',
    'disconnect',
    'shareScreen',
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
