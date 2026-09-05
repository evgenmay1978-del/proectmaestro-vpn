# MaestroVPN — актуальная точка продолжения

Обновлено 05.09.2026. Это текущий handoff, а не новый проект или новый план.
Полная прежняя история сохранена без потерь в
[CONTEXT_HANDOFF_HISTORY_2026-09-05.md](CONTEXT_HANDOFF_HISTORY_2026-09-05.md).
Историю открывать по конкретной необходимости; старые CURRENT/next-action не действуют.

## 1. Откуда продолжать

- Канонический репозиторий: `evgenmay1978-del/proectmaestro-vpn`.
- Единственная рабочая/push-ветка: `codex/yandex-cdn-whitelist-task3-sync`.
- Worktree: `C:/Users/User/Documents/Codex/2026-08-05/new-chat/mvpn-yandex-cdn-whitelist-task3-sync`.
- Последний проверенный Android source: `61076496484144a921d2f28ac83b016c5e459f8b`; последняя GREEN backend база — `7c27caf055e05c5417a0896317ca1395120c4449`. Metering deadlines исправлены и проверены в CI, но ещё не deployed.
- Последующий HEAD может содержать handoff/manifest и исправление docs-check; не путать его с SHA проверенной Android сборки.
- HA run [33967852643](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33967852643) **SUCCESS** на `7c27caf055e05c5417a0896317ca1395120c4449`: Go tests, race, vet, rqlite integration и immutable panel build. Run terminal; не dispatch заново.
- Preview run [33961612988](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33961612988) **SUCCESS** на предшествующем `7d98663ccca2c99224ad501c0f0a437221727c4c`; Android после6107649 не менялся.
- Run [33964867550](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33964867550) **SUCCESS** на `61076496484144a921d2f28ac83b016c5e459f8b`: `mobile-eye-compile`, Kotlin app/test compilation и пять focused классов (прежние четыре UI/geometry + семь тестов `WhiteListBalanceTest`). Preview job101303099838 также SUCCESS; это не новая визуальная приёмка глаза.
- Ни APK, ни production OTA/release не выпускались. Не запускать заново эти завершённые проверки без изменения проверяемого поведения.
- Активная полная цель НЕ завершена. Не переименовывать остаток в новый проект и не добавлять номера этапов ради учёта.

## 2. Обязательные правила владельца

- Самый простой достаточный путь к работающему сервису. Никаких проверок, исследований, абстракций или review ради самих себя.
- Каждый check нужен ровно для требования, наблюдаемой ошибки, конкретного риска или утверждённого production stop-gate.
- Тяжёлые Go/Gradle/race/Android сборки и тесты — только GitHub. Слабый Windows PC: узкие чтения, edits, format/diff и разрешённые программные превью.
- Перед повтором ошибки сначала установить и записать причину. Не повторять тот же workflow/SHA или команду с косметически другими флагами.
- Root — единственный writer Git, CI и repetition ledger. Не стирать чужие dirty/untracked файлы.
- Не заходить в AdminVPS. Работать через сохранённый SSH; не менять host-key policy и не закрывать обычный SSH firewall.
- Краткие сообщения только по существенному изменению; не заставлять владельца снова объяснять архитектуру и давать уже сохранённые доступы.
- После review, exact-SHA CI и применимых backup/rollback/live-validation gates продолжать согласованный isolated rollout без повторного промежуточного подтверждения.
- Не трогать реализацию/бинарники OLCRTC и WDTT. Их UI-скрытие отдельно разрешено владельцем и уже сделано.
- Не включать реальные списания, OTA/release/signing или финальный customer cutover без применимых stop-gates.
- Приоритет владельца: настроенный рабочий результат сегодня/к утру (05–06.09.2026). Это приоритет, не доказательство готовности и не основание отключать защиту.
- Не обещать процент/срок без проверяемой основы. Не выдавать код или GREEN CI за рабочий production.

## 3. Как устроен существующий продукт

