# MaestroVPN Mobile Premium UI Handoff

## Task 1: Responsive policy

Created the pure mobile layout policy consumed by subsequent phone UI tasks:

- `app/src/main/java/com/maestrovpn/tv/compose/premium/MobilePremiumLayout.kt`
- `app/src/test/java/com/maestrovpn/tv/compose/premium/MobilePremiumLayoutTest.kt`

The policy is `Expanded` from 600 dp width, otherwise `Compact` in landscape, and `Regular` in portrait. Horizontal padding is 12/18/32 dp for Compact/Regular/Expanded respectively.

No Android TV, `TvEskizHome`, ViewModel, API/backend, VPN/runtime, or route files were changed. The application remains configured for a universal APK (`splits.abi.isUniversalApk = true`).

## Verification handoff

Run the following in the authoritative GitHub CI checkout:

```powershell
.\gradlew.bat :app:testOtherDebugUnitTest --tests "com.maestrovpn.tv.compose.premium.MobilePremiumLayoutTest"
.\gradlew.bat :app:testOtherDebugUnitTest
.\gradlew.bat :app:assembleOtherDebug
```

Local verification did not reach Kotlin compilation. The Gradle wrapper can be invoked on Windows with:

```powershell
java -classpath gradle\wrapper\gradle-wrapper.jar org.gradle.wrapper.GradleWrapperMain <task>
```

but this snapshot stopped beforehand on Maven read timeouts. Additionally, `app/libs/libbox.aar` is absent, so the CI-generated AAR must be present for the Android dependency path to complete. The owner delegates authoritative build verification to Claude/GitHub CI; therefore no local RED or GREEN test result is claimed.

## Task 2: Mobile premium UI kit

Created the mobile-only reusable Compose kit:

- `app/src/main/java/com/maestrovpn/tv/compose/premium/MobilePremiumTokens.kt`
- `app/src/main/java/com/maestrovpn/tv/compose/premium/MobilePremiumSurface.kt`
- `app/src/main/java/com/maestrovpn/tv/compose/premium/MobilePremiumControls.kt`
- `app/src/androidTest/java/com/maestrovpn/tv/compose/premium/MobilePremiumControlsTest.kt`

It uses the existing mobile surface, nine-patch frames, Playfair title family, and responsive `MobilePremiumLayout`. The kit exposes the specified screen, panel, button, text field, loading, error, segmented, and app-row APIs. Frame content insets are explicit shared tokens; interactive controls expose standard Button, RadioButton, or Checkbox semantics and maintain a 48 dp minimum target. No routes, view models, VPN behavior, API/backend, TV screen, theme, or fantasy primitives were changed.

The final review follow-up also gives `MobilePremiumTextField` a persistent
accessibility purpose: its existing placeholder is exposed as a semantics
content description after entered text replaces the visible placeholder.
`MobilePremiumControlsTest` renders a populated real field and asserts both
that description and its `EditableText` value.

### Verification handoff

Static source inspection and `git diff --check` completed locally. Gradle/device verification was deliberately not run for this task; the local checkout still has the Task 1 dependency limitation (`app/libs/libbox.aar` absent) and this task's execution constraint forbids Gradle/network waits. Run in CI or a prepared Android environment:

```powershell
.\gradlew.bat :app:connectedOtherDebugAndroidTest `
  -Pandroid.testInstrumentationRunnerArguments.class=com.maestrovpn.tv.compose.premium.MobilePremiumControlsTest
.\gradlew.bat :app:assembleOtherDebug
```

## Task 3: Premium phone home and revolver

Updated only the phone branch of `TvHomeScreen` and `PhoneRevolverMenu`:

- the fixed `mobile_home_scene` and connect target remain unchanged;
  `LivingEyeMedallion` retains its blink/gaze/pupil timing, touch-gaze behavior,
  and connect callback while using the final source-derived fit documented below;
- the phone scene now exposes `premium-phone-home`, `premium-status`, `premium-account`, and `premium-revolver` test tags;
- protocol, action, contact, trial, purchase, and phone controls use the mobile premium walnut, aged-gold, and emerald treatment, while retaining their existing callbacks, ordering, URLs, olcRTC routing, and connectivity check;
- protocol tiles expose `Role.RadioButton` and selected state through
  `selectable`; locked olcRTC stays unselected and still invokes its existing
  request callback. The shared action/contact tile exposes `Role.Button`;
  `PhoneRevolverSemanticsTest` covers both roles, selected/locked state, and
  callbacks;
- the `LazyColumn` retains centre snapping outside TalkBack. Its nonlinear, pure `revolverVisualState` function is covered by a JVM test, and touch scrolling emits a restrained centred-row haptic tick. Touch exploration remains flat and unsnapped;
- `MobilePremiumFlowsTest` covers the connected phone home surface and visible active protocol.

### Verification handoff

Static source inspection, callback/order audit, and `git diff --check` completed locally. Gradle tests, Android instrumentation, and device screenshots (`02-home-top.png`, `03-home-mid.png`, `04-home-bottom.png`) were deliberately deferred: this task forbids local Gradle/network execution and the checkout lacks the CI-generated `app/libs/libbox.aar`.

Run in a prepared CI or device environment:

```powershell
.\gradlew.bat :app:connectedOtherDebugAndroidTest `
  -Pandroid.testInstrumentationRunnerArguments.class=com.maestrovpn.tv.compose.premium.MobilePremiumFlowsTest
.\gradlew.bat :app:testOtherDebugUnitTest
.\gradlew.bat :app:assembleOtherDebug
```

