## Текущий результат: Чехия переведена с Hysteria2 на VLESS-Reality — 12.09.2026

На S2 установлен отдельный maestro-vless-s2.service: официальный Xray 26.7.28 (5ca6f4b), TCP 2096, Reality/vision. В конфиге 42 активных клиента с их существующими основными VLESS UUID; новые UUID и второй клиентский аккаунт не создавались. Hysteria v2.9.2 была актуальной версией, но живой журнал показывал повторяющиеся UDP/QUIC timeout/error; после подтверждённого VLESS-перехода unit/config/binary Hysteria удалены из рабочих путей и сохранены в root-only rollback backup.

S1 обычная панель и CDN-controller работают из commit 4d8e23747e82; бинарник обоих SHA256 7881c7b08787a23adb50d141d00ca69763b8fee79a152e93b95405a829b79f42. Подписки links/Xray/app выдают Чехию как VLESS, Hysteria не выдают, сохраняют Испанию/Нидерланды/Германию и 4 CDN-профиля. Адресная проверка выданной ссылки через Xray 26.7.28 получила HTTPS 200 за 0,357 с. Финальная проверка: panel/controller/VLESS/AnyTLS/S2 bot active, controller health 200, failed units S2=0, Hysteria unit not-found, UDP 8443 не слушается.

S4 исправления для этого сбоя не требовал: там активен VLESS-Reality, а не чешская Hysteria. 3x-ui 3.4.0 и встроенный Xray 26.6.22 не обновлялись. Не обновлять 3x-ui ради этой задачи: 3x-ui 3.7.0 запускает миграцию схемы при первом старте, а наблюдаемая неисправность была на S2 UDP. Следующий шаг только по новому наблюдаемому сбою владельца; не возобновлять Hysteria и не менять S4/CDN/ботов/оплаты/приложение без отдельной задачи.

Полные доказательства и откат: docs/yandex-cdn-whitelist/S2_VLESS_CUTOVER_2026-09-12.md.

## Текущий результат: проверка оплат и функций ботов — 11.09.2026

Владелец прямо разрешил проверить остальные функции и самостоятельно подтвердить контрольныеCDN-заявки. Основные клиентские/админские сценарии проверены; найденные исправления установлены в обоих ботах. Контрольные1ГБ в каждом боте подтверждены агентом; уведомления пришли, повторный admin-confirm не увеличил баланс. На реальных клиентах не выполнялись тестовые блокировки/удаления/рассылки/смена тарифов и дат — для них использовались изолированные SQLite и зависимости.

Исправлены: блокировка действия зависшим Telegram ACK; повторная отправка существующей обычной pending-заявки после сбоя уведомления; отсутствующие внешние admin-маршруты S2; неверные дни создания/нулевая цена; неверная Android-ссылка HAPP; потеря часового пояса/последние сутки Naive. Новые API-маршруты без Bearer дают401. Код, доказательства и откат: C:/Users/User/Documents/Codex/2026-08-05/new-chat/mvpn-yandex-cdn-whitelist-task3-sync/docs/telegram-bots/INCIDENT_PAYMENT_2026-09-11.md . Воспроизводимые изолированные проверки: ops/bot-checks. Проверки банковского перевода или всех реальных VPN-соединений не заявлены.

Финальный S2 bot_minimal SHA cf8c8ab845d7de200c430da5281c68f8bb1332e679300fe36ecf13aefd696a43; shared entry dd02e712…, CDN248c8485…, devices7310525b…. Откаты только кода/затронутых bot units, не клиентской БД. Backend controller8ca9bcd не пересобирался, приложение/OTA и VPN/Xray остаются прежними. Разрешение на тесты относится к этой задаче; новые кампании автоматически не запускать. Предыдущая запись о непроверенном начислении ниже историческая.

## Текущая задача: понятные клиентские и админские Telegram-боты — 11.09.2026

Переработка установлена в обоих живых ботах: @MaestroSecureVPN_bot (S1 vpnbot.service), @MaestroSecureNaive_bot (S2 vpn_bot.service). Одна главная, логин, точный срок VPN, отдельно остаток CDN, покупка/продление прежним способом, установка по устройствам, HAPP/INCY/Karing, помощь и поддержка. Админ: очереди конкретных оплат, поиск/карточки, дни/дата и CDN/ГБ. Основная админка S2 теперь inline; старые текстовые команды сохранены.

