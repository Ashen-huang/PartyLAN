import http.server
import json
import threading
import unittest
import urllib.error
import urllib.parse
import urllib.request

import register
from gamecast_control import ControlPlane


class RegisterControlAPITest(unittest.TestCase):
    def setUp(self):
        register.control_plane = ControlPlane(lease_id_factory=lambda: "lease-api")
        register.resolve_authenticated_user = lambda _headers: "tester"
        register.fetch_sfu_room_states = lambda: []
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), register.Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base_url = "http://127.0.0.1:%d" % self.server.server_port

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)

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
        payload = register.sanitize_stats_payload(
            {
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
                "candidateAddress": "10.0.0.2",
                "credential": "secret",
                "sdp": "v=0",
                "nested": {"secret": True},
                "serverNote": "x" * 500,
            }
        )
        self.assertEqual(payload["transport"], "tcp")
        self.assertEqual(payload["shadowAction"], "would_restart_ice")
        self.assertLessEqual(len(payload["serverNote"]), 160)
        self.assertNotIn("candidateAddress", payload)
        self.assertNotIn("credential", payload)
        self.assertNotIn("sdp", payload)
        self.assertNotIn("nested", payload)


if __name__ == "__main__":
    unittest.main()
