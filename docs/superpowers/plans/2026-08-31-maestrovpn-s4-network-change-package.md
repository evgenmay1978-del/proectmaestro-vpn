# MaestroVPN S4 Network Change-Package Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use `subagent-driven-development` to implement this plan task-by-task, `test-driven-development` for each behavior change, and `verification-before-completion` before any completion claim.

**Goal:** Build a fail-closed, repository-only generator for the reviewed S4 network repair change package. It converts one canonical, fresh, read-only S4 inventory into deterministic canonical JSON with either `EVIDENCE_COMPLETE` or `BLOCKED`; it never connects to S4 and never mutates a server.

**Architecture:** A pure Python core validates canonical JSON, trusted UTC evidence, exact S4 ownership/health facts, and deterministic semantic ordering. A thin CLI reads one locally supplied inventory through a race-resistant boundary and publishes one new output through a no-clobber atomic boundary. A dedicated GitHub workflow tests only this inert package generator and enforces its own no-secrets/no-network/no-production-mutation policy. The checked-in runbook keeps the later human-reviewed repair bounded to removing conflicting ifupdown primary ownership while preserving `systemd-networkd`.

**Tech Stack:** Python 3 standard library, `unittest`, GitHub Actions on `ubuntu-24.04`, Markdown runbooks, existing repository baseline-manifest tooling.

**Authority boundary:** This plan does not authorize or implement SSH, console access, network mutation, service restart, firewall change, HA matrix work, S3 work, backup/restore execution, artifact installation, PKI deployment, rqlite deployment, shadow traffic, customer cutover, OLCRTC, or WDTT. Every generated package has `apply_supported: false` and `mutation_authorized: false`.

**Protected owner files:** Do not edit, stage, restore, or delete the dirty files listed in `CONTEXT_HANDOFF.md` and the active handoff. Stage only the exact files named by each task.

## Global Constraints

- The approved design remains authoritative except for the dated owner-authorization amendment created in Task 0. That amendment replaces only an additional chat-reply pause; it preserves every evidence, trusted-time, console, second-operator, rollback, stop-gate, declaration, exact-SHA CI, and review requirement.
- Implementation is repository-only and fail-closed. It imports no network/process client, invokes no subprocess or shell, and accepts no endpoint, credential, token, key, fingerprint, customer datum, production path, executable command, or caller-supplied tooling SHA.
- `EVIDENCE_COMPLETE` means schema/evidence completeness only. It never proves that S4 was changed, that reboot is safe, that customer traffic is ready, or that mutation authority is embedded in the artifact.
- The Windows checkout is for bounded edits/static tests only. POSIX filesystem guarantees and full suites run on GitHub `ubuntu-24.04` against the exact pushed SHA.
- Capture the complete initial dirty set before every task. Never hash protected dirty bytes into a new baseline and never stage a path outside the task's declared `Produces` set.
- Every task follows RED → minimal implementation → GREEN → self-review → exact-path commit. Independent review and exact-SHA GitHub GREEN are mandatory before live inventory.

---

## Contract frozen by the approved design

The implementation must preserve these exact public interfaces:

```python
def canonical_bytes(value: object) -> bytes: ...
def parse_inventory(raw: bytes, *, evaluation_time: datetime) -> dict[str, object]: ...
def evaluate_inventory(
    inventory: Mapping[str, object],
    *,
    inventory_sha256: str,
) -> dict[str, object]: ...
def read_inventory(
    path: Path | str,
    *,
    evaluation_time: datetime,
) -> tuple[dict[str, object], str]: ...
def publish_change_package(output: Path | str, encoded: bytes) -> None: ...
def run(
    argv: Sequence[str] | None,
    stdout: TextIO,
    stderr: TextIO,
) -> int: ...
def main(argv: Sequence[str] | None = None) -> int: ...
```

The only accepted CLI shape is:

```text
python ops/ha/s4-network-change-package.py package \
  --inventory PATH \
  --evaluation-time 2026-08-31T12:00:00Z \
  --output PATH
```

Exit codes are fixed:

- `0`: canonical package created with status `EVIDENCE_COMPLETE`.
- `2`: canonical package created with status `BLOCKED`.
- `3`: invalid CLI, invalid/stale/unsafe input, unsafe output boundary, or system error; no final output is created.

The inventory exact-key/type contract is:

```python
INVENTORY_KEYS = {
    "schema", "captured_at_utc", "expires_at_utc", "node_id",
    "evidence_class", "source_review_completed", "networkd",
    "ifupdown", "health", "console",
}
NETWORKD_KEYS = {
    "active", "enabled", "owns_primary_interface", "owns_default_route",
}
IFUPDOWN_KEYS = {
    "enabled", "declares_primary_interface", "declares_default_route",
    "ifup_unit_failed", "networking_unit_failed",
}
HEALTH_KEYS = {
    "management_reachable", "vpn_units_healthy",
    "expected_vpn_listeners_present", "default_route_present",
}
CONSOLE_KEYS = {
    "independent_access_confirmed", "recovery_procedure_reviewed",
    "second_operator_available",
}
```

`schema`, both UTC fields, `node_id`, and `evidence_class` are strings. `source_review_completed` and every nested value are exact booleans (`type(value) is bool`). Fixed inventory values are `maestro-ha-s4-network-inventory-v1`, `s4`, and `PRODUCTION_READ_ONLY`.