- Одна универсальная Android APK MaestroVPN для телефона/TV; существующие sing-box/libbox и единственный VpnService сохраняются.
- Production baseline: `1.0.157`, tag `tv-v1.0.157`, commit `9653636863cb65cc2ac95545d953d9c5e06db8bb`.
- Следующая разрешённая Android identity пока test-only: `1.0.158-task7-test`, versionCode1015800; не обещать пользователям OTA157→158 до release gates.
- Существующий Go backend/panel остаётся владельцем аккаунтов, обычной подписки, заказов и ручного подтверждения оплаты.
- Два существующих Telegram-бота и канал должны быть единым UX с панелью; не создавать второй poller того же token и не терять pending updates.
- Для CDN: клиент → один Yandex CDN resource → одинаковые isolated Origin sidecars → выбранный отдельный TLS relay/exit → Интернет.
- CDN не устанавливается как отдельный Yandex resource на каждого клиента/сервер. Страна в label — страна exit.
- Обычный VPN и его подписка независимы от CDN quota/ошибок. Bare `/sub/<token>` остаётся совместимым с1.0.157; сторонний links-format дополняется CDN только после всех gates.
- Default CDN для клиента OFF/hidden. Покупка GB после ручного confirm или admin enable включает; admin disable скрывает, но сохраняет купленные GB.
- 400₽ ordinary renewal не начисляет автоматические2GB и не включает CDN. Купленные GB не сгорают; при истечении обычного доступа замораживаются.
- Единица —1GB=1,000,000,000bytes, расход UP+DOWN. Один серверный баланс на аккаунт для всех устройств/клиентов, не локальный счётчик приложения.
- Утверждённые цены/правила/потоки: [commercial design](docs/superpowers/specs/2026-09-01-maestrovpn-whitelist-commercial-delivery-design.md).
- Утверждённый существующий порядок реализации: [commercial plan](docs/superpowers/plans/2026-09-01-maestrovpn-whitelist-commercial-delivery.md). Галочки кода не означают live rollout.

## 4. Что фактически установлено

| Узел | Подтверждённая точка |
|---|---|
| S4 | Isolated commercial source `951dace559671bc9431605b967f8111963213a71`, release `951dace55967-s4commercial-38c3e87adc03cfcb`, ACTIVE, шесть shipped checks PASS. |
| S2 | Source `72975a7a98711c74eac563191610ec6263a79799`, release `72975a7a9871-standard-2aedf96b649fe0e3`, ACTIVE, шесть shipped checks PASS. |
| S3 | Rollout ещё не завершён; authoritative identity/east-west gates читать из сохранённого inventory перед изменением. |
| S1 | Использовать текущий replacement S1 из сохранённого private inventory, не удалённый старый узел. Существующая panel/bots не перевыкатывались этим checkpoint; commercial rollout ещё требуется. |

S2 установлен shipped transactional plan/apply с проверенным backup исходного ABSENT,
проверкой9 archive members и15 необходимых leaf TLS files. Обычные SSH/Caddy/nginx/
Hysteria/AnyTLS/bot PIDs не изменились. Firewall delta: только TCP18084 от S4 Origin
и TCP18443 от S1 controller; все прежние IPv4/IPv6/SSH правила сохранены.
S2 ещё НЕ добавлен в CDN Origin group. S4 НЕ обновлён до72975a7/7d98663.
Не повторять уже выполненную выдачу сертификатов, staging, install или прежние canary add/revoke/resume.
Точные digest/backup/rollback receipts сохранены в истории и локальном operational checkpoint.
Новый panel collector и customer publication остаются OFF. Не включать их лишь потому, что CI прошёл.

## 5. Что сделано в последних исходниках

- Durable observations + admission привязаны к entitlement/exit/origin/Xray boot/paid period/reserve.
- Первый настоящий cumulative полностью списывается через существующий durable accounting/outbox; отсутствующий counter не превращается в ноль.
- Исправлен цикл первой выдачи: новый клиент теперь может получить материал по отдельному доказанному AdmissionFreshUntilUnix до первого sample.
- Нужны current managed user/desired, exact applied receipt, fresh all-Origin observations и покрытый reserve. Старый empty bootstrap не годится.
- Admission lease ограничен period/reserve/receipt/observation/primary сроками; usage watermark не подделывается.
- После observed→missing доступ не переоткрывается ни reserve refresh, ни restart. Независимый six-file review CLEAN; exact HA GREEN.
- В phone/TV/Groups/status скрыты WDTT/OLCRTC/WebRTC aliases, manual/pending selection закрыт. Скрытый active не переименовывается в AUTO. Runtime managers и профили не менялись.
- Существует коммерческий API, manual-order/bot/delivery код по утверждённому плану; их end-to-end production подключение ещё не доказано.
- Android CDN balance реализован в source6107649: отдельная строка существующего phone account block, trusted selected profile, HTTPS/Bearer без redirects, foreground refresh30s, decimal GB; disabled скрыт, pending обновляется, ошибки не превращаются в0. Account identity сбрасывается при смене/редактировании профиля, старые ответы отбрасываются; permissive ordinary hasSubProfile сохраняется. Два замечания независимого review исправлены. На установленном APK это ещё не проверено.
- Полный private working white-list test остаётся неприкосновенным: owner сообщил реальный mobile white-list PASS01.09.2026. Это **OWNER-REPORTED CLIENT PASS**, не production readiness.
- Не удалять/ротировать/пробовать приватную подписку, её credential или static canary до завершения согласованной работы/отдельного разрешённого удаления. Секреты не копировать в Git, новые чаты или логи.

