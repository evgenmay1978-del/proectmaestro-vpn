# Mobile Home Device Registration Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the mobile Home match the owner screenshots on the real full-window viewport: logo/art/controls share one origin, console labels use one scale, and the same relit mosaic continues across the eyelids while they squint, close, and blink.

**Architecture:** The phone Home alone opts out of Material Scaffold content insets and owns the full edge-to-edge window. Portrait art uses a width-derived, top-anchored transform, while the existing crop transform remains the landscape fallback. The eye receives a painter for the already-loaded ring layer; `LivingEyeMedallion` clips that exact texture into the closing lid surfaces using the existing `lidPhase`, so no duplicate bitmap, scale, parallax, or animation clock is introduced.

**Tech Stack:** Kotlin, Jetpack Compose, Material 3, JUnit 4, Pillow simulator, GitHub Actions (`:app:testOtherDebugUnitTest`, `:app:assembleOtherDebug`).

## Global Constraints

- Scope is mobile Home only. TV Home, TV assets/tests/simulators, backend, API, VPN runtime, signing, release, OTA, and workflow files must have zero diff.
- Do not merge to `main`, publish a release, or publish OTA.
- Do not run local Gradle/Android builds on the weak workstation. Kotlin red/green gates run in GitHub Actions; Python simulator/tests may run locally.
- The owner screenshots are the approved visual target. Do not ask for another design confirmation.
- Reference landmarks are full-window 390×844 coordinates. Never hardcode 797, a status-bar height, a navigation-bar height, density, or a phone model.
- For portrait, use exactly `scale = width / 2160`, `translationX = 0`, `translationY = 0`. Preserve the existing `mobile4DSceneLayout()` crop behavior for landscape.
- System bars remain system overlays. Add the navigation-bar safe space only at the end of scroll content; it must not change `deckTop` or any primary landmark.
- Keep the existing title font family, three-layer relief, eye size, direct `890×635 / 822.5` registration, blink timings, gaze, pupil, catchlight, touch response, and VPN-state mapping.
- The eyelid mosaic must use the same loaded `ring` layer, light mix, scene transform, hero shift, parallax, and `lidPhase` as the surrounding mosaic. No new eye/ring flattened bitmap and no second animation.
- Follow TDD: regression tests are committed and observed failing in CI before production code is written.

---

### Task 1: Commit the real-device regression suite and prove RED in CI

**Files:**
- Create: `app/src/test/java/com/maestrovpn/tv/compose/MobileHomeWindowPolicyTest.kt`
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModelTest.kt`
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayoutTest.kt`
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometryTest.kt`

**Interfaces:**
- Consumes: current crop layout, reference bounds, eye fit, and the owner constants already in production.
- Produces the wished-for APIs that Task 2 must implement:
  - `mobileHomeUsesFullWindow(isTelevision: Boolean, isHomeRoute: Boolean): Boolean`
  - `mobile4DHomeSceneLayout(width: Float, height: Float): Mobile4DSceneLayout`
  - `Mobile4DSceneLayout.translatedBy(dx: Float, dy: Float): Mobile4DSceneLayout`
  - `PhoneHomeReferenceLayout.referenceScale: Float`
  - `phoneHomeReferenceBounds(referenceScale, left, top, right, bottom)`
  - `livingEyeMosaicProfile(width, height, lidPhase): LivingEyeMosaicProfile`
  - `livingEyeApertureContour(layerFit, closure, seamOverlapPx)`
  - `livingEyeEyelidEnvelopeBounds(contour, expansionPx, stateBounds)`

- [ ] **Step 1: Write the full-window policy test**

```kotlin
package com.maestrovpn.tv.compose

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class MobileHomeWindowPolicyTest {
    @Test
    fun onlyPhoneHomeOwnsTheFullEdgeToEdgeWindow() {
        assertTrue(mobileHomeUsesFullWindow(isTelevision = false, isHomeRoute = true))
        assertFalse(mobileHomeUsesFullWindow(isTelevision = true, isHomeRoute = true))
        assertFalse(mobileHomeUsesFullWindow(isTelevision = false, isHomeRoute = false))
    }
}
```

The production mutation this catches is reapplying Scaffold safe insets to phone Home or removing them from another route/TV.

- [ ] **Step 2: Add portrait-origin and ring-local-layout tests**

Append to `Mobile4DSceneModelTest.kt`:

```kotlin
@Test
fun portraitHomeUsesOneWidthAnchoredOriginAtEveryViewportHeight() {
    val fullWindow = mobile4DHomeSceneLayout(width = 390f, height = 844f)
    val formerInsetViewport = mobile4DHomeSceneLayout(width = 390f, height = 797f)
    val shortPhone = mobile4DHomeSceneLayout(width = 320f, height = 568f)

    assertEquals(390f / 2160f, fullWindow.scale, 0.000001f)
    assertEquals(0f, fullWindow.translationX, 0f)
    assertEquals(0f, fullWindow.translationY, 0f)
    assertEquals(fullWindow.scale, formerInsetViewport.scale, 0f)
    assertEquals(fullWindow.medallionCenterX, formerInsetViewport.medallionCenterX, 0f)
    assertEquals(fullWindow.medallionCenterY, formerInsetViewport.medallionCenterY, 0f)
    assertEquals(320f / 2160f, shortPhone.scale, 0.000001f)
    assertEquals(0f, shortPhone.translationY, 0f)
}

