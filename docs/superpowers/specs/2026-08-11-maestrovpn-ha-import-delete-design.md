# MaestroVPN HA importer: operational tombstones and canonical delete digest

**Статус:** подтверждено владельцем 11.08.2026.

**Связанные документы:**

- `2026-08-09-maestrovpn-ha-control-plane-design.md`;
- `../plans/2026-08-09-maestrovpn-ha-02-business-api.md`, Task 6.

## Цель и границы

Delta-import обязан одновременно выполнить два требования:

1. удаление клиента не может быть silent hard delete: доступы отзываются, а
   operational tombstone хранится до ACK всех замороженных targets;
2. canonical business digest после `full(base)+delta` обязан совпасть с
   digest нового `fresh full` того же конечного источника, где удалённой
   сущности уже нет.

Этот slice реализует только явный `customer` delete и автоматически выведенный
cascade marker для его owner-bound `encrypted_secret`. Другие delete kinds
блокируются до отдельного контракта. Production CLI factory, live import,
cutover, SSH, боты, OTA и клиентские данные не включаются.

## Рассмотренные варианты

### 1. Немедленный hard delete

Отклонён: удаляет доказательство revoke до ACK S1–S4, может оставить доступ на
недоступном узле и нарушает основной HA-инвариант.

### 2. Включить operational tombstones в canonical business digest

Отклонён: delta-target неизбежно отличается от fresh-full target, хотя их
активное business-состояние одинаково. Проверка Task 6 становится ложной.

### 3. Разделить operational reconciliation state и logical business state

Выбран и подтверждён. Физические revoked/deleted строки и tombstones остаются
для безопасной доставки, но canonical digest представляет только активное
логическое состояние источника. Import registry и delete receipts считаются
bookkeeping и в digest не входят.

## Модель данных

### `imported_entity_state`

Typed registry связывает legacy stable key с cluster target без сохранения
секретов:

- primary key `(entity_kind, source_key)`;
- immutable `target_id`;
- `canonical_sha256` exact последней подтверждённой source entity;
- `lifecycle = active|deleted`;
- timestamp последнего import transition;
- unique `(entity_kind, target_id)`;
- composite unique key, позволяющий delete receipt ссылаться на exact
  `(entity_kind, source_key, canonical_sha256, lifecycle)`.

В этом slice registry принимает только `customer` и `encrypted_secret`.
Customer и secret typed upserts обновляют registry в той же batch transaction.
Переход `deleted -> active`, смена target ID и rollback lifecycle запрещены.

### `import_delete_receipts`

Immutable receipt содержит entity kind, source key, target ID,
`expected_prior_digest`, состояние `deleted`, optional customer tombstone ID,
import run/batch identity и timestamp. Composite FK на registry делает
невозможным receipt, если exact active digest не был атомарно переведён в
`deleted`. Exact retry идемпотентен; подмена digest/target/tombstone запрещена.

### Runtime tombstone

Customer delete использует существующие `tombstones` и `tombstone_targets`.
Tombstone ID детерминирован customer ID и следующей generation. Target set
замораживается из всех `node_services` с `desired_target=1` и `retired=0`,
включая fenced S1. Importer не очищает tombstone и не симулирует ACK.

## Планирование и проверка delta

`PlannedDelete` содержит только несекретные canonical поля: entity, source
key, target internal ID, exact prior digest, prior generation, next generation
и tombstone flag.

Для каждого user-provided delete Plan обязан:

1. потребовать `snapshot_kind=delta`, exact applied parent digest и exact
   parent snapshot;
2. разрешить только explicit `customer` delete;
3. найти ровно одного customer в parent и запретить upsert и delete одного
   stable key в одном delta;
4. потребовать `expected_prior_digest = SHA-256(canonical parent customer)`;
5. получить customer internal ID тем же namespace-deterministic алгоритмом;
6. установить `next_generation = prior_generation + 1` с проверкой overflow;
7. вывести owner-bound encrypted-secret cascade marker с exact SHA-256
   canonical parent secret; marker не принимается из внешнего snapshot.

Пустой/mismatched prior digest, duplicate delete, неизвестный source key,
unsupported entity и конфликт upsert/delete являются blocking report errors.
Ошибки не содержат canonical JSON, login, envelope или secret bytes.

## Атомарный apply

Customer delete входит в одну linearizable rqlite transaction вместе с batch
gate и durable batch receipt:

1. registry exact active digest переводится в `deleted`;
2. customer exact prior generation переводится в `status=deleted` и получает
   только `next_generation`;
3. credentials отключаются, subscription tokens отзываются;
4. deterministic tombstone создаётся/сверяется;
5. target set S1–S4 замораживается в `tombstone_targets`;
6. immutable import delete receipt создаётся;
7. batch становится `applied`.

DB triggers/FK обязаны откатить всю transaction при missing/mismatched registry
state, неверной generation, смене tombstone identity, lifecycle rollback или
неполном customer revoke. Нулевой CAS не считается успехом.

Cascade `encrypted_secret` operation атомарно переводит только его registry в
`deleted` и пишет immutable receipt. Зашифрованная строка
`imported_secrets` физически сохраняется: plaintext не появляется, а future
retention/hard-delete не входит в importer.

## Canonical business digest

Operational reconciliation и import bookkeeping исключаются из digest:

- `tombstones`, `tombstone_targets`, `imported_entity_state` и
  `import_delete_receipts` не участвуют;
- `customers`, `credentials`, `subscription_tokens` и
  `desired_node_state`, связанные с deleted customer registry, фильтруются;
- `imported_secrets` с deleted secret registry фильтруются;
- orders остаются историческими business rows и удаляются только отдельным
  explicit order contract, которого в этом slice нет.

Так active logical rows после полного применения delta совпадают с fresh-full,
а revoke/tombstone продолжают жить независимо до настоящих ACK.

## Crash, concurrency и безопасность

Все transition IDs и digests детерминированы. Crash до commit не оставляет ни
delete receipt, ни частичный revoke; crash после commit возобновляется по
batch receipt. Два resume конкурируют за ту же run/batch identity, а exact
retry возвращает прежний результат. Different digest, lifecycle rollback и
повтор с другой generation fail closed.

В registry, receipt, report и логах запрещены raw login credentials, UUID,
SubID, SubToken, bot token, trial salt и decrypted envelope. Удаление не
разрешает физически удалять immutable secrets или обходить S1 fence.

## Проверка готовности

RED/GREEN набор обязан доказать:

- invalid/missing prior digest и unsupported delete блокируются до write;
- typed customer revoke, deterministic tombstone и frozen S1–S4 targets
  коммитятся атомарно с batch receipt;
- injected mismatch откатывает customer, registry, targets и receipt;
- encrypted secret остаётся физически зашифрованным, но исчезает из logical
  digest после cascade marker;
- crash/resume и concurrent exact retry не дублируют tombstone/receipt;
- real three-node rqlite `full(base)+delta` digest равен fresh-full digest;
- formatting, unit, race, vet, harness и integration проходят в GitHub Actions.
