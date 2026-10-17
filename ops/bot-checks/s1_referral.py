import asyncio,sys,os,tempfile,types,socket,json,logging,time,math
from pathlib import Path
from datetime import datetime,timedelta
root=Path(tempfile.mkdtemp(prefix="maestro-s1-qa-"))
os.environ.update(BOT_TOKEN="101:"+("A"*35),MAESTRO_CUSTOMER_BINDINGS_PATH=str(root/"bindings.db"),MAESTRO_CUSTOMER_CDN_PURCHASES_ENABLE="0")
sys.path.insert(0,"/root/vpn_bot")
from config import config
config.DB_PATH=str(root/"bot.db");config.ADMIN_IDS=[101]
from database import Database
from handlers import client as c,admin as a
logging.disable(logging.CRITICAL)
def deny(*args,**kwargs):raise AssertionError("unexpected real network access")
socket.socket.connect=deny
db=Database()
sent=[]
class Msg:
 def __init__(self,text="",uid=101):
  self.text=text;self.from_user=types.SimpleNamespace(id=uid,username="fixture",is_bot=False);self.chat=types.SimpleNamespace(id=uid,type="private");self.message_id=17;self.bot=Bot();self.html_text=text
 async def answer(self,text="",**kw):sent.append((self.chat.id,str(text),kw));return self
 async def edit_text(self,text="",**kw):self.text=text;sent.append((self.chat.id,str(text),kw));return self
 async def delete(self):pass
 async def answer_photo(self,*a,**kw):return self
 async def copy_to(self,*a,**kw):return self
class Bot:
 id=999
 async def send_message(self,uid,text,**kw):sent.append((uid,str(text),kw));return Msg(text,uid)
 async def send_photo(self,*a,**kw):return Msg()
 async def get_me(self):return types.SimpleNamespace(username="fixture_bot",id=999)
class CB:
 def __init__(self,data,uid=101):self.data=data;self.from_user=types.SimpleNamespace(id=uid,username="fixture",is_bot=False);self.message=Msg(uid=uid);self.bot=Bot()
 async def answer(self,*a,**kw):pass
class State:
 def __init__(self):self.data={};self.state=None
 async def clear(self):self.data={};self.state=None
 async def update_data(self,**kw):self.data.update(kw)
 async def set_state(self,value):self.state=getattr(value,"state",value)
 async def get_state(self):return self.state
 async def get_data(self):return dict(self.data)

clients={}
def newclient(email,days=30):
 v=types.SimpleNamespace(email=email,enable=True,expiry_time=int((time.time()+days*86400)*1000),used_gb=0,total_gb=0,sub_id="fixture-sub",tg_id=0)
 clients[email]=v;return v
class API:
 def get_client(self,email):return (clients[email],1) if email in clients else None
 get_client_by_email=get_client
 def get_all_clients(self):return list(clients.values())
 def get_inbounds(self):return [types.SimpleNamespace(id=1,remark="Fixture",total_clients=len(clients))]
 def get_inbound_clients(self,i):return list(clients.values())
 def set_client_enable(self,email,enable):clients[email].enable=enable;return True
 def subscription_url(self,*args):return "https://subscription.invalid/sub/fixture"
 def get_client_subscription(self,email):return clients[email],"https://subscription.invalid/sub/fixture"
api=API()
db.upsert_user(202,"fixture");db.bind_user(202,1,"qa_user");newclient("qa_user")
async def claim(*args):return "https://subscription.invalid/sub/fixture"
c._claim_sub_url=claim;a._maestro_sub_url=claim
renewals={}
async def renew(login,days,idempotency_key=None):
 if idempotency_key and idempotency_key in renewals:return
 clients[login].expiry_time+=days*86400000
 renewals[idempotency_key or str(len(renewals))]=(login,days)
a._maestro_renew=renew
def provision(api,db,tg_id,days,inbound_id=None):
 email="qa_"+str(tg_id);newclient(email,days);db.bind_user(tg_id,1,email);return email,"https://subscription.invalid/sub/fixture"
c.provision_client=provision;a.provision_client=provision
class HTTP:
 def __init__(self,**kw):pass
 async def __aenter__(self):return self
 async def __aexit__(self,*args):pass
 async def post(self,url,json,**kw):
  assert url.endswith("/admin/set-expiry")
  clients[json["login"]].expiry_time=int(datetime.fromisoformat(json["expires"]).timestamp()*1000)
  return types.SimpleNamespace(raise_for_status=lambda:None)
a.httpx.AsyncClient=HTTP
results=[]
async def case(name,fn):
 if name!="referral bonus once on first payment":return
 try:await fn();results.append({"case":name,"ok":True})
 except Exception as e:results.append({"case":name,"ok":False,"error":type(e).__name__,"detail":str(e)[:160]})
