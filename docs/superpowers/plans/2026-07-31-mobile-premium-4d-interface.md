# MaestroVPN Premium Mobile 4D Interface Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use subagent-driven-development to execute this plan task-by-task.

**Goal:** Rebuild the six screens in MaestroVPN’s normal phone flow in the approved premium 4D language: a true layered and tilt-relit home plus one coherent lightweight 4D shell for its five child screens and reachable dialogs. Preserve all VPN behavior and leave Android TV unchanged.

**Architecture:** Keep `TvHomeScreen` as the universal form-factor gate, but move its phone branch into a dedicated `Mobile4DHome`. A deterministic asset pipeline converts the 2160×4670 source PNG pack into identical left/centre/right atlas pages no larger than 2048×2048, with transparent fragments tightly cropped and edge-extruded. The home compositor draws the full five-layer scene; internal routes use a lighter `MobilePremium4DShell` built from the same walnut/bronze system without loading the medallion composition. A pure scene model owns crop geometry, light weights, parallax, and eye state; Android-specific code owns sensors, bitmap lifecycle, and Compose drawing. Existing premium/fantasy components are upgraded and reused instead of layering another visual tree over donor UI.

**Tech Stack:** Kotlin 2.3.10, Jetpack Compose Material 3, Android `SensorManager`, Android `BitmapFactory`, Python 3 + Pillow for deterministic asset generation, JUnit 4, Gradle Android plugin 9.0.1.

**Visual direction:** Preserve the approved dark walnut, antique bronze, emerald, and ruby language. Keep Playfair Display as the carved-nameplate title and the existing body typography for functional controls. The memorable element is one restrained physical illusion: the carved relief moves by depth and changes lighting with the phone, while the menu remains calm and readable.

**Authoritative mobile scope, corrected by the owner:**

There are exactly six user screens reachable from a normal phone launch:

1. Home: `tvhome`.
2. Login/code activation: `claim`.
3. Camera QR activation: `scanqr`.
4. Subscription and payment: `buy`.
5. Free trial: `trial`.
6. Per-app VPN selection: `split`.

`IosKaringDialog` is one reachable share dialog, not a seventh screen. Account and protocol selection are sections of Home. Disconnected/connecting/connected are Home states. Tariffs/payment/waiting/activating/done/error are states of one `BuyScreen`.

The registered Settings/Log/Groups tree and profile editor are not reachable from the normal phone UI because both navigation lists are empty and Home has no callbacks to them. They are explicitly out of this visual migration. `Dashboard`, `Connections`, and `Tools` are not registered in the active graph. Do not invent or expose any of these screens.

**Global constraints:**

- Never modify `TvEskizHome`, `tvm_*`, TV-only tooling, or TV behavior. Shared files require an explicit phone/TV gate.
- Do not place a new UI on top of the old phone UI. Replace old phone branches and delegate existing shells to the new component kit.
- Disconnected means a fully closed eye; connecting means a stable half-open eye; connected alone gets the living open/blinking eye.
- Preserve callbacks, route arguments, ViewModel state, test tags, roles, content descriptions, TalkBack behavior, minimum 48 dp touch targets, back behavior, and string resources for the six-screen flow.
- No baked text, no eye baked into the ring, and no cross-layer shadows in art.
- Transparent relief blends in an isolated layer with premultiplied additive light mixing; wood uses normal opaque crossfade. Draw at most the two non-zero light variants for a given tilt.
- Respect reduced motion, low-RAM devices, 320×568 phones, 390×844 phones, landscape, and font scales up to 2.0.

---

### Task 1: Lock the pure 4D contracts with failing tests

**Files:**
- Create: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModelTest.kt`
- Create later: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModel.kt`

- [ ] **Step 1: Write tests that name the regressions**

Cover these hand-derived behaviors:

```kotlin
@Test
fun centreTiltUsesOnlyCentreLight() {
    assertEquals(Mobile4DLightMix(Mobile4DLightSide.Right, 1f, 0f), mobile4DLightMix(0f, Mobile4DLightSide.Right))
}

@Test
fun disconnectedEyeIsClosed() {
    assertEquals(Mobile4DEyeState.Disconnected, mobile4DEyeState(connected = false, connecting = false))
}

@Test
fun cropMappingKeepsMedallionOnTheApprovedAnchor() {
    val layout = mobile4DSceneLayout(width = 390f, height = 844f)
    assertEquals(196.6f, layout.medallionCenterX, 0.2f)
    assertEquals(325.3f, layout.medallionCenterY, 0.2f)
}
```

