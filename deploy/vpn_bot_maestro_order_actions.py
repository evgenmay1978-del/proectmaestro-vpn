"""Pure owner callback contract shared by the Maestro Telegram handlers."""

import re
from typing import NamedTuple


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
