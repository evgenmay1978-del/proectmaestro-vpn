# CODEX BOOTSTRAP MASTER TASK
# MaestroVPN / Maestro Panel
# Yandex Cloud CDN White-List Transport
# Per-Client Management + Per-GB Billing
# Production-Safe Engineering Program

Ты работаешь с реальной системой MaestroVPN, Maestro Panel, backend,
ботами, серверами, 3x-ui, Xray, подписками и существующими клиентами.

Это не просьба написать теоретический план и остановиться.

Требуется:

1. Самостоятельно обследовать существующую систему.
2. Зафиксировать проверенные факты.
3. Спроектировать безопасную архитектуру.
4. Создать изолированную реализацию.
5. Добавить автоматические тесты.
6. Поднять тестовый data plane.
7. Реализовать интеграцию с Maestro Panel.
8. Реализовать server-side учёт трафика.
9. Реализовать тарификацию по гигабайтам.
10. Подготовить клиентскую совместимость.
11. Подготовить canary rollout и автоматический rollback.
12. Не повредить ни одного уже работающего VPN-подключения.

Не ограничивай себя механическим копированием предложенной архитектуры.
Проверенные ниже решения являются baseline и критериями приёмки, а не
потолком. Если как инженер ты способен реализовать более устойчивое,
простое, масштабируемое и удобное решение — исследуй, докажи
преимущества тестами и реализуй его в изолированном контуре.

Ты не имеешь права улучшать систему ценой риска для уже работающего VPN.

======================================================================
0. КАК ОБРАБОТАТЬ ЭТО ЗАДАНИЕ В РЕПОЗИТОРИИ
======================================================================

Сразу после чтения этого задания:

1. Найди существующие:
   - AGENTS.md;
   - CLAUDE.md;
   - CONTEXT.md;
   - CHANGELOG;
   - README;
   - docs;
   - ADR;
   - handoff-файлы;
   - правила разработки.

2. Не перезаписывай их вслепую.

3. Сохрани этот документ без сокращений в:

   docs/yandex-cdn-whitelist/MASTER_REQUIREMENTS.md

4. Создай или обнови короткий корневой AGENTS.md.

AGENTS.md не должен содержать весь этот огромный документ.

В AGENTS.md должны находиться только:

- абсолютные production-инварианты;
- команды тестирования;
- правила безопасности;
- правила работы с секретами;
- ссылки на CONTEXT.md;
- ссылки на MASTER_REQUIREMENTS.md;
- ссылки на актуальную SPEC;
- ссылки на ADR;
- stop gates;
- Definition of Done.

5. Создай структуру документации:

docs/yandex-cdn-whitelist/
├── MASTER_REQUIREMENTS.md
├── VERIFIED_FACTS.md
├── RESEARCH.md
├── ARCHITECTURE.md
├── SPEC.md
├── IMPLEMENTATION_PLAN.md
├── TEST_PLAN.md
├── TEST_RESULTS.md
├── CLIENT_COMPATIBILITY.md
├── TRANSPORT_PRESETS.md
├── EDGE_LIFECYCLE.md
├── PANEL_INTEGRATION.md
├── TRAFFIC_METERING.md
├── BILLING.md
├── BILLING_RECONCILIATION.md
├── SECURITY.md
├── PRODUCTION_SAFETY.md
├── DEPLOYMENT.md
├── ROLLBACK.md
└── HANDOFF.md

6. Создай CONTEXT.md с каноническим языком предметной области.

7. Не дублируй одни и те же требования в нескольких документах.
   MASTER_REQUIREMENTS — полный исходный источник.
   Остальные файлы должны ссылаться на него и материализовать
   конкретные решения, факты и результаты.

8. Не останавливайся после подготовки документации.
   После аудита и планирования переходи к безопасной изолированной
   реализации.

9. Не задавай пользователю вопросы, ответы на которые можно получить:

   - из репозитория;
   - с файловой системы;
   - из systemd;
   - из process list;
   - из БД;
   - из конфигураций;
   - из логов;
   - через локальный API;
   - из Git history;
   - из официальной документации;
   - из исходников Xray/3x-ui;
   - из текущей Maestro Panel.

10. Факты — обязанность агента.
    Пользователю передаются только настоящие решения:

    - цена за гигабайт;
    - бизнес-политика лимитов;
    - допустимое снижение совместимости/защиты;
    - разрешение на production cutover;
    - разрешение на миграцию production-БД;
    - разрешение на обновление production 3x-ui;
    - разрешение на production restart;
    - разрешение на real billing;
    - разрешение на OTA.

======================================================================
1. ГЛАВНАЯ БИЗНЕС-ЦЕЛЬ
======================================================================

Нужно построить промышленную систему режима «Белые списки»:

клиентское приложение
→ проверенный shared edge-IP Yandex Cloud CDN:443
→ TLS с нашим SNI
→ HTTP/2
→ XHTTP
→ Yandex Cloud CDN
→ наш origin
→ VLESS/Xray
→ интернет через выбранный зарубежный сервер

Режим предназначен в основном для клиентов из регионов, где мобильный
интернет ограничивается белыми списками.

Функция должна включаться администратором Maestro Panel индивидуально
для каждого клиента по запросу.

По умолчанию:

Белые списки = ВЫКЛЮЧЕНО

Функция должна:

1. Не заменять обычный VPN клиента.
2. Не удалять обычные профили.
3. При включении добавлять отдельные CDN-профили.
4. При выключении удалять только CDN-профили.
5. Работать в MaestroVPN.
6. Генерировать стандартные подписки для сторонних клиентов.
7. Поддерживать Karing, Incy, Happ после полной проверки.
8. Позволять добавлять другие страны и origins.
9. Не требовать отдельного CDN-ресурса для каждого пользователя.
10. Позволять отозвать доступ одному пользователю.
11. Не влиять на остальных пользователей.
12. Не зависеть от Cloudflare.
13. Поддерживать server-side тарификацию по гигабайтам.
14. Позволять задавать стоимость 1 ГБ:
    - глобально;
    - для transport profile;
    - для тарифного плана;
    - индивидуально для клиента.
15. При нехватке баланса или достижении лимита отключать только
    white-list entitlement.
16. Оставлять обычный VPN доступным.
17. Считать трафик независимо от используемого клиентского приложения.
18. Не позволять обходить тарификацию переходом на Karing/Incy/Happ.
19. Легко управляться через Maestro Panel, а не через ручное
    редактирование JSON.

WDTT, qWDTT, CSQTT и OLCTRC сейчас отложены.

В рамках этой задачи:

- не исследовать их заново;
- не переписывать;
- не удалять;
- не ломать;
- не включать их в новый transport rollout.

======================================================================
2. НЕИЗМЕНЯЕМЫЕ PRODUCTION-ИНВАРИАНТЫ
======================================================================

Существующие рабочие подключения клиентов являются абсолютным
приоритетом.

Без отдельного явного подтверждения запрещено:

