"""Shared customer presentation; ordinary payments remain owned by each bot."""
import asyncio
import math
import os
import time
from datetime import datetime, timezone, timedelta
from types import SimpleNamespace
from urllib.parse import urlsplit

try:
    from .maestro_customer_cdn import test_customer_enabled
except ImportError:
    from maestro_customer_cdn import test_customer_enabled

LOGIN_PROMPT = "Введите ваш логин MaestroVPN ответом на это сообщение."
CDN_GUIDE = (
    "CDN — запасное подключение для мобильного интернета с белыми списками, "
    "когда обычный VPN не работает.\n\n"
    "Для CDN нужны действующая подписка VPN и отдельный пакет гигабайтов. "
    "Оплата обычного VPN продлевает дни, покупка CDN добавляет ГБ.\n\n"
    "По Wi-Fi выбирайте обычный VPN. В стороннем приложении выключайте CDN вручную: "
    "он расходует пакет, пока выбран для подключения."
)
_UI = {}


def configure_customer_ui(**hooks):
    _UI.update(hooks)


async def ui_call(name, *args):
    hook = _UI.get(name)
    if hook is None:
        return None
    value = hook(*args)
    return await value if hasattr(value, "__await__") else value


def ui_is_admin(chat_id):
    checker = _UI.get("is_admin")
    return bool(checker and checker(chat_id))


def ui_message_owned(message):
    """Synchronous ownership filter for shared ForceReply conversations."""
    checker = _UI.get("message_owned")
    return bool(checker and checker(message))


async def ordinary_customer_status(message):
    try:
        return await ui_call("status", message) or {}
    except Exception:
        return {"active": None, "bound": None, "error": True}


async def send_customer_home(message, section="main"):
    if message.chat.type != "private":
        return
    await ui_call("home", message, section)


def cdn_preparing():
    return os.getenv("MAESTRO_CUSTOMER_CDN_PURCHASES_ENABLE") != "1"


def customer_balance_text(balance):
    if not isinstance(balance, dict) or "available_bytes" not in balance:
        return "Остаток CDN сейчас не удалось получить. Это не означает, что ГБ закончились."
    available = max(0, int(balance["available_bytes"]))
    gigabytes = f"{available / 1_000_000_000:.2f}".rstrip("0").rstrip(".")
    verdict = str(balance.get("publication_verdict") or "").upper()
    primary = str(balance.get("primary_access_state") or "").upper()
    if primary and primary != "ACTIVE":
        state = "Для подключения CDN продлите обычную подписку VPN."
    elif available == 0:
        state = "Для подключения CDN нужен пакет гигабайтов."
    elif verdict == "PUBLISHABLE":
        state = "CDN доступен для подключения."
    else:
        state = "ГБ на балансе есть. Сейчас CDN недоступен; попробуйте обновить статус."
    return f"CDN: {gigabytes} ГБ\n{state}"


def ordinary_status_text(status):
    active = status.get("active")
    if status.get("bound") is False:
        return "VPN: ещё не подключён. Купите подписку или введите свой существующий логин."
    if active is None:
        return "VPN: статус временно не получен. Оплаченный срок от этого не меняется."
    expires = status.get("expires_at")
    if active and not expires:
        return "VPN: активен · без ограничения срока"
    if expires:
        expires = float(expires)
        when = datetime.fromtimestamp(expires, timezone(timedelta(hours=3))).strftime("%d.%m.%Y %H:%M")
        days = max(0, math.ceil((expires - time.time()) / 86400))
        if active:
            return f"VPN: активен до {when} МСК\nОсталось дней: {days}"
        return f"VPN: доступ завершён или отключён\nСрок: {when} МСК"
    return "VPN: неактивен"


def browser_cabinet_url():
    value = os.getenv("MAESTRO_CUSTOMER_WEB_URL", "https://cdn-test.wapmixx.ru/cabinet/").strip()
    parsed = urlsplit(value)
    if (parsed.scheme == "https" and parsed.hostname and parsed.username is None
            and parsed.password is None and not parsed.query and not parsed.fragment):
        return value
    return ""


