import { X } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import type { Translator } from '../i18n';
import type { ShareSource, ShareSourceKind } from '../types';
import type { ShareSettings } from './RoomControls';
import './RoomToolsDialog.css';

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
  busy: boolean;
  sharing: boolean;
  hostVideoCapable: boolean;
  shareSources: ShareSource[];
  error: string | null;
  t: Translator;
  onClose: () => void;
  onStartSharing: (settings: ShareSettings) => void | Promise<void>;
}

const RESOLUTION_OPTIONS: ResolutionOption[] = [
  { label: '原始画质', width: 0, height: 0 },
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
  return { label: `${width} x ${height}`, width, height };
}

function sourceInitial(source: ShareSource): string {
  const label = source.subtitle || source.label || '?';
  return label.trim().slice(0, 1).toUpperCase() || '?';
}

export default function RoomToolsDialog({
  open,
  busy,
  sharing,
  hostVideoCapable,
  shareSources,
  error,
  t,
  onClose,
  onStartSharing,
}: RoomToolsDialogProps) {
  const [sourceKind, setSourceKind] = useState<ShareSourceKind>('display');
  const [sourceId, setSourceId] = useState('');
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

  return (
    <div className="room-tools-overlay" onClick={onClose}>
      <div
        className="room-tools-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="room-tools-title"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="room-tools-header">
          <div>
            <div className="room-tools-title" id="room-tools-title">{t('tools.title')}</div>
            <div className="room-tools-subtitle">{t('tools.shareSubtitle')}</div>
          </div>
          <button
            className="room-tools-close"
            type="button"
            onClick={onClose}
            title={t('app.close')}
            aria-label={t('app.close')}
          >
            <X size={18} aria-hidden="true" />
          </button>
        </div>

        <div className="room-tools-body">
          <div className="room-tools-section">
            <div className="room-tools-source-toggle" aria-label={t('tools.source')}>
              <button
                type="button"
                className={`room-tools-source-btn ${sourceKind === 'display' ? 'active' : ''}`}
                onClick={() => setSourceKind('display')}
                disabled={busy || sharing}
                aria-pressed={sourceKind === 'display'}
              >
                {t('tools.sourceDisplay')}
              </button>
              <button
                type="button"
                className={`room-tools-source-btn ${sourceKind === 'window' ? 'active' : ''}`}
                onClick={() => setSourceKind('window')}
                disabled={busy || sharing}
                aria-pressed={sourceKind === 'window'}
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
                        aria-pressed={source.id === selectedSource?.id}
                      >
                        <span className="room-tools-source-preview" aria-hidden="true">
                          {source.previewDataUrl ? (
                            <img src={source.previewDataUrl} alt="" />
                          ) : source.iconDataUrl ? (
                            <span className="room-tools-source-icon"><img src={source.iconDataUrl} alt="" /></span>
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
                    return <option key={key} value={key}>{option.label}</option>;
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
                    <option key={option.fps} value={String(option.fps)}>{option.label}</option>
                  ))}
                </select>
              </label>
            </div>

            <button
              className="room-tools-primary"
              onClick={() => {
                if (!selectedSource) return;
                void onStartSharing({
                  sourceId: selectedSource.id,
                  sourceKind: selectedSource.kind,
                  sourceLabel: selectedSource.label,
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

          {error && <div className="room-tools-error" role="alert">{error}</div>}
        </div>
      </div>
    </div>
  );
}
