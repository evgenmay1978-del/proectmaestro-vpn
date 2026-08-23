# Task 3 report — fenced backup RPO lease store

Date: 2026-08-23

## Outcome

Implemented the rqlite-backed `backup-rpo` state reader and fenced lease store. The production adapter uses only database `unixepoch()` for lease and capability decisions, keeps renewals on the existing fence, increments the fence only after an expired takeover, requires the active restore epoch, and resolves an unknown mutation outcome with one exact linearizable evidence read.

The canonical migrations v1-v5 were not changed. No workflow, production, deployment, release, OTA, tag, merge, or version files were changed.

## Files

- `backend/internal/controlplane/backup_rpo.go` — state/verified-object/lease models, strict fail-closed row parsing, acquire, renew, and unknown-outcome evidence resolution.
- `backend/internal/controlplane/backup_rpo_test.go` — recording-rqlite unit contract for SQL gates, fencing, strict numeric parsing, exact evidence reads, fixed redacted errors, and no mutation replay.
- `backend/internal/controlplane/backup_rpo_integration_test.go` — `rqlite_integration` coverage for first acquisition, renewal, database-time expiry boundary, takeover/node handoff, capability expiry, restore activation, and a committed unknown outcome.
- `.superpowers/sdd/2026-08-23-maestrovpn-ha-backup-production-adapters/task-3-report.md` — this report.

## TDD proof

### RED

The test-only patch was created in an external old/new mirror, formatted with the verified official Go 1.25.0 SDK, inspected, validated with `git apply --check --recount -p2`, and applied once.

Command (from `backend`, with `GOMAXPROCS=1`, `GOMEMLIMIT=512MiB`, and `GOTOOLCHAIN=local`):

```text
go test -p=1 ./internal/controlplane -run 'TestBackupRPO' -count=1
```

Expected failure:

```text
undefined: BackupRPOLeaseRequest
undefined: BackupRPOCapability
undefined: NewBackupRPOStore
undefined: BackupRPOPhaseDirty
FAIL .../internal/controlplane [build failed]
EXPECTED_RED: backup RPO production API is absent
```

### GREEN

Focused contract:

```text
go test -p=1 ./internal/controlplane -run 'TestBackupRPO' -count=1
ok  github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane  0.053s
```

Full untagged control-plane package:

```text
go test -p=1 ./internal/controlplane -count=1
ok  github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane  0.061s
```

Tagged integration compile without contacting a cluster:

```text
go test -p=1 -tags=rqlite_integration ./internal/controlplane -run '^$' -count=1
ok  github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane  0.045s [no tests to run]
```

The owner corrected a stale SDK path. The exact existing verified SDK used was `task15-go125-gofmt-6084f26/full-extracted/go/bin/go.exe` and its sibling `gofmt.exe`; `go version` reported `go1.25.0 windows/amd64`. No toolchain was downloaded. The previously verified archive SHA-256 was `89efb4f9b30812eee083cc1770fdd2913c14d301064f6454851428f9707d190b`.

## Lease and unknown-outcome proof

- Acquire sends one `Request(Linearizable, transaction=true)` statement. It is never retried or replayed.
- The only accepted job literal is exact `backup-rpo`.
- A first acquisition requires expected fence zero and returns fence one.
- An existing live lease cannot be replaced. An expired lease can be replaced only at `expires_at_unix<=unixepoch()` with the exact prior restore epoch and fence; the stored fence is incremented by one.
- Renewal updates only `expires_at_unix`; holder, token, restore epoch, fence, and capability identity must all match and the existing lease and capability must still be live.
- Acquire and renew both require the exact active `cluster_restore_state` epoch and matching `backup_rpo_state` epoch.
- Capability expiry must be after database now and cover the full requested TTL.
- Only an rqlite transport error marked `UnknownOutcome` enters evidence resolution. It performs exactly one `QueryLinearizable` call and no second mutation.
- The evidence predicate matches job, holder, token, restore epoch, expected resulting fence, capability generation, capability evidence SHA-256, capability expiry, active restore state, matching backup state, live lease, and live capability.
- No row, multiple rows, malformed data, a mismatched identity, or an evidence-read error returns the fixed redacted `controlplane: backup RPO lease outcome is unresolved` error.
- Known mutation failures and malformed mutation results return fixed redacted errors; SQL, object identity, holder/token, and raw backend errors are not exposed.

## Fail-closed parsing

The store accepts only signed Go integer types, canonical decimal strings, or canonical integral `json.Number` values. It rejects floating-point values, unsigned values, fractions, exponents, leading zeroes, missing values, invalid ranges, incomplete verified-object tuples, non-canonical digests/IDs, invalid object versions, inconsistent phases, stale lease epochs, and malformed lease/capability time relationships.

## Compatibility

- Migration `0005_backup_rpo.sql` remains canonical and unchanged; migrations v1-v4 are also untouched.
- The adapter uses the existing `rqlite.RQLite`, `Statement`, `Result`, `TransportError`, and recording-rqlite conventions.
- The change is additive inside `internal/controlplane`; no existing public HTTP/API shape is changed.
- Existing restore-epoch invalidation remains compatible because restore advancement already deletes `cluster_job_leases`.

## Self-review

Reviewed every production source line and the complete tagged integration test. Confirmed:

- one mutation request per acquire/renew path;
- no retry/replay helper around writes;
- one evidence read only after unknown outcome;
- exact full evidence identity and active epoch gates;
- all lease/capability decisions use SQLite `unixepoch()`;
- same-holder renewal preserves the fence;
- expired takeover increments exactly once;
- fixed redacted error surfaces;
- strict numeric and cross-field validation;
- protected `normalize.patch` and `.superpowers/sdd/2026-08-20-yandex-cdn-whitelist/task-4-report.md` were neither staged nor changed by this task.

`git diff --check` passed before staging.

## Skipped local checks

The owner's weak PC was not used to start a local three-voter rqlite cluster, run the race detector, or run repository-wide vet. The exact implementation SHA instead completed those checks in the existing GitHub Actions HA workflow.

## Risks

- The adapter intentionally fails closed on any rqlite numeric representation outside the canonical accepted set; a backend representation change would surface as a fixed unavailable error instead of being coerced.
- No unresolved implementation verification risk remains. The closeout commit changes this report only and does not alter the exact CI-tested source/test tree.

## CI evidence

- Exact implementation commit and verified remote branch SHA: `54428e3bfa75725dc54abc649b3162684136e929`.
- Remote ref: `origin/refs/heads/codex/yandex-cdn-whitelist-task3-sync`.
- Workflow: `HA control-plane checks`, push event, run 359 / ID `32658818922`: https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/32658818922
- Run head SHA: `54428e3bfa75725dc54abc649b3162684136e929` (exact match); conclusion `success`.
- Run interval: `2026-08-23T18:41:12Z` to `2026-08-23T18:43:05Z`.
- Job: `Go and isolated rqlite`, ID `97241732633`: https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/32658818922/job/97241732633 — conclusion `success`.
- Required successful steps: `Check materialized agent boundary`, `Check Go formatting`, `Test backend`, `Race-test backend`, `Vet backend`, `Test rqlite harness contract`, `Start isolated rqlite cluster`, and `Test rqlite integration`.
- Additional isolated-cluster steps, including `Start isolated three-node mTLS rqlite cluster` and the production importer mTLS proof, also completed successfully.

The follow-up closeout commit contains this report update only. Its remote SHA is verified in the final handoff rather than embedded here, avoiding an impossible self-reference while preserving exact-SHA CI proof for every production and test file.