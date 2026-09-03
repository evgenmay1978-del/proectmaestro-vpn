# Task 13 report: whitelist sidecar external-action delivery

## Result

Task 13 is implemented on `codex/yandex-cdn-whitelist-task3-sync` from the required base `17a6bab12fc35953de3db61e0c5d46a79443d913`. The verified implementation SHA is `4439f314743eb9b2016adc0f840a059fd022abd5`, pushed to `origin/codex/yandex-cdn-whitelist-task3-sync` before exact-SHA GitHub verification.

The change remains fail closed and isolated from production publication. It did not touch a live server, Yandex Cloud, DNS/TLS, the private CDN subscription or canary, OLCRTC, WDTT, Android/TV code, OTA/release publication, real payments or balances, bots/channel, or customer traffic.

## TDD evidence

RED commit: `b4baebe8c0984cbc88be1b753fc45c2d3cc82661` (`test(controlplane): define whitelist sidecar delivery contract`).

The RED SHA was pushed before implementation. Its focused tests failed for the intended missing Task 13 symbols and behavior:

- Isolated sidecar agent checks `33811371417`: failure.
- HA control-plane checks `33811371374`: failure.
- Yandex CDN isolated release checks `33811371525`: failure.

The failures identified the absent node-agent receipt read path, client, control-plane delivery/reconciliation methods, and runtime wiring. No secret or private endpoint was included in the evidence.

Implementation commits:

- `ad06c58993af2e59ed93caa2038c6e6a7a99c924` (`feat(controlplane): deliver whitelist sidecar actions`).
- `4439f314743eb9b2016adc0f840a059fd022abd5` (`test(panel): refresh task 13 source pin`), fixing the observed immutable-source digest failure for the changed runtime source.

## Implementation

- Added the node-agent `ExternalActionSender` with TLS 1.3 mutual authentication, strict server-name verification, bounded POST/lookup timeouts, a 1 MiB request limit, exact action-key and desired-SHA binding, exact Task 12 receipt decoding, stale-generation conflict handling, and no credential logging.
- Extended the existing Task 12 agent with one read-only `GET /v1/receipt` lookup for the exact action key. Lookup verifies the durable receipt against the current process boot and freshness, but never reconciles, mutates state, or sends an action.
- Recovery distinguishes an error before request transmission from an ambiguous outcome after transmission. Only an after-send ambiguity performs one exact-key durable receipt lookup. It never blindly repeats the POST.
- Reused the existing `ExternalActionExecutor` directly for whitelist delivery. No second queue, sender stack, retry layer, or parallel outbox was added.
- Persisted the desired generation before delivery, validated the returned exact receipt, and recorded it through the existing control-plane receipt path.
- Reconciliation resolves all four active Origin senders before delivery, builds one common exact release/generation, executes every active Origin, and declares readiness only from current, healthy, matching, unexpired durable receipts. The returned freshness is the minimum receipt expiry.
- Corrected removal-generation serialization so an empty static or managed user set remains canonical `[]` instead of `null`; this preserves the Task 11 digest and validation contract for revoke.
- Added fail-closed runtime configuration for exactly `s1` through `s4`, requiring explicit enablement, HTTPS endpoints without embedded credentials, and existing mTLS certificate/key/CA files. Client construction fails closed. The later publication source remains deliberately disabled by the existing gate.

## Focused local verification

Only lightweight focused tests were run locally:

- `go test ./internal/sidecaragentclient -run '^TestClient' -count=1`: passed.
- `go test ./internal/controlplane -run '^Test(ExecuteWhiteListSidecarAction|ReconcileWhiteListSidecarGeneration)' -count=1`: passed.
- `go test ./cmd/maestro-panel -run '^TestRuntimeWhiteListSidecar' -count=1`: passed.
- `go test ./cmd/maestro-panel -run '^TestHAServiceTemplateRuntimeContract$' -count=1`: passed after the source pin was refreshed.

Race, vet, full-module, release, and Android work were not run on the weak local workstation; the applicable heavy checks were delegated to GitHub.

## Exact-SHA GitHub verification

All listed runs target implementation SHA `4439f314743eb9b2016adc0f840a059fd022abd5`:

- Isolated sidecar agent checks `33813813797`: success.
- HA immutable panel artifact `33813702695`: success.
- HA S4 network change-package checks `33813702711`: success.
- HA DR restore drill `33813702707`: success.
- HA control-plane checks `33813702744`: success.
- Yandex CDN isolated release checks `33813850101`: success.

## Contract coverage

- Successful mTLS request, exact action key and SHA headers, canonical succeeded receipt, and HTTP 409 stale generation.
- Timeout/error before send without receipt lookup or retry.
- Ambiguous timeout/error after send resolved by one read-only lookup for the same exact action key, with no blind resend.
- Exact receipt lookup not-found, success, and validation paths.
- Existing external-action execution, ordering, and unrelated provisioning remain unchanged.
- One common generation/release across all active Origins, all-or-nothing sender resolution, per-Origin durable receipt validation, current Xray process boot, exit health, exact generation/config/release, and unexpired readiness.
- Revoke/removal generation with canonical empty identity arrays.
- Explicit four-node fail-closed runtime configuration and zero publication until the later authorized gate supplies a ready generation.

## Repository state and boundaries

Before this report was added, local `HEAD` and the canonical remote both resolved to `4439f314743eb9b2016adc0f840a059fd022abd5`. The Task 13 implementation changed only the scoped client, sidecar lookup, control-plane delivery/desired/receipt files, runtime wiring/tests, and the observed runtime source-pin test.

Every pre-existing owner/protected dirty file remained dirty and unstaged exactly as found, including `AGENTS.md`, the protected HA and Yandex reports, protected control-plane/API/panel tests and sources, migrations `0001` through `0010`, and the untracked `normalize.patch`. None was staged or modified by Task 13.

This report does not claim a clean independent review. No live production or customer-facing action was performed.
