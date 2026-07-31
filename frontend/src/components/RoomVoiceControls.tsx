import { Mic, MicOff, Volume2, VolumeX } from 'lucide-react';
import type { Translator } from '../i18n';
import { nextMicrophoneEnabled, roomVoiceStatusKey } from '../media/roomVoice';
import type { RoomVoiceState } from '../types';
import './RoomVoiceControls.css';

interface RoomVoiceControlsProps {
  state: RoomVoiceState;
  busy: boolean;
  t: Translator;
  onMicrophoneChange: (enabled: boolean) => void | Promise<void>;
  onPlaybackMutedChange: (muted: boolean) => void | Promise<void>;
}

export default function RoomVoiceControls({
  state,
  busy,
  t,
  onMicrophoneChange,
  onPlaybackMutedChange,
}: RoomVoiceControlsProps) {
  const microphoneEnabled = state.microphone === 'live';
  const microphoneStarting = state.microphone === 'starting';
  const microphoneLabel = microphoneEnabled
    ? t('voice.muteMicrophone')
    : state.microphone === 'unavailable'
      ? t('voice.retryMicrophone')
      : t('voice.unmuteMicrophone');
  const playbackLabel = state.playbackMuted ? t('voice.unmuteChannel') : t('voice.muteChannel');
  const statusLabel = t(roomVoiceStatusKey(state));
  const statusTitle = state.participantCount > 0
    ? t('voice.statusWithCount', { status: statusLabel, count: state.participantCount })
    : statusLabel;

  return (
    <div className="room-voice-controls" aria-label={t('voice.controls')}>
      <span
        className={`room-voice-status status-${state.connection}`}
        role="status"
        aria-label={statusTitle}
        title={statusTitle}
      />
      <button
        type="button"
        className={`room-voice-button ${microphoneEnabled ? 'active' : ''} ${state.microphone === 'unavailable' ? 'error' : ''}`}
        aria-label={microphoneLabel}
        aria-pressed={!microphoneEnabled}
        title={microphoneLabel}
        disabled={busy || microphoneStarting}
        onClick={() => void onMicrophoneChange(nextMicrophoneEnabled(state))}
      >
        {microphoneEnabled ? <Mic size={20} aria-hidden="true" /> : <MicOff size={20} aria-hidden="true" />}
      </button>
      <button
        type="button"
        className={`room-voice-button ${state.playbackMuted ? '' : 'active'}`}
        aria-label={playbackLabel}
        aria-pressed={state.playbackMuted}
        title={playbackLabel}
        disabled={busy}
        onClick={() => void onPlaybackMutedChange(!state.playbackMuted)}
      >
        {state.playbackMuted ? <VolumeX size={20} aria-hidden="true" /> : <Volume2 size={20} aria-hidden="true" />}
      </button>
    </div>
  );
}
