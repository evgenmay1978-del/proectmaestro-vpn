# MaestroVPN — актуальная точка продолжения

Обновлено 05.09.2026. Это текущий handoff, а не новый проект или новый план.
Полная прежняя история сохранена без потерь в
[CONTEXT_HANDOFF_HISTORY_2026-09-05.md](CONTEXT_HANDOFF_HISTORY_2026-09-05.md).
Историю открывать по конкретной необходимости; старые CURRENT/next-action не действуют.

## 1. Откуда продолжать

- Канонический репозиторий: `evgenmay1978-del/proectmaestro-vpn`.
- Единственная рабочая/push-ветка: `codex/yandex-cdn-whitelist-task3-sync`.
- Worktree: `C:/Users/User/Documents/Codex/2026-08-05/new-chat/mvpn-yandex-cdn-whitelist-task3-sync`.
- Последний проверенный Android/native source: `458fec7c885250f65d474204061dcdd60d71ee56`; последняя GREEN backend база — `15e0df17e32b400a2244e7ff0b73078b5c0a0ab0`, managed runtime BOOTTIME proof — `b2747aa5c785fd96d88eb1d0e7df7964fafe3383`. Их exact CI ниже SUCCESS. Владелец установил test APK, активировал логином и сообщил ordinary VPN PASS; CDN скрыт при прежнем live backend, новый panel не deployed.
- Каждый CI ниже относится к указанному source SHA. Source/CI не доказывают установленный runtime; последующий documentation HEAD не требует повторять завершённые проверки.
- HA run [33967852643](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33967852643) **SUCCESS** на `7c27caf055e05c5417a0896317ca1395120c4449`: Go tests, race, vet, rqlite integration и immutable panel build. Run terminal; не dispatch заново.
- Reserve source: `76d32d012890666d9bc8a6c896a0b2bb039d553d`; HA run [33971035570](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33971035570) **SUCCESS**, job101319473149: Go/race/vet/rqlite integration/immutable build. Run terminal; не dispatch заново. Новый panel не deployed.
- Preview run [33961612988](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33961612988) **SUCCESS** на предшествующем `7d98663ccca2c99224ad501c0f0a437221727c4c`; это историческое visual evidence, не проверка последующего native runtime.
- Run [33964867550](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33964867550) **SUCCESS** на `61076496484144a921d2f28ac83b016c5e459f8b`: `mobile-eye-compile`, Kotlin app/test compilation и пять focused классов (прежние четыре UI/geometry + семь тестов `WhiteListBalanceTest`). Preview job101303099838 также SUCCESS; это не новая визуальная приёмка глаза.
- Test-only APK `1.0.158-task7-test`/1015800 собран в run33982084006; production OTA/release не выпускались. Владелец сообщил об установке test APK после удаления157; это не CDN runtime acceptance. Не запускать заново завершённые проверки без изменения проверяемого поведения.
- Активная полная цель НЕ завершена. Не переименовывать остаток в новый проект и не добавлять номера этапов ради учёта.

## 2. Обязательные правила владельца

- Самый простой достаточный путь к работающему сервису. Никаких проверок, исследований, абстракций или review ради самих себя.
- Каждый check нужен ровно для требования, наблюдаемой ошибки, конкретного риска или утверждённого production stop-gate.
- Тяжёлые Go/Gradle/race/Android сборки и тесты — только GitHub. Слабый Windows PC: узкие чтения, edits, format/diff и разрешённые программные превью.
- Перед повтором ошибки сначала установить и записать причину. Не повторять тот же workflow/SHA или команду с косметически другими флагами.
- Root — единственный writer Git, CI и repetition ledger. Не стирать чужие dirty/untracked файлы.
- Не заходить в AdminVPS. Работать через сохранённый SSH; не менять host-key policy и не закрывать обычный SSH firewall.
- Последняя инструкция владельца05.09: настраивать S3 самостоятельно по SSH, без захода владельца в консоль. Использовать уже сохранённый continuity pin и StrictHostKeyChecking=yes; не требовать повторно консольный fingerprint. Это выбор владельцем существующего SSH trust path, не новая OOB attestation и не разрешение отключать host-key checking.
- Краткие сообщения только по существенному изменению; не заставлять владельца снова объяснять архитектуру и давать уже сохранённые доступы.
- После review, exact-SHA CI и применимых backup/rollback/live-validation gates продолжать согласованный isolated rollout без повторного промежуточного подтверждения.
- Не трогать реализацию/бинарники OLCRTC и WDTT. Их UI-скрытие отдельно разрешено владельцем и уже сделано.
- Не включать реальные списания, OTA/release/signing или финальный customer cutover без применимых stop-gates.
- Приоритет владельца: настроенный рабочий результат сегодня/к утру (05–06.09.2026). Это приоритет, не доказательство готовности и не основание отключать защиту.
- Не обещать процент/срок без проверяемой основы. Не выдавать код или GREEN CI за рабочий production.

