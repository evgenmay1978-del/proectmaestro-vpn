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
3. `test-backup-rqlite.sh` for authenticated encrypted backup verification,
   tamper, wrong-key and signer rejection.
4. `test-restore-rqlite.sh` for fresh-cluster restore, repeat rejection,
   schema/business/receipt/shadow parity, split-process epoch fencing,
   exact-once activation, one-voter availability and two-voter write rejection.
5. An unconditional guarded `ci-rqlite-cluster.sh stop`.

No backup is uploaded as an artifact. Successful CI is evidence for a
repository implementation only; it does not authorize production deployment,
server access, import, restart or customer mutation.
