# XHTTP First-Canary Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Use RED then GREEN, stage only the files named by the current task, and request an independent review before live deployment.

**Goal:** Prove one real, isolated VLESS Encryption + XHTTP GET-body tunnel through the existing Yandex Cloud CDN test resource without touching ordinary x-ui/Xray listeners, customer subscriptions, balances, Telegram bots, Android/TV 1.0.157, OLCRTC or WDTT.

**Architecture:** A new pre-candidate `canary` package generates and validates one protected server/client pair from the pinned Xray 26.7.28 asset, materializes one-client server plus direct/CDN client configs, and stages a dedicated systemd service with an explicit first-canary `ABSENT -> PREPARED -> CANARY_ACTIVE -> ABSENT` lifecycle. It deliberately does not construct `release.Release`: the immutable candidate is created only after the live release gates exist.

**Tech Stack:** Go, Xray-core 26.7.28, systemd, Yandex Cloud CDN, GitHub Actions, Python contract tests.

## Global constraints

- Canonical branch: `codex/yandex-cdn-whitelist-task3-sync`.
- Pinned Xray tag: `26.7.28`; source commit `5ca6f4b7d4dc20a881d4330e498892697627ec0c`.
- Official binary archive URL: `https://github.com/XTLS/Xray-core/releases/download/v26.7.28/Xray-linux-64.zip`.
- Binary archive SHA-256: `8195d909f1109b8f3d99eefe401a3c451d7bf4af71f24d3815420f77e5dd2a40`.
- Extracted Linux amd64 `xray` SHA-256: `64d46afb80adea1bf97a0d467e83f4a9ac1ebd0995891e84bca3f1a1d1affb1d`.
- Official source archive SHA-256: `f7e2426b267f24aabdc72868bf85ebe100df9cce50ed90595a5c959ad188bf70`.
- Source and compiled-binary provenance remain separate.
- Existing S4 x-ui-owned Xray 26.6.22, TCP/443 and UDP/443 remain byte-for-byte and listener-for-listener unchanged.
- Sidecar public port is TCP/18081; local stats API is `127.0.0.1:18082`.
- Secrets, UUID, server decryption, client encryption, secret path, client configs and URI never enter Git, stdout, logs or command-line flags.
- Root-owned request/snapshot/client files are regular `0600`; protected directories are `0700`.
- Runtime directories and copied Xray are `root:maestro-xray-cdn` `0550`; server config is `root:maestro-xray-cdn` `0640`; nothing is group-writable.
- The first transport proof uses the already specified GET-body/session/sequence baseline. Padding/XMUX are a separate A/B gate immediately after the baseline and must not be claimed before measured success.
- A successful Yandex tunnel proves the transport and CDN path. A restrictive-network namespace may approximate an allow-list, but only an actual mobile operator/SIM in restricted mode can close the operator white-list gate.
- Run `ops/maestro-repetition-guard.py` before every repository mutation, network/server action or long validation and record success/failure/correction evidence.

---

### Task 1: Pure pre-candidate snapshot and one-client config pair

**Files:**

- Create: `backend/internal/canary/pins.go`
- Create: `backend/internal/canary/snapshot.go`
- Create: `backend/internal/canary/config.go`
- Create: `backend/internal/canary/snapshot_test.go`
- Create: `backend/internal/canary/config_test.go`

**Interfaces:**

