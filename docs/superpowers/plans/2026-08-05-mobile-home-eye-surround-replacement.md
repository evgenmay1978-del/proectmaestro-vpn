# Mobile Home Eye Surround Replacement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Полностью заменить радиальную мозаику в центральном медальоне на рельефное окружение крупного живого глаза, сохранив всю динамику глаза и единственного владельца каждого runtime-слоя.

**Architecture:** Новый ImageGen master хранится отдельно от runtime-арта. Узкий детерминированный Python-скрипт заменяет RGB внутри круга бывшей мозаики непосредственно в `home_ring_{l,c,r}`, сохраняет внешнюю бронзу и alpha, синхронизирует `kit/source`, после чего штатный генератор пересобирает атласы. `LivingEyeMedallion` остаётся единственным динамическим слоем; один `LivingEyeLayerFit` увеличивает всю анатомию на `1.10` и применяет нормализованный сдвиг.

**Tech Stack:** Python 3 + Pillow + `unittest`; Kotlin/JVM pure unit tests; Jetpack Compose Canvas; существующий `ops/mobile-4d-assets.py`; GitHub Actions `android-test.yml`.

## Global Constraints

- Работать только в `evgenmay1978-del/proectmaestro-vpn`, ветка `codex/mobile-4d-deck`.
- Единственный full-layout baseline: `design/mobile-4d-references/08-owner-installed-test-home-2026-08-08.jpg`, ровно `591×1280`, SHA-256 `9251457407f3aeee17b5281b32634e6c0d03e7fce3e9db12c16706444a9f800b`.
- `design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg` используется только для eye/eye-surround art direction; никогда не брать из него layout, status, contacts, protocol arc или lower-deck geometry.
- Старая мозаика физически удаляется из `home_ring_*`; запрещён новый runtime overlay поверх неё.
- Material circle: центр `(1080,1751)`, радиус `644 px` на master `2160×4670`; внешний guard начинается на радиусе `650 px`.
- Глаз: uniform anatomy scale `1.10`; offsets `3.5/238` ширины canvas и `7/238` высоты canvas.
- Сохраняются blink/gaze/touch/pupil/catchlight/state coroutines и правила закрытия `70/30`.
- Компьютер владельца слабый: локально разрешены только чтение, Git, правки документов, лёгкие Python preview/tests и просмотр скачанных artifacts. Не запускать локально Gradle/APK, full-size ring generation, atlas rebuild или полный simulator.
- Тяжёлую генерацию и Android-проверки выполнять manual jobs в GitHub Actions. Изменения CI допустимы только для изолированных test/artifact jobs; не менять release/OTA triggers.
- Не менять TV, D-pad/focus/Back, backend, API, VPN runtime, lower-deck scroll ownership, signing, release, OTA или `main`.
- Новый APK создаётся только после одобрения владельцем точной пары scripted-preview; OTA — только после физической проверки APK и отдельного явного разрешения.

## File Map

- `design/mobile-asset-redraw/materials/mobile_eye_surround_c.png` — выбранный master без глаза и внешней бронзы.
- `ops/mobile-eye-surround-assets.py` — единственная воспроизводимая операция замены пикселей и создания L/C/R.
- `ops/test_mobile_eye_surround_assets.py` — unit-контракты маски, relight, сохранения alpha/outer bytes и `--check`.
- `ops/mobile-4d-art-check.py` — приёмочный guard против возврата legacy mosaic.
- `design/mobile-asset-redraw/{kit,source}/home_ring_{l,c,r}.png` — синхронные результаты замены.
- `LivingEyeLayerGeometry.kt` / `LivingEyeLayerGeometryTest.kt` — единый увеличенный transform.
- `LivingEyeMedallion.kt` — только актуализация ownership-комментариев; анимационная логика не меняется.
- `ops/phone-screen-sim.py` — mirror production geometry и фазовая визуальная QA.
- `app/src/main/assets/mobile_4d/atlas_{l,c,r}_*.webp` — штатно регенерированные страницы ring-фрагментов.
- `.github/workflows/android-test.yml` — manual non-release jobs для full-size ring, atlas, simulator artifacts и Android test APK; release/OTA triggers не меняются.

---

### Task 1: RED-контракт, запрещающий прежнюю мозаику

**Files:**
- Modify: `ops/mobile-4d-art-check.py`
- Test: встроенный `selftest()` в `ops/mobile-4d-art-check.py`

