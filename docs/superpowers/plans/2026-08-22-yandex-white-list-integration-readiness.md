# Yandex White-List Integration Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Apply RED -> GREEN for every behavior change and keep one writer in this worktree.

**Goal:** Add honest, candidate-bound integration evidence and offline acceptance tooling, enforce evidence-quality gates, validate all required replay suites and client-matrix fields in CI, and build a separately identified non-production APK after backend gates pass.

**Architecture:** The existing signed release evidence gains a typed evidence class with gate-specific minimums. A new pure Go `whitelistready` package strictly validates a synthetic fixture catalog, replay observations, and client matrix, while a small CLI and nine inert shell wrappers reproduce one suite at a time. Cross-package tests exercise control-plane, subscription, shadow-billing, and API contracts together. CI validates code and offline evidence but always reports release readiness `NO_GO` until later real-binary, device, and production gates exist.

**Tech stack:** Go 1.25, standard-library JSON and crypto, Bash, GitHub Actions, Kotlin/Gradle only for a test-version override and the final Android artifact build.

## Global constraints

- Production baseline is MaestroVPN 1.0.157.
- Ordinary VPN, TV, existing VPNService, live subscriptions, UUIDs, balances, 3x-ui/Xray, firewall, databases, CDN origin, and OTA stay unchanged.
- No server/network action, live endpoint, public DNS, production credential, secret, or owner-supplied literal enters Task 7 code or fixtures.
- Fixture replay is never represented as real Xray, Yandex, device, or production evidence.
- The replay CLI has no live mode and accepts no arbitrary URL, endpoint, token, shell command, or environment override.
- Do not modify production Android workflow `.github/workflows/android.yml`, OTA metadata, manifests, TV UI/resources/assets, WDTT/qWDTT/CSQTT/olcRTC sources, or `version.properties`.
- Do not stage `normalize.patch`, ignored SDD artifacts, recovered `app/libs/libbox.aar`, generated Room schemas, build outputs, or the pre-existing Task 4 report change.
- Every mutation, validation, staging action, commit, build, and retry receives a separate repetition-guard check.

---

## Task 1: Enforce evidence quality in signed release gates

**Files:**

- Modify: `backend/internal/release/evidence.go`
- Modify: release test helpers that construct `GateReport`
- Create: `backend/internal/release/evidence_class_test.go`

**Interfaces:**

- Produces: `release.EvidenceClass`
- Produces: `release.MinimumEvidenceClass(gateID string)` as a defensive read-only lookup
- Extends: `release.GateReport` and its canonical signed payload with `evidence_class`
- Preserves: signature, trust origin, freshness, candidate hash, transport hash, runtime hash, and Xray binary hash checks

- [ ] **Step 1: Write RED tests for downgraded evidence**

Add table tests proving correctly signed reports are rejected when:

- `FIXTURE_REPLAY` claims `yandex_get_body`, `direct_origin`, `literal_edge`, `isolated_start`, `local_vless`, `per_user_stats`, or `xray_config_test`;
- `ISOLATED_REAL_BINARY` claims `client_import`;
- `DEVICE_OBSERVED` claims `production_baseline`;
- the class is missing, unknown, mixed-case, or changed after signing.

Also prove `SCHEMA_ONLY` is sufficient only for `config_validation`, fixture replay is sufficient for `billing_identity` and `subscription_regression`, and stronger classes satisfy lower minimums.

- [ ] **Step 2: Run focused RED**

~~~powershell
Set-Location backend
go test ./internal/release -run EvidenceClass -count=1
~~~

Expected: FAIL because evidence class and minimum enforcement do not exist.

- [ ] **Step 3: Implement the signed evidence class**

Use the ordered enum:

~~~go
type EvidenceClass string

const (
    EvidenceSchemaOnly         EvidenceClass = "SCHEMA_ONLY"
    EvidenceFixtureReplay      EvidenceClass = "FIXTURE_REPLAY"
    EvidenceIsolatedRealBinary EvidenceClass = "ISOLATED_REAL_BINARY"
    EvidenceDeviceObserved     EvidenceClass = "DEVICE_OBSERVED"
    EvidenceProductionObserved EvidenceClass = "PRODUCTION_OBSERVED"
)
~~~

Bind `evidence_class` inside `CanonicalUnsignedPayload`. Validation must use an explicit per-gate map, reject unknown gates/classes, and compare ranks without accepting lexical order. Update existing test report builders with the actual minimum for each gate; do not give every helper production evidence by default.

- [ ] **Step 4: Run GREEN and release regression suite**

