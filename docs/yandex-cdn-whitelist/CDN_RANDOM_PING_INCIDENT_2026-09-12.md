# Случайное исчезновение ping CDN — состояние на 12.09.2026

## Установка cee1642 и уточнение владельца — 13.09.2026

Установлен только controller source cee164229318932395fbd853ed2b64ca4d4ae76d. Изменены backend/cmd/maestro-panel/runtime_whitelist_metering.go и backend/internal/shadowbilling/store.go: раннее продление использует тот же оплаченный предел и оригинальный срок полномочий при неизменном наборе unavailable users; изменение available→unavailable не принимается. Подтверждение раннего продления происходит до расчётов, оно не остаётся фоновой незавершённой операцией при раннем выходе. Обновление только времён authenticated receipt не меняет привязку плана; срок ограничен и прежней authority, и текущей receipt. При уже предусмотренном сохранении доступа из-за pending debit сохранён и тот же ограниченный cache. SQL сначала отсеивает подтверждённые outbox-события через MATERIALIZED CTE, затем загружает полные записи ожидающих списаний. Расчёты и подтверждения списаний не подменялись.

Live диагностика до правки: 53 077 source/outbox записей, 67 344 idempotency receipts; исходный pending-read вернул 0 rows за 0,658 с SQLite/0,721 с wall. Эквивалентное чтение с предварительным отбором pending вернуло 0 rows за 0,359/0,406 с. Это адресные чтения текущих данных, не нагрузочный тест. На S1 были фактические metering ошибки после 25–31 с и settlement timeout 50 с; между снимками last_fence_generation действительно менялся. Эти факты подтверждают проблему управляющего контура, но не объясняют каждый n/a.

GitHub build-only run 34722338575, attempt 1, compile-panel SUCCESS, тестовый job SKIPPED. Artifact 10307160441, ZIP 4 441 351 bytes, SHA256 e7a9c9652cfaafd6a4117ad1db5a898a8b10b7e9475508cc517509e598a5ffef; binary 11 047 096 bytes, SHA256 0e99ec36c2baaa3535417702dbb9748f3ab0b99a4c9ecee9817abe1c78d3a48a. Установлен 01:22:56 МСК, /healthz подтвердил точный source. Runtime env, обычная panel, bots, public front и topology settings сохранены. Xray/agent/ingress не менялись. Сборки на компьютере и новые автотесты не запускались.

После установки, 01:24:25–01:26:00 МСК: 96 отсчётов за 95,1 с, все восемь grants active, remaining 15,15–59,56 с, expired=0, last_fence_generation не изменился. Generation вырос на 4, счётчики четырёх используемых маршрутов выросли. В журнале нового controller осталась одна ошибка 01:25:08: origin proofs после 19,141 с; она не сопровождалась fencing в этом окне. Поэтому ранний вывод, что вся текущая нестабильность объяснена lease, неверен.

Владелец сначала сообщил, что n/a по-прежнему появляется, затем уточнил: «а нет работает вроде даже если n a». На прямой вопрос, продолжают ли новые страницы открываться на том же CDN без переключения/переподключения, ответил «да». Текущая работа подтверждена владельцем; n/a пока не устранён и в этом наблюдении не равен отключению VPN. Это не отменяет его более ранние сообщения о реальной потере доступа и не доказывает длительную стабильность.

Дополнительные прямые StatsService reads(reset=false), 01:47:27–01:47:42 МСК, дали нулевую дельту всех counters, при active grants/53,2 с remaining. Это окно не совпало с новым трафиком и не используется как машинное подтверждение открытых страниц. Synthetic traffic не генерировался.

Не повторять установку cee1642 или таймерные/no-lease опыты по одному n/a. Оставить текущую установку; будущие серверные изменения требуют нового конкретного факта фактического сбоя, а не только индикатора задержки. Открыт вопрос механизма ping в INCY; точная версия приложения/core и сеть пока не получены.

