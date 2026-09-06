"""Small customer-facing MaestroVPN Telegram flow.

This module deliberately keeps Telegram presentation separate from the panel
contract.  A bot integration supplies a trusted ``CustomerFlow`` for the
Telegram user; neither a login nor a subscription token is placed in callback
data.
"""
import os
import re
import secrets
import sqlite3
from pathlib import Path
from urllib.parse import parse_qs, urlencode, urlparse, urlunparse

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


def subscription_copy_url(raw: str) -> str:
    """Select the URI-list representation without changing the private token."""
    parsed = urlparse(str(raw or ""))
    if (parsed.scheme != "https" or not parsed.hostname or parsed.username is not None
            or parsed.password is not None or parsed.fragment
            or not re.fullmatch(r"/sub/[^/]+", parsed.path)):
        raise ValueError("invalid subscription copy URL")
    query = parse_qs(parsed.query, keep_blank_values=True)
    query["format"] = ["links"]
    return urlunparse(parsed._replace(query=urlencode(query, doseq=True)))


def callback_data(action: str, opaque_id: str) -> str:
    """Encode only a small action and server-generated opaque identifier."""
    if not _OPAQUE.fullmatch(action) or not _OPAQUE.fullmatch(opaque_id):
        raise ValueError("callback values must be opaque identifiers")
    value = f"mc:{action}:{opaque_id}"
    if len(value.encode("utf-8")) > 64:
        raise ValueError("callback data exceeds Telegram's 64-byte limit")
    return value


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

    async def create_order(self, payload: dict, idempotency_key: str | None = None):
        kwargs = {"json": payload}
        if idempotency_key:
            kwargs["headers"] = {"Idempotency-Key": idempotency_key}
        return await self.request("POST", "/order", **kwargs)

    async def claim_paid(self, order_id: str):
        return await self.request("POST", f"/order/{order_id}/paid-claim", json={})

    async def profile(self):
        return await self.request("GET", "/account/profile")

    async def balance(self):
        return await self.request("GET", "/account/whitelist-balance")

    async def delivery(self, client: str):
        return await self.request("POST", "/account/subscription-delivery", json={"client": client})


