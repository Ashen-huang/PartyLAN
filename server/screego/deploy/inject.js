(function () {
  'use strict';

  var SETTINGS_KEY = 'screegoSettings';
  var STATS_URL = '/api/stats';
  var QUALITY_REPORT_URL = '/api/quality/report';
  var QUALITY_SNAPSHOT_URL = '/api/quality/snapshot';
  var ROOM_STATE_URL = '/api/room-state';
  var PRESENCE_URL = '/api/presence';
  var SHARE_URL = '/api/share';
  var ROOM_STATE_TTL = 15;
  var ROOM_STATE_HEARTBEAT_MS = 5000;
  var PRESENCE_REFRESH_MS = 3000;
  var SFU_SHARE_START_TIMEOUT_MS = 15000;
  var RECOVERY_STABLE_MS = 20000;
  var RECOVERY_BITRATE_STEPS = [6000000, 8000000];
  var PEER_DISCONNECTED_GRACE_MS = 12000;
  var PEER_ORPHAN_GRACE_MS = 12000;
  var PRESENCE_RECOVERY_MAX_FAILURES = 3;
  var NativeWebSocket = window.WebSocket;
  var NativeRTCPeerConnection = window.RTCPeerConnection;
  if (!window.__PARTYLAN_NATIVE_RTC_PEER_CONNECTION__ && NativeRTCPeerConnection) {
    window.__PARTYLAN_NATIVE_RTC_PEER_CONNECTION__ = NativeRTCPeerConnection;
  }
  var currentUser = null;
  var adaptiveStreamingMonitorTimer = 0;
  var adaptiveServerBudgetPeerCount = 0;
  var adaptiveQualitySnapshot = Object.create(null);
  var adaptiveQualitySnapshotPromise = null;
  var screegoPeerSessions = Object.create(null);
  var sfuConnection = null;
  var sfuStatusText = '未启用';
  var activeRoomSocket = null;
  var activeRoomSocketRoomId = '';
  var roomStateHeartbeatTimer = 0;
  var roomStateHeartbeatRoomId = '';
  var lastRoomStatePostSignature = '';
  var lastRoomStatePostAt = 0;
  var roomStateFetchPromise = null;
  var roomStateFetchRoomId = '';
  var lastRoomState = null;
  var lastKnownAuthUser = '';
  var lastKnownLoggedIn = false;
  var authStateMonitorTimer = 0;
  var nativeGetDisplayMedia = null;
  var primedDisplayMediaRequest = null;
  var primedDisplayMediaRequestSeq = 0;
  var shareSetupBypassButton = null;
  var shareSetupBypassUntil = 0;
  var shareSetupPendingButton = null;
  var shareSetupClaimSeq = 0;
  var shareSetupCapturePromise = null;
  var shareSetupCaptureSeq = 0;
  var webParticipantId = '';
  var webPresenceRoom = '';
  var webPresenceRole = '';
  var webPresenceName = '';
  var webPresenceTimer = 0;
  var webPresenceRefreshTimer = 0;
  var webPresenceMembers = [];
  var webPresenceRecoveryPromise = null;
  var webPresenceRecoveryFailures = 0;
  var webPresenceRecoveryGeneration = 0;
  var webShareLease = null;
  var webShareHeartbeatTimer = 0;
  var webShareSafetyTimer = 0;
  var webShareStopPromise = null;
  var webShareAbortPromise = null;
  var activeCaptureAudioMode = 'off';

  var QUALITY = {
    quality:  { label: '清晰优先', bitrate: 50000000, desc: '带宽不足时优先保分辨率', pref: 'maintain-resolution' },
    balanced: { label: '均衡', bitrate: 50000000, desc: '自动平衡分辨率与帧率', pref: 'balanced' },
    smooth:   { label: '流畅优先', bitrate: 50000000, desc: '带宽不足时优先保帧率', pref: 'maintain-framerate' }
  };

  var RESOLUTIONS = {
    'auto':      { label: '自动 (推荐)', w: null, h: null },
    '1920x1080': { label: '1080p (1920×1080)', w: 1920, h: 1080 },
    '1280x720':  { label: '720p (1280×720)', w: 1280, h: 720 },
    '854x480':   { label: '480p (854×480)', w: 854, h: 480 },
    '640x360':   { label: '360p (640×360)', w: 640, h: 360 }
  };

  var FPS_OPTIONS = [30, 45, 60];

  var MAX_SHARE_FPS = 60;
  var STATS_FETCH_TIMEOUT_MS = 3500;
  var trackedPeerCounter = 0;
  var statsFetchRequestSeq = 0;

  var AUDIO_MODES = {
    app: {
      label: '窗口/标签页音频 (推荐)',
      desc: '共享所选窗口或浏览器标签页的声音；浏览器未提供音轨时将仅共享画面。'
    },
    off: {
      label: '不共享音频',
      desc: '只共享画面，最适合一边语音通话一边演示，完全避免回声。'
    },
    system: {
      label: '全屏系统音频',
      desc: '仅在选择整个屏幕时共享系统声音；标签页和普通窗口会自动静音。'
    }
  };

  var SERVER_PROFILE = {
    cores: 2,
    ramGB: 2,
    netMbps: 200,
    safeNetMbps: 160
  };

  function loadCfg() {
    try { return JSON.parse(localStorage.getItem(SETTINGS_KEY) || '{}') || {}; } catch (e) { return {}; }
  }
  function saveCfg(s) { localStorage.setItem(SETTINGS_KEY, JSON.stringify(s)); }
  function ensureDefaultShareSettings() {
    var s = loadCfg();
    var changed = false;
    if (!s.resolution) {
      s.resolution = '1280x720';
      changed = true;
    }
    if (!s.framerate) {
      s.framerate = 30;
      changed = true;
    }
    if (!s.qualityMode) {
      s.qualityMode = 'balanced';
      changed = true;
    }
    if (changed) saveCfg(s);
  }
  function normalizeResolutionKey(key) {
    if (key === '3840x2160' || key === '2560x1440') return '1920x1080';
    return RESOLUTIONS[key] ? key : 'auto';
  }
  function normalizeFramerateValue(value) {
    var fps = parseInt(value, 10);
    if (isNaN(fps)) return 30;
    var best = FPS_OPTIONS[0];
    var bestDiff = Math.abs(fps - best);
    for (var i = 1; i < FPS_OPTIONS.length; i++) {
      var diff = Math.abs(fps - FPS_OPTIONS[i]);
      if (diff < bestDiff) {
        best = FPS_OPTIONS[i];
        bestDiff = diff;
      }
    }
    return best;
  }
  function extCfg() {
    var s = loadCfg();
    return {
      res: normalizeResolutionKey(s.resolution || '1280x720'),
      fps: normalizeFramerateValue(s.framerate || 30),
      qm: s.qualityMode || 'balanced',
      audio: AUDIO_MODES[s.audioMode] ? s.audioMode : 'app'
    };
  }
  function maxBitrate() { return 50000000; }
  function resConstraints() {
    var c = extCfg(), p = RESOLUTIONS[c.res];
    return (p && p.w) ? { width: { ideal: p.w }, height: { ideal: p.h } } : {};
  }

  function roomIdFromLocation() {
    var match = location.search.match(/[?&]room=([^&]+)/);
    return match ? decodeURIComponent(match[1]) : '';
  }

  function currentRoomId() {
    return roomIdFromLocation() || String(activeRoomSocketRoomId || webPresenceRoom || '').trim();
  }

  function currentAdaptivePolicyMode() {
    if (typeof GameCastWebRTCAdaptive === 'undefined') return '';
    return GameCastWebRTCAdaptive.resolveAdaptiveMode(currentRoomId(), location.search || '');
  }

  function browserBWEBaselineEnabled() {
    return typeof GameCastWebRTCAdaptive !== 'undefined' &&
      currentAdaptivePolicyMode() === GameCastWebRTCAdaptive.BASELINE_POLICY_VERSION;
  }

  function createCurrentAdaptiveState(connectedAt) {
    if (typeof GameCastWebRTCAdaptive === 'undefined') return null;
    var state = GameCastWebRTCAdaptive.createAdaptiveState(connectedAt);
    state.policyVersion = currentAdaptivePolicyMode() || GameCastWebRTCAdaptive.POLICY_VERSION;
    return state;
  }

  function normalizeScreegoSessionId(value) {
    value = String(value || '').trim();
    return /^[a-zA-Z0-9._:\-]{1,128}$/.test(value) ? value : '';
  }

  function clearPeerQualityState(pc) {
    if (!pc) return;
    pc._gcAdaptiveState = null;
    pc._gcAdaptivePrev = null;
    pc._gcAdaptiveProfile = null;
    pc._gcAdaptiveLastSample = null;
    pc._gcAdaptivePendingDecision = '';
    pc._gcQualityState = null;
    pc._gcProtectionMaxBitrate = 0;
    pc._gcProtectionMaxWidth = 0;
    pc._gcProtectionMaxHeight = 0;
    pc._gcProtectionRecoveryStep = 0;
    pc._gcProtectionChangedAt = 0;
    pc._gcStatsSnapshot = null;
    pc._gcTelemetryReasonLatch = null;
    pc._gcReceiverParticipantId = '';
  }

  function resetPeerQualityState(pc) {
    if (!pc) return;
    clearPeerQualityState(pc);
    pc._gcAdaptiveState = createCurrentAdaptiveState(Date.now());
    pc._gcQualityState = typeof GameCastWebRTCQuality !== 'undefined'
      ? GameCastWebRTCQuality.createQualityState(Date.now())
      : null;
    pc._gcLifecycleCleaned = false;
    pc._gcLifecycleOrphaned = false;
    pc._gcOrphanedSince = 0;
    pc._gcDisconnectedSince = 0;
  }

  function clearPeerLifecycleTimers(pc) {
    if (!pc) return;
    if (pc._gcDisconnectedTimer) {
      clearTimeout(pc._gcDisconnectedTimer);
      pc._gcDisconnectedTimer = 0;
    }
    if (pc._gcOrphanedTimer) {
      clearTimeout(pc._gcOrphanedTimer);
      pc._gcOrphanedTimer = 0;
    }
  }

  function cleanupPeerConnectionState(pc, close, reason) {
    if (!pc || pc._gcLifecycleCleanupInProgress) return false;
    pc._gcLifecycleCleanupInProgress = true;
    var sessionId = normalizeScreegoSessionId(pc._gcScreegoSessionId);
    if (sessionId && screegoPeerSessions[sessionId] === pc) delete screegoPeerSessions[sessionId];
    if (sessionId) delete adaptiveQualitySnapshot[sessionId];
    clearPeerLifecycleTimers(pc);
    var index = activePCs.indexOf(pc);
    if (index >= 0) activePCs.splice(index, 1);
    clearPeerQualityState(pc);
    pc._gcScreegoSessionId = '';
    pc._gcScreegoPeerId = '';
    pc._gcScreegoRole = '';
    pc._gcLifecycleOrphaned = false;
    pc._gcLifecycleCleaned = true;
    if (reason) console.log('[GameCast] 清理媒体会话:', reason, getTrackedPeerId(pc));
    if (close && String(pc.connectionState || '').toLowerCase() !== 'closed') {
      try { pc.close(); } catch (e) {}
    }
    return true;
  }

  function handleScreegoPeerSession(metadata) {
    metadata = metadata || {};
    var sessionId = normalizeScreegoSessionId(metadata.sid);
    var pc = metadata.pc;
    if (!sessionId || !pc) return false;
    if (metadata.action === 'close') {
      cleanupPeerConnectionState(pc, false, 'session-close');
      return true;
    }
    var existingPeer = screegoPeerSessions[sessionId];
    if (existingPeer && existingPeer !== pc) cleanupPeerConnectionState(existingPeer, true, 'session-replaced');
    pc._gcLifecycleCleaned = false;
    pc._gcLifecycleOrphaned = false;
    pc._gcLifecycleCleanupInProgress = false;
    pc._gcScreegoSessionId = sessionId;
    pc._gcScreegoPeerId = String(metadata.peerId || '').slice(0, 128);
    pc._gcScreegoRole = metadata.role === 'host' ? 'host' : 'viewer';
    screegoPeerSessions[sessionId] = pc;
    return true;
  }

  function clearAdaptiveQualitySnapshot() {
    adaptiveQualitySnapshot = Object.create(null);
    adaptiveQualitySnapshotPromise = null;
  }

  function installScreegoPeerSessionHook() {
    window.__PARTYLAN_REGISTER_PEER_SESSION__ = handleScreegoPeerSession;
  }

  function isUnifiedSfuRoom(state) {
    return !!(
      state &&
      state.active &&
      state.mediaMode === 'sfu' &&
      Number(state.mediaProtocolVersion) === 1
    );
  }

  function applyRoomMediaMode(state) {
    var stateRoom = String(state && state.room || '').trim();
    var roomId = currentRoomId();
    var unified = !!roomId && sameRoomId(stateRoom, roomId) && isUnifiedSfuRoom(state);
    window.__PARTYLAN_SFU_ONLY__ = unified;
    return unified;
  }

  function clearRoomMediaMode() {
    try { delete window.__PARTYLAN_SFU_ONLY__; } catch (e) { window.__PARTYLAN_SFU_ONLY__ = false; }
  }

  function rememberRoomState(state) {
    if (!state) return state;
    lastRoomState = state;
    applyRoomMediaMode(state);
    return state;
  }

  function getWebParticipantId() {
    if (webParticipantId) return webParticipantId;
    try { webParticipantId = sessionStorage.getItem('gcWebParticipantId') || ''; } catch (e) {}
    if (!webParticipantId) {
      webParticipantId = 'web-' + (
        window.crypto && typeof window.crypto.randomUUID === 'function'
          ? window.crypto.randomUUID()
          : Date.now().toString(36) + '-' + Math.random().toString(36).slice(2)
      );
      try { sessionStorage.setItem('gcWebParticipantId', webParticipantId); } catch (e2) {}
    }
    return webParticipantId;
  }

  function webDisplayName() {
    return currentUser || lastKnownAuthUser || '网页成员';
  }

  function canSendProtectedControl() {
    return !!lastKnownLoggedIn && !!currentUser && currentUser !== 'guest';
  }

  function isProtectedControlWrite(url, options) {
    if (!options || String(options.method || 'GET').toUpperCase() === 'GET') return false;
    return [ROOM_STATE_URL, PRESENCE_URL, SHARE_URL, QUALITY_REPORT_URL].some(function (base) {
      return url === base || url.indexOf(base + '/') === 0;
    });
  }

  function suspendProtectedControlWrites() {
    lastKnownLoggedIn = false;
    clearRoomStateHeartbeat();
    if (webPresenceTimer) clearInterval(webPresenceTimer);
    if (webPresenceRefreshTimer) clearInterval(webPresenceRefreshTimer);
    webPresenceTimer = 0;
    webPresenceRefreshTimer = 0;
    clearWebShareTimers();
  }

  function controlRequest(url, options) {
    options = options || {};
    if (isProtectedControlWrite(url, options) && !canSendProtectedControl()) {
      var authError = new Error('not_authenticated');
      authError.code = 'not_authenticated';
      return Promise.reject(authError);
    }
    options.credentials = 'same-origin';
    options.cache = 'no-store';
    if (options.body && !options.headers) options.headers = { 'Content-Type': 'application/json' };
    return fetch(url, options).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (data) {
        if (!response.ok || !data.ok) {
          var code = data.code || data.error || ('http_' + response.status);
          if (response.status === 401) suspendProtectedControlWrites();
          var error = new Error(code);
          error.code = code;
          throw error;
        }
        return data;
      });
    });
  }

  function mergeWebPresence(participants) {
    var seen = {};
    var merged = [];
    (Array.isArray(participants) ? participants : []).forEach(function (participant) {
      var participantId = String(participant && participant.participantId || '').trim();
      if (!participantId || !participant.active || seen[participantId]) return;
      seen[participantId] = true;
      merged.push({
        participantId: participantId,
        name: String(participant.name || participantId),
        clientType: participant.clientType === 'client' ? 'client' : 'web',
        role: String(participant.role || 'member'),
        active: true,
        sharing: !!participant.sharing
      });
    });
    return merged;
  }

  function hasWebPresenceParticipant(participantId) {
    participantId = String(participantId || '').trim();
    if (!participantId) return false;
    return webPresenceMembers.some(function (participant) {
      return participant && participant.participantId === participantId;
    });
  }

  function getPeerReceiverParticipantId(pc) {
    if (!pc) return '';
    if (pc._gcReceiverParticipantId) return pc._gcReceiverParticipantId;
    var sessionId = normalizeScreegoSessionId(pc._gcScreegoSessionId);
    var sample = sessionId ? adaptiveQualitySnapshot[sessionId] : null;
    return String(sample && sample.participantId || '').trim();
  }

  function reconcileOrphanedPeerConnections() {
    if (!activeDisplayStream || !Array.isArray(webPresenceMembers)) return;
    var now = Date.now();
    activePCs.slice().forEach(function (pc) {
      if (!pc || pc._gcLifecycleCleaned || pc._gcScreegoRole !== 'host') return;
      var participantId = getPeerReceiverParticipantId(pc);
      if (!participantId || participantId === getWebParticipantId()) return;
      if (hasWebPresenceParticipant(participantId)) {
        pc._gcLifecycleOrphaned = false;
        pc._gcOrphanedSince = 0;
        if (pc._gcOrphanedTimer) {
          clearTimeout(pc._gcOrphanedTimer);
          pc._gcOrphanedTimer = 0;
        }
        return;
      }
      pc._gcLifecycleOrphaned = true;
      if (!pc._gcOrphanedSince) pc._gcOrphanedSince = now;
      if (!pc._gcOrphanedTimer) {
        pc._gcOrphanedTimer = setTimeout(function () {
          pc._gcOrphanedTimer = 0;
          if (
            !pc._gcLifecycleCleaned &&
            pc._gcLifecycleOrphaned &&
            Date.now() - pc._gcOrphanedSince >= PEER_ORPHAN_GRACE_MS
          ) cleanupPeerConnectionState(pc, true, 'presence-orphan');
        }, PEER_ORPHAN_GRACE_MS);
      }
    });
  }

  function webCanStartShare(state) {
    if (!state || !state.sharing) return true;
    return state.shareOwnerParticipantId === getWebParticipantId();
  }

  function recoverWebPresence(roomId) {
    roomId = String(roomId || currentRoomId() || '').trim();
    if (!roomId) return Promise.resolve(null);
    if (webPresenceRecoveryPromise) return webPresenceRecoveryPromise;
    var generation = webPresenceRecoveryGeneration;
    webPresenceRecoveryPromise = Promise.resolve().then(function () {
      webPresenceRoom = '';
      return ensureWebPresence(roomId);
    }).then(function (data) {
      if (generation !== webPresenceRecoveryGeneration) return null;
      if (!data) throw new Error('presence_recovery_failed');
      webPresenceRecoveryFailures = 0;
      if (activeDisplayStream && webShareLease) return heartbeatWebShare().then(function () {
        if (!webShareLease) throw new Error('share_lease_invalid');
        return refreshWebPresence().then(function (refreshed) {
          if (!refreshed) throw new Error('presence_refresh_failed');
          return data;
        });
      });
      return data;
    }).catch(function (error) {
      webPresenceRecoveryFailures += 1;
      console.warn('[GameCast/control] Presence 自愈失败', webPresenceRecoveryFailures, error && error.message || error);
      if (activeDisplayStream && webPresenceRecoveryFailures >= PRESENCE_RECOVERY_MAX_FAILURES) {
        return stopWebShare(false).then(function () { return null; });
      }
      return null;
    }).finally(function () {
      webPresenceRecoveryPromise = null;
    });
    return webPresenceRecoveryPromise;
  }

  function refreshWebPresence() {
    var roomId = currentRoomId();
    if (!roomId || webPresenceRoom !== roomId) return Promise.resolve(null);
    return controlRequest(PRESENCE_URL + '?room=' + encodeURIComponent(roomId)).then(function (data) {
      webPresenceMembers = mergeWebPresence(data.participants);
      if (data.room) rememberRoomState(data.room);
      reconcileOrphanedPeerConnections();
      if (activeDisplayStream && Array.isArray(data.participants) && !hasWebPresenceParticipant(getWebParticipantId())) {
        recoverWebPresence(roomId);
      }
      return data;
    }).catch(function (error) {
      if (activeDisplayStream && !(error && error.code === 'not_authenticated')) {
        recoverWebPresence(roomId);
      }
      return null;
    });
  }

  function heartbeatWebPresence() {
    var roomId = currentRoomId();
    if (!roomId || webPresenceRoom !== roomId) return Promise.resolve(null);
    return controlRequest(PRESENCE_URL + '/heartbeat', {
      method: 'POST',
      body: JSON.stringify({ room: roomId, participantId: getWebParticipantId(), ttl: ROOM_STATE_TTL })
    }).catch(function (error) {
      if (error && error.code === 'participant_not_found') {
        return recoverWebPresence(roomId);
      }
      if (activeDisplayStream && !(error && error.code === 'not_authenticated')) {
        return recoverWebPresence(roomId);
      }
      return null;
    });
  }

  function startWebPresenceHeartbeat() {
    if (webPresenceTimer) clearInterval(webPresenceTimer);
    if (webPresenceRefreshTimer) clearInterval(webPresenceRefreshTimer);
    webPresenceTimer = setInterval(function () {
      heartbeatWebPresence();
    }, ROOM_STATE_HEARTBEAT_MS);
    webPresenceRefreshTimer = setInterval(function () {
      refreshWebPresence();
    }, PRESENCE_REFRESH_MS);
  }

  function leaveWebPresence(useBeacon) {
    var roomId = webPresenceRoom;
    webPresenceRecoveryGeneration += 1;
    webPresenceRecoveryFailures = 0;
    if (webPresenceTimer) clearInterval(webPresenceTimer);
    if (webPresenceRefreshTimer) clearInterval(webPresenceRefreshTimer);
    webPresenceTimer = 0;
    webPresenceRefreshTimer = 0;
    webPresenceRoom = '';
    webPresenceRole = '';
    webPresenceName = '';
    webPresenceMembers = [];
    clearAdaptiveQualitySnapshot();
    applyRoomMediaMode(lastRoomState);
    if (!roomId) return Promise.resolve(null);
    var payload = JSON.stringify({ room: roomId, participantId: getWebParticipantId() });
    if (useBeacon && navigator.sendBeacon) {
      try { navigator.sendBeacon(PRESENCE_URL + '/leave', payload); } catch (e) {}
      return Promise.resolve(null);
    }
    return controlRequest(PRESENCE_URL + '/leave', { method: 'POST', body: payload }).catch(function () { return null; });
  }

  function ensureWebPresence(roomId) {
    roomId = (roomId || currentRoomId() || '').trim();
    if (!roomId) return Promise.resolve(null);
    var name = webDisplayName();
    if (webPresenceRoom === roomId && webPresenceName === name) return heartbeatWebPresence();
    if (webPresenceRoom && webPresenceRoom !== roomId) leaveWebPresence(false);
    return fetchRoomState(roomId, true).then(function (state) {
      var active = !!(state && state.active);
      var request = {
        room: roomId,
        participantId: getWebParticipantId(),
        clientType: 'web',
        role: active ? 'member' : 'owner',
        origin: active ? '' : 'web',
        name: name,
        mediaProtocolVersion: 1,
        capabilities: {
          sfuViewer: true,
          sfuHost: true,
          h264: true,
          opus: true
        },
        ttl: ROOM_STATE_TTL
      };
      function join() {
        return controlRequest(PRESENCE_URL + '/join', { method: 'POST', body: JSON.stringify(request) });
      }
      return join().catch(function (error) {
        if (error && error.code === 'room_not_found' && !request.origin) {
          request.role = 'owner';
          request.origin = 'web';
          return join();
        }
        throw error;
      }).then(function (data) {
        webPresenceRoom = roomId;
        webPresenceRole = data.participant && data.participant.role || request.role;
        webPresenceName = name;
        if (data.room) rememberRoomState(data.room);
        startWebPresenceHeartbeat();
        refreshWebPresence();
        return data;
      });
    }).catch(function (error) {
      console.warn('[GameCast/control] Presence 加入失败', error && error.message || error);
      return null;
    });
  }

  function clearWebShareTimers() {
    if (webShareHeartbeatTimer) clearInterval(webShareHeartbeatTimer);
    if (webShareSafetyTimer) clearTimeout(webShareSafetyTimer);
    webShareHeartbeatTimer = 0;
    webShareSafetyTimer = 0;
  }

  function effectiveWebShareAudioMode() {
    return activeDisplayStream ? activeCaptureAudioMode : 'off';
  }

  function heartbeatWebShare(phase) {
    if (!webShareLease) return Promise.resolve(null);
    var mediaMode = webShareLease.mediaMode || (isUnifiedSfuRoom(lastRoomState) ? 'sfu' : 'legacy-p2p');
    phase = phase || webShareLease.phase || (mediaMode === 'sfu' ? 'starting' : 'live');
    return controlRequest(SHARE_URL + '/heartbeat', {
      method: 'POST',
      body: JSON.stringify({
        room: webShareLease.room,
        participantId: getWebParticipantId(),
        leaseId: webShareLease.leaseId,
        mediaMode: mediaMode,
        phase: phase,
        sfu: mediaMode === 'legacy-p2p' ? false : undefined,
        audioMode: effectiveWebShareAudioMode(),
        ttl: ROOM_STATE_TTL
      })
    }).then(function (data) {
      if (data.lease) webShareLease = data.lease;
      if (data.room) rememberRoomState(data.room);
      return data;
    }).catch(function (error) {
      if (error && (error.code === 'share_lease_expired' || error.code === 'share_lease_invalid')) {
        clearWebShareTimers();
        webShareLease = null;
        if (activeDisplayStream) stopWebShare(false);
      }
      return null;
    });
  }

  function startWebShareHeartbeat() {
    clearWebShareTimers();
    webShareHeartbeatTimer = setInterval(heartbeatWebShare, ROOM_STATE_HEARTBEAT_MS);
  }

  function claimWebShare() {
    var roomId = currentRoomId();
    if (!roomId) return Promise.reject(new Error('room_not_found'));
    return ensureWebPresence(roomId).then(function () { return fetchRoomState(roomId, true); }).then(function (state) {
      if (!webCanStartShare(state)) throw new Error('share_locked');
      if (webShareLease && webShareLease.room === roomId) return webShareLease;
      var unified = isUnifiedSfuRoom(state);
      var request = {
        room: roomId,
        participantId: getWebParticipantId(),
        mediaMode: unified ? 'sfu' : 'legacy-p2p',
        audioMode: extCfg().audio,
        ttl: ROOM_STATE_TTL
      };
      if (unified) {
        request.mediaMode = 'sfu';
        request.phase = 'starting';
      }
      return controlRequest(SHARE_URL + '/claim', {
        method: 'POST',
        body: JSON.stringify(request)
      }).then(function (data) {
        webShareLease = data.lease;
        if (data.room) rememberRoomState(data.room);
        startWebShareHeartbeat();
        return webShareLease;
      });
    });
  }

  function releaseWebShare(useBeacon) {
    var lease = webShareLease;
    clearWebShareTimers();
    webShareLease = null;
    activeCaptureAudioMode = 'off';
    if (!lease) return Promise.resolve(null);
    var payload = JSON.stringify({ room: lease.room, participantId: getWebParticipantId(), leaseId: lease.leaseId });
    if (useBeacon && navigator.sendBeacon) {
      try { navigator.sendBeacon(SHARE_URL + '/release', payload); } catch (e) {}
      return Promise.resolve(null);
    }
    return controlRequest(SHARE_URL + '/release', { method: 'POST', body: payload }).catch(function () { return null; });
  }

  function sendWebSharePhaseBeacon(phase) {
    if (!webShareLease || !navigator.sendBeacon || !canSendProtectedControl()) return false;
    try {
      return navigator.sendBeacon(SHARE_URL + '/heartbeat', JSON.stringify({
        room: webShareLease.room,
        participantId: getWebParticipantId(),
        leaseId: webShareLease.leaseId,
        mediaMode: webShareLease.mediaMode || 'sfu',
        phase: phase,
        audioMode: effectiveWebShareAudioMode(),
        ttl: ROOM_STATE_TTL
      }));
    } catch (e) {
      return false;
    }
  }

  function stopWebShare(useBeacon) {
    if (webShareStopPromise) return webShareStopPromise;
    var stream = activeDisplayStream;
    var markStopping = Promise.resolve(null);
    if (webShareLease && (webShareLease.mediaMode === 'sfu' || isUnifiedSfuRoom(lastRoomState))) {
      if (useBeacon) sendWebSharePhaseBeacon('stopping');
      else markStopping = heartbeatWebShare('stopping');
    }

    webShareStopPromise = markStopping.catch(function () { return null; }).then(function () {
      sfuDisconnect();
      stopDisplayStreamTracks(stream);
      activeDisplayStream = null;
      activeVideoTrack = null;
      activeCaptureAudioMode = 'off';
      closeActivePeerConnections('share-stop');
      resetAdaptiveStreamingState();
      return releaseWebShare(useBeacon);
    }).finally(function () {
      webShareStopPromise = null;
    });
    return webShareStopPromise;
  }

  function notifyDisplayStreamEnded(stream) {
    if (!stream || typeof stream.getVideoTracks !== 'function') return;
    var track = stream.getVideoTracks()[0];
    if (!track || typeof track.dispatchEvent !== 'function' || typeof Event === 'undefined') return;
    try { track.dispatchEvent(new Event('ended')); } catch (e) {}
  }

  function abortWebShare(error) {
    if (webShareStopPromise) return webShareStopPromise;
    if (webShareAbortPromise) return webShareAbortPromise;
    var failedStream = activeDisplayStream;
    activeDisplayStream = null;
    activeVideoTrack = null;
    activeCaptureAudioMode = 'off';
    sfuDisconnect();
    stopDisplayStreamTracks(failedStream);
    notifyDisplayStreamEnded(failedStream);
    closeActivePeerConnections('share-abort');
    resetAdaptiveStreamingState();
    showSfuStatus('错误: ' + (error && error.code || 'sfu_unavailable'));
    webShareAbortPromise = releaseWebShare(false).finally(function () {
      webShareAbortPromise = null;
    });
    return webShareAbortPromise;
  }

  function sameRoomId(left, right) {
    return ((left || '') + '').trim().toLowerCase() === ((right || '') + '').trim().toLowerCase();
  }

  function ownsCurrentRoomState(roomId) {
    return !!roomId && !!currentUser && currentUser !== 'guest';
  }

  function canMaintainRoomState(roomId) {
    return !!roomId && (!!activeDisplayStream || ownsCurrentRoomState(roomId));
  }

  function currentSfuMode() {
    return isUnifiedSfuRoom(lastRoomState);
  }

  function roomStateSfuActive(roomId) {
    return !!(
      lastRoomState &&
      lastRoomState.room === roomId &&
      isUnifiedSfuRoom(lastRoomState) &&
      lastRoomState.sharing &&
      lastRoomState.sharePhase === 'live' &&
      lastRoomState.sfu
    );
  }

  function clearRoomStateHeartbeat() {
    if (roomStateHeartbeatTimer) {
      clearInterval(roomStateHeartbeatTimer);
      roomStateHeartbeatTimer = 0;
    }
  }

  function resetAdaptiveStreamingState() {
    adaptiveServerBudgetPeerCount = 0;
    clearAdaptiveQualitySnapshot();
    activePCs.forEach(function (pc) {
      resetPeerQualityState(pc);
    });
  }

  function getSfuPeerConnection() {
    if (typeof GC_SFU === 'undefined' || !GC_SFU || typeof GC_SFU.getPeerConnection !== 'function') return null;
    return GC_SFU.getPeerConnection() || null;
  }

  function getSfuRole() {
    if (typeof GC_SFU === 'undefined' || !GC_SFU || typeof GC_SFU.getRole !== 'function') return '';
    return GC_SFU.getRole() || '';
  }

  function parseKbpsValue(value) {
    if (typeof value === 'number') return value;
    if (typeof value !== 'string') return null;
    var match = value.match(/(-?\d+(?:\.\d+)?)\s*kbps/i);
    if (!match) return null;
    return parseFloat(match[1]);
  }

  function formatKbps(value) {
    if (value === null || value === undefined || isNaN(value)) return '-';
    if (value >= 1000) return (value / 1000).toFixed(1) + 'Mbps';
    return Math.round(value) + 'kbps';
  }

  function formatMbps(value) {
    if (value === null || value === undefined || isNaN(value)) return '-';
    return value.toFixed(1) + 'Mbps';
  }

  function getMemberCountEstimate() {
    return Math.max(1, webPresenceMembers.length);
  }

  function syncHostSfuState(roomId) {
    if (!roomId || !activeDisplayStream) {
      if (canMaintainRoomState(roomId)) startRoomStateHeartbeat();
      else stopRoomStateHeartbeat();
      return;
    }

    if (currentSfuMode()) {
      if (!sfuConnection || getSfuRole() !== 'host') sfuConnect(roomId, 'host', activeDisplayStream);
      else startRoomStateHeartbeat();
      return;
    }

    startRoomStateHeartbeat();
    if (sfuConnection) sfuDisconnect();
  }

  function getTrackedPeerId(pc) {
    if (!pc) return '';
    if (!pc._gcTrackedPeerId) pc._gcTrackedPeerId = 'pc-' + (++trackedPeerCounter);
    return pc._gcTrackedPeerId;
  }

  function peerConnectionLooksUsable(pc) {
    if (!pc) return false;
    if (pc._gcLifecycleCleaned || pc._gcLifecycleOrphaned) return false;
    var state = (pc.connectionState || '').toLowerCase();
    return state !== 'closed' && state !== 'failed';
  }

  function peerConnectionHasVideoTrack(pc) {
    if (!pc) return false;
    var senders = typeof pc.getSenders === 'function' ? pc.getSenders() : [];
    for (var i = 0; i < senders.length; i++) {
      if (senders[i] && senders[i].track && senders[i].track.kind === 'video') return true;
    }
    var receivers = typeof pc.getReceivers === 'function' ? pc.getReceivers() : [];
    for (var j = 0; j < receivers.length; j++) {
      if (receivers[j] && receivers[j].track && receivers[j].track.kind === 'video') return true;
    }
    return false;
  }

  function peerConnectionHasVideoSender(pc) {
    if (!pc || typeof pc.getSenders !== 'function') return false;
    var senders = pc.getSenders() || [];
    for (var i = 0; i < senders.length; i++) {
      var track = senders[i] && senders[i].track;
      if (!track || track.kind !== 'video') continue;
      if (!track.readyState || track.readyState === 'live') return true;
    }
    return false;
  }

  function peerConnectionIsMediaConnected(pc) {
    if (!peerConnectionLooksUsable(pc)) return false;
    var state = (pc.connectionState || '').toLowerCase();
    var iceState = (pc.iceConnectionState || '').toLowerCase();
    return state === 'connected' || iceState === 'connected' || iceState === 'completed';
  }

  function peerConnectionCanReportStats(pc) {
    if (!peerConnectionLooksUsable(pc)) return false;
    var state = (pc.connectionState || '').toLowerCase();
    var iceState = (pc.iceConnectionState || '').toLowerCase();
    if (state === 'connected') return true;
    if (iceState === 'connected' || iceState === 'completed') return true;
    return state === 'connecting' && peerConnectionHasVideoTrack(pc);
  }

  function describePeerConnectionState(pc) {
    if (!pc) return '';
    var parts = [];
    if (pc.connectionState) parts.push('Peer ' + pc.connectionState);
    if (pc.iceConnectionState && pc.iceConnectionState !== pc.connectionState) parts.push('ICE ' + pc.iceConnectionState);
    return parts.join(' / ');
  }

  function buildNativePeerDescriptor(pc) {
    if (!pc) return null;
    return {
      pc: pc,
      mode: activeDisplayStream ? 'native-host' : 'native-viewer'
    };
  }

  function getLatestNativePeerDescriptor(preferReady) {
    for (var i = activePCs.length - 1; i >= 0; i--) {
      var pc = activePCs[i];
      if (!peerConnectionLooksUsable(pc)) continue;
      if (preferReady && !peerConnectionCanReportStats(pc)) continue;
      return buildNativePeerDescriptor(pc);
    }
    return null;
  }

  function getStatsPeerDescriptor() {
    var sfuPc = getSfuPeerConnection();
    if (peerConnectionCanReportStats(sfuPc)) {
      var role = getSfuRole();
      return {
        pc: sfuPc,
        mode: role === 'host' ? 'sfu-host' : 'sfu-viewer'
      };
    }
    var nativeReady = getLatestNativePeerDescriptor(true);
    if (nativeReady) return nativeReady;

    if (peerConnectionLooksUsable(sfuPc)) {
      var sfuRole = getSfuRole();
      return {
        pc: sfuPc,
        mode: sfuRole === 'host' ? 'sfu-host' : 'sfu-viewer'
      };
    }

    var nativePending = getLatestNativePeerDescriptor(false);
    if (nativePending) return nativePending;
    return null;
  }

  function getStatsWithTimeout(pc, timeoutMs) {
    return new Promise(function (resolve, reject) {
      var settled = false;
      var timer = setTimeout(function () {
        if (settled) return;
        settled = true;
        reject(new Error('timeout'));
      }, timeoutMs);

      var statsPromise;
      try {
        statsPromise = pc.getStats();
      } catch (err) {
        clearTimeout(timer);
        reject(err);
        return;
      }

      Promise.resolve(statsPromise).then(function (stats) {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        resolve(stats);
      }).catch(function (err) {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        reject(err);
      });
    });
  }

  function buildStatsStatusText(message, pc) {
    var lines = [
      '🛰 SFU: ' + getSfuStatusLabel(),
      '🎛 自适应: ' + getAdaptiveStatusLabel(pc),
      '📡 WebRTC: ' + message
    ];
    var stateText = describePeerConnectionState(pc);
    if (stateText) lines.push('⏱ 状态: ' + stateText);
    lines.push('📝 说明: 实时上下行 / 延迟来自浏览器 getStats；服务器负载与容量为估算值');
    return lines.join('\n');
  }

  function classifyRttQuality(rttText) {
    var rttNum = parseFloat(rttText);
    if (isNaN(rttNum)) return { text: '⚪ 检测中', level: 'probing', value: null };
    if (rttNum <= 200) return { text: '🟢 优', level: 'excellent', value: rttNum };
    if (rttNum <= 500) return { text: '🟡 良', level: 'good', value: rttNum };
    if (rttNum <= 1000) return { text: '🟠 中', level: 'fair', value: rttNum };
    return { text: '🔴 差', level: 'poor', value: rttNum };
  }

  function getAdaptiveStatusLabel(pc) {
    var state = pc && pc._gcAdaptiveState;
    var profile = pc && pc._gcAdaptiveProfile;
    if (state && state.policyVersion === 'browser-bwe-baseline-v1') return 'BWE 基线';
    if (!state || !profile || state.level <= 0) return '默认';
    return 'L' + state.level + ' / ' + profile.maxWidth + 'x' + profile.maxHeight +
      ' / ' + profile.maxFramerate + ' FPS / ' + formatKbps(profile.maxBitrate / 1000);
  }

  function shouldUseAdaptiveStreaming(pc) {
    return !!activeDisplayStream &&
      typeof GameCastWebRTCAdaptive !== 'undefined' &&
      !browserBWEBaselineEnabled() &&
      peerConnectionIsMediaConnected(pc) &&
      peerConnectionHasVideoSender(pc);
  }

  function applyAdaptiveProfile(pc, reason) {
    if (!shouldUseAdaptiveStreaming(pc)) return Promise.resolve(false);
    if (reason) console.log('[GameCast] 逐观众自适应:', getTrackedPeerId(pc), reason);
    return applyBitrate(pc);
  }

  function getAdaptivePeerDescriptors() {
    var descriptors = [];
    var seen = [];
    activePCs.forEach(function (pc) {
      if (!shouldUseAdaptiveStreaming(pc) || pc._gcLifecycleOrphaned || seen.indexOf(pc) >= 0) return;
      seen.push(pc);
      descriptors.push({pc: pc, mode: 'native-host'});
    });
    var sfuPc = getSfuPeerConnection();
    if (getSfuRole() === 'host' && shouldUseAdaptiveStreaming(sfuPc) && seen.indexOf(sfuPc) < 0) {
      descriptors.push({pc: sfuPc, mode: 'sfu-host'});
    }
    return descriptors;
  }

  function isServerBudgetedAdaptivePeer(descriptor) {
    if (!descriptor || !descriptor.pc) return false;
    return descriptor.mode === 'sfu-host' || !!(
      descriptor.pc._gcAdaptiveLastSample &&
      descriptor.pc._gcAdaptiveLastSample.path === 'turn'
    );
  }

  function countServerBudgetedAdaptivePeers(descriptors) {
    var count = 0;
    (descriptors || getAdaptivePeerDescriptors()).forEach(function (descriptor) {
      if (!isServerBudgetedAdaptivePeer(descriptor)) return;
      count += descriptor.mode === 'sfu-host'
        ? Math.max(1, getMemberCountEstimate() - 1)
        : 1;
    });
    return count;
  }

  function getServerBudgetedAdaptivePeerCount() {
    return Math.max(1, countServerBudgetedAdaptivePeers());
  }

  function getAdaptivePeerDescriptor(pc) {
    var descriptors = getAdaptivePeerDescriptors();
    for (var i = 0; i < descriptors.length; i++) {
      if (descriptors[i].pc === pc) return descriptors[i];
    }
    return {pc: pc, mode: 'native-host'};
  }

  function getPeerVideoSender(pc) {
    if (!pc || typeof pc.getSenders !== 'function') return null;
    var senders = pc.getSenders() || [];
    for (var i = 0; i < senders.length; i++) {
      if (senders[i] && senders[i].track && senders[i].track.kind === 'video') return senders[i];
    }
    return null;
  }

  function computePeerAdaptiveProfile(pc, levelOverride) {
    if (typeof GameCastWebRTCAdaptive === 'undefined' || browserBWEBaselineEnabled()) return null;
    var sender = getPeerVideoSender(pc);
    var settings = sender && sender.track && typeof sender.track.getSettings === 'function'
      ? sender.track.getSettings() || {}
      : {};
    var cfg = extCfg();
    var configuredResolution = RESOLUTIONS[cfg.res] || {};
    var sourceWidth = settings.width || configuredResolution.w || 1280;
    var sourceHeight = settings.height || configuredResolution.h || 720;
    var state = pc._gcAdaptiveState || createCurrentAdaptiveState(Date.now());
    pc._gcAdaptiveState = state;
    var descriptor = getAdaptivePeerDescriptor(pc);
    var applyServerBudget = isServerBudgetedAdaptivePeer(descriptor);
    var profile = GameCastWebRTCAdaptive.computeEncodingProfile({
      level: typeof levelOverride === 'number' ? levelOverride : state.level,
      sourceWidth: sourceWidth,
      sourceHeight: sourceHeight,
      configuredFps: cfg.fps || 30,
      targetBitrate: computeTargetBitrate(),
      viewerCount: getServerBudgetedAdaptivePeerCount(),
      serverSafeBitrate: SERVER_PROFILE.safeNetMbps * 1000000,
      applyServerBudget: applyServerBudget,
      protectionMaxWidth: pc._gcProtectionMaxWidth || 0,
      protectionMaxHeight: pc._gcProtectionMaxHeight || 0,
      protectionMaxBitrate: pc._gcProtectionMaxBitrate || 0
    });
    if (typeof levelOverride !== 'number') pc._gcAdaptiveProfile = profile;
    return profile;
  }

  function computePeerAdaptiveFloorsKbps(pc, maxLevel) {
    var floors = [];
    for (var level = 0; level <= maxLevel; level++) {
      var profile = computePeerAdaptiveProfile(pc, level);
      floors.push(profile ? profile.degradeFloorBitrate / 1000 : 0);
    }
    return floors;
  }

  function refreshAdaptiveQualitySnapshot() {
    var roomId = currentRoomId();
    if (
      !activeDisplayStream || !webShareLease ||
      webShareLease.mediaMode !== 'legacy-p2p' || !roomId || !canSendProtectedControl() ||
      webPresenceRecoveryPromise
    ) {
      clearAdaptiveQualitySnapshot();
      return Promise.resolve(null);
    }
    if (adaptiveQualitySnapshotPromise) return adaptiveQualitySnapshotPromise;
    adaptiveQualitySnapshotPromise = controlRequest(
      QUALITY_SNAPSHOT_URL + '?room=' + encodeURIComponent(roomId) +
        '&participantId=' + encodeURIComponent(getWebParticipantId())
    ).then(function (data) {
      var next = Object.create(null);
      var fetchedAt = Date.now();
      (data && Array.isArray(data.samples) ? data.samples : []).forEach(function (sample) {
        var sessionId = normalizeScreegoSessionId(sample && sample.screegoSessionId);
        if (!sessionId) return;
        sample._gcSnapshotFetchedAt = fetchedAt;
        next[sessionId] = sample;
      });
      adaptiveQualitySnapshot = next;
      return data;
    }).catch(function () {
      clearAdaptiveQualitySnapshot();
      return null;
    }).finally(function () {
      adaptiveQualitySnapshotPromise = null;
    });
    return adaptiveQualitySnapshotPromise;
  }

  function receiverQualityForPeer(pc, now) {
    var sessionId = normalizeScreegoSessionId(pc && pc._gcScreegoSessionId);
    if (!sessionId || pc._gcScreegoRole !== 'host') return null;
    var sample = adaptiveQualitySnapshot[sessionId];
    if (!sample) return null;
    var ageMs = Math.max(0, Number(sample.ageSeconds || 0) * 1000) +
      Math.max(0, now - Number(sample._gcSnapshotFetchedAt || now));
    if (ageMs > 6000) return null;
    var totalPackets = Number(sample.packetsReceivedDelta || 0) +
      Number(sample.packetsLostDelta || 0);
    return {
      sessionId: sessionId,
      participantId: String(sample.participantId || ''),
      ageMs: ageMs,
      reportedAtUnix: Math.max(0, Number(sample.reportedAtUnix || 0)),
      decodedFps: Math.max(0, Number(sample.decodedFps || 0)),
      incomingKbps: Math.max(0, Number(sample.incomingBitrateKbps || 0)),
      lossPercent: totalPackets > 0
        ? Math.max(0, Number(sample.packetsLostDelta || 0)) * 100 / totalPackets
        : 0,
      jitterMs: Math.max(0, Number(sample.jitterMs || 0)),
      framesDroppedDelta: Math.max(0, Number(sample.framesDroppedDelta || 0))
    };
  }

  function sampleAdaptiveStreamingPeer(descriptor) {
    var pc = descriptor && descriptor.pc;
    if (!shouldUseAdaptiveStreaming(pc) || pc._gcAdaptiveFetchPromise || webPresenceRecoveryPromise) return;
    var adaptiveState = pc._gcAdaptiveState || createCurrentAdaptiveState(Date.now());
    var adaptiveProfile = computePeerAdaptiveProfile(pc);
    var nextAdaptiveProfile = adaptiveState && adaptiveState.level > 0
      ? computePeerAdaptiveProfile(pc, adaptiveState.level - 1)
      : adaptiveProfile;
    var adaptiveFloorsKbps = adaptiveProfile
      ? computePeerAdaptiveFloorsKbps(pc, adaptiveProfile.maxLevel)
      : [];
    var prev = pc._gcAdaptivePrev;
    var statsPromise = getStatsWithTimeout(pc, STATS_FETCH_TIMEOUT_MS);
    pc._gcAdaptiveFetchPromise = statsPromise;

    statsPromise.then(function (stats) {
      var pairDescription = typeof GameCastWebRTCStats !== 'undefined'
        ? GameCastWebRTCStats.describeSelectedPair(stats)
        : {pairId: '', path: 'unknown'};
      var pair = pairDescription.pairId ? stats.get(pairDescription.pairId) || {} : {};
      var outboundBytesTotal = 0;
      var outboundPacketsSentTotal = 0;
      var capturedFramesTotal = 0;
      var outboundFramesEncodedTotal = 0;
      var outboundFramesSentTotal = 0;
      var captureReportedFps = 0;
      var senderReportedFps = 0;
      var qualityReason = 'none';
      var remotePacketsLostTotal = 0;
      var remoteJitterMs = 0;
      var hasRemoteInbound = false;
      stats.forEach(function (report) {
        if (report.type === 'media-source' && (report.kind === 'video' || report.mediaType === 'video')) {
          capturedFramesTotal += report.frames || 0;
          captureReportedFps = Math.max(captureReportedFps, Number(report.framesPerSecond || 0));
        }
        if (report.type === 'remote-inbound-rtp' && (report.kind === 'video' || report.mediaType === 'video')) {
          hasRemoteInbound = true;
          remotePacketsLostTotal += report.packetsLost || 0;
          remoteJitterMs = Math.max(remoteJitterMs, (report.jitter || 0) * 1000);
        }
        if (report.type === 'outbound-rtp' && (report.kind === 'video' || report.mediaType === 'video')) {
          outboundBytesTotal += report.bytesSent || 0;
          outboundPacketsSentTotal += report.packetsSent || 0;
          outboundFramesEncodedTotal += report.framesEncoded || 0;
          outboundFramesSentTotal += report.framesSent || 0;
          senderReportedFps = Math.max(senderReportedFps, Number(report.framesPerSecond || 0));
          if (report.qualityLimitationReason) qualityReason = report.qualityLimitationReason;
        }
      });

      var now = Date.now();
      var receiverQuality = receiverQualityForPeer(pc, now);
      if (receiverQuality && receiverQuality.participantId) {
        pc._gcReceiverParticipantId = receiverQuality.participantId;
      }
      var previousAdaptivePath = pc._gcAdaptiveLastSample
        ? pc._gcAdaptiveLastSample.path || 'unknown'
        : 'unknown';
      var adaptiveSample = {
        now: now,
        valid: false,
        invalidReason: 'baseline',
        pairId: pairDescription.pairId || '',
        generation: pc._gcIceGeneration || 0,
        path: pairDescription.path || 'unknown',
        lossPercent: 0,
        jitterMs: remoteJitterMs,
        outboundKbps: 0,
        profileKbps: adaptiveProfile ? adaptiveProfile.maxBitrate / 1000 : computeTargetBitrate() / 1000,
        nominalKbps: adaptiveProfile ? adaptiveProfile.nominalBitrate / 1000 : computeTargetBitrate() / 1000,
        degradeFloorKbps: adaptiveProfile
          ? adaptiveProfile.degradeFloorBitrate / 1000
          : computeTargetBitrate() / 1000,
        nextProfileKbps: nextAdaptiveProfile
          ? nextAdaptiveProfile.maxBitrate / 1000
          : computeTargetBitrate() / 1000,
        nextNominalKbps: nextAdaptiveProfile
          ? nextAdaptiveProfile.nominalBitrate / 1000
          : computeTargetBitrate() / 1000,
        maxLevel: adaptiveProfile ? adaptiveProfile.maxLevel : 6,
        levelDegradeFloorsKbps: adaptiveFloorsKbps,
        receiverBound: !!receiverQuality,
        receiverReportAgeMs: receiverQuality ? receiverQuality.ageMs : 0,
        receiverReportedAtUnix: receiverQuality ? receiverQuality.reportedAtUnix : 0,
        receiverParticipantId: receiverQuality ? receiverQuality.participantId : '',
        receiverDecodedFps: receiverQuality ? receiverQuality.decodedFps : 0,
        receiverIncomingKbps: receiverQuality ? receiverQuality.incomingKbps : 0,
        receiverLossPercent: receiverQuality ? receiverQuality.lossPercent : 0,
        receiverJitterMs: receiverQuality ? receiverQuality.jitterMs : 0,
        receiverFramesDroppedDelta: receiverQuality ? receiverQuality.framesDroppedDelta : 0,
        senderCapturedFps: captureReportedFps,
        senderEncodedFps: senderReportedFps,
        senderSentFps: senderReportedFps,
        availableOutgoingBitrate: pair.availableOutgoingBitrate || 0,
        qualityLimitationReason: qualityReason
      };
      if (!pairDescription.pairId) {
        adaptiveSample.invalidReason = 'no-selected-pair';
      } else if (!hasRemoteInbound) {
        adaptiveSample.invalidReason = 'no-remote-inbound';
      } else if (prev) {
        if (prev.pairId !== adaptiveSample.pairId) {
          adaptiveSample.invalidReason = 'pair-changed';
        } else {
          var deltas = GameCastWebRTCAdaptive.computeSenderDeltas({
            bytesSent: outboundBytesTotal,
            packetsSent: outboundPacketsSentTotal,
            packetsLost: remotePacketsLostTotal,
            now: now
          }, {
            bytesSent: prev.bytesSent,
            packetsSent: prev.packetsSent,
            packetsLost: prev.remotePacketsLost,
            now: prev.ts
          });
          adaptiveSample.valid = deltas.valid;
          adaptiveSample.invalidReason = deltas.invalidReason;
          if (deltas.valid) {
            adaptiveSample.outboundKbps = deltas.outboundKbps;
            adaptiveSample.lossPercent = deltas.lossPercent;
            var elapsedSeconds = (now - prev.ts) / 1000;
            var capturedFramesDelta = capturedFramesTotal - Number(prev.framesCaptured || 0);
            var framesEncodedDelta = outboundFramesEncodedTotal - Number(prev.framesEncoded || 0);
            var framesSentDelta = outboundFramesSentTotal - Number(prev.framesSent || 0);
            if (elapsedSeconds > 0) {
              if (capturedFramesDelta >= 0) {
                adaptiveSample.senderCapturedFps = Math.max(
                  adaptiveSample.senderCapturedFps,
                  capturedFramesDelta / elapsedSeconds
                );
              }
              if (framesEncodedDelta >= 0) {
                adaptiveSample.senderEncodedFps = Math.max(
                  adaptiveSample.senderEncodedFps,
                  framesEncodedDelta / elapsedSeconds
                );
              }
            }
            if (elapsedSeconds > 0 && framesSentDelta >= 0) {
              adaptiveSample.senderSentFps = Math.max(
                adaptiveSample.senderSentFps,
                framesSentDelta / elapsedSeconds
              );
            }
          }
        }
      }

      pc._gcAdaptivePrev = {
        bytesSent: outboundBytesTotal,
        packetsSent: outboundPacketsSentTotal,
        remotePacketsLost: remotePacketsLostTotal,
        framesCaptured: capturedFramesTotal,
        framesEncoded: outboundFramesEncodedTotal,
        framesSent: outboundFramesSentTotal,
        pairId: adaptiveSample.pairId,
        ts: now
      };
      var evaluation = GameCastWebRTCAdaptive.evaluateAdaptiveSample(adaptiveState, adaptiveSample);
      adaptiveState = evaluation.state;
      pc._gcAdaptiveState = adaptiveState;
      pc._gcAdaptiveLastSample = adaptiveSample;
      var pathChanged = previousAdaptivePath !== adaptiveSample.path;
      if (evaluation.decision) {
        var adaptiveDecision = evaluation.decision.type + ':' +
          evaluation.decision.fromLevel + '->' + evaluation.decision.toLevel;
        pc._gcAdaptivePendingDecision = adaptiveDecision;
        applyAdaptiveProfile(pc, adaptiveDecision);
      } else if (pathChanged) {
        applyAdaptiveProfile(pc, 'path:' + previousAdaptivePath + '->' + adaptiveSample.path);
      }
    }).catch(function () {}).finally(function () {
      pc._gcAdaptiveFetchPromise = null;
    });
  }

  function pauseAdaptiveStreamingEvidence() {
    if (
      typeof GameCastWebRTCAdaptive === 'undefined' ||
      typeof GameCastWebRTCAdaptive.pauseAdaptiveState !== 'function'
    ) return;
    getAdaptivePeerDescriptors().forEach(function (descriptor) {
      if (!descriptor.pc._gcAdaptiveState) return;
      descriptor.pc._gcAdaptiveState = GameCastWebRTCAdaptive.pauseAdaptiveState(
        descriptor.pc._gcAdaptiveState
      );
    });
  }

  function sampleAdaptiveStreaming() {
    if (!activeDisplayStream || webPresenceRecoveryPromise) return;
    refreshAdaptiveQualitySnapshot().finally(function () {
      if (!activeDisplayStream || webPresenceRecoveryPromise) return;
      var descriptors = getAdaptivePeerDescriptors();
      var budgetedPeerCount = countServerBudgetedAdaptivePeers(descriptors);
      if (budgetedPeerCount !== adaptiveServerBudgetPeerCount) {
        var previousBudgetedPeerCount = adaptiveServerBudgetPeerCount;
        adaptiveServerBudgetPeerCount = budgetedPeerCount;
        descriptors.forEach(function (descriptor) {
          if (!isServerBudgetedAdaptivePeer(descriptor)) return;
          applyAdaptiveProfile(
            descriptor.pc,
            'server-budget:' + previousBudgetedPeerCount + '->' + budgetedPeerCount
          );
        });
      }
      descriptors.forEach(sampleAdaptiveStreamingPeer);
    });
  }

  function startAdaptiveStreamingMonitor() {
    if (adaptiveStreamingMonitorTimer) return;
    adaptiveStreamingMonitorTimer = setInterval(sampleAdaptiveStreaming, 2000);
  }

  function buildTransportLabel(baseConnType, mode) {
    if (mode === 'sfu-host') return baseConnType === 'TURN 中继' ? 'SFU 上行 (TURN)' : 'SFU 上行 (直连服务器)';
    if (mode === 'sfu-viewer') return baseConnType === 'TURN 中继' ? 'SFU 下行 (TURN)' : 'SFU 下行 (直连服务器)';
    return baseConnType;
  }

  function getSfuStatusLabel() {
    if (!currentSfuMode() && !sfuConnection) return '未启用';
    return sfuStatusText || (currentSfuMode() ? '连接中...' : '未启用');
  }

  function postRoomState(active, roomOverride) {
    var roomId = roomOverride || currentRoomId();
    if (!roomId || !canSendProtectedControl()) return Promise.resolve(null);
    var unified = isUnifiedSfuRoom(lastRoomState);
    var payload = {
      room: roomId,
      active: !!active,
      sharing: !!active && !!activeDisplayStream && !unified,
      sfu: false,
      audioMode: active ? effectiveWebShareAudioMode() : 'off',
      ttl: ROOM_STATE_TTL
    };
    var signature = JSON.stringify(payload);
    var now = Date.now();
    if (signature === lastRoomStatePostSignature && now - lastRoomStatePostAt < ROOM_STATE_HEARTBEAT_MS) {
      return Promise.resolve(lastRoomState);
    }
    lastRoomStatePostSignature = signature;
    lastRoomStatePostAt = now;

    return controlRequest(ROOM_STATE_URL, {
      method: 'POST',
      body: JSON.stringify(payload)
    }).then(function (data) {
      if (data && data.state) rememberRoomState(data.state);
      return data;
    }).catch(function () {
      return null;
    });
  }

  function sendRoomStateBeacon(active, roomOverride) {
    var roomId = roomOverride || currentRoomId();
    if (!roomId || !navigator.sendBeacon || !canSendProtectedControl()) return;
    var unified = isUnifiedSfuRoom(lastRoomState);
    try {
      navigator.sendBeacon(ROOM_STATE_URL, JSON.stringify({
        room: roomId,
        active: !!active,
        sharing: !!active && !!activeDisplayStream && !unified,
        sfu: false,
        audioMode: active ? effectiveWebShareAudioMode() : 'off',
        ttl: ROOM_STATE_TTL
      }));
    } catch (e) {}
  }

  function startRoomStateHeartbeat() {
    var roomId = currentRoomId();
    if (!canMaintainRoomState(roomId)) return;
    clearRoomStateHeartbeat();
    roomStateHeartbeatRoomId = roomId;
    postRoomState(true, roomId);
    roomStateHeartbeatTimer = setInterval(function () {
      var activeRoomId = currentRoomId();
      if (!canMaintainRoomState(activeRoomId)) {
        stopRoomStateHeartbeat();
        return;
      }
      roomStateHeartbeatRoomId = activeRoomId;
      postRoomState(true, activeRoomId);
    }, ROOM_STATE_HEARTBEAT_MS);
  }

  function stopRoomStateHeartbeat() {
    var roomId = roomStateHeartbeatRoomId || currentRoomId();
    clearRoomStateHeartbeat();
    roomStateHeartbeatRoomId = '';
    if (roomId) postRoomState(false, roomId);
  }

  function fetchRoomState(roomId, force) {
    if (!roomId) return Promise.resolve(null);
    if (roomStateFetchPromise && !force && roomStateFetchRoomId === roomId) return roomStateFetchPromise;

    roomStateFetchRoomId = roomId;
    roomStateFetchPromise = fetch(ROOM_STATE_URL + '?room=' + encodeURIComponent(roomId), {
      credentials: 'same-origin',
      cache: 'no-store'
    }).then(function (r) { return r.json(); }).then(function (data) {
      if (data && data.ok && data.state) {
        return rememberRoomState(data.state);
      }
      return rememberRoomState({
        room: roomId,
        active: false,
        mediaMode: '',
        mediaProtocolVersion: 0,
        sharing: false,
        sharePhase: 'idle',
        sfu: false
      });
    }).catch(function () {
      return lastRoomState && lastRoomState.room === roomId ? lastRoomState : null;
    }).finally(function () {
      roomStateFetchPromise = null;
      roomStateFetchRoomId = '';
    });

    return roomStateFetchPromise;
  }

  function syncViewerSfuState(roomId, amStreamer) {
    var remoteActive = roomStateSfuActive(roomId);
    var shareOwnedByThisTab = !!(
      lastRoomState && lastRoomState.shareOwnerParticipantId === getWebParticipantId()
    );
    if (remoteActive && !shareOwnedByThisTab && !amStreamer && !activeDisplayStream) {
      if (!sfuConnection) {
        console.log('[SFU] 房主已开启 SFU，观看者自动接入');
        return sfuConnect(roomId, 'viewer', null);
      }
      return Promise.resolve(sfuConnection);
    }

    if ((!remoteActive || amStreamer) && sfuConnection && !activeDisplayStream) {
      sfuDisconnect();
    }
    return Promise.resolve(null);
  }

  function shouldDropNativePeerMessage(event) {
    if (!event || typeof event.data !== 'string') return false;
    var msg;
    try { msg = JSON.parse(event.data); } catch (e) { return false; }
    if (msg.type === 'hostsession' || msg.type === 'clientsession') {
      return window.__PARTYLAN_SFU_ONLY__ === true;
    }
    return false;
  }

  function getStatsPeerDescriptors() {
    var descriptors = [];
    var seen = [];
    var sfuPc = getSfuPeerConnection();
    if (peerConnectionLooksUsable(sfuPc)) {
      var role = getSfuRole();
      descriptors.push({ pc: sfuPc, mode: role === 'host' ? 'sfu-host' : 'sfu-viewer' });
      seen.push(sfuPc);
    }
    activePCs.forEach(function (pc) {
      if (!peerConnectionLooksUsable(pc) || seen.indexOf(pc) >= 0) return;
      descriptors.push(buildNativePeerDescriptor(pc));
      seen.push(pc);
    });
    return descriptors;
  }

  function markPeerStatsChange(field) {
    getStatsPeerDescriptors().forEach(function (descriptor) {
      if (descriptor && descriptor.pc) descriptor.pc[field] = true;
    });
  }

  function roomIdFromStreamEvent(event) {
    if (!event || typeof event.data !== 'string') return '';
    var message;
    try { message = JSON.parse(event.data); } catch (e) { return ''; }
    if (!message || message.type !== 'room') return '';
    return String(message.payload && message.payload.id || '').trim();
  }

  function promoteRoomSocket(socket, roomId) {
    if (!socket || socket._gcRoomConfirmed || !roomId) return;
    socket._gcRoomConfirmed = true;
    socket._gcRoomId = roomId;

    var previous = activeRoomSocket;
    activeRoomSocket = socket;
    activeRoomSocketRoomId = roomId;

    if (previous && previous !== socket) {
      try {
        if (previous.readyState === NativeWebSocket.OPEN || previous.readyState === NativeWebSocket.CONNECTING) {
          previous._gcSilentClose = true;
          previous.close(1000, '');
        }
      } catch (e) {}
    }

  }

  function shouldSilenceSocketClose(socket, event) {
    if (socket && socket._gcSilentClose) return true;
    var code = event && typeof event.code === 'number' ? event.code : 0;
    var reason = event && typeof event.reason === 'string' ? event.reason.trim() : '';
    return !reason && (code === 1000 || code === 1001);
  }

  function sanitizeSocketCloseEvent(event) {
    var reason = event && typeof event.reason === 'string' ? event.reason.trim() : '';
    return {
      code: event && typeof event.code === 'number' ? event.code : 0,
      reason: reason || '连接已断开，请稍后重试。',
      wasClean: !!(event && event.wasClean),
      originalEvent: event
    };
  }

  function sanitizeSocketErrorEvent(event) {
    return {
      originalEvent: event,
      toString: function () {
        return '网络连接出现异常，请稍后重试。';
      }
    };
  }

  function wrapStreamHandler(handler, socket) {
    if (typeof handler !== 'function') return handler;
    return function (event) {
      var roomId = roomIdFromStreamEvent(event);
      if (roomId) promoteRoomSocket(socket, roomId);
      if (shouldDropNativePeerMessage(event)) return;
      return handler.call(socket, event);
    };
  }

  function wrapSocketCloseHandler(handler, socket) {
    if (typeof handler !== 'function') return handler;
    return function (event) {
      if (shouldSilenceSocketClose(socket, event)) return;
      return handler.call(socket, sanitizeSocketCloseEvent(event));
    };
  }

  function wrapSocketErrorHandler(handler, socket) {
    if (typeof handler !== 'function') return handler;
    return function (event) {
      var now = Date.now();
      if (socket && socket._gcLastErrorToastAt && now - socket._gcLastErrorToastAt < 1200) return;
      if (socket) socket._gcLastErrorToastAt = now;
      return handler.call(socket, sanitizeSocketErrorEvent(event));
    };
  }

  function decorateStreamSocket(socket) {
    if (!socket || socket._gcStreamPatched) return;
    socket._gcStreamPatched = true;

    socket.addEventListener('close', function () {
      if (activeRoomSocket === socket) {
        var closedRoomId = activeRoomSocketRoomId || socket._gcRoomId || '';
        activeRoomSocket = null;
        activeRoomSocketRoomId = '';
        if (!roomIdFromLocation() && webPresenceRoom && sameRoomId(webPresenceRoom, closedRoomId)) {
          stopRoomStateHeartbeat();
          leaveWebPresence(false);
          clearRoomMediaMode();
        }
      }
    });

    var proto = NativeWebSocket && NativeWebSocket.prototype;
    var onMessageDesc = proto ? Object.getOwnPropertyDescriptor(proto, 'onmessage') : null;
    var onCloseDesc = proto ? Object.getOwnPropertyDescriptor(proto, 'onclose') : null;
    var onErrorDesc = proto ? Object.getOwnPropertyDescriptor(proto, 'onerror') : null;
    if (onMessageDesc && onMessageDesc.set) {
      Object.defineProperty(socket, 'onmessage', {
        configurable: true,
        enumerable: true,
        get: function () {
          return onMessageDesc.get ? onMessageDesc.get.call(socket) : null;
        },
        set: function (handler) {
          onMessageDesc.set.call(socket, wrapStreamHandler(handler, socket));
        }
      });
    }
    if (onCloseDesc && onCloseDesc.set) {
      Object.defineProperty(socket, 'onclose', {
        configurable: true,
        enumerable: true,
        get: function () {
          return onCloseDesc.get ? onCloseDesc.get.call(socket) : null;
        },
        set: function (handler) {
          onCloseDesc.set.call(socket, wrapSocketCloseHandler(handler, socket));
        }
      });
    }
    if (onErrorDesc && onErrorDesc.set) {
      Object.defineProperty(socket, 'onerror', {
        configurable: true,
        enumerable: true,
        get: function () {
          return onErrorDesc.get ? onErrorDesc.get.call(socket) : null;
        },
        set: function (handler) {
          onErrorDesc.set.call(socket, wrapSocketErrorHandler(handler, socket));
        }
      });
    }

    if (socket.addEventListener) {
      var origAddEventListener = socket.addEventListener.bind(socket);
      socket.addEventListener = function (type, listener, options) {
        if (type === 'message') {
          return origAddEventListener(type, wrapStreamHandler(listener, socket), options);
        }
        if (type === 'close') {
          return origAddEventListener(type, wrapSocketCloseHandler(listener, socket), options);
        }
        if (type === 'error') {
          return origAddEventListener(type, wrapSocketErrorHandler(listener, socket), options);
        }
        return origAddEventListener(type, listener, options);
      };
    }
  }

  function patchWebSocket() {
    if (!NativeWebSocket || window.WebSocket && window.WebSocket._gcWrapped) return;

    function WrappedWebSocket(url, protocols) {
      var socket = protocols === undefined ? new NativeWebSocket(url) : new NativeWebSocket(url, protocols);
      if (typeof url === 'string' && url.indexOf('/stream') >= 0) {
        decorateStreamSocket(socket);
      }
      return socket;
    }

    WrappedWebSocket.prototype = NativeWebSocket.prototype;
    WrappedWebSocket._gcWrapped = true;
    WrappedWebSocket.CONNECTING = NativeWebSocket.CONNECTING;
    WrappedWebSocket.OPEN = NativeWebSocket.OPEN;
    WrappedWebSocket.CLOSING = NativeWebSocket.CLOSING;
    WrappedWebSocket.CLOSED = NativeWebSocket.CLOSED;
    window.WebSocket = WrappedWebSocket;
  }

  function closeActivePeerConnections(reason) {
    activePCs.slice().forEach(function (pc) {
      cleanupPeerConnectionState(pc, true, reason || 'share-stop');
    });
    screegoPeerSessions = Object.create(null);
    clearAdaptiveQualitySnapshot();
  }

  function closeRoomSocket() {
    if (!activeRoomSocket) return;
    try {
      if (activeRoomSocket.readyState === NativeWebSocket.OPEN || activeRoomSocket.readyState === NativeWebSocket.CONNECTING) {
        activeRoomSocket._gcSilentClose = true;
        activeRoomSocket.close(1000, '');
      }
    } catch (e) {}
    activeRoomSocket = null;
    activeRoomSocketRoomId = '';
  }

  /* ── 用户名 ── */
  function applyConfigState(data) {
    var nextUser = ((data && data.user) || '').trim() || 'guest';
    var nextLoggedIn = !!(data && data.loggedIn) && nextUser !== 'guest';
    var prevUser = currentUser || 'guest';
    var prevLoggedIn = prevUser !== 'guest';
    var userChanged = nextUser !== prevUser;
    var loginStateChanged = nextLoggedIn !== prevLoggedIn;

    currentUser = nextUser;
    lastKnownAuthUser = nextUser;
    lastKnownLoggedIn = nextLoggedIn;

    if (nextLoggedIn) {
      var cfg = loadCfg();
      if (cfg.name !== currentUser) {
        cfg.name = currentUser;
        saveCfg(cfg);
      }
      var confirmedRoomId = activeRoomSocketRoomId;
      var confirmedCurrentRoom = confirmedRoomId && sameRoomId(confirmedRoomId, currentRoomId());
      if (confirmedCurrentRoom && (userChanged || loginStateChanged || webPresenceName !== currentUser)) {
        ensureWebPresence(confirmedRoomId).then(function (presence) {
          if (presence) startRoomStateHeartbeat();
        });
      } else if (confirmedCurrentRoom) {
        startRoomStateHeartbeat();
      }
      return;
    }

    stopRoomStateHeartbeat();
  }
  function fetchUsername() {
    return fetch(location.origin + '/config', { credentials: 'same-origin' })
      .then(function (r) { return r.json(); })
      .then(function (data) {
        applyConfigState(data);
        return data;
      }).catch(function () {
        if (!currentUser) currentUser = 'guest';
        return null;
      });
  }
  function startAuthStateMonitor() {
    if (authStateMonitorTimer) return;
    authStateMonitorTimer = setInterval(function () {
      fetchUsername();
    }, 5000);
  }
  /* ── SFU 模式 ── */
  function sfuConnect(roomId, role, stream) {
    if (typeof GC_SFU === 'undefined') {
      console.warn('[SFU] GC_SFU 未加载');
      var missingClient = new Error('sfu_unavailable');
      missingClient.code = 'sfu_unavailable';
      return role === 'host' ? abortWebShare(missingClient) : Promise.resolve(null);
    }
    if (sfuConnection && GC_SFU.isConnected() && getSfuRole() === role) {
      console.log('[SFU] 已连接，跳过');
      return role === 'host' && typeof GC_SFU.waitUntilMediaReady === 'function'
        ? GC_SFU.waitUntilMediaReady(SFU_SHARE_START_TIMEOUT_MS).then(function () {
          return heartbeatWebShare('live');
        })
        : Promise.resolve(GC_SFU);
    }
    if (sfuConnection) sfuDisconnect();
    return Promise.resolve().then(function () {
      if (stream && role === 'host') GC_SFU.shareScreen(stream);
      GC_SFU.connect(roomId, role, {
        name: currentUser || loadCfg().name || role,
        onRemoteStream: function (remoteStream) {
          showSfuViewer(remoteStream);
          showSfuStatus('连接成功，正在接收画面');
        },
        onStatus: function (msg) {
          showSfuStatus(msg);
          if (role === 'host' && /^错误:\s*(?:codec_unavailable|sfu_unavailable|share_start_timeout)/.test(String(msg || ''))) {
            var fatal = new Error(String(msg).replace(/^错误:\s*/, '') || 'sfu_unavailable');
            fatal.code = fatal.message;
            abortWebShare(fatal);
          }
        }
      });
      sfuConnection = GC_SFU;
      if (role !== 'host') return GC_SFU;
      if (typeof GC_SFU.waitUntilMediaReady !== 'function') {
        var unsupported = new Error('sfu_unavailable');
        unsupported.code = 'sfu_unavailable';
        throw unsupported;
      }
      startRoomStateHeartbeat();
      return GC_SFU.waitUntilMediaReady(SFU_SHARE_START_TIMEOUT_MS).then(function () {
        return heartbeatWebShare('live');
      });
    }).catch(function (error) {
      showSfuStatus('错误: ' + (error && error.code || 'sfu_unavailable'));
      return role === 'host' ? abortWebShare(error).then(function () { return null; }) : null;
    });
  }

  function sfuDisconnect() {
    if (sfuConnection) {
      sfuConnection.disconnect();
      sfuConnection = null;
    }
    hideSfuViewer();
  }

  function showSfuStatus(msg) {
    sfuStatusText = msg || '未知';
  }

  function ensureSfuViewer() {
    var shell = document.getElementById('gc-sfu-viewer-shell');
    var video = document.getElementById('gc-sfu-viewer-video');
    if (shell && video) return { shell: shell, video: video };

    shell = document.createElement('div');
    shell.id = 'gc-sfu-viewer-shell';
    shell.style.cssText = 'position:fixed;inset:0;z-index:20;background:#000;display:none;align-items:center;justify-content:center';

    video = document.createElement('video');
    video.id = 'gc-sfu-viewer-video';
    video.autoplay = true;
    video.playsInline = true;
    video.controls = false;
    video.style.cssText = 'width:100%;height:100%;object-fit:contain;background:#000';

    shell.appendChild(video);
    document.body.appendChild(shell);
    return { shell: shell, video: video };
  }

  function showSfuViewer(stream) {
    var viewer = ensureSfuViewer();
    viewer.video.srcObject = stream;
    viewer.video.play().catch(function () {});
    viewer.shell.style.display = 'flex';
  }

  function hideSfuViewer() {
    var shell = document.getElementById('gc-sfu-viewer-shell');
    var video = document.getElementById('gc-sfu-viewer-video');
    if (video) {
      try { video.pause(); } catch (e) {}
      video.srcObject = null;
    }
    if (shell) shell.style.display = 'none';
  }

  function monitorSfuMode() {
    function tick() {
      var roomId = currentRoomId();
      var inRoom = !!roomId;
      if (!inRoom) {
        if (activeDisplayStream) stopRoomStateHeartbeat();
        if (!activeDisplayStream) closeActivePeerConnections('room-exit');
        resetAdaptiveStreamingState();
        if (activeDisplayStream || webShareLease) stopWebShare(false);
        else sfuDisconnect();
        lastRoomState = null;
        clearRoomMediaMode();
        closeRoomSocket();
        sfuStatusText = '???';
        return;
      }

      refreshWebPresence();

      fetchRoomState(roomId, true).then(function () {
        if (activeDisplayStream) {
          syncHostSfuState(roomId);
          return;
        }
        syncViewerSfuState(roomId, false);
      });
    }
    tick();
    setInterval(tick, PRESENCE_REFRESH_MS);
  }

  /* ── 画质补丁 ── */
  var activeVideoTrack = null;
  var activeDisplayStream = null;
  var activePCs = [];

  function buildAudioConstraints(baseAudio, mode) {
    if (mode === 'off') return false;

    var audio = {};
    if (baseAudio && typeof baseAudio === 'object') {
      for (var key in baseAudio) audio[key] = baseAudio[key];
    }

    audio.echoCancellation = false;
    audio.autoGainControl = false;
    audio.noiseSuppression = false;
    audio.googAutoGainControl = false;
    audio.suppressLocalAudioPlayback = false;
    return audio;
  }

  function frameRateFloorForTarget(fps) {
    return fps >= 45 ? 30 : 24;
  }

  function buildDisplayCaptureFrameRateHints(fps) {
    var target = normalizeFramerateValue(fps);
    return {
      ideal: target,
      max: target
    };
  }

  function buildLiveFrameRateConstraints(fps) {
    var target = normalizeFramerateValue(fps);
    return {
      ideal: target,
      max: target,
      min: frameRateFloorForTarget(target)
    };
  }

  function buildDisplayCapturePrefs(mode) {
    var preferences = GameCastWebShareAudioPolicy.buildCapturePreferences(mode);
    if (!preferences.requestAudio) return {};
    var result = {
      systemAudio: preferences.systemAudio,
      windowAudio: preferences.windowAudio,
      surfaceSwitching: preferences.surfaceSwitching
    };
    if (preferences.selfBrowserSurface) result.selfBrowserSurface = preferences.selfBrowserSurface;
    return result;
  }

  function getContentHint(mode) {
    if (mode === 'smooth') return 'motion';
    return 'detail';
  }

  function computeTargetBitrate() {
    return maxBitrate();
  }

  function stopDisplayStreamTracks(stream) {
    if (!stream || typeof stream.getTracks !== 'function') return;
    try {
      stream.getTracks().forEach(function (track) { track.stop(); });
    } catch (e) {}
  }

  function clearPrimedDisplayMediaRequest(request, stopStream) {
    if (!request) return;
    if (request.timeoutId) clearTimeout(request.timeoutId);
    if (primedDisplayMediaRequest && primedDisplayMediaRequest.id === request.id) {
      primedDisplayMediaRequest = null;
    }
    if (stopStream && request.stream) {
      stopDisplayStreamTracks(request.stream);
    }
  }

  function buildDisplayMediaRequest(constraints) {
    var c = extCfg();
    var vc = {};
    if (constraints && constraints.video && typeof constraints.video === 'object') {
      for (var k in constraints.video) vc[k] = constraints.video[k];
    }
    var rc = resConstraints();
    if (rc.width) {
      vc.width = rc.width;
      vc.height = rc.height;
    }
    vc.frameRate = buildDisplayCaptureFrameRateHints(c.fps || 30);

    var nc = {};
    if (constraints && typeof constraints === 'object') {
      for (var k2 in constraints) nc[k2] = constraints[k2];
    }
    nc.video = vc;
    nc.audio = buildAudioConstraints(constraints ? constraints.audio : null, c.audio);

    var displayPrefs = buildDisplayCapturePrefs(c.audio);
    for (var prefKey in displayPrefs) nc[prefKey] = displayPrefs[prefKey];

    return {
      constraints: nc,
      settings: c
    };
  }

  function buildCompatibilityDisplayMediaRequest(audioMode) {
    return {
      video: true,
      audio: audioMode !== 'off'
    };
  }

  function displayMediaErrorName(error) {
    return String(error && (error.name || error.code || '') || '').trim();
  }

  function isRecoverableDisplayMediaError(error) {
    var name = displayMediaErrorName(error);
    return name === 'TypeError' || name === 'OverconstrainedError' || name === 'NotReadableError';
  }

  function requestDisplayMediaWithCompatibilityFallback(preferredConstraints, settings, request) {
    var requester = request || nativeGetDisplayMedia;
    if (!requester) return Promise.reject(new Error('screen capture is not available'));
    var invoke = function (constraints) {
      return Promise.resolve().then(function () { return requester(constraints); });
    };
    return invoke(preferredConstraints).catch(function (preferredError) {
      if (!isRecoverableDisplayMediaError(preferredError)) throw preferredError;
      var mode = settings && settings.audio ? settings.audio : extCfg().audio;
      console.warn('[GameCast] 首选共享约束失败，尝试兼容捕获:', displayMediaErrorName(preferredError) || 'Error');
      return invoke(buildCompatibilityDisplayMediaRequest(mode)).catch(function (fallbackError) {
        try {
          fallbackError._gcPreferredCaptureError = displayMediaErrorName(preferredError);
        } catch (e) {}
        throw fallbackError;
      });
    });
  }

  function activateCapturedDisplayStream(stream, settings) {
    if (!stream) return stream;
    if (stream._gcCaptureActivated) return stream;

    var requestedAudioMode = settings && settings.audio ? settings.audio : extCfg().audio;
    var audioResult = GameCastWebShareAudioPolicy.sanitizeCapturedStream(stream, requestedAudioMode);
    stream = audioResult.stream;
    activeCaptureAudioMode = audioResult.effectiveMode;

    if (currentSfuMode()) {
      try {
        if (typeof GC_SFU === 'undefined' || !GC_SFU || typeof GC_SFU.validateMediaCodecs !== 'function') {
          var unavailable = new Error('sfu_unavailable');
          unavailable.code = 'sfu_unavailable';
          throw unavailable;
        }
        GC_SFU.validateMediaCodecs();
      } catch (error) {
        activeCaptureAudioMode = 'off';
        stopDisplayStreamTracks(stream);
        throw error;
      }
    }

    stream._gcCaptureActivated = true;
    markPeerStatsChange('_gcSourceChanged');
    activeDisplayStream = stream;
    if (webShareLease) heartbeatWebShare();
    resetAdaptiveStreamingState();

    var vt = stream.getVideoTracks()[0];
    if (vt) {
      try { vt.contentHint = getContentHint(settings && settings.qm ? settings.qm : extCfg().qm); } catch (e) {}
      activeVideoTrack = vt;
      if (!vt._gcUnifiedStopWrapped && typeof vt.stop === 'function') {
        var nativeStop = vt.stop.bind(vt);
        vt.stop = function () {
          var result = nativeStop();
          if (activeDisplayStream === stream) stopWebShare(false);
          return result;
        };
        vt._gcUnifiedStopWrapped = true;
      }
      vt.addEventListener('ended', function () {
        if (activeDisplayStream === stream) stopWebShare(false);
      }, { once: true });
    }

    if (currentSfuMode()) {
      var roomId = (location.search.match(/[?&]room=([^&]+)/) || [])[1] || '';
      if (roomId) sfuConnect(roomId, 'host', stream);
    }
    return stream;
  }

  function primeDisplayMediaRequestForShare() {
    if (!nativeGetDisplayMedia) return null;

    if (primedDisplayMediaRequest && primedDisplayMediaRequest.expiresAt > Date.now()) {
      return primedDisplayMediaRequest.promise;
    }
    if (primedDisplayMediaRequest) {
      clearPrimedDisplayMediaRequest(primedDisplayMediaRequest, true);
    }

    var request = {
      id: ++primedDisplayMediaRequestSeq,
      consumed: false,
      expiresAt: Date.now() + 15000,
      timeoutId: 0,
      stream: null
    };
    var built = buildDisplayMediaRequest();

    request.promise = requestDisplayMediaWithCompatibilityFallback(built.constraints, built.settings).then(function (stream) {
      request.stream = stream || null;
      return stream;
    }).catch(function (error) {
      clearPrimedDisplayMediaRequest(request, false);
      releaseWebShare(false);
      throw error;
    });

    request.timeoutId = setTimeout(function () {
      if (!request.consumed) clearPrimedDisplayMediaRequest(request, true);
    }, 15000);

    primedDisplayMediaRequest = request;
    return request.promise;
  }

  function consumePrimedDisplayMediaRequest() {
    var request = primedDisplayMediaRequest;
    if (!request) return null;
    if (request.expiresAt <= Date.now()) {
      clearPrimedDisplayMediaRequest(request, true);
      return null;
    }
    request.consumed = true;
    clearPrimedDisplayMediaRequest(request, false);
    return request.promise;
  }

  function patchGDM() {
    if (!navigator.mediaDevices || !navigator.mediaDevices.getDisplayMedia) return;
    nativeGetDisplayMedia = navigator.mediaDevices.getDisplayMedia.bind(navigator.mediaDevices);
    navigator.mediaDevices.getDisplayMedia = function (constraints) {
      var nativeUiShare = window.__PARTYLAN_NATIVE_UI_SHARE__ === true;
      if (nativeUiShare) {
        var nativeBuilt = buildDisplayMediaRequest(constraints);
        return requestDisplayMediaWithCompatibilityFallback(nativeBuilt.constraints, nativeBuilt.settings).then(function (stream) {
          return claimWebShare().then(function () {
            try {
              return activateCapturedDisplayStream(stream, nativeBuilt.settings);
            } catch (activationError) {
              stopDisplayStreamTracks(stream);
              return Promise.reject(activationError);
            }
          });
        }).catch(function (error) {
          releaseWebShare(false);
          throw error;
        });
      }
      var leasePromise = currentRoomId() && !webShareLease
        ? Promise.reject(new Error('share_locked'))
        : Promise.resolve(null);
      if (currentRoomId() && !webShareLease) return leasePromise;
      var primedPromise = consumePrimedDisplayMediaRequest();
      return leasePromise.then(function () {
        if (primedPromise) {
          var primedSettings = extCfg();
          return primedPromise.then(function (stream) {
            return activateCapturedDisplayStream(stream, primedSettings);
          });
        }

        var built = buildDisplayMediaRequest(constraints);
        return requestDisplayMediaWithCompatibilityFallback(built.constraints, built.settings).then(function (stream) {
          return activateCapturedDisplayStream(stream, built.settings);
        });
      }).catch(function (error) {
        releaseWebShare(false);
        throw error;
      });
    };
  }

  function patchRTC() {
    var Orig = window.RTCPeerConnection;
    function P(cfg) {
      var pc = new Orig(cfg);
      pc._gcQualityState = typeof GameCastWebRTCQuality !== 'undefined'
        ? GameCastWebRTCQuality.createQualityState(Date.now())
        : null;
      pc._gcAdaptiveState = createCurrentAdaptiveState(Date.now());
      pc._gcAdaptiveProfile = null;
      pc._gcAdaptivePendingDecision = '';
      pc._gcAdaptiveConnected = false;
      pc._gcProtectionMaxWidth = 0;
      pc._gcProtectionMaxHeight = 0;
      pc._gcProtectionMaxBitrate = 0;
      pc._gcProtectionRecoveryStep = 0;
      pc._gcProtectionChangedAt = 0;
      pc.__gamecastHandleIceRecoveryRequest = function (action) {
        var now = Date.now();
        if (action === 'restart' || action === 'protect') {
          pc._gcProtectionMaxWidth = 1280;
          pc._gcProtectionMaxHeight = 720;
          pc._gcProtectionMaxBitrate = 3000000;
          pc._gcProtectionRecoveryStep = 0;
          pc._gcProtectionChangedAt = now;
          return applyBitrate(pc).then(function () {
            if (action !== 'restart' || typeof pc.__gamecastRestartIce !== 'function') return true;
            return pc.__gamecastRestartIce();
          });
        }
        if (action !== 'recover' || !pc._gcProtectionMaxBitrate) return Promise.resolve(false);
        if (now - pc._gcProtectionChangedAt < RECOVERY_STABLE_MS) return Promise.resolve(false);
        var targetBitrate = computeTargetBitrate();
        var step = pc._gcProtectionRecoveryStep || 0;
        var nextBitrate = step < RECOVERY_BITRATE_STEPS.length
          ? Math.min(targetBitrate, RECOVERY_BITRATE_STEPS[step])
          : targetBitrate;
        pc._gcProtectionRecoveryStep = step + 1;
        pc._gcProtectionChangedAt = now;
        if (nextBitrate >= targetBitrate) {
          pc._gcProtectionMaxWidth = 0;
          pc._gcProtectionMaxHeight = 0;
          pc._gcProtectionMaxBitrate = 0;
        } else {
          pc._gcProtectionMaxBitrate = nextBitrate;
        }
        return applyBitrate(pc);
      };
      pc._gcStatsSnapshot = null;
      activePCs.push(pc);
      pc.addEventListener('connectionstatechange', function () {
        if (pc.connectionState === 'connected' && !pc._gcAdaptiveConnected) {
          pc._gcAdaptiveConnected = true;
          pc._gcAdaptiveState = createCurrentAdaptiveState(Date.now());
          pc._gcAdaptivePrev = null;
        }
        if (pc.connectionState === 'connected') {
          pc._gcDisconnectedSince = 0;
          if (pc._gcDisconnectedTimer) {
            clearTimeout(pc._gcDisconnectedTimer);
            pc._gcDisconnectedTimer = 0;
          }
        } else if (pc.connectionState === 'disconnected' && !pc._gcDisconnectedTimer) {
          pc._gcDisconnectedSince = Date.now();
          pc._gcDisconnectedTimer = setTimeout(function () {
            pc._gcDisconnectedTimer = 0;
            if (
              !pc._gcLifecycleCleaned &&
              String(pc.connectionState || '').toLowerCase() === 'disconnected' &&
              Date.now() - pc._gcDisconnectedSince >= PEER_DISCONNECTED_GRACE_MS
            ) cleanupPeerConnectionState(pc, true, 'disconnected-timeout');
          }, PEER_DISCONNECTED_GRACE_MS);
        }
        if (['closed', 'failed'].indexOf(pc.connectionState) >= 0) {
          cleanupPeerConnectionState(pc, false, 'connection-' + pc.connectionState);
        }
        setTimeout(applyAll, 500);
      });
      var origAdd = pc.addTrack.bind(pc);
      pc.addTrack = function () {
        var r = origAdd.apply(pc, arguments);
        setTimeout(function () { applyBitrate(pc); applyAll(); }, 600);
        return r;
      };
      return pc;
    }
    P.prototype = Orig.prototype;
    if (Orig.generateCertificate) P.generateCertificate = Orig.generateCertificate;
    window.RTCPeerConnection = P;
  }

  function applyBitrate(pc) {
    if (browserBWEBaselineEnabled()) return Promise.resolve(false);
    var c = extCfg();
    var targetBitrate = computeTargetBitrate();
    var profile = computePeerAdaptiveProfile(pc) || {
      level: 0,
      maxWidth: (RESOLUTIONS[c.res] || {}).w || 1280,
      maxHeight: (RESOLUTIONS[c.res] || {}).h || 720,
      maxFramerate: c.fps || 30,
      maxBitrate: targetBitrate,
      scaleResolutionDownBy: 1
    };
    var updates = [];
    pc.getSenders().forEach(function (sender) {
      if (sender.track && sender.track.kind === 'video') {
        try {
          var p = sender.getParameters();
          if (!p.encodings || !p.encodings.length) p.encodings = [{}];
          if (p.encodings.length > 1) {
            p.encodings.forEach(function (encoding, index) {
              var rid = encoding && encoding.rid ? encoding.rid : '';
              if (rid === 'q' || index === 0) {
                encoding.maxBitrate = Math.max(250000, Math.round(profile.maxBitrate * 0.24));
                encoding.maxFramerate = Math.min(profile.maxFramerate, 24);
                encoding.scaleResolutionDownBy = Math.max(2, profile.scaleResolutionDownBy);
              } else if (rid === 'h' || index === 1) {
                encoding.maxBitrate = Math.max(500000, Math.round(profile.maxBitrate * 0.6));
                encoding.maxFramerate = profile.maxFramerate;
                encoding.scaleResolutionDownBy = Math.max(1.5, profile.scaleResolutionDownBy);
              } else {
                encoding.maxBitrate = profile.maxBitrate;
                encoding.maxFramerate = profile.maxFramerate;
                encoding.scaleResolutionDownBy = profile.scaleResolutionDownBy;
              }
            });
          } else {
            var encoding = p.encodings[0];
            encoding.maxBitrate = profile.maxBitrate;
            encoding.maxFramerate = profile.maxFramerate;
            encoding.scaleResolutionDownBy = profile.scaleResolutionDownBy;
          }
          p.encodings[0].priority = 'high';
          p.degradationPreference = 'maintain-framerate';
          try {
            if (sender.track && 'contentHint' in sender.track) {
              sender.track.contentHint = getContentHint(c.qm);
            }
          } catch (e2) {}
          updates.push(sender.setParameters(p).catch(function (error) {
            console.warn('[GameCast] 逐观众编码参数应用失败:', error && error.name || 'Error');
            return false;
          }));
        } catch (e) {}
      }
    });
    return Promise.all(updates).then(function () { return true; });
  }

  function requestPeerIceRecovery(pc, mode, action) {
    var mappedAction = action === 'protect_and_restart_ice'
      ? 'restart'
      : action === 'protect_bitrate'
        ? 'protect'
        : action === 'recover_bitrate'
          ? 'recover'
          : '';
    if (!mappedAction) return false;
    if (mode === 'native-viewer' && typeof pc.__gamecastRequestIceRecovery === 'function') {
      return pc.__gamecastRequestIceRecovery(mappedAction);
    }
    if (mode === 'native-host' && typeof pc.__gamecastHandleIceRecoveryRequest === 'function') {
      void pc.__gamecastHandleIceRecoveryRequest(mappedAction).catch(function (error) {
        console.warn('[GameCast] ICE 恢复执行失败:', error && error.message || error);
      });
      return true;
    }
    return false;
  }

  function applyAll() { activePCs.forEach(function (pc) { if (pc.connectionState !== 'closed') applyBitrate(pc); }); }
  function applyLiveConstraints() {
    if (!activeVideoTrack || activeVideoTrack.readyState !== 'live') return '无活跃视频';
    var c = extCfg();
    var con = { frameRate: buildLiveFrameRateConstraints(c.fps) };
    var rc = resConstraints();
    if (rc.width) { con.width = rc.width; con.height = rc.height; }
    try {
      try { activeVideoTrack.contentHint = getContentHint(c.qm); } catch (e3) {}
      activeVideoTrack.applyConstraints(con).then(function () {
        markPeerStatsChange('_gcResolutionChanged');
        console.log('[GameCast] 约束已应用:', JSON.stringify(con));
      }).catch(function (e) {
        console.warn('[GameCast] 约束应用失败:', e.name, e.message);
      });
    } catch (e) {
      console.warn('[GameCast] 约束异常:', e);
    }
    return null;
  }

  /* ── 连接诊断 ── */
  function publishConnectionStats(text, summary) {
    try {
      window.dispatchEvent(new CustomEvent('partylan:connection-stats', {
        detail: {
          text: text || '等待连接数据...',
          summary: summary || '等待连接'
        }
      }));
    } catch (e) {}
  }

  function startConnectionStats() {
    setInterval(function () {
      if (!/[?&]room=/.test(location.search)) return;
      var descriptors = getStatsPeerDescriptors();
      if (!descriptors.length) publishConnectionStats('等待媒体连接建立...', '等待连接');
      descriptors.forEach(function (descriptor) {
        updateStats(descriptor);
      });
    }, 2000);
  }

  function reportStats(data) {
    try {
      return fetch(STATS_URL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data)
      }).then(function (response) {
        return !!response.ok;
      }, function () {
        return false;
      });
    } catch (e) {
      return Promise.resolve(false);
    }
  }

  function updateStats(descriptor) {
    if (!descriptor || !descriptor.pc) return;
    var pc = descriptor.pc;
    var mode = descriptor.mode;
    if (!peerConnectionCanReportStats(pc)) return;
    if (pc._gcStatsFetchPromise) return;

    var prevSentBytes = pc._prevSentBytes || 0;
    var prevRecvBytes = pc._prevRecvBytes || 0;
    var prevTime = pc._prevTime || Date.now();
    var requestSeq = ++statsFetchRequestSeq;
    var trackedPeerId = getTrackedPeerId(pc);

    var statsPromise = getStatsWithTimeout(pc, STATS_FETCH_TIMEOUT_MS);
    pc._gcStatsFetchPromise = statsPromise;

    statsPromise.then(function (stats) {
      var connType = '未知';
      var rtt = '-';
      var loss = '-';
      var codec = '-';
      var resolution = '-';
      var fps = '-';
      var jitter = '-';
      var kbps = '-';
      var bytesSentTotal = 0;
      var bytesRecvTotal = 0;
      var diagnosticSample = null;
      var qualityState = pc._gcQualityState;

      if (typeof GameCastWebRTCStats !== 'undefined') {
        diagnosticSample = GameCastWebRTCStats.buildWebRTCSample(stats, pc._gcStatsSnapshot, {
          now: Date.now(),
          targetBitrate: computeTargetBitrate(),
          visible: document.visibilityState !== 'hidden',
          generation: pc._gcIceGeneration || 0,
          sourceChanged: !!pc._gcSourceChanged,
          resolutionChanged: !!pc._gcResolutionChanged
        });
        pc._gcStatsSnapshot = diagnosticSample.snapshot;
        if (
          !pc._gcTelemetryReasonLatch &&
          typeof GameCastWebRTCStats.createTelemetryReasonLatch === 'function'
        ) {
          pc._gcTelemetryReasonLatch = GameCastWebRTCStats.createTelemetryReasonLatch();
        }
        if (
          pc._gcTelemetryReasonLatch && diagnosticSample.telemetryResetReason &&
          typeof GameCastWebRTCStats.recordTelemetryResetReasons === 'function'
        ) {
          GameCastWebRTCStats.recordTelemetryResetReasons(
            pc._gcTelemetryReasonLatch,
            diagnosticSample.telemetryResetReason
          );
        }
        pc._gcSourceChanged = false;
        pc._gcResolutionChanged = false;
        if (typeof GameCastWebRTCQuality !== 'undefined') {
          if (!qualityState) qualityState = GameCastWebRTCQuality.createQualityState(Date.now());
          qualityState = GameCastWebRTCQuality.evaluateQualitySample(qualityState, diagnosticSample).state;
          pc._gcQualityState = qualityState;
          if (qualityState.action) requestPeerIceRecovery(pc, mode, qualityState.action);
        }
      }

      stats.forEach(function (report) {
        if (report.type === 'candidate-pair' && report.state === 'succeeded' && report.nominated) {
          rtt = report.currentRoundTripTime ? (report.currentRoundTripTime * 1000).toFixed(0) + 'ms' : '-';
          var localC = stats.get(report.localCandidateId);
          var remoteC = stats.get(report.remoteCandidateId);
          if (localC && remoteC) {
            connType = (localC.candidateType === 'relay' || remoteC.candidateType === 'relay') ? 'TURN 中继' : 'P2P 直连';
          }
        }
        if (report.type === 'inbound-rtp' && report.kind === 'video') {
          var pl = report.packetsLost || 0;
          var pr = report.packetsReceived || 1;
          loss = (pl / pr * 100).toFixed(1) + '%';
          if (report.frameWidth) resolution = report.frameWidth + '×' + report.frameHeight;
          if (report.framesPerSecond) fps = report.framesPerSecond + 'fps';
          if (report.jitter) jitter = (report.jitter * 1000).toFixed(1) + 'ms';
          bytesRecvTotal += report.bytesReceived || 0;
          if (report.codecId) {
            var c = stats.get(report.codecId);
            if (c) codec = c.mimeType.replace('video/', '') + (c.clockRate ? ' ' + (c.clockRate / 1000) + 'kHz' : '');
          }
        }
        if (report.type === 'outbound-rtp' && report.kind === 'video') {
          if (report.frameWidth) resolution = report.frameWidth + '×' + report.frameHeight;
          if (report.framesPerSecond) fps = report.framesPerSecond + 'fps';
          bytesSentTotal += report.bytesSent || 0;
          if (report.codecId) {
            var c2 = stats.get(report.codecId);
            if (c2) codec = c2.mimeType.replace('video/', '');
          }
        }
      });
      if (diagnosticSample) {
        if (diagnosticSample.path === 'turn') {
          connType = 'TURN 中继/' + String(diagnosticSample.transport || 'unknown').toUpperCase();
        } else if (diagnosticSample.path === 'p2p') {
          connType = 'P2P 直连/' + String(diagnosticSample.transport || 'unknown').toUpperCase();
        }
      }

      var now = Date.now();
      var elapsed = (now - prevTime) / 1000;
      var upKbpsValue = null;
      var downKbpsValue = null;
      if (elapsed > 0) {
        upKbpsValue = Math.max(0, (bytesSentTotal - prevSentBytes) * 8 / elapsed / 1000);
        downKbpsValue = Math.max(0, (bytesRecvTotal - prevRecvBytes) * 8 / elapsed / 1000);
      }
      kbps = formatKbps((upKbpsValue || 0) > 0 ? upKbpsValue : downKbpsValue);
      pc._prevSentBytes = bytesSentTotal;
      pc._prevRecvBytes = bytesRecvTotal;
      pc._prevTime = now;

      if (
        diagnosticSample &&
        typeof GameCastWebRTCStats !== 'undefined' &&
        typeof GameCastWebRTCStats.buildViewerQualityReport === 'function' &&
        now - (pc._gcLastQualityReportAt || 0) >= 2000
      ) {
        var qualityReport = GameCastWebRTCStats.buildViewerQualityReport(diagnosticSample, {
          room: currentRoomId(),
          participantId: getWebParticipantId(),
          screegoSessionId: pc._gcScreegoSessionId || '',
          mode: mode
        });
        if (qualityReport && mode === 'native-viewer') {
          pc._gcLastQualityReportAt = now;
          controlRequest(QUALITY_REPORT_URL, {
            method: 'POST',
            body: JSON.stringify(qualityReport)
          }).catch(function () { return null; });
        }
      }

      var rttQuality = classifyRttQuality(rtt);
      var quality = rttQuality.text;
      var rttNum = rttQuality.value;
      pc._lastRtt = rtt + ' ' + quality;
      pc._lastConnType = buildTransportLabel(connType, mode);

      var targetKbpsValue = Math.round(computeTargetBitrate() / 1000);
      var adaptiveState = pc._gcAdaptiveState;
      var adaptiveProfile = computePeerAdaptiveProfile(pc);
      var adaptiveDecision = pc._gcAdaptivePendingDecision || '';
      var adaptiveTargetKbpsValue = Math.round(
        (adaptiveProfile && adaptiveProfile.maxBitrate ? adaptiveProfile.maxBitrate : computeTargetBitrate()) / 1000
      );
      var currentFlowKbps = Math.max(adaptiveTargetKbpsValue, upKbpsValue || 0, downKbpsValue || 0);
      var estimatedMembers = Math.max(1, getMemberCountEstimate());
      var estimatedViewers = Math.max(1, estimatedMembers - 1);
      var sfuSuggested = Math.max(1, Math.floor((SERVER_PROFILE.safeNetMbps * 1000) / Math.max(1, currentFlowKbps)));
      var turnSuggested = Math.max(1, Math.floor((SERVER_PROFILE.safeNetMbps * 1000) / Math.max(1, currentFlowKbps * 2)));
      var estimatedRelayLoadKbps = 0;
      if (mode === 'sfu-host' || mode === 'sfu-viewer') {
        estimatedRelayLoadKbps = currentFlowKbps * estimatedViewers;
      } else if (connType.indexOf('TURN 中继') === 0) {
        estimatedRelayLoadKbps = currentFlowKbps * estimatedViewers * 2;
      }
      var serverLoadRatio = estimatedRelayLoadKbps > 0 ? (estimatedRelayLoadKbps / (SERVER_PROFILE.safeNetMbps * 1000)) : 0;
      var serverNote = '余量充足';
      if (serverLoadRatio > 1) serverNote = '服务器带宽已超建议值';
      else if (serverLoadRatio > 0.8) serverNote = '服务器带宽接近上限';
      else if (serverLoadRatio > 0.6) serverNote = '服务器带宽压力中等';

      var statsText;
      if (mode === 'native-viewer' || mode === 'sfu-viewer') {
        statsText =
          '🛰 SFU: ' + getSfuStatusLabel() + '\n' +
          '📡 链路: ' + buildTransportLabel(connType, mode) + '  |  延迟: ' + rtt + '  ' + quality + '\n' +
          '📐 接收画面: ' + resolution + '  ' + fps + '  ' + codec + '\n' +
          '📊 实时接收: 下行 ' + formatKbps(downKbpsValue) + '\n' +
          '🧪 丢包: ' + loss + '  抖动: ' + jitter + '  Peer: ' + pc.connectionState + '\n' +
          '📝 说明: 当前为接收侧视角，仅显示你实际收到的链路与画面情况';
      } else {
        statsText =
          '🛰 SFU: ' + getSfuStatusLabel() + '\n' +
          '📡 链路: ' + buildTransportLabel(connType, mode) + '  |  延迟: ' + rtt + '  ' + quality + '\n' +
          '📐 发送画面: ' + resolution + '  ' + fps + '  ' + codec + '\n' +
          '📊 实时(浏览器): 上行 ' + formatKbps(upKbpsValue) + '  下行 ' + formatKbps(downKbpsValue) + '  目标 ' + formatKbps(adaptiveTargetKbpsValue) + '\n' +
          '🎛 自适应: ' + getAdaptiveStatusLabel(pc) + '\n' +
          '🧪 丢包: ' + loss + '  抖动: ' + jitter + '  Peer: ' + pc.connectionState + '\n' +
          '🖥 服务器(估算): ' + SERVER_PROFILE.cores + 'C/' + SERVER_PROFILE.ramGB + 'G/' + SERVER_PROFILE.netMbps + 'M  |  当前估算负载 ' + formatMbps(estimatedRelayLoadKbps / 1000) + '\n' +
          '📦 参考容量(估算): SFU 约≤' + sfuSuggested + ' 人  TURN 约≤' + turnSuggested + ' 人  |  ' + serverNote + '\n' +
          '📝 说明: 当前为发送侧视角；服务器负载与容量为按当前码率推算';
      }
      pc._gcLastStatsText = statsText;
      publishConnectionStats(
        statsText,
        pc._lastConnType + ' · ' + pc._lastRtt + ' · ' + getSfuStatusLabel()
      );

      pc._gcStatsReportCounter = (pc._gcStatsReportCounter || 0) + 1;
      if (pc._gcStatsReportCounter >= 5) {
        pc._gcStatsReportCounter = 0;
        var room = currentRoomId();
        var reportPayload = {
          event: diagnosticSample ? 'webrtc_sample' : 'legacy_webrtc_sample',
          policyVersion: qualityState ? qualityState.policyVersion : '',
          user: currentUser || 'guest',
          room: room,
          peer: trackedPeerId,
          mode: mode,
          conn: buildTransportLabel(connType, mode),
          rtt: rttNum || null,
          resolution: resolution,
          fps: fps,
          codec: codec,
          kbps: kbps,
          loss: loss,
          jitter: jitter,
          pcState: pc.connectionState,
          pcCount: activePCs.length,
          visible: document.visibilityState !== 'hidden',
          adaptiveConfiguredFps: extCfg().fps,
          targetKbps: adaptiveTargetKbpsValue + 'kbps',
          adaptive: getAdaptiveStatusLabel(pc),
          serverLoadKbps: Math.round(estimatedRelayLoadKbps) + 'kbps',
          serverNote: serverNote,
          estimatedRelayLoadKbps: Math.round(estimatedRelayLoadKbps),
          protectionMaxBitrate: pc._gcProtectionMaxBitrate || 0
        };
        if (diagnosticSample) {
          [
            'path', 'transport', 'localCandidateType', 'remoteCandidateType',
            'localAddressFamily', 'remoteAddressFamily', 'generation', 'valid',
            'invalidReason', 'rttMs', 'jitterMs', 'lossPercent', 'upKbps',
            'downKbps', 'availableOutgoingBitrate', 'targetBitrate',
            'packetsReceivedDelta', 'packetsLostDelta', 'packetsSentDelta',
            'framesDecodedDelta', 'framesDroppedDelta',
            'framesCaptured', 'framesCapturedDelta', 'capturedFps',
            'framesEncoded', 'framesEncodedDelta', 'encodedFps',
            'framesSent', 'framesSentDelta', 'sentFps',
            'keyFramesEncoded', 'keyFramesEncodedDelta',
            'outboundPliCount', 'outboundPliCountDelta',
            'outboundNackCount', 'outboundNackCountDelta',
            'inboundPliCount', 'inboundPliCountDelta',
            'inboundNackCount', 'inboundNackCountDelta',
            'retransmittedPacketsSent', 'retransmittedPacketsSentDelta',
            'retransmittedBytesSent', 'retransmittedBytesSentDelta',
            'totalPacketSendDelayMs', 'totalPacketSendDelayDeltaMs',
            'averagePacketSendDelayMs',
            'qualityLimitationReason'
          ].forEach(function (key) {
            if (diagnosticSample[key] !== undefined) reportPayload[key] = diagnosticSample[key];
          });
        }
        if (qualityState) {
          reportPayload.qualityStatus = qualityState.status;
          reportPayload.shadowAction = qualityState.action || '';
          reportPayload.badCount = qualityState.badCount;
          reportPayload.windowSize = qualityState.window.length;
        }
        if (adaptiveState && adaptiveProfile) {
          var receiverSample = pc._gcAdaptiveLastSample || {};
          Object.assign(reportPayload, {
            adaptivePolicyVersion: adaptiveState.policyVersion,
            adaptiveDecision: adaptiveDecision,
            adaptiveLevel: adaptiveState.level,
            adaptiveScale: adaptiveProfile.scaleResolutionDownBy,
            adaptiveMaxBitrate: adaptiveProfile.maxBitrate,
            adaptiveNominalBitrate: adaptiveProfile.nominalBitrate,
            adaptiveDegradeFloorBitrate: adaptiveProfile.degradeFloorBitrate,
            adaptiveStatus: adaptiveState.status,
            screegoSessionId: pc._gcScreegoSessionId || '',
            receiverState: adaptiveState.receiverStatus || 'unavailable',
            receiverReason: adaptiveState.receiverReason || 'receiver-unavailable',
            receiverBound: receiverSample.receiverBound === true,
            receiverReportAgeMs: receiverSample.receiverReportAgeMs || 0,
            receiverDecodedFps: receiverSample.receiverDecodedFps || 0,
            receiverIncomingKbps: receiverSample.receiverIncomingKbps || 0,
            receiverLossPercent: receiverSample.receiverLossPercent || 0,
            receiverJitterMs: receiverSample.receiverJitterMs || 0,
            senderCapturedFps: receiverSample.senderCapturedFps || 0,
            senderEncodedFps: receiverSample.senderEncodedFps || 0,
            senderSentFps: receiverSample.senderSentFps || 0,
            adaptiveStableMs: adaptiveState.stableSince === null
              ? 0
              : Math.max(0, Date.now() - adaptiveState.stableSince),
            adaptiveProbeUntil: adaptiveState.probeUntil || 0,
            adaptiveProbeImprovedCount: adaptiveState.probeImprovedCount || 0
          });
        }
        if (
          pc._gcTelemetryReasonLatch &&
          typeof GameCastWebRTCStats !== 'undefined' &&
          typeof GameCastWebRTCStats.sendTelemetryReport === 'function'
        ) {
          GameCastWebRTCStats.sendTelemetryReport(
            pc._gcTelemetryReasonLatch,
            reportPayload,
            reportStats
          );
        } else {
          reportStats(reportPayload);
        }
        if (adaptiveDecision) pc._gcAdaptivePendingDecision = '';
      }
    }).catch(function (err) {
      if (pc.connectionState === 'closed') return;
      var fallbackText = pc._gcLastStatsText || buildStatsStatusText(
        err && err.message === 'timeout' ? '读取链路数据超时，稍后重试...' : '读取链路数据失败，稍后重试...',
        pc
      );
      pc._gcLastStatsText = fallbackText;
      publishConnectionStats(fallbackText, describePeerConnectionState(pc) || '连接诊断');
    }).finally(function () {
      if (pc._gcStatsFetchPromise === statsPromise) pc._gcStatsFetchPromise = null;
    });
  }

  /* ── 监听 URL ── */
  function monitorRoomUrl() {
    var lastUrl = location.href;
    var lastRoomId = currentRoomId();

    setInterval(function () {
      if (location.href !== lastUrl) {
        lastUrl = location.href;
        var previousRoomId = lastRoomId;
        var roomId = currentRoomId();
        if (previousRoomId && !roomId) {
          var stopPromise = activeDisplayStream || webShareLease
            ? stopWebShare(false)
            : releaseWebShare(false);
          Promise.resolve(stopPromise).finally(function () {
            leaveWebPresence(false);
            clearRoomMediaMode();
          });
          stopRoomStateHeartbeat();
          closeRoomSocket();
        }
        if (roomId && roomId !== previousRoomId) {
          lastRoomState = null;
        }
        lastRoomId = roomId;
      }
    }, 1000);
  }

  function startRuntime() {
    monitorRoomUrl();
    monitorSfuMode();
    startAdaptiveStreamingMonitor();
    startAuthStateMonitor();
    startConnectionStats();
    fetchUsername();
  }

  /* ── 启动 ── */
  function init() {
    console.log('[GameCast] 初始化 v5.0');
    ensureDefaultShareSettings();
    window.addEventListener('beforeunload', function () {
      if (webShareLease && (webShareLease.mediaMode === 'sfu' || isUnifiedSfuRoom(lastRoomState))) {
        sendWebSharePhaseBeacon('stopping');
      }
      var unloadingStream = activeDisplayStream;
      activeDisplayStream = null;
      activeVideoTrack = null;
      sfuDisconnect();
      stopDisplayStreamTracks(unloadingStream);
      releaseWebShare(true);
      if (canMaintainRoomState(currentRoomId())) sendRoomStateBeacon(false, roomStateHeartbeatRoomId || currentRoomId());
      leaveWebPresence(true);
      clearRoomMediaMode();
      closeRoomSocket();
    });
    installScreegoPeerSessionHook();
    patchWebSocket();
    patchGDM();
    patchRTC();
    startRuntime();
  }
  init();
})();
