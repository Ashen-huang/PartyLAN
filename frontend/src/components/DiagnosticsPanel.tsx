import { AudioCapability, RuntimeStatus, VideoCapability } from '../types';
import type { Translator } from '../i18n';
import './DiagnosticsPanel.css';

interface DiagnosticsPanelProps {
  className?: string;
  showHeader?: boolean;
  variant?: 'panel' | 'overlay';
  title?: string;
  mode: string;
  networkBackend: string;
  videoCapability: VideoCapability | null;
  audioCapability: AudioCapability | null;
  runtime: RuntimeStatus | null;
  inRoom: boolean;
  roomId: string | null;
  sharing: boolean;
  selfSharing: boolean;
  hostVideoCapable: boolean;
  canShareToPeers: boolean;
  remoteViewerCount: number;
  t: Translator;
  onOpenAuthUrl: () => void;
}

type Tone = 'good' | 'warn' | 'bad' | 'neutral';

export function pickNetworkNote(lines: string[] | null | undefined): string {
  const normalized = Array.isArray(lines) ? lines.map((line) => line.trim()).filter(Boolean) : [];
  if (normalized.length === 0) {
    return '';
  }

  return (
    normalized.find((line) => line.includes('custom DERP present')) ||
    normalized.find((line) => line.includes('DERP map regions')) ||
    normalized.find((line) => line.includes('path=')) ||
    normalized[0] ||
    ''
  );
}

export function pickMediaNote(lines: string[] | null | undefined): string {
  const normalized = Array.isArray(lines) ? lines.map((line) => line.trim()).filter(Boolean) : [];
  if (normalized.length === 0) {
    return '';
  }

  return (
    normalized.find((line) => line.includes('selected path=')) ||
    normalized.find((line) => line.includes('TURN credentials configured')) ||
    normalized.find((line) => line.includes('TURN configured')) ||
    normalized.find((line) => line.includes('state=')) ||
    normalized.find((line) => line.includes('ICE servers configured')) ||
    normalized[0] ||
    ''
  );
}

function modeLabel(mode: string, t: Translator) {
  switch (mode) {
    case 'loopback':
      return t('mode.loopback');
    case 'lan':
      return t('mode.lan');
    case 'tailscale-system':
      return t('mode.tailscaleSystem');
    case 'networked':
      return t('mode.networked');
    default:
      return mode || t('mode.unknown');
  }
}

function networkLabel(backend: string, t: Translator) {
  switch (backend) {
    case 'loopback':
      return t('network.loopback');
    case 'lan':
      return t('network.lan');
    case 'tailscale-system':
      return t('network.tailscaleSystem');
    case 'networked':
      return t('network.networked');
    default:
      return backend || t('network.unknown');
  }
}

function networkStateLabel(runtime: RuntimeStatus | null, t: Translator) {
  switch (runtime?.networkState) {
    case 'Running':
      return t('runtime.connected');
    case 'NeedsLogin':
      return t('runtime.needsLogin');
    case 'NeedsMachineAuth':
      return t('runtime.needsApproval');
    case 'Starting':
      return t('runtime.starting');
    case 'Stopped':
      return t('runtime.stopped');
    default:
      return runtime?.networkState || t('runtime.unknown');
  }
}

function videoLabel(capability: VideoCapability | null, t: Translator) {
  if (!capability) return t('video.unknown');
  if (capability.backend === 'native' && capability.status === 'supported') return t('video.nativeReady');
  if (capability.backend === 'native') return t('video.nativeLimited');
  if (capability.backend === 'software') return t('video.softwareFallback');
  return capability.backend || t('video.unknown');
}

function audioLabel(runtime: RuntimeStatus | null, t: Translator) {
  if (!runtime) return t('audio.unknown');
  if (runtime.audioBackend === 'app-audio' && runtime.audioStatus === 'available') return t('audio.appReady');
  if (runtime.audioBackend === 'app-audio') return t('audio.appUnavailable');
  if (runtime.audioBackend === 'audio-unavailable') return t('audio.backendUnavailable');
  return runtime.audioBackend || t('audio.unknown');
}

