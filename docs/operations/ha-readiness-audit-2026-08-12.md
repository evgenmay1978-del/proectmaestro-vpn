# Production readiness audit — public redacted summary — 2026-08-12

Status: **INCOMPLETE / PRODUCTION NO-GO**.

This audit followed the completed repository DR proof. It performed no package
installation, file write, service restart, import, database query for customer
rows, bot action, DNS/TLS/OTA change or traffic switch.

## Repository evidence

- The disaster-recovery implementation passed its dedicated workflow.
- The ordinary HA workflow passed on the same code revision.
- The disaster-recovery workflow uploaded no production data artifacts.

## Verified-node summary

One node was authenticated with an existing explicit client key and a
pre-existing strict host identity.

- Existing customer-facing VPN, panel and bot services were active, with no
  failed service units observed.
- The new HA database and HA panel were not yet installed.
- A current, restorable application backup has not yet been proven.
- Storage headroom must be included in the capacity and rollback gate.
- Some legacy bot database and log permissions are broader than the future HA
  policy and must be tightened during a separately approved change window.
- No configuration value, secret, customer identifier or database row was read
  or published.

## Remaining-node identity gate

Both remaining endpoints are reachable, but neither identity is present in the
available trusted sources. Network-observed identities are not accepted as
proof. They were not added to a trust store, and no authentication was attempted.

Before continuing, obtain each SSH host identity independently from its provider
console or an already authenticated console. Only an exact match authorizes
pinning and the same read-only audit. Do not bypass strict host-key checking or
delete old host-key evidence.

## Current blockers

1. Two node identities are not independently authenticated.
2. The HA database is not installed on the verified node.
3. The HA panel/readiness endpoint is not installed on the verified node.
4. Legacy bot database/log permissions are broader than the future HA policy.
5. A current verified Maestro production backup has not been identified.
6. Storage headroom must be included in capacity and rollback design.

No production deployment may advance until the remaining nodes are authenticated
and audited.
