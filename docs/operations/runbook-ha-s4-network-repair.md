# MaestroVPN S4 network repair runbook

**Status:** **PRODUCTION NO-GO**

Production remains **PRODUCTION NO-GO** until an `EVIDENCE_COMPLETE` package,
trusted UTC, console recovery, and fresh S4 read-only preflight are all
confirmed together with every repository-authority and Task 6 condition below.
The fixed envelope is target: `s4`; selected owner: `systemd-networkd`; scope:
`REMOVE_CONFLICTING_IFUPDOWN_PRIMARY_OWNERSHIP_ONLY`.

Every package retains `apply_supported: false` and
`mutation_authorized: false`. The package is evidence for a later bounded
operator decision; it never executes a change or grants execution authority.

The dated authority amendment removes only the additional chat-reply pause
after every gate is GREEN and the full declaration is emitted. It does not
bypass a gate, does not embed execution authority in the package, and does not
expand the scope.

This checked-in runbook contains only semantic review identifiers. Concrete
production paths, current contents, mutation steps, affected backup bytes, and
restore steps belong only in a protected local command sheet outside Git.

## Repository authority and Task 6 gate

Production remains **PRODUCTION NO-GO** until all of these conditions are
independently evidenced:

- S4 repository implementation is complete;
- durable handoff is complete;
- scoped local verification is GREEN;
- canonical branch is pushed and its remote SHA equals the local SHA;
- exact-SHA GitHub CI is GREEN for that SHA;
- detached exact-SHA docs, manifest, and diff verification is GREEN;
- dedicated S4 workflow and every required canonical-branch workflow are GREEN
  for that exact SHA;
- independent review reports `0 Critical / 0 Important / 0 Minor`;
- fresh bounded S4 raw capture was reviewed before canonical inventory
  derivation;
- fresh unchanged inventory and exact package digest were reviewed;
- newly generated `EVIDENCE_COMPLETE` package was reviewed; and
- every Task 6 package and stop gate is GREEN, and rollback is executable.

Until every condition in this section is evidenced, this standalone runbook
does not permit Gate 1, declaration, Gate 2, or semantic execution. Repository
completion is a prerequisite; the runbook cannot be used to enter the live
sequence early.

## Evidence capture

Create a protected bounded raw capture outside Git. The capture is read-only,
bounded, and limited to the facts needed by the canonical S4 inventory. An
operator or owner reviews the raw capture before canonical inventory is
derived. `source_review_completed: true` may be set only after that review.
Raw capture bytes remain outside Git, package output, and ordinary reports.

The reviewed capture must establish the exact current facts for:

- active and enabled `systemd-networkd` ownership of the primary interface and
  default route;
- the conflicting `ifupdown` primary-interface and default-route declaration,
  including its reviewed unit-state evidence;
- management reachability, VPN-unit health, expected VPN listeners, and the
  default route;
- independent console access, the reviewed recovery procedure, and the
  independent second operator; and
- the inventory capture and expiry UTC values.

Any ambiguous, incomplete, stale, unreviewed, or changed fact stops the window.
Derive a new canonical inventory only after a new bounded capture and review.

## Package generation

Use only the repository package generator and an explicit evaluation time from
a trusted UTC source:

```text
python ops/ha/s4-network-change-package.py package --inventory PATH --evaluation-time 2026-08-31T12:00:00Z --output PATH
```

The exit meanings are exact:

- `0`: publishes a canonical `EVIDENCE_COMPLETE` package;
- `2`: publishes a canonical `BLOCKED` package; and
- `3`: invalid, stale, unsafe, or system input; no final output is created.

Exit `2` is durable blocker evidence, not a request to continue. Exit `3`
requires corrected or freshly captured input; no package is available for a
declaration.

## Package review

Review the package as a new protected artifact. Require its exact schema,
status, inventory timestamps, digest, owner, conflict manager, scope, semantic
identifier arrays, blockers, and the two false authority fields. A blocker,
unknown field, changed order, stale timestamp, or digest mismatch stops the
window.

`inventory_sha256` is integrity evidence, not a secrecy mechanism. Do not
invoke `build_manifest`, `pki_verify`, `deploy_node`, or `verify_backup` against
digest-only evidence. Those tools require their own typed source evidence and
must not infer content or authority from this digest.

`EVIDENCE_COMPLETE` records schema and evidence completeness only. It does not
prove that S4 changed, that a reboot is safe, that live state is unchanged, or
that a customer cutover can proceed.

`ifup_unit_failed: true` and `networking_unit_failed: true` are reviewed
conflict evidence for this exact repair. They must not be rewritten merely to
make the package look healthy. The bounded health comparison instead covers
management reachability, default-route presence, VPN-unit health, listener
presence, and `no_new_failed_units`.