~~~powershell
Set-Location backend
gofmt -w internal/release/evidence.go internal/release/evidence_class_test.go <only-modified-release-tests>
go test ./internal/release -count=1
go test -race ./internal/release -count=1
go vet ./internal/release
~~~

- [ ] **Step 5: Inspect, stage, and commit only Task 1 files**

Commit message:

~~~text
fix(release): require gate evidence quality
~~~

---

## Task 2: Strict readiness schemas, fixtures, matrix, and CLI

**Files:**

- Create: `backend/internal/whitelistready/model.go`
- Create: `backend/internal/whitelistready/validate.go`
- Create: `backend/internal/whitelistready/validate_test.go`
- Create: `backend/cmd/maestro-whitelist-ready/main.go`
- Create: `backend/cmd/maestro-whitelist-ready/main_test.go`
- Create: `scripts/repro/fixtures/acceptance-catalog.v1.json`
- Create: `scripts/repro/fixtures/acceptance-evidence.v1.json`
- Create: `scripts/repro/fixtures/client-compatibility-matrix.v1.json`

**Interfaces:**

- Consumes: bounded JSON files only
- Produces: `whitelistready.Validate(catalog, evidence, matrix)`
- Produces: deterministic suite assessment with `harness_status=PASS` and `release_readiness=NO_GO`
- Produces CLI commands: `validate` and `replay --suite <required-suite>`

- [ ] **Step 1: Write RED parser, binding, and matrix tests**

Positive tests cover the complete synthetic catalog, all nine suites, and the four required clients. Adversarial tests cover:

- more than 512 KiB, invalid UTF-8, trailing JSON, and unknown fields;
- duplicate/missing case, observation, client, or check IDs;
- malformed commit/hash/version/time/environment fields;
- binding mismatch for candidate, artifact, config, or catalog hash;
- stale or non-UTC observation timestamps;
- fixture observation claiming a stronger evidence class;
- negative/overflow counters and unsafe control characters;
- endpoint-, credential-, private-key-, subscription-token-, and production-identifier-like content;
- fabricated compatibility status on a `NOT_RUN` client;
- `PASSED` client status without complete `DEVICE_OBSERVED` check evidence;
- import-only evidence being labelled supported;
- a readiness verdict other than `NO_GO` for fixture-only evidence.

- [ ] **Step 2: Run focused RED**

~~~powershell
Set-Location backend
go test ./internal/whitelistready ./cmd/maestro-whitelist-ready -count=1
~~~

- [ ] **Step 3: Implement bounded models and strict decoding**

Required evidence classes match the release package vocabulary. Verification states are `NOT_RUN`, `PASSED`, `FAILED`, and `BLOCKED`. Compatibility status is nullable and, when present, one of `SUPPORTED`, `SUPPORTED_WITH_SETTING`, `EXPERIMENTAL`, `IMPORT_ONLY_UNSTABLE`, or `UNSUPPORTED`.

Use `json.Decoder.DisallowUnknownFields`, one-document EOF checks, UTF-8 validation, explicit count/string limits, fixed reason-code errors, deterministic sorting, SHA-256 comparison, and UTC RFC3339Nano timestamps. Never include raw rejected values in errors.

The catalog includes six body sizes and all required Yandex semantics without real hosts or URLs. Evidence binds every observation to one synthetic candidate commit, artifact/config/catalog hashes, tool/core versions, environment `OFFLINE_FIXTURE`, and a fixed synthetic observation time. The matrix contains exactly MaestroVPN, Karing, Incy, and Happ with `verification_state: NOT_RUN`, null compatibility status, and every required device check present as `NOT_RUN`. MaestroVPN records production baseline `1.0.157`; unmeasured external versions remain null.

- [ ] **Step 4: Implement the CLI and run GREEN**

The CLI accepts only explicit local paths and a required suite enum. It emits one compact JSON object and fixed-code errors. It has no network package import and no live flag.

~~~powershell
Set-Location backend
gofmt -w internal/whitelistready cmd/maestro-whitelist-ready
go test ./internal/whitelistready ./cmd/maestro-whitelist-ready -count=1
go test -race ./internal/whitelistready ./cmd/maestro-whitelist-ready -count=1
go vet ./internal/whitelistready ./cmd/maestro-whitelist-ready
go run ./cmd/maestro-whitelist-ready validate --catalog ../scripts/repro/fixtures/acceptance-catalog.v1.json --evidence ../scripts/repro/fixtures/acceptance-evidence.v1.json --matrix ../scripts/repro/fixtures/client-compatibility-matrix.v1.json
~~~