## 3. Как устроен существующий продукт

- Одна универсальная Android APK MaestroVPN для телефона/TV; существующие sing-box/libbox и единственный VpnService сохраняются.
- Приложение активируется ТОЛЬКО ПО ЛОГИНУ. Не просить владельца вставлять/import subscription URL: ClaimViewModel сам получает внутренний sub_url через /claim, сохраняет Remote profile и выбирает аккаунт. CDN/balance используют тот же выбранный аккаунт. Эта цепочка проверена по исходникам05.09; manual import не нужен.
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
| S3 | Source `72975a7a98711c74eac563191610ec6263a79799`, release `72975a7a9871-standard-6f2626b5db7549c7`, ACTIVE, шесть shipped checks PASS; подтверждено повторным read-only shipped status05.09. |
| S1 | Использовать текущий replacement S1 из сохранённого private inventory, не удалённый старый узел. Существующая panel/bots не перевыкатывались этим checkpoint; commercial rollout ещё требуется. |

S2 установлен shipped transactional plan/apply с проверенным backup исходного ABSENT,
проверкой9 archive members и15 необходимых leaf TLS files. Обычные SSH/Caddy/nginx/
Hysteria/AnyTLS/bot PIDs не изменились. Firewall delta: только TCP18084 от S4 Origin
и TCP18443 от S1 controller; все прежние IPv4/IPv6/SSH правила сохранены.
S2/S3 ещё НЕ добавлены в CDN Origin group. S4 НЕ обновлён до72975a7/7d98663.
S3: current release совпал с operator-state, last_known_good=ABSENT; commercial/agent active
and enabled, owned port-guard active/exited (oneshot). Runtime/config/package/process checks PASS.
S3 ordinary SSH и private S4 canary этим read-only checkpoint не изменялись.
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

1. Завершить production accounting gates: file-report provider/caller реализован в source76d32d0, exact HA33971035570 SUCCESS; реального measured report пока нет. Требование reserve=max(10,000,000bytes, measured p99.9 bytes/s ×5s), collector≤2s/revoke≤5s. Измерения нельзя выдумать. Также открыты same-boot reset и точная граница периода/outage.
2. Проверить реализованный Android XHTTP/UOT runtime на разрешённом устройстве: source458fec7, exact run33982084006 SUCCESS и test APK создан. CDN — явный phone-only выбор через существующий единственный VpnService, вне Auto; source/CI не закрывают device/two-Go-runtime/network/ordinary gates. У ADB сейчас нет подключённых устройств; CI-only APK не считать production и не устанавливать без applicable owner device authority.
3. Проверить реализованный CDN balance на test-only APK: selected-account switch/edit, disabled/pending/offline/expired и строка в реальном account block. Source/compile/unit gate GREEN, APK/runtime gate открыт; сам баланс не разрешает CDN runtime.
4. Закончить связку панели, двух ботов и существующего канала: ручной confirm→GB→тот же аккаунт приложения с входом по логину; отдельная инструкция импорта только для сторонних клиентов, admin hide/revoke, ordinary isolation.
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

