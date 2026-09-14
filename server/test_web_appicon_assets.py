import hashlib
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
DEPLOY_DIR = ROOT / "server" / "screego" / "deploy"
APPICON = ROOT / "build" / "appicon.png"
ICON_PATCH = ROOT / "server" / "screego" / "patches" / "0012-partylan-original-appicon.patch"
ORIGINAL_APPICON_SHA256 = "9d07528535d1b685282890bc3bea041edc692bcdbdaea072fc8be5b6a652f768"


class WebAppIconAssetsTests(unittest.TestCase):
    def test_original_desktop_appicon_is_the_web_source_of_truth(self):
        self.assertTrue(APPICON.is_file())
        self.assertEqual(hashlib.sha256(APPICON.read_bytes()).hexdigest(), ORIGINAL_APPICON_SHA256)

    def test_nginx_serves_original_icon_for_current_and_legacy_urls(self):
        nginx = (DEPLOY_DIR / "nginx-screego.conf").read_text(encoding="utf-8")

        for url in ("/appicon.png", "/logo.svg", "/favicon.ico", "/apple-touch-icon.png", "/og-banner.png"):
            pattern = rf"(?s)location = {re.escape(url)}\s*\{{.*?alias /var/www/screego/appicon\.png;.*?Cache-Control"
            self.assertRegex(nginx, pattern)

    def test_native_web_entry_points_use_appicon_png(self):
        patch = ICON_PATCH.read_text(encoding="utf-8")

        self.assertIn('+    <link rel="icon" href="./appicon.png" />', patch)
        self.assertIn('+    <meta property="og:image" content="./appicon.png" />', patch)
        self.assertIn('+    <link rel="apple-touch-icon" href="./appicon.png" />', patch)
        self.assertIn('+                    src="./appicon.png"', patch)


if __name__ == "__main__":
    unittest.main()