- [ ] **Step 5: Inspect, stage, and commit only Task 2 files**

Commit message:

~~~text
feat(readiness): validate offline acceptance evidence
~~~

---

## Task 3: Cross-package proof and exact reproduction scripts

**Files:**

- Create: `backend/internal/whitelistready/integration_test.go`
- Create: `scripts/repro/_run-white-list-suite.sh`
- Create the nine required `scripts/repro/*.sh` wrappers named in the design
- Create: `scripts/tests/test_yandex_cdn_repro.py`

- [ ] **Step 1: Write RED cross-package and wrapper tests**

The Go integration test must use the exported production-domain seams from `controlplane`, `subgen`, `shadowbilling`, and `whitelistapi/v1`. Cover OFF byte identity, ACTIVE additive rendering, suspension/revocation/release mismatch, edge rotation, cache regeneration, stable server-side billing identity, counter reset, out-of-order and duplicate replay, no ordinary-traffic billing, no real balance mutation, and private API fixture validation.

The Python test must require all nine filenames, executable Git modes, `set -euo pipefail`, cwd-independent path resolution, exact fixed suite selection, zero positional arguments, no `curl`, `wget`, SSH, socket/client library, endpoint variable, `eval`, production mutation command, or secret-like literal, and semantic `PASS` plus `NO_GO` output.

- [ ] **Step 2: Run focused RED**

~~~powershell
Set-Location backend
go test ./internal/whitelistready -run Integration -count=1
Set-Location ..
python -m unittest scripts.tests.test_yandex_cdn_repro
~~~

- [ ] **Step 3: Implement the vertical integration test and inert wrappers**

`_run-white-list-suite.sh` resolves `SCRIPT_DIR`, rejects unexpected arguments, enters `backend`, and `exec`s the Go CLI with repository-owned fixture paths. Each public wrapper passes exactly one hard-coded suite and no user-controlled string. Set executable Git modes for all shell files.

- [ ] **Step 4: Run every wrapper and full backend validation**

