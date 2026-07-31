# MaestroVPN Mobile Home Owner Reference Rebuild Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the rejected phone Home with the exact information hierarchy and proportions of the owner-selected 591×1280 screenshot while preserving the existing VPN callbacks, 4D runtime, six reachable phone screens, and an unchanged TV branch.

**Architecture:** Keep the existing memory-safe L/C/R atlas loader, tilt relighting, and phone/TV seam. Replace the old `PhoneRevolverMenu` presentation with one phone-only control deck ordered exactly like the selected reference, and add a pure geometry policy that moves the medallion group to the approved anchor without changing the master atlas mapping. Use the lightweight 390×844 simulator as the visual gate before any GitHub APK build.

**Tech Stack:** Kotlin, Jetpack Compose, Android Material icons, existing lossless WebP 4D atlas and nine-patch frames, JVM/JUnit tests, Compose instrumentation tests, Python/Pillow visual simulator, GitHub Actions.

## Global Constraints

- Selected visual target: `design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg`, source size 591×1280; rows `y=0..49` are Android-owned status chrome and are not recreated by app code.
- App-owned acceptance viewport: 390×844; target app content begins below system insets.
- Connected: eye fully open at rest; connecting: half-open; disconnected: fully closed with no visible iris.
- Visible order: title/medallion → status → active protocol → phone → support note → Telegram/МАКС/WhatsApp → six-protocol arc → buy → login/test/share.
- Runtime labels: `Авто`, `VLESS`, `Hysteria2`, `AnyTLS`, `NaiveProxy`; transport tag `olcrtc` may be displayed as `WEBRTC` only in the UI and must keep its existing callback and lock semantics.
- Every interactive target is at least 48 dp even when the visible ornament is smaller.
- At 390×844 the primary reference content fits without clipping; short screens, landscape, or large font use scrolling instead of shrinking text below accessible size.
- No full-width black scrim, no opaque emerald selection fill, no 2×N protocol card grid, and no old cylindrical perspective transform.
- Preserve `Mobile4DHome`'s current callback signature so `TvHomeScreen.kt` and `SFANavigation.kt` need no edit.
- Do not change `TvHomeScreen.kt`, `TvEskizHome.kt`, `TvEskizSpec.kt`, `SFANavigation.kt`, `tvm_*`, TV tools, backend, API, VPN runtime, release, OTA, or production signing.
- Do not run a local Gradle/APK build on the owner's computer. Local checks are limited to Python/static checks; authoritative compile/unit/APK verification runs in GitHub Actions after visual approval.
- Do not merge, publish a GitHub Release, sign for production, or publish OTA.

---

### Task 1: Freeze the owner-selected Home reference

**Files:**
- Create: `design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg`
- Modify: `design/mobile-4d-references/README.md`
- Modify: `design/mobile-4d-references/CLAUDE_INSTRUCTIONS.md`

**Interfaces:**
- Consumes: the attached 591×1280 owner screenshot.
- Produces: one durable repository path that every later task and future context window treats as the Home acceptance source.

- [ ] **Step 1: Copy the exact attachment without re-encoding**

Use `Copy-Item -LiteralPath <attachment> -Destination design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg` and verify SHA-256 equality with `Get-FileHash`.

- [ ] **Step 2: Record the measured app-owned geometry**

Add the exact acceptance landmarks to `README.md`: title `(69,54)-(323,88)`, medallion `(26,104)-(364,413)`, status `(128,363)-(265,386)`, phone `(81,407)-(310,445)`, contacts `(34,486)-(356,569)`, protocol arc `(0,570)-(390,705)`, buy `(81,699)-(309,744)`, bottom console `(8,735)-(382,839)`.

- [ ] **Step 3: Make the new file authoritative without deleting historical boards**

State in `CLAUDE_INSTRUCTIONS.md` that boards `00..03` remain product-flow history, while file `04` is the sole Home visual acceptance target from 2026-07-31.

