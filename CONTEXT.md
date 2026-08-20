# Canonical domain language

Status: target-only. Terms define boundaries, not a live audit or deployment.

Customer is a person/organisation. VPN Account is its service account. Device is one installed app instance registered to a VPN Account; it is neither a Customer nor a billing identity. White-List Entitlement is an additive, individually revocable right owned by one VPN Account. It selects a Transport Profile and Compatibility Preset and is default DISABLED, so ordinary VPN remains available.

Transport Profile references public-host policy and an Origin Route; Origin Route is internal CDN-to-sidecar routing and never subscription material. Edge Candidate is discovered/unpublished; Approved Edge is linked to a profile only after repeated evidence and explicit approval. Transport Release is immutable candidate/published/retired configuration; each Data-plane Instance independently reconciles a selected release on S1, S2, S3, or S4.

An entitlement provisions opaque Xray User identities per Data-plane Instance. Meter Epoch scopes counters between process starts. Usage Samples are cumulative readings in an epoch; positive idempotent deltas create Usage Intervals. Billing Period groups those intervals. Tariff Snapshot freezes units, price, basis and limits for an interval. Ledger Entry is immutable money movement; changes are separate adjustments/reversals.

Suspension prevents CDN publication/new sessions for one entitlement only; Grace is a bounded policy allowance. Canary is an approved limited audience bound to a release/edge/profile. Rollback Point is a validated prior immutable release plus scoped reversal procedure. Relationship: `Customer → VPN Account → Device`; `VPN Account → White-List Entitlement → Transport Profile → Transport Release → Data-plane Instance → Xray User → Meter Epoch → Usage Sample → Usage Interval → Billing Period/Tariff Snapshot → Ledger`; approved edge and origin route are profile/release lifecycle references; `Suspension/Grace/Canary/Rollback Point` modify only their stated lifecycle boundary.

Terminology note: operational exclusion is **olcRTC**. The verbatim owner source uses historical spelling **OLCTRC**; it means the same transport and is retained only in `MASTER_REQUIREMENTS.md` as immutable source text.
