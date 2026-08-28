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
