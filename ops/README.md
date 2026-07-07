# ops/ — repeatable MaestroVPN operations as tool-scripts

Applying the lesson from the training videos (esp. Anthropic-skills #5: *"if a task can be done with
code, do it with code"* → run a tested script instead of re-deriving the commands each session →
0 tokens, stable, fast). These encapsulate the operations I used to type out by hand every time.

All run **on S1** (where the panel, telemetry, mirror and repo live).

| script | when to use | safety |
|--------|-------------|--------|
| `deploy-panel.sh [--dry-run]` | after editing the Go backend — build + deploy maestro-panel | verifies /healthz + /order/tariffs + service active; **rolls back** the binary on failure. `--dry-run` = build+vet only. |
| `verify-ota.sh [--sync]` | after cutting an OTA release — confirm the chain reached the fleet | read-only (—sync triggers the mirror upload). **Fails loudly if the 107 waypoint breaks.** |
| `crash-reports.sh` | check the fleet's real crashes — read this, don't wait for «клиенты говорят» | read-only. |

The orient/snapshot script lives separately at `/root/.claude/maestro-orient.sh` (run by the
SessionStart hook). Memory index: see `MEMORY.md` → §🖥️ / §🎓.

## preview.sh / home-preview.sh — показать владельцу картинку без повторного вывода кода
Инлайн-доставка файлов в его мобильный клиент битая → канал = публичный JPG-URL на Яндексе.
- `ops/preview.sh <img> [<img>…]` — конвертит любой webp/png/jpg в мобильный JPG, льёт в
  публичный `preview/` бакета maestro-apk, печатает URL. `--clean` чистит preview/.
- `ops/home-preview.sh` — пересобирает сравнение ОТКЛЮЧЁН/ПОДКЛЮЧЁН из ТЕКУЩИХ запечённых
  фонов (`home_backdrop*.webp`), меряет % заполнения гнезда, льёт 3 картинки, печатает URL.
⛔ Не переписывать этот конвейер в чате заново — это 0-токенный повтор, зови скрипт.

## socket-fit.sh — вписать центральный элемент (изумруд/глаз) в гнездо на N%
Повторяемая операция «сделай как на эскизе / увеличь / отцентруй». По умолчанию НЕ трогает
рабочий файл — пишет temp + льёт превью + печатает URL; `--apply` = бэкап + запись на месте.
`ops/socket-fit.sh <backdrop.webp> <emerald|eye> [pct=100] [--apply]`
Геометрия гнезда этого кадра зашита: центр (428,711), радиус 231px. Для симметричного изумруда
результат идеальный; для глаза даёт заполнение, но зрачок может требовать до-центровки.

## ⛔ ПЕРВЫМ ДЕЛОМ — проверь, нет ли готового скрипта (не деривить в чате заново)
Крашы→`ops/crash-reports.sh` · здоровье S1/S2/S3→`~/.claude/maestro-healthcheck.sh` ·
OTA→`ops/verify-ota.sh` · дрейф деплоя→`ops/deploy-status.sh` · показать картинку→`ops/preview.sh`/
`home-preview.sh` · вписать элемент→`ops/socket-fit.sh`. Любой повтор ≥2 раз → сюда, в ops/.

## socket-transplant.sh — вписать элемент из ЭТАЛОНА владельца (метод «как изумруд вырезал»)
Лучший метод для «сделай как на эскизе, чётко»: берёт пиксели из референс-скриншота, выравнивает
по сокету (зелёная окантовка/интерьер), масштабирует, feather-вставляет. НЕ авто-ресайз (тот давал
кривой серп). Preview по умолчанию, `--apply` = бэкап в /root/.claude/maestro-asset-backups + запись.
`ops/socket-transplant.sh <reference-image> <target-backdrop.webp> [--apply]`
