# HA outbox fencing migration design

Status: approved for repository implementation. Production remains NO-GO.

## Context

The immutable `0001_control_plane.sql` already creates desired-state, outbox,
lease, receipt and tombstone tables. The verified DR history subsequently adds
`0002_restore_epoch.sql`. Rewriting either applied migration would invalidate
their checksums and the existing import/restore evidence.

## Decision

Task 10 will add a checksummed additive migration
`0003_outbox_fencing.sql`. It will extend the existing schema with durable
cluster epoch, node incarnation and monotonic lease fence data required by the
approved Plan 03 contracts. Existing rows receive fail-closed defaults and may
not authorize an apply until an audited activation writes a current
incarnation and fence.

The Task 10 API will use rqlite transactional requests and database time for
all desired-state, outbox, lease and receipt transitions. It will not modify
customer expiry from node observations, create a dual-write path, or perform
any production connection or deployment.

## Invariants

- `0001` and `0002` bytes and checksums remain unchanged.
- Desired generation never moves backward.
- The same generation with a different payload hash is a conflict.
- Lease handoff increments a durable fence; same-holder renewal preserves it.
- A receipt is accepted only for the current epoch, incarnation, live fence,
  generation and desired hash in one transaction.
- Reconciliation may recreate a missing unique outbox event from desired
  state, but node state never advances business truth.
- S1 remains a required, fenced, non-retired desired target until a separately
  audited permanent-retirement command.
- Tombstones remain until every required target acknowledges and retention
  conditions pass.

## Delivery and verification

Implementation follows RED/GREEN TDD. RED tests cover generation rollback,
same-generation hash conflict, lease fence handoff, stale receipt rejection,
missing-outbox repair and required-target tombstone retention. GREEN and race
tests run in GitHub Actions on the exact pushed SHA. This design authorizes
repository code only; production import, service changes and traffic changes
remain separately gated.