`MAX_INVENTORY_BYTES = 16_384`. A real valid canonical inventory must pass at its natural size below the bound. Test the bounded-reader helper separately with a mocked parser accepting exactly `16_384` arbitrary bytes; reject `16_385` bytes before parsing; reject empty input; and reject a valid canonical inventory plus any trailing byte as noncanonical.

The output has exactly these fields and no others:

```json
{
  "schema": "maestro-ha-s4-network-change-package-v1",
  "status": "EVIDENCE_COMPLETE",
  "inventory_sha256": "64-lowercase-hex",
  "inventory_captured_at_utc": "2026-08-31T11:50:00Z",
  "inventory_expires_at_utc": "2026-08-31T12:05:00Z",
  "selected_owner": "systemd-networkd",
  "conflicting_manager": "ifupdown",
  "change_scope": "REMOVE_CONFLICTING_IFUPDOWN_PRIMARY_OWNERSHIP_ONLY",
  "precheck_ids": [],
  "change_step_ids": [],
  "stop_gate_ids": [],
  "validation_ids": [],
  "rollback_ids": [],
  "blockers": [],
  "apply_supported": false,
  "mutation_authorized": false
}
```

Use this exact semantic order everywhere:

```python
BLOCKER_ORDER = (
    "source_review_incomplete",
    "networkd_inactive",
    "networkd_disabled",
    "networkd_not_primary_owner",
    "networkd_not_default_route_owner",
    "ifupdown_disabled",
    "ifupdown_primary_declaration_absent",
    "ifupdown_default_route_declaration_absent",
    "ifup_unit_state_drift",
    "networking_unit_state_drift",
    "management_unreachable",
    "vpn_units_unhealthy",
    "vpn_listeners_missing",
    "default_route_missing",
    "console_access_unconfirmed",
    "recovery_procedure_unreviewed",
    "second_operator_unavailable",
)

PRECHECK_IDS = (
    "inventory_reviewed",
    "networkd_working_owner",
    "ifupdown_conflict_confirmed",
    "management_vpn_health_green",
    "console_recovery_ready",
)

CHANGE_STEP_IDS = (
    "backup_ifupdown_state",
    "remove_ifupdown_primary_declaration",
    "disable_ifupdown_boot_ownership",
    "preserve_systemd_networkd",
)

STOP_GATE_IDS = (
    "trusted_utc_expired",
    "console_unavailable",
    "inventory_drift",
    "unexpected_network_owner",
    "prechange_health_degraded",
    "unexpected_command_result",
    "route_or_listener_loss",
    "fresh_management_session_failed",
)

VALIDATION_IDS = (
    "single_primary_network_owner",
    "networkd_active_enabled",
    "default_route_preserved",
    "fresh_management_session_established",
    "vpn_units_listeners_preserved",
    "no_new_failed_units",
)

ROLLBACK_IDS = (
    "restore_ifupdown_primary_declaration",
    "restore_ifupdown_unit_state",
    "repeat_s4_health_validation",
)
```

The complete blocker mapping is fixed and nested:

```python
BLOCKER_RULES = (
    ("source_review_incomplete", ("source_review_completed",), False),
    ("networkd_inactive", ("networkd", "active"), False),
    ("networkd_disabled", ("networkd", "enabled"), False),
    ("networkd_not_primary_owner", ("networkd", "owns_primary_interface"), False),
    ("networkd_not_default_route_owner", ("networkd", "owns_default_route"), False),
    ("ifupdown_disabled", ("ifupdown", "enabled"), False),
    ("ifupdown_primary_declaration_absent", ("ifupdown", "declares_primary_interface"), False),
    ("ifupdown_default_route_declaration_absent", ("ifupdown", "declares_default_route"), False),
    ("ifup_unit_state_drift", ("ifupdown", "ifup_unit_failed"), False),
    ("networking_unit_state_drift", ("ifupdown", "networking_unit_failed"), False),
    ("management_unreachable", ("health", "management_reachable"), False),
    ("vpn_units_unhealthy", ("health", "vpn_units_healthy"), False),
    ("vpn_listeners_missing", ("health", "expected_vpn_listeners_present"), False),
    ("default_route_missing", ("health", "default_route_present"), False),
    ("console_access_unconfirmed", ("console", "independent_access_confirmed"), False),
    ("recovery_procedure_unreviewed", ("console", "recovery_procedure_reviewed"), False),
    ("second_operator_unavailable", ("console", "second_operator_available"), False),
)
```

Each rule emits its blocker when the addressed value equals the third tuple item. The exact audited `EVIDENCE_COMPLETE` fixture sets every listed boolean to `true`, including both failed-unit facts that prove the known ifupdown conflict state. Any different state is `BLOCKED`; the evaluator never invents another repair.

---

### Task 0: Reconcile the newer owner authorization with durable repository authority

**Consumes:** Approved S4 design, committed `AGENTS.md`, the owner's later standing authorization, and its durable memory note.

**Produces:**

- Create: `docs/superpowers/specs/2026-08-31-maestrovpn-s4-production-authorization-amendment.md`
- Modify: `CONTEXT_HANDOFF.md`
- Regenerate from a clean exact tree: `docs/yandex-cdn-whitelist/BASELINE_MANIFEST.json`

