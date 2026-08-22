# Yandex White-List Integration Readiness Design

Date: 2026-08-22
Status: approved by the owner's standing instruction to complete the production-readiness program
Scope: Task 7 only

## Goal

Produce deterministic integration evidence, fixture-replay tooling, client compatibility records, CI gates, rollback evidence, and a non-production Android test artifact for the additive Yandex white-list feature. Task 7 must prove the harness and repository contracts without presenting fixture replay as live Yandex, Xray, device, billing, or production evidence.

The production compatibility baseline remains MaestroVPN 1.0.157. Ordinary VPN, TV behavior, live servers, balances, subscriptions, firewall, databases, CDN origin, and OTA remain unchanged.

## Safety boundary

Task 7 performs no server deployment, public-network probe, production inventory, origin switch, Xray or 3x-ui restart, database migration, real charging, release publication, or OTA. The replay CLI has no live-network mode. Its shell entry points accept no endpoint, credential, token, URL, or arbitrary command. A green replay job means that the harness and fixture contracts work; its release-readiness verdict remains `NO_GO`.

Only later production stages may collect isolated-real-binary, device, or production observations, and only after their explicit stop gates are approved.

## Evidence taxonomy

Every observation has one of these ordered evidence classes:

- `SCHEMA_ONLY`: static parsing or schema validation.
- `FIXTURE_REPLAY`: deterministic offline replay using synthetic inputs.
- `ISOLATED_REAL_BINARY`: a pinned and hashed real Xray binary in a loopback-only isolated process.
- `DEVICE_OBSERVED`: a named client/core version tested on a real test device.
- `PRODUCTION_OBSERVED`: a separately authorized read-only or canary production observation.

Every observation also has a separate verification state: `NOT_RUN`, `PASSED`, `FAILED`, or `BLOCKED`. Compatibility status is nullable. An untested client has no compatibility status; it is not labelled experimental or unsupported merely because it has not been run.

Signed release-gate reports include their evidence class in the signed payload. Minimum classes are enforced per gate:

- `config_validation`: `SCHEMA_ONLY`.
- `billing_identity` and `subscription_regression`: `FIXTURE_REPLAY`.
- `direct_origin`, `isolated_start`, `literal_edge`, `local_vless`, `per_user_stats`, `xray_config_test`, and `yandex_get_body`: `ISOLATED_REAL_BINARY`.
- `client_import`: `DEVICE_OBSERVED`.
- `production_baseline`: `PRODUCTION_OBSERVED`.

Consequently, fixture-only reports cannot be signed into a publishable candidate for gates that require real binaries, devices, or production. Trusted evidence signatures remain mandatory; evidence class is an additional restriction, not a replacement for trust, freshness, immutable source, or candidate binding.

## Fixture replay model

The Go package `backend/internal/whitelistready` owns strict schemas for:

- a bounded synthetic fixture catalog;
- candidate-bound replay observations;
- the four-client compatibility matrix;
- a deterministic readiness assessment.

The parser rejects oversized or non-UTF-8 input, trailing JSON, unknown fields, duplicate IDs, incomplete required case sets, invalid enum values, unsafe strings, malformed or negative counters, non-UTC timestamps, stale binding fields, and any observation whose candidate commit, artifact hash, config hash, fixture-catalog hash, tool version, core version, or environment differs from the bundle binding.

Fixture cases contain only synthetic identifiers and expected protocol facts. The evidence bundle references the SHA-256 of the separate catalog, avoiding self-referential hashes. The validator emits fixed reason codes and never echoes arbitrary fixture content.

Required replay suites are:

- `yandex_get_body`
- `yandex_active_stream`
- `yandex_idle_cutoff`
- `yandex_literal_edge`
- `xray_counter_reset`
- `billing_idempotency`
- `duplicate_event_replay`
- `subscription_escaping`
- `edge_rotation`

The Yandex body catalog covers 1 byte, 1 KiB, 64 KiB, 256 KiB, typical, and bounded maximum bodies plus digest, authorization-result, sequence, cache-disabled, invalid-host, invalid-path, invalid-status, latency, and retry observations. No load generator or public endpoint is part of the replay harness.

## Cross-package integration proof

Repository tests exercise the real domain packages together rather than duplicating their logic. They prove:

- entitlement OFF returns the ordinary subscription byte-for-byte and adds no CDN node;
- ACTIVE plus a matching published release is additive and preserves the ordinary identity;
- suspension, revocation, release mismatch, and cache regeneration remove only the CDN addition;
- edge rotation changes only the approved CDN edge selection;
- stable server-side entitlement and meter keys survive client re-import and edge changes;
- cumulative counter resets, out-of-order events, and duplicate replay are idempotent;
- ordinary traffic is excluded and shadow billing cannot mutate a real balance;
- the versioned private API fixtures validate for the same account.

## Reproduction commands

The exact required scripts live under `scripts/repro/`:

- `yandex-get-body.sh`
- `yandex-active-stream.sh`
- `yandex-idle-cutoff.sh`
- `yandex-literal-edge.sh`
- `xray-counter-reset.sh`
- `billing-idempotency.sh`
- `duplicate-event-replay.sh`
- `subscription-escaping.sh`
- `edge-rotation.sh`

Each wrapper uses strict shell mode, resolves the repository from its own location, rejects arguments, and invokes the Go CLI for exactly one offline suite. There is no `curl`, SSH, DNS lookup, redirect following, shell evaluation, or environment-variable endpoint override. Semantic output includes both `harness_status: PASS` and `release_readiness: NO_GO`.

## Client compatibility matrix

The matrix contains exactly MaestroVPN, Karing, Incy, and Happ. Each row records app version, core version, preset, import, refresh, TLS, client encryption, XHTTP GET, TCP, UDP, DNS, speed test, five-minute up/down traffic, ninety-second idle recovery, network transitions, sleep/wake, cold start, literal edge, per-user stats, and billing identity.

Initial rows are `NOT_RUN` with null compatibility status and no fabricated evidence. A non-null status (`SUPPORTED`, `SUPPORTED_WITH_SETTING`, `EXPERIMENTAL`, `IMPORT_ONLY_UNSTABLE`, or `UNSUPPORTED`) requires device-observed evidence and a complete result set. Import alone never qualifies as supported.

## CI and Android artifact

The Yandex release workflow runs formatting, Go unit and cross-package integration tests, race tests, vet, strict fixture and matrix validation, all nine shell wrappers, shell syntax checks, and a static no-network/no-production-mutation audit. It has read-only repository permissions and receives no production secrets or endpoints.

Backend contracts must pass before the Android test build. The Android output is a debug/test artifact with a version identity later than and visibly distinct from `1.0.157`; it is never attached to a GitHub release, written to OTA metadata, or presented as production. Its libbox provenance is pinned by workflow run, artifact ID, source revision, ZIP digest, and AAR digest. Building the APK does not count as device evidence, and the dormant Task 6 runtime remains disabled.

## Failure behavior

- Invalid fixture or matrix: fixed-code validation failure and non-zero exit.
- Missing required suite/client/check: fail closed.
- Fixture replay marked as a higher evidence class: reject.
- Replay requested with arguments or an endpoint-like value: reject before execution.
- Missing pinned Android dependency: fail the artifact build; never fall back to latest.
- Any attempted release assessment from fixture-only evidence: return `NO_GO`.
- Any secret-like content in fixtures, logs, or documentation: fail the secrecy audit.

## Rollback

Task 7 is repository-only and additive. Rollback is removal of the readiness package, CLI, fixtures, scripts, CI job, and test-only artifact configuration, plus restoration of the previous release-evidence schema if no candidate has consumed it. It never touches production state. The existing data-plane rollback target remains untested until a later isolated real-binary rehearsal times it; Task 7 must not claim the five-minute objective as proven.

## Acceptance

Task 7 is complete when:

- signed release reports enforce evidence-class minimums;
- strict positive and adversarial tests pass;
- all nine wrappers are executable, cwd-independent, syntax-valid, and semantically green while reporting `NO_GO`;
- the client matrix contains all four clients without unsupported claims;
- cross-package invariants pass under normal and race tests;
- CI contains no production mutation or secret path;
- a separately identified test APK is built only after backend gates pass and its SHA-256/provenance are recorded;
- handoff, test-results, and rollback documents distinguish verified fixture behavior from pending real-binary/device/live gates;
- two independent reviews report no open Critical or Important findings.
