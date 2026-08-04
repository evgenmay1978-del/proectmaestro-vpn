# Mobile Home Foreground Scroll Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Исправить только runtime z-order мобильной Home-деки, чтобы `console`, `contacts` и `arc` при прокрутке проходили поверх неподвижного бокового орнамента с уже существующими scroll delta и clip.

**Architecture:** Generated manifest остаётся техническим inventory/packing order. Compose и QA-симулятор получают отдельный явный runtime render order. Один `ScrollState`, геометрия, atlas, art, callbacks и интерактивность не меняются.

**Tech Stack:** Kotlin, Jetpack Compose Canvas, JUnit 4, Python/Pillow, GitHub Actions.

**Global constraints:** Вся работа выполняется только на GitHub в `codex/mobile-deck-layer-order`; Windows не используется. Разрешён только draft PR в `codex/mobile-4d-deck` и `.github/workflows/android-test.yml`. TV, backend, VPN-runtime, `main`, release и OTA вне scope. `.github/workflows/android.yml` нельзя менять или запускать. До фразы владельца «добро на обновление» существуют только тестовые APK.

## Исходная доказательная база

- Дефектная test APK: implementation `120fb816f4fd8be6c05f328d33d36089af9fbe54`.
- Actions run: `30764526376`.
- Artifact: `maestrovpn-tv-test-apk`, id `8838559790`, size `177365255`, digest `sha256:64aadd731303732a1def8c5fb01db95510197ef730eb2514db10cf377100ac25`.
- Сохранённый QA baseline: `docs/evidence/2026-08-04-owner-home-scroll-proof-qa.svg`.
- SHA-256 вложенного JPEG: `ab153d2a00c05a7326b42ed7856b42dd01dd6f382f79f0ff04716aaab4d85add`.
- Спецификация: `docs/superpowers/specs/2026-08-04-mobile-deck-foreground-scroll-design.md`.
- Рабочий контракт: `docs/agent-working-contract.md`.

## Task 1: Зафиксировать RED-контракт runtime order

**Files:**

- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DGeneratedAssetsTest.kt`
- Update checkpoint: `docs/handoffs/2026-08-04-mobile-deck-layer-order.md`

- [ ] **Step 1: Разделить ожидаемый packing order и runtime order в тесте**

В начале `manifestUsesTheMasterCanvasAndApprovedLayerOrder()` сохранить generated order без изменений:

```kotlin
val expectedManifestOrder = listOf(
    "wood", "console", "contacts", "frame", "cartouche", "vines", "arc", "ring",
)
val expectedRuntimeOrder = listOf(
    "wood", "frame", "cartouche", "vines", "console", "contacts", "arc", "ring",
)

assertEquals(expectedManifestOrder, Mobile4DGeneratedAssets.layerZOrder)
assertEquals(expectedRuntimeOrder, mobile4DHomeLayerOrder)
assertEquals(
    Mobile4DGeneratedAssets.layerZOrder.toSet(),
    mobile4DHomeLayerOrder.toSet(),
)
```

Удалить ошибочное требование полного равенства generated order и runtime order. Проверку `fragments.map { it.layer }.toSet()` сохранить.

- [ ] **Step 2: Добавить контракт состава движущейся деки**

Добавить отдельный тест:

```kotlin
@Test
fun homeDeckMovesOnlyConsoleContactsAndArc() {
    assertEquals(
        listOf("console", "contacts", "arc"),
        mobile4DHomeReliefLayers
            .filter(Mobile4DHomeReliefLayer::movesWithDeck)
            .map(Mobile4DHomeReliefLayer::name),
    )
}
```

Этот тест фиксирует состав scroll-слоёв; существующий тест `mobile4DDeckLayerTranslationYPx` не менять, если он уже подтверждает формулу `staticShiftPx - scrollPx`.

- [ ] **Step 3: Commit RED без production-правки**

Commit message:

```text
test: define foreground mobile deck order
```

В этом checkpoint production-файл `Mobile4DHome.kt` и simulator не меняются.

- [ ] **Step 4: Открыть draft PR и получить ожидаемый RED**

Создать draft PR:

- head: `codex/mobile-deck-layer-order`;
- base: `codex/mobile-4d-deck`;
- title: `fix: move mobile Home deck above side ornament`;
- body: ссылка на spec, baseline evidence и этот план; явные запреты release/OTA.

На PR должен запуститься только `.github/workflows/android-test.yml`.

Ожидаемое доказательство RED:

- `:app:assembleOtherDebug` компилируется;
- upload может создать APK до unit tests, но этот artifact помечается **RED / владельцу не передавать**;
- `:app:testOtherDebugUnitTest` падает только на новом ожидаемом runtime order:
  expected `wood, frame, cartouche, vines, console, contacts, arc, ring`,
  actual `wood, console, contacts, frame, cartouche, vines, arc, ring`;
- никаких иных новых падений нет.

Записать commit SHA, run ID, failed test, artifact id (если появился) и запрет его использования в task handoff. Не перезапускать ожидаемый RED.

## Task 2: Выполнить минимальную runtime-правку и получить GREEN

**Files:**

- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DHome.kt`
- Update checkpoint: `docs/handoffs/2026-08-04-mobile-deck-layer-order.md`

