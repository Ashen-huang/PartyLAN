import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import test from 'node:test';

const require = createRequire(import.meta.url);
const {
  POLICY_VERSION,
  buildCapturePreferences,
  sanitizeCapturedStream,
} = require('./web_share_audio_policy.js');

function fakeTrack(kind, settings = {}) {
  return {
    kind,
    stopped: false,
    getSettings: () => ({...settings}),
    stop() {
      this.stopped = true;
    },
  };
}

function fakeStream(surface, audioCount = 1) {
  const video = fakeTrack('video', surface ? {displaySurface: surface} : {});
  const audio = Array.from({length: audioCount}, () => fakeTrack('audio'));
  const tracks = [video, ...audio];
  return {
    video,
    audio,
    getTracks: () => tracks.slice(),
    getVideoTracks: () => [video],
    getAudioTracks: () => audio.slice(),
    removeTrack(track) {
      const index = tracks.indexOf(track);
      if (index >= 0) tracks.splice(index, 1);
    },
  };
}

function makeVideoOnlyStream(tracks) {
  return {
    tracks: tracks.slice(),
    getTracks: () => tracks.slice(),
    getVideoTracks: () => tracks.filter((track) => track.kind === 'video'),
    getAudioTracks: () => tracks.filter((track) => track.kind === 'audio'),
  };
}

test('capture preferences exclude system and window audio for tab mode', () => {
  assert.deepEqual(buildCapturePreferences('app'), {
    requestAudio: true,
    systemAudio: 'exclude',
    windowAudio: 'exclude',
    selfBrowserSurface: 'exclude',
    surfaceSwitching: 'include',
  });
});

test('capture preferences expose system audio only for full-screen mode', () => {
  assert.deepEqual(buildCapturePreferences('system'), {
    requestAudio: true,
    systemAudio: 'include',
    windowAudio: 'exclude',
    surfaceSwitching: 'include',
  });
  assert.deepEqual(buildCapturePreferences('off'), {requestAudio: false});
});

test('tab mode retains audio only for a browser surface', () => {
  const stream = fakeStream('browser');
  const result = sanitizeCapturedStream(stream, 'app', makeVideoOnlyStream);
  assert.equal(result.policyVersion, POLICY_VERSION);
  assert.equal(result.stream, stream);
  assert.equal(result.effectiveMode, 'tab');
  assert.equal(result.removedAudio, false);
  assert.equal(result.shouldNotify, false);
  assert.equal(stream.audio[0].stopped, false);
});

test('full-screen mode retains audio only for a monitor surface', () => {
  const stream = fakeStream('monitor');
  const result = sanitizeCapturedStream(stream, 'system', makeVideoOnlyStream);
  assert.equal(result.stream, stream);
  assert.equal(result.effectiveMode, 'system');
  assert.equal(result.removedAudio, false);
  assert.equal(result.shouldNotify, false);
});

for (const [mode, surface] of [
  ['app', 'window'],
  ['app', 'monitor'],
  ['app', ''],
  ['system', 'window'],
  ['system', 'browser'],
  ['system', ''],
]) {
  test(`${mode} mode strips audio from ${surface || 'unknown'} surfaces`, () => {
    const stream = fakeStream(surface);
    const result = sanitizeCapturedStream(stream, mode, makeVideoOnlyStream);
    assert.notEqual(result.stream, stream);
    assert.equal(result.stream.getAudioTracks().length, 0);
    assert.equal(result.effectiveMode, 'off');
    assert.equal(result.removedAudio, true);
    assert.equal(result.shouldNotify, true);
    assert.equal(stream.audio[0].stopped, true);
  });
}

test('off mode strips an unexpected audio track without a safety notice', () => {
  const stream = fakeStream('monitor');
  const result = sanitizeCapturedStream(stream, 'off', makeVideoOnlyStream);
  assert.equal(result.stream.getAudioTracks().length, 0);
  assert.equal(result.effectiveMode, 'off');
  assert.equal(result.removedAudio, true);
  assert.equal(result.shouldNotify, false);
  assert.equal(stream.audio[0].stopped, true);
});

test('an allowed surface without an audio track reports off', () => {
  const stream = fakeStream('browser', 0);
  const result = sanitizeCapturedStream(stream, 'app', makeVideoOnlyStream);
  assert.equal(result.stream, stream);
  assert.equal(result.effectiveMode, 'off');
  assert.equal(result.removedAudio, false);
  assert.equal(result.shouldNotify, false);
});
