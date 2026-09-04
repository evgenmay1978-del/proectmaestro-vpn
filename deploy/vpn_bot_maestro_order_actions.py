"""Pure owner callback contract shared by the Maestro Telegram handlers."""

import re
from typing import NamedTuple
from urllib.parse import urlsplit


TOPUP_CONFIRM_PREFIX = "mwcf:"
TOPUP_REJECT_PREFIX = "mwrj:"
_PREFIX_BY_DECISION = {
    "confirm": TOPUP_CONFIRM_PREFIX,
    "reject": TOPUP_REJECT_PREFIX,
}
_OPAQUE_ORDER_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9_-]{0,58}\Z")


class TopUpAdminRequest(NamedTuple):
    decision: str
    order_id: str
    path: str
    idempotency_key: str


class AdminDeliveryChoices(NamedTuple):
    incy_url: str
    happ_url: str
    karing_url: str


def admin_delivery_choices(deliveries: dict) -> AdminDeliveryChoices:
    expected = {
        "incy": "INCY_ONE_TAP",
        "happ": "COPY_HTTPS_URL_AND_QR",
        "karing": "KARING_INSTALL_CONFIG",
    }
    urls = {}
    for client, format_name in expected.items():
        descriptor = deliveries.get(client) if isinstance(deliveries, dict) else None
        if (
            not isinstance(descriptor, dict)
            or descriptor.get("client") != client
            or descriptor.get("format") != format_name
            or not isinstance(descriptor.get("url"), str)
            or not descriptor["url"].strip()
        ):
            raise ValueError("invalid admin subscription delivery")
        urls[client] = descriptor["url"]
    return AdminDeliveryChoices(
        incy_url=urls["incy"],
        happ_url=urls["happ"],
        karing_url=urls["karing"],
    )


def admin_delivery_button_urls(choices: AdminDeliveryChoices) -> tuple[tuple[str, str], ...]:
    buttons = []
    for label, value in (("Открыть в Incy", choices.incy_url), ("Открыть в Karing", choices.karing_url)):
        parsed = urlsplit(value)
        if parsed.scheme in {"http", "https"} and parsed.hostname and parsed.username is None and parsed.password is None:
            buttons.append((label, value))
    return tuple(buttons)


def admin_subscription_caption(
    login: str,
    status: str,
    expires: str,
    days: str,
    protocols: str,
    protocol_count: int,
    sub_url: str,
    deliveries: AdminDeliveryChoices,
) -> str:
    return (
        f"🦊 <b>MaestroVPN — подписка</b>\n"
        f"Клиент: <code>{login}</code>\n"
        f"Статус: {status}  •  до {expires}{days}\n"
        f"Протоколы ({protocol_count}): {protocols}\n\n"
        "1. MaestroVPN 1.0.157: отсканируйте QR или вставьте обычную ссылку. "
        "В этой версии доступны только обычные серверы:\n"
        f"<code>{sub_url}</code>\n\n"
        "2. Incy: скопируйте ссылку, откройте импорт подписки и вставьте её:\n"
        f"<code>{deliveries.incy_url}</code>\n\n"
        "3. Happ: скопируйте HTTPS-ссылку или используйте QR:\n"
        f"<code>{deliveries.happ_url}</code>\n\n"
        "4. Karing: скопируйте ссылку, откройте импорт подписки и вставьте её:\n"
        f"<code>{deliveries.karing_url}</code>\n\n"
        "CDN/LTE доступен только через Incy, Happ или Karing после покупки ГБ. "
        "В MaestroVPN 1.0.157 CDN/LTE не появится до отдельного разрешённого обновления приложения."
    )


def build_topup_callback(decision: str, order_id: str) -> str:
    prefix = _PREFIX_BY_DECISION.get(decision)
    if prefix is None or not _OPAQUE_ORDER_ID.fullmatch(order_id):
        raise ValueError("invalid top-up callback")
    value = prefix + order_id
    if len(value.encode("utf-8")) > 64:
        raise ValueError("top-up callback exceeds Telegram limit")
    return value


def topup_admin_request(callback_data: str) -> TopUpAdminRequest:
    for decision, prefix in _PREFIX_BY_DECISION.items():
        if callback_data.startswith(prefix):
            order_id = callback_data[len(prefix):]
            if build_topup_callback(decision, order_id) != callback_data:
                break
            return TopUpAdminRequest(
                decision=decision,
                order_id=order_id,
                path=f"/admin/order/{order_id}/{decision}",
                idempotency_key=f"telegram-admin-topup-{decision}-{order_id}",
            )
    raise ValueError("invalid top-up callback")
