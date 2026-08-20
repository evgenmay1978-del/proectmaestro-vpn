# Research protocol
Status: target-only. This is a conservative design record, not evidence of a live audit, deployment, migration, test completion, or approval.

## Materialized policy
Recover source-of-truth and upstream semantics through approved read-only evidence. Classify each statement in VERIFIED_FACTS; contradictions become ADR inputs. Source beats copied configuration, and no secret/endpoint/URI/payload is put in notes.

## Gates and safety
Ordinary VPN, existing identity, subscription, balances, panel and TV/mobile behaviour remain non-regression boundaries. Work starts only in an isolated branch/process/config/release. Any live inventory, backup/restore, service/origin/firewall/database change, client switch, charging, OTA, reboot or deletion is a stop gate requiring explicit owner approval. Sensitive origin context is referenced by MASTER section, never repeated here.

## Evidence rule
Record source, date, redacted release/checksum and outcome before changing status. WDTT, qWDTT, CSQTT and olcRTC remain deferred.
