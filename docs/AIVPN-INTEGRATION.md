# AIVPN: выдача ключей клиентам (backend)

Статус: код и Go-тесты готовы, на живом S1 **не проверялось** (см. §12). Ветка `feat/aivpn-tunnel`.

AIVPN — собственный UDP-протокол приложения (Rust, github.com/infosave2007/aivpn). Клиент AIVPN
живёт в VpnService приложения как отдельный движок, **не** как outbound sing-box. Поэтому ключ
`aivpn://…` физически не может ехать в sing-box JSON подписки: панель отдаёт его отдельным
эндпоинтом и, для подходящих установок, дублирует в /info.

## 1. Что реализовано

| Что | Где |
|---|---|
| Кодек ключа (Build/Parse/Validate, редакция секретов) | `backend/internal/aivpn/key.go` |
| Источник ключей: static / management REST / CLI | `backend/internal/aivpn/source.go`, `server.go` |
| Эндпоинт `GET /aivpn/<token>` и гейты | `backend/internal/api/aivpn.go` |
| Флаг в конфиге + роут + поле в /info | `backend/internal/api/api.go` |
| Чтение env и включение | `backend/cmd/maestro-panel/main.go` |
| Тесты | `backend/internal/aivpn/*_test.go`, `backend/internal/api/aivpn_test.go` |

Ключевой инвариант: панель **не умеет минтить** ключи (у неё нет приватного ключа сервера AIVPN) —
она только транслирует ключ, выпущенный сервером, и отказывает, если ключ не проходит Parse.

## 2. Публичный контракт

`GET /aivpn/<token>` — token тот же, что у подписки (per-customer секрет).

| Ситуация | Ответ |
|---|---|
| все гейты пройдены | `200 {"key":"aivpn://…"}`, `Cache-Control: no-store` |
| неизвестный token | `404` |
| подписка истекла/отключена | `402 subscription expired` |
| любой гейт не пройден (версия, whitelist, нет ключа) | `403 aivpn unavailable` |
| метод не GET | `405` + `Allow: GET` |

Один и тот же 403 на все причины — чтобы держатель token не мог зондировать конфигурацию гейтов.

`GET /sub/<token>/info` (как и раньше, отдаётся и для истёкших) при пройденных гейтах
дополнительно содержит:

```
{"login":"…","expires":"…","days_left":…,"active":true,
 "features":{"aivpn":true},
 "aivpn":{"key":"aivpn://…"}}
```

Правила, зафиксированные тестами:

* `/sub/<token>` (sing-box профиль) **никогда** не содержит ключ или слово `aivpn`;
* у неeligible клиентов (старое приложение, не из whitelist, истёкшая подписка) в /info нет ни
  `aivpn`, ни `features` — ответ как до фичи (пустой объект features не появляется);
* при выключенной фиче роут не регистрируется вовсе → 404.

## 3. Формат ключа

`aivpn://<base64url(JSON)>` (base64url **без padding**; при Parse панель принимает и
вариант с padding, чтобы ключи, скопированные руками, не падали).

```
{
  "s":  "host:port",              // публичный адрес сервера AIVPN
  "k":  "<base64 32 байта>",      // публичный ключ сервера
  "p":  "<base64 32 байта>",      // PSK клиента
  "i":  "10.77.0.2",              // VPN IP клиента
  "n":  {                          // сетевая конфигурация (snake_case)
    "client_ip": "10.77.0.2",
    "server_vpn_ip": "10.77.0.1",
    "prefix_len": 24,
    "mtu": 1280,
    "mdh_len": 20,
    "keepalive_secs": 8
  },
  "sk":  "<base64>",               // опционально: ключ подписи
  "mop": "<base64>"                // опционально: публичный ключ mask-operator
}
```

Проверки при Parse/Validate (то, что реально принимает Rust-клиент, плюс защита от мусора):