**Interfaces:**
- Produces: `masked_circle_digest(image, center, radius) -> str`.
- Produces: `outside_circle_digest(image, center, guard_radius) -> str`.
- Produces: `check_eye_surround(name, images, report) -> None`.
- Legacy core SHA-256 at radius `620`: L `8b1e98e7658c0c190d5633d7662624d9fab377836fd59befca2b933640920375`, C `645d7df653316754d43f83b02ffa8e0630c473f7d50d5a4b84d217787e96c5c6`, R `409747235c039a8744b874499eb2a71d94c3ac33b622f7d9749bb8f12ffa3f49`.
- Immutable outside SHA-256 at guard radius `650`: L `9917a32afe8f1532c001e39c39c4fd37eabe13f68d589b0f8fc68e82453f4b03`, C `9a708bd0527be69304776c37b9a7e26a15c54dc8c2084b427b52fa44450ccf43`, R `f6beeabaff79fced29109a2e8f00272fdaae3aa27d946d75a600d0bca254d53e`.

- [ ] **Step 1: Заменить старые mosaic-поля спецификации ring**

Удалить `mosaic_rows`, `mosaic_luma_zones`, `mosaic_luma_radius`,
`mosaic_luma_ratio` и `check_mosaic_continuity`. Добавить константы:

```python
EYE_SURROUND_CENTER = (1080, 1751)
EYE_SURROUND_RADIUS = 644
EYE_SURROUND_CORE_RADIUS = 620
EYE_SURROUND_OUTSIDE_GUARD_RADIUS = 650
LEGACY_MOSAIC_CORE_SHA256 = {
    "l": "8b1e98e7658c0c190d5633d7662624d9fab377836fd59befca2b933640920375",
    "c": "645d7df653316754d43f83b02ffa8e0630c473f7d50d5a4b84d217787e96c5c6",
    "r": "409747235c039a8744b874499eb2a71d94c3ac33b622f7d9749bb8f12ffa3f49",
}
EXPECTED_RING_OUTSIDE_SHA256 = {
    "l": "9917a32afe8f1532c001e39c39c4fd37eabe13f68d589b0f8fc68e82453f4b03",
    "c": "9a708bd0527be69304776c37b9a7e26a15c54dc8c2084b427b52fa44450ccf43",
    "r": "f6beeabaff79fced29109a2e8f00272fdaae3aa27d946d75a600d0bca254d53e",
}
```

- [ ] **Step 2: Добавить новую проверку ring и ломающий selftest**

`check_eye_surround` обязан для каждого света проверить:

```python
report.check(
    masked_circle_digest(image, EYE_SURROUND_CENTER, EYE_SURROUND_CORE_RADIUS)
    != LEGACY_MOSAIC_CORE_SHA256[light],
    f"ring _{light}: прежняя радиальная мозаика удалена",
)
report.check(
    outside_circle_digest(image, EYE_SURROUND_CENTER, EYE_SURROUND_OUTSIDE_GUARD_RADIUS)
    == EXPECTED_RING_OUTSIDE_SHA256[light],
    f"ring _{light}: внешняя бронза вне material-mask неизменна",
)
```

В `selftest()` передать текущую legacy ring-картинку и потребовать, чтобы guard
поймал строку `прежняя радиальная мозаика`.

- [ ] **Step 3: Запустить RED на текущем GitHub-арте**

Run:

```powershell
python ops/mobile-4d-art-check.py --selftest
python ops/mobile-4d-art-check.py --group ring
```

Expected: `--selftest` PASS; `--group ring` FAIL только на проверках присутствия
legacy mosaic. Outside SHA, alpha bbox, формат и L/C/R должны оставаться PASS.

- [ ] **Step 4: Зафиксировать RED-коммит**

```powershell
git add -- ops/mobile-4d-art-check.py
git commit -m "test: reject legacy home eye mosaic"
```

---

### Task 2: Воспроизводимо заменить пиксели ring на новый материал

**Files:**
- Create: `design/mobile-asset-redraw/materials/mobile_eye_surround_c.png`
- Create: `ops/mobile-eye-surround-assets.py`
- Create: `ops/test_mobile_eye_surround_assets.py`
- Modify: `design/mobile-asset-redraw/kit/home_ring_l.png`
- Modify: `design/mobile-asset-redraw/kit/home_ring_c.png`
- Modify: `design/mobile-asset-redraw/kit/home_ring_r.png`
- Modify: `design/mobile-asset-redraw/source/home_ring_l.png`
- Modify: `design/mobile-asset-redraw/source/home_ring_c.png`
- Modify: `design/mobile-asset-redraw/source/home_ring_r.png`

