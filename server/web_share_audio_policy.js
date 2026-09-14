(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.GameCastWebShareAudioPolicy = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  var POLICY_VERSION = 'web-share-audio-isolation-v2';

  function normalizeMode(mode) {
    if (mode === 'app' || mode === 'system') return mode;
    return 'off';
  }

  function buildCapturePreferences(mode) {
    mode = normalizeMode(mode);
    if (mode === 'off') return {requestAudio: false};
    if (mode === 'system') {
      return {
        requestAudio: true,
        systemAudio: 'include',
        windowAudio: 'exclude',
        surfaceSwitching: 'include'
      };
    }
    return {
      requestAudio: true,
      systemAudio: 'exclude',
      windowAudio: 'window',
      selfBrowserSurface: 'exclude',
      surfaceSwitching: 'include'
    };
  }

  function getDisplaySurface(stream) {
    if (!stream || typeof stream.getVideoTracks !== 'function') return '';
    var videoTracks = stream.getVideoTracks();
    var videoTrack = videoTracks && videoTracks[0];
    if (!videoTrack || typeof videoTrack.getSettings !== 'function') return '';
    var settings = videoTrack.getSettings() || {};
    return typeof settings.displaySurface === 'string'
      ? settings.displaySurface.toLowerCase()
      : '';
  }

  function makeVideoOnlyStream(stream, createStream) {
    var videoTracks = typeof stream.getVideoTracks === 'function'
      ? stream.getVideoTracks().slice()
      : [];
    if (typeof createStream === 'function') return createStream(videoTracks);
    if (typeof MediaStream === 'function') return new MediaStream(videoTracks);
    throw new Error('MediaStream is unavailable for audio-safe display capture');
  }

  function sanitizeCapturedStream(stream, requestedMode, createStream) {
    var mode = normalizeMode(requestedMode);
    var surface = getDisplaySurface(stream);
    var audioTracks = stream && typeof stream.getAudioTracks === 'function'
      ? stream.getAudioTracks().slice()
      : [];
    var allowedSurface =
      (mode === 'app' && (surface === 'browser' || surface === 'window')) ||
      (mode === 'system' && surface === 'monitor');
    var effectiveMode = allowedSurface && audioTracks.length > 0
      ? mode
      : 'off';
    var shouldNotify = mode !== 'off' && effectiveMode === 'off';
    var noticeReason = shouldNotify
      ? (allowedSurface ? 'audio-unavailable' : 'source-mismatch')
      : '';

    if (!allowedSurface || mode === 'off') {
      audioTracks.forEach(function (track) {
        try { track.stop(); } catch (error) {}
        if (stream && typeof stream.removeTrack === 'function') {
          try { stream.removeTrack(track); } catch (error2) {}
        }
      });
      if (audioTracks.length > 0) stream = makeVideoOnlyStream(stream, createStream);
    }

    return {
      policyVersion: POLICY_VERSION,
      stream: stream,
      requestedMode: mode,
      effectiveMode: effectiveMode,
      displaySurface: surface,
      removedAudio: audioTracks.length > 0 && effectiveMode === 'off',
      shouldNotify: shouldNotify,
      noticeReason: noticeReason
    };
  }

  return {
    POLICY_VERSION: POLICY_VERSION,
    buildCapturePreferences: buildCapturePreferences,
    sanitizeCapturedStream: sanitizeCapturedStream
  };
});