@Test
fun landscapeHomeKeepsTheExistingCropContract() {
    assertEquals(
        mobile4DSceneLayout(width = 844f, height = 390f),
        mobile4DHomeSceneLayout(width = 844f, height = 390f),
    )
}

@Test
fun ringLocalLayoutIsOnlyATranslationOfTheFullScene() {
    val full = mobile4DHomeSceneLayout(width = 390f, height = 844f)
    val local = full.translatedBy(dx = -76f, dy = -139f)

    assertEquals(full.scale, local.scale, 0f)
    assertEquals(full.translationX - 76f, local.translationX, 0f)
    assertEquals(full.translationY - 139f, local.translationY, 0f)
    assertEquals(full.medallionCenterX - 76f, local.medallionCenterX, 0f)
    assertEquals(full.medallionCenterY - 139f, local.medallionCenterY, 0f)
    assertEquals(full.medallionRadiusX, local.medallionRadiusX, 0f)
    assertEquals(full.medallionRadiusY, local.medallionRadiusY, 0f)
}
```

The mutations caught are restoring centered portrait crop or recalculating scale/parallax for the eyelid mosaic.

- [ ] **Step 3: Add reference-scale and console-zone tests**

Append to `PhoneHomeReferenceLayoutTest.kt`:

```kotlin
@Test
fun referenceScaleMapsConsoleZonesWithoutASecondInsetScale() {
    val full = phoneHomeReferenceLayout(390f, 844f)
    val narrow = phoneHomeReferenceLayout(320f, 568f)
    val left = phoneHomeReferenceBounds(
        referenceScale = full.referenceScale,
        left = 32f,
        top = 782.4f,
        right = 139f,
        bottom = 842.4f,
    )

    assertEquals(1f, full.referenceScale, 0f)
    assertEquals(320f / 390f, narrow.referenceScale, 0.000001f)
    assertEquals(32f, left.left, 0f)
    assertEquals(782.4f, left.top, 0f)
    assertEquals(139f, left.right, 0f)
    assertEquals(842.4f, left.bottom, 0f)
}
```

This fails if the old `(382 - 8) / 390 = 0.95897` scale returns.

- [ ] **Step 4: Add mosaic-lid phase and seam tests**

Append to `LivingEyeLayerGeometryTest.kt`:

```kotlin
@Test
fun mosaicAppearsOnEveryClosingAndClosedLidPhase() {
    val open = livingEyeMosaicProfile(520f, 520f, lidPhase = 0f)
    val squint = livingEyeMosaicProfile(520f, 520f, lidPhase = 0.5f)
    val closed = livingEyeMosaicProfile(520f, 520f, lidPhase = 1f)

    assertEquals(0f, open.textureAlpha, 0f)
    assertEquals(0.78f, squint.textureAlpha, 0.0001f)
    assertEquals(0.78f, closed.textureAlpha, 0.0001f)
    assertTrue(squint.seamOverlapPx >= 1f)
    assertTrue(squint.envelopeExpansionPx > squint.seamOverlapPx)
}

