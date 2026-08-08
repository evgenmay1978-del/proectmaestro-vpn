# ops/ — repeatable MaestroVPN operations as tool-scripts

Applying the lesson from the training videos (esp. Anthropic-skills #5: *"if a task can be done with
code, do it with code"* → run a tested script instead of re-deriving the commands each session →
0 tokens, stable, fast). These encapsulate the operations I used to type out by hand every time.

Server operations run **on S1** (where the panel, telemetry, mirror and repo live).
The lightweight mobile eye-state preview below may run locally; heavy ring/atlas
generation, the full simulator, Gradle and APK builds run only on GitHub Actions
for the owner's weak computer.

## Mobile 4D assets and phone preview

- `mobile-4d-assets.py` validates the 15 source PNGs, builds the committed three-light atlas set and generated Kotlin geometry.
- `mobile-4d-art-check.py` — приёмка арт-чекпойнтов кита ДО переноса в `source/` и сборки атласа. Не рантайм: только читает PNG и печатает PASS/FAIL.
  ```bash
  python3 ops/mobile-4d-art-check.py --group arc
  python3 ops/mobile-4d-art-check.py --selftest
  ```
  Валит приёмку по числу: холст, RGBA8, ICC/APNG, alpha bbox, совпадение альфы между `_l/_c/_r`, реальная разница света, остаток magenta-кея, подъём купола, тепло материала (R−G) и доля тёплого рельефа. ⛔ Подсчёт секторов — СПРАВОЧНЫЙ (строка `ИНФО`): у резьбы полупрозрачные края, и один файл при разных порогах даёт от 0 до 14 «интерьеров» там, где глазом видно шесть. Количество секторов проверяется глазами.
  `--selftest` ломает картинки нарочно и требует, чтобы сторож это поймал — гейт, который ни разу не срабатывал, считается неработающим.
- `mobile-4d-assets.py` refuses to run unless the toolchain is exactly
  Pillow 11.3.0 + libwebp 1.5.0; committed atlas output and `--check` must be
  byte-stable. For the current eye-surround work install that pinned wheel in a
  GitHub Actions test/artifact job and run generation there. Do not use the
  owner's Windows computer or an ad-hoc S1 session as a fallback for atlas
  regeneration.
- `mobile-eye-state-preview.py` is the repeatable lightweight owner-preview for the
  exact installed-test Home baseline
  `design/mobile-4d-references/08-owner-installed-test-home-2026-08-08.jpg`
  (`591×1280`, SHA-256
  `9251457407f3aeee17b5281b32634e6c0d03e7fce3e9db12c16706444a9f800b`).
  ```powershell
  python ops/mobile-eye-state-preview.py
  python ops/mobile-eye-state-preview.py --output-dir build/mobile-eye-state-preview-qa --scale 1
  python ops/mobile-eye-state-preview.py --check
  ```
  It always writes `home-disconnected.png`, `home-connected.png` and
  `home-eye-states-comparison.png` in the selected output directory. The
  allowed-change mask is explicit; `04-owner-selected-home-2026-07-31.jpg` is
  used only as eye/eye-surround art-direction history, never as layout geometry.
  **This is a scripted preview, not a runtime screenshot, Android render, APK
  acceptance result or OTA authorization.**
- `phone-screen-sim.py` reconstructs Home from all eight committed centre-light atlas layers, then draws the measured owner-reference controls. It never uses a legacy flat Home image.
  - Fixed ownership: `wood/frame/cartouche/vines`, `ring`, Playfair title and living eye. Only `console/contacts/arc` receive `+25 dp - deckScrollDp`, clipped below `deckTop = 434 dp`.
  - The simulator uses the direct `890×635 / 822.5` eye registration plus the same bronze clip, eyelid contact shadow and inner occlusion profile as runtime; the preview is static, while the Android eye keeps blink/gaze/touch animation.
  - Deck geometry is copied 1:1 from `PhoneHomeReferenceLayout.kt` and `PhoneHomeControlDeck.kt`. **Change the Kotlin, change this file — otherwise the simulation lies.**
  - Outputs include the three VPN states, `owner-home-comparison.png`, `owner-home-connected-scrolled.png` and `owner-home-scroll-proof.png`; the last board proves that logo/eye are fixed while relief, tiles, icons and labels move together.
  - The removed support sentence must not reappear in runtime or simulator.
  - Current visual findings and acceptance evidence live in `design-qa.md`.
- `mobile-eye-natural-assets.py` rebuilds only the aligned `mobile_eye_open`, `mobile_eye_squint` and `mobile_eye_closed` resources and validates the existing anatomy layers. It never recreates a Home scene. The approved alpha support for every eyelid state is an immutable SHA-256 constant; never replace that contract with a comparison against the runtime WebP that the same script overwrites.

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