**Interfaces:**
- Consumes: archived owner-selected closed-eye reference `design/mobile-4d-references/09-owner-selected-closed-eye-v2-2026-08-08.png`, RGB `852×1846`, archive SHA-256 `97cf7cddbeb780fd23ad8035c394183ce680b149895f0b26cbbb3a004122ac81`; produces RGB `1254×1254` master SHA-256 `0f5d565c2a269579166723b7b59532cde7032cb0b7ea668847b95c5531f278ca`.
- Produces: `replacement_mask(radius: int = 644, feather: int = 6) -> Image.Image`.
- Produces: `directional_material(master: Image.Image, light: str, size: tuple[int, int]) -> Image.Image`.
- Produces: `replace_ring_material(base: Image.Image, material: Image.Image, light: str) -> Image.Image`.
- CLI: `python ops/mobile-eye-surround-assets.py` writes; `--check` is read-only and byte-compares decoded RGBA output.

- [ ] **Step 1: Добавить выбранный ImageGen master**

```powershell
New-Item -ItemType Directory -Force design/mobile-asset-redraw/materials
Copy-Item -LiteralPath '..\generated\eye-surround-c-v1.png' -Destination 'design\mobile-asset-redraw\materials\mobile_eye_surround_c.png'
```

Проверить размер, mode и SHA ровно значениями из `Interfaces`.

- [ ] **Step 2: Написать unit-тесты до генератора**

В тесте использовать синтетическую RGBA-картинку `96×144`, уменьшенные center/radius
и RGB master. Обязательные assertions:

```python
self.assertEqual(result.getchannel("A").tobytes(), base.getchannel("A").tobytes())
self.assertEqual(outside_digest(result), outside_digest(base))
self.assertNotEqual(core_digest(result), core_digest(base))
self.assertEqual(alpha_support(left), alpha_support(center))
self.assertEqual(alpha_support(center), alpha_support(right))
self.assertNotEqual(left.convert("RGB").tobytes(), right.convert("RGB").tobytes())
```

Добавить integration-test: tracked `kit/source` пары должны совпадать после
`render_expected_outputs()`.

- [ ] **Step 3: Запустить тест и подтвердить RED**

```powershell
python -m unittest ops.test_mobile_eye_surround_assets
```

Expected: FAIL с `ModuleNotFoundError` для `mobile_eye_surround_assets`.

- [ ] **Step 4: Реализовать минимальный генератор**

Скрипт должен:

```python
MASTER_SIZE = (2160, 4670)
MATERIAL_CENTER = (1080, 1751)
MATERIAL_RADIUS = 644
MATERIAL_FEATHER = 6
LIGHTS = ("l", "c", "r")
```

1. Создать локальную circular mask `1289×1289`; blur разрешён только внутри
   переходного band, который заканчивается до guard radius `650`.
2. Масштабировать один master в ту же квадратную область.
3. Для `l/r` построить зеркальные горизонтальные поля между версиями brightness
   `0.88` и `1.10`; `c` оставить нейтральным. Геометрия master не меняется.
4. Заменить RGB через `Image.composite`, вернуть исходный alpha без изменений.
5. Перед записью проверить outside SHA из Task 1.
6. Одни и те же RGBA-байты сохранить в `kit` и `source` без ICC.
7. `--check` заново отрендерит ожидаемые RGBA-байты в памяти и завершится `1`,
   если любой tracked output отличается.

- [ ] **Step 5: Получить GREEN и шесть ring-файлов на GitHub runner**

Локально full-size PNG не генерировать. После лёгких synthetic tests вручную
запустить уже добавленный job:

```powershell
gh workflow run android-test.yml --ref codex/mobile-4d-deck -f task=mobile-eye-ring-assets
```

На runner выполняются `mobile-eye-surround-assets.py`, `--check`, полный
`unittest`, art selftest и ring guard. Скачать artifact
`mobile-eye-ring-assets-<sha>` с шестью PNG и digest-report.

Expected: все команды PASS; внутри radius `620` ни один digest не равен legacy;
outside radius `650` совпадает с исходными SHA; `kit/source` идентичны.
Job `227d8cc` пока существует, но его успешный запуск и интеграция artifact
должны быть подтверждены отдельно.