`AuthorizeWhiteListMeteringAdmission` теперь вызывается существующим collector через опциональный
provider `MAESTRO_WHITELIST_RESERVE_FILE`. ID-only candidates берутся из publications+credentials,
не managed plan.Routes; иначе снова возникнет bootstrap deadlock. Sampling получает
deadline start+2s, reconcile — отдельный start+5s; startup recovery failure также вызывает reconcile.
Оба receipt-recovery пути сохраняют deadline поверх WithoutCancel и не повторяют неизвестный POST.
Независимый six-file review CLEAN; четыре focused regressions прошли в exact-SHA HA33967852643.
Это cooperative budgets, НЕ доказанная частота sampling/revoke: Origin/DB calls последовательны,
mutex/нагрузка и live latency ещё требуют существующего canary gate.
Same-boot producer всегда передаёт reset_sequence0/counter_generation1; UsageSnapshot не содержит
достоверный reset marker/terminal pre-reset counters. Не повышать generation и не rebaseline
по одному уменьшению counters: это теряет байты. Existing durable ResetSequence и
ErrResetGenerationRequired сохраняются; до настоящего reset evidence оставить fail-closed.
Точный closing sample при смене периода/outage не производится. Approved plan задаёт формулу p99.9, но не window,
population, sample count или freshness: их нельзя заменить выдуманным числом. Существующий
synthetic commercial counter proof не является p99.9 measurement. Publication/collector остаются OFF.

Source76d32d0: regular JSON report ≤64KiB перечитывается один раз за pass, сроки не продлеваются
автоматически; schema/unit/basis/rate/duplicate-exit/expiry проверяются. Нет default или fallback
между exits. Вызов admission выполняется только после всех authenticated observations и debits,
до reconcile. Missing/invalid report не отменяет accounting/reconcile. Уже записанные leases
не аннулируются удалением файла мгновенно: для немедленного revoke использовать admin disable.
[Точный формат и operator contract](ops/yandex_cdn_commercial/RESERVE_REPORT.md).
Файл — доверенный ввод оператора, не доказательство качества измерения или live SLO. Отчёт с
реальными измерениями ещё не получен. Независимый source review CLEAN, exact HA33971035570 SUCCESS.
Production-adapter integration теперь использует настоящий file provider→collector→admission,
а не прямой вызов Authorize, скрывавший разрыв первой выдачи.

### Текущая реализация runtime и измерителя05.09

- Source `3421418a49fd7a128d2034d07ac1224b231f1a7a`: отдельный read-only `maestro-reserve-measure` через authenticated GET/v1/usage; fixed scheduled windows, all-Origin UP+DOWN одного synthetic account, nearest-rank p999, protected raw evidence. Нет traffic generation, POST/reset или включения collector/publication. [Contract](ops/yandex_cdn_commercial/RESERVE_MEASUREMENT.md).
- Exact run [33978987659](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33978987659), job101340648435 **SUCCESS**: focused Go/race/vet и Linux build. Binary6475924bytes, SHA256 `21fe9cf4c4d9dddc4a6e0ddd52699414954129f9c96f1996d8b8a0179e2e3e41`. Проверенный artifact размещён на current S1 в новом protected reserve-measure-3421418 каталоге существующего controller backup; services не менялись, report не установлен. Реальной измерительной серии пока нет.
- Read-only S4 live receipt05.09 подтвердил source951dace, current desired generation5, один synthetic managed user и один active Origin. Existing receipt refresh жив; private subscription/static canary не читались и не пробовались.
- Native/app source `458fec7c885250f65d474204061dcdd60d71ee56`: isolated c-shared/JNI XHTTP engine, pinned Xray26.5.9, authenticated TCP SOCKS + UOTv2 без UDP listener, generation-bound original-FD protect/bind, bounded stop/poison. Phone selector работает через existing BoxService; service повторно получает fresh typed API material, account/revision/network/expiry fences и no-fallback marker сохраняются. Ordinary libbox AAR взят из прежнего exact artifact; WDTT/OLCRTC не собирались и не менялись. Независимые native/app reviews CLEAN. TCP half-close ограничен pinned core.Dial API и явно описан в android-xhttp/README.md.
- Native delivery source `fd8e7e532b011fb6acca5b7100cec5a8681de252`, fixture correction `179810fbe8d04c0344bafd3800ee9df40c654e78`: GET/account/whitelist-runtime через subscription Bearer и настоящую fresh PublicationSource, typed profiles, lease1–5s, no-store. Bare ordinary/cache остаются прежними. Exact HA [33981504938](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33981504938), job101347435963 **SUCCESS**: Go/race/vet/rqlite integration/immutable panel build. Artifact9974014423, zip SHA256 `8892b3a4b32d3d9bc675a75895d5905eee70d14bcfd5b9fd21b680b89c91c636`. Panel не deployed. [Wire contract](ops/yandex_cdn_commercial/NATIVE_RUNTIME.md).
- Exact Android run [33982084006](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33982084006) **SUCCESS** на458fec7: native job101348988540 (Go/race/vet, arm64-v8a/armeabi-v7a NDK28/API23, JNI exports), app job101349201507 (семь focused classes, assembleOtherDebug, package/version/debuggable=false/AndroidDebug signer). Artifact9974178798, zip168339932bytes, SHA256 `95d626ffb767560f6a945df2a111b67afdaf44f6112665a26ee46a4d27eae8d6`. APK `MaestroVPN-TV-1.0.158-task7-test-debug.apk`,170933530bytes, SHA256 `31a40e0e7abaa30ff6cff7e465ae9df3ddb6e65b4632b958403a8ea8f53e8f36`. По прямому запросу владельца APK скачан, извлечён в projectless artifacts и проверен по обоим hashes. Локальная ссылка оказалась непригодна для скачивания в чате; выдана временная HTTPS ZIP ссылка GitHub blob с проверенным unsigned206/ZIP header/size. Не сохранять signed URL в Git; при новом запросе получать новый ограниченный artifact redirect. Владелец сообщил: поверх157 не установилось, после удаления157 test APK установлен, активация по логину и ordinary VPN работают; CDN в UI отсутствует. Это OWNER-REPORTED TEST APK INSTALL/LOGIN/ORDINARY PASS, не CDN runtime proof. APK имеет debug signer; fingerprint установленной157 и исходный install error не получены, точную signature mismatch не утверждать.
- Старый HA33980215379/fd8e7e5 FAILED до native assertion: fixture запускал Python на каждом SQL и превышал реальный2s collector budget. Новый persistent SQLite fixture в179810f сохраняет transactions/context cancellation/production deadlines; исправленный run33981504938 GREEN. Старую попытку не повторять.
- Все новые source checkpoints сохраняют customer publication/collector OFF. После итоговой runtime integration нужны реальные измерения в окончательном fleet scope, collector/revoke SLO, reset/period/outage gates,48h непрерывного shadow accounting и owner device acceptance; GREEN build не закрывает эти gates.

