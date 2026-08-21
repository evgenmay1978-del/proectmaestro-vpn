# MaestroVPN HA Repository-Only Shadow Export Design

**Date:** 2026-08-12
**Status:** approved design, implementation not started
**Scope:** repository code and isolated GitHub Actions rqlite only

## Goal

Make the existing redacted shadow comparison reproducible. Today
`ops/ha/shadow-verify.sh` validates and compares two explicit JSON exports, but
the repository contains only hand-written synthetic inputs. This slice adds
deterministic producers for both sides and proves parity against the real
three-node rqlite CI harness.

This work does not collect live server data, configure a production endpoint,
enable `maestro-import` apply mode, deploy services, restart bots, change DNS,
publish OTA, or mutate customer state.

## Decision

Implement one versioned `ShadowExport` model with two pure producers:

1. a legacy producer derived from an already validated `ImportPlan`;
2. a candidate producer derived from canonical rows returned by an injected
   read-only source.

The same canonical encoder writes both exports. The existing verifier remains
a separate process and receives only explicit file paths and a protected
run-local salt file.

No live collector or network-capable command is added in this slice. The
candidate producer is exercised through the existing isolated rqlite test
adapter and three-node GitHub Actions cluster. Production I/O adapters require
a later, separately approved design.

## Components

### Versioned export model

Add an importer-owned model matching the strict schema already accepted by
`shadow-verify.sh`:

- `schema_version` is exactly `1`;
- customers contain only stable identity HMAC, absolute expiry, generation,
  sorted protocol tags, sorted node set and redacted Maestro/Karing URL shapes;
- orders contain only order HMAC, canonical state and result expiry;
- settings and principals are represented by canonical SHA-256 fingerprints;
- OTA contains exact version code, version name, APK SHA-256 and APK size.

Raw login, raw token, UUID, SubID, protocol credential, private URL, bot token,
numeric Telegram ID, encryption envelope and secret bytes are forbidden in the
model and encoded output.

### Legacy producer

`ShadowFromPlan` accepts only a blocker-free, fully validated `ImportPlan` and
explicit public URL-shape policy. It never reopens legacy files and never
discovers inputs implicitly.

It preserves exact expiry and generation, derives stable HMAC identities from
already protected plan fields, normalizes ordering, fingerprints public
settings and principal roles, and requires one unambiguous approved OTA
manifest. Missing protocol/node information, malformed URL-shape policy or an
ambiguous OTA value is a hard error rather than an incomplete export.

### Candidate producer

`ShadowFromCandidate` consumes a narrow injected interface that returns
linearizable canonical projections. Its implementation for tests uses the
existing `rqlite.RQLite` boundary and queries only business rows after a fully
applied import run.

The producer must reject:

- a target whose applied source digest does not equal the expected source;
- incomplete or duplicate customer/order identities;
- deleted rows leaking into the active projection;
- disabled credentials or revoked tokens presented as active;
- missing frozen desired targets;
- malformed public setting, principal or OTA data;
- any non-linearizable or partial query result.

It does not decrypt protected values. URL fields are emitted only as approved
constant shapes containing `{opaque-token}`.

### Canonical encoder

One encoder validates the complete model, sorts all maps/sets and writes JSON
atomically with mode `0600`. Repeated encoding of the same model must be
byte-for-byte identical. Existing files are not silently overwritten unless
the caller selected an explicit fresh output path.

Errors are fixed redacted categories. They may include a field name but never
an input value, HMAC, URL, SQL argument or secret material.

## Data flow

```text
validated synthetic snapshot
        |
        v
 blocker-free ImportPlan -----> ShadowFromPlan -----> legacy.export.json
        |
        v
 isolated three-node rqlite import
        |
        v
 linearizable canonical rows -> ShadowFromCandidate -> candidate.export.json

 explicit files + protected run salt
        |
        v
 ops/ha/shadow-verify.sh -> match (0), mismatch (2), invalid/system (3)
```

No step above talks to S1-S4. GitHub-hosted CI starts and destroys only its
isolated runner-local cluster.

## Consistency and failure rules

The parity proof runs only after `import_runs.status='applied'`, every expected
batch receipt exists, and the recomputed business digest equals the committed
target digest. Candidate reads are linearizable and tied to the expected
source digest.

Any missing row, duplicate HMAC, unexpected state, source-digest mismatch,
schema mismatch or malformed projection fails closed. A mismatch report uses
only the existing run-salt-derived subject IDs. Export files and reports are
test artifacts containing synthetic HMACs only; they must never contain fixture
plaintext secrets.

Unknown write outcomes remain governed by the importer receipt logic. The
shadow layer is read-only and never retries or repairs business writes.

## Test strategy

Implementation follows RED -> GREEN.

Unit tests first require:

- byte-stable canonical encoding independent of input order;
- exact legacy expiry/generation/protocol/node/OTA preservation;
- rejection of missing or ambiguous fields;
- candidate source-digest and applied-run binding;
- logical-delete filtering and active credential/token checks;
- absence of raw identity, token, private URL and encrypted envelope bytes in
  encoded exports and errors;
- atomic `0600` output behavior;
- verifier exit `0` for equal exports, `2` for a controlled difference and `3`
  for invalid input.

The integration test then:

1. starts the existing isolated three-node rqlite cluster;
2. applies the synthetic full snapshot through `RQLiteApplyStore`;
3. builds the legacy export from the exact plan;
4. builds the candidate export from linearizable cluster rows;
5. runs `shadow-verify.sh` with a protected synthetic salt;
6. requires status `match`, an empty differences list and no secret markers;
7. resets/stops the runner-local cluster using the existing safe harness.

GitHub Actions is authoritative for Go, race, vet, shell contract and real
rqlite integration. No local heavy build is required.

## Explicit non-goals

This slice does not:

- add SSH, HTTP or filesystem collectors for live S1-S4;
- read current Telegram bot databases or credentials;
- wire the production `maestro-import` factory;
- choose production endpoints, CA files, client certificates or key files;
- perform a real import or shadow comparison with customer data;
- implement backup, restore, fencing, DNS, TLS, panel or cutover workflows;
- modify Android, TV UI/assets, VPN protocols, release or OTA behavior.

## Completion boundary

This design is complete only when the two producers, canonical encoder and
real three-node CI parity proof are green at one exact Git SHA. That result
proves repository behavior on synthetic redacted data only.

Parent Plan 02 Task 6 and the HA project remain incomplete. Production remains
`NO-GO (repository implementation only)` until separately approved live
inventory/export, production factory, backup/restore, bot fence, TLS/DNS and
cutover gates all pass.
