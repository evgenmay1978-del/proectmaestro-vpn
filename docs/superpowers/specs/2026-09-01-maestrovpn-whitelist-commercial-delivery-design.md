# MaestroVPN: коммерческая выдача режима «Белые списки»

Дата решения: 2026-09-01
Статус: письменный дизайн подтверждён 2026-09-01 и уточнён owner policy override 2026-09-02; разрешена реализация
Каноническая ветка: `codex/yandex-cdn-whitelist-task3-sync`

## 1. Цель и границы

Нужно превратить уже проверенный изолированный Yandex CDN/XHTTP canary в управляемый продукт: одна понятная подписка, ручная оплата, пополнение гигабайтов, server-side metering, индивидуальная приостановка CDN и последовательное развёртывание изолированного data plane на S1–S4.

Обычный VPN остаётся независимым и неизменным. CDN-лимит, ошибка metering или отключение entitlement никогда не блокируют аккаунт и обычные узлы. Android/TV production baseline остаётся `1.0.157`; bare `/sub/<token>` не меняется. OLCRTC и WDTT не входят в работу.

## 2. Подтверждённая коммерческая модель

Владелец уточнил единую модель без отдельной subscription URL, но с отдельным управлением CDN/LTE внутри неё:

- «Maestro — 30 дней»: 400 ₽;
- обычные VPN-узлы всегда остаются в подписке и не зависят от CDN/LTE;
- CDN/LTE по умолчанию скрыт и OFF для каждого клиента;
- CDN/LTE появляется только после подтверждённой покупки GB либо явного admin enable;
- admin disable снова скрывает CDN/LTE, но сохраняет купленные GB, ledger и history;
- продление обычного доступа за 400 ₽ не включает CDN/LTE и не начисляет автоматические 2 GB;
- trial/bonus отложен; если его утвердят позднее, это будет отдельный явный идемпотентный grant/product;
- купленные пакеты не сгорают, но при окончании основного доступа замораживаются до продления;
- пакеты: 5 GB — 100 ₽, 20 GB — 300 ₽, 50 GB — 600 ₽, 100 GB — 1 000 ₽;

Связанные технические правила также подтверждены:

- первоначально овердрафт отсутствует;
- traffic basis — `UPLINK_PLUS_DOWNLINK`, измеренный Xray per-user counters;
- единица расчёта — `GB_DECIMAL = 1_000_000_000 bytes`;
- изменение цены применяется только к новым заказам и новым usage intervals.

«Без овердрафта» означает отсутствие отрицательного расчётного баланса и намеренного разрешения трафика при нуле. Периодический Xray Stats не является сетевым hard-cap: байты между последним sample и фактическим revoke записываются как `uncovered_bytes`, баланс насыщается до нуля, публикация прекращается до удаления identities, а максимальная задержка и safety reserve проверяются canary. Если измеренный риск не укладывается в утверждённый предел, платный запуск остаётся NO_GO.

Текущая бизнес-оценка владельца: около 40 клиентов, из них около 3 сейчас нуждаются в белых списках. Это вход для планирования, а не server-verified runtime count.

При 40 оплатах по 400 ₽ выручка равна 16 000 ₽/месяц. Заявленные владельцем серверные расходы — 2 030 ₽/месяц. Один Yandex CDN resource по публичному тарифу 2026-07-01 стоит 150 ₽/месяц и включает 150 GB; трафик сверх пакета — 1,054 ₽/GB. Первые 100 000 000 запросов resource-month включены, далее тариф составляет 1 ₽ за 100 000 запросов. При применимом НПД 4% ориентировочный остаток после налога и указанных fixed costs — 13 180 ₽ до домена, поддержки и прочих расходов. Перед платным запуском расчёт обновляется по фактическим CDN GB, request count и данным canary.

## 3. Fleet/data-plane архитектура

Yandex CDN не устанавливается на каждый сервер. Используется один общий Yandex CDN resource как публичная точка входа; отдельный ресурс на пользователя или сервер не создаётся. Его active Origin group состоит только из одинаковых по ingress-конфигурации проверенных реплик.

На каждом подходящем S1/S2/S3/S4 последовательно разворачивается отдельный immutable sidecar `maestro-xray-cdn`, не связанный с production 3x-ui/Xray. Каждый active Origin принимает одинаковый набор управляемых route identities вида `wl:<entitlementID>:<exitID>`. Статическое Xray routing rule по официально поддерживаемому `user: ["regexp:..."]` направляет такую identity на фиксированный outbound S1/S2/S3/S4 независимо от того, какую Origin-реплику выбрал CDN. Outbound подключается к отдельному TLS VLESS relay inbound `maestro-cdn-exit-in` выбранного sidecar; relay порт доступен только active Origins и не использует production 3x-ui/Xray. Relay traffic всегда выходит через локальный `freedom` выбранного exit и не попадает обратно в origin routing. Неизвестная или искажённая identity попадает только в blackhole; default exit отсутствует. Поэтому публичная подпись страны обозначает exit, а не случайно выбранный Origin. Control plane согласует entitlement identity, route credential, release и desired generation на всех active Origins; маршрут публикуется только после свежих receipts от каждой Origin-реплики и health выбранного exit relay. Receipt связывает `originID`, Xray process boot identity, config digest и desired generation; рестарт или истечение TTL немедленно снимают readiness. В CDN origin routing допускаются только узлы с точным проверенным release, health и rollback point.