Наблюдение через Telegram Web: главные обоих ботов показывают действительные разные аккаунты владельца без повторного входа; открыты CDN-каталог, помощь, Android/INCY-инструкция с кнопкой копирования, S1 поиск/карточка/CDN-баланс, S2 inline-админка. Реальные оплаты, ручные дни и начисление ГБ не выполнялись ради проверки. Источник, установка и просмотр UI не объявляются финансовым end-to-end тестом.

Backend/API: 8ca9bcdc3cdb17ade925e52cc8b7585fa8531e4e, только 3 additive Bearer-admin маршрута над существующей бизнес-логикой. GitHub build-only run34548241394 SUCCESS, test job skipped, artifact10179819882. Controller SHA256 258077b13ae15d9808134415618ba9610a5b939ab22ca57ca4a8ced5babde85d, PID1900452; GET /admin/customers ответил200. Обычная панельPID136164 и runtime.env сохранены. Итоговые боты: S1 PID1903419, S2 PID1390727. Указанные PID — снимок времени установки, проверять только по новой необходимости.

Код/откат/receipts: docs/telegram-bots/ROLLOUT_2026-09-11.md и JSON рядом. На серверах root-only backup: API /var/backups/maestro-bot-api-20260911T011209Z; S1 /var/backups/maestro-bot-ui-S1-20260911T011457Z; S2 /var/backups/maestro-bot-ui-S2-20260911T011455Z. Последние небольшие UI-правки имеют отдельные final backups, указанные в отчёте. При откате кода не восстанавливать БД: нельзя потерять новые оплаты. Приватные .env/БД не скопированы в Git.

Объём и ограничения владельца: наша оплата/тарифы сохраняются; приложение/глаз остановлены, OTA160/1016001. Не менять VPN/Xray/ingress/CDN-учёт ради меню; не запускать новые тесты и локальные сборки. Исследование Akonit завершено в согласованном объёме, включая 23 скрытых поста, 16 изображений и 8 статей. Открытый репозиторий Akonit не найден; комментарии и все остальные медиа не объявлены прочитанными. WDTT-форум больше не читать.

Уточнение проверенного исходника: нынешний legacy_subscription_overlay.go формирует CombinedXrayJSONSubscription, сохраняя обычные узлы и при format=xray. Прежняя заметка о CDN-only относится к старой реализации; её нельзя применять к установленной версии. Форматы выдачи при переработке ботов сохранены. Прежние CDN-опыты и здоровье серверов остаются в docs/yandex-cdn-whitelist; история ниже не расширяет текущую задачу.

## Текущая граница: изолированная live-проверка Yandex CDN — 10.09.2026

Здоровье серверов проверено read-only10.09 в23:41–23:45МСК: `docs/yandex-cdn-whitelist/SERVER_HEALTH_2026-09-10.md` и JSON рядом. S2: диск98%, осталось~246МиБ, около983МиБ в /var/tmp относятся к сборкам приложения; ничего не удалено. S3: rqlite~51% одногоCPU за10s, RAM/диск достаточны. S4: службы active, private/ingress hashes исходные. S1: HTTP health200/93ms, но SSH недоступен до авторизации (direct close / jump banner timeout), OS-инвентарь не получен. НаS2–S4 failed units/OOM не обнаружено. Запрос владельца — проверка здоровья; он не расширяет разрешение на удаление артефактов, перезапуск production или правку БД. CDN-задача сохранена.

Проверка штатного downstream heartbeat в исходнике26.7.28 завершена: применимого переключателя нет; XMUX/padding/Mux не добавляют данные в простаивающий packet-up download. Доказательства сохранены в индексе опытов. Прежний следующий шаг «прочитать исходник» закрыт. Осталось различить общий60s предел ответа и idle-предел; сами ответы с паузой этого не доказывают. Новых серверных изменений при этом разборе не делалось.

