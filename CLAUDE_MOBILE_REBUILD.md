# Claude: обязательный handoff по чистой мобильной 4D-пересборке

## LIVE 02.08.2026 — продолжать только отсюда

Рабочая GitHub-ветка: `codex/mobile-4d-deck`. Claude не видит Windows-worktree, поэтому перед
любой работой выполнить `git fetch --all --prune`, checkout этой ветки и подтвердить remote HEAD.
Локальный файл без push не является checkpoint.

Обязательные новые документы, полностью:

1. `CONTEXT_HANDOFF.md`, самый верхний раздел `0G`;
2. `docs/superpowers/specs/2026-08-02-mobile-home-scroll-logo-eye-design.md`;
3. `docs/superpowers/plans/2026-08-02-mobile-home-scroll-logo-eye.md`;
4. `design-qa.md`, раздел `0. LIVE`;
5. `ops/README.md`, пункт `phone-screen-sim.py`;
6. затем production-файлы `Mobile4DHome.kt`, `PhoneHomeControlDeck.kt`,
   `PhoneHomeReferenceLayout.kt`, `LivingEyeMedallion.kt`, `LivingEyeLayerGeometry.kt`;
7. `ops/mobile-eye-natural-assets.py` и `ops/test_mobile_eye_natural_assets.py` — immutable
   alpha-support contract живых век.

Уже реализовано checkpoint-коммитами `2181813`, `d7b6901`, `281173e`, `9ec2473`: один
`ScrollState`; fixed логотип/картуш/кольцо/мозаика/глаз; только `console/contacts/arc` движутся
вместе с нижней декой; support-фраза удалена; статус опущен ниже медальона; фирменные contact
icons выровнены; Playfair-титул исправлен; живой глаз посажен внутрь мозаики без изменения
регистрации или animation state machine.

Жёсткий runtime-контракт:

- верх Home неподвижен;
- один scroll-owner управляет relief, плитками, иконками, текстом и hit-target нижней деки;
- обе части клипуются ниже `deckTop = 434 dp`; новый UI не накладывается поверх старого;
- глаз сохраняет blink, squint, gaze, iris/pupil motion, touch reaction, catchlight и три VPN
  состояния; меняются только inner occlusion/contact shadows;
- никаких изменений TV, `tvm_*`, backend, release, OTA или signing.

Лёгкий simulator даёт `owner-home-comparison.png` и `owner-home-scroll-proof.png` (scroll 64 dp
достижим за счёт SecondaryDeck). Три независимых review закрыли Kotlin-scroll, simulator и
eye-integration; после исправлений Critical/High/Medium нет. Следующий gate — только GitHub
Actions: `assembleOtherDebug` + `testOtherDebugUnitTest`, затем реально существующий APK
artifact. Локальный Gradle/APK на слабом компьютере владельца запрещён.

Старые разделы ниже исторические там, где противоречат этому LIVE-блоку: особенно упоминания
пояснения после телефона, шести протоколов, отсутствующих arc/console layers и старого branch.


Эта инструкция обязательна для любого нового контекста Claude, который продолжает
мобильный интерфейс MaestroVPN. Она дополняет `CLAUDE.md` и
`CONTEXT_HANDOFF.md`; при расхождении сначала сверить фактический Git, затем
обновить документацию. Не заставлять владельца повторно объяснять задачу.

## Куда зайти

- Репозиторий: `evgenmay1978-del/proectmaestro-vpn`.
- Рабочая ветка: `codex/mobile-4d-interface`.
- Локальный worktree владельца:
  `C:\Users\User\Documents\Codex\2026-07-30\github-plugin-github-openai-curated-remote\work\proectmaestro-vpn-mobile-4d`.
- Draft PR: `#74`.
- Checkpoint на момент создания этого файла: `d60b6b0`.

Сначала выполнить только read-only проверку:

```text
git status --short --branch
git branch --show-current
git log -8 --oneline
git worktree list
```

Если указанного локального пути нет, найти worktree ветки через `git worktree
list`. Не создавать новую реализацию в `main` и не переносить изменения в другой
worktree без явной причины.

## Что прочитать до любого изменения

Читать полностью и именно в таком порядке:

1. `AGENTS.md`.
2. `CLAUDE.md`.
3. Этот файл — `CLAUDE_MOBILE_REBUILD.md`.
4. `CONTEXT_HANDOFF.md`.
5. `design/mobile-4d-references/CLAUDE_INSTRUCTIONS.md`.
6. `design/mobile-4d-references/README.md`.
7. `design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg` —
   единственный визуальный эталон Home.
