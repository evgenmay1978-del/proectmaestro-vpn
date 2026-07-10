# olcRTC carrier «MAX» — статус и что осталось

**Статус: ЗАГОТОВКА (staged), НЕ рабочая end-to-end.** Плюмбинг в этом репозитории готов,
но выбрать/назначить провайдер `max` нельзя — он жёстко заблокирован (`olcMaxCarrierReady = false`)
до тех пор, пока сам бинарник olcrtc не научится подключаться к звонку MAX. Причина — ниже.

## Что такое MAX

MAX (`max.ru`, Android-пакет `ru.oneme.app`) — российский мессенджер от VK. Звонки построены
на WebRTC, есть **групповые звонки по ссылке** (до ~20 участников) и web-версия. Это делает MAX
кандидатом в carrier для olcRTC (по аналогии с Яндекс.Телемостом и WbStream): туннель
маскируется под видеозвонок, а трафик идёт через медиасервер сервиса.

## Как устроен carrier в olcRTC (важно понять до реализации)

Бинарник `libolcrtc.so` собирается НЕ из этого репозитория, а из внешнего
`github.com/openlibrecommunity/olcrtc`, пиннутого в `version.properties`
(`OLCRTC_REF`). Именно там живёт поддержка провайдеров, а не в нашем backend/app.

Carrier у olcRTC двухслойный:

| Слой | Где | Что делает |
|------|-----|------------|
| **provider** (auth) | `internal/auth/<name>/` | получает креды для комнаты; интерфейс `Engine() / DefaultServiceURL() / Issue(ctx, cfg) (Credentials, error)`; регистрируется `auth.Register("<name>", …)` |
| **engine** (SFU) | `internal/engine/<name>/` | говорит на конкретном сигналинге медиасервера |

Привязки в пиннутом бинарнике (`OLCRTC_REF=3b68e0f…`):

| provider | engine | как присоединяется |
|----------|--------|--------------------|
| `jitsi` | `jitsi` | комнатный MUC/Jingle, без регистрации |
| `telemost` | `goolom` | Yandex Telemost, ссылка на комнату |
| `wbstream` | `livekit` | Wb API + account-token, bare room id (UUID) |
| `none` | любой | ручной режим: URL медиасервера + токен задаются вручную |

**В пиннутом бинарнике провайдера `max` НЕТ** (проверено: `internal/auth/` содержит только
`jitsi/`, `telemost/`, `wbstream/`). Поэтому `provider: max` в `client.yaml`/`server.yaml`
будет отвергнут → exit не присоединится, olcRTC для этого login останется нерабочим.

## Что реально требуется, чтобы MAX заработал

1. **Реализовать провайдер `max` в upstream `openlibrecommunity/olcrtc`:**
   пакет `internal/auth/max/`, реализующий `Provider`:
   - `Engine()` — какой SFU у MAX (см. неизвестное №1);
   - `DefaultServiceURL()` — базовый URL медиасервиса MAX;
   - `Issue(ctx, cfg)` — превратить ссылку на звонок в join-credential (см. неизвестное №2);
   - регистрация `auth.Register("max", …)`.
2. Если MAX работает на **собственном** стеке VK, а не на LiveKit/Jitsi — нужен ещё и новый
   **engine** `internal/engine/max/` с реализацией сигналинга MAX (объём — reverse-engineering
   закрытого протокола).
3. **Поднять `OLCRTC_REF`** в `version.properties` на коммит с этим провайдером → workflow
   `olcrtc-bin.yml` пересоберёт `libolcrtc.so` с поддержкой `max`.
4. Снять гейт: `olcMaxCarrierReady = true` (backend) и `READY` в `ops/olcrtc-room.sh`,
   вернуть пункт «MAX» в выпадающий список панели.

## Два неизвестных (нужны данные владельца / решение)

1. **Какой SFU/engine у звонков MAX?**
   - Если **LiveKit** — переиспользуем существующий `livekit`-engine (как wbstream), нужен только
     провайдер `max` с MAX-специфичным `Issue()`. Тракт короткий.
   - Если **собственный VK-стек** — нужен новый engine + разбор проприетарного сигналинга. Тракт
     длинный и рискованный.
   - Публично НЕ подтверждено. Требуется проверка (трафик web-версии звонка MAX / документация VK).
2. **Как получить join-credential из ссылки MAX?** API join-by-link у MAX закрытый и не
   документирован. Варианты: (а) официальный API/токен MAX, если доступен владельцу; (б) режим
   `provider: none` — если владелец умеет вручную извлечь из звонка MAX URL медиасервера + токен
   (сработает ТОЛЬКО если engine MAX — один из поддерживаемых, т.е. LiveKit/Jitsi/goolom).

## Что уже сделано в этом репозитории (staged, за гейтом)

- backend `olcconf` — per-login `Provider`-override уже generic, хранит любой carrier-строкой;
- backend panel/admin — `max` распознаётся как URL-carrier (room = http(s) URL), но **отклоняется**
  гейтом `olcMaxCarrierReady=false` с понятным сообщением, пока бинарник не поддерживает MAX;
- app (ТВ) — общий helper `olcrtcCarrierLabel()` уже умеет метку «через MAX» (сработает, когда
  бэкенд начнёт отдавать `provider:"max"`);
- ops `olcrtc-room.sh` — распознаёт `max` (room=URL, анонимный join), но за флагом `READY`.

Ничего из этого не «включает» MAX — только готовит обвязку к моменту, когда бинарник olcrtc
получит провайдер `max`.
