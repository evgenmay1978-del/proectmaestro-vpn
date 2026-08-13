# olcRTC S1-to-S3 Runtime Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Maestro panel room updates fail closed and activate a verified per-login olcRTC exit on S3 before the room is advertised.

**Architecture:** A root-owned S1 configuration defines the S3 host, dedicated SSH key and strict known-hosts file. The room script stages and validates the S3 exit first, verifies the join, and only then updates the panel; panel failure restores the previous S3 state. The health timer uses the same strict SSH boundary and writes an atomic snapshot consumed by the panel.

**Tech Stack:** POSIX sh, Python unittest, OpenSSH, systemd, Go panel API.

## Global Constraints

- No hard-coded production address in committed scripts.
- `StrictHostKeyChecking=yes`; no trust-on-first-use in production.
- Dedicated S1→S3 key with least possible scope and root-only permissions.
- Never print WbStream tokens, olcRTC keys, customer identifiers or authenticated URLs.
- Never publish a room before the corresponding S3 exit is active and joined.
- TV, payments, bots, subscriptions and other protocols are unchanged.

---

### Task 1: Deterministic fail-closed shell tests

**Files:**
- Create: `ops/test_olcrtc_ops.py`
- Modify: `.github/workflows/android-test.yml`
- Test: `ops/test_olcrtc_ops.py`

**Interfaces:**
- Consumes: `ops/olcrtc-room.sh`, `ops/olcrtc-health.sh`.
- Produces: fake-command integration tests recording ordered `ssh` and `curl` calls without network access.

- [ ] **Step 1: Write the fake-command harness**

Create a temporary root directory with fake `ssh`, `curl`, `systemctl` and `journalctl` commands. Each command appends only its command name and semantic phase to `CALLS`; it never records arguments containing secrets.

- [ ] **Step 2: Write failing ordering tests**

Required cases:

```python
def test_room_is_published_only_after_remote_join(self):
    self.assertLess(calls.index("ssh:verify-joined"), calls.index("curl:panel-post"))

def test_remote_preflight_failure_never_posts_panel(self):
    self.assertNotIn("curl:panel-post", calls)

def test_panel_failure_restores_previous_remote_config(self):
    self.assertIn("ssh:rollback", calls)

def test_health_uses_strict_host_key_and_atomic_output(self):
    self.assertEqual(snapshot["exits"]["owner"]["healthy"], True)
```

Add assertions that committed scripts contain `StrictHostKeyChecking=yes`, consume configured key/known-hosts paths, and do not contain `StrictHostKeyChecking=no` or a production IP literal.

- [ ] **Step 3: Run RED**

Run: `python -m unittest -v ops/test_olcrtc_ops.py`  
Expected: failures for unsafe SSH options, hard-coded S3 address and panel-before-exit ordering.

- [ ] **Step 4: Commit and push RED**

Commit: `test(olcrtc): require fail-closed room orchestration`. Prove the expected failure on the exact GitHub SHA.

### Task 2: Shared strict SSH configuration

**Files:**
- Create: `ops/olcrtc-ssh-config.sh`
- Modify: `ops/olcrtc-room.sh`
- Modify: `ops/olcrtc-health.sh`
- Test: `ops/test_olcrtc_ops.py`

**Interfaces:**
- Consumes root-owned `/etc/maestro-olcrtc.env` keys `S3_HOST`, `S3_USER`, `S3_IDENTITY_FILE`, `S3_KNOWN_HOSTS_FILE`.
- Produces shell variables `SSH_TARGET` and a strict argument vector used by both scripts.

- [ ] **Step 1: Implement strict config loading**

Require every value to be present. Validate host/user character sets, require identity and known-hosts files to be regular files not group/world writable, and construct:

```text
-i <identity>
-o BatchMode=yes
-o IdentitiesOnly=yes
-o StrictHostKeyChecking=yes
-o UserKnownHostsFile=<known-hosts>
-o ConnectTimeout=8
```

Exit before any network action on validation failure.

- [ ] **Step 2: Replace both unsafe SSH calls**

Remove the hard-coded host and every `StrictHostKeyChecking=no`. Source the shared helper from an absolute reviewed install path with a test override.

- [ ] **Step 3: Run focused GREEN tests**

Run: `python -m unittest -v ops/test_olcrtc_ops.py`.  
Expected: strict-config and no-hard-coded-address tests pass; ordering tests may remain RED until Task 3.

### Task 3: Transactional room activation

**Files:**
- Modify: `ops/olcrtc-room.sh`
- Test: `ops/test_olcrtc_ops.py`

