# MaestroVPN White-List Commercial Delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task with review checkpoints.

**Goal:** Довести существующий изолированный Yandex CDN/XHTTP sidecar до безопасного коммерческого режима: одна подписка, ручная оплата, CDN/LTE скрытый по умолчанию, бессрочные пакеты GB, точный server-side metering, индивидуальный use-gate и последовательный rollout S4 → S2 → S3 → S1 без изменения обычного VPN.

**Owner policy override (2026-09-02):**

- CDN/LTE publication is customer-hidden and OFF by default.
- Confirmed GB purchase or explicit admin enable is required to publish CDN/LTE.
- Admin disable preserves purchased balance and ordinary VPN.
- Ordinary 400 RUB renewal grants zero CDN bytes.
- Подтверждённая покупка GB атомарно включает customer publication gate; явное admin enable/disable является отдельным идемпотентным переходом. Disable скрывает CDN/LTE и отзывает только управляемые `wl:` identities, но не стирает balance, journal или history. CDN/LTE trial/bonus отложен и не входит в текущую реализацию.

**Implementation checkpoint (2026-09-03):** Tasks 1–9 are repository-complete
at exact SHA `eba97d7654dfd2f5dc3c65faa2a70348a1874bfb`; all five applicable
exact-SHA GitHub workflows are GREEN and independent final re-review found no
Critical/Important/Minor findings. Task 10 is next. This checkpoint did not deploy servers, charge a real
customer, install an APK, publish OTA or switch customer traffic.

**Architecture:** Bare `/sub/<token>` остаётся byte-compatible с MaestroVPN 1.0.157. Только links-ответ расширяется после ordinary cache/LKG свежим typed publication snapshot. Один Yandex CDN resource использует набор одинаковых active Origin-реплик. Route identity `wl:<entitlementID>:<exitID>` выбирает фиксированный exit S1/S2/S3/S4 через Xray `routing.rules[].user`, поэтому страна не зависит от выбранного CDN Origin. Control plane хранит immutable periods/journal, mutable projection, отдельные top-up orders и desired/receipt state. Xray sidecar сохраняет отдельный процесс и mTLS localhost API; node agent применяет только managed identities через `HandlerService`. Existing exactly-once external-action executor доставляет desired generations на node agents. Любая неопределённость закрывает только CDN-часть.

**Tech Stack:** Go 1.25 backend, Go 1.26 isolated sidecar-agent module, Xray-core v26.5.9 source commit `1bdb488c9ec09ea51e6899697d5b7437f3cf6eb2`, rqlite/SQLite migrations, Python 3 Telegram adapter, official `github.com/INCY-DEV/incy-link-encoder/go` v1.3.0, GitHub Actions exact-SHA CI.

## Global Constraints

- Canonical worktree: `C:\Users\User\Documents\Codex\2026-08-05\new-chat\mvpn-yandex-cdn-whitelist-task3-sync`.
- Canonical branch: `codex/yandex-cdn-whitelist-task3-sync`.
- Before every semantic command or tool action, run `python ops/maestro-repetition-guard.py check`; close it with `success`, or with `fail` followed by a genuinely different `correct` family.
- Preserve every protected dirty path recorded in `CONTEXT_HANDOFF.md`; stage only explicit paths. Never use broad restore, reset, clean, add, commit, or checkout commands.
- Do not modify OLCRTC, WDTT, production signing, production OTA, customer charging, or the ordinary 1.0.157 release path.
- No production mutation until implementation, independent review, exact-SHA CI, preflight, backup, console-access proof, validation, and rollback are GREEN.
- Real charging, production DB cutover, OTA publication, and final customer-traffic switch remain explicit stop gates.
- Every schema mutation is additive by version, reversible by database restore, and tested from schema v10 through the new latest version.
- Accounting guarantee is no negative balance and no intentional service after zero; periodic StatsService cannot promise a byte-exact network hard-cap. `uncovered_bytes`, safety reserve, polling latency, and revoke latency are measured and gated.
- No SubToken, VLESS URI, private key, payment credential, client UUID, or raw sidecar credential in logs, callback data, receipts, Git, tests, or CI output.
- Every task follows RED → minimal GREEN → focused regression → commit. A task is not complete on code inspection alone.

---

## Task 1: Lock the commercial contract inventory and CI scope

**Files:**

- Modify: `.github/workflows/yandex-cdn-release.yml`
- Create: `scripts/tests/test_yandex_cdn_commercial_ci.py`
- Modify: `ops/validate-yandex-cdn-release.sh`
- Modify: `ops/validate-yandex-cdn-release.ps1`

**Contract to freeze:**