- [ ] **Step 1: Write the dated amendment**

Record that the owner's later explicit instruction replaces only the need for another chat reply after every gate is GREEN. Require the pre-mutation declaration to bind exact `S4`, package digest, operator, UTC window, impact, preconditions, protected rollback-sheet identity, and stop gates. Preserve both independent trusted-UTC comparisons, fresh unchanged inventory, independent console, second operator, no concurrent work, protected backups, before-state health, fresh management session, immediate rollback, and every exclusion.

State `PRODUCTION NO-GO` until the amendment, implementation, handoff, exact-SHA CI, and independent review are complete. Do not edit protected dirty `AGENTS.md`.

- [ ] **Step 2: Update the durable handoff without claiming live readiness**

Add the amendment path, the exact owner decision, and the distinction between standing authorization and embedded artifact authority. State that production remains blocked until Task 6 gates pass.

- [ ] **Step 3: Create an intermediate authority commit**

Capture the full dirty set and verify every protected path is unchanged. Stage only the amendment and `CONTEXT_HANDOFF.md`, then commit:

```bash
git add -- \
  docs/superpowers/specs/2026-08-31-maestrovpn-s4-production-authorization-amendment.md \
  CONTEXT_HANDOFF.md
git commit -m "docs: reconcile S4 production authorization"
```

- [ ] **Step 4: Generate the matching baseline from a clean exact tree**

Read and follow `using-git-worktrees`. Create a temporary detached clean worktree at the exact intermediate commit, render twice, require byte-for-byte equality, and transfer only the generated bytes into `docs/yandex-cdn-whitelist/BASELINE_MANIFEST.json` in the canonical checkout. Never render against the canonical dirty worktree.

- [ ] **Step 5: Commit and verify the authority baseline**

Inspect the manifest diff, run `git diff --check`, stage only the generated manifest, then commit:

```bash
git add -- docs/yandex-cdn-whitelist/BASELINE_MANIFEST.json
git commit -m "docs: bind S4 authorization baseline"
```

In a new clean detached worktree at the exact final Task 0 commit, run:

```bash
python -m unittest scripts.tests.test_yandex_cdn_docs -v
python scripts/validate_yandex_cdn_docs.py
git diff --check
```

Safely remove both exact Task 0 temporary worktrees and confirm their paths are absent from `git worktree list`. Then push and require exact-SHA GitHub GREEN. Do not begin Task 1 until an independent reviewer reports `0 Critical / 0 Important / 0 Minor` on this authority reconciliation.

### Task 1: Lock the inventory and deterministic evaluation contract

**Consumes:** Approved design plus Task 0 amendment; canonical inventory bytes and explicit trusted evaluation time.

**Produces:** Strict parser/evaluator module and focused pure-unit tests; no filesystem output and no production action.

**Files:**

- Create: `ops/ha/tests/test_s4_network_change_package.py`
- Create: `ops/ha/s4_network_change_package.py`

- [ ] **Step 1: Write failing tests for the canonical happy path**

Create `valid_inventory()` with the exact top-level/nested keys and types frozen above. Use:

```python
CAPTURED_AT = "2026-08-31T11:50:00Z"
EXPIRES_AT = "2026-08-31T12:05:00Z"
EVALUATION_TIME = datetime(2026, 8, 31, 12, 0, 0, tzinfo=timezone.utc)
```

Assert that `parse_inventory(canonical(valid_inventory()), evaluation_time=EVALUATION_TIME)` accepts only byte-for-byte canonical JSON, preserves the fixed values, and rejects duplicate keys, floats, JSON constants, unknown keys at every level, missing keys, wrong types, integer `0/1` substituted for booleans, non-ASCII ambiguity, and non-canonical whitespace/order.

Assert trusted-time rules exactly:

```python
captured_at <= evaluation_time < expires_at
expires_at - captured_at <= timedelta(minutes=15)
```

Reject timestamps that are not strict UTC seconds in `YYYY-MM-DDTHH:MM:SSZ` form. A stale or future inventory is an invalid-input error, not a `BLOCKED` package.

- [ ] **Step 2: Run the focused test and prove RED**

Run:

```bash
python -m unittest ops.ha.tests.test_s4_network_change_package.S4InventoryContractTests -v
```

Expected: FAIL because `ops.ha.s4_network_change_package` does not exist.

- [ ] **Step 3: Implement strict parsing and canonical serialization**

In `ops/ha/s4_network_change_package.py`, implement:

```python
def canonical_bytes(value: object) -> bytes:
    return (
        json.dumps(
            value,
            ensure_ascii=True,
            allow_nan=False,
            sort_keys=True,
            separators=(",", ":"),
        ).encode("ascii")
        + b"\n"
    )
```

Use an `object_pairs_hook` that raises a redacted `S4ChangePackageError` on duplicate keys and `parse_constant` that rejects `NaN`/`Infinity`. Require `type(value) is dict`; do not accept `bool` where an integer is expected. Match the complete exact inventory-key set from the approved design spec and keep all error messages as stable codes prefixed by `s4-network-change-package:`.

- [ ] **Step 4: Write failing tests for blocker mapping and output order**