Актуальное дополнение: все результаты и неудачные варианты сведены в `docs/yandex-cdn-whitelist/PRIVATE_CDN_LIVE_RESULTS_2026-09-10.md`. После каждого существенного опыта записывать изменение, наблюдение, вывод, evidence и откат; не повторять уже отвергнутые варианты без новых оснований. Loopback-контроль завершён успешно: полный ответ после127,129с. Через CDN ответ обрывается примерно на60с; отдельный private `noSSEHeader=true` дал тот же обрыв60,196с и полностью отменён. Private config восстановлен (SHA2e22b6ea…), production/ingress сохранены. Полной стабильности пока нет. Следующие записи о baseline и незавершённом loopback — исторические, их уточняет этот индекс.

Консоль Yandex аутентифицирована и просмотрена без изменений: cache off, GET/HEAD/OPTIONS, location rules отсутствуют; в просмотренных формах поле idle timeout не найдено. В поддержку ничего не отправлено. Обращение или передача данных — только после отдельного одобрения точного текста владельцем; секреты, конфиги, подписки и данные клиентов не передавать. Текущий следующий шаг — проверить штатный downstream heartbeat по исходнику; прежние XMUX/noSSE прогоны не повторять.

Последние живые наблюдения (после исходного baseline): сохранённые client-cdn.json и client-uri.txt используют DNS-имя CDN, а рабочий пример владельца — фиксированный Yandex edge. Временный профиль с единственным транспортным изменением address на этот edge прошёл initial204 (1,131 с), download8MiB (2,245 с) и новый204 после idle125s (0,404 с). Evidence: /var/tmp/maestro-private-cdn-owner-edge-20260910T194323Z/summary.json. Это подтверждение одного цикла машинного теста, не изменение уже выдаваемой клиентам подписки и не гарантия работы всех длительных сессий.

Отдельно обнаружен обрыв уже открытого ответа: через тот же fixed edge /idle?seconds=125 приходит начало, затем через60,186с поток заканчивается без конечного маркера; curl0/HTTP200 сами по себе скрывают потерю хвоста. Xray журнал: stream ID1 INTERNAL_ERROR received from peer. Evidence: /var/tmp/maestro-private-cdn-owner-edge-stream-20260910T195443Z. Долгий upload в этом цикле не запускался. Существующий S1 probe на18080 имеет рабочие /idle и GET-body echo до4MiB, поэтому утверждение baseline об отсутствии безопасного upload endpoint больше не актуально.

XMUX60–90 не принят: первый временный extra.xmux сбросил исходный GET и получил405/400; исправленный sibling xmux сохранил GET, но download не прошёл до idle. Позднее исходный DNS-профиль тоже получил timeout на первом204, поэтому прежний единичный отказ после125s не доказывает именно idle timeout. Private logging временно включался и private unit дважды перезапускался только для диагностики; исходный config побайтно восстановлен (SHA2e22b6ea…), production/ingress PID и hashes сохранены. Сейчас локализуется60-секундный обрыв: сравнение того же открытого потока через loopback ingress28080 без CDN, evidence /var/tmp/maestro-private-cdn-loopback-idle-20260910T200502Z. Никакого нового Xray, приложения, общего CDN/ingress или коммерческих настроек не устанавливать по этим промежуточным данным. Snapshot и ограничения ниже сохраняются; текущие наблюдения этого дополнения уточняют старый baseline.

Работа над существующим приложением, глазом и мобильным интерфейсом остаётся полностью остановлена. Кандидат 1016002 не выпускать, не устанавливать и не включать в OTA; текущая OTA остаётся 160/1016001. Подготовленный код и артефакты сохранить. Возможное новое приложение с нуля и заново спроектированный интерфейс остаются последующим этапом, а не текущей задачей.

Владелец разрешил прямую настройку и адресную проверку только изолированной тестовой CDN-подписки и отдельного тестового Xray на живом сервере. Цель — добиться и подтвердить стабильность маршрута через Yandex CDN. До первого изменения определить точные test unit/config/path/account без записи секретов, снять inventory и rollback snapshot и воспроизвести сбой. Затем менять только одну переменную за шаг и непосредственно наблюдать результат. GitHub использовать только после подтверждённого рабочего результата на живом тестовом контуре.

Обычный VLESS/VPN, production CDN клиентов, panel, bots, payments, balances, subscriptions и app/UI трогать или перезапускать запрещено. Отдельное исследование нестабильности обычного VLESS не даёт разрешения на live-изменения. Различать обнаружение трафика провайдером/ISP/DPI и распознавание выходного IP сервисами Google/OpenAI.

