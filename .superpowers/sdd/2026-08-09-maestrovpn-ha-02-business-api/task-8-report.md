# Task 8 report: compatible HA Business API and exclusive runtime mode

Date: 2026-08-28
Branch: `codex/yandex-cdn-whitelist-task3-sync`
BASE: `f5044e20d9747b75ff63a040b212c29116bc62c9`
Accepted Task 7 code: `6b46af934280c99f822cfda42d7915c487264134`
Implementation HEAD: `985c77c7457a1a6fbe7fbae97bc25bdb0c320dd8`
Status: local repository-GREEN; production remains **NO-GO**; push and exact-SHA GitHub workflows are intentionally handed to the root agent.

## Exact brief and scope

- Materialized brief: `.superpowers/sdd/2026-08-09-maestrovpn-ha-02-business-api/task-8-brief.md`
- SHA-256: `f78d1135ac9ebb5225c0c5cd7b095ee63015dbbd49d3ba8149f1f6b404786633`
- The slice is the approved Task 8 compatibility adapter, canonical mutation surface, durable external-action behavior and mutually exclusive runtime composition.
- Task 9 URL/RBAC work was not absorbed. Plan 03 local-agent application remains downstream.
- No production, server, release, OTA, bot or customer mutation occurred. Android production baseline remains `1.0.157`.

## Implementation

- Preserved `api.New(...)` and the legacy composition root. Added `api.NewControlPlane(...)`, a complete `api.Business` port and HA-only route/panel dispatch.
- Added one startup mode branch. Unset/`legacy` retains the old JSON-store/provisioner/reconciler behavior; `rqlite` constructs only TLS RQLite, crypto, control-plane Service, WB provider and HA API dependencies, then returns before any legacy store or ticker setup. Unknown or incomplete mode configuration fails closed.
- Froze the public/admin/panel route and action surface, including health/subscription/order/trial/report, every current admin mutation, panel session/password/customer/status/OLCRTC/WB/VKTurn action, and the offline-only `410` bulk-import behavior.
- Added a concrete `ServiceBusiness` adapter across auth, customer reads/writes, tariff/order/payment, subscription/device, trial, status, settings, audit, migration and external-action families. No required Task 8 handler returns a placeholder implementation.
- Added distinct canonical customer transactions for provision, extend, renew, set-expiry, disable, enable, reset and legacy-named expiry sweep. Identity/credentials survive delete/expiry semantics and writes emit absolute desired state/outbox plus protected idempotent responses.
- Added one-transaction trial redemption with canonical anti-abuse HMACs, idempotency, customer state, desired state/outbox and saved response. Quorum failure has no local fallback ledger.
- Added paid visibility gating: legacy `paid` is exposed only after confirmed payment, usable canonical subscription and a current enabled-service apply receipt with `receipt.generation >= result_generation`; owner views preserve independent payment/provisioning axes.
- Added durable external actions in RQLite with cluster-job leases, DB-time fences, `pending -> attempt_started -> succeeded|unknown`, protected request/response envelopes and fail-closed crash/takeover behavior. Provider POSTs are at-most-once per action key.
- Added RQLite-backed WB token decryption, the bounded HTTP WB create-room provider and runtime injection. No token file fallback exists in rqlite mode.
- A successful WB provider result now calls `Service.AssignWBRoom`, which writes room/provider, membership, idempotency/CAS state, `s3-olcrtc` desired state and outbox in one canonical setting transaction. Replay of an already-applied identical room/member is a no-op; conflicting reuse remains a conflict.
- Setting/OLCRTC/VKTurn/WB-token mutations require propagated idempotency keys and use durable request hashes, version CAS, encrypted secret references, audit and protected saved responses.

## Complete changed boundary

`BASE..985c77c` is 47 files, 6,933 insertions and 4 deletions:

- `backend/cmd/maestro-panel/main.go`
- `backend/cmd/maestro-panel/main_test.go`
- `backend/cmd/maestro-panel/runtime_builder_test.go`
- `backend/cmd/maestro-panel/runtime_config.go`
- `backend/cmd/maestro-panel/runtime_config_test.go`
- `backend/cmd/maestro-panel/runtime_rqlite.go`
- `backend/internal/api/controlplane_business.go`
- `backend/internal/api/controlplane_business_test.go`
- `backend/internal/api/controlplane_dispatch_fake_test.go`
- `backend/internal/api/controlplane_dispatch_test.go`
- `backend/internal/api/controlplane_panel_handlers.go`
- `backend/internal/api/controlplane_panel_session_dispatch_test.go`
- `backend/internal/api/controlplane_port.go`
- `backend/internal/api/controlplane_port_test.go`
- `backend/internal/api/controlplane_public_admin.go`
- `backend/internal/api/controlplane_service_business_behavior_test.go`
- `backend/internal/api/controlplane_wb_dispatch_test.go`
- `backend/internal/api/wb_room_sender.go`
- `backend/internal/api/wb_room_sender_test.go`
- `backend/internal/controlplane/business_api.go`
- `backend/internal/controlplane/business_api_behavior_test.go`
- `backend/internal/controlplane/customer_access.go`
- `backend/internal/controlplane/customer_access_test.go`
- `backend/internal/controlplane/customer_commands.go`
- `backend/internal/controlplane/customer_commands_test.go`
- `backend/internal/controlplane/external_actions.go`
- `backend/internal/controlplane/external_actions_rqlite.go`
- `backend/internal/controlplane/external_actions_rqlite_crash_test.go`
- `backend/internal/controlplane/external_actions_rqlite_test.go`
- `backend/internal/controlplane/external_actions_service.go`
- `backend/internal/controlplane/external_actions_service_test.go`
- `backend/internal/controlplane/external_actions_test.go`
- `backend/internal/controlplane/models.go`
- `backend/internal/controlplane/paid_visibility.go`
- `backend/internal/controlplane/paid_visibility_test.go`
- `backend/internal/controlplane/service.go`
- `backend/internal/controlplane/setting_idempotency.go`
- `backend/internal/controlplane/setting_secret_read.go`
- `backend/internal/controlplane/setting_secret_read_test.go`
- `backend/internal/controlplane/status.go`
- `backend/internal/controlplane/status_test.go`
- `backend/internal/controlplane/task8_setting_idempotency_hash_test.go`
- `backend/internal/controlplane/task8_setting_idempotency_test.go`
- `backend/internal/controlplane/task8_setting_secret_sql_test.go`
- `backend/internal/controlplane/trials.go`
- `backend/internal/controlplane/trials_test.go`
- `backend/internal/controlplane/wb_room_assignment.go`

