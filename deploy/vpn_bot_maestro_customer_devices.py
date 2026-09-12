"""Device and application guidance for the shared MaestroVPN customer flow.

The presenter contains only public installation links. Personal subscription
delivery always comes from the authenticated ``CustomerFlow`` supplied by the
hosting bot and is never placed in callback data.
"""

from html import escape
from urllib.parse import urlparse


_MAESTRO_APK = "https://storage.yandexcloud.net/maestro-apk/latest.apk"
_KARING_RELEASES = "https://github.com/KaringX/karing/releases/latest"
_KARING_IOS = "https://apps.apple.com/ru/app/karing/id6472431552"
_KARING_SITE = "https://karing.app"
_HAPP_ANDROID = "https://play.google.com/store/apps/details?id=com.happproxy"
_HAPP_IOS = "https://apps.apple.com/us/app/happ-proxy-utility/id6504287215"
_INCY_ANDROID = "https://play.google.com/store/apps/details?id=llc.itdev.incy"
_INCY_IOS = "https://apps.apple.com/ru/app/incy/id6756943388"

_DEVICE_CLIENTS = {
    "android": (("maestro", "MaestroVPN"), ("karing", "Karing"), ("happ", "HAPP"), ("incy", "INCY"), ("v2raytun", "v2RayTun"), ("mihomo", "Clash Mi · Mihomo")),
    "ios": (("karing", "Karing"), ("happ", "HAPP"), ("incy", "INCY"), ("mihomo", "Clash Mi · Mihomo")),
    "desktop": (("karing", "Karing"), ("mihomo", "Clash Mi · Mihomo")),
    "tv": (("maestro", "MaestroVPN"),),
}

_DEVICE_LABELS = {
    "android": "🤖 Android-телефон",
    "ios": "🍏 iPhone / iPad",
    "desktop": "💻 Компьютер",
    "tv": "📺 Android TV",
}

_DEVICE_STEPS = {
    "android": "Установите приложение, скопируйте личную ссылку, добавьте её как подписку и подключитесь.",
    "ios": "Установите приложение, скопируйте личную ссылку, добавьте её как подписку и подключитесь.",
    "desktop": "Выберите приложение, установите его и добавьте личную ссылку как подписку.",
    "tv": "Установите MaestroVPN, введите ваш Maestro login и подключитесь.",
}

_CLIENT_LABELS = {
    "maestro": "MaestroVPN",
    "karing": "Karing",
    "happ": "HAPP",
    "incy": "INCY",
    "mihomo": "Clash Mi · Mihomo",
    "v2raytun": "v2RayTun",
}

_CLIENT_INSTALLS = {
    "mihomo": (
        ("🤖 Clash Mi для Android", "https://github.com/KaringX/clashmi/releases/latest"),
        ("🍏 Clash Mi для iPhone / iPad", "https://apps.apple.com/ru/app/clash-mi/id6744321968"),
        ("💻 Clash Mi для компьютера", "https://clashmi.app/download"),
        ("📖 Инструкция Clash Mi", "https://clashmi.app"),
    ),
    "v2raytun": (
        ("🤖 Скачать v2RayTun", "https://play.google.com/store/apps/details?id=com.v2raytun.android"),
        ("🌐 Сайт v2RayTun", "https://v2raytun.com/"),
    ),
    "maestro": (("📥 Скачать MaestroVPN", _MAESTRO_APK),),
    "karing": (
        ("🍏 Karing для iPhone / iPad", _KARING_IOS),
        ("🤖 Karing для Android", _KARING_RELEASES),
        ("💻 Karing для компьютера", _KARING_RELEASES),
        ("🌐 Сайт Karing", _KARING_SITE),
    ),
    "happ": (
        ("🤖 HAPP для Android", _HAPP_ANDROID),
        ("🍏 HAPP для iPhone / iPad", _HAPP_IOS),
    ),
    "incy": (
        ("🤖 INCY для Android", _INCY_ANDROID),
        ("🍏 INCY для iPhone / iPad", _INCY_IOS),
    ),
}


def _mode_value(mode: str) -> str:
    value = str(mode or "vpn").strip().lower()
    if value not in {"vpn", "cdn"}:
        raise ValueError("unsupported connection mode")
    return value


def _mode_name(mode: str) -> str:
    return "CDN" if _mode_value(mode) == "cdn" else "Обычный VPN"


