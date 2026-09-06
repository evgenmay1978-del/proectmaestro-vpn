"""Visible entry to the existing customer cabinet from the ordinary /start."""
from functools import wraps
import os
from types import SimpleNamespace

LOGIN_PROMPT = "Введите ваш логин MaestroVPN ответом на это сообщение."


def cdn_preparing():
    return os.getenv("MAESTRO_CUSTOMER_CDN_PURCHASES_ENABLE") != "1"


def customer_balance_text(balance):
    available = max(0, int(balance.get("available_bytes") or 0))
    gigabytes = f"{available / 1_000_000_000:.2f}".rstrip("0").rstrip(".")
    primary_active = str(balance.get("primary_access_state") or "").upper() == "ACTIVE"
    primary = "Обычный VPN: активен." if primary_active else "Обычный VPN: неактивен."
    if cdn_preparing():
        cdn = "CDN: готовится к подключению. Покупка CDN пока закрыта."
    elif str(balance.get("publication_verdict") or "").upper() == "PUBLISHABLE":
        cdn = "CDN: подключён."
    else:
        cdn = "CDN: сейчас не подключён."
    return f"{primary}\n{cdn}\nБаланс CDN: {gigabytes} ГБ."


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


async def send_customer_dashboard(message, flow):
    from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup
    try:
        balance = await flow.show_balance()
    except Exception:
        state = "CDN: готовится к подключению." if cdn_preparing() else "Статус CDN временно недоступен."
        balance = state + "\nБаланс сейчас не удалось получить. Повторите «Моя подписка и баланс»."
    keyboard = InlineKeyboardMarkup(inline_keyboard=[
        [InlineKeyboardButton(text=label, callback_data=value)] for label, value in flow.menu_actions()
    ])
    await message.answer(f"Личный кабинет MaestroVPN\nЛогин: {flow.login}\n\n{balance}",
        reply_markup=keyboard, parse_mode=None)


async def open_customer_dashboard(callback, flow):
    if callback.message.chat.type != "private" or callback.message.chat.id != callback.from_user.id:
        await callback.answer("Откройте личный чат с ботом.", show_alert=True)
        return
    await callback.answer()
    if flow is None:
        from aiogram.types import ForceReply
        await callback.message.answer(LOGIN_PROMPT,
            reply_markup=ForceReply(selective=True, input_field_placeholder="Ваш логин MaestroVPN"))
        return
    await send_customer_dashboard(callback.message, flow)


def with_customer_entry(handler):
    """Keep the existing start handler and append one visible cabinet button."""
    @wraps(handler)
    async def wrapped(message, *args, **kwargs):
        result = await handler(message, *args, **kwargs)
        if message.chat.type == "private":
            from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup
            text = "Личный кабинет: ваша подписка и баланс CDN."
            if cdn_preparing():
                text += "\nCDN готовится к подключению."
            await message.answer(text, parse_mode=None, reply_markup=InlineKeyboardMarkup(inline_keyboard=[
                [InlineKeyboardButton(text="Мой VPN · CDN", callback_data="mc:home:menu")]
            ]))
        return result
    return wrapped