The SDD ledger and this report are added separately after the implementation boundary.

## Commits

1. `c22bdb3` `test(api): define exclusive control-plane runtime mode`
2. `c0c61e8` `feat(api): select exclusive control-plane runtime`
3. `9e5d20f` `test(api): define HA route and action parity`
4. `2eacbd6` `feat(api): add HA business compatibility port`
5. `c852475` `test(panel): define fail-closed rqlite composition`
6. `2e5b239` `feat(panel): validate exclusive rqlite runtime config`
7. `1d189ca` `test(controlplane): define canonical customer and trial writes`
8. `b34c005` `feat(controlplane): add canonical customer and trial writes`
9. `ce5fbd7` `test(controlplane): define external action and paid gates`
10. `f22e6eb` `feat(controlplane): fence external actions and paid visibility`
11. `db98398` `test(api): define control-plane route dispatch`
12. `e846722` `feat(api): dispatch control-plane business routes`
13. `c1d219e` `test(api): require concrete control-plane business adapter`
14. `14882ca` `test(controlplane): require atomic customer access mint`
15. `5404924` `feat(controlplane): mint customer access atomically`
16. `3ee80b6` `test(api): define concrete service business behavior`
17. `04a56e5` `feat(api): bind HA business service`
18. `07ffd92` `test(panel): require real HA composition root`
19. `7bc90cc` `feat(panel): wire exclusive HA runtime`
20. `2c5d2ed` `test(controlplane): require idempotent setting mutations`
21. `864a60e` `feat(controlplane): make setting writes idempotent`
22. `654c339` `test(controlplane): require fenced durable action recovery`
23. `b27d16c` `feat(controlplane): fence durable external actions`
24. `1d1bcfe` `test(controlplane): require durable WB external action execution`
25. `074507e` `feat(controlplane): execute fenced external actions`
26. `2b2126f` `test(api): require durable WB room dispatch`
27. `6765a63` `feat(api): execute durable WB room actions`
28. `3ddeb17` `test(api): require RQLite-backed WB room provider`
29. `5f66ee3` `feat(api): wire RQLite-backed WB room provider`
30. `973cf90` `test(api): require canonical WB room assignment`
31. `9d03e94` `test(controlplane): require canonical WB room assignment`
32. `5e64d97` `test(controlplane): fix WB room request hash fixture`
33. `3eb7628` `test(controlplane): derive WB room request hash fixture`
34. `985c77c` `feat(controlplane): assign WB rooms canonically`

## RED evidence

- Runtime selector RED required unset/legacy exclusivity, explicit rqlite construction and unknown-mode failure before `c0c61e8`.
- Route/action parity RED required the complete public/admin/panel matrix, distinct extend/renew behavior, `410` bulk import and no HA legacy dependency before `2eacbd6`.
- Fail-closed composition RED required complete TLS/crypto config and proved rqlite mode never opened stores, built a legacy provisioner or reached exec/SSH before `2e5b239` and `7bc90cc`.
- Customer/trial RED required distinct idempotent transactions, absolute state, desired/outbox, anti-abuse identities and protected replay before `b34c005`.
- Access-mint RED found a mandatory defect: token and encrypted credentials were not minted atomically. `5404924` made them one idempotent transaction with rollback/replay coverage and no plaintext persistence.
- External-action/paid RED required durable provider state, receipt-gated paid visibility and post-at-most-once crash boundaries before `f22e6eb`, `b27d16c` and `074507e`.
- Adapter behavior RED covered representative auth/read/write methods across every Business family, decrypted access without ciphertext leakage, settings/admin reads and fail-closed errors before `04a56e5`.
- Setting RED found missing durable idempotency/CAS/saved-response and OLCRTC desired/outbox behavior. `864a60e` added it; a SQL arity defect found during GREEN was fixed without weakening assertions.
- WB dispatch/provider RED required a durable fenced executor, RQLite-encrypted token, exact bounded provider request and runtime injection before `6765a63` and `5f66ee3`.
- Final API RED at `973cf90` compiled with `unknown field wbRooms`; direct transaction RED at `9d03e94` compiled with `service.AssignWBRoom undefined`. `985c77c` satisfies both.

The final direct transaction fixture initially used an older helper that omitted `TargetMembers` from the request hash. The fixture was corrected to derive the exact production request hash. Semantic assertions were unchanged: one transaction, canonical setting/idempotency tables, desired state, outbox, `s3-olcrtc` and the idempotency key all remain mandatory. No RED contract was weakened.

## GREEN verification

Definitive disposable clone based on `3eb7628`, with the reviewed production patch:

- `go test ./internal/controlplane -run '^TestAssignWBRoomUsesCanonicalOLCRTCTransaction$' -count=1` — GREEN, `0.049s`.
- `go test ./internal/api -run '^TestServiceBusinessRequestWBRoomExecutesDurableProvider$' -count=1` — GREEN, `0.059s`.
- `go test ./internal/api ./internal/controlplane -count=1` — GREEN: API `1.679s`, controlplane `9.000s`.

Canonical tree immediately before `985c77c` commit:

- direct AssignWBRoom transaction — GREEN, `0.083s`;
- `go test ./internal/api ./internal/controlplane ./cmd/maestro-panel -count=1` — GREEN: API `1.217s`, controlplane `14.067s`, panel `0.189s`.

The full ordinary suites include the exact required guards:

