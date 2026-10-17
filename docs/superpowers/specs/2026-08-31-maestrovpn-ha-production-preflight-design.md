# MaestroVPN S4 Network Repair Change-Package Design

**Date:** 2026-08-31 — **Status:** DRAFT FOR OWNER REVIEW — **Scope:** repository-only — **Production:** **PRODUCTION NO-GO**

## 1. Decision

Implement one narrow first slice: deterministic, fail-closed tooling and a
runbook that turn reviewed, redacted S4 network observations into a protected
S4 repair change package. It has no mutation or `apply` mode.

The selected steady-state network owner is `systemd-networkd`. The 2026-08-30
read-only audit shows that it currently owns the working primary interface and
default route. Enabled ifupdown independently declares the same interface and
gateway, causing the degraded service state. The later repair window therefore
removes or disables only the conflicting ifupdown ownership and preserves the
working `systemd-networkd` configuration.

This document does not approve that repair. Repository implementation and CI
come first, followed by owner review and a separate explicit approval for one
console-recoverable S4-only window.

## 2. Why this is the first slice

S4 double ownership is the first concrete blocker. A generic signed
seven-envelope/eight-key aggregator is rejected: it adds key distribution,
cannot revalidate facts hidden by digests and delays the repair. Later gates are
context only; this slice has no matrix/S3 schema, generic signing, backup,
artifact/GitHub, PKI, rqlite or shadow implementation. Status is only `BLOCKED`
or `EVIDENCE_COMPLETE`, never GO.

## 3. Preserved boundaries

- Android/TV remains `1.0.157`; OLCRTC and WDTT remain frozen.
- No S1-S3, DNS/CDN, bot, payment, customer, VPN, release/signing or OTA mutation.
- No install, manager/unit change, restart, reload, reboot, firewall or listener
  action occurs in the repository slice.
- The tool accepts no credential, key, token, fingerprint, customer datum or
  endpoint literal.

## 4. Repository deliverables for the later implementation plan

Deliverables are one `ops/ha` package parser/CLI, focused tests,
`docs/operations/runbook-ha-s4-network-repair.md`, and read-only exact-SHA CI
with self-policy tests. No matrix interface or real operational artifact ships.

## 5. CLI and authority boundary

The wrapper supports only `package` with inventory, deterministic evaluation
time and new output. Exact-SHA provenance is external CI evidence, not input.

`package` only reads one bounded local input and creates one new local output.
`matrix`, `apply`, `repair`, `disable`, `restart`, `rollback`, aliases, unknown
flags and extra positionals fail before input is opened. The implementation has
no network, GitHub, object-storage or process-execution client and invokes no
shell command.

Output is a new protected mode-`0600` artifact, fsynced and atomically published
under a mode-`0700` directory; streams expose fixed codes only.
`EVIDENCE_COMPLETE` is schema completion only; authority booleans stay false.
Exit codes are `0=EVIDENCE_COMPLETE`, `2=BLOCKED` for valid inputs with named
blockers, and `3=invalid/system` with no output.

## 6. Bounded S4 inventory input

Input schema `maestro-ha-s4-network-inventory-v1` is canonical JSON with exact
top-level fields `schema`, `captured_at_utc`, `expires_at_utc`, `node_id`,
`evidence_class`, `source_review_completed`, `networkd`, `ifupdown`, `health`
and `console`. The four nested objects contain only these booleans:

- `networkd`: `active`, `enabled`, `owns_primary_interface`, `owns_default_route`;
- `ifupdown`: `enabled`, `declares_primary_interface`,
  `declares_default_route`, `ifup_unit_failed`, `networking_unit_failed`;
- `health`: `management_reachable`, `vpn_units_healthy`,
  `expected_vpn_listeners_present`, `default_route_present`;
- `console`: `independent_access_confirmed`, `recovery_procedure_reviewed`,
  `second_operator_available`.

Fixed values are `node_id: s4` and `evidence_class: PRODUCTION_READ_ONLY`.
Times are explicit UTC seconds; evidence is fresh only when
`captured_at_utc <= evaluation_time < expires_at_utc` and the interval is no
longer than 15 minutes. Evaluation time is explicit for deterministic output.

The inventory intentionally contains no IP, hostname, interface name, address,
route, gateway, port, fingerprint, unit output or file content. The owner must
review the bounded raw capture before deriving this inventory. That raw capture
and the concrete repair command sheet remain protected local artifacts outside
Git and ordinary reports.

The SHA-256 of canonical inventory bytes is an integrity reference only. It is
not a secrecy mechanism: unsalted hashes can disclose low-entropy values by
enumeration, so sensitive raw values are never replaced with bare hashes in a
publishable artifact.

The input is a bounded regular, single-link, non-symlink file owned by the
invoking UID with mode `0600`. Duplicate/unknown fields, wrong types,
noncanonical JSON, stale time, races or broad permissions return code `3` and
create no output.

## 7. S4 change-package output

Output schema `maestro-ha-s4-network-change-package-v1` has exact fields
`schema`, `status`, `inventory_sha256`,
`inventory_captured_at_utc`, `inventory_expires_at_utc`, `selected_owner`,
`conflicting_manager`, `change_scope`, `precheck_ids`, `change_step_ids`,
`stop_gate_ids`, `validation_ids`, `rollback_ids`, `blockers`,
`apply_supported` and `mutation_authorized`. Owner is `systemd-networkd`,
conflict is `ifupdown`, scope is
`REMOVE_CONFLICTING_IFUPDOWN_PRIMARY_OWNERSHIP_ONLY`, and both authority
booleans are false.

