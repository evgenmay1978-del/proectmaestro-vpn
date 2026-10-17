from aiogram.utils.keyboard import InlineKeyboardBuilder, InlineKeyboardMarkup
from config import config

# Stable mirror link — the panel's update-mirror auto-overwrites it to the latest release,
# so this button always serves the CURRENT app version.
APP_DOWNLOAD_URL = "https://storage.yandexcloud.net/maestro-apk/latest.apk"
CHANNEL_URL = "https://t.me/maestrovpn"

def client_main_kb(has_active=False, is_admin=False, show_trial=False):
    kb = InlineKeyboardBuilder()
    if has_active:
        kb.button(text="🔄 Продлить подписку", callback_data="client:renew")
    else:
        kb.button(text="💳 Купить подписку", callback_data="client:buy")
    if show_trial:
        kb.button(text="🎁 Попробовать 1 день бесплатно", callback_data="client:trial")
    kb.button(text="📥 Скачать приложение", url=APP_DOWNLOAD_URL)
    kb.button(text="📲 Как подключиться", callback_data="client:howto")
    kb.button(text="📊 Моя подписка", callback_data="client:keys")
    kb.button(text=f"🎁 Пригласить друга (+{config.REFERRAL_BONUS_DAYS} дней)", callback_data="client:referrals")
    kb.button(text="📣 Наш канал", url=CHANNEL_URL)
    kb.button(text="❓ Помощь", callback_data="client:help")
    if is_admin:
        kb.button(text="🔧 Админ-панель", callback_data="admin:main")
    kb.adjust(1)
    return kb.as_markup()


def connect_os_kb():
    """Step 1 of «Как подключиться» — pick the device. The single, clear entry point."""
    kb = InlineKeyboardBuilder()
    kb.button(text="🤖 Android-телефон / Android TV", callback_data="client:connect:android")
    kb.button(text="🍏 iPhone / iPad", callback_data="client:connect:ios")
    kb.button(text="💻 Компьютер (Windows / Mac)", callback_data="client:connect:desktop")
    kb.button(text="◀️ В меню", callback_data="client:main")
    kb.adjust(1)
    return kb.as_markup()


def connect_android_kb(app_url):
    kb = InlineKeyboardBuilder()
    kb.button(text="📥 Скачать приложение MaestroVPN", url=app_url)
    kb.button(text="🛟 Поддержка", url=f"https://t.me/{config.SUPPORT_USERNAME}")
    kb.button(text="◀️ Другое устройство", callback_data="client:howto")
    kb.adjust(1)
    return kb.as_markup()


def connect_other_kb():
    kb = InlineKeyboardBuilder()
    kb.button(text="🛟 Поддержка", url=f"https://t.me/{config.SUPPORT_USERNAME}")
    kb.button(text="◀️ Другое устройство", callback_data="client:howto")
    kb.adjust(1)
    return kb.as_markup()

def help_kb():
    kb = InlineKeyboardBuilder()
    kb.button(text="🛟 Написать в поддержку", url=f"https://t.me/{config.SUPPORT_USERNAME}")
    kb.button(text="📲 Как подключиться", callback_data="client:howto")
    kb.button(text="◀️ В меню", callback_data="client:main")
    kb.adjust(1)
    return kb.as_markup()

def profile_kb(has_active=False, has_key=False):
    kb = InlineKeyboardBuilder()
    if has_key:
        kb.button(text="📷 QR-код для подключения", callback_data="client:qr")
    kb.button(text="🔄 Продлить VPN", callback_data="mc:renew:menu")
    kb.button(text="📲 Подключить устройство", callback_data="mc:devices:menu")
    kb.button(text="◀️ Главное меню", callback_data="mc:home:main")
    kb.adjust(1)
    return kb.as_markup()

def referral_kb(share_url):
    """Referral screen: ONE-TAP share (opens Telegram's «share to…» with a ready
    invite message + the user's referral link) + back to menu."""
    kb = InlineKeyboardBuilder()
    kb.button(text="📤 Поделиться с другом", url=share_url)
    kb.button(text="◀️ В меню", callback_data="client:main")
    kb.adjust(1)
    return kb.as_markup()

