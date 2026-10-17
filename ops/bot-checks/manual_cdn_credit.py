import sys,asyncio,types,json
sys.path.insert(0,"/opt/vpn_bot")
import maestro_customer_admin as a
from maestro_customer_entry import configure_customer_ui
configure_customer_ui(is_admin=lambda n:n==101)
a.admin_token=lambda:"fixture"
class Message:
 chat=types.SimpleNamespace(type="private",id=101)
 from_user=types.SimpleNamespace(id=101,is_bot=False)
 bot=types.SimpleNamespace(id=999)
 reply_to_message=types.SimpleNamespace(message_id=17,from_user=types.SimpleNamespace(id=999))
 message_id=17
 text="2"
 async def answer(self,*args,**kwargs):return self
 async def edit_text(self,*args,**kwargs):return self
class CB:
 from_user=types.SimpleNamespace(id=101)
 message=Message()
 async def answer(self,*args,**kwargs):pass
class Checkout:
 def __init__(self):self.balance=0;self.keys=[]
 async def request(self,method,path,token,**kwargs):
  if method=="POST":
   key=kwargs["headers"]["Idempotency-Key"]
   if key not in self.keys:self.balance+=kwargs["json"]["gb"]*1000000000
   self.keys.append(key)
  return {"remaining_bytes":self.balance,"primary_active":True,"enabled":True}
async def run():
 api=Checkout();admin=a.CustomerAdmin(api);key=a.account_key("fixture")
 await admin.dispatch(CB(),"acredit",key)
 message=Message();assert admin.is_credit_reply(message)
 for value in ("0","-1","abc","1.5"):
  message.text=value;await admin.credit_reply(message);assert not admin.intents
 message.text="2";await admin.credit_reply(message)
 intent=next(iter(admin.intents))
 await admin.dispatch(CB(),"agrant",intent)
 await admin.dispatch(CB(),"agrant",intent)
 assert api.balance==2000000000 and len(set(api.keys))==1
 other=CB();other.from_user=types.SimpleNamespace(id=203)
 await admin.dispatch(other,"agrant",intent)
 assert len(api.keys)==2
 print(json.dumps({"manual_credit_input_validation":True,"reply_owner_check":True,"stable_idempotency_key":True,"non_admin_rejected":True,"real_network_used":False}))
asyncio.run(run())
