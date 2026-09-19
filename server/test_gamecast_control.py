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

    def test_creator_leave_keeps_room_discoverable_while_another_member_is_active(self):
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
        self.control.leave_presence(
            {"room": "room-1", "participantId": "client-owner"},
            "owner",
        )
        rooms = self.control.list_rooms()
        self.assertEqual(len(rooms), 1)
        self.assertEqual(rooms[0]["room"], "room-1")
        self.assertTrue(rooms[0]["active"])
        self.assertEqual(rooms[0]["participantCount"], 1)

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

    def test_legacy_owner_clear_keeps_room_when_presence_member_remains(self):
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
        self.control.set_legacy_room_state(
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

        room = self.control.set_legacy_room_state(
            {"room": "room-1", "active": False},
            "owner",
            "session-1",
        )

        self.assertTrue(room["active"])
        self.assertEqual(room["participantCount"], 2)
        self.assertFalse(room["sharing"])

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

    def test_quality_snapshot_allows_the_current_web_or_client_room_owner(self):
        client_owner, active_ids = self.control.authorize_quality_snapshot(
            "room-1", "client-owner", "owner"
        )
        self.assertEqual(client_owner["clientType"], "client")
        self.assertEqual(active_ids, {"client-owner"})

        web_control = ControlPlane(ttl=15, clock=self.clock)
        web_control.join_presence(
            {
                "room": "web-room",
                "participantId": "web-owner",
                "clientType": "web",
                "origin": "web",
                "role": "owner",
                "name": "Browser Owner",
            },
            "webuser",
        )
        web_control.join_presence(
            {
                "room": "web-room",
                "participantId": "web-member",
                "clientType": "web",
                "role": "member",
                "name": "Browser Viewer",
            },
            "memberuser",
        )

        web_owner, active_ids = web_control.authorize_quality_snapshot(
            "web-room", "web-owner", "webuser"
        )
        self.assertEqual(web_owner["clientType"], "web")
        self.assertEqual(active_ids, {"web-owner", "web-member"})

        with self.assertRaises(ControlError) as caught:
            web_control.authorize_quality_snapshot(
                "web-room", "web-member", "memberuser"
            )
        self.assertEqual(caught.exception.code, "quality_snapshot_forbidden")

        web_control.claim_share(
            {"room": "web-room", "participantId": "web-member"},
            "memberuser",
        )
        sharing_member, _ = web_control.authorize_quality_snapshot(
            "web-room", "web-member", "memberuser"
        )
        self.assertTrue(sharing_member["sharing"])
        reporting_owner = web_control.authorize_quality_report(
            "web-room", "web-owner", "webuser"
        )
        self.assertEqual(reporting_owner["role"], "owner")
        with self.assertRaises(ControlError) as caught:
            web_control.authorize_quality_report(
                "web-room", "web-member", "memberuser"
            )
        self.assertEqual(caught.exception.code, "quality_report_forbidden")

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

    def test_unified_presence_exposes_protocol_capabilities_and_room_mode(self):
        control = ControlPlane(
            ttl=15,
            clock=self.clock,
            unified_sfu_enabled=True,
        )
        capabilities = {
            "sfuViewer": True,
            "sfuHost": True,
            "h264": True,
            "opus": True,
        }
        participant = control.join_presence(
            {
                "room": "unified-room",
                "participantId": "web-1",
                "clientType": "web",
                "origin": "web",
                "role": "owner",
                "name": "Browser",
                "mediaProtocolVersion": 1,
                "capabilities": capabilities,
            },
            "browser-user",
        )
        self.assertEqual(participant["mediaProtocolVersion"], 1)
        self.assertEqual(participant["capabilities"], capabilities)
        room = control.get_room("unified-room")
        self.assertEqual(room["mediaMode"], "sfu")
        self.assertEqual(room["mediaProtocolVersion"], 1)
        self.assertEqual(room["sharePhase"], "idle")

    def test_unified_share_phase_is_lease_driven_and_sfu_only_when_live(self):
        control = ControlPlane(
            ttl=15,
            clock=self.clock,
            lease_id_factory=lambda: "unified-lease",
            unified_sfu_enabled=True,
        )
        for participant_id, client_type, user in (
            ("client-1", "client", "client-user"),
            ("web-1", "web", "web-user"),
        ):
            control.join_presence(
                {
                    "room": "unified-room",
                    "participantId": participant_id,
                    "clientType": client_type,
                    "role": "owner" if participant_id == "client-1" else "member",
                    "name": participant_id,
                    "mediaProtocolVersion": 1,
                    "capabilities": {
                        "sfuViewer": True,
                        "sfuHost": True,
                        "h264": True,
                        "opus": True,
                    },
                    **({"origin": "client"} if participant_id == "client-1" else {}),
                },
                user,
            )

        lease = control.claim_share(
            {
                "room": "unified-room",
                "participantId": "client-1",
                "mediaMode": "sfu",
                "phase": "starting",
                "audioMode": "system",
            },
            "client-user",
        )
        self.assertEqual(lease["phase"], "starting")
        self.assertTrue(control.get_room("unified-room")["sharing"])
        self.assertFalse(control.get_room("unified-room")["sfu"])

        with self.assertRaises(ControlError) as caught:
            control.heartbeat_share(
                {
                    "room": "unified-room",
                    "participantId": "client-1",
                    "leaseId": "unified-lease",
                    "phase": "stopping",
                },
                "client-user",
            )
        self.assertEqual(caught.exception.code, "share_phase_invalid")

        live = control.heartbeat_share(
            {
                "room": "unified-room",
                "participantId": "client-1",
                "leaseId": "unified-lease",
                "phase": "live",
            },
            "client-user",
        )
        self.assertEqual(live["phase"], "live")
        self.assertTrue(control.get_room("unified-room")["sfu"])

        stopping = control.heartbeat_share(
            {
                "room": "unified-room",
                "participantId": "client-1",
                "leaseId": "unified-lease",
                "phase": "stopping",
            },
            "client-user",
        )
        self.assertEqual(stopping["phase"], "stopping")
        self.assertTrue(control.get_room("unified-room")["sharing"])
        self.assertFalse(control.get_room("unified-room")["sfu"])

        with self.assertRaises(ControlError) as caught:
            control.heartbeat_share(
                {
                    "room": "unified-room",
                    "participantId": "client-1",
                    "leaseId": "unified-lease",
                    "phase": "live",
                },
                "client-user",
            )
        self.assertEqual(caught.exception.code, "share_phase_invalid")

        with self.assertRaises(ControlError) as caught:
            control.claim_share(
                {
                    "room": "unified-room",
                    "participantId": "web-1",
                    "mediaMode": "sfu",
                    "phase": "starting",
                },
                "web-user",
            )
        self.assertEqual(caught.exception.code, "share_locked")

    def test_starting_lease_cannot_be_promoted_by_sfu_session_snapshot(self):
        control = ControlPlane(
            ttl=15,
            clock=self.clock,
            lease_id_factory=lambda: "unified-lease",
            unified_sfu_enabled=True,
        )
        control.join_presence(
            {
                "room": "unified-room",
                "participantId": "client-1",
                "clientType": "client",
                "origin": "client",
                "role": "owner",
                "name": "Desktop",
                "mediaProtocolVersion": 1,
                "capabilities": {"sfuViewer": True, "sfuHost": True, "h264": True, "opus": True},
            },
            "client-user",
        )
        control.claim_share(
            {
                "room": "unified-room",
                "participantId": "client-1",
                "mediaMode": "sfu",
                "phase": "starting",
            },
            "client-user",
        )
        room = control.set_legacy_room_state(
            {
                "room": "unified-room",
                "active": True,
                "sharing": True,
                "sfu": True,
                "origin": "client",
            },
            "client-user",
            "legacy-session",
        )
        self.assertEqual(room["sharePhase"], "starting")
        self.assertFalse(room["sfu"])

    def test_rollout_flags_choose_canary_mode_and_gate_legacy_claims(self):
        control = ControlPlane(
            ttl=15,
            clock=self.clock,
            canary_rooms={"canary-room"},
            legacy_p2p_allowed=False,
        )
        for room_id in ("plain-room", "canary-room"):
            control.join_presence(
                {
                    "room": room_id,
                    "participantId": "client-" + room_id,
                    "clientType": "client",
                    "origin": "client",
                    "role": "owner",
                    "name": room_id,
                },
                room_id,
            )
        self.assertEqual(control.get_room("plain-room")["mediaMode"], "legacy-p2p")
        self.assertEqual(control.get_room("canary-room")["mediaMode"], "sfu")
        with self.assertRaises(ControlError) as caught:
            control.claim_share(
                {
                    "room": "plain-room",
                    "participantId": "client-plain-room",
                    "mediaMode": "legacy-p2p",
                },
                "plain-room",
            )
        self.assertEqual(caught.exception.code, "media_protocol_unsupported")

    def test_unified_capabilities_require_json_booleans(self):
        control = ControlPlane(
            ttl=15,
            clock=self.clock,
            unified_sfu_enabled=True,
        )
        participant = control.join_presence(
            {
                "room": "typed-capabilities",
                "participantId": "web-typed",
                "clientType": "web",
                "origin": "web",
                "role": "owner",
                "name": "Browser",
                "mediaProtocolVersion": 1,
                "capabilities": {
                    "sfuViewer": True,
                    "sfuHost": True,
                    "h264": "true",
                    "opus": True,
                },
            },
            "web-user",
        )
        self.assertFalse(participant["capabilities"]["h264"])
        with self.assertRaises(ControlError) as caught:
            control.claim_share(
                {
                    "room": "typed-capabilities",
                    "participantId": "web-typed",
                    "mediaMode": "sfu",
                    "phase": "starting",
                },
                "web-user",
            )
        self.assertEqual(caught.exception.code, "media_protocol_unsupported")


if __name__ == "__main__":
    unittest.main()