@Test
fun mosaicEnvelopeContainsTheAnimatedApertureWithoutChangingEyeFit() {
    val fit = fitLivingEyeLayer(520f, 520f)
    listOf(0f, 0.5f, 1f).forEach { phase ->
        val profile = livingEyeMosaicProfile(520f, 520f, phase)
        val contour = livingEyeApertureContour(
            layerFit = fit,
            closure = phase,
            seamOverlapPx = profile.seamOverlapPx,
        )
        val aperture = contour.bounds
        val state = fit.stateBounds
        val envelope = livingEyeEyelidEnvelopeBounds(
            contour = contour,
            expansionPx = profile.envelopeExpansionPx,
            stateBounds = state,
        )

        assertTrue(envelope.left <= aperture.left)
        assertTrue(envelope.top <= aperture.top)
        assertTrue(envelope.right >= aperture.right)
        assertTrue(envelope.bottom >= aperture.bottom)
        assertTrue(envelope.left >= state.left)
        assertTrue(envelope.top >= state.top)
        assertTrue(envelope.right <= state.right)
        assertTrue(envelope.bottom <= state.bottom)
    }
    assertEquals(520f / 822.5f, fit.scale, 0.000001f)
}
```

These tests protect the owner requirement that connecting, disconnected, and routine blink share one mosaic phase without resizing the eye.

- [ ] **Step 5: Verify the tests are cleanly written before pushing**

Run:

```powershell
git diff --check
git diff -- app/src/test
```

Expected: no whitespace errors; expectations are hand-derived literals and use production APIs, not source-text grep.

- [ ] **Step 6: Commit the RED tests**

```powershell
git add app/src/test/java/com/maestrovpn/tv/compose/MobileHomeWindowPolicyTest.kt app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModelTest.kt app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayoutTest.kt app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometryTest.kt
git commit -m "test: reproduce mobile home registration gaps"
git push
```

- [ ] **Step 7: Prove RED in GitHub Actions**

Inspect the new `android-test.yml` run. Expected: `:app:assembleOtherDebug` succeeds, then `:app:testOtherDebugUnitTest` fails because the wished-for policy/layout/mosaic APIs do not exist. Record the run URL and the exact missing symbol/assertion in the task report. Do not write production code until this RED evidence exists.

---

### Task 2: Implement one coordinate system, corrected console zones, and mosaic eyelids

**Files:**
- Create: `app/src/main/java/com/maestrovpn/tv/compose/MobileHomeWindowPolicy.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/MainActivity.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModel.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DHome.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayout.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeControlDeck.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometry.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeMedallion.kt`

**Interfaces:**
- Consumes: every wished-for signature from Task 1.
- Produces: full-window phone Home; portrait top-anchor; `referenceScale`; ring-local mosaic painter driven by the existing `lidPhase`.

- [ ] **Step 1: Add the narrow full-window route policy**

Create `MobileHomeWindowPolicy.kt`:

```kotlin
package com.maestrovpn.tv.compose

internal fun mobileHomeUsesFullWindow(
    isTelevision: Boolean,
    isHomeRoute: Boolean,
): Boolean = !isTelevision && isHomeRoute
```

In the phone Scaffold branch of `MainActivity.kt`, import foundation `WindowInsets` and Material `ScaffoldDefaults`, compute the predicate from `currentRootRoute == Screen.TvHome.route`, and set:

```kotlin
contentWindowInsets = if (mobileHomeUsesFullWindow(
        isTelevision = isTelevision(this@MainActivity),
        isHomeRoute = currentRootRoute == Screen.TvHome.route,
    )
) {
    WindowInsets(0, 0, 0, 0)
} else {
    ScaffoldDefaults.contentWindowInsets
},
```

Do not alter the rail Scaffold or any other route condition.

- [ ] **Step 2: Implement the portrait Home transform and translated ring layout**

In `Mobile4DSceneModel.kt`:

```kotlin
internal fun Mobile4DSceneLayout.translatedBy(dx: Float, dy: Float): Mobile4DSceneLayout = copy(
    translationX = translationX + dx,
    translationY = translationY + dy,
    medallionCenterX = medallionCenterX + dx,
    medallionCenterY = medallionCenterY + dy,
)

