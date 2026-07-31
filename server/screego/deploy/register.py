#!/usr/bin/env python3
import http.server
import hashlib
import json
import os
import sys
import re
import stat
import tempfile
import threading
import time
from collections import deque
from datetime import datetime
from urllib.parse import urlparse, parse_qs
import urllib.request
import urllib.error

import bcrypt

from gamecast_control import ControlError, ControlPlane, issue_voice_token

HTPASSWD_FILE = os.environ.get('SCREEGO_USERS_FILE_HOST', '/etc/gamecast-sfu/users.htpasswd')
HISTORY_DIR = '/root/screego/history'
STATS_DIR = '/root/screego/stats'
LISTEN_PORT = 5052
ROOM_STATE_TTL = 15
VOICE_TOKEN_TTL = 15
GAMECAST_VOICE_SECRET = os.environ.get('GAMECAST_VOICE_SECRET', '').strip()
MAX_POST_BODY = 256 * 1024
USERNAME_RE = re.compile(r'^[a-zA-Z0-9_\-]{2,20}$')
BACKEND_CONFIG_URL = 'http://127.0.0.1:5060/config'
SFU_SESSIONS_URL = os.environ.get('GAMECAST_SFU_SESSIONS_URL', 'http://127.0.0.1:5053/sessions')
SFU_SESSIONS_TIMEOUT = float(os.environ.get('GAMECAST_SFU_SESSIONS_TIMEOUT', '1.0'))
QUALITY_ACTIVE_TTL = 8.0
QUALITY_RETENTION_TTL = 15.0
try:
    SAFE_VIDEO_EGRESS_KBPS = max(0, int(os.environ.get('GAMECAST_SAFE_VIDEO_EGRESS_KBPS', '0') or 0))
except (TypeError, ValueError):
    SAFE_VIDEO_EGRESS_KBPS = 0
ROOM_STATE_AUDIO_MODES = set(['app', 'off', 'system'])
STATS_STRING_FIELDS = set([
    'event', 'policyVersion', 'user', 'room', 'peer', 'mode', 'conn', 'path',
    'transport', 'localCandidateType', 'remoteCandidateType',
    'localAddressFamily', 'remoteAddressFamily', 'qualityLimitationReason',
    'qualityStatus', 'shadowAction', 'invalidReason', 'telemetryResetReason',
    'resolution', 'fps',
    'codec', 'kbps', 'loss', 'jitter', 'pcState', 'targetKbps', 'adaptive',
    'adaptivePolicyVersion', 'adaptiveDecision', 'serverLoadKbps', 'serverNote',
    'participantId', 'pairId', 'receiverState',
])
STATS_NUMBER_FIELDS = set([
    'generation', 'rtt', 'rttMs', 'jitterMs', 'lossPercent', 'upKbps',
    'downKbps', 'availableOutgoingBitrate', 'targetBitrate',
    'packetsReceivedDelta', 'packetsLostDelta', 'packetsSentDelta',
    'framesDecodedDelta', 'framesDroppedDelta', 'framesEncodedDelta',
    'framesCaptured', 'framesCapturedDelta', 'capturedFps',
    'framesEncoded', 'encodedFps', 'framesSent', 'framesSentDelta', 'sentFps',
    'keyFramesEncoded', 'keyFramesEncodedDelta',
    'outboundPliCount', 'outboundPliCountDelta',
    'outboundNackCount', 'outboundNackCountDelta',
    'inboundPliCount', 'inboundPliCountDelta',
    'inboundNackCount', 'inboundNackCountDelta',
    'retransmittedPacketsSent', 'retransmittedPacketsSentDelta',
    'retransmittedBytesSent', 'retransmittedBytesSentDelta',
    'totalPacketSendDelayMs', 'totalPacketSendDelayDeltaMs',
    'averagePacketSendDelayMs',
    'badCount', 'windowSize', 'pcCount', 'estimatedCapacityKbps',
    'estimatedRelayLoadKbps', 'protectionMaxBitrate',
    'adaptiveLevel', 'adaptiveScale', 'adaptiveMaxBitrate',
    'decodedFps', 'incomingBitrateKbps', 'packetsLost', 'framesDropped',
])
STATS_BOOL_FIELDS = set(['valid', 'visible'])

control_plane = ControlPlane(ttl=ROOM_STATE_TTL)

os.makedirs(HISTORY_DIR, exist_ok=True)
os.makedirs(STATS_DIR, exist_ok=True)

stats_lock = threading.Lock()
room_state_lock = threading.Lock()
viewer_intent_lock = threading.Lock()
rate_limit_lock = threading.Lock()
user_store_lock = threading.Lock()
quality_lock = threading.Lock()
room_states = {}
viewer_intents = {}
rate_buckets = {}
quality_samples = {}
quality_clock = time.time

