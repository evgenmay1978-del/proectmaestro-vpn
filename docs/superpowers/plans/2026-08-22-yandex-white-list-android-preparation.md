# Yandex White-List Android Preparation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Add a mobile-only optional white-list client projection and deterministic heartbeat recovery abstraction while keeping MaestroVPN 1.0.157, TV, ordinary VPN, and OTA behavior unchanged.

**Architecture:** A pure kotlinx.serialization parser produces a bounded mobile display model from an optional white_list object. A separate pure reducer owns watchdog timing and fallback decisions, while a thin dormant adapter shares the existing DefaultNetworkListener actor and never creates another VPN service.

**Tech Stack:** Kotlin, kotlinx.serialization JSON, Android VPNService lifecycle, DefaultNetworkListener, JUnit 4, Gradle testOtherDebugUnitTest.

## Global Constraints

- Production compatibility baseline: MaestroVPN 1.0.157.
- Keep the existing VPNService as the only Android VPN service.
- Keep DefaultNetworkListener as the single ConnectivityManager callback seam.
- Missing, unknown, malformed, or future API fields preserve current behavior.
- TV does not parse, display, or activate white-list runtime state.
- Ordinary VPN remains available during white-list suspension and fallback.
- Do not log tokens, subscription URLs, credentials, origin details, payloads, destinations, exception text, or response bodies.
- Do not modify AndroidManifest.xml, app resources/assets, TV composables, runtime feature gates, version properties, workflows, release metadata, or OTA.
- Do not start real heartbeat traffic in Task 6.
- Use RED then GREEN for every implementation task.
- Stage only the files named by each task; never stage normalize.patch or the pre-existing task-4-report.md bookkeeping change.

---

### Task 1: Mobile client contract and AccountInfo integration

**Files:**
- Create: app/src/main/java/com/maestrovpn/tv/whitelist/WhiteListClientInfo.kt
- Modify: app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/AccountInfo.kt
- Create: app/src/test/java/com/maestrovpn/tv/whitelist/WhiteListClientInfoTest.kt

**Interfaces:**
- Produces: WhiteListClientInfoParser.parseInfoResponse(raw: String, isTelevision: Boolean): WhiteListDisplayModel?
- Produces: WhiteListDisplayModel.runtimeEligible: Boolean
- Produces: AccountInfo.whiteList: WhiteListDisplayModel?
- Consumes: the existing raw GET /sub/<token>/info response and DeviceFormFactor.isTelevision(context)

- [ ] **Step 1: Write the failing parser and mobile-gate tests**

Create WhiteListClientInfoTest with these exact cases:

~~~kotlin
class WhiteListClientInfoTest {
    @Test
    fun absentFieldPreservesExistingBehavior() {
        assertNull(
            WhiteListClientInfoParser.parseInfoResponse(
                """{"login":"alice","future":{"ignored":true}}""",
                isTelevision = false,
            ),
        )
    }

    @Test
    fun activeMobileProjectionIsBoundedAndRuntimeEligible() {
        val model = WhiteListClientInfoParser.parseInfoResponse(
            """
            {
              "white_list": {
                "state": "ACTIVE",
                "transport_profile_id": "profile-a",
                "transport_release_id": "release-a",
                "preset": "MAESTRO_ADVANCED",
                "billing_state": "SHADOW",
                "usage_bytes": 1048576,
                "remaining_limit_bytes": 2097152,
                "suspension_reason": "",
                "edge_ids": ["edge-a", "edge-b"],
                "heartbeat_enabled": true,
                "future_field": "ignored"
              }
            }
            """.trimIndent(),
            isTelevision = false,
        )

        assertEquals(WhiteListState.ACTIVE, model?.state)
        assertEquals(listOf("edge-a", "edge-b"), model?.edgeIds)
        assertTrue(model?.runtimeEligible == true)
    }

    @Test
    fun televisionReturnsBeforeParsing() {
        assertNull(
            WhiteListClientInfoParser.parseInfoResponse(
                raw = "not-json-and-must-not-be-parsed",
                isTelevision = true,
            ),
        )
    }

