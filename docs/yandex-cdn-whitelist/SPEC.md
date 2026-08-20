# White-list transport specification
Status: target-only. This is a conservative design record, not evidence of a live audit, deployment, migration, test completion, or approval.

## Materialized policy
Entitlement states are DISABLED, PROVISIONING, ACTIVE, GRACE, SUSPENDED, ERROR and EXPIRED. ACTIVE additively renders CDN nodes; disabled/suspended removes only those nodes. Profile, edge, release and usage records use opaque IDs, checksums, state and audit references.

## Gates and safety
Ordinary VPN, existing identity, subscription, balances, panel and TV/mobile behaviour remain non-regression boundaries. Work starts only in an isolated branch/process/config/release. Any live inventory, backup/restore, service/origin/firewall/database change, client switch, charging, OTA, reboot or deletion is a stop gate requiring explicit owner approval. Sensitive origin context is referenced by MASTER section, never repeated here.

## Evidence rule
Record source, date, redacted release/checksum and outcome before changing status. WDTT, qWDTT, CSQTT and olcRTC remain deferred.