def cabinet_keyboard(section="main", authenticated=True, test_checkout=False, admin=False, extras=None):
    from aiogram.types import InlineKeyboardButton as B, InlineKeyboardMarkup
    if section == "cdn":
        rows = []
        if authenticated and (not cdn_preparing() or test_checkout):
            rows.append([B(text="💳 Купить ГБ CDN", callback_data="mc:gigabytes:menu")])
        rows.append([B(text="📲 Подключить CDN", callback_data="mc:devices:cdn")])
        rows.append([B(text="🔄 Обновить остаток", callback_data="mc:home:cdn")])
        if not authenticated:
            rows.append([B(text="🔑 Ввести логин", callback_data="mc:home:login")])
        rows.append([B(text="🏠 Главное меню", callback_data="mc:home:main")])
    else:
        rows = [
            [B(text="📲 Подключить VPN", callback_data="mc:devices:menu")],
            [B(text="💳 Купить / продлить VPN", callback_data="mc:renew:menu")],
            [B(text="📶 CDN · остаток и покупка ГБ", callback_data="mc:home:cdn")],
            [B(text="👤 Моя подписка", callback_data="mc:balance:menu"),
             B(text="❓ Помощь", callback_data="mc:help:menu")],
        ]
        if extras:
            rows.extend(extras)
        if not authenticated:
            rows.append([B(text="🔑 У меня есть логин", callback_data="mc:home:login")])
        if admin:
            rows.append([B(text="🛠 Управление", callback_data="mc:admin:menu")])
    return InlineKeyboardMarkup(inline_keyboard=rows)


def cdn_purchase_guidance():
    if cdn_preparing():
        return "Продажа новых пакетов CDN временно закрыта. Ранее оплаченные ГБ остаются на балансе."
    return "После оплаты нажмите «Я оплатил». Администратор проверит перевод; затем ГБ появятся на балансе."


def is_customer_login_reply(message):
    reply = getattr(message, "reply_to_message", None)
    chat = getattr(message, "chat", None)
    sender = getattr(message, "from_user", None)
    replied_by = getattr(reply, "from_user", None)
    bot = getattr(message, "bot", None)
    return bool(chat and chat.type == "private" and sender and chat.id == sender.id
        and reply and reply.text == LOGIN_PROMPT and replied_by and replied_by.is_bot
        and bot and replied_by.id == bot.id and getattr(message, "text", None))


def customer_login_command(message):
    return SimpleNamespace(command="maestro", args=message.text.strip())


async def send_customer_dashboard(message, flow, section="main", ordinary=None):
    ordinary = ordinary if ordinary is not None else await ordinary_customer_status(message)
    ordinary_login = ordinary.get("login")
    login = ordinary_login or (flow.login if flow else "")
    different_logins = bool(flow and ordinary_login and ordinary_login != flow.login)
    test_checkout = bool(flow and test_customer_enabled(flow.login, message.chat.id))
    if flow:
        try:
            balance = await flow.api.balance()
            cdn_text = customer_balance_text(balance)
            if (ordinary.get("active") is None and not ordinary.get("error")
                    and _UI.get("status") is None and not different_logins):
                primary = str(balance.get("primary_access_state") or "").upper()
                if primary:
                    ordinary = {**ordinary, "active": primary == "ACTIVE",
                                "bound": True, "expires_at": balance.get("period_ends_at_unix")}
        except Exception:
            cdn_text = "CDN: остаток временно не получен. Это не означает, что ГБ закончились."
    else:
        cdn_text = ("CDN: сведения пока недоступны для этого логина."
                    if login else "CDN: войдите по существующему логину, чтобы увидеть остаток.")
    if different_logins:
        cdn_text = f"Логин CDN: {flow.login}\n" + cdn_text
        cdn_text += "\nЛогины VPN и CDN различаются. Перед продлением выберите нужный логин в разделе «Помощь»."
    if section == "cdn":
        text = f"📶 MaestroVPN · CDN\n\n{cdn_text}\n\n{CDN_GUIDE}\n\n{cdn_purchase_guidance()}"
    else:
        text = "MaestroVPN\n" + (f"👤 Логин: {login}\n" if login else "Добро пожаловать!\n")
        text += f"\n{ordinary_status_text(ordinary)}\n\n{cdn_text}\n\n"
        text += ("1. Купите VPN или войдите по своему логину.\n"
                 "2. Откройте «Подключить VPN» и выберите приложение.\n"
                 "3. Добавьте подписку и включите соединение."
                 if not login else "Подключение, продление и помощь — кнопками ниже.")
    if test_checkout:
        text += "\n\nТестовая учётная запись: реальный перевод для тестовой покупки не нужен."
    extras = await ui_call("extra_buttons", message) if section == "main" else None
    await message.answer(text, parse_mode=None,
        reply_markup=cabinet_keyboard(section, bool(flow), test_checkout, ui_is_admin(message.chat.id), extras))


