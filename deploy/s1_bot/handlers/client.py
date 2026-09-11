import io
import qrcode
from aiogram import Router, F
from aiogram.types import Message, CallbackQuery, BufferedInputFile
from aiogram.filters import CommandStart, Command
from aiogram.fsm.context import FSMContext
from keyboards import *
from formatters import *
from config import config
from states import ClientStates, AdminStates
from ui import safe_edit
from provision import provision_client
from subscription_links import clean_subscription_url
from datetime import datetime
import math
from urllib.parse import quote

router = Router()

def _make_qr_png(data: str) -> bytes:
    qr = qrcode.QRCode(
        version=None,
        error_correction=qrcode.constants.ERROR_CORRECT_M,
        box_size=10,
        border=3,
    )
    qr.add_data(data)
    qr.make(fit=True)
    img = qr.make_image(fill_color="black", back_color="white")
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    return buf.getvalue()

# MaestroVPN app (multi-protocol) — the panel bridges an existing client into a
# maestro customer and serves the combined sub; the apk is the always-latest mirror.
import os as _os
_MAESTRO_URL = _os.getenv("MAESTRO_URL", "http://127.0.0.1:8910")
_APP_DOWNLOAD_URL = _os.getenv("APP_DOWNLOAD_URL", "https://storage.yandexcloud.net/maestro-apk/latest.apk")
_PROTO_NAMES = {"vless": "VLESS·Reality", "hysteria2": "Hysteria2", "naive": "NaiveProxy", "anytls": "AnyTLS"}

def _is_admin(tg_id):
    return tg_id in config.ADMIN_IDS

async def _user_status(db, api, tg_id):
    """Returns (has_active, days_left). days_left is None for unlimited."""
    user = db.get_user(tg_id)
    has_active = False
    days_left = None
    if user and user["is_bound"] and user["client_email"]:
        result = api.get_client_by_email(user["client_email"])
        if result:
            client, _ = result
            if client.expiry_time:
                remaining = client.expiry_time - datetime.now().timestamp() * 1000
                days_left = max(0, math.ceil(remaining / 86400_000))
                has_active = client.enable and remaining > 0
            else:
                days_left = None  # бессрочно
                has_active = client.enable
    return has_active, days_left

async def ordinary_status(tg_id, db, api):
    """Ordinary VPN is independent of the CDN API; an API failure is unknown."""
    user = db.get_user(tg_id)
    login = user["client_email"] if user and user["is_bound"] else None
    status = {"login": login, "bound": bool(login), "active": None,
              "expires_at": None, "protocols": [],
              "trial_available": _show_trial(db, tg_id)}
    if not login:
        status["active"] = False
        return status
    try:
        result = api.get_client_by_email(login)
    except Exception:
        return status
    if not result:
        return status
    client, _ = result
    expiry = client.expiry_time / 1000 if client.expiry_time else None
    status.update(expires_at=expiry,
                  active=bool(client.enable and (expiry is None or expiry > datetime.now().timestamp())))
    return status


async def open_ordinary_renew(callback, state, db, api):
    if _bound_login(db, callback.from_user.id):
        await client_renew(callback, state, db, api)
    else:
        await client_buy(callback, state, db, api)


async def bind_ordinary_login(message, login, db, api):
    """Update the existing binding only after the shared login was authenticated."""
    if message.chat.type != "private":
        raise ValueError("Откройте личный чат с ботом.")
    tg_id = int(message.chat.id)
    try:
        result = api.get_client_by_email(login)
    except Exception:
        raise ValueError("Не удалось проверить обычную подписку. Привязка сохранена; повторите вход позже.") from None
    if not result:
        raise ValueError("Не удалось подтвердить этот логин в обычном VPN. Привязка сохранена; проверьте логин или обратитесь в поддержку.")
    panel_client, inbound_id = result
    if panel_client.email != login or not inbound_id:
        raise ValueError("Не удалось подтвердить данные обычной подписки. Привязка сохранена; обратитесь в поддержку.")
    owners = [u for u in db.get_all_users()
              if u["client_email"] and u["client_email"].casefold() == login.casefold()]
    if any(int(u["tg_id"]) != tg_id for u in owners):
        raise ValueError("Этот логин уже привязан к другому Telegram. Для переноса обратитесь в поддержку.")
    try:
        panel_owner = int(panel_client.tg_id or 0)
    except (TypeError, ValueError):
        raise ValueError("Не удалось проверить владельца обычной подписки. Обратитесь в поддержку.") from None
    if panel_owner and panel_owner != tg_id:
        raise ValueError("Этот логин закреплён за другим Telegram. Для переноса обратитесь в поддержку.")
    sender = message.from_user
    username = sender.username or "" if sender and sender.id == tg_id else ""
    # No await between the ownership check and existing SQLite writes: simultaneous
    # login replies in this bot cannot both claim an unbound ordinary account.
    db.upsert_user(tg_id, username)
    db.bind_user(tg_id, inbound_id, panel_client.email)
    return True


