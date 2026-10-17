import asyncio,sys,time,os
sys.path.insert(0,os.environ.get("MAESTRO_TEST_BOT_MODULES","/root/vpn_bot/handlers"))
from maestro_customer_entry import callback_ack_middleware
from aiogram import Bot
from aiogram.methods import AnswerCallbackQuery,SendMessage
from aiogram.exceptions import TelegramNetworkError
async def run():
 bot=Bot("101:"+("A"*35));bot.session.middleware.register(callback_ack_middleware)
 async def stalled(bot,method,timeout=None):await asyncio.Event().wait()
 bot.session.make_request=stalled
 start=time.monotonic()
 assert await asyncio.wait_for(bot(AnswerCallbackQuery(callback_query_id="fixture")),5) is True
 assert time.monotonic()-start<4
 async def failed(bot,method,timeout=None):raise TelegramNetworkError(method,"fixture")
 bot.session.make_request=failed
 try:await bot(SendMessage(chat_id=202,text="fixture"))
 except TelegramNetworkError:pass
 else:raise AssertionError("notification failure was swallowed")
 await bot.session.close()
 print("PASS SDK callback deadline and notification error propagation")
asyncio.run(run())