- `TestRQLiteRouteActionSnapshotIsComplete`
- `TestExtendAndRenewRemainDistinct`
- `TestRQLiteBulkImportReturns410WithoutMutation`
- `TestRQLiteModeHasNoLegacyStoreProvisionerExecOrSSH`
- `TestExternalActionCrashBoundariesPostAtMostOnce`
- `TestExternalActionStaleLeaseCannotSend`
- `TestExternalActionReplacementUsesNewKey`
- `TestAssignWBRoomUsesCanonicalOLCRTCTransaction`

Per task authority, no local race, heavy, real-RQLite or Android suite was run. Those remain exact-SHA GitHub work for the root agent after push.

## Changed-boundary and source audit

- Full contextual old/new boundary review completed before every canonical apply; the final implementation boundary is the 47 files listed above.
- `git diff --check` and staged diff checks were clean for the final WB assignment RED, fixture and GREEN commits.
- `api.New(...)` remains the legacy constructor. `api.NewControlPlane(...)` is instantiated only by the rqlite runtime builder.
- `main.go` branches once: the rqlite branch builds/serves/shuts down and returns before legacy store/provisioner/ticker construction; unset mode enters only legacy; unknown mode terminates.
- Fixed-string source guards found no `StatusNotImplemented`, placeholder `unsupported`, TODO/FIXME, legacy store, provisioner, shell, `/bin/sh` or SSH dependency in the HA adapter/control-plane action boundary. The only `unsupported` mode text is the intentional unknown-mode startup error.
- Required HA mutations consume `Idempotency-Key`; setting and external-action paths persist request identity and protected responses. No 503 fallback to legacy exists.
- WB action success with a non-empty room is the only path that invokes canonical room assignment; unknown/non-success states remain inspectable and do not re-send.

## Repository integrity and handoff

- Exact implementation HEAD before this report: `985c77c7457a1a6fbe7fbae97bc25bdb0c320dd8`.
- Remote branch remained at Task 8 base `f5044e20d9747b75ff63a040b212c29116bc62c9` at handoff. No remote mutation was made; the root agent requested responsibility for push, exact-SHA workflows and protected-status review.
- Protected owner files were never staged, edited or deleted: `.superpowers/sdd/2026-08-20-yandex-cdn-whitelist/task-4-report.md` and `normalize.patch`.
- One repetition-ledger writer was used. Unexpected ACL, Windows glob/line-ending and fixture results were recorded fail -> correct -> one corrected attempt; no context-free or zero-context production edit was used.
- No secrets were printed or committed. WB token storage remains encrypted RQLite state; provider plaintext is returned to the caller only where required and durable responses remain sealed.

## Remaining scope

- Root agent: push the final report commit, verify local equals remote, run HA/DR/Yandex exact-SHA workflows (including race/heavy/real-RQLite gates), resolve review feedback and record run/job IDs/protected status.
- Task 9: URL/RBAC/public-host work only; it was intentionally excluded here.
- Plan 03: local agents consume `s3-olcrtc` desired/outbox state; Task 8 does not execute systemd/S3/SSH directly.
- Production remains **NO-GO** until the broader approved rollout gates are completed.

## Review round 1, finding 2: subscription compatibility completion

Date: 2026-08-29
Scope: finding 2 only; local repository-GREEN, not a production authorization.
Starting HEAD: `ba07125031e547c6bb2970919049592a3cdd5982`
Completion code: `e41858c8016f8bf9eebd7023fb7d54598a28f219` (`fix(api): preserve HA subscription request semantics`)

This bounded continuation builds on the already committed renderer wiring (`3d0fe22`), frozen renderer (`e27b8cd`) and subscription info/helpers correction (`ba07125`). It does not redo those fixes, the separate HTTPS runtime fix (`0c9cf3f`), or other review findings.

### Remaining defect and frozen behavior

- The real rqlite composition root had not supplied the frozen subscription topology. It now passes a public-endpoint-only environment topology into `ServiceBusinessConfig.SubscriptionTopology`; customer credentials still come exclusively from canonical cluster access.
- Runtime configuration reuses the existing legacy environment names and defaults for S1 VLESS, S2 Hysteria2, optional Naive, optional AnyTLS, and optional S3/S4 VLESS. S3/S4 retain the existing two-variable enablement conditions (panel-base-URL presence plus VLESS-server presence). Presence checks do not construct or contact legacy panels.
- Frozen `provisionS3`/`provisionS4` code explicitly reuses the primary VLESS UUID (the relevant source was inspected at lines 779-870). Therefore S3/S4 use the existing canonical `vless` credential, not invented per-node credential keys. Missing customer VLESS credentials suppress all three VLESS nodes; stale UUIDs in shared topology cannot be published.
- The real HA HTTP adapter now carries request options into the renderer without changing the frozen `Business` method signature or the public JSON snapshot shape. `ContentType` is internal metadata (`json:"-"`). Existing info/helpers dispatch remains on its already-fixed path.
- The renderer preserves the frozen AWG minimum-version gate, the Naive/Cronet gate for unrecognized SFA clients, the DNS fake-IP kill switch, exact case-sensitive `app=karing` / `format=links` selection, plain-text share-link content type and `no-store`. Share-link selection precedes the Naive JSON gate, as in the frozen API.
- Android mobile and TV `1.0.157` request fixtures retain the frozen generated document byte-for-byte. No generator, selector, outbound, protocol, release or OTA implementation was changed. WG/OLC/VK are not newly enabled by runtime topology; no credentials are minted by rendering.
- Shared topology is cloned before binding customer access and applying per-request gates. No legacy store, Provisioner, SSH, panel connection or local fallback was introduced.

### Exact code boundary and self-review

The code commit contains exactly eight files (416 insertions, 15 deletions):

- `backend/internal/api/controlplane_subscription_compat.go`
- `backend/internal/api/controlplane_business.go`
- `backend/internal/api/controlplane_port.go`
- `backend/internal/api/controlplane_public_admin.go`
- `backend/internal/api/controlplane_subscription_request_test.go`
- `backend/cmd/maestro-panel/runtime_rqlite.go`
- `backend/cmd/maestro-panel/runtime_subscription.go`
- `backend/cmd/maestro-panel/runtime_subscription_test.go`