* `s` — строго `host:port` (SplitHostPort), порт 1..65535, без пробелов и слэшей;
* `k` и `p` — ровно 32 байта после base64 (std или raw-std). Панель требует `p`
  всегда, хотя клиент AIVPN допускает его отсутствие: сервер AIVPN выдаёт PSK всегда, и ключ без
  PSK означает битую выдачу;
* `n`: обязательны `client_ip` и `server_vpn_ip` (IPv4), `prefix_len` 1..32,
  `i` обязан совпадать с `n.client_ip`, `mtu` 0 или 500..65535, `mdh_len`/
  `keepalive_secs` 0..255, `ipv6_address` — IPv6 с `ipv6_prefix_len` 0..128;
* неизвестные поля разрешены (forward-compat: upstream добавляет `bd`/`bu`);
* `Key.String()` печатает только server/client — ключевой материал не попадает в логи через `%v`.

## 4. Гейты

Ключ выдаётся, только если **все** условия истинны (единая точка — `(*Server).aivpnKey`):

1. фича включена на этом хосте (`Source != nil && Enabled()`; по умолчанию nil);
2. подписка активна (`Customer.Active()`: не disabled и не истекла);
3. запрос от нашего приложения: `User-Agent` начинается с `SFA/` и versionCode >= `min_version_code`
   (у curl/Karing/старого клиента versionCode = 0 → отказ при любом минимуме);
4. login в whitelist (`logins` в static-конфиге или `MAESTRO_AIVPN_LOGINS`);
5. источник реально может отдать ключ (файл/кэш/сервер AIVPN) и ключ проходит Parse.

## 5. Включение (переменные окружения)

Полностью opt-in: без переменных фича выключена, поведение панели прежнее.

| Переменная | Значение |
|---|---|
| `MAESTRO_AIVPN_SOURCE` | `static` (по умолчанию) · `http` · `cli` |
| `MAESTRO_AIVPN_FILE` | путь к static-конфигу (§6.1); пусто/нет файла = OFF |
| `MAESTRO_AIVPN_LOGINS` | whitelist логинов через запятую (http/cli); charset `[A-Za-z0-9._@-]{1,64}` |
| `MAESTRO_AIVPN_MIN_VC` | минимальный versionCode приложения; при включённой фиче обязан быть > 0 |
| `MAESTRO_AIVPN_SOCKET` | unix-сокет management API; по умолчанию `/run/aivpn/api.sock` |
| `MAESTRO_AIVPN_CLI` | путь к `aivpn-server` (только cli) |
| `MAESTRO_AIVPN_CLI_CONFIG` | `--config` для cli (server.json) |
| `MAESTRO_AIVPN_SERVER_ADDR` | публичный `host:port`, который CLI вписывает в ключ |

Конфигурация, заданная но невалидная (битый JSON, пустой whitelist при enabled, min_version_code=0,
отсутствующие пути для cli), **роняет старт панели**: лучше не подняться, чем раздавать битые ключи.
Отсутствующий файл/пустое значение = фича выключена.

## 6. Источники ключей

### 6.1 static — пилот

`MAESTRO_AIVPN_SOURCE` не задан или `static`, файл в `MAESTRO_AIVPN_FILE`:

```
{
  "enabled": true,
  "min_version_code": 108,
  "logins": ["alice"],
  "keys": {"alice": "aivpn://…"}
}
```

Требования: при `enabled: true` — `min_version_code > 0`, непустые `logins` и `keys`, у
каждого login есть ключ, логины уникальны и валидны, каждый ключ парсится. Ключи здесь — ровно те
строки, что выдал сервер AIVPN; ничего не минтится.

### 6.2 http — management REST через unix-сокет (заготовка для S1)

`MAESTRO_AIVPN_SOURCE=http`. Панель ходит на `MAESTRO_AIVPN_SOCKET`:

