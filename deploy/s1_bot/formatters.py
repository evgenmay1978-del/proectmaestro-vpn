from html import escape
from datetime import datetime
import math
from config import config

# ──────────────────────── helpers ────────────────────────

def format_bytes(n):
    """Bytes -> human string (КБ/МБ/ГБ/ТБ)."""
    try:
        n = float(n or 0)
    except (TypeError, ValueError):
        n = 0.0
    for unit in ("Б", "КБ", "МБ", "ГБ"):
        if n < 1024:
            return f"{n:.0f} {unit}" if unit in ("Б", "КБ") else f"{n:.2f} {unit}"
        n /= 1024
    return f"{n:.2f} ТБ"

def progress_bar(fraction, width=10):
    """Emoji progress bar. fraction in [0..1]."""
    try:
        fraction = max(0.0, min(1.0, float(fraction)))
    except (TypeError, ValueError):
        fraction = 0.0
    filled = round(fraction * width)
    if fraction >= 0.9:
        block = "🟥"
    elif fraction >= 0.7:
        block = "🟧"
    else:
        block = "🟩"
    return block * filled + "⬜️" * (width - filled)

def days_word(n):
    n = abs(int(n))
    if 11 <= n % 100 <= 14:
        return "дней"
    d = n % 10
    if d == 1:
        return "день"
    if 2 <= d <= 4:
        return "дня"
    return "дней"

def tariff_label(days):
    labels = {30: "1 месяц", 60: "2 месяца", 90: "3 месяца", 180: "6 месяцев", 365: "12 месяцев"}
    if days in labels:
        return labels[days]
    months = days // 30
    return f"{months} мес" if days % 30 == 0 else f"{days} {days_word(days)}"

def _days_left(expiry_time):
    if not expiry_time:
        return None  # бессрочно
    return max(0, math.ceil((expiry_time - datetime.now().timestamp() * 1000) / 86400_000))

def _expiry_date(expiry_time):
    if not expiry_time:
        return "♾ бессрочно"
    from datetime import timezone, timedelta
    return datetime.fromtimestamp(expiry_time / 1000, timezone(timedelta(hours=3))).strftime("%d.%m.%Y %H:%M МСК")

# ──────────────────────── client screens ────────────────────────

def client_welcome(name, has_active=False, days_left=0):
    if has_active:
        emoji = "🟢" if days_left is None or days_left > 3 else "🟡"
        if days_left is None:
            status = f"{emoji} <b>Подписка активна</b> · ♾ бессрочно"
        else:
            status = f"{emoji} <b>Подписка активна</b> · осталось <b>{days_left}</b> {days_word(days_left)}"
    else:
        status = "🔴 <b>Подписка неактивна</b>"
    return (
        f"✨ <b>Привет, {escape(name)}!</b>\n"
        f"<i>MaestroVPN</i> — быстрый и стабильный доступ 🚀\n\n"
        f"{status}\n\n"
        f"Всего <b>2 шага</b>:\n"
        f"1️⃣ <b>💳 Купить подписку</b> — выбери срок, оплати по СБП.\n"
        f"2️⃣ <b>📲 Как подключиться</b> — выбери своё устройство (Android / iPhone / ПК) "
        f"и подключись по простой инструкции.\n\n"
        f"<i>На Android — наше приложение MaestroVPN (все серверы и протоколы в один тап). "
        f"На iPhone — бесплатный Karing.</i>\n\n"
        f"🎁 <b>Бонус:</b> приглашай друзей — <b>+{config.REFERRAL_BONUS_DAYS} дней</b> за каждого.\n"
        f"📣 Новости, советы и акции — наш канал @maestrovpn\n\n"
        f"━━━━━━━━━━━━━━━━━━\n"
        f"Выбери действие в меню ниже ⬇️"
    )

def buy_intro():
    return (
        "💳 <b>Обычная подписка VPN</b>\n\n"
        "Выберите срок ниже. Затем бот покажет сумму и наши реквизиты СБП. "
        "После перевода нажмите «Я оплатил» и приложите чек — администратор проверит оплату.\n\n"
        "Обычная подписка нужна для ежедневного VPN. Пакеты CDN в неё не входят: "
        "это отдельные гигабайты для мобильных белых списков."
    )

