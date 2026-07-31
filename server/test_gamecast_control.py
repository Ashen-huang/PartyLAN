import concurrent.futures
import unittest

from gamecast_control import (
    ControlError,
    ControlPlane,
    issue_voice_token,
    verify_voice_token,
)


class FakeClock:
    def __init__(self):
        self.value = 1000.0

    def __call__(self):
        return self.value

    def advance(self, seconds):
        self.value += seconds


class ControlPlaneTest(unittest.TestCase):
    def setUp(self):
        self.clock = FakeClock()
        self.control = ControlPlane(
            ttl=15,
            clock=self.clock,
            lease_id_factory=lambda: "lease-1",
        )
        self.control.join_presence(
            {
                "room": "room-1",
                "participantId": "client-owner",
                "clientType": "client",
                "origin": "client",
                "role": "owner",
                "name": "Owner",
            },
            "owner",
        )

    def test_room_origin_is_set_by_creator_and_cannot_be_overwritten(self):
        with self.assertRaises(ControlError) as caught:
            self.control.join_presence(
                {
                    "room": "room-1",
                    "participantId": "web-owner",
                    "clientType": "web",
                    "origin": "web",
                    "role": "owner",
                    "name": "Web Owner",
                },
                "webuser",
            )
        self.assertEqual(caught.exception.code, "room_origin_conflict")
        self.assertEqual(self.control.get_room("room-1")["origin"], "client")

    def test_presence_heartbeat_and_ttl_pruning(self):
        self.control.join_presence(
            {
                "room": "room-1",
                "participantId": "web-member",
                "clientType": "web",
                "role": "member",
                "name": "Browser",
            },
            "webuser",
        )
        self.clock.advance(10)
        self.control.heartbeat_presence(
            {"room": "room-1", "participantId": "web-member"},
            "webuser",
        )
        self.clock.advance(6)
        participants = self.control.list_presence("room-1")
        self.assertEqual([item["participantId"] for item in participants], ["web-member"])

    def test_concurrent_share_claim_allows_only_one_participant(self):
        self.control.join_presence(
            {
                "room": "room-1",
                "participantId": "web-member",
                "clientType": "web",
                "role": "member",
                "name": "Browser",
            },
            "webuser",
        )

        def claim(participant_id, auth_user):
            try:
                lease = self.control.claim_share(
                    {"room": "room-1", "participantId": participant_id},
                    auth_user,
                )
                return lease["participantId"]
            except ControlError as error:
                return error.code

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
            results = list(
                executor.map(
                    lambda args: claim(*args),
                    [("client-owner", "owner"), ("web-member", "webuser")],
                )
            )
        self.assertEqual(results.count("share_locked"), 1)
        self.assertEqual(len([result for result in results if result != "share_locked"]), 1)

    def test_non_owner_cannot_heartbeat_or_release_share_lease(self):
        self.control.join_presence(
            {
                "room": "room-1",
                "participantId": "web-member",
                "clientType": "web",
                "role": "member",
                "name": "Browser",
            },
            "webuser",
        )
        lease = self.control.claim_share(
            {"room": "room-1", "participantId": "client-owner"},
            "owner",
        )
        for operation in (self.control.heartbeat_share, self.control.release_share):
            with self.assertRaises(ControlError) as caught:
                operation(
                    {
                        "room": "room-1",
                        "participantId": "web-member",
                        "leaseId": lease["leaseId"],
                    },
                    "webuser",
                )
            self.assertEqual(caught.exception.code, "share_lease_invalid")

    def test_expired_lease_clears_room_sharing_state(self):
        self.control.claim_share(
            {"room": "room-1", "participantId": "client-owner", "sfu": True},
            "owner",
        )
        self.assertTrue(self.control.get_room("room-1")["sharing"])
        self.clock.advance(16)
        room = self.control.get_room("room-1")
        self.assertFalse(room["sharing"])
        self.assertFalse(room["sfu"])
        self.assertEqual(room["shareOwnerParticipantId"], "")

    def test_legacy_room_state_cannot_override_active_lease(self):
        self.control.claim_share(
            {"room": "room-1", "participantId": "client-owner", "sfu": True},
            "owner",
        )
        room = self.control.set_legacy_room_state(
            {
                "room": "room-1",
                "active": True,
                "sharing": False,
                "sfu": False,
                "origin": "client",
            },
            "owner",
            "session-1",
        )
        self.assertTrue(room["sharing"])
        self.assertTrue(room["sfu"])
        self.assertEqual(room["shareOwnerParticipantId"], "client-owner")

    def test_room_and_presence_expose_cross_client_metadata(self):
        self.control.join_presence(
            {
                "room": "room-1",
                "participantId": "web-member",
                "clientType": "web",
                "role": "member",
                "name": "Browser",
            },
            "webuser",
        )
        lease = self.control.claim_share(
            {"room": "room-1", "participantId": "web-member", "audioMode": "system"},
            "webuser",
        )
        room = self.control.get_room("room-1")
        self.assertEqual(room["origin"], "client")
        self.assertEqual(room["shareOrigin"], "web")
        self.assertEqual(room["shareOwnerParticipantId"], "web-member")
        self.assertEqual(room["participantCount"], 2)
        self.assertEqual(lease["audioMode"], "system")
        participants = self.control.list_presence("room-1")
        web = next(item for item in participants if item["participantId"] == "web-member")
        self.assertTrue(web["sharing"])
        self.assertEqual(web["clientType"], "web")

    def test_share_lease_preserves_exclude_process_audio_mode(self):
        lease = self.control.claim_share(
            {
                "room": "room-1",
                "participantId": "client-owner",
                "audioMode": "exclude-process",
            },
            "owner",
        )
        room = self.control.get_room("room-1")
        self.assertEqual(lease["audioMode"], "exclude-process")
        self.assertEqual(room["audioMode"], "exclude-process")

    def test_voice_authorization_requires_current_client_presence(self):
        participant = self.control.authorize_voice(
            "room-1", "client-owner", "owner"
        )
        self.assertEqual(participant["clientType"], "client")

        self.control.join_presence(
            {
                "room": "room-1",
                "participantId": "web-member",
                "clientType": "web",
                "role": "member",
                "name": "Browser",
            },
            "webuser",
        )
        with self.assertRaises(ControlError) as caught:
            self.control.authorize_voice("room-1", "web-member", "webuser")
        self.assertEqual(caught.exception.code, "voice_client_required")
        self.assertEqual(caught.exception.status, 403)

        with self.assertRaises(ControlError) as caught:
            self.control.authorize_voice("room-1", "client-owner", "other-user")
        self.assertEqual(caught.exception.code, "participant_not_found")

        self.clock.advance(16)
        with self.assertRaises(ControlError) as caught:
            self.control.authorize_voice("room-1", "client-owner", "owner")
        self.assertEqual(caught.exception.code, "participant_not_found")

    def test_voice_token_detects_tampering_expiry_and_wrong_audience(self):
        issued = issue_voice_token(
            "voice-secret",
            "room-1",
            "client-owner",
            now=1000,
            ttl=15,
            jti_factory=lambda: "jti-1",
        )
        self.assertEqual(issued["expiresAtUnix"], 1015)
        claims = verify_voice_token("voice-secret", issued["token"], now=1001)
        self.assertEqual(claims["aud"], "voice-sfu")
        self.assertEqual(claims["room"], "room-1")
        self.assertEqual(claims["participantId"], "client-owner")
        self.assertEqual(claims["clientType"], "client")
        self.assertEqual(claims["jti"], "jti-1")

        tampered = issued["token"][:-1] + ("A" if issued["token"][-1] != "A" else "B")
        for token, now, audience, code in (
            (tampered, 1001, "voice-sfu", "voice_token_invalid"),
            (issued["token"], 1016, "voice-sfu", "voice_token_expired"),
            (issued["token"], 1001, "other-service", "voice_token_invalid"),
        ):
            with self.assertRaises(ControlError) as caught:
                verify_voice_token(
                    "voice-secret", token, now=now, audience=audience
                )
            self.assertEqual(caught.exception.code, code)


if __name__ == "__main__":
    unittest.main()