The output contains stable semantic step IDs, never executable commands. It is
`EVIDENCE_COMPLETE` only when the reviewed inventory proves the exact audited
state: `systemd-networkd` owns the working primary interface/default route,
ifupdown declares the conflicting ownership, management and current VPN health
are intact, and independent console recovery is ready. Any different or
ambiguous ownership is `BLOCKED`; the tool does not generalize a new repair.

The package never recommends removing, disabling or rewriting
`systemd-networkd`, changing the working address or default route, touching
loopback/unrelated interfaces, rebooting, or modifying firewall/VPN services.

It contains only `inventory_sha256`; evaluator provenance is external exact-SHA
CI evidence. Later candidate artifact and template identities remain distinct
from each other and the evaluator. Current pinned commits remain existing
planner anchors only, not future candidates.

## 8. Checked-in repair runbook

The runbook defines review gates and semantic actions. Exact production paths,
commands, current file contents and recovery commands belong only in the
protected concrete command sheet reviewed for the approved window.

### Preconditions

- Fresh package, unchanged inventory and tested independent console.
- Second operator, no concurrent work, protected affected-file/unit-state
  backups and a before-state health capture.
- Immediately before explicit same-turn owner approval, compare
  `inventory_expires_at_utc` with an independent trusted UTC clock. Stale or
  uncertain time means stop and repackage. Approval names S4, package digest,
  operator, window, stop gates and rollback sheet.

### Change scope

Only the conflicting ifupdown declaration and boot ownership for the primary
interface/default route may be removed or disabled. `systemd-networkd` remains
active and enabled. No reboot, firewall change, listener change or unrelated
configuration cleanup is bundled into the window.

### Immediate stop gates

- Immediately before execution repeat the trusted-UTC comparison; stale means
  stop. Deterministic `evaluation_time` is never execution freshness authority.
- Stop for unavailable console, inventory drift, wrong owner, degraded
  management/VPN health or an unrelated target in the command sheet.
- Stop for an unexpected command result, route change, lost listener or failure
  to establish a fresh management session.

### Validation and rollback

Validation proves one owner, healthy network services, preserved working route,
fresh management session, preserved VPN units/listeners and no new failed unit.
It does not reboot S4 or infer reboot safety.

Rollback is operator-driven from the independent console using the protected
backups and reviewed commands. It restores only the changed ifupdown declarations
and unit states, then repeats the health comparison. Rollback failure stops all
further work; it does not trigger an improvised network-manager switch.

Current validators are reused only when their original raw inputs are available.
`build_manifest`, `pki_verify`, `deploy_node` and `verify_backup` are not invoked
on digest-only envelopes and do not participate in S4 network evaluation.

## 9. Tests and exact-SHA CI

Focused tests cover strict schemas/bounds and file races; deterministic output;
every ownership, health, console and freshness blocker; fixed
`systemd-networkd` selection; absent mutation/network/subprocess surfaces;
sensitive-output scans; caller-supplied tooling-SHA and `matrix` rejection; both
independent-clock runbook gates; and digest handling without a secrecy claim.

A dedicated Ubuntu workflow uses pinned actions, `contents: read`, no
environment, no secrets, no production values, no artifact upload, bounded
timeouts and unconditional temp cleanup. It runs unit tests, Python compilation,
wrapper help/negative CLI contracts, workflow self-policy, a sensitive-literal
scan and `git diff --check`. Synthetic fixtures use only invented boolean states
and opaque channel IDs.

Completion requires exact pushed-SHA jobs and review with zero Critical or
Important findings. Other-SHA/local/synthetic success is not production evidence.

## 10. Sequence after this repository slice

1. Owner reviews the written spec and exact-SHA repository implementation.
2. Owner gives explicit same-turn approval for the S4 repair only.
3. Operators execute the protected console-recoverable S4 repair window.
4. Obtain authoritative out-of-band S3 identity before trusting S3 evidence or
   mutating S3.
5. Establish the separately approved shadow topology through the later backup,
   artifact/GitHub-control and real-PKI/bootstrap/join gates.
6. Only after that topology exists, separately design and review a checked-in
   closed east-west runtime policy and read-only matrix tooling tied to it.
7. Request and execute matrix collection under its own authority boundary.

Steps 2-7 are outside this implementation slice. Each production mutation has
its own exact approval and rollback boundary.

## 11. Rejected alternatives

- **Generic aggregator:** adds key custody while digests cannot prove hidden
  facts; use one raw-input validator.
- **Mutating auto-repair:** makes stale input operationally dangerous; keep the
  window console-operated.
- **Switch to ifupdown:** replaces the working owner; preserve
  `systemd-networkd` and remove only the conflict.

## 12. Acceptance boundary

This draft claims no implementation, repair or later prerequisite.
`PRODUCTION NO-GO`; S1-S4, Android/TV 1.0.157, OLCRTC, WDTT, DNS/CDN, bots, payments, customers, release/signing and OTA remain unchanged.