Порядок rollout:

1. Существующий изолированный S4 canary.
2. S2 после inventory, backup/restore и port/config proof.
3. S3 после authoritative identity и east-west gates.
4. Текущий S1 после подтверждённого доступа, backup/restore и отсутствия конфликтов.
5. Региональные canaries и 48-часовое наблюдение.
6. Отдельно подтверждённый customer cutover.

Сбой одного sidecar не влияет на ordinary VPN. Удаление CDN-узла из подписки не считается отзывом уже импортированного URI: use-gate обязан отдельно revoke/re-enable только управляемые identities `wl:<entitlementID>:<exitID>` на всех active Origin-репликах и не трогать canary/static users.

## 4. Одна подписка и клиентская выдача

Основной пользовательский принцип — один приватный subscription URL, внутри которого обычные и CDN-узлы имеют разные понятные названия.

- `/sub/<token>` остаётся byte-compatible для MaestroVPN 1.0.157.
- Для сторонних клиентов используется существующий `/sub/<token>?format=links`.
- Ordinary document строится и кешируется как сейчас.
- CDN augmentation выполняется только после ordinary cache/LKG по свежему `WhiteListPublicationSnapshot`.
- CDN-часть никогда не попадает в ordinary cache или LKG.
- При отсутствии entitlement, zero balance, stale/pending projection, suspension, release mismatch или ошибке source возвращается исходный ordinary document byte-for-byte с HTTP 200.
- При положительном свежем verdict добавляются все approved CDN nodes с уникальными стабильными публичными labels без внутренних edge IDs.

Incy получает one-tap обёртку официального формата `incy://crypt1/<payload>`. Для Happ до device proof используется безопасный fallback: копирование приватного HTTPS subscription URL и QR; неподтверждённый wrapper не генерируется. SubToken и wrapped URL запрещены в callback data и логах.

## 5. Authoritative balance и публикационный verdict

Существующий `shadowbilling.RemainingBytes` означает остаток included quota и не является prepaid wallet. Нужен отдельный immutable byte journal и projection:

- `included_bytes` только для отдельного явного grant; commercial default равен `0`;
- `purchased_bytes` без срока сгорания;
- `consumed_bytes`;
- `available_bytes = included_remaining + purchased_remaining`;
- active billing period с точными start/end;
- monotonic projection version;
- freshness/pending flags;
- immutable credit/debit/bonus/adjustment/reversal entries;
- один current period на entitlement;
- idempotency key каждой операции.

Подтверждённая покупка GB создаёт или использует активный zero-grant accounting period, ровно один раз начисляет purchased bytes и включает customer publication gate. Продление ordinary access само по себе не создаёт `INCLUDED_GRANT` и не меняет visibility; для уже существующего CDN entitlement оно может продлить только zero-grant accounting window. Если позднее будет утверждён отдельный explicit grant, неиспользованный included остаток сгорает только на границе такого period. Collector обязан закрыть interval на границе; пересёкший границу interval не делится приблизительно, а переводит projection в pending/stale до точного разрешения. Купленные пакеты доступны при активном primary access и не сгорают.

Publication verdict является отдельным typed результатом: `Publishable=true` только при явном customer activation gate (`CONFIRMED_GB_PURCHASE` либо `ADMIN_ENABLE`), ACTIVE entitlement, действующем основном доступе, свежем непредварительном projection, положительном available balance, точном profile/preset/release binding и пригодном sidecar credential. Отсутствующий либо disabled gate закрывается как `NO_ENTITLEMENT` и возвращает ordinary-only документ.

При `available_bytes <= 0` либо admin disable выполняются два упорядоченных действия: сначала CDN исчезает из свежей links-подписки, затем соответствующие `wl:` identities отзываются на всех active Origins. Admin disable не меняет purchased balance, journal или history. При включении порядок обратный: новая generation применяется на всех Origins, подтверждается receipts и только потом появляется в подписке. Ordinary subscription, ordinary credentials, customer status и основной expiry не меняются. После подтверждённого top-up identities возвращаются идемпотентно.

## 6. Ручная оплата и exactly-once подтверждение

Автоматический эквайринг и real charging в этот дизайн не входят. Сохраняется ручная оплата владельцу.

Поток:

1. Telegram identity привязана ровно к одному Maestro login.
2. Бот постоянно показывает login и кнопку копирования.
3. Клиент выбирает «Продлить 30 дней» либо пакет GB.
4. Создаётся один pending order с зафиксированными product, amount, currency и tariff snapshot.
5. Инструкция просит указать в комментарии перевода только Maestro login, без слова VPN.
6. Клиент нажимает «Я оплатил»; order переходит в `payment_claimed`.
7. Владелец видит login, продукт, сумму и order ID и нажимает «Подтвердить» или «Отклонить».
8. Confirm одной кластерной CAS-транзакцией сохраняет payment result и ровно один раз продлевает срок либо начисляет bytes.
9. Повторный callback, двойной клик, другой panel node или restart возвращают сохранённый результат без повторного начисления.
10. После commit клиент получает подтверждение, баланс и кнопки подключения.