Владелец подтвердил, что текущий маршрут через Yandex обходит ограничение мобильной сети. Рекомендации по Xray применять только после проверки первичными источниками. Исследовательский отчёт: `docs/yandex-cdn-whitelist/CDN_XRAY_STABILITY_RESEARCH_2026-09-10.md`.

Наблюдаемое состояние 10.09.2026: изолированный private-контур подтверждён как `maestro-xray-cdn.service`, `/etc/systemd/system/maestro-xray-cdn.service`, `/opt/maestro-xray-cdn/{xray,config.json}`, Xray 26.7.28, один статический test client, публичный inbound 18081 и локальный API 18082. Отдельный ingress на 28080 направляет два разных exact-path в private 18081 и commercial 28081; сами секретные path не фиксировать.

До каких-либо настроек создан root-only rollback snapshot `/var/backups/maestro-xray-cdn-private-20260910T180218Z`; SHA-256 redacted manifest `da4a0d5bba831e62db327d1582c6dc08ecfa7662611f226e6d33184cb1092f61`. Rollback не выполнялся. Baseline через защищённый `client-cdn.json` воспроизвёл сбой: initial 204 и 8 MiB download прошли, после 125 секунд idle resume 204 завершился HTTP 000 / curl 56 через 13,472 секунды. Private server остался active, ordinary/commercial/controller/ingress PID и hashes не изменились, ephemeral SOCKS 10809 остановлен и освобождён.

Настройки, binary, unit, ingress, firewall и production-контуры не менялись. Незавершённый шаг — локализовать границу idle-разрыва и только затем менять по одной переменной в private-контуре с повтором этого же сценария; длительный upload отдельно не проверен, потому что ранее существовавший безопасный endpoint не найден. После каждого существенного результата, изменения разрешения или блокера в этой же сессии актуализировать AGENTS.md и CONTEXT_HANDOFF.md в обоих рабочих каталогах. Приложение остаётся остановлено, 1016002 не выпускать, OTA остаётся 160/1016001; исторические разрешения ниже не расширяют текущую границу.

## Выпуск160 и проверка мобильного приложения — 10.09.2026

Опубликована исправленная1.0.160/versionCode1016001, прежняя fleet-подпись. Исходник APK e0b737d864e86062e1e5c3fe2fdfb26e663130f8; GitHub release385949773/tag tv-v1.0.160. GitHub, Yandex update.json и S1 static update.json теперь выдают этот выпуск; waypoint107 сохранён. Исходная версия владельца157, затем на его телефон ставился кандидат160/1016000; последний1016001 пока не подтверждён установленным. Android требует штатного подтверждения установки.

Владелец разрешил разумные адресные тесты и эмулятор на GitHub. Проверка завершена:32 адресных JVM и13 Android-сценариев в совокупности прошли; повторялись только затронутые проверки. Настоящие native screenshots просмотрены, включая OFF/STARTING/ON, IME, выбор приложений, QR/ссылки, оплату и русские настройки. На ноутбуке сборки/эмулятор/тесты не запускать. Для будущего GitHub-просмотра использовать native x86_64 из имеющегося AAR, не ARM-трансляцию. Не повторять завершённый набор без новой наблюдаемой ошибки или поручения.

Согласованное оформление сохранять: резная шапка MaestroVPN, дерево/золото/изумруд, реалистичный глаз, орнамент по всей центральной панели. После входа виден логин с карандашом смены, до входа — «Ввести логин»; CDN/ГБ в одной строке. Не менять рабочие серверы/CDN, ботов, платежи, балансы и TV ради мобильной графики. Следующие действия — только по новым замечаниям владельца. Доказательства, файлы и откат в CONTEXT_HANDOFF.md.

## Уточнение владельца от 09.09.2026 — актуальность контекста

AGENTS.md и CONTEXT_HANDOFF.md должны оставаться актуальными. При существенном изменении разрешения, результата или блокера обновлять их в той же рабочей сессии. В handoff фиксировать наблюдаемый результат, незавершённый шаг и откат; в AGENTS — действующие правила. Исторические записи ниже не являются подтверждением текущего состояния сервера.

