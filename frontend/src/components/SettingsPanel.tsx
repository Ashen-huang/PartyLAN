import DiagnosticsPanel from './DiagnosticsPanel';
import type { AudioCapability, GameCastProbeResult, RuntimeStatus, VideoCapability } from '../types';
import { languageOptions, type Language, type Translator } from '../i18n';
import './SettingsPanel.css';

interface SettingsPanelProps {
  open: boolean;
  authKey: string;
  displayName: string;
  gameCastServer: string;
  gameCastProbe: GameCastProbeResult | null;
  gameCastNeedsTrust: boolean;
  runtime: RuntimeStatus | null;
  videoCapability: VideoCapability | null;
  audioCapability: AudioCapability | null;
  inRoom: boolean;
  roomId: string | null;
  sharing: boolean;
  selfSharing: boolean;
  hostVideoCapable: boolean;
  canShareToPeers: boolean;
  remoteViewerCount: number;
  diagnosticsOverlayVisible: boolean;
  language: Language;
  busy: boolean;
  saving: boolean;
  error: string | null;
  t: Translator;
  onClose: () => void;
  onAuthKeyChange: (value: string) => void;
  onDisplayNameChange: (value: string) => void;
  onGameCastServerChange: (value: string) => void;
  onProbeGameCast: () => void;
  onTrustGameCastCertificate: () => void;
  onDiagnosticsOverlayVisibleChange: (visible: boolean) => void;
  onLanguageChange: (language: Language) => void;
  onSave: () => void | Promise<void>;
  onOpenAuthUrl: () => void;
}

