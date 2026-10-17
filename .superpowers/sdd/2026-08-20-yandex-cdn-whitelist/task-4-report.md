## Task 4 report

### Design/API

Added `backend/internal/shadowbilling`, a pure value-in/value-out metering core. `Apply` accepts prior `State`, a cumulative per-Xray-user `UsageEvent` (instance, epoch, opaque identity, stable event ID), and a policy. It clones state, deduplicates events, baselines new epochs/resets, creates positive-delta immutable interval/ledger values, and returns only an entitlement suspension recommendation.

Price resolution is individual > tariff > profile > global. Paid pricing without a configured price errors; free mode is explicit. Amounts are exact rational minor-unit strings plus integer denominator (`math/big`, never float). Each ledger entry snapshots unit, basis, included/soft/hard/grace limits, and resolved price. No balance, wallet, API, callback, ordinary VPN, or production/network code exists in the package; thus real balance mutation and ordinary-VPN mutation are impossible here.

### Files

- `backend/internal/shadowbilling/metering.go`
- `backend/internal/shadowbilling/metering_test.go`
- this report

### TDD evidence

RED: `go test ./internal/shadowbilling` failed as expected with undefined `Policy`, `UsageEvent`, and `NewState` before production code existed.

GREEN: `go test ./internal/shadowbilling` passed. Related `go test ./internal/controlplane ./internal/subgen` passed. `go vet ./internal/shadowbilling` passed. `gofmt` was run on both new Go files.

### Self-review and concerns

Tests hand-check baselines, positive deltas, replay, reset/epoch safety, included-once behavior, precedence, explicit free mode, tariff snapshots, soft limit, and entitlement-only hard-limit recommendation. This task intentionally supplies no persistence/API seam, balance posting, reconciliation service, or production wiring. Commit SHA: pending.