### `precheck_ids`

- `inventory_reviewed`
- `networkd_working_owner`
- `ifupdown_conflict_confirmed`
- `management_vpn_health_green`
- `console_recovery_ready`

## Gate 1 — independent trusted UTC and declaration

Perform the first independent trusted-UTC comparison immediately before the
declaration activates standing authorization. Compare independent UTC with the
package inventory expiry. An expired, uncertain, unavailable, or mismatched
comparison stops the window and requires fresh capture, review, inventory, and
package generation.

Before emitting the declaration, require fresh unchanged inventory, no
concurrent work, an independent second operator, working independent console
recovery, protected affected-file and unit-state backups, and before-state
health capture. Confirm that the protected local command sheet matches only the
reviewed semantic scope and rollback identifiers below.

The declaration envelope binds the exact S4 target, package digest, named
operator, bounded UTC window, expected impact, verified preconditions,
protected rollback-sheet identity, protected affected-file backup identity,
protected unit-state backup identity, before-state management, default-route,
VPN-unit, VPN-listener, and failed-unit health evidence, the immediate rollback
path from the protected rollback sheet, and all stop gates. It also records the
second operator and the first UTC comparison. Missing or non-GREEN declaration
data stops the window.

## Gate 2 — independent trusted UTC before execution

Perform the second independent trusted-UTC comparison immediately before
execution. This is a new comparison, not a reuse of the declaration timestamp.
Reconfirm the package is unexpired, inventory is unchanged, console recovery
works, the second operator remains available, no concurrent work began, the
rollback sheet and backups retain their protected identities, before-state
health remains GREEN, and every stop gate remains clear.

An expired, uncertain, unavailable, or mismatched second comparison terminates
the maintenance window. A later attempt requires a new protected bounded raw
capture, a new operator or owner review of those raw bytes, a fresh canonical
inventory, and a newly generated and reviewed package. The prior declaration
and first comparison cannot be reused for that later attempt.

No semantic step begins if the second comparison or any reconfirmation fails.

## Semantic change scope

The repair is S4 only. It must backup and restore only the conflicting
ifupdown declaration and unit state, preserve active and enabled
`systemd-networkd` ownership, and preserve the default route, management
access, VPN units, and VPN listeners.

Only the following semantic change steps are reviewed. Their concrete local
implementation remains in the protected command sheet.

### `change_step_ids`

- `backup_ifupdown_state`
- `remove_ifupdown_primary_declaration`
- `disable_ifupdown_boot_ownership`
- `preserve_systemd_networkd`

### `stop_gate_ids`

- `trusted_utc_expired`
- `console_unavailable`
- `inventory_drift`
- `unexpected_network_owner`
- `prechange_health_degraded`
- `unexpected_command_result`
- `route_or_listener_loss`
- `fresh_management_session_failed`

## Validation

Compare the bounded before-state capture with the after-state evidence using
only the reviewed validation identifiers. Establish a fresh independent
management session before the original session is closed. The original session
stays open until the new session, route, ownership, VPN units, VPN listeners,
and failed-unit comparison are all independently verified.

### `validation_ids`

- `single_primary_network_owner`
- `networkd_active_enabled`
- `default_route_preserved`
- `fresh_management_session_established`
- `vpn_units_listeners_preserved`
- `no_new_failed_units`

## Rollback

Immediate rollback begins on owner drift, route or listener loss, unhealthy VPN
units, console loss, unexpected command result, failed fresh management
session, or any other active stop gate. Stop further semantic change steps.

Rollback must restore only the conflicting ifupdown declaration and unit state
from the protected narrow backup, restore the reviewed unit-state ownership,
and repeat the same S4 health validation. It must not improvise another network
owner or expand into another service. A rollback failure ends the window and is
recorded as unresolved evidence.

### `rollback_ids`

- `restore_ifupdown_primary_declaration`
- `restore_ifupdown_unit_state`
- `repeat_s4_health_validation`

## Evidence recording

Retain the exact package digest, both independent UTC comparisons, declaration
envelope, named operators, protected rollback-sheet identity, semantic IDs
attempted, before/after health comparison, fresh-session result, and any stop
or rollback outcome. Record stable redacted evidence only. Do not place raw
capture bytes, current contents, command-sheet contents, addresses,
credentials, keys, customer identifiers, or live configuration in Git, the
package, or ordinary reports.

## Exclusions

S4 only is in this runbook. Android/TV remains immutable at `1.0.157`.
The following remain unchanged and outside this scope: S1-S3; DNS/CDN; bots;
payments; customer data; VPN/firewall/listeners; install; restart; reload;
reboot; release; signing; OTA; matrix; PKI; rqlite; shadow traffic; final
cutover; OLCRTC; WDTT; and backups outside this narrow rollback.