```go
type WhiteListPublicationVerdict string

const (
	WhiteListPublishable        WhiteListPublicationVerdict = "PUBLISHABLE"
	WhiteListNoEntitlement      WhiteListPublicationVerdict = "NO_ENTITLEMENT"
	WhiteListPrimaryExpired     WhiteListPublicationVerdict = "PRIMARY_EXPIRED"
	WhiteListNoBalance          WhiteListPublicationVerdict = "NO_BALANCE"
	WhiteListProjectionStale    WhiteListPublicationVerdict = "PROJECTION_STALE"
	WhiteListProjectionPending  WhiteListPublicationVerdict = "PROJECTION_PENDING"
	WhiteListReleaseMismatch    WhiteListPublicationVerdict = "RELEASE_MISMATCH"
	WhiteListSidecarUnavailable WhiteListPublicationVerdict = "SIDECAR_UNAVAILABLE"
)
```

**Steps:**

- [ ] Add a plan-policy test that freezes the publication verdict values, decimal-byte unit, immutable product rows, sidecar desired-generation fields, receipt fields, and the later task/test paths where their production types become compile-time contracts. Do not create placeholder production types ahead of those tasks.
- [ ] Add a CI policy test proving the workflow watches all new paths: `backend/internal/api/**`, `backend/internal/controlplane/**`, `backend/internal/subgen/**`, `backend/internal/whitelistbalance/**`, `sidecar-agent/**`, `deploy/vpn_bot_maestro_*.py`, and this plan.
- [ ] Run `python -X utf8 -m unittest scripts.tests.test_yandex_cdn_commercial_ci`; confirm RED for missing workflow coverage.
- [ ] Extend both release wrappers and the workflow with the new Go/Python packages while keeping the current Android test APK gate unchanged. Compile-time tests for each commercial type are added with its production implementation in Tasks 3, 5, 7, and 11, so every committed task remains GREEN.
- [ ] Run the same focused Python command and `git diff --check`; require GREEN.
- [ ] Commit only Task 1 paths with message `test(whitelist): lock commercial delivery contracts`.

## Task 2: Make links augmentation atomic and multi-node

**Files:**

- Modify: `backend/internal/subgen/whitelist_links.go`
- Create: `backend/internal/subgen/whitelist_links_batch_test.go`

**API:**

```go
func AppendWhiteListShareLinks(ordinaryBase64 string, nodes []WhiteListNode) (string, error)
```

**Steps:**

- [ ] Write tests proving an empty node slice returns the exact original string, two approved nodes append in stable source order, labels are unique, a duplicate public label is rejected, a duplicate Xray identity is rejected, and one invalid node rejects the full batch without a partial result.
- [ ] Add a regression test proving decoded ordinary bytes are an exact prefix and line endings are not normalized.
- [ ] Add a golden 1.0.157 fixture covering HTTP status, content type, content encoding, ETag, content length, body bytes, and the absence of per-user CDN data in ordinary cache/LKG.
- [ ] Run `go test ./internal/subgen -run 'TestAppendWhiteListShareLinks' -count=1`; require RED.
- [ ] Validate every node and render every link before joining anything to the ordinary document. Preserve the single-node function as a compatibility wrapper over the batch API.
- [ ] Keep internal edge IDs out of labels. Require the publication source to supply stable labels such as `Maestro CDN — Нидерланды` and `Maestro CDN — Россия`.
- [ ] Run `go test ./internal/subgen -count=1` and `go test -race ./internal/subgen -count=1`; require GREEN.
- [ ] Commit with message `feat(subgen): append whitelist nodes atomically`.

## Task 3: Add post-cache publication without touching bare subscriptions

**Files:**

- Create: `backend/internal/api/controlplane_whitelist_publication.go`
- Create: `backend/internal/api/controlplane_whitelist_publication_test.go`
- Modify: `backend/internal/api/controlplane_subscription_compat.go`
- Modify: `backend/internal/api/controlplane_subscription_cache.go`
- Modify: `backend/internal/api/controlplane_port.go`
- Modify: `backend/internal/api/controlplane_business.go`
- Modify: `backend/cmd/maestro-panel/runtime_subscription.go`
- Create: `backend/cmd/maestro-panel/runtime_whitelist_publication_test.go`

**Port:**

```go
type WhiteListPublicationSnapshot struct {
	Verdict           WhiteListPublicationVerdict
	Nodes             []subgen.WhiteListNode
	ProjectionVersion int64
	DesiredGeneration int64
	FreshThrough      time.Time
}

type WhiteListPublicationSource interface {
	WhiteListPublication(context.Context, string, time.Time) (WhiteListPublicationSnapshot, error)
}
```

**Steps:**

- [ ] Add table-driven tests for `PUBLISHABLE`, every closed verdict, source error, malformed snapshot, and source timeout.
- [ ] Prove bare `/sub/<token>`, `/info`, `/helpers`, device admission, and the ordinary cached bytes and headers remain byte-for-byte unchanged.
- [ ] Prove links augmentation executes only after ordinary cache/LKG resolution and that an earlier CDN link can never re-enter through cache after suspension.
- [ ] Run `go test ./internal/api ./cmd/maestro-panel -run 'WhiteListPublication|Subscription' -count=1`; require RED.
- [ ] Wire the source as an optional dependency defaulting OFF. For non-links output or any non-publishable/error result, return the original ordinary document and HTTP 200.
- [ ] Apply `AppendWhiteListShareLinks` only to the freshly resolved links document. Recompute links-only ETag/content length after augmentation. Never write augmented bytes or route credentials to ordinary cache, LKG, shared cache, or logs.
- [ ] Run `go test ./internal/api ./cmd/maestro-panel -count=1` and race tests; require GREEN.
- [ ] Commit with message `feat(api): publish whitelist nodes after ordinary cache`.