Откат только unit/controller: /var/backups/maestro-commercial-controller-20260904-s4-qzBchh/controller-upgrade-cee1642/unit.before. Старый binary /opt/maestro-cdn-controller/releases/4d8e237/maestro-panel фактически содержит 79bc504, SHA256 d9b04a24bec207985ff8ee5597dd00e74870a22de13d85e83059c0b770ddbe5d. Не определять его версию по имени каталога. Operational helper/receipt находятся в C:/Users/User/Documents/Codex/2026-09-12/ping-cdn-handoff.


## Текущее уточнение и наблюдение 13.09.2026

Владелец подтвердил последовательное случайное исчезновение любого из четырёх CDN, иногда двух или всех, и потерю фактического доступа к сайтам. Это не только индикатор задержки. Во время этой сессии он выполнял штатный ping в INCY каждые десять секунд. Исправление ещё не установлено.

- Installed commercial Xray: 26.7.28 managed-session runtime, release 19f88042876a-s4commercial-b4f4fd42d2871a14; runtime SHA256 cfd34575e7db6b3086054739fe75e01d6f24641d2ac76e264125dbb222b043fb, agent SHA256 a17607c5323f9a3da46d18feb1e6e3bedcf166072e863fdda7b1c52dacff484a. Фактическая версия INCY/core и тип сети пока не получены.
- Публичный renderer на 8 configs выдаёт четыре ordinary и четыре CDN, один IPv4/Host/commercial path, четыре разных UUID, packet-up/GET-body/query и минимальные семь extra-полей. Наличие выдачи не означает соединение.
- Первое пассивное окно до ручного ping не содержало нового трафика. В согласованном окне 00:29:35–00:30:37 МСК owner counters на четырёх маршрутах росли; HTTP-ответы Xray/nginx — 200. Lease в отсчётах оставалась действующей, около 15,9 MB выделенного запаса на каждом используемом маршруте, pending/final=0. Это не исключает краткие события между отсчётами.
- Связанные XHTTP request timelines позже показали, что начальные upload/download приходили вместе, nginx передавал их в Xray за миллисекунды. Предварительная гипотеза о многосекундном разрыве начальных upload/download из общего несвязанного HTTP-списка НЕ подтвердилась; паузы относятся к более позднему обмену.
- 00:40:42–00:41:37 МСК: восемь downstream-сессий завершились после примерно 0,09–0,2 с. В обеих частях Xray→nginx→CDN совпали тела и времена передачи по TCP (разница 32 байта HTTP headers); 0 повторных data segments в учтённых downstream, Xray посылал финальный chunk 5 bytes. Это показывает раннее завершение с origin-стороны, но не устанавливает исходный триггер: peer close, ошибка ML-KEM/VLESS или dispatch. Metadata — JSON рядом; raw packet payload не сохранялся.
- Relay packet window содержал многочисленные одинаковые служебные TLS health exchanges (UP1727/DOWN~2111–2116); их нельзя выдавать за успешный пользовательский трафик. Для одного S2 handshake была задержка ~0,75 с. Этим причина random CDN не доказана.
- После сообщения «сейчас все 4 n/a», 00:43:29 МСК state показал 8 active grants, остаток deadline 41,2 с, около 16 MB на маршрут, без pending/final. Последний fence уже сменился с 12524/12531 на 12636/12643, поэтому отсутствие fencing на протяжении всей сессии НЕ утверждается.
- 00:49:35 МСК прямой read-only HandlerService/GetInboundUsers подтвердил ровно восемь актуальных пользователей и UUID каждого из protected credential source. ManagedCredentials вначале проверяет immutable directory и затем /var/lib/maestro-xray-cdn-commercial-agent/credentials; отсутствие файла в первой не значит потерю клиента.
- Реальный Xray error.log пишется в отдельный файл, не journald. В просмотренном хвосте — Warning о недоверенном X-Forwarded-For; это игнорирование forwarded metadata, не установленная причина обрыва. Pin Process возвращает ошибки ML-KEM/VLESS на Info, а установлен loglevel warning; пустой journald не означает отсутствие таких ошибок.

Следующий минимальный факт — текст ошибки неудачного подключения из журнала INCY (запрошен у владельца, без подписок/UUID/config). Повторные служебные readiness, увеличение timeout, односторонний no-lease, смена XHTTP/CDN или реконструкция private-теста не обоснованы этими наблюдениями. Config/units/binaries/firewall/CDN/customer data не изменены, сборки/автотесты и искусственный трафик не запускались.

