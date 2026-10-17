# MaestroVPN agent payload boundary design

Date: 2026-08-12
Status: owner-approved architecture; repository design only
Production: NO-GO

## Outcome

Keep one customer subscription and one canonical expiry/generation in the HA
control plane while delivering every enabled protocol to its intended local
service on S1-S4. A node-service agent may decrypt only its own desired payloads;
it cannot decrypt another service or another node. Existing customer access and
the Android/TV clients are outside this change.

Examples include VLESS, Hysteria2, AnyTLS, NaiveProxy, AmneziaWG and any other
protocol represented by an enabled desired-state service. The design does not
hard-code one permanent protocol list. WDTT and olcRTC retain their existing
account allowlist and must not become visible to ordinary customers.

## Considered approaches

1. One cluster-wide payload key. Simple, but compromise of one server exposes
   every service payload. Rejected.
2. One key per customer. It follows subscription ownership but creates excessive
   key distribution and still does not isolate services on the same customer.
   Rejected.
3. One independently rotatable key ring per `(node_id, service_id)`. The control
   plane encrypts each service projection for its destination; only that local
   agent receives the corresponding key ring. Selected.

## Data model and boundaries

The canonical subscription remains a business object in rqlite. A successful
create, renew, expire or delete transaction advances the customer's absolute
generation once and writes one desired projection for every required
`(node_id, service_id)`. A temporarily unavailable node retains pending desired
state and later converges to the same absolute generation without duplicating a
customer or extending expiry from local state.

`DesiredEntry.Payload` remains a signed encrypted transport envelope. The agent
must not pass it to a driver. After command signature, identity, lifetime,
snapshot digest, strong lease and local monotonic marker checks, an injected
`PayloadOpener` authenticates and opens every entry into an in-memory
`MaterializedSnapshot`. Drivers accept only this materialized type.

Plaintext material must never be persisted in the aggregate marker, logs,
receipts, errors, GitHub artifacts or handoff documents. The materialized
snapshot lifetime is one serialized apply operation. Code must release
references immediately after apply; Go cannot promise physical memory wiping,
so the process must not retain or cache payloads.

## Cryptographic contract

Each envelope declares a key version from the destination service key ring. Its
AES-GCM AAD uses a canonical, versioned encoding of:

```text
maestrovpn:desired:v1
node_id
service_id
customer_id
generation
operation_id
tombstone
payload_kind
```

Every field is length-delimited; concatenated strings are forbidden. Moving an
envelope between nodes, services, customers, generations, operations, payload
kinds or tombstone state must fail authentication.

The signed snapshot continues to bind the exact encrypted envelope bytes. After
opening, the agent validates a separately committed plaintext schema/digest
inside the authenticated payload before any driver call. This prevents treating
the envelope digest as proof of driver-visible configuration. Unknown key
version, missing ring, malformed plaintext, schema mismatch, digest mismatch or
AAD failure is fail-closed and makes zero driver calls.

Key rings support current encryption version plus explicitly referenced older
decryption versions during rotation. Readiness is false if any desired envelope
references a missing version. Rotation is staged per node-service; production
key generation/distribution/activation is a later approved operational task.

## Apply sequence

1. Verify exact mTLS dispatcher identity and bounded HTTP body.
2. Verify Ed25519 signature, canonical command, destination identity, lifetime
   and encrypted snapshot digest.
3. Strong-check current cluster epoch, node incarnation, holder, fence and
   snapshot digest.
4. Load the local marker; reject stale generation, same-generation hash conflict
   and marker read/validation failure.
5. Open and validate every payload into one complete materialized snapshot. No
   partial snapshot reaches a driver.
6. Inspect/prepare using only the materialized snapshot.
7. Immediately before commit, repeat the strong lease/current-snapshot check.
   A changed desired snapshot rolls back prepared local state.
8. Commit, health-check and obtain actual per-entry observed hashes.
9. Fsync the aggregate marker, then return per-entry receipts. A receipt never
   changes canonical expiry or becomes business truth.

Tombstones are authenticated projections too. They contain no reusable
credential and instruct the destination driver to remove exactly that customer's
local state at the specified generation.

## Service projection and subscription assembly

The control plane owns which protocols and servers belong to an account. It
builds separate absolute service projections, for example S2 Hysteria2/AnyTLS/
NaiveProxy and S1/S3/S4 local 3x-ui projections, while the subscription endpoint
continues assembling all allowed, ready protocol links for the customer.

Protocol availability is derived from canonical entitlement plus matching
applied receipts, never by reading a local panel's expiry. An unavailable target
may be reported as provisioning/degraded without removing already valid links on
healthy targets. Restricted WDTT/olcRTC projections require the protected
allowlist before desired-state creation and again before subscription rendering.

## Error and availability behavior

- No quorum, stale fence or decryption failure performs no new side effect.
- A failure on one target does not block dispatcher work for other targets.
- Last-good local VPN configuration remains active after prepare/validation
  failure.
- Ambiguous HTTP outcome is reconciled by aggregate and observed hashes; it does
  not create another business generation.
- Missing keys make the affected agent unready and create an owner-visible
  redacted alarm; they never trigger fallback to a cluster-wide key.

## Verification

RED-first tests must prove:

- node/service/customer/generation/operation/kind/tombstone AAD mutation fails;
- unknown or missing key version and malformed payload make zero driver calls;
- drivers cannot compile against encrypted `DesiredSnapshot` input;
- all entries open before prepare, with no partial apply;
- plaintext/envelope digest distinction and canonical serialization;
- strong desired/fence recheck rejects change immediately before commit;
- key rotation reads referenced old versions but new encryption uses current;
- one customer's renewal produces correct projections and receipts across all
  enabled services without duplicate users or divergent expiry;
- WDTT/olcRTC remain absent for ordinary accounts;
- ordinary protocols on healthy targets continue when another target is down.

Targeted package tests run first. Both `HA control-plane checks` and
`HA DR restore drill` must be GREEN on the exact commit before continuing to
local drivers. Production deployment, key distribution and customer migration
remain forbidden by this design.

## Rollout and rollback

This change is repository-only. Runtime wiring happens only after real local
drivers exist. A later deployment must provision node-service key rings with
mode 0600, prove readiness and old-version coverage, canary one non-customer
fixture, and preserve the current services as last-good rollback. No server
installation, restart, traffic change or key rotation is authorized here.