## Task 4: Add durable periods and byte journal schema

**Files:**

- Create: `backend/internal/controlplane/migrations/0011_whitelist_commercial_balance.sql`
- Modify: `backend/internal/controlplane/migrations.go`
- Create: `backend/internal/controlplane/whitelist_balance_migration_test.go`
- Modify: `backend/internal/controlplane/migrations_ordered_test.go`

**Schema:**

- `whitelist_billing_periods`: immutable `period_id`, `entitlement_id`, sequence, start/end, included grant, source access order, and unique source-order binding.
- `whitelist_balance_entries`: immutable signed included/purchased/consumed/uncovered deltas, kind, period, source order or interval, idempotency key, timestamp, and metadata digest.
- `whitelist_balance_projections`: one mutable row per entitlement with current period, included remaining, purchased remaining, lifetime consumed, uncovered bytes, version, pending flag, fresh-through epoch, and update time.
- `whitelist_usage_applications`: immutable unique binding from metering interval to one journal application.
- Immutable triggers reject update/delete on periods, entries, and usage applications. Projection writes require a monotonic version compare-and-swap.
- Database uniqueness keys are exact: included grant uses `(access_order_id, period_ordinal)`, purchased credit uses the durable payment-confirmation/order identity, and usage uses `(meter_epoch, interval_id)`. `meter_epoch` is globally unique across the fleet and binds Origin ID, counter-source identity, Xray process boot identity, and reset sequence.

**Steps:**

- [ ] Write migration tests for v10 → v11, clean bootstrap, repeated migration, foreign keys, immutable triggers, unique period sequence, unique access-order grant, unique idempotency key, and one projection/current-period cursor per entitlement.
- [ ] Store bytes as checked non-negative SQLite integers; cap every single field below `math.MaxInt64`; represent deltas in separate signed columns.
- [ ] Run `go test ./internal/controlplane -run 'Migration.*WhiteListCommercialBalance' -count=1`; require RED.
- [ ] Add migration 11 and bump `SchemaVersion` and ordered loader exactly once.
- [ ] Run all controlplane migration tests and the isolated rqlite migration harness; require GREEN.
- [ ] Commit with message `feat(controlplane): add whitelist balance ledger schema`.

## Task 5: Implement period rollover and prepaid balance service

**Files:**

- Create: `backend/internal/whitelistbalance/model.go`
- Create: `backend/internal/whitelistbalance/model_test.go`
- Create: `backend/internal/controlplane/whitelist_balance.go`
- Create: `backend/internal/controlplane/whitelist_balance_test.go`
- Create: `backend/internal/controlplane/whitelist_balance_rqlite_test.go`

**Domain API:**

```go
const GBDecimal int64 = 1_000_000_000

type BalanceProjection struct {
	EntitlementID          string
	CurrentPeriodID        string
	IncludedRemainingBytes int64
	PurchasedRemainingBytes int64
	LifetimeConsumedBytes  int64
	UncoveredBytes         int64
	Version                int64
	Pending                bool
	FreshThrough           time.Time
}

type UsageAllocation struct {
	IncludedBytes int64
	PurchasedBytes int64
	UncoveredBytes int64
}
```

**Rules:** included grant является только явным параметром операции; commercial default равен нулю. Ordinary access confirmation не создаёт `INCLUDED_GRANT`, не кредитует CDN bytes и не меняет customer publication gate. Для уже существующего CDN entitlement допускается нулевой accounting period, связанный с подтверждённым access order, чтобы purchased bytes продолжали работать после продления без выдачи бонуса. Included bytes расходуются первыми; purchased bytes не сгорают; завершившийся period списывает только неиспользованный explicit included grant; новый period активируется только с его start; истёкший primary access замораживает purchased balance. Actual counter overshoot записывается как uncovered traffic, но available balance никогда не становится отрицательным. Task 5 хранит balance независимо от visibility: admin disable не изменяет periods, journal или projection.

**Steps:**

- [ ] Write pure tests for a zero-grant first period, absence of implicit 2 GB, an optional explicit included grant, early renewal without current-quota reset, multi-period queue, exact boundary rollover, unused explicit included expiry, purchased persistence across rollover and primary expiry, included-first allocation, zero balance, counter overshoot, overflow rejection, and duplicate operation idempotency.
- [ ] Run `go test ./internal/whitelistbalance -count=1`; require RED.
- [ ] Implement pure state transitions with no database or wall-clock access.
- [ ] Add rqlite service methods `ScheduleWhiteListPeriod`, `CreditWhiteListPurchasedBytes`, `ApplyWhiteListUsage`, and `WhiteListBalanceSnapshot`. Each method takes explicit `now` and exact source IDs. Journal insert, stored operation result, and projection CAS commit in one transaction; replay reads the stored result.
- [ ] Resolve unknown transaction outcomes by exact durable read; never blind-retry a credit or debit.
- [ ] Run unit, rqlite integration, and race tests; require GREEN.
- [ ] Commit with message `feat(controlplane): implement whitelist prepaid balance`.