REGISTER_CREATED = 'created'
REGISTER_EXISTS = 'exists'
REGISTER_FAILED = 'failed'


def log(msg):
    sys.stdout.write("[service] %s\n" % msg)
    sys.stdout.flush()


def is_valid_username(username):
    return bool(USERNAME_RE.match(username or ''))


def _credential_content_has_user(content, username):
    username_prefix = username.encode('utf-8') + b':'
    return any(line.startswith(username_prefix) for line in content.splitlines())


def _fsync_directory(directory):
    try:
        directory_fd = os.open(directory, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(directory_fd)
    except OSError:
        pass
    finally:
        os.close(directory_fd)


def create_user(username, password):
    temp_path = None
    with user_store_lock:
        try:
            if os.path.exists(HTPASSWD_FILE):
                with open(HTPASSWD_FILE, 'rb') as users_file:
                    content = users_file.read()
                file_mode = stat.S_IMODE(os.stat(HTPASSWD_FILE).st_mode)
            else:
                content = b''
                file_mode = 0o600

            if _credential_content_has_user(content, username):
                return REGISTER_EXISTS

            password_hash = bcrypt.hashpw(password.encode('utf-8'), bcrypt.gensalt(rounds=12))
            separator = b'' if not content or content.endswith(b'\n') else b'\n'
            updated_content = content + separator + username.encode('utf-8') + b':' + password_hash + b'\n'

            directory = os.path.dirname(os.path.abspath(HTPASSWD_FILE))
            file_descriptor, temp_path = tempfile.mkstemp(
                dir=directory,
                prefix='.%s.' % os.path.basename(HTPASSWD_FILE),
                suffix='.tmp',
            )
            try:
                if hasattr(os, 'fchmod'):
                    os.fchmod(file_descriptor, file_mode)
                with os.fdopen(file_descriptor, 'wb') as temp_file:
                    file_descriptor = None
                    temp_file.write(updated_content)
                    temp_file.flush()
                    os.fsync(temp_file.fileno())
            finally:
                if file_descriptor is not None:
                    os.close(file_descriptor)

            if not hasattr(os, 'fchmod'):
                os.chmod(temp_path, file_mode)
            os.replace(temp_path, HTPASSWD_FILE)
            temp_path = None
            _fsync_directory(directory)
            return REGISTER_CREATED
        except Exception as error:
            log('注册存储失败: %s' % type(error).__name__)
            return REGISTER_FAILED
        finally:
            if temp_path:
                try:
                    os.unlink(temp_path)
                except OSError:
                    pass


def load_history(username):
    if not is_valid_username(username):
        return []
    path = os.path.join(HISTORY_DIR, username + '.json')
    if not os.path.exists(path):
        return []
    try:
        with open(path, 'r', encoding='utf-8') as f:
            data = json.load(f)
            return data if isinstance(data, list) else []
    except:
        return []


def save_history(username, history):
    if not is_valid_username(username):
        return False
    path = os.path.join(HISTORY_DIR, username + '.json')
    with open(path, 'w', encoding='utf-8') as f:
        json.dump(history[:50], f, ensure_ascii=False)
    return True


def get_stats_path():
    today = datetime.now().strftime('%Y-%m-%d')
    return os.path.join(STATS_DIR, 'stats_%s.jsonl' % today)


def sanitize_stats_payload(data):
    if not isinstance(data, dict):
        return None
    sanitized = {}
    for key in STATS_STRING_FIELDS:
        value = data.get(key)
        if value is None:
            continue
        sanitized[key] = str(value)[:160]
    for key in STATS_NUMBER_FIELDS:
        value = data.get(key)
        if isinstance(value, bool):
            continue
        if isinstance(value, (int, float)):
            sanitized[key] = value
    for key in STATS_BOOL_FIELDS:
        value = data.get(key)
        if isinstance(value, bool):
            sanitized[key] = value
    return sanitized


def append_stats(data):
    if not isinstance(data, dict):
        return False
    sanitized = sanitize_stats_payload(data)
    if sanitized is None:
        return False
    sanitized['_ts'] = datetime.now().isoformat()
    line = json.dumps(sanitized, ensure_ascii=False) + '\n'
    with stats_lock:
        with open(get_stats_path(), 'a', encoding='utf-8') as f:
            f.write(line)
    return True


def normalize_quality_sample(data):
    if not isinstance(data, dict):
        raise ControlError('quality_sample_invalid', 400)

    def bounded_text(name, max_length):
        value = str(data.get(name) or '').strip()
        if not value or len(value) > max_length or any(ch in value for ch in '\r\n\t'):
            raise ControlError('quality_sample_invalid', 400)
        return value

    def bounded_number(name, minimum, maximum, integer=False):
        value = data.get(name)
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise ControlError('quality_sample_invalid', 400)
        value = max(minimum, min(maximum, value))
        return int(value) if integer else float(value)

    return {
        'room': bounded_text('room', 120),
        'participantId': bounded_text('participantId', 128),
        'generation': bounded_text('generation', 80),
        'pairId': bounded_text('pairId', 120),
        'packetsReceivedDelta': bounded_number('packetsReceivedDelta', 0, 10000000, True),
        'packetsLostDelta': bounded_number('packetsLostDelta', 0, 10000000, True),
        'framesDroppedDelta': bounded_number('framesDroppedDelta', 0, 1000000, True),
        'decodedFps': bounded_number('decodedFps', 0, 240),
        'incomingBitrateKbps': bounded_number('incomingBitrateKbps', 0, 1000000),
        'jitterMs': bounded_number('jitterMs', 0, 60000),
    }


def store_quality_sample(data, username):
    sample = normalize_quality_sample(data)
    control_plane.authorize_quality_report(sample['room'], sample['participantId'], username)
    now = quality_clock()
    sample['reportedAtUnix'] = now
    with quality_lock:
        room = quality_samples.setdefault(sample['room'], {})
        room[sample['participantId']] = sample
        prune_quality_samples_locked(now)
    return sample


def prune_quality_samples_locked(now):
    for room_id, samples in list(quality_samples.items()):
        for participant_id, sample in list(samples.items()):
            if now - sample.get('reportedAtUnix', 0) > QUALITY_RETENTION_TTL:
                samples.pop(participant_id, None)
        if not samples:
            quality_samples.pop(room_id, None)


def get_quality_snapshot(room_id, participant_id, username):
    _, active_ids = control_plane.authorize_quality_snapshot(room_id, participant_id, username)
    now = quality_clock()
    with quality_lock:
        prune_quality_samples_locked(now)
        samples = []
        for sample in (quality_samples.get(room_id) or {}).values():
            age = now - sample.get('reportedAtUnix', 0)
            if age > QUALITY_ACTIVE_TTL or sample.get('participantId') not in active_ids:
                continue
            item = dict(sample)
            item['ageSeconds'] = max(0.0, age)
            samples.append(item)
    samples.sort(key=lambda item: item['participantId'])
    return {
        'room': room_id,
        'count': len(samples),
        'safeVideoEgressKbps': SAFE_VIDEO_EGRESS_KBPS,
        'samples': samples,
    }


def read_stats(lines=200):
    path = get_stats_path()
    if not os.path.exists(path):
        return []
    try:
        with open(path, 'r') as f:
            all_lines = f.readlines()
        result = []
        for line in all_lines[-lines:]:
            line = line.strip()
            if line:
                try:
                    result.append(json.loads(line))
                except:
                    pass
        return result
    except:
        return []


def normalize_room_id(room_id):
    room_id = (room_id or '').strip()
    if not room_id or len(room_id) > 120:
        return ''
    if any(ch in room_id for ch in '\r\n\t'):
        return ''
    return room_id


def clamp_room_ttl(ttl):
    try:
        ttl = int(ttl)
    except (TypeError, ValueError):
        ttl = ROOM_STATE_TTL
    return max(5, min(ttl, 60))


def normalize_room_audio_mode(value):
    value = (value or '').strip()
    return value if value in ROOM_STATE_AUDIO_MODES else None


def parse_int(value, default=None):
    try:
        return int(value)
    except (TypeError, ValueError):
        return default



def parse_bool(value):
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return value != 0
    if value is None:
        return False
    return str(value).strip().lower() in ('1', 'true', 'yes', 'on')

def check_rate_limit(key, limit, window_seconds):
    now = time.time()
    with rate_limit_lock:
        bucket = rate_buckets.get(key)
        if bucket is None:
            bucket = deque()
            rate_buckets[key] = bucket
        while bucket and now - bucket[0] > window_seconds:
            bucket.popleft()
        if len(bucket) >= limit:
            retry_after = max(1, int(window_seconds - (now - bucket[0])))
            return False, retry_after
        bucket.append(now)
    return True, 0


def resolve_authenticated_user(headers):
    cookie = (headers.get('Cookie') or '').strip()
    if not cookie:
        return None
    req = urllib.request.Request(BACKEND_CONFIG_URL, headers={
        'Cookie': cookie,
        'Accept': 'application/json',
    }, method='GET')
    try:
        with urllib.request.urlopen(req, timeout=3) as resp:
            data = json.loads(resp.read())
    except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError, ValueError, json.JSONDecodeError):
        return None
    user = (data.get('user') or '').strip()
    if not data.get('loggedIn') or not is_valid_username(user):
        return None
    return user


