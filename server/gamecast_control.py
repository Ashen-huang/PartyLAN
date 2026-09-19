import base64
import hashlib
import hmac
import json
import secrets
import threading
import time
import uuid


VALID_ORIGINS = {"web", "client"}
VALID_CLIENT_TYPES = {"web", "client"}
VALID_ROLES = {"owner", "member"}
VALID_AUDIO_MODES = {"app", "exclude-process", "off", "system", "process"}
VALID_MEDIA_MODES = {"sfu", "legacy-p2p"}
VALID_SHARE_PHASES = {"starting", "live", "stopping"}
MEDIA_PROTOCOL_VERSION = 1
MEDIA_CAPABILITY_KEYS = ("sfuViewer", "sfuHost", "h264", "opus")
SHARE_PHASE_TRANSITIONS = {
    "starting": {"starting", "live"},
    "live": {"live", "stopping"},
    "stopping": {"stopping"},
}


class ControlError(Exception):
    def __init__(self, code, status=400):
        super().__init__(code)
        self.code = code
        self.status = status


VOICE_TOKEN_AUDIENCE = "voice-sfu"


def _base64url_encode(data):
    return base64.urlsafe_b64encode(data).decode("ascii").rstrip("=")


def _base64url_decode(value):
    if not isinstance(value, str) or not value:
        raise ValueError("invalid base64url value")
    padding = "=" * ((4 - len(value) % 4) % 4)
    return base64.b64decode(
        (value + padding).encode("ascii"), altchars=b"-_", validate=True
    )


def issue_voice_token(
    secret,
    room_id,
    participant_id,
    now=None,
    ttl=15,
    jti_factory=None,
):
    secret = str(secret or "").strip()
    room_id = str(room_id or "").strip()
    participant_id = str(participant_id or "").strip()
    if not secret:
        raise ControlError("voice_unavailable", 503)
    if not room_id or len(room_id) > 120 or not participant_id or len(participant_id) > 128:
        raise ControlError("voice_token_invalid", 400)
    issued_at = int(time.time() if now is None else now)
    ttl = max(1, min(int(ttl), 60))
    expires_at = issued_at + ttl
    jti_factory = jti_factory or (lambda: secrets.token_urlsafe(12))
    claims = {
        "aud": VOICE_TOKEN_AUDIENCE,
        "room": room_id,
        "participantId": participant_id,
        "clientType": "client",
        "iat": issued_at,
        "exp": expires_at,
        "jti": str(jti_factory()),
    }
    payload = _base64url_encode(
        json.dumps(claims, sort_keys=True, separators=(",", ":")).encode("utf-8")
    )
    signature = _base64url_encode(
        hmac.new(secret.encode("utf-8"), payload.encode("ascii"), hashlib.sha256).digest()
    )
    return {"token": payload + "." + signature, "expiresAtUnix": expires_at}


def verify_voice_token(secret, token, now=None, audience=VOICE_TOKEN_AUDIENCE):
    secret = str(secret or "").strip()
    if not secret or not isinstance(token, str) or len(token) > 4096:
        raise ControlError("voice_token_invalid", 401)
    parts = token.split(".")
    if len(parts) != 2:
        raise ControlError("voice_token_invalid", 401)
    payload, encoded_signature = parts
    try:
        signature = _base64url_decode(encoded_signature)
        expected = hmac.new(
            secret.encode("utf-8"), payload.encode("ascii"), hashlib.sha256
        ).digest()
        if not hmac.compare_digest(signature, expected):
            raise ValueError("signature mismatch")
        claims = json.loads(_base64url_decode(payload).decode("utf-8"))
    except (UnicodeError, ValueError, TypeError, json.JSONDecodeError):
        raise ControlError("voice_token_invalid", 401)

    required_text = ("aud", "room", "participantId", "clientType", "jti")
    if any(not isinstance(claims.get(field), str) or not claims[field] for field in required_text):
        raise ControlError("voice_token_invalid", 401)
    if claims["aud"] != audience or claims["clientType"] != "client":
        raise ControlError("voice_token_invalid", 401)
    if len(claims["room"]) > 120 or len(claims["participantId"]) > 128:
        raise ControlError("voice_token_invalid", 401)
    if not isinstance(claims.get("iat"), int) or not isinstance(claims.get("exp"), int):
        raise ControlError("voice_token_invalid", 401)
    checked_at = int(time.time() if now is None else now)
    if claims["iat"] > checked_at + 5:
        raise ControlError("voice_token_invalid", 401)
    if claims["exp"] <= checked_at:
        raise ControlError("voice_token_expired", 401)
    return claims