internal fun mobile4DHomeSceneLayout(width: Float, height: Float): Mobile4DSceneLayout {
    if (height < width) return mobile4DSceneLayout(width, height)
    val scale = width / MOBILE_4D_MASTER_WIDTH
    return Mobile4DSceneLayout(
        scale = scale,
        translationX = 0f,
        translationY = 0f,
        medallionCenterX = MOBILE_4D_MEDALLION_CENTER_X * scale,
        medallionCenterY = MOBILE_4D_MEDALLION_CENTER_Y * scale,
        medallionRadiusX = MOBILE_4D_MEDALLION_RADIUS_X * scale,
        medallionRadiusY = MOBILE_4D_MEDALLION_RADIUS_Y * scale,
    )
}
```

Use `mobile4DHomeSceneLayout()` only in `Mobile4DHome`. Leave generic scene callers unchanged.

- [ ] **Step 3: Give every reference control one scale**

Add `referenceScale` to `PhoneHomeReferenceLayout` and replace the local bounds builder with:

```kotlin
internal fun phoneHomeReferenceBounds(
    referenceScale: Float,
    left: Float,
    top: Float,
    right: Float,
    bottom: Float,
) = PhoneHomeReferenceBounds(
    left = left * referenceScale,
    top = top * referenceScale,
    right = right * referenceScale,
    bottom = bottom * referenceScale,
)
```

Set `referenceScale = width / ReferenceWidth` once in `phoneHomeReferenceLayout()`.

In `PhoneHomeControlDeck`, use `layout.referenceScale` for contacts and pass it to `BottomConsole`. Delete `CONSOLE_REFERENCE_WIDTH` and the old `(bounds.right - bounds.left) / 390` calculation. Map each console zone through `phoneHomeReferenceBounds()`, then subtract only `deckTop` from its mapped Y.

- [ ] **Step 4: Keep the final scroll actions above the system navigation overlay**

At the end of the existing deck `Column`, after `SecondaryDeck`, add:

```kotlin
Spacer(Modifier.windowInsetsBottomHeight(WindowInsets.navigationBars))
```

Import `WindowInsets`, `navigationBars`, and `windowInsetsBottomHeight`. Do not add top padding and do not place this spacer inside the fixed primary deck.

- [ ] **Step 5: Add pure lid-mosaic geometry**

In `LivingEyeLayerGeometry.kt`, move the aperture source-point data into pure internal geometry and implement:

```kotlin
internal data class LivingEyeLayerPoint(val x: Float, val y: Float)

internal data class LivingEyeApertureContour(
    val upper: List<LivingEyeLayerPoint>,
    val lower: List<LivingEyeLayerPoint>,
) {
    val bounds: LivingEyeLayerBounds
        get() = LivingEyeLayerBounds(
            left = minOf(upper.minOf { it.x }, lower.minOf { it.x }),
            top = minOf(upper.minOf { it.y }, lower.minOf { it.y }),
            right = maxOf(upper.maxOf { it.x }, lower.maxOf { it.x }),
            bottom = maxOf(upper.maxOf { it.y }, lower.maxOf { it.y }),
            scale = 1f,
        )
}

internal data class LivingEyeMosaicProfile(
    val textureAlpha: Float,
    val seamOverlapPx: Float,
    val envelopeExpansionPx: Float,
)