function toneFor(value: boolean | number | string, kind: 'mode' | 'network' | 'video' | 'audio' | 'share' | 'room' | 'viewer'): Tone {
  switch (kind) {
    case 'mode':
      return value === 'networked' || value === 'lan' ? 'good' : value === 'tailscale-system' ? 'warn' : 'neutral';
    case 'network':
      return value === 'networked' || value === 'lan' ? 'good' : value === 'tailscale-system' ? 'warn' : 'neutral';
    case 'video':
      return value === 'supported' ? 'good' : value === 'native' ? 'warn' : 'neutral';
    case 'audio':
      return value === 'available' ? 'good' : 'warn';
    case 'share':
      if (value === 'diag.live' || value === 'diag.ready') return 'good';
      if (value === 'diag.watching') return 'neutral';
      return 'bad';
    case 'room':
      return value ? 'good' : 'warn';
    case 'viewer':
      return Number(value) > 0 ? 'good' : 'warn';
    default:
      return 'neutral';
  }
}

function Badge({ label, value, tone }: { label: string; value: string; tone: Tone }) {
  return (
    <div className={`diag-badge tone-${tone}`}>
      <span className="diag-badge-label">{label}</span>
      <span className="diag-badge-value">{value}</span>
    </div>
  );
}

export default function DiagnosticsPanel({
  className,
  showHeader = true,
  variant = 'panel',
  title = 'Diagnostics',
  mode,
  networkBackend,
  videoCapability,
  audioCapability,
  runtime,
  inRoom,
  roomId,
  sharing,
  selfSharing,
  hostVideoCapable,
  canShareToPeers,
  remoteViewerCount,
  t,
  onOpenAuthUrl,
}: DiagnosticsPanelProps) {
  const healthLine = runtime?.networkHealth?.[0] || '';
  const showAuthButton = Boolean(runtime?.networkAuthUrl);
  const audioReason = audioCapability?.reason || '';
  const preferredHealthLine = pickNetworkNote(runtime?.networkHealth);
  const preferredMediaLine = pickMediaNote(runtime?.mediaHealth);
  const effectiveShareKey = !hostVideoCapable
    ? 'diag.unavailable'
    : selfSharing
      ? 'diag.live'
      : sharing
        ? 'diag.watching'
        : 'diag.ready';
  const effectiveShareValue = t(effectiveShareKey);

  return (
    <div className={className ? `diagnostics-panel diagnostics-${variant} ${className}` : `diagnostics-panel diagnostics-${variant}`}>
      {showHeader && <div className="diagnostics-panel-header">{title}</div>}
      <div className="diagnostics-badges">
        <Badge label={t('diag.mode')} value={modeLabel(mode, t)} tone={toneFor(mode, 'mode')} />
        <Badge label={t('diag.network')} value={networkLabel(networkBackend, t)} tone={toneFor(networkBackend, 'network')} />
        <Badge label={t('diag.state')} value={networkStateLabel(runtime, t)} tone={runtime?.networkState === 'Running' ? 'good' : 'warn'} />
        <Badge label={t('diag.video')} value={videoLabel(videoCapability, t)} tone={toneFor(videoCapability?.status || '', 'video')} />
        <Badge label={t('diag.audio')} value={audioLabel(runtime, t)} tone={toneFor(runtime?.audioStatus || '', 'audio')} />
        <Badge label={t('diag.room')} value={inRoom ? roomId || t('diag.joined') : t('diag.notJoined')} tone={toneFor(inRoom, 'room')} />
        <Badge label={t('diag.share')} value={effectiveShareValue} tone={toneFor(effectiveShareKey, 'share')} />
        <Badge
          label={t('diag.viewers')}
          value={remoteViewerCount > 0 ? `${remoteViewerCount}` : '0'}
          tone={toneFor(remoteViewerCount, 'viewer')}
        />
      </div>
      <div className="diagnostics-detail">
        <span>{t('diag.runtime', { value: runtime?.mode ? modeLabel(runtime.mode, t) : t('runtime.unknown') })}</span>
        <span>{t('diag.version', { value: runtime?.version || t('runtime.unknown') })}</span>
      </div>
      {audioReason && (
        <div className="diagnostics-detail diagnostics-detail-stack diagnostics-detail-plain">
          <span>{t('diag.audioReason', { value: audioReason })}</span>
        </div>
      )}
      {(showAuthButton || healthLine || preferredMediaLine) && (
        <div className="diagnostics-detail diagnostics-detail-stack">
          {preferredHealthLine && <span>{t('diag.networkNote', { value: preferredHealthLine })}</span>}
          {preferredMediaLine && <span>{t('diag.mediaNote', { value: preferredMediaLine })}</span>}
          {showAuthButton && (
            <button className="diagnostics-link-btn" onClick={onOpenAuthUrl}>
              {t('diag.openAuth')}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
