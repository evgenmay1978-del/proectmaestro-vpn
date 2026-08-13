# MaestroVPN HA Plan Coverage and Execution Index

**Status:** Утверждённая design-spec принята владельцем 09.08.2026 командой продолжить реализацию. Этот индекс фиксирует порядок repository implementation; он не является разрешением production deploy/import/DNS/TLS/bot/OTA mutations.

## Execution order

1. [Plan 01 — transactional foundation](2026-08-09-maestrovpn-ha-01-transactional-foundation.md)
2. [Plan 02 — business, API and import](2026-08-09-maestrovpn-ha-02-business-api.md)
3. [Plan 03 — outbox, agents and Telegram](2026-08-09-maestrovpn-ha-03-bots-agents.md)
4. [Plan 04 — operations, DNS, TLS and cutover](2026-08-09-maestrovpn-ha-04-operations-cutover.md)

Each task uses TDD and an atomic commit. A later task begins only after the preceding task is GREEN on the exact pushed SHA. Production remains `NO-GO (repository implementation only)` until every Task 18 gate and separate owner approval are present.

## Design-spec section coverage

| Spec section | Implemented/planned by |
|---|---|
| 1–4 goal, constraints, target architecture | Master plan plus every plan's Global Constraints |
| 5 data/schema | Plan 01 Task 3; Plan 02 Tasks 5–7; Plan 03 Tasks 10/13 |
| 6 transaction invariants | Plan 01 Tasks 1/3; Plan 02 Task 7 |
| 7 manual payment | Plan 02 Tasks 7/8; Plan 03 Task 13 |
| 8 Telegram | Plan 03 Task 13; Plan 04 Task 18 live-source/fence gate |
| 9 URL/API/OTA compatibility | Plan 02 Tasks 8/9; Plan 04 Task 18 |
| 10 outbox/reconciliation/agent | Plan 03 Tasks 10–12 |
| 11 fencing/S1 return | Plan 03 Tasks 10/11; Plan 04 Task 18 |
| 12 ingress/TLS/DNS/health | Plan 02 Tasks 5/9; Plan 04 Tasks 15–17 |
| 13 authorization/security | Plan 01 Task 4; Plan 02 Tasks 5/9; Plan 03 Tasks 11/13; Plan 04 Tasks 14–17 |
| 14 migration/cutover | Plan 02 Task 6; Plan 04 Tasks 15/18 |
| 15 rollback/restore epoch | Plan 04 Tasks 14/18 |
| 16 backup/RPO/RTO/observability | Plan 02 Task 5; Plan 03 Task 10; Plan 04 Tasks 14/18 |
| 17 mandatory tests | Matrix below and Plan 04 Task 18 exact-SHA workflow |
| 18 GO/NO-GO | Plan 04 Task 18 |
| 19 out of scope | Global Constraints in all plans |
| 20 late operational inputs | Plan 02 Task 6; Plan 03 Task 13; Plan 04 Task 18 |

## Mandatory test matrix

| # | Required evidence | Plan/task |
|---:|---|---|
| 1 | 100 confirms, saved restart result and zero replay-row secret | Plan 02 Task 7 |
| 2 | same key/different hash and receipt/provider-event uniqueness | Plan 02 Task 7 |
| 3 | two paid orders, confirm/cancel winner and receipt generation `>=` | Plan 02 Tasks 7/8 |
| 4 | cross-bot guard, unclaimed TTL and retained `payment_claimed` | Plan 01 Task 3; Plan 02 Task 7; Plan 03 Task 13 |
| 5 | leader kill before/after commit and unknown-outcome recovery | Plan 01 Tasks 1/2; Plan 02 Task 7 |
| 6 | one voter down, global no-quorum 503 and bounded stale `/sub` | Plan 01 Task 2; Plan 02 Task 9 |
| 7 | expiry worker crash/lease handoff/renew race and stale-fence rejection | Plan 02 Task 7 |
| 8 | bot crash at every poll/inbox/callback state and duplicate update | Plan 03 Task 13 |
| 9 | same-bot token rotation preserves offset/fence/callbacks/one poller | Plan 03 Task 13 |
| 10 | imported legacy/replacement callback and paid claim is not missed/doubled | Plan 02 Task 6; Plan 03 Task 13 |
| 11 | ambiguous Telegram delivery cannot repeat a business command | Plan 03 Task 13 |
| 12 | agent rejects stale epoch/incarnation/fence and no-quorum side effects | Plan 03 Tasks 10/11 |
| 13 | S1-down create/renew/expire/delete and exact catch-up/tombstones | Plan 03 Task 10; Plan 04 Task 18 |
| 14 | returned-S1 new lease-verifier identity works; old identity is denied | Plan 03 Task 11; Plan 04 Tasks 15/18 |
| 15 | S2 full-snapshot validate/fsync/swap/one reload/last-good rollback | Plan 03 Task 12 |
| 16 | Naive adoption reaches zero-unowned and preserves unrelated bytes | Plan 03 Task 12 |
| 17 | S3 olcRTC desired snapshot/grant removal/rollback without shell or SSH | Plan 02 Task 8; Plan 03 Task 12 |
| 18 | importer collision, full/delta batch resume/delete/final digest | Plan 02 Task 6 |
| 19 | settings/secret AAD/default-deny RBAC/session/missing-key readiness | Plan 01 Tasks 3/4; Plan 02 Task 5 |
| 20 | backup dirty watermark/node failover/cleanup/verified restore epoch | Plan 04 Task 14 |
| 21 | WB external-action crash boundaries and at-most-one POST per key | Plan 02 Task 8 |
| 22 | URL variants, S1 endpoint migration, APK fallback and log redaction | Plan 02 Task 9; Plan 04 Tasks 15/18 |
| 23 | DNS alternate paths/hysteresis/initial ambiguity/post-marker no-S1 rollback | Plan 04 Task 16 |
| 24 | TLS rollback, disk readiness and exact TV/API/OTA live canaries | Plan 02 Tasks 5/8/9; Plan 04 Tasks 17/18 |

## Review record

- Root self-review checked spec coverage, type ownership, exact file paths, RED/GREEN commands, production boundaries and vague-language risks.
- Independent backend/business review identified and closed admin/device/trial-salt/result-CAS/no-quorum gaps.
- Independent agent/bot review identified and closed S2 full-snapshot, tombstone-target, strong-fence, legacy-unit isolation, mTLS gateway, QR and live-source-gate gaps.
- No implementation code, production data or external service state is changed by these plans.
