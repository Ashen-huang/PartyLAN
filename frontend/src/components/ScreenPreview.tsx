import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import type { StreamFrame, StreamSettings, StreamStats, WebRTCPlaybackStats } from '../types';
import type { Translator } from '../i18n';
import { WindowFullscreen, WindowIsFullscreen, WindowUnfullscreen } from '../../wailsjs/runtime/runtime';
import './ScreenPreview.css';

interface ScreenPreviewProps {
  frame: StreamFrame | null;
  remoteStream?: MediaStream | null;
  webrtcStats?: WebRTCPlaybackStats | null;
  localStreamStats?: StreamStats | null;
  inRoom: boolean;
  sharing: boolean;
  selfSharing: boolean;
  remoteViewerCount: number;
  streamSettings: StreamSettings | null;
  streamOwnerName: string | null;
  diagnosticsOverlay?: ReactNode;
  t: Translator;
}

interface FullscreenCapableElement extends HTMLDivElement {
  webkitRequestFullscreen?: () => Promise<void> | void;
  msRequestFullscreen?: () => Promise<void> | void;
}

interface FullscreenCapableDocument extends Document {
  webkitFullscreenElement?: Element | null;
  msFullscreenElement?: Element | null;
  webkitExitFullscreen?: () => Promise<void> | void;
  msExitFullscreen?: () => Promise<void> | void;
}

function getCanvasSize(canvas: HTMLCanvasElement) {
  const host = canvas.parentElement;
  return {
    width: host?.clientWidth || canvas.clientWidth || 640,
    height: host?.clientHeight || canvas.clientHeight || 360,
  };
}

function drawEmptyState(canvas: HTMLCanvasElement, ctx: CanvasRenderingContext2D, title: string, detail?: string) {
  const size = getCanvasSize(canvas);
  canvas.width = size.width;
  canvas.height = size.height;
  ctx.fillStyle = '#0d1730';
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.fillStyle = '#dff6ff';
  ctx.font = '600 24px "Segoe UI", sans-serif';
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  ctx.fillText(title, canvas.width / 2, canvas.height / 2 - 12);
  if (detail) {
    ctx.fillStyle = '#84a3b7';
    ctx.font = '14px "Segoe UI", sans-serif';
    ctx.fillText(detail, canvas.width / 2, canvas.height / 2 + 18);
  }
}

function drawFittedImage(
  canvas: HTMLCanvasElement,
  ctx: CanvasRenderingContext2D,
  image: HTMLImageElement,
  sourceWidth: number,
  sourceHeight: number,
) {
  const size = getCanvasSize(canvas);
  canvas.width = size.width;
  canvas.height = size.height;

  ctx.fillStyle = '#09111f';
  ctx.fillRect(0, 0, canvas.width, canvas.height);

  const scale = Math.min(canvas.width / sourceWidth, canvas.height / sourceHeight);
  const drawWidth = Math.max(1, Math.floor(sourceWidth * scale));
  const drawHeight = Math.max(1, Math.floor(sourceHeight * scale));
  const offsetX = Math.floor((canvas.width - drawWidth) / 2);
  const offsetY = Math.floor((canvas.height - drawHeight) / 2);

  ctx.imageSmoothingEnabled = true;
  ctx.imageSmoothingQuality = 'high';
  ctx.drawImage(image, offsetX, offsetY, drawWidth, drawHeight);
}

function describeEmptyMessage(
  inRoom: boolean,
  sharing: boolean,
  selfSharing: boolean,
  remoteViewerCount: number,
  streamSettings: StreamSettings | null,
  t: Translator,
) {
  if (!inRoom) {
    return {
      title: t('preview.lobbyTitle'),
      detail: t('preview.lobbyDetail'),
    };
  }
  if (selfSharing) {
    if (streamSettings) {
      return {
        title: t('preview.localLive'),
        detail: t('preview.localLiveDetail', {
          width: streamSettings.width,
          height: streamSettings.height,
          fps: streamSettings.fps,
        }),
      };
    }
    return {
      title: t('preview.youSharing'),
      detail: remoteViewerCount > 0 ? t('preview.youSharingDetailRemote') : t('preview.youSharingDetailLocal'),
    };
  }
  if (sharing) {
    return {
      title: t('preview.streamStarting'),
      detail: t('preview.waitFirstFrame'),
    };
  }
  if (remoteViewerCount > 0) {
    return {
      title: t('preview.noOneSharing'),
      detail: t('preview.noOneSharingDetail'),
    };
  }
  return {
    title: t('preview.noActiveShare'),
    detail: t('preview.noActiveShareDetail'),
  };
}

