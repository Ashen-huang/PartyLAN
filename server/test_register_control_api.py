import concurrent.futures
import http.server
import json
import os
from pathlib import Path
import tempfile
import threading
import unittest
import urllib.error
import urllib.parse
import urllib.request
from unittest import mock

import bcrypt

import register
from gamecast_control import ControlPlane, verify_voice_token


DIAGNOSTIC_NUMBER_FIELDS = (
    "framesCaptured",
    "framesCapturedDelta",
    "capturedFps",
    "framesEncoded",
    "framesEncodedDelta",
    "encodedFps",
    "framesSent",
    "framesSentDelta",
    "sentFps",
    "keyFramesEncoded",
    "keyFramesEncodedDelta",
    "outboundPliCount",
    "outboundPliCountDelta",
    "outboundNackCount",
    "outboundNackCountDelta",
    "inboundPliCount",
    "inboundPliCountDelta",
    "inboundNackCount",
    "inboundNackCountDelta",
    "retransmittedPacketsSent",
    "retransmittedPacketsSentDelta",
    "retransmittedBytesSent",
    "retransmittedBytesSentDelta",
    "totalPacketSendDelayMs",
    "totalPacketSendDelayDeltaMs",
    "averagePacketSendDelayMs",
)


class RegisterControlAPITest(unittest.TestCase):
    def setUp(self):
        register.control_plane = ControlPlane(lease_id_factory=lambda: "lease-api")
        register.resolve_authenticated_user = lambda _headers: "tester"
        register.fetch_sfu_room_states = lambda: []
        register.rate_buckets.clear()
        register.quality_samples.clear()
        register.quality_clock = lambda: 1000.0
        self.original_htpasswd_file = register.HTPASSWD_FILE
        self.original_voice_secret = register.GAMECAST_VOICE_SECRET
        register.GAMECAST_VOICE_SECRET = "test-voice-secret"
        self.temp_dir = tempfile.TemporaryDirectory()
        register.HTPASSWD_FILE = os.path.join(self.temp_dir.name, "users.htpasswd")
        self.legacy_record = b"legacy:" + bcrypt.hashpw(b"legacy-pass", bcrypt.gensalt(rounds=4)) + b"\n"
        with open(register.HTPASSWD_FILE, "wb") as users_file:
            users_file.write(self.legacy_record)
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), register.Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base_url = "http://127.0.0.1:%d" % self.server.server_port

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        register.HTPASSWD_FILE = self.original_htpasswd_file
        register.GAMECAST_VOICE_SECRET = self.original_voice_secret
        self.temp_dir.cleanup()

    def request(self, method, path, payload=None):
        body = None if payload is None else json.dumps(payload).encode("utf-8")
        request = urllib.request.Request(
            self.base_url + path,
            data=body,
            method=method,
            headers={
                "Accept": "application/json",
                "Content-Type": "application/json",
                "Cookie": "user=tester",
            },
        )
        try:
            with urllib.request.urlopen(request, timeout=3) as response:
                return response.status, json.loads(response.read())
        except urllib.error.HTTPError as error:
            return error.code, json.loads(error.read())

    def test_presence_origin_and_share_lock_routes(self):
        status, joined = self.request(
            "POST",
            "/api/presence/join",
            {
                "room": "cross-room",
                "participantId": "client-owner",
                "clientType": "client",
                "origin": "client",
                "role": "owner",
                "name": "Desktop",
            },
        )
        self.assertEqual(status, 200)
        self.assertEqual(joined["room"]["origin"], "client")

        status, _ = self.request(
            "POST",
            "/api/presence/join",
            {
                "room": "cross-room",
                "participantId": "web-member",
                "clientType": "web",
                "origin": "web",
                "role": "member",
                "name": "Browser",
            },
        )
        self.assertEqual(status, 200)

        status, presence = self.request("GET", "/api/presence?room=cross-room")
        self.assertEqual(status, 200)
        self.assertEqual(presence["count"], 2)
        self.assertEqual(presence["room"]["origin"], "client")

        status, claimed = self.request(
            "POST",
            "/api/share/claim",
            {"room": "cross-room", "participantId": "client-owner", "sfu": True},
        )
        self.assertEqual(status, 200)
        self.assertEqual(claimed["lease"]["leaseId"], "lease-api")
        self.assertTrue(claimed["room"]["sharing"])

        status, conflict = self.request(
            "POST",
            "/api/share/claim",
            {"room": "cross-room", "participantId": "web-member", "sfu": True},
        )
        self.assertEqual(status, 409)
        self.assertEqual(conflict["code"], "share_locked")

        status, released = self.request(
            "POST",
            "/api/share/release",
            {
                "room": "cross-room",
                "participantId": "client-owner",
                "leaseId": "lease-api",
            },
        )
        self.assertEqual(status, 200)
        self.assertFalse(released["room"]["sharing"])

    def test_unified_api_exposes_phase_and_capability_contract(self):
        register.control_plane = ControlPlane(
            lease_id_factory=lambda: "unified-api-lease",
            unified_sfu_enabled=True,
        )
        capabilities = {
            "sfuViewer": True,
            "sfuHost": True,
            "h264": True,
            "opus": True,
        }
        status, joined = self.request(
            "POST",
            "/api/presence/join",
            {
                "room": "unified-api-room",
                "participantId": "web-api",
                "clientType": "web",
                "origin": "web",
                "role": "owner",
                "name": "Browser",
                "mediaProtocolVersion": 1,
                "capabilities": capabilities,
            },
        )
        self.assertEqual(status, 200)
        self.assertEqual(joined["participant"]["mediaProtocolVersion"], 1)
        self.assertEqual(joined["participant"]["capabilities"], capabilities)
        self.assertEqual(joined["room"]["mediaMode"], "sfu")

        status, claimed = self.request(
            "POST",
            "/api/share/claim",
            {
                "room": "unified-api-room",
                "participantId": "web-api",
                "mediaMode": "sfu",
                "phase": "starting",
            },
        )
        self.assertEqual(status, 200)
        self.assertEqual(claimed["lease"]["phase"], "starting")
        self.assertFalse(claimed["room"]["sfu"])

        status, live = self.request(
            "POST",
            "/api/share/heartbeat",
            {
                "room": "unified-api-room",
                "participantId": "web-api",
                "leaseId": "unified-api-lease",
                "phase": "live",
            },
        )
        self.assertEqual(status, 200)
        self.assertEqual(live["lease"]["phase"], "live")
        self.assertTrue(live["room"]["sfu"])

    def test_sfu_session_snapshot_cannot_promote_a_starting_lease(self):
        register.control_plane = ControlPlane(
            lease_id_factory=lambda: "starting-api-lease",
            unified_sfu_enabled=True,
        )
        status, _ = self.request(
            "POST",
            "/api/presence/join",
            {
                "room": "starting-api-room",
                "participantId": "client-api",
                "clientType": "client",
                "origin": "client",
                "role": "owner",
                "name": "Desktop",
                "mediaProtocolVersion": 1,
                "capabilities": {
                    "sfuViewer": True,
                    "sfuHost": True,
                    "h264": True,
                    "opus": True,
                },
            },
        )
        self.assertEqual(status, 200)
        status, _ = self.request(
            "POST",
            "/api/share/claim",
            {
                "room": "starting-api-room",
                "participantId": "client-api",
                "mediaMode": "sfu",
                "phase": "starting",
            },
        )
        self.assertEqual(status, 200)
        register.fetch_sfu_room_states = lambda: [
            {
                "room": "starting-api-room",
                "active": True,
                "sharing": True,
                "sfu": True,
                "hostConnected": True,
                "trackCount": 1,
            }
        ]

        status, response = self.request(
            "GET", "/api/room-state?room=starting-api-room"
        )
        self.assertEqual(status, 200)
        self.assertEqual(response["state"]["sharePhase"], "starting")
        self.assertTrue(response["state"]["sharing"])
        self.assertFalse(response["state"]["sfu"])

    def test_voice_token_requires_authenticated_client_presence(self):
        status, _ = self.request(
            "POST",
            "/api/presence/join",
            {
                "room": "voice-room",
                "participantId": "client-voice",
                "clientType": "client",
                "origin": "client",
                "role": "owner",
                "name": "Desktop",
            },
        )
        self.assertEqual(status, 200)

        status, response = self.request(
            "POST",
            "/api/voice/token",
            {"room": "voice-room", "participantId": "client-voice"},
        )
        self.assertEqual(status, 200)
        self.assertTrue(response["ok"])
        self.assertNotIn("test-voice-secret", json.dumps(response))
        claims = verify_voice_token(
            "test-voice-secret", response["token"], now=response["expiresAtUnix"] - 1
        )
        self.assertEqual(claims["room"], "voice-room")
        self.assertEqual(claims["participantId"], "client-voice")

        original_resolver = register.resolve_authenticated_user
        register.resolve_authenticated_user = lambda _headers: None
        try:
            status, response = self.request(
                "POST",
                "/api/voice/token",
                {"room": "voice-room", "participantId": "client-voice"},
            )
        finally:
            register.resolve_authenticated_user = original_resolver
        self.assertEqual(status, 401)
        self.assertFalse(response["ok"])

    def test_voice_token_rejects_web_presence_and_missing_secret(self):
        status, _ = self.request(
            "POST",
            "/api/presence/join",
            {
                "room": "web-voice-room",
                "participantId": "web-voice",
                "clientType": "web",
                "origin": "web",
                "role": "owner",
                "name": "Browser",
            },
        )
        self.assertEqual(status, 200)
        status, response = self.request(
            "POST",
            "/api/voice/token",
            {"room": "web-voice-room", "participantId": "web-voice"},
        )
        self.assertEqual(status, 403)
        self.assertEqual(response["code"], "voice_client_required")

        register.GAMECAST_VOICE_SECRET = ""
        status, response = self.request(
            "POST",
            "/api/voice/token",
            {"room": "web-voice-room", "participantId": "web-voice"},
        )
        self.assertEqual(status, 503)
        self.assertEqual(response["code"], "voice_unavailable")

    def test_quality_report_requires_active_room_participant(self):
        status, response = self.request(
            "POST",
            "/api/quality/report",
            {
                "room": "quality-room",
                "participantId": "missing-viewer",
                "generation": "g1",
                "pairId": "p1",
                "packetsReceivedDelta": 100,
                "packetsLostDelta": 8,
                "decodedFps": 18,
                "incomingBitrateKbps": 1200,
                "jitterMs": 120,
                "framesDroppedDelta": 2,
            },
        )

        self.assertEqual(status, 404)
        self.assertEqual(response["code"], "participant_not_found")

    def test_quality_snapshot_is_room_scoped_owner_only_and_expires(self):
        for participant in (
            {
                "room": "quality-room",
                "participantId": "web-owner",
                "clientType": "web",
                "origin": "web",
                "role": "owner",
                "name": "Browser Owner",
            },
            {
                "room": "quality-room",
                "participantId": "web-viewer",
                "clientType": "web",
                "origin": "web",
                "role": "member",
                "name": "Browser",
            },
        ):
            status, _ = self.request("POST", "/api/presence/join", participant)
            self.assertEqual(status, 200)

        payload = {
            "room": "quality-room",
            "participantId": "web-viewer",
            "screegoSessionId": "session-a",
            "generation": "g1",
            "pairId": "relay-udp",
            "packetsReceivedDelta": 100,
            "packetsLostDelta": 8,
            "decodedFps": 18,
            "incomingBitrateKbps": 1200,
            "jitterMs": 120,
            "framesDroppedDelta": 2,
        }
        status, report = self.request("POST", "/api/quality/report", payload)
        self.assertEqual(status, 200)
        self.assertTrue(report["ok"])

        second_payload = dict(payload)
        second_payload["screegoSessionId"] = "session-b"
        second_payload["decodedFps"] = 30
        status, report = self.request("POST", "/api/quality/report", second_payload)
        self.assertEqual(status, 200)
        self.assertTrue(report["ok"])

        status, forbidden = self.request(
            "GET",
            "/api/quality/snapshot?room=quality-room&participantId=web-viewer",
        )
        self.assertEqual(status, 403)
        self.assertEqual(forbidden["code"], "quality_snapshot_forbidden")

        status, snapshot = self.request(
            "GET",
            "/api/quality/snapshot?room=quality-room&participantId=web-owner",
        )
        self.assertEqual(status, 200)
        self.assertEqual(snapshot["count"], 2)
        self.assertEqual(
            [sample["screegoSessionId"] for sample in snapshot["samples"]],
            ["session-a", "session-b"],
        )
        self.assertTrue(
            all(sample["participantId"] == "web-viewer" for sample in snapshot["samples"])
        )
        self.assertTrue(
            all(sample["packetsLostDelta"] == 8 for sample in snapshot["samples"])
        )
        self.assertNotIn("candidateAddress", snapshot["samples"][0])

        invalid_payload = dict(payload)
        invalid_payload["screegoSessionId"] = "bad session id"
        status, invalid = self.request("POST", "/api/quality/report", invalid_payload)
        self.assertEqual(status, 400)
        self.assertEqual(invalid["code"], "quality_sample_invalid")

        register.quality_clock = lambda: 1008.1
        status, expired = self.request(
            "GET",
            "/api/quality/snapshot?room=quality-room&participantId=web-owner",
        )
        self.assertEqual(status, 200)
        self.assertEqual(expired["count"], 0)

    def test_legacy_room_state_cannot_clear_active_lease(self):
        self.request(
            "POST",
            "/api/presence/join",
            {
                "room": "legacy-room",
                "participantId": "client-owner",
                "clientType": "client",
                "origin": "client",
                "role": "owner",
                "name": "Desktop",
            },
        )
        self.request(
            "POST",
            "/api/share/claim",
            {"room": "legacy-room", "participantId": "client-owner", "sfu": True},
        )

        status, response = self.request(
            "POST",
            "/api/room-state",
            {
                "room": "legacy-room",
                "active": True,
                "sharing": False,
                "sfu": False,
                "origin": "client",
            },
        )
        self.assertEqual(status, 200)
        self.assertTrue(response["state"]["sharing"])
        self.assertTrue(response["state"]["sfu"])

    def test_webrtc_stats_payload_is_bounded_and_drops_sensitive_fields(self):
        diagnostic_values = {
            field: index + 0.25 for index, field in enumerate(DIAGNOSTIC_NUMBER_FIELDS)
        }
        submitted = {
            "event": "webrtc_sample",
            "policyVersion": "ice-shadow-v1",
            "room": "stats-room",
            "peer": "pc-1",
            "path": "turn",
            "transport": "tcp",
            "localCandidateType": "relay",
            "remoteCandidateType": "srflx",
            "localAddressFamily": "ipv4",
            "remoteAddressFamily": "ipv4",
            "lossPercent": 8.2,
            "jitterMs": 150,
            "rttMs": 120,
            "upKbps": 320,
            "downKbps": 280,
            "packetsReceivedDelta": 100,
            "packetsLostDelta": 9,
            "qualityStatus": "degraded-suspected",
            "shadowAction": "would_restart_ice",
            "adaptivePolicyVersion": "adaptive-per-peer-v1",
            "adaptiveDecision": "downshift:0->1",
            "adaptiveLevel": 1,
            "adaptiveScale": 1.5,
            "adaptiveMaxBitrate": 3000000,
            "telemetryResetReason": "outbound-identity-changed,optional-counter-reset",
            "candidateAddress": "10.0.0.2",
            "credential": "secret",
            "sdp": "v=0",
            "nested": {"secret": True},
            "unknownTelemetry": 999,
            "diagnosticPacketsSentDelta": 77,
            "serverNote": "x" * 500,
        }
        submitted.update(diagnostic_values)

        payload = register.sanitize_stats_payload(submitted)

        self.assertEqual(payload["transport"], "tcp")
        self.assertEqual(payload["shadowAction"], "would_restart_ice")
        self.assertEqual(payload["adaptiveDecision"], "downshift:0->1")
        self.assertEqual(payload["adaptiveLevel"], 1)
        self.assertEqual(payload["adaptiveScale"], 1.5)
        self.assertEqual(
            payload["telemetryResetReason"],
            "outbound-identity-changed,optional-counter-reset",
        )
        for field, value in diagnostic_values.items():
            self.assertEqual(payload[field], value, field)
        self.assertLessEqual(len(payload["serverNote"]), 160)
        self.assertNotIn("candidateAddress", payload)
        self.assertNotIn("credential", payload)
        self.assertNotIn("sdp", payload)
        self.assertNotIn("nested", payload)
        self.assertNotIn("unknownTelemetry", payload)
        self.assertNotIn("diagnosticPacketsSentDelta", payload)

    def test_webrtc_stats_post_writes_public_telemetry_to_jsonl_only(self):
        diagnostic_values = {
            field: index + 1 for index, field in enumerate(DIAGNOSTIC_NUMBER_FIELDS)
        }
        submitted = {
            "event": "webrtc_sample",
            "room": "stats-room",
            "adaptiveConfiguredFps": 30,
            "telemetryResetReason": "media-source-identity-changed",
            "candidateAddress": "10.0.0.2",
            "credential": "secret",
            "sdp": "v=0",
            "nested": {"secret": True},
            "unknownTelemetry": 999,
            "diagnosticPacketsSentDelta": 77,
        }
        submitted.update(diagnostic_values)
        stats_dir = Path(self.temp_dir.name) / "stats"
        stats_dir.mkdir()

        with mock.patch.object(register, "STATS_DIR", str(stats_dir)):
            status, response = self.request("POST", "/api/stats", submitted)
            records = [
                json.loads(line)
                for line in Path(register.get_stats_path()).read_text(encoding="utf-8").splitlines()
            ]

        self.assertEqual(status, 200)
        self.assertTrue(response["ok"])
        self.assertEqual(len(records), 1)
        record = records[0]
        self.assertEqual(record["adaptiveConfiguredFps"], 30)
        self.assertEqual(record["telemetryResetReason"], "media-source-identity-changed")
        for field, value in diagnostic_values.items():
            self.assertEqual(record[field], value, field)
        self.assertTrue(record["_ts"])
        self.assertNotIn("candidateAddress", record)
        self.assertNotIn("credential", record)
        self.assertNotIn("sdp", record)
        self.assertNotIn("nested", record)
        self.assertNotIn("unknownTelemetry", record)
        self.assertNotIn("diagnosticPacketsSentDelta", record)

    def test_register_succeeds_without_the_htpasswd_command_and_preserves_existing_users(self):
        with mock.patch.dict(os.environ, {"PATH": ""}):
            status, response = self.request(
                "POST",
                "/api/register",
                {"user": "new_user", "pass": "correct horse battery staple"},
            )

        self.assertEqual(status, 200)
        self.assertTrue(response["ok"])
        with open(register.HTPASSWD_FILE, "rb") as users_file:
            records = users_file.read()
        self.assertTrue(records.startswith(self.legacy_record))
        new_record = next(line for line in records.splitlines() if line.startswith(b"new_user:"))
        self.assertTrue(bcrypt.checkpw(b"correct horse battery staple", new_record.split(b":", 1)[1]))

    def test_concurrent_registration_creates_only_one_record_for_a_username(self):
        def register_same_user(_index):
            return self.request(
                "POST",
                "/api/register",
                {"user": "same_user", "pass": "shared-password"},
            )[0]

        with mock.patch.dict(os.environ, {"PATH": ""}):
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
                statuses = list(executor.map(register_same_user, range(2)))

        self.assertEqual(sorted(statuses), [200, 409])
        with open(register.HTPASSWD_FILE, "rb") as users_file:
            matching = [line for line in users_file.read().splitlines() if line.startswith(b"same_user:")]
        self.assertEqual(len(matching), 1)

    def test_atomic_replace_failure_keeps_the_original_credentials_file(self):
        with mock.patch.object(register.os, "replace", side_effect=OSError("replace failed")) as replace:
            status, response = self.request(
                "POST",
                "/api/register",
                {"user": "write_failure", "pass": "shared-password"},
            )

        self.assertEqual(status, 500)
        self.assertFalse(response["ok"])
        replace.assert_called_once()
        with open(register.HTPASSWD_FILE, "rb") as users_file:
            self.assertEqual(users_file.read(), self.legacy_record)
        self.assertEqual(os.listdir(self.temp_dir.name), ["users.htpasswd"])

    def test_deployment_mounts_the_credentials_directory_for_atomic_updates(self):
        compose_path = Path(__file__).parent / "screego" / "deploy" / "docker-compose.yml"
        compose = compose_path.read_text(encoding="utf-8")

        self.assertIn(
            "${SCREEGO_USERS_DIR_HOST:-/etc/gamecast-sfu/users}:/app/users:ro",
            compose,
        )
        self.assertIn("SCREEGO_USERS_FILE: /app/users/users.htpasswd", compose)
        self.assertNotIn("SCREEGO_USERS_FILE_HOST:-", compose)

    def test_deployment_register_is_byte_identical_to_source(self):
        source_path = Path(register.__file__).resolve()
        deploy_path = source_path.parent / "screego" / "deploy" / "register.py"

        self.assertEqual(source_path.read_bytes(), deploy_path.read_bytes())

    def test_nginx_proxies_quality_control_api(self):
        nginx_path = Path(__file__).parent / "screego" / "deploy" / "nginx-screego.conf"
        nginx = nginx_path.read_text(encoding="utf-8")
        self.assertIn("location /api/quality", nginx)
        self.assertIn("proxy_pass http://127.0.0.1:5052;", nginx)


if __name__ == "__main__":
    unittest.main()