## Task 6: Debit Xray intervals and derive the publication verdict

**Files:**

- Create: `backend/internal/controlplane/whitelist_usage.go`
- Create: `backend/internal/controlplane/whitelist_usage_test.go`
- Create: `backend/internal/controlplane/whitelist_publication.go`
- Create: `backend/internal/controlplane/whitelist_publication_test.go`
- Modify: `backend/internal/shadowbilling/store.go`
- Create: `backend/internal/shadowbilling/commercial_debit_test.go`

**Steps:**

- [ ] Write tests proving one `UPLINK_PLUS_DOWNLINK` interval creates one debit, `(meter_epoch, interval_id)` replay is a no-op with the stored result, counter reset starts a new globally unique epoch, two Origins cannot collide on an epoch, out-of-order intervals are rejected, and zero balance first closes publication then produces a sidecar revoke intent without changing ordinary access.
- [ ] Write publication tests proving default OFF/hidden, confirmed-purchase activation, explicit admin enable, admin disable to ordinary-only, and preservation of purchased balance across disable. A disabled or absent customer gate maps to the existing closed `NO_ENTITLEMENT` verdict and never changes ordinary output.
- [ ] Write boundary tests proving the collector samples and closes an interval at period end. If an interval still crosses the boundary after an outage, mark projection pending/stale and publish no CDN rather than apportion bytes approximately.
- [ ] Write publication tests for active primary access, positive balance, freshness window, pending projection, exact profile/preset/release binding, applied node receipt, and credential validity.
- [ ] Run `go test ./internal/shadowbilling ./internal/controlplane -run 'CommercialDebit|WhiteListPublication' -count=1`; require RED.
- [ ] Add a narrow `CommercialDebiter` callback after the durable shadow interval is accepted. Bind debit idempotency to meter epoch, immutable interval ID, and basis. The conditional allocation consumes active included first, then purchased, saturates both at zero, and writes the remainder to uncovered bytes.
- [ ] Use an initial maximum collector interval of 2 seconds and revoke target of 5 seconds. Compute reserve as `max(10_000_000 bytes, measured_p999_bytes_per_second × 5 seconds)`; expose it separately from billable balance and keep launch NO_GO if canary cannot satisfy the bound.
- [ ] Return `PUBLISHABLE` only when every gate is true. Return no nodes on any other verdict.
- [ ] Enqueue only an entitlement-scoped desired-state change when publishability crosses usable/unusable. Do not mutate customer expiry or ordinary credentials.
- [ ] Run package tests, race tests, and `scripts/repro/xray-counter-reset.sh`; require GREEN.
- [ ] Commit with message `feat(metering): enforce whitelist balance use gate`.

## Task 7: Add immutable GB products and exactly-once top-up orders

**Files:**

- Create: `backend/internal/controlplane/migrations/0012_whitelist_topup_orders.sql`
- Modify: `backend/internal/controlplane/migrations.go`
- Create: `backend/internal/controlplane/whitelist_products.go`
- Create: `backend/internal/controlplane/whitelist_products_test.go`
- Create: `backend/internal/controlplane/whitelist_topup_orders.go`
- Create: `backend/internal/controlplane/whitelist_topup_orders_test.go`
- Create: `backend/internal/controlplane/whitelist_topup_orders_rqlite_test.go`
- Modify: `backend/internal/controlplane/orders.go`
- Create: `backend/internal/controlplane/orders_whitelist_period_test.go`

**Catalog:**

| Product ID | Kind | Price minor RUB | Bytes |
|---|---:|---:|---:|
| `wl-gb-5-v1` | `WHITELIST_BYTES` | 10000 | 5000000000 |
| `wl-gb-20-v1` | `WHITELIST_BYTES` | 30000 | 20000000000 |
| `wl-gb-50-v1` | `WHITELIST_BYTES` | 60000 | 50000000000 |
| `wl-gb-100-v1` | `WHITELIST_BYTES` | 100000 | 100000000000 |

Existing `40000 RUB / 30 days` access flow remains unchanged: confirmation extends only ordinary access and grants zero CDN bytes. A confirmed GB top-up atomically finds or creates a zero-grant accounting period (`included_grant_bytes=0`) bound to the customer's confirmed ordinary access order, creates no `INCLUDED_GRANT` journal row for zero bytes, exactly once credits purchased bytes, and enables the CDN/LTE customer publication gate in the same cluster transaction. Explicit admin enable/disable is a separate idempotent transition; disable preserves balance. CDN/LTE trial is deferred.

**Steps:**