The complete staged contextual diff was reviewed. The existing five-file source delta is confined to runtime injection, request-aware dispatch/rendering and internal content-type metadata; three new files supply the environment adapter and focused tests. Staged path equality against the eight-file allowlist and `git diff --cached --check` both passed. The renderer and runtime tests use synthetic fixture data only.

### Behavioral RED

Working directory for all Go commands below: canonical repository `backend`. Portable Go 1.25.0, `GOMAXPROCS=2`, `GOTOOLCHAIN=local`; ordinary focused tests only.

Before the production fix, the following combined command exited 1:

```powershell
$env:GOMAXPROCS = "2"
$env:GOTOOLCHAIN = "local"
& "$env:TEMP\maestro-gofmt-go1.25.0-windows-amd64\toolchain\go\bin\go.exe" test ./internal/api ./cmd/maestro-panel -run 'TestControlPlaneSubscription(PreservesFrozenRequestSemantics|MissingCredentialsDoNotPublishTopologyIdentity)|TestRQLiteRuntimeSubscriptionUsesFrozenEnvironmentTopology' -count=1
```

The valid API portion of that exact output was:

```text
--- FAIL: TestControlPlaneSubscriptionPreservesFrozenRequestSemantics (0.00s)
    --- FAIL: TestControlPlaneSubscriptionPreservesFrozenRequestSemantics/installed_mobile (0.00s)
        controlplane_subscription_request_test.go:98: HTTP subscription differs from frozen legacy client document
    --- FAIL: TestControlPlaneSubscriptionPreservesFrozenRequestSemantics/installed_tv (0.00s)
        controlplane_subscription_request_test.go:98: HTTP subscription differs from frozen legacy client document
    --- FAIL: TestControlPlaneSubscriptionPreservesFrozenRequestSemantics/older_sfa (0.00s)
        controlplane_subscription_request_test.go:98: HTTP subscription differs from frozen legacy client document
    --- FAIL: TestControlPlaneSubscriptionPreservesFrozenRequestSemantics/stock_core (0.00s)
        controlplane_subscription_request_test.go:98: HTTP subscription differs from frozen legacy client document
    --- FAIL: TestControlPlaneSubscriptionPreservesFrozenRequestSemantics/plain_karing (0.00s)
        controlplane_subscription_request_test.go:98: HTTP subscription differs from frozen legacy client document
    --- FAIL: TestControlPlaneSubscriptionPreservesFrozenRequestSemantics/malformed_sfa (0.00s)
        controlplane_subscription_request_test.go:98: HTTP subscription differs from frozen legacy client document
    --- FAIL: TestControlPlaneSubscriptionPreservesFrozenRequestSemantics/karing_links (0.00s)
        controlplane_subscription_request_test.go:95: content type="application/json", want "text/plain; charset=utf-8"
        controlplane_subscription_request_test.go:98: HTTP subscription differs from frozen legacy client document
    --- FAIL: TestControlPlaneSubscriptionPreservesFrozenRequestSemantics/format_links (0.00s)
        controlplane_subscription_request_test.go:95: content type="application/json", want "text/plain; charset=utf-8"
        controlplane_subscription_request_test.go:98: HTTP subscription differs from frozen legacy client document
    --- FAIL: TestControlPlaneSubscriptionPreservesFrozenRequestSemantics/case_sensitive_app (0.00s)
        controlplane_subscription_request_test.go:98: HTTP subscription differs from frozen legacy client document
    --- FAIL: TestControlPlaneSubscriptionPreservesFrozenRequestSemantics/fakeip_kill_switch (0.00s)
        controlplane_subscription_request_test.go:98: HTTP subscription differs from frozen legacy client document
--- FAIL: TestControlPlaneSubscriptionMissingCredentialsDoNotPublishTopologyIdentity (0.00s)
    controlplane_subscription_request_test.go:132: missing customer credential still published UUID at tag "vless-s3"
FAIL
FAIL	github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api	0.059s
```

The first runtime portion also returned 503, but was **not accepted as runtime RED evidence**: the initial SQL double returned an envelope as raw JSON instead of the base64 BLOB representation that canonical `openCustomerSecret` actually consumes. The subsequent first GREEN attempt passed API but retained the same runtime 503. The fixture was corrected to base64-encode the sealed JSON envelope and to prove `runtime.business.CustomerByToken` succeeds before rendering. No production decryption, access check or expected behavior was weakened.

For an independent valid runtime RED, only the new `SubscriptionTopology: rqliteSubscriptionTopologyFromEnvironment()` injection line was reversibly removed using a generated contextual patch. All corrected canonical-access preflight checks passed, and this command exited 1 at the subscription assertion:

```powershell
$env:GOMAXPROCS = "2"
$env:GOTOOLCHAIN = "local"
& "$env:TEMP\maestro-gofmt-go1.25.0-windows-amd64\toolchain\go\bin\go.exe" test ./cmd/maestro-panel -run '^TestRQLiteRuntimeSubscriptionUsesFrozenEnvironmentTopology$' -count=1
```

```text
--- FAIL: TestRQLiteRuntimeSubscriptionUsesFrozenEnvironmentTopology (0.00s)
    --- FAIL: TestRQLiteRuntimeSubscriptionUsesFrozenEnvironmentTopology/configured (0.00s)
        runtime_subscription_test.go:66: subscription status=503
    --- FAIL: TestRQLiteRuntimeSubscriptionUsesFrozenEnvironmentTopology/optional_servers_absent (0.00s)
        runtime_subscription_test.go:66: subscription status=503
    --- FAIL: TestRQLiteRuntimeSubscriptionUsesFrozenEnvironmentTopology/node_enablement_absent (0.00s)
        runtime_subscription_test.go:66: subscription status=503
FAIL
FAIL	github.com/evgenmay1978-del/proectmaestro-vpn/backend/cmd/maestro-panel	0.065s
FAIL
```

The same checked contextual patch was then applied forward to restore the injection line. This proves that the real runtime composition, not only an isolated renderer helper, needs and now receives the topology.

### Focused GREEN and full affected-package verification

After restoring the wiring and correcting the SQL fixture, the exact combined focused command above exited 0:

```text
ok  	github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api	0.084s
ok  	github.com/evgenmay1978-del/proectmaestro-vpn/backend/cmd/maestro-panel	0.066s
```

The HTTP request matrix covers installed mobile/TV 157, older SFA, stock core, plain Karing, malformed SFA, both link selectors, selector case sensitivity and the fake-IP kill switch. It compares exact frozen `subgen.GenerateSingbox` / `subgen.ShareLinks` bytes, response headers and topology immutability. A separate assertion rejects topology UUID leakage when customer credentials are absent. The real runtime + canonical service + crypto + HTTP test covers configured topology, missing optional server settings and missing S3/S4 enablement settings; only the remote SQL read boundary is doubled, with mutations rejected.

Final canonical verification after gofmt exited 0:

```powershell
$env:GOMAXPROCS = "2"
$env:GOTOOLCHAIN = "local"
& "$env:TEMP\maestro-gofmt-go1.25.0-windows-amd64\toolchain\go\bin\go.exe" test ./internal/api ./internal/controlplane ./internal/subgen ./cmd/maestro-panel -count=1
```

```text
ok  	github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api	0.708s
ok  	github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane	10.696s
ok  	github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen	0.057s
ok  	github.com/evgenmay1978-del/proectmaestro-vpn/backend/cmd/maestro-panel	0.069s
```

Final exact-file formatting/whitespace commands:

```powershell
$ownedSourceFiles = @(
    'backend/internal/api/controlplane_subscription_compat.go',
    'backend/internal/api/controlplane_business.go',
    'backend/internal/api/controlplane_port.go',
    'backend/internal/api/controlplane_public_admin.go',
    'backend/internal/api/controlplane_subscription_request_test.go',
    'backend/cmd/maestro-panel/runtime_rqlite.go',
    'backend/cmd/maestro-panel/runtime_subscription.go',
    'backend/cmd/maestro-panel/runtime_subscription_test.go'
)
& "$env:TEMP\maestro-gofmt-go1.25.0-windows-amd64\toolchain\go\bin\gofmt.exe" -l @ownedSourceFiles
git diff --check -- @ownedSourceFiles
git diff --cached --check
```

All exited 0; gofmt printed no file paths and both whitespace checks printed no errors. Git emitted its existing LF-to-CRLF working-copy warnings only. A preceding gofmt gate had detected CRLF inserted when Git applied source patches; a read-only gofmt diff confirmed line endings were the sole difference, and gofmt was run on exactly these eight files before the final package verification.

### Editing integrity and remaining gates

- Production changes used full old/new temporary mirrors created with `apply_patch Add File`, generated contextual `git diff --no-index` patches, explicit LF serialization, `git apply --check --recount --whitespace=error -p2`, then `git apply --recount --whitespace=error -p2`. No hand-counted, zero-context or canonical overwrite edit was used. The prior unverified implementer draft was not applied.
- The first test-patch serializer used CRLF; Git whitespace warnings were recorded, then the affected new tests were formatted. Later generated patches used explicit LF. The final canonical source gate above also corrected Git working-copy CRLF. Repetition-guard failure/correction records were kept for these mechanical issues and the runtime fixture; failed command families were not blindly retried.
- Root-owned `progress.md`, protected `task-4-report.md` and `normalize.patch` were not edited or staged. Existing unrelated Go/EOL/gofmt dirt was preserved. No broad add/reset/checkout/cleanup was used.
- A report-only apply preflight caught an extra EOF newline introduced by the initial full-file mirror serialization. Canonical report content was unchanged; exact EOF mirrors regenerated the contextual patch. No context or whitespace check was bypassed.
- No push, production/server action, release/OTA change, secrets output, real-customer output or external write occurred.
- Ordinary API/controlplane/subgen/panel packages are GREEN at code SHA `e41858c8016f8bf9eebd7023fb7d54598a28f219`. Independent review and any exact-SHA GitHub heavy/race/real-rqlite/Android gates remain with the root agent. This evidence closes the bounded local implementation for finding 2, not other findings or the broader production NO-GO.

## Review round 1, findings 12–14: OTA, backfill bodies and compatibility reports

Date: 2026-08-29
Scope: findings 12, 13 and 14 only. OLCRTC and WDTT were explicitly excluded and were not changed.
Base HEAD: `66d041dbda95dacc00c2ff880948f65b2ae1c4b1`
State at handoff: local changes intentionally unstaged and uncommitted; no push, deploy or production action.

### Root causes and minimal compatibility fixes

- **Finding 12 — APK delivery:** the HA catch-all `/update/` handler called `ApprovedOTA` for every path, so the manifest's `/update/<version>.apk` URL returned manifest JSON. The adapter now keeps `/update/update.json` on the canonical approval read, but serves only traversal-safe bare `.apk` filenames from the already-configured `UpdateDir` using `http.ServeFile`. This restores byte delivery, Range/resume, `application/vnd.android.package-archive`, one-day APK cache and the frozen path checks.
- **Finding 13 — body compatibility:** AnyTLS/S3 backfill and AnyTLS migration now accept the frozen empty body; S4 accepts an empty body or the frozen optional `{"logins":[...]}` canary object. A dedicated decoder retains POST and `Idempotency-Key` requirements for authenticated HA writes, the 1 MiB bound, unknown-field rejection, malformed JSON rejection and exactly one JSON document. S4 logins are carried intact in `ReconcileServicesCommand.Logins`; the real adapter validates supplied canonical logins. Empty migration bodies resolve the current configured AnyTLS server from the frozen subscription environment topology instead of requiring a new request field.
- **Finding 14 — report persistence:** the HA route no longer returns unconditional 204. Both legacy and HA adapters call the same frozen report implementation: POST only, 64 KiB bounded JSON, field sanitization/clipping, server timestamp, serialized append and a fixed per-day `reports-YYYY-MM-DD.jsonl` path under `ReportDir`. Storage remains best-effort 204 as required.
- No credential, customer, generator, OLCRTC, WDTT, release, OTA publication or external state was accessed or mutated. Tests use synthetic APK/report bytes and Go-owned temporary directories only.

### Exact owned source boundary

