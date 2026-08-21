# MaestroVPN HA DR Restore Drill Design

**Status:** approved by the owner on 2026-08-12 for repository-only implementation.

**Scope boundary:** this design proves disaster-recovery mechanics only against
synthetic GitHub Actions infrastructure. It does not connect to or mutate S1-S4,
panels, Telegram bots, customers, VPN services, DNS, Android/TV, Release or OTA.

## 1. Goal

Build a fail-closed, authenticated backup and empty-cluster restore drill for
the MaestroVPN three-voter rqlite control plane. The drill must prove that a
verified backup can recreate the exact business state on a fresh S2/S3/S4-shaped
cluster, advance a monotonic restore epoch and reject every command or lease
bound to the previous epoch.

This is the last repository gate before a separate read-only S2/S3/S4 readiness
audit. Passing it does not authorize production deployment.

## 2. Chosen sequence

1. Implement and prove the mechanism using only synthetic data, ephemeral test
   keys and an isolated three-node mandatory-mTLS rqlite cluster in GitHub
   Actions.
2. After exact-SHA GREEN, perform a separate read-only inventory of S2/S3/S4.
3. Produce a deployment plan from the verified repository artifacts and the
   observed inventory.
4. Mutate live servers only after a separate explicit owner approval.

The rejected alternatives are restoring a real customer backup during
development or installing incomplete components directly on S2/S3/S4. Both
reduce feedback time but create an unacceptable path to customer or VPN impact.

## 3. Safety invariants

- The production VPN data plane remains untouched throughout this slice.
- Backup and restore use the rqlite HTTP API over verified mandatory client
  mTLS. Direct reads or writes of a live rqlite SQLite/Raft directory are
  forbidden.
- The canonical image is requested through `GET /db/backup?fmt=delete`.
- Restore uses the official SQLite load path only after the target proves fresh
  and empty. No write traffic may coexist with restore.
- A non-empty, previously restored or ambiguously initialized target is
  rejected before upload.
- The backup manifest is derived entirely from the downloaded SQLite image.
  Mixing live pre-reads with a later image is forbidden.
- Every private temporary directory is mode `0700`; contained files are
  `0600`. Cleanup targets are validated descendants of one runner-owned root.
- Errors and logs never contain raw customer identities, credentials, keys,
  encrypted payloads, endpoints, file paths or database response bodies.
- The workflow uploads no backup, decrypted image, key or receipt artifact.
- No production credential, address or customer input is accepted by the
  repository-only workflow.
- Every failure is fail-closed and leaves the destination cluster unactivated.

The official rqlite backup/restore contract is
https://rqlite.io/docs/guides/backup/. It recommends a full SQLite backup,
fresh deployment for restore and no concurrent writes.

## 4. Components and boundaries

### 4.1 Offline backup verifier

`ops/ha/verify-backup.py` owns strict parsing and offline verification. It
accepts an extracted runner-local bundle plus explicitly supplied synthetic
verification material and returns only a redacted structured result.

The verifier requires one canonical manifest with:

- `format_version = 1`;
- full repository commit SHA and workflow run identity;
- rqlite version and exact control-plane schema version/checksum;
- backup creation time in UTC;
- source cluster epoch and source cluster identity digest;
- SQLite image filename, byte size and lowercase SHA-256;
- sorted exact member list with byte sizes and SHA-256 values;
- sorted table counts derived from the image;
- import-run and batch-receipt high-watermarks;
- backup watermark high-watermark;
- frozen synthetic node inventory S2/S3/S4;
- signing key ID and encryption recipient key ID.

It rejects unknown or missing fields, extra or missing members, noncanonical
JSON/base64/hex, path traversal, links, duplicate archive names, oversized
members, wrong signature, wrong recipient, byte tampering, schema mismatch,
SQLite integrity/foreign-key failure, table-count drift and plaintext marker
leakage.

### 4.2 Backup creator

`ops/ha/backup-rqlite.sh` is an orchestration boundary, not a business-data
parser. It:

1. validates its runner-owned root and installs cleanup traps;
2. proves all three HTTPS voters and mandatory client mTLS inputs;
3. downloads a DELETE-journal SQLite image from the isolated cluster;
4. invokes the verifier in manifest-construction mode against that image;
5. signs the canonical manifest and exact member digest set;
6. creates a deterministic archive;
7. encrypts it to an ephemeral test recipient;
8. decrypts and verifies a fresh copy before declaring success.

Tests use ephemeral GPG signing/encryption identities created inside
`RUNNER_TEMP`. Production key distribution, Yandex upload and retention are
outside this slice.

### 4.3 Restore epoch

An additive control-plane migration introduces a singleton
`cluster_restore_state` row with:

- `cluster_id`;
- positive monotonic `restore_epoch`;
- `restored_from_backup_sha256`;
- `activated` constrained to 0 or 1;
- `created_at_unix` and optional `activated_at_unix`.

New typed control-plane code exposes read-current-epoch and a single atomic
post-restore transition. The transition:

- requires an unactivated freshly restored state;
- computes an epoch strictly greater than the manifest epoch and every
  epoch-bearing restored receipt;
