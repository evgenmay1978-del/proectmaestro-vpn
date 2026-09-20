# ИНЦИДЕНТ 19.09.2026: панель не поднимается (rqlite + раздутая БД)
> ✅ **ИНЦИДЕНТ ЗАКРЫТ 19.09.2026 ~20:45 UTC.** Фактически выполненное лечение, цифры и ловушки —
> в `docs/handoff/POST-INCIDENT-2026-09-19-rqlite.md`. Runbook из раздела 3 в лоб не работает
> (immutable-триггеры, FK на ledger, clean-snapshot-проверка rqlite v10) — см. раздел 2 пост-инцидента.


> Состояние на 19.09.2026 ~19:25 UTC. **Подписки отдают 502**, VPN-ноды работают.
> Этот файл — точка входа для продолжения. Читать вместе с `HANDOFF-2026-09-19.md` и `README.md`.

## 1. Что произошло
1. База rqlite (на ней держится панель коммерции `maestro-cdn-controller`, порт 18910) выросла до **~1.5 ГБ**:
   - `whitelist_metering_events` — **665 МБ** (169 903 строки, из них 402 МБ — payload `result_json`);
   - `idempotency_requests` — 202 МБ; `whitelist_commercial_debit_outbox` — 83 МБ;
     `whitelist_commercial_metering_sources` — 73 МБ; `external_actions` — 51 МБ; индексы по 20–50 МБ.
2. На старте панель выполняет проверку схемы, в т.ч. `PRAGMA foreign_key_check` по всей базе.
   На раздутой базе она занимает **~120 секунд** (проверено: 118 785 мс) — клиент rqlite у панели отваливается
   по таймауту → в логе `build rqlite runtime … verify voter foreign keys: rqlite: request transport failed`,
   панель уходит в цикл ретраев и **не слушает 18910** → `/sub/` и `/cdn-sub/` через nginx = **502**.
3. Дополнительно кластер rqlite «икает»: перезапуск узла = восстановление из снапшота (~26 с), в это время
   heartbeat/ReadIndex не проходят → выборы (доходило до term 98–99). В норме strong-чтения 165–205 мс,
   в момент передёра — таймауты 10–30 с.

## 2. Что уже сделано в этом инциденте (не откатывать бездумно)
- Остановлен таймер списаний: `systemctl stop maestro-flat-charge.timer`
  (**ВКЛЮЧИТЬ ОБРАТНО после починки:** `systemctl start maestro-flat-charge.timer`, юнит остаётся enabled).
- Перезапущены rqlite на S2 и S3; сейчас **лидер S2** (85.137.166.237), term 99.
- В `/etc/maestro-cdn-controller/runtime.env` порядок эндпоинтов изменён на «S2 первым»
  (`MAESTRO_RQLITE_ENDPOINTS="https://85.137.166.237:4001,https://46.30.42.151:4001"`), бэкап `/root/runtime.env.before-order-*`.
  (Библиотека панели, похоже, НЕ делает failover между эндпоинтами — поэтому первым должен быть лидер.)
- На S1 узел ES переведён на быстрый REALITY-dest: `dest=www.cloudflare.com:443`, `serverNames=[www.intel.com, www.cloudflare.com]`
  (старые клиенты со SNI intel продолжают работать), обновлены `/etc/maestro-panel.env`, runtime.env и
  `/var/lib/maestro/customers.json` (бэкапы `*.before-dest-*`). Цель — убрать ~200 мс с хендшейка ES.
- Попытка «облегчить» события (`update … set result_json=''` чанками) **не сработала**: 0 строк, один запрос ~249 с.
  Ничего не изменилось, вреда нет.

