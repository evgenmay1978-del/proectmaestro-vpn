# MaestroVPN HA Production Import Factory Design

**Date:** 2026-08-12
**Status:** approved design, implementation not started
**Scope:** repository code and isolated GitHub Actions rqlite only

## Goal

Replace the intentional `factory=nil` boundary in `maestro-import` with one
fail-closed production composition that can construct the already implemented
rqlite apply store from explicit protected inputs. The command must retain the
existing deterministic plan, blocker, resume, target-digest and shadow-parity
contracts.

This slice makes a production-capable binary and proves it only against the
isolated three-node rqlite GitHub Actions harness. It does not collect live
data, create a production cluster, run schema migrations on production, import
a real customer, deploy or restart any service, fence a legacy writer, alter a
bot, change DNS/TLS, publish OTA, or modify Android/TV behavior.

## Decision

Wire `main()` to one production factory, but invoke that factory only after the
existing snapshot has planned cleanly and apply-specific approvals have been
validated. Dry-run remains network-free and does not read rqlite credentials,
control-plane keys, the legacy trial salt or receipt-signing material.

Production apply requires all of the following explicit inputs:

- exact snapshot and immutable report paths;
- exact previously approved plan digest and stable run ID;
- a protected rqlite target configuration;
- a protected versioned control-plane key bundle;
- the exact protected legacy trial salt when the snapshot contains trials;
- a protected Ed25519 import-receipt signing key and explicit receipt path.

No environment fallback, default production endpoint, implicit file discovery,
SSH, DNS lookup for inventory, plaintext CLI secret or dual-write path is
allowed.

## Command and configuration contract

### CLI boundary

Keep the existing flags and add apply-only explicit paths:

- `--rqlite-config`;
- `--receipt`;
- `--receipt-signing-key-file`.

`--key-file` becomes the strict versioned control-plane key bundle. The
existing `--legacy-trial-salt-file` remains a distinct protected raw-byte
input. Paths may be visible in process metadata; their contents may not be
placed in argv, environment-derived defaults, stdout, stderr or reports.

Dry-run plans and writes the redacted report exactly as today, never constructs
the factory and never opens any protected apply input. Apply requires every
relevant flag and rejects unknown arguments before I/O.

### Protected rqlite target configuration

Use strict JSON with unknown fields rejected and `schema_version=1`. It contains
exactly three voter records with node IDs `S2`, `S3`, `S4`, one HTTPS base
URL per voter, and explicit CA, client certificate and client private-key paths.
The URLs must be unique and contain no userinfo, query, fragment or path. HTTP,
a missing voter, an extra voter, redirects and TLS verification bypass are
forbidden.

The configuration file and client private key must be regular, bounded files
with no group/other permission bits. CA and client certificate are public
material but must still be regular bounded files. The factory uses the existing
rqlite client with mandatory CA verification, mandatory client certificate,
TLS 1.2 minimum, bounded timeout/response sizes and redirect refusal. Basic
Auth is not introduced by this slice; authentication is mTLS only.

The canonical SHA-256 of normalized node IDs, endpoint origins, CA certificate
fingerprint and client-certificate fingerprint becomes
`target_config_sha256`. The receipt carries only that digest, not endpoint or
path values. A later deployment manifest must approve the same digest before
any live import.

### Versioned control-plane key bundle

Use strict protected JSON with `schema_version=1`, one positive current
encryption-key version, a bounded set of versioned 32-byte AES keys, and one
distinct 32-byte HMAC key. Binary values are canonical base64. Duplicate
versions, missing current key, noncanonical base64, wrong lengths or identical
encryption/HMAC keys fail before any write.

Construct the existing `controlplane.SecretBox`. Every encrypted secret already
present in the normalized snapshot must reference an available key version and
authenticate under its exact owner/field/kind scope before the factory exposes
an apply store. Plaintexts are zeroed immediately and never enter the plan,
report, receipt, error or SQL argument. This binds imported envelopes to the
supplied key bundle without publishing key fingerprints.

## Legacy trial salt binding

The current snapshot contains only legacy/current HMAC values, so merely
checking that a salt file is non-empty cannot prove it is the original salt.
Add `legacy_trial_salt_sha256` to the versioned normalized snapshot contract.
When trials are present it is required, canonical lowercase hex and included in
the source/plan digests. When trials are absent, a supplied salt digest or salt
file is rejected as ambiguous.

For apply, hash the exact raw salt bytes without trimming or newline rewriting
and require equality with the snapshot digest. Seal those exact bytes through
`SecretBox` using the fixed scope:

- owner type: `trial_lookup`;
- owner ID: `legacy`;
- field: `salt`;
- kind: `hmac-key`.

