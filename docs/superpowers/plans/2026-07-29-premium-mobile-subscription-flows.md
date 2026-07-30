# Premium Mobile Subscription Flows Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle the phone purchase, claim, and trial flows with the approved Emerald Relic premium kit while preserving all behavior and TV rendering.

**Architecture:** Keep each existing screen and ViewModel contract intact. Gate new composables behind the existing `rememberIsTv()` branch, reuse `compose/premium`, and leave the TV branches structurally unchanged.

**Tech Stack:** Kotlin, Jetpack Compose, Material 3, Android Compose UI tests, JUnit.

## Global Constraints

- Phone UI only; Android TV rendering remains unchanged.
- Do not change navigation, ViewModels, API calls, polling, pricing, payment, profile, or VPN behavior.
- Preserve input state, focus, callbacks, state ordering, and QR scan contrast.
- Do not run local Gradle; authoritative verification is GitHub CI.

---

### Task 1: Claim and trial phone forms

**Files:**
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/claim/ClaimScreen.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/trial/TrialScreen.kt`
- Test: `app/src/androidTest/java/com/maestrovpn/tv/compose/premium/MobilePremiumFlowsTest.kt`

**Interfaces:**
- Consumes: `MobilePremiumScreen`, `MobilePremiumPanel`, `MobilePremiumTextField`, `MobilePremiumButton`, `MobilePremiumError`.
- Produces: phone claim/trial branches with tags `premium-claim` and `premium-trial`.

- [ ] **Step 1: Write failing Compose tests**

Add tests that render the phone branches and assert the title, input placeholder,
primary action, and error/loading state through the real composables.

- [ ] **Step 2: Record RED boundary**

The local Gradle run is prohibited and blocked by the missing `app/libs/libbox.aar`;
record the exact CI command without claiming a local failure:

```powershell
.\gradlew.bat :app:connectedOtherDebugAndroidTest `
  -Pandroid.testInstrumentationRunnerArguments.class=com.maestrovpn.tv.compose.premium.MobilePremiumFlowsTest
```

- [ ] **Step 3: Implement the phone-only premium branches**

Use the shared premium surface and controls when `isTv == false`. Keep the existing
TV tree and invoke exactly `viewModel.claim(code)` and `viewModel.activate(nick)`.

- [ ] **Step 4: Static verification**

Run:

```powershell
git diff --check
rg -n "viewModel\\.claim\\(code\\)|viewModel\\.activate\\(nick\\)" app/src/main/java/com/maestrovpn/tv/compose/screen
```

- [ ] **Step 5: Commit**

```powershell
git add -- app/src/main/java/com/maestrovpn/tv/compose/screen/claim/ClaimScreen.kt app/src/main/java/com/maestrovpn/tv/compose/screen/trial/TrialScreen.kt app/src/androidTest/java/com/maestrovpn/tv/compose/premium/MobilePremiumFlowsTest.kt
git commit -m "feat(android): refresh premium activation flows"
```

### Task 2: Phone tariff selection

**Files:**
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/purchase/BuyScreen.kt`
- Test: `app/src/androidTest/java/com/maestrovpn/tv/compose/premium/MobilePremiumFlowsTest.kt`

**Interfaces:**
- Consumes: `BuyState.Tariffs`, `TariffItem`, `MobilePremiumPanel`, and existing `buy(key)`.
- Produces: tagged phone tariff surface `premium-tariffs`.

- [ ] **Step 1: Write a failing tariff-selection test**

Render two literal tariffs, assert both names/prices are visible, click the second,
and assert its literal key reaches the callback.

- [ ] **Step 2: Implement premium phone tariff rows**

Keep the TV two-column `TariffCard` path unchanged. On phone, render the same items
in order as premium framed rows and call the existing `viewModel.buy(tariff.key)`.

- [ ] **Step 3: Verify callback and ordering statically**

```powershell
git diff --check
rg -n "s\\.items|viewModel\\.buy\\(tariff\\.key\\)" app/src/main/java/com/maestrovpn/tv/compose/screen/purchase/BuyScreen.kt
```

- [ ] **Step 4: Commit**

```powershell
git add -- app/src/main/java/com/maestrovpn/tv/compose/screen/purchase/BuyScreen.kt app/src/androidTest/java/com/maestrovpn/tv/compose/premium/MobilePremiumFlowsTest.kt
git commit -m "feat(android): refresh premium tariff selection"
```

### Task 3: Phone payment and result states

**Files:**
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/purchase/BuyScreen.kt`
- Test: `app/src/androidTest/java/com/maestrovpn/tv/compose/premium/MobilePremiumFlowsTest.kt`
- Modify: `MAESTROVPN_UI_HANDOFF.md`

**Interfaces:**
- Consumes: all existing `BuyState` variants and callbacks.
- Produces: tagged phone payment surface `premium-payment` with unchanged QR/payment behavior.

- [ ] **Step 1: Write failing payment-state tests**

Assert the literal amount, order code, open-payment action, “Я оплатил”, waiting,
activating, done, and retry content for representative real state values.

- [ ] **Step 2: Implement premium state presentation**

Wrap phone states in premium panels/loading/error controls. Preserve amount → QR →
instructions → order code → manual phone → “Я оплатил” order and retain the white QR surface.

- [ ] **Step 3: Audit behavioral invariants**

```powershell
rg -n "viewModel\\.iPaid\\(\\)|viewModel\\.loadTariffs\\(\\)|ACTION_VIEW|payUrl|QRCodeGenerator" app/src/main/java/com/maestrovpn/tv/compose/screen/purchase/BuyScreen.kt
git diff --check
```

- [ ] **Step 4: Update handoff and commit**

Record changed phone screens, preserved behavior, and these CI commands:

```powershell
.\gradlew.bat :app:connectedOtherDebugAndroidTest `
  -Pandroid.testInstrumentationRunnerArguments.class=com.maestrovpn.tv.compose.premium.MobilePremiumFlowsTest
.\gradlew.bat :app:testOtherDebugUnitTest
.\gradlew.bat :app:assembleOtherDebug
```

Commit:

```powershell
git add -- app/src/main/java/com/maestrovpn/tv/compose/screen/purchase/BuyScreen.kt app/src/androidTest/java/com/maestrovpn/tv/compose/premium/MobilePremiumFlowsTest.kt MAESTROVPN_UI_HANDOFF.md
git commit -m "feat(android): finish premium subscription flow"
```