- [ ] **Step 6: Зафиксировать арт и генератор**

```powershell
git add -- design/mobile-asset-redraw/materials/mobile_eye_surround_c.png design/mobile-asset-redraw/kit/home_ring_l.png design/mobile-asset-redraw/kit/home_ring_c.png design/mobile-asset-redraw/kit/home_ring_r.png design/mobile-asset-redraw/source/home_ring_l.png design/mobile-asset-redraw/source/home_ring_c.png design/mobile-asset-redraw/source/home_ring_r.png ops/mobile-eye-surround-assets.py ops/test_mobile_eye_surround_assets.py
git commit -m "design: replace home mosaic with eye surround"
```

---

### Task 3: RED→GREEN для единого увеличенного transform глаза

**Files:**
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometryTest.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometry.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeMedallion.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModel.kt`

**Interfaces:**
- `fitLivingEyeLayer(width, height)` сохраняет тип результата.
- New constants: `LIVING_EYE_ANATOMY_SCALE = 1.10f`, `LIVING_EYE_OFFSET_X_FRACTION = 3.5f / 238f`, `LIVING_EYE_OFFSET_Y_FRACTION = 7f / 238f`.
- For `520×520`: scale `0.6954407`; state bounds `(-41.8241,54.4917)–(577.1182,496.0965)`; open aperture `(68.0556,201.9251)–(463.7613,352.8358)`; aperture ratios `0.760973×0.290213`.

- [ ] **Step 1: Переписать geometry-тест под новый утверждённый размер**

Удалить тест про `base mosaic` и прямой scale `520/822.5`. Добавить:

```kotlin
@Test
fun anatomyUsesOneOwnerApprovedScaleAndOffset() {
    val fit = fitLivingEyeLayer(520f, 520f)
    val aperture = livingEyeApertureContour(fit, closure = 0f, seamOverlapPx = 0f).bounds

    assertEquals(0.6954407f, fit.scale, 0.000001f)
    assertEquals(-41.8241f, fit.stateBounds.left, 0.001f)
    assertEquals(54.4917f, fit.stateBounds.top, 0.001f)
    assertEquals(577.1182f, fit.stateBounds.right, 0.001f)
    assertEquals(496.0965f, fit.stateBounds.bottom, 0.001f)
    assertEquals(0.760973f, (aperture.right - aperture.left) / 520f, 0.00001f)
    assertEquals(0.290213f, (aperture.bottom - aperture.top) / 520f, 0.00001f)
}
```

Сохранить тесты общего mapping, 70/30, zero-height closure и render policy.

- [ ] **Step 2: Создать test-only commit, push и доказать RED в GitHub**

```powershell
git add -- app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometryTest.kt
git commit -m "test: require larger living eye geometry"
git push origin codex/mobile-4d-deck
gh workflow run android-test.yml --ref codex/mobile-4d-deck -f task=android
```

Получить run id через `gh run list --workflow android-test.yml --branch codex/mobile-4d-deck --limit 1`
и дождаться завершения. Expected: `testOtherDebugUnitTest` FAIL именно на новом scale/bounds.

- [ ] **Step 3: Реализовать один uniform transform**

В `fitLivingEyeLayer`:

```kotlin
val layerScale = LIVING_EYE_ANATOMY_SCALE
val anatomyOffsetX = medallionSize * LIVING_EYE_OFFSET_X_FRACTION
val anatomyOffsetY = medallionSize * LIVING_EYE_OFFSET_Y_FRACTION
val layerTranslationX =
    width / 2f - (rawStateLeft + rawStateRight) / 2f * layerScale + anatomyOffsetX
val layerTranslationY =
    height / 2f - (rawStateTop + rawStateBottom) / 2f * layerScale + anatomyOffsetY
