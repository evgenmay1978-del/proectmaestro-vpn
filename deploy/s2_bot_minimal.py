#!/usr/bin/env python3
"""NaiveProxy VPN Bot — Enhanced Edition"""
import os, asyncio, logging, sqlite3, secrets, string, io, html, threading, re, hashlib
from datetime import datetime, timedelta, timezone
from contextlib import contextmanager
from typing import Optional

import httpx

import qrcode
from qrcode.image.pure import PyPNGImage
from apscheduler.schedulers.asyncio import AsyncIOScheduler
from aiogram import Bot, Dispatcher, types, F
from aiogram.filters import Command
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from aiogram.fsm.storage.memory import MemoryStorage
from aiogram.types import (
    Message, CallbackQuery,
    ReplyKeyboardMarkup, KeyboardButton,
    InlineKeyboardMarkup, InlineKeyboardButton,
)
from aiogram.client.default import DefaultBotProperties
from dotenv import load_dotenv

from subscription_links import clean_subscription_url

load_dotenv("/opt/vpn_bot/.env", override=True)
BOT_TOKEN    = os.getenv("BOT_TOKEN")
ADMIN_ID     = int(os.getenv("ADMIN_ID", "0"))
PANEL_URL    = os.getenv("PANEL_URL")
PANEL_USER   = os.getenv("PANEL_USER")
PANEL_PASS   = os.getenv("PANEL_PASS")
DOMAIN       = os.getenv("DOMAIN", "wapmix.duckdns.org")
PAYMENT_PHONE = os.getenv("PAYMENT_PHONE", "").strip()
PAYMENT_BANK = os.getenv("PAYMENT_BANK", "").strip()
PAYMENT_NAME = os.getenv("PAYMENT_NAME", "").strip()
DB_PATH      = os.getenv("DB_PATH", "/opt/vpn_bot/bot_minimal.db")
LOG_PATH     = os.getenv("LOG_PATH", "/opt/vpn_bot/bot_minimal.log")
SUPPORT_USER = os.getenv("SUPPORT_USER", "@wapmixx")

TARIFFS = {30: 400, 60: 800, 90: 1200, 180: 2400, 365: 4800}
TARIFF_LABELS = {30: "1 месяц", 60: "2 месяца", 90: "3 месяца", 180: "6 месяцев", 365: "12 месяцев"}

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(message)s",
    handlers=[logging.StreamHandler(), logging.FileHandler(LOG_PATH)],
)
logger = logging.getLogger("vpn_bot")

bot = Bot(token=BOT_TOKEN, default=DefaultBotProperties(parse_mode="HTML"))
dp  = Dispatcher(storage=MemoryStorage())

from maestro_customer import (build_customer_router_from_env,
    not_maestro_customer_message, not_maestro_customer_callback)
dp.include_router(build_customer_router_from_env())
scheduler = AsyncIOScheduler(timezone="Europe/Moscow")


# ═══════════════════════════════════════════════════════
# FSM STATES
# ═══════════════════════════════════════════════════════
class AdminFSM(StatesGroup):
    add_user  = State()   # "логин пароль [дней]"
    link_user = State()   # "логин tg_id"
    broadcast = State()   # любой текст
    bind_link = State()   # логин для ссылки-привязки
    set_days  = State()   # ввод количества дней для выбранного пользователя
    search_user = State()
    set_date = State()


# ═══════════════════════════════════════════════════════
# DATABASE
# ═══════════════════════════════════════════════════════
def _init_db(conn: sqlite3.Connection):
    """Create tables and run any pending migrations. Called once per connection."""
    conn.executescript("""
        CREATE TABLE IF NOT EXISTS binds (
            tg_id INTEGER PRIMARY KEY, proxy_user TEXT UNIQUE
        );
        CREATE TABLE IF NOT EXISTS payments (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            tg_id INTEGER, username TEXT, tariff_days INTEGER, amount INTEGER,
            status TEXT DEFAULT 'pending',
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        );
        CREATE TABLE IF NOT EXISTS subscriptions (
            tg_id INTEGER PRIMARY KEY,
            proxy_user TEXT,
            expires_at TIMESTAMP,
            notified_7d INTEGER DEFAULT 0,
            notified_3d INTEGER DEFAULT 0
        );
    """)
    # Migration: promo_code column (added by modules version, may be missing in older DBs)
    try:
        conn.execute("ALTER TABLE payments ADD COLUMN promo_code TEXT")
        conn.commit()
        logger.info("DB migration: added payments.promo_code")
    except Exception:
        pass  # Column already exists


@contextmanager
def get_db():
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    _init_db(conn)
    conn.commit()
    try:
        yield conn
    finally:
        conn.close()


def db_get_bind(tg_id: int) -> Optional[str]:
    with get_db() as db:
        r = db.execute("SELECT proxy_user FROM binds WHERE tg_id=?", (tg_id,)).fetchone()
    return r["proxy_user"] if r else None


def _unclaim_payment(payment_id):
    """Roll the single-flight claim back to pending if the panel op failed."""
    with get_db() as db:
        db.execute("UPDATE payments SET status='pending' WHERE id=? AND status='approved'",
                   (payment_id,))
        db.commit()


def payment_from_callback(cb, action):
    """Resolve old notices without ever claiming another pending request."""
    parts = (cb.data or "").split(":")
    with get_db() as db:
        if len(parts) == 2 and parts[0] == f"{action}_id" and parts[1].isdigit():
            return db.execute("SELECT * FROM payments WHERE id=?", (int(parts[1]),)).fetchone()
        if not parts or parts[0] != action:
            return None
        if action == "approve" and len(parts) == 4 and parts[1].isdigit() and parts[3].isdigit():
            query = "SELECT * FROM payments WHERE tg_id=? AND username=? AND tariff_days=?"
            values = [int(parts[1]), parts[2], int(parts[3])]
        elif action == "reject" and len(parts) == 2 and parts[1].isdigit():
            query = "SELECT * FROM payments WHERE tg_id=?"
            values = [int(parts[1])]
        else:
            return None
        # Both historic payment notice variants contain their exact request number.
        text = (getattr(cb.message, "text", None) or getattr(cb.message, "caption", None) or "")
        match = re.search(r"(?:плат[её]ж)\s*#(\d+)", text, re.IGNORECASE)
        if not match:
            return None  # Reopen the payments list; don't guess from a reused old button.
        return db.execute(query + " AND id=?", (*values, int(match.group(1)))).fetchone()


def maestro_sync_expiry(proxy_user: str, expires_at: datetime):
    """Мгновенное зеркало новой даты подписки в maestro-панель (/admin/set-expiry) —
    «единый организм»: оплата, подтверждённая в этом боте, сразу доезжает до панели
    (и веером до x-ui S1/S3, Hy2, AnyTLS). Только для логинов, УЖЕ известных панели:
    незнакомые оставляем hourly-сверятелю на S1 (он матчит без учёта регистра и не
    плодит дубли). Любая ошибка здесь НЕ ломает платёжный флоу — лог + страховка тем
    же сверятелем (сходимость ≤1ч). Работает в фоновом потоке, event loop не блокирует."""
    def _do():
        url = (os.getenv("MAESTRO_SYNC_URL") or "").rstrip("/")
        tok = os.getenv("MAESTRO_ADMIN_TOKEN") or ""
        if not url or not tok:
            return
        try:
            hdr = {"Authorization": f"Bearer {tok}"}
            r = httpx.get(f"{url}/admin/customer", params={"login": proxy_user},
                          headers=hdr, timeout=10)
            if r.status_code == 404:
                logger.info(f"maestro_sync: {proxy_user} не в панели — оставлен hourly-сверятелю")
                return
            iso = expires_at.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
            r = httpx.post(f"{url}/admin/set-expiry",
                           json={"login": proxy_user, "expires": iso},
                           headers=hdr, timeout=30)
            logger.info(f"maestro_sync: {proxy_user} -> {iso} [{r.status_code}]")
        except Exception as e:
            logger.error(f"maestro_sync: {proxy_user}: {e} (подстрахует hourly-сверятель)")
    threading.Thread(target=_do, daemon=True).start()



def db_set_sub(tg_id: int, proxy_user: str, expires_at: datetime):
    # UPSERT (NOT INSERT OR REPLACE) so an existing row's sub_url is PRESERVED on renew.
    # INSERT OR REPLACE re-created the row and wiped sub_url -> a renewal silently dropped
    # a 5-protocol client back to the 1-protocol naive link.
    with get_db() as db:
        db.execute("""
            INSERT INTO subscriptions
                (tg_id, proxy_user, expires_at, notified_7d, notified_3d)
            VALUES (?, ?, ?, 0, 0)
            ON CONFLICT(tg_id) DO UPDATE SET
                sub_url=CASE WHEN subscriptions.proxy_user=excluded.proxy_user
                             THEN subscriptions.sub_url ELSE NULL END,
                proxy_user=excluded.proxy_user,
                expires_at=excluded.expires_at,
                notified_7d=0,
                notified_3d=0
        """, (tg_id, proxy_user, expires_at.isoformat()))
        db.commit()
    maestro_sync_expiry(proxy_user, expires_at)


def db_mark_notified(tg_id: int, field: str):
    with get_db() as db:
        db.execute(f"UPDATE subscriptions SET {field}=1 WHERE tg_id=?", (tg_id,))
        db.commit()


def db_all_binds():
    with get_db() as db:
        return db.execute("SELECT * FROM binds").fetchall()


def db_pending_count() -> int:
    with get_db() as db:
        return db.execute("SELECT COUNT(*) as c FROM payments WHERE status='pending'").fetchone()["c"]