Also test clamping beyond `[-1, 1]`, left/right blend symmetry, side-selection hysteresis, and the `Connecting` eye phase.

- [ ] **Step 2: Run the focused test and confirm RED**

Run:

```powershell
.\gradlew.bat :app:testOtherDebugUnitTest --tests "*Mobile4DSceneModelTest" --no-daemon
```

Expected: compilation fails because the 4D model does not exist yet. If the local libbox artifact is unavailable, obtain the normal non-OTA CI artifact before treating infrastructure failure as the RED result.

- [ ] **Step 3: Implement the minimal pure model**

Create immutable data/enums and pure functions for:

- the 2160×4670 master scene coordinate system and density-independent viewport mapping;
- the approved medallion anchor derived from `(430, 711, radius 260)` on 853×1844;
- `ContentScale.Crop` mapping;
- clamped centre/side light weights;
- left/right side hysteresis;
- per-layer parallax offsets;
- `Disconnected`, `Connecting`, and `Connected` eye states.

- [ ] **Step 4: Run the focused test and confirm GREEN**

Run the same Gradle command. Expected: all model tests pass.

- [ ] **Step 5: Commit the pure model**

```powershell
git add app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModel.kt app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModelTest.kt
git commit -m "test: define mobile 4D scene behavior"
```

### Task 2: Build deterministic, memory-bounded runtime assets

**Files:**
- Create: `ops/mobile-4d-assets.py`
- Create: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DGeneratedAssetsTest.kt`
- Generate: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DGeneratedAssets.kt`
- Generate: `app/src/main/assets/mobile_4d/*.webp`
- Modify: `design/mobile-asset-redraw/README.md`

- [ ] **Step 1: Write the generated-manifest contract test**

The test must fail until the generated manifest exists. Assert:

- master canvas is exactly 2160×4670;
- z-order is `wood`, `frame`, `cartouche`, `vines`, `ring`;
- every page is at most 2048×2048 and every fragment source rectangle is inside its page;
- every fragment has distinct `_l`, `_c`, `_r` page paths;
- page layout and every scene rectangle are identical for `_l`, `_c`, and `_r`;
- every fragment includes the required two-pixel edge-extruded gutter;
- the complete manifest can reconstruct every layer in the original scene coordinates.

- [ ] **Step 2: Confirm RED**

Run:

```powershell
.\gradlew.bat :app:testOtherDebugUnitTest --tests "*Mobile4DGeneratedAssetsTest" --no-daemon
```

Expected: compilation fails because `Mobile4DGeneratedAssets` does not exist.

- [ ] **Step 3: Implement the deterministic pipeline**

`ops/mobile-4d-assets.py` must:

- validate the exact 15-file source inventory and 2160×4670 geometry;
- require RGB wood and RGBA relief layers;
- require identical alpha geometry for `_l/_c/_r`;
- cut every layer on one deterministic 3×8 logical grid and tightly crop non-empty transparent fragments;
- add a two-pixel edge-extruded gutter to prevent sampling seams;
- pack fragments into identical lossless WebP atlas pages no larger than 2048×2048 for all light directions;
- generate `Mobile4DGeneratedAssets.kt` with page paths, page source rectangles, scene rectangles, z-order, and layer identity;
- reconstruct atlas pages back to full canvases and compare decoded pixels with the source as part of generation;
- support `--check` without changing tracked output.

Runtime decoding uses density targeting rather than storing a second baked size: bucket the physical viewport width to 64-pixel steps, cap it at 1620, and decode with `inScaled`, `inDensity`, and `inTargetDensity`. The loader must keep decoded art below 35–40% of `ActivityManager.memoryClass`. Low-RAM mode loads only centre lighting, caps the target width at 1080, and disables tilt relighting. This avoids roughly 577 MiB of decoded source canvases while keeping all three light directions available on normal devices.

