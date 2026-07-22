import { useState, type ReactNode } from 'react';
import type { GameCastRoomState, GameCastSessionState } from '../types';
import type { Translator } from '../i18n';
import './LobbyView.css';

interface LobbyHistoryEntry {
  id: string;
  time: number;
}

interface LobbyStatusItem {
  label: string;
  value: string;
}

interface LobbyViewProps {
  t: Translator;
  busy: boolean;
  error: string | null;
  session: GameCastSessionState | null;
  authUser: string;
  authPass: string;
  rooms: GameCastRoomState[];
  history: LobbyHistoryEntry[];
  statusItems: LobbyStatusItem[];
  serverConfigured: boolean;
  serverNeedsTrust: boolean;
  canSubmitAuth: boolean;
  canOpenWeb: boolean;
  legacyContent?: ReactNode;
  onAuthUserChange: (value: string) => void;
  onAuthPassChange: (value: string) => void;
  onLogin: () => void;
  onRegister: () => void;
  onLogout: () => void;
  onCreateRoom: () => void;
  onJoinRoom: (roomId: string, room?: GameCastRoomState) => void;
  onRemoveHistory: (roomId: string) => void;
  onOpenWeb: () => void;
  onOpenSettings: () => void;
}

function roomOriginLabel(room: GameCastRoomState, t: Translator): string {
  if (room.origin === 'client') return t('gamecast.originClient');
  if (room.origin === 'web') return t('gamecast.originWeb');
  return t('gamecast.originCompat');
}