class CustomerBindingStore:
    """Durable Telegram-chat binding established only by bearer possession."""

    def __init__(self, path: str):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with sqlite3.connect(self.path) as connection:
            connection.execute(
                "CREATE TABLE IF NOT EXISTS customer_bindings "
                "(chat_id INTEGER PRIMARY KEY, login TEXT NOT NULL, customer_token TEXT NOT NULL)"
            )
        try:
            os.chmod(self.path, 0o600)
        except OSError:
            pass

    def bind(self, chat_id: int, login: str, customer_token: str) -> None:
        if not login.strip() or not customer_token.strip():
            raise ValueError("customer binding requires an authenticated bearer and login")
        with sqlite3.connect(self.path) as connection:
            connection.execute(
                "INSERT INTO customer_bindings(chat_id, login, customer_token) VALUES (?, ?, ?) "
                "ON CONFLICT(chat_id) DO UPDATE SET login=excluded.login, customer_token=excluded.customer_token",
                (chat_id, login, customer_token),
            )

    def get(self, chat_id: int) -> tuple[str, str] | None:
        with sqlite3.connect(self.path) as connection:
            row = connection.execute(
                "SELECT login, customer_token FROM customer_bindings WHERE chat_id = ?", (chat_id,)
            ).fetchone()
        return (str(row[0]), str(row[1])) if row else None


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
            {"tariff": "1m", "sub_token": self.sub_token, "login": self.login}
        )

    async def buy_gigabytes(self, gigabytes: int, intent_id: str):
        if gigabytes not in {pack[0] for pack in GB_PACKS}:
            raise ValueError("unsupported gigabyte pack")
        if not _OPAQUE.fullmatch(intent_id):
            raise ValueError("intent must be an opaque identifier")
        return await self.api.create_order(
            {"product_id": GB_PRODUCT_IDS[gigabytes], "sub_token": self.sub_token},
            idempotency_key=f"tg-order-{intent_id}",
        )

    async def claim_paid(self, order_id: str):
        return await self.api.claim_paid(order_id)

    def balance_text(self, balance: dict) -> str:
        available = int(balance.get("available_bytes") or 0)
        gigabytes = available // 1_000_000_000
        primary = balance.get("primary_access_state", "")
        publication = balance.get("publication_verdict", "DISABLED")
        if primary.upper() != "ACTIVE":
            return f"Основной доступ истёк: сначала продлите его. Сохранённый баланс: {gigabytes} ГБ."
        if publication == "DISABLED":
            return f"Обычная подписка активна. Сохранённый баланс: {gigabytes} ГБ."
        return f"Обычная подписка активна. CDN/LTE баланс: {gigabytes} ГБ."

    async def show_balance(self) -> str:
        return self.balance_text(await self.api.balance())

    async def delivery(self, client: str) -> dict:
        result = await self.api.delivery(client)
        if client == "incy" and result.get("format") == "INCY_ONE_TAP":
            copy_url = result.get("copy_url")
            if not copy_url:
                # The old controller exposes only a deep link for INCY.
                # Its Happ descriptor is the same account's plain HTTPS URL.
                copy_url = (await self.api.delivery("happ"))["url"]
            return {"copy_url": subscription_copy_url(copy_url), "open_url": result["url"], "label": "HTTPS-ссылка для Incy"}
        if client == "happ" and result.get("format") == "COPY_HTTPS_URL_AND_QR":
            return {
                "url": subscription_copy_url(result.get("copy_url") or result["url"]),
                "steps": (
                    "1. Скопируйте HTTPS-ссылку.",
                    "2. Откройте Happ.",
                    "3. Добавьте подписку и вставьте ссылку или отсканируйте QR.",
                    "4. Убедитесь, что профиль MaestroVPN появился.",
                ),
            }
        if client == "karing" and result.get("format") == "KARING_INSTALL_CONFIG":
            copy_url = result.get("copy_url")
            if not copy_url:
                copy_url = (parse_qs(urlparse(result["url"]).query).get("url") or [""])[0]
            return {"copy_url": subscription_copy_url(copy_url), "open_url": result["url"], "label": "HTTPS-ссылка для Karing"}
        raise ValueError("unexpected subscription delivery result")

    def support_text(self) -> str:
        return "Напишите в поддержку и укажите ваш Maestro login."


def configured_customer_api(customer_token: str) -> CustomerAPI:
    return CustomerAPI(os.getenv("MAESTRO_URL"), customer_token)


def legacy_customer_logins(chat_id: int) -> tuple[str, ...]:
    """Read only existing account bindings for this exact private Telegram ID."""
    if isinstance(chat_id, bool) or not isinstance(chat_id, int) or chat_id <= 0:
        return ()
    module_root = Path(__file__).resolve().parent
    if module_root == Path("/root/vpn_bot/handlers"):
        path = Path("/root/vpn_bot/data/bot.db")
        query = "SELECT client_email FROM users WHERE tg_id=? AND is_bound=1"
        arguments = (chat_id,)
    elif module_root == Path("/opt/vpn_bot"):
        path = module_root / "bot_minimal.db"
        query = "SELECT proxy_user FROM binds WHERE tg_id=? UNION SELECT proxy_user FROM subscriptions WHERE tg_id=?"
        arguments = (chat_id, chat_id)
    else:
        return ()
    try:
        info = path.lstat()
        if path.is_symlink() or not path.is_file() or info.st_uid != 0 or info.st_nlink != 1 or info.st_mode & 0o022:
            return ()
        with sqlite3.connect(path.as_uri() + "?mode=ro", uri=True, timeout=2) as connection:
            connection.execute("PRAGMA query_only=ON")
            rows = connection.execute(query, arguments).fetchmany(3)
        if len(rows) > 2:
            return ()
        return tuple(sorted({str(row[0]).strip() for row in rows if isinstance(row[0], str)
                             and re.fullmatch(r"[A-Za-z0-9_.@+-]{1,96}", row[0].strip())}))
    except (OSError, sqlite3.Error):
        return ()