### Подтверждённые следующие source/live задачи05.09

- Current S1 read-only SSH preflight18:06UTC: один running panel, disk/process SHA256 `867df76e07a4e1ca753c0cc3940d5d4da43c97c353e47c049efe9fa1f6ded83a`; legacy `/usr/local/bin/maestro-panel`, `/etc/maestro-panel.env`, loopback8910. Control-plane/sidecar enable unset, rqlite endpoints0, commercial CA/cert/key/reserve inputs не configured. На S1 нет rqlited process/default listeners/known units/local schema files. Отдельный strict SSH preflight S2/S3/S4 в18:15UTC также подтвердил отсутствие rqlited process,4001/4002 listeners, известных rqlite/rqlited units и обоих штатных data paths; HA cluster ещё не развёрнут. Protected projectless reports: work/s2-ha-presence-20260905.json, s3/s4 аналоги. Ничего не перезапускалось.
- Existing S1 bot main.py уже импортирует/includes maestro_orders, один start_polling; новый customer/order_actions adapter и customer binding DB отсутствуют. Повторный router hook/poller не добавлять. Protected report: projectless `work/s1-commercial-preflight-20260905.json`.
- Existing `maestro-import` умеет dry-run/apply/full/delta/resume для normalized Snapshot format_version2 с HMAC identities и encrypted envelopes; это не raw legacy customers/orders/trials JSON. Production normalizer этих legacy inputs ещё требуется. Runtime rqlite bootstrap применяет schema17, importer проверяет её, но не создаёт. `ops/ha/deploy-node` пока plan-only; binary-only replacement не включает commercial runtime и не переносит ordinary данные.
- Import reader adapter реализован в3d821fb и дополнен CI fixture correction8008cd3: typed full legacy identity проверяется до открытия production DB, обычный token и каждый supported protocol seal в настоящем reader Envelope/scope. Исходные token/UUID/passwords сохраняются; SubID/generation/HMAC binding обязательны. В этой исторической базе неизвестные device/WG/per-node identity mappings fail-closed до записи; Devices/Naive compatibility исправлена позднее вa685760/15e0df1 ниже. Это adapter, не raw JSON normalizer и не live migration. Первая HA33984759546 прошла Go/race/vet, но integration fixture имел expiry1970 и пытался DELETE immutable provenance. В8008cd3 fixture привязан к DB unixepoch, immutable seed живёт до teardown disposable cluster; byte-exact subscription/no-mint assertions и backup-RPO CAS сохранены. Исправленный exact HA [33986006452](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33986006452) на8008cd3 **SUCCESS**: Go/race/vet/actual rqlite reader roundtrip/immutable build. Artifact9975310180, SHA256 `ed6ed72fca3e65579e54da30d528c3e8c6b3fbbe6872bf4a341f2dee8264a126`; panel не installed, run не повторять.
- Период нельзя чинить сменой >= или подстановкой timestamp: агент ставит SampledAt после QueryStats(reset=false), точного cutoff нет. Pinned Xray RemoveUser удаляет validator maps, уже принятый forward stream остаётся жить. Значит revoke receipt не доказывает закрытие active stream≤5s. Нужен managed-user fence+cancel/drain+реальный final counter receipt перед границей, затем durable cross-period continuity. Нельзя rebaseline, выдумывать generation или считать два одинаковых counters proof завершения idle stream. Backend period checks не ослаблены.
- Для этого в3bb8f15 реализован отдельный maestro-xray-cdn-runtime на pinned Xray1bdb488c9ec0: managed dispatcher default-deny, grant/fence RPC через существующий Commander, boot/config/generation+MemoryUser binding, cancel/drain и final cumulative counters; все destructive Stats resets запрещены. Normal completion сохраняет shared mux parent/queued DOWN, explicit fence закрывает captured parent; независимый scoped review CLEAN. Это source proof: реальный agent caller, backend terminal receipt/period wiring и live proof ещё обязательны; commercial bundle остаётся прежним upstream runtime, proof artifact не deployable. Первый run33984878803 остановился до compile из-за go.mod; metadata-only GitHub run33985271508/de95a19 SUCCESS дал verified go.mod/go.sum, pinned Xray/grpc/protobuf versions не изменились. Исправленный exact proof run [33986003940](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33986003940) на8008cd3 **SUCCESS**: policy/Go/race/vet/release-template и Linux runtime build. Artifact9975202752, zip12174643bytes, SHA256 `d09adce2d335ebd5dbedf74c48a70d8d392e126de67f6a58256d99997d1cd1c7`. Это managed-session-fence-source-proof, не deployable commercial bundle; на серверах не установлен. Run terminal, не повторять.