Статусы: `created`, `awaiting_payment`, `payment_claimed`, `confirmed`, `provisioning`, `ready`, `rejected`, `failed`, `unknown`. Provisioning запрещён при `unknown`.

## 7. Telegram UX

Главное меню содержит только:

- «Моя подписка и баланс»;
- «Продлить 30 дней — 400 ₽»;
- «Купить гигабайты»;
- «Подключить устройство»;
- «Помощь».

Если основной доступ истёк, бот сначала предлагает продление и не создаёт отдельный заказ на GB, который клиент всё равно не сможет использовать. После продления покупка пакетов снова доступна.

После оплаты бот показывает короткое сообщение: что куплено, срок, купленные/доступные GB и одну кнопку «Открыть в приложении». Explicit included grant показывается только если он действительно существует. Если one-tap для выбранного клиента не доказан, бот даёт три шага: скопировать ссылку, открыть импорт подписки, вставить ссылку. Ошибки описываются простыми действиями без технических терминов.

Уведомления дедуплицируются на 50%, 80%, 90%, 100%, suspension, resume, stale metering и failed provisioning. Никаких секретов в уведомлениях.

## 8. Ошибки и деградация

- Любая неопределённость publication source удаляет только CDN из нового документа.
- Ordinary cache/LKG продолжает работать независимо.
- Collector outage не создаёт приблизительных списаний и не обнуляет counters. Каждый `meter_epoch` глобально уникален и включает Origin/source identity, Xray process boot identity и counter-reset sequence.
- Уже активным entitlement предоставляется ограниченный grace; admin получает alert. После grace решение принимается индивидуально и не запускает массовый account suspension.
- Unknown sidecar apply outcome разрешается чтением durable receipt, без blind retry.
- Ошибка top-up не может оставить bytes начисленными без committed order result.
- Все финансовые исправления выполняются adjustment/reversal entry, а не редактированием истории.

## 9. Обязательные проверки

Repository/API:

- bare `/sub`, `/info`, `/helpers`, device admission и ordinary bytes не меняются;
- positive verdict даёт ordinary + все CDN nodes;
- zero/stale/pending/error/mismatch даёт точный ordinary;
- CDN из старого ответа не возвращается cache/LKG после suspension;
- unique labels и all-or-none batch augmentation;
- period rollover, non-expiring purchased balance и one-current-period constraint;
- duplicate payment/claim/confirm/restart остаются exactly-once;
- top-up возвращает CDN без изменения ordinary bytes;
- revoke/re-enable затрагивает только конкретную `wl:` identity;
- meter epoch/reset/replay не создаёт двойной debit.

Fleet/client:

- direct sidecar, CDN path, literal edge+SNI/Host, per-user counters;
- rollback меньше 5 минут и ordinary baseline без изменений;
- canary последовательно на S4, затем S2/S3/S1;
- Incy/Happ import, refresh, TCP/UDP/DNS, idle, network transition и attribution;
- MaestroVPN 1.0.157 остаётся ordinary-only до отдельного совместимого test build.

## 10. Реализационные этапы

1. Post-cache `WhiteListPublicationSource` и batch links augmentation, default OFF.
2. Durable active-period/prepaid byte journal и projection read.
3. Cache/version fences и entitlement-only sidecar revoke/re-enable orchestration.
4. Manual-payment GB products и exactly-once confirmation.
5. Telegram customer delivery и one-tap/fallback UX.
6. Panel balance/entitlement/order/audit surfaces.
7. Exact-SHA CI, independent review, client matrix и shadow billing.
8. Gated fleet rollout S4 → S2 → S3 → S1, regional canary и observation.

Real charging, OTA/release publication, production DB cutover и final customer traffic switch остаются отдельными явными stop gates.

## 11. Источники и утверждение

- `docs/yandex-cdn-whitelist/MASTER_REQUIREMENTS.md`.
- `docs/research/2026-08-30-akonit-telegram-product-notes.md`.
- Official Yandex Cloud CDN pricing: `https://yandex.cloud/ru/docs/cdn/pricing`.
- Official Incy link encoder: `https://github.com/INCY-DEV/incy-link-encoder`.
- Official Happ subscription FAQ: `https://www.happ.su/main/faq/adding-configuration-subscription`.

Владелец 2026-09-02 уточнил дизайн: CDN/LTE default OFF и скрыт; публикацию разрешает только подтверждённая покупка GB либо admin enable; disable сохраняет purchased balance; ordinary VPN не меняется; автоматические 2 GB за 400 ₽ отменены; CDN/LTE trial отложен. Сохраняются пакеты, `GB_DECIMAL`, `UPLINK_PLUS_DOWNLINK`, отсутствие намеренного овердрафта, one-subscription UX и последовательное размещение isolated sidecars на S1–S4.
