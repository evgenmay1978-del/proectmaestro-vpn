"""Pure owner callback contract shared by the Maestro Telegram handlers."""

import re
from typing import NamedTuple
from urllib.parse import parse_qs, urlencode, urlsplit, urlunsplit


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


def subscription_copy_url(raw: str) -> str:
    parsed = urlsplit(str(raw or ""))
    if (parsed.scheme != "https" or not parsed.hostname or parsed.username is not None
            or parsed.password is not None or parsed.fragment
            or not re.fullmatch(r"/sub/[^/]+", parsed.path)):
        raise ValueError("invalid subscription copy URL")
    query = parse_qs(parsed.query, keep_blank_values=True)
    query["format"] = ["links"]
    return urlunsplit(parsed._replace(query=urlencode(query, doseq=True)))


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
        urls[client] = descriptor.get("copy_url") or descriptor["url"]
    # Keep accepting the prior controller contract during the rolling upgrade.
    # One-tap descriptors remain available at the API, while these captions
    # intentionally give a normal HTTPS URL accepted by every import field.
    fallback = subscription_copy_url(urls["happ"])
    for client in ("incy", "karing"):
        if urlsplit(urls[client]).scheme != "https":
            urls[client] = fallback
    return AdminDeliveryChoices(
        incy_url=subscription_copy_url(urls["incy"]),
        happ_url=fallback,
        karing_url=subscription_copy_url(urls["karing"]),
    )


def admin_delivery_button_urls(choices: AdminDeliveryChoices) -> tuple[tuple[str, str], ...]:
    # A normal subscription URL downloads data; it does not open the named app.
    # Telegram's URL buttons also cannot carry the custom incy/karing schemes.
    return ()


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
        "1. Приложение MaestroVPN активируется по логину клиента.\n\n"
        "2. Incy, Happ или Karing: скопируйте HTTPS-ссылку целиком и добавьте подписку, "
        "либо отсканируйте QR:\n"
        f"<code>{deliveries.happ_url}</code>\n\n"
        "Для CDN нужен включённый доступ и баланс ГБ. После изменения обновите подписку в клиенте."
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
