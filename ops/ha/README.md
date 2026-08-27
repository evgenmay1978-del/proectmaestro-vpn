# Isolated HA and DR proofs

Everything in this directory is repository-only test and recovery tooling. The
CI harness creates three loopback rqlite voters and ephemeral mTLS/GPG material
under `RUNNER_TEMP`. It must never receive production addresses, credentials,
customer data, GitHub environments or repository secrets.

The dedicated workflow `.github/workflows/ha-dr-restore-drill.yml` runs:

1. Go formatting, shell syntax, unit, race and vet gates.
2. `test-dr-workflow-policy.py`, which fails closed on unpinned actions,
   excessive permissions, production secret/environment access, artifact
   upload, missing timeout or missing unconditional cleanup.
3. `test-backup-rqlite.sh` for signed manifest-v2 attempt binding,
   authenticated encrypted backup verification, fail-closed v2 argument
   parsing, tamper, wrong-key and signer rejection.
4. The `backup_worker` unit, security and identity-race suites for fenced
   leases, crash recovery, fresh versioned-object readback and safe cleanup.
5. `test-restore-rqlite.sh` for fresh-cluster restore, repeat rejection,
   schema/business/receipt/shadow parity, split-process epoch fencing,
   exact-once activation, one-voter availability and two-voter write rejection.
6. An unconditional guarded `ci-rqlite-cluster.sh stop`.

Manifest v1 remains verifiable for historical restore inspection but is never
RPO-eligible. Manifest v2 signs the backup attempt identity, captured
generation, lease fence and object key. The worker may advance
`verified_generation` only after an exact key/version readback whose ciphertext
digest and signed fields match the active fenced attempt. The production
durable watermark store and versioned-storage adapter are separate required
integration gates; this repository-only worker proof does not substitute for
them.

## Production backup adapter operations

This section is a provisioning contract, not a ready-to-use secret-bearing
configuration. Never commit field values. The strict worker config contains
these field names:

- identity: `version`, `holder_id`;
- rqlite: `rqlite_endpoints`, `rqlite_credentials_file`, `rqlite_ca_file`,
  `rqlite_cert_file`, `rqlite_key_file`;
- Yandex Object Storage: `yandex_endpoint`, `yandex_region`, `yandex_bucket`,
  `yandex_prefix`, `yandex_credentials_file`;
- runtime tools and inputs: `runtime_dir`, `backup_script_path`,
  `verify_script_path`, `keys_path`, `gpg_path`, `python_path`, `gpg_home`;
- provenance: `signer_fingerprint`, `recipient_fingerprint`,
  `repository_commit_sha`, `build_run_id`, `capability_evidence_file`;
- time bounds: `lease_ttl_seconds`, `capability_ttl_seconds`,
  `deadline_seconds`, `command_timeout_seconds`, `max_transitions`;
- byte bounds: `max_response_bytes`, `max_backup_bytes`, `max_image_bytes`,
  `max_bundle_bytes`, `max_archive_bytes`, `max_extracted_bytes`.

The rqlite credential file schema is `version`, `username`, `password`; the
Yandex credential file schema is `version`, `access_key_id`,
`secret_access_key`. Capability evidence contains exactly `version`,
`generation`, `issued_at_unix`, `expires_at_unix`, `rqlite_endpoints`,
`rqlite_ca_sha256`, `rqlite_cert_sha256`, `rqlite_key_sha256`,
`yandex_endpoint`, `yandex_region`, `yandex_bucket`, `yandex_prefix`,
`object_probe_key`, `object_probe_version_id`, `object_probe_sha256`,
`object_probe_size_bytes`, `signer_fingerprint`, `recipient_fingerprint`,
`verify_script_sha256`, `gpg_sha256`, and `python_sha256`. This documentation
intentionally supplies no production values.

Provisioning is manual and approval-gated:

1. Create the dedicated `maestro-backup` service user and group. Install the
   repository HA templates under their checked logical names
   `maestro-ha-backup.service` and `maestro-ha-backup.timer`; do not enable or
   start them yet.
2. Create `/etc/maestro/backup-worker.json` and every referenced credential,
   mTLS, GPG, executable and capability-evidence file outside Git. Files that
   the systemd unit reads at mode `0600` must be owned by
   `maestro-backup:maestro-backup`; root-owned public inputs use `0644`, and
   root-owned executable inputs use `0755`. Root-owned mode `0600` is valid
   only for a separate manual invocation by root, not for the service unit.
   Private directories are `0700`. Inputs must be regular, single-link,
   non-symlink files owned by root or the service UID as permitted above.
3. Pin `runtime_dir` to the persistent state path
   `/var/lib/maestro-backup`. The unit creates it through
   `StateDirectory=maestro-backup` with mode `0700`, so an encrypted resume
   bundle survives oneshot completion and reboot. `/run/maestro-backup` is the
   separately bounded systemd runtime directory and is not the resume store.
4. Enable bucket versioning manually and provision one bucket, one configured
   prefix and one pinned probe object/version. Grant only the API operations the
   adapter uses: `GetBucketVersioning`, `PutObject`, exact-version `GetObject`,
   and bounded `ListObjectVersions`. Do not grant object delete, delete-marker,
   lifecycle, bucket-policy, bucket-versioning mutation or bucket-delete
   capability.

Every successful PUT must return a non-empty, non-`null` `VersionId`. The
worker persists that exact ID and authenticates readback by exact object key
plus `VersionId`. An unknown PUT is adopted only when bounded version listing
finds exactly one candidate whose metadata, length, full ciphertext SHA-256 and
authenticated manifest all match. ETag is not an object-identity or SHA-256
proof, and `IsLatest`/an unversioned latest read is mutable; none can authorize
acknowledgement.

RPO health is red when there is no verified identity/timestamp or the verified
age is greater than 60 minutes. Exactly 60 minutes is the boundary, not an
automatic breach. Report the durable dirty-versus-verified generation gap
separately as pending or caught up; do not hide it behind object-store health.

Cutover requires a separately captured, owner-approved proof. Set the shared
control-plane mode to exact `rqlite`, then prove each legacy unit
(`maestro-backup.service`, `maestro-backup.timer`, and
`maestro-backup-onchange.path`) is stopped, disabled and masked. Validate that
evidence with
`python ops/ha/test-backup-systemd-policy.py --cutover-evidence <reviewed-0600-evidence-path>`;
`Conflicts=` alone is insufficient. The evidence object contains exactly
`version` (`1`), `control_plane_mode` (`rqlite`), `ha_enable_requested`
(`true`), and `legacy_units`. Each of the three exact legacy unit entries
contains `active: false`, `enabled: false`, and `masked: true`. The evidence
path must be a regular, non-symlink, single-link file with mode `0600`, owned
by the UID invoking the validator. Only a later explicit approval may enable
the HA timer. The repository templates themselves remain inert.

Before the irreversible first live rqlite business-write marker, rollback
requires a write freeze and a proved hard fence. After that marker, legacy/S1
backup and JSON/SQLite truth are forbidden rollback targets: rollback stays
inside rqlite truth, or uses write-freeze plus a verified rqlite export. The
runtime has no object-delete capability, so retained versions are never pruned
by this worker.

Authoritative references:

- [focused production-adapter plan](../../docs/superpowers/plans/2026-08-23-maestrovpn-ha-backup-production-adapters.md)
- [parent HA operations/cutover plan](../../docs/superpowers/plans/2026-08-09-maestrovpn-ha-04-operations-cutover.md)
- [exact-SHA handoff evidence](../../CONTEXT_HANDOFF.md) — updated only after
  all required GitHub runs finish

No backup is uploaded as an artifact. Successful CI is evidence for a
repository implementation only; it does not authorize production deployment,
server access, import, restart or customer mutation.