**Interfaces:**
- Consumes: login, room and provider arguments plus strict SSH configuration.
- Produces: exit code 0 only when S3 joined the room and the panel accepted the same room/key.

- [ ] **Step 1: Preflight without mutation**

Strict SSH must prove access, required S3 paths, systemd template and olcRTC binary before reading/minting a key. Failure exits without panel POST.

- [ ] **Step 2: Build secret-bearing YAML outside command arguments**

Create a root-only temporary file on S1, stream it over SSH stdin to a root-only S3 temporary file, run an S3 syntax/shape check, and atomically rename it. Trap removal on every exit. Tokens and keys must not appear in process arguments or output.

- [ ] **Step 3: Stage, restart and verify before publication**

Back up any prior room file and unit state, restart the per-login unit, then require both `systemctl is-active` and a post-start `Link connected|KCP started` journal event. On failure restore the backup and previous unit state.

- [ ] **Step 4: Publish through the panel last**

POST the room/key/provider only after join verification. If the POST fails, restore the previous S3 room and unit state. Return a fixed redacted error.

- [ ] **Step 5: Run all tests GREEN**

Run: `python -m unittest -v ops/test_olcrtc_ops.py`.  
Expected: all ordering, rollback, strict-host and redaction tests pass.

- [ ] **Step 6: Commit and push**

Commit: `fix(olcrtc): activate exit before publishing room`.

### Task 4: Strict atomic health probe

**Files:**
- Modify: `ops/olcrtc-health.sh`
- Modify: `ops/maestro-olcrtc-health.service`
- Modify: `ops/maestro-olcrtc-health.timer`
- Test: `ops/test_olcrtc_ops.py`

**Interfaces:**
- Consumes: same strict SSH config as room activation.
- Produces: `{"checked": <unix>, "exits": {<login>: {"active": ..., "joined": ..., "healthy": ...}}}` written atomically.

- [ ] **Step 1: Make SSH failure explicit and stale-safe**

A failed probe writes a current snapshot with zero healthy exits and a non-secret error state; it never leaves stale green data.

- [ ] **Step 2: Validate login names before JSON serialization**

Accept only `[A-Za-z0-9._-]+`. Ignore malformed remote lines. Write to a mode-0600 temporary file in the destination directory, fsync, then atomic rename.

- [ ] **Step 3: Harden the systemd unit**

Use `UMask=0077`, `NoNewPrivileges=true`, `PrivateTmp=true`, bounded timeout and read/write path restrictions compatible with the script.

- [ ] **Step 4: Run GREEN**

Run focused Python tests plus `systemd-analyze verify` in CI for both unit files.

- [ ] **Step 5: Commit and push**

Commit: `fix(olcrtc): make exit health strict and stale-safe`.

### Task 5: Production provisioning and real traffic

**Files:**
- Modify after validation: `CURRENT_PRODUCTION_HANDOFF.md`
- Server-only root-owned files: dedicated key, known-hosts, `/etc/maestro-olcrtc.env`, reviewed scripts and units.

**Interfaces:**
- Consumes: exact reviewed GitHub SHA and verified S3 host fingerprint.
- Produces: working panel-driven room updates and current health status.

- [ ] **Step 1: Establish trust before access**

Obtain the S3 host fingerprint through existing trusted infrastructure and compare it with a separately retrieved fingerprint. A mismatch stops deployment. Generate one dedicated key on S1 and authorize only its public key on S3 without changing password-login policy for the owner.

- [ ] **Step 2: Install recoverably**

Create root-only backups, install exact-SHA scripts/helpers/units, set owner/modes, run shell syntax checks and `systemd-analyze verify`, then daemon-reload. Do not change existing rooms yet.

- [ ] **Step 3: Enable health and prove the panel reads it**

Enable the timer, run one probe, require a fresh timestamp and explicit state for each configured room. The panel must stop showing `?`.

- [ ] **Step 4: Exercise one room transaction**

Use the Maestro panel. Require success response, S3 unit active, joined event after restart, fresh healthy snapshot, and matching room in subscription/info. On any failure verify automatic rollback.

- [ ] **Step 5: Prove real phone traffic**

Refresh app state, select olcRTC, verify public egress, reconnect and sleep/wake. Do not expose room IDs or device/customer data in evidence.

- [ ] **Step 6: Update durable memory**

Record exact source/deployment SHA, anonymized health and phone results, backups and rollback state in `CURRENT_PRODUCTION_HANDOFF.md`. Commit: `docs: record olcRTC runtime validation`.