def _callback_target_mode(raw: str) -> tuple[str, str]:
    for mode in ("vpn", "cdn"):
        suffix = "-" + mode
        if raw.endswith(suffix):
            return raw[:-len(suffix)], mode
    return raw, "vpn"


def _clients_for_device(device: str, mode: str):
    clients = _DEVICE_CLIENTS.get(device)
    if clients is None:
        return None
    if _mode_value(mode) == "cdn":
        return tuple(item for item in clients if item[0] != "maestro")
    return clients


def _https_url(value) -> str:
    raw = str(value or "").strip()
    parsed = urlparse(raw)
    if (parsed.scheme != "https" or not parsed.hostname or parsed.username is not None
            or parsed.password is not None or parsed.fragment):
        return ""
    return raw


async def send_device_menu(message, mode="vpn"):
    """Show the device selector without requiring a customer login."""
    from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup

    mode = _mode_value(mode)
    mode_name = _mode_name(mode)
    rows = [[InlineKeyboardButton(text=label, callback_data=f"mc:device:{device}-{mode}")]
            for device, label in _DEVICE_LABELS.items()]
    rows.append([
        InlineKeyboardButton(text="◀️ Назад", callback_data="mc:home:main"),
        InlineKeyboardButton(text="🏠 Главная", callback_data="mc:home:main"),
    ])
    await message.answer(
        f"<b>{mode_name} · Подключение</b>\n\nВыберите устройство. Установка доступна и без входа.",
        reply_markup=InlineKeyboardMarkup(inline_keyboard=rows),
        parse_mode="HTML",
        disable_web_page_preview=True,
    )


async def _send_device_clients(message, device: str, mode="vpn"):
    from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup

    mode = _mode_value(mode)
    clients = _clients_for_device(device, mode)
    if clients is None:
        raise ValueError("unsupported device")
    rows = [[InlineKeyboardButton(text=label, callback_data=f"mc:install:{client}-{mode}")]
            for client, label in clients]
    rows.append([
        InlineKeyboardButton(text="◀️ Назад", callback_data=f"mc:devices:{mode}"),
        InlineKeyboardButton(text="🏠 Главная", callback_data="mc:home:main"),
    ])
    if mode == "cdn" and not clients:
        details = (
            "Для CDN на этом устройстве совместимое приложение пока не указано. "
            "MaestroVPN продолжает подключать обычный VPN по вашему login."
        )
    else:
        details = _DEVICE_STEPS[device]
    await message.answer(
        f"<b>{escape(_DEVICE_LABELS[device])} · {_mode_name(mode)}</b>\n\n"
        f"{escape(details)}\n\n" + ("Выберите приложение:" if clients else ""),
        reply_markup=InlineKeyboardMarkup(inline_keyboard=rows),
        parse_mode="HTML",
        disable_web_page_preview=True,
    )


def _instruction_keyboard(client: str, copy_url: str, mode: str):
    from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup

    rows = [[InlineKeyboardButton(text=label, url=url)] for label, url in _CLIENT_INSTALLS[client]]
    copy_button_added = False
    if copy_url and len(copy_url) <= 256:
        try:
            from aiogram.types import CopyTextButton

            rows.append([InlineKeyboardButton(
                text="📋 Скопировать личную ссылку",
                copy_text=CopyTextButton(text=copy_url),
            )])
            copy_button_added = True
        except (ImportError, TypeError, ValueError):
            pass
    rows.append([
        InlineKeyboardButton(text="◀️ Назад", callback_data=f"mc:devices:{mode}"),
        InlineKeyboardButton(text="🏠 Главная", callback_data="mc:home:main"),
    ])
    return InlineKeyboardMarkup(inline_keyboard=rows), copy_button_added


