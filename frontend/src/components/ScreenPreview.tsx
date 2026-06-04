import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import type { StreamFrame, StreamSettings, WebRTCPlaybackStats } from '../types';
import type { Translator } from '../i18n';
import './ScreenPreview.css';

interface ScreenPreviewProps {
  frame: StreamFrame | null;
  remoteStream?: MediaStream | null;
  webrtcStats?: WebRTCPlaybackStats | null;
  inRoom: boolean;
  sharing: boolean;
  selfSharing: boolean;
  remoteViewerCount: number;
  streamSettings: StreamSettings | null;
  streamOwnerName: string | null;
  diagnosticsOverlay?: ReactNode;
  t: Translator;
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

export default function ScreenPreview({
  frame,
  remoteStream,
  webrtcStats,
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
  const showRemoteVideo = Boolean(remoteStream && !selfSharing);

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
    const onFullscreenChange = () => {
      const root = containerRef.current;
      setFullscreen(Boolean(root && document.fullscreenElement === root));
    };

    document.addEventListener('fullscreenchange', onFullscreenChange);
    return () => {
      document.removeEventListener('fullscreenchange', onFullscreenChange);
    };
  }, []);

  const toggleFullscreen = async () => {
    const root = containerRef.current;
    if (!root) {
      return;
    }

    if (document.fullscreenElement === root) {
      await document.exitFullscreen();
      return;
    }

    try {
      await root.requestFullscreen();
    } catch {
      // Ignore unsupported fullscreen API errors.
    }
  };

  const streamMode = selfSharing ? t('preview.localPreview') : sharing ? t('preview.watching') : t('preview.ready');
  const decodedResolution = frame
    ? `${frame.width}x${frame.height}`
    : showRemoteVideo && streamSettings
      ? `${streamSettings.width}x${streamSettings.height}`
      : '--';
  const realDecodedFPS =
    frame && typeof frame.decodedFps === 'number' && Number.isFinite(frame.decodedFps)
      ? `${frame.decodedFps.toFixed(1)}fps`
      : webrtcStats && typeof webrtcStats.decodedFps === 'number' && Number.isFinite(webrtcStats.decodedFps)
        ? `${webrtcStats.decodedFps.toFixed(1)}fps`
        : '--';
  const realSourceFPS =
    frame && typeof frame.estimatedSourceFps === 'number' && Number.isFinite(frame.estimatedSourceFps)
      ? `${frame.estimatedSourceFps.toFixed(1)}fps`
      : '--';
  const realBitrate =
    frame && typeof frame.incomingBitrateKbps === 'number' && Number.isFinite(frame.incomingBitrateKbps)
      ? `${frame.incomingBitrateKbps.toFixed(0)}kbps`
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
            onClick={() => {
              void toggleFullscreen();
            }}
          >
            {fullscreen ? t('preview.exitFullscreen') : t('preview.fullscreen')}
          </button>
        </div>
      </div>
      <div className="screen-stage">
        {showRemoteVideo ? (
          <video ref={videoRef} className="screen-video" autoPlay playsInline />
        ) : (
          <canvas ref={canvasRef} className="screen-canvas" />
        )}
        {diagnosticsOverlay && <div className="screen-diagnostics-overlay">{diagnosticsOverlay}</div>}
      </div>
    </div>
  );
}
