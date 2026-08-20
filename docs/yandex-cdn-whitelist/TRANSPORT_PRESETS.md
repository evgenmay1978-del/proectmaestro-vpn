# Transport preset policy
Status: target-only. This is a conservative design record, not evidence of a live audit, deployment, migration, test completion, or approval.

## Materialized policy
MAESTRO_ADVANCED, GENERIC_XHTTP_GET_COMPAT and EXPERIMENTAL_RESUMABLE are distinct versioned contracts. Each declares core/client range, capabilities, protection level and fixtures. Never silently lower protection, leak decryption/origin/internal ID, or mix preset fields.

## Gates and safety
Ordinary VPN, existing identity, subscription, balances, panel and TV/mobile behaviour remain non-regression boundaries. Work starts only in an isolated branch/process/config/release. Any live inventory, backup/restore, service/origin/firewall/database change, client switch, charging, OTA, reboot or deletion is a stop gate requiring explicit owner approval. Sensitive origin context is referenced by MASTER section, never repeated here.

## Evidence rule
Record source, date, redacted release/checksum and outcome before changing status. WDTT, qWDTT, CSQTT and olcRTC remain deferred.
