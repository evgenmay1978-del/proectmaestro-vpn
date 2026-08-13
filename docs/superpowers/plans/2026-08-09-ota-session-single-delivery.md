# MaestroVPN OTA: single-delivery install confirmation implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Устранить подтверждённую гонку `Session files in use`, оставить один отменяемый путь ручного OTA и гарантировать повторное предложение той же версии после ошибки установки.

**Architecture:** Системный confirm-Intent доставляется взаимоисключающим способом: foreground-приложение запускает его немедленно, background-приложение только паркует его до `MainActivity.onResume()`. Ручная проверка mobile/TV больше не открывает второй Material download/install flow, а публикует `UpdateInfo` и принудительный prompt-request в существующий Compose OTA-flow. Постоянный `lastShownUpdateVersion` меняется только при явном отказе пользователя.

**Tech Stack:** Kotlin, Android `PackageInstaller`, Compose state, JUnit 4, GitHub Actions `android-test.yml`, production OTA verification scripts.

## Global Constraints

- Работать только в `codex/mobile-4d-deck`, которая является предком production `main`/`tv-v1.0.154`.
- Не менять `TvEskizHome.kt`, `TvEskizSpec.kt`, `tvm_*`, D-pad/focus/Back, TV-геометрию или TV-ассеты.
- Не запускать Gradle/APK локально на слабом компьютере владельца; RED и GREEN подтверждать GitHub Actions.
- Не выпускать следующий OTA, пока GitHub APK, panel manifest и публичное Yandex-зеркало не совпадут по versionCode, размеру и SHA-256.
- Не считать зелёный build доказательством исправленной установки: нужен новый возрастающий production versionCode и проверка обновления поверх `1.0.154`.

---

### Task 1: Одноразовая доставка системного подтверждения

**Files:**

- Create: `app/src/test/java/com/maestrovpn/tv/vendor/InstallConfirmationDeliveryTest.kt`
- Create: `app/src/github/java/com/maestrovpn/tv/vendor/InstallConfirmationDelivery.kt`
- Modify: `app/src/github/java/com/maestrovpn/tv/vendor/InstallResultReceiver.kt`

**Interfaces:**

- Produces: `deliverInstallConfirmation(confirmation, appInForeground, launch, park): InstallConfirmationDelivery`.
- Consumes: `AppLifecycleObserver.isForeground.value`, `Context.startActivity(Intent)`, `UpdateState.pendingConfirmIntent`.

- [ ] **Step 1: Write the failing test**

```kotlin
class InstallConfirmationDeliveryTest {
    @Test
    fun foregroundConfirmationIsLaunchedOnceAndNeverParked() {
        var launches = 0
        var parks = 0
        val result = deliverInstallConfirmation(
            confirmation = "confirm",
            appInForeground = true,
            launch = { launches++ },
            park = { parks++ },
        )
        assertEquals(InstallConfirmationDelivery.Launched, result)
        assertEquals(1, launches)
        assertEquals(0, parks)
    }

    @Test
    fun backgroundConfirmationIsParkedAndNeverLaunched() {
        var launches = 0
        var parks = 0
        val result = deliverInstallConfirmation(
            confirmation = "confirm",
            appInForeground = false,
            launch = { launches++ },
            park = { parks++ },
        )
        assertEquals(InstallConfirmationDelivery.Parked, result)
        assertEquals(0, launches)
        assertEquals(1, parks)
    }

    @Test
    fun failedForegroundLaunchFallsBackToOnePark() {
        var parks = 0
        val result = deliverInstallConfirmation(
            confirmation = "confirm",
            appInForeground = true,
            launch = { error("blocked") },
            park = { parks++ },
        )
        assertEquals(InstallConfirmationDelivery.Parked, result)
        assertEquals(1, parks)
    }
}
```

- [ ] **Step 2: Push RED and verify the exact failure in GitHub Actions**

Run through the repository helper:

```text
python ops/github-actions-artifact.py --task android
```

Expected: `:app:testOtherDebugUnitTest` fails because `deliverInstallConfirmation` and `InstallConfirmationDelivery` do not exist; APK compile may also fail for the same missing API. Record run ID and exact HEAD.

- [ ] **Step 3: Write the minimal delivery policy**