export default function LobbyView({
  t,
  busy,
  error,
  session,
  authUser,
  authPass,
  rooms,
  history,
  statusItems,
  serverConfigured,
  serverNeedsTrust,
  canSubmitAuth,
  canOpenWeb,
  legacyContent,
  onAuthUserChange,
  onAuthPassChange,
  onLogin,
  onRegister,
  onLogout,
  onCreateRoom,
  onJoinRoom,
  onRemoveHistory,
  onOpenWeb,
  onOpenSettings,
}: LobbyViewProps) {
  const [moreOpen, setMoreOpen] = useState(false);
  const serverBlocked = !serverConfigured || serverNeedsTrust;

  return (
    <main className="lobby-view">
      <header className="lobby-heading">
        <h1>PartyLAN</h1>
        <p>{t('lobby.subtitle')}</p>
      </header>

      <section className="lobby-action-panel" aria-label={t('lobby.actions')}>
        {serverBlocked && (
          <div className="lobby-server-alert" role="status">
            <span>
              {serverNeedsTrust ? t('lobby.serverTrustRequired') : t('lobby.serverMissing')}
            </span>
            <button type="button" className="lobby-link-button" onClick={onOpenSettings}>
              {t('lobby.openSettings')}
            </button>
          </div>
        )}

        <section className="lobby-section" aria-labelledby="lobby-account-title">
          <div className="lobby-section-header">
            <h2 id="lobby-account-title">{t('lobby.account')}</h2>
            {session?.loggedIn && <span className="lobby-account-state">{session.user}</span>}
          </div>

          {session?.loggedIn ? (
            <div className="lobby-account-row">
              <span>{t('gamecast.loggedInAs', { user: session.user || 'unknown' })}</span>
              <button type="button" className="lobby-button quiet" onClick={onLogout} disabled={busy}>
                {t('gamecast.logout')}
              </button>
            </div>
          ) : (
            <form
              className="lobby-auth-form"
              onSubmit={(event) => {
                event.preventDefault();
                onLogin();
              }}
            >
              <label className="lobby-field">
                <span>{t('gamecast.username')}</span>
                <input
                  value={authUser}
                  onChange={(event) => onAuthUserChange(event.target.value)}
                  autoComplete="username"
                  maxLength={80}
                />
              </label>
              <label className="lobby-field">
                <span>{t('gamecast.password')}</span>
                <input
                  value={authPass}
                  onChange={(event) => onAuthPassChange(event.target.value)}
                  type="password"
                  autoComplete="current-password"
                />
              </label>
              <div className="lobby-auth-actions">
                <button type="submit" className="lobby-button secondary" disabled={busy || !canSubmitAuth}>
                  {t('gamecast.login')}
                </button>
                <button
                  type="button"
                  className="lobby-button quiet"
                  onClick={onRegister}
                  disabled={busy || !canSubmitAuth}
                >
                  {t('gamecast.register')}
                </button>
              </div>
            </form>
          )}
        </section>

        <section className="lobby-section lobby-room-section" aria-labelledby="lobby-rooms-title">
          <button
            type="button"
            className="lobby-button primary lobby-create-button"
            onClick={onCreateRoom}
            disabled={busy || serverBlocked}
          >
            {t('gamecast.createOwnRoom')}
          </button>

          <div className="lobby-section-header">
            <h2 id="lobby-rooms-title">{t('gamecast.availableRooms')}</h2>
            <span>{t('gamecast.roomCount', { count: rooms.length })}</span>
          </div>

          <div className="lobby-room-list">
            {rooms.length > 0 ? (
              rooms.map((room) => (
                <button
                  key={`${room.room}-${room.updatedAt || room.user || ''}`}
                  type="button"
                  className="lobby-room-row"
                  onClick={() => onJoinRoom(room.room, room)}
                  disabled={busy || serverBlocked}
                  aria-label={room.room}
                  title={room.room}
                >
                  <span className="lobby-room-copy">
                    <strong>{room.room}</strong>
                    <span>{room.user || t('gamecast.remoteHost')} - {roomOriginLabel(room, t)}</span>
                  </span>
                  <span className={`lobby-room-state ${room.sharing ? 'live' : ''}`}>
                    {room.sharing ? t('gamecast.roomLive') : t('gamecast.roomActive')}
                  </span>
                </button>
              ))
            ) : (
              <div className="lobby-empty-state">{t('gamecast.noAvailableRooms')}</div>
            )}
          </div>
        </section>

        <section className="lobby-more-section">
          <button
            type="button"
            className="lobby-more-toggle"
            aria-expanded={moreOpen}
            aria-controls="lobby-more-content"
            onClick={() => setMoreOpen((current) => !current)}
          >
            <span>{t('lobby.more')}</span>
            <span className="lobby-more-indicator" aria-hidden="true" />
          </button>

          {moreOpen && (
            <div id="lobby-more-content" className="lobby-more-content">
              <div className="lobby-status-list">
                {statusItems.map((item) => (
                  <div key={item.label} className="lobby-status-row">
                    <span>{item.label}</span>
                    <strong>{item.value}</strong>
                  </div>
                ))}
              </div>

              {history.length > 0 && (
                <div className="lobby-history">
                  <h3>{t('gamecast.recentRooms')}</h3>
                  {history.slice(0, 5).map((entry) => (
                    <div key={`${entry.id}-${entry.time}`} className="lobby-history-row">
                      <button
                        type="button"
                        className="lobby-history-room"
                        onClick={() => onJoinRoom(entry.id)}
                        title={entry.id}
                      >
                        {entry.id}
                      </button>
                      <button
                        type="button"
                        className="lobby-history-remove"
                        onClick={() => onRemoveHistory(entry.id)}
                        aria-label={t('gamecast.removeHistory', { room: entry.id })}
                        title={t('gamecast.removeHistory', { room: entry.id })}
                      >
                        x
                      </button>
                    </div>
                  ))}
                </div>
              )}

              <button type="button" className="lobby-button quiet lobby-web-button" onClick={onOpenWeb} disabled={!canOpenWeb}>
                {t('gamecast.openWeb')}
              </button>
            </div>
          )}
        </section>

        {error && <div className="lobby-error" role="alert">{error}</div>}
      </section>

      {legacyContent && <div className="lobby-legacy-content">{legacyContent}</div>}
    </main>
  );
}