def renew_intro():
    return (
        f"🔄 <b>Продление подписки</b>\n\n"
        "Оплаченный срок добавится после подтверждения перевода. Если подписка ещё действует, "
        "оставшиеся дни сохранятся; если закончилась — новый срок начнётся с момента продления.\n\n"
        "Это продление обычного VPN. Гигабайты CDN покупаются отдельно.\n\n"
        "Выберите период:"
    )

def tariff_card(days, price):
    per_month = round(price / (days / 30)) if days >= 30 else price
    economy = ""
    if days >= 90:
        economy = f"\n💸 <i>Выгода: всего {per_month}₽/мес</i>"
    return (
        f"╭───────────────╮\n"
        f"  💎 <b>{tariff_label(days)}</b>\n"
        f"╰───────────────╯\n\n"
        f"📅 Срок: <b>{days} {days_word(days)}</b>\n"
        f"Обычная подписка VPN · без пакета CDN{economy}\n\n"
        f"━━━━━━━━━━━━━━━━━━\n"
        f"💰 К оплате: <b>{price}₽</b>\n\n"
        f"📲 <b>Оплата по СБП</b>:\n"
        f"📱 Номер: <code>{escape(config.PAYMENT_PHONE)}</code>\n"
        f"🏦 Банк: {escape(config.PAYMENT_BANK)}\n"
        f"👤 Получатель: {escape(config.PAYMENT_NAME)}\n"
        f"<i>(нажми на номер — скопируется)</i>\n\n"
        f"Переведите ровно <b>{price}₽</b>, затем нажмите «✅ Я оплатил» и отправьте чек. "
        "Нажатие кнопки отправляет заявку на проверку и само по себе не списывает деньги."
    )

def payment_sent(order_id, days, amount):
    return (
        f"⏳ <b>Заявка №{order_id} создана</b>\n\n"
        f"📦 {tariff_label(days)} · <b>{amount}₽</b>\n"
        f"🕐 {datetime.now():%d.%m %H:%M}\n\n"
        f"Заявка отправлена администратору.\n"
        "После проверки перевода бот сообщит о результате здесь. Повторно оплачивать эту заявку не нужно."
    )

def order_history(orders):
    if not orders:
        return "📋 <b>История заказов</b>\n\n<i>Пока пусто. Оформи первую подписку! 🚀</i>"
    status_meta = {
        "approved": ("✅", "оплачен"),
        "rejected": ("❌", "отклонён"),
        "pending": ("⏳", "проверяется"),
        "cancelled": ("🚫", "отменён"),
    }
    lines = ["📋 <b>История заказов</b>\n"]
    for o in orders:
        emoji, label = status_meta.get(o["status"], ("❓", o["status"]))
        lines.append(
            f"{emoji} <b>№{o['id']}</b> · {tariff_label(o['days'])} · {o['amount']}₽ · <i>{label}</i>"
        )
    return "\n".join(lines)

def profile_card(client, config_link):
    """Красочная карточка подписки с трафиком и днями."""
    dl = _days_left(client.expiry_time)
    active = client.enable and (dl is None or dl > 0)

    if active:
        head = "🟢 <b>АКТИВНА</b>"
    elif client.enable:
        head = "🔴 <b>ИСТЕКЛА</b>"
    else:
        head = "⛔️ <b>ОТКЛЮЧЕНА</b>"

    # срок
    if dl is None:
        time_block = "⏳ Срок: <b>♾ бессрочно</b>"
    else:
        time_block = (
            f"⏳ Осталось: <b>{dl}</b> {days_word(dl)}\n"
            f"📆 До: <b>{_expiry_date(client.expiry_time)}</b>"
        )

    # трафик
    used = client.used_gb or 0
    total = client.total_gb or 0
    if total and total > 0:
        frac = used / total if total else 0
        traffic_block = (
            f"📊 Трафик: <b>{format_bytes(used)}</b> / {format_bytes(total)}\n"
            f"{progress_bar(frac)}"
        )
    else:
        traffic_block = (
            f"📊 Трафик: <b>♾ безлимит</b>\n"
            f"   израсходовано: {format_bytes(used)}"
        )

    link_block = ""
    if config_link and config_link != "#":
        link_block = (
            f"\n━━━━━━━━━━━━━━━━━━\n"
            f"🔗 <b>Ссылка-подписка</b> <i>(5 протоколов · для Karing/приложения)</i>\n"
            f"<i>нажми, чтобы скопировать:</i>\n"
            f"<code>{escape(config_link)}</code>\n"
            f"♻️ обновляется автоматически"
        )

    return (
        f"┏━━━ 👤 <b>МОЙ VPN</b> ━━━┓\n\n"
        f"{head}\n"
        f"📧 Логин: <code>{escape(client.email)}</code>\n\n"
        f"{time_block}\n\n"
        f"{traffic_block}"
        f"{link_block}"
    )

