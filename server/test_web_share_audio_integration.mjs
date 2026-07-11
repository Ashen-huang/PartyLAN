import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

const inject = await readFile(new URL('./screego/deploy/inject.js', import.meta.url), 'utf8');
const nginx = await readFile(new URL('./screego/deploy/nginx-screego.conf', import.meta.url), 'utf8');

test('web share audio policy loads before inject', () => {
  const replacement = nginx.match(/sub_filter '<\/head>' '([^']+)<\/head>'/);
  assert.ok(replacement);
  const policyIndex = replacement[1].indexOf('/web-share-audio.js');
  const injectIndex = replacement[1].indexOf('/inject.js');
  assert.ok(policyIndex >= 0);
  assert.ok(injectIndex >= 0 && policyIndex < injectIndex);
});

test('display capture preferences come from the strict audio policy', () => {
  assert.match(inject, /GameCastWebShareAudioPolicy\.buildCapturePreferences\(mode\)/);
  assert.match(inject, /windowAudio:\s*preferences\.windowAudio/);
  assert.doesNotMatch(inject, /windowAudio:\s*'system'/);
  assert.doesNotMatch(inject, /windowAudio:\s*'window'/);
});

test('every captured stream is sanitized before it becomes active', () => {
  const activation = inject.slice(
    inject.indexOf('function activateCapturedDisplayStream('),
    inject.indexOf('function primeDisplayMediaRequestForShare('),
  );
  const sanitizeIndex = activation.indexOf('sanitizeCapturedStream(');
  const activeIndex = activation.indexOf('activeDisplayStream =');
  assert.ok(sanitizeIndex >= 0);
  assert.ok(activeIndex > sanitizeIndex);
  assert.match(activation, /stream\s*=\s*audioResult\.stream/);
  assert.match(activation, /activeCaptureAudioMode\s*=\s*audioResult\.effectiveMode/);
});

test('room state and share heartbeat report the effective audio mode', () => {
  const heartbeat = inject.slice(
    inject.indexOf('function heartbeatWebShare()'),
    inject.indexOf('function startWebShareHeartbeat()'),
  );
  const roomState = inject.slice(
    inject.indexOf('function postRoomState('),
    inject.indexOf('function startRoomStateHeartbeat()'),
  );
  assert.match(inject, /function effectiveWebShareAudioMode\(\)/);
  assert.match(heartbeat, /audioMode:\s*effectiveWebShareAudioMode\(\)/);
  assert.doesNotMatch(heartbeat, /audioMode:\s*extCfg\(\)\.audio/);
  assert.match(roomState, /effectiveWebShareAudioMode\(\)/);
  assert.doesNotMatch(roomState, /audioMode:\s*extCfg\(\)\.audio/);
  assert.equal((roomState.match(/audioMode:\s*active \? effectiveWebShareAudioMode\(\) : 'off'/g) || []).length, 2);
  assert.match(inject, /activeCaptureAudioMode\s*=\s*'off'/);
});

test('blocked window audio produces one non-blocking safety notice', () => {
  assert.match(inject, /function showWebAudioSafetyNotice\(\)/);
  assert.match(inject, /为防止泄露系统声音，窗口共享已自动关闭音频。/);
  assert.match(inject, /if \(audioResult\.shouldNotify\) showWebAudioSafetyNotice\(\)/);
});

test('audio mode labels describe tab and full-screen scope', () => {
  assert.match(inject, /label:\s*'标签页音频 \(推荐\)'/);
  assert.match(inject, /label:\s*'全屏系统音频'/);
});