Новые тесты и тестовый CI — только по явному указанию владельца. На компьютере сборки не запускать. Использовать кратчайшее достаточное решение без лишних wrapper, рефакторинга и повторных исследований. Уже полученное разрешение повторно не запрашивать.

09.09.2026 владелец разрешил «применяй»: после исправления порядка final reports установлен controller 0cb9592392702c400d1f95d41fff37e987d7c8bc с досрочным обновлением readiness receipt за полное 60-секундное окно. Наблюдение 100 секунд подтвердило 7 продлений deadline, минимум 39,5 секунды и отсутствие ready/expired/finals; внутренний допуск больше не истекает. Владелец затем подтвердил, что CDN в клиенте на мобильной сети работает правильно. Обычная панель не перезапускалась, боты, база, front, server Xray, runtime.env и балансы сохранены. Исправление завершено; не повторять final-ordering и lease-renewal исследования без новой наблюдаемой ошибки. Подробности и откат в CONTEXT_HANDOFF.md.

# Yandex CDN white-list task

Navigation: [requirements](docs/yandex-cdn-whitelist/MASTER_REQUIREMENTS.md), [vocabulary](CONTEXT.md), [SPEC](docs/yandex-cdn-whitelist/SPEC.md), [ADR/Wayfinder map](docs/yandex-cdn-whitelist/ADR_MAP.md), [Definition of Done](docs/yandex-cdn-whitelist/DEFINITION_OF_DONE.md), and [handoff](docs/yandex-cdn-whitelist/HANDOFF.md).

Current execution contract (22.08.2026):

- `codex/yandex-cdn-whitelist-task3-sync` is the sole canonical working/push
  branch; one writer owns it. Alternate branches/worktrees are review-only and
  must never become the handoff or push source.
- The weak local Windows PC is used only for edits, repetition guard, narrow
  Git/diff/static/unit checks. Heavy/full/race/vet/rqlite/Android APK validation
  runs in GitHub Actions against the exact pushed SHA.
- Immutable production Android/TV baseline: version `1.0.157`, tag
  `tv-v1.0.157`, commit
  `9653636863cb65cc2ac95545d953d9c5e06db8bb`, APK SHA-256
  `0c51d1036c76b2d9a7347b59b9f967942159ec27738a2d6bcae099529695a148`.
- The only next Android identity is test-only
  `versionName 1.0.158-task7-test`, `versionCode 1015800`; it is never a
  production tag/release/OTA.
- After every completed top-level task, update `CONTEXT_HANDOFF.md` and the
  redacted baseline manifest, verify them, commit, and push the canonical
  branch so local and GitHub resolve to the same exact SHA.
- Existing owner authorization covers non-production pushes and GitHub Actions
  on the canonical branch only. Merge, tag, release/publish/signing, OTA,
  production deploy/server/client/billing/cutover mutation require a new
  explicit owner approval.

Invariants: ordinary VPN is unchanged; White-List Entitlement defaults OFF and is additive; initial work is isolated; mandatory target is independently reconciled S1/S2/S3/S4 nodes; no production restart/update/migration/firewall/UUID/URI/client/OTA/real charging; no secrets in Git/logs/docs; WDTT/qWDTT/CSQTT/olcRTC are out of scope.

Local checks: `python -m unittest scripts.tests.test_yandex_cdn_docs`; `python scripts/validate_yandex_cdn_docs.py`; `python scripts/render_redacted_baseline.py`.

Stop gates: live inventory, backup/restore, sidecar deployment, CDN origin switch, production Xray/3x-ui/firewall/DB, real-client switch, charging, OTA, reboot, deletion, and push require explicit owner approval. Sensitive owner-supplied test context remains only in MASTER_REQUIREMENTS; derivative docs/logs must cite sections rather than repeat literals.

---

# MaestroVPN — точка входа для Codex и других агентов

Перед любой работой в этом репозитории:

1. Полностью прочитать [`CONTEXT_HANDOFF.md`](CONTEXT_HANDOFF.md).
2. Проверить текущие ветку, HEAD и `git status`; volatile-факты из handoff
   перепроверять, а не принимать навсегда.
3. Прочитать перечисленные в handoff документы именно по текущей задаче.
4. Сохранять чужие tracked/untracked изменения и не выполнять очистку,
   reset, merge, release, OTA или деплой без прямого разрешения владельца.