```

Не добавлять отдельный scale/offset в `LivingEyeMedallion`: он уже использует
`fit` для anatomy, aperture, iris, pupil, catchlight, gaze и seam.

- [ ] **Step 4: Актуализировать ownership-комментарии**

Убрать формулировки `mosaic` и `only runtime overlay allowed over the mosaic`.
Новая формулировка: full closure exposes baked eye-surround material and the thin
contact seam. В `Mobile4DSceneModel.kt` заменить комментарий «кольцо с мозаикой»
на «кольцо с eye-surround»; числовую геометрию медальона не менять.

- [ ] **Step 5: Зафиксировать GREEN implementation commit**

```powershell
git add -- app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometry.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeMedallion.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModel.kt
git commit -m "feat: enlarge living eye with one transform"
```

---

### Task 4: Пересобрать runtime-атласы и честную фазовую QA

**Files:**
- Modify: `ops/phone-screen-sim.py`
- Modify if needed: `.github/workflows/android-test.yml` — расширить только manual test/artifact job, потому что существующий `task=mobile-eye-ring-assets` покрывает Task 2, но ещё не atlas/full simulator.
- Regenerate: `app/src/main/assets/mobile_4d/atlas_l_*.webp`
- Regenerate: `app/src/main/assets/mobile_4d/atlas_c_*.webp`
- Regenerate: `app/src/main/assets/mobile_4d/atlas_r_*.webp`
- Verify/possibly unchanged: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DGeneratedAssets.kt`

**Interfaces:**
- Consumes: six synchronized `source/home_ring_*` outputs and geometry constants from Task 3.
- Produces: deterministic atlas pages plus `build/phone-screen-sim/owner-eye-blink-phases.png` and `owner-home-comparison.png`.

- [ ] **Step 1: Обновить simulator mirror до production transform**

Добавить:

```python
LIVING_EYE_ANATOMY_SCALE = 1.10
LIVING_EYE_OFFSET_X_FRACTION = 3.5 / 238.0
LIVING_EYE_OFFSET_Y_FRACTION = 7.0 / 238.0
```

Умножить `state_w/state_h` на `LIVING_EYE_ANATOMY_SCALE`; в
`_living_eye_components` добавить offsets к `state_left/state_top`. Не менять
`canvas_px`: material/socket остаётся `≈238 dp`.

- [ ] **Step 2: Переписать фазовые подписи и assertions**

Текст листа должен говорить `новый eye-surround + один dynamic open-eye`. Assertion
вне aperture/seam сохраняется, но сообщает `base eye-surround changed`. При
`phase == 1.0` по-прежнему обязательны zero open-eye alpha и только контактный
seam поверх baked material. Удалить все утверждения, что base является мозаикой.

- [ ] **Step 3: Пересобрать штатные атласы только на GitHub runner**

Не запускать эти команды на слабом Windows-компьютере владельца. Выполнить их в
manual test/artifact job GitHub Actions:

```powershell
python ops/mobile-4d-assets.py
python ops/mobile-4d-assets.py --check
```

Expected: artifact содержит только изменённые страницы с ring-fragments;
manifest остаётся byte-stable, если alpha и placement не изменились.

- [ ] **Step 4: Разделить лёгкие локальные и тяжёлые GitHub-проверки**

Локально допустимы только:

```powershell
python -m py_compile ops/mobile-eye-surround-assets.py ops/mobile-4d-art-check.py ops/mobile-4d-assets.py ops/phone-screen-sim.py
python -m unittest ops.test_mobile_eye_surround_assets ops.test_mobile_eye_natural_assets
git diff --check
```

На GitHub runner выполнить full-size ring/atlas checks и обе симуляции:

```powershell
python ops/mobile-4d-art-check.py --selftest
python ops/mobile-4d-art-check.py --group ring
python ops/phone-screen-sim.py --eye-phases-only
python ops/phone-screen-sim.py
```

Загрузить `owner-eye-blink-phases.png`, `owner-home-comparison.png` и QA-JPEG
как workflow artifacts. Expected: все команды PASS; глаз крупнее прежнего,
радиальные спицы отсутствуют, на пяти фазах старый узор не проявляется.

- [ ] **Step 5: Проверить два разных визуальных контракта**

Открыть скачанные `owner-home-comparison-qa.jpg` и
`owner-eye-blink-phases-qa.jpg`.

- Полный layout, title, status, phone, contacts, protocol arc и lower deck
  сравнивать только с
  `design/mobile-4d-references/08-owner-installed-test-home-2026-08-08.jpg`.
- `09-owner-selected-closed-eye-v2-2026-08-08.png` is the current closed-lid/eye-surround reference; `04-owner-selected-home-2026-07-31.jpg` is historical only.
- Проверить края material circle, отсутствие crop глаза, совпадение центра,
  бронзовую кромку и отсутствие старой сетки. При расхождении больше `±4 dp`
  корректировать только общие scale/offset constants и повторять runner Step 4.

