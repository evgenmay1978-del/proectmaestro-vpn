# Task 12 report: isolated mTLS Xray node agent

## Result

Task 12 is complete on `codex/yandex-cdn-whitelist-task3-sync`. The verified implementation SHA is `94f61f01f66da5f7e13737b041fc620fcdd90dcc`, and the same SHA was pushed to `origin/codex/yandex-cdn-whitelist-task3-sync` before exact-SHA GitHub verification.

The implementation remains isolated from production 3x-ui/Xray. No production host, live firewall, DNS/TLS, active private CDN subscription or canary, OLCRTC, WDTT, Android/TV baseline, OTA/release publication, bot/channel, payment, billing charge, or customer traffic was touched.

## Implementation

- Added a separate `sidecar-agent` Go module pinned to `github.com/xtls/xray-core v0.0.0-20260509173629-1bdb488c9ec0` and Go 1.26. The agent imports the official pinned Xray protobuf/types instead of maintaining a parallel HandlerService model.
- Consumes the exact Task 11 canonical desired payload and action-key contract. The parser enforces canonical JSON, deterministic field ordering, a 1 MiB body limit, SHA-256 binding, managed-user-set digest binding, and action key `<node-id>:<generation>:<desired-sha256>`.
- Reconciles only identities in the reserved `wl:` namespace. It adds users before removing obsolete managed users, preserves static/canary and every non-managed user, rejects stale or conflicting generations, and verifies the exact final managed identity set before emitting readiness.
- Uses official HandlerService `GetInboundUsers` and `AlterInbound` RPCs over fixed loopback `127.0.0.1:18082` with TLS 1.3 mutual authentication and the exact sidecar client identity. The implementation uses official `AddUserOperation`, `RemoveUserOperation`, `protocol.User`, and VLESS account types.
- Loads each managed route credential from an absolute private file named by `sha256(email)`, rejects links, non-regular files, permissive modes on Linux, oversized content, and non-canonical UUIDs, and never places credential material in desired state, receipts, logs, test fixtures, or this report.
- Persists desired state and receipts in an absolute private state directory using mode `0700`, files `0600`, temp-file fsync, close, atomic rename, and directory fsync. Retention is bounded, and the last durable desired state is recovered on startup.
- Computes the Xray process boot identity from the host boot ID, configured Xray PID, and `/proc/<pid>/stat` process start time. Receipts from another Xray process boot are invalidated on startup and before apply/refresh.
- Emits the exact Task 11 receipt fields only after complete reconciliation. Receipts expire after 30 seconds and refresh every 10 seconds while the same durable desired state, current Xray process identity, and exact managed set remain valid. Partial RPC failures never create a receipt.
- Exposes only `POST /v1/desired` on fixed `0.0.0.0:18443`, protected by TLS 1.3 mTLS and the exact controller identity `maestro-whitelist-controller`. It rejects plaintext, untrusted/wrong-name/expired certificates, non-canonical or oversized payloads, and mismatched digest/action headers.
- Extended the existing immutable release/template path narrowly: API services are exactly StatsService and HandlerService; the API listener remains loopback-only; relay traffic enters on `maestro-cdn-exit-in` at port 18084; one local relay identity exits direct; four public managed suffixes route to the exact `exit-s1` through `exit-s4` outbounds; and the terminal public-inbound rule blocks every unmatched identity.
- Runtime material now requires exactly four sorted relay routes, one matching local exit, valid IP/SNI/UUID material, and the complete exit matrix. Credentials and relay TLS key material remain runtime-only and are included through the existing runtime-material commitment rather than the immutable template.
- Added a dedicated systemd service and non-secret environment example. The unit uses a dedicated no-shell account, strict filesystem protection, private devices and temp space, read-only certificate/API/credential/PID inputs, and only the receipt state directory as writable storage.
- Added the minimal Task 12-specific GitHub workflow with self-policy checks and Linux verification for the new module and release template, including pinned-module verification, focused/full tests, race, vet, formatting, filesystem modes, and the official pinned router integration test.

## TDD evidence

RED commit: `21e5c7d7172f40978c79a65b6bd121c8134a1a73` (`test(sidecar): define mtls reconcile contract`).

The RED SHA was pushed before implementation. Exact-SHA workflow `33757100485` provided the intended missing-feature evidence: the sidecar and release-template jobs failed to compile because the required receipt, durable store, reconciler, handler, canonical desired parser/digests, and release material APIs did not exist. The workflow self-policy job succeeded.

GREEN implementation commit: `94f61f01f66da5f7e13737b041fc620fcdd90dcc` (`feat(sidecar): reconcile whitelist identities over mtls`).

## Verification

Fresh local verification on the GREEN implementation:

