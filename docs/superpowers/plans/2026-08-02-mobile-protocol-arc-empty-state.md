# Mobile Protocol Arc Empty-State Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep all seven owner-approved phone protocol labels registered on the carved arc when the runtime selector list is temporarily empty.

**Architecture:** Change only the pure phone helper `orderedHomeProtocols`. Empty input uses the existing immutable `HOME_PROTOCOL_ORDER`; every non-empty input keeps the current runtime-driven ordering and extra-tag behavior.

**Tech Stack:** Kotlin, JUnit4, Jetpack Compose consumer, GitHub Actions `android-test.yml`.

## Global Constraints

- Phone Home only; do not change TV, `GroupsViewModel`, VPN runtime, backend, workflows, Release or OTA.
- Preserve callbacks, selector tags, seven measured cell bounds and `WEBRTC` as display-only label for `olcrtc`.
- Do not run local Gradle or build an APK on the owner's computer.
- Use a CI-only draft PR for RED/GREEN and close it without merge.

---

### Task 1: Preserve the full arc for an empty runtime list

**Files:**
- Test: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeProtocolOrderTest.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeControlDeck.kt`

**Interfaces:**
- Consumes: `orderedHomeProtocols(protocols: List<String>): List<String>` and existing `HOME_PROTOCOL_ORDER`.
- Produces: the seven-tag approved order for empty input without changing non-empty behavior.

- [ ] **Step 1: Write the failing regression test**

Replace the old empty-input expectation with:

```kotlin
@Test
fun emptyRuntimeListKeepsEveryOwnerApprovedArcLabel() {
    assertEquals(
        listOf("auto", "vless", "hysteria2", "anytls", "naive", "vk-turn", "olcrtc"),
        orderedHomeProtocols(emptyList()),
    )
}
```

Keep the existing assertion that `olcrtc` appears exactly once when the backend already provides it.

- [ ] **Step 2: Verify RED on GitHub**

Commit and push the test-only change, open a CI-only draft PR against `main`, and wait for
`android-test.yml`. Expected: `PhoneHomeProtocolOrderTest.emptyRuntimeListKeepsEveryOwnerApprovedArcLabel`
fails because the actual value is `[olcrtc]`; build may pass, unit-test job must fail for this assertion.

- [ ] **Step 3: Write the minimal production implementation**

At the start of `orderedHomeProtocols`, add:

```kotlin
if (protocols.isEmpty()) return HOME_PROTOCOL_ORDER
```

Add one short comment tying the fallback to the cold-start selector gap. Do not change
`ProtocolArc`, `arcSectorCells`, callbacks, labels, assets or shared group loading.

- [ ] **Step 4: Verify GREEN and the APK artifact on GitHub**

Commit and push the production change. Require success for `:app:assembleOtherDebug`, APK upload,
and `:app:testOtherDebugUnitTest`. Fetch the artifact list and record the new artifact id, digest,
tested head SHA and expiry.

- [ ] **Step 5: Close and hand off**

Close the CI-only PR without merge. Run `git diff --check`, confirm the branch is clean/synced and
that code scope contains only the phone helper plus its JVM test. Update `CONTEXT_HANDOFF.md` with
the old-link correction, RED/GREEN evidence and exact new APK link, commit and push the docs.

## Self-Review

- The test reproduces the exact centered-WEBRTC screenshot path.
- The implementation is a single pure fallback and leaves all non-empty runtime behavior intact.
- No placeholder, shared-state refactor, TV change or release action is included.