```go
type XrayProvenance struct {
    Version             string `json:"version"`
    Commit              string `json:"commit"`
    SourceArchiveURL    string `json:"source_archive_url"`
    SourceArchiveSHA256 string `json:"source_archive_sha256"`
    BinaryArchiveURL    string `json:"binary_archive_url"`
    BinaryArchiveSHA256 string `json:"binary_archive_sha256"`
    BinarySHA256        string `json:"binary_sha256"`
}

type Request struct {
    SchemaVersion                int    `json:"schema_version"`
    PublicHost                   string `json:"public_host"`
    DiagnosticProbeURL           string `json:"diagnostic_probe_url"`
    DiagnosticResponseSHA256     string `json:"diagnostic_response_sha256"`
}

type Material struct {
    ClientID          string
    ClientEmail       string
    ServerDecryption  string
    ClientEncryption  string
    PairTranscriptSHA256 string
    SecretPath        string
}

func ParseRequest(raw []byte) (Request, error)
func NewSnapshot(request Request, material Material) (Snapshot, error)
func ParseSnapshot(raw []byte) (Snapshot, error)
func (Snapshot) CanonicalJSON() []byte
func (Snapshot) SHA256() string
func (Snapshot) Materialize() (Artifacts, error)
```

`Artifacts` contains canonical `ServerConfig`, `DirectClientConfig`, `CDNClientConfig`, `ClientURI` and a non-secret `Receipt`. It exposes byte copies only.

- [ ] **Step 1: Write RED snapshot tests**

Cover canonical round-trip, unknown/duplicate/trailing fields, oversize input, non-UTF-8, unsafe hosts/URLs, malformed UUID, wrong VLESS encryption roles, mixed pair evidence, wrong pins and source/binary provenance substitution.

- [ ] **Step 2: Run the focused RED tests**

Run from `backend`:

```powershell
go test -count=1 ./internal/canary -run 'Test(ParseRequest|Snapshot)'
```

Expected: fail because the package and interfaces do not exist.

- [ ] **Step 3: Implement the smallest strict snapshot model**

Use bounded canonical JSON, `DisallowUnknownFields`, a second-decode EOF check, duplicate-key rejection before decode, constant-time digest comparison, defensive byte copies and stable reason-coded errors. Pins are constants, not request overrides.

- [ ] **Step 4: Write RED config-pair tests**

Assert:

- server inbound is `vless`, `0.0.0.0:18081`, `network=xhttp`, `mode=packet-up`, `uplinkHTTPMethod=GET`, `uplinkDataPlacement=body`;
- exactly one inbound client exists with only `id`, `email`, `level:0`; client encryption never appears server-side;
- direct client targets `127.0.0.1:18081` without TLS;
- CDN client targets the public host on 443 with TLS/SNI/Host equal to the public host;
- both clients use the same UUID, encryption, path, session and sequence metadata;
- SOCKS test inbounds are loopback-only and use distinct fixed ports;
- server policy enables per-user uplink/downlink stats for level 0 and stats API is loopback-only on 18082;
- receipt contains hashes/reason codes only, never raw secrets or operational URLs.

- [ ] **Step 5: Implement materialization and make Task 1 GREEN**

Do not modify the existing `release.RuntimeMaterial`, `release.NewCandidate`, published-subscription gate or activation store. This avoids both the evidence cycle and unrelated fixture churn.

Run:

```powershell
go test -count=1 ./internal/canary
go test -count=1 ./internal/controlplane ./internal/subgen ./internal/release
```

Expected: all pass.

- [ ] **Step 6: Commit Task 1**

Commit only the five Task 1 files with message `feat(cdn): materialize isolated xhttp canary pair`.

---

### Task 2: Protected Linux stage and first-canary lifecycle

**Files:**

- Create: `backend/internal/canary/store.go`
- Create: `backend/internal/canary/store_linux.go`
- Create: `backend/internal/canary/store_unsupported.go`
- Create: `backend/internal/canary/store_linux_test.go`
- Create: `backend/internal/canary/store_unsupported_test.go`

**Interfaces:**