def build_room_state_session_fingerprint(headers):
    cookie = (headers.get('Cookie') or '').strip()
    if not cookie:
        return None
    user_agent = (headers.get('User-Agent') or '').strip()
    raw = '%s\n%s' % (cookie, user_agent)
    return hashlib.sha256(raw.encode('utf-8')).hexdigest()


def prune_room_states(now_ts=None):
    now_ts = now_ts or time.time()
    with room_state_lock:
        expired = [
            room_id for room_id, state in room_states.items()
            if (not state.get('active')) or (now_ts - state.get('updated_at', 0) > state.get('ttl', ROOM_STATE_TTL))
        ]
        for room_id in expired:
            room_states.pop(room_id, None)


def build_room_state(room_id, state=None):
    if not state:
        return {
            'room': room_id,
            'active': False,
            'sharing': False,
            'sfu': False,
            'audioMode': None,
            'user': None,
            'ttl': ROOM_STATE_TTL,
            'ageSeconds': None,
        }

    age = max(0.0, time.time() - state.get('updated_at', time.time()))
    return {
        'room': room_id,
        'active': bool(state.get('active')),
        'sharing': bool(state.get('sharing')),
        'sfu': bool(state.get('sfu')),
        'audioMode': state.get('audioMode'),
        'user': state.get('user'),
        'ttl': state.get('ttl', ROOM_STATE_TTL),
        'ageSeconds': round(age, 2),
    }


