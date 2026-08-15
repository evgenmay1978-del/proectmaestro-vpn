# WDTT Provider Bridge Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make mobile WDTT establish the complete `phone -> VK TURN -> S1 -> Internet` path or expose one safe, exact failure stage, while retaining the last known-good VK room in the Maestro panel.

**Architecture:** Keep upstream WDTT commit `1ff024899a577cb5db4691e526614619bf5a06a3` immutable and apply a repository-owned, checksum-bound patch series in GitHub Actions. Add a typed native-to-Kotlin event/control boundary, a fail-closed mobile CAPTCHA activity, and a backend candidate/active/last-known-good room state whose candidate is promoted only after the pinned Linux probe succeeds.

**Tech Stack:** Go, Kotlin/JVM, Android WebView/Compose host activity, Go backend, GitHub Actions, Python unittest policy checks.

## Global Constraints

- Exact upstream client base: `1ff024899a577cb5db4691e526614619bf5a06a3`.
- Production server remains on its current reviewed server revision during client validation.
- Universal APK remains; TV source, TV resources, TV assets, D-pad/focus/Back and TV runtime are read-only and WDTT-free.
- TLS verification must remain enabled; no `InsecureSkipVerify`, insecure environment switch, certificate-error proceed, cookie/session export or browser-profile scraping.
- Existing owner allowlist, WDTT passwords, WireGuard keys and blank-secret-preservation behavior remain unchanged.
- No customer expiry, payment, bot, DNS, other-protocol, SSH-authentication, Release or OTA mutation.
- No room hashes, provider URLs, tokens, private subscription URLs, credentials or customer identifiers in Git, CI output, panel JSON, logs or handoffs.
- Heavy Go/Android builds run in GitHub Actions because the owner's computer is weak.
- Every production code change follows RED -> exact failing evidence -> GREEN -> exact passing evidence.

## File and interface map

- `third_party/wdtt/series`: ordered list of WDTT patches.
- `third_party/wdtt/patches/0001-provider-bridge-tests.patch`: upstream Go tests added before production code.
- `third_party/wdtt/patches/0002-provider-bridge-runtime.patch`: verified roots, safe stage events, probe mode and READY ordering.
- `third_party/wdtt/certs/cacert.pem`: pinned official curl/Mozilla CA bundle.
- `third_party/wdtt/SHA256SUMS`: digests for patches and CA bundle.
- `ops/test_wdtt_patchset.py`: executable policy and synthetic-apply regression test.
- `app/src/main/java/com/maestrovpn/tv/bg/WdttEventParser.kt`: pure event parser and safe public states.
- `app/src/main/java/com/maestrovpn/tv/bg/WdttStartPolicy.kt`: pure state-aware start/deadline decisions.
- `app/src/main/java/com/maestrovpn/tv/bg/WdttManager.kt`: process lifecycle, persistent synchronized stdin, state flow and CAPTCHA handoff.
- `app/src/main/java/com/maestrovpn/tv/compose/wdtt/WdttCaptchaPolicy.kt`: exact HTTPS/top-level-host/result validation.
- `app/src/main/java/com/maestrovpn/tv/compose/wdtt/WdttCaptchaActivity.kt`: mobile-only restricted WebView.
- `backend/internal/vkturnconf/vkturnconf.go`: durable candidate/active/last-known-good state and atomic transitions.
- `backend/internal/vkturnprobe/runner.go`: bounded subprocess probe using stdin and redacted output.
- `backend/internal/api/vkturn_panel.go`: authenticated candidate submission and redacted status.
- `backend/internal/api/panel_ui.go`: candidate/status UI; secrets remain write-only.
- `.github/workflows/wdtt-bin.yml`: exact base, patch application, tests, both ABIs and provenance.
- `.github/workflows/android-test.yml`: exact patched artifact consumption and mobile test APK.

---

### Task 1: Native patch boundary — RED

**Files:**
- Create: `ops/test_wdtt_patchset.py`
- Create: `third_party/wdtt/series`
- Create: `third_party/wdtt/patches/0001-provider-bridge-tests.patch`
- Create: `third_party/wdtt/certs/cacert.pem`
- Create: `third_party/wdtt/SHA256SUMS`
- Modify: `.github/workflows/wdtt-bin.yml`

**Interfaces:**
- Consumes: exact `WDTT_REF` from `version.properties`.
- Produces: an ordered patch series which applies only to that exact revision and fails while the production symbols are absent.

