var GC_SFU = (function () {
  'use strict';

  var SFU_URL = 'wss://' + location.host + '/sfu-ws';
  var MAX_RECONNECT_ATTEMPTS = 5;
  var BaseRTC = window.__PARTYLAN_NATIVE_RTC_PEER_CONNECTION__ || window.RTCPeerConnection;
  var ws = null;
  var pc = null;
  var localStream = null;
  var remoteStream = null;
  var connecting = false;
  var hostReady = false;
  var mediaReady = false;
  var pendingIce = [];
  var onRemoteStream = null;
  var onStatus = null;
  var currentRoomId = '';
  var currentRole = '';
  var currentOpts = null;
  var currentViewerLayer = '';
  var reconnectTimer = null;
  var reconnectAttempts = 0;
  var manualDisconnect = false;
  var viewerLayerSwitchTimer = null;
  var mediaReadyWaiters = [];

  function log(msg) { console.log('[SFU] ' + msg); }
  function setStatus(msg) { if (onStatus) onStatus(msg); log(msg); }

  function codedError(code, message) {
    var error = new Error(message || code);
    error.code = code;
    return error;
  }

  function removeMediaReadyWaiter(waiter) {
    var index = mediaReadyWaiters.indexOf(waiter);
    if (index >= 0) mediaReadyWaiters.splice(index, 1);
  }

  function settleMediaReadyWaiters(error) {
    var waiters = mediaReadyWaiters.slice();
    mediaReadyWaiters = [];
    waiters.forEach(function (waiter) {
      clearTimeout(waiter.timer);
      if (error) waiter.reject(error);
      else waiter.resolve(pc);
    });
  }

  function markMediaReady() {
    mediaReady = true;
    resetReconnectState();
    settleMediaReadyWaiters(null);
  }

  function rejectMediaReady(error) {
    var stableError = error && error.code
      ? error
      : codedError('sfu_unavailable', 'SFU 媒体连接不可用');
    settleMediaReadyWaiters(stableError);
  }

  function waitUntilMediaReady(timeoutMs) {
    if (mediaReady && pc) return Promise.resolve(pc);
    var timeout = Math.max(1, Number(timeoutMs) || 15000);
    return new Promise(function (resolve, reject) {
      var waiter = { resolve: resolve, reject: reject, timer: 0 };
      waiter.timer = setTimeout(function () {
        removeMediaReadyWaiter(waiter);
        reject(codedError('share_start_timeout', 'SFU 媒体连接超时'));
      }, timeout);
      mediaReadyWaiters.push(waiter);
    });
  }

  function codecCapabilities(kind) {
    var capabilities = null;
    if (typeof RTCRtpSender !== 'undefined' && RTCRtpSender.getCapabilities) {
      capabilities = RTCRtpSender.getCapabilities(kind);
    }
    if ((!capabilities || !capabilities.codecs) &&
        typeof RTCRtpReceiver !== 'undefined' && RTCRtpReceiver.getCapabilities) {
      capabilities = RTCRtpReceiver.getCapabilities(kind);
    }
    return capabilities && Array.isArray(capabilities.codecs) ? capabilities.codecs : [];
  }

  function requiredCodecPreferences(kind) {
    return codecCapabilities(kind).filter(function (codec) {
      var mimeType = String(codec && codec.mimeType || '').toLowerCase();
      if (kind === 'video') return mimeType === 'video/h264';
      return mimeType === 'audio/opus' &&
        Number(codec.clockRate || 48000) === 48000 &&
        Number(codec.channels || 2) === 2;
    });
  }

  function requireMediaCodecs() {
    var video = requiredCodecPreferences('video');
    var audio = requiredCodecPreferences('audio');
    if (!video.length || !audio.length) {
      throw codedError('codec_unavailable', 'H.264 或 Opus 编解码能力不可用');
    }
    return { video: video, audio: audio };
  }

  function applyCodecPreferences(transceiver, kind, codecs) {
    if (!transceiver || typeof transceiver.setCodecPreferences !== 'function') {
      throw codedError('codec_unavailable', '浏览器无法锁定共享编解码格式');
    }
    transceiver.setCodecPreferences(codecs[kind]);
  }

  function applyAnswerCodecPreferences(codecs) {
    if (!pc || typeof pc.getTransceivers !== 'function') {
      throw codedError('codec_unavailable', '浏览器无法锁定接收编解码格式');
    }
    pc.getTransceivers().forEach(function (transceiver) {
      var track = transceiver && transceiver.receiver && transceiver.receiver.track;
      var kind = track && track.kind;
      if (kind === 'video' || kind === 'audio') {
        applyCodecPreferences(transceiver, kind, codecs);
      }
    });
  }

  function clearReconnectTimer() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
  }

  function resetReconnectState() {
    clearReconnectTimer();
    reconnectAttempts = 0;
  }

  function hasLiveLocalStream() {
    return !!(
      localStream &&
      typeof localStream.getTracks === 'function' &&
      localStream.getTracks().some(function (track) {
        return track && track.readyState === 'live';
      })
    );
  }

  function scheduleReconnect(reason) {
    if (manualDisconnect || reconnectTimer || !currentRoomId) return;
    if (currentRole !== 'viewer' && !(currentRole === 'host' && hasLiveLocalStream())) return;
    if (reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
      setStatus('错误: sfu_unavailable');
      return;
    }
    reconnectAttempts += 1;
    var delay = Math.min(6000, 1000 * Math.pow(2, reconnectAttempts - 1));
    setStatus('连接不稳定，' + Math.round(delay / 1000) + ' 秒后重试');
    log('准备重连, reason=' + reason + ', delay=' + delay + 'ms');
    reconnectTimer = setTimeout(function () {
      reconnectTimer = null;
      if (manualDisconnect || !currentRoomId) return;
      if (currentRole !== 'viewer' && !(currentRole === 'host' && hasLiveLocalStream())) return;
      var previousPeer = pc;
      pc = null;
      try { if (previousPeer) previousPeer.close(); } catch (e) {}
      var previousSocket = ws;
      ws = null;
      try { if (previousSocket) previousSocket.close(); } catch (e2) {}
      connecting = false;
      mediaReady = false;
      hostReady = false;
      connect(currentRoomId, currentRole, currentOpts || {});
    }, delay);
  }

  function buildIceServers() {
    return [{
      urls: [
        'stun:' + location.hostname + ':3478',
        'stun:stun.l.google.com:19302'
      ]
    }];
  }

  function detectPreferredViewerLayer() {
    var width = Math.max(window.innerWidth || 0, window.screen && window.screen.width || 0);
    var height = Math.max(window.innerHeight || 0, window.screen && window.screen.height || 0);
    var longSide = Math.max(width, height);
    var shortSide = Math.min(width, height);
    if (longSide <= 800 || shortSide <= 450) return 'q';
    if (longSide <= 1280 || shortSide <= 720) return 'h';
    return 'f';
  }

  function buildViewerLayerQuery() {
    if (currentRole !== 'viewer') return '';
    currentViewerLayer = detectPreferredViewerLayer();
    return '&layer=' + encodeURIComponent(currentViewerLayer);
  }

  function clearViewerLayerSwitchTimer() {
    if (viewerLayerSwitchTimer) {
      clearTimeout(viewerLayerSwitchTimer);
      viewerLayerSwitchTimer = null;
    }
  }

  function maybeRefreshViewerLayer(reason) {
    if (currentRole !== 'viewer' || !currentRoomId || manualDisconnect) return;
    var nextLayer = detectPreferredViewerLayer();
    if (!nextLayer || nextLayer === currentViewerLayer) return;
    currentViewerLayer = nextLayer;
    clearViewerLayerSwitchTimer();
    viewerLayerSwitchTimer = setTimeout(function () {
      viewerLayerSwitchTimer = null;
      if (currentRole !== 'viewer' || !currentRoomId || manualDisconnect) return;
      log('viewer 层切换为 ' + nextLayer + ', reason=' + reason);
      setStatus('窗口大小变化，正在切换更合适的接收层');
      try { if (pc) pc.close(); } catch (e) {}
      pc = null;
      if (ws) {
        var socket = ws;
        ws = null;
        try { socket.close(); } catch (e2) {}
      }
      connecting = false;
      mediaReady = false;
      connect(currentRoomId, currentRole, currentOpts || {});
    }, 400);
  }

  function buildSimulcastEncodings() {
    return [
      { rid: 'q', scaleResolutionDownBy: 2.0, maxBitrate: 900000, maxFramerate: 24, active: true },
      { rid: 'h', scaleResolutionDownBy: 1.5, maxBitrate: 2200000, maxFramerate: 30, active: true },
      { rid: 'f', scaleResolutionDownBy: 1.0, maxBitrate: 4500000, maxFramerate: 30, active: true },
    ];
  }

  function parseSDP(raw, fallbackType) {
    if (!raw) return null;
    if (typeof raw === 'object' && raw.sdp) {
      return {
        type: raw.type || fallbackType,
        sdp: raw.sdp
      };
    }
    if (typeof raw === 'string') {
      if (raw.indexOf('v=') === 0) {
        return { type: fallbackType, sdp: raw };
      }
      try {
        var parsed = JSON.parse(raw);
        if (parsed && parsed.sdp) {
          return {
            type: parsed.type || fallbackType,
            sdp: parsed.sdp
          };
        }
      } catch (e) {}
    }
    return null;
  }

  function createRemoteStream() {
    remoteStream = new MediaStream();
    return remoteStream;
  }

  function ensureRemoteStream() {
    return remoteStream || createRemoteStream();
  }

  function addRemoteTrack(track) {
    var stream = ensureRemoteStream();
    markMediaReady();
    if (!stream.getTracks().some(function (existing) { return existing.id === track.id; })) {
      stream.addTrack(track);
    }
    if (onRemoteStream) {
      onRemoteStream(stream);
    }
  }

  function createPC() {
    if (!BaseRTC) {
      throw new Error('RTCPeerConnection 不可用');
    }
    if (pc) {
      try { pc.close(); } catch (e) {}
    }
    pendingIce = [];
    mediaReady = false;
    createRemoteStream();
    pc = new BaseRTC({
      iceServers: buildIceServers()
    });
    pc.onicecandidate = function (e) {
      if (e.candidate && ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'ice-candidate', ice: e.candidate.toJSON() }));
      }
    };
    pc.onconnectionstatechange = function () {
      if (!pc) return;
      log('连接状态: ' + pc.connectionState);
      if (pc.connectionState === 'connected') {
        markMediaReady();
        resetReconnectState();
        setStatus('连接成功');
      } else if (pc.connectionState === 'failed') {
        rejectMediaReady(codedError('sfu_unavailable', 'SFU 媒体连接失败'));
        setStatus('连接失败');
        scheduleReconnect('pc-failed');
      } else if (pc.connectionState === 'disconnected') {
        setStatus('连接中断');
        scheduleReconnect('pc-disconnected');
      }
    };
    pc.ontrack = function (e) {
      log('收到远程轨道: ' + e.track.kind);
      addRemoteTrack(e.track);
      setStatus('正在接收画面');
    };
    return pc;
  }

  function flushPendingIce() {
    if (!pc || !pc.remoteDescription) return;
    var queue = pendingIce.slice();
    pendingIce = [];
    queue.forEach(function (candidate) {
      pc.addIceCandidate(candidate).catch(function (e) {
        log('补发 ICE 失败: ' + e.message);
      });
    });
  }

  function queueOrAddIce(candidate) {
    if (!candidate) return;
    if (!pc || !pc.remoteDescription) {
      pendingIce.push(candidate);
      return;
    }
    pc.addIceCandidate(candidate).catch(function (e) {
      log('添加 ICE 失败: ' + e.message);
    });
  }

  function connect(roomId, role, opts) {
    if (ws || connecting) {
      log('已有连接，跳过');
      return;
    }
    currentRoomId = roomId;
    currentRole = role;
    opts = opts || {};
    currentOpts = opts;
    manualDisconnect = false;
    connecting = true;
    onRemoteStream = opts.onRemoteStream || null;
    onStatus = opts.onStatus || null;

    var url = SFU_URL + '?room=' + encodeURIComponent(roomId) + '&role=' + role + buildViewerLayerQuery();
    log('连接: ' + url + ' role=' + role);
    setStatus('连接中...');

    var socket = new WebSocket(url);
    ws = socket;

    socket.onopen = function () {
      if (ws !== socket) return;
      log('WebSocket 已连接');
      connecting = false;
      setStatus('已连接 SFU');
      if (role === 'host' && localStream) {
        try {
          setupHostPeer();
        } catch (error) {
          rejectMediaReady(error);
          setStatus('错误: ' + (error.code || 'sfu_unavailable'));
        }
      }
    };

    socket.onmessage = function (e) {
      if (ws !== socket) return;
      var msg;
      try { msg = JSON.parse(e.data); } catch (err) { return; }
      handleMessage(msg);
    };

    socket.onerror = function () {
      if (ws !== socket) return;
      log('WebSocket 错误');
      rejectMediaReady(codedError('sfu_unavailable', 'SFU WebSocket 连接错误'));
      setStatus('连接错误');
    };

    socket.onclose = function () {
      if (ws !== socket) {
        log('旧 WebSocket 已关闭');
        return;
      }
      log('WebSocket 关闭');
      ws = null;
      connecting = false;
      setStatus('已断开');
      cleanup({ preserveLocalStream: currentRole === 'host' && hasLiveLocalStream() });
      scheduleReconnect('ws-close');
    };
  }

  function handleMessage(msg) {
    if (msg.error) {
      log('SFU 返回错误');
      rejectMediaReady(codedError('sfu_unavailable', 'SFU 拒绝了媒体会话'));
      setStatus('错误: sfu_unavailable');
      return;
    }
    switch (msg.type) {
      case 'offer': handleOffer(msg); break;
      case 'answer': handleAnswer(msg); break;
      case 'ice-candidate': handleRemoteICE(msg); break;
    }
  }

  function setupHostPeer() {
    if (hostReady) {
      log('host PC 已存在，跳过');
      return;
    }
    hostReady = true;
    log('创建 host PeerConnection');
    var codecs = requireMediaCodecs();
    createPC();

    if (localStream) {
      localStream.getTracks().forEach(function (track) {
        if (typeof pc.addTransceiver === 'function') {
          try {
            var options = {
              direction: 'sendonly',
              streams: [localStream]
            };
            if (track.kind === 'video') options.sendEncodings = buildSimulcastEncodings();
            var transceiver = pc.addTransceiver(track, options);
            applyCodecPreferences(transceiver, track.kind, codecs);
            log(track.kind === 'video' ? '添加 H.264 simulcast 视频轨道' : '添加 Opus 音频轨道');
            return;
          } catch (e) {
            if (e && e.code === 'codec_unavailable') throw e;
            log('transceiver 添加失败，回退单轨发送: ' + e.message);
          }
        }
        var sender = pc.addTrack(track, localStream);
        var fallback = typeof pc.getTransceivers === 'function' && pc.getTransceivers().find(function (item) {
          return item.sender === sender || item.sender && item.sender.track === track;
        });
        applyCodecPreferences(fallback, track.kind, codecs);
        log('添加轨道: ' + track.kind);
      });
    }

    pc.createOffer().then(function (offer) {
      return pc.setLocalDescription(offer).then(function () { return offer; });
    }).then(function (offer) {
      ws.send(JSON.stringify({
        type: 'offer',
        sdp: offer.sdp
      }));
      log('已发送 offer 给 SFU');
      setStatus('等待 SFU 响应...');
    }).catch(function (e) {
      hostReady = false;
      log('创建 offer 失败: ' + e.message);
      rejectMediaReady(e && e.code ? e : codedError('sfu_unavailable', 'SFU offer 创建失败'));
      setStatus('错误: ' + (e.code || 'sfu_unavailable'));
    });
  }

  function handleOffer(msg) {
    var sdp = parseSDP(msg.sdp, 'offer');
    if (!sdp) {
      log('无效的 SDP');
      setStatus('SDP 格式错误');
      return;
    }
    log('收到 SFU offer, type=' + sdp.type);

    createPC();

    var codecs;
    try {
      codecs = requireMediaCodecs();
    } catch (error) {
      rejectMediaReady(error);
      setStatus('错误: ' + error.code);
      return;
    }

    pc.setRemoteDescription(new RTCSessionDescription(sdp)).then(function () {
      flushPendingIce();
      applyAnswerCodecPreferences(codecs);
      return pc.createAnswer();
    }).then(function (answer) {
      return pc.setLocalDescription(answer).then(function () { return answer; });
    }).then(function (answer) {
      ws.send(JSON.stringify({
        type: 'answer',
        sdp: answer.sdp
      }));
      log('已发送 answer 给 SFU');
    }).catch(function (e) {
      log('处理 offer 失败: ' + e.message);
      rejectMediaReady(e && e.code ? e : codedError('sfu_unavailable', 'SFU offer 处理失败'));
      setStatus('错误: ' + (e.code || 'sfu_unavailable'));
    });
  }

  function handleAnswer(msg) {
    var sdp = parseSDP(msg.sdp, 'answer');
    if (!sdp) {
      log('无效的 answer SDP');
      return;
    }
    log('收到 SFU answer');
    if (pc) {
      pc.setRemoteDescription(new RTCSessionDescription(sdp)).then(function () {
        flushPendingIce();
      }).catch(function (e) {
        log('设置远程描述失败: ' + e.message);
      });
    }
  }

  function handleRemoteICE(msg) {
    if (!msg.ice) return;
    queueOrAddIce(msg.ice);
  }

  function shareScreen(stream) {
    requireMediaCodecs();
    localStream = stream;
    if (ws && ws.readyState === WebSocket.OPEN) {
      setupHostPeer();
    }
  }

  function disconnect() {
    manualDisconnect = true;
    resetReconnectState();
    clearViewerLayerSwitchTimer();
    if (ws) {
      var socket = ws;
      ws = null;
      socket.close();
    }
    connecting = false;
    cleanup();
  }

  function cleanup(opts) {
    opts = opts || {};
    clearViewerLayerSwitchTimer();
    pendingIce = [];
    mediaReady = false;
    rejectMediaReady(codedError('sfu_unavailable', 'SFU 媒体连接已关闭'));
    remoteStream = null;
    if (pc) {
      try { pc.close(); } catch (e) {}
      pc = null;
    }
    if (!opts.preserveLocalStream) {
      localStream = null;
    }
    hostReady = false;
  }

  return {
    connect: connect,
    disconnect: disconnect,
    shareScreen: shareScreen,
    validateMediaCodecs: requireMediaCodecs,
    waitUntilMediaReady: waitUntilMediaReady,
    maybeRefreshViewerLayer: maybeRefreshViewerLayer,
    isConnected: function () { return ws && ws.readyState === WebSocket.OPEN; },
    isMediaReady: function () { return !!mediaReady; },
    getPeerConnection: function () { return pc; },
    getRole: function () { return currentRole; }
  };
})();

window.addEventListener('resize', function () {
  if (typeof GC_SFU === 'undefined' || !GC_SFU) return;
  GC_SFU.maybeRefreshViewerLayer && GC_SFU.maybeRefreshViewerLayer('resize');
});
