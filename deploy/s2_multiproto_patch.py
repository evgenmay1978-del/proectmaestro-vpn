#!/usr/bin/env python3
"""Additive patch for the server-2 naive bot (/opt/vpn_bot/bot_minimal.py):
adds a '🆕 Мультипротокол' button + handler that bridges the client into the
MaestroVPN panel and shows their login + QR + sub + protocols + app info, plus a
one-line app mention in both greeting variants. ONLY adds — never edits existing
logic. Backs up, verifies every insertion, py_compiles, rolls back on any failure."""
import py_compile
import shutil
import sys

F = "/opt/vpn_bot/bot_minimal.py"
BAK = F + ".mvbak"

src = open(F, encoding="utf-8").read()
if "🆕 Мультипротокол" in src:
    print("already patched — nothing to do")
    sys.exit(0)

orig = src


def must_replace(s, old, new, what):
    if s.count(old) < 1:
        raise SystemExit(f"PATCH ABORT: anchor not found for [{what}]")
    return s.replace(old, new, 1)


# 1) add the reply-keyboard button row to kb_main
src = must_replace(
    src,
    '        [KeyboardButton(text="🛍 Тарифы"),  KeyboardButton(text="ℹ️ Помощь")],\n    ]',
    '        [KeyboardButton(text="🛍 Тарифы"),  KeyboardButton(text="ℹ️ Помощь")],\n'
    '        [KeyboardButton(text="🆕 Мультипротокол")],\n    ]',
    "kb_main button",
)

# 2) greeting — bound variant
src = must_replace(
    src,
    'f"Нажмите 📊 <b>Статус</b> для проверки подписки."',
    'f"Нажмите 📊 <b>Статус</b> для проверки подписки.\\n\\n"\n'
    '            f"🆕 <b>Вышло наше приложение MaestroVPN</b> — 4 протокола в одном '
    '(Android/TV). Жми «🆕 Мультипротокол»."',
    "greeting bound",
)

# 2b) greeting — unbound variant
src = must_replace(
    src,
    '"👇 Выберите действие:"',
    '"🆕 <b>Вышло наше приложение MaestroVPN</b> — 4 протокола (Android/TV).\\n\\n"\n'
    '            "👇 Выберите действие:"',
    "greeting unbound",
)

# 3) append the handler just before async def main()
HANDLER = '''
# ─── MaestroVPN multi-protocol app (added) ───────────────────────────
APP_DOWNLOAD_URL = "https://wapmixx.ru:8911/update/latest.apk"
MAESTRO_CLAIM_URL = "https://wapmixx.ru:8911/claim"
_MV_PROTO = {"vless": "VLESS·Reality", "hysteria2": "Hysteria2", "naive": "NaiveProxy", "mieru": "Mieru"}


@dp.message(F.text == "🆕 Мультипротокол")
async def cmd_multiproto(msg: Message):
    proxy_user = db_get_bind(msg.from_user.id)
    if not proxy_user:
        await msg.answer("Сначала оформите подписку через 🛍 Тарифы.")
        return
    try:
        async with httpx.AsyncClient(verify=False, timeout=30) as c:
            r = await c.post(MAESTRO_CLAIM_URL, json={"code": proxy_user})
        if r.status_code != 200:
            await msg.answer(f"Панель вернула HTTP {r.status_code}. Попробуйте позже.")
            return
        d = r.json()
        sub_url = d.get("sub_url") or ""
        if not sub_url:
            await msg.answer("Не удалось получить подписку.")
            return
        protos = [_MV_PROTO.get(p, p) for p in (d.get("protocols") or [])]
        caption = (
            f"🆕 <b>Наше приложение MaestroVPN</b>\\n\\n"
            f"Сразу <b>{len(protos)} современных протокола</b>: {', '.join(protos)} — переключаются в один тап.\\n\\n"
            f"🔑 <b>Ваш логин для входа в приложение:</b>\\n<code>{proxy_user}</code>\\n"
            f"<i>(в приложении: «Ввести код подписки» → вставьте логин)</i>\\n\\n"
            f"📷 Либо отсканируйте QR / вставьте ссылку:\\n<code>{sub_url}</code>\\n\\n"
            f"⬇️ <b>Скачать (всегда актуальная версия):</b>\\n{APP_DOWNLOAD_URL}\\n\\n"
            f"📱 Только <b>Android</b> и <b>Android TV</b>.\\n"
            f"🍏 Для iPhone версии пока нет — нужен платный аккаунт разработчика Apple. "
            f"Наберётся достаточно желающих с iPhone, готовых оплатить — выложим в App Store.\\n\\n"
            f"💡 Приложения с таким набором протоколов больше нет ни у кого."
        )
        await msg.answer_photo(
            types.BufferedInputFile(gen_qr(sub_url), filename="maestrovpn_app.png"),
            caption=caption,
        )
    except Exception as e:  # noqa: BLE001
        await msg.answer(f"Ошибка: {e}")


'''
src = must_replace(src, "async def main():", HANDLER + "async def main():", "append handler")

shutil.copy2(F, BAK)
open(F, "w", encoding="utf-8").write(src)
try:
    py_compile.compile(F, doraise=True)
except Exception as e:
    shutil.move(BAK, F)
    raise SystemExit(f"PATCH ABORT: py_compile failed, rolled back: {e}")
print("PATCHED OK. backup at", BAK)
