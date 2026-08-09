# MaestroVPN HA Control Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Перевести MaestroVPN с единственного S1/file-store control plane на отказоустойчивый транзакционный control plane rqlite на S2/S3/S4, сохранив старые Android/API-контракты и гарантируя ровно одно начисление по ручной оплате.

**Architecture:** Новый backend-режим использует один rqlite-кворум, транзакционный command service, desired state/outbox и fenced apply-agents; старый JSON/SQLite режим остаётся отдельным default до cutover и никогда не получает dual-write. Stateless-панели и оба Telegram-бота используют одну бизнес-истину, а `wapmixx.ru:8911` переключается только на write-ready TLS-узел активной A-записью.

**Tech Stack:** Go 1.25, SQLite SQL через rqlite HTTP API v10.1.0, Python 3 standard library для повторяемых ops-проверок, Bash/systemd/nginx, GitHub Actions, SpaceWeb JSON-RPC DNS API, существующее Yandex Object Storage.

## Global Constraints

- Ровно три voting-узла rqlite: S2 `85.137.166.237`, S3 `46.30.42.151`, S4 `89.125.19.95`; S1 `194.48.141.106` не voter.
- Новые платные VM, балансировщики, базы, SaaS и платёжные провайдеры запрещены.
- S1 остаётся обязательной non-retired целью desired state/outbox/tombstones, но его apply выключен и fenced до безопасного возврата с новой incarnation.
- Публичный клиентский адрес остаётся `https://wapmixx.ru:8911`.
- До отдельного production approval запрещены DNS/API mutations, deploy, systemd/nginx/firewall changes, service restart, database import, bot-token rotation, Release и OTA.
- TV UI/assets, `TvEskizHome.kt`, `TvEskizSpec.kt`, `tvm_*`, D-pad/focus/Back и TV-геометрия не изменяются.
- Тяжёлые сборки, 3-node/fault-тесты и Android compile выполняются только GitHub Actions.
- Legacy и rqlite режимы выбираются взаимоисключающе; dual-write и local pending writes запрещены.
- Все mutating commands получают stable idempotency key; неизвестный outcome разрешается повторным linearizable/strong read, а не повторным side effect.
- Canonical payment hash содержит только `{order_id,decision,tariff_version,payment_reference}`; actor/channel/callback/time остаются audit metadata.
- `queued` rqlite writes и ручные SQL `BEGIN`/`COMMIT` запрещены; транзакции задаются только параметром HTTP API `transaction`.
- Секреты, customer identifiers и private subscription URLs не попадают в Git, логи, owner Telegram messages, audit, test fixtures или artifacts; encrypted client-ready delivery может содержать только собственный URL этого клиента.
- Производственный rqlite pin: `v10.1.0`; Linux amd64 archive SHA-256 `9dca2fc957ee9445bdb94c08ca0ccd1b761d33c7e6fd729c224d1066594a3375`.
- SQLite foreign keys are mandatory: every CI and production `rqlited` process starts with `-fk`, and readiness rejects `PRAGMA foreign_keys != 1` on any voter.
- Новые GitHub workflows используют SHA pins: checkout `11d5960a326750d5838078e36cf38b85af677262`, setup-go `40f1582b2485089dde7abd97c1529aa768e1baff`, upload-artifact `ea165f8d65b6e75b540449e92b4886f43607fa02`, download-artifact `d3f86a106a0bac45b974a628896c90dbdf5c8093`.
- GO возможен только при выполнении всех условий раздела 18 утверждённой design-spec; любой неразрешённый import/fence/TLS/DNS/TV/OTA diff означает NO-GO.

---

## File and ownership map

### Existing files modified narrowly

- `backend/cmd/maestro-panel/main.go` — composition root; adds an exclusive `legacy|rqlite` mode and HA dependencies.
- `backend/internal/api/api.go` — keeps existing routes and response shapes; delegates business reads/writes to a port and adds `/livez`, `/readyz/read`, `/readyz/write`.
- `backend/internal/api/order.go` — removes process-local correctness from HA mode and maps canonical payment results back to legacy `pending|paid` responses.
- `backend/internal/api/admin.go` — routes owner renew/create and confirm/cancel through the same command service.
- `backend/internal/api/panel.go` and `panel_ui.go` — cluster-backed sessions/RBAC plus owner order status/confirm/cancel without displaying tokens.
- `backend/internal/api/trial.go` — cluster-backed trial redemption in HA mode.
- `backend/internal/api/*_test.go` — exact old-client contract tests remain authoritative.
- `backend/internal/subgen/sharelinks.go` — no format change; consumed by the new canonical URL builder.
- `deploy/vpn_bot_maestro_orders.py` — temporary legacy bridge is changed only after the shared HA bot adapter is green; canonical Maestro/Karing buttons replace prose that tells users to append `app=karing`.
- `deploy/maestro-panel.env.example`, `deploy/maestro-panel.service`, `deploy/maestro-backup.sh`, `ops/README.md`, `docs/runbook-s1-recovery.md` — HA-safe configuration and procedures; no live values.
- `.github/workflows/android-test.yml` — unchanged unless a later compatibility task must compile a common Android contract; no release job is touched.

