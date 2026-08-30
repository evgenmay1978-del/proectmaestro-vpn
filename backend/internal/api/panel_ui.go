package api

// panelHTML is the self-contained web-admin SPA (one file: inline CSS + vanilla JS, no external
// CDN so it works under the strict CSP in panelApp). It talks to the /api/* endpoints under the
// same secret PanelPath, sending the session cookie + the X-CSRF header on writes. NB: no
// backtick template literals in the JS below — this whole string is a Go raw string.
const panelHTML = `<!doctype html>
<html lang="ru"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>MaestroVPN — Панель</title>
<style>
:root{--bg:#0e1116;--card:#171c24;--card2:#1e242e;--line:#2a323d;--fg:#e6edf3;--mut:#8b98a8;--acc:#46E05A;--accd:#2f9d40;--warn:#e0a23a;--err:#e0574a;--blue:#4aa3e0}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 -apple-system,Segoe UI,Roboto,Arial,sans-serif}
a{color:var(--blue)}
.wrap{max-width:1200px;margin:0 auto;padding:16px}
.hdr{display:flex;align-items:center;gap:14px;padding:14px 16px;border-bottom:1px solid var(--line);background:var(--card)}
.hdr b{font-size:16px}.hdr .sp{flex:1}
.tabs{display:flex;gap:6px;margin:14px 0}
.tab{padding:8px 14px;border-radius:8px;background:var(--card2);cursor:pointer;border:1px solid var(--line);color:var(--mut)}
.tab.on{color:var(--fg);border-color:var(--acc)}
.btn{padding:7px 12px;border-radius:7px;border:1px solid var(--line);background:var(--card2);color:var(--fg);cursor:pointer;font:inherit}
.btn:hover{border-color:var(--acc)}.btn.pri{background:var(--accd);border-color:var(--accd)}.btn.dng{border-color:var(--err);color:#f2b3ac}
.btn.sm{padding:4px 8px;font-size:12px}
input,select{padding:8px 10px;border-radius:7px;border:1px solid var(--line);background:#0b0e13;color:var(--fg);font:inherit}
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:12px;margin-bottom:16px}
.c{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:14px}
.c .n{font-size:26px;font-weight:700}.c .l{color:var(--mut);font-size:12px;text-transform:uppercase;letter-spacing:.04em}
table{width:100%;border-collapse:collapse;background:var(--card);border-radius:12px;overflow:hidden}
th,td{padding:9px 10px;text-align:left;border-bottom:1px solid var(--line);font-size:13px;white-space:nowrap}
th{color:var(--mut);font-weight:600;font-size:12px;text-transform:uppercase;cursor:pointer;user-select:none}
tr:hover td{background:#1b212b}
.badge{display:inline-block;padding:2px 8px;border-radius:20px;font-size:11px;font-weight:600}
.b-ok{background:rgba(70,224,90,.15);color:var(--acc)}.b-exp{background:rgba(224,87,74,.15);color:#f2b3ac}.b-off{background:rgba(139,152,168,.15);color:var(--mut)}.b-soon{background:rgba(224,162,58,.15);color:var(--warn)}
.proto{display:inline-block;background:#0b0e13;border:1px solid var(--line);border-radius:5px;padding:1px 6px;font-size:11px;margin-right:3px;color:var(--mut)}
.row-act{display:flex;gap:5px;flex-wrap:wrap}
.toolbar{display:flex;gap:10px;margin-bottom:12px;flex-wrap:wrap;align-items:center}
.toolbar .sp{flex:1}
.mut{color:var(--mut)}
.login{max-width:340px;margin:12vh auto;background:var(--card);border:1px solid var(--line);border-radius:14px;padding:26px}
.login h1{font-size:18px;margin:0 0 4px}.login p{color:var(--mut);margin:0 0 18px;font-size:13px}
.login input{width:100%;margin-bottom:12px}.login .btn{width:100%}
.err{color:#f2b3ac;font-size:13px;min-height:18px;margin-top:8px}
.modal{position:fixed;inset:0;background:rgba(0,0,0,.6);display:flex;align-items:center;justify-content:center;padding:16px;z-index:9}
.modal .box{background:var(--card);border:1px solid var(--line);border-radius:14px;padding:20px;max-width:520px;width:100%;max-height:86vh;overflow:auto}
.modal h3{margin:0 0 12px}
.kv{display:grid;grid-template-columns:120px 1fr;gap:4px 10px;font-size:13px}.kv .k{color:var(--mut)}
.field{display:flex;gap:8px;align-items:center;margin:8px 0;flex-wrap:wrap}
.field label{min-width:80px;color:var(--mut)}
.toast{position:fixed;bottom:18px;left:50%;transform:translateX(-50%);background:var(--card2);border:1px solid var(--acc);padding:10px 16px;border-radius:10px;z-index:20;max-width:90vw}
code{background:#0b0e13;padding:1px 5px;border-radius:4px;font-size:12px;word-break:break-all}
</style></head>
<body><div id="app"></div>
<script>
var BASE=location.pathname.replace(/\/?$/,'/');var CSRF=null,PERMS=[];
function esc(s){return String(s==null?'':s).replace(/[&<>"']/g,function(m){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[m];});}
function el(id){return document.getElementById(id);}
function toast(m){var t=document.createElement('div');t.className='toast';t.textContent=m;document.body.appendChild(t);setTimeout(function(){t.remove();},2600);}
function fmtBytes(n){n=+n||0;if(n<1024)return n+' Б';var u=['КБ','МБ','ГБ','ТБ'],i=-1;do{n/=1024;i++;}while(n>=1024&&i<u.length-1);return n.toFixed(n<10?1:0)+' '+u[i];}
function fmtAgo(s){if(!s)return '—';var d=(Date.now()-new Date(s))/1000;if(d<0)return 'только что';if(d<120)return 'только что';if(d<3600)return Math.floor(d/60)+' мин назад';if(d<86400)return Math.floor(d/3600)+' ч назад';return Math.floor(d/86400)+' дн назад';}
function isOnline(s){if(!s)return false;return (Date.now()-new Date(s))/1000 < 1200;}
function newIdempotencyKey(){if(window.crypto&&crypto.randomUUID)return crypto.randomUUID();return Date.now().toString(36)+'-'+Math.random().toString(36).slice(2)+'-'+Math.random().toString(36).slice(2);}
function api(path,opts){opts=opts||{};opts.credentials='same-origin';opts.headers=opts.headers||{};if(opts.body){opts.headers['Content-Type']='application/json';opts.method=opts.method||'POST';}var method=(opts.method||'GET').toUpperCase();if(CSRF)opts.headers['X-CSRF']=CSRF;if(method!=='GET'&&method!=='HEAD'&&!opts.headers['Idempotency-Key'])opts.headers['Idempotency-Key']=newIdempotencyKey();return fetch(BASE+path,opts).then(function(r){if(r.status===401){CSRF=null;PERMS=[];showLogin('Сессия истекла — войди снова');throw new Error('unauth');}return r.text().then(function(t){var j=null;try{j=t?JSON.parse(t):{};}catch(e){}if(!r.ok)throw new Error((j&&j.error)||t||('HTTP '+r.status));return j;});});}
function post(path,obj){return api(path,{body:JSON.stringify(obj||{})});}

function showLogin(msg){document.getElementById('app').innerHTML=
 '<div class="login"><h1>MaestroVPN</h1><p>Панель управления</p>'+
 '<input id="pw" type="password" placeholder="Пароль" autofocus>'+
 '<button class="btn pri" id="lg">Войти</button><div class="err" id="le">'+esc(msg||'')+'</div></div>';
 function go(){el('le').textContent='';post('api/login',{password:el('pw').value}).then(function(j){CSRF=j.csrf;return api('api/me');}).then(function(me){PERMS=me.permissions||[];showApp();}).catch(function(e){el('le').textContent=(e.message==='wrong password'?'Неверный пароль':e.message==='too many attempts — try later'?'Слишком много попыток, подожди':e.message);});}
 el('lg').onclick=go;el('pw').onkeydown=function(e){if(e.key==='Enter')go();};}

var TAB='dash',CUST=[],SORT={k:'expires',d:1},PAGE_LIMIT=50,CUST_REQ=0,OPS_REQ=0;
function can(p){return PERMS.indexOf(p)>=0;}
function canOwner(){return can('payment.decide')||can('settings.critical');}
function newPager(){return{cursor:'',back:[],next:'',page:1};}
function resetPager(p){p.cursor='';p.back=[];p.next='';p.page=1;}
function pagePath(path,p,params){var q=['limit='+PAGE_LIMIT],k;params=params||{};for(k in params)if(Object.prototype.hasOwnProperty.call(params,k))q.push(encodeURIComponent(k)+'='+encodeURIComponent(params[k]));if(p.cursor)q.push('cursor='+encodeURIComponent(p.cursor));return path+'?'+q.join('&');}
function pageNext(p,render){if(!p.next)return;p.back.push(p.cursor);p.cursor=p.next;p.next='';p.page++;render();}
function pagePrev(p,render){if(!p.back.length)return;p.cursor=p.back.pop();p.next='';p.page=Math.max(1,p.page-1);render();}
function pagerButtons(p,prefix,label){return '<div class="toolbar" style="margin-top:10px"><span class="mut">'+esc(label)+' · стр. '+p.page+'</span><span class="sp"></span><button class="btn sm" id="'+prefix+'_prev"'+(p.back.length?'':' disabled')+'>Назад</button><button class="btn sm" id="'+prefix+'_next"'+(p.next?'':' disabled')+'>Далее</button></div>';}
function bindPager(p,prefix,render){var prev=el(prefix+'_prev'),next=el(prefix+'_next');if(prev)prev.onclick=function(){pagePrev(p,render);};if(next)next.onclick=function(){pageNext(p,render);};}
var CUST_PAGE=newPager(),ORDER_CREATED_PAGE=newPager(),ORDER_CLAIMED_PAGE=newPager(),AUDIT_PAGE=newPager();
function showApp(){document.getElementById('app').innerHTML=
 '<div class="hdr"><b>MaestroVPN</b> <span class="mut">панель</span><span class="sp"></span>'+
 '<button class="btn sm" id="rf">Обновить</button> <button class="btn sm" id="pwb">Пароль</button> <button class="btn sm" id="lo">Выйти</button></div>'+
 '<div class="wrap"><div class="tabs">'+
 '<div class="tab" data-t="dash">Дашборд</div><div class="tab" data-t="cust">Клиенты</div>'+
 (canOwner()?'<div class="tab" data-t="ops">Операции</div>':'')+
 '<div class="tab" data-t="olc">olcRTC</div><div class="tab" data-t="wdtt">WDTT</div>'+
 '</div><div id="body"></div></div>';
 el('lo').onclick=function(){post('api/logout',{}).finally(function(){CSRF=null;PERMS=[];showLogin('Вы вышли');});};
 el('rf').onclick=function(){render();};
 el('pwb').onclick=changePwDlg;
 Array.prototype.forEach.call(document.querySelectorAll('.tab'),function(t){t.onclick=function(){TAB=t.getAttribute('data-t');render();};});
 render();}

function render(){Array.prototype.forEach.call(document.querySelectorAll('.tab'),function(t){t.classList.toggle('on',t.getAttribute('data-t')===TAB);});
 if(TAB==='dash')return renderDash();if(TAB==='cust')return renderCust();if(TAB==='ops')return renderOps();if(TAB==='olc')return renderOlc();if(TAB==='wdtt')return renderWDTT();}

function renderDash(){el('body').innerHTML='<div class="mut">Загрузка…</div>';api('api/stats').then(function(s){
 function card(n,l){return '<div class="c"><div class="n">'+n+'</div><div class="l">'+l+'</div></div>';}
 el('body').innerHTML='<div class="cards">'+card(s.total,'Всего')+card(s.active,'Активных')+card(s.expiring_7d,'Истекают ≤7д')+card(s.expired,'Истекли')+card(s.disabled,'Отключены')+card(s.devices,'Устройств')+'</div>'+
 '<p class="mut">Клиенты и действия — во вкладке «Клиенты». olcRTC-комнаты — во вкладке «olcRTC».</p>';
}).catch(function(e){el('body').innerHTML='<div class="err">'+esc(e.message)+'</div>';});}

function fmtDate(s){var d=new Date(s);if(isNaN(d))return '—';return d.toISOString().slice(0,10);}
function statusBadge(c){if(c.disabled)return '<span class="badge b-off">выкл</span>';if(!c.active)return '<span class="badge b-exp">истёк</span>';if(c.days_left<=7)return '<span class="badge b-soon">'+c.days_left+'д</span>';return '<span class="badge b-ok">'+c.days_left+'д</span>';}

function renderCust(){var seq=++CUST_REQ;el('body').innerHTML='<div class="mut">Загрузка…</div>';api(pagePath('api/customers',CUST_PAGE)).then(function(j){if(seq!==CUST_REQ)return;CUST=j.customers||[];CUST_PAGE.next=j.next_cursor||'';drawCust('');}).catch(function(e){if(seq===CUST_REQ)el('body').innerHTML='<div class="err">'+esc(e.message)+'</div>';});}
function drawCust(q){
 el('body').innerHTML='<div class="toolbar"><input id="q" placeholder="Поиск на текущей странице по логину…" style="min-width:260px" value="'+esc(q)+'"><span class="sp"></span>'+
 '<button class="btn dng" id="delexp">Обработать просроченных (вся база)</button> <button class="btn pri" id="add">+ Выдать клиента</button></div><div id="tbl"></div>';
 el('q').oninput=function(){drawCust(el('q').value);requestAnimationFrame(function(){var i=el('q');i.focus();i.setSelectionRange(i.value.length,i.value.length);});};
 el('add').onclick=provisionDlg;
 el('delexp').onclick=function(){if(!confirm('Запустить один глобальный пакет обработки просроченных клиентов (до 100 по всей базе)? Клиенты будут переведены в expired, а снятие доступа поставлено в очередь для всех настроенных целевых сервисов. Записи клиентов из базы не удаляются.'))return;post('api/action',{action:'delete_expired'}).then(function(j){toast('Обработано в этом запуске: '+(j.deleted||0));resetPager(CUST_PAGE);renderCust();}).catch(function(e){toast('Ошибка: '+e.message);});};
 var rows=CUST.filter(function(c){return !q||c.login.toLowerCase().indexOf(q.toLowerCase())>=0;});
 rows.sort(function(a,b){var k=SORT.k,va=a[k],vb=b[k];if(k==='expires'){va=+new Date(a.expires);vb=+new Date(b.expires);}if(va<vb)return -1*SORT.d;if(va>vb)return 1*SORT.d;return 0;});
 var h='<table><thead><tr>'+
  th('login','Логин')+th('expires','Истекает')+th('days_left','Осталось')+'<th>Статус</th><th>Активность</th><th>Устр.</th><th>Протоколы</th><th>Действия</th></tr></thead><tbody>';
 rows.forEach(function(c){h+='<tr>'+
  '<td><b>'+esc(c.login)+'</b></td>'+
  '<td>'+fmtDate(c.expires)+'</td>'+
  '<td>'+(c.days_left>3000?'∞':c.days_left)+'</td>'+
  '<td>'+statusBadge(c)+'</td>'+
  '<td>'+(isOnline(c.last_seen)?'<span class="badge b-ok">● в сети</span>':'<span class="mut">'+fmtAgo(c.last_seen)+'</span>')+'</td>'+
  '<td>'+c.devices+'</td>'+
  '<td>'+(c.protocols||[]).map(function(p){return '<span class="proto">'+esc(p)+'</span>';}).join('')+'</td>'+
  '<td><div class="row-act">'+
    '<button class="btn sm" data-a="ext30" data-l="'+esc(c.login)+'">+30д</button>'+
    '<button class="btn sm" data-a="more" data-l="'+esc(c.login)+'">…</button>'+
  '</div></td></tr>';});
 h+='</tbody></table><p class="mut">На текущей странице: '+rows.length+' из '+CUST.length+'.</p>'+pagerButtons(CUST_PAGE,'cust_page','Клиенты');
 el('tbl').innerHTML=h;
 bindPager(CUST_PAGE,'cust_page',renderCust);
 Array.prototype.forEach.call(document.querySelectorAll('#tbl th[data-k]'),function(t){t.onclick=function(){var k=t.getAttribute('data-k');if(SORT.k===k)SORT.d*=-1;else{SORT.k=k;SORT.d=1;}drawCust(q);};});
 Array.prototype.forEach.call(document.querySelectorAll('#tbl button[data-a]'),function(b){b.onclick=function(){var l=b.getAttribute('data-l'),a=b.getAttribute('data-a');if(a==='ext30')doAction({action:'extend',login:l,days:30},'Продлён '+l+' на 30д');else if(a==='more')custDlg(l);};});
}
function th(k,label){return '<th data-k="'+k+'">'+label+(SORT.k===k?(SORT.d>0?' ▲':' ▼'):'')+'</th>';}

function doAction(body,okMsg){return post('api/action',body).then(function(){toast(okMsg||'Готово');closeModal();resetPager(CUST_PAGE);renderCust();}).catch(function(e){toast('Ошибка: '+e.message);});}

function custDlg(login){api('api/customer?login='+encodeURIComponent(login)).then(function(j){var c=j.customer;var devs=j.device_ids||{};
 var dl=Object.keys(devs);
 modal('<h3>'+esc(login)+' '+statusBadge(c)+'</h3>'+
  '<div class="kv"><div class="k">Истекает</div><div>'+fmtDate(c.expires)+' ('+(c.days_left>3000?'∞':c.days_left+' дн.')+')</div>'+
  '<div class="k">Активность</div><div>'+(isOnline(c.last_seen)?'<span class="badge b-ok">● в сети</span>':esc(fmtAgo(c.last_seen)))+'</div>'+
  '<div class="k">Трафик</div><div>'+fmtBytes(j.traffic_bytes)+' <span class="mut">(VLESS)</span></div>'+
  '<div class="k">Протоколы</div><div>'+(c.protocols||[]).map(function(p){return '<span class="proto">'+esc(p)+'</span>';}).join('')+'</div>'+
  '<div class="k">Устройства</div><div>'+dl.length+' / '+(j.device_limit||'∞')+'</div></div>'+
  '<div class="field"><label>Продлить</label><input id="d_days" type="number" value="30" style="width:80px"> дней <button class="btn pri sm" id="d_ext">Продлить</button></div>'+
  '<div class="field"><label>Дата до</label><input id="d_date" type="date"> <button class="btn sm" id="d_set">Установить</button></div>'+
  '<div class="field"><label></label>'+
    '<button class="btn sm" id="d_rst">Сброс устройств</button>'+
    (c.disabled?'<button class="btn sm" id="d_en">Включить</button>':'<button class="btn dng sm" id="d_dis">Отключить</button>')+
  '</div>'+
  '<div class="field" style="border-top:1px solid var(--line);padding-top:10px;margin-top:12px"><label></label><button class="btn dng" id="d_del">Удалить клиента</button> <span class="mut">насовсем (панель + VLESS-узлы)</span></div>');
 el('d_ext').onclick=function(){doAction({action:'extend',login:login,days:+el('d_days').value||30},'Продлён на '+(el('d_days').value)+'д');};
 el('d_set').onclick=function(){if(!el('d_date').value)return;doAction({action:'set_expiry',login:login,expires:new Date(el('d_date').value+'T12:00:00Z').toISOString()},'Дата установлена');};
 el('d_rst').onclick=function(){doAction({action:'reset_devices',login:login},'Устройства сброшены');};
 if(el('d_dis'))el('d_dis').onclick=function(){doAction({action:'disable',login:login},login+' отключён');};
 if(el('d_en'))el('d_en').onclick=function(){doAction({action:'enable',login:login},login+' включён');};
 el('d_del').onclick=function(){if(!confirm('Удалить клиента '+login+' насовсем? Уберёт из панели и со всех настроенных целевых сервисов.'))return;post('api/action',{action:'delete',login:login}).then(function(){toast(login+' удалён');closeModal();renderCust();}).catch(function(e){toast('Ошибка: '+e.message);});};
}).catch(function(e){toast('Ошибка: '+e.message);});}

function changePwDlg(){modal('<h3>Смена пароля панели</h3>'+
 '<div class="field"><label>Текущий</label><input id="pw_cur" type="password" style="min-width:180px"></div>'+
 '<div class="field"><label>Новый</label><input id="pw_new" type="password" style="min-width:180px" placeholder="мин. 8 символов"></div>'+
 '<div class="field"><label>Ещё раз</label><input id="pw_new2" type="password" style="min-width:180px"></div>'+
 '<div class="field"><label></label><button class="btn pri" id="pw_go">Сменить</button></div>'+
 '<div class="err" id="pw_e"></div>');
 el('pw_go').onclick=function(){var a=el('pw_new').value;if(a.length<8){el('pw_e').textContent='Новый пароль слишком короткий (мин. 8)';return;}if(a!==el('pw_new2').value){el('pw_e').textContent='Пароли не совпадают';return;}post('api/password',{current:el('pw_cur').value,'new':a}).then(function(){toast('Пароль изменён');closeModal();}).catch(function(e){el('pw_e').textContent=(e.message==='current password is wrong'?'Текущий пароль неверный':e.message);});};}

function provisionDlg(){modal('<h3>Выдать нового клиента</h3>'+
 '<div class="field"><label>Логин</label><input id="p_login" placeholder="login" style="min-width:180px"></div>'+
 '<div class="field"><label>Дней</label><input id="p_days" type="number" value="30" style="width:100px"></div>'+
 '<div class="field"><label></label><button class="btn pri" id="p_go">Создать</button></div>'+
 '<p class="mut">Создаст аккаунт на всех узлах (VLESS/Hy2/Naive/AnyTLS/S3).</p>');
 el('p_go').onclick=function(){var l=el('p_login').value.trim();if(!l)return;doAction({action:'provision',login:l,days:+el('p_days').value||30},'Выдан '+l);};}

function tri(v){return v===true?'OK':v===false?'нет':'неизвестно';}
function metric(v){return v==null?'неизвестно':esc(v);}
function sectionError(title,item){return '<h3>'+esc(title)+'</h3><div class="err">'+esc(item&&item.error&&item.error.message||'Недоступно')+'</div>';}
function orderActions(o){var s=o.payment_state||o.status;if(s==='payment_claimed'||s==='claimed'||s==='awaiting_confirm')return '<button class="btn pri sm" data-confirm="'+esc(o.order_id)+'">Подтвердить</button> <button class="btn dng sm" data-cancel="'+esc(o.order_id)+'">Отменить</button>';if(s==='created'||s==='pending')return '<button class="btn dng sm" data-cancel="'+esc(o.order_id)+'">Отменить</button>';return '—';}
function orderSection(title,item,pager,prefix){if(!item)return '';if(!item.ok)return sectionError(title,item);var j=item.value||{},orders=j.orders||[];pager.next=j.next_cursor||'';var h='<h3>'+esc(title)+'</h3><table><thead><tr><th>ID</th><th>Код</th><th>Сумма</th><th>Статус</th><th>Действия</th></tr></thead><tbody>';orders.forEach(function(o){var state=o.payment_state||o.status;h+='<tr><td><code>'+esc(o.order_id)+'</code></td><td>'+esc(o.code)+'</td><td>'+esc(o.rub)+' ₽</td><td>'+esc(state)+'</td><td>'+orderActions(o)+'</td></tr>';});h+='</tbody></table>'+pagerButtons(pager,prefix,title);return h;}
function statusSection(item){if(!item)return '';if(!item.ok)return sectionError('Состояние сервиса',item);var s=item.value||{},rep=s.replication||{},nodes=s.nodes||{},apply=s.apply||{},outbox=s.outbox||{},telegram=s.telegram||{},dns=s.dns_tls||{},backup=s.backup||{},restore=s.restore||{},failures=s.failures||[];var h='<h3>Состояние сервиса</h3><div class="cards">'+
 '<div class="c"><div class="n">'+tri(s.quorum)+'</div><div class="l">Кворум</div></div>'+
 '<div class="c"><div class="n">'+tri(s.read_ready)+'</div><div class="l">Чтение</div></div>'+
 '<div class="c"><div class="n">'+metric(s.write_readiness)+'</div><div class="l">Запись</div></div>'+
 '<div class="c"><div class="n">'+tri(s.data_complete)+'</div><div class="l">Полнота данных</div></div></div>';
 h+='<div class="c"><div class="kv">'+
 '<div class="k">Replication</div><div>'+metric(rep.state)+' · узлов '+metric(rep.reachable_nodes)+' · lag '+metric(rep.max_lag_entries)+'</div>'+
 '<div class="k">Узлы</div><div>voters '+metric(nodes.enabled_voters)+'/'+metric(nodes.voters)+' · active '+metric(nodes.active_service_targets)+' · fenced '+metric(nodes.fenced_service_targets)+' · stale '+metric(nodes.stale_receipts)+'</div>'+
 '<div class="k">Apply</div><div>pending '+metric(apply.pending)+' · failed '+metric(apply.failed)+' · receipts '+metric(apply.failed_receipts)+' · lag '+metric(apply.max_generation_lag)+'</div>'+
 '<div class="k">Outbox</div><div>pending '+metric(outbox.pending)+' · failed '+metric(outbox.failed)+' · oldest '+metric(outbox.oldest_pending_age_seconds)+' сек.</div>'+
 '<div class="k">Telegram</div><div>routes '+metric(telegram.routes)+' · pollers '+metric(telegram.active_pollers)+' · rejected '+metric(telegram.inbox_rejected)+' · delivery failed '+metric(telegram.delivery_failed)+' · complete '+tri(telegram.data_complete)+'</div>'+
 '<div class="k">DNS/TLS</div><div>'+metric(dns.state)+' · targets '+metric(dns.targets)+' · complete '+tri(dns.data_complete)+'</div>'+
 '<div class="k">Backup</div><div>'+metric(backup.state)+' · generation '+metric(backup.verified_generation)+'/'+metric(backup.dirty_generation)+' · gap '+metric(backup.generation_gap)+'</div>'+
 '<div class="k">Restore</div><div>'+metric(restore.state)+' · epoch '+metric(restore.epoch)+' · complete '+tri(restore.data_complete)+'</div></div></div>';
 h+='<h4>Сводка отказов</h4>'+(failures.length?'<table><thead><tr><th>Компонент</th><th>Количество</th></tr></thead><tbody>'+failures.map(function(f){return '<tr><td>'+esc(f.component)+'</td><td>'+metric(f.count)+'</td></tr>';}).join('')+'</tbody></table>':'<p class="mut">Агрегированных отказов нет.</p>');return h;}
function auditSection(item){if(!item)return '';if(!item.ok)return sectionError('Последний аудит',item);var j=item.value||{},events=j.events||[];AUDIT_PAGE.next=j.next_cursor||'';var h='<h3>Последний аудит</h3><table><thead><tr><th>Время</th><th>Действие</th><th>Актор</th></tr></thead><tbody>';events.forEach(function(e){h+='<tr><td>'+esc(fmtDate(e.created_at))+'</td><td>'+esc(e.action)+'</td><td>'+esc(e.actor)+'</td></tr>';});return h+'</tbody></table>'+pagerButtons(AUDIT_PAGE,'audit_page','Аудит');}
function drawOps(data){var h=statusSection(data.status)+orderSection('Оплата заявлена',data.claimed,ORDER_CLAIMED_PAGE,'claimed_page')+orderSection('Ожидают оплаты',data.created,ORDER_CREATED_PAGE,'created_page')+auditSection(data.audit);if(!h)h='<p class="mut">Нет доступных операционных разделов.</p>';el('body').innerHTML=h;bindPager(ORDER_CLAIMED_PAGE,'claimed_page',renderOps);bindPager(ORDER_CREATED_PAGE,'created_page',renderOps);bindPager(AUDIT_PAGE,'audit_page',renderOps);Array.prototype.forEach.call(document.querySelectorAll('[data-confirm]'),function(b){b.onclick=function(){var id=b.getAttribute('data-confirm');if(!confirm('Подтвердить оплату '+id+'?'))return;post('api/order/confirm',{order_id:id}).then(function(){toast('Оплата подтверждена');resetPager(ORDER_CLAIMED_PAGE);resetPager(ORDER_CREATED_PAGE);renderOps();}).catch(function(e){toast('Ошибка: '+e.message);});};});Array.prototype.forEach.call(document.querySelectorAll('[data-cancel]'),function(b){b.onclick=function(){var id=b.getAttribute('data-cancel');if(!confirm('Отменить заказ '+id+'?'))return;post('api/order/cancel',{order_id:id}).then(function(){toast('Заказ отменён');resetPager(ORDER_CLAIMED_PAGE);resetPager(ORDER_CREATED_PAGE);renderOps();}).catch(function(e){toast('Ошибка: '+e.message);});};});}
function renderOps(){var seq=++OPS_REQ,reqs=[];el('body').innerHTML='<div class="mut">Загрузка…</div>';if(can('payment.decide')){reqs.push({key:'claimed',p:api(pagePath('api/orders',ORDER_CLAIMED_PAGE,{status:'payment_claimed'}))});reqs.push({key:'created',p:api(pagePath('api/orders',ORDER_CREATED_PAGE,{status:'created'}))});}if(can('settings.critical')){reqs.push({key:'status',p:api('api/cluster-status')});reqs.push({key:'audit',p:api(pagePath('api/audit',AUDIT_PAGE))});}Promise.allSettled(reqs.map(function(r){return r.p;})).then(function(results){if(seq!==OPS_REQ)return;var data={};results.forEach(function(result,index){data[reqs[index].key]=result.status==='fulfilled'?{ok:true,value:result.value}:{ok:false,error:result.reason};});drawOps(data);});}

function renderOlc(){el('body').innerHTML='<div class="mut">Загрузка…</div>';api('api/olcrtc').then(function(o){
 if(!o.enabled){el('body').innerHTML='<p class="mut">olcRTC выключен.</p>';return;}
 var rooms=o.rooms||{};var wbset=o.wb_token_set;
 var h='<p class="mut">Провайдер по умолчанию '+esc(o.provider)+' · транспорт '+esc(o.transport)+'</p>'+
  '<div class="toolbar" style="margin:8px 0"><b>WbStream токен:</b> '+(wbset?'<span class="badge b-ok">задан</span>':'<span class="badge b-exp">не задан</span>')+
   '<input id="o_wbtok" type="password" placeholder="account-токен eyJ… (один раз)" style="flex:1;min-width:220px"><button class="btn" id="o_wbtokb">Сохранить токен</button></div>'+
  '<h3 style="margin:6px 0">Клиенты с доступом к olcRTC</h3>'+
  '<table><thead><tr><th>Логин</th><th>Комната</th><th>Провайдер</th><th>Exit-сервер</th><th></th></tr></thead><tbody>';
 var he=o.health||{};
 function exitCell(lg,hasRoom){if(!hasRoom)return '<span class="mut">—</span>';var s=he[lg];if(!s)return '<span class="mut">?</span>';if(s.healthy)return '<span class="badge b-ok">● живой</span>';if(s.active==="active")return '<span class="badge b-soon">не в комнате</span>';return '<span class="badge b-exp">● мёртв</span>';}
 h+='<tr><td class="mut">(общая)</td><td><code>'+esc(o.global_room||'—')+'</code></td><td class="mut">fallback</td><td></td><td></td></tr>';
 (o.logins||[]).forEach(function(lg){var r=rooms[lg];var pv=(r&&r.provider)?r.provider:o.provider;h+='<tr><td><b>'+esc(lg)+'</b></td><td><code>'+esc(r?r.room:'(нет комнаты)')+'</code></td><td>'+esc(pv)+(r?'':' <span class="badge b-off">заперт</span>')+'</td><td>'+exitCell(lg,!!r)+'</td><td><button class="btn sm" data-wbnew="'+esc(lg)+'" title="создать свежую WbStream-комнату и назначить">новая WbStream</button> <button class="btn dng sm" data-rm="'+esc(lg)+'">убрать</button></td></tr>';});
 h+='</tbody></table>'+
  '<div class="toolbar" style="margin-top:10px"><input id="o_add" placeholder="логин клиента" style="min-width:180px"><button class="btn pri" id="o_addb">+ Добавить клиента в olcRTC</button><span class="mut">даёт доступ; комнату назначь ниже</span></div>'+
  '<h3 style="margin:14px 0 6px">Назначить комнату вручную</h3>'+
  '<div class="toolbar"><select id="o_login"><option value="">(общая комната)</option>'+(o.logins||[]).map(function(lg){return '<option>'+esc(lg)+'</option>';}).join('')+'</select>'+
  '<select id="o_prov"><option value="telemost">Telemost (ссылка)</option><option value="wbstream">WbStream (id комнаты)</option></select>'+
  '<input id="o_url" placeholder="https://telemost.yandex.ru/j/…  или  wbstream room id" style="flex:1;min-width:240px">'+
  '<button class="btn pri" id="o_set">Назначить</button></div>'+
  '<p class="mut">Telemost: создай комнату в Яндекс.Телемосте, вставь ссылку. WbStream: проще нажми «новая WbStream» у клиента — панель создаст комнату сама (нужен токен выше). Отдельный exit поднимается ~15 сек.</p>';
 el('body').innerHTML=h;
 el('o_addb').onclick=function(){var l=el('o_add').value.trim();if(!l)return;post('api/olcrtc/login',{login:l,action:'add'}).then(function(){toast(l+' добавлен в olcRTC');renderOlc();}).catch(function(e){toast('Ошибка: '+e.message);});};
 Array.prototype.forEach.call(document.querySelectorAll('#body button[data-rm]'),function(b){b.onclick=function(){var l=b.getAttribute('data-rm');if(!confirm('Убрать '+l+' из olcRTC? (доступ + своя комната)'))return;post('api/olcrtc/login',{login:l,action:'remove'}).then(function(){toast(l+' убран');renderOlc();}).catch(function(e){toast('Ошибка: '+e.message);});};});
 el('o_set').onclick=function(){var u=el('o_url').value.trim();if(!u)return;toast('Назначаю комнату…');post('api/olcrtc/room',{login:el('o_login').value,room:u,provider:el('o_prov').value}).then(function(){toast('Комната назначена');renderOlc();}).catch(function(e){toast('Ошибка: '+e.message);});};
 el('o_wbtokb').onclick=function(){var t=el('o_wbtok').value.trim();if(!t)return;post('api/olcrtc/wbtoken',{token:t}).then(function(){toast('WbStream токен сохранён');renderOlc();}).catch(function(e){toast('Ошибка: '+e.message);});};
 Array.prototype.forEach.call(document.querySelectorAll('#body button[data-wbnew]'),function(b){b.onclick=function(){var l=b.getAttribute('data-wbnew');if(!confirm('Создать новую WbStream-комнату для '+l+' и назначить?'))return;toast('Создаю комнату…');post('api/olcrtc/wbroom',{login:l}).then(function(r){toast('Новая комната назначена');renderOlc();}).catch(function(e){toast('Ошибка: '+e.message);});};});
}).catch(function(e){el('body').innerHTML='<div class="err">'+esc(e.message)+'</div>';});}

function renderWDTT(){el('body').innerHTML='<div class="mut">Загрузка…</div>';api('api/vkturn').then(function(o){
 var logins=o.logins||[];var clients=o.clients||{};var configured=!!o.configured;var enabled=!!o.enabled;
 var badge=!configured?'<span class="badge b-off">не настроен</span>':(enabled?'<span class="badge b-ok">включён</span>':'<span class="badge b-exp">выключен</span>');
 var h='<div class="toolbar" style="margin:8px 0"><b>WDTT / VK-TURN</b> '+badge+'<span class="sp"></span>'+
   (configured?'<button class="btn '+(enabled?'dng':'pri')+'" id="w_toggle">'+(enabled?'Выключить':'Включить')+'</button>':'')+'</div>'+
   '<p class="mut">Мобильный обходной транспорт (WireGuard поверх VK-TURN). Только для '+esc(logins.join(', '))+'. Секреты не показываются — оставь поле пустым, чтобы не менять.</p>'+
   '<div class="field"><label>Мин. версия</label><input id="w_mvc" type="number" value="'+esc(o.min_version_code||'')+'" style="width:130px" placeholder="напр. 90181"></div>'+
   '<div class="field"><label>Сервер</label><input id="w_server" value="'+esc(o.server||'')+'" placeholder="host:port (DTLS, напр. 56000)" style="min-width:260px"></div>'+
   '<div class="field"><label>VK-хеши</label><input id="w_hashes" value="'+esc((o.vk_hashes||[]).join(', '))+'" placeholder="хеш1, хеш2 (1..4, через запятую)" style="flex:1;min-width:280px"></div>';
 logins.forEach(function(lg){var c=clients[lg]||{};var wg=c.wg||{};
   h+='<div style="border-top:1px solid var(--line);margin-top:10px;padding-top:8px"><b>'+esc(lg)+'</b>'+
     '<div class="field"><label>Пароль</label><input id="w_pw_'+esc(lg)+'" type="password" placeholder="'+(c.password_set?'•••• задан — пусто = не менять':'задать пароль (8..128)')+'" style="min-width:220px"></div>'+
     '<div class="field"><label>WG приватный</label><input id="w_pk_'+esc(lg)+'" type="password" placeholder="'+(wg.private_key_set?'•••• задан — пусто = не менять':'32-байт base64')+'" style="min-width:220px"></div>'+
     '<div class="field"><label>WG сервер-ключ</label><input id="w_pub_'+esc(lg)+'" value="'+esc(wg.peer_public_key||'')+'" placeholder="peer public key (base64)" style="min-width:220px"></div>'+
     '<div class="field"><label>Адрес</label><input id="w_addr_'+esc(lg)+'" value="'+esc(wg.local_address||'')+'" placeholder="10.66.66.2/32" style="width:170px"></div>'+
   '</div>';
 });
 h+='<div class="field" style="margin-top:12px"><label></label><button class="btn pri" id="w_save">Сохранить WDTT</button> <span class="mut">валидируется перед записью; включение — отдельной кнопкой</span></div>';
 el('body').innerHTML=h;
 if(el('w_toggle'))el('w_toggle').onclick=function(){post('api/vkturn/enabled',{enabled:!enabled}).then(function(){toast(enabled?'WDTT выключен':'WDTT включён');renderWDTT();}).catch(function(e){toast('Ошибка: '+e.message);});};
 el('w_save').onclick=function(){
   var hashes=el('w_hashes').value.split(',').map(function(s){return s.trim();}).filter(Boolean);
   var cl={};logins.forEach(function(lg){cl[lg]={password:el('w_pw_'+lg).value,wg:{private_key:el('w_pk_'+lg).value,peer_public_key:el('w_pub_'+lg).value.trim(),local_address:el('w_addr_'+lg).value.trim()}};});
   post('api/vkturn',{min_version_code:+el('w_mvc').value||0,server:el('w_server').value.trim(),vk_hashes:hashes,clients:cl}).then(function(){toast('WDTT сохранён');renderWDTT();}).catch(function(e){toast('Ошибка: '+e.message);});
 };
}).catch(function(e){el('body').innerHTML='<div class="err">'+esc(e.message)+'</div>';});}

function modal(html){closeModal();var m=document.createElement('div');m.className='modal';m.id='modal';m.innerHTML='<div class="box">'+html+'<div style="text-align:right;margin-top:14px"><button class="btn" id="mclose">Закрыть</button></div></div>';document.body.appendChild(m);el('mclose').onclick=closeModal;m.onclick=function(e){if(e.target===m)closeModal();};}
function closeModal(){var m=el('modal');if(m)m.remove();}

api('api/me').then(function(j){if(j.logged_in){CSRF=j.csrf;PERMS=j.permissions||[];showApp();}else showLogin();}).catch(function(){showLogin();});
</script></body></html>`