from .maestro_customer_entry import send_customer_home, send_customer_help

@router.message(CommandStart())
async def cmd_start(msg: Message, state: FSMContext, db, api):
    db.upsert_user(msg.from_user.id, msg.from_user.username or "")
    # referral
    if " " in msg.text and "bind_" not in msg.text:
        try:
            ref_id = int(msg.text.split()[1])
            user = db.get_user(msg.from_user.id)
            if ref_id != msg.from_user.id and not user["referred_by"]:
                db.set_referrer(msg.from_user.id, ref_id)
        except (ValueError, IndexError):
            pass
    await state.clear()
    await state.set_state(ClientStates.main)
    await send_customer_home(msg)

@router.message(Command("help"))
async def cmd_help(msg: Message):
    await send_customer_help(msg)

def _show_trial(db, tg_id):
    u = db.get_user(tg_id)
    return bool(u and not u["is_bound"] and not u["trial_used"])

@router.callback_query(F.data == "client:main")
async def client_main(cb: CallbackQuery, state: FSMContext, db, api):
    await state.clear()
    await state.set_state(ClientStates.main)
    await cb.answer()
    await send_customer_home(cb.message)

@router.callback_query(F.data == "client:help")
async def client_help(cb: CallbackQuery):
    await cb.answer()
    await send_customer_help(cb.message)

@router.callback_query(F.data == "client:trial")
async def client_trial(cb, state, db, api):
    user = db.get_user(cb.from_user.id)
    if not user or user["is_bound"] or user["trial_used"]:
        await cb.answer("Пробный период уже использован или у тебя есть подписка", show_alert=True)
        return
    # Atomically reserve the one-time trial BEFORE provisioning so a double-tap / two
    # devices can't both pass the check above and create two free accounts.
    if not db.claim_trial(cb.from_user.id):
        await cb.answer("Пробный период уже активируется или использован", show_alert=True)
        return
    await cb.answer("🎁 Активирую пробный период…")
    try:
        email, sub = provision_client(api, db, cb.from_user.id, config.TRIAL_DAYS)
    except Exception:
        db.unclaim_trial(cb.from_user.id)  # provisioning failed → let them retry
        await safe_edit(cb,
            f"⚠️ Не удалось активировать пробный период. Напиши в поддержку @{config.SUPPORT_USERNAME}.",
            reply_markup=admin_back_kb("client:main"))
        return
    # Trial gets the full maestro 5-protocol subscription (like paid clients), NOT the
    # 1-protocol 3x-ui link — best first impression + connects on RU-DPI. Fallback to 3x-ui.
    maestro = await _claim_sub_url(email)
    link = clean_subscription_url(maestro or sub or "#")
    await safe_edit(cb, trial_activated(config.TRIAL_DAYS, link),
                    reply_markup=profile_kb(True, bool(maestro or sub)), disable_web_page_preview=True)

@router.callback_query(F.data == "client:buy")
async def client_buy(cb, state, db, api):
    tariffs = db.get_tariffs()
    await safe_edit(cb, buy_intro(), reply_markup=tariffs_kb("buy", tariffs))
    await state.set_state(ClientStates.choosing_tariff)