1. `GET /api/v1/clients` — ищет клиента по `name == login`;
2. если нет — `POST /api/v1/clients {"name":"<login>"}`; `409` трактуется как «уже есть»;
3. `GET /api/v1/clients/<id>/connection-key` → `{"connection_key":"aivpn://…"}`;
4. ключ парсится и кладётся в память с TTL 10 минут; `Invalidate(login)` сбрасывает запись.

Отключённый/отозванный клиент (`enabled:false`) не отдаётся: панель вернёт «нет ключа»,
а не старый ключ. Сетевого порта у management API нет: только unix-сокет с правами файловой системы.

### 6.3 cli — вызов бинарника на S1

`MAESTRO_AIVPN_SOURCE=cli`; нужны `MAESTRO_AIVPN_CLI`, `MAESTRO_AIVPN_CLI_CONFIG`,
`MAESTRO_AIVPN_SERVER_ADDR`. Панель выполняет:

```
aivpn-server --config <server.json> --add-client <login>          # ошибка «уже существует» игнорируется
aivpn-server --config <server.json> --show-client <login> --server-ip <host:port>
```

и вытаскивает первую строку `aivpn://…` из вывода. Вывод бинарника в тексте ошибки
санитизируется: все ключи заменяются на `aivpn://[redacted]`, длина ограничена 200 символами —
ключ не должен попасть в лог панели. Логин перед подстановкой в argv проверяется по charset
(защита от инъекции аргументов). Процесс запускается без shell.

> Для пилота на S1 рекомендуется `http`: он не спавнит процесс на каждый выпуск и умеет
> различать «клиент отозван». `cli` — резервный путь, если management API недоступен.

## 7. Выпуск клиента (runbook)

1. Выпустить клиента на сервере AIVPN (S1): `POST /api/v1/clients {"name":"<login>"}`
   (или `aivpn-server --config … --add-client <login>`) и забрать `connection-key`.
2. Внести login в `MAESTRO_AIVPN_LOGINS` (http/cli) **или** в `logins` вместе с ключом в static-файле.
3. Задать/поднять `MAESTRO_AIVPN_MIN_VC` до versionCode сборки, в которой появился AIVPN-клиент.
4. Перезапустить панель (`systemctl restart maestro-panel`), проверить лог:
   `AIVPN key issuance enabled (min app versionCode N)`.
5. Проверить: `curl -s -H 'User-Agent: SFA/1.0.N (N; sing-box …)' https://<panel>/aivpn/<token>`
   → 200 и ключ; на телефоне — `/sub/<token>/info` содержит `features.aivpn`.

## 8. Отзыв клиента (runbook)

1. Убрать login из whitelist (`MAESTRO_AIVPN_LOGINS`/`logins`) — панель сразу перестаёт отдавать ключ
   и убирает `features.aivpn` из /info. **Уже выданный ключ продолжает работать на сервере.**
2. Отозвать клиента на сервере AIVPN: `POST /api/v1/clients/<id>/revoke`
   (или `aivpn-server --config … --remove-client <login>`). Для http-источника панель увидит
   `enabled:false` и перестанет отдавать ключ; запись в кэше живёт до 10 минут — при
   необходимости перезапустить панель.
3. Для одноразового клиента (`one_time`) сервер сам гасит его после первого подключения.

## 9. Риски

* **token = носитель ключа.** Кто владеет token подписки, получает ключ AIVPN. Это та же модель, что
  и у /sub; дополнительно ключ — полноценный сетевой креденшел. Утечка token → отзывать клиента на
  сервере, а не только прятать ответ.
* **Нет автоматического отзыва по истечении подписки.** Панель перестаёт выдавать ключ, но на
  сервере AIVPN клиент остаётся активным. Пока это ручной шаг; перед включением на всех нужен
  периодический sweeper (истёк → revoke). Не реализовано — см. §12.
* **Unix-сокет — root-equivalent.** `/run/aivpn/api.sock` не имеет HTTP-аутентификации, защищён
  только правами на файл. Не монтировать в контейнеры, не публиковать через socat/nginx; панели
  нужен доступ к сокету (группа/права), а не к сети S1.
