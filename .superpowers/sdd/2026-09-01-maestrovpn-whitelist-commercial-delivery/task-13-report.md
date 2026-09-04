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

## Review fix round 1

The first bounded review round addressed two Important findings without adding another queue, poller, sender, or retry abstraction:

- The production rqlite runtime now invokes whitelist sidecar intent reconciliation from the existing renewal reconciler pass and ticker. It consumes the sender resolver populated by `main`, removes managed access only after the revoke generation is delivered, and derives enabled publication only after all active Origins report the exact generation ready and fresh.
- A transport error that proves the request was not sent now returns the durable external action from `applying` to `pending` under its exact attempt owner and lease. A later pass safely retries the same action key without receipt lookup. Only an ambiguous after-send result performs exact-key durable receipt lookup; it is never blindly resent.

Review RED commit: `9f6221ab44cc5065187d13d6de80ae41e903ad14` (`test(controlplane): cover task 13 delivery review gaps`). The focused tests failed for the intended missing runtime boundary and definite-not-sent transition. Its push runs were:

- HA DR restore drill `33818445888`: failure.
- HA immutable panel artifact `33818445929`: failure.
- HA control-plane checks `33818445947`: failure.
- Yandex CDN isolated release checks `33818445982`: failure.
- HA S4 network change-package checks `33818446038`: success (unaffected contract).

Implementation commits:

- `a92e4f68a72cd18bb935c3335138bb752e806540` (`fix(controlplane): wire whitelist sidecar reconciliation`).
- `1b122f2a5c9db96125cbccbf1fdf11aab38fa74d` (`test(controlplane): extend ordered migration scripts to v16`).

The first implementation SHA exposed only stale ordered-migration scripted fakes that stopped at v15. The sidecar contract run `33820136104` succeeded; the four broader failures were corrected by extending those existing fakes and exact assertions through immutable migration v16. No earlier migration was changed.

Focused local verification after the fix passed:

- Sidecar client definite-before-send contract.
- External-action definite-not-sent durable transition and same-key retry contract.
- Whitelist generation reconciliation and production runtime boundary contract.
- Exact ordered migration prefix cases through v16.

All applicable exact-SHA GitHub runs for `1b122f2a5c9db96125cbccbf1fdf11aab38fa74d` succeeded:

- HA DR restore drill `33820815395`: success.
- HA immutable panel artifact `33820815439`: success.
- HA S4 network change-package checks `33820815391`: success.
- HA control-plane checks `33820815410`: success.
- Yandex CDN isolated release checks `33820815445`: success.
- Isolated sidecar agent checks `33820855313`: success.

All pre-existing protected dirty files and the untracked `normalize.patch` remained untouched and unstaged. This review fix did not touch live production, Yandex Cloud, the private canary/subscription, OLCRTC, WDTT, Android/OTA, payments, bots/channel, or customer traffic. This report still does not claim that independent review is clean.

## Review fix round 2

The second bounded review round fixed the remaining Important publication-state gap without adding another evaluator, queue, sender, poller, or retry path. The runtime no longer treats the latest `enabled` control as sufficient to remain installed on Origins.

The focused real-store RED commit is `6e683c543313b2f6aa237841fe5d81eb5459aa3a` (`test(controlplane): cover zero-balance sidecar removal`). Its single Python-SQLite-backed migration/store test failed on the intended old behavior: the latest desired generation still contained the enabled entitlement after its fresh durable balance projection reached zero. The RED push produced the following exact-SHA evidence:

- HA DR restore drill `33825314559`: failure.
- HA immutable panel artifact `33825314575`: failure.
- HA control-plane checks `33825314573`: failure.
- Yandex CDN isolated release checks `33825314596`: failure.
- HA S4 network change-package checks `33825314578`: success (unaffected contract).

Implementation commit `779001a1b726c0ec608c95961d8f270904d3b7d8` (`fix(controlplane): enforce whitelist publication decision`) reuses `EvaluateWhiteListPublication` directly. The runtime resolves its facts from the latest durable control source, exact primary status/expiry, the existing balance snapshot service (projection version/pending state, available bytes and observation freshness), active-Origin release binding, usable route credentials and approved node count. The durable enable edge supplies only the candidate generation for reconciliation; after delivery, the same evaluator receives the exact generation, current receipt set and minimum receipt expiry. A closed decision is converted to a revoke before the removal generation is delivered.

The focused real-store test then passed locally. All applicable runs for exact implementation SHA `779001a1b726c0ec608c95961d8f270904d3b7d8` succeeded:

- HA DR restore drill `33825826023`: success.
- HA immutable panel artifact `33825826079`: success.
- HA S4 network change-package checks `33825826197`: success.
- HA control-plane checks `33825826034`: success.
- Yandex CDN isolated release checks `33825826048`: success.
- Isolated sidecar agent checks `33825937359`: success.

All pre-existing protected dirty files and the untracked `normalize.patch` remained untouched and unstaged. No live server, Yandex Cloud resource, private canary/subscription, OLCRTC, WDTT, Android/OTA, payment, bot/channel, or customer traffic was accessed or changed. This report still does not claim that independent review is clean.

## Review fix round 3

The third bounded review round removed relay health/existence from the managed-to-empty removal gate while retaining the same durable node delivery and receipt path. The exception applies only when a prior generation contains managed users and the target generation contains none; initial empty generations and every non-empty enable/resume remain fail-closed on the exact healthy Exit.

Focused RED commit `9ed8c40ac69a71b69cdafff8eaa4ca4b751f441d` (`test(controlplane): cover removal with unhealthy exit`) extends the existing migration-backed real-store case by marking the retained Exit unhealthy after the managed generation is durable. Against the old runtime it failed before removal with `controlplane: conflict`.

Implementation commit `080793acd7a25ddf3cae6f3ebe2b9b16bc96f691` (`fix(controlplane): remove whitelist users without relay`) preserves the previous Exit identity for removal even when current inventory has no matching record, permits incomplete relay metadata only for an empty route matrix, and requires the unhealthy-Exit exception to be a real managed-to-empty transition. Empty-set readiness still validates the exact durable desired generation and node receipt set, including boot, config, digest, action key and freshness; only relay health is ignored after all managed users are absent. The existing non-removal unhealthy-Exit test remains GREEN.

The focused real-store case and the existing strict unhealthy-Exit case both passed locally. All applicable exact-SHA GitHub runs for `080793acd7a25ddf3cae6f3ebe2b9b16bc96f691` succeeded:

- HA DR restore drill `33829503605`: success.
- HA immutable panel artifact `33829503652`: success.
- HA S4 network change-package checks `33829503606`: success.
- HA control-plane checks `33829503622`: success.
- Yandex CDN isolated release checks `33829503596`: success.
- Isolated sidecar agent checks `33829567545`: success.

All pre-existing protected dirty files and the untracked `normalize.patch` remained untouched and unstaged. No live server, Yandex Cloud resource, private canary/subscription, OLCRTC, WDTT, Android/OTA, payment, bot/channel, or customer traffic was accessed or changed. This report still does not claim that independent review is clean.
