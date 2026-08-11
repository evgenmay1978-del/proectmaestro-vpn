# MaestroVPN backlog after production restoration

Обновлено: **2026-08-11**. Пункты ниже не считаются реализованными. Перед каждым
этапом нужен отдельный design/implementation plan, RED/GREEN checks, rollback и
явное подтверждение production mutation.

## P0 — сохранить восстановленную работу

- Не переустанавливать и не «выравнивать» работающие S1–S4 без доказанного
  дефекта. Сначала read-only audit и backup.
- Проверить ручное подтверждение оплаты на тестовой fixture/копии базы:
  повторная кнопка и повторное admin-confirm дают ровно одно продление и одну
  durable operation. Реальный платёж клиента ради теста не менять.
- Запланировать координированную ротацию секретов и очистку Git history двух
  bot repositories; не выполнять это одновременно с HA-миграцией.

## P1 — WDTT в mobile 1.0.153/1.0.154

Наблюдение владельца: в установленной **1.0.154** WDTT не отображается для
одного из трёх разрешённых owner aliases. Состояние **1.0.153** неизвестно.
Проверить все три aliases, но не коммитить их имена или subscription payload.

Диагностика по порядку:

1. Read-only зафиксировать server feature flag, allowlist membership и набор
   профилей, который Maestro выдаёт каждому разрешённому alias.
2. Сравнить exact payload и mobile protocol filtering/order в 1.0.153 и
   1.0.154; отделить «профиль не выдан» от «выдан, но UI скрыл».
3. Проверить, что WDTT не выдаётся обычному клиенту и TV не изменяется.
4. Только после локального/CI RED воспроизведения исправлять первый доказанный
   boundary. S1 WDTT сейчас `enabled=false`, поэтому включение — отдельный gate.

## P1 — mobile protocol arc и недостающие протоколы

Не уменьшать текст/ячейки, чтобы втиснуть всё. Целевое направление: phone-only
scrollable **protocol arc** с горизонтальным swipe по дуге, snap к ячейкам,
частично видимым соседним сектором/стрелкой как affordance и сохранением крупной
touch area. `АВТО` остаётся легко доступным; selected/locked/disabled state и
callback каждого протокола сохраняются.

Нужны RED checks: весь runtime inventory достижим жестом; выбранная ячейка
остается видимой после recomposition/process restore; RTL/accessibility; нет
наложения на боковой орнамент; TV D-pad/geometry имеет нулевой diff.

## P2 — ресницы на едином изумрудном веке

Добавить eyelashes как часть того же изумрудного eye-surround/lid material, а
не отдельный бронзовый/чужой overlay и не слой поверх старой мозаики. OFF остаётся
полностью закрытым без радужки/зрачка; ON, blink, gaze и touch остаются одной
динамической моделью. Сначала owner-approved OFF/ON/five-phase preview, затем
GitHub CI test APK; OTA отдельно.

## P1 — отказоустойчивый Maestro и Telegram

Текущий fleet работает, но S1 остаётся single point of failure. Целевая
архитектура требует отдельного утверждения:

- 3-node `rqlite` quorum на S2/S3/S4 как authoritative customer/operation
  ledger; один лидер, никаких конфликтующих multi-master writes;
- global operation/idempotency key и unique constraints для create, renewal,
  payment confirmation и import, чтобы retry/failover не создавал дубль;
- durable outbox + per-server reconciler для 3x-ui/protocol provisioning на
  S1–S4; повторное применение операции должно быть безопасным;
- Maestro API/panel на нескольких узлах за health-aware endpoint, с read-after-
  write и явным поведением при потере quorum;
- Telegram bot state вне локального single-node SQLite либо строго
  реплицированный; leader lease/**one poller** на token, автоматический failover
  без потери `getUpdates`, pending orders и admin confirmation;
- восстановившийся узел догоняет authoritative log и сверяет identities,
  expiries и operation IDs без создания дублей;
- fault-injection acceptance: потеря любого одного узла, рестарт лидера,
  duplicate Telegram update, network timeout после commit, stale node rejoin и
  backup restore.

`rqlite` не считать live, пока не подтверждены quorum, node-loss failover,
rejoin/reconciliation, expiry invariants и duplicate audit. Во время миграции
старый S1 workflow должен иметь проверенный rollback и не выключаться до
двухсторонней сверки данных.
