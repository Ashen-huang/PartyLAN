import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

const inject = await readFile(new URL('./screego/deploy/inject.js', import.meta.url), 'utf8');
const nginx = await readFile(new URL('./screego/deploy/nginx-screego.conf', import.meta.url), 'utf8');
const sourcePolicy = await readFile(new URL('./web_share_audio_policy.js', import.meta.url), 'utf8');
const deployedPolicy = await readFile(new URL('./screego/deploy/web-share-audio.js', import.meta.url), 'utf8');

test('source and deployed web audio policies are byte-identical', () => {
  assert.equal(deployedPolicy, sourcePolicy);
});

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

test('video-only fallback produces one non-blocking capture result notice', () => {
  assert.match(inject, /function showWebAudioCaptureNotice\(reason\)/);
  assert.match(inject, /未获取到所选来源音频，本次仅共享画面。/);
  assert.match(inject, /所选来源与音频模式不匹配，本次仅共享画面。/);
  assert.match(inject, /if \(audioResult\.shouldNotify\) showWebAudioCaptureNotice\(audioResult\.noticeReason\)/);
});

test('audio mode labels describe window, tab and full-screen scope', () => {
  assert.match(inject, /label:\s*'窗口\/标签页音频 \(推荐\)'/);
  assert.match(inject, /共享所选窗口或浏览器标签页的声音/);
  assert.match(inject, /label:\s*'全屏系统音频'/);
});

test('changed audio assets use new cache versions', () => {
  assert.match(nginx, /web-share-audio\.js\?v=2/);
  assert.match(nginx, /inject\.js\?v=6\.20/);
});