- `backend/internal/api/controlplane_port.go`
- `backend/internal/api/controlplane_public_admin.go`
- `backend/internal/api/controlplane_business.go`
- `backend/internal/api/report.go`
- `backend/internal/api/controlplane_compatibility_contract_test.go`

Existing protected and unrelated dirt was preserved. The staging area remained empty throughout this bounded work.

### Behavioral RED

Working directory: canonical repository `backend`. Portable Go 1.25.0; ordinary focused test only.

```powershell
$env:GOMAXPROCS = "2"
$env:GOTOOLCHAIN = "local"
& "$env:TEMP\maestro-gofmt-go1.25.0-windows-amd64\toolchain\go\bin\go.exe" test ./internal/api -run "TestControlPlane(OTAUsesFrozenAPKByteRangeAndPathContract|BackfillAndMigrationPreserveFrozenBodies|ReportValidatesAndPersistsUnderReportDir)$" -count=1
```

The exact corrected frozen-login fixture produced this expected exit 1 before production edits:

```text
--- FAIL: TestControlPlaneOTAUsesFrozenAPKByteRangeAndPathContract (0.01s)
    controlplane_compatibility_contract_test.go:44: APK range status=200 body="{\"version\":\"1.0.157\",\"url\":\"/update/1.0.157.apk\",\"sha256\":\"fixture-sha\"}\n"
--- FAIL: TestControlPlaneBackfillAndMigrationPreserveFrozenBodies (0.01s)
    --- FAIL: TestControlPlaneBackfillAndMigrationPreserveFrozenBodies/anytls_empty (0.00s)
        controlplane_compatibility_contract_test.go:86: status=400 body="{\"error\":\"invalid json\"}\n"
    --- FAIL: TestControlPlaneBackfillAndMigrationPreserveFrozenBodies/s3_empty (0.00s)
        controlplane_compatibility_contract_test.go:86: status=400 body="{\"error\":\"invalid json\"}\n"
    --- FAIL: TestControlPlaneBackfillAndMigrationPreserveFrozenBodies/s4_empty (0.00s)
        controlplane_compatibility_contract_test.go:86: status=400 body="{\"error\":\"invalid json\"}\n"
    --- FAIL: TestControlPlaneBackfillAndMigrationPreserveFrozenBodies/s4_canary (0.00s)
        controlplane_compatibility_contract_test.go:86: status=400 body="{\"error\":\"invalid json\"}\n"
    --- FAIL: TestControlPlaneBackfillAndMigrationPreserveFrozenBodies/migration_empty (0.00s)
        controlplane_compatibility_contract_test.go:86: status=400 body="{\"error\":\"invalid json\"}\n"
    --- FAIL: TestControlPlaneBackfillAndMigrationPreserveFrozenBodies/anytls_unknown_field (0.00s)
        controlplane_compatibility_contract_test.go:127: status=200 body="{\"id\":\"invalid-anytls-unknown-field\",\"state\":\"accepted\"}\n"
--- FAIL: TestControlPlaneReportValidatesAndPersistsUnderReportDir (0.00s)
    controlplane_compatibility_contract_test.go:148: open C:\Users\User\AppData\Local\Temp\TestControlPlaneReportValidatesAndPersistsUnderReportDir1060740004\001\reports: The system cannot find the file specified.
FAIL
FAIL	github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api	0.086s
FAIL
```

The RED proves all three root causes independently: APK requests returned manifest JSON/status 200, empty legacy admin bodies returned 400 (while an AnyTLS body field was wrongly accepted), and no report directory/file was created. The S4 fixture was corrected before production edits after the frozen `BackfillS4` source proved that canary logins are compared literally; no invented normalization was retained.

### GREEN and affected-package verification

The identical focused command exited 0:

```text
ok  	github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api	0.082s
```

The focused contract exercises a real HA HTTP handler and covers:

- manifest URL followed by ranged APK bytes, content type/content range and traversal rejection;
- empty AnyTLS/S3/S4/migration bodies, S4 canary logins, unknown fields, damaged JSON and trailing documents;
- valid report persistence/sanitization/path containment, wrong method, malformed JSON, 64 KiB rejection and no append on rejected requests.

Complete ordinary affected API package:

```powershell
$env:GOMAXPROCS = "2"
$env:GOTOOLCHAIN = "local"
& "$env:TEMP\maestro-gofmt-go1.25.0-windows-amd64\toolchain\go\bin\go.exe" test ./internal/api -count=1
```

```text
ok  	github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api	0.782s
```

Complete ordinary panel composition package:

```powershell
$env:GOMAXPROCS = "2"
$env:GOTOOLCHAIN = "local"
& "$env:TEMP\maestro-gofmt-go1.25.0-windows-amd64\toolchain\go\bin\go.exe" test ./cmd/maestro-panel -count=1
```

```text
ok  	github.com/evgenmay1978-del/proectmaestro-vpn/backend/cmd/maestro-panel	0.072s
```

Final exact-file hygiene:

```powershell
$owned = @(
    'backend/internal/api/controlplane_port.go',
    'backend/internal/api/controlplane_public_admin.go',
    'backend/internal/api/controlplane_business.go',
    'backend/internal/api/report.go',
    'backend/internal/api/controlplane_compatibility_contract_test.go'
)
& "$env:TEMP\maestro-gofmt-go1.25.0-windows-amd64\toolchain\go\bin\gofmt.exe" -l @owned
git diff --check -- @owned
git diff --cached --name-only
```

The formatting command printed no paths, the whitespace check printed no errors, and the staging check printed no paths. Existing Git LF-to-CRLF working-copy warnings are unchanged repository configuration, not source-format failures.

Per task authority, no local race, heavy, real-RQLite, Android or release/OTA suite was run. Those remain separate exact-SHA CI work after owner review and an explicit commit/push decision.

## Accepted dirty-closure checkpoint: findings 7, 8, 12, 14 and 15

