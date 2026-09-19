# ПОСТ-ИНЦИДЕНТ 19.09.2026: панель коммерции и раздутая БД rqlite — устранено

> ✅ Инцидент закрыт 19.09.2026 ~20:45 UTC. Панель работает, подписки отдаются, списания идут.
> Этот файл описывает, что реально сделано, и почему runbook из `INCIDENT-2026-09-19-rqlite.md` в лоб
> не работает. Читать вместе с `HANDOFF-2026-09-19.md` и `README.md`.

## 1. Подтверждённая причина
- Панель `maestro-cdn-controller` (127.0.0.1:18910) 44 раза не могла собрать rqlite-runtime:
  `build rqlite runtime … verify voter foreign keys / verify schema: rqlite: request transport failed`.
  Клиентский таймаут панели — **15 с** (`backend/internal/rqlite/client.go`, `defaultTimeout`).
- База rqlite выросла до ~1.5 ГБ; `PRAGMA foreign_key_check` через API — **11 751 мс**, на холодном
  кэше и при догоняющем снапшоте заметно дольше; raft-commit периодически уходил в 20+ с.
- Итог: старт не укладывался в 15 с → порт 18910 закрыт → nginx на 8911 отдавал **502** на `/sub/`
  и `/cdn-sub/`. VPN-ноды (443/8443/2096) от rqlite не зависят.

## 2. Почему runbook INCIDENT в лоб не работает (проверено на копии)
1. Колонка в `idempotency_requests` — `created_at_unix`, а не `created_at`.
2. На 5 таблицах стоят триггеры «immutable delete»: `whitelist_metering_events`,
   `whitelist_metering_intervals`, `whitelist_commercial_metering_sources`,
   `whitelist_commercial_debit_outbox`, `whitelist_usage_applications` — обычный `DELETE` падает.
3. FK `ON DELETE RESTRICT`: intervals→events, sources→events, outbox→sources,
   usage_applications→intervals и **balance_entries→intervals**. Интервалы, на которые ссылается
   финансовый ledger (`whitelist_balance_entries`, 4 010 записей), удалять нельзя.
4. Офлайн-правка `db.sqlite` на остановленном узле rqlite v10 не выживает: при старте сверяется
   fingerprint файла с `clean_snapshot`; при несовпадении делается full restore из `wsnapshots`
   (удалённое вернётся), а CRC-проверка может уронить узел.
5. `VACUUM` через `/db/execute` не предусмотрен; `/db/load` репликуется через raft, но на S2
   ~0.9 ГБ свободной RAM — грузить 0.4–0.7 ГБ одной raft-командой рискованно. Штатный путь — `-auto-vacuum-int`.

## 3. Что сделано (с доказательствами)
1. **Бэкап:** `GET /db/backup` с лидера → `/root/backups/maestro-rqlite-20260919T200552Z.sqlite`
   (1 513 508 864 Б, sha256 `3bee72cd…adc6a`) на S1.
2. **Заморозка писателей:** остановлены `maestro-cdn-controller` и `maestro-flat-cdn-sync.timer`.
3. **Чистка через rqlite (реплицируется на оба voter'а, кворум не терялся), cut-off 3 суток.**
   Сняты 5 immutable-delete триггеров; удалены outbox → sources → intervals → events (только события без
   ссылок из `whitelist_balance_entries`/`whitelist_usage_applications`) и `idempotency_requests`
   старше 3 суток; триггеры возвращены дословно. Удалено по 134 037 строк из 4 таблиц событий и 155 171
   строка идемпотентности. Осталось: events 35 704, balance_entries 4 010 (не тронуты), usage_applications 4 000.
4. **Восстановление квитанций:** чистка удалила старые квитанции `whitelist-balance/apply-usage`, но
   outbox-записи защищённых ledger-интервалов остались → флаг `commercial_debit_pending` отвергал
   списания с 409. Из бэкапа восстановлена **3 441 квитанция**; orphans = 0.
5. **Компакция:** drop-in `95-auto-vacuum.conf` на S2/S3, `-auto-vacuum-int=1m` для разового
   VACUUM, затем **24h**.
6. Панель поднята, таймер списаний возвращён, проводки проверены.

## 4. Цифры до/после
| Метрика | До | После |
|---|---|---|
| `db.sqlite` (S2 / S3) | 1 513 504 768 / 1 513 426 944 Б | 386 994 176 / 386 994 176 Б |
| `whitelist_metering_events` | 169 903 | 35 704 |
| `idempotency_requests` | 190 309 | ~38 400 |
| `foreign_key_check` через API | 11 751 мс | 483 мс |
| `wsnapshots` на S2 | 1.5 ГБ | ~370 МБ |
| `/healthz` 18910 | не слушал (000) | 200 |
| публичный `/sub/<token>` | 502 | 200 |

## 5. Что осталось (вне шагов 1–3)
1. Retention-джоб и мониторинг: прирост ~70 МБ/сутки; алерт при `foreign_key_check` > 5 с или базе
   > 800 МБ. Учитывать immutable-триггеры и FK на ledger/квитанции (иначе снова 409).
2. Третий voter rqlite (см. `README.md`, дело `rqlite-third-node-drift`).
3. `/sub/<неизвестный токен>` паникует в `subscription_snapshot.go:67` (срез пустого HMAC) → 502
   вместо 404.
4. Поправить runbook INCIDENT по пунктам раздела 2.

## 6. Ловушки для следующей сессии
- Не править `db.sqlite` при остановленном rqlite v10 — откатится на clean-snapshot-проверке.
- Чистка обязана удалять квитанции `whitelist-balance/apply-usage` согласованно с outbox, иначе
  `commercial_debit_pending` заблокирует списания.
- `/sub/<unknown>` = panic → 502; это не падение панели, а баг обработчика.
- Одиночный рестарт rqlite при двух voter'ах роняет strong-чтения — панель уходит в ретраи.