# ═══════════════════════════════════════════════════════
# PANEL CLIENT (async, auto-relogin)
# ═══════════════════════════════════════════════════════
class Panel:
    def __init__(self):
        self._client: Optional[httpx.AsyncClient] = None
        self._cookies: dict = {}

    async def _cli(self) -> httpx.AsyncClient:
        if self._client is None or self._client.is_closed:
            self._client = httpx.AsyncClient(timeout=15)
        return self._client

    async def login(self) -> bool:
        try:
            c = await self._cli()
            r = await c.post(f"{PANEL_URL}/api/login",
                             json={"username": PANEL_USER, "password": PANEL_PASS})
            if r.status_code == 200 and r.json().get("success"):
                self._cookies = dict(r.cookies)
                logger.info("Panel login OK")
                return True
        except Exception as e:
            logger.error(f"Panel login error: {e}")
        return False

    async def _req(self, method: str, path: str, **kw):
        """One retry with re-login on 401/403."""
        for attempt in range(2):
            try:
                c = await self._cli()
                r = await c.request(method, f"{PANEL_URL}{path}",
                                    cookies=self._cookies, **kw)
                if r.status_code in (401, 403) and attempt == 0:
                    await self.login()
                    continue
                return r
            except Exception as e:
                logger.error(f"Panel {method} {path}: {e}")
                if attempt == 0:
                    await self.login()
        return None

    async def users(self) -> list:
        r = await self._req("GET", "/api/naive/users")
        return r.json().get("users", []) if r and r.status_code == 200 else []

    async def find(self, username: str) -> Optional[dict]:
        us = await self.users()
        return next((x for x in us if x["username"] == username), None)

    async def add(self, username: str, password: str,
                  expires_at: Optional[datetime] = None) -> bool:
        payload = {"username": username, "password": password}
        if expires_at:
            payload["expireDays"] = max(1, min((expires_at - datetime.now()).days + 1, 3650))
        r = await self._req("POST", "/api/naive/users", json=payload)
        return r is not None and r.status_code in (200, 201)

    async def delete(self, username: str) -> bool:
        r = await self._req("DELETE", f"/api/naive/users/{username}")
        return r is not None and r.status_code in (200, 201, 204)


panel = Panel()


# ═══════════════════════════════════════════════════════
# HELPERS
# ═══════════════════════════════════════════════════════
def is_admin(tg_id) -> bool:
    return int(tg_id) == ADMIN_ID

def db_create_bind_token(proxy_user: str, ttl_hours: int = 168) -> str:
    tok = secrets.token_urlsafe(10)
    with get_db() as db:
        db.execute("CREATE TABLE IF NOT EXISTS binding_tokens (token TEXT PRIMARY KEY, proxy_user TEXT, expires_at TIMESTAMP, used INTEGER DEFAULT 0, tg_id INTEGER)")
        db.execute("INSERT INTO binding_tokens (token, proxy_user, expires_at, used) VALUES (?,?,datetime('now', ?), 0)", (tok, proxy_user, "+%d hours" % int(ttl_hours)))
        db.commit()
    return tok

def db_get_bind_token(token: str):
    with get_db() as db:
        try:
            r = db.execute("SELECT proxy_user FROM binding_tokens WHERE token=? AND used=0 AND expires_at>datetime('now')", (token,)).fetchone()
        except sqlite3.OperationalError:
            return None
    return r["proxy_user"] if r else None

def db_use_bind_token(token: str, tg_id: int):
    with get_db() as db:
        db.execute("UPDATE binding_tokens SET used=1, tg_id=? WHERE token=?", (tg_id, token))
        db.commit()

def make_key(u: str, p: str) -> str:
    return f"naive+https://{u}:{p}@{DOMAIN}:443"

def db_get_sub_url(proxy_user: str):
    with get_db() as db:
        r = db.execute("SELECT sub_url FROM subscriptions WHERE proxy_user=?", (proxy_user,)).fetchone()
    return r["sub_url"] if r and r["sub_url"] else None

def db_set_sub_url(proxy_user: str, sub_url: str):
    with get_db() as db:
        db.execute("UPDATE subscriptions SET sub_url=? WHERE proxy_user=?", (sub_url, proxy_user))
        db.commit()

def client_link(proxy_user: str, password: str) -> str:
    """Multi-protocol subscription URL (all 5 protocols, auto-updating) if the client is
    provisioned into the panel; else the legacy single naive link (fallback)."""
    return db_get_sub_url(proxy_user) or make_key(proxy_user, password)


def connection_label(proxy_user: str) -> str:
    return "Подписка MaestroVPN" if db_get_sub_url(proxy_user) else "Ключ NaiveProxy"


PANEL_MAX_DAYS = 3650  # naiveproxy panel silently treats >180 days as unlimited

def panel_safe_date(real_expires_at: datetime) -> datetime:
    """Return the date to push to the panel: min(real, now+179 days)."""
    cap = datetime.now() + timedelta(days=PANEL_MAX_DAYS)
    return min(real_expires_at, cap)


async def panel_renew(username: str, password: str, new_expires: datetime,
                      old_expires: Optional[datetime]) -> tuple:
    """Renew a naive user's expiry. The panel has no update endpoint, so this is
    delete+add — but if the re-add fails the user would be left with NO access (a
    silent total-loss on a transient panel hiccup). Guard: on failure, restore them at
    their PREVIOUS expiry (or unlimited) so a renewal error never revokes a paying
    customer. Returns (renewed_ok, restored_ok): renewed_ok=True ⇒ the new date is live;
    renewed_ok=False & restored_ok=True ⇒ renewal failed but the old access is intact."""
    if not await panel.delete(username):
        if await panel.find(username):
            return False, True
        restore_date = panel_safe_date(old_expires) if old_expires else None
        return False, await panel.add(username, password, restore_date)
    if await panel.add(username, password, panel_safe_date(new_expires)):
        return True, True
    restore_date = panel_safe_date(old_expires) if old_expires else None
    restored = await panel.add(username, password, restore_date)
    logger.error(f"panel_renew: re-add FAILED for {username}; restore={'ok' if restored else 'FAILED'}")
    return False, restored


def gen_pass(n=16) -> str:
    return ''.join(secrets.choice(string.ascii_letters + string.digits) for _ in range(n))

def days_left(exp_str: Optional[str]) -> int:
    if not exp_str:
        return -1
    try:
        d = datetime.fromisoformat(exp_str.replace("Z", "+00:00")).replace(tzinfo=None)
        return max(0, (d - datetime.now()).days)
    except:
        return -1

def fmt_exp(exp_str: Optional[str]) -> str:
    if not exp_str:
        return "навсегда"
    try:
        d = datetime.fromisoformat(exp_str.replace("Z", "+00:00")).replace(tzinfo=None)
        return d.strftime("%d.%m.%Y")
    except:
        return "?"

def sub_icon(d: int) -> tuple[str, str]:
    if d == -1: return "🟢", "Бессрочная"
    if d > 14:  return "🟢", "Активна"
    if d > 7:   return "🟡", "Активна"
    if d > 3:   return "🟠", "Заканчивается"
    if d > 0:   return "🔴", "Истекает"
    return "⚫", "Истекла"

def gen_qr(text: str) -> bytes:
    qr = qrcode.QRCode(box_size=10, border=2)
    qr.add_data(text)
    qr.make(fit=True)
    img = qr.make_image(image_factory=PyPNGImage)
    buf = io.BytesIO()
    img.save(buf)
    buf.seek(0)
    return buf.read()


# ═══════════════════════════════════════════════════════
# KEYBOARDS
# ═══════════════════════════════════════════════════════
def kb_main(tg_id) -> ReplyKeyboardMarkup:
    rows = [
        [KeyboardButton(text="🏠 Главная")],
    ]
    if is_admin(tg_id):
        rows.append([KeyboardButton(text="⚙️ Админ")])
    return ReplyKeyboardMarkup(keyboard=rows, resize_keyboard=True)

def kb_admin() -> ReplyKeyboardMarkup:
    return ReplyKeyboardMarkup(keyboard=[
        [KeyboardButton(text="👥 Юзеры"),     KeyboardButton(text="📈 Статистика")],
        [KeyboardButton(text="🔎 Найти клиента")],
        [KeyboardButton(text="➕ Добавить"),   KeyboardButton(text="🗑 Удалить")],
        [KeyboardButton(text="🔗 Привязать"), KeyboardButton(text="🔓 Отвязать")],
        [KeyboardButton(text="📲 Привязка по ссылке")],
        [KeyboardButton(text="💰 Платежи"),   KeyboardButton(text="📢 Рассылка")],
        [KeyboardButton(text="📅 Дни"),       KeyboardButton(text="🏠 Главная")],
    ], resize_keyboard=True)

def kb_admin_home() -> InlineKeyboardMarkup:
    return InlineKeyboardMarkup(inline_keyboard=[
        [InlineKeyboardButton(text="👥 Клиенты", callback_data="s2:users:0"),
         InlineKeyboardButton(text="🔎 Поиск", callback_data="s2:search")],
        [InlineKeyboardButton(text="💳 Оплаты VPN", callback_data="s2:payments"),
         InlineKeyboardButton(text="📶 Оплаты CDN", callback_data="mc:admin:orders")],
        [InlineKeyboardButton(text="📈 Статистика", callback_data="s2:stats")],
        [InlineKeyboardButton(text="⚙️ Другие действия", callback_data="s2:more")],
        [InlineKeyboardButton(text="🏠 Главное меню", callback_data="mc:home:main")],
    ])


def kb_tariffs() -> InlineKeyboardMarkup:
    rows = []
    for d, p in sorted(TARIFFS.items()):
        label = TARIFF_LABELS.get(d, f"{d} дн.")
        rows.append([InlineKeyboardButton(
            text=f"📦 {label} — {p}₽",
            callback_data=f"buy:{d}"
        )])
    rows.append([InlineKeyboardButton(text="🏠 Главная", callback_data="mc:home:main")])
    return InlineKeyboardMarkup(inline_keyboard=rows)

def kb_renew() -> InlineKeyboardMarkup:
    return InlineKeyboardMarkup(inline_keyboard=[
        [InlineKeyboardButton(text="🔄 Продлить подписку", callback_data="renew")]
    ])

def kb_approve_reject(payment_id: int) -> InlineKeyboardMarkup:
    return InlineKeyboardMarkup(inline_keyboard=[[
        InlineKeyboardButton(text="✅ Подтвердить",
                             callback_data=f"approve_id:{payment_id}"),
        InlineKeyboardButton(text="❌ Отклонить",
                             callback_data=f"reject_id:{payment_id}"),
    ]])