## 3. ЧТО ДЕЛАТЬ ДАЛЬШЕ (по порядку)
### Шаг 1. Уменьшить базу (главное)
- Быстрый путь (рекомендуется, но требует остановки rqlite): на узле-лидере остановить `maestro-cdn-rqlite-s2`,
  выполнить напрямую в SQLite и запустить обратно:
  ```bash
  systemctl stop maestro-cdn-rqlite-s2
  cd /var/lib/maestro-cdn-rqlite/s2
  sqlite3 db.sqlite "DELETE FROM whitelist_metering_events WHERE created_at_unix < strftime('%s','now')-5*86400;
                     DELETE FROM idempotency_requests WHERE created_at < strftime('%s','now')-5*86400;"  # проверить имя колонки!
  sqlite3 db.sqlite "VACUUM;"
  systemctl start maestro-cdn-rqlite-s2
  ```
  ⚠️ В кластере 2 voter'а (S2+S3) → остановка лидера ломает кворум. Варианты: (а) делать в окне, когда панель и так лежит;
  (б) сначала поднять S3 лидером и останавливать S2; (в) сделать третий voter на S1 (см. шаг 3) и тогда останавливать любой узел безопасно.
  ⚠️ Перед удалением **снять копию** `db.sqlite` (места на S2: 11 ГБ свободно — хватит).
- Путь через rqlite (без остановки): `POST /db/execute` c `["DELETE FROM whitelist_metering_events WHERE created_at_unix < …"]`
  крупными порциями, затем `["VACUUM"]`. Каждая порция — отдельная запись в raft, будет медленно (минуты).
- Цель: `foreign_key_check` < 2 c, файл базы < 400 МБ.

### Шаг 2. Поднять панель и проверить
```bash
systemctl restart maestro-cdn-controller
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18910/healthz     # ждём 200
curl -s -o /dev/null -w '%{http_code}\n' "http://127.0.0.1:18910/sub/<token>?format=xray"
curl -s -o /dev/null -w '%{http_code}\n' "http://127.0.0.1:18910/cdn-sub/<token>"
curl -s -o /dev/null -w '%{http_code}\n' "https://wapmixx.ru:8911/sub/<token>?format=xray"   # публично
systemctl start maestro-flat-charge.timer        # ВЕРНУТЬ СПИСАНИЯ
```

### Шаг 3. Чтобы не повторилось
1. **Retention-джоб** для `whitelist_metering_events` и `idempotency_requests` (например, хранить 7–14 дней) —
   отдельный таймер на S1, пишет через rqlite чанками вне пиков.
2. **Третий voter rqlite на S1** (кворум 2 из 3) — панель перестанет зависеть от одного медленного узла,
   а чтения можно будет делать локально через `127.0.0.1:4001`. Ключи CA лежат на S1:
   `/var/lib/maestro-cdn-rqlite-bootstrap/pki/{http-ca,raft-ca}.key` — сгенерировать `s1-*` сертификаты и запустить rqlited.
3. **Бэкап базы rqlite** (снапшот `/var/lib/maestro-cdn-rqlite/*/db.sqlite` + raft) на внешний носитель/в S3-совместимое хранилище.
4. Мониторинг: алерт, если `foreign_key_check` > 5 c или размер базы > 800 МБ.

## 4. Полезные факты
- Панель: `maestro-cdn-controller.service` (билд 87fe24eb), слушает 127.0.0.1:18910; legacy-панель `maestro-panel` — 8910.
- rqlite: S2 `85.137.166.237:4001/4002` (лидер), S3 `46.30.42.151:4001/4002`; клиентские сертификаты S1 —
  `/var/lib/maestro-cdn-rqlite-bootstrap/pki/s1-controller.{crt,key}`, CA — `http-ca.crt`.
  На S3 стоит drop-in `90-cdn-recovery.conf` (`-raft-non-voter=false -join=85.137.166.237:4002 -bootstrap-expect=0`).
- Быстрые локальные чтения: `/db/query?level=none` (0.2 c) — использовать для диагностики, чтобы не ждать strong.
- Слабые чтения strong падают в моменты передёра — это симптом, а не причина.
- VPN-ноды (443/8443/2096, CDN-цепочка) от этого инцидента не зависят и работают.
