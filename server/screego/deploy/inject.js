(function () {
  'use strict';

  var SETTINGS_KEY = 'screegoSettings';
  var HISTORY_KEY = 'screegoRoomHistory';
  var REGISTER_URL = '/api/register';
  var STATS_URL = '/api/stats';
  var ROOM_STATE_URL = '/api/room-state';
  var PRESENCE_URL = '/api/presence';
  var SHARE_URL = '/api/share';
  var VIEWER_INTENT_URL = '/api/viewer-intent';
  var PRESENT_TO_ALL_ICON_PATH = 'M21 3H3c-1.11 0-2 .89-2 2v14c0 1.11.89 2 2 2h18c1.11 0 2-.89 2-2V5c0-1.11-.89-2-2-2m0 16.02H3V4.98h18zM10 12H8l4-4 4 4h-2v4h-4z';
  var ROOM_STATE_TTL = 15;
  var ROOM_STATE_HEARTBEAT_MS = 5000;
  var AUTO_SFU_MIN_PARTICIPANTS = 3;
  var NativeWebSocket = window.WebSocket;
  var currentUser = null;
  var sfuMode = false;
  var autoSfuActive = false;
  var nativeViewerIntentActive = false;
  var autoSfuSuppressedForCurrentShare = false;
  var adaptiveStreamingMonitorTimer = 0;
  var sfuConnection = null;
  var sfuStatusText = '未启用';
  var activeRoomSocket = null;
  var activeRoomSocketRoomId = '';
  var roomStateHeartbeatTimer = 0;
  var roomStateHeartbeatRoomId = '';
  var roomStateFetchPromise = null;
  var roomStateFetchRoomId = '';
  var lastRoomState = null;
  var lastKnownAuthUser = '';
  var lastKnownLoggedIn = false;
  var historyLoadedForUser = '';
  var authStateMonitorTimer = 0;
  var nativeFetch = null;
  var rawSuggestedRoomId = '';
  var suggestedRoomId = '';
  var nativeGetDisplayMedia = null;
  var primedDisplayMediaRequest = null;
  var primedDisplayMediaRequestSeq = 0;
  var shareSetupBypassButton = null;
  var shareSetupBypassUntil = 0;
  var shareSetupPendingButton = null;
  var shareSetupClaimSeq = 0;
  var webParticipantId = '';
  var webPresenceRoom = '';
  var webPresenceRole = '';
  var webPresenceName = '';
  var webPresenceTimer = 0;
  var webPresenceMembers = [];
  var webShareLease = null;
  var webShareHeartbeatTimer = 0;
  var webShareSafetyTimer = 0;
  var webAudioSafetyNoticeTimer = 0;
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

  var FPS_OPTIONS = [35, 45, 60];

  var MAX_SHARE_FPS = 60;
  var STATS_FETCH_TIMEOUT_MS = 3500;
  var trackedPeerCounter = 0;
  var statsFetchRequestSeq = 0;

  var AUDIO_MODES = {
    app: {
      label: '标签页音频 (推荐)',
      desc: '仅在选择浏览器标签页时共享该标签页声音；普通窗口会自动静音。'
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
    cores: 4,
    ramGB: 4,
    netMbps: 20,
    safeNetMbps: 16
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
      s.framerate = 35;
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
    if (isNaN(fps)) return 35;
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
    sfuMode = s.sfuMode || false;
    return {
      res: normalizeResolutionKey(s.resolution || '1280x720'),
      fps: normalizeFramerateValue(s.framerate || 35),
      qm: s.qualityMode || 'balanced',
      audio: AUDIO_MODES[s.audioMode] ? s.audioMode : 'app'
    };
  }
  function maxBitrate() { return 50000000; }
  function resConstraints() {
    var c = extCfg(), p = RESOLUTIONS[c.res];
    return (p && p.w) ? { width: { ideal: p.w }, height: { ideal: p.h } } : {};
  }

  function currentRoomId() {
    var match = location.search.match(/[?&]room=([^&]+)/);
    return match ? decodeURIComponent(match[1]) : '';
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

  function controlRequest(url, options) {
    options = options || {};
    options.credentials = 'same-origin';
    options.cache = 'no-store';
    if (options.body && !options.headers) options.headers = { 'Content-Type': 'application/json' };
    return fetch(url, options).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (data) {
        if (!response.ok || !data.ok) {
          var code = data.code || data.error || ('http_' + response.status);
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

  function webCanStartShare(state) {
    if (!state || !state.sharing) return true;
    return state.shareOwnerParticipantId === getWebParticipantId();
  }

  function renderWebPresenceMembers() {
    document.querySelectorAll('[data-gc-presence-member="1"]').forEach(function (item) { item.remove(); });
    if (!currentRoomId() || !webPresenceMembers.length) return;
    var list = null;
    var candidates = document.querySelectorAll('ul.MuiList-root, .MuiList-root');
    for (var i = 0; i < candidates.length; i++) {
      if (candidates[i].closest && candidates[i].closest('[role="dialog"]')) continue;
      if (candidates[i].querySelector('.MuiListItem-root')) {
        list = candidates[i];
        break;
      }
    }
    if (!list) return;
    var existingText = (list.textContent || '').toLowerCase();
    var template = list.querySelector('.MuiListItem-root');
    webPresenceMembers.forEach(function (participant) {
      if (participant.clientType !== 'client') return;
      var name = participant.name || participant.participantId;
      if (existingText.indexOf(name.toLowerCase()) >= 0) return;
      var item = template ? template.cloneNode(false) : document.createElement('li');
      item.removeAttribute('id');
      item.dataset.gcPresenceMember = '1';
      item.textContent = name + ' · 客户端' + (participant.sharing ? ' · 共享中' : '');
      list.appendChild(item);
    });
  }

  function refreshWebPresence() {
    var roomId = currentRoomId();
    if (!roomId || webPresenceRoom !== roomId) return Promise.resolve(null);
    return controlRequest(PRESENCE_URL + '?room=' + encodeURIComponent(roomId)).then(function (data) {
      webPresenceMembers = mergeWebPresence(data.participants);
      if (data.room) lastRoomState = data.room;
      renderWebPresenceMembers();
      syncShareButtonAvailability();
      return data;
    }).catch(function () { return null; });
  }

  function heartbeatWebPresence() {
    var roomId = currentRoomId();
    if (!roomId || webPresenceRoom !== roomId) return Promise.resolve(null);
    return controlRequest(PRESENCE_URL + '/heartbeat', {
      method: 'POST',
      body: JSON.stringify({ room: roomId, participantId: getWebParticipantId(), ttl: ROOM_STATE_TTL })
    }).catch(function (error) {
      if (error && error.code === 'participant_not_found') {
        webPresenceRoom = '';
        return ensureWebPresence(roomId);
      }
      return null;
    });
  }

  function startWebPresenceHeartbeat() {
    if (webPresenceTimer) clearInterval(webPresenceTimer);
    webPresenceTimer = setInterval(function () {
      heartbeatWebPresence();
      refreshWebPresence();
    }, ROOM_STATE_HEARTBEAT_MS);
  }

  function leaveWebPresence(useBeacon) {
    var roomId = webPresenceRoom;
    if (webPresenceTimer) clearInterval(webPresenceTimer);
    webPresenceTimer = 0;
    webPresenceRoom = '';
    webPresenceRole = '';
    webPresenceName = '';
    webPresenceMembers = [];
    renderWebPresenceMembers();
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
        if (data.room) lastRoomState = data.room;
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

  function heartbeatWebShare() {
    if (!webShareLease) return Promise.resolve(null);
    return controlRequest(SHARE_URL + '/heartbeat', {
      method: 'POST',
      body: JSON.stringify({
        room: webShareLease.room,
        participantId: getWebParticipantId(),
        leaseId: webShareLease.leaseId,
        sfu: currentSfuMode(),
        audioMode: effectiveWebShareAudioMode(),
        ttl: ROOM_STATE_TTL
      })
    }).then(function (data) {
      if (data.room) lastRoomState = data.room;
      return data;
    }).catch(function (error) {
      if (error && (error.code === 'share_lease_expired' || error.code === 'share_lease_invalid')) {
        clearWebShareTimers();
        webShareLease = null;
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
      return controlRequest(SHARE_URL + '/claim', {
        method: 'POST',
        body: JSON.stringify({
          room: roomId,
          participantId: getWebParticipantId(),
          sfu: currentSfuMode(),
          audioMode: extCfg().audio,
          ttl: ROOM_STATE_TTL
        })
      }).then(function (data) {
        webShareLease = data.lease;
        if (data.room) lastRoomState = data.room;
        startWebShareHeartbeat();
        syncShareButtonAvailability();
        return webShareLease;
      });
    });
  }

  function releaseWebShare(useBeacon) {
    var lease = webShareLease;
    clearWebShareTimers();
    webShareLease = null;
    activeCaptureAudioMode = 'off';
    syncShareButtonAvailability();
    if (!lease) return Promise.resolve(null);
    var payload = JSON.stringify({ room: lease.room, participantId: getWebParticipantId(), leaseId: lease.leaseId });
    if (useBeacon && navigator.sendBeacon) {
      try { navigator.sendBeacon(SHARE_URL + '/release', payload); } catch (e) {}
      return Promise.resolve(null);
    }
    return controlRequest(SHARE_URL + '/release', { method: 'POST', body: payload }).catch(function () { return null; });
  }

  function syncShareButtonAvailability() {
    var blocked = !!(lastRoomState && lastRoomState.sharing && !webCanStartShare(lastRoomState));
    collectShareStartButtons(document.body).forEach(function (button) {
      if (blocked) {
        if (!button.dataset.gcControlDisabled) button.dataset.gcControlWasDisabled = button.disabled ? '1' : '0';
        button.dataset.gcControlDisabled = '1';
        button.disabled = true;
        button.title = '当前已有成员正在共享';
      } else if (button.dataset.gcControlDisabled) {
        if (button.dataset.gcControlWasDisabled !== '1') button.disabled = false;
        delete button.dataset.gcControlDisabled;
        delete button.dataset.gcControlWasDisabled;
        if (button.title === '当前已有成员正在共享') button.removeAttribute('title');
      }
    });
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
    return !!(autoSfuActive || nativeViewerIntentActive) || !!(
      lastRoomState && lastRoomState.origin === 'client' && currentRoomId()
    );
  }

  function sfuEnabledNow() {
    return currentSfuMode();
  }

  function roomStateSfuActive(roomId) {
    return !!(
      lastRoomState &&
      lastRoomState.room === roomId &&
      lastRoomState.active &&
      lastRoomState.sfu &&
      lastRoomState.sharing
    );
  }

  function clearRoomStateHeartbeat() {
    if (roomStateHeartbeatTimer) {
      clearInterval(roomStateHeartbeatTimer);
      roomStateHeartbeatTimer = 0;
    }
  }

  function resetAutoSfuState() {
    autoSfuActive = false;
    autoSfuSuppressedForCurrentShare = false;
  }

  function resetAdaptiveStreamingState() {
    activePCs.forEach(function (pc) {
      pc._gcAdaptiveState = null;
      if (typeof GameCastWebRTCAdaptive !== 'undefined') {
        pc._gcAdaptiveState = GameCastWebRTCAdaptive.createAdaptiveState(Date.now());
      }
      pc._gcAdaptivePrev = null;
      pc._gcAdaptiveProfile = null;
      pc._gcAdaptivePendingDecision = '';
    });
  }

  function isSfuMediaReady() {
    return typeof GC_SFU !== 'undefined' &&
      !!GC_SFU &&
      typeof GC_SFU.isConnected === 'function' &&
      typeof GC_SFU.isMediaReady === 'function' &&
      GC_SFU.isConnected() &&
      GC_SFU.isMediaReady();
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
    var seen = {};
    var count = 0;
    document.querySelectorAll('.MuiListItem-root, .user-item, [class*="user"]').forEach(function (item) {
      var text = (item.textContent || '').trim();
      if (!text || seen[text]) return;
      seen[text] = true;
      count += 1;
    });
    return count;
  }

  function shouldPromoteAutoSfu() {
    return getMemberCountEstimate() >= AUTO_SFU_MIN_PARTICIPANTS;
  }

  function refreshAutoSfuState() {
    if (!activeDisplayStream || !currentRoomId()) {
      resetAutoSfuState();
      return false;
    }
    if (nativeViewerIntentActive) {
      autoSfuActive = true;
      sfuStatusText = '???????????? SFU';
      return true;
    }
    if (!sfuMode) {
      autoSfuActive = false;
      return false;
    }
    if (!autoSfuActive && shouldPromoteAutoSfu()) {
      autoSfuActive = true;
      sfuStatusText = '检测到多个观众，已自动切换 SFU';
    }
    return autoSfuActive;
  }

  function syncHostSfuState(roomId) {
    if (!roomId || !activeDisplayStream) {
      if (canMaintainRoomState(roomId)) startRoomStateHeartbeat();
      else stopRoomStateHeartbeat();
      return;
    }

    refreshAutoSfuState();
    if (currentSfuMode()) {
      if (!sfuConnection || typeof GC_SFU === 'undefined' || !GC_SFU || !GC_SFU.isConnected()) sfuConnect(roomId, 'host', activeDisplayStream);
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
    if (!state || !profile || state.level <= 0) return '默认';
    return 'L' + state.level + ' / ' + profile.maxWidth + 'x' + profile.maxHeight +
      ' / ' + profile.maxFramerate + ' FPS / ' + formatKbps(profile.maxBitrate / 1000);
  }

  function shouldUseAdaptiveStreaming(pc) {
    return !!activeDisplayStream &&
      typeof GameCastWebRTCAdaptive !== 'undefined' &&
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
      if (!shouldUseAdaptiveStreaming(pc) || seen.indexOf(pc) >= 0) return;
      seen.push(pc);
      descriptors.push({pc: pc, mode: 'native-host'});
    });
    var sfuPc = getSfuPeerConnection();
    if (getSfuRole() === 'host' && shouldUseAdaptiveStreaming(sfuPc) && seen.indexOf(sfuPc) < 0) {
      descriptors.push({pc: sfuPc, mode: 'sfu-host'});
    }
    return descriptors;
  }

  function getPeerVideoSender(pc) {
    if (!pc || typeof pc.getSenders !== 'function') return null;
    var senders = pc.getSenders() || [];
    for (var i = 0; i < senders.length; i++) {
      if (senders[i] && senders[i].track && senders[i].track.kind === 'video') return senders[i];
    }
    return null;
  }

  function computePeerAdaptiveProfile(pc) {
    if (typeof GameCastWebRTCAdaptive === 'undefined') return null;
    var sender = getPeerVideoSender(pc);
    var settings = sender && sender.track && typeof sender.track.getSettings === 'function'
      ? sender.track.getSettings() || {}
      : {};
    var cfg = extCfg();
    var configuredResolution = RESOLUTIONS[cfg.res] || {};
    var sourceWidth = settings.width || configuredResolution.w || 1280;
    var sourceHeight = settings.height || configuredResolution.h || 720;
    var state = pc._gcAdaptiveState || GameCastWebRTCAdaptive.createAdaptiveState(Date.now());
    pc._gcAdaptiveState = state;
    var profile = GameCastWebRTCAdaptive.computeEncodingProfile({
      level: state.level,
      sourceWidth: sourceWidth,
      sourceHeight: sourceHeight,
      configuredFps: cfg.fps || 35,
      targetBitrate: computeTargetBitrate(),
      viewerCount: Math.max(1, getAdaptivePeerDescriptors().length),
      serverSafeBitrate: SERVER_PROFILE.safeNetMbps * 1000000
    });
    pc._gcAdaptiveProfile = profile;
    return profile;
  }

  function sampleAdaptiveStreamingPeer(descriptor) {
    var pc = descriptor && descriptor.pc;
    if (!shouldUseAdaptiveStreaming(pc) || pc._gcAdaptiveFetchPromise) return;
    var adaptiveState = pc._gcAdaptiveState || GameCastWebRTCAdaptive.createAdaptiveState(Date.now());
    var adaptiveProfile = computePeerAdaptiveProfile(pc);
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
      var qualityReason = 'none';
      var remotePacketsLostTotal = 0;
      var remoteJitterMs = 0;
      var hasRemoteInbound = false;
      stats.forEach(function (report) {
        if (report.type === 'remote-inbound-rtp' && (report.kind === 'video' || report.mediaType === 'video')) {
          hasRemoteInbound = true;
          remotePacketsLostTotal += report.packetsLost || 0;
          remoteJitterMs = Math.max(remoteJitterMs, (report.jitter || 0) * 1000);
        }
        if (report.type === 'outbound-rtp' && (report.kind === 'video' || report.mediaType === 'video')) {
          outboundBytesTotal += report.bytesSent || 0;
          outboundPacketsSentTotal += report.packetsSent || 0;
          if (report.qualityLimitationReason) qualityReason = report.qualityLimitationReason;
        }
      });

      var now = Date.now();
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
          }
        }
      }

      pc._gcAdaptivePrev = {
        bytesSent: outboundBytesTotal,
        packetsSent: outboundPacketsSentTotal,
        remotePacketsLost: remotePacketsLostTotal,
        pairId: adaptiveSample.pairId,
        ts: now
      };
      var evaluation = GameCastWebRTCAdaptive.evaluateAdaptiveSample(adaptiveState, adaptiveSample);
      adaptiveState = evaluation.state;
      pc._gcAdaptiveState = adaptiveState;
      pc._gcAdaptiveLastSample = adaptiveSample;
      if (evaluation.decision) {
        var adaptiveDecision = evaluation.decision.type + ':' +
          evaluation.decision.fromLevel + '->' + evaluation.decision.toLevel;
        pc._gcAdaptivePendingDecision = adaptiveDecision;
        applyAdaptiveProfile(pc, adaptiveDecision);
      }
    }).catch(function () {}).finally(function () {
      pc._gcAdaptiveFetchPromise = null;
    });
  }

  function sampleAdaptiveStreaming() {
    if (!activeDisplayStream) return;
    getAdaptivePeerDescriptors().forEach(sampleAdaptiveStreamingPeer);
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
    if (!currentSfuMode() && !sfuConnection) return sfuMode ? '待多观众时自动开启' : '未启用';
    return sfuStatusText || (currentSfuMode() ? '连接中...' : '未启用');
  }

  function postRoomState(active, roomOverride) {
    var roomId = roomOverride || currentRoomId();
    if (!roomId) return Promise.resolve(null);

    return fetch(ROOM_STATE_URL, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        room: roomId,
        active: !!active,
        sharing: !!active && !!activeDisplayStream,
        sfu: !!active && currentSfuMode(),
        audioMode: active ? effectiveWebShareAudioMode() : 'off',
        ttl: ROOM_STATE_TTL
      })
    }).then(function (r) { return r.json(); }).then(function (data) {
      if (data && data.ok && data.state) {
        lastRoomState = data.state;
      }
      return data;
    }).catch(function () {
      return null;
    });
  }

  function sendRoomStateBeacon(active, roomOverride) {
    var roomId = roomOverride || currentRoomId();
    if (!roomId || !navigator.sendBeacon) return;
    try {
      navigator.sendBeacon(ROOM_STATE_URL, JSON.stringify({
        room: roomId,
        active: !!active,
        sharing: !!active && !!activeDisplayStream,
        sfu: !!active && currentSfuMode(),
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
        lastRoomState = data.state;
        syncShareButtonAvailability();
        return data.state;
      }
      lastRoomState = {
        room: roomId,
        active: false,
        sharing: false,
        sfu: false
      };
      return lastRoomState;
    }).catch(function () {
      return lastRoomState && lastRoomState.room === roomId ? lastRoomState : null;
    }).finally(function () {
      roomStateFetchPromise = null;
      roomStateFetchRoomId = '';
    });

    return roomStateFetchPromise;
  }

  function fetchViewerIntent(roomId) {
    if (!roomId) return Promise.resolve(null);
    return fetch(VIEWER_INTENT_URL + '?room=' + encodeURIComponent(roomId), {
      credentials: 'same-origin',
      cache: 'no-store'
    }).then(function (r) { return r.json(); }).then(function (data) {
      return data && data.ok && data.intent ? data.intent : null;
    }).catch(function () {
      return null;
    });
  }

  function applyViewerIntent(intent) {
    nativeViewerIntentActive = !!(intent && intent.active && intent.wantsSfu && intent.viewerCount > 0);
    if (nativeViewerIntentActive) {
      autoSfuActive = true;
      sfuStatusText = '???????????? SFU';
    }
    return nativeViewerIntentActive;
  }

  function syncViewerSfuState(roomId, amStreamer) {
    var remoteActive = roomStateSfuActive(roomId);
    var shareOwnedByThisTab = !!(
      lastRoomState && lastRoomState.shareOwnerParticipantId === getWebParticipantId()
    );
    if (remoteActive && !shareOwnedByThisTab && !amStreamer && !activeDisplayStream) {
      if (!sfuConnection) {
        console.log('[SFU] 房主已开启 SFU，观看者自动接入');
        sfuConnect(roomId, 'viewer', null);
      }
      return;
    }

    if ((!remoteActive || amStreamer) && sfuConnection && !activeDisplayStream) {
      sfuDisconnect();
    }
  }

  function shouldDropNativePeerMessage(event) {
    if (!event || typeof event.data !== 'string') return false;
    var msg;
    try { msg = JSON.parse(event.data); } catch (e) { return false; }
    if (msg.type === 'hostsession') {
      return !!activeDisplayStream && sfuEnabledNow() && isSfuMediaReady();
    }
    if (msg.type === 'clientsession') {
      return roomStateSfuActive(currentRoomId()) && isSfuMediaReady();
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

    activateConfirmedRoom(roomId);
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
        activeRoomSocket = null;
        activeRoomSocketRoomId = '';
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

  function closeActivePeerConnections() {
    activePCs.slice().forEach(function (pc) {
      try { pc.close(); } catch (e) {}
    });
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

  function leaveRoomGracefully() {
    if (activeDisplayStream) {
      resetAutoSfuState();
      resetAdaptiveStreamingState();
      sendRoomStateBeacon(false);
      stopRoomStateHeartbeat();
      try {
        activeDisplayStream.getTracks().forEach(function (track) { track.stop(); });
      } catch (e) {}
      activeDisplayStream = null;
      activeVideoTrack = null;
      activeCaptureAudioMode = 'off';
    }
    sfuDisconnect();
    releaseWebShare(false);
    leaveWebPresence(false);
    closeActivePeerConnections();
    closeRoomSocket();
    setTimeout(function () {
      window.location.href = '/';
    }, 80);
  }

  /* ── 房间历史（云端同步） ── */
  function historyKey(user) {
    user = user || currentUser || loadCfg().name || null;
    return user ? HISTORY_KEY + '_' + user : HISTORY_KEY;
  }
  function preferredRoomIdForUser(user, fallbackRoomId) {
    user = (user || '').trim();
    var history = loadHistory(user);
    if (history.length > 0 && history[0] && history[0].id) return history[0].id;
    if (user && user !== 'guest') return user;
    return (fallbackRoomId || '').trim();
  }
  function normalizeHistoryEntries(history) {
    if (!Array.isArray(history)) return [];
    var seen = {};
    return history.filter(function (r) {
      if (!r || typeof r.id !== 'string') return false;
      var roomId = r.id.trim();
      if (!roomId || seen[roomId]) return false;
      seen[roomId] = true;
      r.id = roomId;
      r.time = parseInt(r.time, 10) || Date.now();
      return true;
    }).sort(function (a, b) {
      return (b.time || 0) - (a.time || 0);
    }).slice(0, 20);
  }
  function loadHistory(user) {
    try {
      var h = JSON.parse(localStorage.getItem(historyKey(user)) || '[]');
      return normalizeHistoryEntries(h);
    } catch (e) {
      return [];
    }
  }
  function saveHistory(h, user) {
    localStorage.setItem(historyKey(user), JSON.stringify(normalizeHistoryEntries(h)));
  }
  function addRoomHistory(roomId) {
    if (!roomId) return;
    var h = loadHistory();
    var idx = h.findIndex(function (r) { return r.id === roomId; });
    if (idx >= 0) h.splice(idx, 1);
    h.unshift({ id: roomId, time: Date.now() });
    saveHistory(h);
    syncHistoryToServer();
    fillRoomIdDefault({ forceAutofill: true, preferHistory: true });
    refreshHistoryDropdown();
  }
  function removeRoomHistory(roomId) {
    roomId = (roomId || '').trim();
    if (!roomId) return;
    var h = loadHistory().filter(function (r) { return r.id !== roomId; });
    saveHistory(h);
    syncHistoryToServer();
  }
  function syncHistoryToServer() {
    var user = currentUser;
    if (!user || user === 'guest') return;
    fetch('/api/history', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ history: loadHistory(user) })
    }).catch(function () {});
  }
  function loadHistoryFromServer(user) {
    return fetch('/api/history', { credentials: 'same-origin' })
      .then(function (r) { return r.json(); })
      .then(function (data) {
        var merged = loadHistory(user);
        if (data.ok && Array.isArray(data.history)) {
          merged = normalizeHistoryEntries(data.history.concat(merged));
          saveHistory(merged, user);
        }
        refreshHistoryDropdown();
        fillRoomIdDefault({ forceAutofill: true, preferHistory: true });
        return merged;
      }).catch(function () {
        refreshHistoryDropdown();
        fillRoomIdDefault({ forceAutofill: true, preferHistory: true });
        return loadHistory(user);
      });
  }
  function refreshHistoryDropdown() {
    var arrow = document.getElementById('enh-hist-arrow');
    if (arrow) arrow.remove();
    var list = document.getElementById('enh-hist-list');
    if (list) list.remove();
    document.querySelectorAll('input[data-enh-history]').forEach(function (inp) { delete inp.dataset.enhHistory; });
    setTimeout(injectHistoryDropdown, 100);
  }
  function isConfigRequest(input) {
    var url = '';
    if (typeof input === 'string') url = input;
    else if (input && typeof input.url === 'string') url = input.url;
    if (!url) return false;
    try {
      return new URL(url, location.origin).pathname === '/config';
    } catch (e) {
      return /\/config(?:\?|$)/.test(url);
    }
  }
  function buildJsonResponseLike(response, data) {
    var headers = new Headers(response.headers || {});
    headers.set('Content-Type', 'application/json; charset=utf-8');
    headers.delete('Content-Length');
    return new Response(JSON.stringify(data), {
      status: response.status,
      statusText: response.statusText,
      headers: headers
    });
  }
  function patchFetch() {
    if (!window.fetch || window.fetch._gcWrapped) return;
    nativeFetch = window.fetch.bind(window);
    window.fetch = function (input, init) {
      return nativeFetch(input, init).then(function (response) {
        if (!isConfigRequest(input) || !response || !response.ok) return response;
        return response.clone().json().then(function (data) {
          if (!data || typeof data !== 'object') return response;
          var user = ((data.user || '') + '').trim();
          rawSuggestedRoomId = ((data.roomName || '') + '').trim();
          if (data.loggedIn && user && user !== 'guest') {
            data.roomName = preferredRoomIdForUser(user, data.roomName);
          }
          return buildJsonResponseLike(response, data);
        }).catch(function () {
          return response;
        });
      });
    };
    window.fetch._gcWrapped = true;
  }

  /* ── 用户名 ── */
  function scheduleRoomIdDefaultRefresh() {
    fillRoomIdDefault({ preferHistory: true, forceAutofill: true });
    setTimeout(function () { fillRoomIdDefault({ preferHistory: true, forceAutofill: true }); }, 300);
    setTimeout(function () { fillRoomIdDefault({ preferHistory: true, forceAutofill: true }); }, 1200);
    setTimeout(function () { fillRoomIdDefault({ preferHistory: true, forceAutofill: true }); }, 2500);
  }
  function applyConfigState(data, options) {
    options = options || {};

    var nextUser = ((data && data.user) || '').trim() || 'guest';
    var nextLoggedIn = !!(data && data.loggedIn) && nextUser !== 'guest';
    var prevUser = currentUser || 'guest';
    var prevLoggedIn = prevUser !== 'guest';
    var userChanged = nextUser !== prevUser;
    var loginStateChanged = nextLoggedIn !== prevLoggedIn;

    currentUser = nextUser;
    rawSuggestedRoomId = rawSuggestedRoomId || suggestedRoomId;
    suggestedRoomId = ((data && data.roomName) || '').trim();
    lastKnownAuthUser = nextUser;
    lastKnownLoggedIn = nextLoggedIn;

    if (nextLoggedIn) {
      var cfg = loadCfg();
      if (cfg.name !== currentUser) {
        cfg.name = currentUser;
        saveCfg(cfg);
      }
      if (userChanged) historyLoadedForUser = '';
      if (options.forceHistoryReload || userChanged || loginStateChanged || historyLoadedForUser !== currentUser) {
        loadHistoryFromServer(currentUser).then(function () {
          historyLoadedForUser = currentUser;
        });
      }
      scheduleRoomIdDefaultRefresh();
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
    if (userChanged || loginStateChanged) {
      historyLoadedForUser = '';
      refreshHistoryDropdown();
    }
    if (!options.silent) scheduleRoomIdDefaultRefresh();
  }
  function fetchUsername(options) {
    options = options || {};
    return fetch(location.origin + '/config', { credentials: 'same-origin' })
      .then(function (r) { return r.json(); })
      .then(function (data) {
        applyConfigState(data, options);
        return data;
      }).catch(function () {
        if (!currentUser) currentUser = 'guest';
        if (!options.silent) scheduleRoomIdDefaultRefresh();
        return null;
      });
  }
  function startAuthStateMonitor() {
    if (authStateMonitorTimer) return;
    authStateMonitorTimer = setInterval(function () {
      fetchUsername({ silent: true });
    }, 1500);
  }
  function isRoomIdField(input) {
    if (!input) return false;
    var wrapper = input.closest('.MuiFormControl-root') || input.parentElement;
    if (!wrapper) return false;
    var labelText = wrapper.textContent || '';
    return labelText.indexOf('id') >= 0 || labelText.indexOf('Id') >= 0;
  }
  function clearRoomIdManualOverride(input) {
    if (!input) return;
    delete input.dataset.enhManualEdit;
    delete input.dataset.enhManualEditUser;
  }
  function markRoomIdManualOverride(input) {
    if (!input) return;
    input.dataset.enhManualEdit = '1';
    input.dataset.enhManualEditUser = currentUser || '';
    input.dataset.enhAutofilled = '0';
    input.dataset.enhAutofilledValue = '';
  }
  function hasManualRoomIdOverride(input) {
    if (!input || input.dataset.enhManualEdit !== '1') return false;
    var editUser = input.dataset.enhManualEditUser || '';
    return editUser === (currentUser || '');
  }
  function bindRoomIdInputAutofillGuards(root) {
    queryAllWithSelf(root, 'input').forEach(function (input) {
      if (!isRoomIdField(input) || input.dataset.enhRoomIdGuardBound) return;
      input.dataset.enhRoomIdGuardBound = '1';
      input.addEventListener('input', function () {
        if (input.dataset.enhProgrammaticWrite === '1') return;
        markRoomIdManualOverride(input);
      });
    });
  }
  function setRoomIdInputValue(input, value) {
    if (!input) return;
    var previousValue = input.value;
    var nativeSet = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
    input.dataset.enhProgrammaticWrite = '1';
    nativeSet.call(input, value);
    if (input._valueTracker && typeof input._valueTracker.setValue === 'function') {
      input._valueTracker.setValue(previousValue);
    }
    try {
      input.dispatchEvent(new InputEvent('input', { bubbles: true, data: value, inputType: 'insertReplacementText' }));
    } catch (e) {
      input.dispatchEvent(new Event('input', { bubbles: true }));
    }
    input.dispatchEvent(new Event('change', { bubbles: true }));
    delete input.dataset.enhProgrammaticWrite;
  }
  function canAutofillRoomIdInput(input, defaultVal, forceAutofill) {
    if (!input) return false;
    if (hasManualRoomIdOverride(input)) return false;
    var currentVal = (input.value || '').trim();
    var lastAutofilled = (input.dataset.enhAutofilledValue || '').trim();
    if (!currentVal) return true;
    if (currentVal === defaultVal) return true;
    if (currentVal === rawSuggestedRoomId) return true;
    if (currentVal === suggestedRoomId) return true;
    if (currentVal === currentUser) return true;
    if (input.dataset.enhAutofilled === '1' && currentVal === lastAutofilled) return true;
    return !!(forceAutofill && input.dataset.enhAutofilled === '1');
  }
  function fillRoomIdDefault(options) {
    options = options || {};
    if (!currentUser || currentUser === 'guest') return;
    bindRoomIdInputAutofillGuards(document.body);
    var history = loadHistory();
    var defaultVal = history.length > 0 ? history[0].id : currentUser;
    document.querySelectorAll('input').forEach(function (inp) {
      if (!isRoomIdField(inp)) return;
      if (!canAutofillRoomIdInput(inp, defaultVal, options.forceAutofill)) return;
      inp.dataset.enhFilled = '1';
      inp.dataset.enhAutofilled = '1';
      inp.dataset.enhAutofilledValue = defaultVal;
      clearRoomIdManualOverride(inp);
      if ((inp.value || '').trim() === defaultVal) return;
      setRoomIdInputValue(inp, defaultVal);
    });
  }

  /* ── SFU 模式 ── */
  function sfuConnect(roomId, role, stream) {
    if (typeof GC_SFU === 'undefined') {
      console.warn('[SFU] GC_SFU 未加载');
      return;
    }
    if (sfuConnection && GC_SFU.isConnected() && getSfuRole() === role) {
      console.log('[SFU] 已连接，跳过');
      return;
    }
    if (sfuConnection) sfuDisconnect();
    GC_SFU.connect(roomId, role, {
      name: currentUser || loadCfg().name || role,
      onRemoteStream: function (remoteStream) {
        showSfuViewer(remoteStream);
        showSfuStatus('连接成功，正在接收画面');
      },
      onStatus: function (msg) {
        showSfuStatus(msg);
        if (role === 'host' && /error|failed/i.test(String(msg || ''))) {
          releaseWebShare(false);
        }
      }
    });
    if (stream && role === 'host') {
      setTimeout(function () {
        GC_SFU.shareScreen(stream);
        startRoomStateHeartbeat();
      }, 500);
    }
    sfuConnection = GC_SFU;
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
    var lastSfuState = false;
    function tick() {
      var roomId = currentRoomId();
      var inRoom = !!roomId;
      if (!inRoom) {
        if (activeDisplayStream) stopRoomStateHeartbeat();
        nativeViewerIntentActive = false;
        resetAutoSfuState();
        resetAdaptiveStreamingState();
        sfuDisconnect();
        lastSfuState = false;
        lastRoomState = null;
        closeRoomSocket();
        sfuStatusText = '???';
        return;
      }

      var hasStreamer = false;
      var amStreamer = false;
      document.querySelectorAll('[data-streaming="true"], .streaming-indicator').forEach(function () {
        hasStreamer = true;
      });

      var memberItems = document.querySelectorAll('.MuiListItem-root, .user-item, [class*="user"]');
      memberItems.forEach(function (item) {
        var text = item.textContent || '';
        if (text.indexOf('???') >= 0 || text.indexOf('Streaming') >= 0) {
          hasStreamer = true;
          if (text.indexOf('(?)') >= 0 || text.indexOf('(You)') >= 0) {
            amStreamer = true;
          }
        }
      });

      fetchViewerIntent(roomId).then(function (intent) {
        var hasNativeViewerIntent = applyViewerIntent(intent);
        if (hasNativeViewerIntent && activeDisplayStream) {
          syncHostSfuState(roomId);
          return;
        }
        if (activeDisplayStream) {
          syncHostSfuState(roomId);
          return;
        }
        fetchRoomState(roomId, true).then(function (state) {
          syncViewerSfuState(roomId, amStreamer);
          lastSfuState = !!(state && state.active && state.sfu && state.sharing && !amStreamer);

          if (!lastSfuState && sfuConnection && !hasStreamer) {
            sfuDisconnect();
          }
        });
      });
    }
    tick();
    setInterval(tick, 3000);
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

  function bitrateBaseForResolution(res) {
    switch (res) {
      case '640x360': return 1500000;
      case '854x480': return 2500000;
      case '1280x720': return 6000000;
      case '1920x1080': return 10000000;
      case '2560x1440': return 14000000;
      case '3840x2160': return 24000000;
      default: return 6000000;
    }
  }

  function qualityMultiplier(mode) {
    if (mode === 'quality') return 1.25;
    if (mode === 'smooth') return 0.95;
    return 1.0;
  }

  function fpsMultiplier(fps) {
    if (!fps || fps <= 30) return 1;
    return Math.min(1.7, 1 + ((fps - 30) / 30) * 0.5);
  }

  function computeTargetBitrate() {
    var c = extCfg();
    var bitrate = bitrateBaseForResolution(c.res) * qualityMultiplier(c.qm) * fpsMultiplier(c.fps || 35);
    return Math.max(1200000, Math.round(bitrate));
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
    vc.frameRate = buildDisplayCaptureFrameRateHints(c.fps || 35);

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

  function showWebAudioSafetyNotice() {
    var previous = document.getElementById('gc-audio-safety-notice');
    if (previous) previous.remove();
    if (webAudioSafetyNoticeTimer) clearTimeout(webAudioSafetyNoticeTimer);

    var notice = document.createElement('div');
    notice.id = 'gc-audio-safety-notice';
    notice.setAttribute('role', 'status');
    notice.textContent = '为防止泄露系统声音，窗口共享已自动关闭音频。';
    notice.style.cssText = 'position:fixed;right:20px;bottom:20px;z-index:100002;max-width:420px;padding:10px 14px;background:rgba(15,23,42,.96);color:#f8fafc;border:1px solid rgba(251,191,36,.45);border-left:4px solid #fbbf24;border-radius:6px;box-shadow:0 12px 32px rgba(2,6,23,.32);font-size:13px;line-height:1.5;letter-spacing:0';
    document.body.appendChild(notice);
    webAudioSafetyNoticeTimer = setTimeout(function () {
      if (notice.parentNode) notice.parentNode.removeChild(notice);
      webAudioSafetyNoticeTimer = 0;
    }, 5000);
  }

  function activateCapturedDisplayStream(stream, settings) {
    if (!stream) return stream;
    if (stream._gcCaptureActivated) return stream;

    var requestedAudioMode = settings && settings.audio ? settings.audio : extCfg().audio;
    var audioResult = GameCastWebShareAudioPolicy.sanitizeCapturedStream(stream, requestedAudioMode);
    stream = audioResult.stream;
    activeCaptureAudioMode = audioResult.effectiveMode;
    if (audioResult.shouldNotify) showWebAudioSafetyNotice();

    stream._gcCaptureActivated = true;
    markPeerStatsChange('_gcSourceChanged');
    activeDisplayStream = stream;
    if (webShareLease) heartbeatWebShare();
    resetAutoSfuState();
    resetAdaptiveStreamingState();
    refreshAutoSfuState();

    var vt = stream.getVideoTracks()[0];
    if (vt) {
      try { vt.contentHint = getContentHint(settings && settings.qm ? settings.qm : extCfg().qm); } catch (e) {}
      activeVideoTrack = vt;
      vt.addEventListener('ended', function () {
        activeVideoTrack = null;
        activeDisplayStream = null;
        activeCaptureAudioMode = 'off';
        resetAutoSfuState();
        resetAdaptiveStreamingState();
        if (canMaintainRoomState(currentRoomId())) startRoomStateHeartbeat();
        else stopRoomStateHeartbeat();
        sfuDisconnect();
        releaseWebShare(false);
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

    request.promise = nativeGetDisplayMedia(built.constraints).then(function (stream) {
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
      if (currentRoomId() && !webShareLease) {
        return Promise.reject(new Error('share_locked'));
      }
      var primedPromise = consumePrimedDisplayMediaRequest();
      if (primedPromise) {
        var primedSettings = extCfg();
        return primedPromise.then(function (stream) {
          return activateCapturedDisplayStream(stream, primedSettings);
        });
      }

      var built = buildDisplayMediaRequest(constraints);
      return nativeGetDisplayMedia(built.constraints).then(function (stream) {
        return activateCapturedDisplayStream(stream, built.settings);
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
      pc._gcAdaptiveState = typeof GameCastWebRTCAdaptive !== 'undefined'
        ? GameCastWebRTCAdaptive.createAdaptiveState(Date.now())
        : null;
      pc._gcAdaptiveProfile = null;
      pc._gcAdaptivePendingDecision = '';
      pc._gcAdaptiveConnected = false;
      pc._gcStatsSnapshot = null;
      activePCs.push(pc);
      pc.addEventListener('connectionstatechange', function () {
        if (pc.connectionState === 'connected' && !pc._gcAdaptiveConnected) {
          pc._gcAdaptiveConnected = true;
          pc._gcAdaptiveState = typeof GameCastWebRTCAdaptive !== 'undefined'
            ? GameCastWebRTCAdaptive.createAdaptiveState(Date.now())
            : null;
          pc._gcAdaptivePrev = null;
        }
        if (['closed', 'failed', 'disconnected'].indexOf(pc.connectionState) >= 0) {
          var i = activePCs.indexOf(pc); if (i >= 0) activePCs.splice(i, 1);
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
    var c = extCfg();
    var targetBitrate = computeTargetBitrate();
    var profile = computePeerAdaptiveProfile(pc) || {
      level: 0,
      maxWidth: (RESOLUTIONS[c.res] || {}).w || 1280,
      maxHeight: (RESOLUTIONS[c.res] || {}).h || 720,
      maxFramerate: c.fps || 35,
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

  /* ── 中文翻译 ── */
  var T = {
    'Login to Screego': '登录 GameCast', 'Username': '用户名', 'Password': '密码',
    'Login': '登录', 'Go Back': '返回', 'Logout': '退出登录',
    'Create or Join a Room': '创建或加入房间',
    'Close Room after you leave': '离开后关闭房间',
    'Nat Traversal via:': 'NAT 穿越:',
    'Settings': '设置', 'Preferred Codec': '首选编码',
    'Display Mode': '显示模式', 'FrameRate': '帧率',
    'Cancel': '取消', 'Save': '保存',
    'FitToWindow': '适应窗口', 'FitWidth': '适应宽度',
    'FitHeight': '适应高度', 'OriginalSize': '原始大小',
    'Start Presentation': '开始共享', 'Cancel Presentation': '停止共享',
    'Fullscreen': '全屏', 'Copy Link': '复制链接',
    'Link Copied': '链接已复制', 'Member List': '成员列表',
    'no stream available': '暂无共享画面',
    'You': '你', 'Owner': '房主', 'Streaming': '共享中',
    'Preset: Best Quality': '预设: 最佳画质', 'Preset: Browser Default': '预设: 浏览器默认',
    'screen sharing for developers': '游戏 · 娱乐 · 屏幕共享',
    'Screego requires JavaScript (': '需要启用 JavaScript 才能使用 GameCast (',
    'Could not start presentation': '无法开始共享',
    'Are you using https?': '请确认使用 HTTPS 访问',
    'Hello ': '你好 ',
    'Turn Server': 'TURN 服务器',
    'Host': '房主',
    'Viewer': '观看者',
    'Close': '关闭',
    'Open': '打开',
    'Connected': '已连接',
    'Disconnected': '已断开',
    'Reconnecting': '重新连接中...',
    'Error': '错误',
    'Warning': '警告',
    'Info': '提示',
    'OK': '确定',
    'Yes': '是',
    'No': '否',
    'Dismiss': '关闭',
    'Logged in!': '登录成功',
    'Logged Out.': '已退出登录'
  };

  function localizeDisplayMediaReason(reason) {
    var r = (reason || '').trim();
    if (!r) return '浏览器未返回具体原因。';
    if (r.indexOf('NotAllowedError') >= 0 || /permission denied by user/i.test(r)) return '你已取消屏幕或窗口共享授权。';
    if (r.indexOf('NotReadableError') >= 0) return '系统当前无法读取要共享的内容，可能正被其他程序占用。';
    if (r.indexOf('AbortError') >= 0) return '共享启动已被中断，请再试一次。';
    if (r.indexOf('NotFoundError') >= 0) return '没有找到可共享的屏幕、窗口或标签页。';
    if (r.indexOf('InvalidStateError') >= 0) return '浏览器当前状态不允许开始共享，请刷新页面后重试。';
    if (r.indexOf('OverconstrainedError') >= 0) return '当前分辨率或帧率设置不受支持，请调低后重试。';
    if (r.indexOf('TypeError') >= 0) return '浏览器返回了无效的共享参数，请刷新后重试。';
    return r;
  }

  function trimTrailingPunctuation(text) {
    return (text || '').replace(/[.?!:;,]+$/g, '').trim();
  }

  function quoteLabel(text) {
    var value = trimTrailingPunctuation(text);
    return value ? '“' + value + '”' : '“未命名”';
  }

  function localizeNotificationDetail(detail, fallbackUnknown) {
    var normalized = trimTrailingPunctuation(detail || '');
    var match;

    if (!normalized) return fallbackUnknown ? '发生了未知错误，请稍后重试。' : normalized;
    if (T[normalized]) return T[normalized];
    if (normalized === '[object Event]') return '网络连接出现异常，请稍后重试。';

    match = normalized.match(/^room with id\s+(.+?)\s+does not exist$/i);
    if (match) return '房间 ' + quoteLabel(match[1]) + ' 不存在。';

    match = normalized.match(/^room\s+(.+?)\s+does not exist$/i);
    if (match) return '房间 ' + quoteLabel(match[1]) + ' 不存在。';

    match = normalized.match(/^room with id\s+(.+?)\s+already exists$/i);
    if (match) return '房间 ' + quoteLabel(match[1]) + ' 已存在。';

    match = normalized.match(/^room\s+(.+?)\s+already exists$/i);
    if (match) return '房间 ' + quoteLabel(match[1]) + ' 已存在。';

    match = normalized.match(/^user(?:name)?\s+(.+?)\s+already exists$/i);
    if (match) return '用户 ' + quoteLabel(match[1]) + ' 已存在。';

    if (/^room is full$/i.test(normalized)) return '房间已满，暂时无法加入。';
    if (/^access denied$/i.test(normalized)) return '访问被拒绝。';
    if (/^permission denied$/i.test(normalized)) return '权限不足，当前操作被拒绝。';
    if (/^unauthorized$/i.test(normalized)) return '当前未登录或登录已失效，请重新登录。';
    if (/^forbidden$/i.test(normalized)) return '当前操作被服务器拒绝。';
    if (/^network error$/i.test(normalized)) return '网络连接异常，请稍后重试。';
    if (/^connection error$/i.test(normalized)) return '连接出现异常，请稍后重试。';
    if (/^connection closed$/i.test(normalized)) return '连接已关闭，请稍后重试。';
    if (/^disconnected$/i.test(normalized)) return '连接已断开，请稍后重试。';
    if (/^failed to fetch$/i.test(normalized)) return '请求失败，可能是网络异常，请稍后重试。';
    if (/^internal server error$/i.test(normalized)) return '服务器内部错误，请稍后重试。';
    if (/^not found$/i.test(normalized)) return '未找到对应内容。';
    if (/^(request )?timed? out$/i.test(normalized)) return '请求超时，请稍后重试。';
    if (/^invalid username or password$/i.test(normalized)) return '用户名或密码不正确。';
    if (/^you are not allowed to join this room$/i.test(normalized)) return '你没有权限加入这个房间。';
    if (/^you are not allowed to access this room$/i.test(normalized)) return '你没有权限访问这个房间。';
    if (/^you are not allowed to perform this action$/i.test(normalized)) return '你没有权限执行这个操作。';

    match = normalized.match(/^failed to join room\s*:?\s*(.+)$/i);
    if (match) return '加入房间失败：' + localizeNotificationDetail(match[1]);

    match = normalized.match(/^could not join room\s*:?\s*(.+)$/i);
    if (match) return '无法加入房间：' + localizeNotificationDetail(match[1]);

    match = normalized.match(/^failed to create room\s*:?\s*(.+)$/i);
    if (match) return '创建房间失败：' + localizeNotificationDetail(match[1]);

    match = normalized.match(/^could not create room\s*:?\s*(.+)$/i);
    if (match) return '无法创建房间：' + localizeNotificationDetail(match[1]);

    match = normalized.match(/^failed to leave room\s*:?\s*(.+)$/i);
    if (match) return '离开房间失败：' + localizeNotificationDetail(match[1]);

    match = normalized.match(/^could not leave room\s*:?\s*(.+)$/i);
    if (match) return '无法离开房间：' + localizeNotificationDetail(match[1]);

    match = normalized.match(/^failed to copy\s*:?\s*(.+)$/i);
    if (match) return '复制失败：' + localizeNotificationDetail(match[1]);

    return normalized;
  }

  function localizeText(text) {
    if (!text) return text;
    var raw = String(text);
    var trimmed = raw.trim();
    var mapped = T[trimmed];
    var match;

    if (!trimmed) return raw;
    if (mapped) return raw.replace(trimmed, mapped);
    if (trimmed === '[object Event]') return '网络连接出现异常，请稍后重试。';

    match = trimmed.match(/^Hello\s+(.+)$/);
    if (match) return '你好 ' + match[1];

    match = trimmed.match(/^Could not start presentation\. \(getDisplayMedia error\)\. (.+)$/);
    if (match) return '未开始共享：' + localizeDisplayMediaReason(match[1]);

    match = trimmed.match(/^Could not start presentation\. Are you using https\? \(mediaDevices undefined\)$/);
    if (match) return '无法开始共享：浏览器没有提供屏幕共享能力，请确认当前通过 HTTPS 访问。';

    match = trimmed.match(/^Could not start presentation\. Your browser likely doesn't support screensharing\.(.*)$/);
    if (match) return '无法开始共享：当前浏览器可能不支持屏幕共享。';

    match = trimmed.match(/^Could not start presentation\.(.*)$/);
    if (match) return '无法开始共享，请稍后重试。';

    match = trimmed.match(/^Copy Failed\s*(.+)$/);
    if (match) return '复制失败：' + localizeNotificationDetail(match[1], true);

    match = trimmed.match(/^Login Failed:\s*(.+)$/);
    if (match) return '登录失败：' + localizeNotificationDetail(match[1], true);

    match = trimmed.match(/^Logout Failed:\s*(.+)$/);
    if (match) return '退出登录失败：' + localizeNotificationDetail(match[1], true);

    match = trimmed.match(/^Unknown Event:\s*(.+)$/);
    if (match) return '收到未知服务端事件：' + localizeNotificationDetail(match[1], true);

    match = trimmed.match(/^(Error|Warning|Info):\s*(.+)$/i);
    if (match) {
      var prefixMap = { error: '错误', warning: '警告', info: '提示' };
      return (prefixMap[match[1].toLowerCase()] || match[1]) + '：' + localizeNotificationDetail(match[2], true);
    }

    var detailLocalized = localizeNotificationDetail(trimmed, false);
    if (detailLocalized !== trimTrailingPunctuation(trimmed)) return raw.replace(trimmed, detailLocalized);

    return raw;
  }

  function translateNode(root) {
    var w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, null, false), n;
    while ((n = w.nextNode())) {
      var original = n.textContent;
      var localized = localizeText(original);
      if (localized !== original) n.textContent = localized;
    }
    root.querySelectorAll('[title],[placeholder],[alt]').forEach(function (el) {
      ['title', 'placeholder', 'alt'].forEach(function (a) {
        var v = el.getAttribute(a);
        if (!v) return;
        var localized = localizeText(v);
        if (localized !== v) el.setAttribute(a, localized);
      });
    });
  }

  function hideCloseCheckbox(root) {
    root.querySelectorAll('span, p, label').forEach(function (el) {
      if (el.textContent.trim() === 'Close Room after you leave' || el.textContent.trim() === '离开后关闭房间') {
        var parent = el.closest('.MuiFormControlLabel-root') || el.parentElement;
        if (parent) {
          var cb = parent.querySelector('input[type="checkbox"]');
          if (cb && cb.checked) {
            cb.click();
          }
          parent.style.display = 'none';
        }
      }
    });
  }

  function getStartPresentationButtons(dialog) {
    var buttons = [];
    dialog.querySelectorAll('.MuiDialogActions-root button').forEach(function (btn) {
      var text = btn.textContent.trim();
      if (text === 'Start Presentation' || text === '开始共享') buttons.push(btn);
    });
    return buttons;
  }

  function saveCaptureSettings(resolution, framerate) {
    var s = loadCfg();
    var normalizedRes = normalizeResolutionKey(resolution);
    var normalizedFps = normalizeFramerateValue(framerate);
    s.resolution = normalizedRes;
    s.framerate = normalizedFps;
    saveCfg(s);
    return { res: normalizedRes, fps: normalizedFps };
  }

  function queryAllWithSelf(root, selector) {
    var nodes = [];
    if (root && root.nodeType === 1 && root.matches(selector)) nodes.push(root);
    root.querySelectorAll(selector).forEach(function (node) {
      nodes.push(node);
    });
    return nodes;
  }

  function buildFpsOptionsHtml(selectedFps) {
    var normalized = normalizeFramerateValue(selectedFps);
    var html = '';
    FPS_OPTIONS.forEach(function (fps) {
      html += '<option value="' + fps + '"' + (normalized === fps ? ' selected' : '') + '>' + fps + ' FPS</option>';
    });
    return html;
  }

  function enhanceSnackbars(root) {
    queryAllWithSelf(root, '.notistack-MuiContent, [class*="notistack-MuiContent"]').forEach(function (snack) {
      if (snack.dataset.gcSnackEnhanced) {
        translateNode(snack);
      } else {
        snack.dataset.gcSnackEnhanced = '1';
        translateNode(snack);
      }

      var message = snack.querySelector('#notistack-snackbar');
      var text = message ? (message.textContent || '').trim() : (snack.textContent || '').trim();
      var tone = 'default';
      var cls = snack.className || '';

      if (text.indexOf('未开始共享：') === 0 || text.indexOf('你已取消屏幕或窗口共享授权') >= 0) tone = 'soft-warning';
      else if (cls.indexOf('notistack-MuiContent-error') >= 0) tone = 'error';
      else if (cls.indexOf('notistack-MuiContent-success') >= 0) tone = 'success';
      else if (cls.indexOf('notistack-MuiContent-warning') >= 0) tone = 'warning';
      else if (cls.indexOf('notistack-MuiContent-info') >= 0) tone = 'info';

      if (message && !text) {
        if (tone === 'success') message.textContent = '操作已完成。';
        else if (tone === 'info') message.textContent = '有新的提示信息。';
        else if (tone === 'warning' || tone === 'soft-warning') message.textContent = '当前操作已取消，请确认后重试。';
        else message.textContent = '连接已断开，请稍后重试。';
        text = message.textContent;
      }

      snack.dataset.gcTone = tone;

      snack.querySelectorAll('button').forEach(function (btn) {
        var localized = localizeText(btn.textContent);
        if (localized !== btn.textContent) btn.textContent = localized;
      });
    });
  }

  function mountDialogSection(dialog, section) {
    var content = dialog.querySelector('.MuiDialogContent-root');
    var actions = dialog.querySelector('.MuiDialogActions-root');
    if (content) {
      content.appendChild(section);
      return;
    }
    if (actions && actions.parentElement) {
      actions.parentElement.insertBefore(section, actions);
      return;
    }
    dialog.appendChild(section);
  }

  function getShareSetupModal() {
    return document.getElementById('gc-share-setup-modal');
  }

  function closeShareSetupModal(keepLease) {
    shareSetupClaimSeq += 1;
    var modal = getShareSetupModal();
    if (modal) modal.remove();
    shareSetupPendingButton = null;
    if (!keepLease && !activeDisplayStream) releaseWebShare(false);
  }

  function createShareSetupModal(button) {
    closeShareSetupModal(false);
    var claimSeq = ++shareSetupClaimSeq;
    shareSetupPendingButton = button || null;

    var c = extCfg();
    var overlay = document.createElement('div');
    overlay.id = 'gc-share-setup-modal';
    overlay.style.cssText = 'position:fixed;inset:0;z-index:100001;background:rgba(2,6,23,.66);display:flex;align-items:center;justify-content:center;padding:18px;backdrop-filter:blur(8px)';

    var panel = document.createElement('div');
    panel.style.cssText = 'width:min(420px,100%);background:rgba(15,12,41,.96);border:1px solid rgba(124,58,237,.35);border-radius:18px;box-shadow:0 24px 80px rgba(0,0,0,.5);padding:18px 18px 16px;color:#e2e8f0';

    var html = '<div style="font-size:17px;font-weight:700;color:#f8fafc">共享前画面设置</div>';
    html += '<div style="font-size:12px;line-height:1.6;color:#94a3b8;margin-top:6px;margin-bottom:14px">这里修改后，会先保存参数，再继续打开浏览器的共享选择窗口。</div>';
    html += '<div style="margin-bottom:12px"><label style="display:block;font-size:12px;color:#94a3b8;margin-bottom:5px">分辨率</label><select id="gc-share-modal-res" style="width:100%;padding:8px 10px;background:rgba(15,12,41,.7);color:#e2e8f0;border:1px solid rgba(124,58,237,.3);border-radius:8px;font-size:13px">';
    for (var k in RESOLUTIONS) html += '<option value="' + k + '"' + (c.res === k ? ' selected' : '') + '>' + RESOLUTIONS[k].label + '</option>';
    html += '</select></div>';
    html += '<div style="margin-bottom:10px"><label style="display:block;font-size:12px;color:#94a3b8;margin-bottom:5px">帧率</label><select id="gc-share-modal-fps" style="width:100%;padding:8px 10px;background:rgba(15,12,41,.7);color:#e2e8f0;border:1px solid rgba(124,58,237,.3);border-radius:8px;font-size:13px">' + buildFpsOptionsHtml(c.fps) + '</select></div>';
    html += '<div id="gc-share-control-hint" style="font-size:11px;color:#64748b;margin-bottom:14px">正在确认房间共享状态...</div>';
    html += '<div style="display:flex;gap:10px;justify-content:flex-end"><button id="gc-share-modal-cancel" type="button" style="padding:8px 14px;background:transparent;color:#cbd5e1;border:1px solid rgba(124,58,237,.25);border-radius:10px;cursor:pointer;font-size:13px">取消</button><button id="gc-share-modal-confirm" type="button" disabled style="padding:8px 14px;background:linear-gradient(135deg,#7c3aed,#06b6d4);color:#fff;border:none;border-radius:10px;cursor:pointer;font-size:13px;font-weight:600">确认中...</button></div>';
    panel.innerHTML = html;
    overlay.appendChild(panel);
    document.body.appendChild(overlay);

    var fpsSelect = panel.querySelector('#gc-share-modal-fps');
    var resSelect = panel.querySelector('#gc-share-modal-res');
    var confirmButton = panel.querySelector('#gc-share-modal-confirm');
    var controlHint = panel.querySelector('#gc-share-control-hint');

    claimWebShare().then(function () {
      if (claimSeq !== shareSetupClaimSeq || !confirmButton.isConnected) {
        releaseWebShare(false);
        return;
      }
      confirmButton.disabled = false;
      confirmButton.textContent = '继续共享';
      controlHint.textContent = '共享权限已取得，可以选择要共享的画面。';
    }).catch(function (error) {
      if (claimSeq !== shareSetupClaimSeq || !confirmButton.isConnected) return;
      confirmButton.disabled = true;
      confirmButton.textContent = '暂不可共享';
      controlHint.style.color = '#f87171';
      controlHint.textContent = error && error.message === 'share_locked'
        ? '当前已有成员正在共享，请等待对方停止后再试。'
        : '共享状态确认失败，请关闭后重试。';
    });

    panel.querySelector('#gc-share-modal-cancel').addEventListener('click', function () {
      closeShareSetupModal(false);
    });

    overlay.addEventListener('click', function (event) {
      if (event.target === overlay) closeShareSetupModal(false);
    });

    panel.querySelector('#gc-share-modal-confirm').addEventListener('click', function () {
      var btn = shareSetupPendingButton;
      if (!btn || !btn.isConnected || confirmButton.disabled || !webShareLease) return closeShareSetupModal(false);
      var state = saveCaptureSettings(resSelect.value, fpsSelect.value);
      fpsSelect.value = state.fps;
      try { primeDisplayMediaRequestForShare(); } catch (e) { releaseWebShare(false); }
      shareSetupBypassButton = btn;
      shareSetupBypassUntil = Date.now() + 4000;
      closeShareSetupModal(true);
      btn.click();
      if (webShareSafetyTimer) clearTimeout(webShareSafetyTimer);
      webShareSafetyTimer = setTimeout(function () {
        if (!activeDisplayStream) releaseWebShare(false);
      }, 15000);
    });
  }

  function collectShareStartButtons(root) {
    var buttons = [];
    queryAllWithSelf(root, 'svg path').forEach(function (path) {
      if (path.getAttribute('d') !== PRESENT_TO_ALL_ICON_PATH) return;
      var btn = path.closest('button');
      if (btn && buttons.indexOf(btn) < 0) buttons.push(btn);
    });
    return buttons;
  }

  function isEditableTarget(node) {
    if (!node || node === document.body) return false;
    if (node.isContentEditable) return true;
    if (node.closest && node.closest('[contenteditable="true"]')) return true;
    var tag = ((node.tagName || '') + '').toUpperCase();
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true;
    if (node.getAttribute && node.getAttribute('role') === 'textbox') return true;
    return false;
  }

  function findShareStartButton() {
    var buttons = collectShareStartButtons(document.body);
    for (var i = 0; i < buttons.length; i++) {
      if (buttons[i] && buttons[i].isConnected && !buttons[i].disabled) return buttons[i];
    }
    return null;
  }

  function interceptShareButtonClicks(root) {
    collectShareStartButtons(root).forEach(function (btn) {
      if (btn.dataset.gcShareInterceptBound) return;
      btn.dataset.gcShareInterceptBound = '1';
      btn.addEventListener('click', function (event) {
        var bypassActive = shareSetupBypassButton === btn && Date.now() < shareSetupBypassUntil;
        if (bypassActive) {
          shareSetupBypassButton = null;
          shareSetupBypassUntil = 0;
          return;
        }
        if (btn.disabled) return;
        event.preventDefault();
        event.stopPropagation();
        event.stopImmediatePropagation();
        createShareSetupModal(btn);
      }, true);
    });
  }

  function interceptShareHotkey() {
    if (document.body.dataset.gcShareHotkeyBound) return;
    document.body.dataset.gcShareHotkeyBound = '1';
    document.addEventListener('keydown', function (event) {
      if (event.defaultPrevented) return;
      if (event.repeat) return;
      if (event.ctrlKey || event.metaKey || event.altKey) return;

      if (event.key === 'Escape' && getShareSetupModal()) {
        event.preventDefault();
        event.stopPropagation();
        closeShareSetupModal();
        return;
      }

      if (event.key !== 's' && event.key !== 'S' && event.code !== 'KeyS') return;
      if (isEditableTarget(event.target)) return;
      if (getShareSetupModal()) {
        event.preventDefault();
        event.stopPropagation();
        return;
      }

      var btn = findShareStartButton();
      if (!btn) return;

      event.preventDefault();
      event.stopPropagation();
      event.stopImmediatePropagation();
      createShareSetupModal(btn);
    }, true);
  }

  function injectShareQuickSettings(dialog) {
    var startButtons = getStartPresentationButtons(dialog);
    if (!startButtons.length || dialog.querySelector('#gc-share-quick-settings')) return;

    var c = extCfg();
    var wrap = document.createElement('div');
    wrap.id = 'gc-share-quick-settings';
    wrap.style.cssText = 'margin:12px 24px 0;padding:12px;border:1px solid rgba(124,58,237,.2);border-radius:10px;background:rgba(15,12,41,.35)';

    var html = '<div style="font-size:13px;font-weight:600;color:#a78bfa;margin-bottom:10px">共享前画面设置</div>';
    html += '<div style="margin-bottom:10px"><label style="display:block;font-size:12px;color:#94a3b8;margin-bottom:4px">分辨率</label><select id="gc-share-res" style="width:100%;padding:6px 8px;background:rgba(15,12,41,.6);color:#e2e8f0;border:1px solid rgba(124,58,237,.3);border-radius:6px;font-size:13px">';
    for (var k in RESOLUTIONS) html += '<option value="' + k + '"' + (c.res === k ? ' selected' : '') + '>' + RESOLUTIONS[k].label + '</option>';
    html += '</select></div>';
    html += '<div style="margin-bottom:10px"><label style="display:block;font-size:12px;color:#94a3b8;margin-bottom:4px">帧率</label><select id="gc-share-fps" style="width:100%;padding:6px 8px;background:rgba(15,12,41,.6);color:#e2e8f0;border:1px solid rgba(124,58,237,.3);border-radius:6px;font-size:13px">' + buildFpsOptionsHtml(c.fps) + '</select></div>';
    html += '<div id="gc-share-hint" style="font-size:11px;color:#64748b">修改后会在本次点击“开始共享”时直接生效。</div>';
    wrap.innerHTML = html;

    mountDialogSection(dialog, wrap);

    var resSelect = wrap.querySelector('#gc-share-res');
    var fpsSelect = wrap.querySelector('#gc-share-fps');
    var hint = wrap.querySelector('#gc-share-hint');

    function setHint(text, color) {
      hint.textContent = text;
      hint.style.color = color || '#64748b';
    }

    function syncCaptureSettings(showSavedHint) {
      var state = saveCaptureSettings(resSelect.value, fpsSelect.value);
      fpsSelect.value = state.fps;
      if (showSavedHint) {
        setHint('已保存: ' + RESOLUTIONS[state.res].label + ' / ' + state.fps + ' FPS', '#34d399');
        clearTimeout(wrap._hintTimer);
        wrap._hintTimer = setTimeout(function () {
          setHint('修改后会在本次点击“开始共享”时直接生效。');
        }, 1800);
      } else {
        setHint('修改后会在本次点击“开始共享”时直接生效。');
      }
      return state;
    }

    resSelect.addEventListener('change', function () {
      syncCaptureSettings(true);
    });
    fpsSelect.addEventListener('change', function () {
      syncCaptureSettings(true);
    });

    startButtons.forEach(function (btn) {
      if (btn.dataset.gcShareQuickBound) return;
      btn.dataset.gcShareQuickBound = '1';
      btn.addEventListener('click', function () {
        syncCaptureSettings(false);
      }, true);
    });
  }

  /* ── 隐藏 Screego 自带的帧率和用户名，并增强共享弹窗 ── */
  function hideScreegoSettings(root) {
    queryAllWithSelf(root, '.MuiDialogTitle-root').forEach(function (title) {
      if (title.textContent.trim() !== '设置' && title.textContent.trim() !== 'Settings') return;
      var dialog = title.closest('.MuiPaper-root');
      if (!dialog) return;
      dialog.querySelectorAll('.MuiFormControl-root').forEach(function (fc) {
        var text = fc.textContent.trim();
        if (text.indexOf('帧率') >= 0 || text.indexOf('FrameRate') >= 0) fc.style.display = 'none';
        if (text.indexOf('用户名') >= 0 || text.indexOf('Username') >= 0) fc.style.display = 'none';
      });
      dialog.querySelectorAll('.MuiDialogActions-root button').forEach(function (btn) {
        var t = btn.textContent.trim();
        if (t === '保存' || t === 'Save') {
          btn.id = 'gc-hidden-save';
          btn.style.cssText = 'position:absolute;opacity:0;pointer-events:none;height:0;width:0;overflow:hidden;padding:0;border:0';
        }
        if (t === '取消' || t === 'Cancel') btn.textContent = '关闭';
      });
      if (!dialog.querySelector('#gc-quality-injected')) injectQualityIntoDialog(dialog);
    });

    queryAllWithSelf(root, '.MuiDialogActions-root').forEach(function (actions) {
      var dialog = actions.closest('.MuiPaper-root');
      if (!dialog) return;
      injectShareQuickSettings(dialog);
    });
  }

  function injectQualityIntoDialog(dialog) {
    var content = dialog.querySelector('.MuiDialogContent-root') || dialog;
    var c = extCfg();
    var wrap = document.createElement('div');
    wrap.id = 'gc-quality-injected';
    wrap.style.cssText = 'margin-top:12px;padding-top:12px;border-top:1px solid rgba(124,58,237,.2)';

    var html = '<div style="font-size:13px;font-weight:600;color:#a78bfa;margin-bottom:10px">共享画质设置</div>';

    html += '<div style="margin-bottom:10px"><label style="display:block;font-size:12px;color:#94a3b8;margin-bottom:4px">分辨率</label><select id="gc-res" style="width:100%;padding:6px 8px;background:rgba(15,12,41,.6);color:#e2e8f0;border:1px solid rgba(124,58,237,.3);border-radius:6px;font-size:13px">';
    for (var k in RESOLUTIONS) html += '<option value="' + k + '"' + (c.res === k ? ' selected' : '') + '>' + RESOLUTIONS[k].label + '</option>';
    html += '</select></div>';

    html += '<div style="margin-bottom:10px"><label style="display:block;font-size:12px;color:#94a3b8;margin-bottom:4px">帧率</label><select id="gc-fps" style="width:100%;padding:6px 8px;background:rgba(15,12,41,.6);color:#e2e8f0;border:1px solid rgba(124,58,237,.3);border-radius:6px;font-size:13px">' + buildFpsOptionsHtml(c.fps) + '</select></div>';

    html += '<div style="margin-bottom:6px"><label style="display:block;font-size:12px;color:#94a3b8;margin-bottom:6px">优先模式</label><div style="display:flex;gap:6px">';
    for (var qk in QUALITY) html += '<div class="gc-qbtn' + (c.qm === qk ? ' gc-active' : '') + '" data-m="' + qk + '" style="flex:1;padding:6px 4px;border:1px solid rgba(124,58,237,.3);background:rgba(15,12,41,.6);color:#e2e8f0;border-radius:6px;cursor:pointer;text-align:center;font-size:11px;transition:.2s">' + QUALITY[qk].label + '</div>';
    html += '</div><div id="gc-qhint" style="font-size:11px;color:#64748b;margin-top:4px">' + (QUALITY[c.qm] ? QUALITY[c.qm].desc : '') + '</div></div>';

    html += '<div style="margin-bottom:10px"><label style="display:block;font-size:12px;color:#94a3b8;margin-bottom:4px">音频共享</label><select id="gc-audio" style="width:100%;padding:6px 8px;background:rgba(15,12,41,.6);color:#e2e8f0;border:1px solid rgba(124,58,237,.3);border-radius:6px;font-size:13px">';
    for (var ak in AUDIO_MODES) html += '<option value="' + ak + '"' + (c.audio === ak ? ' selected' : '') + '>' + AUDIO_MODES[ak].label + '</option>';
    html += '</select><div id="gc-audio-hint" style="font-size:11px;color:#64748b;margin-top:4px">' + (AUDIO_MODES[c.audio] ? AUDIO_MODES[c.audio].desc : '') + '</div>';
    html += '<div style="font-size:11px;color:#fbbf24;margin-top:4px">音频模式会在下次重新开始共享时生效。</div></div>';

    html += '<div style="margin-bottom:10px;padding:8px 10px;background:rgba(6,182,212,.08);border:1px solid rgba(6,182,212,.2);border-radius:6px">';
    html += '<label style="display:flex;align-items:center;gap:8px;cursor:pointer;font-size:12px;color:#e2e8f0">';
    html += '<input type="checkbox" id="gc-sfu-toggle" style="accent-color:#06b6d4"' + (sfuMode ? ' checked' : '') + '>';
    html += '<span style="font-weight:600">允许多人时自动启用 SFU</span></label>';
    html += '<div style="font-size:11px;color:#64748b;margin-top:4px;margin-left:24px">开启后，只有在房主之外还有 2 位及以上观众时，当前共享才会自动切到 SFU；只有 1 位观众时仍保持默认共享方式。关闭后将始终使用默认共享方式。</div>';
    html += '</div>';

    html += '<button id="gc-apply" style="width:100%;padding:8px;margin-top:6px;background:linear-gradient(135deg,#7c3aed,#06b6d4);color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:13px;font-weight:600">应用画质设置</button>';

    wrap.innerHTML = html;
    content.appendChild(wrap);

    var fpsS = document.getElementById('gc-fps');
    var audioSelect = document.getElementById('gc-audio');
    audioSelect.addEventListener('change', function () {
      document.getElementById('gc-audio-hint').textContent = AUDIO_MODES[audioSelect.value] ? AUDIO_MODES[audioSelect.value].desc : '';
    });

    wrap.querySelectorAll('.gc-qbtn').forEach(function (btn) {
      btn.addEventListener('click', function () {
        wrap.querySelectorAll('.gc-qbtn').forEach(function (b) { b.classList.remove('gc-active'); b.style.background = 'rgba(15,12,41,.6)'; b.style.borderColor = 'rgba(124,58,237,.3)'; });
        btn.classList.add('gc-active');
        btn.style.background = 'linear-gradient(135deg,#7c3aed,#06b6d4)';
        btn.style.borderColor = 'transparent';
        document.getElementById('gc-qhint').textContent = QUALITY[btn.dataset.m] ? QUALITY[btn.dataset.m].desc : '';
      });
    });

    wrap.querySelectorAll('.gc-active').forEach(function (btn) {
      btn.style.background = 'linear-gradient(135deg,#7c3aed,#06b6d4)';
      btn.style.borderColor = 'transparent';
    });

    document.getElementById('gc-apply').addEventListener('click', function () {
      var myRes = document.getElementById('gc-res').value;
      var myFps = normalizeFramerateValue(fpsS.value);
      var myAudio = audioSelect.value;
      var act = wrap.querySelector('.gc-active');
      var myQm = act ? act.dataset.m : 'balanced';
      var sfuToggle = document.getElementById('gc-sfu-toggle');
      sfuMode = sfuToggle ? sfuToggle.checked : false;

      var hiddenSave = document.getElementById('gc-hidden-save');
      if (hiddenSave) hiddenSave.click();

      setTimeout(function () {
        var s = loadCfg();
        var oldAudio = AUDIO_MODES[s.audioMode] ? s.audioMode : 'app';
        var oldSfuMode = !!s.sfuMode;
        s.resolution = normalizeResolutionKey(myRes);
        s.framerate = normalizeFramerateValue(myFps);
        s.qualityMode = myQm;
        s.audioMode = myAudio;
        s.sfuMode = sfuMode;
        saveCfg(s);
        if (oldSfuMode && !sfuMode) resetAutoSfuState();

        if (activeDisplayStream) {
          var roomId = currentRoomId();
          syncHostSfuState(roomId);
        }

        var err = applyLiveConstraints();
        applyAll();
        var btn = document.getElementById('gc-apply');
        var modeText = currentSfuMode() ? ' [自动 SFU]' : '';
        var audioText = activeDisplayStream && oldAudio !== myAudio ? '，音频下次共享生效' : '';
        if (err) { btn.textContent = err; btn.style.color = '#fbbf24'; }
        else {
          btn.textContent = '已应用 (' + RESOLUTIONS[myRes].label + ' ' + myFps + 'fps' + modeText + audioText + ')';
          btn.style.color = '#34d399';
        }
        setTimeout(function () { btn.textContent = '应用画质设置'; btn.style.color = ''; }, 2500);
      }, 100);
    });
  }

  /* ── 连接性能显示 ── */
  var statsOverlayId = 'gc-stats-overlay';
  var statsCollapsed = true;
  function startConnectionStats() {
    setInterval(function () {
      var inRoom = /[?&]room=/.test(location.search);
      var overlay = document.getElementById(statsOverlayId);
      if (!inRoom) { if (overlay) overlay.style.display = 'none'; return; }
      if (!overlay) {
        overlay = document.createElement('div');
        overlay.id = statsOverlayId;
        overlay.style.cssText = 'position:fixed;top:56px;left:12px;z-index:9999;background:rgba(15,12,41,.9);color:#e2e8f0;border:1px solid rgba(124,58,237,.25);border-radius:10px;font:12px/1.6 -apple-system,BlinkMacSystemFont,monospace;backdrop-filter:blur(10px);cursor:pointer;user-select:none;transition:all .2s;min-width:32px';
        overlay.innerHTML = '<div id="gc-stats-header" style="padding:4px 10px;display:flex;align-items:center;gap:6px"><span style="font-size:14px">📊</span><span id="gc-stats-toggle" style="font-size:11px;color:#94a3b8">展开</span></div><div id="gc-stats-body" style="padding:0 10px 8px;display:none;white-space:pre"></div>';
        overlay.addEventListener('click', function () {
          statsCollapsed = !statsCollapsed;
          var body = document.getElementById('gc-stats-body');
          var toggle = document.getElementById('gc-stats-toggle');
          if (body) body.style.display = statsCollapsed ? 'none' : 'block';
          if (toggle) toggle.textContent = statsCollapsed ? '展开' : '收起';
        });
        document.body.appendChild(overlay);
      }
      overlay.style.display = 'block';
      var body = document.getElementById('gc-stats-body');
      var primaryDescriptor = getStatsPeerDescriptor();
      getStatsPeerDescriptors().forEach(function (descriptor) {
        updateStats(body, descriptor, !!primaryDescriptor && descriptor.pc === primaryDescriptor.pc);
      });
      if (body && statsCollapsed) {
        var descriptor = primaryDescriptor;
        if (descriptor && descriptor.pc) {
          var pc = descriptor.pc;
          var pathText = pc._lastConnType ? pc._lastConnType : (describePeerConnectionState(pc) || '链路检测中');
          var rttText = pc._lastRtt ? pc._lastRtt : (pc._gcStatsFetchPromise ? '读取中...' : '...');
          var sfuText = getSfuStatusLabel();
          document.getElementById('gc-stats-toggle').textContent = pathText + ' | SFU ' + sfuText + ' | ' + rttText;
        } else {
          document.getElementById('gc-stats-toggle').textContent = 'SFU ' + getSfuStatusLabel();
        }
      }
    }, 2000);
  }

  function reportStats(data) {
    try {
      fetch(STATS_URL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data)
      }).catch(function () {});
    } catch (e) {}
  }

  function updateStats(overlay, descriptor, showOverlay) {
    if (!descriptor || !descriptor.pc) {
      if (showOverlay && overlay) overlay.textContent = buildStatsStatusText('等待连接...', null);
      return;
    }
    var pc = descriptor.pc;
    var mode = descriptor.mode;
    if (!peerConnectionCanReportStats(pc)) {
      if (showOverlay && !statsCollapsed && overlay) overlay.textContent = pc._gcLastStatsText || buildStatsStatusText('连接建立中...', pc);
      return;
    }
    if (pc._gcStatsFetchPromise) {
      if (showOverlay && overlay && !statsCollapsed) {
        overlay.textContent = pc._gcLastStatsText || buildStatsStatusText('正在读取链路数据...', pc);
      }
      return;
    }

    var prevSentBytes = pc._prevSentBytes || 0;
    var prevRecvBytes = pc._prevRecvBytes || 0;
    var prevTime = pc._prevTime || Date.now();
    var requestSeq = ++statsFetchRequestSeq;
    var trackedPeerId = getTrackedPeerId(pc);
    if (showOverlay && overlay) {
      overlay.dataset.gcStatsPeerId = trackedPeerId;
      overlay.dataset.gcStatsRequestSeq = String(requestSeq);
      if (!statsCollapsed) overlay.textContent = pc._gcLastStatsText || buildStatsStatusText('正在读取链路数据...', pc);
    }

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
        pc._gcSourceChanged = false;
        pc._gcResolutionChanged = false;
        if (typeof GameCastWebRTCQuality !== 'undefined') {
          if (!qualityState) qualityState = GameCastWebRTCQuality.createQualityState(Date.now());
          qualityState = GameCastWebRTCQuality.evaluateQualitySample(qualityState, diagnosticSample).state;
          pc._gcQualityState = qualityState;
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
      var currentFlowKbps = Math.max(targetKbpsValue, upKbpsValue || 0, downKbpsValue || 0);
      var estimatedMembers = Math.max(1, getMemberCountEstimate());
      var estimatedViewers = Math.max(1, estimatedMembers - 1);
      var sfuSuggested = Math.max(1, Math.floor((SERVER_PROFILE.safeNetMbps * 1000) / Math.max(1, currentFlowKbps)));
      var turnSuggested = Math.max(1, Math.floor((SERVER_PROFILE.safeNetMbps * 1000) / Math.max(1, currentFlowKbps * 2)));
      var serverLoadKbps = 0;
      if (mode === 'sfu-host' || mode === 'sfu-viewer') {
        serverLoadKbps = currentFlowKbps * estimatedViewers;
      } else if (connType.indexOf('TURN 中继') === 0) {
        serverLoadKbps = currentFlowKbps * estimatedViewers * 2;
      }
      var serverLoadRatio = serverLoadKbps > 0 ? (serverLoadKbps / (SERVER_PROFILE.safeNetMbps * 1000)) : 0;
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
          '🖥 服务器(估算): ' + SERVER_PROFILE.cores + 'C/' + SERVER_PROFILE.ramGB + 'G/' + SERVER_PROFILE.netMbps + 'M  |  当前估算负载 ' + formatMbps(serverLoadKbps / 1000) + '\n' +
          '📦 参考容量(估算): SFU 约≤' + sfuSuggested + ' 人  TURN 约≤' + turnSuggested + ' 人  |  ' + serverNote + '\n' +
          '📝 说明: 当前为发送侧视角；服务器负载与容量为按当前码率推算';
      }
      pc._gcLastStatsText = statsText;
      if (showOverlay && overlay && !statsCollapsed &&
        overlay.dataset.gcStatsPeerId === trackedPeerId &&
        overlay.dataset.gcStatsRequestSeq === String(requestSeq)
      ) {
        overlay.textContent = statsText;
      }

      pc._gcStatsReportCounter = (pc._gcStatsReportCounter || 0) + 1;
      if (pc._gcStatsReportCounter >= 5) {
        pc._gcStatsReportCounter = 0;
        var room = (location.search.match(/[?&]room=([^&]+)/) || [])[1] || '';
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
          targetKbps: adaptiveTargetKbpsValue + 'kbps',
          adaptive: getAdaptiveStatusLabel(pc),
          serverLoadKbps: Math.round(serverLoadKbps) + 'kbps',
          serverNote: serverNote,
          estimatedCapacityKbps: Math.round(serverLoadKbps)
        };
        if (diagnosticSample) {
          [
            'path', 'transport', 'localCandidateType', 'remoteCandidateType',
            'localAddressFamily', 'remoteAddressFamily', 'generation', 'valid',
            'invalidReason', 'rttMs', 'jitterMs', 'lossPercent', 'upKbps',
            'downKbps', 'availableOutgoingBitrate', 'targetBitrate',
            'packetsReceivedDelta', 'packetsLostDelta', 'packetsSentDelta',
            'framesDecodedDelta', 'framesDroppedDelta', 'framesEncodedDelta',
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
          Object.assign(reportPayload, {
            adaptivePolicyVersion: adaptiveState.policyVersion,
            adaptiveDecision: adaptiveDecision,
            adaptiveLevel: adaptiveState.level,
            adaptiveScale: adaptiveProfile.scaleResolutionDownBy,
            adaptiveMaxBitrate: adaptiveProfile.maxBitrate
          });
        }
        reportStats(reportPayload);
        if (adaptiveDecision) pc._gcAdaptivePendingDecision = '';
      }
    }).catch(function (err) {
      if (pc.connectionState === 'closed') return;
      var fallbackText = pc._gcLastStatsText || buildStatsStatusText(
        err && err.message === 'timeout' ? '读取链路数据超时，稍后重试...' : '读取链路数据失败，稍后重试...',
        pc
      );
      pc._gcLastStatsText = fallbackText;
      if (showOverlay && overlay && !statsCollapsed &&
        overlay.dataset.gcStatsPeerId === trackedPeerId &&
        overlay.dataset.gcStatsRequestSeq === String(requestSeq)
      ) {
        overlay.textContent = fallbackText;
      }
    }).finally(function () {
      if (pc._gcStatsFetchPromise === statsPromise) pc._gcStatsFetchPromise = null;
    });
  }

  /* ── 主题 ── */
  function applyTheme() {
    document.title = 'GameCast';
    var existingFavicon = document.querySelector('link[rel="icon"]');
    if (existingFavicon) existingFavicon.href = '/favicon.ico';
    else { var l = document.createElement('link'); l.rel = 'icon'; l.type = 'image/png'; l.href = '/favicon.ico'; document.head.appendChild(l); }

    if (document.getElementById('gc-theme')) return;
    var s = document.createElement('style');
    s.id = 'gc-theme';
    s.textContent =
      'body, html { background: linear-gradient(135deg, #0f0c29 0%, #1a1a2e 40%, #16213e 100%) !important; min-height: 100vh; }' +
      'body::before { content: ""; position: fixed; top: 0; left: 0; width: 100%; height: 100%; background-image: radial-gradient(2px 2px at 20% 30%, rgba(124,58,237,.3), transparent), radial-gradient(2px 2px at 40% 70%, rgba(6,182,212,.25), transparent), radial-gradient(1px 1px at 60% 20%, rgba(244,114,182,.2), transparent), radial-gradient(1px 1px at 80% 50%, rgba(167,139,250,.3), transparent), radial-gradient(2px 2px at 10% 80%, rgba(34,211,238,.2), transparent); pointer-events: none; z-index: 0; animation: gc-twinkle 8s ease-in-out infinite alternate; }' +
      '@keyframes gc-twinkle { 0%{opacity:.6} 100%{opacity:1} }' +
      '@keyframes gc-fadeIn { from{opacity:0;transform:scale(.8)} to{opacity:1;transform:scale(1)} }' +
      '.MuiPaper-root, .MuiCard-root { background: rgba(15,12,41,.75) !important; backdrop-filter: blur(12px); border: 1px solid rgba(124,58,237,.25) !important; border-radius: 16px !important; box-shadow: 0 8px 32px rgba(0,0,0,.4) !important; }' +
      '.MuiInputBase-root, .MuiOutlinedInput-root { background: rgba(15,12,41,.6) !important; border-radius: 10px !important; color: #e2e8f0 !important; }' +
      '.MuiInputBase-root input { color: #e2e8f0 !important; }' +
      '.MuiInputLabel-root, .MuiFormLabel-root { color: #a78bfa !important; }' +
      '.MuiOutlinedInput-notchedOutline { border-color: rgba(124,58,237,.4) !important; }' +
      '.MuiOutlinedInput-root:hover .MuiOutlinedInput-notchedOutline { border-color: #7c3aed !important; }' +
      '.MuiButton-contained { background: linear-gradient(135deg, #7c3aed, #06b6d4) !important; color: #fff !important; font-weight: 600 !important; border-radius: 10px !important; text-transform: none !important; box-shadow: 0 4px 15px rgba(124,58,237,.4) !important; }' +
      '.MuiButton-contained:hover { box-shadow: 0 6px 25px rgba(124,58,237,.6) !important; transform: translateY(-1px); }' +
      '.MuiButton-outlined { border-color: rgba(124,58,237,.5) !important; color: #a78bfa !important; border-radius: 10px !important; }' +
      '*, .MuiTypography-root, p, span, label { color: #e2e8f0; }' +
      '.MuiTypography-h4, .MuiTypography-h5 { background: linear-gradient(135deg, #a78bfa, #22d3ee); -webkit-background-clip: text; -webkit-text-fill-color: transparent; font-weight: 700 !important; }' +
      'a { color: #22d3ee !important; }' +
      '.MuiToolbar-root { background: linear-gradient(90deg, rgba(15,12,41,.95), rgba(26,26,46,.95)) !important; border-bottom: 1px solid rgba(124,58,237,.3); }' +
      '.MuiCheckbox-root { color: #7c3aed !important; }' +
      '.MuiTooltip-tooltip { background: rgba(15,12,41,.95) !important; border: 1px solid rgba(124,58,237,.3); border-radius: 8px !important; }' +
      '.MuiBadge-badge { background: #7c3aed !important; }' +
      '.MuiSlider-root { color: #7c3aed !important; }' +
      '.MuiSlider-thumb { background: #a78bfa !important; }' +
      '.MuiIconButton-root { color: #a78bfa !important; }' +
      '.MuiIconButton-root:hover { background: rgba(124,58,237,.15) !important; }' +
      '#enh-exit-btn:hover { color: #ef4444 !important; }' +
      '#reg-modal > div { background: rgba(15,12,41,.95) !important; border: 1px solid rgba(124,58,237,.4) !important; border-radius: 16px !important; backdrop-filter: blur(12px); }' +
      'div[style*="position: absolute"][style*="bottom"] { color: rgba(167,139,250,.5) !important; }' +
      '.notistack-SnackbarContainer { z-index: 100002 !important; }' +
      '.notistack-SnackbarContainer .notistack-MuiContent { background: linear-gradient(135deg, rgba(15,12,41,.94), rgba(30,41,59,.92)) !important; color: #e2e8f0 !important; border: 1px solid rgba(148,163,184,.18) !important; border-left: 4px solid rgba(148,163,184,.4) !important; border-radius: 14px !important; box-shadow: 0 18px 50px rgba(2,6,23,.35) !important; backdrop-filter: blur(12px); min-width: 320px; max-width: 560px; }' +
      '.notistack-SnackbarContainer .notistack-MuiContent[data-gc-tone="success"] { background: linear-gradient(135deg, rgba(6,78,59,.94), rgba(15,23,42,.94)) !important; border-color: rgba(52,211,153,.28) !important; border-left-color: #34d399 !important; }' +
      '.notistack-SnackbarContainer .notistack-MuiContent[data-gc-tone="info"] { background: linear-gradient(135deg, rgba(8,47,73,.94), rgba(15,23,42,.94)) !important; border-color: rgba(34,211,238,.24) !important; border-left-color: #22d3ee !important; }' +
      '.notistack-SnackbarContainer .notistack-MuiContent[data-gc-tone="warning"], .notistack-SnackbarContainer .notistack-MuiContent[data-gc-tone="soft-warning"] { background: linear-gradient(135deg, rgba(69,39,12,.95), rgba(15,23,42,.94)) !important; border-color: rgba(251,191,36,.26) !important; border-left-color: #fbbf24 !important; }' +
      '.notistack-SnackbarContainer .notistack-MuiContent[data-gc-tone="error"] { background: linear-gradient(135deg, rgba(69,10,10,.94), rgba(30,41,59,.94)) !important; border-color: rgba(248,113,113,.24) !important; border-left-color: #f87171 !important; }' +
      '.notistack-SnackbarContainer .notistack-MuiContent #notistack-snackbar { font-size: 13px !important; line-height: 1.6 !important; letter-spacing: 0 !important; }' +
      '.notistack-SnackbarContainer .notistack-MuiContent button { color: #cbd5e1 !important; text-transform: none !important; letter-spacing: 0 !important; font-weight: 600 !important; border-radius: 8px !important; }' +
      '.notistack-SnackbarContainer .notistack-MuiContent button:hover { background: rgba(255,255,255,.08) !important; }';
    document.head.appendChild(s);
  }

  /* ── 替换 logo ── */
  function replaceLogo(root) {
    root.querySelectorAll('img').forEach(function (img) {
      if (img.dataset.gcLogo) return;
      if (img.src && (img.src.indexOf('logo') >= 0 || img.alt === 'logo')) {
        img.dataset.gcLogo = '1';
        img.src = '/favicon.ico';
        img.style.filter = 'drop-shadow(0 0 20px rgba(124,58,237,.4))';
      }
    });
  }

  /* ── 监听 URL ── */
  function activateConfirmedRoom(roomId) {
    if (!roomId) return;
    addRoomHistory(roomId);
    ensureWebPresence(roomId).then(function (presence) {
      if (presence && activeRoomSocketRoomId === roomId) startRoomStateHeartbeat();
    });
  }

  function monitorRoomUrl() {
    var lastUrl = location.href;
    var lastRoomId = currentRoomId();

    setInterval(function () {
      if (location.href !== lastUrl) {
        lastUrl = location.href;
        var previousRoomId = lastRoomId;
        var roomId = currentRoomId();
        if (previousRoomId && !roomId) {
          releaseWebShare(false);
          leaveWebPresence(false);
          if (activeDisplayStream) {
            resetAutoSfuState();
            resetAdaptiveStreamingState();
          }
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

  /* ── 退出房间按钮 ── */
  var exitBtnId = 'enh-exit-btn';
  var EXIT_SVG = '<svg xmlns="http://www.w3.org/2000/svg" height="24" viewBox="0 0 24 24" width="24" fill="currentColor"><path d="M10.09 15.59L11.5 17l5-5-5-5-1.41 1.41L12.67 11H3v2h9.67l-2.58 2.59zM19 3H5c-1.11 0-2 .9-2 2v4h2V5h14v14H5v-4H3v4c0 1.1.89 2 2 2h14c1.1 0 2-.9 2-2V5c0-1.1-.9-2-2-2z"/></svg>';
  function monitorExitButton() {
    setInterval(function () {
      var inRoom = /[?&]room=/.test(location.search);
      var existing = document.getElementById(exitBtnId);
      if (inRoom && !existing) {
        var refBtn = document.querySelector('.MuiIconButton-root');
        if (!refBtn) return;
        var container = refBtn.parentElement;
        if (!container) return;
        var btn = document.createElement('button');
        btn.id = exitBtnId;
        btn.className = refBtn.className;
        btn.innerHTML = EXIT_SVG;
        btn.title = '退出房间';
        btn.addEventListener('click', function () { leaveRoomGracefully(); });
        container.appendChild(btn);
      } else if (!inRoom && existing) {
        existing.remove();
      }
    }, 800);
  }

  /* ── 房间历史下拉 ── */
  function injectHistoryDropdown() {
    if (document.getElementById('enh-hist-arrow')) return;
    var idInput = null;
    document.querySelectorAll('input').forEach(function (inp) {
      if (idInput) return;
      if (isRoomIdField(inp)) idInput = inp;
    });
    if (!idInput || idInput.dataset.enhHistory) return;
    idInput.dataset.enhHistory = '1';
    var inputRect = idInput.getBoundingClientRect();
    if (inputRect.width < 30) { idInput.dataset.enhHistory = ''; return; }

    var arrow = document.createElement('div');
    arrow.id = 'enh-hist-arrow';
    arrow.innerHTML = '&#9662;';
    arrow.style.cssText = 'position:absolute;right:6px;top:50%;transform:translateY(-50%);cursor:pointer;color:#a78bfa;font-size:16px;z-index:101;user-select:none;padding:4px 6px;line-height:1;transition:color .2s';
    arrow.addEventListener('mouseenter', function () { arrow.style.color = '#22d3ee'; });
    arrow.addEventListener('mouseleave', function () { arrow.style.color = '#a78bfa'; });

    var parent = idInput.parentElement;
    if (parent) {
      if (getComputedStyle(parent).position === 'static') parent.style.position = 'relative';
      parent.appendChild(arrow);
    }

    var list = document.createElement('div');
    list.id = 'enh-hist-list';
    list.style.cssText = 'position:absolute;top:100%;left:0;right:0;background:rgba(15,12,41,.97);border:1px solid rgba(124,58,237,.35);border-top:none;border-radius:0 0 10px 10px;max-height:180px;overflow-y:auto;z-index:200;display:none;font-size:12px;backdrop-filter:blur(16px);box-shadow:0 8px 24px rgba(0,0,0,.4)';
    if (parent) parent.appendChild(list);

    function refreshList() {
      var history = loadHistory();
      list.innerHTML = '';
      if (!history.length) { list.innerHTML = '<div style="padding:12px;color:#64748b;text-align:center">暂无历史记录</div>'; return; }
      history.forEach(function (r) {
        var item = document.createElement('div');
        var name = document.createElement('span');
        var time = document.createElement('span');
        var remove = document.createElement('button');
        var d = new Date(r.time);
        var ts = (d.getMonth() + 1) + '/' + d.getDate() + ' ' + d.getHours() + ':' + String(d.getMinutes()).padStart(2, '0');
        item.style.cssText = 'padding:8px 12px;cursor:pointer;border-bottom:1px solid rgba(124,58,237,.1);display:flex;align-items:center;gap:8px;transition:background .15s';
        name.textContent = r.id;
        name.style.cssText = 'color:#e2e8f0;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap';
        time.textContent = ts;
        time.style.cssText = 'color:#64748b;margin-left:auto;font-size:11px;flex-shrink:0';
        remove.type = 'button';
        remove.textContent = '×';
        remove.title = '删除此历史记录';
        remove.setAttribute('aria-label', '删除 ' + r.id);
        remove.style.cssText = 'flex-shrink:0;width:22px;height:22px;border:none;border-radius:999px;background:rgba(239,68,68,.12);color:#fda4af;cursor:pointer;font-size:15px;line-height:1;display:flex;align-items:center;justify-content:center;padding:0';
        item.addEventListener('mouseenter', function () { item.style.background = 'rgba(124,58,237,.2)'; });
        item.addEventListener('mouseleave', function () { item.style.background = ''; });
        item.addEventListener('click', function () {
          clearRoomIdManualOverride(idInput);
          setRoomIdInputValue(idInput, r.id);
          idInput.dataset.enhAutofilled = '0';
          idInput.dataset.enhAutofilledValue = '';
          list.style.display = 'none';
          open = false;
        });
        remove.addEventListener('click', function (event) {
          event.preventDefault();
          event.stopPropagation();
          removeRoomHistory(r.id);
          if (idInput.dataset.enhAutofilledValue === r.id) {
            fillRoomIdDefault({ forceAutofill: true, preferHistory: true });
          }
          refreshList();
        });
        remove.addEventListener('mouseenter', function (event) {
          event.stopPropagation();
          remove.style.background = 'rgba(239,68,68,.2)';
          remove.style.color = '#fecdd3';
        });
        remove.addEventListener('mouseleave', function (event) {
          event.stopPropagation();
          remove.style.background = 'rgba(239,68,68,.12)';
          remove.style.color = '#fda4af';
        });
        item.appendChild(name);
        item.appendChild(time);
        item.appendChild(remove);
        list.appendChild(item);
      });
    }

    var open = false;
    arrow.addEventListener('click', function (e) {
      e.stopPropagation();
      open = !open;
      if (open) { refreshList(); list.style.display = 'block'; }
      else { list.style.display = 'none'; }
    });
    document.addEventListener('click', function (e) {
      if (!parent || !parent.contains(e.target)) { open = false; list.style.display = 'none'; }
    });
  }

  /* ── 注册 ── */
  function showRegisterModal() {
    if (document.getElementById('reg-modal')) return;
    var overlay = document.createElement('div');
    overlay.id = 'reg-modal';
    overlay.style.cssText = 'position:fixed;top:0;left:0;width:100%;height:100%;background:rgba(0,0,0,.7);z-index:99999;display:flex;align-items:center;justify-content:center;backdrop-filter:blur(4px)';
    var box = document.createElement('div');
    box.style.cssText = 'background:rgba(15,12,41,.95);color:#e2e8f0;border:1px solid rgba(124,58,237,.4);border-radius:16px;padding:28px;min-width:320px;max-width:380px;backdrop-filter:blur(12px);box-shadow:0 8px 32px rgba(0,0,0,.5),0 0 30px rgba(124,58,237,.15)';
    box.innerHTML =
      '<div style="font-size:18px;font-weight:700;margin-bottom:18px;background:linear-gradient(135deg,#a78bfa,#22d3ee);-webkit-background-clip:text;-webkit-text-fill-color:transparent">注册账号</div>' +
      '<div style="margin-bottom:12px"><input id="reg-user" placeholder="用户名" style="width:100%;padding:10px 12px;background:rgba(15,12,41,.6);color:#e2e8f0;border:1px solid rgba(124,58,237,.4);border-radius:10px;font-size:14px;box-sizing:border-box;outline:none"></div>' +
      '<div style="margin-bottom:12px"><input id="reg-pass" type="password" placeholder="密码" style="width:100%;padding:10px 12px;background:rgba(15,12,41,.6);color:#e2e8f0;border:1px solid rgba(124,58,237,.4);border-radius:10px;font-size:14px;box-sizing:border-box;outline:none"></div>' +
      '<div id="reg-msg" style="font-size:12px;color:#94a3b8;margin-bottom:12px;min-height:16px"></div>' +
      '<div style="display:flex;gap:10px"><button id="reg-ok" style="flex:1;padding:10px;background:linear-gradient(135deg,#7c3aed,#06b6d4);color:#fff;border:none;border-radius:10px;cursor:pointer;font-size:14px;font-weight:600">注册</button>' +
      '<button id="reg-cancel" style="flex:1;padding:10px;background:rgba(255,255,255,.08);color:#e2e8f0;border:1px solid rgba(124,58,237,.3);border-radius:10px;cursor:pointer;font-size:14px">取消</button></div>';
    overlay.appendChild(box);
    document.body.appendChild(overlay);

    document.getElementById('reg-cancel').addEventListener('click', function () { overlay.remove(); });
    overlay.addEventListener('click', function (e) { if (e.target === overlay) overlay.remove(); });
    document.getElementById('reg-ok').addEventListener('click', function () {
      var user = document.getElementById('reg-user').value.trim();
      var pass = document.getElementById('reg-pass').value;
      var msg = document.getElementById('reg-msg');
      if (!user || !pass) { msg.textContent = '用户名和密码不能为空'; return; }
      msg.textContent = '注册中...'; msg.style.color = '#94a3b8';
      fetch(REGISTER_URL, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ user: user, pass: pass })
      }).then(function (r) { return r.json(); }).then(function (data) {
        if (data.ok) {
          msg.style.color = '#34d399';
          msg.textContent = data.message || '注册成功！请返回登录。';
          setTimeout(function () { overlay.remove(); }, 1800);
        }
        else { msg.style.color = '#f87171'; msg.textContent = data.error || '注册失败'; }
      }).catch(function () { msg.style.color = '#f87171'; msg.textContent = '网络错误'; });
    });
  }

  function injectRegisterButton(root) {
    root.querySelectorAll('form').forEach(function (form) {
      if (form.dataset.enhReg) return;
      var hasLogin = false;
      form.querySelectorAll('button, [type=submit]').forEach(function (btn) {
        if (btn.textContent.trim() === 'Login' || btn.textContent.trim() === '登录') hasLogin = true;
      });
      if (!hasLogin) return;
      form.dataset.enhReg = '1';
      var regBtn = document.createElement('button');
      regBtn.type = 'button'; regBtn.textContent = '注册';
      regBtn.style.cssText = 'margin-top:8px;width:100%;padding:8px;background:transparent;color:#a78bfa;border:1px solid rgba(124,58,237,.4);border-radius:10px;cursor:pointer;font-size:13px;transition:all .2s';
      regBtn.addEventListener('mouseenter', function () { regBtn.style.background = 'rgba(124,58,237,.1)'; });
      regBtn.addEventListener('mouseleave', function () { regBtn.style.background = 'transparent'; });
      regBtn.addEventListener('click', function (e) { e.preventDefault(); showRegisterModal(); });
      form.appendChild(regBtn);
    });
  }

  /* ── 主 Observer ── */
  function startObserver() {
    var obs = new MutationObserver(function (muts) {
      muts.forEach(function (m) {
        m.addedNodes.forEach(function (nd) {
          if (nd.nodeType !== 1) return;
          translateNode(nd);
          enhanceSnackbars(nd);
          hideCloseCheckbox(nd);
          hideScreegoSettings(nd);
          interceptShareButtonClicks(nd);
          syncShareButtonAvailability();
          injectRegisterButton(nd);
          injectHistoryDropdown();
          fillRoomIdDefault();
          replaceLogo(nd);
        });
      });
    });
    obs.observe(document.body, { childList: true, subtree: true });
    translateNode(document.body);
    enhanceSnackbars(document.body);
    hideCloseCheckbox(document.body);
    hideScreegoSettings(document.body);
    interceptShareButtonClicks(document.body);
    renderWebPresenceMembers();
    syncShareButtonAvailability();
    interceptShareHotkey();
    injectRegisterButton(document.body);
    monitorRoomUrl();
    monitorExitButton();
    monitorSfuMode();
    startAdaptiveStreamingMonitor();
    startAuthStateMonitor();
    startConnectionStats();
    fetchUsername();
    applyTheme();
    replaceLogo(document.body);
    setTimeout(injectHistoryDropdown, 1500);
    setTimeout(fillRoomIdDefault, 2000);
  }

  /* ── 启动 ── */
  function init() {
    console.log('[GameCast] 初始化 v5.0');
    ensureDefaultShareSettings();
    window.addEventListener('beforeunload', function () {
      if (canMaintainRoomState(currentRoomId())) sendRoomStateBeacon(false, roomStateHeartbeatRoomId || currentRoomId());
      releaseWebShare(true);
      leaveWebPresence(true);
      closeRoomSocket();
    });
    patchWebSocket();
    patchFetch();
    patchGDM();
    patchRTC();
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', function () { startObserver(); });
    } else { startObserver(); }
  }
  init();
})();