def trial_activated(days, sub_url):
    return (
        f"🎁 <b>Пробный период активирован!</b>\n\n"
        f"✅ Тебе доступен VPN на <b>{days} {days_word(days)}</b> — бесплатно 🚀\n"
        f"♾ Безлимит · ⚡️ полная скорость\n\n"
        f"🔗 <b>Ссылка-подписка</b> <i>(для Karing)</i>:\n"
        f"<code>{escape(sub_url)}</code>\n\n"
        f"📲 Нажми «📷 QR-код» или «Как подключиться».\n"
        f"💡 Понравится — оформи полный тариф в один клик!"
    )

def help_text():
    return (
        f"❓ <b>Помощь · MaestroVPN</b>\n\n"
        f"<b>Что это?</b>\n"
        f"Быстрый VPN с обходом блокировок. Работает на iOS · Android · ПК · ТВ — "
        f"<b>до 5 устройств</b> на один аккаунт.\n\n"
        f"<b>Как начать?</b>\n"
        f"1️⃣ «💳 Купить подписку» → выбери срок → оплати по СБП → нажми «✅ Я оплатил».\n"
        f"2️⃣ Мы подтвердим оплату за <b>5–15 минут</b> — доступ включится автоматически.\n"
        f"3️⃣ «📲 Как подключиться» → выбери устройство (Android / iPhone / ПК) и подключись по шагам.\n\n"
        f"<b>Частые вопросы</b>\n"
        f"• <i>Не подключается?</i> Переключи протокол или сервер в приложении.\n"
        f"• <i>Сколько устройств?</i> До 5 на аккаунт.\n"
        f"• <i>Как продлить?</i> «🔄 Продлить подписку» — дни добавятся к текущим.\n\n"
        f"🛟 <b>Поддержка:</b> @{config.SUPPORT_USERNAME}\n"
        f"📞 <b>По оплате:</b> <code>{config.PAYMENT_PHONE}</code>"
    )

def referral_info(tg_id, count, earnings, ref_link):
    return (
        f"┏━━━ 👥 <b>ПРИГЛАШАЙ ДРУЗЕЙ</b> ━━━┓\n\n"
        f"Делись VPN — получай <b>бесплатные дни</b>! 🎁\n\n"
        f"🎯 <b>Как это работает:</b>\n"
        f"• Друг переходит по твоей ссылке и оплачивает 1-й тариф\n"
        f"• Ты получаешь <b>+{config.REFERRAL_BONUS_DAYS} дней</b> 🚀\n"
        f"• Друг получает <b>+{config.REFERRAL_FRIEND_BONUS_DAYS} дня</b> в подарок 🎉\n\n"
        f"📊 Приглашено друзей: <b>{count}</b>\n"
        f"💰 Их покупок на сумму: <b>{earnings}₽</b>\n\n"
        f"━━━━━━━━━━━━━━━━━━\n"
        f"🔗 <b>Твоя ссылка</b> <i>(нажми — скопируется)</i>:\n"
        f"<code>{escape(ref_link)}</code>\n\n"
        f"📣 Отправь её друзьям в личку или истории!"
    )