def prune_viewer_intents(now_ts=None):
    now_ts = now_ts or time.time()
    with viewer_intent_lock:
        expired_rooms = []
        for room_id, clients in list(viewer_intents.items()):
            for client_id, intent in list(clients.items()):
                if (not intent.get('active')) or (now_ts - intent.get('updated_at', 0) > intent.get('ttl', ROOM_STATE_TTL)):
                    clients.pop(client_id, None)
            if not clients:
                expired_rooms.append(room_id)
        for room_id in expired_rooms:
            viewer_intents.pop(room_id, None)


def build_viewer_intent(room_id, clients=None):
    room_id = normalize_room_id(room_id)
    if not room_id:
        return None
    now_ts = time.time()
    active_clients = []
    if clients:
        for client_id, intent in clients.items():
            if not intent or not intent.get('active'):
                continue
            age = max(0.0, now_ts - intent.get('updated_at', now_ts))
            active_clients.append({
                'id': client_id,
                'client': intent.get('client'),
                'user': intent.get('user'),
                'wantsSfu': bool(intent.get('wantsSfu')),
                'ageSeconds': round(age, 2),
            })
    return {
        'room': room_id,
        'active': len(active_clients) > 0,
        'wantsSfu': any(client.get('wantsSfu') for client in active_clients),
        'viewerCount': len(active_clients),
        'clients': active_clients,
        'ttl': ROOM_STATE_TTL,
    }


def get_viewer_intent(room_id):
    room_id = normalize_room_id(room_id)
    if not room_id:
        return None
    prune_viewer_intents()
    with viewer_intent_lock:
        clients = dict(viewer_intents.get(room_id) or {})
    return build_viewer_intent(room_id, clients)


def build_viewer_intent_fingerprint(headers, client_ip, data):
    session_fingerprint = build_room_state_session_fingerprint(headers)
    if session_fingerprint:
        return session_fingerprint
    user_agent = (headers.get('User-Agent') or '').strip()
    raw = "\n".join([
        client_ip or '',
        user_agent,
        (data.get('client') or '').strip(),
        (data.get('user') or '').strip(),
    ])
    return hashlib.sha256(raw.encode('utf-8')).hexdigest()


def set_viewer_intent(data, username, client_fingerprint):
    room_id = normalize_room_id(data.get('room'))
    if not room_id:
        return False, 'invalid room', 400
    if not client_fingerprint:
        return False, 'missing client fingerprint', 400

    active = bool(data.get('active'))
    ttl = clamp_room_ttl(data.get('ttl'))
    client_name = ((data.get('client') or 'partylan') + '').strip()[:40] or 'partylan'
    user = username or ((data.get('user') or '') + '').strip()[:40] or 'viewer'
    wants_sfu = bool(data.get('wantsSfu')) if active else False
    prune_viewer_intents()

    with viewer_intent_lock:
        clients = viewer_intents.get(room_id)
        if not active:
            if clients:
                clients.pop(client_fingerprint, None)
                if not clients:
                    viewer_intents.pop(room_id, None)
                    snapshot = {}
                else:
                    snapshot = dict(clients)
            else:
                snapshot = {}
            return True, build_viewer_intent(room_id, snapshot), 200
        if clients is None:
            clients = {}
            viewer_intents[room_id] = clients
        clients[client_fingerprint] = {
            'active': True,
            'wantsSfu': wants_sfu,
            'client': client_name,
            'user': user,
            'ttl': ttl,
            'updated_at': time.time(),
        }
        snapshot = dict(clients)
    return True, build_viewer_intent(room_id, snapshot), 200