- [ ] Add migration 12 tables for immutable products, top-up orders, payment claims, stored confirmation results, idempotency bindings, and one versioned default-OFF customer publication control per entitlement. Keep existing order tables unchanged.
- [ ] Seed exact catalog rows idempotently; historical rows remain immutable and visible to existing orders.
- [ ] Write RED tests for catalog snapshots, price-change isolation, expired-primary rejection, duplicate claim, duplicate confirmation, different panel node, restart, unknown outcome, rejected order, and exact one-time byte credit.
- [ ] Implement `CreateWhiteListTopUpOrder`, `ClaimWhiteListTopUpPayment`, `ConfirmWhiteListTopUpPayment`, and `RejectWhiteListTopUpOrder` using one rqlite CAS transaction per transition.
- [ ] In top-up confirmation, first find or create the active zero-grant period bound to the customer's confirmed ordinary access order, then insert `PURCHASED_CREDIT` and enable the publication control atomically. Zero grant must not create an `INCLUDED_GRANT` entry.
- [ ] Keep ordinary access confirmation byte-compatible and independent from CDN. Only when a customer already has a CDN entitlement may renewal extend a zero-grant accounting period; it must not enable publication or create an `INCLUDED_GRANT`.
- [ ] Add idempotent admin enable/disable commands. Enable requires a usable balance or an explicit separately recorded admin grant; disable closes publication first and preserves balance/journal/history.
- [ ] Run all order/controlplane integration and race tests; require GREEN.
- [ ] Commit with message `feat(orders): add exactly-once whitelist products`.

## Task 8: Expose one commercial API without breaking old routes

**Files:**

- Modify: `backend/internal/api/controlplane_port.go`
- Modify: `backend/internal/api/controlplane_business.go`
- Create: `backend/internal/api/controlplane_commercial.go`
- Create: `backend/internal/api/controlplane_commercial_test.go`
- Create: `backend/internal/api/controlplane_commercial_admin_test.go`

**Routes:**

- `GET /order/catalog`: access tariff plus four GB products.
- `POST /order`: existing access payload remains accepted; a new `product_id` selects a GB pack.
- `POST /order/<id>/paid-claim`: durable claim for either order family.
- `POST /admin/order/<id>/confirm` and `/reject`: dispatch by durable order lookup, not by callback text or ID prefix.
- `GET /account/whitelist-balance`: included, purchased, available, period end, primary access state, and redacted publication verdict.
- `POST /admin/accounts/{id}/whitelist-publication`: explicit enable/disable with idempotency key, actor/audit evidence, no balance mutation, and ordinary-access isolation.
- `POST /account/subscription-delivery`: authenticated Incy/Happ delivery descriptor; never returns secrets to a different account.

**Steps:**

- [x] Write route tests for authentication, account binding, exact amounts, state transitions, duplicate callbacks, expired-primary GB rejection, and legacy route compatibility.
- [x] Run focused API tests; require RED.
- [x] Add typed port methods and response DTOs. Preserve `/order/tariffs`, current access order JSON, and current admin callbacks.
- [x] Redact tokens and credentials from errors and structured logs.
- [x] Run all API, controlplane, and race tests; require GREEN.
- [x] Commit with message `feat(api): expose whitelist catalog balance and orders`.

## Task 9: Add official Incy one-tap and Happ fallback delivery

**Files:**

- Modify: `backend/go.mod`
- Modify: `backend/go.sum`
- Create: `backend/internal/subgen/incy.go`
- Create: `backend/internal/subgen/incy_test.go`
- Create: `backend/internal/subgen/delivery.go`
- Create: `backend/internal/subgen/delivery_test.go`

**Dependency:** pin `github.com/INCY-DEV/incy-link-encoder/go v1.3.0`; its official Go port is standard-library-only and exposes `incylink.EncryptLink(url, name)`.

**Steps:**

- [x] Import the upstream pinned vector and write deterministic compatibility tests without storing a real subscription token.
- [x] Test that only HTTPS Maestro subscription URLs are accepted, display name is fixed to `MaestroVPN`, and no unsupported Happ wrapper is produced.
- [x] Run `go test ./internal/subgen -run 'Incy|Delivery' -count=1`; require RED.
- [x] Implement Incy encoding through the official library. Return Happ as `COPY_HTTPS_URL_AND_QR` until device proof is recorded.
- [x] Add redaction tests proving neither delivery URL nor token appears in logs/errors.
- [x] Run `go mod verify`, subgen tests, race tests, and offline replay; require GREEN.
- [x] Commit with message `feat(delivery): add official Incy one-tap links`.

## Task 10: Replace Telegram clutter with the five-action customer flow

**Files:**

- Modify: `deploy/vpn_bot_maestro_orders.py`
- Create: `deploy/vpn_bot_maestro_customer.py`
- Create: `deploy/tests/__init__.py`
- Create: `deploy/tests/test_vpn_bot_maestro_customer.py`
- Create: `deploy/tests/test_vpn_bot_maestro_orders.py`

**Menu:** `Моя подписка и баланс`, `Продлить 30 дней — 400 ₽`, `Купить гигабайты`, `Подключить устройство`, `Помощь`.

