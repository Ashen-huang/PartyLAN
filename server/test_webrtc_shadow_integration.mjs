import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

const inject = await readFile(
  new URL('./screego/deploy/inject.js', import.meta.url),
  'utf8',
);
const nginx = await readFile(
  new URL('./screego/deploy/nginx-screego.conf', import.meta.url),
  'utf8',
);

test('non-primary peer sampling never writes through a missing overlay', () => {
  assert.doesNotMatch(
    inject,
    /if \(pc\._gcStatsFetchPromise\) \{\s*overlay\.textContent/,
  );
});

test('protocol-specific labels are applied after the legacy stats loop', () => {
  const updateStats = inject.slice(inject.indexOf('function updateStats('));
  const loop = updateStats.indexOf('stats.forEach(function (report)');
  const turnProtocol = updateStats.indexOf("connType = 'TURN 中继/'");
  assert.ok(loop >= 0 && turnProtocol > loop);
});

test('telemetry helpers load before inject and shadow mode contains no recovery calls', () => {
  const replacement = nginx.match(/sub_filter '<\/head>' '([^']+)<\/head>'/);
  assert.ok(replacement);
  assert.ok(replacement[1].indexOf('/webrtc-stats.js') < replacement[1].indexOf('/inject.js'));
  assert.ok(replacement[1].indexOf('/webrtc-quality.js') < replacement[1].indexOf('/inject.js'));
  assert.ok(replacement[1].indexOf('/inject.js') < replacement[1].indexOf('/sfu-client.js'));
  assert.doesNotMatch(inject, /restartIce\(|iceRestart:\s*true|restartsession/);
});