async def send_client_instructions(message, flow, client, mode="vpn"):
    """Show public installation links and, when authenticated, personal setup."""
    client = str(client or "").strip().lower()
    if client not in _CLIENT_LABELS:
        raise ValueError("unsupported client")
    mode = _mode_value(mode)
    mode_name = _mode_name(mode)
    label = _CLIENT_LABELS[client]

    if client == "maestro":
        keyboard, _ = _instruction_keyboard(client, "", mode)
        if mode == "cdn":
            details = (
                "MaestroVPN сейчас получает по login профиль обычного VPN. "
                "CDN-ссылка для него в этом меню не выдаётся. Для CDN выберите Karing, HAPP или INCY."
            )
        elif flow is None:
            details = (
                "1. Установите приложение.\n"
                "2. Войдите в бота по вашему Maestro login.\n"
                "3. Откройте эту инструкцию снова, введите login в приложении и подключитесь."
            )
        else:
            login = escape(str(getattr(flow, "login", "") or "").strip())
            details = (
                "1. Установите приложение.\n"
                "2. Откройте MaestroVPN.\n"
                f"3. Введите Maestro login: <code>{login}</code>\n"
                "4. Выберите подключение и нажмите «Подключить»."
            )
        await message.answer(
            f"<b>{label} · {mode_name}</b>\n\n{details}",
            reply_markup=keyboard,
            parse_mode="HTML",
            disable_web_page_preview=True,
        )
        return

    copy_url = ""
    delivery_error = False
    if flow is not None:
        try:
            delivery = await flow.delivery(client, mode=mode)
            copy_url = _https_url(delivery.get("copy_url") or delivery.get("url"))
            delivery_error = not copy_url
        except Exception:
            delivery_error = True

    keyboard, copy_button_added = _instruction_keyboard(client, copy_url, mode)
    if flow is None:
        details = (
            "1. Установите приложение.\n"
            "2. Войдите в бота по вашему Maestro login.\n"
            "3. Откройте эту инструкцию снова, скопируйте личную ссылку, добавьте подписку и подключитесь."
        )
    elif delivery_error:
        details = (
            "Приложение можно установить сейчас. Личная ссылка временно недоступна — "
            "откройте эту инструкцию ещё раз позже."
        )
    else:
        mode_details = (
            "В приложении выберите обычный VPN-узел."
            if mode == "vpn"
            else "В приложении выберите CDN-узел. После покупки новых ГБ обновите CDN-профиль."
        )
        details = (
            "1. Установите приложение.\n"
            "2. Скопируйте личную HTTPS-ссылку ниже.\n"
            f"3. Откройте {label} и добавьте ссылку как подписку.\n"
            "4. Выберите профиль MaestroVPN и подключитесь.\n\n"
            f"{mode_details}\n"
            "Это подписка вашего аккаунта. В списке серверов выбирайте обычный VPN или CDN. После покупки ГБ обновите подписку."
        )
        if not copy_button_added:
            details += f"\n\n<code>{escape(copy_url)}</code>"

    if client == "mihomo":
        details += "\n\nВ Clash Mi откройте «Профили», нажмите «+» и добавьте профиль по URL. Затем выберите MaestroVPN. По умолчанию выбран обычный VPN; CDN выбирается вручную и расходует купленные ГБ. Для CDN используйте актуальное ядро Mihomo с поддержкой XHTTP."
    elif client == "v2raytun":
        details += "\n\nВ v2RayTun нажмите «+» → импорт из буфера обмена. Для CDN требуется версия с поддержкой XHTTP; подтверждённые у нас клиенты CDN — HAPP и INCY."

    await message.answer(
        f"<b>{label} · {mode_name}</b>\n\n{details}",
        reply_markup=keyboard,
        parse_mode="HTML",
        disable_web_page_preview=True,
    )


async def route_device_callback(callback, flow) -> bool:
    """Route guest-safe device callbacks before the host bot requires login."""
    data = str(getattr(callback, "data", "") or "")
    message = getattr(callback, "message", None)
    if data.startswith("mc:devices:"):
        mode = data.removeprefix("mc:devices:")
        if mode == "open":
            mode = "vpn"
        if mode not in {"vpn", "cdn"}:
            await callback.answer("Неизвестный режим.", show_alert=True)
            return True
        if message is None:
            await callback.answer("Не удалось открыть меню.", show_alert=True)
        else:
            await send_device_menu(message, mode=mode)
            await callback.answer()
        return True
    if data.startswith("mc:device:"):
        device, mode = _callback_target_mode(data.removeprefix("mc:device:"))
        if message is None or device not in _DEVICE_CLIENTS:
            await callback.answer("Неизвестное устройство.", show_alert=True)
        else:
            await _send_device_clients(message, device, mode=mode)
            await callback.answer()
        return True
    if data.startswith("mc:install:"):
        client, mode = _callback_target_mode(data.removeprefix("mc:install:"))
        if message is None or client not in _CLIENT_LABELS:
            await callback.answer("Неизвестное приложение.", show_alert=True)
        else:
            await send_client_instructions(message, flow, client, mode=mode)
            await callback.answer()
        return True
    return False