```kotlin
internal enum class InstallConfirmationDelivery { Launched, Parked }

internal inline fun <T> deliverInstallConfirmation(
    confirmation: T,
    appInForeground: Boolean,
    launch: (T) -> Unit,
    park: (T) -> Unit,
): InstallConfirmationDelivery {
    if (appInForeground) {
        val launched = runCatching { launch(confirmation) }.isSuccess
        if (launched) return InstallConfirmationDelivery.Launched
    }
    park(confirmation)
    return InstallConfirmationDelivery.Parked
}
```

In `InstallResultReceiver`, set `AwaitingConfirm`, then call the policy exactly once. Foreground uses `context.startActivity`; background or a thrown launch parks in `UpdateState`. Never both launch and park the same Intent.

- [ ] **Step 4: Verify Task 1 GREEN in GitHub Actions**

Expected: all three focused tests pass and `:app:assembleOtherDebug` compiles the receiver against the real Android APIs.

- [ ] **Step 5: Commit Task 1**

```text
git add app/src/github/java/com/maestrovpn/tv/vendor/InstallConfirmationDelivery.kt app/src/github/java/com/maestrovpn/tv/vendor/InstallResultReceiver.kt app/src/test/java/com/maestrovpn/tv/vendor/InstallConfirmationDeliveryTest.kt
git commit -m "fix(update): deliver install confirmation exactly once"
```

---

### Task 2: Единый ручной OTA-flow и повторный prompt после ошибки

**Files:**

- Create: `app/src/test/java/com/maestrovpn/tv/update/UpdatePromptPolicyTest.kt`
- Create: `app/src/main/java/com/maestrovpn/tv/update/UpdatePromptPolicy.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/update/UpdateState.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/MainActivity.kt`
- Modify: `app/src/other/java/com/maestrovpn/tv/vendor/Vendor.kt`

**Interfaces:**

- Produces: `UpdatePromptRequest(sequence: Long, versionCode: Int)`, `UpdateState.requestUpdatePrompt(info)`, `UpdateState.clearUpdatePrompt(sequence)`, `shouldShowUpdatePrompt(...)`, `lastShownVersionAfterPromptAction(...)`.
- Removes: private duplicate `Vendor.showUpdateDialog()` and `Vendor.startInPlaceInstall()`; `Vendor.checkUpdate()` retains no-updates/error dialogs but routes an available update into the Compose flow.

- [ ] **Step 1: Write the failing policy tests**

```kotlin
class UpdatePromptPolicyTest {
    @Test
    fun failedInstallKeepsSameVersionEligible() {
        assertEquals(
            153,
            lastShownVersionAfterPromptAction(153, 154, UpdatePromptAction.InstallFailed),
        )
        assertTrue(shouldShowUpdatePrompt(154, 153, requestedVersion = 154))
    }

    @Test
    fun explicitDeclineSuppressesOnlyThatVersion() {
        assertEquals(
            154,
            lastShownVersionAfterPromptAction(153, 154, UpdatePromptAction.Decline),
        )
        assertFalse(shouldShowUpdatePrompt(154, 154, requestedVersion = null))
    }

    @Test
    fun manualCheckCanReopenExplicitlyDeclinedVersion() {
        assertTrue(shouldShowUpdatePrompt(154, 154, requestedVersion = 154))
    }
}
```

- [ ] **Step 2: Push RED and record the expected GitHub failure**

Expected: unit test compile fails only because the new policy API is absent. Record run ID and exact HEAD.

- [ ] **Step 3: Implement minimal prompt policy and request state**

```kotlin
internal enum class UpdatePromptAction { Hide, Decline, InstallFailed }

internal fun lastShownVersionAfterPromptAction(
    current: Int,
    available: Int,
    action: UpdatePromptAction,
): Int = if (action == UpdatePromptAction.Decline) maxOf(current, available) else current

internal fun shouldShowUpdatePrompt(
    availableVersion: Int?,
    lastShownVersion: Int,
    requestedVersion: Int?,
): Boolean = availableVersion != null &&
    (availableVersion > lastShownVersion || requestedVersion == availableVersion)
```