- [ ] **Step 1: Изменить только порядок `mobile4DHomeReliefLayers`**

Заменить текущий список на:

```kotlin
internal val mobile4DHomeReliefLayers = listOf(
    Mobile4DHomeReliefLayer("frame", Mobile4DParallaxLayer.Frame, movesWithDeck = false),
    Mobile4DHomeReliefLayer("cartouche", Mobile4DParallaxLayer.Cartouche, movesWithDeck = false),
    Mobile4DHomeReliefLayer("vines", Mobile4DParallaxLayer.Vines, movesWithDeck = false),
    Mobile4DHomeReliefLayer("console", Mobile4DParallaxLayer.Console, movesWithDeck = true),
    Mobile4DHomeReliefLayer("contacts", Mobile4DParallaxLayer.Console, movesWithDeck = true),
    Mobile4DHomeReliefLayer("arc", Mobile4DParallaxLayer.Arc, movesWithDeck = true),
)
```

Не менять:

- `mobile4DHomeLayerOrder`;
- `MOBILE_HOME_LOWER_DECK_SHIFT_DP = 25f`;
- `mobile4DDeckLayerTranslationYPx`;
- `deckTop` и `clipRect`;
- hero/ring/eye/logo;
- parallax mapping;
- Compose controls и callbacks.

- [ ] **Step 2: Проверить diff до commit**

Через GitHub compare/file diff подтвердить, что в production-коде изменены только шесть строк порядка списка. Generated sources, WebP/atlas и workflow-файлы должны иметь нулевой diff.

- [ ] **Step 3: Commit минимального исправления**

Commit message:

```text
fix: draw mobile Home deck above side ornament
```

- [ ] **Step 4: Дождаться GREEN run**

На новом PR run проверить:

- `:app:assembleOtherDebug` success;
- `:app:testOtherDebugUnitTest` success;
- новый runtime-order test success;
- scroll-layer membership test success;
- нет skipped у обязательных build/unit steps.

Если есть иной сбой, сначала записать его в failure log с run ID и root cause; не повторять run вслепую.

## Task 3: Синхронизировать существующий QA-симулятор

**Files:**

- Modify: `ops/phone-screen-sim.py:420-451`
- Update: `design-qa.md`
- Update checkpoint: `docs/handoffs/2026-08-04-mobile-deck-layer-order.md`

- [ ] **Step 1: Сохранить проверку generated manifest order**

Переименовать локальную переменную в `manifest_order` и оставить ожидаемый packing order:

```python
expected_manifest_order = [
    'wood', 'console', 'contacts', 'frame', 'cartouche', 'vines', 'arc', 'ring',
]
if manifest_order != expected_manifest_order:
    raise ValueError(f'Unexpected 4D manifest order: {manifest_order!r}')
```

- [ ] **Step 2: Ввести отдельный явный runtime order**

```python
runtime_order = [
    'wood', 'frame', 'cartouche', 'vines', 'console', 'contacts', 'arc', 'ring',
]
if len(runtime_order) != len(set(runtime_order)):
    raise ValueError(f'Duplicate runtime 4D layers: {runtime_order!r}')
if set(runtime_order) != set(manifest_order):
    raise ValueError(
        f'Runtime/manifest 4D layer inventory differs: '
        f'runtime={runtime_order!r}, manifest={manifest_order!r}'
    )
```

