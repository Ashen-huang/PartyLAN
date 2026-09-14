import json
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
WEB_BRAND_PATCH = ROOT / "server" / "screego" / "patches" / "0013-partylan-lowercase-brand.patch"


class PartylanBrandingTests(unittest.TestCase):
    def test_desktop_compatibility_client_uses_the_public_partylan_name(self):
        wails = json.loads((ROOT / "wails.json").read_text(encoding="utf-8"))

        self.assertEqual(wails["name"], "partylan")
        self.assertEqual(wails["outputfilename"], "partylan")
        self.assertIn('<title>partylan</title>', (ROOT / "frontend" / "index.html").read_text(encoding="utf-8"))
        self.assertIn('Title:     "partylan"', (ROOT / "main.go").read_text(encoding="utf-8"))

    def test_web_patch_keeps_the_public_name_lowercase(self):
        patch = WEB_BRAND_PATCH.read_text(encoding="utf-8")

        self.assertIn('+    <title>partylan</title>', patch)
        self.assertIn('+                        <Typography style={{flex: 1}}>登录 partylan</Typography>', patch)
        self.assertIn('+                    alt="partylan"', patch)
        self.assertNotIn('+    <title>PartyLAN</title>', patch)


if __name__ == "__main__":
    unittest.main()
