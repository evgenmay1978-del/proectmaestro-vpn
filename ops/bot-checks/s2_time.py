import ast,asyncio,importlib.util,json,os,sys,tempfile,types,logging,socket
from pathlib import Path
from datetime import datetime,timedelta
root=Path(tempfile.mkdtemp(prefix="maestro-bot-qa-"))
os.environ.update(BOT_TOKEN="101:"+("A"*35),ADMIN_IDS="101",DB_PATH=str(root/"bot.db"),LOG_PATH=str(root/"bot.log"),MAESTRO_CUSTOMER_BINDINGS_PATH=str(root/"bindings.db"),MAESTRO_CUSTOMER_CDN_PURCHASES_ENABLE="0")
os.environ.pop("MAESTRO_CUSTOMER_CDN_TEST_LOGIN",None)
sys.path.insert(0,"/opt/vpn_bot")
source=Path("/opt/vpn_bot/bot_minimal.py").read_text()
lines=source.splitlines(keepends=True)
for n in ast.walk(ast.parse(source)):
 if isinstance(n,ast.Expr) and isinstance(n.value,ast.Call) and isinstance(n.value.func,ast.Name) and n.value.func.id=="load_dotenv":
  for i in range(n.lineno-1,n.end_lineno):lines[i]="\n"
module=types.ModuleType("bot_qa");module.__file__=str(root/"bot.py");sys.modules["bot_qa"]=module
exec(compile("".join(lines),module.__file__,"exec"),module.__dict__)
m=module;logging.disable(logging.CRITICAL)
def deny(*a,**k):raise AssertionError("unexpected real network access")
socket.socket.connect=deny
m.ADMIN_ID=101
assert m.is_admin(101) and not m.is_admin(203)
sent=[]
class Msg:
 def __init__(self,text="",uid=101):
  self.text=text;self.from_user=types.SimpleNamespace(id=uid,username="fixture",is_bot=False);self.chat=types.SimpleNamespace(id=uid,type="private");self.message_id=17
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
 def __init__(self,data,uid=101):self.data=data;self.from_user=types.SimpleNamespace(id=uid,username="fixture",first_name="Fixture",is_bot=False);self.message=Msg(uid=uid);self.bot=Bot()
 async def answer(self,*a,**kw):pass
class State:
 def __init__(self):self.data={};self.state=None
 async def clear(self):self.data={};self.state=None
 async def update_data(self,**kw):self.data.update(kw)
 async def set_state(self,value):self.state=getattr(value,"state",value)
 async def get_state(self):return self.state
 async def get_data(self):return dict(self.data)
m.bot=Bot()
with m.get_db() as db:
 db.execute('ALTER TABLE subscriptions ADD COLUMN sub_url TEXT')
 db.commit()
users={}
class Resp:
 def __init__(self,status=200,payload=None):self.status_code=status;self.payload=payload or {}
 def json(self):return self.payload
async def req(method,path,**kw):
 if method=="GET":return Resp(payload={"users":list(users.values())})
 if method=="POST":
  p=kw["json"];u=p["username"]
  if u in users:return Resp(409)
  users[u]={"username":u,"password":p["password"],"expiresAt":(datetime.now()+timedelta(days=p["expireDays"])).isoformat() if p.get("expireDays") else None}
  return Resp(201)
 if method=="DELETE":
  u=path.rsplit("/",1)[-1]
  return Resp(204 if users.pop(u,None) else 404)
 raise AssertionError("unexpected panel operation")
m.panel._req=req
mirrors=[];m.maestro_sync_expiry=lambda u,e:mirrors.append((u,e))
async def claim(u):return "https://subscription.invalid/sub/fixture"
m._claim_sub_url=claim
results=[]
async def case(name,fn):
 if name not in {"create isolated client","bind existing client","UTC expiry preserves last hour and renewal"}:return
 try:await fn();results.append({"case":name,"ok":True})
 except Exception as e:results.append({"case":name,"ok":False,"error":type(e).__name__,"detail":str(e)[:160]})