function activeFullscreenElement(doc: FullscreenCapableDocument): Element | null {
  return doc.fullscreenElement || doc.webkitFullscreenElement || doc.msFullscreenElement || null;
}

function requestElementFullscreen(element: FullscreenCapableElement): Promise<void> | void {
  if (typeof element.requestFullscreen === 'function') {
    return element.requestFullscreen();
  }
  if (typeof element.webkitRequestFullscreen === 'function') {
    return element.webkitRequestFullscreen();
  }
  if (typeof element.msRequestFullscreen === 'function') {
    return element.msRequestFullscreen();
  }
}

function exitElementFullscreen(doc: FullscreenCapableDocument): Promise<void> | void {
  if (typeof doc.exitFullscreen === 'function') {
    return doc.exitFullscreen();
  }
  if (typeof doc.webkitExitFullscreen === 'function') {
    return doc.webkitExitFullscreen();
  }
  if (typeof doc.msExitFullscreen === 'function') {
    return doc.msExitFullscreen();
  }
}

function attachFullscreenPromise(result: Promise<void> | void) {
  if (result && typeof (result as Promise<void>).catch === 'function') {
    void (result as Promise<void>).catch((error) => {
      console.warn('[preview/fullscreen] toggle failed', error);
    });
  }
}

function hasWailsWindowRuntime() {
  const runtime = (window as Window & { runtime?: Record<string, unknown> }).runtime;
  return typeof runtime?.WindowFullscreen === 'function' && typeof runtime?.WindowUnfullscreen === 'function';
}