# ═══════════════════════════════════════════════════════
# SCHEDULED JOBS
# ═══════════════════════════════════════════════════════
async def job_check_expiry():
    """Runs every 6 hours. Notifies users at 7d and 3d before expiry."""
    us = await panel.users()
    panel_map = {u["username"]: u for u in us}

    with get_db() as db:
        subs = db.execute("SELECT * FROM subscriptions").fetchall()

    for sub in subs:
        pu = panel_map.get(sub["proxy_user"])
        if not pu:
            continue
        d = days_left(pu.get("expiresAt"))
        if d < 0:          # бессрочный — не трогаем
            continue
        tg_id = sub["tg_id"]

        if d <= 3 and not sub["notified_3d"]:
            try:
                await bot.send_message(
                    tg_id,
                    f"🔴 <b>Подписка истекает через {d} дн.!</b>\n\n"
                    f"Продлите сейчас, чтобы не прерывать доступ:",
                    reply_markup=kb_renew()
                )
                db_mark_notified(tg_id, "notified_3d")
                db_mark_notified(tg_id, "notified_7d")
            except Exception as e:
                logger.warning(f"Notify 3d → {tg_id}: {e}")

        elif d <= 7 and not sub["notified_7d"]:
            try:
                await bot.send_message(
                    tg_id,
                    f"🟠 <b>Подписка заканчивается через {d} дн.</b>\n\n"
                    f"Рекомендуем продлить заранее:",
                    reply_markup=kb_renew()
                )
                db_mark_notified(tg_id, "notified_7d")
            except Exception as e:
                logger.warning(f"Notify 7d → {tg_id}: {e}")


async def job_sync_panel_expiry():
    """Runs every 6 hours. Extends panel subscriptions running low
    when the real expiry (in DB) is still in the future."""
    with get_db() as db:
        subs = db.execute("SELECT * FROM subscriptions").fetchall()
    if not subs:
        return

    us = await panel.users()
    panel_map = {u["username"]: u for u in us}

    for sub in subs:
        real_str = sub["expires_at"]
        if not real_str:
            continue
        try:
            real_exp = datetime.fromisoformat(real_str)
            if real_exp.tzinfo is not None:
                real_exp = real_exp.replace(tzinfo=None)  # normalize → naive (was crashing vs datetime.now())
        except Exception:
            continue

        if real_exp <= datetime.now():
            continue  # already expired in DB

        pu = panel_map.get(sub["proxy_user"])
        if not pu:
            continue

        panel_days = days_left(pu.get("expiresAt"))
        if panel_days >= 0 and panel_days > 30:
            continue  # panel has plenty of time

        new_panel_date = panel_safe_date(real_exp)
        pwd = pu.get("password", "")
        await panel.delete(sub["proxy_user"])
        ok = await panel.add(sub["proxy_user"], pwd, new_panel_date)
        if ok:
            logger.info(
                f"sync_panel: extended {sub['proxy_user']} → "
                f"{new_panel_date.strftime('%d.%m.%Y')} "
                f"(real: {real_exp.strftime('%d.%m.%Y')})"
            )
        else:
            logger.error(f"sync_panel: failed to extend {sub['proxy_user']}")


async def job_daily_report():
    """Daily admin report at 09:00 Moscow."""
    us = await panel.users()
    total    = len(us)
    active   = sum(1 for u in us if days_left(u.get("expiresAt")) != 0)
    expiring = sum(1 for u in us if 0 < days_left(u.get("expiresAt")) <= 7)
    expired  = sum(1 for u in us if days_left(u.get("expiresAt")) == 0)

    with get_db() as db:
        pending   = db.execute("SELECT COUNT(*) as c FROM payments WHERE status='pending'").fetchone()["c"]
        month_rev = db.execute(
            "SELECT COALESCE(SUM(amount),0) as s FROM payments "
            "WHERE status='approved' AND created_at >= date('now','-30 days')"
        ).fetchone()["s"]

    await bot.send_message(
        ADMIN_ID,
        f"📊 <b>Ежедневный отчёт</b> — {datetime.now().strftime('%d.%m.%Y')}\n\n"
        f"👥 Всего: <b>{total}</b>  |  🟢 Активных: <b>{active}</b>\n"
        f"🟠 Истекают ≤7д: <b>{expiring}</b>  |  ⚫ Истекших: <b>{expired}</b>\n\n"
        f"💰 Ожидают подтверждения: <b>{pending}</b>\n"
        f"💵 Выручка за 30 дн.: <b>{month_rev}₽</b>"
    )


# ═══════════════════════════════════════════════════════
# /start  /cancel
# ═══════════════════════════════════════════════════════
from maestro_customer_entry import (with_customer_entry, send_customer_home,
    configure_customer_ui, customer_admin_button)

@dp.message(not_maestro_customer_message, Command("start"))
@dp.message(not_maestro_customer_message, F.text == "🏠 Главная")
@with_customer_entry
async def cmd_start(msg: Message, state: FSMContext):
    await state.clear()
    payload = (msg.text or "").split(maxsplit=1)
    if len(payload) == 2 and payload[1].startswith("bind_"):
        token = payload[1][5:].strip()
        pu = db_get_bind_token(token)
        if not pu:
            return await msg.answer("❌ Ссылка недействительна или истекла. Попроси новую.")
        with get_db() as db:
            db.execute("INSERT OR REPLACE INTO binds (tg_id, proxy_user) VALUES (?,?)", (msg.from_user.id, pu))
            db.commit()
        db_use_bind_token(token, msg.from_user.id)
        found = await panel.find(pu)
        pwd = found["password"] if found else ""
        k = client_link(pu, pwd)
        kind = "Подписка MaestroVPN" if db_get_sub_url(pu) else "Ключ NaiveProxy"
        detail = f"\n\n<b>{kind}:</b>\n<code>{html.escape(k)}</code>" if found or db_get_sub_url(pu) else ""
        await msg.answer(f"✅ Аккаунт <code>{html.escape(pu)}</code> привязан.{detail}",
                         reply_markup=types.ReplyKeyboardRemove())
    await send_customer_home(msg)


@dp.message(not_maestro_customer_message, Command("cancel"))
async def cmd_cancel(msg: Message, state: FSMContext):
    await state.clear()
    if is_admin(msg.from_user.id):
        await msg.answer("↩️ Отменено.", reply_markup=kb_admin_home())
    else:
        await send_customer_home(msg)


# ═══════════════════════════════════════════════════════
# HELP
# ═══════════════════════════════════════════════════════
@dp.message(not_maestro_customer_message, F.text == "ℹ️ Помощь")
@dp.message(not_maestro_customer_message, Command("help"))
async def cmd_help(msg: Message):
    await msg.answer(
        "📖 <b>Помощь — MAESTRO VPN</b>\n\n"
        "На главной виден срок обычного VPN. Кнопка продления открывает наши тарифы и реквизиты.\n"
        "После перевода нажмите «Я оплатил» и дождитесь подтверждения администратора.\n\n"
        "В разделе подключения выберите устройство и приложение. Для старого аккаунта NaiveProxy "
        "бот выдаст его действующий ключ, для MaestroVPN — ссылку-подписку.\n\n"
        "CDN — отдельный пакет ГБ для мобильных белых списков. Его покупка не продлевает обычный VPN.\n\n"
        f"Поддержка: {html.escape(SUPPORT_USER)}",
        reply_markup=InlineKeyboardMarkup(inline_keyboard=[
            [InlineKeyboardButton(text="🏠 Главная", callback_data="mc:home:main")],
            [InlineKeyboardButton(text="📲 Подключение", callback_data="mc:devices:menu")],
        ])
    )


# ═══════════════════════════════════════════════════════
# KEY
# ═══════════════════════════════════════════════════════
@dp.message(not_maestro_customer_message, F.text == "🔑 Ключ")
@dp.message(not_maestro_customer_message, Command("key"))
async def btn_key(msg: Message):
    u = db_get_bind(msg.from_user.id)
    if not u:
        return await msg.answer(
            "❌ <b>Вы не привязаны к VPN-аккаунту.</b>\n\n"
            "Приобретите подписку через <b>🛍 Тарифы</b>."
        )
    found = await panel.find(u)
    if not found:
        return await msg.answer("❌ Аккаунт не найден. Обратитесь к поддержке.")

    k = client_link(u, found["password"])
    qr_bytes = gen_qr(k)
    d = days_left(found.get("expiresAt"))
    icon, _ = sub_icon(d)
    days_txt = "∞" if d == -1 else f"{d} дн."

    await msg.answer_photo(
        types.BufferedInputFile(qr_bytes, filename="qr.png"),
        caption=(
            f"📲 <b>{connection_label(u)}</b>\n\n"
            f"<code>{html.escape(k)}</code>\n\n"
            f"{icon} Подписка: <b>{days_txt}</b>  |  до {fmt_exp(found.get('expiresAt'))}\n\n"
            f"📲 <b>Подключение (Karing):</b>\n"
            f"1. Скопируйте ссылку выше\n"
            f"2. Karing → <b>+</b> → <b>Импорт из буфера</b>\n"
            f"3. Нажмите «Подключить» ✅\n\n"
            f"<i>Ссылка даёт доступ к вашей подписке. Не передавайте её другим.</i>"
        )
    )
    if 0 < d <= 7:
        await msg.answer(
            f"⚠️ <b>Подписка истекает через {d} дн.!</b>",
            reply_markup=kb_renew()
        )


# ═══════════════════════════════════════════════════════
# STATUS
# ═══════════════════════════════════════════════════════
@dp.message(not_maestro_customer_message, F.text == "📊 Статус")
@dp.message(not_maestro_customer_message, Command("status"))
async def btn_status(msg: Message):
    u = db_get_bind(msg.from_user.id)
    if not u:
        return await msg.answer(
            "❌ <b>Вы не привязаны к VPN-аккаунту.</b>\n\n"
            "Приобретите подписку через <b>🛍 Тарифы</b>."
        )
    found = await panel.find(u)
    if not found:
        return await msg.answer("❌ Аккаунт не найден. Обратитесь к поддержке.")

    d = days_left(found.get("expiresAt"))
    icon, status_text = sub_icon(d)
    days_str = "∞" if d == -1 else str(d)

    markup = None
    if d != -1 and d <= 14:
        markup = kb_renew()

    await msg.answer(
        f"╔══════════════════╗\n"
        f"║  📊 <b>СТАТУС VPN</b>  ║\n"
        f"╚══════════════════╝\n\n"
        f"{icon} <b>{status_text}</b>\n"
        f"┌─────────────────┐\n"
        f"│ 👤 Логин: <code>{u}</code>\n"
        f"│ ⏳ Дней: <b>{days_str}</b>\n"
        f"│ 📅 До: {fmt_exp(found.get('expiresAt'))}\n"
        f"│ 🌍 {connection_label(u)}\n"
        f"└─────────────────┘",
        reply_markup=markup
    )


# ═══════════════════════════════════════════════════════
# TARIFFS & PAYMENT FLOW
# ═══════════════════════════════════════════════════════
@dp.message(not_maestro_customer_message, F.text == "🛍 Тарифы")
async def btn_tariffs(msg: Message):
    await msg.answer(
        "🛍 <b>Тарифы MAESTRO VPN</b>\n\n"
        "Обычный VPN: срок подписки оплачивается отдельно от CDN-пакета ГБ.\n"
        "Действующая подписка продлевается после проверки перевода.\n\n"
        "Выберите срок подписки:",
        reply_markup=kb_tariffs()
    )

