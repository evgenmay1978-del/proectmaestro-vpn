# Panel integration contract
Status: target-only. This is a conservative design record, not evidence of a live audit, deployment, migration, test completion, or approval.

## Materialized policy
Panel intent adds entitlement, profile, preset, approved edge, release, usage, tariff, limits, suspension and audit controls to the existing source of truth after audit. Desired state/outbox is reconciled per node. CDN suspension never reuses account deletion/ordinary disable.

## Gates and safety
Ordinary VPN, existing identity, subscription, balances, panel and TV/mobile behaviour remain non-regression boundaries. Work starts only in an isolated branch/process/config/release. Any live inventory, backup/restore, service/origin/firewall/database change, client switch, charging, OTA, reboot or deletion is a stop gate requiring explicit owner approval. Sensitive origin context is referenced by MASTER section, never repeated here.

## Evidence rule
Record source, date, redacted release/checksum and outcome before changing status. WDTT, qWDTT, CSQTT and olcRTC remain deferred.
