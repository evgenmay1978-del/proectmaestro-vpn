# Результаты живых проверок private Yandex CDN — 10.09.2026

## Статус и границы

Объект — существующий изолированный private-контур S4: `maestro-xray-cdn.service`, Xray 26.7.28/5ca6f4b, `/opt/maestro-xray-cdn/{xray,config.json}`, private 18081/API 18082. Ingress 28080 разделяет private и commercial по двум разным exact-path. Значения путей, UUID, encryption, ключи и полные URL подписок здесь не хранить.

Подтверждён один успешный цикл подключения/download/нового подключения после простоя на fixed edge из примера владельца. Существующий открытый ответ через CDN всё ещё обрывается после примерно 60 секунд без downstream-данных. Полная стабильность НЕ достигнута. Выдаваемые клиентам подписки не менялись.

Во всех завершённых опытах ephemeral SOCKS 10809 освобождён; защищённые production/ingress PID и hashes сохранены. Private logging и noSSEHeader менялись временно и возвращены; private unit при этих опытах перезапускался. Утверждение раннего baseline «ничего не менялось/не перезапускалось» относится только к baseline, а не ко всей сессии. Приложение, OTA, обычный VPN, commercial CDN, panel, bots, payments, balances и общий CDN/ingress не изменялись.

## Все выполненные сценарии

Evidence ниже находится на S4; общий префикс каталогов — `/var/tmp/maestro-private-cdn-`. Это сохранённые результаты, а не повод выполнять их заново.

| № | Вариант / единственное существенное отличие | Наблюдение | Вывод и состояние | Каталог / файл evidence |
|---|---|---|---|---|
| 1 | Исходный сохранённый DNS-профиль | initial204 0,976 с; download 8 MiB/200 3,328 с; после idle125s новый запрос HTTP000/curl56 за13,472 с | Сбой воспроизведён. Один этот результат не доказывает idle-specific причину | `baseline-20260910T180718Z/summary.txt` |
| 2 | Ошибочный `extra.xmux` без остальных исходных параметров | initial/download/resume: curl35, HTTP405/400; effective GET заменён на POST | Невалидное сравнение. `extra` заменяет внешние поля кроме host/path/mode. Не повторять этот способ; источник не менялся | `xmux60-20260910T181817Z/summary.txt` |
| 3 | Исправленный sibling `xmux`: maxConnections3, reuse60–90s, GET сохранён | initial204 1,104 с; download TLS timeout/curl28 за10,003 с, 0 bytes | Остановлено до idle; XMUX не принят. Прямой S4→download endpoint контроль с1byte:200/0,762 с | `xmux-sibling-20260910T191221Z/summary.json` |
| 4 | Временный info log только private server; исходный DNS-профиль | Первый204 уже timeout/curl28 за10,003 с, до idle. Server принял VLESS и открыл TCP к Google IPv4 за4ms, клиент не дождался TLS-ответа | Нет основания объяснять все DNS-профиль отказы125s idle. Второй A/B case пропущен; исходный config восстановлен | `idle-pair-20260910T192249Z/summary.json` |
| 5 | Временный клиент: address заменён на fixed Yandex edge из рабочего примера владельца; исходные GET/session/seq сохранены, без XMUX | initial204 1,130963 с; download 8 MiB/200 2,244904 с; idle125s; новый204 0,404481 с | PASS одного цикла. Не проверяет выживание уже открытого потока, не изменяет текущую подписку | `owner-edge-20260910T194323Z/summary.json` |
| 6 | Fixed edge; существующий S1 probe, ответ с паузой125s | health200/22bytes/0,41472 с; idle HTTP200/curl0/20bytes за60,185967 с; только begin, end отсутствует. Xray: `stream ID 1; INTERNAL_ERROR; received from peer` | FAIL: усечённый ответ, несмотря на200/exit0. Долгий upload не запускался | `owner-edge-stream-20260910T195443Z/summary.json` |
| 7 | Тот же private client/path/server/probe через loopback ingress28080, без CDN/TLS | idle HTTP200/curl0/38bytes за127,129371 с, оба маркера | PASS. Граница сбоя — CDN/H2-facing участок. В прямом контроле внешний HTTP — H1, поэтому нельзя объявлять транспортные версии идентичными | `loopback-idle-20260910T200502Z/summary.json` |
| 8 | Fixed edge; только private server `noSSEHeader=true` | idle HTTP200/curl0/20bytes за60,196116 с, end отсутствует | FAIL; не помогло. Исходный private config побайтно восстановлен, private active. Upload пропущен | `no-sse-20260910T201230Z/summary.json` |

## Что проверено в Yandex Console

