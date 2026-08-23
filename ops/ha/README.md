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

No backup is uploaded as an artifact. Successful CI is evidence for a
repository implementation only; it does not authorize production deployment,
server access, import, restart or customer mutation.
