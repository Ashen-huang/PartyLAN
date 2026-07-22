import { useEffect, useMemo, useState } from 'react';
import AudioPanel from './AudioPanel';
import type { ShareSettings } from './RoomControls';
import type { AudioCapability, AudioShareSource, AudioState, ShareSource, ShareSourceKind, VoiceChatState } from '../types';
import type { Translator } from '../i18n';
import './RoomToolsDialog.css';

export type RoomToolsTab = 'share' | 'audio' | 'voice';

interface ResolutionOption {
  label: string;
  width: number;
  height: number;
}

interface FPSOption {
  label: string;
  fps: number;
}

interface RoomToolsDialogProps {
  open: boolean;
  initialTab: RoomToolsTab;
  busy: boolean;
  sharing: boolean;
  hostVideoCapable: boolean;
  shareSources: ShareSource[];
  audio: AudioState;
  audioCapability: AudioCapability | null;
  audioSources: AudioShareSource[];
  voice: VoiceChatState;
  error: string | null;
  t: Translator;
  onClose: () => void;
  onStartSharing: (settings: ShareSettings) => void | Promise<void>;
  onToggleMic: () => void;
  onToggleSpeaker: () => void;
  onVolumeChange: (volume: number) => void;
  onToggleAudioSource: (pid: number) => void | Promise<void>;
  onStartVoice: () => void | Promise<void>;
  onStopVoice: () => void | Promise<void>;
  onToggleVoiceMuted: () => void | Promise<void>;
  onVoiceVolumeChange: (volume: number) => void | Promise<void>;
}

const RESOLUTION_OPTIONS: ResolutionOption[] = [
  { label: 'Original', width: 0, height: 0 },
  { label: '1920 x 1080', width: 1920, height: 1080 },
  { label: '1600 x 900', width: 1600, height: 900 },
  { label: '1280 x 720', width: 1280, height: 720 },
  { label: '960 x 540', width: 960, height: 540 },
];

const FPS_OPTIONS: FPSOption[] = [
  { label: '15 FPS', fps: 15 },
  { label: '24 FPS', fps: 24 },
  { label: '30 FPS', fps: 30 },
  { label: '45 FPS', fps: 45 },
  { label: '60 FPS', fps: 60 },
];

function formatResolutionKey(width: number, height: number): string {
  return width > 0 && height > 0 ? `${width}x${height}` : 'original';
}

function parseResolutionKey(key: string): ResolutionOption {
  if (key === 'original') {
    return RESOLUTION_OPTIONS[0];
  }

  const [wRaw, hRaw] = key.split('x');
  const width = Number.parseInt(wRaw || '', 10);
  const height = Number.parseInt(hRaw || '', 10);
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    return RESOLUTION_OPTIONS[0];
  }

  return {
    label: `${width} x ${height}`,
    width,
    height,
  };
}

function voiceStatusLabel(voice: VoiceChatState, t: Translator): string {
  if (voice.status === 'starting') return t('voice.connecting');
  if (voice.status === 'error') return t('voice.error');
  if (voice.muted) return t('voice.muted');
  if (voice.active) return t('voice.active');
  return t('voice.idle');
}

function sourceInitial(source: ShareSource): string {
  const label = source.subtitle || source.label || '?';
  return label.trim().slice(0, 1).toUpperCase() || '?';
}