`UpdateState.requestUpdatePrompt(info)` must publish the exact `UpdateInfo` and issue a monotonically increasing request sequence. `clearUpdatePrompt(sequence)` clears only the request the caller actually consumed.

- [ ] **Step 4: Route the manual Home action into the existing Compose dialog**

In `Vendor.checkUpdate`, replace the pre-install write to `Settings.lastShownUpdateVersion` and the duplicate Material install flow with:

```kotlin
activity.runOnUiThread {
    UpdateState.requestUpdatePrompt(updateInfo)
}
```

Keep `showNoUpdatesDialog` and `showTrackNotSupportedDialog`. Remove the now-unused app-lifetime coroutine scope, non-cancelable progress dialog and duplicate download call.

- [ ] **Step 5: Make MainActivity consume/reissue prompt requests**

Use `shouldShowUpdatePrompt` with the forced request version. Key local dialog visibility by `updateInfo.versionCode` plus request sequence. Persist `lastShownUpdateVersion` only through `UpdatePromptAction.Decline`. When the user closes an error result, call `UpdateState.requestUpdatePrompt(updateInfo)` so the same version is immediately eligible again; the existing cancel button must continue cancelling `downloadJob` and resetting `UpdateState`.

- [ ] **Step 6: Verify Task 2 GREEN in GitHub Actions**

Expected: policy tests pass, the Android job compiles, all existing unit tests pass, and a real APK artifact exists. Confirm `git diff` contains no TV-specific file or asset.

- [ ] **Step 7: Commit Task 2**

```text
git add app/src/main/java/com/maestrovpn/tv/update/UpdatePromptPolicy.kt app/src/main/java/com/maestrovpn/tv/update/UpdateState.kt app/src/main/java/com/maestrovpn/tv/compose/MainActivity.kt app/src/other/java/com/maestrovpn/tv/vendor/Vendor.kt app/src/test/java/com/maestrovpn/tv/update/UpdatePromptPolicyTest.kt
git commit -m "fix(update): reoffer failed manual updates"
```

---

### Task 3: Проверка, review и forward OTA

**Files:**

- Modify: `CONTEXT_HANDOFF.md`
- Modify only if behavior documentation is missing: `KNOWN_ISSUES.md` or `ops/README.md`

**Interfaces:**

- Consumes: exact GREEN branch SHA, GitHub test run/artifacts, production workflow, `ops/verify-ota.sh`.
- Produces: next monotonic release (at least VC155), GitHub Release APK, synchronized panel/Yandex manifests, durable handoff.

- [ ] **Step 1: Run final branch checks**

```text
git diff --check
git status --short --branch
git diff --name-only origin/main...HEAD
```

Require zero diff for `TvEskizHome.kt`, `TvEskizSpec.kt`, `tvm_*`, TV geometry/assets and `ops/tv-*`.

- [ ] **Step 2: Request independent code review**

Review must trace foreground/background/launch-failure branches, MainActivity one-shot consumption, explicit decline, install error, user cancellation, activity recreation and process death. Resolve every Critical/Important finding before merge.

- [ ] **Step 3: Merge only the reviewed branch and run production build**

Confirm production workflow uses stable signing, normal libbox from the exact release commit, package `com.maestrovpn.tv`, and a versionCode greater than 154. Record APK filename, bytes, SHA-256 and signing certificate SHA-256.

- [ ] **Step 4: Synchronize and verify the whole OTA chain before telling users**

```text
ops/verify-ota.sh --sync
ops/verify-ota.sh
```

Require GitHub, panel and `https://storage.yandexcloud.net/maestro-apk/update.json` to advertise the same version. Download the public Yandex APK and compare size/SHA-256 with the release asset. If S1/panel is unreachable or Yandex remains stale, stop: do not call the OTA ready.

- [ ] **Step 5: Verify in-place update and record the incident**

On a device running `1.0.154`, perform one update through the app. Acceptance: one download, one system confirmation, no `Session files in use`, failure/cancel keeps the offer recoverable, successful install advances version without data loss, mobile Home remains approved and TV-specific diff is zero.

- [x] **Step 6: Update permanent context**

Add a new top section to `CONTEXT_HANDOFF.md` with root cause, RED/GREEN run IDs, exact commit/release, artifact hashes, mirror state, unperformed device checks and the next safe step.