- [ ] **Step 4: Commit**

```text
git add design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg design/mobile-4d-references/README.md design/mobile-4d-references/CLAUDE_INSTRUCTIONS.md
git commit -m "design: record owner-selected mobile home target"
```

### Task 2: Define the phone Home geometry test-first

**Files:**
- Create: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayout.kt`
- Create: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayoutTest.kt`

**Interfaces:**
- Consumes: raw `Mobile4DSceneLayout` and viewport width/height.
- Produces: `phoneHomeReferenceLayout(width: Float, height: Float): PhoneHomeReferenceLayout` with owner-derived bounds, `heroScale`, `heroTranslationY`, `deckTop`, `primaryDeckBottom`, and `minimumInteractiveHeight`.

- [ ] **Step 1: Write the failing tests**

```kotlin
@Test fun portrait390x844MatchesOwnerLandmarks() {
    val layout = phoneHomeReferenceLayout(390f, 844f)
    assertEquals(1.0f, layout.heroScale, 0.03f)
    assertEquals(-58f, layout.heroTranslationY, 10f)
    assertEquals(PhoneHomeBounds(69f, 54f, 323f, 88f), layout.titleBounds)
    assertEquals(363f, layout.deckTop, 3f)
    assertTrue(layout.primaryDeckBottom <= 839f)
}

@Test fun shortViewportScrollsInsteadOfShrinkingTouchTargets() {
    val layout = phoneHomeReferenceLayout(320f, 568f)
    assertTrue(layout.requiresScroll)
    assertTrue(layout.minimumInteractiveHeight >= 48f)
}
```

Production mutation caught: restoring the old hero centre near `y=325.3`, scaling the whole ring to `1.42`, or squeezing the deck will fail owner-derived bounds.

- [ ] **Step 2: Record RED limitation honestly**

The owner forbids local Gradle on this computer, so author the test before production code and record it as `NOT RUN LOCALLY`; the authoritative GitHub command later is:

```text
./gradlew :app:testOtherDebugUnitTest --tests "com.maestrovpn.tv.compose.screen.tvhome.PhoneHomeReferenceLayoutTest" --stacktrace --no-daemon
```

- [ ] **Step 3: Implement the smallest pure layout policy**

Use only Android/Compose-free data classes and arithmetic. Record the exact target rectangles for title, medallion visual region, status, phone, contacts, protocol arc, buy, and bottom console. Keep touch targets at least 48 dp even where the visible ornament is shorter. Extra trial/QR/split/update actions are appended below the primary deck and therefore scroll without changing the first 844 dp.

- [ ] **Step 4: Commit**

```text
git add app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayout.kt app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayoutTest.kt
git commit -m "test: define owner-approved mobile home geometry"
```

### Task 3: Replace the old revolver with the selected control deck

**Files:**
- Create: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeControlDeck.kt`
- Create: `app/src/androidTest/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeControlDeckTest.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DHome.kt`
- Delete: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneRevolverMenu.kt`
- Delete: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneRevolverVisualStateTest.kt`
- Delete: `app/src/androidTest/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneRevolverSemanticsTest.kt`

**Interfaces:**
- Consumes: the exact existing `PhoneRevolverMenu` callback/data parameter set.
- Produces: `PhoneHomeControlDeck(...)` with the same parameter set, stable `home-*` tags, and no dependency on the old revolver symbols.

- [ ] **Step 1: Write failing interaction tests**

```kotlin
@Test fun selectedReferencePrimaryActionsRemainReachable() {
    composeRule.onNodeWithTag("home-action-buy").performClick()
    composeRule.onNodeWithTag("home-action-login").performClick()
    composeRule.onNodeWithTag("home-action-network-test").assertHasClickAction()
    composeRule.onNodeWithTag("home-action-share").performClick()
    assertEquals(listOf("buy", "login", "share"), calls)
}

