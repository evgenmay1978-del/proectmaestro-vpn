import pathlib
import unittest


def order_actions_module():
    try:
        from deploy import vpn_bot_maestro_order_actions
    except ImportError as exc:
        raise AssertionError("pure owner callback contract is missing") from exc
    return vpn_bot_maestro_order_actions


class OrdersAdapterTests(unittest.TestCase):
    def test_admin_callbacks_keep_opaque_legacy_shape_and_tls_verification_is_not_disabled(self):
        source = pathlib.Path("deploy/vpn_bot_maestro_orders.py").read_text(encoding="utf-8")
        self.assertIn('startswith("moconf:")', source)
        self.assertIn('startswith("mocancel:")', source)
        self.assertNotIn("verify=False", source)
        self.assertIn("https://localhost:8910", source)

    def test_admin_callbacks_use_authenticated_canonical_routes_and_idempotency(self):
        source = pathlib.Path("deploy/vpn_bot_maestro_orders.py").read_text(encoding="utf-8")
        self.assertIn('f"{MAESTRO_URL}/admin/order/confirm"', source)
        self.assertIn('json={"order_id": order_id}', source)
        self.assertIn('f"{MAESTRO_URL}/admin/order/{order_id}/reject"', source)
        self.assertIn('"Idempotency-Key": f"telegram-admin-confirm-{order_id}"', source)
        self.assertIn('"Idempotency-Key": f"telegram-admin-reject-{order_id}"', source)
        self.assertNotIn("owner_decision", pathlib.Path("deploy/vpn_bot_maestro_customer.py").read_text(encoding="utf-8"))

    def test_existing_deployment_entrypoint_registers_the_customer_child_router(self):
        source = pathlib.Path("deploy/vpn_bot_maestro_orders.py").read_text(encoding="utf-8")
        self.assertIn("build_customer_router_from_env", source)
        self.assertIn("router.include_router(customer_router)", source)

    def test_aclsub_login_callback_is_legacy_parser_only_and_never_generated(self):
        source = pathlib.Path("deploy/vpn_bot_maestro_orders.py").read_text(encoding="utf-8")
        self.assertEqual(source.count('"aclsub:"'), 1)
        self.assertNotIn("callback_data=f\"aclsub:", source)

    def test_whitelist_topup_callbacks_are_opaque_and_route_to_dynamic_admin_decisions(self):
        actions = order_actions_module()
        order_id = "whitelist-topup-order_0123456789abcdef0123456789abcdef"

        confirm = actions.build_topup_callback("confirm", order_id)
        reject = actions.build_topup_callback("reject", order_id)

        self.assertEqual(confirm, f"mwcf:{order_id}")
        self.assertEqual(reject, f"mwrj:{order_id}")
        self.assertLessEqual(len(confirm.encode("utf-8")), 64)
        self.assertLessEqual(len(reject.encode("utf-8")), 64)
        self.assertEqual(
            actions.topup_admin_request(confirm),
            actions.TopUpAdminRequest(
                decision="confirm",
                order_id=order_id,
                path=f"/admin/order/{order_id}/confirm",
                idempotency_key=f"telegram-admin-topup-confirm-{order_id}",
            ),
        )
        self.assertEqual(
            actions.topup_admin_request(reject),
            actions.TopUpAdminRequest(
                decision="reject",
                order_id=order_id,
                path=f"/admin/order/{order_id}/reject",
                idempotency_key=f"telegram-admin-topup-reject-{order_id}",
            ),
        )
        self.assertEqual(actions.topup_admin_request(confirm), actions.topup_admin_request(confirm))

    def test_whitelist_topup_callbacks_reject_nonopaque_or_oversized_values(self):
        actions = order_actions_module()
        for unsafe_id in ("", "customer@example.test", "https://example.test/sub/token", "a/b", "a:b", "x" * 60):
            with self.subTest(unsafe_id=unsafe_id):
                with self.assertRaises(ValueError):
                    actions.build_topup_callback("confirm", unsafe_id)
        with self.assertRaises(ValueError):
            actions.build_topup_callback("unknown", "order-1")
        with self.assertRaises(ValueError):
            actions.topup_admin_request("mwcf:")

    def test_admin_delivery_choices_use_backend_descriptors_without_manual_suffixes(self):
        actions = order_actions_module()
        builder = getattr(actions, "admin_delivery_choices", None)
        self.assertIsNotNone(builder, "admin delivery choice builder is missing")
        choices = builder({
            "incy": {"client": "incy", "format": "INCY_ONE_TAP", "url": "incy://fixture"},
            "happ": {"client": "happ", "format": "COPY_HTTPS_URL_AND_QR", "url": "https://safe.invalid/sub/token?format=links"},
            "karing": {"client": "karing", "format": "KARING_INSTALL_CONFIG", "url": "karing://install-config?fixture"},
        })
        self.assertEqual(choices.incy_url, "incy://fixture")
        self.assertEqual(choices.happ_url, "https://safe.invalid/sub/token?format=links")
        self.assertEqual(choices.karing_url, "karing://install-config?fixture")
