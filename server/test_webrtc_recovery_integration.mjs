import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const inject = fs.readFileSync(new URL('./screego/deploy/inject.js', import.meta.url), 'utf8');
const statsSource = fs.readFileSync(new URL('./webrtc_stats.js', import.meta.url), 'utf8');
const deployedStats = fs.readFileSync(new URL('./screego/deploy/webrtc-stats.js', import.meta.url), 'utf8');
const registerSource = fs.readFileSync(new URL('./register.py', import.meta.url), 'utf8');
const deployedRegister = fs.readFileSync(new URL('./screego/deploy/register.py', import.meta.url), 'utf8');
const nginx = fs.readFileSync(new URL('./screego/deploy/nginx-screego.conf', import.meta.url), 'utf8');

const diagnosticNumberFields = [
  'framesCaptured',
  'framesCapturedDelta',
  'capturedFps',
  'framesEncoded',
  'framesEncodedDelta',
  'encodedFps',
  'framesSent',
  'framesSentDelta',
  'sentFps',
  'keyFramesEncoded',
  'keyFramesEncodedDelta',
  'outboundPliCount',
  'outboundPliCountDelta',
  'outboundNackCount',
  'outboundNackCountDelta',
  'inboundPliCount',
  'inboundPliCountDelta',
  'inboundNackCount',
  'inboundNackCountDelta',
  'retransmittedPacketsSent',
  'retransmittedPacketsSentDelta',
  'retransmittedBytesSent',
  'retransmittedBytesSentDelta',
  'totalPacketSendDelayMs',
  'totalPacketSendDelayDeltaMs',
  'averagePacketSendDelayMs',
];

function pythonSetBody(source, name) {
  const match = source.match(new RegExp(`${name}\\s*=\\s*set\\(\\[([\\s\\S]*?)\\]\\)`));
  assert.ok(match, `${name} must remain a literal set list`);
  return match[1];
}

function loadReportStats(fetchImpl) {
  const start = inject.indexOf('function reportStats(data)');
  const end = inject.indexOf('function updateStats(', start);
  assert.ok(start >= 0 && end > start, 'reportStats must remain a bounded adapter');
  const source = inject.slice(start, end).trim();
  return Function('fetch', 'STATS_URL', `'use strict'; return (${source});`)(
    fetchImpl,
    '/api/stats',
  );
}