* **Кэш 10 минут.** Отзыв на сервере не мгновенен для панели (кроме `Invalidate` при
  перезапуске). При аварийном отзыве — перезапуск панели или обращение к серверу напрямую.
* **Логи.** Ключ — секрет: в коде нет его печати (String() редактирует, вывод CLI санитизируется,
  ошибки не содержат ключ — это закрыто тестами). Но nginx/прокси могут логировать URL `/aivpn/<token>`;
  token и так секрет подписки, отдельного логирования ключа нет.
* **Публичный адрес/порт** (`s`) в ключе фиксируется сервером; при смене адреса нужно перевыпустить
  ключи, панель ничего не подставляет.
* **static-файл** содержит живые ключи: права `0600`, root, вне git.
* **Один источник на процесс.** `MAESTRO_AIVPN_SOURCE` выбирает одну реализацию; смешивать static и
  http в одном процессе нельзя (для пилота это нормально, для раскатки — нет).

## 10. Откат

1. Панель: убрать `MAESTRO_AIVPN_*` из env-файла панели и перезапустить панель. Фича исчезает
   целиком: `/aivpn/*` → 404, в /info нет `aivpn` и пустой `features` не появляется
   (поведение как до патча).
2. Код: `git revert <commit>` в `feat/aivpn-tunnel` (или удалить файлы `internal/aivpn`,
   `api/aivpn.go` и три правки в api.go/main.go). Миграций БД нет, формат /sub не менялся.
3. Сервер AIVPN на S1 при откате **не трогается**: выданные ключи живут, пока их не отзовут (см. §8).
4. Бэкап перед выкаткой: копия env-файла панели и файла static-конфига (вне репозитория).

## 11. Тесты

```
cd backend
go test ./internal/aivpn/              # кодек + оба источника (stub unix-сокет / stub CLI, S1 не вызывается)
go test ./internal/api/ -run TestAivpn # гейты эндпоинта и /info
go vet ./internal/aivpn/ ./internal/api/
```

`internal/api` в целом содержит 3 падающих теста (`TestAdminCustomerReturnsCanonicalDeliveryChoices`,
`TestSubscriptionDeliveryUsesTask9ClientContract`,
`TestTask15EntitledLinksFailuresReturnServiceUnavailable`), а также пакеты
`shadowbilling`, `sidecaragentclient`, `subgen` — это **предсуществующие** падения на HEAD
(проверено прогоном на чистом worktree HEAD: набор падающих тестов совпадает, новых нет).

## 12. Не проверено / ограничения

* Живой S1: management-сокет, реальные флаги `aivpn-server` и настоящая выдача ключа не проверялись.
  Тесты используют локальные stub-сокет и stub-скрипт, S1 и прод не задействованы.
* Реальный APK: versionCode и User-Agent настоящей сборки с AIVPN не проверены (гейт проверен на
  синтетических UA вида `SFA/1.0.N (N; …)`).
* `MAESTRO_CONTROL_PLANE=rqlite`: другой composition root и другой handler — AIVPN там **не**
  зарегистрирован. Пилот рассчитан на legacy-режим (по умолчанию); для rqlite нужна отдельная проводка.
* Нет admin-эндпоинтов панели для выпуска/отзыва (сейчас только env/файл и сервер AIVPN).
* Нет автоматического отзыва по истечении подписки (§9).
* Таймаут обращений к management API — 10 с по умолчанию, отдельной настройки нет.

## 13. Зона изменений

Только `backend/**` и `docs/**`: `backend/internal/aivpn/{key,source,server}.go` и тесты,
`backend/internal/api/{aivpn.go,aivpn_test.go}`, правки `backend/internal/api/api.go` и
`backend/cmd/maestro-panel/main.go`, этот документ. `app/**` и `.github/workflows` не тронуты.
