# Yandex Cloud CDN White-List Transport — execution plan

## Authority and constraints

- Binding requirements: `docs/yandex-cdn-whitelist/MASTER_REQUIREMENTS.md`, created verbatim from the owner request in Task 1.
- Production is read-only. Do not restart/update 3x-ui or Xray, modify existing UUIDs/inbounds/ports/firewall/database, publish OTA, or charge balances without separate approval.
- WDTT, qWDTT, CSQTT and olcRTC are out of scope and must not appear in feature diffs.
- Current production source is the regression oracle; this branch is isolated from the reviewed HA baseline.

## Task 1 — Documentation bootstrap and read-only baseline tooling

Create the required documentation tree, concise root AGENTS pointers, canonical CONTEXT glossary, exact master requirements, verified-facts/research/ADR map/spec/test/rollback/handoff documents. Add safe local scripts that render a redacted baseline manifest and validate document links; do not connect to production or capture secrets. Tests prove document inventory, required invariants, and redaction behavior.

## Task 2 — White-list domain model and subscription seam

Add additive in-memory/testable control-plane domain types for transport profiles, compatibility presets, origins, approved edges, immutable releases and per-account white-list entitlement. Implement deterministic subscription node rendering with ordinary subscription identity unchanged when entitlement is not ACTIVE. Add API contract tests and unit tests first. No production persistence migration or live panel change.

## Task 3 — Isolated sidecar release skeleton

Add release manifest/config schema validation, checksums, candidate/published/retired transitions, Xray sidecar systemd/config templates and local validation scripts. Use port 18081 only in templates; retain 18080 fallback. No server deployment. Tests cover immutable release validation, rollback selection, forbidden secret leakage and config schema.

## Task 4 — Metering and shadow-billing core

Implement pure domain logic for meter epochs, cumulative counter deltas, event idempotency, traffic basis, tariff-resolution hierarchy, exact integer billing and immutable ledger entries. Implement shadow mode only; real balance changes remain impossible. Tests cover counter reset, replay, duplicate events, no float money, tariff snapshots, limits and suspension isolation.

## Task 5 — Panel/API integration seam

Add versioned internal/API contracts and panel-facing DTOs for entitlement, health, usage, ledger and audit. Keep old clients and ordinary subscriptions behaviorally identical. Add auth/validation contract tests. Do not expose control/stats APIs publicly.

## Task 6 — Android compatibility preparation

Add mobile-gated parsing/display model and heartbeat/watchdog abstraction using the existing single VpnService and DefaultNetworkListener seam. Do not change TV UI, assets, runtime gates or publish OTA. Test no-TV behavior, no-token logging, network-change state transitions and fallback behavior.

## Task 7 — Integration evidence and release readiness

Add isolated fixture-driven Xray/Yandex acceptance harnesses, client compatibility matrix fixtures, CI jobs, two independent reviews, handoff updates and rollback evidence. Build a test APK only after backend contracts pass. Stop before any live CDN origin switch, server deployment, production migration, real charging or OTA.

Status (2026-09-02): repository-only acceptance is complete. Exact checkpoint
`670e19dcd092400252555b2ffa8ff82a89348054` passed all five applicable GitHub
workflows and independent review with no P0/P1. The Android artifact was built
only as a GitHub test APK; it was not installed, signed for production,
published or delivered by OTA. Live commercial rollout remains a later gate.

## Cross-task interfaces

- Task 2 exposes `WhiteListEntitlement`, `TransportProfile`, `ApprovedEdge`, `TransportRelease` and a renderer result consumed by Tasks 5–6.
- Task 3 consumes `TransportRelease` and defines immutable release manifest validation used by Task 5.
- Task 4 exposes idempotent usage-event and ledger APIs consumed by Task 5.
- Task 5 exposes only additive `/sub/<token>/info` fields and internal panel DTOs; Task 6 treats unknown fields as absent.

## Rulings

- Use an external sibling worktree because the existing project-local `.worktrees` directory is absent and not ignored; changing the HA baseline `.gitignore` solely for worktree placement would create unrelated drift.
- Shadow billing reports both DOWNLINK_ONLY and UPLINK_PLUS_DOWNLINK until the owner selects a commercial policy. No paid entitlement is publishable without an explicit price or FREE mode.
- The initial data plane is an isolated Xray sidecar; production 3x-ui integration and core forks are deferred behind independent evidence gates.

## Acceptance

Every task follows RED → GREEN → review. Each material checkpoint is committed, pushed only after review, and documented with exact SHA and tests. No task may claim production activation.
