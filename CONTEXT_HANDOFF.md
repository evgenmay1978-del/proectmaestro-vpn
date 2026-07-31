# MaestroVPN — актуальный контекст и передача работы

## 0. LIVE: mobile 4D реализация завершена, GitHub CI и test APK готовы

Этот раздел новее остальных и имеет приоритет, если ниже встречается устаревшая формулировка.

### НОВЕЙШИЙ implementation checkpoint — АВТОРИТЕТНЫЙ

- Ветка: `codex/mobile-4d-interface`.
- Последний implementation HEAD перед этим docs-checkpoint: `8d1a431`.
- Последовательность UI-коммитов:
  - `1ec8bb9` — общий phone-only `MobilePremium4DShell` и premium component kit;
  - `77dec2b` — phone purchase flow перенесён на 4D shell;
  - `7b09aa2` — phone QR scanner/error surfaces и share dialog перенесены на premium kit;
  - `67e567a` — phone split-tunnel presentation перенесён на premium shell;
  - `e197617` — исправлены dismissal и адаптивная ширина phone share dialog;
  - `c15b4e3` — удалён старый flat Home, mobile tooling переведён на 4D atlas;
  - `8d1a431` — исправлен пакет импорта Compose `zIndex`, обнаруженный первой CI-сборкой.
- Task 7 завершает визуальную миграцию ровно **6 достижимых phone-экранов + 1 dialog**:
  `tvhome`, `claim`, `trial`, `buy`, `scanqr`, `split` и `IosKaringDialog`.
- Полная матрица source composables, состояний и исключений находится в
  `docs/mobile-screen-coverage.md`.
- Phone Home использует полный многослойный `Mobile4DHome`; пять внутренних экранов используют
  лёгкий `MobilePremium4DShell` без удержания Home atlas; share остаётся dialog поверх Home.
- TV остаётся отдельной неизменённой presentation-веткой universal APK: `TvEskizHome`,
  `TvEskizSpec`, D-pad/focus/Back, TV geometry, `tvm_*` и TV tooling не переносились на phone kit.
- Локальная Android/Gradle/APK-сборка не выполнялась по прямому запрету владельца о слабом ПК.
  GitHub Actions run `30645395284` (№239) успешно выполнил `assembleOtherDebug`, загрузил
  test APK и завершил `testOtherDebugUnitTest` без ошибок.
- Cleanup и combined review завершены: старый `mobile_home_scene.webp` удалён, app/ops больше
  не содержат его потребителей, два найденных замечания share dialog закрыты повторным review.
- Test APK artifact `8799326567` (`maestrovpn-tv-test-apk`) имеет размер `153712227` байт,
  SHA-256 `1044d64e805fb2ac72f9c430e74ba4973ae2eaedebe24a0dbff6580d19894fda`
  и не истёк. Следующий внешний шаг — ручной просмотр владельцем на телефоне. Не делать merge,
  GitHub Release, production signing или OTA.

### Текущее Git-состояние

- Изолированный worktree:
  `C:\Users\User\Documents\Codex\2026-07-30\github-plugin-github-openai-curated-remote\work\proectmaestro-vpn-mobile-4d`.
- Ветка реализации: `codex/mobile-4d-interface`.
- База ветки: `1019339ac29135e79c9901b8e562a2cbe240c06a`
  (`codex/mobile-4d-reference-pack`).