async def send_customer_help(message, flow=None, topic="menu"):
    from aiogram.types import InlineKeyboardButton as B, InlineKeyboardMarkup
    texts = {
        "payment": (
            "💳 Оплата и продление\n\n"
            "Выберите «Купить / продлить VPN» или пакет в разделе CDN. "
            "Реквизиты, сумма и назначение перевода появятся в конкретной заявке.\n\n"
            "Переведите указанную сумму, затем нажмите «Я оплатил» именно под этой заявкой. "
            "После проверки администратором бот сообщит результат. Одна заявка — один перевод.\n\n"
            "Если перевод уже сделан, не платите повторно. Сообщите поддержке номер заявки, "
            "время и сумму. Не отправляйте пароль банка или код из SMS."
        ),
        "cdn": "📶 Как пользоваться CDN\n\n" + CDN_GUIDE,
        "connection": (
            "🔌 Не подключается\n\n"
            "1. Проверьте доступ в интернет и срок VPN в «Моей подписке».\n"
            "2. Отключите другие VPN. Обновите подписку в выбранном приложении.\n"
            "3. Попробуйте другой обычный сервер. При белых списках на мобильной сети выберите CDN, если есть ГБ.\n"
            "4. Если не помогло, отправьте в поддержку: приложение и версию, оператора, "
            "Wi-Fi или мобильную сеть, страну сервера, время ошибки и скриншот.\n\n"
            "Ошибка обновления подписки и ошибка соединения — разные вещи. "
            "Уточните, на каком шаге проблема. Ссылку подписки не публикуйте."
        ),
        "subscription": (
            "👤 Логин и подписка\n\n"
            "Бот узнаёт привязанный Telegram-аккаунт. Повторный вход для обычного использования не нужен. "
            "Если подписка куплена раньше под другим логином, нажмите «Ввести / сменить логин».\n\n"
            "Для приложения MaestroVPN вводится логин; для HAPP, INCY и Karing добавляется "
            "персональная ссылка подписки. После покупки CDN обновляется та же подписка.\n\n"
            "Ссылка даёт доступ к вашей подписке: не публикуйте её и не передавайте посторонним. "
            "Продление выполняется в боте, переустанавливать приложение не нужно."
        ),
    }
    if topic == "menu":
        text = ("❓ Помощь MaestroVPN\n\n"
                "Выберите вопрос. Для подключения сначала установите приложение, "
                "затем добавьте свою подписку через «Подключить VPN».")
        rows = [
            [B(text="💳 Оплата и подтверждение", callback_data="mc:help:payment")],
            [B(text="📶 Когда нужен CDN", callback_data="mc:help:cdn")],
            [B(text="🔌 Не подключается", callback_data="mc:help:connection")],
            [B(text="👤 Логин и подписка", callback_data="mc:help:subscription")],
            [B(text="🔑 Ввести / сменить логин", callback_data="mc:home:login")],
        ]
        url = browser_cabinet_url()
        if url:
            rows.append([B(text="🌐 Кабинет без Telegram", url=url)])
        support = os.getenv("MAESTRO_CUSTOMER_SUPPORT_URL", "").strip()
        if support.startswith("https://t.me/"):
            rows.append([B(text="✉️ Написать в поддержку", url=support)])
    else:
        text = texts.get(topic, texts["connection"])
        rows = [[B(text="❓ Все вопросы", callback_data="mc:help:menu")]]
        support = os.getenv("MAESTRO_CUSTOM_SUPPORT_URL", os.getenv("MAESTRO_CUSTOMER_SUPPORT_URL", "")).strip()
        if support.startswith("https://t.me/"):
            rows.append([B(text="✉️ Поддержка", url=support)])
    rows.append([B(text="🏠 Главное меню", callback_data="mc:home:main")])
    await message.answer(text, parse_mode=None, reply_markup=InlineKeyboardMarkup(inline_keyboard=rows))


async def open_customer_dashboard(callback, flow):
    if callback.message.chat.type != "private" or callback.message.chat.id != callback.from_user.id:
        await callback.answer("Откройте личный чат с ботом.", show_alert=True)
        return
    if callback.data == "mc:home:login":
        from aiogram.types import ForceReply
        await callback.message.answer(LOGIN_PROMPT, reply_markup=ForceReply(
            selective=True, input_field_placeholder="Ваш логин MaestroVPN"))
    else:
        await send_customer_dashboard(callback.message, flow, "cdn" if callback.data == "mc:home:cdn" else "main")
    await callback.answer()


def with_customer_entry(handler):
    """Compatibility for old imports; /start now explicitly renders one home."""
    return handler


def customer_admin_button(login):
    try:
        from .maestro_customer_admin import customer_admin_button as button
    except ImportError:
        from maestro_customer_admin import customer_admin_button as button
    return button(login)