- [ ] **Step 4: Generate and validate**

Run:

```powershell
python ops/mobile-4d-assets.py
python ops/mobile-4d-assets.py --check
```

Expected: deterministic identical page geometry for all three lights, no page above 2048×2048, strict reconstruction PASS, and stable generated output.

- [ ] **Step 5: Confirm GREEN**

Run the generated-manifest test. Expected: pass.

- [ ] **Step 6: Commit the pipeline and runtime art**

```powershell
git add ops/mobile-4d-assets.py design/mobile-asset-redraw/README.md app/src/main/assets/mobile_4d app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DGeneratedAssets.kt app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DGeneratedAssetsTest.kt
git commit -m "feat: prepare memory-safe mobile 4D assets"
```

### Task 3: Add lifecycle-safe tilt and bitmap loading

**Files:**
- Create: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DTilt.kt`
- Create: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DBitmapLoader.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModel.kt`
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModelTest.kt`

- [ ] **Step 1: Add failing tests for sensor-independent normalization**

Test display-rotation remapping, clamping, dead zone, and low-pass behavior through pure functions. The production change caught is an inverted or permanently saturated tilt axis.

- [ ] **Step 2: Confirm RED**

Run the focused model tests and observe the missing normalization API.

- [ ] **Step 3: Implement the sensor adapter**

Use `TYPE_GAME_ROTATION_VECTOR`, falling back to `TYPE_ROTATION_VECTOR`. Calibrate the first stable pose as neutral, remap for display rotation, low-pass by elapsed time, clamp the physical range to ±12° and normalize to `[-1, 1]`, register only while resumed, and unregister on pause/disposal. Respect disabled system animation on API 26+ by returning neutral tilt.

- [ ] **Step 4: Implement the bitmap loader**

Decode atlas pages with `BitmapFactory` on `Dispatchers.IO` using the target-density bucket. Normal devices may retain all three light page sets only while the full home is composed and the measured byte count remains inside the memory-class budget. Otherwise retain centre plus the active side selected with hysteresis. Release superseded/disposed bitmaps only after they are no longer part of composition. Until a side set loads, render centre at full opacity. Internal screens must use the lightweight shell and must not retain the home medallion atlas.

- [ ] **Step 5: Confirm GREEN and inspect memory assumptions**

Run model tests, then add a debug-only log or local diagnostic that prints decoded byte counts. Remove transient diagnostics before commit; retain the explicit budget calculation in KDoc.

- [ ] **Step 6: Commit**

```powershell
git add app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DTilt.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DBitmapLoader.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModel.kt app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModelTest.kt
git commit -m "feat: add mobile 4D tilt and asset lifecycle"
```

### Task 4: Build the clean mobile-only 4D compositor

**Files:**
- Create: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DHome.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeMedallion.kt`
- Modify: `app/src/androidTest/java/com/maestrovpn/tv/compose/premium/MobilePremiumFlowsTest.kt`

- [ ] **Step 1: Add failing interaction/state coverage**

Add instrumentation assertions for:

- disconnected home exposes “Подключить VPN” and a closed-eye semantic state;
- connecting home exposes a distinct connecting eye state;
- connected home exposes “Отключить VPN”;
- existing `premium-phone-home`, `premium-status`, `premium-account`, and `premium-revolver` tags remain.

Keep the existing protocol/menu flow assertions intact.

- [ ] **Step 2: Implement `Mobile4DHome`**

Render in this exact order:

```text
wood → code shadow → frame → code shadow → cartouche
     → code shadow → vines → code shadow → ring
     → existing eye → Playfair title → existing revolver UI
```

Use the pure triangular weights `L=max(-x,0)`, `C=1-abs(x)`, `R=max(x,0)` and draw only the two non-zero variants. Opaque wood uses normal source-over crossfade. Transparent relief uses an isolated `saveLayer` and premultiplied additive blending so alpha does not dip or grow a halo. Apply restrained depth offsets: wood 0.5 dp, frame 1.5 dp, cartouche 2.5 dp, vines 3.5 dp, ring and eye 5 dp. Shadows are tinted runtime draws of each layer’s own alpha, never baked into another asset. Clip the full scene to the viewport.