@router.callback_query(F.data == "client:renew")
async def client_renew(cb, state, db, api):
    tariffs = db.get_tariffs()
    await safe_edit(cb, renew_intro(), reply_markup=tariffs_kb("renew", tariffs))
    await state.set_state(ClientStates.choosing_tariff)

@router.callback_query(F.data.startswith("client:tariff:"))
async def client_tariff_selected(cb, state, db, api):
    _, _, action, days_str = cb.data.split(":")
    days = int(days_str)
    tariffs = db.get_tariffs()
    price = tariffs.get(days, 0)
    if not price:
        await cb.answer("❌ Тариф не найден", show_alert=True)
        return
    user = db.get_user(cb.from_user.id)
    # Продление возможно только при активной привязке; покупка — всегда (клиент создастся при оплате)
    if action == "renew" and (not user or not user["is_bound"]):
        await safe_edit(cb, "ℹ️ У тебя ещё нет активной подписки.\nОформи покупку 👇",
                        reply_markup=tariffs_kb("buy", db.get_tariffs()))
        return
    await state.update_data(action=action, days=days, amount=price)
    await safe_edit(cb, tariff_card(days, price), reply_markup=payment_kb(0))
    await state.set_state(ClientStates.waiting_payment)

@router.callback_query(F.data.startswith("client:paid:"))
async def client_paid(cb, state, db, api):
    data = await state.get_data()
    if not all(data.get(key) for key in ("days", "amount", "action")):
        if db.has_pending_order(cb.from_user.id):
            await cb.answer("Ваша заявка уже ожидает проверки. Повторно отправлять её не нужно.", show_alert=True)
            return
        await safe_edit(cb, "Это сообщение об оплате устарело. Выберите тот же срок и отправьте заявку заново. "
                        "Если перевод уже сделан, повторно переводить деньги не нужно.",
                        reply_markup=admin_back_kb("mc:renew:menu"))
        return
    await safe_edit(cb, 
        "📸 <b>Пришли скриншот перевода</b>\n\nИли нажми кнопку ниже:",
        reply_markup=skip_photo_kb()
    )
    await state.set_state(ClientStates.waiting_photo)

async def _submit_order(bot, state, db, msg_target, tg_id, username, photo_id=None):
    data = await state.get_data()
    days = data.get("days")
    amount = data.get("amount")
    action = data.get("action")
    if not days or not amount or action not in {"buy", "renew"}:
        await state.clear()
        await msg_target.answer("Не удалось восстановить выбранный тариф. Выберите срок заново. "
                                "Если перевод уже сделан, повторно платить не нужно.",
                                reply_markup=admin_back_kb("mc:renew:menu"))
        return
    # Clear FSM state FIRST so a fast second tap (photo + «Пропустить») can't create a 2nd
    # order for the same payment; and dedup any already-pending order from this user.
    await state.set_state(ClientStates.main)
    if db.has_pending_order(tg_id):
        await msg_target.answer("⏳ Заявка уже отправлена администратору. Подтверждение придёт сюда после проверки перевода.")
        return
    user = db.get_user(tg_id)
    inbound_id = user["inbound_id"] if user else None
    client_email = user["client_email"] if user else None
    order_id = db.create_order(tg_id, inbound_id, client_email, days, amount, action, photo_id)
    await msg_target.answer(payment_sent(order_id, days, amount))
    for admin_id in config.ADMIN_IDS:
        try:
            if photo_id:
                await bot.send_photo(admin_id, photo_id,
                    caption=admin_order_notification(order_id, tg_id, username or "", days, amount, action, True),
                    reply_markup=admin_order_kb(order_id))
            else:
                await bot.send_message(admin_id,
                    admin_order_notification(order_id, tg_id, username or "", days, amount, action),
                    reply_markup=admin_order_kb(order_id))
        except Exception:
            pass
    await state.set_state(ClientStates.main)

@router.callback_query(F.data == "client:skip_photo", ClientStates.waiting_photo)
async def client_skip_photo_btn(cb, state, db, api):
    try:
        await cb.message.edit_reply_markup(reply_markup=None)
    except Exception:
        pass  # old/uneditable message must NOT block order creation
    await _submit_order(cb.bot, state, db, cb.message, cb.from_user.id, cb.from_user.username)
    await cb.answer()