- Последний implementation HEAD перед docs-checkpoint: `8d1a431`.
- Уже созданы локальные коммиты:
  - `ba11ff8` — исправленный scope/план: ровно 6 экранов + 1 dialog;
  - `3d0902d` — чистая модель scene/crop/light/parallax/eye + JVM tests;
  - `a1b1bbf` — deterministic memory-safe atlas generator, manifest, tests и 24 WebP;
  - `ac5defe` — path-safe, interruption-recoverable asset transaction + focused tests;
  - `a3b9cfc` — durable checkpoint этого handoff;
  - `f54c0fe` — lifecycle-safe tilt и memory-budgeted bitmap loader;
  - `5acc6db` — cancellation/OOM-safe bitmap ownership и actual allocation gate;
  - `e34335e` — durable runtime checkpoint этого handoff;
  - `ad7f60d` — чистый mobile 4D Home compositor и eye-state tests;
  - `d5228d9` — hero atlas переведён на обязательный `FilterQuality.High`;
  - `ba87356` — новый Home подключён в phone seam, старое phone-дерево удалено;
  - `1d423f7` — durable checkpoint подключённого mobile 4D Home;
  - `1ec8bb9` — общий lightweight phone-only premium shell;
  - `77dec2b` — purchase phone presentation на premium 4D shell;
  - `7b09aa2` — QR/share phone presentation на premium surfaces;
  - `67e567a` — split phone presentation на premium shell;
  - `e197617` — исправления premium phone dialog после review;
  - `c15b4e3` — удаление obsolete mobile flatten и ремонт phone tooling;
  - `6608f0d` — durable docs handoff перед CI;
  - `8d1a431` — исправление импорта Compose `zIndex` после CI run №238.
