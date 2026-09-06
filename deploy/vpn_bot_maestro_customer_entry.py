"""Visible entry to the existing customer cabinet from the ordinary /start."""
from functools import wraps
import os
from types import SimpleNamespace
from urllib.parse import urlsplit

LOGIN_PROMPT = "Введите ваш логин MaestroVPN ответом на это сообщение."
CDN_GUIDE = (
    "CDN — запасное подключение для мобильного интернета с белыми списками, "
    "когда обычные подключения не работают.\n\n"
    "Обычная подписка — основной VPN. Пакет CDN — отдельные гигабайты "
    "в дополнение к действующей обычной подписке.\n\n"
    "Дома и по Wi-Fi используйте обычный VLESS или Hy2. CDN выключайте, "
    "чтобы не тратить купленные ГБ.\n"
    "Выключайте CDN вручную при переходе на Wi-Fi."
)


def cdn_preparing():
    return os.getenv("MAESTRO_CUSTOMER_CDN_PURCHASES_ENABLE") != "1"


def customer_balance_text(balance):
    available = max(0, int(balance.get("available_bytes") or 0))
    gigabytes = f"{available / 1_000_000_000:.2f}".rstrip("0").rstrip(".")
    primary_active = str(balance.get("primary_access_state") or "").upper() == "ACTIVE"
    primary = "Обычная подписка: активна." if primary_active else "Обычная подписка: неактивна."
    if cdn_preparing():
        cdn = "CDN при белых списках: готовится. Продажа ГБ пока закрыта."
    elif str(balance.get("publication_verdict") or "").upper() == "PUBLISHABLE":
        cdn = "CDN при белых списках: доступен."
    else:
        cdn = "CDN при белых списках: сейчас недоступен."
    return f"{primary}\n{cdn}\nБаланс CDN: {gigabytes} ГБ."


def browser_cabinet_url():
    value = os.getenv("MAESTRO_CUSTOMER_WEB_URL", "https://cdn-test.wapmixx.ru/cabinet/").strip()
    parsed = urlsplit(value)
    if (parsed.scheme == "https" and parsed.hostname and parsed.username is None
            and parsed.password is None and not parsed.query and not parsed.fragment):
        return value
    return ""


def cabinet_keyboard(section="main", authenticated=True):
    from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup
    rows = []
    if section == "cdn":
        if authenticated and not cdn_preparing():
            rows.append([InlineKeyboardButton(text="Купить ГБ CDN", callback_data="mc:gigabytes:menu")])
        if not authenticated:
            rows.append([InlineKeyboardButton(text="Войти по логину", callback_data="mc:home:login")])
        elif authenticated:
            rows.append([InlineKeyboardButton(text="Обновить баланс CDN", callback_data="mc:home:cdn")])
        browser_url = browser_cabinet_url()
        if browser_url:
            rows.append([InlineKeyboardButton(text="Кабинет без Telegram", url=browser_url)])
        rows.append([InlineKeyboardButton(text="Моя обычная подписка", callback_data="mc:home:menu")])
    else:
        rows = [
            [InlineKeyboardButton(text="Обычная подписка и баланс", callback_data="mc:balance:menu")],
            [InlineKeyboardButton(text="CDN при белых списках", callback_data="mc:home:cdn")],
            [InlineKeyboardButton(text="Подключить устройство", callback_data="mc:devices:menu")],
            [InlineKeyboardButton(text="Помощь", callback_data="mc:help:menu")],
        ]
    return InlineKeyboardMarkup(inline_keyboard=rows)


def cdn_purchase_guidance():
    if cdn_preparing():
        return "CDN ещё готовится к подключению. Сейчас мы не принимаем оплату за пакеты ГБ."
    if browser_cabinet_url():
        return "Если Telegram недоступен, откройте «Кабинет без Telegram» в браузере. Сохраните адрес заранее."
    return "Пополняйте ГБ заранее, пока Telegram доступен. Покупка без Telegram пока не подключена."


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


async def send_customer_dashboard(message, flow, section="main"):
    try:
        balance = await flow.show_balance()
    except Exception:
        state = "CDN: готовится к подключению." if cdn_preparing() else "Статус CDN временно недоступен."
        balance = state + "\nБаланс сейчас не удалось получить. Повторите «Моя подписка и баланс»."
    if section == "cdn":
        text = f"CDN при белых списках\n\n{balance}\n\n{CDN_GUIDE}\n\n{cdn_purchase_guidance()}"
    else:
        text = (f"MaestroVPN · Моя подписка\nЛогин: {flow.login}\n\n{balance}\n\n"
            "Обычный VPN используйте каждый день; продление — в основном меню /start.\n"
            "CDN нужен только при белых списках. Для него покупается отдельный пакет ГБ.\n"
            "Дома и по Wi-Fi выбирайте обычный VLESS/Hy2, CDN выключайте.")
    await message.answer(text, reply_markup=cabinet_keyboard(section), parse_mode=None)


async def send_customer_help(message, flow):
    text = ("MaestroVPN: обычная подписка и CDN\n\n" + CDN_GUIDE
        + "\n\nДля MaestroVPN используйте свой логин. Для Incy, Happ и Karing "
        "откройте «Подключить устройство» и скопируйте HTTPS-ссылку подписки.\n\n"
        + cdn_purchase_guidance())
    await message.answer(text, reply_markup=cabinet_keyboard("cdn"), parse_mode=None)


async def open_customer_dashboard(callback, flow):
    if callback.message.chat.type != "private" or callback.message.chat.id != callback.from_user.id:
        await callback.answer("Откройте личный чат с ботом.", show_alert=True)
        return
    await callback.answer()
    section = "cdn" if callback.data == "mc:home:cdn" else "main"
    if flow is None:
        if section == "cdn":
            await callback.message.answer("CDN при белых списках\n\n" + CDN_GUIDE + "\n\n"
                + cdn_purchase_guidance() + "\n\nВойдите по логину, чтобы увидеть свой баланс.",
                reply_markup=cabinet_keyboard("cdn", authenticated=False), parse_mode=None)
            return
        from aiogram.types import ForceReply
        await callback.message.answer(LOGIN_PROMPT,
            reply_markup=ForceReply(selective=True, input_field_placeholder="Ваш логин MaestroVPN"))
        return
    await send_customer_dashboard(callback.message, flow, section)


def with_customer_entry(handler):
    """Keep the existing start handler and append one visible cabinet button."""
    @wraps(handler)
    async def wrapped(message, *args, **kwargs):
        result = await handler(message, *args, **kwargs)
        if message.chat.type == "private":
            from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup
            text = ("Обычная подписка — для ежедневного VPN.\n"
                "CDN — отдельный пакет ГБ только для мобильных белых списков.\n"
                "Дома и по Wi-Fi выбирайте VLESS/Hy2 и выключайте CDN.")
            if cdn_preparing():
                text += "\nПродажа CDN ещё не открыта."
            await message.answer(text, parse_mode=None, reply_markup=InlineKeyboardMarkup(inline_keyboard=[
                [InlineKeyboardButton(text="Моя обычная подписка", callback_data="mc:home:menu")],
                [InlineKeyboardButton(text="CDN при белых списках", callback_data="mc:home:cdn")]
            ]))
        return result
    return wrapped