@dp.callback_query(not_maestro_customer_callback, F.data == "renew")
async def cb_renew(cb: CallbackQuery):
    await cb.message.answer("🛍 <b>Выберите тариф для продления:</b>", reply_markup=kb_tariffs())
    await cb.answer()

@dp.callback_query(not_maestro_customer_callback, F.data.startswith("buy:"))
async def cb_buy(cb: CallbackQuery):
    try:
        days = int(cb.data.split(":")[1])
        price = TARIFFS[days]
    except (ValueError, KeyError, IndexError):
        return await cb.answer("Выберите действующий тариф.", show_alert=True)
    if not all((PAYMENT_PHONE, PAYMENT_BANK, PAYMENT_NAME)):
        return await cb.answer("Реквизиты временно недоступны. Обратитесь в поддержку.", show_alert=True)
    label = TARIFF_LABELS.get(days, f"{days} дн.")
    await cb.message.edit_text(
        f"💳 <b>Оплата подписки</b>\n\n"
        f"📦 Тариф: <b>{label}</b>\n"
        f"💰 Сумма: <b>{price}₽</b>\n\n"
        f"══════════════════\n"
        f"📱 Номер: <code>{html.escape(PAYMENT_PHONE)}</code>\n"
        f"🏦 Банк: {html.escape(PAYMENT_BANK)}\n"
        f"👤 Получатель: {html.escape(PAYMENT_NAME)}\n"
        f"══════════════════\n\n"
        f"⚠️ В комментарии напишите:\n"
        f"<code>VPN {cb.from_user.id}</code>\n\n"
        f"После перевода нажмите ✅:",
        reply_markup=InlineKeyboardMarkup(inline_keyboard=[
            [InlineKeyboardButton(text="✅ Я оплатил",
                                  callback_data=f"paid:{days}:{price}")],
            [InlineKeyboardButton(text="◀️ Назад к тарифам",
                                  callback_data="back_tariffs")],
        ])
    )
    await cb.answer()

@dp.callback_query(not_maestro_customer_callback, F.data == "back_tariffs")
async def cb_back_tariffs(cb: CallbackQuery):
    await cb.message.edit_text("🛍 <b>Выберите тариф:</b>", reply_markup=kb_tariffs())
    await cb.answer()

@dp.callback_query(not_maestro_customer_callback, F.data.startswith("paid:"))
async def cb_paid(cb: CallbackQuery):
    try:
        _, days, price = cb.data.split(":")
        days, price = int(days), int(price)
        if TARIFFS.get(days) != price:
            raise ValueError
    except (ValueError, TypeError):
        return await cb.answer("Тариф изменился. Откройте тарифы заново.", show_alert=True)
    tg_id    = cb.from_user.id
    u        = db_get_bind(tg_id)
    username = u or f"u{tg_id}"
    label    = TARIFF_LABELS.get(days, f"{days} дн.")

    # Guard: don't create duplicate pending payments for the same user+tariff
    with get_db() as db:
        existing = db.execute(
            "SELECT id FROM payments WHERE tg_id=? AND tariff_days=? AND status='pending'",
            (tg_id, days)
        ).fetchone()
        if existing:
            await cb.answer("⏳ Заявка уже отправлена, ожидайте подтверждения.", show_alert=True)
            return

        db.execute(
            "INSERT INTO payments (tg_id, username, tariff_days, amount, status) "
            "VALUES (?,?,?,?,'pending')",
            (tg_id, username, days, price)
        )
        pid = db.execute("SELECT last_insert_rowid()").fetchone()[0]
        db.commit()

    name = f"@{cb.from_user.username}" if cb.from_user.username else html.escape(cb.from_user.first_name or "")
    try:
        await bot.send_message(
            ADMIN_ID,
            f"💰 <b>Новый платёж #{pid}</b>\n\n"
            f"👤 {name} (ID: <code>{tg_id}</code>)\n"
            f"🔑 Логин: <code>{username}</code>\n"
            f"📦 Тариф: <b>{label}</b> ({days} дн.)\n"
            f"💵 Сумма: <b>{price}₽</b>",
            reply_markup=kb_approve_reject(pid)
        )
        await cb.message.edit_text(
            "✅ <b>Заявка отправлена!</b>\n\n"
            "Администратор проверит платёж и пришлёт подтверждение."
        )
    except Exception as e:
        logger.error(f"cb_paid notify/edit error for tg_id={tg_id}: {e}")
        # Payment is recorded; user still gets confirmation even if edit fails
        await cb.answer("✅ Заявка принята! Ожидайте подтверждения.", show_alert=True)
        return
    await cb.answer()


# ═══════════════════════════════════════════════════════
# APPROVE / REJECT
# ═══════════════════════════════════════════════════════
payment_account_locks = {}


@dp.callback_query(not_maestro_customer_callback, F.data.startswith("approve:"))
@dp.callback_query(not_maestro_customer_callback, F.data.startswith("approve_id:"))
async def cb_approve(cb: CallbackQuery):
    if not is_admin(cb.from_user.id):
        return await cb.answer("⛔")

    payment = payment_from_callback(cb, "approve")
    if payment is None:
        return await cb.answer("Не удалось определить платёж. Откройте «💰 Платежи».", show_alert=True)
    async with payment_account_locks.setdefault(payment["username"], asyncio.Lock()):
        await apply_payment(cb, payment)


async def apply_payment(cb, payment):
    payment_id = payment["id"]
    tg_id, username, days = payment["tg_id"], payment["username"], payment["tariff_days"]
    label    = TARIFF_LABELS.get(days, f"{days} дн.")

    # single-flight: атомарно «забираем» pending-платёж ДО операций с панелью, чтобы
    # двойной тап «✅ Подтвердить» не продлил подписку дважды. Откат при сбое панели.
    with get_db() as _db:
        _claimed = _db.execute(
            "UPDATE payments SET status='approved' WHERE id=? AND status='pending'",
            (payment_id,)).rowcount
        _db.commit()
    if not _claimed:
        return await cb.answer("Уже обработан", show_alert=True)

    found = await panel.find(username)

    if found:
        # ── Существующий пользователь: продление (delete+add с защитой от потери доступа) ──
        pwd = found["password"]
        old_exp = found.get("expiresAt")
        old_expires_dt = None
        base = datetime.now()
        if old_exp:
            try:
                old_expires_dt = datetime.fromisoformat(old_exp.replace("Z", "+00:00")).replace(tzinfo=None)
                base = max(old_expires_dt, datetime.now())
            except Exception as e:
                logger.error(f"cb_approve: failed to parse expiresAt={old_exp!r} for {username}: {e}")
                # base stays as datetime.now() — days are added from today
        expires_at = base + timedelta(days=days)

        renewed, restored = await panel_renew(username, pwd, expires_at, old_expires_dt)
        if not renewed:
            _unclaim_payment(payment_id)
            return await cb.answer(
                "❌ Ошибка обновления в панели. Прежний доступ клиента сохранён — повторите."
                if restored else
                "❌ Ошибка панели и восстановления. Доступ не подтверждён — проверьте панель.",
                show_alert=True)
    else:
        # ── Новый пользователь ──
        pwd        = gen_pass()
        expires_at = datetime.now() + timedelta(days=days)
        ok = await panel.add(username, pwd, panel_safe_date(expires_at))
        if not ok:
            _unclaim_payment(payment_id)
            return await cb.answer("❌ Ошибка создания в панели", show_alert=True)

    # Обновить БД
    with get_db() as db:
        db.execute("INSERT OR REPLACE INTO binds (tg_id, proxy_user) VALUES (?,?)",
                   (tg_id, username))
        db.commit()
    db_set_sub(tg_id, username, expires_at)

    # Provision/refresh the maestro 5-protocol subscription (VLESS-Reality, Hysteria2,
    # Naive, AnyTLS + 2nd VLESS) so a paid/renewed client gets ALL 5 - not the 1-protocol
    # naive link. Best-effort: if the panel is unreachable the prior sub_url / naive stands.
    try:
        _msub = await _claim_sub_url(username)
        if _msub:
            db_set_sub_url(username, _msub)
    except Exception as _e:
        logger.error(f"cb_approve: maestro claim failed for {username}: {_e}")

    exp_fmt = expires_at.strftime("%d.%m.%Y")
    await cb.message.edit_text(
        f"✅ <b>Подтверждено</b>  —  <code>{username}</code>\n"
        f"📅 Срок до: {exp_fmt}  |  TG: <code>{tg_id}</code>"
    )

    k = client_link(username, pwd)
    _is5 = bool(db_get_sub_url(username))
    if _is5:
        sub_line = (f"📲 <b>Ваша подписка MaestroVPN (5 протоколов):</b>\n<code>{k}</code>\n\n"
                    f"Импортируйте в Karing или наше приложение MaestroVPN — обновляется автоматически.")
    else:
        sub_line = (f"🔑 <b>Ваш ключ:</b>\n<code>{k}</code>\n\n"
                    f"📲 Установите <b>Karing</b> → + → Импорт из буфера.")
    if found:
        await bot.send_message(
            tg_id,
            f"🎉 <b>Подписка продлена!</b>\n\n"
            f"📦 Тариф: <b>{label}</b>\n"
            f"📅 Действует до: <b>{exp_fmt}</b>\n\n"
            f"{sub_line}"
        )
    else:
        await bot.send_message(
            tg_id,
            f"🎉 <b>Добро пожаловать в MAESTRO VPN!</b>\n\n"
            f"📦 Тариф: <b>{label}</b>\n"
            f"📅 Действует до: <b>{exp_fmt}</b>\n\n"
            f"{sub_line}"
        )
    await cb.answer()


@dp.callback_query(not_maestro_customer_callback, F.data.startswith("reject:"))
@dp.callback_query(not_maestro_customer_callback, F.data.startswith("reject_id:"))
async def cb_reject(cb: CallbackQuery):
    if not is_admin(cb.from_user.id):
        return await cb.answer("⛔")
    payment = payment_from_callback(cb, "reject")
    if payment is None:
        return await cb.answer("Не удалось определить платёж. Откройте «💰 Платежи».", show_alert=True)
    tg_id = payment["tg_id"]
    with get_db() as db:
        changed = db.execute("UPDATE payments SET status='rejected' WHERE id=? AND status='pending'",
                             (payment["id"],)).rowcount
        db.commit()
    if not changed:
        return await cb.answer("Уже обработан", show_alert=True)
    await cb.message.edit_text("❌ <b>Платёж отклонён</b>")
    await bot.send_message(
        tg_id,
        f"❌ <b>Платёж отклонён.</b>\n\n"
        f"Перевод не найден или указана неверная сумма.\n"
        f"Свяжитесь с поддержкой: {SUPPORT_USER}"
    )
    await cb.answer()