For every boolean inventory fact, flip only that fact and assert the exact blocker code and its fixed position in `blockers`. Assert multiple blockers follow `BLOCKER_ORDER`, regardless of input construction order. Assert:

```python
package["status"] == ("EVIDENCE_COMPLETE" if not blockers else "BLOCKED")
package["apply_supported"] is False
package["mutation_authorized"] is False
```

Assert the precheck, change-step, stop-gate, validation, and rollback arrays contain the exact IDs above. Assert the generated object binds the caller-supplied SHA-256 of the exact canonical inventory bytes, copies only `captured_at_utc` and `expires_at_utc`, does not emit evaluation time, `node_id`, or `evidence_class`, and contains no password, token, SSH key, endpoint, production command output, or mutable action.

- [ ] **Step 5: Run the evaluation tests and prove RED**

Run:

```bash
python -m unittest \
  ops.ha.tests.test_s4_network_change_package.S4EvaluationTests -v
```

Expected: FAIL until `evaluate_inventory()` is implemented.

- [ ] **Step 6: Implement deterministic evaluation**

Use the complete frozen `BLOCKER_RULES` table above, not condition-order side effects. Resolve each nested path with a small pure helper and construct only the exact output fields:

```python
def evaluate_inventory(
    inventory: Mapping[str, object],
    *,
    inventory_sha256: str,
) -> dict[str, object]:
    if not re.fullmatch(r"[0-9a-f]{64}", inventory_sha256):
        _fail("inventory-digest")
    blockers = [
        code
        for code, path, blocked_value in BLOCKER_RULES
        if _nested_bool(inventory, path) is blocked_value
    ]
    return {
        "apply_supported": False,
        "blockers": blockers,
        "change_scope": CHANGE_SCOPE,
        "change_step_ids": list(CHANGE_STEP_IDS),
        "conflicting_manager": "ifupdown",
        "inventory_captured_at_utc": inventory["captured_at_utc"],
        "inventory_expires_at_utc": inventory["expires_at_utc"],
        "inventory_sha256": inventory_sha256,
        "mutation_authorized": False,
        "precheck_ids": list(PRECHECK_IDS),
        "rollback_ids": list(ROLLBACK_IDS),
        "schema": OUTPUT_SCHEMA,
        "selected_owner": "systemd-networkd",
        "status": "BLOCKED" if blockers else "EVIDENCE_COMPLETE",
        "stop_gate_ids": list(STOP_GATE_IDS),
        "validation_ids": list(VALIDATION_IDS),
    }
```

Return a new object; do not mutate the parsed inventory. No network, subprocess, socket, SSH, DNS, HTTP, environment-secret, or production-path access is allowed in this module.

- [ ] **Step 7: Run the complete core suite and prove GREEN**

Run:

```bash
python -m unittest ops.ha.tests.test_s4_network_change_package -v
python -m py_compile ops/ha/s4_network_change_package.py
```

Expected: all tests pass.

- [ ] **Step 8: Self-review and commit Task 1 only**

Review the diff against the approved design, run `git diff --check`, then stage only:

```bash
git add -- ops/ha/s4_network_change_package.py \
  ops/ha/tests/test_s4_network_change_package.py
git commit -m "feat(ha): add deterministic S4 change package core"
```

### Task 2: Add the fail-closed filesystem and CLI boundary

**Consumes:** Task 1 parser/evaluator, one protected local inventory path, explicit evaluation time, and one new output path.

**Produces:** A protected canonical output or no output; stable exit code `0`, `2`, or `3`; no stdout data and no production action.

**Files:**

- Modify: `ops/ha/tests/test_s4_network_change_package.py`
- Modify: `ops/ha/s4_network_change_package.py`
- Create: `ops/ha/s4-network-change-package.py`

- [ ] **Step 1: Write failing secure-input tests**

Cover a bounded regular single-link, non-symlink inventory owned by the invoking UID with mode `0600`. Reject directory, FIFO, socket, symlink, hardlink count other than one, wrong owner, group/other permissions, oversized input, empty input, and file replacement/truncation/metadata drift between open, read, and recheck. POSIX-only owner/mode/race tests use `@unittest.skipUnless(os.name == "posix", ...)`.

The read boundary must return both the parsed inventory and:

```python
hashlib.sha256(raw).hexdigest()
```

- [ ] **Step 2: Run the secure-input tests and prove RED**

Run:

```bash
python -m unittest \
  ops.ha.tests.test_s4_network_change_package.S4SecureInputTests -v
```

- [ ] **Step 3: Implement descriptor-pinned bounded input**

Follow the existing `ops/ha/deploy_node.py` metadata-fingerprint pattern. Open with `O_RDONLY`, plus `O_CLOEXEC` and `O_NOFOLLOW` when available. Validate `fstat()` before and after a bounded read, reject trailing bytes beyond `MAX_INVENTORY_BYTES`, and compare device, inode, mode, UID, link count, size, mtime-ns, and ctime-ns. Never include the supplied path or source content in an error.

- [ ] **Step 4: Write failing output-publication tests**

Cover an existing output directory owned by the invoking UID, non-symlink, mode `0700`. Assert the final output must not already exist. Cover temp-file collision, final-file collision, symlink/hardlink substitution, write failure, temp-fsync failure, link failure, final recheck failure, directory-fsync failure, and cleanup behavior. Assert exit `3` leaves no final output, never overwrites/unlinks a pre-existing final file, and removes only an invocation-owned published inode. On success assert owner UID, mode `0600`, regular-file type, exact bytes, and final `nlink == 1`.

