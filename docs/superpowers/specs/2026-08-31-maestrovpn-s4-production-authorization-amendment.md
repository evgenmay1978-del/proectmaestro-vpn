# MaestroVPN S4 production authorization amendment

**Date:** 2026-08-31
**Status:** repository authority record; **PRODUCTION NO-GO**

## Decision and precedence

The owner's later explicit standing instruction removes only the need to wait
for one more chat reply after every applicable S4 gate is GREEN. It does not
weaken, replace, or bypass the durable repository authority, the approved S4
design, or the individual evidence and stop-gate requirements.

The standing instruction applies only after this amendment, the S4
implementation, the handoff, exact-SHA GitHub CI, and independent review are
complete. Until then, and until all Task 6 gates pass, production remains
**PRODUCTION NO-GO**.

## Required authority before any S4 mutation

Before each production mutation, the acting operator must make and retain a
pre-mutation declaration that binds all of the following exact values:

- target: `S4`;
- the immutable S4 change-package digest;
- named operator and independent second operator;
- bounded UTC maintenance window and the expected impact;
- verified preconditions and the before-state health evidence;
- identity of the protected rollback sheet and protected backups; and
- every applicable stop gate and the immediate rollback path.

The declaration is an execution record, not a replacement for the package.
The package's `apply_supported: false` and `mutation_authorized: false` values
remain authoritative for the repository artifact: standing owner authorization
is external execution authority and is never embedded artifact authority.

## Gates that remain independent and mandatory

The following conditions must all be GREEN and evidenced without inference:

1. Two independent trusted-UTC comparisons: one when accepting the fresh
   package/inventory and a second immediately before mutation. Each compares
   the package inventory expiry with an independent trusted UTC source; stale,
   uncertain, or mismatched time stops the window and requires fresh evidence.
2. Fresh, unchanged reviewed inventory and the exact package-digest binding.
3. Tested independent console access, a ready second operator, no concurrent
   work, protected affected-file and unit-state backups, and captured
   before-state management, route, VPN-unit, and listener health.
4. The narrow approved scope only: remove conflicting `ifupdown` primary
   interface/default-route ownership while preserving active, enabled
   `systemd-networkd` ownership.
5. A fresh management session after the bounded change; an immediate rollback
   from the protected rollback sheet on any stop gate, unexpected command
   result, route/listener loss, degraded health, or failed fresh session.

Rollback restores only the changed `ifupdown` declarations and unit state,
then repeats the health comparison. A rollback failure stops further work; it
does not authorize an improvised network-manager change.

## Exclusions preserved by this amendment

This amendment does not authorize OLCRTC, WDTT, real customer charges,
OTA/release publication or signing, destructive actions, final customer-traffic
cutover, S1-S3 work, DNS/CDN/firewall mutation, backup/restore execution, PKI
deployment, rqlite deployment, shadow traffic, or any operation outside the
single declared S4 window. Reboot safety, customer readiness, and live
production readiness are not inferred from an `EVIDENCE_COMPLETE` package.

No mutation proceeds when any required evidence, trusted-time comparison,
console path, operator, backup, health check, declaration field, stop gate,
exact-SHA CI result, or independent review is missing or non-GREEN.
