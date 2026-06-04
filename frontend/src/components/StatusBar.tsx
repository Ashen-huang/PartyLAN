import { NetworkStats, VideoCapability, RuntimeStatus } from '../types';
import type { Translator } from '../i18n';
import './StatusBar.css';

interface StatusBarProps {
  stats: NetworkStats;
  capability: VideoCapability | null;
  runtime: RuntimeStatus | null;
  t: Translator;
}

function describeMode(mode: string, t: Translator) {
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

function describeNetworkBackend(backend: string, t: Translator) {
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

function describeVideoBackend(backend: string, status: string, t: Translator) {
  if (backend === 'native' && status === 'supported') {
    return t('video.nativeReady');
  }
  if (backend === 'native') {
    return t('video.nativeLimited');
  }
  if (backend === 'software') {
    return t('video.softwareFallback');
  }
  return backend || t('video.unknown');
}

export default function StatusBar({ stats, capability, runtime, t }: StatusBarProps) {
  const latencyText = stats.latency !== null ? `${stats.latency}ms` : '--ms';
  const bitrateText = stats.bitrate !== null ? `${stats.bitrate.toFixed(2)}Mbps` : '--Mbps';
  const stateLabels: Record<string, string> = {
    disconnected: t('status.disconnected'),
    connecting: t('status.connecting'),
    connected: t('status.connected'),
  };

  return (
    <div className="status-bar">
      <span>{t('status.latency', { value: latencyText })}</span>
      <span className="status-sep">|</span>
      <span>{t('status.bitrate', { value: bitrateText })}</span>
      <span className="status-sep">|</span>
      <span>{stateLabels[stats.connectionState]}</span>
      {stats.connectionState === 'connected' && (
        <>
          <span className="status-sep">|</span>
          <span>{stats.connectionType}</span>
        </>
      )}
      {capability && (
        <>
          <span className="status-sep">|</span>
          <span>{capability.supportsHost ? t('status.hostBackend', { backend: capability.backend }) : t('status.hostLimited')}</span>
        </>
      )}
      {runtime && (
        <>
          <span className="status-sep">|</span>
          <span>{describeMode(runtime.mode, t)}</span>
          <span className="status-sep">|</span>
          <span>{describeNetworkBackend(runtime.networkBackend, t)}</span>
          <span className="status-sep">|</span>
          <span>{describeVideoBackend(runtime.videoBackend, runtime.videoStatus, t)}</span>
        </>
      )}
    </div>
  );
}