```go
type State string

const (
    StateAbsent           State = "ABSENT"
    StatePrepared         State = "PREPARED"
    StateCanaryActive     State = "CANARY_ACTIVE"
    StateRollbackRequired State = "ROLLBACK_REQUIRED"
)

type Stage struct {
    RuntimeID          string
    State              State
    SnapshotSHA256     string
    XraySHA256         string
    ServerConfigSHA256 string
    UnitSHA256         string
}

type ConfigTester interface {
    Test(context.Context, string, string, uint32, uint32) error
}

type ServiceController interface {
    Reload(context.Context) error
    IsActive(context.Context, string) (bool, error)
    Start(context.Context, string) error
    Stop(context.Context, string) error
}

type DiagnosticOrigin interface {
    RestoreAndVerify(context.Context, string, string) error
}

func (s *Store) Prepare(context.Context, Snapshot, []byte, Artifacts, ConfigTester) (Stage, error)
func (s *Store) Activate(context.Context, string, ServiceController) error
func (s *Store) RollbackToAbsence(context.Context, string, ServiceController, DiagnosticOrigin) error
```

The raw `[]byte` accepted by `Prepare` is exactly the extracted pinned Linux
Xray executable. `Prepare` generates the safe runtime ID internally, re-parses
the canonical snapshot, re-materializes every supplied artifact, compares the
bytes, and verifies the executable against `PinnedXrayBinarySHA256` before any
config test or filesystem write.

- [ ] **Step 1: Write RED Linux store tests**

Test exact UID/GID/modes and link counts, safe-parent and regular-file enforcement, symlink/hardlink refusal, existing-target refusal, digest-before-exec ordering, atomic temp+fsync+no-replace-rename+directory-fsync, generated unit with the exact stage path, no ordinary x-ui paths, `Prepare` leaving the service inactive, `ABSENT -> PREPARED -> ROLLBACK_REQUIRED -> CANARY_ACTIVE -> ABSENT`, active/unknown-service fail-closed behavior, ambiguous-start recovery, and rollback retaining immutable evidence while removing the runnable static layer and restoring plus verifying the diagnostic origin.

- [ ] **Step 2: Run the RED tests on Linux CI-compatible code**

```powershell
go test -count=1 ./internal/canary -run 'TestStore'
```

- [ ] **Step 3: Implement the Linux store and unsupported-platform fail-closed stub**

Runtime layout:

```text
/opt/maestro-xray-cdn/runtime/<runtime-id>/xray
/run/maestro-xray-cdn/config.json
/etc/systemd/system/maestro-xray-cdn.service
/var/lib/maestro-xray-cdn-canary/state.json
/root/.maestro-xray-cdn-canary/<runtime-id>/snapshot.json
/root/.maestro-xray-cdn-canary/<runtime-id>/client-direct.json
/root/.maestro-xray-cdn-canary/<runtime-id>/client-cdn.json
/root/.maestro-xray-cdn-canary/<runtime-id>/client-uri.txt
```

Parents under `/opt` and `/run` are `root:maestro-xray-cdn 0750`; the immutable
runtime directory and copied executable are `root:maestro-xray-cdn 0550`; the
runtime config is `root:maestro-xray-cdn 0640`. State, snapshot and client
evidence directories are `root:root 0700`, with regular one-link files mode
`0600`. The unit is `root:root 0644` and contains no secret.

The generated unit uses the exact immutable runtime path and
`/run/maestro-xray-cdn/config.json`; it runs as `maestro-xray-cdn`, is refused
if an unrelated unit already exists, and carries the established sidecar
hardening boundary from `backend/internal/release/templates.go`:
`UMask=0077`, `NoNewPrivileges=true`, `PrivateTmp=true`,
`ProtectSystem=strict`, `ProtectHome=true`, fixed read-only runtime paths and
only the dedicated log path writable. It uses no shell.

`Prepare` writes the durable `PREPARED` record last and never starts systemd.
`Activate` reloads systemd, proves the unit inactive, persists
`ROLLBACK_REQUIRED` before `Start`, then records `CANARY_ACTIVE` only after a
positive active check. Any ambiguous start remains rollback-required.
`RollbackToAbsence` accepts `PREPARED`, `ROLLBACK_REQUIRED` and
`CANARY_ACTIVE`; it stops and proves the unit inactive, restores and verifies
the diagnostic origin recorded in the protected snapshot, removes only
digest-matching generated config/unit files, reloads systemd, and persists
`ABSENT` last. Immutable runtime and root-only evidence remain retained.