- [ ] Write `ops/test_wdtt_patchset.py` so a temporary exact-base checkout verifies every digest, runs `git apply --check` and applies each listed patch once; reject missing files, duplicate series entries, `InsecureSkipVerify`, `VKTURN_INSECURE_TLS`, `handler.proceed()` and non-exact base revisions.
- [ ] In `0001-provider-bridge-tests.patch`, add upstream Go tests whose hand-derived expectations require:
  `verifiedRootPool(systemPool, embeddedPEM)` to retain system roots and append the embedded bundle; an invalid CA to fail; `emitStage`/`emitSafeError` to omit supplied secret markers; `runProviderProbe` to return only `{ok,stage,code}`; and READY to be emitted only after a worker is registered.
- [ ] Make `wdtt-bin.yml` verify `git rev-parse HEAD`, `SHA256SUMS`, series order and `git apply --check` before applying each patch, then run `go test ./...` before any artifact build.
- [ ] Run `python -m unittest -v ops/test_wdtt_patchset.py`; expected local PASS for patch mechanics.
- [ ] Commit/push tests only as `test(wdtt): require verified provider bridge`.
- [ ] Dispatch `wdtt-bin.yml` on the exact RED SHA; expected failure is only missing production symbols/behavior from `0002-provider-bridge-runtime.patch`. Record run/job IDs.

### Task 2: Native verified roots, stages, probe and READY — GREEN

**Files:**
- Create: `third_party/wdtt/patches/0002-provider-bridge-runtime.patch`
- Modify: `third_party/wdtt/SHA256SUMS`
- Test: upstream files introduced by `0001-provider-bridge-tests.patch`

**Interfaces:**
- Produces native lines `__WDTT_EVENT__|STAGE|{"stage":"DNS"}` and fixed safe errors, plus `__WDTT_PROBE__|{"ok":true,"stage":"TURN_ALLOCATED","code":"OK"}`.
- Probe request is one stdin JSON document `{"vk_hash":"<value>"}`; the hash never appears in argv or output.

- [ ] Implement `verifiedRootPool` using `x509.SystemCertPool()` when available and appending the pinned curl/Mozilla PEM; return an error if the embedded bundle cannot be parsed.
- [ ] Pass the pool to every `tlsclient.NewHttpClient` path in `creds_vkcalls.go`, `vk_auth.go` and captcha HTTP code via `tlsclient.WithTransportOptions`; add no insecure override.
- [ ] Add fixed stages `STARTING`, `DNS`, `TLS`, `VK_AUTH`, `CAPTCHA_REQUIRED`, `TURN_ALLOCATED`, `DTLS`, `WRAP`, `WIREGUARD`, `READY`, `FAILED`, `STOPPED`. Map errors to fixed codes without wrapping provider bodies or credentials into structured events.
- [ ] Add `-provider-probe-stdin`; validate a single safe hash, perform VKCalls/TURN allocation only, require a usable relay address, emit one redacted result and exit without binding the local relay or contacting S1.
- [ ] Move READY emission until after `Dispatcher.Register(slot)` and confirm the process still owns a live worker.
- [ ] Run the patch policy locally and the exact-SHA `wdtt-bin.yml`; expected GREEN includes upstream Go tests, both Android ABIs, Linux probe client, checksums and provenance.
- [ ] Commit/push as `fix(wdtt): add verified provider bridge`.

### Task 3: Kotlin event parser and state-aware deadline — RED/GREEN

**Files:**
- Create: `app/src/main/java/com/maestrovpn/tv/bg/WdttEventParser.kt`
- Create: `app/src/main/java/com/maestrovpn/tv/bg/WdttStartPolicy.kt`
- Create: `app/src/test/java/com/maestrovpn/tv/bg/WdttEventParserTest.kt`
- Create: `app/src/test/java/com/maestrovpn/tv/bg/WdttStartPolicyTest.kt`

**Interfaces:**
- `internal fun parseWdttEvent(line: String): WdttEvent?`
- `internal data class WdttCaptchaRequest(val mode: String, val redirectUri: String, val sessionToken: String)`
- `internal fun nextWdttStartDecision(state, childAlive, nowMs, ordinaryDeadlineMs, captchaDeadlineMs): WdttStartDecision`

- [ ] Write JVM tests first for every typed stage, exact legacy `CAPTCHA_SOLVE` parsing, malformed/unknown input, fatal error mapping, secret-marker redaction and the rule that unknown input can never yield READY.
- [ ] Write JVM tests first for ordinary deadline, paused CAPTCHA deadline, child exit, cancellation, fatal failure and READY-with-live-child.
- [ ] Push the RED tests and use `android-test.yml` exact-SHA evidence; failure must name the absent parser/policy APIs.
- [ ] Implement only the pure parser and decision policy. Structured payloads are accepted only under the exact prefix; legacy CAPTCHA uses `split('|', limit = 4)` and validated HTTPS input.
- [ ] Run focused tests, then the full Android unit-test/compile gate in GitHub. Commit GREEN as `feat(wdtt): parse provider bridge states`.

### Task 4: Process control and secure mobile CAPTCHA bridge — RED/GREEN

