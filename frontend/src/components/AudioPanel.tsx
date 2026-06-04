import { AudioCapability, AudioShareSource, AudioState } from '../types';
import type { Translator } from '../i18n';
import './AudioPanel.css';

interface AudioPanelProps {
  audio: AudioState;
  capability: AudioCapability | null;
  audioSources: AudioShareSource[];
  t: Translator;
  onToggleMic: () => void;
  onToggleSpeaker: () => void;
  onVolumeChange: (volume: number) => void;
  onToggleAudioSource: (pid: number) => void | Promise<void>;
}

export default function AudioPanel({
  audio,
  capability,
  audioSources,
  t,
  onToggleMic,
  onToggleSpeaker,
  onVolumeChange,
  onToggleAudioSource,
}: AudioPanelProps) {
  const appAudioReady = Boolean(capability?.supportsProcessCapture);
  const capabilityTitle = appAudioReady ? t('audio.captureReady') : t('audio.captureUnavailable');
  const capabilityReason = capability?.reason || t('audio.captureFallback');
  const sourceInitial = (source: AudioShareSource) => {
    const label = source.displayName || source.name || '?';
    return label.trim().slice(0, 1).toUpperCase() || '?';
  };

  return (
    <div className="audio-panel">
      <div className="audio-panel-header">
        <span>{t('audio.title')}</span>
        <span className={`audio-capability-pill ${appAudioReady ? 'ready' : 'limited'}`}>
          {appAudioReady ? t('audio.ready') : t('audio.unavailable')}
        </span>
      </div>
      <div className="audio-controls">
        <button
          className={`audio-btn ${audio.micEnabled ? 'active' : 'muted'}`}
          onClick={onToggleMic}
          title={audio.micEnabled ? t('audio.mute') : t('audio.unmute')}
        >
          {audio.micEnabled ? t('audio.mic') : t('audio.muted')}
          <span className="btn-label">{audio.micEnabled ? t('audio.mute') : t('audio.unmute')}</span>
        </button>

        <div className="volume-control">
          <span className="volume-icon" onClick={onToggleSpeaker}>
            {audio.speakerEnabled ? t('audio.volume') : t('audio.mute')}
          </span>
          <input
            type="range"
            className="volume-slider"
            min={0}
            max={100}
            value={audio.volume}
            onChange={(e) => onVolumeChange(Number(e.target.value))}
            disabled={!audio.speakerEnabled}
          />
          <span className="volume-value">{audio.volume}%</span>
        </div>

        <button
          className={`audio-btn ${audio.speakerEnabled ? 'active' : 'muted'}`}
          onClick={onToggleSpeaker}
          title={audio.speakerEnabled ? t('audio.mute') : t('audio.speaker')}
        >
          {audio.speakerEnabled ? t('audio.speaker') : t('audio.muted')}
          <span className="btn-label">{audio.speakerEnabled ? t('audio.speaker') : t('audio.muted')}</span>
        </button>
      </div>

      <div className={`audio-capability-card ${appAudioReady ? 'ready' : 'limited'}`}>
        <div className="audio-capability-title">{capabilityTitle}</div>
        <div className="audio-capability-reason">{capabilityReason}</div>
      </div>

      <div className="audio-share-header">{t('audio.sharedSources')}</div>
      <div className="audio-share-list">
        {audioSources.length === 0 ? (
          <div className="audio-share-empty">{t('audio.noActiveSources')}</div>
        ) : (
          audioSources.map((source) => (
            <label
              key={source.pid}
              className={`audio-share-item ${source.selected ? 'selected' : ''}`}
              onClick={(event) => {
                event.preventDefault();
                void onToggleAudioSource(source.pid);
              }}
            >
              <span className="audio-share-icon" aria-hidden="true">
                {source.iconDataUrl ? (
                  <img src={source.iconDataUrl} alt="" />
                ) : (
                  <span className="audio-share-icon-fallback">{sourceInitial(source)}</span>
                )}
              </span>
              <span className="audio-share-meta">
                <span className="audio-share-name">{source.displayName || source.name || `Process ${source.pid}`}</span>
                <span className="audio-share-process">{source.name || `PID ${source.pid}`}</span>
              </span>
              <input
                type="radio"
                name="partylan-audio-source"
                checked={source.selected}
                readOnly
              />
            </label>
          ))
        )}
      </div>
    </div>
  );
}