def normalize_sfu_room_state(item):
    if not isinstance(item, dict):
        return None
    room_id = normalize_room_id(item.get('room') or item.get('id'))
    if not room_id:
        return None
    track_count = parse_int(item.get('trackCount'), 0) or 0
    viewer_count = parse_int(item.get('viewerCount'), 0) or 0
    sharing = parse_bool(item.get('sharing')) or track_count > 0
    host_connected = parse_bool(item.get('hostConnected')) or sharing
    active = parse_bool(item.get('active')) or host_connected or sharing or viewer_count > 0
    if not active:
        return None
    user = (item.get('user') or item.get('host') or '').strip()
    return {
        'room': room_id,
        'active': True,
        'sharing': bool(sharing),
        'sfu': True,
        'audioMode': normalize_room_audio_mode(item.get('audioMode')),
        'user': user or None,
        'ttl': ROOM_STATE_TTL,
        'ageSeconds': 0,
        'viewerCount': viewer_count,
        'hostConnected': bool(host_connected),
        'trackCount': track_count,
        'updatedAt': item.get('updatedAt') or item.get('firstTrackAt'),
        'source': 'sfu',
    }


def fetch_sfu_room_states():
    req = urllib.request.Request(SFU_SESSIONS_URL, headers={'Accept': 'application/json'}, method='GET')
    try:
        with urllib.request.urlopen(req, timeout=SFU_SESSIONS_TIMEOUT) as resp:
            data = json.loads(resp.read())
    except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError, ValueError, json.JSONDecodeError):
        return []
    rooms = data.get('rooms') or data.get('sessions') or []
    if not isinstance(rooms, list):
        return []
    states = []
    for item in rooms:
        state = normalize_sfu_room_state(item)
        if state:
            states.append(state)
    return states


def merge_room_state(base, live):
    if not live:
        return base
    if not base:
        return live
    merged = dict(base)
    merged['active'] = bool(base.get('active')) or bool(live.get('active'))
    merged['sharing'] = bool(base.get('sharing')) or bool(live.get('sharing'))
    merged['sfu'] = bool(base.get('sfu')) or bool(live.get('sfu'))
    if not merged.get('audioMode') and live.get('audioMode'):
        merged['audioMode'] = live.get('audioMode')
    if not merged.get('user') and live.get('user'):
        merged['user'] = live.get('user')
    for key in ('viewerCount', 'hostConnected', 'trackCount', 'updatedAt', 'source'):
        if key in live and live.get(key) is not None:
            merged[key] = live.get(key)
    if live.get('active'):
        merged['ageSeconds'] = 0
    return merged


def merge_room_state_lists(local_states, live_states):
    merged = {}
    for state in local_states:
        room_id = normalize_room_id(state.get('room'))
        if room_id and state.get('active'):
            merged[room_id] = state
    for state in live_states:
        room_id = normalize_room_id(state.get('room'))
        if room_id and state.get('active'):
            merged[room_id] = merge_room_state(merged.get(room_id), state)
    snapshot = [state for state in merged.values() if state and state.get('active')]
    snapshot.sort(key=lambda item: (bool(item.get('sharing')), -float(item.get('ageSeconds') or 0)), reverse=True)
    return snapshot


def get_sfu_room_state(room_id):
    room_id = normalize_room_id(room_id)
    if not room_id:
        return None
    for state in fetch_sfu_room_states():
        if state.get('room') == room_id:
            return state
    return None


def get_room_state(room_id):
    room_id = normalize_room_id(room_id)
    if not room_id:
        return None
    local_state = control_plane.get_room(room_id)
    return merge_room_state(local_state, get_sfu_room_state(room_id))


def list_room_states():
    snapshot = control_plane.list_rooms()
    return merge_room_state_lists(snapshot, fetch_sfu_room_states())


def can_write_room_state(existing_state, username, session_fingerprint, incoming_active, incoming_sharing, incoming_sfu):
    if not existing_state or not existing_state.get('active'):
        return True, None
    if existing_state.get('owner_user') == username and existing_state.get('owner_session') == session_fingerprint:
        return True, None
    existing_idle = not existing_state.get('sharing') and not existing_state.get('sfu')
    incoming_live = bool(incoming_active) and (bool(incoming_sharing) or bool(incoming_sfu))
    if existing_idle and incoming_live:
        return True, None
    if existing_state.get('owner_user') != username:
        return False, '???????????????'
    if existing_state.get('owner_session') != session_fingerprint:
        return False, '?????????????????'
    return True, None