**Files:**
- Modify: `app/src/main/java/com/maestrovpn/tv/bg/WdttManager.kt`
- Create: `app/src/main/java/com/maestrovpn/tv/compose/wdtt/WdttCaptchaPolicy.kt`
- Create: `app/src/main/java/com/maestrovpn/tv/compose/wdtt/WdttCaptchaActivity.kt`
- Modify: `app/src/main/AndroidManifest.xml`
- Modify: `app/src/test/java/com/maestrovpn/tv/bg/WdttManagerTest.kt`
- Create: `app/src/test/java/com/maestrovpn/tv/compose/wdtt/WdttCaptchaPolicyTest.kt`

**Interfaces:**
- `WdttManager.state: StateFlow<WdttPublicState>` exposes stage and fixed safe error only.
- `WdttManager.submitCaptchaResult(requestId: Long, result: WdttCaptchaResult): Boolean` writes exactly one matching result.
- `WdttCaptchaPolicy.isAllowedTopLevel(uri)` permits HTTPS VK identity hosts only; `sanitizeSuccessToken` rejects blank/control/oversized values.

- [ ] Add failing tests for one persistent writer, serialized STOP/CAPTCHA commands, stale request IDs, cancel/timeout result values, early process exit and TV spawn denial. Use a narrow fake process boundary; assertions target manager outcomes and written bytes, not mock existence.
- [ ] Add failing policy tests for HTTP, userinfo, ports, fragments, encoded-host tricks, wrong hosts, subdomain confusion, control characters and oversized success tokens.
- [ ] Replace the 30-second latch with the pure state-aware decision loop; retain one synchronized writer for the child lifetime; close/reap on every terminal path; never log raw lines carrying URLs/tokens.
- [ ] Launch `WdttCaptchaActivity` only from an exact parsed request and only when `DeviceFormFactor.isTelevision` is false. The activity blocks file/content access, mixed content, downloads, popups and non-allowlisted top-level navigation; SSL errors always cancel; cookies are not exported or logged.
- [ ] Return only the success token, `error:cancelled` or `error:timeout`; destroy the WebView and clear the pending request on every exit.
- [ ] Prove RED then GREEN in exact-SHA Android CI. Commit as `fix(android): bridge WDTT captcha safely`.

### Task 5: Selection, errors and watchdog behavior

**Files:**
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/dashboard/groups/GroupsViewModel.kt`
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/dashboard/groups/GroupsViewModelTest.kt` if present; otherwise create `WdttSelectionPolicyTest.kt` beside the ViewModel tests.

**Interfaces:**
- Consumes `WdttManager.state` and `ensureStarted()`.
- Produces one user-safe Russian error mapped from the terminal fixed code; ordinary selectors remain unchanged.

- [ ] Write a failing test proving CAPTCHA_REQUIRED is not treated as a failed 30-second start and that FAILED prevents `selectOutbound`.
- [ ] Remove the second blind warmup retry while a CAPTCHA request is active; keep bounded prewarm only before the first child start.
- [ ] Watchdog restart is allowed only after a previously READY child dies; pre-READY failures surface their exact safe stage and do not loop.
- [ ] Run Android focused/full CI and verify no files under TV-only paths changed. Commit as `fix(wdtt): make selection stage aware`.

### Task 6: Candidate, active and last-known-good room state

**Files:**
- Modify: `backend/internal/vkturnconf/vkturnconf.go`
- Modify: `backend/internal/vkturnconf/vkturnconf_store_test.go`

**Interfaces:**
- `type ProbeStatus string` with `active`, `checking`, `failed`.
- `Store.StageCandidate(hashes []string, startedAt time.Time) error`
- `Store.PromoteCandidate(checkedAt time.Time) error`
- `Store.RejectCandidate(code string, checkedAt time.Time) error`
- `Config.VKHashes` remains the active list consumed by installed clients.

- [ ] Write failing table tests for candidate isolation, atomic promotion, rejection retaining active, maximum four unique hashes, persisted reload, concurrent stage/promotion serialization and legacy JSON defaulting to active.
- [ ] Extend the persisted schema with candidate hashes, last-known-good hashes, status, checked timestamp and fixed error code while keeping existing `vk_hashes` as the backward-compatible active field.
- [ ] Implement all transitions inside one store write lock and one 0600 temp+rename transaction. No failed candidate may alter `ClientFor` or subscription output.
- [ ] Run focused backend tests, `go test ./...`, race and vet in GitHub. Commit as `feat(wdtt): preserve last known good room`.

### Task 7: Pinned probe runner, panel API and UI

