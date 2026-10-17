# Panel integration contract
Status: source contracts and remaining integration requirements. Source implementation is not evidence of live deployment, migration, billing activation or production readiness.

## Materialized policy
Panel intent adds entitlement, profile, preset, approved edge, release, usage, tariff, limits, suspension and audit controls to the existing source of truth after audit. Desired state/outbox is reconciled per node. CDN suspension never reuses account deletion/ordinary disable.

## Gates and safety
Ordinary VPN, existing identity, subscription, balances, panel and TV/mobile behaviour remain non-regression boundaries. Work starts only in an isolated branch/process/config/release. Bounded server configuration follows the current standing owner authorization in CONTEXT_HANDOFF.md, including the 05.09.2026 SSH-only instruction, scoped backup and rollback. This does not authorize real customer charging, OTA publication, destructive operations or final customer-traffic cutover without their applicable gates. Sensitive origin context is referenced by MASTER section, never repeated here.

## Read-only sidecar usage snapshot

GET `/v1/usage` uses the existing controller mTLS connection and
`X-Maestro-Action-Key`; it opens no new listener. The key identifies the exact
currently applied desired generation. A successful no-store JSON response has
`receipt`, `sampled_at`, `users` and `unavailable_users`. Each available user
has `email`, `uplink_bytes` and `downlink_bytes`; both counters are required
nonnegative integers, never omitted/null. Both arrays are sorted, unique and
disjoint, and their union must hash to the receipt's managed-user-set digest.
The caller also binds the receipt to its expected current desired state.

The sidecar reads real Xray StatsService counters with reset=false under the
existing apply mutex, checking runtime files, boot identity and fresh receipt
before and after the read. Static/private/ordinary identities are not returned.
A missing counter pair places only that identity in `unavailable_users`:
it is not a zero sample and does not block available users. Malformed counters
or runtime drift reject the snapshot. GET does not create counters, make
customer/relay connections, alter users or write desired/receipt state.

Responses: wrong method 405; unverified client 401; invalid header 400;
non-current or unknown action 404; unavailable/corrupt runtime data 503.

## Durable producer and remaining runtime connection

The existing backend mTLS client gains typed `LookupUsage`. The shadow store
gains `EnsureCommercialProducerCursor`, which reuses the existing epoch table
and accepted source rows after restart, and
`PendingCommercialDebitEntitlementIDs`, which finds undrained intervals even
after an identity is revoked. Existing `ApplyCommercialOrdered` and
`DebitCommercialInterval` remain the durable exactly-once byte-debit path.

These seams do not themselves start a collector or enable paid access.
Remaining work in the same commercial Task 6: the 2-second production loop,
first-use bootstrap without fabricated counters, exact billing-boundary pending
handling, fleet-minimum freshness and immediate entitlement-local revoke.
Do not advance publication freshness merely because a different Origin replied.

## Evidence rule
Record source, date, redacted release/checksum and outcome before changing status. WDTT, qWDTT, CSQTT and olcRTC remain deferred.