    @Test
    fun malformedAndUnsafeValuesFailClosed() {
        val invalid = listOf(
            """{"white_list":{"state":"ACTIVE","usage_bytes":-1}}""",
            """{"white_list":{"state":"ACTIVE","edge_ids":["edge-a","edge-a"]}}""",
            """{"white_list":{"state":"ACTIVE","transport_profile_id":"bad/token"}}""",
            """{"white_list":{"state":"ACTIVE","suspension_reason":"line
break"}}""",
        )
        invalid.forEach {
            assertNull(WhiteListClientInfoParser.parseInfoResponse(it, isTelevision = false))
        }
    }

    @Test
    fun unknownStateCanDisplayButCannotActivateRuntime() {
        val model = WhiteListClientInfoParser.parseInfoResponse(
            """{"white_list":{"state":"FUTURE","heartbeat_enabled":true,"edge_ids":["edge-a"]}}""",
            isTelevision = false,
        )
        assertEquals(WhiteListState.UNKNOWN, model?.state)
        assertFalse(model?.runtimeEligible == true)
    }
}
~~~

- [ ] **Step 2: Run the focused test and record RED**

Run:

~~~powershell
./gradlew.bat :app:testOtherDebugUnitTest --tests "com.maestrovpn.tv.whitelist.WhiteListClientInfoTest" --no-daemon
~~~

Expected: FAIL because WhiteListClientInfoParser, WhiteListDisplayModel, and WhiteListState do not exist.

- [ ] **Step 3: Implement the bounded parser and display model**

Create these public-in-module types and keep parsing pure:

~~~kotlin
enum class WhiteListState {
    DISABLED, PROVISIONING, ACTIVE, GRACE, SUSPENDED, ERROR, EXPIRED, UNKNOWN
}

enum class WhiteListBillingState {
    OFF, SHADOW, REAL, FREE, UNKNOWN
}

data class WhiteListDisplayModel(
    val state: WhiteListState,
    val transportProfileId: String?,
    val transportReleaseId: String?,
    val preset: String?,
    val billingState: WhiteListBillingState,
    val usageBytes: Long?,
    val remainingLimitBytes: Long?,
    val suspensionReason: String?,
    val edgeIds: List<String>,
    val heartbeatEnabled: Boolean,
) {
    val runtimeEligible: Boolean
        get() = state in setOf(WhiteListState.ACTIVE, WhiteListState.GRACE) &&
            heartbeatEnabled &&
            transportProfileId != null &&
            transportReleaseId != null &&
            preset != null &&
            edgeIds.isNotEmpty()
}

object WhiteListClientInfoParser {
    fun parseInfoResponse(raw: String, isTelevision: Boolean): WhiteListDisplayModel? {
        if (isTelevision) return null
        return runCatching { parseMobile(raw) }.getOrNull()
    }
}
~~~

parseMobile must use Json.parseToJsonElement, require a JSON object named white_list, ignore unknown keys, and enforce:

- opaque IDs match ^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$
- preset has the same bound
- edge_ids has at most 16 unique opaque IDs
- byte values are absent or non-negative Long values
- suspension_reason is absent/blank or 1 to 160 printable characters without CR, LF, NUL, or other controls
- heartbeat_enabled defaults to false
- missing state is invalid
- unknown state and billing values map to UNKNOWN
- ACTIVE or GRACE may parse incomplete data for display, but runtimeEligible remains false

Modify rememberAccountInfo so it obtains LocalContext.current before produceState, computes isTelevision with DeviceFormFactor, includes that Boolean in the produceState keys, and sets:

~~~kotlin
whiteList = WhiteListClientInfoParser.parseInfoResponse(
    raw = json,
    isTelevision = isTelevision,
)
~~~

Do not pass the model into TvHomeScreen and do not modify any TV or resource file.

- [ ] **Step 4: Run focused GREEN and existing subscription helper tests**

Run:

~~~powershell
./gradlew.bat :app:testOtherDebugUnitTest --tests "com.maestrovpn.tv.whitelist.WhiteListClientInfoTest" --tests "com.maestrovpn.tv.utils.MaestroSubTest" --no-daemon
~~~

Expected: PASS.

- [ ] **Step 5: Format, inspect, stage, and commit only Task 1 files**

Run ktlint only if the repository already provides a configured task; otherwise preserve the existing Kotlin formatting style. Then run git diff --check and inspect the three files.

Commit:

~~~powershell
git add -- app/src/main/java/com/maestrovpn/tv/whitelist/WhiteListClientInfo.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/AccountInfo.kt app/src/test/java/com/maestrovpn/tv/whitelist/WhiteListClientInfoTest.kt
git commit -m "feat(android): parse mobile white-list client state"
~~~

---

### Task 2: Deterministic watchdog reducer and safe telemetry

**Files:**
- Create: app/src/main/java/com/maestrovpn/tv/whitelist/WhiteListWatchdog.kt
- Create: app/src/test/java/com/maestrovpn/tv/whitelist/WhiteListWatchdogTest.kt

**Interfaces:**
- Consumes: WhiteListDisplayModel.runtimeEligible and edgeIds.size
- Produces: WhiteListWatchdog.reduce(snapshot, event, jitterMillis): WhiteListWatchdogResult
- Produces: WhiteListLogEvent.renderSafe(): String
- Produces typed actions only; no Android, network, clock, coroutine, URL, token, Throwable, or arbitrary-message dependency

- [ ] **Step 1: Write failing transition, fallback, and log-redaction tests**

Create tests for the exact behavior below:

~~~kotlin
@Test
fun successSchedulesJitteredHeartbeat() {
    val started = watchdog.reduce(WatchdogSnapshot.stopped(), WatchdogEvent.Start(edgeCount = 2))
    val online = watchdog.reduce(started.snapshot, WatchdogEvent.NetworkAvailable(epoch = 1), jitterMillis = 500)
    val probing = watchdog.reduce(online.snapshot, WatchdogEvent.Timer)
    val healthy = watchdog.reduce(probing.snapshot, WatchdogEvent.ProbeSucceeded, jitterMillis = 30_000)

    assertEquals(listOf(WatchdogAction.Schedule(30_000)), healthy.actions)
    assertEquals(0, healthy.snapshot.failures)
}

@Test
fun replacementNetworkClearsAndRedialsOnce() {
    val snapshot = WatchdogSnapshot(
        state = WatchdogState.SCHEDULED,
        edgeIndex = 0,
        edgeCount = 2,
        failures = 0,
        networkEpoch = 1,
    )
    val result = watchdog.reduce(snapshot, WatchdogEvent.NetworkAvailable(epoch = 2), jitterMillis = 700)

    assertEquals(
        listOf(
            WatchdogAction.CancelProbe,
            WatchdogAction.ClearSession,
            WatchdogAction.ControlledRedial(edgeIndex = 0),
            WatchdogAction.Schedule(700),
        ),
        result.actions,
    )
}

@Test
fun failuresAreBoundedThenFallbackToOrdinaryVpn() {
    var snapshot = onlineSnapshot(edgeCount = 1)
    repeat(5) {
        snapshot = watchdog.reduce(probing(snapshot), WatchdogEvent.ProbeFailed).snapshot
    }
    val final = watchdog.reduce(probing(snapshot), WatchdogEvent.ProbeFailed)

    assertEquals(WatchdogState.FALLBACK, final.snapshot.state)
    assertTrue(final.actions.contains(WatchdogAction.FallbackOrdinaryVpn))
}

@Test
fun safeLogHasNoArbitrarySecretChannel() {
    val rendered = WhiteListLogEvent(
        code = WhiteListLogCode.PROBE_FAILED,
        state = WatchdogState.BACKING_OFF,
        failureCount = 2,
        edgeOrdinal = 1,
    ).renderSafe()

    listOf("token-secret", "https://secret/sub/token", "credential-secret", "exception-secret")
        .forEach { assertFalse(rendered.contains(it)) }
}
~~~

