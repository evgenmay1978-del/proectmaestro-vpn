from aiogram import Router, F
from aiogram.types import CallbackQuery, Message, ForceReply, InlineKeyboardButton
from aiogram.filters import Command, StateFilter
from aiogram.fsm.context import FSMContext
from keyboards import *
from formatters import *
from config import config
from states import AdminStates
from ui import safe_edit
from backup import run_backup_job
from html import escape
import uuid
import os
import asyncio
import httpx
from datetime import datetime, timezone, timedelta
from aiogram.exceptions import TelegramRetryAfter
from provision import provision_client
from subscription_links import clean_subscription_url
from functools import wraps

router = Router()
_order_locks = {}

# Unified-account renewal: route an owner-confirmed RENEWAL through the maestro panel,
# which is the SINGLE expiry authority — it writes the 3x-ui date AND the store AND the
# Hy2/Naive membership in one shot. We call this INSTEAD of the bot's own relative
# bulkAdjust (api.extend_client) so days are never double-counted by two clocks.
_MAESTRO_URL = os.getenv("MAESTRO_URL", "http://127.0.0.1:8910")
_MAESTRO_ADMIN_TOKEN = os.getenv("MAESTRO_ADMIN_TOKEN", "")


async def _maestro_renew(login: str, days: int, idempotency_key=None):
    """POST {panel}/admin/renew {login, days}. Raises on failure so admin_approve
    surfaces the error (no silent loss) instead of half-renewing."""
    headers = {"Authorization": f"Bearer {_MAESTRO_ADMIN_TOKEN}"}
    if idempotency_key:
        headers["Idempotency-Key"] = idempotency_key
    async with httpx.AsyncClient(timeout=30) as c:
        r = await c.post(
            f"{_MAESTRO_URL}/admin/renew",
            json={"login": login, "days": int(days)},
            headers=headers,
        )
        r.raise_for_status()


async def _maestro_sub_url(login: str):
    """The maestro 5-protocol sub_url (Karing imports all 5: VLESS·Reality, Hysteria2,
    Naive, AnyTLS + 2nd VLESS) or None on failure."""
    try:
        async with httpx.AsyncClient(timeout=30) as c:
            r = await c.post(f"{_MAESTRO_URL}/claim", json={"code": login})
        if r.status_code != 200:
            return None
        return (r.json() or {}).get("sub_url") or None
    except Exception:
        return None

def is_admin(user_id):
    return user_id in config.ADMIN_IDS


def _serialize_order(handler):
    """Approve and reject share the same lock and re-read status inside it."""
    @wraps(handler)
    async def wrapped(cb, state, db, api):
        if not is_admin(cb.from_user.id):
            return
        try:
            order_id = int(cb.data.rsplit(":", 1)[1])
        except (ValueError, IndexError):
            await cb.answer("Неверный номер заявки.", show_alert=True)
            return
        async with _order_locks.setdefault(order_id, asyncio.Lock()):
            return await handler(cb, state, db, api)
    return wrapped


def _admin_home_text(db):
    pending = len(db.get_pending_orders())
    return ("🔐 <b>Администратор MaestroVPN</b>\n\n"
            f"Обычный VPN: <b>{pending}</b> заявок на проверку оплаты.\n"
            "Выберите заявку и проверьте перевод перед подтверждением.\n\n"
            "В карточке клиента: срок VPN, добавление дней, установка даты и CDN. "
            "Поиск принимает логин, Telegram ID или имя пользователя.")


@router.message(Command("admin"))
async def admin_command(msg, state, db, api):
    if not is_admin(msg.from_user.id):
        await msg.answer("Доступ только для администратора.")
        return
    await state.clear()
    await msg.answer(_admin_home_text(db), reply_markup=admin_main_kb(len(db.get_pending_orders())))

@router.callback_query(F.data == "admin:main")
async def admin_main(cb, state, db, api):
    if not is_admin(cb.from_user.id):
        await cb.answer("⛔ Доступ запрещён", show_alert=True)
        return
    await state.clear()
    await safe_edit(cb, _admin_home_text(db), reply_markup=admin_main_kb(len(db.get_pending_orders())))

@router.callback_query(F.data == "admin:backup")
async def admin_backup(cb, state, db, api):
    if not is_admin(cb.from_user.id):
        await cb.answer("⛔ Доступ запрещён", show_alert=True)
        return
    await cb.answer("💾 Создаю бэкап…")
    await run_backup_job(cb.bot)