- [ ] **Step 4: Make Task 2 GREEN and commit**

```powershell
go test -count=1 ./internal/canary
```

Commit only Task 2 files with message `feat(cdn): add reversible first-canary store`.

---

### Task 3: Operator CLI with in-process secret generation

**Files:**

- Create: `backend/cmd/maestro-xray-cdn-canary/main.go`
- Create: `backend/cmd/maestro-xray-cdn-canary/main_test.go`
- Create: `backend/cmd/maestro-xray-cdn-canary/runner_linux.go`
- Create: `backend/cmd/maestro-xray-cdn-canary/runner_unsupported.go`

**Command contract:**

```text
maestro-xray-cdn-canary prepare --request-file <0600 root file> --xray-archive <0600 root file>
maestro-xray-cdn-canary activate --runtime-id <safe id>
maestro-xray-cdn-canary rollback --runtime-id <safe id>
maestro-xray-cdn-canary status
```

The request file, archive file and runtime ID are validated without printing resolved sensitive paths. Secrets are generated inside `prepare`: extract exactly one `xray` member from the pinned archive, verify archive and binary digests, run the verified binary directly with `vlessenc`, select exactly the ML-KEM-768 block, generate UUIDv4 and a cryptographically random path, construct the snapshot, stage it and run `xray run -test` as the sidecar UID/GID.

- [ ] **Step 1: Write RED CLI tests**

Cover duplicate/missing flags, archive traversal, duplicate/colliding `xray` members, oversize/ambiguous `vlessenc` output, wrong digest before process execution, wrong block selection, invalid UUID/path generation, stdout/stderr redaction, no shell invocation, prepare without systemd/network start, lifecycle state enforcement and unsupported platform. Permit the official archive's unrelated non-executable members while extracting only the single exact root-level `xray` member.

- [ ] **Step 2: Implement CLI and make it GREEN**

```powershell
go test -count=1 ./internal/canary ./cmd/maestro-xray-cdn-canary
go vet ./internal/canary ./cmd/maestro-xray-cdn-canary
```

- [ ] **Step 3: Commit Task 3**

Commit only Task 3 files with message `feat(cdn): add xhttp canary operator command`.

---

### Task 4: Exact-SHA GitHub validation and independent review

**Files:**

- Modify: `.github/workflows/yandex-cdn-release.yml`
- Modify: `scripts/tests/test_yandex_cdn_ci.py`
- Modify: `ops/validate-yandex-cdn-release.sh`
- Modify: `ops/validate-yandex-cdn-release.ps1`
- Modify: `docs/yandex-cdn-whitelist/TEST_RESULTS.md`
- Modify: `CONTEXT_HANDOFF.md`
- Modify: `docs/yandex-cdn-whitelist/BASELINE_MANIFEST.json`

- [ ] **Step 1: Add RED workflow-contract assertions**

Require path filters, gofmt scope, unit/race/vet package lists and both wrapper package lists to include `backend/internal/canary/**`, `backend/cmd/maestro-xray-cdn-canary/**`, `./internal/canary` and `./cmd/maestro-xray-cdn-canary`.

- [ ] **Step 2: Update workflow/wrappers and run full applicable validation**

```powershell
cd backend
go test -count=1 ./internal/controlplane ./internal/subgen ./internal/release ./internal/canary ./cmd/maestro-release-validate ./cmd/maestro-xray-cdn-canary
go test -count=1 -race ./internal/controlplane ./internal/subgen ./internal/release ./internal/canary ./cmd/maestro-release-validate ./cmd/maestro-xray-cdn-canary
go vet ./internal/controlplane ./internal/subgen ./internal/release ./internal/canary ./cmd/maestro-release-validate ./cmd/maestro-xray-cdn-canary
cd ..
python -X utf8 -m unittest scripts.tests.test_yandex_cdn_ci scripts.tests.test_yandex_cdn_docs
python -X utf8 scripts/validate_yandex_cdn_docs.py
git diff --check
```