**Steps:**

- [x] Write mocked-transport tests for login display, access renewal, four pack choices, CDN absent before purchase/admin enable, CDN present after purchase, ordinary-only output after admin disable with purchased balance preserved, expired-primary blocking, paid claim, owner confirmation/rejection, duplicate callback, successful balance message, Incy button, Happ three-step fallback, and support handoff. Do not add a trial button.
- [x] Prove callback data contains only opaque action/order identifiers and never login, SubToken, URL, UUID, or payment data.
- [x] Run `python -X utf8 -m unittest deploy.tests.test_vpn_bot_maestro_customer deploy.tests.test_vpn_bot_maestro_orders`; require RED.
- [x] Implement a small pure presentation layer and one API adapter. Keep the existing admin confirmation callbacks compatible.
- [x] Remove unconditional `verify=False`; default to verified TLS and allow loopback HTTP only through an explicit local endpoint configuration.
- [x] Tell clients to put only their Maestro login in the transfer comment; never mention VPN in the payment comment.
- [ ] Deduplicate 50/80/90/100%, suspension, resume, stale, and failed-provisioning notifications by durable event key.
  Deferred until the metering/reconcile producers and live outbox delivery loop exist; Tasks 11–13 must close this before production rollout.
- [ ] Integrate and live-validate the flow across both existing Telegram bots and the existing customer channel; discover their actual live configuration first, preserve one poller per bot token, and prevent duplicate channel notifications.
- [x] Run unit tests and syntax compile; require GREEN.
- [x] Commit the customer flow and owner top-up callback contract.

## Task 11: Add sidecar desired state and durable receipts

**Files:**

- Create: `backend/internal/controlplane/migrations/0015_whitelist_sidecar_reconcile.sql`
- Modify: `backend/internal/controlplane/migrations.go`
- Create: `backend/internal/controlplane/whitelist_sidecar_desired.go`
- Create: `backend/internal/controlplane/whitelist_sidecar_desired_test.go`
- Create: `backend/internal/controlplane/whitelist_route_credentials.go`
- Create: `backend/internal/controlplane/whitelist_route_credentials_test.go`
- Create: `backend/internal/controlplane/whitelist_sidecar_receipt.go`
- Create: `backend/internal/controlplane/whitelist_sidecar_receipt_test.go`
- Create: `backend/internal/controlplane/whitelist_sidecar_rqlite_test.go`

**State:** immutable route credentials keyed by `(entitlement_id, exit_id)` with managed email `wl:<entitlementID>:<exitID>`; one monotonic desired generation per Origin; canonical managed-user-set digest; exact origin release/profile/preset and exit-route binding; immutable action key; and a readiness receipt containing `origin_id`, `xray_process_boot_id`, `config_digest`, `desired_generation`, `managed_user_set_digest`, `applied_at`, and `expires_at`. Raw credentials are encrypted/protected payload material and excluded from digests shown in logs.

**Steps:**

- [x] Write migration tests for v14 → v15, monotonic generation, receipt replay, stale receipt rejection, release mismatch, and immutable action binding.
- [x] Write service tests proving adding/removing one entitlement changes only its `wl:<entitlementID>:<exitID>` identities, preserves canary/static users, and produces a new generation for every active Origin.
- [x] Write route-matrix tests proving any active Origin routes each managed identity to the same selected exit and that the public country label is derived from exit metadata only.
- [x] Run focused controlplane tests; require RED.
- [x] Add migration 15 and typed desired/receipt services. Reuse `ExternalActionCommand` with action key `<node-id>:<generation>:<desired-sha256>`.
- [x] Mark a route generation `ready` only after every active Origin has a matching unexpired receipt for its current Xray process boot identity and config digest and the selected exit relay is healthy. On unknown provider outcome, read the durable receipt for the same action key; never resend before resolution.
- [x] Run migration, rqlite, external-action, and race tests; require GREEN.
- [x] Commit with message `feat(controlplane): persist sidecar desired generations`.

## Task 12: Build the isolated mTLS node agent

**Files:**

- Create: `sidecar-agent/go.mod`
- Create: `sidecar-agent/go.sum`
- Create: `sidecar-agent/cmd/maestro-xray-cdn-agent/main.go`
- Create: `sidecar-agent/internal/agent/model.go`
- Create: `sidecar-agent/internal/agent/reconcile.go`
- Create: `sidecar-agent/internal/agent/reconcile_test.go`
- Create: `sidecar-agent/internal/agent/receipts.go`
- Create: `sidecar-agent/internal/agent/receipts_test.go`
- Create: `sidecar-agent/internal/xrayclient/client.go`
- Create: `sidecar-agent/internal/xrayclient/client_test.go`
- Create: `sidecar-agent/internal/server/server.go`
- Create: `sidecar-agent/internal/server/server_test.go`
- Create: `deploy/maestro-xray-cdn-agent.service`
- Create: `deploy/maestro-xray-cdn-agent.env.example`
- Modify: `backend/internal/release/templates.go`
- Create: `backend/internal/release/handler_service_test.go`