export default function RoomToolsDialog({
  open,
  initialTab,
  busy,
  sharing,
  hostVideoCapable,
  shareSources,
  audio,
  audioCapability,
  audioSources,
  voice,
  error,
  t,
  onClose,
  onStartSharing,
  onToggleMic,
  onToggleSpeaker,
  onVolumeChange,
  onToggleAudioSource,
  onStartVoice,
  onStopVoice,
  onToggleVoiceMuted,
  onVoiceVolumeChange,
}: RoomToolsDialogProps) {
  const [activeTab, setActiveTab] = useState<RoomToolsTab>(initialTab);
  const [sourceKind, setSourceKind] = useState<ShareSourceKind>('display');
  const [sourceId, setSourceId] = useState('');
  const [shareAudioMode, setShareAudioMode] = useState<'default' | 'off' | 'process'>('default');
  const [resolutionKey, setResolutionKey] = useState<string>('1280x720');
  const [fpsValue, setFpsValue] = useState<number>(30);
  const selectedResolution = useMemo(() => parseResolutionKey(resolutionKey), [resolutionKey]);
  const displaySources = useMemo(() => shareSources.filter((source) => source.kind === 'display'), [shareSources]);
  const windowSources = useMemo(() => shareSources.filter((source) => source.kind === 'window'), [shareSources]);
  const visibleSources = sourceKind === 'window' ? windowSources : displaySources;
  const selectedSource = useMemo(
    () => visibleSources.find((source) => source.id === sourceId) || visibleSources[0] || null,
    [visibleSources, sourceId],
  );
  const audioPolicySummary = useMemo(() => {
    if (!selectedSource) {
      return t('audio.defaultNone');
    }
    if (selectedSource.audioDefault === 'system') {
      return t('audio.defaultSystem');
    }
    if (selectedSource.audioDefault === 'process' && selectedSource.audioProcessName) {
      return t('audio.defaultProcess', { name: selectedSource.audioProcessName });
    }
    return t('audio.defaultNone');
  }, [selectedSource, t]);

  useEffect(() => {
    if (open) {
      setActiveTab(initialTab);
      setShareAudioMode('default');
    }
  }, [initialTab, open]);

  useEffect(() => {
    const nextSources = sourceKind === 'window' ? windowSources : displaySources;
    if (nextSources.length === 0) {
      setSourceId('');
      return;
    }
    if (!nextSources.some((source) => source.id === sourceId)) {
      setSourceId(nextSources[0].id);
    }
  }, [sourceKind, displaySources, windowSources, sourceId]);

  if (!open) {
    return null;
  }

  const tabs: Array<{ id: RoomToolsTab; label: string }> = [
    { id: 'share', label: t('tools.screenShare') },
    { id: 'audio', label: t('tools.sharedAudio') },
    { id: 'voice', label: t('tools.voiceChat') },
  ];

  return (
    <div className="room-tools-overlay" onClick={onClose}>
      <div className="room-tools-dialog" onClick={(event) => event.stopPropagation()}>
        <div className="room-tools-header">
          <div>
            <div className="room-tools-title">{t('tools.title')}</div>
            <div className="room-tools-subtitle">
              {activeTab === 'share'
                ? t('tools.shareSubtitle')
                : activeTab === 'audio'
                  ? t('tools.audioSubtitle')
                  : t('tools.voiceSubtitle')}
            </div>
          </div>
          <button className="room-tools-close" onClick={onClose}>
            X
          </button>
        </div>

        <div className="room-tools-tabs">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              className={`room-tools-tab ${activeTab === tab.id ? 'active' : ''}`}
              onClick={() => setActiveTab(tab.id)}
              type="button"
            >
              {tab.label}
            </button>
          ))}
        </div>

        <div className="room-tools-body">
          {activeTab === 'share' && (
            <div className="room-tools-section">
              <div className="room-tools-source-toggle">
                <button
                  type="button"
                  className={`room-tools-source-btn ${sourceKind === 'display' ? 'active' : ''}`}
                  onClick={() => setSourceKind('display')}
                  disabled={busy || sharing}
                >
                  {t('tools.sourceDisplay')}
                </button>
                <button
                  type="button"
                  className={`room-tools-source-btn ${sourceKind === 'window' ? 'active' : ''}`}
                  onClick={() => setSourceKind('window')}
                  disabled={busy || sharing}
                >
                  {t('tools.sourceWindow')}
                </button>
              </div>

              <div className="room-tools-grid">
                <div className="room-tools-field room-tools-field-wide">
                  <span>{t('tools.source')}</span>
                  {visibleSources.length > 0 ? (
                    <div className="room-tools-source-list">
                      {visibleSources.map((source) => (
                        <button
                          key={source.id}
                          type="button"
                          className={`room-tools-source-card ${source.id === selectedSource?.id ? 'selected' : ''}`}
                          onClick={() => setSourceId(source.id)}
                          disabled={busy || sharing}
                        >
                          <span className="room-tools-source-preview" aria-hidden="true">
                            {source.previewDataUrl ? (
                              <img src={source.previewDataUrl} alt="" />
                            ) : source.iconDataUrl ? (
                              <span className="room-tools-source-icon">
                                <img src={source.iconDataUrl} alt="" />
                              </span>
                            ) : (
                              <span className="room-tools-source-icon">
                                <span className="room-tools-source-icon-fallback">{sourceInitial(source)}</span>
                              </span>
                            )}
                          </span>
                          <span className="room-tools-source-meta">
                            <span className="room-tools-source-name">{source.label}</span>
                            <span className="room-tools-source-subtitle">{source.subtitle || t('tools.sourceDisplay')}</span>
                          </span>
                        </button>
                      ))}
                    </div>
                  ) : (
                    <div className="room-tools-note">{t('tools.sourceUnavailable')}</div>
                  )}
                </div>

                <label className="room-tools-field">
                  <span>{t('tools.resolution')}</span>
                  <select
                    value={resolutionKey}
                    onChange={(event) => setResolutionKey(event.target.value)}
                    disabled={busy || sharing}
                  >
                    {RESOLUTION_OPTIONS.map((option) => {
                      const key = formatResolutionKey(option.width, option.height);
                      return (
                        <option key={key} value={key}>
                          {option.label}
                        </option>
                      );
                    })}
                  </select>
                </label>

                <label className="room-tools-field">
                  <span>{t('tools.frameRate')}</span>
                  <select
                    value={String(fpsValue)}
                    onChange={(event) => setFpsValue(Number.parseInt(event.target.value, 10) || 30)}
                    disabled={busy || sharing}
                  >
                    {FPS_OPTIONS.map((option) => (
                      <option key={option.fps} value={String(option.fps)}>
                        {option.label}
                      </option>
                    ))}
                  </select>
                </label>
              </div>

              <div className="room-tools-note">
                {selectedSource ? t('tools.sourceSelected', { source: selectedSource.label }) : t('tools.sourceUnavailable')}
              </div>
              <div className="room-tools-note">
                {audioPolicySummary}
              </div>

              <button
                className="room-tools-primary"
                onClick={() => {
                  if (!selectedSource) {
                    return;
                  }
                  void onStartSharing({
                    sourceId: selectedSource.id,
                    sourceKind: selectedSource.kind,
                    sourceLabel: selectedSource.label,
                    audioMode: shareAudioMode,
                    audioDefault: selectedSource.audioDefault,
                    audioProcessName: selectedSource.audioProcessName,
                    audioProcessId: selectedSource.processId,
                    width: selectedResolution.width,
                    height: selectedResolution.height,
                    fps: fpsValue,
                  });
                }}
                disabled={busy || sharing || !hostVideoCapable || !selectedSource}
                type="button"
              >
                {sharing ? t('tools.sharingActive') : t('room.startSharing')}
              </button>
            </div>
          )}

          {activeTab === 'audio' && (
            <AudioPanel
              audio={audio}
              capability={audioCapability}
              audioSources={audioSources}
              shareAudioPolicy={audioPolicySummary}
              shareAudioMode={shareAudioMode}
              onToggleMic={onToggleMic}
              onToggleSpeaker={onToggleSpeaker}
              onVolumeChange={onVolumeChange}
              onShareAudioModeChange={setShareAudioMode}
              onToggleAudioSource={onToggleAudioSource}
              t={t}
            />
          )}

          {activeTab === 'voice' && (
            <div className="room-tools-section voice-tools-section">
              <div className={`voice-state-card status-${voice.status}`}>
                <div className="voice-state-main">
                  <span className="voice-state-label">{t('tools.voice')}</span>
                  <span className="voice-state-value">{voiceStatusLabel(voice, t)}</span>
                </div>
                <div className="voice-state-detail">{voice.message}</div>
                {voice.error && <div className="voice-state-error">{voice.error}</div>}
              </div>

              <div className="voice-actions">
                <button
                  className="room-tools-primary"
                  onClick={() => {
                    void (voice.active ? onStopVoice() : onStartVoice());
                  }}
                  disabled={busy || voice.status === 'starting'}
                  type="button"
                >
                  {voice.active ? t('tools.stopVoice') : voice.status === 'starting' ? t('tools.connecting') : t('tools.startVoice')}
                </button>
                <button
                  className="room-tools-secondary"
                  onClick={() => {
                    void onToggleVoiceMuted();
                  }}
                  disabled={!voice.active || voice.status === 'starting'}
                  type="button"
                >
                  {voice.muted ? t('tools.unmute') : t('tools.mute')}
                </button>
              </div>

              <label className="voice-volume-control">
                <span>{t('tools.voiceVolume')}</span>
                <input
                  type="range"
                  min={0}
                  max={100}
                  value={voice.volume}
                  onChange={(event) => {
                    void onVoiceVolumeChange(Number(event.target.value));
                  }}
                />
                <span>{voice.volume}%</span>
              </label>
            </div>
          )}

          {error && <div className="room-tools-error">{error}</div>}
        </div>
      </div>
    </div>
  );
}