test('viewer quality decisions are routed to the authenticated peer recovery channel', () => {
  assert.match(inject, /function requestPeerIceRecovery\s*\(/);
  assert.match(inject, /qualityState\.action[\s\S]{0,500}requestPeerIceRecovery\s*\(/);
  assert.match(inject, /__gamecastRequestIceRecovery/);
  assert.match(inject, /__gamecastHandleIceRecoveryRequest/);
});

test('host protection profile is capped at 720p and 3 Mbps before recovery', () => {
  assert.match(inject, /protectionMaxWidth:\s*pc\._gcProtectionMaxWidth/);
  assert.match(inject, /protectionMaxHeight:\s*pc\._gcProtectionMaxHeight/);
  assert.match(inject, /protectionMaxBitrate:\s*pc\._gcProtectionMaxBitrate/);
  assert.match(inject, /_gcProtectionMaxWidth\s*=\s*1280/);
  assert.match(inject, /_gcProtectionMaxHeight\s*=\s*720/);
  assert.match(inject, /_gcProtectionMaxBitrate\s*=\s*3000000/);
});

test('telemetry distinguishes estimated relay load from server capacity', () => {
  assert.match(inject, /estimatedRelayLoadKbps:\s*Math\.round\(estimatedRelayLoadKbps\)/);
  assert.doesNotMatch(inject, /estimatedCapacityKbps:\s*Math\.round\(serverLoadKbps\)/);
  assert.match(deployedRegister, /'estimatedRelayLoadKbps'/);
  assert.match(deployedRegister, /'protectionMaxBitrate'/);
});

test('diagnostic telemetry is forwarded through the versioned async reporter', () => {
  const copyList = inject.match(
    /if \(diagnosticSample\) \{\s*\[([\s\S]*?)\]\.forEach\(function \(key\)/,
  );
  assert.ok(copyList, 'diagnostic sample forwarding list must remain explicit');
  for (const field of diagnosticNumberFields) {
    assert.match(copyList[1], new RegExp(`['"]${field}['"]`), `${field} must be forwarded`);
  }
  assert.doesNotMatch(copyList[1], /diagnosticPacketsSentDelta/);
  assert.doesNotMatch(copyList[1], /telemetryResetReason/);

  assert.match(statsSource, /sendTelemetryReport:\s*sendTelemetryReport/);
  assert.match(
    inject,
    /pc\._gcTelemetryReasonLatch\s*=\s*GameCastWebRTCStats\.createTelemetryReasonLatch\(\)/,
  );
  assert.match(
    inject,
    /GameCastWebRTCStats\.recordTelemetryResetReasons\(\s*pc\._gcTelemetryReasonLatch\s*,\s*diagnosticSample\.telemetryResetReason\s*\)/,
  );
  assert.match(
    inject,
    /GameCastWebRTCStats\.sendTelemetryReport\(\s*pc\._gcTelemetryReasonLatch\s*,\s*reportPayload\s*,\s*reportStats\s*\)/,
  );
});

test('reportStats resolves fetch response.ok and normalizes failures', async () => {
  const start = inject.indexOf('function reportStats(data)');
  const end = inject.indexOf('function updateStats(', start);
  assert.ok(start >= 0 && end > start, 'reportStats must remain directly testable as a bounded adapter');
  const reportStats = inject.slice(start, end);
  assert.match(reportStats, /response\.ok/);
  assert.match(reportStats, /return\s+false/);
  assert.match(reportStats, /return\s+(?:Promise\.)?resolve\(false\)/);

  let posted;
  const successfulReporter = loadReportStats(async (url, options) => {
    posted = {url, options};
    return {ok: true};
  });
  assert.equal(await successfulReporter({event: 'webrtc_sample'}), true);
  assert.equal(posted.url, '/api/stats');
  assert.equal(posted.options.method, 'POST');
  assert.deepEqual(JSON.parse(posted.options.body), {event: 'webrtc_sample'});

  const non2xxReporter = loadReportStats(async () => ({ok: false}));
  assert.equal(await non2xxReporter({}), false);
  const rejectedReporter = loadReportStats(() => Promise.reject(new Error('offline')));
  assert.equal(await rejectedReporter({}), false);
  const throwingReporter = loadReportStats(() => {
    throw new Error('sync failure');
  });
  assert.equal(await throwingReporter({}), false);
});

test('source and deployed telemetry copies stay byte-identical with complete whitelists', () => {
  assert.equal(deployedStats, statsSource);
  assert.equal(deployedRegister, registerSource);

  const stringFields = pythonSetBody(deployedRegister, 'STATS_STRING_FIELDS');
  const numberFields = pythonSetBody(deployedRegister, 'STATS_NUMBER_FIELDS');
  assert.match(stringFields, /['"]telemetryResetReason['"]/);
  assert.match(stringFields, /['"]adaptiveStatus['"]/);
  for (const field of diagnosticNumberFields) {
    assert.match(numberFields, new RegExp(`['"]${field}['"]`), `${field} must be persisted`);
  }
  assert.match(numberFields, /['"]adaptiveNominalBitrate['"]/);
  assert.match(numberFields, /['"]adaptiveDegradeFloorBitrate['"]/);
  assert.match(numberFields, /['"]adaptiveConfiguredFps['"]/);
  assert.doesNotMatch(numberFields, /diagnosticPacketsSentDelta/);
});

test('deployed telemetry resource versions and load order are exact', () => {
  const subFilter = nginx.match(/sub_filter '<\/head>' '([^']+)<\/head>';/);
  assert.ok(subFilter, 'script injection sub_filter must exist');
  const scripts = Array.from(subFilter[1].matchAll(/<script src="\/([^"]+)"><\/script>/g), (match) => match[1]);
  assert.deepEqual(scripts, [
    'webrtc-stats.js?v=5',
    'webrtc-quality.js?v=2',
    'webrtc-adaptive.js?v=9',
    'web-share-audio.js?v=2',
    'inject.js?v=6.38',
    'sfu-client.js?v=1.9',
  ]);
});