## Task 3 follow-up: living eye medallion fit

The final fit is derived from the rendered source geometry rather than an
abstract square or a 90% whole-Canvas scale:

- the real state rectangle is `(230,745)`, `890 x 635`, in the owner-frame
  source coordinate system;
- the established virtual mapping has origin `(268.8,637.3)` and size `822.5`;
- for the fixed 520 medallion and intended 26 px horizontal bronze inset, the
  direct source-to-canvas affine transform has uniform scale `0.5258427`,
  translation X `-94.94382`, and translation Y `-298.70786`;
- the real state bounds map to
  `[26,93.04494]-[494,426.95505]`, centred at `(260,260)`;
- the same `LivingEyeLayerFit` maps open, squint, closed, sclera, iris, pupil,
  catchlight, aperture points, and source-pixel gaze/blink offsets, so the
  layers remain registered;
- every squint/closed state rectangle covers the complete static open-eye
  aperture bounds `[75.36049,202.12036]-[435.09302,339.31186]`, preventing the
  open foundation from being exposed around a blink state;
- `LivingEyeLayerGeometryTest` asserts the literal mapped state and aperture
  bounds, centre, direct scale/translations, uniform X/Y length mapping, and
  static-aperture containment.

Blink timing, gaze/touch timing, pupil response, connection transitions, and
the connect callback are unchanged.

Authoritative CI verification remains:

```powershell
.\gradlew.bat :app:testOtherDebugUnitTest `
  --tests "com.maestrovpn.tv.compose.screen.tvhome.LivingEyeLayerGeometryTest"
.\gradlew.bat :app:testOtherDebugUnitTest
.\gradlew.bat :app:assembleOtherDebug
```

## Premium subscription flow: phone payment and result states

Updated the phone branch of `BuyScreen` without changing its ViewModel or the
Android TV presentation:

- every phone purchase state now consumes the existing responsive
  Compact/Regular/Expanded horizontal-padding policy (12/18/32 dp), while
  retaining the previous 20 dp phone vertical padding;
- the phone payment surface exposes `premium-payment` and uses the mobile
  premium panel and controls;
- the phone QR image now derives its square size from the panel's constrained
  content width. At 320 dp portrait, 18 dp outer horizontal padding and 22 dp
  panel insets leave 240 dp; the 12 dp white-card insets therefore produce a
  216 x 216 dp QR. Wider phones remain capped at 220 dp;
- TV keeps its existing 48 dp purchase padding and 170 dp QR value;
- amount, white QR scan field, scan instructions, direct payment action, order
  code, manual СБП phone, and `Я оплатил` retain their existing order and data;
- the direct action still launches `payUrl` with `Intent.ACTION_VIEW`, and the
  confirmation and retry controls still call `iPaid()` and `loadTariffs()`;
- waiting and activating use the premium loading treatment, done uses a premium
  panel, and errors use the premium retry control;
- `MobilePremiumFlowsTest` covers representative real payment values, action
  callbacks, content order, and waiting, activating, done, and error/retry
  states.

Polling, purchase and activation state routing, tariff pricing, API calls,
profile creation, navigation, and TV behavior remain unchanged.

### Verification handoff

Static source inspection, payment invariant audit, and `git diff --check` were
completed locally. Local Gradle was deliberately not run because this task
explicitly delegates authoritative verification to GitHub CI. Run:

```powershell
.\gradlew.bat :app:connectedOtherDebugAndroidTest `
  -Pandroid.testInstrumentationRunnerArguments.class=com.maestrovpn.tv.compose.premium.MobilePremiumFlowsTest
.\gradlew.bat :app:testOtherDebugUnitTest
.\gradlew.bat :app:assembleOtherDebug
```

## Final review fix wave

Implementation commit:
`fae49273817c23c43c3dc74a80f51ec7d0473ab7`
(`fix(android): address premium mobile review findings`).

The final wave combines the source-derived living-eye fit, responsive
`BuyScreen`/QR policy, revolver roles/selected state, and persistent text-field
purpose described above. Local Gradle was explicitly prohibited, so no local
JVM, instrumentation, build, emulator, or device result is claimed.

Run the final matrix in authoritative GitHub CI or a prepared Android
environment:

```powershell
.\gradlew.bat :app:testOtherDebugUnitTest `
  --tests "com.maestrovpn.tv.compose.screen.tvhome.LivingEyeLayerGeometryTest" `
  --tests "com.maestrovpn.tv.compose.premium.MobilePremiumLayoutTest"

.\gradlew.bat :app:connectedOtherDebugAndroidTest `
  -Pandroid.testInstrumentationRunnerArguments.class=com.maestrovpn.tv.compose.premium.MobilePremiumControlsTest

.\gradlew.bat :app:connectedOtherDebugAndroidTest `
  -Pandroid.testInstrumentationRunnerArguments.class=com.maestrovpn.tv.compose.premium.MobilePremiumFlowsTest

.\gradlew.bat :app:connectedOtherDebugAndroidTest `
  -Pandroid.testInstrumentationRunnerArguments.class=com.maestrovpn.tv.compose.screen.tvhome.PhoneRevolverSemanticsTest

.\gradlew.bat :app:testOtherDebugUnitTest
.\gradlew.bat :app:connectedOtherDebugAndroidTest
.\gradlew.bat :app:assembleOtherDebug
```
