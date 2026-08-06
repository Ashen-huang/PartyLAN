import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

import {
  normalizeRoomVoiceState,
  nextMicrophoneEnabled,
  roomVoiceStatusKey,
} from '../src/media/roomVoice.ts';

const controlsSource = await readFile(new URL('../src/components/RoomVoiceControls.tsx', import.meta.url), 'utf8');
const controlsStyles = await readFile(new URL('../src/components/RoomVoiceControls.css', import.meta.url), 'utf8');
const dialogSource = await readFile(new URL('../src/components/RoomToolsDialog.tsx', import.meta.url), 'utf8');
const memberSource = await readFile(new URL('../src/components/MemberList.tsx', import.meta.url), 'utf8');
const roomControlSource = await readFile(new URL('../src/components/RoomControls.tsx', import.meta.url), 'utf8');
const hostSource = await readFile(new URL('../src/media/gamecastHost.ts', import.meta.url), 'utf8');
const appSource = await readFile(new URL('../src/App.tsx', import.meta.url), 'utf8');

test('room voice state keeps microphone and playback controls orthogonal', () => {
  const state = normalizeRoomVoiceState({
    connection: 'connected',
    microphone: 'unavailable',
    playbackMuted: true,
    participantCount: 3,
    errorCode: 'voice_microphone_unavailable',
  });

  assert.equal(state.connection, 'connected');
  assert.equal(state.microphone, 'unavailable');
  assert.equal(state.playbackMuted, true);
  assert.equal(state.participantCount, 3);
  assert.equal(nextMicrophoneEnabled(state), true);
  assert.equal(nextMicrophoneEnabled({ ...state, microphone: 'live' }), false);
  assert.equal(roomVoiceStatusKey(state), 'voice.statusConnected');
  assert.equal(roomVoiceStatusKey({ ...state, connection: 'full' }), 'voice.statusFull');
});

test('room voice controls use two accessible fixed-size Lucide icon buttons', () => {
  assert.match(controlsSource, /from 'lucide-react'/);
  assert.match(controlsSource, /MicOff/);
  assert.match(controlsSource, /VolumeX/);
  assert.match(controlsSource, /onMicrophoneChange/);
  assert.match(controlsSource, /onPlaybackMutedChange/);
  assert.ok((controlsSource.match(/aria-label=/g) || []).length >= 2);
  assert.match(controlsStyles, /inline-size:\s*44px/);
  assert.match(controlsStyles, /block-size:\s*44px/);
  assert.match(controlsStyles, /:focus-visible/);
});

test('room tools and member list no longer expose audio or legacy voice controls', () => {
  assert.doesNotMatch(dialogSource, /AudioPanel|RoomToolsTab|shareAudioMode|onToggleVoice|onVoiceVolumeChange/);
  assert.doesNotMatch(memberSource, /onOpenVoice|member-voice-btn|voiceLabel/);
  assert.doesNotMatch(roomControlSource, /audioMode|audioDefault|audioProcessName|audioProcessId/);
});

test('native sharing defaults to localized original quality at 60 FPS', () => {
  assert.match(dialogSource, /\{ label: '原始画质', width: 0, height: 0 \}/);
  assert.doesNotMatch(dialogSource, /label: 'Original'/);
  assert.match(dialogSource, /useState<string>\('original'\)/);
  assert.match(dialogSource, /useState<number>\(60\)/);
  assert.match(dialogSource, /width: selectedResolution\.width/);
  assert.match(dialogSource, /height: selectedResolution\.height/);
  assert.match(dialogSource, /fps: fpsValue/);
});

test('GameCast UI uses the isolated room voice API and web compatibility sharing is video-only', () => {
  assert.match(appSource, /GetRoomVoiceState/);
  assert.match(appSource, /SetRoomVoiceMicrophoneEnabled/);
  assert.match(appSource, /SetRoomVoicePlaybackMuted/);
  assert.match(appSource, /gamecast:voice-state/);
  assert.doesNotMatch(appSource, /StartVoiceChat|StopVoiceChat|SetVoiceMuted|SetVoiceVolume/);
  assert.match(hostSource, /audio:\s*false/);
});