class ControlPlane:
    def __init__(
        self,
        ttl=15,
        clock=None,
        lease_id_factory=None,
        unified_sfu_enabled=False,
        canary_rooms=None,
        legacy_p2p_allowed=True,
    ):
        self.ttl = self._clamp_ttl(ttl)
        self._clock = clock or time.time
        self._lease_id_factory = lease_id_factory or (lambda: uuid.uuid4().hex)
        self._unified_sfu_enabled = bool(unified_sfu_enabled)
        self._canary_rooms = {
            self._clean_text(room_id, 120).lower()
            for room_id in (canary_rooms or set())
            if self._clean_text(room_id, 120)
        }
        self._legacy_p2p_allowed = bool(legacy_p2p_allowed)
        self._lock = threading.RLock()
        self._rooms = {}
        self._presence = {}
        self._leases = {}

    def join_presence(self, data, auth_user):
        room_id = self._required_text(data.get("room"), "room_not_found", 400, 120)
        participant_id = self._required_text(
            data.get("participantId"), "participant_not_found", 400, 128
        )
        client_type = self._choice(
            data.get("clientType"), VALID_CLIENT_TYPES, "participant_not_found"
        )
        requested_role = self._choice(
            data.get("role") or "member", VALID_ROLES, "participant_not_found"
        )
        requested_origin = self._optional_choice(
            data.get("origin"), VALID_ORIGINS, "room_origin_conflict"
        )
        name = self._clean_text(data.get("name") or auth_user or "member", 80)
        media_protocol_version = self._media_protocol_version(
            data.get("mediaProtocolVersion")
        )
        capabilities = self._media_capabilities(data.get("capabilities"))
        ttl = self._clamp_ttl(data.get("ttl"))
        now = self._clock()

        with self._lock:
            self._prune_locked(now)
            room = self._rooms.get(room_id)
            if room is None:
                if not requested_origin:
                    raise ControlError("room_not_found", 404)
                requested_role = "owner"
                room = self._new_room(
                    room_id,
                    requested_origin,
                    participant_id,
                    name,
                    ttl,
                    now,
                )
                self._rooms[room_id] = room
            elif requested_role == "owner" and requested_origin:
                if room.get("origin") and room["origin"] != requested_origin:
                    raise ControlError("room_origin_conflict", 409)
                if not room.get("origin"):
                    room["origin"] = requested_origin
                if not room.get("owner_participant_id"):
                    room["owner_participant_id"] = participant_id
                    room["owner_name"] = name

            participants = self._presence.setdefault(room_id, {})
            existing = participants.get(participant_id)
            if existing and existing.get("auth_user") != auth_user:
                raise ControlError("participant_not_found", 403)
            if room.get("owner_participant_id") == participant_id:
                requested_role = "owner"
            participant = {
                "participant_id": participant_id,
                "name": name,
                "client_type": client_type,
                "role": requested_role,
                "auth_user": auth_user,
                "media_protocol_version": media_protocol_version,
                "capabilities": capabilities,
                "ttl": ttl,
                "updated_at": now,
            }
            participants[participant_id] = participant
            room["active"] = True
            room["ttl"] = max(room.get("ttl", self.ttl), ttl)
            room["updated_at"] = now
            return self._build_participant(room_id, participant, now)

    def heartbeat_presence(self, data, auth_user):
        room_id, participant_id = self._presence_identity(data)
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            participant = self._get_participant_locked(room_id, participant_id, auth_user)
            participant["updated_at"] = now
            participant["ttl"] = self._clamp_ttl(data.get("ttl") or participant["ttl"])
            room = self._rooms.get(room_id)
            if room:
                room["active"] = True
                room["updated_at"] = now
            return self._build_participant(room_id, participant, now)

    def leave_presence(self, data, auth_user):
        room_id, participant_id = self._presence_identity(data)
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            self._get_participant_locked(room_id, participant_id, auth_user)
            participants = self._presence.get(room_id) or {}
            participants.pop(participant_id, None)
            if not participants:
                self._presence.pop(room_id, None)
            lease = self._leases.get(room_id)
            if lease and lease.get("participant_id") == participant_id:
                self._leases.pop(room_id, None)
            return {"room": room_id, "participantId": participant_id, "active": False}

    def list_presence(self, room_id):
        room_id = self._required_text(room_id, "room_not_found", 400, 120)
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            participants = self._presence.get(room_id) or {}
            result = [
                self._build_participant(room_id, participant, now)
                for participant in participants.values()
            ]
            result.sort(key=lambda item: (item["role"] != "owner", item["name"], item["participantId"]))
            return result

    def authorize_quality_report(self, room_id, participant_id, auth_user):
        room_id = self._required_text(room_id, "room_not_found", 400, 120)
        participant_id = self._required_text(
            participant_id, "participant_not_found", 400, 128
        )
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            participant = self._get_participant_locked(room_id, participant_id, auth_user)
            lease = self._leases.get(room_id)
            if lease and lease.get("participant_id") == participant_id:
                raise ControlError("quality_report_forbidden", 403)
            return self._build_participant(room_id, participant, now)

    def authorize_quality_snapshot(self, room_id, participant_id, auth_user):
        room_id = self._required_text(room_id, "room_not_found", 400, 120)
        participant_id = self._required_text(
            participant_id, "participant_not_found", 400, 128
        )
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            participant = self._get_participant_locked(room_id, participant_id, auth_user)
            room = self._rooms.get(room_id) or {}
            lease = self._leases.get(room_id)
            owns_room = participant.get("role") == "owner" and (
                room.get("owner_participant_id") == participant_id
            )
            owns_share = bool(lease and lease.get("participant_id") == participant_id)
            if not owns_room and not owns_share:
                raise ControlError("quality_snapshot_forbidden", 403)
            active_ids = set((self._presence.get(room_id) or {}).keys())
            return self._build_participant(room_id, participant, now), active_ids

    def authorize_voice(self, room_id, participant_id, auth_user):
        room_id = self._required_text(room_id, "room_not_found", 400, 120)
        participant_id = self._required_text(
            participant_id, "participant_not_found", 400, 128
        )
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            participant = self._get_participant_locked(
                room_id, participant_id, auth_user
            )
            if participant.get("client_type") != "client":
                raise ControlError("voice_client_required", 403)
            return self._build_participant(room_id, participant, now)

    def claim_share(self, data, auth_user):
        room_id, participant_id = self._presence_identity(data)
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            participant = self._get_participant_locked(room_id, participant_id, auth_user)
            room = self._rooms.get(room_id)
            if not room:
                raise ControlError("room_not_found", 404)
            current = self._leases.get(room_id)
            if current:
                if current.get("participant_id") != participant_id:
                    raise ControlError("share_locked", 409)
                return self._build_lease(room_id, current, now)
            media_mode = self._optional_choice(
                data.get("mediaMode"), VALID_MEDIA_MODES, "media_protocol_unsupported"
            ) or room.get("media_mode") or "legacy-p2p"
            if media_mode != room.get("media_mode"):
                raise ControlError("media_protocol_unsupported", 409)
            if media_mode == "legacy-p2p" and not self._legacy_p2p_allowed:
                raise ControlError("media_protocol_unsupported", 409)
            if media_mode == "sfu" and not self._participant_supports_unified_sfu(participant):
                raise ControlError("media_protocol_unsupported", 409)

            requested_phase = self._optional_choice(
                data.get("phase"), VALID_SHARE_PHASES, "share_phase_invalid"
            )
            if requested_phase and requested_phase != "starting":
                raise ControlError("share_phase_invalid", 409)
            if requested_phase:
                phase = requested_phase
            elif media_mode == "sfu" and participant.get("media_protocol_version") == MEDIA_PROTOCOL_VERSION:
                phase = "starting"
            else:
                phase = "live"

            ttl = self._clamp_ttl(data.get("ttl"))
            lease = {
                "lease_id": self._lease_id_factory(),
                "participant_id": participant_id,
                "client_type": participant["client_type"],
                "name": participant["name"],
                "media_mode": media_mode,
                "phase": phase,
                "legacy_sfu": bool(data.get("sfu", True)),
                "audio_mode": self._audio_mode(data.get("audioMode")),
                "ttl": ttl,
                "updated_at": now,
            }
            self._leases[room_id] = lease
            if room:
                room["legacy_sharing"] = False
                room["legacy_sfu"] = False
                room["updated_at"] = now
            return self._build_lease(room_id, lease, now)

    def heartbeat_share(self, data, auth_user):
        room_id, participant_id = self._presence_identity(data)
        lease_id = self._required_text(
            data.get("leaseId"), "share_lease_invalid", 409, 128
        )
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            self._get_participant_locked(room_id, participant_id, auth_user)
            lease = self._leases.get(room_id)
            self._validate_lease_locked(lease, participant_id, lease_id)
            requested_media_mode = self._optional_choice(
                data.get("mediaMode"), VALID_MEDIA_MODES, "media_protocol_unsupported"
            )
            if requested_media_mode and requested_media_mode != lease.get("media_mode"):
                raise ControlError("media_protocol_unsupported", 409)
            requested_phase = self._optional_choice(
                data.get("phase"), VALID_SHARE_PHASES, "share_phase_invalid"
            )
            if requested_phase:
                current_phase = lease.get("phase") or "live"
                if requested_phase not in SHARE_PHASE_TRANSITIONS.get(current_phase, set()):
                    raise ControlError("share_phase_invalid", 409)
                lease["phase"] = requested_phase
            lease["updated_at"] = now
            lease["ttl"] = self._clamp_ttl(data.get("ttl") or lease["ttl"])
            if "sfu" in data and lease.get("media_mode") == "legacy-p2p":
                lease["legacy_sfu"] = bool(data.get("sfu"))
            if "audioMode" in data:
                lease["audio_mode"] = self._audio_mode(data.get("audioMode"))
            room = self._rooms.get(room_id)
            if room:
                room["updated_at"] = now
            return self._build_lease(room_id, lease, now)

    def release_share(self, data, auth_user):
        room_id, participant_id = self._presence_identity(data)
        lease_id = self._required_text(
            data.get("leaseId"), "share_lease_invalid", 409, 128
        )
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            self._get_participant_locked(room_id, participant_id, auth_user)
            lease = self._leases.get(room_id)
            self._validate_lease_locked(lease, participant_id, lease_id)
            self._leases.pop(room_id, None)
            room = self._rooms.get(room_id)
            if room:
                room["updated_at"] = now
            return {"room": room_id, "leaseId": lease_id, "active": False}

    def set_legacy_room_state(self, data, auth_user, session_fingerprint):
        room_id = self._required_text(data.get("room"), "room_not_found", 400, 120)
        if not auth_user or not session_fingerprint:
            raise ControlError("participant_not_found", 401)
        requested_origin = self._optional_choice(
            data.get("origin"), VALID_ORIGINS, "room_origin_conflict"
        )
        active = bool(data.get("active"))
        now = self._clock()
        ttl = self._clamp_ttl(data.get("ttl"))
        with self._lock:
            self._prune_locked(now)
            room = self._rooms.get(room_id)
            if not active:
                if room and not self._legacy_owner_matches(room, auth_user, session_fingerprint):
                    raise ControlError("room_origin_conflict", 409)
                if room_id not in self._leases:
                    # A legacy owner leaving must not erase a room that still has
                    # Presence members. Their heartbeats are the room's lifecycle.
                    if room and self._presence.get(room_id):
                        room["legacy_sharing"] = False
                        room["legacy_sfu"] = False
                        room["legacy_audio_mode"] = "off"
                        room["active"] = True
                        room["updated_at"] = now
                    else:
                        self._rooms.pop(room_id, None)
                return self._build_room_locked(room_id, now)

            if room is None:
                room = self._new_room(room_id, requested_origin, "", auth_user, ttl, now)
                room["legacy_owner_user"] = auth_user
                room["legacy_owner_session"] = session_fingerprint
                self._rooms[room_id] = room
            elif requested_origin and room.get("origin") and room["origin"] != requested_origin:
                raise ControlError("room_origin_conflict", 409)
            elif requested_origin and not room.get("origin"):
                room["origin"] = requested_origin

            if not self._legacy_owner_matches(room, auth_user, session_fingerprint):
                legacy_idle = not room.get("legacy_sharing") and not room.get("legacy_sfu")
                incoming_live = bool(data.get("sharing")) or bool(data.get("sfu"))
                if not legacy_idle or not incoming_live or self._leases.get(room_id):
                    raise ControlError("room_origin_conflict", 409)
                room["legacy_owner_user"] = auth_user
                room["legacy_owner_session"] = session_fingerprint

            if room_id not in self._leases:
                room["legacy_sharing"] = bool(data.get("sharing"))
                room["legacy_sfu"] = bool(data.get("sfu"))
                room["legacy_audio_mode"] = self._audio_mode(data.get("audioMode"))
            room["active"] = True
            room["owner_name"] = room.get("owner_name") or auth_user
            room["ttl"] = ttl
            room["updated_at"] = now
            return self._build_room_locked(room_id, now)

    def get_room(self, room_id):
        room_id = self._required_text(room_id, "room_not_found", 400, 120)
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            return self._build_room_locked(room_id, now)

    def list_rooms(self):
        now = self._clock()
        with self._lock:
            self._prune_locked(now)
            rooms = [
                self._build_room_locked(room_id, now)
                for room_id in self._rooms
                if self._rooms[room_id].get("active")
            ]
            rooms.sort(key=lambda item: (not item["sharing"], item["room"]))
            return rooms

    def _presence_identity(self, data):
        return (
            self._required_text(data.get("room"), "room_not_found", 400, 120),
            self._required_text(
                data.get("participantId"), "participant_not_found", 400, 128
            ),
        )

    def _get_participant_locked(self, room_id, participant_id, auth_user):
        participant = (self._presence.get(room_id) or {}).get(participant_id)
        if not participant or participant.get("auth_user") != auth_user:
            raise ControlError("participant_not_found", 404)
        return participant

    @staticmethod
    def _validate_lease_locked(lease, participant_id, lease_id):
        if not lease:
            raise ControlError("share_lease_expired", 409)
        if lease.get("participant_id") != participant_id or lease.get("lease_id") != lease_id:
            raise ControlError("share_lease_invalid", 409)

    def _build_room_locked(self, room_id, now):
        room = self._rooms.get(room_id)
        if not room:
            media_mode = self._media_mode_for_room(room_id)
            return {
                "room": room_id,
                "active": False,
                "origin": "",
                "ownerParticipantId": "",
                "ownerName": "",
                "user": None,
                "sharing": False,
                "mediaMode": media_mode,
                "mediaProtocolVersion": MEDIA_PROTOCOL_VERSION if media_mode == "sfu" else 0,
                "sharePhase": "idle",
                "shareOwnerParticipantId": "",
                "shareOwnerName": "",
                "shareOrigin": "",
                "sfu": False,
                "audioMode": "off",
                "ttl": self.ttl,
                "ageSeconds": None,
                "participantCount": 0,
            }
        lease = self._leases.get(room_id)
        sharing = bool(lease) or bool(room.get("legacy_sharing"))
        share_phase = lease.get("phase") if lease else ("live" if sharing else "idle")
        sfu = self._lease_sfu(lease) if lease else bool(room.get("legacy_sfu"))
        audio_mode = lease.get("audio_mode") if lease else room.get("legacy_audio_mode")
        participants = self._presence.get(room_id) or {}
        return {
            "room": room_id,
            "active": bool(room.get("active")),
            "origin": room.get("origin") or "",
            "ownerParticipantId": room.get("owner_participant_id") or "",
            "ownerName": room.get("owner_name") or "",
            "user": room.get("owner_name") or None,
            "sharing": sharing,
            "mediaMode": room.get("media_mode") or "legacy-p2p",
            "mediaProtocolVersion": room.get("media_protocol_version", 0),
            "sharePhase": share_phase or "idle",
            "shareOwnerParticipantId": lease.get("participant_id") if lease else "",
            "shareOwnerName": lease.get("name") if lease else "",
            "shareOrigin": lease.get("client_type") if lease else "",
            "sfu": sfu,
            "audioMode": audio_mode or "off",
            "ttl": room.get("ttl", self.ttl),
            "ageSeconds": max(0.0, now - room.get("updated_at", now)),
            "participantCount": len(participants),
            "updatedAtUnix": room.get("updated_at", now),
        }

    def _build_participant(self, room_id, participant, now):
        lease = self._leases.get(room_id)
        return {
            "participantId": participant["participant_id"],
            "room": room_id,
            "name": participant["name"],
            "clientType": participant["client_type"],
            "role": participant["role"],
            "active": True,
            "sharing": bool(lease and lease.get("participant_id") == participant["participant_id"]),
            "mediaProtocolVersion": participant.get("media_protocol_version", 0),
            "capabilities": dict(participant.get("capabilities") or {}),
            "ttl": participant["ttl"],
            "ageSeconds": max(0.0, now - participant["updated_at"]),
        }

    @classmethod
    def _build_lease(cls, room_id, lease, now):
        return {
            "room": room_id,
            "leaseId": lease["lease_id"],
            "participantId": lease["participant_id"],
            "clientType": lease["client_type"],
            "name": lease["name"],
            "active": True,
            "mediaMode": lease.get("media_mode") or "legacy-p2p",
            "phase": lease.get("phase") or "live",
            "sfu": cls._lease_sfu(lease),
            "audioMode": lease.get("audio_mode") or "off",
            "ttl": lease["ttl"],
            "expiresAtUnix": lease["updated_at"] + lease["ttl"],
            "ageSeconds": max(0.0, now - lease["updated_at"]),
        }

    def _prune_locked(self, now):
        for room_id, participants in list(self._presence.items()):
            for participant_id, participant in list(participants.items()):
                if now - participant.get("updated_at", 0) > participant.get("ttl", self.ttl):
                    participants.pop(participant_id, None)
            if not participants:
                self._presence.pop(room_id, None)

        for room_id, lease in list(self._leases.items()):
            owner_present = lease.get("participant_id") in (self._presence.get(room_id) or {})
            if (
                not owner_present
                or now - lease.get("updated_at", 0) > lease.get("ttl", self.ttl)
            ):
                self._leases.pop(room_id, None)

        for room_id, room in list(self._rooms.items()):
            has_presence = bool(self._presence.get(room_id))
            has_lease = bool(self._leases.get(room_id))
            expired = now - room.get("updated_at", 0) > room.get("ttl", self.ttl)
            if not room.get("active") or (expired and not has_presence and not has_lease):
                self._rooms.pop(room_id, None)

    def _new_room(self, room_id, origin, owner_participant_id, owner_name, ttl, now):
        media_mode = self._media_mode_for_room(room_id)
        return {
            "room": room_id,
            "active": True,
            "origin": origin or "",
            "owner_participant_id": owner_participant_id,
            "owner_name": owner_name,
            "legacy_owner_user": "",
            "legacy_owner_session": "",
            "legacy_sharing": False,
            "legacy_sfu": False,
            "legacy_audio_mode": "off",
            "media_mode": media_mode,
            "media_protocol_version": MEDIA_PROTOCOL_VERSION if media_mode == "sfu" else 0,
            "ttl": ttl,
            "updated_at": now,
        }

    @staticmethod
    def _legacy_owner_matches(room, auth_user, session_fingerprint):
        owner_user = room.get("legacy_owner_user")
        owner_session = room.get("legacy_owner_session")
        if not owner_user and not owner_session:
            return True
        return owner_user == auth_user and owner_session == session_fingerprint

    def _clamp_ttl(self, value):
        try:
            ttl = int(value)
        except (TypeError, ValueError):
            ttl = getattr(self, "ttl", 15)
        return max(5, min(ttl, 60))

    @staticmethod
    def _clean_text(value, max_length):
        return str(value or "").strip()[:max_length]

    def _required_text(self, value, code, status, max_length):
        text = self._clean_text(value, max_length)
        if not text or any(char in text for char in "\r\n\t"):
            raise ControlError(code, status)
        return text

    def _choice(self, value, choices, code):
        normalized = self._clean_text(value, 32).lower()
        if normalized not in choices:
            raise ControlError(code, 400)
        return normalized

    def _optional_choice(self, value, choices, code):
        normalized = self._clean_text(value, 32).lower()
        if not normalized:
            return ""
        if normalized not in choices:
            raise ControlError(code, 400)
        return normalized

    def _audio_mode(self, value):
        normalized = self._clean_text(value, 32).lower()
        return normalized if normalized in VALID_AUDIO_MODES else "off"

    def _media_mode_for_room(self, room_id):
        normalized = self._clean_text(room_id, 120).lower()
        if self._unified_sfu_enabled or normalized in self._canary_rooms:
            return "sfu"
        return "legacy-p2p"

    @staticmethod
    def _media_protocol_version(value):
        if value in (None, ""):
            return 0
        try:
            version = int(value)
        except (TypeError, ValueError):
            raise ControlError("media_protocol_unsupported", 400)
        if version < 0 or version > MEDIA_PROTOCOL_VERSION:
            raise ControlError("media_protocol_unsupported", 400)
        return version

    @staticmethod
    def _media_capabilities(value):
        source = value if isinstance(value, dict) else {}
        return {key: source.get(key) is True for key in MEDIA_CAPABILITY_KEYS}

    @staticmethod
    def _participant_supports_unified_sfu(participant):
        capabilities = participant.get("capabilities") or {}
        return (
            participant.get("media_protocol_version") == MEDIA_PROTOCOL_VERSION
            and capabilities.get("sfuHost")
            and capabilities.get("h264")
            and capabilities.get("opus")
        )

    @staticmethod
    def _lease_sfu(lease):
        if not lease:
            return False
        if lease.get("media_mode") == "sfu":
            return lease.get("phase") == "live"
        return bool(lease.get("legacy_sfu"))