Уточнение пассивности: agent Usage() штатно сохраняет новый lease challenge, поэтому частое внешнее GET /v1/usage способно вмешаться в nonce-обмен контроллера. В этой сессии вместо него использовались прямые StatsService QueryStats(reset=false), HandlerService GetInboundUsers и чтение state. Это не доказательство, что прежний единичный /v1/usage вызвал неисправность.

## Наблюдаемый результат владельца

В HAPP/INCY ping всех CDN-профилей исчезает случайно. Стабильность обычного VPN этой записью не опровергается. Серверная readiness-квитанция может быть свежей, когда реальный клиентский ping уже отсутствует; readiness нельзя снова использовать как доказательство клиентского соединения.

Последний эксперимент `MAESTRO_WHITELIST_USE_LEASE=0` вместе с `MAESTRO_COMMERCIAL_RUNTIME_LEASE=false` дал худший результат: владелец сообщил, что не пингуется ни один CDN. Эксперимент полностью откатан. После отката текущий клиентский ping владельцем ещё не подтверждён; исходная случайная неисправность считается нерешённой.

## Подтверждённые причины и границы

- На всех S1–S4 не было failed units или OOM; обычные VPN-службы не изменялись.
- S4 rqlite follower был перегружен: около 92% одного CPU, на лидере S2 за сутки 1165 timeout и 1961 ошибок связи с S4, а за отдельные 5 секунд ещё 22 TCP timeout. S4 Raft писал `broken pipe` и отставал по FSM. S3 таких ошибок не имел.
- S4 исключён только из HTTP-endpoints контроллера; controller использует S2 и S3. Сам S4 остаётся единственным активным CDN-origin с ingress и commercial Xray.
- S4 rqlite follower остановлен, но не disabled и не удалён. Данные сохранены, S2+S3 остаются активными и держат кворум. После остановки нагрузка S4 упала до 0,17, оба endpoint дали 8/8, а agent refresh ошибок не показал.
- Эти меры очистили серверные `transport failed`, но не доказали реальный клиентский ping. Владелец после них всё равно наблюдал случайное исчезновение ping.
- Восемь коммерческих маршрутов используют одну 60-секундную use-lease. Снимок показывал 39 секунд до deadline и 54 секунды до максимума. Это реальный источник одновременного снятия маршрутов при задержке, но простое выключение lease оказалось несовместимо с текущим publication state и не является рабочим решением.
- Проход metering раньше обрывался на фактических 21,6 секунды usage и 30,001 секунды admission. Текущий controller увеличивает только эти бюджеты до 25/50 секунд; это серверная мера, не подтверждение клиентской стабильности.

## Текущее live-состояние после отката

- S1 `maestro-cdn-controller.service`: active, код `79bc50456a5e4fcd839df1ae6d3e0144c2200044`, binary SHA-256 `d9b04a24bec207985ff8ee5597dd00e74870a22de13d85e83059c0b770ddbe5d`, два rqlite endpoint S2/S3, use-lease в прежнем default-режиме.
- S4 `maestro-xray-cdn-commercial-agent.service`: active, `MAESTRO_COMMERCIAL_RUNTIME_LEASE=true`. Commercial Xray, ingress, private Xray и обычный x-ui active. S4 rqlite inactive, данные на месте.
- Неудачный source commit `354a727` откатан commit `223e2bb`; ветка больше не предлагает no-lease режим как актуальный код. Deployed controller остаётся `79bc504`, а GitHub HEAD после фиксации должен включать `223e2bb` и этот handoff.
- Изолированная тестовая CDN-подписка удалена по указанию владельца: в private Xray 0 тестовых клиентов, активные `client-cdn.json` и `client-uri.txt` убраны. Пустой private service сохранён active. Backup: `/var/backups/maestro-private-test-subscription-20260912T192022Z`.
- Рекламный пилот остановлен до решения CDN; бюджет до 10 000 ₽ сохранён, расходов 0 ₽.