@router.callback_query(F.data == "admin:orders")
@router.callback_query(F.data.startswith("admin:orders_page:"))
async def admin_orders(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    orders = db.get_pending_orders()
    await state.clear()
    page = int(cb.data.rsplit(":", 1)[1]) if cb.data.startswith("admin:orders_page:") else 0
    page = min(max(page, 0), max(0, (len(orders) - 1) // 10))
    await safe_edit(cb, f"🧾 <b>Оплаты обычного VPN</b>\n\nОжидают проверки: {len(orders)}.\n"
                    "Откройте заявку, сверьте сумму и чек с фактическим переводом.",
                    reply_markup=admin_orders_list_kb(orders, page))


@router.callback_query(F.data.startswith("admin:order:"))
async def admin_order_show(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    order = db.get_order(int(cb.data.rsplit(":", 1)[1]))
    if not order or order["status"] != "pending":
        await cb.answer("Заявка уже обработана или не найдена.", show_alert=True)
        return
    user = db.get_user(order["tg_id"])
    text = admin_order_notification(order["id"], order["tg_id"], user["username"] if user else "",
                                    order["days"], order["amount"], order["type"], bool(order["photo_id"]))
    text += "\n\nПодтверждение выдаст оплаченный срок."
    kb = admin_order_kb(order["id"])
    kb.inline_keyboard.append([InlineKeyboardButton(text="◀️ К заявкам", callback_data="admin:orders")])
    if order["photo_id"]:
        await cb.message.answer_photo(order["photo_id"], caption=text, reply_markup=kb)
        await cb.answer()
    else:
        await safe_edit(cb, text, reply_markup=kb)

async def _maybe_referral_payout(cb, db, api, order, friend_email):
    """Двусторонний бонус с защитой от накруток.
    Платим только на ПЕРВУЮ оплаченную покупку друга, один раз."""
    if order["referral_bonus_given"]:
        return
    buyer = db.get_user(order["tg_id"])
    if not buyer or not buyer["referred_by"]:
        return
    # анти-накрутка: только ПЕРВАЯ оплаченная покупка друга. Вызывается ДО пометки заказа
    # approved → 0 прежних approved = это первая покупка.
    if db.count_approved_orders(order["tg_id"]) != 0:
        return
    ref = db.get_user(buyer["referred_by"])
    if not ref or not ref["is_bound"] or not ref["client_email"]:
        return
    # анти-накрутка: разные люди и разные ключи
    if ref["tg_id"] == buyer["tg_id"] or ref["client_email"] == friend_email:
        return
    # Пригласившему — через ЕДИНУЮ ОСЬ maestro (panel /admin/renew = 3x-ui + store + все
    # протоколы), а НЕ 3x-ui-only api.extend_client (иначе бонус повиснет на VLESS-инбаунде
    # и не виден 5-протокольному клиенту). Помечаем «выдан» ТОЛЬКО после реального начисления.
    try:
        await _maestro_renew(ref["client_email"], config.REFERRAL_BONUS_DAYS)
    except Exception:
        return  # панель недоступна → НЕ помечаем выдан, бонус доначислится при ретрае
    db.mark_referral_bonus_given(order["id"])
    try:
        await cb.bot.send_message(
            ref["tg_id"],
            f"🎉 <b>Реферальный бонус!</b>\nТвой друг оформил подписку — "
            f"тебе начислено <b>+{config.REFERRAL_BONUS_DAYS} дней</b> 🚀\nСпасибо, что приглашаешь!")
    except Exception:
        pass
    # другу +3 дн (best-effort)
    try:
        await _maestro_renew(friend_email, config.REFERRAL_FRIEND_BONUS_DAYS)
        await cb.bot.send_message(
            order["tg_id"],
            f"🎁 <b>Бонус новичку!</b>\nТы пришёл по приглашению — "
            f"<b>+{config.REFERRAL_FRIEND_BONUS_DAYS} дня</b> в подарок к подписке!")
    except Exception:
        pass

@router.callback_query(F.data.startswith("admin:approve:"))
@_serialize_order
async def admin_approve(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    order_id = int(cb.data.split(":")[2])
    order = db.get_order(order_id)
    if not order or order["status"] != "pending":
        await cb.answer("Уже обработан", show_alert=True)
        return
    # Новая покупка без привязанного ключа → создаём клиента в панели.
    is_new = order["type"] == "buy" and not order["client_email"]
    try:
        if is_new:
            email, _ = provision_client(api, db, order["tg_id"], order["days"], order["inbound_id"])
        else:
            email = order["client_email"]
            # Single expiry authority: the panel renews 3x-ui + store + all 4 protocols
            # in one shot (replaces the bot's relative bulkAdjust → no double-counting).
            await _maestro_renew(email, order["days"], f"s1-order:{order_id}")
    except Exception as e:
        await cb.answer(f"⚠️ Ошибка панели: {e}", show_alert=True)
        return
    # referral payout BEFORE marking approved → «первая покупка» = нет прежних approved
    await _maybe_referral_payout(cb, db, api, order, email)
    db.update_order_status(order_id, "approved")
    # Prefer the maestro 5-protocol subscription (Karing imports all 5); fall back to the
    # 3x-ui VLESS-only sub link so the client still gets something if the panel is down.
    link = clean_subscription_url(await _maestro_sub_url(email))
    if not link:
        try:
            sub = api.get_client_subscription(email)
            link = clean_subscription_url(sub[1] if sub else None)
        except Exception:
            link = None
    try:
        await cb.bot.send_message(order["tg_id"], client_approved(order_id, order["days"], link or "#"))
    except Exception:
        pass
    await safe_edit(cb, admin_order_approved(order_id, order["days"]), reply_markup=None)

@router.callback_query(F.data.startswith("admin:reject:"))
@_serialize_order
async def admin_reject(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    order_id = int(cb.data.split(":")[2])
    order = db.get_order(order_id)
    if not order or order["status"] != "pending":
        await cb.answer("Уже обработан", show_alert=True)
        return
    db.update_order_status(order_id, "rejected")
    try:
        await cb.bot.send_message(order["tg_id"], client_rejected(order_id))
    except Exception:
        pass
    await safe_edit(cb, admin_order_rejected(order_id), reply_markup=None)

@router.callback_query(F.data == "admin:clients")
@router.callback_query(F.data.startswith("admin:clients_page:"))
async def admin_clients(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    if cb.data == "admin:clients":
        await state.clear()
    data = await state.get_data()
    query = data.get("client_query", "")
    try:
        clients = api.get_all_clients()
    except Exception as e:
        await safe_edit(cb, f"⚠️ Панель недоступна: {escape(str(e))}", reply_markup=admin_back_kb("admin:main"))
        return
    if query:
        clients = _filter_clients(clients, db, query)
    clients.sort(key=lambda c: (c.expiry_time or 0))
    page = int(cb.data.rsplit(":", 1)[1]) if cb.data.startswith("admin:clients_page:") else 0
    page = min(max(page, 0), max(0, (len(clients) - 1) // 12))
    await safe_edit(cb,
        f"👥 <b>Клиенты ({len(clients)})</b>\n" + (f"Поиск: {escape(query)}\n" if query else "")
        + "Выберите клиента для управления:",
        reply_markup=admin_clients_list_kb(clients, page, bool(query)))


def _filter_clients(clients, db, query):
    query = query.casefold().lstrip("@").strip()
    bound = {u["client_email"] for u in db.get_all_users()
             if u["client_email"] and (query in str(u["tg_id"]) or query in (u["username"] or "").casefold())}
    return [c for c in clients if query in c.email.casefold() or c.email in bound]


@router.callback_query(F.data == "admin:search")
async def admin_search_start(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    await state.clear()
    await state.set_state(AdminStates.client_search)
    await cb.message.answer("🔎 Введите логин клиента, Telegram ID или @имя. Отмена — /admin.",
                            reply_markup=ForceReply(input_field_placeholder="Логин, Telegram ID или @имя"))
    await cb.answer()


@router.message(F.text, AdminStates.client_search)
async def admin_search_result(msg, state, db, api):
    if not is_admin(msg.from_user.id): return
    query = msg.text.strip()
    if not query:
        return
    try:
        clients = _filter_clients(api.get_all_clients(), db, query)
    except Exception:
        await msg.answer("Не удалось получить клиентов. Повторите поиск позже или откройте /admin.")
        return
    await state.set_state(None)
    await state.update_data(client_query=query)
    clients.sort(key=lambda c: (c.expiry_time or 0))
    await msg.answer(f"🔎 <b>Найдено: {len(clients)}</b>\nЗапрос: {escape(query)}",
                     reply_markup=admin_clients_list_kb(clients, searching=True))

async def _show_client(cb, db, api, email):
    r = api.get_client(email)
    if not r:
        await cb.answer("❌ Клиент не найден", show_alert=True)
        return
    client, _ = r
    u = db.get_user_by_client_email(email)
    tg_id = u["tg_id"] if u else None
    uname = u["username"] if u else None
    text = admin_client_card(client, tg_id, uname)
    if tg_id:
        orders = db.get_orders_by_user(tg_id, limit=3)
        text += "\n\n" + order_history(orders)
    await safe_edit(cb, text,
                    reply_markup=admin_client_detail_kb(email, client.enable, bool(tg_id)))

@router.callback_query(F.data.startswith("aclshow:"))
async def admin_client_show(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    # Opening/returning to a client card is a navigation, not a text-input step. Clear any
    # dangling FSM state (e.g. a "write to client" prompt the admin backed out of) so a later
    # stray message can't be delivered to the wrong client from a stale msg_target.
    await state.clear()
    await _show_client(cb, db, api, cb.data.split(":", 1)[1])

@router.callback_query(F.data.startswith("aclext:"))
async def admin_client_extend_menu(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    email = cb.data.split(":", 1)[1]
    await state.update_data(extend_email=email, extend_intent=uuid.uuid4().hex)
    await safe_edit(cb, f"🔄 На сколько продлить <b>{escape(email)}</b>?",
                    reply_markup=admin_client_extend_kb(email))

@router.callback_query(F.data.startswith("acldo:"))
async def admin_client_do_extend(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    _, email, days = cb.data.split(":", 2)
    data = await state.get_data()
    intent = data.get("extend_intent") if data.get("extend_email") == email else None
    intent = intent or f"legacy:{cb.message.chat.id}:{cb.message.message_id}"
    try:
        if int(days) <= 0:
            raise ValueError("invalid days")
        await _maestro_renew(email, int(days), f"s1-days:{intent}:{email}:{days}")
    except Exception:
        await cb.answer("Не удалось подтвердить продление. Срок не помечен как начисленный; повторите эту же операцию позже.", show_alert=True)
        return
    await cb.answer(f"✅ +{days} дн начислено")
    u = db.get_user_by_client_email(email)
    if u:
        try:
            await cb.bot.send_message(u["tg_id"], f"✅ Тебе продлили подписку на <b>+{days} дней</b> 🚀")
        except Exception:
            pass
    await _show_client(cb, db, api, email)


@router.callback_query(F.data.startswith("acldays:"))
async def admin_client_days_start(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    email = cb.data.split(":", 1)[1]
    await state.clear()
    await state.update_data(edit_client=email)
    await state.set_state(AdminStates.client_days)
    await cb.message.answer(f"➕ Сколько дней добавить клиенту <b>{escape(email)}</b>?\n"
                            "Введите положительное целое число. Отмена — /admin.",
                            reply_markup=ForceReply(input_field_placeholder="Количество дней"))
    await cb.answer()


@router.message(F.text, AdminStates.client_days)
async def admin_client_days_input(msg, state, db, api):
    if not is_admin(msg.from_user.id): return
    try:
        days = int(msg.text.strip())
        if days <= 0:
            raise ValueError
    except ValueError:
        await msg.answer("Введите положительное целое число дней.")
        return
    data = await state.get_data()
    email = data["edit_client"]
    intent = uuid.uuid4().hex
    await state.update_data(edit_days=days, edit_intent=intent)
    await state.set_state(None)
    kb = InlineKeyboardBuilder()
    kb.button(text=f"✅ Добавить {days} дн.", callback_data=f"acldaysapply:{intent}")
    kb.button(text="Отмена", callback_data=f"aclshow:{email}")
    kb.adjust(1)
    await msg.answer(f"Добавить <b>{days} {days_word(days)}</b> клиенту <b>{escape(email)}</b>?\n"
                     "Оставшийся оплаченный срок сохранится. Пакет CDN не изменится.", reply_markup=kb.as_markup())


@router.callback_query(F.data.startswith("acldaysapply:"))
async def admin_client_days_apply(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    data = await state.get_data()
    intent = cb.data.split(":", 1)[1]
    if data.get("edit_intent") != intent or not data.get("edit_client") or not data.get("edit_days"):
        await cb.answer("Откройте карточку клиента и задайте дни заново.", show_alert=True)
        return
    email, days = data["edit_client"], int(data["edit_days"])
    try:
        await _maestro_renew(email, days, f"s1-days:{intent}")
    except Exception:
        await cb.answer("Панель не подтвердила продление. Повторите эту же кнопку позже.", show_alert=True)
        return
    await state.clear()
    await cb.answer(f"Добавлено {days} дн.")
    await _show_client(cb, db, api, email)


@router.callback_query(F.data.startswith("acldate:"))
async def admin_client_date_start(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    email = cb.data.split(":", 1)[1]
    await state.clear()
    await state.update_data(edit_client=email)
    await state.set_state(AdminStates.client_expiry)
    await cb.message.answer(f"📆 Новая дата окончания для <b>{escape(email)}</b>.\n\n"
                            "Введите ДД.ММ.ГГГГ или ДД.ММ.ГГГГ ЧЧ:ММ по московскому времени. "
                            "Без времени — 23:59. Это заменит текущую дату; перед применением будет подтверждение. "
                            "Отмена — /admin.", reply_markup=ForceReply(input_field_placeholder="ДД.ММ.ГГГГ ЧЧ:ММ, МСК"))
    await cb.answer()


@router.message(F.text, AdminStates.client_expiry)
async def admin_client_date_input(msg, state, db, api):
    if not is_admin(msg.from_user.id): return
    raw = msg.text.strip()
    try:
        if " " in raw:
            expires = datetime.strptime(raw, "%d.%m.%Y %H:%M")
        else:
            expires = datetime.strptime(raw, "%d.%m.%Y").replace(hour=23, minute=59)
        expires = expires.replace(tzinfo=timezone(timedelta(hours=3)))
    except ValueError:
        await msg.answer("Не удалось прочитать дату. Пример формата: 25.12.2026 23:59 (МСК).")
        return
    data = await state.get_data()
    email, intent = data["edit_client"], uuid.uuid4().hex
    await state.update_data(edit_expiry=expires.isoformat(), edit_intent=intent)
    await state.set_state(None)
    kb = InlineKeyboardBuilder()
    kb.button(text="✅ Установить дату", callback_data=f"acldateapply:{intent}")
    kb.button(text="Отмена", callback_data=f"aclshow:{email}")
    kb.adjust(1)
    warning = "\nЭта дата уже прошла: обычная подписка станет истёкшей." if expires <= datetime.now(timezone.utc) else ""
    await msg.answer(f"📆 <b>{escape(email)}</b>\nНовая дата: <b>{expires:%d.%m.%Y %H:%M} МСК</b>.\n"
                     "Текущая дата будет заменена. Гигабайты CDN не изменятся." + warning,
                     reply_markup=kb.as_markup())


@router.callback_query(F.data.startswith("acldateapply:"))
async def admin_client_date_apply(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    data = await state.get_data()
    intent = cb.data.split(":", 1)[1]
    if data.get("edit_intent") != intent or not data.get("edit_client") or not data.get("edit_expiry"):
        await cb.answer("Откройте карточку клиента и задайте дату заново.", show_alert=True)
        return
    email = data["edit_client"]
    try:
        async with httpx.AsyncClient(timeout=30) as http:
            response = await http.post(f"{_MAESTRO_URL}/admin/set-expiry",
                json={"login": email, "expires": data["edit_expiry"]},
                headers={"Authorization": f"Bearer {_MAESTRO_ADMIN_TOKEN}",
                         "Idempotency-Key": f"s1-date:{intent}"})
            response.raise_for_status()
    except Exception:
        await cb.answer("Панель не подтвердила дату. Повторите эту же кнопку позже.", show_alert=True)
        return
    await state.clear()
    await cb.answer("Дата установлена.")
    await _show_client(cb, db, api, email)

@router.callback_query(F.data.startswith("acltog:"))
async def admin_client_toggle(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    email = cb.data.split(":", 1)[1]
    r = api.get_client(email)
    if not r:
        await cb.answer("❌ Не найден", show_alert=True)
        return
    new_enable = not r[0].enable
    try:
        api.set_client_enable(email, new_enable)
    except Exception as e:
        await cb.answer(f"⚠️ {e}", show_alert=True)
        return
    await cb.answer("✅ Разблокирован" if new_enable else "⛔️ Заблокирован")
    await _show_client(cb, db, api, email)

@router.callback_query(F.data.startswith("aclmsg:"))
async def admin_client_msg_start(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    email = cb.data.split(":", 1)[1]
    u = db.get_user_by_client_email(email)
    if not u:
        await cb.answer("❌ Клиент не привязан к боту", show_alert=True)
        return
    await state.update_data(msg_target=u["tg_id"], msg_email=email)
    await state.set_state(AdminStates.client_message)
    # ForceReply opens the keyboard focused on a reply with a visible placeholder, so the
    # admin gets a real INPUT FIELD. The old version only edited the card into a text hint —
    # many admins read that as "there is no way to write" and never typed. The reply lands in
    # admin_client_msg_send (F.text + AdminStates.client_message). /start cancels (it switches
    # state to ClientStates.main). Not a media edit, so no safe_edit needed here.
    await cb.message.answer(
        f"✍️ <b>Сообщение для {escape(email)}</b>\n"
        f"Ответь на это сообщение текстом — оно уйдёт клиенту в бота. Отмена — /start.",
        reply_markup=ForceReply(input_field_placeholder=f"Сообщение для {email}…"),
    )
    await cb.answer()

@router.message(F.text, AdminStates.client_message)
async def admin_client_msg_send(msg, state, db, api):
    if not is_admin(msg.from_user.id): return
    data = await state.get_data()
    target = data.get("msg_target")
    await state.clear()
    try:
        await msg.bot.send_message(target, f"📩 <b>Сообщение от поддержки</b>\n\n{msg.html_text}")
        await msg.answer("✅ Отправлено клиенту")
    except Exception as e:
        await msg.answer(f"⚠️ Не удалось отправить: {e}")

@router.callback_query(F.data == "admin:bindings")
async def admin_bindings(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    try:
        inbounds = api.get_inbounds()
    except Exception as e:
        await safe_edit(cb, f"⚠️ Панель недоступна: {escape(str(e))}", reply_markup=admin_back_kb("admin:main"))
        return
    await safe_edit(cb, "📡 <b>Выбери инбаунд:</b>", reply_markup=admin_bind_inbounds_kb(inbounds))

@router.callback_query(F.data.startswith("admin:bind_ib:"))
async def admin_bind_ib(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    inbound_id = int(cb.data.split(":")[2])
    clients = api.get_inbound_clients(inbound_id)
    await safe_edit(cb, "👤 <b>Выбери клиента:</b>", reply_markup=admin_bind_clients_kb(inbound_id, clients))

@router.callback_query(F.data.startswith("admin:bind_client:"))
async def admin_bind_client(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    parts = cb.data.split(":")
    inbound_id = int(parts[2])
    email = parts[3]
    token = str(uuid.uuid4())[:12]
    db.create_binding_token(token, inbound_id, email)
    link = f"https://t.me/{config.BOT_USERNAME}?start=bind_{token}"
    await safe_edit(cb, 
        f"🔗 <b>Ссылка для привязки:</b>\n\nКлиент: <code>{escape(email)}</code>\n"
        f"Инбаунд: <b>{inbound_id}</b>\n\n<code>{escape(link)}</code>\n\n"
        f"Срок: 24ч.",
        reply_markup=admin_back_kb("admin:bindings")
    )

@router.callback_query(F.data == "admin:stats")
async def admin_stats(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    users = db.get_all_users()
    orders = db.get_pending_orders()
    try:
        inbounds = api.get_inbounds()
        total = sum(ib.total_clients for ib in inbounds)
        ib_count = len(inbounds)
    except Exception:
        total = 0
        ib_count = 0
    ref_count = sum(1 for u in users if u["referred_by"])
    await safe_edit(cb, 
        f"📊 <b>Статистика:</b>\n\n"
        f"Пользователей: <b>{len(users)}</b>\n"
        f"Привязано: <b>{sum(1 for u in users if u['is_bound'])}</b>\n"
        f"Рефералов: <b>{ref_count}</b>\n"
        f"Инбаундов: <b>{ib_count}</b>\n"
        f"Клиентов 3x-ui: <b>{total}</b>\n"
        f"Заказов ожидает: <b>{len(orders)}</b>",
        reply_markup=admin_back_kb("admin:main")
    )

@router.callback_query(F.data == "admin:broadcast")
async def admin_broadcast_start(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    await safe_edit(cb, "📨 <b>Рассылка</b>\n\nОтправь текст:", reply_markup=admin_back_kb("admin:main"))
    await state.set_state(AdminStates.broadcast_text)

@router.message(F.text, AdminStates.broadcast_text)
async def admin_broadcast_send(msg, state, db, api):
    if not is_admin(msg.from_user.id): return
    users = db.get_all_users()
    count = 0
    for u in users:
        try:
            await msg.bot.send_message(u["tg_id"], msg.html_text)
            count += 1
        except TelegramRetryAfter as e:
            await asyncio.sleep(e.retry_after)
            try:
                await msg.bot.send_message(u["tg_id"], msg.html_text)
                count += 1
            except Exception:
                pass
        except Exception:
            pass  # blocked/deleted user
        await asyncio.sleep(0.05)  # ≤~20 msg/s — stay under Telegram's flood limit
    await msg.answer(broadcast_done(count))
    await state.clear()

@router.callback_query(F.data == "admin:settings")
async def admin_settings(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    tariffs = db.get_tariffs()
    await safe_edit(cb, admin_settings_text(tariffs), reply_markup=admin_tariffs_kb(tariffs))

@router.callback_query(F.data.startswith("admin:edit_tariff:"))
async def admin_edit_tariff(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    days = int(cb.data.split(":")[2])
    await state.update_data(edit_days=days)
    await safe_edit(cb, f"✏️ Тариф {days} дн\nВведи новую цену:", reply_markup=admin_back_kb("admin:settings"))
    await state.set_state(AdminStates.edit_tariff_price)

@router.message(F.text, AdminStates.edit_tariff_price)
async def admin_update_tariff(msg, state, db, api):
    if not is_admin(msg.from_user.id): return
    try:
        price = int(msg.text.strip())
        if price <= 0: raise ValueError
    except ValueError:
        await msg.answer("❌ Введи число > 0")
        return
    data = await state.get_data()
    days = data["edit_days"]
    tariffs = db.get_tariffs()
    tariffs[days] = price
    db.set_tariffs(tariffs)
    await state.clear()
    await msg.answer(f"✅ Тариф {days} дн → {price}₽", reply_markup=admin_back_kb("admin:settings"))

@router.callback_query(F.data == "admin:add_tariff")
async def admin_add_tariff_start(cb, state, db, api):
    if not is_admin(cb.from_user.id): return
    await safe_edit(cb, "➕ Добавить тариф\nВведи количество дней:", reply_markup=admin_back_kb("admin:settings"))
    await state.set_state(AdminStates.add_tariff_days)

@router.message(F.text, AdminStates.add_tariff_days)
async def admin_add_tariff_days(msg, state, db, api):
    if not is_admin(msg.from_user.id): return
    try:
        days = int(msg.text.strip())
        if days <= 0: raise ValueError
    except ValueError:
        await msg.answer("❌ Введи число > 0")
        return
    await state.update_data(new_days=days)
    await msg.answer(f"Дней: {days}\nТеперь цену в рублях:")
    await state.set_state(AdminStates.add_tariff_price)

@router.message(F.text, AdminStates.add_tariff_price)
async def admin_add_tariff_price(msg, state, db, api):
    if not is_admin(msg.from_user.id): return
    try:
        price = int(msg.text.strip())
        if price <= 0: raise ValueError
    except ValueError:
        await msg.answer("❌ Введи число > 0")
        return
    data = await state.get_data()
    days = data["new_days"]
    tariffs = db.get_tariffs()
    tariffs[days] = price
    db.set_tariffs(tariffs)
    await state.clear()
    await msg.answer(f"✅ Добавлен: {days} дн — {price}₽", reply_markup=admin_back_kb("admin:settings"))


# Registered LAST + StateFilter(None): fires only for an ADMIN's free text when NO flow is
# active. Previously such text was silently dropped (aiogram "update not handled"), so an admin
# who opened a client card and started typing — the natural "выбрал клиента → пишу" reflex — saw
# nothing happen and reported «написать нельзя». Now it's an actionable hint, never a dead end.
# Gated to admins so ordinary customers' stray text is untouched (still not-handled, as before).
@router.message(F.text, StateFilter(None), F.from_user.id.in_(set(config.ADMIN_IDS)))
async def admin_text_fallback(msg, state, db, api):
    await msg.answer(
        "✍️ Чтобы <b>написать клиенту</b>: 👥 <b>Клиенты</b> → выбери клиента → "
        "✍️ <b>Написать клиенту</b>, затем отправь текст.\n\n"
        "Открыть меню — /start",
    )
