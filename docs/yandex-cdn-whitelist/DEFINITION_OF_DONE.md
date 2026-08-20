# Definition of Done navigation

Status: target-only. This page is a navigation aid, not a completion assertion.

The authoritative acceptance list is `MASTER_REQUIREMENTS.md`, section 50, and it remains verbatim owner source. Completion requires all listed non-regression, isolated data-plane, CDN transport, entitlement, edge lifecycle, client compatibility, server-side metering, shadow billing, audit, backup/restore, documentation, and rollback evidence. In particular, no local documentation test satisfies a live gate. Follow `TEST_PLAN.md` for evidence categories, `TEST_RESULTS.md` for recorded outcomes, and `ROLLBACK.md` before a canary. Production cutover, real charging, OTA, restart, firewall/database/origin changes remain explicit owner stop gates.
