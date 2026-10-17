# Definition of Done navigation

Status: target-only. This page is a navigation aid, not a completion assertion.

The authoritative acceptance list remains the byte-for-byte owner source in `MASTER_REQUIREMENTS.md`. Its `50. DEFINITION OF DONE` delimiter is plain source text rather than a Markdown heading, so it intentionally has no generated section anchor; locate it by exact-text search in the master file. This navigation page therefore links only to the master file, not to a fabricated anchor.

Completion requires the master list's non-regression, isolated data-plane, CDN transport, entitlement, edge lifecycle, client compatibility, server-side metering, shadow billing, audit, backup/restore, documentation, and rollback evidence. No local documentation test satisfies a live gate. Follow `TEST_PLAN.md` for evidence categories, `TEST_RESULTS.md` for recorded outcomes, and `ROLLBACK.md` before a canary. Production cutover, real charging, OTA, restart, firewall/database/origin changes remain explicit owner stop gates.