**Pins and ports:**

- Separate module uses Go 1.26 and `github.com/xtls/xray-core v0.0.0-20260509173629-1bdb488c9ec0`, the exact v26.5.9 release commit.
- Xray API remains `127.0.0.1:18082` with mTLS and services `StatsService`, `HandlerService`; port 18082 is never opened in any host firewall.
- Each sidecar exposes isolated TLS VLESS relay inbound `maestro-cdn-exit-in` on port 18084. Firewall port 18084 accepts only the current active Origin IP set. It is independent of production 3x-ui/Xray.
- Node agent listens on `0.0.0.0:18443` with mandatory client certificate name `maestro-whitelist-controller`; production firewall permits this port only from the current S1 control-plane IP.
- Xray API accepts client certificate names `maestro-metering-client` and `maestro-sidecar-agent` from the dedicated client CA.

**Steps:**

- [x] Write fake HandlerService tests for list/add/remove, exact desired convergence, duplicate request, stale generation, partial RPC failure, restart receipt invalidation/recovery, receipt TTL expiry, config digest mismatch, release mismatch, and no add/remove operation against a non-managed or canary/static email.
- [x] Write TLS tests that reject plaintext, unknown CA, wrong client name, expired cert, oversized body, non-canonical JSON, and request digest mismatch.
- [x] Run `go test ./...` from `sidecar-agent`; require RED.
- [x] Implement canonical desired manifests and local receipts under `/var/lib/maestro-xray-cdn-agent/receipts` using atomic rename, mode 0600, fsync, and bounded retention.
- [x] Use upstream v26.5.9 `HandlerService` types to read current users and call `AlterInbound`. Apply adds before removals, diff only the managed prefix, preserve all static/canary users, verify the final managed set, and emit a receipt only after exact convergence.
- [x] Derive `xray_process_boot_id` from the host boot ID and Xray process start identity. On process start and after Xray restart, invalidate the prior receipt and reconcile the last durable desired generation before reporting readiness.
- [x] Refresh exact-set readiness every 10 seconds and set receipt TTL to 30 seconds; an expired receipt is non-ready even when its generation number matches.
- [x] Extend the immutable release template validator for `HandlerService` while preserving loopback-only Xray API and mTLS.
- [x] Add fixed outbound/relay route metadata and four static rules such as `user: ["regexp:^wl:[^:]+:exit-s1$"] → outboundTag: "exit-s1"`. Each `exit-*` outbound uses TLS VLESS to port 18084 of the selected isolated sidecar, including loopback-to-local relay for the local exit. Route `inboundTag: ["maestro-cdn-exit-in"]` only to local freedom. Add an explicit terminal blackhole and no default exit. Reject a release whose Origin→exit matrix is incomplete or can loop.
- [x] Validate relay server certificate/SNI, protected per-exit relay credential, ALPN, source firewall, and exact exit health without storing relay secrets in the immutable template or receipts.
- [x] Run an integration test against pinned Xray 26.5.9 proving every managed exit suffix selects the intended outbound, while unknown, malformed, or unsupported exit identities are blocked.
- [x] Add systemd sandboxing, dedicated user, read-only certificate paths, and no shell execution.
- [x] Run sidecar-agent unit/race/vet tests and release template tests; require GREEN.
- [x] Commit with message `feat(sidecar): reconcile whitelist identities over mtls`.

## Task 13: Connect external actions to node agents

**Files:**

- Create: `backend/internal/sidecaragentclient/client.go`
- Create: `backend/internal/sidecaragentclient/client_test.go`
- Create: `backend/internal/controlplane/whitelist_sidecar_actions.go`
- Create: `backend/internal/controlplane/whitelist_sidecar_actions_test.go`
- Modify: `backend/cmd/maestro-panel/main.go`
- Create: `backend/cmd/maestro-panel/runtime_whitelist_sidecar_test.go`

**Steps:**

- [x] Write tests for mTLS request, exact action key, succeeded receipt, 409 stale generation, timeout-before-send, timeout-after-send, receipt lookup, and no blind retry after unknown outcome.
- [x] Run focused tests; require RED.
- [x] Implement `ExternalActionSender` for node agents with bounded timeouts, request-size limits, strict hostname verification, and zero credential logging.
- [x] Wire only explicit whitelist outbox events to this sender. Existing order, ordinary provisioning, and unrelated external actions remain unchanged.
- [x] For enable/resume: create generation → reconcile every active Origin → verify current process-boot/config/generation receipts and exit health → mark ready → publish. For revoke: stop publication → create removal generation → reconcile every active Origin. Make publication depend on the unexpired ready generation and exact release.
- [x] Run controlplane, panel, external-action, race, and vet tests; require GREEN.
- [x] Commit with message `feat(controlplane): deliver whitelist sidecar actions`.

## Task 14: Prove compatibility, accounting, and rollback in CI

**Files:**