@Test fun sixProtocolsExposeRadioSemanticsWithoutOpaqueSelectionCard() {
    composeRule.onNodeWithTag("home-protocol-vless").assertIsSelected().performClick()
    composeRule.onNodeWithTag("home-protocol-olcrtc").assertIsNotSelected().performClick()
}
```

Add callback tests for `onScanQr`, `onSplitTunnel`, and `onEnterTrial` below the primary fold so the six-screen flow remains reachable.

- [ ] **Step 2: Build the clean section order**

Use one `LazyColumn` only for overflow behavior; remove snap fling, masks, `graphicsLayer` perspective, haptic cylinder ticks, and the 2×N grid. The first item group must be the exact owner order. Use existing `frame_button`/`frame_panel` nine-patches and Material icons; do not draw fake icons or duplicate fullscreen backgrounds.

- [ ] **Step 3: Build the fixed three-contact row**

Order must be `Telegram`, `МАКС`, `WhatsApp`; preserve URI callbacks `https://t.me/wapmixx`, `https://max.ru/`, and `https://wa.me/79778116564`. The phone panel preserves `tel:+79778116564`.

- [ ] **Step 4: Build the six-sector protocol row**

Use available runtime protocols plus `olcrtc`, with stable order `auto`, `vless`, `hysteria2`, `anytls`, `naive`, `olcrtc`. Keep `Role.RadioButton`, selected semantics, locked olcRTC request behavior, controlled autosize, and a thin emerald indicator only—no emerald background fill.

- [ ] **Step 5: Preserve non-reference navigation below the fold**

When applicable, append trial, QR scan, split tunnel, and update entries after the bottom console. They must remain reachable by scroll but must not displace the primary target at 390×844.

- [ ] **Step 6: Remove obsolete symbols and commit**

```text
rg -n "PhoneRevolverMenu|revolverVisualState|premium-revolver" app ops
git add app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome app/src/test app/src/androidTest
git commit -m "feat: rebuild mobile home controls from owner reference"
```

### Task 4: Re-anchor the 4D hero without touching TV

**Files:**
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DHome.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeMedallion.kt`
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayoutTest.kt`

**Interfaces:**
- Consumes: `PhoneHomeReferenceLayout` from Task 2.
- Produces: a phone-only ring/eye group with the selected target anchor and a connected initial resting eye that is open.

- [ ] **Step 1: Add failing resting-state tests**

```kotlin
@Test fun connectedInitialRestingPhaseIsOpen() = assertEquals(0f, initialLidPhase(true, null), 0f)
@Test fun disconnectedInitialRestingPhaseIsClosed() = assertEquals(1f, initialLidPhase(false, null), 0f)
@Test fun connectingOverrideIsHalfOpen() = assertEquals(0.5f, initialLidPhase(false, 0.5f), 0f)
```

- [ ] **Step 2: Apply one hero transform to ring and eye**

Use the same `heroScale` (approximately `1.0`, not `1.42`) and `heroTranslationY` (approximately `-58` at 390x844) for `home_ring` and the eye hit target so they remain registered. Keep `home_vines` on the master scene crop with its existing parallax; it is a full-height layer and translating/scaling it with the medallion would expose the bottom and clip the sides. Position the title from its exact target bounds. Keep wood, cartouche, and perimeter frame on the original crop.

- [ ] **Step 3: Keep the living-eye state machine**

Initial composition in connected state starts at lid phase `0f`; a real transition from disconnected still animates through squint; disconnected and connecting overrides cannot enter the autonomous blink loop. Keep gaze/touch and normal connected blinking.

- [ ] **Step 4: Prove zero TV seam diff and commit**

```text
git diff --exit-code 3c72aa1ff2d4d17c0dd78a48da295f73f0c0104b..HEAD -- app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/TvHomeScreen.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/TvEskizHome.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/TvEskizSpec.kt app/src/main/java/com/maestrovpn/tv/compose/navigation/SFANavigation.kt ":(glob)app/src/main/res/**/tvm_*" ":(glob)ops/tv-*"
git add app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DHome.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeMedallion.kt app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayoutTest.kt
git commit -m "fix: match mobile hero to owner-selected anchor"
```