async def run():
 st=State()
 async def status():
  clients["qa_user"].expiry_time=int((time.time()+3600)*1000)
  active,days=await c._user_status(db,api,202);assert active and days==1
 await case("last paid hour remains active",status)
 for name,fn in [
 ("buy tariffs",lambda:c.client_buy(CB("client:buy",202),st,db,api)),
 ("renew tariffs",lambda:c.client_renew(CB("client:renew",202),st,db,api)),
 ("keys and subscription",lambda:c.client_keys(CB("client:keys",202),st,db,api)),
 ("history",lambda:c.client_history(CB("client:history",202),st,db,api)),
 ("referral screen",lambda:c.client_referrals(CB("client:referrals",202),st,db,api)),
 ("admin home",lambda:a.admin_main(CB("admin:main"),st,db,api)),
 ("admin client list",lambda:a.admin_clients(CB("admin:clients"),st,db,api)),
 ("admin search",lambda:a.admin_search_result(Msg("qa_user"),st,db,api)),
 ("admin client card",lambda:a.admin_client_show(CB("aclshow:qa_user"),st,db,api)),
 ("admin orders",lambda:a.admin_orders(CB("admin:orders"),st,db,api)),
 ("statistics",lambda:a.admin_stats(CB("admin:stats"),st,db,api)),
 ("tariff settings",lambda:a.admin_settings(CB("admin:settings"),st,db,api)),
 ("binding inbound selection",lambda:a.admin_bindings(CB("admin:bindings"),st,db,api)),
 ("binding client selection",lambda:a.admin_bind_ib(CB("admin:bind_ib:1"),st,db,api)),
 ("create binding link",lambda:a.admin_bind_client(CB("admin:bind_client:1:qa_user"),st,db,api)),
 ]:await case(name,fn)
 async def paid():
  await st.update_data(days=30,amount=400,action="renew")
  await c._submit_order(Bot(),st,db,Msg(uid=202),202,"fixture")
  await c._submit_order(Bot(),st,db,Msg(uid=202),202,"fixture")
  orders=db.get_pending_orders();assert len(orders)==1
  assert any(uid==101 and kw.get("reply_markup") for uid,_,kw in sent)
  ident=orders[0]["id"];before=clients["qa_user"].expiry_time
  await a.admin_approve(CB("admin:approve:"+str(ident)),st,db,api)
  await a.admin_approve(CB("admin:approve:"+str(ident)),st,db,api)
  assert db.get_order(ident)["status"]=="approved" and clients["qa_user"].expiry_time-before==30*86400000
 await case("ordinary order notification approval and no duplicate renewal",paid)
 async def reject():
  oid=db.create_order(202,1,"qa_user",30,400,"renew",None);old=clients["qa_user"].expiry_time
  await a.admin_reject(CB("admin:reject:"+str(oid)),st,db,api)
  assert db.get_order(oid)["status"]=="rejected" and clients["qa_user"].expiry_time==old
 await case("reject payment without changing days",reject)
 async def trial():
  db.upsert_user(203,"trial")
  await c.client_trial(CB("client:trial",203),st,db,api);n=len(clients)
  await c.client_trial(CB("client:trial",203),st,db,api)
  assert len(clients)==n and db.get_user(203)["trial_used"]
 await case("trial activated only once",trial)
 async def block():
  await a.admin_client_toggle(CB("acltog:qa_user"),st,db,api);assert not clients["qa_user"].enable
  await a.admin_client_toggle(CB("acltog:qa_user"),st,db,api);assert clients["qa_user"].enable
 await case("block and unblock isolated client",block)
 async def days():
  old=clients["qa_user"].expiry_time
  await a.admin_client_days_start(CB("acldays:qa_user"),st,db,api)
  await a.admin_client_days_input(Msg("5"),st,db,api)
  key=st.data["edit_intent"]
  await a.admin_client_days_apply(CB("acldaysapply:"+key),st,db,api)
  await a.admin_client_days_apply(CB("acldaysapply:"+key),st,db,api)
  assert clients["qa_user"].expiry_time-old==5*86400000
 await case("manual days and repeated confirmation",days)
 async def date():
  await a.admin_client_date_start(CB("acldate:qa_user"),st,db,api)
  await a.admin_client_date_input(Msg("25.12.2027 12:00"),st,db,api)
  expected=int(datetime.fromisoformat(st.data["edit_expiry"]).timestamp()*1000)
  await a.admin_client_date_apply(CB("acldateapply:"+st.data["edit_intent"]),st,db,api)
  assert clients["qa_user"].expiry_time==expected
 await case("explicit Moscow date applied exactly",date)
 async def broadcast():
  await a.admin_broadcast_send(Msg("fixture message"),st,db,api)
  assert any(uid==202 and text=="fixture message" for uid,text,_ in sent)
 await case("broadcast to isolated clients only",broadcast)
 async def unauthorized():
  old=clients["qa_user"].enable;await a.admin_client_toggle(CB("acltog:qa_user",204),st,db,api)
  assert clients["qa_user"].enable==old
 await case("non-admin cannot block",unauthorized)
 async def referral_bonus():
  config.REFERRAL_BONUS_DAYS=7;config.REFERRAL_FRIEND_BONUS_DAYS=3
  db.upsert_user(203,"friend");db.bind_user(203,1,"qa_friend");db.set_referrer(203,202)
  newclient("qa_friend")
  before_ref=clients["qa_user"].expiry_time;before_friend=clients["qa_friend"].expiry_time
  oid=db.create_order(203,1,"qa_friend",30,400,"renew",None)
  await a.admin_approve(CB("admin:approve:"+str(oid)),st,db,api)
  await a.admin_approve(CB("admin:approve:"+str(oid)),st,db,api)
  assert clients["qa_user"].expiry_time-before_ref==7*86400000
  assert clients["qa_friend"].expiry_time-before_friend==33*86400000
  oid=db.create_order(203,1,"qa_friend",30,400,"renew",None)
  await a.admin_approve(CB("admin:approve:"+str(oid)),st,db,api)
  assert clients["qa_user"].expiry_time-before_ref==7*86400000
 await case("referral bonus once on first payment",referral_bonus)
 print(json.dumps({"isolated":True,"network_blocked":True,"results":results},ensure_ascii=False))
 if not all(r["ok"] for r in results):raise SystemExit(1)
asyncio.run(run())