- Create: `scripts/repro/whitelist-commercial-balance.sh`
- Create: `scripts/repro/whitelist-publication-cache.sh`
- Create: `scripts/repro/whitelist-sidecar-reconcile.sh`
- Create: `scripts/tests/test_yandex_cdn_commercial_repro.py`
- Modify: `.github/workflows/yandex-cdn-release.yml`
- Modify: `ops/validate-yandex-cdn-release.sh`
- Modify: `ops/validate-yandex-cdn-release.ps1`
- Create: `docs/yandex-cdn-whitelist/COMMERCIAL_ACCEPTANCE_MATRIX.md`

**Steps:**

- [x] Add offline fixtures for period rollover, boundary interval closure, top-up, duplicate payment, meter reset/replay, post-cache suspension, all-Origin readiness, any-Origin→selected-exit routing, desired generation, unknown receipt recovery, Xray restart reconciliation, and resume.
- [x] Extend CI with a separate Go 1.26 sidecar-agent job, backend Go 1.25 jobs, Python bot tests, race/vet, migration integration, security tests, and unchanged Android 1.0.158-task7-test artifact proof.
- [x] Prove current production 1.0.157 bare subscription fixture remains exact.
- [x] Run both release wrappers, `git diff --check`, secret scans, and the complete local test set possible on the host.
- [x] Push the exact commit, wait for every required GitHub Action on that SHA, and record run IDs and conclusions.
- [x] Request independent code/security review. Resolve every blocking finding with a new exact-SHA CI cycle.
- [x] Commit with message `test(whitelist): prove commercial delivery release gates`.

## Task 15: Inventory, backup, canary, and staged production rollout

**Files:**

- Create: `docs/yandex-cdn-whitelist/PRODUCTION_FLEET_INVENTORY.md`
- Create: `docs/yandex-cdn-whitelist/COMMERCIAL_ROLLOUT_RUNBOOK.md`
- Create: `docs/yandex-cdn-whitelist/COMMERCIAL_ROLLBACK.md`
- Create: `docs/yandex-cdn-whitelist/COMMERCIAL_CANARY_EVIDENCE.md`
- Modify: `CONTEXT_HANDOFF.md`
- Modify: `docs/yandex-cdn-whitelist/BASELINE_MANIFEST.json`

**Preconditions for every host mutation:** exact reviewed SHA; all required CI GREEN; current console access; authoritative host/IP/role; ordinary service inventory; port 18081/18082/18084/18443 conflict check; verified backup and restore command; immutable release artifact digest; node certificate proof; firewall plan; rollback under five minutes.

**Steps:**

- [ ] Read-only inventory S4, S2, S3, then current S1. Record facts separately from design assumptions and redact secrets.
- [ ] Capture and verify backups before each mutation. Never touch production 3x-ui/Xray units or their ports/config directories.
- [ ] Apply S4 sidecar/agent first. Validate direct sidecar, Yandex CDN path, literal edge with correct SNI/Host, per-user uplink/downlink counters, add/revoke/resume of one test route identity, receipt recovery, exit label truth, and ordinary baseline.
- [ ] Roll back S4 once deliberately and prove restoration in under five minutes; then re-apply the same immutable release.
- [ ] Repeat gated rollout on S2, S3, and current S1. Before each Origin joins the common CDN group, prove it holds the complete managed desired set and routes every exposed country identity to the same selected exit. Stop immediately on any identity, route, release, port, health, accounting, or rollback mismatch.
- [ ] Run regional client canaries for Incy and Happ: import, refresh, TCP, UDP, DNS, idle, network transition, attribution, zero balance, top-up resume, and ordinary-only fallback.
- [ ] Prove the same customer subscription imports, refreshes, and works through MaestroVPN, Happ, Incy, Karing, and standards-compliant clients; prove delivery and refresh through both Telegram bots, the customer channel, panel login, and last-known-good cache without exposing CDN routes to customers who have not bought the CDN entitlement.
- [ ] Keep the private test subscription active as the rollback canary throughout rollout. Delete it automatically only after S4 → S2 → S3 → S1 is GREEN, automatic refresh has reached existing customer subscriptions, all required client/bot/channel checks are GREEN, and a recoverable last-known-good path is recorded. Any mismatch blocks deletion.
- [ ] Run shadow accounting and request-cost observation for 48 hours. Compare Xray counters, ledger debits, Yandex CDN GB/request counts, and expected customer balance.
- [ ] Keep customer publication default OFF until the separate final cutover gate. Real charges and production OTA remain OFF.
- [ ] Regenerate `BASELINE_MANIFEST.json` from a clean exact-SHA tree, update handoff with branch/SHA/CI/run IDs/host evidence/remaining stop gates, and commit with message `docs(whitelist): record staged production evidence`.

## Completion Definition

Implementation is complete only when Tasks 1–14 are merged on the canonical exact SHA with all required CI GREEN and independent review closed. Production staging is complete only when Task 15 records GREEN evidence for S4 → S2 → S3 → S1, verified rollback, client matrix, and 48-hour observation. The service is not called customer-live until the owner separately authorizes the final traffic cutover; ordinary VPN must remain working throughout.
