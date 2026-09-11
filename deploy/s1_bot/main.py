import sys
import asyncio
import logging

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    stream=sys.stdout,
    force=True,
)
log = logging.getLogger(__name__)

from aiogram import Bot, Dispatcher
from aiogram.enums import ParseMode
from aiogram.client.default import DefaultBotProperties
from aiogram.fsm.storage.memory import MemoryStorage
from aiogram.types import BotCommand, InlineKeyboardButton
from config import config
from database import Database
from api_3xui import API3xUI
from handlers import client, admin, bind, maestro_orders
from scheduler import setup_scheduler

async def main():
    db = Database()
    api = API3xUI()
    bot = Bot(token=config.BOT_TOKEN, default=DefaultBotProperties(parse_mode=ParseMode.HTML))
    dp = Dispatcher(storage=MemoryStorage())

    try:
        inbounds = api.get_inbounds()
        log.info(f"3x-ui OK: {len(inbounds)} инбаундов")
    except Exception as e:
        log.warning(f"3x-ui недоступен: {e}")

    scheduler = setup_scheduler(bot, db, api)
    scheduler.start()
    log.info("Планировщик запущен")

    dp["db"] = db
    dp["api"] = api

    from handlers.maestro_customer_entry import configure_customer_ui, callback_ack_middleware
    bot.session.middleware.register(callback_ack_middleware)

    def callback_state(callback):
        return dp.fsm.get_context(bot=callback.bot, chat_id=callback.message.chat.id,
                                  user_id=callback.from_user.id)

    async def ordinary_status(message):
        return await client.ordinary_status(message.chat.id, db, api)

    async def ordinary_renew(callback):
        await client.open_ordinary_renew(callback, callback_state(callback), db, api)

    async def ordinary_connect(callback):
        return False

    async def bind_login(message, login):
        return await client.bind_ordinary_login(message, login, db, api)

    async def clear_state(message):
        state = dp.fsm.get_context(bot=message.bot, chat_id=message.chat.id,
                                   user_id=message.chat.id)
        await state.clear()

    async def open_admin(callback):
        await admin.admin_main(callback, callback_state(callback), db, api)

    def extra_buttons(message):
        rows = []
        if client._show_trial(db, message.chat.id):
            rows.append([InlineKeyboardButton(
                text=f"🎁 Попробовать VPN: {config.TRIAL_DAYS} дн.", callback_data="client:trial")])
        if client._bound_login(db, message.chat.id):
            rows.append([InlineKeyboardButton(text="🔗 Мой ключ VPN / QR", callback_data="client:keys")])
        rows.append([InlineKeyboardButton(text="🎁 Пригласить друга", callback_data="client:referrals"),
                     InlineKeyboardButton(text="🧾 Мои оплаты", callback_data="client:history")])
        return rows

    configure_customer_ui(status=ordinary_status, renew=ordinary_renew,
                          connect=ordinary_connect, is_admin=admin.is_admin,
                          admin=open_admin, extra_buttons=extra_buttons,
                          bind_login=bind_login, clear_state=clear_state)

    @dp.message.outer_middleware()
    async def _private_message(handler, event, data):
        if event.chat.type != "private" or event.chat.id != event.from_user.id:
            await event.answer("Откройте личный чат с ботом: /start.")
            return
        return await handler(event, data)

    @dp.callback_query.outer_middleware()
    async def _log_cb(handler, event, data):
        if (not event.message or event.message.chat.type != "private"
                or event.message.chat.id != event.from_user.id):
            await event.answer("Откройте личный чат с ботом.", show_alert=True)
            return
        log.info("CALLBACK action=%s", (event.data or "").split(":", 1)[0])
        return await handler(event, data)

    @dp.errors()
    async def _on_error(event):
        log.exception(f"HANDLER ERROR on {getattr(event.update, 'event_type', '?')}: {event.exception}")
        # Give the user feedback instead of a silently dead button / no reply.
        msg = ("⚠️ Что-то пошло не так. Попробуйте ещё раз, а если повторится — "
               f"напишите в поддержку @{config.SUPPORT_USERNAME}.")
        try:
            upd = event.update
            if getattr(upd, "callback_query", None):
                await upd.callback_query.answer(msg, show_alert=True)
            elif getattr(upd, "message", None):
                await upd.message.answer(msg)
        except Exception:
            pass
        return True

    dp.include_router(bind.router)
    from handlers.maestro_customer import build_customer_router_from_env
    dp.include_router(build_customer_router_from_env())
    dp.include_router(client.router)
    dp.include_router(admin.router)
    dp.include_router(maestro_orders.router)

    # The «menu» button (BotFather command list) — a professional touch so users
    # see /start and /help without guessing.
    try:
        await bot.set_my_commands([
            BotCommand(command="start", description="🏠 Главное меню"),
            BotCommand(command="help", description="❓ Помощь и поддержка"),
            BotCommand(command="admin", description="🔐 Администратору"),
        ])
    except Exception as e:
        log.warning(f"set_my_commands: {e}")

    log.info(f"Бот запущен: @{config.BOT_USERNAME}")
    try:
        await dp.start_polling(bot, skip_updates=False)
    finally:
        scheduler.shutdown(wait=False)
        await bot.session.close()

if __name__ == "__main__":
    asyncio.run(main())