### New backend files

- `backend/internal/rqlite/client.go` / `client_test.go` — mTLS HTTP client, parameterized statements, transaction and per-result error handling.
- `backend/internal/controlplane/migrations/0001_control_plane.sql` — schema, FK/CHECK/UNIQUE constraints and transition/idempotency triggers.
- `backend/internal/controlplane/migrations.go` / `migrations_test.go` — embedded checksummed migrations.
- `backend/internal/controlplane/types.go` — stable IDs, states, DTOs and typed errors.
- `backend/internal/controlplane/crypto.go` / `crypto_test.go` — AES-256-GCM envelopes plus separate HMAC lookup keys.
- `backend/internal/controlplane/store.go` / `store_test.go` — customer, token, device, tariff, settings and audit reads/writes.
- `backend/internal/controlplane/orders.go` / `orders_test.go` — create/claim/confirm/cancel and exactly-once expiry transaction.
- `backend/internal/controlplane/trials.go` / `trials_test.go` — durable trial anti-abuse ledger.
- `backend/internal/controlplane/outbox.go` / `outbox_test.go` — desired state, tombstones, leases, events and receipts.
- `backend/internal/controlplane/telegram.go` / `telegram_test.go` — durable inbox/poller lease/delivery records.
- `backend/internal/controlplane/health.go` / `health_test.go` — read/write readiness and bounded committed write-canary.
- `backend/internal/controlplane/service.go` — narrow façade used by HTTP, panel and bot adapters.
- `backend/internal/api/controlplane_port.go` / `controlplane_port_test.go` — compatibility mapping between existing API JSON and the new service.
- `backend/cmd/maestro-import/main.go`, `backend/internal/importer/*.go` — deterministic legacy importer, collision report, shadow digest and fixtures.
- `backend/cmd/maestro-agent/main.go`, `backend/internal/applyagent/*.go` — signed/fenced local apply protocol, drivers and reconciliation.
- `backend/cmd/maestro-bot/main.go`, `backend/internal/bot/*.go` — one binary used twice, crash-safe long polling and shared payment/link flows.

### New repeatable operations and deployment files

- `ops/ha/ci-rqlite-cluster.sh` — downloads the pinned archive, verifies SHA-256 and starts/stops an isolated 3-node test cluster.
- `ops/ha/fault-matrix.sh` — deterministic leader/quorum/crash/restore scenarios against fixtures only.
- `ops/ha/spaceweb_dns.py` and `ops/ha/tests/test_spaceweb_dns.py` — read-before-write JSON-RPC client with exact record/IP allowlist.
- `ops/ha/failover.py` and `ops/ha/tests/test_failover.py` — hysteresis/state machine; dry-run by default.
- `ops/ha/tls_dns_hook.py` and tests — DNS-01 TXT create/verify/cleanup without logging credentials.
- `ops/ha/backup-rqlite.sh`, `ops/ha/restore-rqlite.sh`, `ops/ha/verify-backup.py` and tests — authenticated encrypted DR bundle and restore epoch drill.
- `ops/ha/inventory.sh`, `ops/ha/fence-audit.sh`, `ops/ha/shadow-verify.sh` — redacted read-only evidence and fail-closed gates.
- `deploy/ha/rqlited@.service`, `deploy/ha/rqlite-{s2,s3,s4}.env.example` — exact voter identities/addresses and mTLS paths.
- `deploy/ha/maestro-panel.service`, `deploy/ha/maestro-agent.service`, `deploy/ha/nginx-maestro-panel.conf` — stateless panel, local agent and TLS listener templates.
- `deploy/ha/README.md`, `docs/runbook-ha-cutover.md`, `docs/runbook-ha-rollback.md`, `docs/runbook-ha-s1-return.md` — staged rollout, fencing and rollback boundaries.
- `.github/workflows/ha-control-plane.yml` — repository-only Go/unit/3-node/fault/contract tests.
- `.github/workflows/ha-dns-failover.yml` — protected production-environment scheduled/manual failover; no PR secret access.
- `.github/workflows/ha-tls-renewal.yml` — protected DNS-01 renewal and atomic S2/S3/S4 delivery.

## Interface contract used by all tasks