internal fun livingEyeMosaicProfile(
    width: Float,
    height: Float,
    lidPhase: Float,
): LivingEyeMosaicProfile {
    val size = minOf(width, height)
    return LivingEyeMosaicProfile(
        textureAlpha = (lidPhase.coerceIn(0f, 1f) / 0.5f)
            .coerceIn(0f, 1f) * 0.78f,
        seamOverlapPx = maxOf(1f, size * 0.005f),
        envelopeExpansionPx = size * 0.046f,
    )
}
```

`livingEyeEyelidEnvelopeBounds()` expands that mapped contour by `envelopeExpansionPx` and
clamps the result to the unchanged `layerFit.stateBounds`; the Compose path in the next step must
use those same bounds instead of a second oval or state-specific constants.
`livingEyeApertureContour()` maps the existing upper/lower source points through `LivingEyeLayerFit`, interpolates both toward their seam by `closure`, and contracts the remaining aperture by at most `seamOverlapPx` without crossing the seam.

- [ ] **Step 6: Paint the same relit ring texture onto the moving lids**

Add an optional `mosaicPainter: (DrawScope.() -> Unit)? = null` parameter to `LivingEyeMedallion`.

Inside its existing bronze clip, after the open/squint/closed frames and before `drawEyelidContactShadow`:

1. Read the same local `phase = lidPhase.value`.
2. Get `LivingEyeMosaicProfile` and the current contracted aperture contour.
3. Build an outer eyelid envelope from the uncontracted open contour expanded by `envelopeExpansionPx`; keep it inside `layerFit.stateBounds`.
4. Clip to the envelope, then use `ClipOp.Difference` to exclude the current aperture.
5. Save an offscreen layer with `Paint.alpha = profile.textureAlpha`, call `mosaicPainter`, and restore.
6. Draw the existing contact shadows and inner occlusion after the mosaic so lid relief remains visible.

Keep the open-eye anatomy clip unchanged; the new contracted aperture is only the mosaic mask.

- [ ] **Step 7: Supply a ring-local painter without another transform**

In `Mobile4DHome`, after the integer eye box is known:

```kotlin
val eyeLocalLayout = layout.translatedBy(
    dx = -eyeLeftPx.toFloat(),
    dy = -eyeTopPx.toFloat(),
)
```

Pass this painter to `LivingEyeMedallion`:

```kotlin
mosaicPainter = {
    drawAtlasLayer(
        layer = "ring",
        parallaxLayer = Mobile4DParallaxLayer.RingAndEye,
        layout = eyeLocalLayout,
        tilt = tilt,
        lightMix = lightMix,
        pages = pages,
        opaque = false,
        heroShift = heroShift,
    )
},
```

This must reuse the same `tilt`, `lightMix`, `pages`, `heroShift`, and ring parallax. Do not derive a second eye offset.

- [ ] **Step 8: Perform surgical static verification**

Run:

```powershell
git diff --check
rg -n "mobile4DHomeSceneLayout|mobileHomeUsesFullWindow|referenceScale|mosaicPainter|livingEyeMosaicProfile" app/src/main app/src/test
git diff -- app/src/main app/src/test
```

Expected: every new production API is consumed by the test that failed in Task 1; no changes under TV-specific files, backend, workflows, release, or OTA.

- [ ] **Step 9: Commit and push GREEN implementation**

```powershell
git add app/src/main/java/com/maestrovpn/tv/compose/MobileHomeWindowPolicy.kt app/src/main/java/com/maestrovpn/tv/compose/MainActivity.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModel.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DHome.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayout.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeControlDeck.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometry.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeMedallion.kt
git commit -m "fix: register mobile home to the full window"
git push
```

- [ ] **Step 10: Prove GREEN in GitHub Actions**

Inspect the new `android-test.yml` run. Required evidence:

- `:app:assembleOtherDebug` exit 0 and APK artifact uploaded;
- `:app:testOtherDebugUnitTest` exit 0 with the new tests included;
- no workflow/release/OTA changes.

Record run URL, commit SHA, APK artifact ID/name, and test count in the report. A green assemble alone is not sufficient.

---

### Task 3: Make the simulator expose the old inset bug and the mosaic blink contract

**Files:**
- Modify: `ops/phone-screen-sim.py`
- Modify: `ops/README.md`
- Modify: `design-qa.md`

**Interfaces:**
- Consumes: the exact constants and behavior implemented in Task 2.
- Produces: deterministic QA boards for full-window start/scroll and connected/connecting/disconnected eyelid mosaic states.

- [ ] **Step 1: Add a failing simulator assertion before changing rendering**

Add assertions near the simulator geometry definitions:

```python
assert home_scene_transform(390.0, 844.0).translation_y == 0.0
assert home_scene_transform(390.0, 797.0).translation_y == 0.0
assert lid_mosaic_alpha(0.0) == 0.0
assert lid_mosaic_alpha(0.5) == 0.78
assert lid_mosaic_alpha(1.0) == 0.78
```

Run `python ops/phone-screen-sim.py`. Expected: fail because these helpers do not exist. Keep this local RED output in the report; do not commit the transient failing state.

- [ ] **Step 2: Implement the simulator transform and eyelid texture**

Implement `home_scene_transform(width, height)` with the same portrait/landscape branch as Kotlin. Preserve the pre-eye ring scene, composite the live eye, then crop the exact pre-eye ring pixels into the eye box and apply them only to the expanded lid envelope outside the phase-dependent aperture at alpha `lid_mosaic_alpha(phase)`. Draw contact shadows afterward.

Do not synthesize mosaic colors and do not reuse the owner screenshot as an asset.

- [ ] **Step 3: Generate required boards**

Run:

```powershell
python ops/phone-screen-sim.py
```

Required output:

- existing 390×844 comparison board;
- existing scroll 0 / 64 dp proof;
- three-state eye board where squint and closed lids visibly carry the surrounding mosaic;
- one guide comparing the rejected inset-shrunk 390×797 origin with the fixed full-window/top-anchored origin.

- [ ] **Step 4: Visually inspect at original resolution**

Open the owner screenshots and the newly generated connected/connecting/disconnected and scroll boards. Verify:

- title/cartouche share one full-window origin;
- art and every deck label subtract the same scroll delta;
- console labels sit in their measured zones;
- open iris remains unobstructed;
- squint and closed lids contain the same tile pattern as the surrounding ring;
- no circular sticker edge, duplicate ring, or eye-size change appears.

If a visible P0/P1/P2 mismatch remains, fix and regenerate before continuing.

- [ ] **Step 5: Update QA documentation**

In `ops/README.md` document the full-window/inset guide and mosaic-lid states. In `design-qa.md`, replace claims that the Python 390×844 board proves Compose placement; state that it is a geometry/asset oracle and cite the green Android CI plus owner-device APK as the final gate.

- [ ] **Step 6: Verify and commit simulator/QA changes**

Run:

```powershell
python ops/phone-screen-sim.py
git diff --check
git diff -- ops/phone-screen-sim.py ops/README.md design-qa.md
```

Commit:

```powershell
git add ops/phone-screen-sim.py ops/README.md design-qa.md
git commit -m "test: cover full-window home and mosaic eyelids"
```

---

### Task 4: Final boundary audit, branch review, and handoff

**Files:**
- Modify: `CONTEXT_HANDOFF.md`
- Modify: `CLAUDE_MOBILE_REBUILD.md`

**Interfaces:**
- Consumes: green CI evidence, generated boards, task reviews, and the final diff.
- Produces: an auditable handoff without merging or releasing.

- [ ] **Step 1: Prove the forbidden scope has zero diff**

Run a path audit from the task base commit through HEAD. Required zero diff:

```text
TvHomeScreen.kt
TvEskizHome.kt
TvEskizSpec.kt
SFANavigation.kt
tvm_*
TV tests/simulators
backend/API/VPN runtime
.github/workflows
signing/release/OTA
```

Record the exact command and empty result in the report.

- [ ] **Step 2: Run final lightweight verification**

```powershell
python ops/phone-screen-sim.py
git diff --check
git status --short --branch
```

Then confirm the latest GitHub Actions run still shows both `assembleOtherDebug` and `testOtherDebugUnitTest` green for the implementation SHA.

- [ ] **Step 3: Request final whole-branch review**

Review the complete diff from the task merge base. The reviewer must verify:

- full-window policy affects only phone Home;
- portrait top anchor and landscape fallback match the spec;
- no double scale remains in BottomConsole;
- ring-local mosaic uses the same phase/light/layout/parallax;
- opening anatomy and all interaction callbacks remain unchanged;
- tests are behavior tests and actually caught the pre-fix code;
- forbidden scope is untouched.

Address all Critical/Important findings through the SDD fix loop, then run one scoped re-review.

- [ ] **Step 4: Update durable handoff**

Record in both handoff documents:

- owner symptom and screenshot dimensions;
- confirmed Scaffold + crop + console root causes;
- explicit owner decision that closing/blinking lids carry mosaic;
- RED and GREEN CI run URLs;
- implementation SHA and APK artifact;
- exact local simulator command and output names;
- remaining truth: final visual acceptance requires installing that APK and taking a device screenshot.

- [ ] **Step 5: Commit and push documentation**

```powershell
git add CONTEXT_HANDOFF.md CLAUDE_MOBILE_REBUILD.md
git commit -m "docs: hand off mobile home registration fix"
git push
```

Verify the final push did not introduce a new code diff or alter release/OTA state.