- Ветка отправлена в `origin/codex/mobile-4d-interface`; открыт draft PR
  [№74](https://github.com/evgenmay1978-del/proectmaestro-vpn/pull/74).
- Исходный worktree `work/proectmaestro-vpn` не использовать для реализации. Его ложный
  `M ops/phone-screen-sim.py` связан с CRLF; отдельный implementation-worktree создан именно
  для чистой работы.

### Последнее решение владельца — ОБЯЗАТЕЛЬНО

Владелец сначала потребовал все реально существующие mobile-экраны, а затем отдельно
исправил завышенный подсчёт:

> «У меня нет столько экранов в приложении моем».

Следовательно, scope определяется не количеством зарегистрированных внутренних routes, а
фактическими переходами из обычного мобильного запуска. Это **6 экранов + 1 dialog**.
Android TV остаётся строго вне scope. Нельзя менять поведение `TvEskizHome`, TV focus/D-pad/Back,
`tvm_*`, TV-геометрию или TV-симуляторы.

Новое прямое решение владельца по сборке:

> «На компьютере ты не соберешь у меня слабый комп. на гитхаб собери тестовый апк я посмотрю».

Поэтому не выполнять локальную Android APK-сборку на компьютере владельца. После появления
визуально рабочего 6-screen flow отправить implementation branch на GitHub и запускать
`.github/workflows/android-test.yml`. Результат — только test APK artifact для ручного просмотра
владельцем. Запрещены GitHub Release, production signing, merge, OTA и обновление пользователей.

### Инвентарь реальных mobile-экранов

Из обычного запуска пользователь реально достигает:

- `tvhome` — главный экран, встроенный выбор протокола и account card;
- `claim` — ввод логина/кода;
- `trial` — пробный период;
- `buy` — тарифы, оплата и все состояния оплаты;
- `scanqr` — камера, ручной ввод, permission/error states;
- `split` — выбор приложений для VPN;
- `IosKaringDialog` — dialog подключения/передачи на другой телефон, не отдельный экран.

Итого: ровно **6 экранов + 1 dialog**.

Не считать отдельными экранами:

- выбор протокола и account card — секции `tvhome`;
- «отключено / подключение / подключено» — состояния `tvhome`;
- тарифы / ожидание оплаты / подтверждение / активация / done / error — состояния `buy`;
- permission/error — состояния `scanqr`;
- поиск/выбор/предупреждение — состояния `split`.

Внешний OS-intent import/profile flow (`profile/new`, `profile/edit/{id}` и вложенные editor
routes) не входит в обычный MaestroVPN mobile flow и исключён из текущей визуальной работы.
`Dashboard`, `Connections` и `Tools` не зарегистрированы в активном `SFANavHost`.

### Обнаруженная проблема reachability

`Settings`, `Log`, `Groups` и их дочерние routes зарегистрированы, но фактически скрыты:
`bottomNavigationScreens` и `railScreens` пусты, переходов с Home нет,
`pendingNavigationRoute` нигде не получает значение. По последнему уточнению владельца они
**не считаются экранами его текущего приложения и исключены из scope**. Не рестайлить их и не
возвращать в навигацию без отдельного прямого запроса.

### Утверждённая техническая архитектура 4D

Нельзя загружать 15 исходных PNG обычным `painterResource`/`ImageBitmap.imageResource`:
15 × 2160×4670 ARGB требуют примерно **577 MiB decoded RAM**.

Безопасная схема:

1. Сохранить master PNG 2160×4670 в `design/mobile-asset-redraw/source`.
2. Детерминированный generator режет **все пять слоёв** сеткой 3×8.
3. У RGBA убираются прозрачные поля; для каждого fragment сохраняется одинаковая геометрия
   `_l/_c/_r`.
4. Добавляется 2 px edge-extruded gutter.
5. Fragments пакуются в одинаковые для L/C/R atlas pages не больше 2048×2048, чтобы не упереться
   в texture limit старых API 23 GPU.
6. Generator реконструирует atlas обратно в canvas и попиксельно сравнивает с source.
7. Runtime декодирует страницы сразу под физическую ширину viewport, шаг 64 px, максимум 1620.
8. На нормальном устройстве все три направления могут оставаться resident; за кадр рисуются
   только два ненулевых света. На low-RAM — только `_c`, максимум 1080, tilt-light выключен.
9. Бюджет decoded-art — не более 35–40% `ActivityManager.memoryClass`.

Ориентир полной трёхсветовой памяти atlas-комплекта: около 62 MiB при scene width 1110,
111 MiB при 1480, 133 MiB при 1620; существующий eye добавляет около 7.5 MiB.

Прозрачные relief-слои смешиваются внутри изолированного `saveLayer` через
premultiplied `BlendMode.Plus`; обычный последовательный `SrcOver` создаёт alpha dip/кайму.
Wood можно смешивать обычным centre + active-side crossfade.

Предлагаемые runtime-файлы:

- `Mobile4DSceneModel.kt` — чистая математика света/crop/parallax/memory profile;
- `Mobile4DTilt.kt` — lifecycle-safe `TYPE_GAME_ROTATION_VECTOR`, fallback
  `TYPE_ROTATION_VECTOR`, калибровка, display remap, ±12°, low-pass;
- сгенерированный atlas manifest;
- `Mobile4DBitmapStore.kt` — последовательный IO decode и lifecycle;
- `Mobile4DScene.kt` — единый Canvas;
- `Mobile4DHome.kt` — eye/title/connect/revolver;
- общий phone-only `MobilePremium4DShell`/background/dialog для пяти дочерних экранов normal flow.

Глубина: wood ≈0.5 dp, frame 1.5 dp, cartouche 2.5 dp, vines 3.5 dp,
ring/eye 5 dp. Тени рисуются кодом повторным tinted alpha draw; fullscreen blur запрещён.

### LIVE checkpoint реализации — после Task 2

Task 1 принят отдельным review без замечаний:

- commit `3d0902d`;
- `Mobile4DSceneModel.kt` остаётся Android/Compose-free;
- тестами зафиксированы 2160×4670, ContentScale.Crop, L/C/R weights, hysteresis,
  глубины parallax и три состояния глаза;
- локальный Gradle не дошёл до компиляции: wrapper 9.3.1 не скачивается из sandbox.
  Это остаётся CI-gate, а не локальный PASS.

Task 2 завершён коммитами `a1b1bbf..ac5defe` и принят scoped re-review:

- 77 logical fragments;
- 8 одинаковых layouts для каждого направления света;
- 24 lossless WebP, всего 41 749 476 bytes;
- все pages ≤2048×2048;
- source→atlas→source reconstruction exact для всех 15 layers;
- `python ops/mobile-4d-assets.py` — PASS;
- `python ops/mobile-4d-assets.py --check` — PASS, output byte-stable;
- Pillow 11.3.0 и libwebp 1.5.0 закреплены как deterministic toolchain;
- WebP method 0 выбран из-за стабильности на слабом компьютере; это увеличило payload
  на 6 896 538 bytes (≈19,8%), но не decoded RAM и не точность;
- eye assets, old flatten, UI, TV не менялись.

Scoped re-review подтвердил `ADDRESSED` для обоих прежних Important findings:

1. Atlas, manifest и все их ancestors проходят lexical/resolved-path и symlink/reparse check
   до создания каталогов или замены файлов.
2. Persistent fsynced transaction journal восстанавливает согласованные atlas + manifest после
   `KeyboardInterrupt`, kill/process crash и проверяет committed inventory по SHA-256.

Focused safety tests: **7/7 PASS**. Независимый `--check`: **PASS**, reconstruction exact,
output stable. Состояние Task 3 зафиксировано в следующем checkpoint.

### LIVE checkpoint реализации — после Task 3

Task 3 завершён коммитами `f54c0fe..5acc6db` и принят scoped re-review:

- `TYPE_GAME_ROTATION_VECTOR` с fallback на `TYPE_ROTATION_VECTOR`;
- lifecycle registration только в `RESUMED`, neutral state при pause/dispose/no sensor/reduced motion;
- калибровка стабильной нейтрали, display rotation remap, dead zone, ±12° clamp и elapsed-time low-pass;
- target buckets 64 px, caps 1620/1080, 40% memory-class budget и ≈8 MiB reserve под глаз;
- internal-screen mode не удерживает home atlas;
- centre/active-side/all-lights retention выбирается по памяти и проверяется повторно по
  фактическому `Bitmap.allocationByteCount`;
- sequential IO decode, centre fallback, hysteresis и bounded retry при смене стороны;
- reference-counted leases не recycle bitmap, пока предыдущий Compose draw может его видеть;
- prompt cancellation, OOM и partial retain проходят transactional cleanup/rollback.

В первом review было 3 Important finding; fix round 1/5 закрыл все 3 (`ADDRESSED`), новых
регрессий scoped review не нашёл. Добавлены pure tests для cancellation handoff, actual budget,
OOM rollback и partial-retain rollback. Локальный Gradle/APK не запускался; compile/GREEN остаётся
GitHub CI gate. Состояние Task 4/5 зафиксировано в следующем checkpoint.

### LIVE checkpoint реализации — после Task 5

Task 4 завершён коммитами `ad7f60d..d5228d9` и принят review:

- чистый `Mobile4DHome` без `mobile_home_scene.webp` fallback;
- wood → frame → cartouche → vines → ring → existing eye → Playfair title → revolver;
- L/C/R relighting, bounded additive relief, runtime shadows и parallax;
- atlas fragments рисуются с `FilterQuality.High`;
- disconnected eye = `opennessOverride=0f`, connecting = `0.5f`, connected = living/null;
- sensor-rate updates изолированы от `PhoneRevolverMenu`;
- instrumentation tests фиксируют eye semantics/click и старые premium tags.

Task 5 завершён коммитом `ba87356` и принят review:

- phone-ветка `TvHomeScreen` теперь содержит один `Mobile4DHome`;
- старые flat scene/glow/eye/menu layers удалены из phone Compose tree, а не скрыты;
- оба call site передают `serviceStatus == Status.Starting` как `connecting`;
- `connected = Started || Starting` и все callbacks сохранены;
- diff `TvEskizHome`, `TvEskizSpec`, `tvm_*`, ресурсов и tools пуст.

### Исторический checkpoint реализации — Task 6

Task 6 реализован в коммите, содержащем этот checkpoint (`feat: add shared mobile premium 4D shell`):

- `MobilePremiumScreen` обратно совместимо делегирует новому phone-only `MobilePremium4DShell`;
- shell учитывает safe-drawing/IME insets, compact/regular/expanded layout и сохраняет 48 dp Back;
- фон не использует `mobile_surface.webp`, home atlas, cartouche, ring или eye: только сдержанный
  walnut/light draw и существующая настоящая nine-patch `frame_panel`;
- добавлены общие Playfair top bar, dialog/sheet surfaces, setting row и switch;
- shell возвращает нейтральный content-only fallback, если его ошибочно вызвали на TV; нормальные
  TV-ветки и TV-файлы не менялись;
- тесты добавлены до production API; Android/Gradle по прямому запрету владельца локально не
  запускались, поэтому compile/GREEN остаётся GitHub CI gate;
- статические scope-проверки и `git diff --check` прошли.

На момент этого checkpoint следующей была Task 7; её актуальный результат зафиксирован в
новейшем checkpoint в начале раздела 0.

### Состояния глаза

- `Stopped`/`Stopping`/«Отключено» — глаз полностью закрыт.
- `Starting`/«Подключение…» — отдельное полуоткрытое состояние.
- `Started`/«Подключено» — открыт, моргает, смотрит и реагирует на touch как сейчас.

Для этого добавить `connecting: Boolean = false` в `TvHomeScreen`, передавать
`serviceStatus == Status.Starting` из обоих call sites `SFANavigation`. TV этот параметр
игнорирует.

### Повторно используемый premium-кит

Не переписывать без причины:

- `premium/MobilePremiumSurface.kt`;
- `premium/MobilePremiumControls.kt`;
- `premium/MobilePremiumLayout.kt`;
- `premium/MobilePremiumTokens.kt`;
- `fantasy/FantasyDialog.kt`;
- `fantasy/FantasyFrame.kt`;
- `fantasy/FantasyListRow.kt`;
- `fantasy/FantasyToggle.kt`;
- `fantasy/CarvedKit.kt`.

`Claim`, `Trial` и большая часть `Buy` уже используют premium controls. Основная задача пяти
дочерних экранов normal flow — заменить плоский `mobile_surface` единым лёгким 4D shell и
хирургически убрать donor Material UI только в `scanqr`/`split`, не меняя callbacks/данные.
Скрытые Settings/Log/Groups/profile composables не трогать.

### Глобальные overlays, входящие в scope

Premium shell нужен также для app-owned dialogs/sheets:

- `IosKaringDialog`;
- `SelectableMessageDialog`;
- `UpdateDialog`;
- QR permission/error states;
- dialogs в normal-flow `PerAppProxyScreen`;
- service/update/download dialogs, реально показываемые поверх normal flow в `MainActivity`.

OS-intent-only import/profile dialogs и скрытый groups `ModalBottomSheet` исключены.

Системный Android permission dialog стилизовать невозможно и не требуется; стилизуется только
app-owned pre-permission explanation.

### Что уже сделано в этой implementation-сессии

- полностью прочитаны project handoff/spec/reference docs;
- визуально открыт `PREVIEW_c.png` и boards `01`, `02`, `03`;
- подтверждён безопасный seam: TV остаётся в `if (isTv) { TvEskizHome(...) }`, phone `else`
  заменяется clean mobile composable;
- подтверждено, что старый phone glow/web находится вне `else` в `TvHomeScreen` и должен быть
  удалён, а не остаться под новой сценой;
- старый `mobile_home_scene.webp` удалён; simulator реконструирует Home из centre-light 4D atlas,
  а eye generator больше не создаёт плоскую сцену;
- создан isolated worktree/branch;
- материализованы sparse paths `design`, `docs`, `gradle`, `.github`;
- подробный TDD-план исправлен после замечания владельца: scope = 6 normal-flow screens +
  `IosKaringDialog`, а не все зарегистрированные routes;
- Task 1 реализован и принят review (`3d0902d`);
- Task 2 реализован и принят (`a1b1bbf..ac5defe`): generation/`--check` PASS, оба safety finding
  закрыты scoped re-review;
- Task 3 реализован и принят (`f54c0fe..5acc6db`): tilt/loader/ownership готовы, 3 review finding
  закрыты;
- Task 4 Home реализован и принят (`ad7f60d..d5228d9`);
- Task 5 phone seam реализован и принят (`ba87356`): новый Home подключён, старое phone-дерево удалено;
- Task 6 shared phone-only shell реализован (`1ec8bb9`);
- Task 7 завершён (`77dec2b`, `7b09aa2`, `67e567a`): purchase, QR/share и split перенесены,
  а Claim/Trial получают тот же shell через обратно совместимый `MobilePremiumScreen`;
- cleanup завершён (`c15b4e3`), combined review закрыт (`e197617`).

### Локальные ограничения проверки

- `adb devices -l` не показывает подключённого устройства.
- Владелец отдельно запретил нагружать слабый компьютер локальной APK-сборкой; Android build
  переносится в GitHub Actions `android-test.yml`.
- Локально отсутствует `app/libs/libbox.aar`; CI скачивает normal libbox из успешного
  `libbox.yml`.
- `.github/workflows/android-test.yml` запускает `assembleOtherDebug` и
  `testOtherDebugUnitTest`, но существующие instrumentation tests не компилирует и не запускает.
- Для полной mobile-проверки нужно минимум добавить/запустить
  `assembleOtherDebugAndroidTest`; реальный `connectedOtherDebugAndroidTest` требует устройство.

### Точная следующая точка продолжения

1. Использовать исправленный план
   `docs/superpowers/plans/2026-07-31-mobile-premium-4d-interface.md`.
2. `subagent-driven-development/SKILL.md` уже прочитан; plan-scoped SDD ledger создан в
   `.superpowers/sdd/2026-07-31-mobile-premium-4d-interface/progress.md` и игнорируется Git.
3. Проверить чистый worktree, отсутствие runtime-потребителей старого flatten и пустой TV/tvm diff.
4. Локально запускать только лёгкие Python/статические проверки. Android compile/test APK выполнить
   через GitHub Actions `android-test.yml`, скачать/передать владельцу artifact для просмотра.
5. Push ветку, открыть draft PR, дождаться test APK, затем провести visual QA по шести экранам и
   dialog. Не merge/release/OTA.

Обновлено: **31.07.2026**. Этот документ — первая точка входа для нового окна
Codex/Claude. Сначала проверить volatile-факты командами Git и на GitHub, затем
продолжать с раздела «Следующий безопасный шаг».

## 1. Подтверждённое состояние

- Репозиторий: `evgenmay1978-del/proectmaestro-vpn`.
- Базовая ветка: `main`; на момент проверки её HEAD — `5e16c00`.
- Рабочая ветка мобильных референсов:
  `codex/mobile-4d-reference-pack`.
- Базовый коммит готового комплекта ассетов: `21ad085`
  (`design: add mobile 4D redraw assets`). Текущий HEAD может быть новее из-за
  обновления документации — всегда проверять `git rev-parse HEAD`.
- Открыт draft PR
  [№73](https://github.com/evgenmay1978-del/proectmaestro-vpn/pull/73):
  `design: mobile 4D references and 15-layer redraw pack`.
- Ветка и PR содержат документацию, визуальные референсы и исходный арт.
  **Android-код, backend, TV, release, OTA и workflow в этой ветке не менялись.**
- `AGENTS.md` и этот handoff существуют в PR-ветке; до merge PR №73 новый
  checkout от `main` их не увидит. Для продолжения выбрать указанную ветку.
- Новый арт ещё не подключён в приложение. Старый
  `app/src/main/res/drawable-nodpi/mobile_home_scene.webp` пока остаётся рабочим
  runtime-ресурсом и удаляется только вместе с проверенной миграцией к слоям.

Локальная особенность текущего Windows-worktree: `ops/phone-screen-sim.py` может
показываться как `M` из-за CRLF-метаданных, хотя при последней проверке
worktree/index имели одинаковый blob `8b15218daf7f32c7f95d780e65b730cd81cfa963`.
Не добавлять и не «восстанавливать» файл автоматически; сначала повторно
сравнить `git hash-object -- ops/phone-screen-sim.py` с
`git rev-parse HEAD:ops/phone-screen-sim.py`.

## 2. Решение владельца

Мобильный интерфейс MaestroVPN нужно пересобрать начисто как многослойную
«4D»-сцену: глубина, параллакс, переосвещение по наклону телефона, программные
межслойные тени и живой глаз.

Нельзя класть новый дизайн поверх старого Compose-дерева или оставлять старые
полноэкранные мобильные слои скрытыми под новым. После переноса нужно удалить
заменённый mobile-only код и ассеты, но только после проверки всех call sites.

Телевизионная версия в этой работе **строго вне области изменений**.

## 3. Что уже подготовлено

### Визуальные референсы экранов

Каталог [`design/mobile-4d-references/`](design/mobile-4d-references/):

- `00-current-mobile-ui.png` — структура текущего телефона;
- `01-core-flow-4d.png` — главный экран и основные состояния;
- `02-subscription-activation-4d.png` — подписка и активация;
- `03-settings-advanced-4d.png` — настройки и дополнительные экраны;
- `CLAUDE_INSTRUCTIONS.md` — контракт чистой реализации.

Критичное состояние: **`ОТКЛЮЧЕНО` — глаз полностью закрыт**, радужка и зрачок
не видны. `ПОДКЛЮЧЕНИЕ` — глаз открывается. `ПОДКЛЮЧЕНО` — полностью открыт.

### Готовый исходный арт главного экрана

Полное ТЗ: [`design/mobile-asset-redraw/SPEC.md`](design/mobile-asset-redraw/SPEC.md).

Готовые файлы:
[`design/mobile-asset-redraw/source/`](design/mobile-asset-redraw/source/).

Комплект состоит ровно из 15 PNG:

| Слой | Свет слева | Центр | Свет справа |
|---|---|---|---|
| дерево | `home_wood_l.png` | `home_wood_c.png` | `home_wood_r.png` |
| рамка | `home_frame_l.png` | `home_frame_c.png` | `home_frame_r.png` |
| картуш | `home_cartouche_l.png` | `home_cartouche_c.png` | `home_cartouche_r.png` |
| кольцо | `home_ring_l.png` | `home_ring_c.png` | `home_ring_r.png` |
| лозы | `home_vines_l.png` | `home_vines_c.png` | `home_vines_r.png` |

Формат всех файлов: **2160×4670**, PNG, 8 бит, sRGB без ICC. Дерево — RGB,
остальные 12 файлов — RGBA. Варианты `_l/_c/_r` одного слоя имеют идентичную
альфа-геометрию; меняется только освещение.

Контрольная сборка:
[`design/mobile-asset-redraw/PREVIEW_c.png`](design/mobile-asset-redraw/PREVIEW_c.png).

## 4. Контракт сборки

Порядок наложения без ручных смещений:

```text
home_wood
home_frame
home_cartouche
home_vines
home_ring
existing mobile_eye_* layers
Playfair title
interactive mobile UI
```

Код должен:

- плавно смешивать `_l/_c/_r` по наклону телефона;
- разводить слои на разную глубину параллаксом;
- рисовать мягкие тени между слоями;
- рисовать `MaestroVPN` шрифтом
  `app/src/main/res/font/playfair_display.ttf`;
- сохранить существующие `mobile_eye_open`, `mobile_eye_squint`,
  `mobile_eye_closed`, `mobile_eye_sclera`, `mobile_eye_iris` и
  `mobile_eye_catchlight`.

В арт не запечены и не должны запекаться текст, глаз и тени одного слоя на
другом. У `home_ring_*` центр намеренно прозрачный.

## 5. Жёсткие запреты

- Не использовать `mobile_home_scene.webp` как скрытый фон под новой сценой.
- Не внедрять новый экран дополнительной полноэкранной обёрткой поверх старого.
- Не прятать старые слои через `alpha = 0`, `visible = false`, clipping или
  перекрывающую картинку.
- Не удалять общий ресурс до проверки всех ссылок и TV-потребителей.
- Не менять `tvm_*`, `TvEskizHome.kt`, `TvEskizSpec.kt`, D-pad/focus/Back,
  TV-геометрию, TV-симуляторы и ветки `isTv`.
- Не менять backend, API или VPN-runtime ради визуальной задачи.
- Не делать merge в `main`, release или OTA без отдельного разрешения владельца.

## 6. Что проверено

Для комплекта из 15 PNG выполнена локальная строгая проверка:

- точные имена и холст 2160×4670;
- PNG 8-bit и декларация sRGB без ICC;
- RGB для дерева и RGBA для остальных слоёв;
- одинаковая alpha во всех `_l/_c/_r`;
- отсутствие геометрического сдвига между вариантами света;
- прозрачный центр кольца;
- контрольные композиты для левого, центрального и правого света.

Результат последнего запуска: **PASS, 15/15 файлов, 0 ошибок**.

Не выполнялись Android build, emulator/device-тест и внедрение в runtime,
поскольку этот PR пока содержит только референсы и исходники.

## 7. Что не завершено

- Владелец ещё должен визуально принять или скорректировать новый центральный
  композит.
- 15 исходных PNG не конвертированы в Android lossless WebP.
- В мобильном Compose-коде ещё нет многослойного композитора, смешивания света,
  параллакса и межслойных теней.
- Старый плоский `mobile_home_scene.webp` и заменяемые mobile-only слои ещё не
  удалены.
- Нет нового Android CI-build и ручной проверки APK на физическом телефоне.

## 8. Следующий безопасный шаг

После явного разрешения владельца на реализацию:

1. Проверить текущие branch/HEAD/PR и прочитать все документы из раздела 9.
2. Инвентаризировать mobile-only composable, ресурсы и call sites старой сцены.
3. Показать владельцу список экранов и точный план удаления старого mobile-only
   UI до правки кода.
4. В отдельном implementation PR подключить обработанные lossless WebP,
   mobile-only композитор и состояния глаза, не меняя TV-ветку.
5. После успешной миграции удалить старый плоский композит и мёртвые мобильные
   слои, подтвердив поиском отсутствие ссылок.
6. Запустить `assembleOtherDebug`, unit tests, доступные UI-тесты и сделать
   сравнение всех мобильных экранов на телефоне. Отдельно доказать, что TV diff
   отсутствует.

## 9. Обязательный порядок чтения

1. Этот `CONTEXT_HANDOFF.md`.
2. [`AGENTS.md`](AGENTS.md).
3. [`CLAUDE.md`](CLAUDE.md).
4. [`design/mobile-4d-references/README.md`](design/mobile-4d-references/README.md).
5. [`design/mobile-4d-references/CLAUDE_INSTRUCTIONS.md`](design/mobile-4d-references/CLAUDE_INSTRUCTIONS.md).
6. [`design/mobile-asset-redraw/SPEC.md`](design/mobile-asset-redraw/SPEC.md).
7. [`design/mobile-asset-redraw/README.md`](design/mobile-asset-redraw/README.md).
8. Только для подтверждённо нужной старой геометрии глаза и истории CI:
   [`MAESTROVPN_UI_HANDOFF.md`](MAESTROVPN_UI_HANDOFF.md).

`MAESTROVPN_UI_HANDOFF.md` — исторический документ предыдущей premium-итерации.
Его старое утверждение «fixed mobile_home_scene remains unchanged» **заменено**
решением владельца от 31.07.2026 о чистой многослойной пересборке. Исторические
числа геометрии глаза и факты CI можно использовать только после сверки с кодом.

## 10. Как поддерживать этот handoff

После каждого материального изменения обновлять:

- дату проверки;
- ветку, HEAD и PR;
- разделы «Что проверено», «Что не завершено» и «Следующий безопасный шаг»;
- ссылки на новые решения и артефакты.

Не записывать сюда токены, пароли, приватные URL подписок, данные клиентов или
непроверенные предположения.
