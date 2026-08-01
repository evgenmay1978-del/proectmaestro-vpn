# ops/ — repeatable MaestroVPN operations as tool-scripts

Applying the lesson from the training videos (esp. Anthropic-skills #5: *"if a task can be done with
code, do it with code"* → run a tested script instead of re-deriving the commands each session →
0 tokens, stable, fast). These encapsulate the operations I used to type out by hand every time.

All run **on S1** (where the panel, telemetry, mirror and repo live).

## Mobile 4D assets and phone preview

- `mobile-4d-assets.py` validates the 15 source PNGs, builds the committed three-light atlas set and generated Kotlin geometry.
- `mobile-4d-assets.py` refuses to run unless the toolchain is exactly Pillow 11.3.0 + libwebp 1.5.0 — the atlas is committed and `--check` has to be byte-stable. S1 ships Pillow 12.3.0, so run it from the pinned venv:
  ```bash
  /root/.venvs/maestro-mobile4d/bin/python ops/mobile-4d-assets.py
  ```
  Create it once with `python3 -m venv /root/.venvs/maestro-mobile4d && /root/.venvs/maestro-mobile4d/bin/pip install pillow==11.3.0` (that wheel bundles libwebp 1.5.0 — verified 2026-08-01).
- `phone-screen-sim.py` reconstructs Home from the committed centre-light atlas geometry, then draws the owner-reference control deck over it. It does not depend on a legacy flat Home image.
  - The `ring` layer is composited separately so it can carry the same `heroTranslationY` as the eye; wood/frame/cartouche/vines stay on the original crop.
  - Deck geometry is copied 1:1 from `PhoneHomeReferenceLayout.kt` and the constants of `PhoneHomeControlDeck.kt`. **Change the Kotlin, change this file — otherwise the simulation lies.**
  - Outputs into `build/phone-screen-sim/`: `owner-home-connected.png`, `owner-home-connecting.png`, `owner-home-disconnected.png` (780×1688 = 390×844@2x), `owner-home-comparison.png` (owner reference beside the connected preview) and `phone-screens.png` (all three eye states).
  - Measured deltas against the reference and the blocking asset gap live in `design-qa.md`.
- `mobile-eye-natural-assets.py` rebuilds only the aligned `mobile_eye_open`, `mobile_eye_squint` and `mobile_eye_closed` resources and validates the existing anatomy layers. It never recreates a Home scene.

The mobile 4D atlas is the single Home-art pipeline. Keep text and the eye separate; do not add a flattened Home drawable back. TV assets and TV tools are outside this pipeline.

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
