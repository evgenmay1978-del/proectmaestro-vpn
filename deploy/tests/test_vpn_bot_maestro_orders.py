import pathlib
import unittest


class OrdersAdapterTests(unittest.TestCase):
    def test_admin_callbacks_keep_opaque_legacy_shape_and_tls_verification_is_not_disabled(self):
        source = pathlib.Path("deploy/vpn_bot_maestro_orders.py").read_text(encoding="utf-8")
        self.assertIn('startswith("moconf:")', source)
        self.assertIn('startswith("mocancel:")', source)
        self.assertNotIn("verify=False", source)
        self.assertIn("https://localhost:8910", source)

    def test_admin_callbacks_use_authenticated_canonical_routes_and_idempotency(self):
        source = pathlib.Path("deploy/vpn_bot_maestro_orders.py").read_text(encoding="utf-8")
        self.assertIn('f"{MAESTRO_URL}/admin/order/{order_id}/confirm"', source)
        self.assertIn('f"{MAESTRO_URL}/admin/order/{order_id}/reject"', source)
        self.assertIn('"Idempotency-Key": f"telegram-admin-confirm-{order_id}"', source)
        self.assertIn('"Idempotency-Key": f"telegram-admin-reject-{order_id}"', source)
        self.assertNotIn("owner_decision", pathlib.Path("deploy/vpn_bot_maestro_customer.py").read_text(encoding="utf-8"))

    def test_existing_deployment_entrypoint_registers_the_customer_child_router(self):
        source = pathlib.Path("deploy/vpn_bot_maestro_orders.py").read_text(encoding="utf-8")
        self.assertIn("build_customer_router_from_env", source)
        self.assertIn("router.include_router(customer_router)", source)