## 6. Что осталось — продолжать отсюда

1. Завершить production accounting wiring: реальный verified reserve provider/caller пока отсутствует. Требование reserve=max(10,000,000bytes, measured p99.9 bytes/s ×5s), collector≤2s/revoke≤5s. Измерения нельзя выдумать. Также открыты same-boot reset и точная граница периода/outage.
2. Доделать настоящую Android CDN runtime/selector интеграцию: WhiteListRuntimeGate не подключён, простая карточка не заменяет working XHTTP path. CDN — явный выбор только для enabled клиента, не paid Auto. TV/mobile ограничения менять только в согласованном scope.
3. Проверить реализованный CDN balance на test-only APK: selected-account switch/edit, disabled/pending/offline/expired и строка в реальном account block. Source/compile/unit gate GREEN, APK/runtime gate открыт; сам баланс не разрешает CDN runtime.
4. Закончить связку панели, двух ботов и существующего канала: ручной confirm→GB→одна подписка→понятный импорт/инструкция, admin hide/revoke, ordinary isolation.
5. Продолжить bounded rollout S4→S2→S3→S1 с existing shipped tooling; закончить cross-node receipt/runtime proof, требуемые canaries и наблюдение. Не повторять выполненную установкуS2.
6. Довести глаз по всем трём визуальным условиям и проверить runtime. Затем применимые release/OTA/customer-cutover gates. Полный продукт пока не готов.

### Android: реализованный остаток GB, runtime ещё не проверен

- Реальный API: GET `/account/whitelist-balance`; Authorization Bearer — тот же subscription token. Account ID/query/cookie/CSRF не нужны.
- Только trusted HTTPS Maestro origin: путь заменить на balance endpoint, убрать query/fragment; token только header, никогда не URL/log или redirect на другой origin.
- DTO: `available_bytes`, `included_remaining_bytes`, `purchased_remaining_bytes`, `period_ends_at_unix`, `primary_access_state`, `publication_verdict`.
- `DISABLED` скрывает CDN UI, не обнуляет сохранённые GB. `PROJECTION_PENDING` приходит200 с числами: показывать обновление, не гарантированный остаток.
- Ошибка/404/503 — неизвестно, НЕ0GB. 404 неоднозначен: отсутствующий entitlement, token или старый route.
- `PUBLISHABLE` в balance adapter НЕ доказывает all-Origin readiness и не разрешает подключение CDN.
- Старый Android `WhiteListClientInfo` ожидает `remaining_limit_bytes`; это не commercial wallet. Не использовать его как prepaid balance.
- Реализованное размещение: отдельная строка «CDN: осталось … ГБ» в существующем account block телефона; source/CI проверены, установленный runtime ещё не принят.
- AccountInfo.kt→SFANavigation.kt→TvHomeScreen.kt→Mobile4DHome.kt→PhoneHomeControlDeck.kt/SecondaryDeck сохраняется. Баланс получает phone-only `PhoneWhiteListBalance.kt` через `WhiteListBalanceClient`; account selection/revision hook общий по правилам для /info и баланса, TV-компоненты не менялись.
- Учитывать выбранный trusted account; неизвестный баланс не маскировать нулём, не менять ordinary expiry gating или deferred transport managers.
- Happ/Incy: названия CDN/страны есть в links renderer, live rendering статистики и richer protocol labels ещё не проверены. Не обещать остаток в любом стороннем клиенте; бот остаётся общей точкой информации.

### Уточнённый accounting blocker по source audit 05.09

`AuthorizeWhiteListMeteringAdmission` уже существует, production reserve provider/caller отсутствует.
Admission candidates должны браться из publications+credentials: plan.Routes содержит только уже
managed users и при прямом wiring оставит bootstrap deadlock. В новых исходниках sampling получает
deadline start+2s, reconcile — отдельный start+5s; startup recovery failure также вызывает reconcile.
Оба receipt-recovery пути сохраняют deadline поверх WithoutCancel и не повторяют неизвестный POST.
Независимый six-file review CLEAN; четыре focused regressions прошли в exact-SHA HA33967852643.
Это cooperative budgets, НЕ доказанная частота sampling/revoke: Origin/DB calls последовательны,
mutex/нагрузка и live latency ещё требуют существующего canary gate.
Same-boot producer всегда передаёт reset_sequence0/counter_generation1; точный closing sample
при смене периода/outage не производится. Approved plan задаёт формулу p99.9, но не window,
population, sample count или freshness: их нельзя заменить выдуманным числом. Существующий
synthetic commercial counter proof не является p99.9 measurement. Publication/collector остаются OFF.