Place the eye and menu from the pure crop mapping. Keep touch gaze and the transparent connect target. Use `opennessOverride = 0f` while disconnected, a stable half-open value while connecting, and no override only while connected. This makes the user-required closed disconnected state deterministic.

Draw `MaestroVPN` as real Playfair text on the empty cartouche, with the existing premium gold palette and restrained text shadow.

- [ ] **Step 3: Preserve accessibility and motion restraint**

Keep all existing roles/test tags, 48 dp touch targets, TalkBack-flat revolver behavior, and readable state colors. Decorative art has no content description. Reduced motion returns neutral tilt without changing functionality.

- [ ] **Step 4: Compile-check the composable**

Run:

```powershell
.\gradlew.bat :app:compileOtherDebugKotlin --no-daemon
```

Expected: Kotlin compiles with no new warnings promoted to errors.

- [ ] **Step 5: Commit**

```powershell
git add app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DHome.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeMedallion.kt app/src/androidTest/java/com/maestrovpn/tv/compose/premium/MobilePremiumFlowsTest.kt
git commit -m "feat: compose premium mobile 4D home"
```

### Task 5: Switch only the phone seam and preserve VPN behavior

**Files:**
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/TvHomeScreen.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/navigation/SFANavigation.kt`

- [ ] **Step 1: Add the explicit connecting input**

Add `connecting: Boolean = false` to `TvHomeScreen`. Pass `serviceStatus == Status.Starting` from both navigation call sites.

- [ ] **Step 2: Replace the old phone tree**

Leave the `if (isTv) { TvEskizHome(...) }` branch byte-for-byte behaviorally unchanged. Replace only the phone `else` with `Mobile4DHome(...)`. Remove the old phone-only glow/web from the universal outer `drawBehind`; do not leave it under the new scene.

- [ ] **Step 3: Preserve callbacks exactly**

Forward the existing status/account/protocol values and every callback unchanged: connect, protocol live switch/pending selection, olcRTC, buy, login/code, QR, split tunnel, share phone, and trial.

- [ ] **Step 4: Run the JVM regression suite**

Run:

```powershell
.\gradlew.bat :app:testOtherDebugUnitTest --no-daemon
```

Expected: all tests pass.

- [ ] **Step 5: Audit TV scope**

Run:

```powershell
git diff -- app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/TvEskizHome.kt app/src/main/res/drawable-nodpi/tvm_*
```

Expected: empty diff.

- [ ] **Step 6: Commit**

```powershell
git add app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/TvHomeScreen.kt app/src/main/java/com/maestrovpn/tv/compose/navigation/SFANavigation.kt
git commit -m "feat: switch phone home to clean 4D scene"
```

### Task 6: Introduce one clean phone-only 4D shell and component kit

**Files:**
- Create: `app/src/main/java/com/maestrovpn/tv/compose/premium/MobilePremium4DShell.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/premium/MobilePremiumSurface.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/premium/MobilePremiumControls.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/premium/MobilePremiumLayout.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/premium/MobilePremiumTokens.kt`
- Modify: relevant instrumentation tests under `app/src/androidTest/`

- [ ] **Step 1: Add failing shared-component coverage**

Test the semantic and interaction contracts for the phone scaffold, top bar, setting row, section panel, button, text field, switch, dialog, and bottom sheet. Verify that controls retain accessible names, roles, enabled/disabled behavior, and 48 dp targets. Add a form-factor test that proves the new shell is not selected for TV.

- [ ] **Step 2: Implement the shared shell**

Create a single phone-only scaffold with:

- a lightweight centre-lit wood/frame background, loading no cartouche, ring, or home eye;
- subtle tilt-driven light/parallax only when motion and memory policy allow it;
- system-bar insets, safe keyboard handling, compact and landscape layout policies;
- real Playfair headings and existing localized body strings;
- code-rendered depth, focus, pressed, selected, disabled, error, and loading states;
- reusable premium top bar, section panel, list/setting row, dialog surface, and modal-sheet surface.

The shell replaces `MobilePremiumScreen` only in the six-screen flow. It must not wrap an already-rendered donor background. Do not change the hidden Settings/Log/Groups/profile screens or shared fantasy components in this task.

- [ ] **Step 3: Keep the TV seam explicit**

Every shared composable touched by both form factors must preserve its existing TV branch. If a source file has no safe form-factor gate, add a phone wrapper rather than changing the shared rendering unconditionally.

- [ ] **Step 4: Confirm shared tests and compile**

Run focused instrumentation compilation plus unit tests. Capture one component-gallery screen at 320×568 and 390×844 if a device is available.

- [ ] **Step 5: Commit**

```powershell
git add app/src/main/java/com/maestrovpn/tv/compose/premium app/src/androidTest
git commit -m "feat: add shared mobile premium 4D shell"
```

### Task 7: Migrate activation, purchase, QR, split, and share flows

**Files:**
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/claim/ClaimScreen.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/trial/TrialScreen.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/purchase/BuyScreen.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/qrscan/ScanQrActivateScreen.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/component/qr/QRScanSheet.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/profileoverride/PerAppProxyScreen.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/IosKaringDialog.kt`
- Modify: `app/src/androidTest/java/com/maestrovpn/tv/compose/premium/MobilePremiumFlowsTest.kt`

