# Canonical domain language

Status: target-only. These terms describe intended boundaries, not a live audit or deployment.

- **Customer** owns one or more VPN Accounts; it is not an app instance.
- **Device** is one installed application instance and never the financial identity.
- **VPN Account** owns ordinary service and zero or more White-List Entitlements.
- **Xray User** is a technical per-instance identity with an opaque stats key; it is never rendered as an internal identifier.
- **White-List Entitlement** is an additive, individually revocable right to CDN nodes; default `DISABLED` never disables ordinary VPN.
- **Transport Profile** is a versioned public-host, origin-route, country and transport policy reference.
- **Compatibility Preset** is a versioned capability contract used by the subscription renderer.
- **Origin Route** is internal CDN-to-sidecar routing; it is sensitive and never subscription material.
- **Edge Candidate** is discovered but unpublished; an **Approved Edge** has repeated evidence and an approval record.
- **Transport Release** is an immutable candidate/published/retired data-plane configuration with checksum.
- **Data-plane Instance** is one independently reconciled target on S1, S2, S3, or S4; multi-node is not one shared process.
- **Meter Epoch** scopes cumulative counters between process starts/resets; **Usage Sample** is one cumulative reading; **Usage Interval** is its positive idempotent delta.
- **Billing Period** groups intervals; **Tariff Snapshot** freezes units, price, basis and limits; **Ledger Entry** is immutable money movement.
- **Suspension** stops only CDN entitlement; **Grace** is bounded policy allowance; **Canary** is a limited approved audience; **Rollback Point** is a validated prior immutable release.

Relationship: `Customer → VPN Account → White-List Entitlement → (Transport Profile + Compatibility Preset) → Xray User per Data-plane Instance → Usage Sample → Usage Interval + Tariff Snapshot → Ledger Entry`. Never use the single word “client” for all of these entities.
