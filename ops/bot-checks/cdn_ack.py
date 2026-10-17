import os
SUPPORT=os.environ.get("MAESTRO_TEST_BOT_MODULES","/opt/vpn_bot")
SOURCE=SUPPORT+"/maestro_customer_cdn.py"
import asyncio,importlib.util,sys,types
from pathlib import Path
sys.path.insert(0,SUPPORT)
spec=importlib.util.spec_from_file_location("cdn_candidate",SOURCE)
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
m.enabled=lambda:True
m.owner_ids=lambda:(101,)
m.admin_token=lambda:"fixture"
class Message:
 chat=types.SimpleNamespace(type="private",id=101)
 text="Control order"
 async def answer(self,*a,**kw): pass
 async def edit_text(self,*a,**kw): pass
class Callback:
 message=Message()
 from_user=types.SimpleNamespace(id=101)
 async def answer(self,*a,**kw):
  await asyncio.Event().wait()
async def run():
 obj=m.CDNCheckout.__new__(m.CDNCheckout);obj.locks={}
 calls=[]
 async def paid(cb,flow,identity):calls.append(identity)
 obj.paid=paid
 await obj.dispatch(Callback(),"paid","fixture-order",types.SimpleNamespace(login="fixture"))
 assert calls==["fixture-order"],"Telegram ACK blocked paid claim"
 row={"payment_state":"payment_claimed","product_id":"wl-gb-1-20260906","bytes":1000000000,"customer_notified":1}
 obj.order=lambda identity:dict(row)
 def update(identity,**kw):row["payment_state"]=kw.get("state",row["payment_state"])
 obj.update=update
 requests=[]
 async def request(method,path,token,**kwargs):
  requests.append(kwargs["headers"]["Idempotency-Key"])
  return {"order_id":"fixture-order","product_id":row["product_id"],"payment_state":"confirmed"}
 obj.request=request
 await obj.decide(Callback(),"cf","fixture-order")
 await obj.decide(Callback(),"cf","fixture-order")
 assert len(requests)==1 and row["payment_state"]=="confirmed","duplicate credit on confirmation replay"
 print("PASS: stalled acknowledgement does not block paid claim or confirmation; confirmation replay does not issue another credit")
asyncio.run(run())