- [ ] **Step 1: Extend tests before visual code**

Lock all existing callbacks and state transitions:

- claim/trial idle, loading, success, and error;
- tariffs, payment method, waiting, activating, done, and payment error;
- QR permission explanation, denied/no-camera, scanner, success, and unsupported payload;
- split-tunnel empty/search/selected/save and its manage-route reuse;
- share link and QR actions.

The tests must assert behavior and semantics, not implementation class names or only screenshots.

- [ ] **Step 2: Replace donor surfaces with the shared 4D kit**

Preserve each ViewModel, callback, route, and copy. Use one dominant carved panel per screen, clear primary action hierarchy, premium state badges, and readable long-form/error text. Keep the live camera preview unobstructed and do not style the system permission dialog itself.

- [ ] **Step 3: Check adaptive layouts**

Verify 320×568, 390×844, landscape, keyboard-open forms, and font scale 2.0. No fixed-height panel may clip a translated string or block the primary action.

- [ ] **Step 4: Run focused tests and commit**

```powershell
git add app/src/main/java/com/maestrovpn/tv/compose app/src/androidTest
git commit -m "feat: migrate mobile activation and purchase flows"
```

### Task 8: Unify reachable app-owned overlays and prove the six-screen scope

**Files:**
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/MainActivity.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/base/SelectableMessageDialog.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/component/UpdateDialog.kt`
- Modify: any dialog owned by Home, Claim, QR, Buy, Trial, or Split and identified by active call sites
- Create: `docs/mobile-screen-coverage.md`
- Add/modify: navigation and overlay tests under `app/src/androidTest/`

- [ ] **Step 1: Inventory call sites before editing**

Map every app-owned alert, QR permission explanation, service/update/download progress dialog, and six-screen validation dialog to an owning route/state. Exclude Android system permission UI, OS-intent-only import/profile flows, hidden Groups UI, and unused dialog declarations with no call site.

- [ ] **Step 2: Delegate every active overlay to the premium surfaces**

Preserve dismissal rules, cancelability, progress, error details, links, and action ordering. Large/error text must scroll. Destructive actions remain visually distinct but not exaggerated.

- [ ] **Step 3: Add a route coverage contract**

Document the six normal-flow routes, the share dialog, their source composables, their reference-board mapping, their migrated shell, and automated/manual evidence. Explicitly record that standalone account/protocol/payment-state screens were not invented.

Record Settings/Log/Groups/profile and their children as excluded hidden/internal or OS-intent-only flows. Do not add navigation entry points in this migration.

- [ ] **Step 4: Compile instrumentation and commit**

```powershell
.\gradlew.bat :app:assembleOtherDebugAndroidTest --no-daemon
git add app/src/main/java docs/mobile-screen-coverage.md app/src/androidTest
git commit -m "feat: unify mobile premium overlays"
```

### Task 9: Delete the replaced mobile flatten and repair mobile tooling

**Files:**
- Delete: `app/src/main/res/drawable-nodpi/mobile_home_scene.webp`
- Modify: `ops/phone-screen-sim.py`
- Modify: `ops/mobile-eye-natural-assets.py`
- Modify: `ops/README.md`

- [ ] **Step 1: Move the phone simulator to generated central tiles**

Reconstruct the central scene from `app/src/main/assets/mobile_4d/` and the generated geometry, then draw the existing closed/open eye, Playfair title, and current menu. Do not modify any TV simulator.

- [ ] **Step 2: Stop the eye tool from rebuilding the obsolete flat scene**

Keep generation/validation of the six eye resources, but remove `save_scene()` and every dependency on `mobile_home_scene_lower_base.webp` or the deleted runtime flatten.

- [ ] **Step 3: Prove the obsolete resource has no consumers**

Run:

```powershell
rg -n "mobile_home_scene|mobile_home_scene_lower_base" app ops
```

Expected: no runtime/tool references remain.

- [ ] **Step 4: Delete the old mobile-only flatten**

Delete only `app/src/main/res/drawable-nodpi/mobile_home_scene.webp`. Do not delete or modify `mobile_eye_*`, `mobile_surface.webp`, `tvm_*`, or TV tooling.

- [ ] **Step 5: Run the simulator**

Run:

```powershell
python ops/phone-screen-sim.py
```

Expected: a new `build/phone-screen-sim/phone-screens.png` with closed disconnected eye, open connected eye, crisp Playfair title, and no old flattened background.

- [ ] **Step 6: Commit**

```powershell
git add ops/phone-screen-sim.py ops/mobile-eye-natural-assets.py ops/README.md
git rm app/src/main/res/drawable-nodpi/mobile_home_scene.webp
git commit -m "chore: remove replaced mobile home flatten"
```

### Task 10: Verify all six mobile screens, build, and hand off durably

**Files:**
- Create: `design-qa.md`
- Modify: `CONTEXT_HANDOFF.md`
- Modify: `design/mobile-asset-redraw/README.md`

- [ ] **Step 1: Create equal-size visual evidence**

Capture/export the six rows in `docs/mobile-screen-coverage.md` plus the share dialog, including important state variants of the same screen. For each reference-backed screen, normalize the approved board crop and implementation capture to the same pixel dimensions and build a side-by-side comparison containing both source and implementation. The home set must include disconnected, connecting, and connected at 390×844; disconnected must show a fully closed eye.

- [ ] **Step 2: Run Product Design QA**

Open the combined comparisons and inspect typography, crop/spacing, colors, image sharpness/alpha edges, closed/half-open/open eye states, dialog/sheet elevation, scrolling, keyboard behavior, and functional copy. Record every P0/P1/P2 finding in `design-qa.md`, fix it, recapture, and repeat. A simulator-only comparison cannot produce `final result: passed`; record Android device QA as blocked until a real emulator/device capture exists.

- [ ] **Step 3: Run all local gates**

Run:

```powershell
python ops/mobile-4d-assets.py --check
python ops/phone-screen-sim.py
.\gradlew.bat :app:testOtherDebugUnitTest :app:assembleOtherDebug :app:assembleOtherDebugAndroidTest --no-daemon
git diff --check
git status --short
```

When a device is available, also run `connectedOtherDebugAndroidTest` and capture the route matrix. Expected: asset check PASS, simulator PASS, unit tests/build/instrumentation compilation PASS, no whitespace errors, and only intentional files changed.

- [ ] **Step 4: Update durable context**

Record the implementation branch/commit, runtime asset architecture, memory budget, exact mobile/TV boundary, completed route matrix, remaining device-QA rows, test evidence, CI state, and the next safe action in `CONTEXT_HANDOFF.md`.

- [ ] **Step 5: Commit verification docs**

```powershell
git add design-qa.md CONTEXT_HANDOFF.md design/mobile-asset-redraw/README.md
git commit -m "docs: hand off premium mobile 4D interface"
```

- [ ] **Step 6: Push and open a separate draft PR**

Push `codex/mobile-4d-interface` and open a draft PR targeting `main`. Do not merge, publish a release, or trigger OTA. Wait for the non-OTA Android build and unit-test checks; fix failures on this branch.