def referral_nudge(ref_link):
    return (
        f"💡 <b>Зарабатывай дни VPN бесплатно!</b>\n\n"
        f"Пригласи друга — и вы оба в плюсе:\n"
        f"🎁 тебе <b>+{config.REFERRAL_BONUS_DAYS} дней</b>, другу <b>+{config.REFERRAL_FRIEND_BONUS_DAYS} дня</b> "
        f"после его первой оплаты.\n\n"
        f"🔗 Твоя ссылка:\n<code>{escape(ref_link)}</code>"
    )

def winback_message():
    """Sent ~3 days after a subscription lapsed — a warm comeback nudge (once)."""
    return (
        f"👋 <b>Скучаем по тебе!</b>\n\n"
        f"Твоя подписка <i>MaestroVPN</i> закончилась пару дней назад. "
        f"Вернись — продли за пару минут и снова быстрый стабильный VPN на всех устройствах 🚀\n\n"
        f"🎁 И помни: за каждого приглашённого друга — <b>+{config.REFERRAL_BONUS_DAYS} дней</b>.\n"
        f"📣 Новости и советы — @maestrovpn\n\n"
        f"Продлить 👉 @{config.BOT_USERNAME}"
    )

def expiry_warning(email, days_left):
    if days_left <= 0:
        return (
            f"🔴 <b>Подписка истекла</b>\n\n"
            f"📧 {escape(email)}\n\n"
            f"Продли в пару кликов, чтобы не терять доступ 👉 @{config.BOT_USERNAME}"
        )
    return (
        f"🟡 <b>Подписка скоро истекает!</b>\n\n"
        f"📧 {escape(email)}\n"
        f"⏳ Осталось всего <b>{days_left}</b> {days_word(days_left)}\n\n"
        f"Продли сейчас и сохрани доступ 👉 "
        f"<a href='https://t.me/{config.BOT_USERNAME}'>открыть бота</a>"
    )

# ──────────────────────── admin screens ────────────────────────

def admin_order_notification(order_id, tg_id, username, days, amount, action_type, has_photo=False):
    a = "🆕 Покупка" if action_type == "buy" else "🔄 Продление"
    u = f"@{username}" if username else f"<code>{tg_id}</code>"
    p = "\n📸 Скриншот прикреплён" if has_photo else "\n⚠️ Без скриншота"
    return (
        f"🔔 <b>Заявка №{order_id}</b>\n\n"
        f"{a}\n"
        f"👤 Клиент: {u}\n"
        f"📅 Срок: <b>{days} {days_word(days)}</b>\n"
        f"💰 Сумма: <b>{amount}₽</b>{p}\n"
        f"🕐 {datetime.now():%d.%m %H:%M}"
    )

def admin_order_approved(order_id, days):
    return f"✅ <b>Заявка №{order_id}</b> подтверждена\n➕ {days} {days_word(days)} начислено"

def admin_order_rejected(order_id):
    return f"❌ <b>Заявка №{order_id}</b> отклонена"

def client_approved(order_id, days, config_link):
    return (
        f"🎉 <b>Оплата подтверждена!</b>\n\n"
        f"✅ Заявка №{order_id} · <b>+{tariff_label(days)}</b>\n"
        f"Твой VPN активен 🚀\n\n"
        f"🔗 <b>Ссылка-подписка</b> <i>(5 протоколов · нажми, чтобы скопировать)</i>:\n"
        f"<code>{escape(config_link)}</code>\n\n"
        f"📲 Добавь её как <b>подписку</b> в приложении <b>Karing</b> "
        f"(см. кнопку «Как подключиться»)."
    )

def client_rejected(order_id):
    return (
        f"❌ <b>Заявка №{order_id} отклонена</b>\n\n"
        f"Похоже, оплата не найдена. Если ты точно платил — "
        f"напиши в поддержку: @{config.SUPPORT_USERNAME}"
    )

def broadcast_done(count):
    return f"✅ Рассылка завершена\n📨 Доставлено: <b>{count}</b>"