async def run():
 st=State()
 async def add():
  await m.adm_add_start(Msg(),st);await m.adm_add_handle(Msg("qa_user password 30"),st)
  assert "qa_user" in users and st.state is None
 await case("create isolated client",add)
 async def invalid():
  await m.adm_add_handle(Msg("bad_days password invalid"),st)
  assert "bad_days" not in users
 await case("reject invalid creation days",invalid)
 async def bind():
  await m.adm_link_start(Msg(),st);await m.adm_link_handle(Msg("qa_user 202"),st)
  assert m.db_get_bind(202)=="qa_user"
 await case("bind existing client",bind)
 async def ownership():
  try:await m.legacy_customer_bind_login(Msg(uid=203),"qa_user")
  except ValueError:pass
  else:raise AssertionError("another user took binding")
  assert m.db_get_bind(202)=="qa_user"
 await case("reject binding owned by another Telegram",ownership)
 for name,fn in [("admin home",lambda:m.cb_admin_home(CB("s2:admin"),st)),("client list",lambda:m.send_admin_users(Msg())),("client search",lambda:m.send_admin_users(Msg(),query="qa_user")),("client card",lambda:m.send_admin_card(Msg(),users["qa_user"])),("statistics",lambda:m.send_admin_stats(Msg())),("payment queue",lambda:m.send_admin_payments(Msg())),("legacy connect",lambda:m.legacy_customer_connect(CB("mc:ordinary:connect",202)))]:
  await case(name,fn)
 async def days():
  old=users["qa_user"]["expiresAt"];await st.set_state(m.AdminFSM.set_days);await st.update_data(username="qa_user")
  await m.adm_setdays_handle(Msg("5"),st);assert users["qa_user"]["expiresAt"]>old
 await case("add days to isolated client",days)
 async def date():
  await st.set_state(m.AdminFSM.set_date);await st.update_data(username="qa_user")
  text=(datetime.now()+timedelta(days=10)).strftime("%d.%m.%Y");await m.adm_setdays_handle(Msg(text),st);assert st.state is None
 await case("set date for isolated client",date)
 async def bad_date():
  old=users["qa_user"].copy();await st.set_state(m.AdminFSM.set_date);await st.update_data(username="qa_user")
  await m.adm_setdays_handle(Msg("31.02.2027"),st);assert users["qa_user"]==old
 await case("reject invalid date without changing access",bad_date)
 async def link():
  await m.adm_bindlink_make(Msg("qa_user"),st)
  assert any("start=bind_" in t for _,t,_ in sent)
 await case("create binding link",link)
 async def broadcast():
  n=len(sent);await m.adm_broadcast_send(Msg("fixture-only announcement"),st)
  assert any(uid==202 and text=="fixture-only announcement" for uid,text,_ in sent[n:])
 await case("broadcast only to isolated fixture recipients",broadcast)
 async def delete():
  await m.cb_del(CB("del:qa_user"));assert "qa_user" not in users and m.db_get_bind(202) is None
 await case("delete isolated client and binding",delete)
 async def deny_admin():
  n=len(users);await m.adm_add_handle(Msg("unauthorized password 30",203),st);assert len(users)==n
 await case("reject non-admin mutation",deny_admin)
 async def utc_expiry():
  from datetime import timezone
  assert m.days_left((datetime.now(timezone.utc)+timedelta(hours=1)).isoformat())==1
  assert m.days_left((datetime.now(timezone.utc)-timedelta(hours=1)).isoformat())==0
  old=datetime.now(timezone.utc)+timedelta(days=10,hours=2)
  users["qa_user"]["expiresAt"]=old.isoformat()
  await m.cb_paid(CB("paid:30:400",202))
  with m.get_db() as q:payment=q.execute("select id from payments").fetchone()[0]
  await m.cb_approve(CB("approve_id:"+str(payment)))
  with m.get_db() as q:value=q.execute("select expires_at from subscriptions where tg_id=202").fetchone()[0]
  actual=datetime.fromisoformat(value).astimezone(timezone.utc)
  assert abs((actual-(old+timedelta(days=30))).total_seconds())<1
 await case("UTC expiry preserves last hour and renewal",utc_expiry)
 print(json.dumps({"isolated":True,"real_network_blocked":True,"results":results},ensure_ascii=False))
 if not all(r["ok"] for r in results):raise SystemExit(1)
 return
 from maestro_customer_entry import send_customer_help
 from maestro_customer_devices import send_device_menu,send_client_instructions,route_device_callback
 class Flow:
  login="fixture"
  async def delivery(self,client,mode="vpn"):return {"copy_url":"https://subscription.invalid/sub/fixture?format=links","url":"https://subscription.invalid/sub/fixture"}
 for topic in ("menu","payment","cdn","connection","subscription"):
  await case("help "+topic,lambda topic=topic:send_customer_help(Msg(),Flow(),topic))
 for mode in ("vpn","cdn"):
  await case("device menu "+mode,lambda mode=mode:send_device_menu(Msg(),mode))
  for platform in ("android","ios","desktop","tv"):
   await case(platform+" "+mode,lambda platform=platform,mode=mode:route_device_callback(CB("mc:device:"+platform+"-"+mode),Flow()))
  for client in ("karing","happ","incy","maestro"):
   await case(client+" "+mode,lambda client=client,mode=mode:send_client_instructions(Msg(),Flow(),client,mode))
 assert all(uid in (101,202) for uid,_,_ in sent)
 print(json.dumps({"isolated":True,"real_network_blocked":True,"results":results},ensure_ascii=False))
asyncio.run(run())