- [ ] **Step 5: Run output tests and prove RED**

Run:

```bash
python -m unittest \
  ops.ha.tests.test_s4_network_change_package.S4SecureOutputTests -v
```

- [ ] **Step 6: Implement no-clobber atomic publication**

On non-POSIX platforms fail closed with stable code `unsupported-platform` before opening input/output; do not claim equivalent ownership/ACL guarantees. On POSIX, follow the existing `_publish_manifest` rollback pattern: pin the parent directory, create a randomized temp name with `O_WRONLY | O_CREAT | O_EXCL` and mode `0600`, write all bytes, `fsync()` the temp descriptor, and publish with a same-directory hard link. Fingerprint the invocation-owned final inode. Unlink only the invocation-owned temp, recheck directory/final metadata, require final `nlink == 1`, then `fsync()` the parent. If any post-link recheck or fsync fails, unlink the final only when its fingerprint still matches this invocation, repeat parent-directory `fsync`, and return exit `3` with no output.

- [ ] **Step 7: Write failing CLI boundary tests**

Test `run()` directly and the wrapper through `subprocess.run()`. Assert only `package` is accepted. Assert `matrix`, `apply`, `repair`, `disable`, `restart`, `rollback`, aliases, unknown options, duplicate options, `--tooling-sha`, and extra positionals fail with exit `3` before the inventory is opened. Patch the open function to raise if touched in every negative test. Assert help is redacted and contains no environment values or paths. Assert stdout is empty on success because the canonical result is written only to `--output`; stderr contains only a stable redacted code on failure.

Simulate a non-POSIX platform and install sentinels on `lstat`, `open`, `read_inventory`, and `publish_change_package`. Assert exit `3`, fixed `unsupported-platform` stderr, no filesystem sentinel call, and no output path.

- [ ] **Step 8: Implement the thin wrapper and redacted CLI**

`ops/ha/s4-network-change-package.py` must contain only import/bootstrap code and:

```python
if __name__ == "__main__":
    raise SystemExit(main())
```

Before constructing or invoking `argparse`, manually preflight argv: optional top-level help is the only non-package form; package argv has exactly six option/value tokens; the option set is exactly `--inventory`, `--evaluation-time`, and `--output`; each appears once; no option-like value or extra token is accepted. Then use a redacted parser and parse strict trusted UTC before opening the inventory. Map a published `BLOCKED` package to exit `2`, `EVIDENCE_COMPLETE` to `0`, and every validation/system error to `3` with no final output.

- [ ] **Step 9: Run all package tests and prove GREEN**

Run:

```bash
python -m unittest ops.ha.tests.test_s4_network_change_package -v
python -m py_compile \
  ops/ha/s4_network_change_package.py \
  ops/ha/s4-network-change-package.py
python ops/ha/s4-network-change-package.py --help
```

- [ ] **Step 10: Self-review and commit Task 2 only**

Run `git diff --check`, then stage only:

```bash
git add -- ops/ha/s4_network_change_package.py \
  ops/ha/s4-network-change-package.py \
  ops/ha/tests/test_s4_network_change_package.py
git commit -m "feat(ha): harden S4 change package boundary"
```

### Task 3: Add the reviewed operator runbook

**Consumes:** Stable semantic IDs and the dated authority amendment.

**Produces:** Checked-in review gates only; concrete production paths, commands, current contents, backups, and restore commands remain in a protected local command sheet outside Git.

**Files:**

- Create: `docs/operations/runbook-ha-s4-network-repair.md`
- Modify: `ops/ha/tests/test_s4_network_change_package.py`

- [ ] **Step 1: Write failing runbook-contract tests**

Assert the runbook contains:

- `PRODUCTION NO-GO` until an `EVIDENCE_COMPLETE` package, trusted UTC, console recovery, and fresh S4 read-only preflight are all confirmed.
- Exact target `s4` and exact chosen owner `systemd-networkd`.
- Exact scope `REMOVE_CONFLICTING_IFUPDOWN_PRIMARY_OWNERSHIP_ONLY`.
- Exact package command and exit-code meanings.
- A pre-mutation declaration of target, impact, preconditions, rollback, and stop gates.
- Backup and restore of only the conflicting ifupdown declaration/unit state.
- Preservation of systemd-networkd ownership, default route, management access, VPN units, and VPN listeners.
- A fresh independent management session before the original session is closed.
- Immediate rollback triggers for owner drift, route/listener loss, unhealthy units, console loss, unexpected command result, or failed fresh session.
- Explicit exclusions for matrix/S3/backups outside this narrow rollback/PKI/rqlite/shadow traffic/customer cutover/OLCRTC/WDTT.
- Two distinct independent trusted-UTC comparisons: immediately before the declaration/standing-authorization activation and immediately before execution.
- Fresh unchanged inventory, no concurrent work, second operator, protected affected-file/unit-state backups, and before-state health capture.
- A declaration envelope binding S4, package digest, operator, UTC window, impact, preconditions, protected rollback-sheet identity, and stop gates.
- A statement that `inventory_sha256` is integrity evidence, not a secrecy mechanism.
- A prohibition on invoking `build_manifest`, `pki_verify`, `deploy_node`, or `verify_backup` against digest-only evidence.
- A protected bounded raw-capture gate: an operator/owner reviews the raw capture before deriving canonical inventory and only then may set `source_review_completed: true`; raw bytes stay outside Git, package output, and ordinary reports.
- Exact target/exclusions: S4 only; Android/TV remains immutable `1.0.157`; S1-S3, DNS/CDN, bots, payments, customer data, VPN/firewall/listeners, install, restart, reload, reboot, release, signing, OTA, matrix, PKI, rqlite, shadow traffic, final cutover, OLCRTC, and WDTT are unchanged.