Also cover NetworkLost, Wake, Stop, ordered edge advancement, duplicate NetworkAvailable for the same epoch, and backoff values 5, 10, 20, 40, 80, and capped 120 seconds.

- [ ] **Step 2: Run the focused test and record RED**

Run:

~~~powershell
./gradlew.bat :app:testOtherDebugUnitTest --tests "com.maestrovpn.tv.whitelist.WhiteListWatchdogTest" --no-daemon
~~~

Expected: FAIL because watchdog types do not exist.

- [ ] **Step 3: Implement the pure reducer**

Define these exact types:

~~~kotlin
data class WhiteListWatchdogPolicy(
    val intervalMinMillis: Long = 25_000,
    val intervalMaxMillis: Long = 35_000,
    val probeTimeoutMillis: Long = 9_000,
    val baseBackoffMillis: Long = 5_000,
    val maxBackoffMillis: Long = 120_000,
    val redialAfterFailures: Int = 3,
    val advanceEdgeAfterFailures: Int = 6,
)

enum class WatchdogState {
    STOPPED, WAITING_NETWORK, SCHEDULED, PROBING, BACKING_OFF, FALLBACK
}

data class WatchdogSnapshot(
    val state: WatchdogState,
    val edgeIndex: Int,
    val edgeCount: Int,
    val failures: Int,
    val networkEpoch: Long?,
) {
    companion object {
        fun stopped() = WatchdogSnapshot(WatchdogState.STOPPED, 0, 0, 0, null)
    }
}

sealed interface WatchdogEvent {
    data class Start(val edgeCount: Int) : WatchdogEvent
    data class NetworkAvailable(val epoch: Long) : WatchdogEvent
    data object NetworkLost : WatchdogEvent
    data object Timer : WatchdogEvent
    data object ProbeSucceeded : WatchdogEvent
    data object ProbeFailed : WatchdogEvent
    data object Wake : WatchdogEvent
    data object Stop : WatchdogEvent
}

sealed interface WatchdogAction {
    data class Schedule(val delayMillis: Long) : WatchdogAction
    data class Probe(val timeoutMillis: Long) : WatchdogAction
    data class ControlledRedial(val edgeIndex: Int) : WatchdogAction
    data object CancelProbe : WatchdogAction
    data object ClearSession : WatchdogAction
    data object FallbackOrdinaryVpn : WatchdogAction
}

data class WhiteListWatchdogResult(
    val snapshot: WatchdogSnapshot,
    val actions: List<WatchdogAction>,
)
~~~

Reducer rules:

- Start with edgeCount less than 1 enters FALLBACK and emits FallbackOrdinaryVpn; otherwise WAITING_NETWORK.
- First NetworkAvailable schedules a clamped 0 to 2 second startup jitter without redial.
- A different non-null network epoch emits CancelProbe, ClearSession, one ControlledRedial, and a clamped 0 to 2 second Schedule.
- Same-epoch NetworkAvailable is idempotent.
- NetworkLost emits CancelProbe and WAITING_NETWORK, without redial.
- Timer from SCHEDULED or BACKING_OFF emits Probe(9_000) and enters PROBING.
- ProbeSucceeded resets failures and schedules a validated 25 to 35 second jitter.
- ProbeFailed uses min(120_000, 5_000 shifted by failures minus one).
- Failure 3 emits ClearSession and ControlledRedial on the current edge.
- Failure 6 advances to the next edge, resets the per-edge failure counter, clears, redials, and schedules backoff.
- If failure 6 occurs on the last edge, enter FALLBACK and emit CancelProbe, ClearSession, and FallbackOrdinaryVpn.
- Wake schedules a clamped 0 to 2 second probe only when a network exists.
- Stop emits CancelProbe and ClearSession and returns stopped().
- Invalid policy, snapshot, event order, or jitter throws IllegalArgumentException during tests; callers will own fail-closed containment.

WhiteListLogEvent must accept only enums and integers and render a fixed key-value line.

- [ ] **Step 4: Run focused GREEN**