8. `design/mobile-asset-redraw/SPEC.md` и
   `design/mobile-asset-redraw/README.md`.
9. `docs/mobile-screen-coverage.md` — точный scope: 6 экранов + 1 dialog.
10. `docs/superpowers/plans/2026-07-31-mobile-home-owner-reference-rebuild.md`.

После чтения сверить с кодом:

- `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DHome.kt`;
- `PhoneHomeReferenceLayout.kt` и его JVM-тест;
- `PhoneRevolverMenu.kt` — это старый mobile-only слой, подлежащий полной
  замене и удалению, а не визуальному маскированию;
- `LivingEyeMedallion.kt` и `LivingEyeLayerGeometry.kt`;
- `Mobile4DSceneModel.kt`, `Mobile4DBitmapLoader.kt`, `Mobile4DTilt.kt`;
- `MobilePremium4DShell.kt` и `MobilePremiumSurface.kt`;
- `TvHomeScreen.kt` и `SFANavigation.kt` читать только для проверки seam и
  callback-контракта; без необходимости не менять.

## Что уже сделано

- `5756def` — точный скриншот владельца сохранён в репозитории и объявлен
  единственным Home-эталоном; SHA-256 исходника и копии совпал.
- `ee3e350` — записан подробный план реализации.
- `9e8cc5e` — исправлена критическая ошибка измерения: внешний медальон нельзя
  сравнивать с внутренним hitbox глаза. Правильный scale около `1.0`, основной
  перенос ring+eye вверх около `-58` при 390×844; `1.42` запрещён.
- `aebc57e`, `d60b6b0` — создана чистая Android/Compose-free геометрия
  выбранного Home и тесты; responsive-высота берётся из масштабированной нижней
  границы консоли.
- Корневая причина отвергнутого APK доказана: новый 4D-композитор оставил старый
  `PhoneRevolverMenu` полноэкранным слоем. Его маска, старый барабан и тяжёлые
  карточки дали чёрную плиту и старый интерфейс поверх нового арта.

Локальный Gradle/APK на компьютере владельца не запускать: компьютер слабый.
До визуального одобрения использовать только статические проверки и лёгкий
Python/Pillow simulator. Сборка и тестовый APK — затем в GitHub Actions.

## Принцип работы

1. Источник истины — присланный владельцем экран, а не вкусовая импровизация.
2. Сначала измерение и тест/симулятор, затем production-код.
3. Один элемент — один владелец отрисовки. Нельзя держать старую полноэкранную
   сцену под новой, дублировать дерево/раму/кольцо или закрывать старое alpha,
   чёрной маской, clipping либо `visible = false`.
4. Home собирается из настоящих слоёв: wood → frame → cartouche → vines →
   ring → living eye → кодовый Playfair title → интерактивные controls.
5. Ring и eye получают один статический перенос и остаются зарегистрированы.
   Полноэкранные vines не масштабировать вместе с медальоном.
6. L/C/R освещение, memory-safe atlas, tilt/parallax и runtime shadows сохранить.
7. `ПОДКЛЮЧЕНО`: глаз открыт в исходном resting frame. `ПОДКЛЮЧЕНИЕ`:
   полуоткрыт. `ОТКЛЮЧЕНО`: полностью закрыт, радужка не видна и blink не
   запускается.
8. Владелец сначала смотрит превью 390×844. Только после его одобрения можно
   тратить GitHub CI на APK.

## Какой интерфейс должен получиться

Home в первом viewport 390×844 повторяет порядок эталона:

1. `MaestroVPN` и большой медальон с живым глазом;
2. статус и активный протокол;
3. телефон и пояснение поддержки;
4. `Telegram` → `МАКС` → `WhatsApp`;
5. шесть протоколов в порядке `Авто`, `VLESS`, `Hysteria2`, `AnyTLS`,
   `NaiveProxy`, `WEBRTC` (`WEBRTC` — только display label существующего
   runtime-тега `olcrtc`);
6. `Купить подписку`;
7. `Ввести логин`, `Тест сети`, `Подключить телефон`.

Запрещены: полноширинная чёрная плашка, старый цилиндрический revolver,
2×N-сетка протокольных карточек, сплошная зелёная заливка выбранной карточки,
уменьшение hit target ниже 48 dp. На коротком экране используется scroll.

Точный normal-flow scope приложения — не больше и не меньше:

- `tvhome`;
- `claim`;
- `trial`;
- `buy` со всеми состояниями оплаты;
- `scanqr` с permission/error states;
- `split`;
- `IosKaringDialog` поверх Home.

Скрытые `Settings`, `Log`, `Groups`, editor routes и незарегистрированные
Dashboard/Connections/Tools не превращать в «новые экраны» без отдельного
запроса владельца.

## Чистая замена и удаление старого mobile-only UI

После того как новый путь подключён и callbacks доказаны тестами:

1. удалить `PhoneRevolverMenu.kt`;
2. удалить или переписать связанные старые revolver tests;
3. удалить mask/snap/perspective/haptic-cylinder code и старые test tags;
4. удалить заменённые mobile-only fullscreen backgrounds/ресурсы только после
   `rg`-проверки отсутствия ссылок;
5. не оставлять «на всякий случай» скрытые старые composable или невидимые
   bitmap-слои;
6. выполнить нулевой TV-diff gate.

Удалять по доказанным ссылкам, а не по похожему имени. Никогда не удалять:

- `TvEskizHome.kt`, `TvEskizSpec.kt`, TV composable;
- `tvm_*`;
- TV D-pad/focus/Back и TV simulators;
- общую VPN/business/navigation логику;
- живые eye assets и подтверждённый L/C/R atlas, пока новый путь их использует.

Обязательная проверка остатков:

```text
rg -n "PhoneRevolverMenu|revolverVisualState|premium-revolver" app ops
git diff --exit-code 3c72aa1..HEAD -- app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/TvHomeScreen.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/TvEskizHome.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/TvEskizSpec.kt app/src/main/java/com/maestrovpn/tv/compose/navigation/SFANavigation.kt ":(glob)app/src/main/res/**/tvm_*" ":(glob)ops/tv-*"
```

## Все обновления должны устанавливаться последовательно

UI-задача не разрешает merge/release/OTA. Когда владелец отдельно разрешит
выкатить обновление, нельзя считать зелёную сборку доказательством обновляемости.

Для каждой поддерживаемой версии проверять in-place update, а не чистую установку:

1. сохранять `applicationId = com.maestrovpn.tv`;
2. использовать прежний стабильный signing key; throwaway/debug key не подходит
   для OTA и не обновит установленное приложение;
3. `versionCode` нового релиза строго выше установленного и монотонен;
4. установить поддерживаемую старую APK, создать реальные локальные данные,
   затем поставить новую APK через update/`adb install -r`, не очищая данные;
5. после обновления проверить cold start, login/account, срок подписки, выбранный
   протокол, профили, настройки auto-update, VPN connect/disconnect, фоновые
   workers, повторный update check и отсутствие crash loop;
6. проверить минимум: последнюю production-версию, предыдущую поддерживаемую и
   существующий waypoint старого флота. Не ломать действующую цепочку 105→107,
   пока она не заменена проверенным правилом;
7. проверить manifest/mirror/panel/waypoint через `ops/verify-ota.sh`, а также
   фактические artifact size и SHA-256; зелёный upload-step без artifact не PASS;
8. выкатывать поэтапно, смотреть install/crash telemetry и иметь стоп-критерий;
9. не делать несколько частых OTA подряд — собрать исправления в один
   проверенный релиз.

Совсем древние версии разрешено исключить, но только управляемо:

- заранее записать конкретный `minSupportedVersionCode`/минимальный waypoint;
- не выбирать порог молча во время релиза;
- сервер/манифест должен вернуть понятный обязательный upgrade path, а не
  несовместимую схему или бесконечную загрузку;
- все версии не старше порога обязаны обновляться последовательно без удаления
  приложения и потери данных.

## Definition of done

Работа не готова, пока одновременно не выполнено всё:

- comparison board содержит эталон и preview одного viewport/state рядом;
- Home visual QA прошёл и владелец его одобрил;
- показаны все 6 реальных экранов + 1 dialog, без выдуманных экранов;
- старый mobile-only UI удалён, поиск старых символов пуст;
- TV diff пуст;
- callbacks протоколов и действий сохранены;
- GitHub compile/unit gates зелёные, APK artifact реально существует и имеет
  зафиксированный SHA-256;
- перед release отдельно пройден upgrade matrix для всех поддерживаемых версий;
- `CONTEXT_HANDOFF.md` обновлён фактическими branch/HEAD/PR/run/artifact данными.

Не писать «готово» по одному скриншоту, одному compile job или одному чистому
install.