Reject forbidden wording that claims automatic apply, production readiness, customer traffic readiness, or permission to mutate.

- [ ] **Step 2: Run the runbook tests and prove RED**

Run:

```bash
python -m unittest \
  ops.ha.tests.test_s4_network_change_package.S4RunbookContractTests -v
```

- [ ] **Step 3: Write the bounded runbook**

Separate these sections explicitly: evidence capture, package generation, package review, first independent-clock/declaration gate, second independent-clock/execution gate, semantic change scope, validation, rollback, and evidence recording. Checked-in text contains semantic IDs only. Do not check in mutation/restore command templates, production paths, current file contents, server addresses, credentials, tokens, private keys, customer UUIDs, or live config contents.

The runbook must say that the dated amendment removes an additional chat-reply pause only after all gates are GREEN and the full declaration is emitted; it does not bypass any gate, embed authority in the package, or expand scope.

- [ ] **Step 4: Run focused tests and prove GREEN**

Run:

```bash
python -m unittest ops.ha.tests.test_s4_network_change_package -v
git diff --check -- \
  docs/operations/runbook-ha-s4-network-repair.md \
  ops/ha/tests/test_s4_network_change_package.py
```

- [ ] **Step 5: Self-review and commit Task 3 only**

Run `git diff --check`, then stage only:

```bash
git add -- docs/operations/runbook-ha-s4-network-repair.md \
  ops/ha/tests/test_s4_network_change_package.py
git commit -m "docs(ha): add bounded S4 network repair runbook"
```

- [ ] **Step 6: Verify Task 3 from its clean exact commit**

Create a new detached clean worktree at the exact Task 3 commit, then run:

```bash
python -m unittest ops.ha.tests.test_s4_network_change_package -v
python -m unittest scripts.tests.test_yandex_cdn_docs -v
python scripts/validate_yandex_cdn_docs.py
git diff --check
```

Safely remove that exact temporary worktree and confirm its path is absent from `git worktree list`.

### Task 4: Add a dedicated inert GitHub workflow and self-policy

**Consumes:** Tasks 1-3 repository-only implementation and tests.

**Produces:** One pinned, no-secret, no-network, no-artifact-upload workflow plus mandatory active-source self-policy.

**Files:**

- Create: `.github/workflows/ha-s4-network-change-package.yml`
- Create: `ops/ha/tests/test_s4_network_workflow_policy.py`
- Modify: `ops/ha/tests/test_s4_network_change_package.py`

- [ ] **Step 1: Write failing workflow-policy tests**

Reuse the existing bounded YAML subset parser from `ops.ha.build_workflow_policy`; do not add PyYAML and do not refactor the existing 895-line build policy. Validate exact top-level keys, triggers, permissions, concurrency, job image, timeout, checkout pin, and exact run-step names.

Required policy:

```yaml
name: HA S4 network change-package checks
on:
  push:
    branches:
      - codex/yandex-cdn-whitelist-task3-sync
  pull_request:
    branches:
      - codex/yandex-cdn-whitelist-task3-sync
  workflow_dispatch:
permissions:
  contents: read
concurrency:
  group: ha-s4-network-change-package-${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: false
```

The single job must use `ubuntu-24.04`, `timeout-minutes: 15`, and checkout exactly:

```yaml
uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683
with:
  persist-credentials: false
```

Reject `pull_request_target`, `environment`, `${{ secrets`, `upload-artifact`, non-pinned actions, network clients, SSH, production literals, remote mutation verbs, and any server address. Require a runner-temp directory with mode `0700`, `PYTHONPYCACHEPREFIX` inside it, and unconditional local runner-temp cleanup.

- [ ] **Step 2: Run the workflow-policy tests and prove RED**

Run:

```bash
python -m unittest ops.ha.tests.test_s4_network_workflow_policy -v
```

Before implementing the workflow, add RED `S4CapabilityDenylistTests` and `S4SensitiveLiteralTests` to `test_s4_network_change_package.py`. The capability class parses the active core and wrapper AST. The sensitive class scans these exact active files:

```text
ops/ha/s4_network_change_package.py
ops/ha/s4-network-change-package.py
.github/workflows/ha-s4-network-change-package.yml
docs/operations/runbook-ha-s4-network-repair.md
docs/superpowers/specs/2026-08-31-maestrovpn-s4-production-authorization-amendment.md
```

Reject credential assignments, `github_pat_`, `Bearer `, PEM/private-key markers, IPv4/IPv6 and hostname/host:port endpoint literals, customer UUID/data labels, production filesystem paths, and install/restart/reload/reboot/firewall/listener mutation-command literals. Allow only the reviewed public GitHub action identifier, semantic IDs, fixed schema strings, and synthetic opaque test values. The tests scan real file bytes, not only inline samples.

