import { useEffect, useMemo, useState } from 'react';
import AudioPanel from './AudioPanel';
import type { ShareSettings } from './RoomControls';
import type { AudioCapability, AudioShareSource, AudioState, VoiceChatState } from '../types';
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

export default function RoomToolsDialog({
  open,
  initialTab,
  busy,
  sharing,
  hostVideoCapable,
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
  const [resolutionKey, setResolutionKey] = useState<string>('1280x720');
  const [fpsValue, setFpsValue] = useState<number>(30);
  const selectedResolution = useMemo(() => parseResolutionKey(resolutionKey), [resolutionKey]);

  useEffect(() => {
    if (open) {
      setActiveTab(initialTab);
    }
  }, [initialTab, open]);

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
              <div className="room-tools-grid">
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
                {t('tools.audioNote')}
              </div>

              <button
                className="room-tools-primary"
                onClick={() => {
                  void onStartSharing({
                    width: selectedResolution.width,
                    height: selectedResolution.height,
                    fps: fpsValue,
                  });
                }}
                disabled={busy || sharing || !hostVideoCapable}
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
              onToggleMic={onToggleMic}
              onToggleSpeaker={onToggleSpeaker}
              onVolumeChange={onVolumeChange}
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
