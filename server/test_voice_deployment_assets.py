import hashlib
import re
import unittest
from pathlib import Path


SERVER_DIR = Path(__file__).resolve().parent
DEPLOY_DIR = SERVER_DIR / "screego" / "deploy"
SCREEGO_DIR = SERVER_DIR / "screego"
PRODUCTION_SFU_CLIENT_SHA256 = (
    "74826211f05c512f0ac403984ded48778d16692eb1f5054c6a84db4324a22450"
)


class VoiceDeploymentAssetsTests(unittest.TestCase):
    def test_sfu_client_is_versioned_with_the_production_hash(self):
        asset = DEPLOY_DIR / "sfu-client.js"
        self.assertTrue(asset.is_file(), "production sfu-client.js is not version controlled")
        lock = (SCREEGO_DIR / "upstream.lock").read_text(encoding="utf-8")
        self.assertIn(
            "productionSfuClientSha256=" + PRODUCTION_SFU_CLIENT_SHA256,
            lock,
        )
        current_hash = re.search(r"(?m)^sfuClientSha256=([0-9a-f]{64})$", lock)
        self.assertIsNotNone(current_hash)
        self.assertEqual(
            hashlib.sha256(asset.read_bytes()).hexdigest(),
            current_hash.group(1),
        )

        nginx = (DEPLOY_DIR / "nginx-screego.conf").read_text(encoding="utf-8")
        self.assertRegex(
            nginx,
            r"(?s)location = /sfu-client\.js\s*\{.*?alias /var/www/screego/sfu-client\.js;.*?\}",
        )

    def test_nginx_proxies_voice_control_and_websocket_without_sensitive_logging(self):
        nginx = (DEPLOY_DIR / "nginx-screego.conf").read_text(encoding="utf-8")

        self.assertRegex(
            nginx,
            r"(?s)location /api/voice\s*\{.*?proxy_pass http://127\.0\.0\.1:5052;.*?\}",
        )
        self.assertRegex(
            nginx,
            r"(?s)location /voice-ws\s*\{.*?proxy_pass http://127\.0\.0\.1:5054/ws;"
            r".*?proxy_http_version 1\.1;.*?proxy_set_header Upgrade \$http_upgrade;"
            r".*?proxy_set_header Connection \$connection_upgrade;.*?\}",
        )
        lowered = nginx.lower()
        self.assertNotIn("$http_authorization", lowered)
        self.assertNotIn("$request_body", lowered)

    def test_compose_declares_independent_fixed_voice_sfu_service(self):
        compose = (DEPLOY_DIR / "docker-compose.yml").read_text(encoding="utf-8")

        self.assertRegex(compose, r"(?m)^  voice-sfu:\s*$")
        image = re.search(
            r"(?ms)^  voice-sfu:\s*$.*?^    image:\s*([^\s]+)\s*$", compose
        )
        self.assertIsNotNone(image)
        self.assertNotIn("${", image.group(1))
        self.assertNotRegex(image.group(1), r"(?i):latest$")
        self.assertIn('GAMECAST_VOICE_SECRET: "${GAMECAST_VOICE_SECRET}"', compose)
        self.assertIn('VOICE_SFU_LISTEN_ADDR: "127.0.0.1:5054"', compose)
        self.assertIn('VOICE_SFU_UDP_ADDR: "0.0.0.0:5054"', compose)
        self.assertIn('VOICE_SFU_ROOM_LIMIT: "20"', compose)
        self.assertIn('SCREEGO_SERVER_ADDRESS: ":5060"', compose)

    def test_screego_patch_set_preserves_udp_only_turn(self):
        compose = (DEPLOY_DIR / "docker-compose.yml").read_text(encoding="utf-8")
        self.assertIn('SCREEGO_TURN_ENABLE_TCP: "false"', compose)

        patch = SCREEGO_DIR / "patches" / "0004-partylan-turn-udp-only.patch"
        self.assertTrue(patch.is_file(), "UDP-only TURN patch is not version controlled")
        source = patch.read_text(encoding="utf-8")
        self.assertIn('TurnEnableTCP bool   `default:"true"', source)
        self.assertIn("if conf.TurnEnableTCP {", source)
        self.assertIn("if tcp && r.config.TurnEnableTCP {", source)
        self.assertIn("TestInternalServerDoesNotListenOnTCPWhenDisabled", source)
        self.assertIn("TestTurnAddressesOmitTCPWhenDisabled", source)

    def test_runtime_environment_example_contains_names_without_secret_values(self):
        example = (DEPLOY_DIR / "runtime.env.example").read_text(encoding="utf-8")
        assignments = [
            line.strip()
            for line in example.splitlines()
            if line.strip() and not line.lstrip().startswith("#")
        ]

        self.assertIn("GAMECAST_VOICE_SECRET=", assignments)
        self.assertIn("VOICE_SFU_PUBLIC_IP=", assignments)
        self.assertIn("VOICE_SFU_ICE_SERVERS_JSON=", assignments)
        for assignment in assignments:
            self.assertRegex(assignment, r"^[A-Z][A-Z0-9_]*=$")

    def test_voice_service_log_calls_do_not_reference_sensitive_payloads(self):
        forbidden = ("authorization", "token", "sdp", "candidate", "signal", "payload")
        logging_calls = []
        for source in (SERVER_DIR / "voice-sfu").rglob("*.go"):
            if source.name.endswith("_test.go"):
                continue
            for line_number, line in enumerate(source.read_text(encoding="utf-8").splitlines(), 1):
                if re.search(r"\b(?:log|slog)\.[A-Za-z]+", line):
                    logging_calls.append((source, line_number, line.strip()))

        self.assertTrue(logging_calls)
        for source, line_number, line in logging_calls:
            for term in forbidden:
                self.assertNotIn(
                    term,
                    line.lower(),
                    f"{source}:{line_number} logs sensitive {term}",
                )


if __name__ == "__main__":
    unittest.main()
