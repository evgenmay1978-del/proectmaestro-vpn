# Task 1 Report: Freeze migration v5 and upgrade invariants in RED tests

## Status

RED complete. Task 2 is not implemented.

## Summary

Task 1 freezes the migration-v5 contract as failing tests before production implementation. The tests require the exact v1-v5 migration chain, an exact v4-to-v5 upgrade, a v5 no-op, the complete v5 state/attempt/lease schema, the canonical initial state row, and local SQL constraints. The production schema remains at `SchemaVersion=4`, so the targeted suite is expected to stay RED until Task 2 implements migration v5.

## Files changed

Exactly three Go test files were changed:

- `backend/internal/controlplane/migrations_ordered_test.go`
- `backend/internal/controlplane/migrations_test.go`
- `backend/internal/controlplane/schema_constraints_test.go`

## Contract frozen by the tests

- Exact ordered migration chain v1 through v5.
- An existing v4 database applies only migration v5.
- An existing v5 database is a no-op.
- Complete v5 columns for restore state, attempts, and leases.
- Canonical seed state: singleton id 1, dirty generation 1, verified generation 0, phase `dirty`, last attempt sequence 0, and all verified-object fields null.
- Attempt sequencing is unique per restore epoch with `UNIQUE(restore_epoch, attempt_sequence)`.
- Table-driven local constraint coverage includes singleton identity, tuple completeness, canonical lowercase SHA-256 values, generation ordering, phase enums, per-epoch sequence uniqueness, version-value rules, and zero/negative rejection for positive fields.
- Cross-row semantics are intentionally outside the SQL constraint test boundary.

## Evidence

| Evidence | Result |
| --- | --- |
| Task 1 RED-test commit `6084f26942a377858b543e64dc12fbd41e84dad0` | Added the planned migration-v5 RED tests. |
| Formatting-only commit `15d4dee880e7b54e6a9d3938614b201112baa33a` | Applied `gofmt`; closeout later restores only the two accepted legacy formatting differences in `migrations_test.go` without changing test semantics because the current OAuth credential cannot update workflows. |
| GitHub Yandex run `32643510220` | Formatting passed; the run failed at `Test Task 7 Go packages`, as expected for the missing v5 implementation. |
| Targeted local Go 1.25.0 test | `cd backend && go test ./internal/controlplane -run '^TestOrderedMigrationsExposeExactChain$' -count=1` compiled successfully under single-core and memory-limited settings, then failed only because `SchemaVersion=4` and migration v5 is absent. |
| RED wrapper | Confirmed exactly `EXPECTED_RED_CONFIRMED=SchemaVersion4_missing_v5`. |
| GitHub HA run `32643510264` | Failed because `migrations_test.go` had become canonical while still listed as accepted legacy debt; the credential lacks workflow scope, so the formatting-only change for that file was reversed and the existing fail-closed workflow contract remains unchanged. |

The local toolchain was acquired from the official `go1.25.0.windows-amd64.zip` archive. Its official SHA-256 is `89efb4f9b30812eee083cc1770fdd2913c14d301064f6454851428f9707d190b`. Verification used `GOMAXPROCS=1` and a memory limit to fit the available machine.

## Boundaries

- Task 2 production migration-v5 implementation has not started as part of this task and is not claimed here.
- No production code was changed by Task 1.
- `normalize.patch` and `.superpowers/sdd/2026-08-20-yandex-cdn-whitelist/task-4-report.md` were untouched.
- This report is not committed or pushed by this action.