Следующий минимальный wiring: существующий `WhiteListAdmissionReserve` содержит measured p999,
measured-at и valid-until; `RequiredBytes` уже считает утверждённую формулу.
Получать ID-only candidates из publications × credentials через `whiteListAdmissionBase`
(пример обхода — `EnsureWhiteListMeteringBootstrap`), не из уже managed plan.Routes.
После всех authenticated Origin observations перед reconcile нужен вызов существующего
`AuthorizeWhiteListMeteringAdmission` с реальным verified-reserve provider.
Готового production measured-report/loader не найдено; его ещё нужно реализовать и получить
измерения. Отсутствующий/просроченный report должен оставлять admission закрытым.

## 7. Глаз: точная незакрытая работа

Программная правка разрешена. Kotlin/Pillow используют общие58upper/18lower follicles,
original green/closed fold, registered ocular sprites и фиксированные corners.
Latest preview: `eyelashes-current-phone-preview`, actual390dp; lashes всё ещё слишком
неразличимы. **Визуально НЕ принято**, даже при GREEN preview/compile tests.
Три условия одновременно: анатомически реальный глаз, исходное зелёное окружение,
невидимая граница композита. Ни одно не закрывать одной автоматической проверкой.
Текущая reference: `design/mobile-4d-references/10-owner-installed-home-2026-09-01.jpg`.
Архивные August/dark-fan/spherical previews не предлагать как новое исправление.
[Visual evidence/status](docs/design/mobile-eye-natural/2026-09-05-programmatic-intermediate.md).

## 8. Ошибки, которые не повторять

- Старый Android workflow упирался в WDTT packaging до Gradle. Для bounded UI есть `mobile-eye-compile`; для одной картинки `mobile-eye-state-preview`. Не трогать WDTT ради компиляции глаза.
- Не считать normal Android/preview run сборкой installable APK. Не запускать workflow по старому SHA и не повторять204 dispatch: сначала сохранить run ID.
- Fixture paid order: expiry=created_at+86400. Интервалы считать по реальным source/event joins, не несуществующему entitlement_id.
- Не сканировать random Ed25519 signature на подстроки uuid/token: receipt test проверяет точную схему публичных JSON fields.
- В migration schema fixture были пропущены две таблицы0017; исправлено. Строгое сравнение схемы не ослаблять.
- При смене reference менять вместе path/hash/dimensions; September preview исправлен и GREEN. Pixel-outside-mask assertions сохранены.
- В GroupsCard фильтр передавать через uiState.copy в дочерний composable, не обращаться к чужому локальному scope.
- Git push с workflow-файлами — существующий strict SSH путь, не повтор HTTPS/auth failures.
- Чтения: один файл, bounded unprefixed slices; не передавать rg одновременно файл и его parent. Проверять truncation до следующего действия.
- Не повторять boot/usage/provisioning цикл на working private canary ради статуса. Не открывать глобально порты и не сбрасывать firewall.

## 9. Локальная оперативная передача и dirty state

Локальный подробный checkpoint:
`C:/Users/User/Documents/Codex/2026-08-21/webcmd-plugin-webcmd-openai-curated-remote/.scratch/current-commercial-ops-20260904.md`.
Сохранённые helper/SSH/ledger находятся в
`C:/Users/User/AppData/Local/Temp/maestro-s4-ops-559fb0072de143f097d5cff6044214fa`.
Их существование/актуальность проверять перед использованием; секреты не печатать.
GitHub helper `github-eye-preview.py` уже умеет exact-SHA dispatch/runs/jobs/artifacts;
не писать другой dispatcher и не грузить тяжёлые архивы на слабый PC.

Существующие unrelated dirty: AGENTS.md; два .superpowers progress/report;
backend/cmd/maestro-panel/main_test.go; backend/internal/api controlplane_business_test/
controlplane_port_test; controlplane customer_access/external_actions/paid_visibility/
setting_secret_read/status/task8_setting tests; migrations0001–0010; normalize.patch.
Не stage/reset/format их целиком. Root добавлял в AGENTS только короткий output rule,
старые изменения сохранены. Точный список — текущий git status, не предположение.
Незавершённые Android balance edits предыдущей задачи восстановлены, исправлены и включены в6107649.
Других writer-агентов нет. Исходный unrelated dirty сохранён; в AGENTS дополнительно осталась
локальная двухстрочная инструкция по отдельным option/value аргументам guard, она не staged.
Docs/manifest для этого checkpoint проверяются в узком committed-tree snapshot с новым handoff,
чтобы unrelated dirty AGENTS не попал в commit или в manifest опубликованного дерева.
Устранено устаревшее ожидание docs-test только для PANEL_INTEGRATION.md: точный source-contract
status с запретом считать source production-ready, остальные19 target-only/ADR проверки сохранены.
