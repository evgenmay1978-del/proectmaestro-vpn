"""CDN-only checkout handled by the existing customer bot router.

The native panel owns orders, payment decisions and credit. This small local
table only binds Telegram presentation to orders created in this bot; it is
not a payment ledger and has no background worker or poller.
"""
import asyncio
import json
import os
from pathlib import Path
import re
import secrets
import sqlite3
import stat
from urllib.parse import urlsplit

import httpx

try:
    from .maestro_customer_cdn_actions import build_topup_callback, topup_admin_request
except ImportError:
    from maestro_customer_cdn_actions import build_topup_callback, topup_admin_request

PRODUCTS = {1: "wl-gb-1-20260906", 5: "wl-gb-5-20260906", 10: "wl-gb-10-20260906",
            25: "wl-gb-25-20260906", 50: "wl-gb-50-20260906"}
LEGACY_PRODUCTS = frozenset({"wl-gb-5-v1", "wl-gb-20-v1", "wl-gb-50-v1", "wl-gb-100-v1"})
OPAQUE = re.compile(r"[A-Za-z0-9_-]{1,58}\Z")


def enabled():
    return os.getenv("MAESTRO_CUSTOMER_CDN_PURCHASES_ENABLE") == "1"


def test_customer_enabled(login, chat_id):
    return (os.getenv("MAESTRO_CUSTOMER_CDN_TEST_LOGIN") == "cdntest0906"
            and login == "cdntest0906" and type(chat_id) is int and chat_id in owner_ids())


def callback(action, identity):
    if action not in {"paid", "cf", "cr", "gigabytes"} | {"gb" + str(gb) for gb in PRODUCTS} or not OPAQUE.fullmatch(identity):
        raise ValueError("invalid CDN callback")
    value = "mc:" + action + ":" + identity
    if len(value.encode()) > 64:
        raise ValueError("CDN callback too long")
    return value


def native_url():
    value = os.environ["MAESTRO_CUSTOMER_CDN_URL"].rstrip("/")
    parsed = urlsplit(value)
    if (parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.password
            or parsed.query or parsed.fragment or (parsed.scheme == "http" and parsed.hostname not in ("127.0.0.1", "localhost", "::1"))):
        raise ValueError("invalid native CDN endpoint")
    return value


def owner_ids():
    values = os.environ.get("MAESTRO_CUSTOMER_CDN_OWNER_IDS", "").split(",")
    if not values or not all(re.fullmatch(r"[1-9][0-9]{0,15}", value.strip()) for value in values):
        raise ValueError("CDN owner IDs are required")
    return tuple(sorted({int(value.strip()) for value in values}))


def admin_token():
    path = Path(os.environ["MAESTRO_CUSTOMER_CDN_ADMIN_TOKEN_FILE"])
    info = path.lstat()
    if not path.is_absolute() or not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077 or not 0 < info.st_size <= 4096:
        raise ValueError("protected native admin token is required")
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), "r", encoding="utf-8") as source:
        value = source.read(4097).strip()
    if not value or len(value) > 4096 or any(char.isspace() for char in value):
        raise ValueError("invalid native admin token")
    return value