~~~bash
bash -n scripts/repro/*.sh
for script in scripts/repro/yandex-get-body.sh scripts/repro/yandex-active-stream.sh scripts/repro/yandex-idle-cutoff.sh scripts/repro/yandex-literal-edge.sh scripts/repro/xray-counter-reset.sh scripts/repro/billing-idempotency.sh scripts/repro/duplicate-event-replay.sh scripts/repro/subscription-escaping.sh scripts/repro/edge-rotation.sh; do "$script"; done
~~~

~~~powershell
Set-Location backend
go test ./internal/controlplane ./internal/subgen ./internal/shadowbilling ./internal/whitelistapi/v1 ./internal/release ./internal/whitelistready ./cmd/maestro-whitelist-ready -count=1
go test -race ./internal/release ./internal/whitelistready ./cmd/maestro-whitelist-ready -count=1
go vet ./internal/controlplane ./internal/subgen ./internal/shadowbilling ./internal/whitelistapi/v1 ./internal/release ./internal/whitelistready ./cmd/maestro-whitelist-ready
Set-Location ..
python -m unittest scripts.tests.test_yandex_cdn_docs scripts.tests.test_yandex_cdn_repro
python scripts/validate_yandex_cdn_docs.py
~~~

- [ ] **Step 5: Inspect, stage, and commit only Task 3 files**

Commit message:

~~~text
test(readiness): replay white-list integration suites
~~~

---

## Task 4: CI gates and separately versioned test APK

**Files:**

- Modify: `.github/workflows/yandex-cdn-release.yml`
- Modify: `app/build.gradle.kts`
- Create: `scripts/tests/test_yandex_cdn_ci.py`
- Build only, do not stage: `app/build/outputs/apk/other/debug/*.apk`

- [ ] **Step 1: Write RED CI and version-boundary tests**

Static tests require:

- read-only workflow permissions and no production secret, endpoint, deploy, release, SSH, firewall, database, service-control, or OTA command;
- complete path filters for readiness code, fixtures, wrappers, tests, and workflow;
- separate formatting/unit, race/vet, and offline-replay jobs;
- all nine wrappers and syntax checks;
- explicit assertion that replay output remains `NO_GO`;
- an Android test-version override that is absent by default, requires name and code together, accepts only a bounded `*-task7-test` name and a code above production, and does not alter `version.properties`.

- [ ] **Step 2: Run RED**

~~~powershell
python -m unittest scripts.tests.test_yandex_cdn_ci
~~~

- [ ] **Step 3: Implement CI and test-only version override**

Extend only `yandex-cdn-release.yml`. Keep `android.yml` unchanged. The offline workflow has no production environment and no write permission.

In `app/build.gradle.kts`, read optional `maestroTask7TestVersionName` and `maestroTask7TestVersionCode`. Reject partial, malformed, production-equal, or non-test values during configuration. When absent, preserve `version.properties` exactly. The override is used only by the explicit Task 7 build command and is never committed into release metadata.

- [ ] **Step 4: Run backend gates before any APK build**

Run all Task 3 GREEN commands plus:

~~~powershell
python -m unittest scripts.tests.test_yandex_cdn_ci
git diff --check
~~~

Only after all commands pass, build:

~~~powershell
./gradlew.bat :app:assembleOtherDebug -PmaestroTask7TestVersionName=1.0.158-task7-test -PmaestroTask7TestVersionCode=1015800 --no-daemon
~~~

Use the recovered, ignored `app/libs/libbox.aar` only after verifying:

- source workflow run `31758418005`;
- artifact ID `9203846250`;
- ZIP SHA-256 `baf1ae39b845601ef5291c92c9638261f4a9dfd7725d04ffd856431d48d42b37`;
- AAR SHA-256 `c70ce917b331fd333d52b8e3c01eba2c2a343497f896fdf62c30436490a88f05`.

Verify the APK with `apkanalyzer` or `aapt`: package `com.maestrovpn.tv`, version name `1.0.158-task7-test`, version code `1015800`, non-debuggable production safety flag, and expected signer class. Record the APK SHA-256. Do not upload, release, install, or modify OTA.

- [ ] **Step 5: Inspect, stage, and commit only source/CI/test files**

Commit message:

~~~text
ci(readiness): gate fixture replay before test build
~~~

---

## Task 5: Evidence documents, reviews, and handoff

**Files:**

- Modify: `docs/yandex-cdn-whitelist/TEST_RESULTS.md`
- Modify: `docs/yandex-cdn-whitelist/ROLLBACK.md`
- Modify: `docs/yandex-cdn-whitelist/HANDOFF.md`
- Modify: `CONTEXT_HANDOFF.md`
- Create ignored: `.superpowers/sdd/2026-08-20-yandex-cdn-whitelist/task-7-report.md`
- Modify ignored: `.superpowers/sdd/2026-08-20-yandex-cdn-whitelist/progress.md`

- [ ] **Step 1: Update evidence without upgrading unverified gates**

Record exact commits and commands, fixture catalog/evidence/matrix SHA-256 values, wrapper results, CI scope, Android version/provenance/APK SHA-256, and remaining `NOT_RUN` gates. State explicitly:

- fixture harness verified;
- release readiness remains `NO_GO`;
- no real Xray isolated-process, Yandex CDN, client-device, backup/restore, canary, charging, OTA, or production cutover evidence exists yet;
- the five-minute rollback objective is not yet timed.

- [ ] **Step 2: Run documentation, secrecy, and scope validation**

~~~powershell
python -m unittest scripts.tests.test_yandex_cdn_docs scripts.tests.test_yandex_cdn_repro scripts.tests.test_yandex_cdn_ci
python scripts/validate_yandex_cdn_docs.py
python scripts/render_redacted_baseline.py
git diff --check
~~~

Audit the Task 7 range and require zero changes to `.github/workflows/android.yml`, `version.properties`, Android manifest, TV UI/resources/assets, WDTT/qWDTT/CSQTT/olcRTC source, production server configuration, database schema, subscription UUIDs, balances, firewall, or OTA files.

- [ ] **Step 3: Request two independent reviews**

One reviewer checks specification coverage and false readiness claims. A second reviewer checks evidence downgrade resistance, parser bounds, secret channels, command injection, workflow permissions, Android production isolation, billing identity, ordinary-VPN preservation, and rollback honesty. Fix every Critical or Important finding RED -> GREEN, up to five rounds, and repeat both reviews.

- [ ] **Step 4: Run final verification**

Repeat all focused and full Task 7 checks from Tasks 1-4, confirm the worktree contains only protected pre-existing files outside the intended range, and re-hash the final test APK if source changes required rebuilding it.

- [ ] **Step 5: Commit evidence docs and complete the ignored ledger**

Commit message:

~~~text
docs(readiness): record Task 7 evidence and no-go gates
~~~

Append:

~~~text
Task 7: complete (<design-sha>..<final-sha>, review clean, fixture readiness NO_GO)
~~~

Do not push, deploy, publish a release, install the APK, switch CDN origin, migrate production, charge balances, or publish OTA. Transition to the separately approved production inventory/backup-readiness stage.