def _tariff_emoji(days):
    if days >= 365:
        return "👑"
    if days >= 180:
        return "💎"
    if days >= 90:
        return "🔥"
    if days >= 60:
        return "⭐️"
    return "✨"

def tariffs_kb(action, tariffs):
    kb = InlineKeyboardBuilder()
    base_pm = tariffs.get(30) or (min(tariffs.values()) if tariffs else 0)
    for days in sorted(tariffs):
        months = days // 30
        label = f"{months} мес" if months >= 1 else f"{days} дн"
        badge = ""
        if base_pm and days >= 60:
            pm = tariffs[days] / (days / 30)
            disc = round((1 - pm / base_pm) * 100)
            if disc >= 1:
                badge = f" · 🔥-{disc}%"
        kb.button(text=f"{_tariff_emoji(days)} {label} · {tariffs[days]}₽{badge}",
                  callback_data=f"client:tariff:{action}:{days}")
    kb.button(text="◀️ Назад", callback_data="client:main")
    kb.adjust(1)
    return kb.as_markup()

def close_kb():
    kb = InlineKeyboardBuilder()
    kb.button(text="✖️ Закрыть", callback_data="client:close")
    return kb.as_markup()

def skip_photo_kb():
    kb = InlineKeyboardBuilder()
    kb.button(text="⏭ Пропустить (без скрина)", callback_data="client:skip_photo")
    kb.button(text="❌ Отмена", callback_data="client:cancel")
    kb.adjust(1)
    return kb.as_markup()

def payment_kb(order_id=0):
    kb = InlineKeyboardBuilder()
    kb.button(text="✅ Я оплатил", callback_data=f"client:paid:{order_id}")
    kb.button(text="❌ Отмена", callback_data="client:cancel")
    kb.adjust(1)
    return kb.as_markup()

def admin_main_kb(pending_count=None):
    kb = InlineKeyboardBuilder()
    label = "🧾 Оплаты VPN" + (f" · {pending_count}" if pending_count is not None else "")
    kb.button(text=label, callback_data="admin:orders")
    kb.button(text="🌐 Оплаты CDN / панель", callback_data="mc:admin:orders")
    kb.button(text="🔎 Найти клиента", callback_data="admin:search")
    kb.button(text="👥 Клиенты", callback_data="admin:clients")
    kb.button(text="🔗 Привязка", callback_data="admin:bindings")
    kb.button(text="📊 Статистика", callback_data="admin:stats")
    kb.button(text="📨 Рассылка", callback_data="admin:broadcast")
    kb.button(text="⚙️ Настройки", callback_data="admin:settings")
    kb.button(text="💾 Бэкап сейчас", callback_data="admin:backup")
    kb.button(text="🏠 В меню", callback_data="client:main")
    kb.adjust(1, 1, 2, 2, 2, 1, 1)
    return kb.as_markup()

def admin_order_kb(order_id):
    kb = InlineKeyboardBuilder()
    kb.button(text="✅ Подтвердить", callback_data=f"admin:approve:{order_id}")
    kb.button(text="❌ Отклонить", callback_data=f"admin:reject:{order_id}")
    kb.adjust(2)
    return kb.as_markup()

def admin_back_kb(action="admin:main"):
    kb = InlineKeyboardBuilder()
    kb.button(text="◀️ Назад", callback_data=action)
    return kb.as_markup()

def admin_bind_inbounds_kb(inbounds):
    kb = InlineKeyboardBuilder()
    for ib in inbounds:
        kb.button(text=f"📡 {ib.remark} (порт {ib.port})",
                  callback_data=f"admin:bind_ib:{ib.id}")
    kb.button(text="◀️ Назад", callback_data="admin:main")
    kb.adjust(1)
    return kb.as_markup()

def admin_bind_clients_kb(inbound_id, clients):
    kb = InlineKeyboardBuilder()
    for c in clients:
        kb.button(text=f"👤 {c.email}", callback_data=f"admin:bind_client:{inbound_id}:{c.email}")
    kb.button(text="◀️ Назад", callback_data="admin:bindings")
    kb.adjust(1)
    return kb.as_markup()