**Files:**
- Create: `backend/internal/vkturnprobe/runner.go`
- Create: `backend/internal/vkturnprobe/runner_test.go`
- Modify: `backend/internal/api/api.go`
- Modify: `backend/internal/api/panel.go`
- Modify: `backend/internal/api/vkturn_panel.go`
- Modify: `backend/internal/api/vkturn_panel_test.go`
- Modify: `backend/internal/api/panel_ui.go`
- Modify: `backend/cmd/maestro-panel/main.go`

**Interfaces:**
- `type Prober interface { Probe(context.Context, []string) Result }`
- `type Result struct { OK bool; Stage string; Code string }`
- `POST <panel>/api/vkturn/candidate` accepts full VK invite(s) or bare hashes; returns only redacted status.

- [ ] Write failing runner tests using a helper subprocess: exact stdin document, hard timeout, output size limit, malformed output, nonzero exit and secret-marker rejection.
- [ ] Write failing API tests for auth/CSRF, full-link normalization, candidate isolation during probe, success promotion, failure rollback, one concurrent probe lease and redacted GET response.
- [ ] Implement `Runner` with no hash in argv/environment, bounded stdin/stdout, fixed result validation and no cause wrapping into panel responses.
- [ ] Wire `MAESTRO_VKTURN_PROBE_BIN`; empty/missing runner fails candidate checks closed and keeps active configuration.
- [ ] Change the WDTT panel form so the room control submits a candidate, displays `checking/active/failed/last-known-good`, and never claims “saved” until promotion. Secret fields and the enable switch retain current behavior.
- [ ] Run focused/full backend, race, vet and panel HTTP tests. Commit as `feat(panel): verify WDTT rooms before promotion`.

### Task 8: Exact artifact provenance and candidate APK

**Files:**
- Modify: `.github/workflows/wdtt-bin.yml`
- Modify: `.github/workflows/android-test.yml`
- Modify: `.github/workflows/android.yml`
- Modify: `ops/test_wdtt_patchset.py`

**Interfaces:**
- WDTT artifact adds `WDTT_PATCHSET_SHA256` and `WDTT_CA_BUNDLE_SHA256` alongside `WDTT_UPSTREAM_COMMIT` and binary checksums.
- Android consumers require exact equality with repository values and both `arm64-v8a` and `armeabi-v7a` binaries.

- [ ] Extend the policy test first so current workflows fail for missing patch/CA provenance checks.
- [ ] Add exact digest files to the artifact and reject a mismatched base, patch series, CA bundle, ABI or binary checksum before copying `libwdtt.so`.
- [ ] Run exact-SHA native and Android workflows. Inspect APK signature, versionCode, both packaged ABIs and confirm the TV-only diff is empty.
- [ ] Publish only the test artifact from the workflow; do not create a Release or OTA. Commit as `ci(wdtt): bind APK to provider bridge patchset`.

### Task 9: Review, durable memory and physical-phone gate

**Files:**
- Modify: `docs/implementation/mobile-vk-turn-handoff.md`
- Modify: `CURRENT_PRODUCTION_HANDOFF.md`
- Modify: `CONTEXT_HANDOFF.md`

**Interfaces:**
- Produces anonymized exact-SHA CI and phone acceptance evidence; does not expose endpoints, hashes, credentials or identifiers.

- [ ] Audit final diff against the approved specification and scan for secrets, insecure TLS, TV changes, customer/payment/bot/DNS/OTA changes and unbounded retry.
- [ ] Obtain an independent review with Critical=0 and Important=0; address findings through another RED/GREEN cycle.
- [ ] Install the signed test APK without deleting app data. Confirm WDTT survives subscription refresh and restart.
- [ ] Select WDTT and require: native worker READY, local relay bound, `vk-turn` selected, one new S1 handshake/count, increasing bidirectional counters and public HTTPS egress.
- [ ] Repeat disconnect/reconnect, app restart, sleep/wake, Wi-Fi -> mobile and mobile -> Wi-Fi. Record only counts and pass/fail.
- [ ] If any gate fails, keep production/OTA unchanged and continue from the surfaced single stage. Only after every gate passes may a separate owner-authorized production panel rollout and OTA task begin.
- [ ] Update the three handoff documents with exact source SHA, workflow IDs, candidate artifact identity, phone result, rollback state and next step. Commit/push as `docs: record WDTT provider bridge verification`.

## Final verification matrix

- Native: exact base, patch apply/check, Go tests, verified CA rejection, stage redaction, provider probe, READY-after-register, both Android ABIs and Linux probe artifact.
- Android: JVM tests, compile, APK assembly/signature, process lifecycle, CAPTCHA cancellation/timeout, TV denial, selector non-regression and no TV diff.
- Backend/panel: unit/integration, race, vet, file mode/atomicity, concurrent probe lease, candidate rollback, redacted JSON and old-client subscription compatibility.
- Device/server: complete phone path, S1 handshake/counters, egress and lifecycle matrix.
- Release: test artifact only until explicit OTA authorization after physical acceptance.
