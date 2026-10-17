# Rollback policy
Status: target-only. This is a conservative design record, not evidence of a live audit, deployment, migration, test completion, or approval.

## Materialized policy
Release/config/entitlement rollback selects a prior immutable release,
unpublishes edge, disables one entitlement or returns billing to shadow. That
scope preserves ordinary VPN, usage, ledger, balance, identity and unrelated
nodes. No duration or fallback is claimed until approved isolated/live evidence
exists.

## Schema v4 forward-only boundary

Schema v4 adds the durable White-List entitlement identity mapping. The
checksummed migrator rejects an unknown newer migration, so an older v3 binary
must not be started against a database that has recorded v4. Replacing only the
executable is not a valid rollback after v4 is applied.

Before the first v4 apply, the operator must create and verify a pre-v4 rqlite
snapshot plus its authenticated manifest. A schema rollback means restoring
that verified pre-v4 rqlite snapshot into a fresh empty cluster through the
existing fenced DR procedure, after same-turn owner approval; it is never an
in-place down migration or an overwrite of an ambiguous cluster. The v4 table
allows deletion only as part of the existing audited customer purge cascade.
Unlike release/config/entitlement rollback, schema snapshot restore can discard
all writes and identity creation after the snapshot recovery point. It is a
destructive disaster-recovery action, permitted only with an explicitly accepted
recovery point and data-loss window, frozen writes, a verified snapshot, a fresh
empty target cluster, and same-turn owner approval.
Production remains NO-GO until this exact backup/restore path has isolated
evidence on the release candidate.

## Gates and safety
Ordinary VPN, existing identity, subscription, balances, panel and TV/mobile behaviour remain non-regression boundaries. Work starts only in an isolated branch/process/config/release. Any live inventory, backup/restore, service/origin/firewall/database change, client switch, charging, OTA, reboot or deletion is a stop gate requiring explicit owner approval. Sensitive origin context is referenced by MASTER section, never repeated here.

## Evidence rule
Record source, date, redacted release/checksum and outcome before changing status. WDTT, qWDTT, CSQTT and olcRTC remain deferred.