- изменять существующие UUID;
- менять рабочие URI клиентов;
- менять существующие порты;
- менять существующие server addresses;
- удалять production inbounds;
- отключать production clients;
- останавливать production Xray;
- перезапускать production 3x-ui;
- обновлять production 3x-ui;
- обновлять production Xray внутри 3x-ui;
- мигрировать production-БД;
- перезаписывать существующую БД;
- переписывать существующий firewall;
- занимать используемые порты;
- заменять существующий Nginx default config;
- перезапускать сервер;
- обновлять ядро;
- запускать \`apt full-upgrade\`;
- выполнять production OTA;
- включать реальное автоматическое списание денег;
- удалять старую реализацию;
- удалять backups;
- удалять существующий контент;
- создавать повторные Yandex CDN-ресурсы без доказанной необходимости.

Нельзя:

- списывать деньги задним числом;
- считать обычный VPN-трафик CDN-трафиком;
- блокировать весь аккаунт из-за CDN-лимита;
- использовать float для денег;
- создавать двойные начисления;
- доверять client-side счётчику как финансовому source of truth;
- тарифицировать по IP пользователя;
- публиковать origin IP в подписке;
- публиковать server decryption;
- логировать секретные URI;
- логировать payload;
- логировать посещаемые домены для billing;
- публиковать непроверенный edge-IP;
- автоматически применять непроверенную миграцию.

Вся новая реализация сначала создаётся:

- в отдельной Git branch/worktree;
- на отдельном порту;
- в отдельном процессе;
- с отдельным config;
- с отдельными логами;
- с отдельным rollback;
- без воздействия на production.

======================================================================
3. ПРОВЕРЕННАЯ ЧУЖАЯ СХЕМА
======================================================================

Была исследована чужая действующая подписка.

В ней используются shared Yandex Cloud CDN edge-IP:

- 188.72.111.7
- 188.72.111.19
- 188.72.111.35

Это не зарубежные VPS и не персональные origins.

Клиент подключается к буквальному IP Yandex CDN, но отдельно передаёт:

- TLS SNI;
- HTTP Host.

Страны выбираются через разные CDN hostnames:

- cdn-nl.file-racing.online
- cdn-fi.file-racing.online
- cdn-ru.file-racing.online
- cdn-mv.file-racing.online

Для каждой страны используется несколько альтернативных edge-IP.

Транспорт референсной схемы:

- Xray 26.7.28;
- VLESS;
- VLESS Encryption;
- \`mlkem768x25519plus.native.0rtt.<client-material>\`;
- соответствующий server decryption;
- XHTTP;
- mode \`packet-up\`;
- GET с body;
- HTTP/2;
- внешний TLS;
- XMUX;
- fingerprint \`firefox\`;
- ALPN с \`h2\`.

Подтверждённый path имеет вид:

\`/static/main/video/segment.ts/<secret-suffix>\`

Метаданные XHTTP:

- \`auth=<session-id>\`;
- \`chunk_id=<sequence>\`;
- session и sequence передаются в query;
- uplink передаётся через GET body.

Из логов Incy подтверждено:

- запуск Xray 26.7.28;
- \`XHTTP is dialing\`;
- \`mode packet-up\`;
- HTTP version 2;
- Host CDN-домена;
- создание XMUX client;
- GET-запросы с auth и chunk_id;
- туннелирование TCP;
- туннелирование UDP.

Cloudflare в этой схеме не используется.

Не копировать неизвестные значения вслепую.

Все поля необходимо сверить с исходниками закреплённой версии Xray.

======================================================================
4. УЖЕ СОЗДАННЫЙ НАШ ТЕСТОВЫЙ YANDEX CDN
======================================================================

Yandex Cloud folder:

maestrocdn-test

Certificate Manager certificate:

maestrocdn-wapmixx-test-cert

Домен сертификата:

cdn-test.wapmixx.ru

Статус сертификата:

Issued

DNS validation:

_acme-challenge.cdn-test.wapmixx.ru
CNAME
fpq6tsquvmiqfb1ksp8v.cm.yandexcloud.net.

Cloud CDN public hostname:

cdn-test.wapmixx.ru

Provider CNAME:

64b33aab8eb3217b.topology.gslb.yccdn.ru.

Рабочая DNS-запись:

cdn-test.wapmixx.ru
CNAME
64b33aab8eb3217b.topology.gslb.yccdn.ru.

Текущий origin:

193.17.183.48:18080

CDN → origin protocol:

HTTP

Origin Host:

cdn-test.wapmixx.ru

Клиент → CDN:

TLS 1.2+
HTTP/2

CDN resource settings:

- кеширование CDN выключено;
- browser cache выключен;
- gzip выключен;
- large-file segmentation выключена;
- разрешены GET, HEAD, OPTIONS;
- origin shielding выключен;
- CDN log export выключен;
- protected token выключен;
- IP restriction выключена.

На origin существует диагностический сервис:

Service:

maestro-cdn-probe.service

File:

/opt/maestro-cdn-probe/server.py

Port:

18080

Этот сервис не удалять до полного production sign-off и завершения
rollback window.

Он является проверенным fallback origin.

======================================================================
5. ДОКАЗАННЫЕ НАМИ ТЕХНИЧЕСКИЕ ФАКТЫ
======================================================================

Эти пункты являются acceptance criteria, а не предположениями.

1. Клиент подключается к Yandex CDN по HTTPS/HTTP2.

2. Текущий обнаруженный edge:

188.72.103.4

Он не считается вечным.

3. Yandex CDN обращается к origin по HTTP/1.1.

4. Наблюдались origin-side addresses:

188.72.113.1
188.72.113.2

Они не являются полным постоянным allowlist.

5. Yandex передаёт GET body без изменения.

Проверено дважды на случайных телах размером:

262144 bytes

SHA-256 на отправителе и origin совпал байт-в-байт.

6. Сохраняются:

- Host;
- Content-Length;
- query string;
- auth;
- chunk_id;
- Content-Type.

7. На origin приходят:

- \`Via: Yandex-CDN\`;
- \`CDN-Loop: yandex\`;
- \`X-Request-Id\`;
- \`X-Forwarded-Proto\`;
- \`X-Forwarded-Host\`;
- \`X-Forwarded-For\`.

8. Выключенный CDN cache действительно не возвращает старый ответ.

9. Активный streaming response работает минимум 180 секунд.

Результат:

- HTTP 200;
- HTTP/2;
- TTFB около 0.55 секунды;
- total около 180.6 секунды;
- получены все 180 сообщений.

10. Literal edge-IP + отдельные SNI/Host работают.

Проверено:

address:
188.72.103.4

port:
443

SNI:
cdn-test.wapmixx.ru

Host:
cdn-test.wapmixx.ru

Запрос успешно дошёл до:

193.17.183.48:18080

11. Полностью молчащий downstream закрывается Yandex CDN примерно
через 60 секунд.

Измерение:

- HTTP 200;
- HTTP/2;
- первый байт около 0.404 секунды;
- соединение закрыто примерно через 60.338 секунды;
- начальное сообщение получено;
- сообщение после 180 секунд тишины не получено.

Эту проблему нельзя считать решённой Nginx timeout или HTTP/2 PING.

======================================================================
6. ИНЖЕНЕРНАЯ СВОБОДА
======================================================================

Проверенный baseline:

VLESS
→ VLESS Encryption
→ XHTTP packet-up
→ GET body
→ TLS/HTTP2
→ Yandex Cloud CDN
→ HTTP/1.1 origin
→ Xray

Baseline должен быть сохранён как fallback.

Но финальная реализация может быть лучше.

Разрешено исследовать и реализовывать:

- более новую совместимую версию Xray;
- отдельный Xray sidecar;
- контейнерный data plane;
- blue-green Xray;
- direct CDN → Xray;
- Nginx → Xray;
- Caddy/HAProxy/Envoy gateway;
- отдельный Maestro data-plane agent;
- безопасный Xray fork;
- core-level downlink recovery;
- resumable downlink;
- versioned transport extensions;
- automatic edge rotation;
- capability-based profiles;
- более точный billing service;
- durable event queue;
- transactional outbox;
- event-sourced ledger;
- автоматизированный canary;
- автоматизированный rollback.

Новое решение допустимо только если:

1. Основано на исходниках и тестах.
2. Имеет ADR.
3. Развёрнуто изолированно.
4. Имеет compatibility fallback.
5. Имеет тесты.
6. Имеет rollback.
7. Проходит реальные Yandex CDN tests.
8. Не меняет production без подтверждения.
9. Не ломает стандартные subscriptions.
10. Не ломает сторонние клиенты без явного решения.
11. Не снижает защиту незаметно.
12. Доказывает преимущества измерениями.

Фразы вроде «архитектурно лучше» без тестов недостаточно.

======================================================================
7. ИНЖЕНЕРНЫЙ WORKFLOW ПО МОТИВАМ MATT POCOCK
======================================================================

Изучи публичные репозитории профиля:

https://github.com/mattpocock

Особенно, если они существуют и актуальны:

- skills;
- agent-rules-books;
- course-video-manager;
- slopwatch;
- resumable-stream;
- sandcastle;
- agent-browser.

Не копируй их вслепую.

Используй полезные принципы:

1. Короткий AGENTS.md.
2. Progressive disclosure.
3. CONTEXT.md как канонический язык.
4. ADR для реальных trade-offs.
5. Wayfinder для открытых решений.
6. Spec до реализации.
7. Вертикальные tracer-bullet tickets.
8. TDD по внешним seam.
9. Tight feedback loop до гипотез.
10. Изолированные worktrees/sandboxes.
11. Два независимых review:
    - Standards review;
    - Spec review.
12. Immutable release lifecycle.
13. Listener → central server для metering.
14. Handoff через ссылки на артефакты, а не копирование контекста.

Если импортируются project-local skills:

- проверить исходный код;
- проверить лицензию;
- сохранить source commit SHA;
- не включать auto-update;
- не запускать непроверенный installer на production;
- не давать skills production secrets;
- хранить lock-документ.

Предпочтительные skills:

- research;
- diagnosing-bugs;
- domain-modeling;
- codebase-design;
- wayfinder;
- to-spec;
- to-tickets;
- tdd;
- implement;
- code-review;
- wizard;
- handoff.

Не превращать workflow в бесконечное планирование.

После принятия решений переходить к коду.

======================================================================
8. КАНОНИЧЕСКИЙ DOMAIN LANGUAGE
======================================================================

Создай CONTEXT.md без implementation details.

Раздели минимум:

Customer
- человек или организация, покупающая услугу.

Device
- конкретное пользовательское устройство.

VPN Account
- учётная запись Maestro, владеющая подпиской.

Xray User
- серверная identity/UUID в конкретном data plane.

White-List Entitlement
- право VPN Account использовать режим белых списков.

Transport Profile
- логическая конфигурация public host, origin и transport.

Compatibility Preset
- набор параметров под возможности конкретного клиента/core.

Origin Route
- маршрут CDN → origin.

Edge Candidate
- обнаруженный, но ещё не разрешённый edge-IP.

Approved Edge
- edge-IP, прошедший проверки и разрешённый в подписках.

Transport Release
- неизменяемая версия data-plane config.

Meter Epoch
- эпоха процесса Xray между сбросами cumulative counters.

Usage Sample
- один снимок cumulative counters.

Usage Interval
- вычисленная дельта между samples.

Billing Period
- расчётный период.

Tariff Snapshot
- неизменяемые условия тарифа для конкретого начисления.

Ledger Entry
- неизменяемая финансовая операция.

Suspension
- приостановка только white-list entitlement.

Grace
- разрешённый запас времени/трафика/баланса.

Canary
- ограниченная тестовая публикация.

Rollback Point
- проверенная предыдущая версия.

Не использовать одно слово «клиент» одновременно для:

- человека;
- приложения;
- устройства;
- UUID;
- подписки.

======================================================================
9. ОБЯЗАТЕЛЬНЫЕ ADR
======================================================================

Создай ADR минимум для:

1. Data-plane architecture.
2. 3x-ui integration vs isolation.
3. Xray version strategy.
4. Transport preset strategy.
5. 60-second idle solution.
6. Transport Release lifecycle.
7. Per-user identity.
8. Dynamic AddUser/RemoveUser.
9. Edge discovery/approval.
10. Subscription rendering.
11. Traffic source of truth.
12. Meter epoch handling.
13. Billing ledger.
14. Billing suspension.
15. Origin security.
16. Production rollout.
17. Rollback.

ADR должен содержать:

- проблему;
- ограничения;
- варианты;
- плюсы;
- минусы;
- риски;
- совместимость;
- testing;
- rollback;
- выбранный вариант;
- доказательства.

======================================================================
10. ПЕРВЫЙ ЭТАП — READ-ONLY AUDIT
======================================================================

До первого изменения выполни полный read-only audit.

Найди:

- все репозитории Maestro;
- ветки;
- git status;
- незакоммиченные изменения;
- backend;
- frontend панели;
- Android app;
- Telegram-боты;
- API;
- subscription generator;
- billing;
- wallet;
- payments;
- notifications;
- database;
- migrations;
- cron;
- workers;
- queues;
- deployment scripts;
- CI/CD;
- secrets handling;
- server inventory;
- 3x-ui;
- Xray;
- reverse proxies;
- certificates;
- firewall;
- backups;
- monitoring.

Зафиксируй:

- точную версию 3x-ui;
- точную версию production Xray;
- пути binary;
- SHA-256 binaries;
- systemd units;
- PID;
- uptime;
- listening ports;
- panel port 2053;
- inbounds;
- client counts;
- UUID;
- configs;
- DB path;
- SQLite integrity;
- API availability;
- current subscription output;
- current balances;
- current tariffs;
- ordinary billing state;
- current resource usage.

Не выводи в отчёт:

- пароли;
- private keys;
- токены;
- полные клиентские URI;
- server decryption;
- API secrets.

Создай baseline report и machine-readable snapshot.

======================================================================
11. BACKUP И ДОКАЗАННОЕ ВОССТАНОВЛЕНИЕ
======================================================================

До изменений создай backup:

- 3x-ui DB;
- Xray configs;
- binaries;
- systemd units;
- Nginx/Caddy configs;
- certificates metadata;
- subscription templates;
- backend DB;
- migrations state;
- env templates без раскрытия секретов;
- firewall rules;
- panel build;
- Android relevant config;
- current port inventory.

Для каждого backup:

- timestamp;
- source path;
- destination path;
- SHA-256;
- size;
- owner;
- permissions.

Не считать backup проверенным, пока не выполнено:

- чтение архива;
- checksum verification;
- SQLite integrity;
- test restore в изолированную директорию;
- config validation;
- документированная restore-команда.

======================================================================
12. PRODUCTION NON-REGRESSION BASELINE
======================================================================

Создай одну команду:

scripts/verify-production-baseline.sh

Она должна проверять:

- 3x-ui доступна на 2053;
- production Xray PID;
- production Xray uptime;
- listening ports;
- количество inbounds;
- количество users;
- UUID fingerprints без раскрытия UUID;
- subscription regression fixtures;
- ordinary API;
- ordinary billing;
- current balances checksum;
- firewall;
- systemd state;
- health ordinary VPN profiles.

Запускай baseline:

- до изменения;
- после изменения;
- до canary;
- после canary;
- после rollback.

При неожиданной разнице:

1. Остановить rollout.
2. Сохранить evidence.
3. Выполнить rollback.
4. Не чинить production серией случайных изменений.
5. Создать incident report.

======================================================================
13. TARGET DATA-PLANE BASELINE
======================================================================

Самый безопасный начальный baseline:

/opt/maestro-xray-cdn/

Systemd:

maestro-xray-cdn.service

Тестовый origin port:

18081

Diagnostic probe остаётся:

18080

Начальная схема:

Yandex CDN
→ 193.17.183.48:18081
→ isolated Xray sidecar

Существующая 3x-ui остаётся нетронутой.

Sidecar должен иметь:

- отдельный binary;
- pinned version;
- source commit/version;
- checksum;
- отдельный config;
- отдельный logs path;
- отдельный runtime directory;
- отдельный API listener;
- отдельный stats;
- отдельный systemd unit;
- \`Restart=on-failure\`;
- \`LimitNOFILE\`;
- минимальные permissions;
- config validation;
- atomic release promotion;
- rollback.

Если direct Xray не обеспечивает нужную защиту/маршрутизацию,
разрешено добавить отдельный gateway:

Yandex CDN
→ Nginx/Caddy на 18081
→ Xray на 127.0.0.1:18082

Но:

- не менять existing default site;
- создать отдельный config;
- \`nginx -t\` перед reload;
- не занимать production 80/443;
- не считать Nginx timeout решением idle cutoff.

======================================================================
14. IMMUTABLE TRANSPORT RELEASES
======================================================================

Не редактировать активный Xray config на месте.

Реализовать:

Draft
→ Candidate
→ Published
→ Retired

Пример:

/opt/maestro-xray-cdn/releases/<release-id>/
├── xray
├── config.json
├── clients-manifest.json
├── edge-set.json
├── subscriptions-manifest.json
├── build-info.json
├── checksums.sha256
├── validation-report.json
├── compatibility-report.json
└── rollback.json

Активная версия:

/opt/maestro-xray-cdn/current
→ releases/<release-id>

Published release immutable.

Candidate обязан пройти:

1. JSON validation.
2. Xray config test.
3. Isolated start.
4. Local VLESS test.
5. Direct origin test.
6. Yandex CDN GET body test.
7. Literal edge-IP test.
8. Client import test.
9. Per-user stats test.
10. Subscription regression.
11. Billing identity test.
12. Production baseline comparison.

Только затем atomic promote.

Предыдущая published-версия остаётся rollback point.

======================================================================
15. XRAY VERSION STRATEGY
======================================================================

Xray 26.7.28 является доказанной compatibility baseline, а не
обязательной финальной версией.

Изучи:

- текущий upstream Xray;
- изменения после 26.7.28;
- VLESS Encryption;
- XHTTP GET body;
- XHTTP fields;
- packet-up;
- XMUX;
- Stats API;
- HandlerService;
- AddUser/RemoveUser;
- idle-downstream fixes;
- compatibility with Karing/Incy/Happ.

Выбери версию на основе тестов.

Для каждой версии сохраняй:

- version;
- commit SHA;
- build method;
- checksum;
- release source;
- compatibility matrix;
- rollback binary.

Не обновлять production Xray ради новой функции, пока sidecar не доказан.

======================================================================
16. TRANSPORT PRESETS
======================================================================

Создай versioned capability-based presets.

Минимум:

A. MAESTRO_ADVANCED

- VLESS Encryption;
- XHTTP packet-up;
- GET body;
- literal Yandex edge-IP;
- custom SNI;
- custom Host;
- secret path;
- custom session/seq metadata;
- проверенный padding;
- проверенный XMUX;
- Maestro heartbeat/watchdog или более сильный core fix.

B. GENERIC_XHTTP_GET_COMPAT

- стандартный VLESS;
- XHTTP packet-up;
- GET body;
- настройки, поддерживаемые сторонними клиентами;
- может использовать decryption=none только как явно маркированный
  compatibility fallback.

C. EXPERIMENTAL_RESUMABLE

- только для экспериментального core-level решения;
- feature negotiation;
- стандартный fallback;
- не публиковать обычным клиентам без проверки.

Не смешивать presets.

Не снижать VLESS Encryption незаметно.

В панели показывать:

- preset name;
- version;
- capabilities;
- protection level;
- supported clients;
- experimental flag.

======================================================================
17. VLESS ENCRYPTION
======================================================================

Проверь точную семантику VLESS Encryption выбранной версии.

Не предполагай без проверки:

- одна ли encryption pair на inbound;
- одна ли pair на transport profile;
- возможна ли pair на пользователя;
- как это взаимодействует с UUID;
- как это взаимодействует с dynamic AddUser;
- как это влияет на stats.

Используй официальный инструмент выбранного Xray, например:

\`xray vlessenc\`

Не смешивай server/client пары.

Никогда не помещай server decryption в subscription.

Секреты хранить:

- через существующий secrets mechanism;
- либо защищённые файлы с минимальными permissions;
- не в открытом frontend/API;
- не в Git;
- не в audit log.

======================================================================
18. XHTTP BASELINE
======================================================================

Проверенный baseline должен поддерживать:

network:
xhttp

mode:
packet-up

uplinkHTTPMethod:
GET

uplinkDataPlacement:
body

session metadata:
query

sequence metadata:
query

public host:
cdn-test.wapmixx.ru

secret path:
криптографически случайный путь

Не копировать пустые defaults из видео/чужого конфига.

Сверить в исходниках выбранной версии:

- xPaddingBytes;
- xPaddingObfsMode;
- xPaddingKey;
- xPaddingHeader;
- xPaddingPlacement;
- xPaddingMethod;
- sessionIDPlacement;
- sessionIDKey;
- sessionIDLength;
- seqPlacement;
- seqKey;
- uplinkDataPlacement;
- uplinkDataKey;
- uplinkChunkSize;
- scMaxEachPostBytes;
- scMinPostsIntervalMs;
- scMaxBufferedPosts;
- scStreamUpServerSecs;
- serverMaxHeaderBytes;
- noSSEHeader;
- noGRPCHeader;
- xmux;
- enableXmux.

Создай автоматические tests для schema/types.

======================================================================
19. 60-СЕКУНДНЫЙ IDLE CUTOFF
======================================================================

Не ограничиваться одним решением.

Исследовать:

A. MaestroVPN tunnel heartbeat.

- каждые 25–35 секунд;
- jitter;
- запрос идёт именно через VPN;
- ответ проходит через тот же transport;
- timeout 8–10 секунд;
- controlled redial;
- backoff;
- Doze/sleep support;
- network change support.

Не считать heartbeat работающим, пока не доказано, что его ответ
проходит через ту же XHTTP downlink session и сбрасывает CDN timeout.

B. Watchdog + session redial.

C. Standard Xray Mux / mux.cool.

Отдельно отличать:

- XHTTP XMUX;
- Xray protocol Mux.

D. Core-level solution.

Исследовать:

- framed keepalive;
- downlink sequence;
- resume token;
- last acknowledged offset;
- reopen downlink GET;
- session resume;
- dead-downlink watchdog;
- feature negotiation;
- backwards compatibility.

Допускается fork Xray, если:

- patch включается feature flag;
- есть unit tests;
- есть protocol integration tests;
- есть standard fallback;
- есть reproducible build;
- есть client support;
- есть Yandex CDN tests;
- есть rollback;
- stable clients не ломаются.

Heartbeat/watchdog остаётся обязательным fallback, пока более сильное
решение не доказано.

======================================================================
20. YANDEX EDGE REGISTRY
======================================================================

Не жёстко привязываться только к:

188.72.103.4

Создай Edge Registry.

Поля:

- id;
- transport_profile_id;
- ip;
- source;
- resolver;
- discovered_at;
- last_checked_at;
- last_success_at;
- last_failure_at;
- consecutive_successes;
- consecutive_failures;
- tls_ok;
- certificate_ok;
- sni_ok;
- host_ok;
- origin_ok;
- operator_test_status;
- score;
- state;
- approved_for_subscription;
- approved_by;
- approved_at;
- quarantine_reason;
- notes.

Состояния:

DISCOVERED
PROBING
CANDIDATE
APPROVED
DEGRADED
QUARANTINED
RETIRED

Discovery:

- provider CNAME;
- public hostname;
- несколько resolvers;
- разные географические probes;
- операторские тесты;
- manual import.

Перед APPROVED:

1. TCP 443.
2. TLS handshake.
3. Certificate.
4. SNI.
5. Host.
6. Health endpoint.
7. Origin proof.
8. Несколько успешных проверок.
9. Проверка во времени.
10. Canary.

Не публиковать IP после единственного DNS-ответа.

В подписке допускаются несколько nodes с:

- одним UUID;
- одним Host/SNI;
- одним path;
- одной encryption конфигурацией;
- разными APPROVED edge-IP.

Добавить domain-based fallback node отдельно.

======================================================================
21. 3X-UI STRATEGY
======================================================================

3x-ui не является обязательной основой новой системы.

Сначала audit.

Проверить:

- current version;
- current Xray version;
- DB migrations;
- raw inbound support;
- new JSON fields preservation;
- API;
- user stats;
- AddUser/RemoveUser;
- subscription integration;
- traffic counters;
- custom core binary;
- backup/restore.

Допустимые результаты:

A. Production 3x-ui безопасно обновляется.

B. Обновляется только Xray core после стенда.

C. Production 3x-ui не меняется, новая система живёт отдельно.

Предпочтение:

- sidecar/data plane;
- затем обновление только core;
- полное обновление панели — только при доказанной необходимости.

Для проверки обновления 3x-ui:

1. Копия DB.
2. Изолированный instance.
3. Отключённые production inbounds.
4. Migration.
5. Сравнение clients/UUID/configs.
6. Subscription comparison.
7. Traffic counters comparison.
8. Restore test.
9. Rollback до 5 минут.

Не выполнять production update без подтверждения.

======================================================================
22. MAESTRO CONTROL PLANE
======================================================================

Новая система должна управляться из Maestro Panel.

Не создавать независимый второй список клиентов.

Сначала найти существующий source of truth.

Panel должна управлять:

- customers/accounts;
- entitlements;
- transport profiles;
- origins;
- countries;
- data-plane instances;
- compatibility presets;
- Xray users;
- encryption material references;
- edge registry;
- subscriptions;
- health;
- releases;
- canaries;
- traffic;
- tariffs;
- limits;
- suspensions;
- audit;
- rollback.

Не оставлять основной workflow в виде:

- вручную открыть JSON;
- вручную добавить UUID;
- вручную перезапустить Xray;
- вручную заменить edge-IP;
- вручную посчитать трафик.

CLI/runbook допустим только как аварийный fallback.

======================================================================
23. ENTITLEMENT MODEL
======================================================================

Добавь логическую сущность White-List Entitlement.

Поля адаптировать под текущую БД:

- id;
- vpn_account_id;
- transport_profile_id;
- compatibility_preset_id;
- enabled;
- state;
- enabled_at;
- enabled_by;
- disabled_at;
- disabled_by;
- optional_expires_at;
- reason;
- notes;
- billing_enabled;
- tariff_id;
- individual_price_override;
- traffic_basis_override;
- included_bytes_override;
- soft_limit_override;
- hard_limit_override;
- grace_override;
- auto_suspend_override;
- auto_resume_override;
- suspension_reason;
- billing_started_at;
- billing_stopped_at.

States:

DISABLED
PROVISIONING
ACTIVE
GRACE
SUSPENDED
ERROR
EXPIRED

При DISABLED/SUSPENDED:

- ordinary subscription остаётся;
- CDN nodes не публикуются;
- ordinary VPN работает;
- другие entitlements не затрагиваются;
- usage/ledger history не удаляется.

======================================================================
24. PANEL UI
======================================================================

В карточке клиента добавить:

Белые списки / Yandex CDN

Поля:

- ВКЛ/ВЫКЛ;
- state;
- transport profile;
- country;
- compatibility preset;
- active edges;
- health;
- data-plane release;
- дата включения;
- кто включил;
- срок действия;
- reason;
- notes;
- проверить подписку;
- suspend;
- resume;
- disable;
- audit history.

Раздел:

Тарификация трафика

Поля:

- billing ВКЛ/ВЫКЛ;
- режим:
  - бесплатно;
  - наследовать;
  - по гигабайтам;
- тариф;
- цена за 1 ГБ;
- валюта;
- source цены;
- GB/GiB;
- downlink-only или uplink+downlink;
- included traffic;
- used traffic;
- billable traffic;
- remaining included;
- soft limit;
- hard limit;
- grace;
- auto suspend;
- auto resume;
- current billing period;
- accrued amount;
- posted amount;
- balance;
- estimated remaining traffic;
- last meter sample;
- data freshness;
- collector health;
- Xray health;
- suspension reason;
- corrections;
- ledger history.

Изменения тарифа требуют:

- permission;
- reason;
- audit log;
- old/new values.

======================================================================
25. STANDARD SUBSCRIPTIONS
======================================================================

Если entitlement OFF:

- ordinary subscription должна оставаться функционально идентичной;
- CDN nodes отсутствуют;
- обычные UUID не меняются.

Если entitlement ACTIVE:

- ordinary nodes сохраняются;
- CDN nodes добавляются;
- ordinary nodes не заменяются.

CDN node:

- protocol VLESS;
- address = APPROVED literal Yandex edge-IP;
- port = 443;
- security = TLS;
- SNI = public CDN host;
- XHTTP Host = public CDN host;
- XHTTP path = secret path;
- mode = packet-up;
- encryption = client encryption;
- ALPN = проверенный вариант;
- fingerprint = проверенный вариант;
- extra = корректно URL-encoded JSON;
- label явно помечен как БС/Yandex.

Добавить domain fallback node.

Не помещать в URI:

- origin IP;
- server decryption;
- billing values;
- internal client ID;
- internal Xray email;
- admin notes.

Проверить:

- base64 subscription;
- plain URI list;
- UTF-8;
- escaping;
- long URI;
- QR;
- refresh;
- duplicate prevention;
- import/reimport;
- revocation;
- cache invalidation.

Смена приложения не должна менять billing identity.

======================================================================
26. CLIENT COMPATIBILITY MATRIX
======================================================================

Обязательные:

1. MaestroVPN.
2. Karing.
3. Incy.
4. Happ.

Дополнительные кандидаты:

- v2rayNG;
- v2rayN;
- Shadowrocket.

Для каждого:

- app version;
- core version;
- preset;
- import;
- subscription refresh;
- TLS;
- VLESS Encryption;
- XHTTP GET body;
- TCP;
- UDP;
- DNS;
- Speedtest;
- 5-minute download;
- 5-minute upload;
- 90-second idle;
- recovery;
- Wi-Fi → mobile;
- mobile → Wi-Fi;
- sleep/wake;
- cold start on mobile;
- literal edge-IP;
- stats attribution;
- billing attribution.

Statuses:

SUPPORTED
SUPPORTED_WITH_SETTING
EXPERIMENTAL
IMPORT_ONLY_UNSTABLE
UNSUPPORTED

Импорт ссылки не равен поддержке.

Референсная чужая подписка работает в Karing, но нашу конфигурацию
считать неподтверждённой до теста.

======================================================================
27. MAESTROVPN ANDROID
======================================================================

Добавь state-aware white-list transport management.

Приложение получает от API:

- entitlement state;
- selected transport profile;
- edges;
- preset;
- billing state;
- usage;
- remaining limit;
- suspension reason.

Не использовать нестандартный URI-флаг как единственный source.

Реализовать:

- heartbeat через VPN;
- jitter;
- watchdog;
- controlled redial;
- exponential backoff;
- network callback;
- Wi-Fi/mobile transition;
- Doze handling;
- sleep/wake;
- process recreation;
- stale session cleanup;
- edge failover;
- telemetry без секретов.

Heartbeat:

- должен идти через VPN;
- должен получать маленький ответ;
- должен доказанно поддерживать downlink;
- не должен обходить TUN;
- timeout 8–10 секунд;
- интервал 25–35 секунд с jitter;
- не создавать синхронный storm клиентов.

При failure:

- закрыть мёртвую сессию;
- очистить connection pool;
- controlled redial;
- сменить edge при необходимости;
- не зациклить быстрые reconnect.

Client-side usage — только визуальная подсказка.

Финансовый source of truth — backend metering.

При CDN suspension:

- обычный VPN остаётся;
- показать понятную причину;
- показать usage/limit/balance;
- предложить пополнение или обращение к администратору.

Сначала test build.

Production OTA — только после подтверждения.

======================================================================
28. PER-USER SERVER-SIDE METERING
======================================================================

Тарификация должна работать одинаково для:

- MaestroVPN;
- Karing;
- Incy;
- Happ;
- любого совместимого клиента.

Использовать Xray per-user cumulative stats.

Каждому Xray User назначить стабильный внутренний stats identity,
например:

wl:<opaque-internal-id>

Не использовать реальный email.

Не помещать identity в subscription.

Собирать:

- uplink cumulative bytes;
- downlink cumulative bytes.

Stats API:

- только localhost или Unix socket;
- не открывать наружу;
- минимальные permissions;
- не логировать payload.

Не использовать:

- IP пользователя;
- Yandex aggregate как индивидуальный source;
- Android counters;
- общий interface traffic.

======================================================================
29. METERING ARCHITECTURE
======================================================================

Предпочтительный pattern:

Xray Meter Listener на каждом server
→ normalized Usage Events
→ Central Metering Server
→ Billing Ledger

Listener:

- читает cumulative counters;
- знает process/meter epoch;
- формирует stable event ID;
- не рассчитывает деньги;
- не хранит payload;
- повторяет доставку безопасно;
- имеет local durable spool при необходимости.

Metering Server:

- единственный writer финансового ledger;
- проверяет idempotency;
- вычисляет deltas;
- применяет included traffic;
- выбирает tariff snapshot;
- создаёт usage interval;
- создаёт ledger entry;
- управляет suspension;
- выполняет reconciliation.

Можно выбрать другую архитектуру, если она надёжнее и доказана ADR.

======================================================================
30. METER EPOCH И COUNTER RESET
======================================================================

Xray counters могут сбрасываться при restart.

Нужен Meter Epoch.

Для каждого Xray runtime:

- instance ID;
- epoch ID;
- process start time;
- binary checksum;
- config release ID.

Collector хранит:

- last cumulative uplink;
- last cumulative downlink;
- last sample time;
- epoch.

При новом epoch:

- не создавать отрицательную delta;
- закрыть старый epoch;
- начать новый baseline;
- не списывать предыдущий трафик повторно;
- создать diagnostic event.

Не применять \`reset=true\` без доказанной атомарной схемы.

Предпочитать cumulative counters + snapshots.

======================================================================
31. BILLING UNITS
======================================================================

Хранить traffic в целых bytes.

Поддержать:

GB_DECIMAL:
1 GB = 1 000 000 000 bytes

GIB_BINARY:
1 GiB = 1 073 741 824 bytes

Показывать единицу явно.

Не использовать float.

Money:

- integer minor units;
- либо Decimal/fixed-point;
- либо rational calculation.

Не округлять каждый маленький interval до копейки.

Хранить:

- exact calculated amount;
- posted amount;
- rounding rule.

======================================================================
32. TARIFF HIERARCHY
======================================================================

Эффективная цена определяется:

1. Individual client override.
2. Tariff plan.
3. Transport profile default.
4. Global default.

Если цена нигде не задана:

- не публиковать платный CDN-профиль;
- либо требовать явного FREE mode;
- не считать случайный zero бесплатным режимом.

Настройки:

- price per GB/GiB;
- currency;
- traffic basis;
- included bytes;
- soft limit;
- hard limit;
- grace bytes;
- billing period;
- insufficient balance policy;
- hard limit policy;
- auto resume;
- notification thresholds.

Изменение цены действует только на новый usage.

Каждый Usage Interval хранит Tariff Snapshot.

======================================================================
33. TRAFFIC BASIS
======================================================================

Поддержать:

DOWNLINK_ONLY

UPLINK_PLUS_DOWNLINK

FREE

Опционально CUSTOM только при явной необходимости.

По умолчанию бизнес-решение не принимать самостоятельно.
Подготовить варианты и расчёты себестоимости.

В UI показывать effective basis.

======================================================================
34. BILLING DATA MODEL
======================================================================

Адаптировать под существующую БД.

Не создавать дубликаты существующего wallet/ledger.

Логически нужны:

TransportProfile
WhiteListEntitlement
MeterEpoch
TrafficCounterSnapshot
UsageSample
UsageInterval
BillingPeriod
TariffVersion
TariffSnapshot
LedgerEntry
Adjustment
Suspension
ReconciliationReport
AuditEvent

Usage Interval:

- account;
- entitlement;
- transport;
- Xray identity;
- instance;
- epoch;
- interval start/end;
- uplink delta;
- downlink delta;
- billable bytes;
- basis;
- tariff snapshot;
- calculated amount;
- status;
- idempotency key.

Ledger immutable.

Исправления:

- reversal;
- adjustment;
- refund;
- bonus.

Не редактировать старые financial entries.

======================================================================
35. BILLING COLLECTOR
======================================================================

Базовый interval:

30–60 секунд, конфигурируемо.

Collector должен:

1. Получать cumulative stats пакетно.
2. Определять epoch.
3. Сравнивать snapshot.
4. Вычислять positive delta.
5. В одной транзакции:
   - сохранить sample;
   - сохранить interval;
   - обновить period;
   - создать ledger operation;
   - обновить balance, если real charging включён.
6. Использовать idempotency key.
7. Выдерживать retry/replay.
8. Не терять трафик после restart.
9. Не создавать двойное списание.
10. Не обнулять данные при API error.
11. Показывать STALE при устаревших данных.
12. Переводить billing в REVIEW_REQUIRED при аномалии.
13. Не массово отключать clients при временной ошибке.

Предпочтительно сначала SHADOW billing.

======================================================================
36. SHADOW BILLING
======================================================================

До реального списания:

- измерять трафик;
- вычислять стоимость;
- показывать в панели;
- не менять баланс;
- сравнивать с known-size tests;
- сравнивать с Yandex aggregate;
- проверять restart/replay/idempotency;
- мониторить минимум 48 часов.

Real charging включается только после отдельного подтверждения.

======================================================================
37. INCLUDED TRAFFIC И LIMITS
======================================================================

Поддержать:

- included bytes;
- soft limit;
- hard limit;
- grace bytes;
- grace time.

Формула:

billable_bytes =
max(0, measured_bytes - remaining_included_bytes)

Included traffic не должен повторно применяться после restart.

Soft limit:

- уведомление;
- без автоматического отключения, если не выбрано.

Hard limit:

- приостановить только white-list entitlement;
- ordinary VPN остаётся;
- записать причину;
- сохранить usage/ledger;
- уведомить.

======================================================================
38. BALANCE И SUSPENSION
======================================================================

Найди существующий wallet/balance.

Не создавай скрытый второй кошелёк без необходимости.

При prepaid:

- списывать через существующий ledger;
- использовать idempotency;
- сохранять source reference.

При недостатке баланса:

- приостановить только CDN entitlement;
- убрать CDN nodes из следующей subscription;
- прекратить новые CDN sessions;
- ordinary VPN оставить;
- уведомить;
- записать \`insufficient_balance\`;
- auto resume после пополнения, если разрешено.

При billing collector outage:

- не массово отключать клиентов;
- применить контролируемый grace;
- уведомить администратора;
- не списывать приблизительно.

======================================================================
39. HEARTBEAT TRAFFIC И BILLING
======================================================================

Heartbeat может входить в Xray user counters.

Не вычитать приблизительные значения.

Если технический трафик можно точно отделить — хранить отдельно.

Если нельзя:

- считать по server-side counters;
- сделать heartbeat минимальным;
- предусмотреть бесплатный service allowance;
- документировать это.

Yandex HTTP/TLS overhead не равен Xray payload.

Не распределять aggregate overhead по клиентам без бизнес-решения.

======================================================================
40. RECONCILIATION
======================================================================

Сравнивать:

- сумму per-user Xray traffic;
- Xray instance totals;
- Yandex CDN aggregate;
- Yandex cost;
- service traffic;
- test/probe traffic;
- heartbeat;
- разницу;
- overhead percent;
- revenue;
- internal cost;
- margin.

Reconciliation не изменяет автоматически индивидуальные начисления.

Показывать:

- status;
- difference;
- anomaly;
- last reconciliation;
- action required.

======================================================================
41. NOTIFICATIONS
======================================================================

Поддержать:

- 50%;
- 80%;
- 90%;
- 100%;
- low balance;
- entitlement suspended;
- entitlement resumed;
- collector stale;
- counter reset;
- billing anomaly;
- reconciliation anomaly;
- edge degraded;
- edge quarantined;
- subscription unavailable.

Использовать существующие:

- Panel;
- Telegram bot;
- push;
- email, если уже есть.

Не отправлять secrets.

======================================================================
42. ORIGIN SECURITY
======================================================================

После end-to-end доказательства:

- ограничить sidecar origin port официальными Yandex CDN prefixes;
- получать prefixes из официального источника;
- обновлять allowlist атомарно;
- не использовать только 188.72.113.1/.2;
- не менять firewall production ports;
- сохранить SSH;
- иметь rollback.

Stats/control API:

- localhost/Unix socket;
- authentication;
- minimal permissions.

Wrong Host/path:

- обычный 404/fallback;
- без Xray details;
- rate limit;
- без влияния на valid traffic.

Рассмотреть HTTPS CDN → origin отдельной фазой.

После перехода повторить все acceptance tests.

======================================================================
43. YANDEX CDN ACCEPTANCE SUITE
======================================================================

Создай reusable scripts:

scripts/repro/yandex-get-body.sh
scripts/repro/yandex-active-stream.sh
scripts/repro/yandex-idle-cutoff.sh
scripts/repro/yandex-literal-edge.sh
scripts/repro/xray-counter-reset.sh
scripts/repro/billing-idempotency.sh
scripts/repro/duplicate-event-replay.sh
scripts/repro/subscription-escaping.sh
scripts/repro/edge-rotation.sh

Тесты Yandex:

- GET body 1 B;
- 1 KB;
- 64 KB;
- 256 KB;
- typical XHTTP chunk;
- configured maximum;
- SHA-256;
- auth/seq preservation;
- cache disabled;
- active stream;
- idle cutoff;
- literal edge;
- invalid Host;
- invalid path;
- HTTP status;
- latency;
- retry;
- load.

Не запускать опасный load test без ограничения и отдельного окна.

======================================================================
44. PRODUCTION RELEASE GATES
======================================================================

GATE 0:
Read-only inventory.

GATE 1:
Backup + restore proof.

GATE 2:
Isolated implementation.

GATE 3:
Automated tests.

GATE 4:
Direct/local test.

GATE 5:
Yandex CDN test.

GATE 6:
Client matrix.

GATE 7:
Shadow metering.

GATE 8:
Shadow billing.

GATE 9:
One internal canary.

GATE 10:
2–5 regional canaries.

GATE 11:
48-hour observation.

GATE 12:
Production cutover with approval.

Codex продолжает автономно до stop gate, если изменения:

- изолированы;
- обратимы;
- не влияют на production;
- покрыты тестами.

======================================================================
45. ОБЯЗАТЕЛЬНЫЕ STOP GATES
======================================================================

Остановись и запроси явное подтверждение только перед:

1. Restart production Xray.
2. Update production Xray.
3. Update production 3x-ui.
4. Production DB migration.
5. Изменением существующих UUID/URI.
6. Изменением production firewall.
7. Переключением реальных клиентов.
8. Real balance charging.
9. Production OTA MaestroVPN.
10. Reboot сервера.
11. Удалением probe.
12. Удалением backup.
13. Удалением старой реализации.
14. Необратимой операцией.

До этих gates самостоятельно:

- исследуй;
- проектируй;
- создавай ADR;
- пиши код;
- создавай migrations в shadow/test;
- запускай unit/integration tests;
- поднимай isolated services;
- создавай test build;
- создавай Candidate Releases;
- выполняй Yandex probes;
- готовь canary;
- готовь rollback.

======================================================================
46. ROLLBACK
======================================================================

Автоматизировать:

- rollback app;
- rollback DB migration;
- rollback Xray binary;
- rollback transport release;
- rollback edge publication;
- rollback Yandex origin;
- disable entitlement;
- disable real billing;
- return to shadow billing;
- restore subscriptions;
- restore 3x-ui;
- restore config;
- restore firewall.

Rollback не должен:

- удалять usage history;
- удалять ledger;
- обнулять balance;
- менять ordinary VPN.

Target data-plane rollback:

не более 5 минут.

Проверенный fallback:

193.17.183.48:18080
maestro-cdn-probe.service

======================================================================
47. VERTICAL IMPLEMENTATION PHASES
======================================================================

Фаза 0:
Audit, domain model, backups, baseline.

Фаза 1:
Immutable release skeleton и isolated sidecar.

Фаза 2:
Один test Xray User и direct VLESS/XHTTP.

Фаза 3:
Yandex CDN переключается на Candidate origin только после validation.

Фаза 4:
Один standard subscription node.

Фаза 5:
Client matrix.

Фаза 6:
Idle baseline fallback и исследование core fix.

Фаза 7:
Per-user stats.

Фаза 8:
Meter Listener + central Metering Server.

Фаза 9:
Shadow usage.

Фаза 10:
Shadow billing.

Фаза 11:
Panel entitlement.

Фаза 12:
Panel tariff UI.

Фаза 13:
Edge registry.

Фаза 14:
One internal canary.

Фаза 15:
Regional canaries.

Фаза 16:
Real billing с разрешения.

Не строить сначала всю БД, потом весь backend, потом весь frontend.

Каждая фаза должна давать полный проверяемый vertical slice.

======================================================================
48. ОБЯЗАТЕЛЬНЫЕ ТЕСТЫ BILLING
======================================================================

Проверить:

1. Billing OFF по умолчанию.
2. Один known-size download.
3. Один known-size upload.
4. Два clients одновременно.
5. Counters не смешиваются.
6. Ordinary VPN не тарифицируется.
7. Karing тарифицируется server-side.
8. Incy тарифицируется server-side.
9. Happ тарифицируется server-side.
10. MaestroVPN тарифицируется server-side.
11. Repeat poll не создаёт duplicate.
12. Collector restart не создаёт duplicate.
13. Backend restart не создаёт duplicate.
14. Xray restart создаёт новый epoch.
15. Negative delta не создаётся.
16. Stats API outage не обнуляет state.
17. Stale data блокирует posting.
18. Price change не пересчитывает прошлое.
19. Included traffic расходуется один раз.
20. Soft limit уведомляет.
21. Hard limit отключает только CDN.
22. Low balance отключает только CDN.
23. Ordinary nodes сохраняются.
24. Adjustment создаёт новую операцию.
25. Reversal компенсирует ошибку.
26. CSV совпадает с ledger.
27. GB/GiB отображаются корректно.
28. Нет float.
29. Round-off корректен.
30. Idempotency key работает.
31. Subscription reimport не обнуляет usage.
32. App change не обнуляет usage.
33. IP change не обнуляет usage.
34. Edge change не обнуляет usage.
35. Free mode явный.
36. Нельзя тарифицировать без цены.
37. Reconciliation показывает разницу.
38. Real charging невозможно включить случайно.

======================================================================
49. ОБЯЗАТЕЛЬНЫЕ ТЕСТЫ PRODUCTION REGRESSION
======================================================================

До и после:

- existing VPN users connect;
- existing UUID unchanged;
- existing subscription output unchanged;
- 3x-ui 2053 available;
- production ports unchanged;
- production PID/uptime unchanged;
- ordinary billing unchanged;
- balances unchanged;
- bots unchanged;
- ordinary API unchanged;
- firewall ordinary rules unchanged.

Ноль намеренных отключений существующих клиентов.

======================================================================
50. DEFINITION OF DONE
======================================================================

Задача завершена только когда:

1. Existing VPN не пострадал.
2. New data plane изолирован.
3. Yandex CDN transport работает.
4. Literal edge + SNI/Host работает.
5. GET body работает.
6. VLESS Encryption работает в выбранном stable preset.
7. Idle handling доказано.
8. Standard fallback сохранён.
9. Per-client entitlement работает.
10. Default OFF.
11. Ordinary nodes сохраняются.
12. CDN nodes добавляются только при ACTIVE.
13. Один клиент отключается индивидуально.
14. Edge Registry работает.
15. Transport Releases immutable.
16. Canary и rollback протестированы.
17. Karing status документирован.
18. Incy status документирован.
19. Happ status документирован.
20. MaestroVPN heartbeat/recovery работает.
21. Server-side per-user stats работают.
22. Ordinary VPN не попадает в CDN billing.
23. Global price per GB работает.
24. Profile price работает.
25. Tariff price работает.
26. Individual price работает.
27. GB/GiB явно определён.
28. Included traffic работает.
29. Soft/hard limits работают.
30. Low-balance suspension отключает только CDN.
31. Collector idempotent.
32. Meter epochs работают.
33. Ledger immutable.
34. Shadow billing проверен.
35. Real charging включается только после approval.
36. Reconciliation с Yandex реализована.
37. Audit log реализован.
38. Backups проверены восстановлением.
39. 3x-ui либо безопасно обновлена, либо обоснованно не тронута.
40. WDTT/qWDTT/CSQTT/OLCTRC не затронуты.
41. Документация завершена.
42. HANDOFF обновлён.
43. Все изменения имеют тестовые доказательства.
44. Rollback занимает не более 5 минут для data plane.
45. Ни один старый контент не удалён без разрешения.

======================================================================
51. ФОРМАТ ПРОМЕЖУТОЧНЫХ ОТЧЁТОВ
======================================================================

Не присылай поток рассуждений.

Присылай краткие доказательные отчёты:

1. Этап.
2. Что проверено.
3. Что найдено.
4. Что изменено.
5. Какие команды/tests выполнены.
6. Результаты.
7. Production baseline.
8. Риски.
9. Rollback point.
10. Следующий шаг.
11. Нужен ли approval.

Все утверждения «готово», «работает», «безопасно» подтверждать:

- test output;
- diff;
- health;
- checksums;
- logs;
- comparison;
- rollback test.

======================================================================
52. С ЧЕГО НАЧАТЬ ПРЯМО СЕЙЧАС
======================================================================

Начни немедленно:

1. Проверь Git status и структуру репозиториев.
2. Найди AGENTS/CLAUDE/CONTEXT/docs.
3. Сохрани этот master task.
4. Создай короткий AGENTS.md.
5. Создай CONTEXT.md.
6. Проведи read-only audit.
7. Зафиксируй production baseline.
8. Создай backups.
9. Проверь restore.
10. Найди source of truth клиентов.
11. Найди subscription generator.
12. Найди source of truth баланса и ledger.
13. Найди текущую 3x-ui/Xray архитектуру.
14. Изучи upstream Xray и референсные источники:
    - https://github.com/ServerTechnologies/proxy-via-russian-cdn
    - https://github.com/XTLS/Xray-core/pull/5414
    - https://github.com/XTLS/Xray-core/issues/6554
    - https://github.com/mattpocock
15. Создай VERIFIED_FACTS.md.
16. Создай Wayfinder/ADR map.
17. Создай SPEC.
18. Разбей на vertical tickets.
19. Начни isolated sidecar implementation.
20. Не останавливайся после отчёта.
21. До stop gate продолжай автономно.
22. Перед production change запроси явное подтверждение.

Финальный принцип:

Не нужно механически повторять чужой конфиг.

Нужно построить для MaestroVPN промышленную систему:

- устойчивую к региональным белым спискам;
- управляемую из Maestro Panel;
- индивидуально включаемую;
- совместимую со сторонними клиентами;
- тарифицируемую по гигабайтам;
- наблюдаемую;
- безопасную;
- масштабируемую;
- тестируемую;
- с immutable releases;
- с canary rollout;
- с быстрым rollback.

Проверенный Yandex CDN, чужая схема, видео, репозитории и наши тесты
являются рабочей технической основой.

Ты имеешь право улучшить любой внутренний компонент.

Ты не имеешь права рисковать уже работающим VPN.
