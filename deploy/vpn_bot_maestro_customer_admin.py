"""CDN admin presentation over the existing order ledger and panel commands."""
import hashlib
import re
import secrets
import sqlite3
import time

try:
    from .maestro_customer_entry import ui_is_admin
    from .maestro_customer_cdn import admin_token
except ImportError:
    from maestro_customer_entry import ui_is_admin
    from maestro_customer_cdn import admin_token

_ACCOUNTS = {}


def account_key(login):
    key = hashlib.sha256(("maestro-cdn-admin:" + login).encode()).hexdigest()[:24]
    _ACCOUNTS[key] = login
    return key


def customer_admin_button(login):
    from aiogram.types import InlineKeyboardButton
    return InlineKeyboardButton(text="📶 CDN · баланс / добавить ГБ",
                                callback_data="mc:acard:" + account_key(login))


def keyboard(rows):
    from aiogram.types import InlineKeyboardButton as B, InlineKeyboardMarkup
    return InlineKeyboardMarkup(inline_keyboard=[
        [B(text=label, callback_data=value) for label, value in row] for row in rows])


class CustomerAdmin:
    def __init__(self, checkout):
        self.checkout = checkout
        self.prompts = {}
        self.intents = {}
        self.pages = {}

    async def request(self, method, path, **kwargs):
        return await self.checkout.request(method, path, admin_token(), **kwargs)

    async def card(self, message, key):
        login = _ACCOUNTS.get(key)
        if login is None:
            await message.answer("Карточка устарела после обновления бота. Откройте клиента заново.")
            return
        data = await self.request("GET", "/admin/customer/whitelist-balance", params={"login": login})
        available = int(data.get("remaining_bytes", 0)) / 1_000_000_000
        text = (f"📶 CDN · {login}\n\n"
                f"Остаток: {available:g} ГБ\n"
                f"Обычная подписка: {'активна' if data.get('primary_active') else 'неактивна'}\n"
                f"Выдача CDN: {'включена' if data.get('enabled') else 'выключена'}\n\n"
                "Добавление ГБ меняет только баланс CDN. Дни VPN и оплаченные заявки остаются отдельно.")
        await message.answer(text, parse_mode=None, reply_markup=keyboard([
            [("➕ Добавить ГБ", "mc:acredit:" + key)],
            [("🔄 Обновить", "mc:acard:" + key)],
            [("💳 Заявки CDN", "mc:admin:orders")],
            [("🛠 Управление", "mc:admin:menu")]]))

    async def orders(self, message):
        with sqlite3.connect(self.checkout.path) as db:
            db.row_factory = sqlite3.Row
            exists = db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='customer_cdn_orders'").fetchone()
            rows = db.execute(
                "SELECT * FROM customer_cdn_orders WHERE payment_state NOT IN ('confirmed','canceled') "
                "ORDER BY rowid DESC LIMIT 20").fetchall() if exists else []
        await message.answer(
            "💳 Последние незавершённые заявки CDN\n"
            "Подтверждайте только фактически полученный перевод. "
            "Обычные платежи находятся в очереди вашего бота.",
            reply_markup=keyboard([[("🛠 Управление", "mc:admin:menu")]]), parse_mode=None)
        if not rows:
            await message.answer("Незавершённых заявок CDN нет.")
        for row in rows:
            await message.answer(
                f"Заявка {row['order_id']}\nЛогин: {row['login']}\n"
                f"{row['bytes'] // 1_000_000_000} ГБ · {row['amount_minor'] / 100:g} ₽\n"
                f"Статус: {row['payment_state']}", parse_mode=None,
                reply_markup=keyboard([
                    [("✅ Подтвердить", "mc:cf:" + row["order_id"]),
                     ("❌ Отклонить", "mc:cr:" + row["order_id"])],
                    [("👤 Клиент", "mc:acard:" + account_key(row["login"]))]]))

    async def customers(self, message, cursor=""):
        data = await self.request("GET", "/admin/customers", params={"limit": 10, "cursor": cursor})
        rows = [[(item["login"], "mc:acard:" + account_key(item["login"]))]
                for item in data.get("customers", [])]
        if data.get("next_cursor"):
            key = secrets.token_hex(8)
            self.pages[key] = data["next_cursor"]
            rows.append([("Далее →", "mc:apage:" + key)])
        rows.append([("🛠 Управление", "mc:admin:menu")])
        await message.answer("Клиенты Maestro · CDN", reply_markup=keyboard(rows), parse_mode=None)

    def is_credit_reply(self, message):
        reply = getattr(message, "reply_to_message", None)
        return bool(message.chat.type == "private" and ui_is_admin(message.from_user.id)
            and message.chat.id == message.from_user.id and reply
            and getattr(reply, "from_user", None) and reply.from_user.id == message.bot.id
            and (message.chat.id, reply.message_id) in self.prompts)

    async def credit_reply(self, message):
        key, deadline = self.prompts[(message.chat.id, message.reply_to_message.message_id)]
        if time.monotonic() > deadline or key not in _ACCOUNTS:
            await message.answer("Запрос устарел. Откройте карточку клиента заново.")
            return
        value = (message.text or "").strip()
        if value.lower() in ("отмена", "/cancel"):
            self.prompts.pop((message.chat.id, message.reply_to_message.message_id), None)
            await self.card(message, key)
            return
        if not re.fullmatch(r"[1-9][0-9]{0,5}", value):
            await message.answer("Ответьте целым положительным количеством ГБ (1–999999) или «отмена».")
            return
        token = secrets.token_hex(12)
        self.intents[token] = {"login": _ACCOUNTS[key], "gb": int(value), "chat_id": message.chat.id,
                              "key": key, "deadline": time.monotonic() + 1800}
        await message.answer(
            f"Добавить клиенту {_ACCOUNTS[key]} {int(value)} ГБ CDN?\n"
            "Это ручное начисление. Подтверждение оплаченной заявки делается в её карточке.",
            parse_mode=None, reply_markup=keyboard([
                [("✅ Начислить " + value + " ГБ", "mc:agrant:" + token)],
                [("Отмена", "mc:acard:" + key)]]))

    async def dispatch(self, cb, action, identity):
        if action not in ("acard", "acredit", "agrant", "apage") and not (
                action == "admin" and identity in ("orders", "customers")):
            return False
        if not ui_is_admin(cb.from_user.id) or cb.message.chat.id != cb.from_user.id:
            await cb.answer("Доступ только администратору.", show_alert=True)
            return True
        await cb.answer()
        if action == "admin":
            if identity == "orders":
                await self.orders(cb.message)
            else:
                await self.customers(cb.message)
        elif action == "acard":
            await self.card(cb.message, identity)
        elif action == "apage":
            if identity not in self.pages:
                await cb.message.answer("Страница устарела. Откройте список заново.")
            else:
                await self.customers(cb.message, self.pages[identity])
        elif action == "acredit":
            if identity not in _ACCOUNTS:
                await cb.message.answer("Карточка устарела. Откройте клиента заново.")
                return True
            from aiogram.types import ForceReply
            prompt = await cb.message.answer(
                f"Сколько ГБ CDN добавить клиенту {_ACCOUNTS[identity]}?\n"
                "Ответьте числом на это сообщение или напишите «отмена».", parse_mode=None,
                reply_markup=ForceReply(selective=True))
            self.prompts[(cb.from_user.id, prompt.message_id)] = (identity, time.monotonic() + 1800)
        else:
            intent = self.intents.get(identity)
            if not intent or intent["chat_id"] != cb.from_user.id or time.monotonic() > intent["deadline"]:
                await cb.message.answer("Подтверждение устарело. Откройте карточку заново.")
                return True
            data = await self.request("POST", "/admin/customer/whitelist-credit",
                json={"login": intent["login"], "gb": intent["gb"]},
                headers={"Idempotency-Key": "tg-cdn-admin-" + identity})
            await cb.message.edit_text(
                f"Начислено {intent['gb']} ГБ CDN клиенту {intent['login']}.\n"
                f"Остаток: {int(data.get('remaining_bytes', 0)) / 1_000_000_000:g} ГБ.",
                parse_mode=None, reply_markup=keyboard([[("👤 Карточка", "mc:acard:" + intent["key"])]]))
        return True
