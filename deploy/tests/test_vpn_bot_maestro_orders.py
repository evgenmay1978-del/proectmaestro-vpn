import pathlib
import unittest


class OrdersAdapterTests(unittest.TestCase):
    def test_admin_callbacks_keep_opaque_legacy_shape_and_tls_verification_is_not_disabled(self):
        source = pathlib.Path("deploy/vpn_bot_maestro_orders.py").read_text(encoding="utf-8")
        self.assertIn('startswith("moconf:")', source)
        self.assertIn('startswith("mocancel:")', source)
        self.assertNotIn("verify=False", source)
        self.assertIn("https://localhost:8910", source)

