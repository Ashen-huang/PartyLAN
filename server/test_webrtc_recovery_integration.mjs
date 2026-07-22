import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const inject = fs.readFileSync(new URL('./screego/deploy/inject.js', import.meta.url), 'utf8');
const register = fs.readFileSync(new URL('./screego/deploy/register.py', import.meta.url), 'utf8');
const nginx = fs.readFileSync(new URL('./screego/deploy/nginx-screego.conf', import.meta.url), 'utf8');

test('viewer quality decisions are routed to the authenticated peer recovery channel', () => {
  assert.match(inject, /function requestPeerIceRecovery\s*\(/);
  assert.match(inject, /qualityState\.action[\s\S]{0,500}requestPeerIceRecovery\s*\(/);
  assert.match(inject, /__gamecastRequestIceRecovery/);
  assert.match(inject, /__gamecastHandleIceRecoveryRequest/);
});

test('host protection profile is capped at 720p and 3 Mbps before recovery', () => {
  assert.match(inject, /protectionMaxWidth:\s*pc\._gcProtectionMaxWidth/);
  assert.match(inject, /protectionMaxHeight:\s*pc\._gcProtectionMaxHeight/);
  assert.match(inject, /protectionMaxBitrate:\s*pc\._gcProtectionMaxBitrate/);
  assert.match(inject, /_gcProtectionMaxWidth\s*=\s*1280/);
  assert.match(inject, /_gcProtectionMaxHeight\s*=\s*720/);
  assert.match(inject, /_gcProtectionMaxBitrate\s*=\s*3000000/);
});

test('telemetry distinguishes estimated relay load from server capacity', () => {
  assert.match(inject, /estimatedRelayLoadKbps:\s*Math\.round\(estimatedRelayLoadKbps\)/);
  assert.doesNotMatch(inject, /estimatedCapacityKbps:\s*Math\.round\(serverLoadKbps\)/);
  assert.match(register, /'estimatedRelayLoadKbps'/);
  assert.match(register, /'protectionMaxBitrate'/);
});

test('deployed resource versions are bumped for recovery behavior', () => {
  assert.match(nginx, /webrtc-quality\.js\?v=2/);
  assert.match(nginx, /webrtc-adaptive\.js\?v=2/);
  assert.match(nginx, /inject\.js\?v=6\.18/);
});