def _days_left_short(expiry_time):
    if not expiry_time:
        return "♾"
    import time
    import math
    dl = math.ceil((expiry_time - time.time() * 1000) / 86400_000)
    return f"{dl}д" if dl > 0 else "истёк"

def admin_clients_list_kb(clients, page=0, searching=False):
    kb = InlineKeyboardBuilder()
    size = 12
    for c in clients[page * size:(page + 1) * size]:
        import time
        mark = "🟢" if c.enable and (not c.expiry_time or c.expiry_time > time.time() * 1000) else "🔴" if c.enable else "⛔️"
        kb.button(text=f"{mark} {c.email} · {_days_left_short(c.expiry_time)}",
                  callback_data=f"aclshow:{c.email}")
    if page:
        kb.button(text="← Предыдущие", callback_data=f"admin:clients_page:{page - 1}")
    if (page + 1) * size < len(clients):
        kb.button(text="Следующие →", callback_data=f"admin:clients_page:{page + 1}")
    kb.button(text="🔎 Найти клиента", callback_data="admin:search")
    if searching:
        kb.button(text="👥 Все клиенты", callback_data="admin:clients")
    kb.button(text="◀️ В админ-панель", callback_data="admin:main")
    kb.adjust(1)
    return kb.as_markup()

def admin_client_detail_kb(email, enabled, has_tg):
    kb = InlineKeyboardBuilder()
    kb.button(text="➕ Добавить дни", callback_data=f"aclext:{email}")
    kb.button(text="📆 Установить дату", callback_data=f"acldate:{email}")
    from handlers.maestro_customer_entry import customer_admin_button
    kb.add(customer_admin_button(email))
    if enabled:
        kb.button(text="⛔️ Заблокировать", callback_data=f"acltog:{email}")
    else:
        kb.button(text="✅ Разблокировать", callback_data=f"acltog:{email}")
    if has_tg:
        kb.button(text="✍️ Написать клиенту", callback_data=f"aclmsg:{email}")
    kb.button(text="🔗 Подписка (приложение)", callback_data=f"aclsub:{email}")
    kb.button(text="◀️ К списку", callback_data="admin:clients")
    kb.adjust(2, 1, 1, 1)
    return kb.as_markup()

def admin_client_extend_kb(email):
    kb = InlineKeyboardBuilder()
    for d in (30, 90, 180, 365):
        kb.button(text=f"+{d} дн", callback_data=f"acldo:{email}:{d}")
    kb.button(text="✏️ Другое количество дней", callback_data=f"acldays:{email}")
    kb.button(text="◀️ Назад", callback_data=f"aclshow:{email}")
    kb.adjust(2, 2, 1, 1)
    return kb.as_markup()


def admin_orders_list_kb(orders, page=0):
    kb = InlineKeyboardBuilder()
    for order in orders[page * 10:(page + 1) * 10]:
        kb.button(text=f"№{order['id']} · {order['days']} дн. · {order['amount']} ₽",
                  callback_data=f"admin:order:{order['id']}")
    if page:
        kb.button(text="← Предыдущие", callback_data=f"admin:orders_page:{page - 1}")
    if (page + 1) * 10 < len(orders):
        kb.button(text="Следующие →", callback_data=f"admin:orders_page:{page + 1}")
    kb.button(text="◀️ Администратор", callback_data="admin:main")
    kb.adjust(1)
    return kb.as_markup()

def admin_tariffs_kb(tariffs):
    kb = InlineKeyboardBuilder()
    for days in sorted(tariffs):
        kb.button(text=f"⚙️ {days} дн — {tariffs[days]}₽", callback_data=f"admin:edit_tariff:{days}")
    kb.button(text="➕ Добавить тариф", callback_data="admin:add_tariff")
    kb.button(text="◀️ В админ-панель", callback_data="admin:main")
    kb.adjust(1)
    return kb.as_markup()