Run:

~~~powershell
./gradlew.bat :app:testOtherDebugUnitTest --tests "com.maestrovpn.tv.whitelist.WhiteListWatchdogTest" --no-daemon
~~~

Expected: PASS.

- [ ] **Step 5: Inspect, stage, and commit only Task 2 files**

Run git diff --check and inspect both files.

Commit:

~~~powershell
git add -- app/src/main/java/com/maestrovpn/tv/whitelist/WhiteListWatchdog.kt app/src/test/java/com/maestrovpn/tv/whitelist/WhiteListWatchdogTest.kt
git commit -m "feat(android): add white-list watchdog reducer"
~~~

---

### Task 3: Shared DefaultNetworkListener adapter and Android boundary tests

**Files:**
- Create: app/src/main/java/com/maestrovpn/tv/bg/WhiteListNetworkBinding.kt
- Create: app/src/main/java/com/maestrovpn/tv/whitelist/WhiteListRuntimeGate.kt
- Create: app/src/test/java/com/maestrovpn/tv/bg/WhiteListAndroidBoundaryTest.kt
- Create: app/src/test/java/com/maestrovpn/tv/whitelist/WhiteListRuntimeGateTest.kt

**Interfaces:**
- Consumes: DefaultNetworkListener.start(key, listener) and DefaultNetworkListener.stop(key)
- Consumes: WhiteListDisplayModel.runtimeEligible
- Produces: WhiteListNetworkBinding.start(listener: (Network?) -> Unit) and stop()
- Produces: WhiteListRuntimeGate.enabled(isTelevision: Boolean, model: WhiteListDisplayModel?): Boolean
- Does not modify or instantiate VPNService, ConnectivityManager callbacks, services, manifests, or UI

- [ ] **Step 1: Write failing runtime-gate and source-boundary tests**

Runtime gate:

~~~kotlin
@Test
fun tvAndAbsentPolicyNeverEnableRuntime() {
    assertFalse(WhiteListRuntimeGate.enabled(isTelevision = true, model = activeModel()))
    assertFalse(WhiteListRuntimeGate.enabled(isTelevision = false, model = null))
}

@Test
fun onlyEligibleMobilePolicyEnablesRuntime() {
    assertTrue(WhiteListRuntimeGate.enabled(isTelevision = false, model = activeModel()))
    assertFalse(WhiteListRuntimeGate.enabled(isTelevision = false, model = suspendedModel()))
}
~~~

Source boundary:

~~~kotlin
@Test
fun bindingSharesExistingListenerAndCreatesNoVpnService() {
    val binding = appFile("src/main/java/com/maestrovpn/tv/bg/WhiteListNetworkBinding.kt").readText()
    val manifest = appFile("src/main/AndroidManifest.xml").readText()

    assertTrue(binding.contains("DefaultNetworkListener.start"))
    assertTrue(binding.contains("DefaultNetworkListener.stop"))
    assertFalse(binding.contains("registerNetworkCallback"))
    assertFalse(binding.contains("requestNetwork"))
    assertFalse(binding.contains("VpnService()"))
    assertEquals(1, Regex("android\\.net\\.VpnService").findAll(manifest).count())
}
~~~

appFile must check File(path) and File("app", path) and require exactly one existing result.

- [ ] **Step 2: Run the focused tests and record RED**

Run:

~~~powershell
./gradlew.bat :app:testOtherDebugUnitTest --tests "com.maestrovpn.tv.whitelist.WhiteListRuntimeGateTest" --tests "com.maestrovpn.tv.bg.WhiteListAndroidBoundaryTest" --no-daemon
~~~

Expected: FAIL because WhiteListRuntimeGate and WhiteListNetworkBinding do not exist.

- [ ] **Step 3: Implement the dormant adapter and gate**

Use:

~~~kotlin
internal class WhiteListNetworkBinding(
    private val key: Any = Any(),
) {
    private var started = false

    suspend fun start(listener: (Network?) -> Unit) {
        check(!started)
        DefaultNetworkListener.start(key, listener)
        started = true
    }

    suspend fun stop() {
        if (!started) return
        DefaultNetworkListener.stop(key)
        started = false
    }
}