@router.message(F.photo, ClientStates.waiting_photo)
async def client_photo_received(msg, state, db, api):
    photo_id = msg.photo[-1].file_id
    await _submit_order(msg.bot, state, db, msg, msg.from_user.id, msg.from_user.username, photo_id)

@router.callback_query(F.data == "client:cancel")
async def client_cancel(cb, state):
    await safe_edit(cb, "❌ Отменено", reply_markup=None)
    await state.clear()

@router.callback_query(F.data == "client:keys")
async def client_keys(cb, state, db, api):
    user = db.get_user(cb.from_user.id)
    if not user or not user["is_bound"] or not user["client_email"]:
        await safe_edit(cb, "❌ Не привязан", reply_markup=admin_back_kb("client:main"))
        return
    try:
        result = api.get_client_by_email(user["client_email"])
    except Exception:
        result = None
    if not result:
        sub_url = clean_subscription_url(await _claim_sub_url(user["client_email"]))
        text = "Не удалось обновить срок обычного VPN. Это не означает, что оплаченная подписка закончилась."
        if sub_url:
            text += "\n\nСсылка-подписка:\n<code>" + escape(sub_url) + "</code>"
        await safe_edit(cb, text, reply_markup=profile_kb(False, bool(sub_url)), disable_web_page_preview=True)
        return
    client, _ = result
    # Prefer the maestro 5-protocol subscription (Karing imports all 5: VLESS·Reality,
    # Hysteria2, Naive, AnyTLS + 2nd VLESS); fall back to the 3x-ui VLESS-only sub if the
    # panel is unreachable so the card never breaks.
    maestro = await _claim_sub_url(user["client_email"])
    sub_url = clean_subscription_url(maestro or api.subscription_url(client.sub_id) or "#")
    has_active = client.enable and (not client.expiry_time or client.expiry_time > datetime.now().timestamp() * 1000)
    has_key = bool(maestro) or bool(client.sub_id)
    await safe_edit(cb,
        profile_card(client, sub_url),
        reply_markup=profile_kb(has_active, has_key),
        disable_web_page_preview=True,
    )

@router.callback_query(F.data == "client:qr")
async def client_qr(cb, state, db, api):
    user = db.get_user(cb.from_user.id)
    if not user or not user["is_bound"] or not user["client_email"]:
        await cb.answer("❌ Аккаунт не привязан", show_alert=True)
        return
    result = api.get_client_by_email(user["client_email"])
    # Maestro 5-protocol sub for the QR (Karing imports all 5); fall back to 3x-ui sub.
    maestro = await _claim_sub_url(user["client_email"])
    sub_url = clean_subscription_url(
        maestro or (api.subscription_url(result[0].sub_id) if result else None)
    )
    if not sub_url:
        await cb.answer("❌ Подписка не найдена", show_alert=True)
        return
    await cb.answer("📷 Генерирую QR-код…")
    png = _make_qr_png(sub_url)
    photo = BufferedInputFile(png, filename="vpn_sub_qr.png")
    caption = (
        "📷 <b>QR-код обычной подписки VPN</b>\n\n"
        "В приложении <b>Karing</b> нажми «＋» → <i>«Добавить подписку»</i> → "
        "<i>«Сканировать QR-код»</i> и наведи камеру на это изображение.\n\n"
        "✅ Подписка обновляется автоматически — сервер всегда актуальный."
    )
    await cb.message.answer_photo(photo, caption=caption, reply_markup=close_kb())

@router.callback_query(F.data == "client:close")
async def client_close(cb, state):
    try:
        await cb.message.delete()
    except Exception:
        await cb.answer()

@router.callback_query(F.data == "client:howto")
async def client_howto(cb, state, db, api):
    # Single, clear entry to connecting: pick the device first, then show exact steps.
    await safe_edit(cb, connect_choose(), reply_markup=connect_os_kb())


def _bound_login(db, tg_id):
    u = db.get_user(tg_id)
    if not u or not u["is_bound"] or not u["client_email"]:
        return None
    return u["client_email"]


