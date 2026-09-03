import asyncio
import tempfile
import unittest

from deploy.vpn_bot_maestro_customer import (
    GB_PACKS,
    PRIMARY_ACTIONS,
    CustomerAPI,
    CustomerBindingStore,
    CustomerFlow,
    NotificationLedger,
    callback_data,
    panel_base_url,
)


class FakeTransport:
    def __init__(self, responses):
        self.responses = list(responses)
        self.calls = []

    async def request(self, method, path, **kwargs):
        self.calls.append((method, path, kwargs))
        return self.responses.pop(0)


class CustomerFlowTests(unittest.TestCase):
    def test_menu_is_exactly_the_five_customer_actions_and_login_is_visible(self):
        flow = CustomerFlow(CustomerAPI("https://panel.invalid", "token", FakeTransport([])), "maestro-login", "subtoken")
        self.assertEqual(PRIMARY_ACTIONS, (
            "Моя подписка и баланс",
            "Продлить 30 дней — 400 ₽",
            "Купить гигабайты",
            "Подключить устройство",
            "Помощь",
        ))
        self.assertIn("maestro-login", flow.menu_text())
        self.assertNotIn("trial", flow.menu_text().lower())
        self.assertEqual(GB_PACKS, ((5, 100), (20, 300), (50, 600), (100, 1000)))
        self.assertEqual([label for label, _ in flow.menu_actions()], list(PRIMARY_ACTIONS))
        self.assertTrue(all(value.startswith("mc:") for _, value in flow.menu_actions()))

    def test_callback_data_is_opaque_and_never_carries_customer_secrets(self):
        value = callback_data("pay", "order_opaque_123")
        self.assertEqual(value, "mc:pay:order_opaque_123")
        for forbidden in ("maestro-login", "subtoken", "https://", "uuid", "400"):
            self.assertNotIn(forbidden, value)
        with self.assertRaises(ValueError):
            callback_data("pay", "a" * 60)

    def test_https_is_the_default_and_http_requires_an_explicit_loopback_endpoint(self):
        self.assertEqual(panel_base_url(), "https://localhost:8910")
        self.assertEqual(panel_base_url("http://127.0.0.1:8910"), "http://127.0.0.1:8910")
        with self.assertRaises(ValueError):
            panel_base_url("http://panel.example")

    def test_purchase_claim_uses_durable_customer_order_api(self):
        transport = FakeTransport([
            {"order_id": "order_opaque_123", "amount_minor": 30000, "bytes": 20_000_000_000, "payment_state": "PENDING"},
            {"order_id": "order_opaque_123", "payment_state": "AWAITING_CONFIRM"},
        ])
        flow = CustomerFlow(CustomerAPI("https://panel.invalid", "subtoken", transport), "maestro-login", "subtoken")
        order = asyncio.run(flow.buy_gigabytes(20, "intent_20"))
        asyncio.run(flow.claim_paid(order["order_id"]))
        self.assertEqual([call[:2] for call in transport.calls], [
            ("POST", "/order"),
            ("POST", "/order/order_opaque_123/paid-claim"),
        ])
        self.assertEqual(transport.calls[0][2]["json"], {"product_id": "wl-gb-20-v1", "sub_token": "subtoken"})
        self.assertNotIn("maestro-login", str(transport.calls))

    def test_renewal_stays_on_ordinary_access_flow_and_payment_comment_is_login_only(self):
        transport = FakeTransport([{"order_id": "ordinary_opaque", "amount_minor": 40000}])
        flow = CustomerFlow(CustomerAPI("https://panel.invalid", "subtoken", transport), "maestro-login", "subtoken")
        asyncio.run(flow.renew_access())
        self.assertEqual(transport.calls[0][2]["json"], {
            "tariff": "1m", "sub_token": "subtoken", "login": "maestro-login",
        })
        payment = flow.payment_instructions()
        self.assertIn("maestro-login", payment)
        self.assertNotIn("VPN", payment.upper())

    def test_gigabyte_purchase_uses_one_stable_opaque_intent_key_per_callback(self):
        transport = FakeTransport([{"order_id": "first"}, {"order_id": "retry"}, {"order_id": "next"}])
        flow = CustomerFlow(CustomerAPI("https://panel.invalid", "subtoken", transport), "maestro-login", "subtoken")
        asyncio.run(flow.buy_gigabytes(20, "intent_one"))
        asyncio.run(flow.buy_gigabytes(20, "intent_one"))
        asyncio.run(flow.buy_gigabytes(20, "intent_two"))
        headers = [call[2]["headers"] for call in transport.calls]
        self.assertEqual([header["Idempotency-Key"] for header in headers], [
            "tg-order-intent_one", "tg-order-intent_one", "tg-order-intent_two",
        ])
        self.assertTrue(all(header["Authorization"] == "Bearer subtoken" for header in headers))

    def test_balance_hides_cdn_before_purchase_or_after_admin_disable_and_blocks_expired_access(self):
        flow = CustomerFlow(CustomerAPI("https://panel.invalid", "subtoken", FakeTransport([])), "maestro-login", "subtoken")
        hidden = flow.balance_text({"primary_access_state": "active", "publication_verdict": "DISABLED", "available_bytes": 0})
        disabled = flow.balance_text({"primary_access_state": "active", "publication_verdict": "DISABLED", "available_bytes": 5_000_000_000})
        expired = flow.balance_text({"primary_access_state": "expired", "publication_verdict": "READY", "available_bytes": 5_000_000_000})
        self.assertNotIn("CDN/LTE", hidden)
        self.assertIn("обычная подписка", disabled.lower())
        self.assertIn("5 ГБ", disabled)
        self.assertIn("сначала продлите", expired)

    def test_balance_uses_the_task8_whitelist_balance_contract(self):
        transport = FakeTransport([{"primary_access_state": "ACTIVE", "publication_verdict": "DISABLED"}])
        flow = CustomerFlow(CustomerAPI("https://panel.invalid", "subtoken", transport), "maestro-login", "subtoken")
        self.assertIn("Обычная подписка", asyncio.run(flow.show_balance()))
        self.assertEqual(transport.calls[0][:2], ("GET", "/account/whitelist-balance"))

    def test_delivery_uses_task9_incy_result_and_exact_happ_three_step_fallback(self):
        transport = FakeTransport([
            {"client": "incy", "format": "INCY_ONE_TAP", "url": "https://safe.invalid/one-tap"},
            {"client": "happ", "format": "COPY_HTTPS_URL_AND_QR", "url": "https://safe.invalid/sub/token"},
        ])
        flow = CustomerFlow(CustomerAPI("https://panel.invalid", "subtoken", transport), "maestro-login", "subtoken")
        incy = asyncio.run(flow.delivery("incy"))
        happ = asyncio.run(flow.delivery("happ"))
        self.assertEqual(incy["button_url"], "https://safe.invalid/one-tap")
        self.assertEqual(happ["steps"], (
            "1. Скопируйте HTTPS-ссылку.",
            "2. Откройте Happ.",
            "3. Вставьте ссылку или отсканируйте QR.",
        ))

    def test_notifications_are_deduplicated_by_durable_event_key(self):
        with tempfile.TemporaryDirectory() as directory:
            ledger = NotificationLedger(f"{directory}/events.sqlite3")
            self.assertTrue(ledger.first("order_opaque_123:80"))
            self.assertFalse(ledger.first("order_opaque_123:80"))
            self.assertTrue(ledger.first("order_opaque_123:suspended"))

    def test_binding_requires_bearer_proof_then_persists_only_for_that_chat(self):
        with tempfile.TemporaryDirectory() as directory:
            store = CustomerBindingStore(f"{directory}/customer.sqlite3")
            store.bind(1001, "maestro-login", "customer-bearer")
            self.assertEqual(store.get(1001), ("maestro-login", "customer-bearer"))
            self.assertIsNone(store.get(1002))