object WhiteListRuntimeGate {
    fun enabled(isTelevision: Boolean, model: WhiteListDisplayModel?): Boolean =
        !isTelevision && model?.runtimeEligible == true
}
~~~

Do not reference WhiteListNetworkBinding from VPNService or BoxService in Task 6. The class is a tested integration seam for Task 7 activation after routing and heartbeat endpoint evidence.

- [ ] **Step 4: Run focused GREEN and the full existing unit suite**

Run:

~~~powershell
./gradlew.bat :app:testOtherDebugUnitTest --tests "com.maestrovpn.tv.whitelist.*" --tests "com.maestrovpn.tv.bg.WhiteListAndroidBoundaryTest" --no-daemon
./gradlew.bat :app:testOtherDebugUnitTest --no-daemon
~~~

Expected: PASS for both commands.

- [ ] **Step 5: Inspect, stage, and commit only Task 3 files**

Run git diff --check and inspect all four files.

Commit:

~~~powershell
git add -- app/src/main/java/com/maestrovpn/tv/bg/WhiteListNetworkBinding.kt app/src/main/java/com/maestrovpn/tv/whitelist/WhiteListRuntimeGate.kt app/src/test/java/com/maestrovpn/tv/bg/WhiteListAndroidBoundaryTest.kt app/src/test/java/com/maestrovpn/tv/whitelist/WhiteListRuntimeGateTest.kt
git commit -m "feat(android): add shared white-list network seam"
~~~

---

### Task 4: Task 6 verification, review, and handoff evidence

**Files:**
- Create: .superpowers/sdd/2026-08-20-yandex-cdn-whitelist/task-6-report.md
- Modify: .superpowers/sdd/2026-08-20-yandex-cdn-whitelist/progress.md
- Do not stage either ignored SDD artifact

**Interfaces:**
- Consumes: all Task 6 commits and test output
- Produces: exact commit range, RED and GREEN evidence, scope audit, and independent review verdict for Task 7

- [ ] **Step 1: Run focused and full verification**

Run:

~~~powershell
./gradlew.bat :app:testOtherDebugUnitTest --tests "com.maestrovpn.tv.whitelist.*" --tests "com.maestrovpn.tv.bg.WhiteListAndroidBoundaryTest" --no-daemon
./gradlew.bat :app:testOtherDebugUnitTest --no-daemon
git diff --check
~~~

Expected: all commands exit 0.

- [ ] **Step 2: Prove the forbidden scope is unchanged**

Inspect the Task 6 implementation range and require no changes under:

- app/src/main/AndroidManifest.xml
- app/src/main/res
- app/src/main/assets
- app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/TvHomeScreen.kt
- app/build.gradle.kts
- version.properties
- .github/workflows
- update or OTA source sets

Require exactly one existing VpnService manifest action and no new Android Service declaration.

- [ ] **Step 3: Write the ignored task report**

Record:

- design SHA bbb22b8
- every implementation SHA
- exact RED failure names
- exact GREEN commands and outcomes
- production version 1.0.157 compatibility statement
- no live heartbeat, OTA, server, billing, or production mutation
- rollback point bbb22b8
- the complete scoped file list

- [ ] **Step 4: Request independent specification and security review**

The reviewer must verify:

- optional-field backward compatibility
- TV parse/display/runtime gate is always off
- parser bounds and fail-closed behavior
- no secret-capable log channel
- deterministic network-change, retry, edge-failover, and fallback transitions
- single VPNService and shared DefaultNetworkListener
- no forbidden-scope changes
- no live activation or OTA

Fix every Critical or Important finding with a new RED to GREEN commit and repeat review, up to five rounds.

- [ ] **Step 5: Complete the SDD ledger**

After a clean review, append:

Task 6: complete (commits bbb22b8..<final-sha>, review clean)

Do not push or publish OTA in Task 6. Transition to Task 7 integration evidence and test APK gates.