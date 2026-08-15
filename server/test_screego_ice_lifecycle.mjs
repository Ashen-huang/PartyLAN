import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

const patch = await readFile(
  new URL('./screego/patches/0001-partyLAN-ice-and-fullscreen.patch', import.meta.url),
  'utf8',
);
const metadataPatch = await readFile(
  new URL('./screego/patches/0005-partylan-peer-session-metadata.patch', import.meta.url),
  'utf8',
).catch(() => '');
const source = patch
  .split(/\r?\n/)
  .filter((line) => line.startsWith('+') && !line.startsWith('+++'))
  .map((line) => line.slice(1))
  .join('\n');

test('peer sessions are registered before asynchronous offer or answer work starts', () => {
  assert.doesNotMatch(
    source,
    /hostSession\([\s\S]*?\)\.then\(\(peer\) => \{\s*host\.current\[event\.payload\.id\] = peer;/,
  );
  assert.doesNotMatch(
    source,
    /clientSession\([\s\S]*?\)\.then\(\(peer\) => \(client\.current\[event\.payload\.id\] = peer\)\)/,
  );
});

test('remote ICE is queued through a session coordinator instead of being dropped', () => {
  assert.doesNotMatch(
    source,
    /host\.current\[event\.payload\.sid\]\?\.addIceCandidate\(event\.payload\.value\)/,
  );
  assert.doesNotMatch(
    source,
    /client\.current\[event\.payload\.sid\]\?\.addIceCandidate\(event\.payload\.value\)/,
  );
  assert.match(source, /queueRemoteCandidate/);
});

test('peer sessions expose exact metadata through an optional non-fatal hook', () => {
  assert.match(metadataPatch, /__PARTYLAN_REGISTER_PEER_SESSION__/);
  assert.match(metadataPatch, /pc:\s*session\.pc/);
  assert.match(metadataPatch, /sid:\s*session\.sid/);
  assert.match(metadataPatch, /peerId:\s*session\.peerId/);
  assert.match(metadataPatch, /role:\s*session\.role/);
  assert.match(metadataPatch, /notifyPartyLANPeerSession\(session,\s*'close'\)/);
  assert.match(metadataPatch, /catch/);
});
