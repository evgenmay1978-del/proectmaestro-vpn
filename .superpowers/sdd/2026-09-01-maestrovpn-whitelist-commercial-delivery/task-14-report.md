# Task 14 report: commercial-delivery release gates

Date: 2026-09-04

## Scope and authority

- Canonical branch: `codex/yandex-cdn-whitelist-task3-sync`
- Frozen base: `b74bff67a4376a0203f500a5d4bc57e4f99ca8af`
- Exact implementation proof SHA: `8d756783ff969530a94e6525473e59d61836533c`
- Evidence class: repository fixtures and exact-SHA GitHub CI only
- Live production readiness: `NO_GO`; no server, Yandex Cloud, DNS/TLS, payment, bot, channel, customer-traffic, OLCRTC, WDTT, OTA, or release-publication mutation was performed

This report is redacted. It contains no private subscription URL, credential, UUID, endpoint address, payment data, or customer data.

## Commits

| Commit | Purpose |
|---|---|
| `ce0cc63ca884483a289f85049b69dd2fa14d4255` | Required Task 14 implementation commit: `test(whitelist): prove commercial delivery release gates` |
| `1aa74a5dec06f2a7724aa420f80693ec3eca8fe7` | Restore exact-SHA CI prerequisites without changing product behavior |
| `b1e7d42687c0bfb5889234366ab8dcfbf2e2f86c` | Bind the committed baseline manifest to committed governance content while preserving the protected dirty worktree copy |
| `d766942e325a7c124e8b259774d8566ae3cccfe7` | Keep each required repro's stdout as one machine-readable JSON envelope; focused Go output remains visible on stderr |
| `15763c2b56cc366a455617a1b19e857248d83578` | Add this redacted Task 14 evidence report |
| `ec35d72f29fcdb956297cc63003c077545fdba31` | Independent-review round 1: consume `go test -json` and fail closed unless every explicitly named offline test emits exactly one pass and no skip event |
| `8d756783ff969530a94e6525473e59d61836533c` | Keep the build-tagged duplicate/unknown-payment replay in the isolated rqlite gate and align the offline proof count, acceptance matrix, and manifest |

## Required proof mapping

| Task 14 proof | Repository evidence | Exact-SHA CI evidence |
|---|---|---|
| Period rollover and half-open boundary closure | `scripts/repro/whitelist-commercial-balance.sh` | `offline-replay` job `100933951281`: `success` |
| Top-up credit is committed once | `scripts/repro/whitelist-commercial-balance.sh` | `offline-replay` job `100933951281`: `success` |
| Duplicate/unknown-payment replay | `whitelist_topup_migration_rqlite_test.go` | `rqlite-purge` job `100933951263`: `success` |
| Meter reset/replay | `scripts/repro/whitelist-commercial-balance.sh` | `offline-replay` job `100933951281`: `success` |
| Production `1.0.157` bare-subscription golden remains exact | `scripts/repro/whitelist-publication-cache.sh` | `offline-replay` job `100933951281`: `success` |
| Post-cache suspension cannot resurrect a closed node | `scripts/repro/whitelist-publication-cache.sh` | `offline-replay` job `100933951281`: `success` |
| Every active Origin is reconciled before readiness | `scripts/repro/whitelist-publication-cache.sh` | `offline-replay` job `100933951281`: `success` |
| Any Origin routes through selected exit metadata | `scripts/repro/whitelist-publication-cache.sh` | `offline-replay` job `100933951281`: `success` |
| Desired generation changes only managed identity and reaches every Origin | `scripts/repro/whitelist-publication-cache.sh` | `offline-replay` job `100933951281`: `success` |
| Unknown receipt is read before any write | `scripts/repro/whitelist-publication-cache.sh` | `offline-replay` job `100933951281`: `success` |
| Xray restart identity, exact managed reconciliation, static-user preservation, receipt expiry, and resume | `scripts/repro/whitelist-sidecar-reconcile.sh` | `commercial-sidecar-agent` job `100933951219`: `success` |
| Missing or skipped explicitly named repro test fails closed before a PASS envelope | `scripts/tests/test_yandex_cdn_commercial_repro.py` | `format-unit` job `100933207248`: `success` |
| Focused offline fixture contract and workflow/wrapper wiring | `scripts/tests/test_yandex_cdn_commercial_repro.py`, `scripts/tests/test_yandex_cdn_ci.py` | `format-unit` job `100933207248`: `success` |
| Backend race/vet and security gates | Existing mandatory release gates | `race-vet` job `100933951268`: `success` |
| Migration integration | Existing mandatory release gate | `rqlite-purge` job `100933951263`: `success` |
| Unchanged test-only Android `1.0.158-task7-test` metadata/signer proof | Existing isolated Android artifact job | `android-test-apk` job `100934785945`: `success` |

The detailed repository/CI versus live-state acceptance boundary is recorded in `docs/yandex-cdn-whitelist/COMMERCIAL_ACCEPTANCE_MATRIX.md`.

## Exact-SHA GitHub evidence

All required workflows below completed on `8d756783ff969530a94e6525473e59d61836533c`, attempt 1:

| Workflow run | Conclusion |
|---|---|
| [HA immutable panel artifact 33844446577](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33844446577) | `success` |
| [HA S4 network change-package checks 33844446668](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33844446668) | `success` |
| [Yandex CDN isolated release checks 33844446580](https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/33844446580) | `success` |

The Yandex release workflow's six jobs all concluded `success`: `format-unit`, `race-vet`, `commercial-sidecar-agent`, `rqlite-purge`, `offline-replay`, and `android-test-apk`.

Artifact `9926356282`, named `maestrovpn-task7-test-8d756783ff969530a94e6525473e59d61836533c`, was uploaded only as the isolated test artifact after its metadata and signer gate passed. It was not installed, published, promoted, or connected to OTA. Production Android remains `1.0.157`.

## Lightweight local evidence

- Commercial repro contract: 5 tests discovered; 3 passed and 2 Bash-runtime cases skipped because the Windows host had no usable Bash runtime. The focused review test was RED against all three original repros, then GREEN after exact pass/skip-event validation was added; its missing/skip simulations also passed on Linux in `format-unit` job `100933207248`.
- Release workflow/wrapper contract: 44 passed and 1 Windows Bash-runtime skip.
- Bot release tests: 16 passed with normal Python import handling and 16 passed under `python -S`, proving the test-only optional HTTP client stub path.
- Documentation and manifest suite: 21 passed before the committed-governance manifest correction; clean-checkout correctness was then proven by exact-SHA GitHub runs `33839585099` and `33839585106`.
- PowerShell wrapper parsing, scoped staged diff checks, and staged secret-pattern scans passed.
- Heavy Go, race/vet, rqlite, sidecar Go 1.26, and Android work ran only in GitHub Actions.

## Protected state and review boundary

- All pre-existing dirty/protected files and untracked `normalize.patch` were left unstaged and uncommitted.
- The private working CDN subscription/canary was not queried, changed, rotated, deleted, or rolled back.
- Independent review round 1 reported one Important false-positive risk: a zero-match or skipped named Go test could still allow a repro PASS envelope. Commits `ec35d72f29fcdb956297cc63003c077545fdba31` and `8d756783ff969530a94e6525473e59d61836533c` resolved it and completed a new exact-SHA CI cycle.
- No independent-review-clean claim is made by this report. Any further blocking finding requires a new commit and exact-SHA CI cycle.
- The evidence above proves repository contracts and release-gate execution; it does not assert live fleet rollout, live billing correctness, or production customer readiness.