```go
type Statement struct {
    SQL  string
    Args []any
}

type RQLite interface {
    Request(ctx context.Context, consistency Consistency, transaction bool, statements ...Statement) ([]Result, error)
    Backup(ctx context.Context, w io.Writer) error
}

type CommandMeta struct {
    Scope, CommandType, IdempotencyKey string
    ActorID, Channel, SourceEventID string
    OccurredAt time.Time
}

type ConfirmPaymentCommand struct {
    Meta             CommandMeta
    OrderID          string
    TariffVersionID  string
    PaymentReference string
    ConfirmedAt      time.Time
}

type ConfirmPaymentResult struct {
    OperationID string
    OrderID     string
    CustomerID  string
    Login       string
    ExpiresAt   time.Time
    Generation  int64
    Replayed    bool
}

type Service interface {
    CreateOrder(context.Context, CreateOrderCommand) (OrderView, error)
    MarkPaymentClaimed(context.Context, ClaimPaymentCommand) (OrderView, error)
    ConfirmPayment(context.Context, ConfirmPaymentCommand) (ConfirmPaymentResult, error)
    CancelOrder(context.Context, CancelOrderCommand) (OrderView, error)
    CustomerByToken(context.Context, string) (CustomerView, error)
    CustomerByLogin(context.Context, string) (CustomerView, error)
    RedeemTrial(context.Context, RedeemTrialCommand) (CustomerView, error)
    ListCustomers(context.Context, CustomerFilter) ([]CustomerView, error)
    CustomerStats(context.Context) (CustomerStatsView, error)
    CustomerUsage(context.Context, string) (CustomerUsageView, error)
    ListOrders(context.Context, OrderFilter) ([]OrderView, error)
    ClusterStatus(context.Context) (ClusterStatusView, error)
    RecentAudit(context.Context, AuditFilter) ([]AuditView, error)
    Tariffs(context.Context) ([]TariffView, error)
    OrderByID(context.Context, string) (OrderView, error)
    SubscriptionSnapshot(context.Context, string) (SubscriptionSnapshot, error)
    TouchDevice(context.Context, TouchDeviceCommand) (DeviceDecision, error)
    ProvisionCustomer(context.Context, ProvisionCustomerCommand) (CustomerView, error)
    ExtendCustomer(context.Context, ExtendCustomerCommand) (CustomerView, error)
    RenewCustomer(context.Context, RenewCustomerCommand) (CustomerView, error)
    SetCustomerExpiry(context.Context, SetExpiryCommand) (CustomerView, error)
    DeleteCustomer(context.Context, DeleteCustomerCommand) error
    ResetDevices(context.Context, ResetDevicesCommand) error
    DisableCustomer(context.Context, CustomerStateCommand) (CustomerView, error)
    EnableCustomer(context.Context, CustomerStateCommand) (CustomerView, error)
    RunExpirySweep(context.Context, ExpirySweepCommand) (OperationView, error)
    ReconcileServices(context.Context, ReconcileServicesCommand) (OperationView, error)
    ApprovedOTA(context.Context) (OTAManifestView, error)
    CreateSession(context.Context, CreateSessionCommand) (SessionView, error)
    Authorize(context.Context, AuthorizeCommand) (PrincipalView, error)
    RevokeSessions(context.Context, RevokeSessionsCommand) error
    ReadSetting(context.Context, string) (SettingView, error)
    UpdateSetting(context.Context, UpdateSettingCommand) (SettingView, error)
    ChangePrincipalPassword(context.Context, ChangePasswordCommand) error
    OLCRTCState(context.Context) (OLCRTCView, error)
    SetOLCRTCRoom(context.Context, SetOLCRTCRoomCommand) (SettingView, error)
    SetOLCRTCGrant(context.Context, SetOLCRTCGrantCommand) (SettingView, error)
    WBTokenStatus(context.Context) (SecretStatusView, error)
    SetWBToken(context.Context, SetSecretCommand) error
    RequestWBRoom(context.Context, RequestWBRoomCommand) (ExternalActionView, error)
    VKTurnState(context.Context) (VKTurnView, error)
    UpdateVKTurn(context.Context, UpdateVKTurnCommand) (SettingView, error)
    SetVKTurnEnabled(context.Context, SetVKTurnEnabledCommand) (SettingView, error)
    MigrateServiceEndpoint(context.Context, MigrateEndpointCommand) (OperationView, error)
}
```

`ErrConflict` maps to HTTP 409, `ErrNoQuorum`/`ErrUnavailable` to retryable 503, `ErrNotFound` to 404, invalid input to 400, expired `/sub` to the existing 402. Legacy external fields remain `pending|paid` even though internal payment/provisioning states are separate.

---

## Approved execution package

Owner approval to continue implementation was given on 09.08.2026. The repository implementation order is mandatory:

1. [Transactional foundation](2026-08-09-maestrovpn-ha-01-transactional-foundation.md)
2. [Business, API and import](2026-08-09-maestrovpn-ha-02-business-api.md)
3. [Outbox, agents and Telegram](2026-08-09-maestrovpn-ha-03-bots-agents.md)
4. [Operations, DNS, TLS and cutover](2026-08-09-maestrovpn-ha-04-operations-cutover.md)

The complete section/test mapping and independent review record are in [HA plan coverage](2026-08-09-maestrovpn-ha-coverage.md).

Repository implementation does not authorize production import/deploy, DNS/TLS changes, bot token/service changes, Release or OTA. Production status remains `NO-GO (repository implementation only)` until every Task 18 gate and a later explicit owner approval.

---