- [ ] **Step 3: Refresh documentation without overclaim**

Record implementation as repository-ready only. Keep direct VLESS/XHTTP, CDN tunnel, per-user stats, operator restriction and billing as NOT TESTED until live evidence exists. Regenerate `BASELINE_MANIFEST.json` only from a clean exact-HEAD worktree so protected dirty files cannot contaminate it.

- [ ] **Step 4: Push and wait for all applicable exact-SHA GitHub checks**

No server mutation before all checks are GREEN and an independent reviewer reports zero Critical/Important findings.

---

### Task 5: Isolated S4 deployment and real Yandex CDN tunnel proof

**Repository files:** none during the live window. Raw evidence is written only to the protected evidence directory and later summarized redacted.

- [ ] **Step 1: Read-only preflight**

Capture S4 OS/resources, existing x-ui binary hash/version, `ss -lntup`, UFW rules, failed units, ports 18080/18081/18082, service/unit/file absence, rollback commands and Yandex diagnostic resource state. Abort on any drift from the recorded baseline.

- [ ] **Step 2: Build exact-SHA CLI in GitHub or clean Linux environment**

Record repository SHA and binary SHA-256. Transfer only the reviewed CLI and the already pinned official Xray archive. Create the sidecar user/group and protected request file; do not copy secrets through Git or chat.

- [ ] **Step 3: Prepare without starting**

Run `prepare`; require pinned archive/binary/config GREEN, exact modes/owners, sidecar inactive, ports unchanged and x-ui binary/listeners unchanged.

- [ ] **Step 4: Activate isolated sidecar and run direct tunnel smoke**

Start only `maestro-xray-cdn.service`. Start the direct client config with the staged pinned Xray, send a deterministic HTTP request through its loopback SOCKS port, verify response hash and query the exact canary email's uplink/downlink counters. Abort and stop the sidecar on any mismatch.

- [ ] **Step 5: Bounded Yandex resource switch and CDN tunnel smoke**

Record the current diagnostic origin receipt, change only the test CDN resource origin to S4 TCP/18081, keep caches/transformations off, wait for propagation, then run the CDN client and verify actual response bytes, GET-body transport, TLS/SNI/Host, counter growth and ordinary listener invariance.

- [ ] **Step 6: Restore first, then rollback to ABSENT**

Restore the diagnostic origin in Yandex Cloud, verify the original diagnostic body hash through the CDN, run `rollback`, verify sidecar stopped, 18081/18082 closed, state `ABSENT`, staged evidence retained and x-ui listeners/binary unchanged.

- [ ] **Step 7: Optional restrictive-network approximation**

In an isolated Linux network namespace only, deny all client egress except DNS and the resolved Yandex CDN edges, then repeat the CDN smoke. Never modify the host's global default route or ordinary firewall policy. Mark this result as an approximation, not proof of a mobile operator's DPI/white-list.

- [ ] **Step 8: Record live evidence and next gate**

Store raw evidence under `C:\Users\User\.codex\private\maestrovpn-cdn-live-evidence\<UTC-id>`, hash every file, update redacted docs/manifest from a clean worktree, and push exact-SHA evidence. If the CDN tunnel passes, the next task is measured padding/XMUX A/B followed by actual phone/SIM restricted-mode validation. If it fails, retain rollback state and fix only the identified layer.

## Completion boundary

This plan is complete only when the direct tunnel, Yandex CDN tunnel, per-user counter and restoration checks all pass on the same pinned runtime and ordinary S4 listeners remain unchanged. It does not by itself complete customer subscription wiring, gigabyte charging, Telegram/panel integration, mobile-operator proof or production customer cutover.