def legacy_customer_choice_key(login: str) -> str:
    import hashlib
    return hashlib.sha256(("maestro-account-choice\0" + login).encode()).hexdigest()[:24]


def build_customer_router(store: CustomerBindingStore):
    """Return the production aiogram child router without importing aiogram in unit tests."""
    from aiogram import F, Router
    from aiogram.filters import CommandStart
    from aiogram.types import BufferedInputFile, CallbackQuery, InlineKeyboardButton, InlineKeyboardMarkup

    router = Router(name="maestro_customer")

    def flow_for(chat_id: int) -> CustomerFlow | None:
        binding = store.get(chat_id)
        if binding is None:
            return None
        login, customer_token = binding
        return CustomerFlow(configured_customer_api(customer_token), login, customer_token)

    async def resolve_customer_callback(callback, choice: str | None = None):
        message = callback.message
        if message.chat.type != "private" or message.chat.id != callback.from_user.id:
            await callback.answer("Откройте личный чат с ботом.", show_alert=True)
            return None, True
        flow = flow_for(message.chat.id)
        if flow is not None:
            return flow, False
        logins = legacy_customer_logins(message.chat.id)
        if choice is not None:
            logins = tuple(login for login in logins if legacy_customer_choice_key(login) == choice)
            if len(logins) != 1:
                await callback.answer("Список аккаунтов изменился. Откройте кабинет снова.", show_alert=True)
                return None, True
        elif len(logins) > 1:
            keyboard = InlineKeyboardMarkup(inline_keyboard=[[
                InlineKeyboardButton(text=login, callback_data=callback_data("account", legacy_customer_choice_key(login)))
            ] for login in logins])
            await callback.answer()
            await message.answer("Выберите вашу подписку:", reply_markup=keyboard, parse_mode=None)
            return None, True
        if not logins:
            return None, False
        try:
            login, customer_token = await claim_customer_login(logins[0])
            if login != logins[0]:
                raise ValueError("known account profile mismatch")
            store.bind(message.chat.id, login, customer_token)
        except Exception:
            await callback.answer("Ваш аккаунт найден. Данные временно недоступны, откройте кабинет чуть позже.", show_alert=True)
            return None, True
        return flow_for(message.chat.id), False

    async def require_flow(callback: CallbackQuery) -> CustomerFlow | None:
        flow, handled = await resolve_customer_callback(callback)
        if flow is None and not handled:
            await callback.answer("Сначала откройте /start с вашей ссылкой Maestro.", show_alert=True)
        return flow

    @router.message(CommandStart(deep_link=True))
    async def bind_customer(message, command):
        customer_token = (command.args or "").strip()
        if not customer_token:
            await message.answer("Откройте /start из вашей личной ссылки Maestro.")
            return
        try:
            profile = await configured_customer_api(customer_token).profile()
            login = str(profile.get("login") or "").strip()
            if not login:
                raise ValueError("profile has no login")
            store.bind(message.chat.id, login, customer_token)
        except Exception:
            await message.answer("Не удалось подтвердить личную ссылку Maestro.")
            return
        try:
            await message.delete()
        except Exception:
            pass
        flow = flow_for(message.chat.id)
        keyboard = InlineKeyboardMarkup(inline_keyboard=[
            [InlineKeyboardButton(text=label, callback_data=value)] for label, value in flow.menu_actions()
        ])
        await message.answer(flow.menu_text(), reply_markup=keyboard)

    @router.callback_query(F.data.func(lambda value: bool(value) and value.startswith("mc:")))
    async def customer_action(callback: CallbackQuery):
        try:
            _, action, opaque_id = callback.data.split(":", 2)
            if not _OPAQUE.fullmatch(action) or not _OPAQUE.fullmatch(opaque_id):
                raise ValueError("invalid callback")
        except (AttributeError, ValueError):
            await callback.answer("Неверное действие.", show_alert=True)
            return
        if action == "account":
            flow, handled = await resolve_customer_callback(callback, opaque_id)
            if handled or flow is None:
                return
            await callback.message.answer(await flow.show_balance())
            await callback.answer()
            return
        flow = await require_flow(callback)
        if flow is None:
            return
        if action == "balance":
            await callback.message.answer(await flow.show_balance())
        elif action == "renew":
            order = await flow.renew_access()
            order_id = str(order.get("order_id") or "")
            keyboard = InlineKeyboardMarkup(inline_keyboard=[[InlineKeyboardButton(
                text="Я оплатил", callback_data=callback_data("paid", order_id)
            )]]) if _OPAQUE.fullmatch(order_id) else None
            await callback.message.answer(flow.payment_instructions(), reply_markup=keyboard)
        elif action == "gigabytes":
            keyboard = InlineKeyboardMarkup(inline_keyboard=[[
                InlineKeyboardButton(
                    text=f"{gigabytes} ГБ — {price} ₽",
                    callback_data=callback_data(f"gb{gigabytes}", secrets.token_urlsafe(9)),
                )] for gigabytes, price in GB_PACKS])
            await callback.message.answer("Выберите пакет гигабайтов:", reply_markup=keyboard)
        elif action.startswith("gb") and action[2:].isdigit():
            order = await flow.buy_gigabytes(int(action[2:]), opaque_id)
            order_id = str(order.get("order_id") or "")
            keyboard = InlineKeyboardMarkup(inline_keyboard=[[InlineKeyboardButton(
                text="Я оплатил", callback_data=callback_data("paid", order_id)
            )]]) if _OPAQUE.fullmatch(order_id) else None
            await callback.message.answer(flow.payment_instructions(), reply_markup=keyboard)
        elif action == "paid":
            await flow.claim_paid(opaque_id)
            await callback.message.answer("Заявка об оплате отправлена владельцу на подтверждение.")
        elif action == "devices":
            incy = await flow.delivery("incy")
            happ = await flow.delivery("happ")
            karing = await flow.delivery("karing")
            await callback.message.answer(
                "1. Для Incy скопируйте HTTPS-ссылку ниже, откройте импорт подписки и вставьте её:\n"
                f"{incy['copy_url']}\n\n"
                "2. Для Karing скопируйте HTTPS-ссылку ниже, откройте импорт подписки и вставьте её:\n"
                f"{karing['copy_url']}\n\n"
                "3. Для Happ используйте QR из следующего сообщения.\n"
                "4. Убедитесь, что профиль MaestroVPN появился.\n\n"
                "В приложение MaestroVPN входите по логину. "
                "Для CDN нужен включённый доступ и баланс ГБ. После изменения обновите подписку в клиенте.",
            )
            import qrcode
            from io import BytesIO
            qr = qrcode.make(happ["url"])
            image = BytesIO()
            qr.save(image, format="PNG")
            await callback.message.answer_photo(
                BufferedInputFile(image.getvalue(), filename="maestro_happ.png"),
                caption="\n".join((*happ["steps"], happ["url"])),
            )
        elif action == "help":
            await callback.message.answer(flow.support_text())
        else:
            await callback.answer("Неверное действие.", show_alert=True)
            return
        await callback.answer()

    return router


def build_customer_router_from_env():
    return build_customer_router(CustomerBindingStore(
        os.getenv("MAESTRO_CUSTOMER_BINDINGS_PATH", "/var/lib/vpn_bot/maestro_customer.sqlite3")
    ))
