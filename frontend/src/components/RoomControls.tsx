import type { ShareAudioDefault, ShareSourceKind } from '../types';
import { useState } from 'react';
import type { Translator } from '../i18n';
import './RoomControls.css';

export interface ShareSettings {
  sourceId: string;
  sourceKind: ShareSourceKind;
  sourceLabel: string;
  audioMode: 'default' | 'off' | 'process';
  audioDefault: ShareAudioDefault;
  audioProcessName?: string;
  audioProcessId?: number;
  width: number;
  height: number;
  fps: number;
}

interface RoomControlsProps {
  inRoom: boolean;
  roomId: string | null;
  sharing: boolean;
  selfSharing: boolean;
  canControlSharing: boolean;
  hostVideoCapable: boolean;
  canShareToPeers: boolean;
  remoteViewerCount: number;
  busy: boolean;
  t: Translator;
  onCreateRoom: () => void | Promise<void>;
  onJoinRoom: (targetNodeId: string) => void | Promise<void>;
  onLeaveRoom: () => void | Promise<void>;
  onOpenShareSettings: () => void | Promise<void>;
  onStopSharing: () => void | Promise<void>;
}

export default function RoomControls({
  inRoom,
  roomId,
  sharing,
  selfSharing,
  canControlSharing,
  hostVideoCapable,
  canShareToPeers,
  remoteViewerCount,
  busy,
  t,
  onCreateRoom,
  onJoinRoom,
  onLeaveRoom,
  onOpenShareSettings,
  onStopSharing,
}: RoomControlsProps) {
  const [joinId, setJoinId] = useState('');

  if (inRoom) {
    const singleUserRoom = remoteViewerCount === 0;
    const shareActionLabel = sharing ? t('room.stopSharing') : singleUserRoom ? t('room.startSharingLocal') : t('room.startSharing');

    return (
      <div className="room-controls">
        <div className="room-info">
          <span className="room-label">{t('room.room')}</span>
          <span className="room-id">{roomId}</span>
        </div>

        <div className="room-hint">
          {selfSharing
            ? t('room.selfSharingHint')
            : singleUserRoom
              ? t('room.singleUserHint')
              : canShareToPeers
                ? t('room.readyHint')
                : t('room.waitingHint')}
        </div>

        <button
          className={`room-btn ${sharing ? 'share-stop' : 'share-start'}`}
          onClick={sharing ? onStopSharing : onOpenShareSettings}
          disabled={busy || (sharing && !canControlSharing) || (!sharing && !hostVideoCapable)}
          title={!sharing && !hostVideoCapable ? t('room.shareUnavailableTitle') : undefined}
        >
          {shareActionLabel}
        </button>
        <button className="room-btn leave" onClick={onLeaveRoom} disabled={busy}>
          {t('room.leave')}
        </button>
      </div>
    );
  }

  return (
    <div className="room-controls">
      <button className="room-btn create" onClick={onCreateRoom} disabled={busy}>
        {t('room.create')}
      </button>
      <div className="room-join-row">
        <input
          className="room-input"
          placeholder={t('room.targetNode')}
          value={joinId}
          onChange={(e) => setJoinId(e.target.value)}
        />
        <button
          className="room-btn join"
          onClick={() => {
            if (joinId.trim()) {
              void onJoinRoom(joinId.trim());
            }
          }}
          disabled={busy || !joinId.trim()}
        >
          {t('room.join')}
        </button>
      </div>
    </div>
  );
}