- [ ] **Step 3: Implement the inert workflow**

The workflow has this complete mandatory step set: checkout; create mode-`0700` runner temp; core unit tests; secure-I/O and CLI negative tests including explicit `--tooling-sha` rejection; AST/static capability-denylist test; sensitive-literal scan; Python compilation; wrapper help; workflow self-policy; canonical docs tests/validator; `git diff --check`; and unconditional runner-temp cleanup. Self-policy pins the exact step names and these exact active command lines:

```bash
test -n "$RUNNER_TEMP"
case "$RUNNER_TEMP" in /*) ;; *) exit 1 ;; esac
s4_ci_tmp="$RUNNER_TEMP/maestro-s4-network-change-package"
case "$s4_ci_tmp" in "$RUNNER_TEMP"/*) ;; *) exit 1 ;; esac
install -d -m 700 "$s4_ci_tmp/pycache"
printf 'PYTHONPYCACHEPREFIX=%s\n' "$s4_ci_tmp/pycache" >> "$GITHUB_ENV"
python -m unittest \
  ops.ha.tests.test_s4_network_change_package \
  ops.ha.tests.test_s4_network_workflow_policy -v
python -m unittest \
  ops.ha.tests.test_s4_network_change_package.S4CliBoundaryTests \
  ops.ha.tests.test_s4_network_change_package.S4CapabilityDenylistTests \
  ops.ha.tests.test_s4_network_change_package.S4SensitiveLiteralTests -v
python -m py_compile \
  ops/ha/s4_network_change_package.py \
  ops/ha/s4-network-change-package.py
python ops/ha/s4-network-change-package.py --help
python -m unittest scripts.tests.test_yandex_cdn_docs -v
python scripts/validate_yandex_cdn_docs.py
git diff --check
s4_ci_tmp="$RUNNER_TEMP/maestro-s4-network-change-package"
case "$s4_ci_tmp" in "$RUNNER_TEMP"/*) ;; *) exit 1 ;; esac
rm -rf -- "$s4_ci_tmp"
```

The setup and cleanup are separate named workflow steps; cleanup has `if: always()`. The duplicated CLI/security class invocation is intentional evidence for the negative/scan step even though the full module also runs it.

The named unittest classes must explicitly exercise negative CLI forms, sensitive-literal scanning, and AST/static denial of `socket`, `urllib`, HTTP clients, SSH libraries, `subprocess`, `os.system`, shell/process execution, and mutating entry points. Use only invented boolean fixtures, opaque channel IDs, or runner-temp files. Do not upload artifacts and do not read GitHub secrets.

- [ ] **Step 4: Make the self-policy fail closed**

The policy test uses the existing external-constant pattern: `APPROVED_ACTIVE_SOURCE_SHA256` is a mandatory hard-coded 64-hex constant in `test_s4_network_workflow_policy.py`; `_active_source_sha256()` excludes all standalone comments and blank lines exactly as `ops.ha.build_workflow_policy` does, then hashes every remaining workflow byte. After finalizing the workflow, compute the hash once and insert that literal into the Python constant. Prove comment/uncomment of active content, step insertion/removal, branch-flow syntax changes, or command mutation fails policy. Assert the complete exact setup/test/scan/cleanup command set. Every error is a stable redacted assertion message.

- [ ] **Step 5: Run the dedicated workflow suite locally**

Run:

```bash
python -m unittest \
  ops.ha.tests.test_s4_network_change_package \
  ops.ha.tests.test_s4_network_workflow_policy -v
python -m py_compile \
  ops/ha/s4_network_change_package.py \
  ops/ha/s4-network-change-package.py \
  ops/ha/tests/test_s4_network_workflow_policy.py
git diff --check
```

- [ ] **Step 6: Self-review and commit Task 4 only**

Stage only:

```bash
git add -- .github/workflows/ha-s4-network-change-package.yml \
  ops/ha/tests/test_s4_network_workflow_policy.py \
  ops/ha/tests/test_s4_network_change_package.py
git commit -m "build(ha): verify inert S4 change packages"
```

### Task 5: Refresh durable handoff and baseline integrity

**Consumes:** Reviewed committed implementation/docs and the complete initial dirty-set record.

**Produces:** Evidence-only handoff plus a baseline generated from a clean exact tree, never from protected dirty working bytes.

**Files:**

- Modify: `CONTEXT_HANDOFF.md`
- Regenerate: `docs/yandex-cdn-whitelist/BASELINE_MANIFEST.json`

- [ ] **Step 1: Record only evidence-backed state**

Add the approved design, implementation files, exact tests, current commit SHAs, dedicated workflow, and remaining production `NO-GO` gates. Keep design/fixture/repository readiness separate from live S4 readiness. Do not claim S4 was changed or that production/customer traffic is ready.

- [ ] **Step 2: Commit intended handoff bytes before baseline generation**

Run `git diff --check` only on the intended handoff paths, stage only those paths, and create an intermediate docs commit. Do not run the manifest validator while `CONTEXT_HANDOFF.md` differs from the persisted baseline or from the dirty canonical checkout. Reconfirm the initial protected dirty set is byte-for-byte untouched.