- clears all restored node/job/bot leases;
- records the verified backup digest;
- remains `activated=0` until reconciliation proof succeeds.

All future panel, bot and apply-agent deployment plans must bind commands and
leases to this epoch. Until those consumers are implemented and tested,
production activation remains forbidden.

### 4.4 Empty-cluster restore orchestrator

`ops/ha/restore-rqlite.sh --drill` operates only below validated
`RUNNER_TEMP` and only on the isolated mandatory-mTLS harness. It:

1. decrypts into a private temporary directory;
2. verifies signature, canonical manifest, hashes, SQLite integrity,
   foreign keys, schema identity, counts and high-watermarks offline;
3. proves the target is a freshly created three-node cluster with no
   application schema or business rows;
4. loads the verified SQLite image through the official rqlite API;
5. performs strong reads through each S2/S3/S4-shaped voter;
6. atomically advances the restore epoch and invalidates restored leases;
7. runs deterministic reconciliation and shadow/digest parity checks;
8. activates the synthetic cluster only after every proof passes.

A failed check destroys the isolated test cluster and starts from a newly
created cluster. It never retries restore into the same ambiguous target.

### 4.5 Fake fenced consumers

Repository-only fake bot, panel and apply-agent probes carry
`expected_restore_epoch`. They can read or propose a synthetic mutation only
when that value equals the current active epoch. Proofs require:

- old-epoch command rejected after restore;
- old node lease rejected;
- old cluster-job lease rejected;
- old Telegram poller lease rejected;
- current-epoch synthetic command accepted exactly once after activation;
- duplicate current-epoch command returns the stored result without a second
  mutation.

These probes demonstrate the fencing contract without starting a real bot,
panel, agent or VPN protocol.

## 5. Data flow

The workflow first prepares schema and applies the existing synthetic full
snapshot using the already verified production importer binary. It records the
canonical business digest and signed import receipt, then creates the encrypted
backup bundle.

A brand-new mTLS cluster is created with separate state directories. Before
load, the restore tool proves it contains no Maestro schema or business state.
After load and epoch advancement, the workflow recomputes:

- control-plane schema identity;
- exact table counts;
- import and batch receipt evidence;
- canonical business digest;
- redacted shadow export parity;
- lease and command fencing results.

All values must match the manifest except the intentionally increased restore
epoch and the new post-restore audit/activation records.

## 6. Failure and adversarial matrix

The exact workflow must prove rejection of:

- truncated, bit-flipped or substituted SQLite image;
- changed manifest field or member hash;
- missing, extra, duplicate, linked or traversal archive member;
- wrong signer, wrong encryption recipient or malformed key material;
- stale or decreasing restore epoch;
- schema version/checksum mismatch;
- SQLite integrity or foreign-key failure;
- table count or receipt high-watermark mismatch;
- HTTP target, missing client certificate or wrong CA/client certificate;
- restore into a non-empty or previously attempted cluster;
- concurrent fake writer;
- restored old node/job/bot lease;
- interruption before load, after load, before epoch commit and before
  activation;
- second restore attempt against the same destination;
- loss of one voter after activation;
- loss of quorum, which must reject writes while preserving last committed
  reads.

No unexplained workflow retry counts as evidence. A corrected behavior receives
a new exact commit SHA and a fresh full run.

## 7. GitHub Actions proof

A dedicated repository-only workflow or bounded extension of the existing HA
workflow uses Ubuntu 24.04, pinned actions, `contents: read`, a bounded timeout
and no environment or production secrets. It generates all PKI and GPG keys
ephemerally.

Named gates cover formatting/syntax, Python unit tests, shell contract tests,
backend unit/race/vet, source mTLS cluster, exact production importer binary,
backup creation, adversarial verification, fresh destination mTLS cluster,
restore, epoch fencing, digest/shadow parity, one-voter loss, no-quorum behavior
and unconditional cleanup.

The workflow publishes only step conclusions and redacted text. Backup bundles,
SQLite images, keys and detailed reports are not Actions artifacts.

## 8. Read-only production readiness audit after GREEN

The subsequent audit may read but not change S2/S3/S4. It records redacted
evidence for OS/architecture, disk and inode headroom, time synchronization,
current listeners/firewall, service users, existing panel/bot/x-ui/VPN units,
available backup/decryption capability, exact management reachability and safe
paths for three isolated rqlite data directories.

It does not install packages, open ports, stop/restart services, copy keys,
change firewall/systemd/nginx/DNS or read customer rows into logs. Any
unexpected state returns NO-GO and changes the later deployment plan.

S1 remains outside the voter cluster and is not touched during this audit.

## 9. Acceptance

This design is complete only when an exact GitHub code SHA proves all positive
and negative cases above, the scope/secret scan is clean, and
`CONTEXT_HANDOFF.md` records run/job evidence.

The result is still:

`NO-GO (repository DR implementation only)`

`S1-S4, panels, bots, customers, VPN protocols, Android/TV, Release and OTA unchanged.`

The next decision after GREEN is permission for a read-only S2/S3/S4 readiness
audit, not permission to deploy.