def admin_client_card(client, tg_id=None, username=None):
    dl = _days_left(client.expiry_time)
    if not client.enable:
        st = "⛔️ <b>Заблокирован</b>"
    elif dl is None:
        st = "🟢 <b>Активен</b> · ♾ бессрочно"
    elif dl > 0:
        st = f"🟢 <b>Активен</b> · осталось {dl} {days_word(dl)}"
    else:
        st = "🔴 <b>Истёк</b>"
    used = format_bytes(client.used_gb or 0)
    total = format_bytes(client.total_gb) if client.total_gb else "♾ безлимит"
    tg_line = ""
    if tg_id:
        u = f"@{username}" if username else f"<code>{tg_id}</code>"
        tg_line = f"\n💬 Telegram: {u}"
    else:
        tg_line = "\n💬 Telegram: <i>не привязан к боту</i>"
    return (
        f"👤 <b>Клиент {escape(client.email)}</b>\n\n"
        f"{st}\n"
        f"📆 До: <b>{_expiry_date(client.expiry_time)}</b>\n"
        f"📊 Трафик: {used} / {total}{tg_line}"
    )

def admin_settings_text(tariffs):
    lines = ["⚙️ <b>Настройки · Тарифы</b>\n"]
    for d in sorted(tariffs):
        pm = round(tariffs[d] / (d / 30)) if d >= 30 else tariffs[d]
        lines.append(f"💎 {tariff_label(d)} ({d} {days_word(d)}) — <b>{tariffs[d]}₽</b> <i>({pm}₽/мес)</i>")
    lines.append("\n🖊 Нажми на тариф, чтобы изменить цену")
    return "\n".join(lines)


def connect_choose():
    return (
        "📲 <b>Как подключиться</b>\n\n"
        "Выбери своё устройство — покажу простую инструкцию по шагам 👇"
    )


def connect_android(login, app_url):
    return (
        "🤖 <b>Android-телефон и Android TV</b>\n\n"
        "Наше приложение <b>MaestroVPN</b> — все серверы и протоколы в один тап.\n\n"
        "1️⃣ Скачай приложение кнопкой <b>«📥 Скачать приложение»</b> внизу "
        "(или по ссылке — нажми, скопируется, открой в браузере):\n"
        f"<code>{app_url}</code>\n"
        "2️⃣ Установи и открой его.\n"
        "3️⃣ Нажми <b>«Ввести код подписки»</b> и впиши свой логин:\n"
        f"<code>{login}</code>  <i>(нажми — скопируется)</i>\n"
        "4️⃣ Готово ✅ — приложение само подтянет все серверы и протоколы.\n\n"
        "📺 <i>На Android TV: открой ссылку выше в браузере телевизора (или скинь APK на флешку) "
        "и установи так же.</i>"
    )


def connect_ios(karing_url):
    return (
        "🍏 <b>iPhone / iPad</b>\n\n"
        "Нашего приложения в App Store пока нет — подключаемся через бесплатный <b>Karing</b>.\n\n"
        "1️⃣ В <b>App Store</b> установи приложение <b>Karing</b>.\n"
        "2️⃣ Скопируй свою ссылку-подписку (нажми — скопируется):\n"
        f"<code>{karing_url}</code>\n"
        "3️⃣ Открой Karing → «＋» → <b>«Добавить подписку»</b> → вставь ссылку → «Готово».\n"
        "    <i>Или отсканируй QR-код ниже камерой в Karing.</i>\n"
        "4️⃣ Выбери сервер и нажми <b>«Подключить»</b> ✅\n\n"
        "♻️ Подписка обновляется сама — серверы всегда актуальны."
    )


def connect_desktop(karing_url):
    return (
        "💻 <b>Windows / Mac</b>\n\n"
        "Подключаемся через бесплатный <b>Karing</b> (сайт <b>karing.app</b>).\n\n"
        "1️⃣ Скачай и установи <b>Karing</b> с сайта karing.app.\n"
        "2️⃣ Скопируй свою ссылку-подписку (нажми — скопируется):\n"
        f"<code>{karing_url}</code>\n"
        "3️⃣ В Karing → «＋» → <b>«Добавить подписку»</b> → вставь ссылку → «Готово».\n"
        "4️⃣ Выбери сервер и нажми <b>«Подключить»</b> ✅"
    )