def set_room_state(data, username, session_fingerprint):
    try:
        state = control_plane.set_legacy_room_state(data, username, session_fingerprint)
        return True, state, 200
    except ControlError as error:
        return False, error.code, error.status


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        log(fmt % args)

    def cors_origin(self):
        origin = (self.headers.get('Origin') or '').strip()
        if not origin:
            return None
        return origin if self.is_same_site_url(origin) else None

    def request_host(self):
        host = (self.headers.get('Host') or '').strip()
        return host.split(':', 1)[0].lower()

    def is_same_site_url(self, value):
        if not value:
            return True
        try:
            parsed = urlparse(value)
        except Exception:
            return False
        if not parsed.scheme or not parsed.hostname:
            return False
        return parsed.hostname.lower() == self.request_host()

    def enforce_same_site_source(self):
        origin = (self.headers.get('Origin') or '').strip()
        referer = (self.headers.get('Referer') or '').strip()
        fetch_site = (self.headers.get('Sec-Fetch-Site') or '').strip().lower()
        if origin and not self.is_same_site_url(origin):
            self.send_json(403, {'ok': False, 'error': '非法来源'})
            return False
        if referer and not self.is_same_site_url(referer):
            self.send_json(403, {'ok': False, 'error': '非法来源'})
            return False
        if fetch_site and fetch_site not in ('same-origin', 'same-site', 'none'):
            self.send_json(403, {'ok': False, 'error': '非法来源'})
            return False
        return True

    def client_ip(self):
        forwarded = (self.headers.get('X-Real-IP') or '').strip()
        if forwarded:
            return forwarded
        return self.client_address[0] if self.client_address else 'unknown'

    def enforce_rate_limit(self, bucket_name, limit, window_seconds):
        allowed, retry_after = check_rate_limit(
            '%s:%s' % (bucket_name, self.client_ip()),
            limit,
            window_seconds
        )
        if allowed:
            return True
        self.send_json(429, {'ok': False, 'error': '请求过于频繁，请稍后再试'}, extra_headers={
            'Retry-After': str(retry_after)
        })
        return False

    def require_authenticated_user(self):
        user = resolve_authenticated_user(self.headers)
        if not user:
            self.send_json(401, {'ok': False, 'error': '请先登录'})
            return None
        return user

    def require_room_state_writer(self):
        user = self.require_authenticated_user()
        if not user:
            return None, None
        session_fingerprint = build_room_state_session_fingerprint(self.headers)
        if not session_fingerprint:
            self.send_json(401, {'ok': False, 'error': '无法确认当前登录会话'})
            return None, None
        return user, session_fingerprint

    def do_GET(self):
        try:
            parsed = urlparse(self.path)
            if parsed.path == '/api/history':
                if not self.enforce_rate_limit('history:get', 120, 60):
                    return
                username = self.require_authenticated_user()
                if not username:
                    return
                history = load_history(username)
                self.send_json(200, {'ok': True, 'history': history})
            elif parsed.path == '/api/stats':
                if not self.enforce_rate_limit('stats:get', 120, 60):
                    return
                username = self.require_authenticated_user()
                if not username:
                    return
                params = parse_qs(parsed.query)
                n = parse_int(params.get('n', ['200'])[0], None)
                if n is None:
                    self.send_json(400, {'ok': False, 'error': 'n 参数必须是整数'})
                    return
                stats = read_stats(min(max(n, 1), 1000))
                self.send_json(200, {'ok': True, 'count': len(stats), 'stats': stats})
            elif parsed.path == '/api/rooms':
                if not self.enforce_rate_limit('rooms:get', 240, 60):
                    return
                rooms = list_room_states()
                self.send_json(200, {'ok': True, 'count': len(rooms), 'rooms': rooms})
            elif parsed.path == '/api/room-state':
                if not self.enforce_rate_limit('room-state:get', 240, 60):
                    return
                params = parse_qs(parsed.query)
                room_id = (params.get('room', [''])[0]).strip()
                state = get_room_state(room_id)
                if not state:
                    self.send_json(400, {'ok': False, 'error': '缺少 room 参数'})
                    return
                self.send_json(200, {'ok': True, 'state': state})
            elif parsed.path == '/api/viewer-intent':
                if not self.enforce_rate_limit('viewer-intent:get', 240, 60):
                    return
                params = parse_qs(parsed.query)
                room_id = (params.get('room', [''])[0]).strip()
                intent = get_viewer_intent(room_id)
                if not intent:
                    self.send_json(400, {'ok': False, 'error': '?? room ??'})
                    return
                self.send_json(200, {'ok': True, 'intent': intent})
            elif parsed.path == '/api/presence':
                if not self.enforce_rate_limit('presence:get', 240, 60):
                    return
                params = parse_qs(parsed.query)
                room_id = (params.get('room', [''])[0]).strip()
                try:
                    participants = control_plane.list_presence(room_id)
                    room = get_room_state(room_id)
                except ControlError as error:
                    self.send_control_error(error)
                    return
                self.send_json(200, {
                    'ok': True,
                    'count': len(participants),
                    'participants': participants,
                    'room': room,
                })
            elif parsed.path == '/api/quality/snapshot':
                if not self.enforce_rate_limit('quality:snapshot', 120, 60):
                    return
                username = self.require_authenticated_user()
                if not username:
                    return
                params = parse_qs(parsed.query)
                room_id = (params.get('room', [''])[0]).strip()
                participant_id = (params.get('participantId', [''])[0]).strip()
                try:
                    snapshot = get_quality_snapshot(room_id, participant_id, username)
                except ControlError as error:
                    self.send_control_error(error)
                    return
                self.send_json(200, dict({'ok': True}, **snapshot))
            else:
                self.send_json(404, {'ok': False, 'error': '未知路径'})
        except Exception as e:
            log('GET 处理异常: %s' % e)
            try:
                self.send_json(500, {'ok': False, 'error': '服务内部错误'})
            except BrokenPipeError:
                return

    def do_POST(self):
        try:
            if not self.enforce_same_site_source():
                return
            length = parse_int(self.headers.get('Content-Length', 0), 0)
            if length < 0:
                self.send_json(400, {'ok': False, 'error': '非法 Content-Length'})
                return
            if length > MAX_POST_BODY:
                self.send_json(413, {'ok': False, 'error': '请求体过大'})
                return
            body = self.rfile.read(length) if length > 0 else b''

            try:
                data = json.loads(body)
            except (json.JSONDecodeError, ValueError):
                self.send_json(400, {'ok': False, 'error': '无效的 JSON'})
                return

            request_path = urlparse(self.path).path
            if request_path == '/api/register':
                if not self.enforce_rate_limit('register:post', 8, 900):
                    return
                self.handle_register(data)
            elif request_path == '/api/history':
                if not self.enforce_rate_limit('history:post', 120, 60):
                    return
                self.handle_save_history(data)
            elif request_path == '/api/stats':
                if not self.enforce_rate_limit('stats:post', 240, 60):
                    return
                self.handle_stats(data)
            elif request_path == '/api/quality/report':
                if not self.enforce_rate_limit('quality:report', 120, 60):
                    return
                self.handle_quality_report(data)
            elif request_path == '/api/room-state':
                if not self.enforce_rate_limit('room-state:post', 240, 60):
                    return
                self.handle_room_state(data)
            elif request_path == '/api/viewer-intent':
                if not self.enforce_rate_limit('viewer-intent:post', 240, 60):
                    return
                self.handle_viewer_intent(data)
            elif request_path in (
                '/api/presence/join',
                '/api/presence/heartbeat',
                '/api/presence/leave',
            ):
                if not self.enforce_rate_limit('presence:post', 360, 60):
                    return
                self.handle_presence(request_path, data)
            elif request_path == '/api/voice/token':
                if not self.enforce_rate_limit('voice-token:post', 360, 60):
                    return
                self.handle_voice_token(data)
            elif request_path in (
                '/api/share/claim',
                '/api/share/heartbeat',
                '/api/share/release',
            ):
                if not self.enforce_rate_limit('share:post', 360, 60):
                    return
                self.handle_share(request_path, data)
            else:
                self.send_json(404, {'ok': False, 'error': '未知路径'})
        except Exception as e:
            log('POST 处理异常: %s' % e)
            try:
                self.send_json(500, {'ok': False, 'error': '服务内部错误'})
            except BrokenPipeError:
                return

    def handle_register(self, data):
        username = (data.get('user') or '').strip()
        password = data.get('pass') or ''

        if not username or not password:
            self.send_json(400, {'ok': False, 'error': '用户名和密码不能为空'})
            return

        if not is_valid_username(username):
            self.send_json(400, {'ok': False, 'error': '用户名只能包含字母、数字、下划线、横线，2-20字符'})
            return

        if len(password) < 4:
            self.send_json(400, {'ok': False, 'error': '密码至少4个字符'})
            return

        result = create_user(username, password)
        if result == REGISTER_EXISTS:
            self.send_json(409, {'ok': False, 'error': '用户名已存在'})
            return

        if result == REGISTER_CREATED:
            log("新用户注册: %s" % username)
            self.send_json(200, {
                'ok': True,
                'message': '账号已创建，现在可以直接登录。'
            })
        else:
            self.send_json(500, {'ok': False, 'error': '注册失败'})

    def handle_save_history(self, data):
        username = self.require_authenticated_user()
        history = data.get('history')
        if not username:
            return
        if not isinstance(history, list):
            self.send_json(400, {'ok': False, 'error': 'history 必须是数组'})
            return
        if save_history(username, history):
            self.send_json(200, {'ok': True})
        else:
            self.send_json(400, {'ok': False, 'error': '保存失败'})

    def handle_stats(self, data):
        if not isinstance(data, dict):
            self.send_json(400, {'ok': False, 'error': 'stats 必须是对象'})
            return
        if not append_stats(data):
            self.send_json(400, {'ok': False, 'error': 'stats 保存失败'})
            return
        self.send_json(200, {'ok': True})

    def handle_quality_report(self, data):
        username = self.require_authenticated_user()
        if not username:
            return
        try:
            sample = store_quality_sample(data, username)
        except ControlError as error:
            self.send_control_error(error)
            return
        self.send_json(200, {
            'ok': True,
            'reportedAtUnix': sample['reportedAtUnix'],
        })

    def handle_room_state(self, data):
        username, session_fingerprint = self.require_room_state_writer()
        if not username:
            return
        ok, result, status = set_room_state(data, username, session_fingerprint)
        if ok:
            self.send_json(200, {'ok': True, 'state': result})
        else:
            self.send_json(status, {'ok': False, 'error': result})

    def handle_viewer_intent(self, data):
        username = resolve_authenticated_user(self.headers)
        client_fingerprint = build_viewer_intent_fingerprint(self.headers, self.client_ip(), data)
        ok, result, status = set_viewer_intent(data, username, client_fingerprint)
        if ok:
            self.send_json(200, {'ok': True, 'intent': result})
        else:
            self.send_json(status, {'ok': False, 'error': result})

    def handle_presence(self, request_path, data):
        username = self.require_authenticated_user()
        if not username:
            return
        try:
            if request_path == '/api/presence/join':
                participant = control_plane.join_presence(data, username)
                response = {
                    'ok': True,
                    'participant': participant,
                    'room': get_room_state(participant['room']),
                }
            elif request_path == '/api/presence/heartbeat':
                participant = control_plane.heartbeat_presence(data, username)
                response = {'ok': True, 'participant': participant}
            else:
                participant = control_plane.leave_presence(data, username)
                response = {'ok': True, 'participant': participant}
        except ControlError as error:
            self.send_control_error(error)
            return
        self.send_json(200, response)

    def handle_voice_token(self, data):
        if not GAMECAST_VOICE_SECRET:
            self.send_control_error(ControlError('voice_unavailable', 503))
            return
        username = self.require_authenticated_user()
        if not username:
            return
        try:
            participant = control_plane.authorize_voice(
                data.get('room'), data.get('participantId'), username
            )
            issued = issue_voice_token(
                GAMECAST_VOICE_SECRET,
                participant['room'],
                participant['participantId'],
                ttl=VOICE_TOKEN_TTL,
            )
        except ControlError as error:
            self.send_control_error(error)
            return
        self.send_json(200, {
            'ok': True,
            'token': issued['token'],
            'expiresAtUnix': issued['expiresAtUnix'],
        })

    def handle_share(self, request_path, data):
        username = self.require_authenticated_user()
        if not username:
            return
        try:
            if request_path == '/api/share/claim':
                live = get_sfu_room_state(normalize_room_id(data.get('room')))
                if live and (live.get('sharing') or live.get('hostConnected')):
                    raise ControlError('share_locked', 409)
                lease = control_plane.claim_share(data, username)
            elif request_path == '/api/share/heartbeat':
                lease = control_plane.heartbeat_share(data, username)
            else:
                lease = control_plane.release_share(data, username)
        except ControlError as error:
            self.send_control_error(error)
            return
        self.send_json(200, {
            'ok': True,
            'lease': lease,
            'room': get_room_state(data.get('room')),
        })

    def send_control_error(self, error):
        self.send_json(error.status, {
            'ok': False,
            'code': error.code,
            'error': error.code,
        })

    def do_OPTIONS(self):
        origin = self.cors_origin()
        self.send_response(200)
        if origin:
            self.send_header('Access-Control-Allow-Origin', origin)
            self.send_header('Vary', 'Origin')
        self.send_header('Access-Control-Allow-Methods', 'GET, POST, OPTIONS')
        self.send_header('Access-Control-Allow-Headers', 'Content-Type')
        self.end_headers()

    def send_json(self, code, data, extra_headers=None):
        body = json.dumps(data, ensure_ascii=False).encode('utf-8')
        origin = self.cors_origin()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json; charset=utf-8')
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Cache-Control', 'no-store')
        if origin:
            self.send_header('Access-Control-Allow-Origin', origin)
            self.send_header('Vary', 'Origin')
        if extra_headers:
            for key, value in extra_headers.items():
                self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)


def main():
    server = http.server.ThreadingHTTPServer(('127.0.0.1', LISTEN_PORT), Handler)
    log('服务监听 http://127.0.0.1:%d (注册 + 历史 + 性能日志)' % LISTEN_PORT)
    log('房间状态 TTL: %ds' % ROOM_STATE_TTL)
    log('性能日志存储: %s' % STATS_DIR)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    server.server_close()


if __name__ == '__main__':
    main()