Marshal the resulting versioned envelope canonically and construct
`NewRQLiteApplyStoreWithTrialProtection`. Zero salt and key material on every
success and error path. A missing or mismatched salt fails before the first
rqlite mutation.

## Factory sequence

The composition order is fixed:

1. Complete the bounded snapshot decode, deterministic planning and atomic
   redacted report write.
2. Reject blockers, plan-digest drift, missing run ID or invalid protected-file
   metadata.
3. Strictly decode target configuration and construct the mandatory-mTLS
   rqlite client.
4. Call only `controlplane.NewMigrator(db).Verify(ctx)`. The importer never
   calls `Migrator.Apply`; schema creation is a separate future gate.
5. Decode the key bundle, authenticate snapshot envelopes, bind and seal the
   legacy trial salt, then construct the rqlite apply store.
6. Call existing `importer.Apply`. Its target inspection, empty/full or
   parent/delta rule, stable batches, unknown-outcome resume and final business
   digest remain authoritative.
7. Linearizable-read the completed run and batch receipts again and require the
   exact run/source/plan/parent/target digests, batch count and completed state.
8. Atomically write and fsync a signed import receipt. Only then print the
   generic success line and exit `0`.

Every operation uses a bounded context. Mutating requests are never retried by
the rqlite client; recovery re-enters with the same stable run ID and relies on
durable batch receipts.

## Signed applied-run receipt

The canonical version-1 receipt contains no customer rows or secrets. It binds:

- receipt schema version and stable run ID;
- snapshot kind plus source, plan, parent and target SHA-256 values;
- expected and applied batch counts plus a canonical batch-receipt digest;
- immutable schema version/checksum;
- `target_config_sha256`;
- signer key ID and completion time read from committed cluster state.

Sign canonical unsigned bytes with Ed25519. The signing-key file is a bounded
protected canonical seed/key encoding; the receipt contains only its
non-secret key ID and signature. Provide a pure verifier used by tests and later
cutover gates.

Receipt creation is resumable. If import commits but receipt writing is
interrupted, the command exits `3`; rerunning the same run ID verifies the
already applied run and emits the same canonical unsigned receipt. A conflicting
run/config/schema digest refuses to sign. Write through a same-directory `0600`
temporary file, fsync, atomic rename and directory sync.

## Errors and secrecy

Keep exit codes `0=clean`, `2=plan blockers`, `3=input/system`. Errors remain
short fixed messages. They never render endpoint URLs, certificate paths, raw
JSON or rqlite bodies, SQL, salt/key/envelope bytes or customer identifiers.

The redacted plan report remains writable before factory construction. The
signed receipt is a protected operational artifact and is not uploaded by
ordinary PR workflows. Tests scan stdout, stderr, report, receipt and GitHub job
logs for synthetic secret markers.

## Test strategy

Implementation follows RED -> GREEN and uses synthetic data only.

Unit tests require:

- production `main()` supplies a non-nil factory while dry-run makes zero
  factory/network/protected-file calls;
- apply rejects missing/HTTP/duplicate/non-S2-S4 endpoints, optional mTLS,
  broad private-file permissions and unknown JSON fields;
- malformed key bundles, unavailable envelope versions, wrong scoped keys,
  wrong trial-salt digest and newline drift fail before a request;
- the factory calls schema `Verify` but never schema `Apply`;
- no receipt is signed before a completed exact run can be re-read;
- receipt write interruption resumes without a second business mutation;
- changed run, schema or target-config digest cannot reuse a receipt;
- every printable failure and artifact is secret-free.

The isolated GitHub Actions proof starts the existing three-node rqlite harness
with test-only mTLS, pre-applies schema as an explicit harness step, runs the
real command for a synthetic full import, verifies the Ed25519 receipt, reruns
the same run as a no-op, exports candidate shadow state and requires exact
legacy/candidate parity. A valid but wrong client/key/salt/config input must
prove zero imported business rows.

Local Go builds are not required on the weak owner computer. Exact full Git SHA,
GitHub run/job IDs and every required step conclusion form the evidence.

## Production and compatibility boundary

Completion of this slice still reports
`NO-GO (repository implementation only)`. Before production invocation,
separate gates remain required: live read-only inventory, exact source capture,
approved target-config digest, prepared schema, verified backup and empty-cluster
restore drill, legacy-writer fencing, collision-free dry-run, write freeze/final
delta, shadow parity, canary, cutover and rollback.

S1-S4 services, current panels, both Telegram bots, customer expiry, protocol
credentials, payment flow, subscription URLs, Android mobile, TV UI/assets,
Release and OTA remain unchanged by this slice.

## Completion boundary

This design is complete when the repository has one tested non-nil production
factory, strict mTLS/key/salt configuration, exact schema verification,
resumable signed applied-run receipts and full synthetic GitHub evidence. It
does not authorize a real import or deployment.
