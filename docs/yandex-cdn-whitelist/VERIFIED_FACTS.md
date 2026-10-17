# Verified facts and provenance register

Status: target-only. This register does not call any server, CDN, client, backup, billing path, or edge “verified” without dated, cited evidence.

| Class | Meaning | Permitted wording |
| --- | --- | --- |
| OWNER-PROVIDED ACCEPTANCE CLAIM | Owner/master requirement. | Owner-provided claim; not live verified. |
| CODE/REPO FACT | Direct local checkout observation with path/date. | Local repository fact. |
| UNVERIFIED | Needs audit/test observation. | Unverified; evidence required. |

| ID | Class | Claim | Source and date | Evidence/status |
| --- | --- | --- | --- | --- |
| F-001 | OWNER-PROVIDED ACCEPTANCE CLAIM | White-list is additive and defaults OFF. | MASTER §§1/23, supplied 2026-08-20. | Not independently revalidated. |
| F-002 | OWNER-PROVIDED ACCEPTANCE CLAIM | Four targets S1/S2/S3/S4 are mandatory. | Owner direction, 2026-08-20. | Target only; no node contacted. |
| F-003 | OWNER-PROVIDED ACCEPTANCE CLAIM | CDN body/literal-edge/idle checks are acceptance work. | MASTER §§3–5/43, supplied 2026-08-20. | Sensitive test context, not re-tested. |
| F-004 | CODE/REPO FACT | Local docs validator, manifest generator and tests exist. | `scripts/`, reviewed 2026-08-20. | Local-only evidence. |
| U-001 | UNVERIFIED | Production inventory, versions, ports, counts, API and subscriptions. | Approved read-only audit required. | Do not infer. |
| U-002 | UNVERIFIED | Backup/restore, firewall, edge approval, client support, metering and billing. | Isolated/live evidence required. | No deployment or charging claim. |

Sensitive owner-supplied test context is canonical only in MASTER_REQUIREMENTS. It is not a Git-safe public fact and must not be copied into derivative docs, fixtures, telemetry, reports, or logs.