## Обязательный барьер от повторных ошибок

Перед серверной/сетевой операцией, записью в GitHub, изменением файлов,
долгим сканированием или сборкой, а также перед любой повторной попыткой сначала
выполнить из корня репозитория:

```text
python ops/maestro-repetition-guard.py check --action <действие> --family <способ>
```

`action` и `family` — только короткие смысловые идентификаторы, например
`s1-key-login` и `openssh-key-probe`. Команды, пути с секретами, пароли, ключи,
токены, URL подписок и данные клиентов передавать в guard запрещено.

После первого неожиданного сбоя или замечания владельца, что выбран неверный
способ, немедленно записать `fail` и остановить это действие. Повтор той же
команды, смена флагов, другой клиент или импровизированная альтернатива также
запрещены, пока отдельно не установлена причина и не зарегистрирован иной способ
через `correct`. Перед ним снова выполнить `check`; после доказанного результата
записать `success`. Точные формы команд и коды выхода описаны в
[`ops/README.md`](ops/README.md). Если guard отсутствует, повреждён или сам
возвращает блокировку, production- и внешние действия запрещены.

### Атомарный барьер попытки

`check` всегда выполняется отдельным вызовом инструмента или отдельной командой.
Его запрещено объединять в одной shell-команде с изменением файлов, тестом,
сетевым действием, `git add`, `commit` или `push`. Один успешный `check` разрешает
ровно одно смысловое действие. Сразу после него нужно прочитать код выхода и
вывод; последующая успешная команда не может маскировать предыдущий сбой.

Если действие завершилось неожиданно, инструмент вернул ошибку или владелец
указал, что способ неверен, следующим исполняемым действием обязан быть `fail`.
До `fail` запрещены повтор, альтернативная команда, другой инструмент и новая
мутация. Затем разрешены только безопасная диагностика, `correct`, отдельный
`check` новой семьи и одна исправленная попытка. Переименование `action` не
сбрасывает блокировку: одинаковая корневая причина считается тем же сбоем. При
повторе корневой причины сначала обновить `AGENTS.md` и постоянный навык, и лишь
потом возвращаться к реализации.

Для изменений исходников действуют дополнительные триггеры:

- создание или изменение patch-файла тоже считается мутацией;
- после второго `context mismatch` для одной смысловой правки запрещены новые
  ручные hunks. Считать фактический текущий файл, построить новый желаемый файл
  вне worktree, получить generated diff из сравнения этих двух файлов, проверить
  его отдельно и применить один раз;

#### Bounded instruction bootstrap

Apply the 200-line output bound to bootstrap and skill/reference reads too. Do not combine instruction files in one exec result. Bound output at the outer functions.exec layer as well as the nested command; after truncation, read only the missing portion of one file, never rerun the full combined read.

#### Sandbox apply_patch ACL fallback

If `apply_patch` returns `helper_unknown_error: apply deny-read ACLs` even for a verified writable external mirror, record `fail` and do not retry `apply_patch` or broaden permissions. Use one escalated exact-line structural generator only on the external `new/` mirror, require exactly one bounded match, inspect the changed region, generate a contextual old/new diff, then run separate `git apply --check` and one `git apply` against the canonical worktree. Never use this exception to shell-edit the canonical file directly.

#### File discovery before content search

For an unfamiliar file or directory, first run `rg --files` from a confirmed existing project root, optionally filtered by a filename glob. Pass only exact returned paths to content searches. A remembered or plausible name is a search term, not a verified path. If the expected directory is absent, return to its nearest confirmed parent and discover paths there; do not guess a sibling file.

#### Python validator preflight

After `ModuleNotFoundError`, check `importlib.util.find_spec` for the missing module in the exact candidate interpreter before retrying the full command. An alternate runtime is not evidence that the dependency exists. Use a verified isolated dependency directory when needed; on Windows run UTF-8 repository/skill validators with `python -X utf8`.

#### Windows generated-patch path rule

Never hand-author unified-diff hunk range counts. Build verified `old/` and `new/` mirror trees and let `git diff --no-index` calculate every range/count.
After any `git apply --check` result containing `corrupt patch`, discard that artifact and generate a fresh diff from the mirrors; do not repair or tune hunk headers manually.