class CDNCheckout:
    def __init__(self, binding_store):
        self.path = binding_store.path
        self.locks = {}
        if not enabled() and os.getenv("MAESTRO_CUSTOMER_CDN_TEST_LOGIN") != "cdntest0906":
            return
        native_url()
        owner_ids()
        admin_token()
        with sqlite3.connect(self.path) as connection:
            connection.execute("""CREATE TABLE IF NOT EXISTS customer_cdn_orders (
                order_id TEXT PRIMARY KEY, chat_id INTEGER NOT NULL, login TEXT NOT NULL,
                product_id TEXT NOT NULL, amount_minor INTEGER NOT NULL, bytes INTEGER NOT NULL,
                payment_state TEXT NOT NULL, notified_owners TEXT NOT NULL DEFAULT '[]',
                customer_notified INTEGER NOT NULL DEFAULT 0)""")

    def order(self, order_id):
        if not OPAQUE.fullmatch(order_id):
            raise ValueError("invalid CDN order")
        with sqlite3.connect(self.path) as connection:
            connection.row_factory = sqlite3.Row
            row = connection.execute("SELECT * FROM customer_cdn_orders WHERE order_id=?", (order_id,)).fetchone()
        if row is None or row["product_id"] not in set(PRODUCTS.values()) | LEGACY_PRODUCTS:
            raise ValueError("unknown CDN order")
        return dict(row)

    def save(self, view, chat_id, login):
        fields = (str(view["order_id"]), chat_id, login, view["product_id"], view["amount_minor"], view["bytes"], view["payment_state"])
        if not OPAQUE.fullmatch(fields[0]):
            raise ValueError("invalid native order identity")
        with sqlite3.connect(self.path) as connection:
            connection.execute("INSERT OR IGNORE INTO customer_cdn_orders(order_id,chat_id,login,product_id,amount_minor,bytes,payment_state) VALUES(?,?,?,?,?,?,?)", fields)
        row = self.order(fields[0])
        if tuple(row[key] for key in ("order_id", "chat_id", "login", "product_id", "amount_minor", "bytes")) != fields[:6]:
            raise ValueError("CDN order binding conflict")
        return row

    def update(self, order_id, *, state=None, notified_owner=None, customer_notified=False):
        with sqlite3.connect(self.path) as connection:
            if state:
                connection.execute("UPDATE customer_cdn_orders SET payment_state=? WHERE order_id=?", (state, order_id))
            if notified_owner:
                row = connection.execute("SELECT notified_owners FROM customer_cdn_orders WHERE order_id=?", (order_id,)).fetchone()
                owners = set(json.loads(row[0])); owners.add(notified_owner)
                connection.execute("UPDATE customer_cdn_orders SET notified_owners=? WHERE order_id=?", (json.dumps(sorted(owners)), order_id))
            if customer_notified:
                connection.execute("UPDATE customer_cdn_orders SET customer_notified=1 WHERE order_id=?", (order_id,))

    async def request(self, method, path, token, **kwargs):
        headers = {"Authorization": "Bearer " + token, **kwargs.pop("headers", {})}
        async with httpx.AsyncClient(timeout=20, follow_redirects=False) as client:
            response = await client.request(method, native_url() + path, headers=headers, **kwargs)
        response.raise_for_status()
        return response.json()

    async def catalog(self, flow):
        value = await self.request("GET", "/order/catalog", flow.sub_token)
        rows = {}
        for product in value.get("products", []):
            if product.get("id") not in PRODUCTS.values():
                continue
            gigabytes = next(gb for gb, product_id in PRODUCTS.items() if product_id == product["id"])
            if (product.get("currency") != "RUB" or product.get("bytes") != gigabytes * 1_000_000_000
                    or type(product.get("amount_minor")) is not int or product["amount_minor"] <= 0):
                raise ValueError("invalid native CDN catalog")
            rows[gigabytes] = product
        if not rows:
            raise ValueError("native CDN catalog unavailable")
        return rows

    @staticmethod
    def keyboard(rows):
        from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup
        return InlineKeyboardMarkup(inline_keyboard=[[InlineKeyboardButton(text=text, callback_data=value)] for text, value in rows])

    async def purchase(self, cb, flow, gigabytes, intent):
        products = await self.catalog(flow)
        if gigabytes not in products or not OPAQUE.fullmatch(intent):
            raise ValueError("unsupported CDN pack")
        instructions = await self.request("GET", "/order/tariffs", flow.sub_token)
        phone, pay_url = str(instructions.get("sbp_phone") or "").strip(), str(instructions.get("pay_url") or "").strip()
        if not phone and not pay_url:
            raise ValueError("payment details unavailable")
        product = products[gigabytes]
        view = await self.request("POST", "/order", flow.sub_token,
            json={"product_id": product["id"], "sub_token": flow.sub_token},
            headers={"Idempotency-Key": "tg-cdn-order-" + intent})
        if (view.get("product_id") != product["id"] or view.get("amount_minor") != product["amount_minor"]
                or view.get("bytes") != product["bytes"] or view.get("currency") != "RUB"
                or view.get("payment_state") not in ("created", "payment_claimed")):
            raise ValueError("native CDN order mismatch")
        row = self.save(view, cb.message.chat.id, flow.login)
        if row["payment_state"] in ("confirmed", "canceled"):
            await cb.message.answer("По этому заказу уже принято решение. Для новой покупки снова откройте выбор пакетов.")
            return
        details = "\n".join(value for value in (("СБП: " + phone) if phone else "", pay_url) if value)
        instruction = ("Тестовая покупка: реальный перевод не нужен. Нажмите «Я оплатил»."
            if test_customer_enabled(flow.login, cb.message.chat.id) else "После перевода нажмите «Я оплатил».")
        await cb.message.answer(f"CDN: {gigabytes} ГБ — {product['amount_minor'] / 100:g} ₽.\n{details}\n"
            f"Комментарий к переводу: {flow.login}\n{instruction}", parse_mode=None,
            reply_markup=self.keyboard([("Я оплатил", callback("paid", row["order_id"]))]))

    async def paid(self, cb, flow, order_id):
        async with self.locks.setdefault(order_id, asyncio.Lock()):
            row = self.order(order_id)
            if row["chat_id"] != cb.message.chat.id or row["login"] != flow.login:
                raise ValueError("CDN order belongs to another customer")
            if row["payment_state"] in ("confirmed", "canceled"):
                await cb.message.answer("По заявке уже принято решение. Откройте «Моя подписка и баланс».")
                return
            view = await self.request("POST", "/order/" + order_id + "/paid-claim", flow.sub_token, json={},
                headers={"Idempotency-Key": "tg-cdn-claim-" + order_id})
            if view.get("order_id") != order_id or view.get("product_id") != row["product_id"] or view.get("payment_state") != "payment_claimed":
                raise ValueError("native CDN claim mismatch")
            self.update(order_id, state="payment_claimed")
            notified = set(json.loads(row["notified_owners"]))
            for owner in owner_ids():
                if owner in notified:
                    continue
                try:
                    instruction = ("Тестовая заявка без перевода: подтвердите для проверки начисления."
                        if test_customer_enabled(row["login"], row["chat_id"])
                        else "Подтвердите только после получения перевода.")
                    await cb.bot.send_message(owner, f"CDN — заявка об оплате\nЛогин: {row['login']}\n"
                        f"Пакет: {row['bytes'] // 1_000_000_000} ГБ\nСумма: {row['amount_minor'] / 100:g} ₽\n"
                        f"Заказ: {order_id}\n{instruction}", parse_mode=None,
                        reply_markup=self.keyboard([("Подтвердить оплату", callback("cf", order_id)), ("Отклонить", callback("cr", order_id))]))
                    self.update(order_id, notified_owner=owner); notified.add(owner)
                except Exception:
                    continue
            if not notified:
                await cb.message.answer("Заявка сохранена, но сообщение владельцу пока не доставлено. Нажмите «Я оплатил» ещё раз; повторный перевод не нужен.")
                return
            await cb.message.answer("Заявка об оплате передана владельцу. После подтверждения гигабайты появятся в балансе.")

    async def decide(self, cb, action, order_id):
        if cb.from_user.id not in owner_ids() or cb.message.chat.id != cb.from_user.id:
            await cb.answer("Только владелец", show_alert=True)
            return
        await cb.answer()
        async with self.locks.setdefault(order_id, asyncio.Lock()):
            row = self.order(order_id)
            decision = "confirm" if action == "cf" else "reject"
            state = "confirmed" if decision == "confirm" else "canceled"
            if row["payment_state"] in ("confirmed", "canceled") and row["payment_state"] != state:
                await cb.message.answer("По заявке уже принято другое решение.")
                return
            if row["payment_state"] != state:
                request = topup_admin_request(build_topup_callback(decision, order_id))
                view = await self.request("POST", request.path, admin_token(), json={},
                    headers={"Idempotency-Key": request.idempotency_key})
                if view.get("order_id") != order_id or view.get("product_id") != row["product_id"] or view.get("payment_state") != state:
                    raise ValueError("native CDN decision mismatch")
                self.update(order_id, state=state)
            result = f"Начислено {row['bytes'] // 1_000_000_000} ГБ CDN." if state == "confirmed" else "Заявка на покупку гигабайтов отклонена."
            if not row["customer_notified"]:
                try:
                    await cb.bot.send_message(row["chat_id"], result + " Откройте /maestro → «Моя подписка и баланс».", parse_mode=None)
                    self.update(order_id, customer_notified=True)
                except Exception:
                    await cb.message.answer(result + " Уведомление клиенту не доставлено. Повторите эту кнопку для доставки; повторного начисления не будет.")
                    return
            await cb.message.edit_text((cb.message.text or "CDN заявка") + "\n\n" + result, parse_mode=None, reply_markup=None)

    async def dispatch(self, cb, action, identity, flow=None):
        if cb.message.chat.type != "private" or cb.message.chat.id != cb.from_user.id:
            await cb.answer("Откройте личный чат с ботом.", show_alert=True)
            return
        if (not enabled() and action not in ("cf", "cr")
                and not test_customer_enabled(getattr(flow, "login", None), cb.from_user.id)):
            await cb.answer("Покупка CDN пока недоступна.", show_alert=True)
            return
        try:
            if action in ("cf", "cr"):
                await self.decide(cb, action, identity)
                return
            await cb.answer()
            if flow is None:
                raise ValueError("customer binding required")
            if action == "gigabytes":
                products = await self.catalog(flow)
                keyboard = self.keyboard([(f"{gb} ГБ — {product['amount_minor'] / 100:g} ₽", callback("gb" + str(gb), secrets.token_urlsafe(9))) for gb, product in sorted(products.items())])
                await cb.message.answer("Выберите пакет CDN:", reply_markup=keyboard)
            elif action.startswith("gb") and action[2:].isdigit():
                await self.purchase(cb, flow, int(action[2:]), identity)
            elif action == "paid":
                await self.paid(cb, flow, identity)
            else:
                raise ValueError("unsupported CDN action")
        except Exception:
            await cb.message.answer("Не удалось завершить действие. Повторите эту же кнопку; повторный перевод не нужен.")