- Renewal lease реализована в4b7e517, независимый six-file review CLEAN; exact proof run [33987120409](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33987120409) **SUCCESS** (Go/race/vet/build). Grant/renew требуют lease_ms1..5000, monotonic expiry автономно fences; exact retries не продлевают deadline, expired renew не открывает доступ, final counters только после drain. Generation здесь версия управляющей операции, не durable desired revision. Receipt expiry — clock hint, не выдуманный byte cutoff. Artifact9975505512, zip12178190bytes, SHA256 `ab460f68c909a1d8f79f7dff31f4c28c691936c31d229beefa1c995080302863`. Это по-прежнему не deployable bundle: реальный caller/renew lifecycle ещё не wired.
- Unused fence реализован в7dc36690ae64a46db9dd364be1ed4fd573b52f8f. После полного drain оба counters отсутствуют и этот email ни разу за физический boot не начинал stream — отдельный receipt fenced_unused без UP/DOWN. Partial pair или отсутствие counters после успешного старта остаются ошибкой; реальная zero pair сохраняет обычный fenced. Exact proof run [33988998254](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33988998254) SUCCESS: Go/race/vet/build. Artifact9976057281, zip12178237bytes, SHA256 `2e21a805023759d0b477c55753d6dc52cbe0767140216e727ef1be5d72c91075`. Source proof, не deployable bundle; run не повторять.
- Причина отсутствия CDN после login подтверждена: running S1 MAESTRO_SUB_BASE выдаёт существующий trusted public origin на порту8911; unauthenticated GET/account/whitelist-runtime и GET/account/whitelist-balance возвращают404. Login-issued origin/port важнее BuildConfig.BACKEND_URL. Private token/account/canary не запрашивались. Повторная активация/переустановка эту серверную причину не исправит.
- Legacy compatibility реализована вa685760cf58dbcdc14ea24cc8a78b5892f27329a: Devices/HMAC/deterministic IDs и60d TTL, оригинальный Naive username в typed AAD envelope через оба real readers/renderer, disabled→suspended, latest protected identity в существующем mutable desired_node_state. Final delta использует authenticated parent revision/status/expiry/provenance CAS, конфликт откатывает всю batch; удаляются только отсутствующие parent-owned device HMAC с last_seen строго раньше capture, target-only и concurrent claims сохраняются. Один scoped peer review CLEAN. Actual rqlite test проверяет full→delta byte-exact ordinary output и rollback одинаковой следующей revision. HA33989963726 остановился в Test backend: три fixtures отстали от SQL projection/Args и canonical login, не runtime renderer failure. В15e0df17e32b400a2244e7ff0b73078b5c0a0ab0 исправлены эти fixtures; validator принимает существующую SQL seconds projection при сохранении precise legacy expiry в encrypted identity, добавлен regression. Corrected exact HA [33990399919](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33990399919), job101371489267, SUCCESS: Go/race/vet/actual rqlite full-to-delta parity and conflict rollback/immutable build. Artifact9976608384, zip11625420bytes, SHA256 `3da048c900fc13cca5b1eeb120caa2c7d6de4e29b977cfca9ed7c840cb087780`. Live migration не выполнялась; run terminal, не повторять.
- Вb2747aa5c785fd96d88eb1d0e7df7964fafe3383 относительная lease заменена schema2 Control/Receipt: absolute `deadline_boottime_ns` + independently computed `clock_domain` (clock kind, kernel boot, time namespace). Late RPC не начинает новый срок; BOOTTIME учитывает suspend, ошибки/регресс часов закрывают доступ. Kernel deadline проверяется при grant/renew/start/counted I/O, timer только wake hint. Exact retry не переустанавливает deadline/timer; non-Linux constructor отвергается без fallback. Any config schema1, drain/counters/fenced_unused сохраняются; x/sys0.43.0 только переведён в direct. Scoped peer review CLEAN; exact proof [33990884326](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33990884326) SUCCESS: policy/release-template/Go/race/vet/Linux build. Artifact9976594232, zip12181186bytes, SHA256 `4095bc94c09be473cdcca892ab1a93f8524f6b4eda90d65a58797880b5de3801`. Source proof не deployed; реальный caller, durable operation journal и terminal receipt/period wiring всё ещё открыты. Старый schema1/lease_ms не использовать. Run terminal, не повторять.
- Raw legacy normalizer пока не завершён. Начат bounded nine-file producer в существующем maestro-import: normalize/capture-xui + importer helper, tests и optional per-node SubID map в protected identity. Android agent владеет normalizer/main/importer; measurement agent получил только новые capture_xui.go/capture_xui_test.go; root — Git/CI/ledger. Guard action implement-legacy-customer-capture-normalizer, family nine-file-protected-source-to-existing-snapshot ALLOW. Точный dirty/source freeze проверять по сессии, этот checkpoint не утверждает завершение producer.
- Не использовать store.Open для capture: он допускает missing-file→empty и map overwrite дубликатов. Authoritative SubID доступен через existing read-only xui.GetClient(login) с matching UUID/email, может различаться по nodes; не заменять SubToken. Producer сохраняет original precise time.Time, raw credentials/devices в protected identity и использует существующую SQL seconds projection. Legacy Provision/ActivateExisting для capture не вызывать. Customer-only producer не доказывает полный перенос: непустые orders/trials, старый trial salt, settings/principals и реальный HA bootstrap/PKI требуют своих действительных источников и применимых migration gates; ничего не пропускать молча.

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
- Run33970719260/source0f26ed1 упал на compile candidate test: reused whiteListConfirmedOrderStatement находится под build tag rqlite_integration. Исправлено в76d32d0 собственным exact SQL fixture; при reuse helper читать также build constraints его файла. Старый SHA/run не повторять.
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
Root ведёт Git/CI/ledger; source writers получают отдельные точные allowlists. Текущие активные
агенты и незакоммиченные изменения проверять по состоянию сессии. Исходный unrelated dirty сохранён;
в AGENTS дополнительно осталась
локальная двухстрочная инструкция по отдельным option/value аргументам guard, она не staged.
Docs/manifest для этого checkpoint проверяются в узком committed-tree snapshot с новым handoff,
чтобы unrelated dirty AGENTS не попал в commit или в manifest опубликованного дерева.
Устранено устаревшее ожидание docs-test только для PANEL_INTEGRATION.md: точный source-contract
status с запретом считать source production-ready, остальные19 target-only/ADR проверки сохранены.