Аутентифицированная консоль доступна. Существующий ресурс активен; origin group указывает на S4 ingress28080, origin protocol HTTP, Host передаётся от клиента. CDN cache и browser cache отключены. Разрешены GET/HEAD/OPTIONS. Правила location отсутствуют. В просмотренных формах редактирования ресурса и отдельного location нет поля response idle timeout. Это результат просмотра UI, а не доказательство отсутствия такой возможности у провайдера вообще.

Поля не менялись, новое правило не создавалось, Save не нажимался. Поддержке ничего не отправлялось. Без отдельного одобрения точного текста не отправлять обращение и не передавать данные владельца. Возможный вопрос ограничить фактическим поведением HTTP/2 и настройкой таймаута; не придумывать другое назначение сервиса, не включать VPN, конфиги, секреты или клиентские данные.

## Источник, backup и откат

- Исходные защищённые `client-cdn.json` и `client-uri.txt` используют DNS-имя, а присланный рабочий JSON владельца — фиксированный edge. Исходные файлы не изменены. Нельзя считать, что они совпадают с ответом фактически используемой подписки: её текущая выдача в этом этапе не проверена.
- SHA256 исходного client JSON: `e532a979463c7c10ae0211974a320e7d85428189dd1768787f0b18d7178f7345`; URI: `5bff0dd670ba4ffbf4d6d0437471fee4046bacdcc0170811c25a8e8231ac29b7`.
- Восстановленный private config SHA256: `2e22b6eaa365073a62966b463759ed07c5860af714013fe949c0c34df42f6a0a`.
- Private binary SHA256: `64d46afb80adea1bf97a0d467e83f4a9ac1ebd0995891e84bca3f1a1d1affb1d`.
- Неизменённый ingress config SHA256: `d0fb82bd61409ff633e5f85bd64d6df3104024de30798f5490339409d179cc54`.
- Root-only snapshot: `/var/backups/maestro-xray-cdn-private-20260910T180218Z`; redacted manifest SHA256 `da4a0d5bba831e62db327d1582c6dc08ecfa7662611f226e6d33184cb1092f61`.
- Сохранены private unit/config/binary и четыре protected evidence-файла. `rollback-private.py` проверен, но не запускался. Не применять другой lifecycle rollback к этому flat layout.
- Временные клиентские JSON/logs остаются в защищённых каталогах опытов; не переносить их без редактирования секретов в Git, память или сообщения.

## Важные различия и незавершённое

По read-only разбору исходника Xray26.7.28 (5ca6f4b) downstream `packet-up` не имеет штатного генератора heartbeat-данных: `transport/internet/splithttp/hub.go:348-421` только передаёт данные; padding-таймер `hub.go:196-237` относится к ответу streaming-upload. `hKeepAlivePeriod` — HTTP2 connection PING, не DATA конкретного stream. Mux-таймеры только закрывают пустые workers (`common/mux/client.go:226`, `server.go:122`); штатной отправки heartbeat нет. MLKEM пустой Write не отправляет данные (`proxy/vless/encryption/common.go:47`); произвольный padding в тело нарушит протокол. Перечисленные поля не пробовать как исправление этого обрыва без новых оснований. Следующий неразрешённый вопрос: 60с — именно отсутствие данных или общий предел времени ответа; текущие опыты с паузой сами по себе этого не различают.

1. Новый HTTP-запрос после125s и тот же открытый ответ с паузой125s — разные сценарии. Первый fixed-edge прошёл, второй через CDN не прошёл.
2. HTTP200/curl0 недостаточны: проверяется полный ответ, включая end marker/ожидаемый объём.
3. Nginx private route уже имеет proxy_read_timeout/proxy_send_timeout3600s и buffering/request_buffering off. Default Xray policy idle300s не объясняет peer reset через60s. Не увеличивать эти параметры вслепую.
4. HTTP2 connection PING (`hKeepAlivePeriod`, default45s) не равен данным внутри HTTP response stream; наблюдался peer RST_STREAM, а не доказанный client ping timeout.
5. S1 `/opt/maestro-cdn-probe/server.py` уже имеет `/idle`, `/stream` и GET-body echo до4MiB с проверкой Host. Можно использовать синтетическое тело; POST не реализован. Старое утверждение об отсутствии upload endpoint исправлено. Долгий upload пока не выполнен, этот пункт не объявлять пройденным.
6. Не повторять noSSE/XMUX догадки и не обновлять Xray без подтверждённой относящейся к сбою функции/исправления. Продолжение — точечная проверка штатного downstream heartbeat по исходнику26.7.28 и возможностей таймаута CDN; никакого изменения общего ресурса без сохранения commercial-пути.

По прямому указанию владельца фиксировать все следующие существенные результаты в этом индексе и одновременно поддерживать актуальными AGENTS.md/CONTEXT_HANDOFF.md в обоих рабочих каталогах. GitHub — после подтверждённого рабочего live-результата; текущий документ не означает выпуск или перенос кандидата клиентам.