## Не повторять

- Не считать свежую readiness-квитанцию или отсутствие controller errors доказательством client ping.
- Не разворачивать `354a727` и не выключать lease только двумя env-флагами: владелец подтвердил полный `n/a`/отсутствие ping, затем выполнен откат.
- Не возвращать S4 в controller endpoints и не запускать S4 rqlite до отдельного решения по follower CPU/пересборке узла.
- Не увеличивать таймеры дальше и не менять XHTTP/Yandex/приложения без нового факта на реальном клиентском пути.

## Следующий обязательный шаг

Следующий чат должен начать с одновременного наблюдения реального сбоя владельца: точное время исчезновения ping, S4 ingress, commercial Xray и фактический список managed users через `/v1/usage`. Для rqlite `payload_json` приходит Base64-строкой; сначала декодировать Base64, затем JSON. Проверять наличие пользователей и прохождение XHTTP в момент клиентского `n/a`, а не состояние controller до или после него.

После локализации упростить схему по принципу Akonit: оплаченный положительный баланс и активный VPN удерживают пользователя в Xray, а временный сбой асинхронного учёта сам по себе не снимает доступ. Это требует согласованного изменения publication и agent enforcement; прежнее одностороннее выключение lease непригодно.

Обязательное указание владельца: в первую очередь найти и адаптировать уже проверенное простое решение; если подходящего готового решения нет — построить минимальную надёжную схему. Выбор технологий заранее не ограничивать: rqlite, HA, readiness или учёт допустимы там, где их необходимость доказана и результат стабилен. Не связывать ими клиентское подключение без необходимости, не продолжать текущую сложную архитектуру только потому, что она уже написана, и не добавлять механизмы, ухудшающие работу.

## Откат текущих операционных мер

- Предыдущее трёхendpointное env сохранено в `/var/backups/maestro-rqlite-s4-quarantine-20260912T191210Z/runtime.env.before`; возвращать его только вместе с решением по S4 follower.
- Текущий controller `79bc504` сохранён в `/var/backups/maestro-static-cdn-controller-20260912T201522Z/controller.before` и уже восстановлен оттуда после неудачного no-lease опыта.
- S4 agent env с lease=true сохранён в `/var/backups/maestro-static-cdn-agent-20260912T201515Z/runtime.env.before` и уже восстановлен.
- Для возврата S4 follower достаточно `systemctl start maestro-cdn-rqlite-s4.service`, но до устранения перегрузки это вернёт доказанные timeout; автоматически не запускать.

Официальная причина осторожности: документация rqlite указывает, что постоянно отстающий follower означает недостаточную производительность узла или кластера; частые snapshots, медленный диск и сеть ухудшают запись. Источники: https://rqlite.io/docs/guides/performance/ и https://rqlite.io/docs/clustering/general-guidelines/.

## Read-only baseline 13.09.2026

Новый одинарный snapshot не воспроизводил client `n/a` и поэтому не закрывает инцидент. S2/S3 приняли два linearizable rqlite read. Единственный active origin соответствует S4; current desired generation `8080` после Base64→JSON decode содержит восемь canonical managed users, и payload/durable receipt digest совпадают. Authenticated `/v1/usage` вернул согласованные action/digest и покрытие всех восьми: четыре complete counter pairs и четыре `unavailable`. По exact `ManagedUserCounters` source `unavailable` означает, что Xray ещё не создал полную пару uplink/downlink counters, а не автоматически отсутствие managed user.

Отдельное безопасное чтение S4 lease state показало 8 `active` users с deadline и нули для `fenced`, `removed`, `unknown`, pending и final receipts. Ingress, commercial Xray и agent active; `/proc/net` подтвердил listeners 28080/28081/28082. Config, unit, managed users, billing, balance и traffic не менялись; `/v1/usage` штатно сохранил текущий lease challenge. Journald ingress и commercial Xray за последние пять минут не дал строк, что соответствует текущему `access_log off` ingress template. Это оставляет единственный корректный следующий шаг: коррелировать единичный snapshot с точным моментом `n/a`; не считать baseline доказательством ping и не менять lease/timeout.