async def _claim_sub_url(login):
    """Bridge the bot client into the maestro panel → the multi-protocol sub_url (or None)."""
    import httpx
    try:
        async with httpx.AsyncClient(timeout=30) as c:
            r = await c.post(f"{_MAESTRO_URL}/claim", json={"code": login})
        if r.status_code != 200:
            return None
        return (r.json() or {}).get("sub_url") or None
    except Exception:
        return None


async def _ordinary_sub_url(login, api):
    url = await _claim_sub_url(login)
    if not url:
        try:
            result = api.get_client_subscription(login)
            url = result[1] if result else None
        except Exception:
            return None
    return clean_subscription_url(url)


_NEED_SUB = "Сначала оформи подписку — кнопка «💳 Купить подписку» 👇"


@router.callback_query(F.data == "client:connect:android")
async def client_connect_android(cb, state, db, api):
    login = _bound_login(db, cb.from_user.id)
    if not login:
        await cb.answer(_NEED_SUB, show_alert=True)
        return
    await safe_edit(cb, connect_android(login, _APP_DOWNLOAD_URL),
                    reply_markup=connect_android_kb(_APP_DOWNLOAD_URL), disable_web_page_preview=True)


@router.callback_query(F.data == "client:connect:ios")
async def client_connect_ios(cb, state, db, api):
    login = _bound_login(db, cb.from_user.id)
    if not login:
        await cb.answer(_NEED_SUB, show_alert=True)
        return
    await cb.answer("Готовлю ссылку…")
    sub_url = await _ordinary_sub_url(login, api)
    if not sub_url:
        await cb.answer(f"Не удалось получить подписку. Напиши в поддержку @{config.SUPPORT_USERNAME}.", show_alert=True)
        return
    karing_url = clean_subscription_url(sub_url)
    await safe_edit(cb, connect_ios(karing_url), reply_markup=connect_other_kb(), disable_web_page_preview=True)
    await cb.message.answer_photo(
        BufferedInputFile(_make_qr_png(karing_url), filename="maestrovpn_sub.png"),
        caption="📷 <b>QR ссылки-подписки</b> — в Karing нажми «＋» → «Сканировать QR» → наведи на этот код.",
        parse_mode="HTML",
    )


@router.callback_query(F.data == "client:connect:desktop")
async def client_connect_desktop(cb, state, db, api):
    login = _bound_login(db, cb.from_user.id)
    if not login:
        await cb.answer(_NEED_SUB, show_alert=True)
        return
    await cb.answer("Готовлю ссылку…")
    sub_url = await _ordinary_sub_url(login, api)
    if not sub_url:
        await cb.answer(f"Не удалось получить подписку. Напиши в поддержку @{config.SUPPORT_USERNAME}.", show_alert=True)
        return
    await safe_edit(cb, connect_desktop(clean_subscription_url(sub_url)),
                    reply_markup=connect_other_kb(), disable_web_page_preview=True)

@router.callback_query(F.data == "client:history")
async def client_history(cb, state, db, api):
    orders = db.get_orders_by_user(cb.from_user.id)
    await safe_edit(cb, order_history(orders), reply_markup=admin_back_kb("client:main"))

@router.callback_query(F.data == "client:referrals")
async def client_referrals(cb, state, db, api):
    user = db.get_user(cb.from_user.id)
    count = db.get_referral_count(cb.from_user.id) if user else 0
    earnings = db.get_referral_earnings(cb.from_user.id) if user else 0
    link = f"https://t.me/{config.BOT_USERNAME}?start={cb.from_user.id}"
    # One-tap share: opens Telegram's «share to…» picker pre-filled with a friendly
    # pitch + the referral link. The friend taps the link → /start parses ?start=<id>
    # → set_referrer → on their first paid order both get bonus days (admin payout).
    share_text = ("🛡 MaestroVPN — стабильный VPN для Android и ТВ, работает в России, "
                  "без рекламы. Заходи по моей ссылке — будет бонус к первой подписке 🎁")
    share_url = f"https://t.me/share/url?url={quote(link, safe='')}&text={quote(share_text, safe='')}"
    await safe_edit(cb,
        referral_info(cb.from_user.id, count, earnings, link),
        reply_markup=referral_kb(share_url)
    )