export default function SettingsPanel({
  open,
  authKey,
  displayName,
  gameCastServer,
  gameCastProbe,
  gameCastNeedsTrust,
  runtime,
  videoCapability,
  audioCapability,
  inRoom,
  roomId,
  sharing,
  selfSharing,
  hostVideoCapable,
  canShareToPeers,
  remoteViewerCount,
  diagnosticsOverlayVisible,
  language,
  busy,
  saving,
  error,
  t,
  onClose,
  onAuthKeyChange,
  onDisplayNameChange,
  onGameCastServerChange,
  onProbeGameCast,
  onTrustGameCastCertificate,
  onDiagnosticsOverlayVisibleChange,
  onLanguageChange,
  onSave,
  onOpenAuthUrl,
}: SettingsPanelProps) {
  if (!open) {
    return null;
  }

  const showLegacyNetworkSettings = runtime?.mode === 'networked' || runtime?.mode === 'tailscale-system';

  return (
    <div className="settings-overlay" onClick={onClose}>
      <div className="settings-panel" onClick={(e) => e.stopPropagation()}>
        <div className="settings-header">
          <div>
            <div className="settings-title">{t('settings.title')}</div>
            <div className="settings-subtitle">
              {runtime?.mode === 'gamecast-client'
                ? t('settings.gamecastSubtitle')
                : runtime?.mode === 'networked'
                ? t('settings.networkedSubtitle')
                : t('settings.defaultSubtitle')}
            </div>
          </div>
          <button className="settings-close" onClick={onClose} aria-label={t('settings.cancel')} title={t('settings.cancel')}>
            X
          </button>
        </div>

        <div className="settings-body">
          <div className="settings-section">
            <div className="settings-section-title">{t('settings.identity')}</div>
            <label className="settings-field">
              <span className="settings-label">{t('settings.displayName')}</span>
              <input
                className="settings-input"
                type="text"
                value={displayName}
                onChange={(e) => onDisplayNameChange(e.target.value)}
                placeholder={t('settings.displayNamePlaceholder')}
                maxLength={40}
                autoComplete="nickname"
              />
            </label>
            <div className="settings-note">{t('settings.displayNameNote')}</div>
          </div>

          {runtime?.mode === 'gamecast-client' && (
            <div className="settings-section settings-section-wide settings-section-gamecast">
              <div className="settings-section-title">{t('settings.gamecastServer')}</div>
              <label className="settings-field">
                <span className="settings-label">{t('settings.serverAddress')}</span>
                <input
                  className="settings-input"
                  type="url"
                  value={gameCastServer}
                  onChange={(event) => onGameCastServerChange(event.target.value)}
                  placeholder={t('settings.serverAddressPlaceholder')}
                  spellCheck={false}
                  autoComplete="url"
                />
              </label>
              <div className="settings-server-row">
                <button
                  type="button"
                  className="settings-btn outline"
                  onClick={onProbeGameCast}
                  disabled={saving || busy || !gameCastServer.trim()}
                >
                  {t('gamecast.probe')}
                </button>
                <span className="settings-server-status" role="status">
                  {gameCastProbe
                    ? t('gamecast.serverState', { value: gameCastProbe.status })
                    : t('settings.serverNotChecked')}
                </span>
              </div>
              {gameCastProbe?.message && (
                <div className="settings-note settings-server-message">{gameCastProbe.message}</div>
              )}
              {gameCastNeedsTrust && (
                <div className="settings-certificate" role="alert">
                  <span>{t('settings.certificateFingerprint')}</span>
                  <code>{gameCastProbe?.certificateFingerprint || ''}</code>
                  <button
                    type="button"
                    className="settings-btn outline"
                    onClick={onTrustGameCastCertificate}
                    disabled={saving || busy}
                  >
                    {t('gamecast.trust')}
                  </button>
                </div>
              )}
              <div className="settings-note">{t('settings.serverAddressNote')}</div>
            </div>
          )}

          {showLegacyNetworkSettings && (
            <div className="settings-section">
              <div className="settings-section-title">{t('settings.network')}</div>
              <label className="settings-field">
                <span className="settings-label">{t('settings.authKey')}</span>
                <input
                  className="settings-input"
                  type="password"
                  value={authKey}
                  onChange={(e) => onAuthKeyChange(e.target.value)}
                  placeholder="tskey-auth-..."
                  spellCheck={false}
                  autoComplete="off"
                />
              </label>

              <div className="settings-note">
                {t('settings.authNote')}
              </div>
            </div>
          )}

          <div className="settings-section">
            <div className="settings-section-title">{t('settings.preferences')}</div>
            <label className="settings-field">
              <span className="settings-label">{t('settings.language')}</span>
              <select
                className="settings-input"
                value={language}
                onChange={(event) => onLanguageChange(event.target.value as Language)}
              >
                {languageOptions.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>
          </div>

          <div className="settings-section settings-section-wide">
            <div className="settings-section-title">{t('settings.diagnostics')}</div>
            <label className="settings-toggle">
              <input
                type="checkbox"
                checked={diagnosticsOverlayVisible}
                onChange={(event) => onDiagnosticsOverlayVisibleChange(event.target.checked)}
              />
              <span>{t('settings.showOverlay')}</span>
            </label>
            <DiagnosticsPanel
              className="settings-diagnostics-panel"
              showHeader={false}
              mode={runtime?.mode || 'unknown'}
              networkBackend={runtime?.networkBackend || 'unknown'}
              videoCapability={videoCapability}
              audioCapability={audioCapability}
              runtime={runtime}
              inRoom={inRoom}
              roomId={roomId}
              sharing={sharing}
              selfSharing={selfSharing}
              hostVideoCapable={hostVideoCapable}
              canShareToPeers={canShareToPeers}
              remoteViewerCount={remoteViewerCount}
              t={t}
              onOpenAuthUrl={onOpenAuthUrl}
            />
          </div>

          {error && <div className="settings-error">{error}</div>}
        </div>

        <div className="settings-actions">
          <button className="settings-btn secondary" onClick={onClose} disabled={saving || busy}>
            {t('settings.cancel')}
          </button>
          <button className="settings-btn primary" onClick={onSave} disabled={saving || busy}>
            {saving ? t('settings.saving') : t('settings.save')}
          </button>
        </div>
      </div>
    </div>
  );
}
