# White-list commercial delivery acceptance matrix

Status: target-only

This matrix is an offline repository and exact-SHA CI acceptance contract. It
does not claim live production readiness, successful customer traffic, a real
payment, a Yandex Cloud result, or a server rollout. Those remain `NO_GO` until
the later production task supplies its own evidence.

| Required proof | Concrete offline fixture | Exact-SHA CI gate | Acceptance |
| --- | --- | --- | --- |
| Period rollover and half-open boundary closure | `whitelist-commercial-balance.sh`: `TestAdvanceUsesHalfOpenBoundaryAndExpiresOnlyUnusedIncluded`, `TestApplyUsageDebitsOldPeriodBeforeBoundaryRollover` | `offline-replay` on backend Go 1.25 | Both named tests pass with `GOPROXY=off` and `GOSUMDB=off`. |
| Top-up credit and duplicate-payment replay | `whitelist-commercial-balance.sh`: `TestConfirmWhiteListTopUpPaymentCreditsOnceAndEnablesPublication`, `TestWhiteListTopUpConfirmationCommitsOnceAndReplaysAfterUnknownOutcome` | `offline-replay`; `rqlite-purge` migration integration | The confirmed credit is applied once and an unknown outcome replays without a second credit. |
| Meter reset/replay | `whitelist-commercial-balance.sh`: `TestIntegrationFixtureCompositionShadowMeteringKeysResetReplay` | `offline-replay` | Reset/replay preserves the fixture's accounting keys and deterministic projection. |
| Production 1.0.157 bare subscription remains exact and post-cache suspension cannot resurrect CDN publication | `whitelist-publication-cache.sh`: `TestControlPlaneSubscription10157BareGoldenDoesNotAugment`, `TestTask3PublicationAfterOrdinaryCacheCannotResurrectClosedNode` | `offline-replay` on backend Go 1.25 | Golden status, headers, body bytes and no-CDN property remain exact; a closed publication stays closed after ordinary cache use. |
| Every active Origin must be ready | `whitelist-publication-cache.sh`: `TestReconcileWhiteListSidecarGenerationCoversEveryActiveOriginBeforeReady` | `offline-replay` | Readiness remains closed until every active Origin has the required generation. |
| Any Origin routes the customer to the selected exit country | `whitelist-publication-cache.sh`: `TestBuildWhiteListRouteMatrixUsesOnlyExitCountryMetadata` | `offline-replay` | Route identity depends on selected exit metadata, not the serving Origin. |
| Desired generation changes only managed identity state | `whitelist-publication-cache.sh`: `TestBuildWhiteListSidecarDesiredChangesOnlyManagedIdentityAndBumpsEveryOrigin` | `offline-replay` | Every Origin generation advances while unmanaged/static state is outside the mutation. |
| Unknown receipt recovery is read-before-write | `whitelist-publication-cache.sh`: `TestResolveWhiteListSidecarUnknownReadsExactReceiptWithoutWrite` | `offline-replay` | Exact receipt recovery performs no speculative write. |
| Xray restart reconciliation | `whitelist-sidecar-reconcile.sh`: `TestWriteXrayPIDFileReplacesRestartIdentity`, `TestReconcileConvergesExactManagedSetAddsBeforeRemovalsAndPreservesStaticUsers` | `commercial-sidecar-agent` on sidecar Go 1.26 | Restart identity is replaced and reconciliation converges the exact managed set while preserving static users. |
| Resume after sidecar process restart | `whitelist-sidecar-reconcile.sh`: `TestReceiptExpiresAndRefreshRecoversAfterProcessRestart` | `commercial-sidecar-agent` on sidecar Go 1.26 | An expired receipt refreshes and readiness resumes after restart. |
| Telegram commercial purchase UX remains covered | Existing `deploy/tests/test_vpn_bot_maestro_*.py` tests | `format-unit` and both release wrappers | Both customer and order bot test modules pass; no live bot is contacted. |
| Backend race/vet and schema migration integration | Existing package suites and isolated rqlite harness | `race-vet`, `rqlite-purge` | Backend Go 1.25 race/vet and the listed commercial rqlite transactions pass. |
| Release workflow security policy | Existing `scripts.tests.test_yandex_cdn_ci` | `format-unit` | The release workflow remains read-only, pinned, and free of prohibited mutation channels. |
| Android test artifact stays test-only and production stays 1.0.157 | Existing pinned libbox fetch, metadata, signer, and upload-only checks | `android-test-apk` after all release jobs | Exactly `1.0.158-task7-test` / `1015800` is built and uploaded as a seven-day test artifact; no release, OTA, install, or promotion occurs. |

The three repro scripts emit `harness_status=PASS`,
`evidence_class=OFFLINE_REPRO`, and `release_readiness=NO_GO`. A Task 14 report
may mark a row accepted only after all named jobs are green for the same exact
commit SHA.
