"""Small customer-facing MaestroVPN Telegram flow.

This module deliberately keeps Telegram presentation separate from the panel
contract.  A bot integration supplies a trusted ``CustomerFlow`` for the
Telegram user; neither a login nor a subscription token is placed in callback
data.
"""
import os
import re
import sqlite3
from pathlib import Path
from urllib.parse import urlparse

import httpx


PRIMARY_ACTIONS = (
    "Моя подписка и баланс",
    "Продлить 30 дней — 400 ₽",
    "Купить гигабайты",
    "Подключить устройство",
    "Помощь",
)
GB_PACKS = ((5, 100), (20, 300), (50, 600), (100, 1000))
GB_PRODUCT_IDS = {5: "wl-gb-5-v1", 20: "wl-gb-20-v1", 50: "wl-gb-50-v1", 100: "wl-gb-100-v1"}
_OPAQUE = re.compile(r"^[A-Za-z0-9_-]{1,96}$")


def panel_base_url(configured: str | None = None) -> str:
    """Use TLS by default; plain HTTP is only an explicit loopback setting."""
    value = (configured or "https://localhost:8910").rstrip("/")
    parsed = urlparse(value)
    if parsed.scheme == "https" and parsed.netloc:
        return value
    if parsed.scheme == "http" and parsed.hostname in {"127.0.0.1", "localhost", "::1"}:
        return value
    raise ValueError("MAESTRO_URL must use HTTPS or explicit loopback HTTP")


def callback_data(action: str, opaque_id: str) -> str:
    """Encode only a small action and server-generated opaque identifier."""
    if not _OPAQUE.fullmatch(action) or not _OPAQUE.fullmatch(opaque_id):
        raise ValueError("callback values must be opaque identifiers")
    return f"mc:{action}:{opaque_id}"


class CustomerAPI:
    """Narrow API adapter; ``transport`` makes the public contract mockable."""

    def __init__(self, base_url: str | None, customer_token: str, transport=None):
        self.base_url = panel_base_url(base_url)
        self.customer_token = customer_token
        self.transport = transport

    async def request(self, method: str, path: str, **kwargs):
        headers = dict(kwargs.pop("headers", {}))
        headers.setdefault("Authorization", f"Bearer {self.customer_token}")
        if self.transport is not None:
            return await self.transport.request(method, path, headers=headers, **kwargs)
        async with httpx.AsyncClient(timeout=20) as client:
            response = await client.request(method, self.base_url + path, headers=headers, **kwargs)
        response.raise_for_status()
        return response.json()

    async def create_order(self, payload: dict):
        return await self.request("POST", "/order", json=payload)

    async def claim_paid(self, order_id: str):
        return await self.request("POST", f"/order/{order_id}/paid-claim", json={})

    async def owner_decision(self, order_id: str, confirmed: bool):
        decision = "confirm" if confirmed else "reject"
        return await self.request("POST", f"/admin/order/{order_id}/{decision}", json={})

    async def balance(self):
        return await self.request("GET", "/account/whitelist-balance")

    async def delivery(self, client: str):
        return await self.request("POST", "/account/subscription-delivery", json={"client": client})


class NotificationLedger:
    """One durable key per notification, including thresholds and transitions."""

    def __init__(self, path: str):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with sqlite3.connect(self.path) as connection:
            connection.execute(
                "CREATE TABLE IF NOT EXISTS customer_notification_events (event_key TEXT PRIMARY KEY)"
            )

    def first(self, event_key: str) -> bool:
        with sqlite3.connect(self.path) as connection:
            cursor = connection.execute(
                "INSERT OR IGNORE INTO customer_notification_events(event_key) VALUES (?)", (event_key,)
            )
            return cursor.rowcount == 1


class CustomerFlow:
    def __init__(self, api: CustomerAPI, login: str, sub_token: str):
        self.api = api
        self.login = login
        self.sub_token = sub_token

    def menu_text(self) -> str:
        return f"Maestro login: {self.login}\nВыберите действие:"

    def menu_actions(self) -> tuple[tuple[str, str], ...]:
        return tuple(
            (label, callback_data(action, "menu"))
            for label, action in zip(PRIMARY_ACTIONS, ("balance", "renew", "gigabytes", "devices", "help"))
        )

    def payment_instructions(self) -> str:
        return f"В комментарии к переводу укажите только ваш Maestro login: {self.login}"

    async def renew_access(self):
        return await self.api.create_order(
            {"tariff": "40000", "sub_token": self.sub_token, "login": self.login}
        )

    async def buy_gigabytes(self, gigabytes: int):
        if gigabytes not in {pack[0] for pack in GB_PACKS}:
            raise ValueError("unsupported gigabyte pack")
        return await self.api.create_order(
            {"product_id": GB_PRODUCT_IDS[gigabytes], "sub_token": self.sub_token}
        )

    async def claim_paid(self, order_id: str):
        return await self.api.claim_paid(order_id)

    async def owner_decision(self, order_id: str, confirmed: bool):
        return await self.api.owner_decision(order_id, confirmed)

    async def reject_order(self, order_id: str):
        return await self.owner_decision(order_id, confirmed=False)

    def balance_text(self, balance: dict) -> str:
        available = int(balance.get("available_bytes") or 0)
        gigabytes = available // 1_000_000_000
        primary = balance.get("primary_access_state", "")
        publication = balance.get("publication_verdict", "DISABLED")
        if primary != "ACTIVE":
            return f"Основной доступ истёк: сначала продлите его. Сохранённый баланс: {gigabytes} ГБ."
        if publication == "DISABLED":
            return f"Обычная подписка активна. Сохранённый баланс: {gigabytes} ГБ."
        return f"Обычная подписка активна. CDN/LTE баланс: {gigabytes} ГБ."

    async def show_balance(self) -> str:
        return self.balance_text(await self.api.balance())

    async def delivery(self, client: str) -> dict:
        result = await self.api.delivery(client)
        if client == "incy" and result.get("format") == "INCY_ONE_TAP":
            return {"button_url": result["url"], "label": "Открыть в Incy"}
        if client == "happ" and result.get("format") == "COPY_HTTPS_URL_AND_QR":
            return {
                "url": result["url"],
                "steps": (
                    "1. Скопируйте HTTPS-ссылку.",
                    "2. Откройте Happ.",
                    "3. Вставьте ссылку или отсканируйте QR.",
                ),
            }
        raise ValueError("unexpected subscription delivery result")

    def support_text(self) -> str:
        return "Напишите в поддержку и укажите ваш Maestro login."


def configured_customer_api(customer_token: str) -> CustomerAPI:
    return CustomerAPI(os.getenv("MAESTRO_URL"), customer_token)
