"""MaestroVPN TV — order confirmation handler for the vpn_bot (server 1).

When a customer presses «Я оплатил» on the TV, maestro-panel sends the admin a
message with a one-tap «✅ Подтвердить оплату» button (callback_data
"moconf:<order_id>"). This handler catches that tap and tells maestro-panel to
confirm + provision the customer (POST /admin/order/confirm), then the TV's poll
activates automatically.

INSTALL (gated production edit — run yourself on server 1):
  1. cp /root/maestrovpn-tv/deploy/vpn_bot_maestro_orders.py /root/vpn_bot/handlers/maestro_orders.py
  2. in /root/vpn_bot/main.py:
       - add `maestro_orders` to the `from handlers import ...` line
       - add `dp.include_router(maestro_orders.router)` next to the other include_router calls
  3. systemctl restart vpnbot
"""
import os

import httpx
from aiogram import F, Router
from aiogram.types import CallbackQuery

router = Router()

MAESTRO_URL = os.getenv("MAESTRO_URL", "http://127.0.0.1:8910")

try:
    import config as _cfg

    _ADMINS = {str(x) for x in getattr(_cfg, "ADMIN_IDS", [])}
except Exception:
    _ADMINS = {x.strip() for x in os.getenv("ADMIN_IDS", "").split(",") if x.strip()}


def _maestro_token() -> str:
    try:
        with open("/etc/maestro-panel.env", encoding="utf-8") as f:
            for line in f:
                if line.startswith("MAESTRO_ADMIN_TOKEN="):
                    return line.split("=", 1)[1].strip()
    except OSError:
        pass
    return ""


@router.callback_query(F.data.func(lambda d: bool(d) and d.startswith("moconf:")))
async def confirm_order(cb: CallbackQuery):
    if _ADMINS and str(cb.from_user.id) not in _ADMINS:
        await cb.answer("Только администратор", show_alert=True)
        return
    order_id = cb.data.split(":", 1)[1]
    try:
        async with httpx.AsyncClient(verify=False, timeout=30) as client:
            r = await client.post(
                f"{MAESTRO_URL}/admin/order/confirm",
                json={"order_id": order_id},
                headers={"Authorization": f"Bearer {_maestro_token()}"},
            )
        if r.status_code == 200:
            base = cb.message.text or ""
            await cb.message.edit_text(base + "\n\n✅ Подтверждено — подписка выдана")
        else:
            await cb.answer(f"Панель вернула HTTP {r.status_code}: {r.text[:120]}", show_alert=True)
    except Exception as e:  # noqa: BLE001
        await cb.answer(f"Ошибка: {e}", show_alert=True)


@router.callback_query(F.data.func(lambda d: bool(d) and d.startswith("aclsub:")))
async def show_app_subscription(cb: CallbackQuery):
    """Admin button «🔗 Подписка (приложение)» on a client card → MaestroVPN app sub."""
    if _ADMINS and str(cb.from_user.id) not in _ADMINS:
        await cb.answer("Только администратор", show_alert=True)
        return
    login = cb.data.split(":", 1)[1]
    try:
        async with httpx.AsyncClient(verify=False, timeout=20) as client:
            r = await client.get(
                f"{MAESTRO_URL}/admin/customer",
                params={"login": login},
                headers={"Authorization": f"Bearer {_maestro_token()}"},
            )
        if r.status_code == 404:
            await cb.answer("В приложении MaestroVPN у этого клиента подписки нет.", show_alert=True)
            return
        if r.status_code != 200:
            await cb.answer(f"Панель вернула HTTP {r.status_code}", show_alert=True)
            return
        d = r.json()
        exp = d.get("expires", "") or ""
        days = "?"
        try:
            from datetime import datetime, timezone
            dt = datetime.fromisoformat(exp.replace("Z", "+00:00"))
            days = max(0, (dt - datetime.now(timezone.utc)).days)
        except Exception:
            pass
        status = "🟢 активна" if d.get("active") else "🔴 истекла"
        protos = ", ".join(d.get("protocols") or []) or "—"
        await cb.answer(
            f"MaestroVPN-приложение — {login}\n"
            f"Подписка: {status}\n"
            f"До: {exp[:10]} ({days} дн)\n"
            f"Протоколы: {protos}",
            show_alert=True,
        )
    except Exception as e:  # noqa: BLE001
        await cb.answer(f"Ошибка: {e}", show_alert=True)