export default function ScreenPreview({
  frame,
  remoteStream,
  webrtcStats,
  localStreamStats,
  inRoom,
  sharing,
  selfSharing,
  remoteViewerCount,
  streamSettings,
  streamOwnerName,
  diagnosticsOverlay,
  t,
}: ScreenPreviewProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const [fullscreen, setFullscreen] = useState(false);
  const [remoteVideoMetrics, setRemoteVideoMetrics] = useState<{
    width: number;
    height: number;
    frameRate: number | null;
  }>({ width: 0, height: 0, frameRate: null });
  const showRemoteVideo = Boolean(remoteStream);

  const frameDataURL = useMemo(() => {
    if (!frame) {
      return '';
    }
    return `data:image/${frame.format};base64,${frame.data}`;
  }, [frame]);

  const emptyMessage = useMemo(
    () => describeEmptyMessage(inRoom, sharing, selfSharing, remoteViewerCount, streamSettings, t),
    [inRoom, sharing, selfSharing, remoteViewerCount, streamSettings, t],
  );

  useEffect(() => {
    const canvas = canvasRef.current;
    if (showRemoteVideo) {
      return;
    }
    if (!canvas) {
      return;
    }

    const ctx = canvas.getContext('2d');
    if (!ctx) {
      return;
    }

    if (!frame || !frameDataURL) {
      drawEmptyState(canvas, ctx, emptyMessage.title, emptyMessage.detail);
      return;
    }

    let cancelled = false;
    const image = new Image();
    image.onload = () => {
      if (cancelled) {
        return;
      }
      drawFittedImage(canvas, ctx, image, frame.width, frame.height);
    };
    image.onerror = () => {
      if (!cancelled) {
        drawEmptyState(canvas, ctx, emptyMessage.title, emptyMessage.detail);
      }
    };
    image.src = frameDataURL;

    return () => {
      cancelled = true;
    };
  }, [frame, frameDataURL, emptyMessage, showRemoteVideo]);

  useEffect(() => {
    const canvas = canvasRef.current;
    const container = containerRef.current;
    if (showRemoteVideo) {
      return;
    }
    if (!canvas) {
      return;
    }

    const ctx = canvas.getContext('2d');
    if (!ctx) {
      return;
    }

    const redraw = () => {
      if (!frame || !frameDataURL) {
        drawEmptyState(canvas, ctx, emptyMessage.title, emptyMessage.detail);
        return;
      }

      const image = new Image();
      image.onload = () => {
        drawFittedImage(canvas, ctx, image, frame.width, frame.height);
      };
      image.src = frameDataURL;
    };

    window.addEventListener('resize', redraw);
    let observer: ResizeObserver | null = null;
    if (container && typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(() => redraw());
      observer.observe(container);
    }
    return () => {
      window.removeEventListener('resize', redraw);
      if (observer) {
        observer.disconnect();
      }
    };
  }, [frame, frameDataURL, emptyMessage, showRemoteVideo]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) {
      return;
    }
    video.srcObject = remoteStream || null;
    if (remoteStream) {
      void video.play();
    }
    return () => {
      video.srcObject = null;
    };
  }, [remoteStream]);

  useEffect(() => {
    if (!hasWailsWindowRuntime()) {
      return;
    }

    let cancelled = false;
    const syncWindowFullscreen = () => {
      void WindowIsFullscreen()
        .then((isFullscreen) => {
          if (!cancelled) {
            setFullscreen(isFullscreen);
          }
        })
        .catch(() => undefined);
    };

    syncWindowFullscreen();
    window.addEventListener('resize', syncWindowFullscreen);
    return () => {
      cancelled = true;
      window.removeEventListener('resize', syncWindowFullscreen);
    };
  }, []);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !remoteStream) {
      setRemoteVideoMetrics({ width: 0, height: 0, frameRate: null });
      return;
    }

    const updateMetrics = () => {
      const track = remoteStream.getVideoTracks()[0];
      const settings = track?.getSettings?.() || {};
      const nextWidth = video.videoWidth || (typeof settings.width === 'number' ? settings.width : 0);
      const nextHeight = video.videoHeight || (typeof settings.height === 'number' ? settings.height : 0);
      const nextFrameRate = typeof settings.frameRate === 'number' && Number.isFinite(settings.frameRate)
        ? settings.frameRate
        : null;
      setRemoteVideoMetrics((current) =>
        current.width === nextWidth && current.height === nextHeight && current.frameRate === nextFrameRate
          ? current
          : { width: nextWidth, height: nextHeight, frameRate: nextFrameRate },
      );
    };

    updateMetrics();
    video.addEventListener('loadedmetadata', updateMetrics);
    video.addEventListener('resize', updateMetrics);
    const timer = window.setInterval(updateMetrics, 1000);
    return () => {
      video.removeEventListener('loadedmetadata', updateMetrics);
      video.removeEventListener('resize', updateMetrics);
      window.clearInterval(timer);
    };
  }, [remoteStream]);

  useEffect(() => {
    const onFullscreenChange = () => {
      const root = containerRef.current;
      const doc = document as FullscreenCapableDocument;
      setFullscreen(Boolean(root && activeFullscreenElement(doc) === root));
    };

    document.addEventListener('fullscreenchange', onFullscreenChange);
    document.addEventListener('webkitfullscreenchange', onFullscreenChange);
    document.addEventListener('MSFullscreenChange', onFullscreenChange);
    return () => {
      document.removeEventListener('fullscreenchange', onFullscreenChange);
      document.removeEventListener('webkitfullscreenchange', onFullscreenChange);
      document.removeEventListener('MSFullscreenChange', onFullscreenChange);
    };
  }, []);

  const toggleFullscreen = async () => {
    if (hasWailsWindowRuntime()) {
      const isFullscreen = await WindowIsFullscreen().catch(() => fullscreen);
      if (isFullscreen) {
        WindowUnfullscreen();
        setFullscreen(false);
      } else {
        WindowFullscreen();
        setFullscreen(true);
      }
      return;
    }

    const root = containerRef.current as FullscreenCapableElement | null;
    if (!root) {
      return;
    }
    const doc = document as FullscreenCapableDocument;

    if (activeFullscreenElement(doc) === root) {
      attachFullscreenPromise(exitElementFullscreen(doc));
      return;
    }

    attachFullscreenPromise(requestElementFullscreen(root));
  };

  const streamMode = selfSharing ? t('preview.localPreview') : sharing ? t('preview.watching') : t('preview.ready');
  const decodedResolution = frame
    ? `${frame.width}x${frame.height}`
    : selfSharing && localStreamStats && localStreamStats.width > 0 && localStreamStats.height > 0
      ? `${localStreamStats.width}x${localStreamStats.height}`
    : remoteVideoMetrics.width > 0 && remoteVideoMetrics.height > 0
      ? `${remoteVideoMetrics.width}x${remoteVideoMetrics.height}`
    : showRemoteVideo && streamSettings
      ? `${streamSettings.width}x${streamSettings.height}`
      : '--';
  const realDecodedFPS =
    frame && typeof frame.decodedFps === 'number' && Number.isFinite(frame.decodedFps)
      ? `${frame.decodedFps.toFixed(1)}fps`
      : selfSharing &&
          localStreamStats &&
          typeof localStreamStats.encodedFps === 'number' &&
          Number.isFinite(localStreamStats.encodedFps)
        ? `${localStreamStats.encodedFps.toFixed(1)}fps`
      : webrtcStats && typeof webrtcStats.decodedFps === 'number' && Number.isFinite(webrtcStats.decodedFps)
        ? `${webrtcStats.decodedFps.toFixed(1)}fps`
        : '--';
  const realSourceFPS =
    frame && typeof frame.estimatedSourceFps === 'number' && Number.isFinite(frame.estimatedSourceFps)
      ? `${frame.estimatedSourceFps.toFixed(1)}fps`
      : selfSharing &&
          localStreamStats &&
          typeof localStreamStats.estimatedSourceFps === 'number' &&
          Number.isFinite(localStreamStats.estimatedSourceFps)
        ? `${localStreamStats.estimatedSourceFps.toFixed(1)}fps`
      : remoteVideoMetrics.frameRate !== null
        ? `${remoteVideoMetrics.frameRate.toFixed(1)}fps`
        : streamSettings && streamSettings.fps > 0
          ? `${streamSettings.fps.toFixed(1)}fps`
      : '--';
  const realBitrate =
    frame && typeof frame.incomingBitrateKbps === 'number' && Number.isFinite(frame.incomingBitrateKbps)
      ? `${frame.incomingBitrateKbps.toFixed(0)}kbps`
      : selfSharing &&
          localStreamStats &&
          typeof localStreamStats.bitrateKbps === 'number' &&
          Number.isFinite(localStreamStats.bitrateKbps)
        ? `${localStreamStats.bitrateKbps.toFixed(0)}kbps`
      : webrtcStats &&
          typeof webrtcStats.incomingBitrateKbps === 'number' &&
          Number.isFinite(webrtcStats.incomingBitrateKbps)
        ? `${webrtcStats.incomingBitrateKbps.toFixed(0)}kbps`
        : '--';

  return (
    <div
      ref={containerRef}
      className={`screen-preview ${fullscreen ? 'is-fullscreen' : ''}`}
      onDoubleClick={() => {
        void toggleFullscreen();
      }}
    >
      <div className="screen-topbar">
        <div className="screen-topbar-left">
          <span className="screen-mode">{streamMode}</span>
          <span className="screen-owner">
            {streamOwnerName
              ? t('preview.source', { name: streamOwnerName })
              : selfSharing
                ? t('preview.sourceYou')
                : t('preview.sourceEmpty')}
          </span>
        </div>
        <div className="screen-topbar-right">
          <span className="screen-metric">{t('preview.frame', { value: decodedResolution })}</span>
          <span className="screen-metric">{t('preview.decodeFps', { value: realDecodedFPS })}</span>
          <span className="screen-metric">{t('preview.sourceFps', { value: realSourceFPS })}</span>
          <span className="screen-metric">{t('preview.bitrate', { value: realBitrate })}</span>
          <button
            type="button"
            className="fullscreen-btn"
            onClick={(event) => {
              event.stopPropagation();
              void toggleFullscreen();
            }}
          >
            {fullscreen ? t('preview.exitFullscreen') : t('preview.fullscreen')}
          </button>
        </div>
      </div>
      <div className="screen-stage">
        {showRemoteVideo ? (
          <video ref={videoRef} className="screen-video" autoPlay playsInline muted={selfSharing} />
        ) : (
          <canvas ref={canvasRef} className="screen-canvas" />
        )}
        {diagnosticsOverlay && <div className="screen-diagnostics-overlay">{diagnosticsOverlay}</div>}
      </div>
    </div>
  );
}