Date: 2026-08-29
Scope: checkpoint for independently accepted findings 7, 8, 12, 14 and 15.
Finding 13 was provisionally accepted here, but the later combined review
reopened it because the real adapter discarded the requested login scope.
Findings 2, 4, 5, 6, 9 and 10 remain open; finding 10 is not accepted and was
not changed. OLCRTC findings 3/11 and WDTT remain frozen.
No production, server, network, release, OTA or remote mutation occurred.

Fresh local verification (working directory backend, Go 1.25.0,
GOMAXPROCS=2, GOTOOLCHAIN=local) exited 0:

Commands:
go test ./internal/controlplane -run '^TestTask8(TrialDuplicateIdentityPreservesAllStateSQLite|TrialIdentityClaimAfterReadsIsAtomicSQLite|TrialCustomerGenerationRaceRollsBackSQLite|TrialRequiresOneRedemptionSQLite|TrialWithoutDRMDoesNotShareIdentitySQLite|TrialEmptyAnchorCannotBypassRedemptionSQLite|TrialDerivesLegacyDeviceFromAnchorSQLite|TrialUnrelatedImportedIdentityRemainsEligibleSQLite|ExistingMutationsKeepAbsoluteAccessSQLite|ConcurrentSameIdempotencyReturnsCommittedAccessSQLite|ResetDevicesPreservesInactiveStatusSQLite)$' -count=1
go test ./internal/api -run '^TestControlPlane(OTAUsesFrozenAPKByteRangeAndPathContract|BackfillAndMigrationPreserveFrozenBodies|ReportValidatesAndPersistsUnderReportDir)$' -count=1
go test ./internal/api -count=1
go test ./cmd/maestro-panel -count=1

The API commands reported ok in 0.085s, 0.870s and the panel command
reported ok in 0.065s; the focused real-SQL command exited 0. gofmt -l
printed no paths after formatting only the five changed control-plane files.
The final staged-diff whitespace and allowlist checks are recorded with the
commit checkpoint; protected owner dirt remains unstaged.

## Finding 13 corrected closure: S4 canary customer scope

Date: 2026-08-29

The independent combined review reopened finding 13: the HTTP adapter accepted
`Logins`, verified that those customers existed, and then invoked the
service-wide reconcile path. A real `ServiceBusiness` + migrations + SQLite
test seeded Alice and Bob for S4. Before the production change, this exact test
failed because an Alice-only canary created durable outbox rows for both Alice
and Bob:

```powershell
& "$env:TEMP\maestro-gofmt-go1.25.0-windows-amd64\toolchain\go\bin\go.exe" test ./internal/controlplane -run '^TestServiceBusinessS4CanaryReconcilesOnlySelectedCustomerSQL$' -count=1
```

The canonical adapter now resolves all requested logins to customer IDs before
any reconcile write. The scoped service method preserves the existing
service-wide method for callers without a filter and passes each selected
customer ID into `ReconcileNode`. The outbox insertion uses a parameterized
`d.customer_id` predicate, so non-selected desired rows cannot be enqueued.

The focused test then passed in 4.300s. Fresh package verification also exited
0:

```powershell
& "$env:TEMP\maestro-gofmt-go1.25.0-windows-amd64\toolchain\go\bin\go.exe" test ./internal/controlplane ./internal/api -count=1
```

The control-plane package passed in 89.443s and API in 0.844s. `gofmt -l`
printed no paths for the four scoped files, and `git diff --check` exited 0
with only the repository's existing LF-to-CRLF warnings. Independent re-review
returned Spec PASS and Quality APPROVED with no actionable findings.

Findings 2, 4, 5, 6, 9 and 10 remain open. OLCRTC findings 3/11 and WDTT remain
frozen. Android/TV production version 1.0.157 and all production/server state
were unchanged.

## Exact-SHA CI correction checkpoint

Date: 2026-08-29
Source SHA: `12102738635ce5f4d9a541b8cef84e87136247ca`

All three push workflows completed with failure before their main suites:

- HA control-plane run `33245602554`, job `99082404572`, stopped at
  `Check materialized agent boundary`.
- HA DR run `33245602522`, job `99082404207`, stopped at the same boundary.
- Yandex isolated release run `33245602496`, job `99082404046`, passed Go
  formatting/packages and stopped at the Python documentation contracts.

The HA/DR cause was mechanical: formatting `outbox.go` had removed that file
from the workflow's exact intentional noncanonical allowlist. The functional
customer-ID SQL predicate remains unchanged; only the original materialized
boundary formatting was restored. Prospective Git-blob verification now
matches the exact 25-file allowlist, and `test-agent-payload-policy.py` passes
with the explicit Go 1.25 runtime on PATH.

The Yandex cause was governance drift from the earlier S1 refresh and policy
update: the persisted baseline rows for `AGENTS.md` and `CONTEXT_HANDOFF.md`
were stale, and the handoff exposed a raw S1 endpoint forbidden by the docs
secrecy gate. The handoff now uses symbolic `CURRENT_S1`; its exact operational
value remains in the canonical server inventory/runbook and ignored local SSH
configuration. The computed manifest rows were updated.

Fresh local evidence: all 64 Yandex Python contracts pass (`3` skipped), the
direct docs validator reports `OK: docs policy`, `git diff --check` exits 0,
and the focused finding-13 real-SQL test passes in 5.202s. No production,
server, network, OTA, OLCRTC or WDTT mutation occurred. A correction commit,
push and new exact-SHA CI evidence are still required.

## Second exact-SHA CI follow-up: reviewed workflow seals

Date: 2026-08-29
Source SHA: `6b6559b7ca76a238451ec22cf8a78c65dc77958d`

The second push confirmed that the prior boundary and documentation fixes
worked. Yandex isolated release run `33246566144` completed successfully. HA
control-plane run `33246566402` and HA DR run `33246566175` progressed beyond
the materialized-agent boundary, then stopped in their Python contract step
because the reviewed workflow source seals were stale.

The cause predates this Task 8 checkpoint. Commit
`66d041dbda95dacc00c2ff880948f65b2ae1c4b1` changed only the materialized
gofmt allowlists in both workflow files but did not refresh the exact source
digests enforced by `ops/ha/test-dr-workflow-policy.py`. The old digests match
the two parent workflow blobs exactly; the replacement digests match the
current reviewed workflow text:

- control-plane: `c5352f25b1982f49c3c331873601b2de038ccfb9066f383497e54826570cf7c3`;
- DR: `238c35712b2fcffbbf16df70381d00dc40aec3acc5627c0f3f2b61b47fe57998`.

No workflow command, permission, trigger or runtime behavior was changed.
Fresh local verification reports 93 HA tests passing with 18 skipped, the
agent-payload boundary policy passing, and the DR workflow policy passing.
Independent review returned Spec PASS and Quality APPROVED with no findings.

A third exact-SHA commit/push and all three GitHub workflow results are still
required before this CI correction is accepted. Findings 2, 4, 5, 6, 9 and
10 remain open. OLCRTC findings 3/11 and WDTT remain frozen; Android/TV
version 1.0.157 and production/server state remain unchanged.

## Third exact-SHA CI result: accepted

Date: 2026-08-29
Source SHA: `55961f4d1b9b5f7d5c1e4ad23850038d836b7f9d`

The local and remote branch SHA matched exactly after push. All required
workflows completed successfully on that source SHA:

- HA control-plane run `33247164525`: GREEN;
- HA DR restore drill run `33247164516`: GREEN;
- Yandex isolated release run `33247272977`: GREEN.

The seal-only push touched `ops/ha/test-dr-workflow-policy.py`, which is not in
the Yandex workflow's push path filter. The enabled `workflow_dispatch` entry
was therefore used once against the same branch head; the resulting run
reported the exact source SHA above.

The reviewed workflow-seal CI correction is accepted. No production, server,
release, OTA, OLCRTC or WDTT mutation occurred. Findings 2, 4, 5, 6, 9 and 10
remain the open Task 8 review set pending fresh owner verification.

## Finding 2 final closure: real subscription status path

Date: 2026-08-29

The first owner rerun of the existing endpoint compatibility test was GREEN,
but independent review rejected it because its fake `Business` returned an
inactive snapshot directly. The production `ServiceBusiness` first called
`BusinessSubscriptionDocument`, whose activity/expiry authorization returned
HTTP 403 before `/info` and the HTTP 402 gate could execute.

A new public-handler test applies the real migrations to SQLite, constructs the
real secret box, store, service and `ServiceBusiness`, and seeds live, inactive
and expired customers with sealed tokens and VLESS credentials. Its first valid
RED kept the live control at 200 and proved all inactive/expired endpoints were
403: `/info` should be 200, while base `/sub` and `/helpers` should be 402.

The production adapter now reads canonical customer metadata by token first.
Unknown tokens retain their original error, an active customer without
credentials remains fail-closed, and only active customers with credentials
reach the renderer. Inactive/expired snapshots contain only the public
`CustomerView`, so no document or credential is exposed before the HTTP layer
returns legacy-compatible status.

Fresh verification exited 0:

- real ServiceBusiness/migrations/SQLite focused test: 9.134s, then 9.196s;
- focused subscription API tests: 0.093s;
- complete `internal/api`: 0.996s;
- complete `internal/controlplane`: 118.343s;
- `gofmt -l` empty and scoped `git diff --check` clean.

Independent re-review returned Spec PASS and Quality APPROVED with no findings.
Durable instructions now forbid fake-only owner closure where production
adapters add policy gates and record the Windows native-`rg` glob and
PowerShell array-concatenation rules discovered during this fix. Findings
4, 5, 6, 9 and 10 remain open. OLCRTC findings 3/11 and WDTT remain frozen;
production/server/OTA and Android/TV 1.0.157 were unchanged.

## Finding 2 exact-SHA CI result: accepted

Date: 2026-08-29
Source SHA: `f32a057a547bbf07aab6b9bc0419fc5ce114fcbe`

The local branch and remote
`codex/yandex-cdn-whitelist-task3-sync` resolved to the exact source SHA above.
All three push-triggered workflows completed successfully against that SHA:

- HA control-plane checks run `33249795546`: GREEN;
- HA DR restore drill run `33249795549`: GREEN;
- Yandex CDN isolated release checks run `33249795581`: GREEN.

Finding 2 is accepted with real-adapter local evidence, independent Spec PASS /
Quality APPROVED review and exact-SHA GitHub CI. Findings 4, 5, 6, 9 and 10
remain open. OLCRTC findings 3/11 and WDTT remain frozen. No production,
server, release, OTA or Android/TV 1.0.157 mutation occurred.

## Finding 4 local closure: legacy public mutation idempotency

Date: 2026-08-29

Android/TV 1.0.157 omits `Idempotency-Key` on `/claim`, `/trial`, `/order` and
`/order/paid-claim`. The shared mutation decoder therefore returned HTTP 428
before those public calls reached the business adapter. Authenticated admin and
panel mutations must continue to require an explicit key.

Public stable-identity routes now derive a cluster-stable key through the
existing `SecretBox.LookupHMAC`. The input is versioned, uint64 length-framed,
route-separated and contains no raw login, claim code, order ID or token in the
result. An explicit nonblank caller key is preserved byte-for-byte. Missing or
failed derivation on a stable route fails closed with HTTP 503.

Anonymous keyless `/order` deliberately remains outside deterministic replay
because 1.0.157 supplies no stable customer identity at that point. Each tap
creates a new purchase intent; real customer/order persistence remains finding
6 and is not claimed by this closure.

The first handler RED reproduced HTTP 428 on all four legacy public routes; the
core RED failed because `LegacyPublicIdempotencyKey` did not exist. Handler
tests now cover same-input replay, changed identity and route separation, raw
identity non-disclosure, exact explicit-key preservation, fail-closed adapter
absence, two distinct anonymous order intents, and unchanged admin/panel 428.

Fresh root verification passed:

- real `ServiceBusiness` + migrations + SQLite HMAC bridge: 3.990s;
- complete `internal/api`: 1.388s;
- complete `internal/controlplane`: 124.665s;
- complete `cmd/maestro-panel`: 0.129s;
- `gofmt -d` empty and exact six-file `git diff --check` clean.

Independent re-review returned Spec PASS and Quality APPROVED with no findings.