- `sidecar-agent`: `go test -count=1 ./...` passed.
- `sidecar-agent`: `go test -race -count=1 ./...` was delegated to the exact-SHA Linux workflow and passed there, as required for this weak workstation.
- `sidecar-agent`: `go vet ./...` passed.
- `sidecar-agent`: `go mod verify` returned `all modules verified`.
- `sidecar-agent`: `gofmt -l .` returned no paths.
- `backend`: `go test -count=1 ./internal/release` passed.
- All touched backend Go files passed formatting inspection.
- `python -m unittest scripts.tests.test_sidecar_agent_ci -v` passed all 3 tests.
- `git diff --check` passed.

The local Go 1.26.8 Windows AMD64 toolchain was downloaded from the official Go distribution, verified against SHA-256 `b92c3b2adae85a11ba71fe7216daf0d84e82af4c8ab6c5625807f28622043a59`, and installed outside the repository. It was not committed.

Exact-SHA GitHub verification for pushed SHA `94f61f01f66da5f7e13737b041fc620fcdd90dcc`:

- Isolated sidecar agent checks `33764684525`: success.
- HA control-plane checks `33764684540`: success.
- HA DR restore drill `33764684477`: success.
- HA immutable panel artifact `33764684637`: success.
- HA S4 network change-package checks `33764684537`: success.
- Yandex CDN isolated release checks `33764684541`: success.

## Acceptance coverage

- Exact Task 11 wire compatibility: covered by canonical parser, digest, action-key, and exact receipt serialization tests against the control-plane contract.
- Monotonic apply semantics: covered by stale-generation, same-generation conflict, replay, durable restart, and no-receipt-on-partial-RPC-failure tests.
- Managed namespace safety: covered by add-before-remove, exact final-set, static/canary preservation, non-managed preservation, invalid managed suffix, and missing credential tests.
- mTLS and request binding: covered by successful controller request plus unknown CA, wrong name, expired certificate, plaintext, oversized body, non-canonical body, digest mismatch, and action-key mismatch tests.
- Durable receipt safety: covered by private mode checks on Linux, atomic persistence, bounded retention, startup recovery, Xray boot invalidation, exact expiry, and 10-second refresh tests.
- Official Xray authority: covered by compilation against the pinned module, direct HandlerService request assertions, and an official `app/router.Router` integration test proving all four managed exit mappings while blocking an unknown exit, malformed identity, and static identity.
- Release isolation and fail-closed routing: covered by immutable-template tests for exact services/listeners/outbounds/rules, runtime-only secrets, complete four-exit matrix, exactly one local exit, invalid material rejection, and terminal public-inbound blocking.
- Host packaging: covered by systemd/environment policy tests for the dedicated user, fixed ports, read-only inputs, isolated writable receipt directory, and absence of secrets.

## Repository state and concerns

All owner/protected dirty files were preserved and never staged. Task 12 did not change `AGENTS.md`, migrations `0001` through `0010`, the protected backend tests and sources named in the brief, old SDD reports, `normalize.patch`, `CONTEXT_HANDOFF.md`, the commercial plan, or `BASELINE_MANIFEST.json`.

Live source-firewall state and live health for the exact four exit servers were intentionally not inspected or changed because Task 12 explicitly forbids touching live servers, Yandex Cloud, production Xray, DNS/TLS, the active CDN canary/subscription, or customer traffic. The code and immutable template enforce the required addresses, identities, route matrix, TLS verification, and fail-closed behavior; live firewall and exit-health confirmation remain deployment-time external gates for the later authorized rollout.

## Review fix round 2 (03.09.2026)

An independent review found that the source-firewall attestation accepted nftables control flow it did not model. An early jump to an auxiliary chain containing an unconditional accept, or an early return, could therefore coexist with the expected allow/drop rules and produce a false-ready result.

- Added focused negative cases for `jump -> auxiliary accept` and early `return`. Before the production change, both subtests failed with `unsafe firewall accepted`, providing the required RED evidence.
- Changed only the nftables parser and its focused test. The parser now fails closed on auxiliary chains, rules attached to a non-managed chain, malformed rule expressions, and the unsafe `jump`, `goto`, `return`, `continue`, and `queue` verdicts.
- Fix commit: `658de155aca69a696a18b452c4f3e99348c158c7` (`fix(sidecar): reject nftables control-flow bypasses`). The exact SHA was pushed to the canonical branch before CI.
- Local GREEN evidence: the focused bypass test and the complete `sidecar-agent/internal/preflight` package passed; both touched Go files were `gofmt` clean; scoped `git diff --check` reported no whitespace errors.
- Exact-SHA GitHub evidence: Isolated sidecar agent checks `33797670720`, HA S4 network change-package checks `33797670614`, HA immutable panel artifact `33797670632`, and Yandex CDN isolated release checks `33797670748` all completed successfully for `658de155aca69a696a18b452c4f3e99348c158c7`.
- No production host, live firewall, Yandex resource, private canary/subscription, OLCRTC, WDTT, Android/TV code, OTA/release publication, payment, bot/channel, or customer traffic was touched. The fix remains pending its scoped independent re-review; this report does not claim review closure.