# ═══════════════════════════════════════════════════════
# ADMIN PANEL ENTRY
# ═══════════════════════════════════════════════════════
@dp.message(not_maestro_customer_message, F.text == "⚙️ Админ")
async def adm_panel(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    await state.clear()
    pending = db_pending_count()
    badge   = f" 🔴 {pending} платеж(ей)" if pending else ""
    await msg.answer(f"⚙️ <b>Админ-панель</b>{badge}", reply_markup=kb_admin_home())


# ─── USERS ────────────────────────────────────────────
def admin_user_key(username):
    return hashlib.sha256(username.encode()).hexdigest()[:20]


async def admin_user_from_key(key):
    return next((u for u in await panel.users() if admin_user_key(u["username"]) == key), None)


async def send_admin_users(message, page=0, query=""):
    response = await panel._req("GET", "/api/naive/users")
    if response is None or response.status_code != 200:
        return await message.answer("Панель сейчас недоступна. Повторите позже.")
    users = response.json().get("users", [])
    with get_db() as db:
        binds = {r["proxy_user"]: r["tg_id"] for r in db.execute("SELECT * FROM binds")}
    if query:
        users = [u for u in users if query.casefold() in u["username"].casefold()
                 or query == str(binds.get(u["username"], ""))]
    users.sort(key=lambda u: (days_left(u.get("expiresAt")) == -1,
                             days_left(u.get("expiresAt")), u["username"]))
    if not users:
        return await message.answer("Клиенты не найдены.", reply_markup=kb_admin())
    page = max(0, min(page, (len(users) - 1) // 12))
    rows = []
    for user in users[page * 12:(page + 1) * 12]:
        days = days_left(user.get("expiresAt"))
        period = "бессрочно" if days == -1 else f"{days} дн."
        rows.append([InlineKeyboardButton(text=f"{user['username']} · {period}",
                     callback_data=f"s2:user:{admin_user_key(user['username'])}")])
    # Search results are intentionally bounded; a narrower login/TG query finds any card.
    if not query:
        nav = []
        if page:
            nav.append(InlineKeyboardButton(text="← Назад", callback_data=f"s2:users:{page - 1}"))
        if (page + 1) * 12 < len(users):
            nav.append(InlineKeyboardButton(text="Далее →", callback_data=f"s2:users:{page + 1}"))
        if nav:
            rows.append(nav)
    rows.append([InlineKeyboardButton(text="🔎 Поиск", callback_data="s2:search")])
    rows.append([InlineKeyboardButton(text="⚙️ Админ", callback_data="s2:admin")])
    note = "\nУточните запрос, если нужный клиент не попал в первые 12." if query and len(users) > 12 else ""
    await message.answer(f"👥 <b>Клиенты: {len(users)}</b>\nВыберите карточку.{note}",
                         reply_markup=InlineKeyboardMarkup(inline_keyboard=rows))


async def send_admin_card(message, user):
    login = user["username"]
    key = admin_user_key(login)
    with get_db() as db:
        bind = db.execute("SELECT tg_id FROM binds WHERE proxy_user=?", (login,)).fetchone()
        sub = db.execute("SELECT expires_at FROM subscriptions WHERE proxy_user=?", (login,)).fetchone()
        payments = db.execute("SELECT id, amount, status FROM payments WHERE username=? ORDER BY id DESC LIMIT 3",
                              (login,)).fetchall()
    expiry = sub["expires_at"] if sub else user.get("expiresAt")
    text = (f"👤 <b>{html.escape(login)}</b>\n"
            f"Telegram: <code>{bind['tg_id'] if bind else 'не привязан'}</code>\n"
            f"Доступ: {connection_label(login)}\n"
            f"Срок: <b>{fmt_exp(expiry)}</b>\n\n")
    status_names = {"pending": "ожидает", "approved": "подтверждён", "rejected": "отклонён"}
    if payments:
        text += "Последние платежи:\n" + "\n".join(
            f"#{p['id']} · {p['amount']} ₽ · {status_names.get(p['status'], p['status'])}" for p in payments)
    else:
        text += "Платежей в этом боте нет."
    rows = [
        [InlineKeyboardButton(text="➕ Добавить дни", callback_data=f"s2:days:{key}"),
         InlineKeyboardButton(text="📅 Установить дату", callback_data=f"s2:date:{key}")],
        [customer_admin_button(login)],
        [InlineKeyboardButton(text="👥 Клиенты", callback_data="s2:users:0"),
         InlineKeyboardButton(text="⚙️ Админ", callback_data="s2:admin")],
    ]
    await message.answer(text, reply_markup=InlineKeyboardMarkup(inline_keyboard=rows))


@dp.callback_query(not_maestro_customer_callback, F.data.startswith("s2:users:"))
async def cb_admin_users(cb: CallbackQuery, state: FSMContext):
    if not is_admin(cb.from_user.id): return await cb.answer("⛔")
    await state.clear()
    await cb.answer()
    try:
        page = int(cb.data.rsplit(":", 1)[1])
    except ValueError:
        page = 0
    await send_admin_users(cb.message, page)


@dp.callback_query(not_maestro_customer_callback, F.data.startswith("s2:user:"))
async def cb_admin_card(cb: CallbackQuery, state: FSMContext):
    if not is_admin(cb.from_user.id): return await cb.answer("⛔")
    await state.clear()
    user = await admin_user_from_key(cb.data.rsplit(":", 1)[1])
    if not user:
        return await cb.answer("Клиент не найден или панель недоступна.", show_alert=True)
    await cb.answer()
    await send_admin_card(cb.message, user)


@dp.callback_query(not_maestro_customer_callback, F.data == "s2:admin")
async def cb_admin_home(cb: CallbackQuery, state: FSMContext):
    if not is_admin(cb.from_user.id): return await cb.answer("⛔")
    await state.clear()
    await cb.answer()
    await cb.message.answer(f"⚙️ <b>Админ-панель</b>\nОжидают оплаты: {db_pending_count()}", reply_markup=kb_admin_home())


@dp.callback_query(not_maestro_customer_callback, F.data.in_({"s2:payments", "s2:stats", "s2:more"}))
async def cb_admin_section(cb: CallbackQuery, state: FSMContext):
    if (not is_admin(cb.from_user.id) or not cb.message or cb.message.chat.type != "private"
            or cb.message.chat.id != cb.from_user.id):
        return await cb.answer("Доступ только администратору в личном чате.", show_alert=True)
    await state.clear()
    await cb.answer()
    if cb.data == "s2:payments":
        await send_admin_payments(cb.message)
    elif cb.data == "s2:stats":
        await send_admin_stats(cb.message)
    else:
        await cb.message.answer(
            "⚙️ <b>Другие действия</b>\n\n"
            "Добавление, удаление, привязка и рассылка доступны на клавиатуре под полем ввода. "
            "Если она скрыта, нажмите значок клавиатуры справа от поля ввода.\n\n"
            "Вернуться к основным кнопкам: /cancel",
            reply_markup=kb_admin())


@dp.message(not_maestro_customer_message, F.text == "🔎 Найти клиента")
async def adm_search(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    await state.set_state(AdminFSM.search_user)
    await msg.answer("Пришлите логин, часть логина или Telegram ID. Отмена: /cancel",
                     reply_markup=types.ReplyKeyboardRemove())


@dp.callback_query(not_maestro_customer_callback, F.data == "s2:search")
async def cb_admin_search(cb: CallbackQuery, state: FSMContext):
    if not is_admin(cb.from_user.id): return await cb.answer("⛔")
    await state.set_state(AdminFSM.search_user)
    await cb.answer()
    await cb.message.answer("Пришлите логин, часть логина или Telegram ID. Отмена: /cancel",
                            reply_markup=types.ReplyKeyboardRemove())


def admin_text_input(message):
    text = (message.text or "").strip()
    navigation = {button.text for row in kb_admin().keyboard for button in row}
    return bool(text) and text not in navigation and not text.startswith("/")


@dp.message(not_maestro_customer_message, AdminFSM.search_user, admin_text_input)
async def adm_search_input(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    await state.clear()
    await send_admin_users(msg, query=msg.text.strip())


@dp.callback_query(not_maestro_customer_callback, F.data.startswith("s2:days:"))
@dp.callback_query(not_maestro_customer_callback, F.data.startswith("s2:date:"))
async def cb_admin_expiry(cb: CallbackQuery, state: FSMContext):
    if not is_admin(cb.from_user.id): return await cb.answer("⛔")
    user = await admin_user_from_key(cb.data.rsplit(":", 1)[1])
    if not user:
        return await cb.answer("Клиент не найден или панель недоступна.", show_alert=True)
    set_date = cb.data.startswith("s2:date:")
    await state.set_state(AdminFSM.set_date if set_date else AdminFSM.set_days)
    await state.update_data(username=user["username"])
    await cb.answer()
    prompt = "Пришлите дату ДД.ММ.ГГГГ. Доступ будет действовать до конца этого дня по времени сервера." if set_date else "Сколько дней добавить к текущему сроку?"
    await cb.message.answer(f"<b>{html.escape(user['username'])}</b>\n{prompt}\nОтмена: /cancel",
                            reply_markup=types.ReplyKeyboardRemove())


@dp.message(not_maestro_customer_message, F.text == "👥 Юзеры")
async def adm_users(msg: Message):
    if not is_admin(msg.from_user.id): return
    await send_admin_users(msg)

# ─── STATS ────────────────────────────────────────────
@dp.message(not_maestro_customer_message, F.text == "📈 Статистика")
async def adm_stats(msg: Message):
    if not is_admin(msg.from_user.id): return
    await send_admin_stats(msg)


async def send_admin_stats(msg: Message):
    us       = await panel.users()
    total    = len(us)
    active   = sum(1 for u in us if days_left(u.get("expiresAt")) != 0)
    expiring = sum(1 for u in us if 0 < days_left(u.get("expiresAt")) <= 7)
    expired  = sum(1 for u in us if days_left(u.get("expiresAt")) == 0)
    eternal  = sum(1 for u in us if days_left(u.get("expiresAt")) == -1)

    with get_db() as db:
        pending   = db.execute("SELECT COUNT(*) as c FROM payments WHERE status='pending'").fetchone()["c"]
        approved  = db.execute("SELECT COUNT(*) as c FROM payments WHERE status='approved'").fetchone()["c"]
        total_rev = db.execute(
            "SELECT COALESCE(SUM(amount),0) as s FROM payments WHERE status='approved'"
        ).fetchone()["s"]
        month_rev = db.execute(
            "SELECT COALESCE(SUM(amount),0) as s FROM payments "
            "WHERE status='approved' AND created_at >= date('now','-30 days')"
        ).fetchone()["s"]

    await msg.answer(
        f"📈 <b>Статистика MAESTRO VPN</b>\n\n"
        f"👥 Всего пользователей: <b>{total}</b>\n"
        f"  🟢 Активных: <b>{active}</b>\n"
        f"  ♾ Бессрочных: <b>{eternal}</b>\n"
        f"  🟠 Истекают ≤7д: <b>{expiring}</b>\n"
        f"  ⚫ Истекших: <b>{expired}</b>\n\n"
        f"💰 <b>Финансы</b>\n"
        f"  ⏳ Ожидают: <b>{pending}</b>\n"
        f"  ✅ Подтверждено всего: <b>{approved}</b>\n"
        f"  💵 Выручка за 30д: <b>{month_rev}₽</b>\n"
        f"  💵 Выручка всего: <b>{total_rev}₽</b>",
        reply_markup=kb_admin_home()
    )


# ─── PAYMENTS ─────────────────────────────────────────
@dp.message(not_maestro_customer_message, F.text == "💰 Платежи")
async def adm_payments(msg: Message):
    if not is_admin(msg.from_user.id): return
    await send_admin_payments(msg)


async def send_admin_payments(msg: Message):
    with get_db() as db:
        rows = db.execute(
            "SELECT * FROM payments WHERE status='pending' ORDER BY id DESC LIMIT 10"
        ).fetchall()
    if not rows:
        return await msg.answer("✅ Нет ожидающих платежей.", reply_markup=kb_admin_home())
    for p in rows:
        label = TARIFF_LABELS.get(p["tariff_days"], f"{p['tariff_days']} дн.")
        await msg.answer(
            f"💰 <b>Платёж #{p['id']}</b>\n"
            f"👤 <code>{p['username']}</code> (TG: <code>{p['tg_id']}</code>)\n"
            f"📦 {label}  |  💵 {p['amount']}₽\n"
            f"📅 {p['created_at'][:16]}",
            reply_markup=kb_approve_reject(p["id"])
        )


# ─── ADD USER (FSM) ────────────────────────────────────
@dp.message(not_maestro_customer_message, F.text == "➕ Добавить")
async def adm_add_start(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    await state.set_state(AdminFSM.add_user)
    await msg.answer(
        "➕ <b>Добавление пользователя</b>\n\n"
        "Формат: <code>логин пароль [дней]</code>\n\n"
        "Примеры:\n"
        "  <code>ivan securepass 30</code>  — на месяц\n"
        "  <code>ivan securepass</code>  — бессрочный\n\n"
        "Отмена: /cancel",
        reply_markup=types.ReplyKeyboardRemove()
    )

@dp.message(not_maestro_customer_message, AdminFSM.add_user, admin_text_input)
async def adm_add_handle(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    parts = msg.text.strip().split()
    if len(parts) < 2:
        return await msg.answer("❌ Формат: <code>логин пароль [дней]</code>")

    u, p = parts[0], parts[1]
    days = int(parts[2]) if len(parts) >= 3 and parts[2].isdigit() else None
    expires_at = datetime.now() + timedelta(days=days) if days else None

    ok = await panel.add(u, p, expires_at)
    if ok:
        k = client_link(u, p)
        exp_txt = expires_at.strftime("%d.%m.%Y") if expires_at else "бессрочно"
        await msg.answer(
            f"✅ <b>Пользователь создан</b>\n\n"
            f"👤 <code>{u}</code>  |  📅 до {exp_txt}\n"
            f"🔑 <code>{k}</code>",
            reply_markup=kb_admin()
        )
    else:
        await msg.answer("❌ Ошибка. Возможно, логин уже существует.", reply_markup=kb_admin())
    await state.clear()


# ─── DELETE ────────────────────────────────────────────
@dp.message(not_maestro_customer_message, F.text == "🗑 Удалить")
async def adm_del(msg: Message):
    if not is_admin(msg.from_user.id): return
    us = await panel.users()
    if not us:
        return await msg.answer("Нет пользователей.")

    with get_db() as db:
        binds = {r["proxy_user"]: r["tg_id"]
                 for r in db.execute("SELECT * FROM binds").fetchall()}

    rows = []
    for u in us:
        d = days_left(u.get("expiresAt"))
        icon, _ = sub_icon(d)
        tg_note = f"·{binds[u['username']]}" if u["username"] in binds else ""
        rows.append([InlineKeyboardButton(
            text=f"{icon} {u['username']}{tg_note}",
            callback_data=f"del:{u['username']}"
        )])
    await msg.answer("🗑 Выберите для удаления:",
                     reply_markup=InlineKeyboardMarkup(inline_keyboard=rows))

@dp.callback_query(not_maestro_customer_callback, F.data.startswith("del:"))
async def cb_del(cb: CallbackQuery):
    if not is_admin(cb.from_user.id): return await cb.answer("⛔")
    u = cb.data.split(":")[1]
    ok = await panel.delete(u)
    if ok:
        with get_db() as db:
            db.execute("DELETE FROM binds WHERE proxy_user=?", (u,))
            db.execute("DELETE FROM subscriptions WHERE proxy_user=?", (u,))
            db.commit()
        await cb.message.edit_text(f"🗑 <code>{u}</code> удалён из панели и БД")
    else:
        await cb.answer("❌ Ошибка удаления", show_alert=True)


# ─── LINK (FSM) ────────────────────────────────────────
@dp.message(not_maestro_customer_message, F.text == "🔗 Привязать")
async def adm_link_start(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    await state.set_state(AdminFSM.link_user)
    await msg.answer(
        "🔗 <b>Привязка</b>\n\n"
        "Формат: <code>логин tg_id</code>\n"
        "Пример: <code>ivan 123456789</code>\n\n"
        "Отмена: /cancel",
        reply_markup=types.ReplyKeyboardRemove()
    )

@dp.message(not_maestro_customer_message, AdminFSM.link_user, admin_text_input)
async def adm_link_handle(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    parts = msg.text.strip().split()
    if len(parts) != 2 or not parts[1].lstrip("-").isdigit():
        return await msg.answer("❌ Формат: <code>логин tg_id</code>")

    u, tg = parts[0], int(parts[1])
    found = await panel.find(u)
    if not found:
        return await msg.answer(f"❌ <code>{u}</code> не найден в панели.")

    with get_db() as db:
        db.execute("INSERT OR REPLACE INTO binds (tg_id, proxy_user) VALUES (?,?)", (tg, u))
        db.commit()
    await msg.answer(f"🔗 <code>{u}</code> ↔ TG <code>{tg}</code>", reply_markup=kb_admin())
    await state.clear()


# ─── UNLINK ────────────────────────────────────────────
@dp.message(not_maestro_customer_message, F.text == "🔓 Отвязать")
async def adm_unlink(msg: Message):
    if not is_admin(msg.from_user.id): return
    with get_db() as db:
        rows = db.execute("SELECT * FROM binds").fetchall()
    if not rows:
        return await msg.answer("Нет привязанных пользователей.")
    btns = [[InlineKeyboardButton(
        text=f"{r['proxy_user']} → {r['tg_id']}",
        callback_data=f"unlink:{r['proxy_user']}"
    )] for r in rows]
    await msg.answer("🔓 Выберите для отвязки:",
                     reply_markup=InlineKeyboardMarkup(inline_keyboard=btns))

@dp.callback_query(not_maestro_customer_callback, F.data.startswith("unlink:"))
async def cb_unlink(cb: CallbackQuery):
    if not is_admin(cb.from_user.id): return await cb.answer("⛔")
    u = cb.data.split(":")[1]
    with get_db() as db:
        db.execute("DELETE FROM binds WHERE proxy_user=?", (u,))
        db.commit()
    await cb.message.edit_text(f"🔓 <code>{u}</code> отвязан")


# ─── SET DAYS (FSM) ────────────────────────────────────
@dp.message(not_maestro_customer_message, F.text == "📅 Дни")
async def adm_setdays_start(msg: Message):
    if not is_admin(msg.from_user.id): return
    us = await panel.users()
    if not us:
        return await msg.answer("Нет пользователей.")

    def sort_key(u):
        d = days_left(u.get("expiresAt"))
        return (1, d) if d >= 0 else (0, 9999)

    rows = []
    for u in sorted(us, key=sort_key):
        d = days_left(u.get("expiresAt"))
        icon, _ = sub_icon(d)
        days_str = "∞" if d == -1 else f"{d}д"
        rows.append([InlineKeyboardButton(
            text=f"{icon} {u['username']} — {days_str}",
            callback_data=f"setdays:{u['username']}"
        )])
    await msg.answer(
        "📅 <b>Изменить дни подписки</b>\n\nВыберите пользователя:",
        reply_markup=InlineKeyboardMarkup(inline_keyboard=rows)
    )


@dp.callback_query(not_maestro_customer_callback, F.data.startswith("setdays:"))
async def cb_setdays_pick(cb: CallbackQuery, state: FSMContext):
    if not is_admin(cb.from_user.id): return await cb.answer("⛔")
    username = cb.data.split(":")[1]
    found = await panel.find(username)
    if not found:
        return await cb.answer("❌ Пользователь не найден", show_alert=True)

    d = days_left(found.get("expiresAt"))
    days_str = "∞" if d == -1 else f"{d} дн."
    await state.set_state(AdminFSM.set_days)
    await state.update_data(username=username, password=found["password"],
                            old_exp=found.get("expiresAt"))
    await cb.message.edit_text(
        f"📅 <b>Добавление дней</b>\n\n"
        f"👤 Пользователь: <code>{username}</code>\n"
        f"⏳ Сейчас: <b>{days_str}</b>\n\n"
        f"Сколько дней добавить?\n\n"
        f"Отмена: /cancel"
    )
    await cb.answer()


@dp.message(not_maestro_customer_message, AdminFSM.set_days, admin_text_input)
@dp.message(not_maestro_customer_message, AdminFSM.set_date, admin_text_input)
async def adm_setdays_handle(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    set_date = await state.get_state() == AdminFSM.set_date.state
    if set_date:
        try:
            expires_at = datetime.strptime(msg.text.strip(), "%d.%m.%Y").replace(hour=23, minute=59, second=59)
        except ValueError:
            return await msg.answer("❌ Пришлите дату ДД.ММ.ГГГГ, например: 31.12.2026.")
        if not datetime.now() < expires_at <= datetime.now() + timedelta(days=PANEL_MAX_DAYS):
            return await msg.answer("❌ Дата должна быть в будущем и не дальше 3650 дней.")
    else:
        if not msg.text.strip().isdigit():
            return await msg.answer("❌ Введите целое число дней, например: <code>30</code>")
        days = int(msg.text.strip())
        if not 1 <= days <= 3650:
            return await msg.answer("❌ Допустимый диапазон: от 1 до 3650 дней.")
    data = await state.get_data()
    username = data.get("username")
    found = await panel.find(username) if username else None
    if not found:
        await state.clear()
        return await msg.answer("Клиент не найден или панель недоступна. Откройте карточку заново.", reply_markup=kb_admin())
    password = found["password"]
    old_exp = found.get("expiresAt")
    old_expires_dt = None
    base = datetime.now()
    if old_exp:
        try:
            old_expires_dt = datetime.fromisoformat(old_exp.replace("Z", "+00:00")).replace(tzinfo=None)
            base = max(old_expires_dt, datetime.now())
        except Exception as e:
            logger.error(f"set_days: failed to parse expiresAt={old_exp!r} for {username}: {e}")
    if not set_date:
        expires_at = base + timedelta(days=days)

    renewed, restored = await panel_renew(username, password, expires_at, old_expires_dt)
    if not renewed:
        await state.clear()
        return await msg.answer(
            "❌ Ошибка обновления в панели. Прежний доступ клиента сохранён — повторите."
            if restored else
            "❌ Ошибка панели и восстановления. Доступ не подтверждён — проверьте панель.",
            reply_markup=kb_admin())

    # Обновить подписку в БД если пользователь привязан
    with get_db() as db:
        bind = db.execute("SELECT tg_id FROM binds WHERE proxy_user=?", (username,)).fetchone()
    if bind:
        db_set_sub(bind["tg_id"], username, expires_at)
    else:
        maestro_sync_expiry(username, expires_at)

    exp_fmt = expires_at.strftime("%d.%m.%Y")
    await msg.answer(
        f"✅ <b>Готово!</b>\n\n"
        f"👤 <code>{username}</code>\n"
        f"📅 Подписка действует до <b>{exp_fmt}</b>",
        reply_markup=kb_admin()
    )
    await state.clear()


# ─── BIND-BY-LINK (FSM) ────────────────────────────────
BOT_USERNAME = os.getenv("BOT_USERNAME", "MaestroSecureNaive_bot")

@dp.message(not_maestro_customer_message, F.text == "📲 Привязка по ссылке")
async def adm_bindlink_start(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    await state.set_state(AdminFSM.bind_link)
    await msg.answer("📲 <b>Привязка по ссылке</b>\n\nПришлите <b>логин</b> клиента — бот вернёт ссылку, отправь её клиенту: он жмёт, привязывается и получает подписку.\n\nОтмена: /cancel", reply_markup=types.ReplyKeyboardRemove())

@dp.message(not_maestro_customer_message, AdminFSM.bind_link, admin_text_input)
async def adm_bindlink_make(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    login = (msg.text or "").strip().split()[0] if (msg.text or "").strip() else ""
    if not login:
        return await msg.answer("❌ Пришлите логин.")
    note = "" if db_get_sub_url(login) else "\n\n⚠️ У логина пока нет подписки в панели — клиент получит старую naive-ссылку. Заведи его в панель для полной подписки."
    tok = db_create_bind_token(login)
    link = "https://t.me/%s?start=bind_%s" % (BOT_USERNAME, tok)
    await state.clear()
    await msg.answer("🔗 <b>Ссылка для %s</b> (7 дней):\n\n<code>%s</code>\n\nОтправь её клиенту.%s" % (login, link, note), reply_markup=kb_admin())

# ─── BROADCAST (FSM) ───────────────────────────────────
@dp.message(not_maestro_customer_message, F.text == "📢 Рассылка")
async def adm_broadcast_start(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    with get_db() as db:
        count = db.execute("SELECT COUNT(*) as c FROM binds").fetchone()["c"]
    await state.set_state(AdminFSM.broadcast)
    await msg.answer(
        f"📢 <b>Рассылка</b> ({count} получателей)\n\n"
        f"Отправьте текст сообщения (HTML разметка поддерживается).\n\n"
        f"Отмена: /cancel",
        reply_markup=types.ReplyKeyboardRemove()
    )

@dp.message(not_maestro_customer_message, AdminFSM.broadcast, admin_text_input)
async def adm_broadcast_send(msg: Message, state: FSMContext):
    if not is_admin(msg.from_user.id): return
    rows = db_all_binds()
    sent = failed = 0
    for row in rows:
        try:
            await bot.send_message(row["tg_id"], msg.text)
            sent += 1
            await asyncio.sleep(0.05)   # flood protection
        except Exception as e:
            logger.warning(f"Broadcast → {row['tg_id']}: {e}")
            failed += 1

    await msg.answer(
        f"📢 <b>Рассылка завершена</b>\n\n"
        f"✅ Отправлено: <b>{sent}</b>\n"
        f"❌ Ошибок: <b>{failed}</b>",
        reply_markup=kb_admin()
    )
    await state.clear()


# ═══════════════════════════════════════════════════════
# MAIN
# ═══════════════════════════════════════════════════════

# ─── MaestroVPN multi-protocol app (added) ───────────────────────────
APP_DOWNLOAD_URL = "https://storage.yandexcloud.net/maestro-apk/latest.apk"
MAESTRO_CLAIM_URL = "https://wapmixx.ru:8911/claim"


def kb_connect_os():
    return InlineKeyboardMarkup(inline_keyboard=[
        [InlineKeyboardButton(text="\U0001F916 Android-телефон / Android TV", callback_data="conn:android")],
        [InlineKeyboardButton(text="\U0001F34F iPhone / iPad", callback_data="conn:ios")],
        [InlineKeyboardButton(text="\U0001F4BB Компьютер (Windows / Mac)", callback_data="conn:desktop")],
    ])


async def _claim_sub_url(proxy_user):
    try:
        async with httpx.AsyncClient(timeout=30) as c:
            r = await c.post(MAESTRO_CLAIM_URL, json={"code": proxy_user})
        if r.status_code != 200:
            return None
        return (r.json() or {}).get("sub_url") or None
    except Exception:
        return None


@dp.message(not_maestro_customer_message, F.text == "\U0001F4F2 Как подключиться")
async def cmd_howto(msg: Message):
    if not db_get_bind(msg.from_user.id):
        await msg.answer("Сначала оформите подписку \u2014 кнопка \u00ab\U0001F6CD \u0422\u0430\u0440\u0438\u0444\u044b\u00bb \U0001F447")
        return
    await msg.answer(
        "\U0001F4F2 <b>\u041a\u0430\u043a \u043f\u043e\u0434\u043a\u043b\u044e\u0447\u0438\u0442\u044c\u0441\u044f</b>\n\n"
        "\u0412\u044b\u0431\u0435\u0440\u0438\u0442\u0435 \u0441\u0432\u043e\u0451 \u0443\u0441\u0442\u0440\u043e\u0439\u0441\u0442\u0432\u043e \U0001F447",
        reply_markup=kb_connect_os(),
    )


@dp.callback_query(not_maestro_customer_callback, F.data == "conn:android")
async def conn_android(cb):
    if await legacy_customer_connect(cb):
        return
    proxy_user = db_get_bind(cb.from_user.id)
    if not proxy_user:
        await cb.answer("\u0421\u043d\u0430\u0447\u0430\u043b\u0430 \u043e\u0444\u043e\u0440\u043c\u0438\u0442\u0435 \u043f\u043e\u0434\u043f\u0438\u0441\u043a\u0443 \U0001F6CD", show_alert=True)
        return
    text = (
        "\U0001F916 <b>Android-\u0442\u0435\u043b\u0435\u0444\u043e\u043d \u0438 Android TV</b>\n\n"
        "\u041d\u0430\u0448\u0435 \u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u0435 <b>MaestroVPN</b> \u2014 \u0432\u0441\u0435 \u0441\u0435\u0440\u0432\u0435\u0440\u044b \u0438 \u043f\u0440\u043e\u0442\u043e\u043a\u043e\u043b\u044b \u0432 \u043e\u0434\u0438\u043d \u0442\u0430\u043f.\n\n"
        "1\uFE0F\u20E3 \u0421\u043a\u0430\u0447\u0430\u0439 \u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u0435 \u043a\u043d\u043e\u043f\u043a\u043e\u0439 \u00ab\U0001F4E5 \u0421\u043a\u0430\u0447\u0430\u0442\u044c\u00bb \u0432\u043d\u0438\u0437\u0443.\n"
        "2\uFE0F\u20E3 \u0423\u0441\u0442\u0430\u043d\u043e\u0432\u0438 \u0438 \u043e\u0442\u043a\u0440\u043e\u0439 \u0435\u0433\u043e.\n"
        "3\uFE0F\u20E3 \u041d\u0430\u0436\u043c\u0438 \u00ab\u0412\u0432\u0435\u0441\u0442\u0438 \u043a\u043e\u0434 \u043f\u043e\u0434\u043f\u0438\u0441\u043a\u0438\u00bb \u0438 \u0432\u043f\u0438\u0448\u0438 \u043b\u043e\u0433\u0438\u043d:\n"
        f"<code>{proxy_user}</code>\n"
        "4\uFE0F\u20E3 \u0413\u043e\u0442\u043e\u0432\u043e \u2705 \u2014 \u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u0435 \u0441\u0430\u043c\u043e \u043f\u043e\u0434\u0442\u044f\u043d\u0435\u0442 \u0432\u0441\u0451.\n\n"
        "\U0001F4FA <i>Android TV: \u043e\u0442\u043a\u0440\u043e\u0439 \u0441\u0441\u044b\u043b\u043a\u0443 \u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u044f \u0432 \u0431\u0440\u0430\u0443\u0437\u0435\u0440\u0435 \u0422\u0412 \u0438\u043b\u0438 \u0441\u043a\u0438\u043d\u044c APK \u043d\u0430 \u0444\u043b\u0435\u0448\u043a\u0443.</i>"
    )
    kbd = InlineKeyboardMarkup(inline_keyboard=[
        [InlineKeyboardButton(text="\U0001F4E5 \u0421\u043a\u0430\u0447\u0430\u0442\u044c \u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u0435", url=APP_DOWNLOAD_URL)],
        [InlineKeyboardButton(text="\U0001F4AC \u041f\u043e\u0434\u0434\u0435\u0440\u0436\u043a\u0430", url="https://t.me/wapmixx")],
    ])
    await cb.message.answer(text, reply_markup=kbd)
    await cb.answer()


async def _karing_flow(cb, head, with_qr):
    if await legacy_customer_connect(cb):
        return
    proxy_user = db_get_bind(cb.from_user.id)
    if not proxy_user:
        await cb.answer("\u0421\u043d\u0430\u0447\u0430\u043b\u0430 \u043e\u0444\u043e\u0440\u043c\u0438\u0442\u0435 \u043f\u043e\u0434\u043f\u0438\u0441\u043a\u0443 \U0001F6CD", show_alert=True)
        return
    await cb.answer("\u0413\u043e\u0442\u043e\u0432\u043b\u044e \u0441\u0441\u044b\u043b\u043a\u0443\u2026")
    sub_url = await _claim_sub_url(proxy_user)
    if not sub_url:
        await cb.message.answer(f"\u041d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u043f\u043e\u043b\u0443\u0447\u0438\u0442\u044c \u043f\u043e\u0434\u043f\u0438\u0441\u043a\u0443. \u041f\u043e\u0434\u0434\u0435\u0440\u0436\u043a\u0430 {SUPPORT_USER}.")
        return
    subscription_url = clean_subscription_url(sub_url)
    text = head + (
        "1\uFE0F\u20E3 \u0423\u0441\u0442\u0430\u043d\u043e\u0432\u0438 <b>Karing</b>.\n"
        "2\uFE0F\u20E3 \u0421\u043a\u043e\u043f\u0438\u0440\u0443\u0439 \u0441\u0441\u044b\u043b\u043a\u0443-\u043f\u043e\u0434\u043f\u0438\u0441\u043a\u0443 (\u043d\u0430\u0436\u043c\u0438 \u2014 \u0441\u043a\u043e\u043f\u0438\u0440\u0443\u0435\u0442\u0441\u044f):\n"
        f"<code>{subscription_url}</code>\n"
        "3\uFE0F\u20E3 Karing \u2192 \u00ab\uFF0B\u00bb \u2192 <b>\u00ab\u0414\u043e\u0431\u0430\u0432\u0438\u0442\u044c \u043f\u043e\u0434\u043f\u0438\u0441\u043a\u0443\u00bb</b> \u2192 \u0432\u0441\u0442\u0430\u0432\u044c \u0441\u0441\u044b\u043b\u043a\u0443 \u2192 \u00ab\u0413\u043e\u0442\u043e\u0432\u043e\u00bb.\n"
        "4\uFE0F\u20E3 \u0412\u044b\u0431\u0435\u0440\u0438 \u0441\u0435\u0440\u0432\u0435\u0440 \u0438 \u043d\u0430\u0436\u043c\u0438 <b>\u00ab\u041f\u043e\u0434\u043a\u043b\u044e\u0447\u0438\u0442\u044c\u00bb</b> \u2705"
    )
    supp = InlineKeyboardMarkup(inline_keyboard=[[InlineKeyboardButton(text="\U0001F4AC \u041f\u043e\u0434\u0434\u0435\u0440\u0436\u043a\u0430", url="https://t.me/wapmixx")]])
    if with_qr:
        await cb.message.answer_photo(types.BufferedInputFile(gen_qr(subscription_url), filename="sub.png"),
                                      caption=text, reply_markup=supp)
    else:
        await cb.message.answer(text, reply_markup=supp)


@dp.callback_query(not_maestro_customer_callback, F.data == "conn:ios")
async def conn_ios(cb):
    await _karing_flow(cb, "\U0001F34F <b>iPhone / iPad</b>\n\n\u041f\u043e\u0434\u043a\u043b\u044e\u0447\u0430\u0435\u043c\u0441\u044f \u0447\u0435\u0440\u0435\u0437 \u0431\u0435\u0441\u043f\u043b\u0430\u0442\u043d\u044b\u0439 <b>Karing</b> (App Store).\n\n", True)


@dp.callback_query(not_maestro_customer_callback, F.data == "conn:desktop")
async def conn_desktop(cb):
    await _karing_flow(cb, "\U0001F4BB <b>Windows / Mac</b>\n\n\u0427\u0435\u0440\u0435\u0437 \u0431\u0435\u0441\u043f\u043b\u0430\u0442\u043d\u044b\u0439 <b>Karing</b> (\u0441\u0430\u0439\u0442 karing.app).\n\n", False)


async def legacy_customer_bind_login(message, login):
    if message.chat.type != "private" or message.chat.id <= 0:
        raise ValueError("Откройте личный чат с ботом.")
    sender = message.from_user
    if not sender or (sender.is_bot and sender.id != bot.id) or (not sender.is_bot and sender.id != message.chat.id):
        raise ValueError("Откройте личный чат с ботом.")
    login = str(login or "").strip()
    response = await panel._req("GET", "/api/naive/users")
    if response is None or response.status_code != 200:
        raise ValueError("Панель временно недоступна. Повторите вход позже.")
    if not any(user.get("username") == login for user in response.json().get("users", [])):
        raise ValueError("Этот логин не найден в этом боте. Обратитесь в поддержку.")
    with get_db() as db:
        db.execute("BEGIN IMMEDIATE")
        owners = db.execute("SELECT tg_id FROM binds WHERE proxy_user=? "
                            "UNION SELECT tg_id FROM subscriptions WHERE proxy_user=?",
                            (login, login)).fetchall()
        if any(owner["tg_id"] != message.chat.id for owner in owners):
            raise ValueError("Логин уже привязан к другому Telegram. Обратитесь в поддержку.")
        db.execute("INSERT INTO binds (tg_id, proxy_user) VALUES (?, ?) "
                   "ON CONFLICT(tg_id) DO UPDATE SET proxy_user=excluded.proxy_user",
                   (message.chat.id, login))
        db.commit()
    return True


async def legacy_customer_clear_state(message):
    if message.chat.type != "private" or message.chat.id <= 0:
        return
    state = dp.fsm.get_context(bot=bot, chat_id=message.chat.id, user_id=message.chat.id)
    await state.clear()


async def legacy_customer_status(message):
    login = db_get_bind(message.chat.id)
    if not login:
        return {"login": "", "expires_at": None, "active": None, "protocols": [], "bound": False}
    protocols = ["VLESS", "Hysteria2", "NaiveProxy", "AnyTLS"] if db_get_sub_url(login) else ["NaiveProxy"]
    result = {"login": login, "expires_at": None, "active": None,
              "protocols": protocols, "bound": True}
    with get_db() as db:
        sub = db.execute("SELECT expires_at FROM subscriptions WHERE tg_id=? AND proxy_user=?",
                         (message.chat.id, login)).fetchone()
    if sub and sub["expires_at"]:
        raw = sub["expires_at"]
    else:
        response = await panel._req("GET", "/api/naive/users")
        if response is None or response.status_code != 200:
            return result
        user = next((u for u in response.json().get("users", []) if u["username"] == login), None)
        if not user:
            return result
        raw = user.get("expiresAt")
        if not raw:
            result["active"] = True
            return result
    try:
        expires = datetime.fromisoformat(raw.replace("Z", "+00:00"))
        result["expires_at"] = int(expires.timestamp())
        result["active"] = expires.timestamp() > datetime.now().timestamp()
    except (TypeError, ValueError):
        pass
    return result


async def legacy_customer_renew(callback):
    await callback.answer()
    await callback.message.answer(
        "🛍 <b>Обычная подписка VPN</b>\n\nВыберите срок. После перевода нажмите «Я оплатил». "
        "Администратор проверит платёж и подтвердит подписку. CDN-пакеты ГБ оплачиваются отдельно.",
        reply_markup=kb_tariffs())


async def legacy_customer_connect(callback):
    login = db_get_bind(callback.from_user.id)
    if not login or db_get_sub_url(login):
        return False
    user = await panel.find(login)
    if not user:
        await callback.answer("Аккаунт не найден или панель недоступна. Повторите позже.", show_alert=True)
        return True
    link = make_key(login, user["password"])
    await callback.answer()
    await callback.message.answer(
        f"🔑 <b>Ваш ключ NaiveProxy</b>\n\n<code>{html.escape(link)}</code>\n\n"
        "Этот аккаунт использует NaiveProxy. Для него подходит Karing.\n"
        "1. Установите Karing по кнопке ниже.\n"
        "2. Скопируйте ключ целиком.\n"
        "3. Karing → «+» → «Импорт из буфера».\n"
        "4. Выберите добавленный профиль и включите соединение.\n\n"
        "Ключ даёт доступ к вашей подписке. Не пересылайте его другим.",
        reply_markup=InlineKeyboardMarkup(inline_keyboard=[
            [InlineKeyboardButton(text="📲 Скачать Karing", url="https://karing.app")],
            [InlineKeyboardButton(text="🏠 Главная", callback_data="mc:home:main")],
        ]))
    return True


async def legacy_admin(callback):
    if not is_admin(callback.from_user.id):
        return await callback.answer("⛔", show_alert=True)
    state = dp.fsm.get_context(bot=bot, chat_id=callback.message.chat.id, user_id=callback.from_user.id)
    await cb_admin_home(callback, state)


async def main():
    configure_customer_ui(status=legacy_customer_status, renew=legacy_customer_renew,
                          connect=legacy_customer_connect, is_admin=is_admin, admin=legacy_admin,
                          bind_login=legacy_customer_bind_login, clear_state=legacy_customer_clear_state)
    await panel.login()

    scheduler.add_job(job_check_expiry, "interval", hours=6, id="expiry_check",
                      next_run_time=datetime.now())
    scheduler.add_job(job_sync_panel_expiry, "interval", hours=6, id="panel_sync",
                      next_run_time=datetime.now())
    scheduler.add_job(job_daily_report, "cron", hour=9, minute=0, id="daily_report")
    scheduler.start()
    logger.info("Bot started — polling")
    await dp.start_polling(bot, skip_updates=False)


if __name__ == "__main__":
    asyncio.run(main())
