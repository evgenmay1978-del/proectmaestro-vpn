# ops/ — repeatable MaestroVPN operations as tool-scripts

Applying the lesson from the training videos (esp. Anthropic-skills #5: *"if a task can be done with
code, do it with code"* → run a tested script instead of re-deriving the commands each session →
0 tokens, stable, fast). These encapsulate the operations I used to type out by hand every time.

## Обязательный барьер повторов

`maestro-repetition-guard.py` не даёт агенту повторить необъяснённо упавшую
операцию. Запускать из корня репозитория перед внешней/долгой/изменяющей
операцией и перед любой повторной попыткой:

```powershell
python ops/maestro-repetition-guard.py check --action s1-key-login --family openssh-key-probe
```

После первой ошибки или указания владельца, что способ неверен:

```powershell
python ops/maestro-repetition-guard.py fail --action s1-key-login --family puttygen-gui --reason-code hidden-interactive-prompt
```

Продолжать можно только после установления причины и регистрации действительно
другого способа:

```powershell
python ops/maestro-repetition-guard.py correct --action s1-key-login --old-family puttygen-gui --new-family openssh-key-probe --root-cause-code gui-needs-tty
python ops/maestro-repetition-guard.py check --action s1-key-login --family openssh-key-probe
python ops/maestro-repetition-guard.py success --action s1-key-login --family openssh-key-probe --evidence-code key-only-login-confirmed
```

Код `42` означает обязательную остановку, `43` — повреждённый/нечитаемый
журнал и тоже обязательную остановку. Локальный журнал находится в
`.maestro-state/repetition-guard.json`, исключён из Git и хранит только
смысловые коды и SHA-256 способов. Никогда не подставлять туда текст команды,
пароль, ключ, токен, URL подписки или данные клиента. Проверка:

Один результат `ALLOW` разрешает ровно одну смысловую операцию и один
исполняемый шаг. Нельзя объединять под одним `check` несколько проверок через
`;`, pipeline или один shell-вызов: `unittest`, syntax compile,
`git diff --check`, status и чтение diff получают отдельные
`check`/результат/`success`,
даже когда все они read-only. Иначе поздний успешный шаг может скрыть ранний
сбой, а вся составная попытка считается ошибочной.

```powershell
python -m unittest ops.test_maestro_repetition_guard -v
```

Server operations run **on S1** (where the panel, telemetry, mirror and repo live).
Heavy ring/atlas generation, the full simulator, Gradle and APK builds run only on GitHub Actions
for the owner's weak computer.

## GitHub Actions test/artifact handoff

`github-actions-artifact.py` replaces the error-prone manual dispatch, run lookup,
artifact download and ZIP extraction sequence with one tested command:

```powershell
python ops/github-actions-artifact.py --task android
```

The helper fails closed unless the worktree is clean, the current branch is exactly
`codex/mobile-4d-deck`, and local `HEAD` is already present at the same remote ref.
It dispatches only `android-test.yml` and the listed non-release Android task, then
accepts only a new `workflow_dispatch` run with that exact `head_sha`. The artifact
name must match the selected task exactly. Results are downloaded to
`build/github-artifacts/run-<id>/`, with `metadata.json`, `artifact.sha256`, the
original ZIP and a path-traversal-safe `extracted/` directory.

Authentication lookup order is `GH_TOKEN`, `GITHUB_TOKEN`, then
`git credential fill`; credentials are never written to metadata or printed. This
script has no GitHub Release or OTA endpoint and does not merge or publish anything.

When polling workflow status through an execution tool with a 30-second yield,
do not combine a blocking sleep with several GitHub API calls. Use an immediate
batch poll, or one API request after a short wait that leaves enough time for the
response; preserve the session ID whenever the command may outlive the yield.

## Isolated rqlite CI cluster

`ops/ha/ci-rqlite-cluster.sh` is a GitHub Actions-only harness for the HA control
plane. It downloads the pinned rqlite 10.1.0 archive, verifies its SHA-256 before
extraction, and starts three loopback-only voters below the runner's temporary
directory. It never connects to S1-S4 and contains no production credentials.

On the owner's computer run syntax checks only; do not run `start` or the full
contract because those download and start three rqlite processes:

```powershell
bash -n ops/ha/ci-rqlite-cluster.sh
bash -n ops/ha/test-ci-rqlite-cluster.sh
```

The full lifecycle, leader/voter checks, per-node foreign-key checks, backend
tests, race tests, vet and integration tests run in
`.github/workflows/ha-control-plane.yml`. Its `always()` cleanup stops only PIDs
recorded under the validated runner-temp root. A GREEN run on the exact pushed
SHA is required before proceeding to the checksummed control-plane schema.

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
  byte-stable. For atlas maintenance install that pinned wheel in a
  GitHub Actions test/artifact job and run generation there. Do not use the
  owner's Windows computer or an ad-hoc S1 session as a fallback for atlas
  regeneration.
- `phone-screen-sim.py` preserves the archived 4D Home layout from eight committed centre-light atlas layers and its measured reference controls. It is not a screenshot or a preview of the current `PhoneDashboard`.
  - Fixed ownership: `wood/frame/cartouche/vines`, `ring` and Playfair title. Only `console/contacts/arc` receive `+25 dp - deckScrollDp`, clipped below `deckTop = 434 dp`.
  - Outputs include the three VPN states, `owner-home-comparison.png`, `owner-home-connected-scrolled.png` and `owner-home-scroll-proof.png`.
  - The removed support sentence must not reappear in runtime or simulator.
  - Current visual findings and acceptance evidence live in `design-qa.md`.

The mobile 4D atlas retains the earlier Home artwork. Current phone controls stay in Kotlin; `mobile-dashboard-assets.py` packages the wood, frame and title artwork layers. TV assets and TV tools are outside this pipeline.

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