- [ ] **Step 6: Зафиксировать simulator и regenerated atlases**

```powershell
git add -- ops/phone-screen-sim.py app/src/main/assets/mobile_4d app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DGeneratedAssets.kt
git commit -m "build: regenerate home eye surround atlases"
```

---

### Task 5: Обновить долговременные контракты и завершить GitHub-проверку

**Files:**
- Modify: `design/mobile-asset-redraw/README.md`
- Modify: `design/mobile-asset-redraw/SPEC.md`
- Modify: `design/mobile-asset-redraw/KIT.md`
- Modify: `design-qa.md`
- Modify: `CLAUDE_MOBILE_REBUILD.md`
- Modify: `CONTEXT_HANDOFF.md`
- Modify: `docs/superpowers/specs/2026-08-02-mobile-home-scroll-logo-eye-design.md`
- Add: `docs/superpowers/specs/2026-08-05-mobile-home-eye-surround-replacement-design.md`
- Add: `docs/superpowers/plans/2026-08-05-mobile-home-eye-surround-replacement.md`

**Interfaces:**
- Produces: один непротиворечивый handoff: `ring = bronze + baked eye-surround`, dynamic layer = anatomy only.

- [ ] **Step 1: Удалить устаревшие обещания сохранить мозаику**

Во всех перечисленных документах закрепить:

```text
The radial mosaic is superseded and must not exist in runtime ring pixels.
home_ring owns the static bronze + eye-surround material.
LivingEyeMedallion owns only dynamic anatomy and the contact seam.
```

Старую spec 02.08 не удалять; добавить сверху ссылку `Superseded by
2026-08-05-mobile-home-eye-surround-replacement-design.md`.

- [ ] **Step 2: Запустить полный audit с соблюдением weak-PC policy**

Локально выполнить только лёгкие `py_compile`, `unittest`, `git diff --check`
и `git status --short`. Full-size `mobile-eye-surround-assets.py --check`,
`mobile-4d-assets.py --check`, ring art checks и полный simulator выполнить на
GitHub runner и сохранить отчёты/artifacts.

Expected: PASS; status содержит только файлы этого плана, без TV/backend/API/
VPN runtime/release/OTA изменений.

- [ ] **Step 3: Зафиксировать docs/handoff commit**

```powershell
git add -- design/mobile-asset-redraw/README.md design/mobile-asset-redraw/SPEC.md design/mobile-asset-redraw/KIT.md design-qa.md CLAUDE_MOBILE_REBUILD.md CONTEXT_HANDOFF.md docs/superpowers/specs/2026-08-02-mobile-home-scroll-logo-eye-design.md docs/superpowers/specs/2026-08-05-mobile-home-eye-surround-replacement-design.md docs/superpowers/plans/2026-08-05-mobile-home-eye-surround-replacement.md
git commit -m "docs: record home eye surround ownership"
```

- [ ] **Step 4: Push финальный HEAD и запустить GREEN GitHub Actions**

```powershell
git push origin codex/mobile-4d-deck
gh workflow run android-test.yml --ref codex/mobile-4d-deck -f task=android
```

Дождаться run. Expected: `assembleOtherDebug` и `testOtherDebugUnitTest` PASS.

- [ ] **Step 5: Проверить GitHub source of truth и artifact**

```powershell
git fetch origin codex/mobile-4d-deck
git rev-parse HEAD
git rev-parse origin/codex/mobile-4d-deck
git status --short --branch
```

Expected: локальный и remote SHA совпадают, worktree чист. Скопировать URL run и
URL APK artifact из успешного workflow; не создавать release, OTA или merge.


## Option-2 closed-eye material correction (2026-08-08)

Task 2 consumes archived owner selection
`design/mobile-4d-references/09-owner-selected-closed-eye-v2-2026-08-08.png` (RGB 852×1846; archive SHA-256 `97cf7cddbeb780fd23ad8035c394183ce680b149895f0b26cbbb3a004122ac81`)
for the closed lids. `04` is historical only. Regenerate or verify tracked master
`mobile_eye_surround_c.png` (RGB 1254×1254; SHA-256 `0f5d565c2a269579166723b7b59532cde7032cb0b7ea668847b95c5531f278ca`) without rendering
`home_ring_*`:

```text
python ops/mobile-eye-surround-assets.py --material-only
python ops/mobile-eye-surround-assets.py --material-only --check
```