Цикл композиции должен стать `for layer in runtime_order:`. Множество `deck_layers = {'console', 'contacts', 'arc'}`, формула `25 dp - deck_scroll_dp`, hero translation и clip не меняются.

- [ ] **Step 3: Не создавать новый скрипт и не запускать simulator на Windows**

`ops/phone-screen-sim.py` остаётся единственным генератором. В этой сессии Windows-запуск запрещён. Поэтому в `design-qa.md` честно записать:

- старое SVG/JPEG — baseline до исправления;
- source parity simulator/runtime проверено review diff;
- новый render simulator не запускался;
- визуальный runtime gate — физический телефон с новой test APK.

Не заменять baseline новым изображением и не заявлять визуальный PASS без устройства.

- [ ] **Step 4: Commit simulator parity**

Commit message:

```text
ops: align mobile scroll preview layer order
```

GitHub Actions может не запускаться для ops/docs-only commit. Это ожидаемо; итоговый проверяемый SHA должен получить отдельный разрешённый `android-test.yml` run через draft PR/workflow dispatch, если автоматический PR run не стартовал.

## Task 4: Проверить итоговый SHA и тестовый APK

**Files:**

- Update: `docs/handoffs/2026-08-04-mobile-deck-layer-order.md`
- Update: `CONTEXT_HANDOFF.md`
- Optional index update: `evgenmay1978-del/maestro-memory`

- [ ] **Step 1: Получить GREEN именно для итогового SHA**

Итоговый run обязан быть привязан к HEAD, содержащему Kotlin fix, test contract и simulator parity. Использовать только `.github/workflows/android-test.yml`.

Проверить в логах точные команды workflow:

```text
./gradlew :app:assembleOtherDebug --stacktrace --no-daemon
./gradlew :app:testOtherDebugUnitTest --stacktrace --no-daemon
```

- [ ] **Step 2: Проверить фактический APK artifact**

Для `maestrovpn-tv-test-apk` записать:

- run ID и URL;
- artifact ID;
- size > 0;
- digest;
- expiration/availability;
- успешную выдачу download endpoint.

Workflow использует `VERSION_NAME=1.0.92-test`; не называть сборку «153».

- [ ] **Step 3: Выполнить scope audit**

Сравнить `codex/mobile-4d-deck...codex/mobile-deck-layer-order` и подтвердить:

- ожидаемые изменения только в test/runtime/simulator/task docs;
- нулевой diff в TV/`tvm_*`;
- нулевой diff в backend, API и VPN-runtime;
- нулевой diff в `.github/workflows/**`;
- нулевой diff в version/signing/release/OTA;
- `main` не менялся.

- [ ] **Step 4: Обновить durable handoff**

В верхнем LIVE-разделе `CONTEXT_HANDOFF.md` и task handoff записать итоговый HEAD, PR, GREEN run, artifact metadata, выполненные и невыполненные проверки, а также один следующий шаг: установка владельцем test APK и прокрутка Home.

Ни один старый RED artifact не должен оставаться помеченным как пригодный.

## Task 5: Визуальная приёмка владельцем без релиза

**Evidence:**

- Existing baseline: `docs/evidence/2026-08-04-owner-home-scroll-proof-qa.svg`
- New evidence: скриншот или видео владельца с установленной test APK

- [ ] **Step 1: Передать только итоговый GREEN test artifact**

В сообщении указать run, artifact id, digest и что это `1.0.92-test`, не OTA.

- [ ] **Step 2: Проверить один утверждённый сценарий**

На физическом телефоне:

1. открыть мобильный Home;
2. прокрутить вверх до пересечения с боковой резьбой;
3. проверить Telegram/МАКС/WhatsApp;
4. проверить весь ряд `Ввести логин / Тест сети / Подключить телефон`;
5. подтвердить, что оба ряда идут поверх бокового орнамента так же, как дуга;
6. подтвердить неподвижность логотипа, кольца и глаза;
7. проверить, что элементы исчезают только у прежнего верхнего clip `deckTop = 434 dp`.

- [ ] **Step 3: Зафиксировать решение владельца**

Если есть дефект, записать точное наблюдение и продолжить в той же test-ветке новым плановым циклом RED/GREEN.

Если владелец доволен, зафиксировать визуальную приёмку, но не выполнять merge/release/OTA. К выпуску можно переходить только после отдельной точной фразы **«добро на обновление»** и отдельного release-плана.
