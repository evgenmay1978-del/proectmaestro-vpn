Актуальное состояние: переработка установлена; результаты, сохранённые границы и откат в ROLLOUT_2026-09-11.md. Исторические формулировки о предстоящей реализации ниже не являются текущим статусом.

# Минимальная карта реализации ботов — 11.09.2026

План, не выполненные изменения. Новый сервис, биллинг или миграция пользователей не нужны.

## Источники
S1 @MaestroSecureVPN_bot: vpnbot.service, /root/vpn_bot/main.py; каталог неGit.
S2 @MaestroSecureNaive_bot: vpn_bot.service, /opt/vpn_bot/bot_minimal.py. HEADfd89204f945c0725a9ab11564765ba929efc1f7f не отражает live: entry и mc-модули untracked.

Canonical deploy:
- vpn_bot_maestro_customer_entry.py → S1 handlers/maestro_customer_entry.py, S2 maestro_customer_entry.py. with_customer_entry/send_customer_dashboard/cabinet_keyboard/customer_balance_text: один главный экран, срокVPN иCDN отдельно.
- vpn_bot_maestro_customer.py → maestro_customer.py обоихботов (SHA совпадает). resolve_customer_callback/legacy_customer_logins/CustomerFlow.delivery/customer_action: известныйTelegram-клиент без второго входа, существующие open_url иcopy_url.
- vpn_bot_maestro_customer_cdn.py → S1 совпадает, S2 устарел. catalog/purchase/paid/decide/dispatch: актуальный каталог, старыеproduct_id/заказы совместимы, выключение продаж не блокирует подтверждения.
- vpn_bot_maestro_order_actions.py → live maestro_customer_cdn_actions.py. Сохранить order_id, проверкуадмина иидемпотентность.
- vpn_bot_maestro_orders.py → S1 handlers/maestro_orders.py. LiveSHA отличается: сначала сохранить, не затирать.

Перед правкой сохранить root-only точные live-only исходники:
S1 main.py, handlers/client.py, handlers/admin.py, keyboards.py, formatters.py, states.py, api_3xui.py, subscription_links.py, handlers/maestro_orders.py.
S2 bot_minimal.py, subscription_links.py.
Затем чистые исходники/изменения зарегистрировать в canonical deploy безсекретов. s2_multiproto_patch.py — исторический патчер, не нынешний код.

## Клиентская логика
Убрать два домашних экрана и противоречащие привязки. Использовать существующие S1 users.is_bound/client_email и S2 binds.proxy_user/db_get_bind.
S1 _user_status связывает активность с округлённым вниз days_left>0: последние24ч могут считаться неактивными. Сравнивать точный expiry/enable; дни — только отображение.
S1 обычный статус: api.get_client_by_email(enable,expiry_time), ссылкаMaestro с существующим fallback.
S2 обычный статус: panel.find(expiresAt), существующийdays_left. client_link возвращает сохранённую multi-protocol URL либо naive+https; Naive-only не отправлять в общийCDN HAPP/INCY импорт.
Отказ API/CDN/зеркала не означает отсутствие оплаченной обычнойподписки. Статусы показывать независимо.
Подключение: доставка изсуществующихдескрипторов, кнопка открытия и копирования, инструкция для выбранного приложения.
Цены сохранить: S1CDN1/5/10/25/50ГБ сновымиID, S2 ещё5/20/50/100ГБ состарымиID. Синхронизировать выдачу каталога, сохраняя старые заказы.

## Оплата и администратор
S1 admin_approve → _maestro_renew → /admin/renew. admin_client_do_extend:231 сейчас вызывает api.extend_client/bulkAdjust иигнорируетbool. Ручные дни направить черезтотжепроверяемыйпуть, чтооплата.
S2 сохранить panel_renew → db_set_sub → maestro_sync_expiry. db_set_sub сохраняет sub_url и зеркалит абсолютныйexpiresAt через /admin/set-expiry для известногоMaestrologin. НЕ добавлять второй относительный /admin/renew.
НовыеS2 approve/reject — по конкретномуpayment_id. Старыйapprove:tg_id:username:days помечает всеpending tg_id+username: совместимыйразбор не должен подтверждатьнеоднозначныйнабор.
Сохранить восстановление Naive-доступа приошибкеdelete+add.
Админка: очередь конкретныхзаявок; поиск/списокклиентов; однакарточка сдатойVPN,CDNГБ,оплатами и действиями добавитьдни/установитьдату/добавитьГБ.

## API
Уже есть GET /sub/<token>/info, GET /admin/customer?login=, GET /account/whitelist-balance, /order/catalog, /order/tariffs, /order, /order/<id>/paid-claim, POST /admin/order/<id>/confirm/reject сIdempotency-Key, POST /account/subscription-delivery(open_url/URL/copy_url), /admin/renew, /admin/set-expiry.
Нужны узкие Bearer-admin handlers существующих ListCustomers/CustomerByLogin и CreditWhiteListManualGB снынешнимиactor/command/idempotency проверками. Panel-cookie/CSRF не переносить вбот.
Файлыbackend/internal/api: controlplane_port.go, controlplane_public_admin.go, controlplane_panel_whitelist.go. Бизнес-метод backend/internal/controlplane/whitelist_admin_credit.go уже есть, учёт не переписывать.

## Сохранение и установка
Сохранить S1 client:paid,admin:approve/reject,moconf/mocancel,aclsub; S2 paid/approve/reject/bind; CDNmc:paid/cf/cr, pending, привязки,сроки/ГБ.
Backup: изменяемыеPython,unit/drop-in,старыйbackendbinary. Затронутыйbackend собирать тольконаGitHub; на ноутбуке не собирать. Новыхтестов/CI автоматически не запускать.
Откат кода — толькофайлы/затронутыеслужбы. КлиентскуюБД не откатывать: потеряютсяновыеоплаты.
App/OTA/обычныеVPN/Xray/ingress,тарифы иреальныебалансы не менятьрадиинтерфейса.