If a generated patch is needed on Windows, never build it from absolute drive paths and then rewrite quoted `a/C:\...` headers. Create external `old/<repo-relative-path>` and `new/<repo-relative-path>` mirror trees; run `git diff --no-index --src-prefix=a/ --dst-prefix=b/ -- old/<repo-relative-path> new/<repo-relative-path>` from their common parent; inspect headers; then use `git apply --check --recount -p2 <patch>` before one `git apply --recount -p2 <patch>`. After one absolute-path normalization failure, record it and move to this method; do not tune quoting, slash conversion, prefix stripping, or header text.
Before any `git apply` inside an external mirror, run `git rev-parse --show-toplevel`. If Git discovers an ancestor repository above the mirror, do not trust the process working directory: either set `GIT_CEILING_DIRECTORIES` to the verified mirror parent for both check and apply, or run from the discovered top-level with an explicit verified `--directory=<repo-relative-mirror-root>`. Immediately verify that the intended mirror file changed; exit code 0 without the expected bounded diff is a failed attempt.
- для существующего исходника запрещены `--unidiff-zero` и hunks без устойчивого
  контекста функции/типа;
- после применения патча до staging показать целиком изменённую функцию или
  ограниченный участок с обеими структурными границами, затем выполнить
  `git diff --check` и профильную локальную проверку;
- локально видимую структурную или синтаксическую ошибку запрещено отправлять в
  GitHub «для проверки CI»;
- mutation, validation, `git add`, `git commit` и `git push` — пять отдельных
  смысловых действий, каждое со своим отдельным `check` и проверкой результата.

Не выполнять рекурсивные обходы всего `C:\Users\User\Documents\Codex` на слабом
компьютере владельца. Использовать уже зафиксированный список репозиториев и
узкие `rg`/Git-проверки конкретного проекта; тяжёлые сборки выполнять в GitHub
Actions, если handoff не требует другого проверенного места исполнения.

Большие текстовые файлы и широкие поисковые выборки читать не более чем по 200
строк на один вывод либо одним приостанавливаемым потоковым процессом. После
первого усечения вывода запрещено расширять лимит, объединять несколько файлов
или повторять широкий поиск: перейти к одному файлу и блокам до 200 строк.

Отвечать владельцу по-русски.

Для текущей переработки мобильного 4D-интерфейса телевизионная часть доступна
только для чтения: не менять `tvm_*`, TV-компоненты, D-pad/focus/Back,
TV-геометрию и ветки `isTv`.

Если состояние проекта, ветка, коммит, PR, выполненные проверки или следующий
шаг материально изменились, перед завершением обновить `CONTEXT_HANDOFF.md`.

### PowerShell regex generation guard

When a generated Python or PowerShell regex contains both quote types, use a single-quoted here-string as the whole-block template. Write it to an external new mirror, inspect it, and generate the patch from old/new mirrors; never embed that regex in a quoted PowerShell expression. Keep synthetic URL/token fixtures in named in-memory test data and keep owner-supplied endpoints or credentials out of derivative docs and secrecy scans.

### PowerShell exact-line patch preflight guard

For an exact-line presence/removal preflight used to generate a patch, do not use regex. Read the source as a line array and count every required line with literal case-sensitive equality (`Where-Object { $_ -ceq $literal }` or an explicit `foreach`), especially lines containing `$`, quotes, or backslashes; assert the exact count before any removal or insertion.

### PowerShell exact-source transformation guard

For an exact multiline source transformation, represent the input and output as
explicit line arrays or as one single-quoted here-string, then serialize with
`-join "`n"` and BOM-less UTF-8. A PowerShell single-quoted string treats
backslashes literally: `\n` and `\r\n` are text, not line separators. Assert the
exact match/replacement count, inspect the generated mirror, and only then build
the old/new patch; after this root cause, do not tune another escaped fragment.

For tab-prefixed exact-line checks, construct the tab as [char]9 (for example [string]::Concat([char]9, $literal)); never rely on backtick escapes inside single-quoted PowerShell strings.

### Windows native rg glob guard