### Task 5: Produce and visually compare the lightweight 390×844 preview

**Files:**
- Modify: `ops/phone-screen-sim.py`
- Modify: `ops/README.md`
- Create: `design-qa.md`

**Interfaces:**
- Consumes: the committed center-light atlas, eye assets, and exact geometry from the selected reference.
- Produces: `build/phone-screen-sim/owner-home-connected.png`, `owner-home-connecting.png`, `owner-home-disconnected.png`, and a comparison board against file `04`.

- [ ] **Step 1: Rewrite only the simulator Home section**

Render three Home states. Connected uses `mobile_eye_open.webp`, connecting uses `mobile_eye_squint.webp`, disconnected uses `mobile_eye_closed.webp`. Render the same section order and labels as the production deck; do not simulate the deleted black mask or 2×N cards.

- [ ] **Step 2: Run lightweight gates**

```text
python ops/mobile-4d-assets.py --check
python ops/phone-screen-sim.py
```

Expected: both exit 0; Home images are 780×1688 physical pixels for 390×844@2x and no content is cropped.

- [ ] **Step 3: Perform blocking design QA**

Compare the selected source and simulated connected state at the same app-owned viewport. Record measured differences in `design-qa.md`; fix P0/P1/P2 issues until it says `final result: passed`. If exact ornamental chrome is impossible with existing source assets, write `final result: blocked` and show the preview to the owner instead of starting CI.

- [ ] **Step 4: Commit the simulator, not ignored build output**

```text
git add ops/phone-screen-sim.py ops/README.md design-qa.md
git commit -m "test: add owner-reference mobile home visual gate"
```

### Task 6: GitHub compile gate and test APK after visual approval

**Files:**
- Modify: `.github/workflows/android-test.yml` only if needed to compile `androidTest` with `assembleOtherDebugAndroidTest`.
- Modify: `CONTEXT_HANDOFF.md`

**Interfaces:**
- Consumes: visually accepted Home implementation.
- Produces: one GitHub Actions test APK artifact and a durable continuation checkpoint; no release or OTA.

- [ ] **Step 1: Run static final checks before push**

```text
git diff --check
git status --short --branch
rg -n "PhoneRevolverMenu|revolverVisualState|premium-revolver" app ops
python ops/mobile-4d-assets.py --check
python ops/phone-screen-sim.py
```

- [ ] **Step 2: Push only after owner accepts the preview**

Push `codex/mobile-4d-interface`; the open draft PR triggers `.github/workflows/android-test.yml` automatically.

- [ ] **Step 3: Verify CI and artifact, not only the green job**

Require `assembleOtherDebug`, `testOtherDebugUnitTest`, and, if added, `assembleOtherDebugAndroidTest` to exit 0. Query the run artifacts and confirm the APK file exists despite `Upload test APK` using `continue-on-error`.

- [ ] **Step 4: Refresh handoff and commit**

Record current branch/HEAD/PR, exact run ID, unit/build results, artifact ID/size/SHA-256, manual phone verification pending, and prohibitions on merge/release/OTA.

## Self-review

- Spec coverage: selected Home hierarchy, eye states, protocol callbacks, support contacts, all six normal-flow routes, short-screen scroll, safe insets, visual preview, TV isolation, CI artifact verification, and durable context are each assigned to a task.
- Asset gap: the selected screenshot contains ornament not present in the current 15-layer atlas. Task 5 explicitly blocks CI if the existing real assets cannot achieve an acceptable match; it does not invent or hide that gap.
- Placeholder scan: no TBD/TODO steps remain.
- Type consistency: Tasks 2 and 4 share `PhoneHomeReferenceLayout`; Task 3 preserves the existing callback surface; Task 5 consumes the same measured target.