- [ ] **Step 3: Regenerate the baseline from a clean exact tree**

Read and follow `using-git-worktrees` before creating an isolated temporary clean worktree at the exact intermediate docs commit. In that clean worktree run the renderer and write its stdout mechanically to a new temporary manifest:

```bash
python scripts/render_redacted_baseline.py > BASELINE_MANIFEST.generated.json
```

Render a second time and require byte-for-byte equality. Transfer only the generated bytes to `docs/yandex-cdn-whitelist/BASELINE_MANIFEST.json` in the canonical checkout as a mechanical generated-file rewrite. Inspect the diff and confirm it contains no secret, address, token, password, private key, customer identifier, protected dirty hash, or unreviewed owner-file change. Remove the temporary worktree through the worktree skill's safe cleanup procedure.

- [ ] **Step 4: Inspect the generated manifest before commit**

Require only the expected `CONTEXT_HANDOFF.md` baseline row/digest changes, no protected dirty hash, no secret pattern, and a clean `git diff --check`. Do not run the working-tree manifest validator here.

- [ ] **Step 5: Self-review and commit the generated manifest only**

Stage only the regenerated manifest:

```bash
git add -- docs/yandex-cdn-whitelist/BASELINE_MANIFEST.json
git commit -m "docs(ha): record S4 change package evidence"
```

- [ ] **Step 6: Verify Task 5 from the clean exact final commit**

Create a new detached clean worktree at the exact Task 5 final commit and run:

```bash
python -m unittest scripts.tests.test_yandex_cdn_docs -v
python scripts/validate_yandex_cdn_docs.py
git diff --check
```

Safely remove that exact temporary worktree and confirm its path is absent from `git worktree list`.

### Task 6: Independent review, GitHub-first verification, and next production gate

**Consumes:** All Task 0-5 commits, unchanged protected dirty files, and the canonical branch.

**Produces:** Exact-SHA CI/review evidence and, only afterward, one fresh protected read-only S4 inventory/package; no mutation until every declaration and runbook gate is satisfied.

**Files:** None unless review finds a defect.

- [ ] **Step 1: Run the complete scoped local verification**

Run:

```bash
python -m unittest \
  ops.ha.tests.test_s4_network_change_package \
  ops.ha.tests.test_s4_network_workflow_policy -v
python -m py_compile \
  ops/ha/s4_network_change_package.py \
  ops/ha/s4-network-change-package.py \
  ops/ha/tests/test_s4_network_change_package.py \
  ops/ha/tests/test_s4_network_workflow_policy.py
git diff --check
```

Verify `git status --short`, branch name, staged set, local HEAD, and remote exact SHA while preserving every protected dirty owner file.

- [ ] **Step 2: Request independent severity-ranked review**

Use `requesting-code-review`. Require Critical/Important/Minor findings with exact file/line evidence. Resolve valid findings with TDD, rerun the focused and complete suites, and repeat review until it reports `0 Critical / 0 Important / 0 Minor`.

- [ ] **Step 3: Push only the canonical branch**

Push:

```bash
git push origin HEAD:refs/heads/codex/yandex-cdn-whitelist-task3-sync
```

Verify `git ls-remote` reports the exact local HEAD for that branch. Never use the leaked PAT and never switch to another source-of-truth branch.

- [ ] **Step 4: Require exact-SHA GitHub GREEN**

Create a new detached clean worktree at the exact pushed SHA and run the docs unit tests, manifest validator, and `git diff --check`; safely remove the exact worktree afterward. Wait for the dedicated `HA S4 network change-package checks` workflow and every required canonical-branch workflow for that same SHA. A green run for an older SHA is not evidence. Fix failures task-by-task with the same TDD/review cycle.

- [ ] **Step 5: Produce the fresh S4 read-only inventory only after repository GREEN**

The next live step is read-only: create a protected bounded raw capture outside Git; an operator/owner reviews those raw bytes; only after that review derive the exact canonical S4 inventory and set `source_review_completed: true`. Include trusted UTC, management/VPN health, current network ownership, ifupdown conflict facts, console recovery confirmation, reviewed recovery procedure, and second-operator availability. Raw bytes never enter Git, package output, or ordinary reports. Do not mutate S4 while collecting it.

- [ ] **Step 6: Generate and review the package**

Generate into a new protected local output. Continue to the production repair runbook only if the result is `EVIDENCE_COMPLETE`, the inventory is still fresh, every stop gate remains GREEN, and rollback is executable. If the result is `BLOCKED` or exit `3`, stop before mutation and report the exact stable blocker/error code.

The dated authorization amendment means there is no additional chat-reply pause after all gates are proven. Before any mutation, emit the complete declaration binding exact S4, package digest, operator, UTC window, impact, preconditions, protected rollback-sheet identity, and stop gates; perform the first independent-UTC comparison immediately before this declaration and the second immediately before execution. If repository authority, time, raw review, inventory, console, second operator, rollback, or health is uncertain, stop without mutation. Target is S4 only; Android/TV stays at `1.0.157`; S1-S3, DNS/CDN, bots, payments, customer data, VPN/firewall/listeners, install/restart/reload/reboot, release/signing/OTA, matrix, PKI, rqlite, shadow traffic, final cutover, OLCRTC, and WDTT remain outside this authorization.