On Windows PowerShell, never pass a wildcard path such as `path/*.go` to native
`rg`: PowerShell does not expand it and `rg` receives a nonexistent literal
path. Pass the verified directory as a path and use `-g '*.go'` or
`--glob '*.go'`.

### PowerShell exact-line array guard

When a PowerShell array element is built by concatenating `[char]9` or another
prefix with a literal, wrap every concatenation in parentheses or use
`[string]::Concat`. An unparenthesized comma/`+` expression can split one
intended line into multiple array elements. Assert the array count and exact
element lengths before using it as a structural preflight.

### PowerShell foreach pipeline guard

Never pipe directly from a PowerShell `foreach (...) { ... }` statement; that grammar can produce `ParserError: An empty pipe element is not allowed`. Collect the loop output into an array variable first, then pipe that variable. After this parser error, do not tune braces or pipe placement.

### Production adapter compatibility test guard

A fake `Business`, handler, store, or port test cannot close a compatibility
finding when the production adapter adds authorization, status, expiry, cache,
or rendering gates. Closure requires the public boundary through the real
production adapter and real migrations/storage (or an explicitly proven exact
equivalent), with both positive and negative states asserted.
## Текущий результат: пропадание CDN из подписок — 11.09.2026

Karing/HAPP/INCY исправлены server-side: controller `ae1ed4d`, legacy subscription timeout 10s, 30/30 `included`, текущие три формата содержат CDN/XHTTP. Наше приложение использует отдельный strict runtime с двумя 3s попытками; воспроизведение осталось нестабильным (5/12). Три server runtime-варианта (TTL/cadence/pass budget) не помогли и полностью откатаны. На сервере оставлен только подтверждённый timeout10; обычный VPN, панель, Xray, боты и учёт не менялись. Доказательства и откат: `docs/yandex-cdn-whitelist/CDN_SUBSCRIPTION_FLICKER_2026-09-11.md`. Дальше не менять серверные таймауты: нужен выбор между обновлением приложения (короче) и отдельной оптимизацией rqlite round trips. Прежний запрет на приложение действует до нового указания владельца.
## Подтверждённый результат CDN для сторонних клиентов — 11.09.2026

Установлен controller `c2960b77863411f2d0fa62f6d0a9d07b24859d01`: Xray JSON теперь вкладывает в `xhttpSettings.extra` только точные семь параметров действующего server profile. Build-only run `34623638053`, artifact `10272804684`, binary SHA-256 `63942fffa596c288e456be960430a5ca96d3f0597b1c223d4f939cf77501c66a`; rollback `/var/backups/maestro-cdn-minimal-extra-20260911T164540Z/unit.before`. Владелец подтвердил: CDN пингуется и работает в INCY и HAPP. Karing CDN не пингуется, такое же поведение наблюдается у Akonit; не продолжать исправлять Karing без новой задачи. Обычный VLESS/Xray, ingress, боты, платежи и балансы не менялись.
## Дополнение клиентов ботов — 12.09.2026

Оба бота получили Mihomo/Clash Mi и v2RayTun со скачиванием, инструкциями и персональной ссылкой. Controller a5ab80e добавляет только новый format=mihomo, прежняя выдача HAPP/INCY/Karing сохранена. На official Mihomo1.19.30 ordinary VPN и CDN прошли HTTPS200; session-table=uuid/session-length=16 обязательны для проверенного маршрута. v2RayTun: выдача ссылки проверена, соединение в приложении не заявлять. Источник, пределы и откат: docs/telegram-bots/CLIENTS_2026-09-12.md. Оплату/Xray/app/OTA не менять ради меню. Глобальное правило сверки версий внесено в C:/Users/User/.codex/AGENTS.md.
## Разбор CDN-видео и Yandex — 12.09.2026

Изучены Selectel-гайд и найденные материалы про Yandex. Selectel использует provider-specific `DELETE/POST`; к Yandex эти параметры не переносить. Текущий Yandex resource read-only подтверждён: HTTP origin, client Host, GET/HEAD/OPTIONS, cache/gzip/segmentation off, без location rules, shielding и raw logs. Сохранять доказанный Maestro-профиль `packet-up + GET-body + query session/seq + ML-KEM`; production не менять по видео. Полный разбор: `docs/yandex-cdn-whitelist/VIDEO_GUIDES_ANALYSIS_2026-09-12.md`.
