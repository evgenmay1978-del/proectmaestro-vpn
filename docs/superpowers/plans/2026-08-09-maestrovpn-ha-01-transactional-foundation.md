# MaestroVPN HA Transactional Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Создать проверяемый rqlite-клиент, изолированный 3-node CI-кластер, checksummed schema и безопасное хранение секретов без изменения production.

**Architecture:** Go backend обращается к любому voter через mTLS HTTP API rqlite, проверяет каждую statement result и не повторяет write с неизвестным outcome. Схема и crypto слой становятся основанием всех следующих планов; старые JSON stores пока остаются единственным live/default режимом.

**Tech Stack:** Go 1.25 standard library, rqlite v10.1.0, SQLite SQL, Bash, GitHub Actions.

## Global Constraints

- S2/S3/S4 — три voter; S1 не voter.
- Production/DNS/OTA/systemd/VPN/bot/database mutations запрещены.
- Dual-write и rqlite queued writes запрещены.
- Секреты и customer data не входят в Git/tests/logs/artifacts.
- Локально не запускать 3-node cluster на слабом компьютере; integration/fault tests выполняются GitHub Actions.
- rqlite archive: `rqlite-v10.1.0-linux-amd64.tar.gz`, SHA-256 `9dca2fc957ee9445bdb94c08ca0ccd1b761d33c7e6fd729c224d1066594a3375`.
- Actions pins: checkout `11d5960a326750d5838078e36cf38b85af677262`, setup-go `40f1582b2485089dde7abd97c1529aa768e1baff`.

---

### Task 1: Fail-closed rqlite HTTP client

**Files:**
- Create: `backend/internal/rqlite/client.go`
- Create: `backend/internal/rqlite/client_test.go`

**Interfaces:**
- Produces: `New(Config) (*Client, error)`, `Request`, `QueryLinearizable`, `QueryStrong`, `Backup`, `Statement`, `Result`, `StatementError`, `TransportError`.
- Consumes: Go standard library; certificate/key/CA paths are injected through `Config` and never logged.

- [ ] **Step 1: Write the failing response-contract tests**

```go
func TestRequestRejectsStatementErrorInsideHTTP200(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Query().Get("transaction") == "" { t.Error("transaction flag missing") }
        _, _ = io.WriteString(w, `{"results":[{"rows_affected":1},{"error":"constraint failed"}]}`)
    }))
    defer srv.Close()
    c := mustClient(t, srv.URL)
    _, err := c.Request(context.Background(), Linearizable, true,
        Statement{SQL: "INSERT INTO a VALUES(?)", Args: []any{1}},
        Statement{SQL: "UPDATE b SET x=?", Args: []any{2}},
    )
    var stmtErr *StatementError
    if !errors.As(err, &stmtErr) || stmtErr.Index != 1 { t.Fatalf("error = %#v", err) }
}

func TestRequestUsesUnifiedEndpointAndNeverQueues(t *testing.T) {
    // Record request and assert POST /db/request, associative, level=linearizable,
    // transaction present, queue absent, body exactly [[sql,arg...]].
}

func TestWriteUnknownOutcomeIsNotRetriedByClient(t *testing.T) {
    // Consume the request body, close the connection, and assert exactly one call.
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `cd backend && go test ./internal/rqlite -run 'TestRequest|TestWrite' -count=1`

Expected: FAIL because package/types do not exist.

- [ ] **Step 3: Implement the minimal client**

```go
type Consistency string
const (
    Linearizable Consistency = "linearizable"
    Strong       Consistency = "strong"
)
type Statement struct { SQL string; Args []any }
type Config struct {
    Endpoints []string
    Username, Password, CAFile, CertFile, KeyFile string
    Timeout time.Duration
}
func (c *Client) Request(ctx context.Context, level Consistency, tx bool, ss ...Statement) ([]Result, error)
func (c *Client) Backup(ctx context.Context, w io.Writer) error
```

Encode each statement as `[SQL,arg1,...]`, POST only to `/db/request`, request `associative`, set `transaction` only for atomic batches, and reject every `results[i].error` even on HTTP 200. A mutating request is sent once to one selected endpoint and is never automatically replayed after any transport response; it omits rqlite's `redirect` parameter so follower-to-leader forwarding, when needed, occurs inside the cluster. Disable Go client-side redirects: any 3xx that reaches the client is a transport/unknown-outcome error and is never re-issued. The command layer resolves an unknown outcome by linearizable operation-row read. Only read-only query helpers may rotate/retry endpoints under their context deadline.

- [ ] **Step 4: Add TLS, redirect, limits and backup tests**

Cover invalid CA/cert/key, Basic Auth without credential leakage, response size cap, context cancellation, `TestMutatingRequestRejectsRedirectWithoutReplay`, server-side follower forwarding and streaming `GET /db/backup?fmt=delete`.

- [ ] **Step 5: Run tests and static checks**

Run: `cd backend && go test ./internal/rqlite -count=1 && go vet ./internal/rqlite`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/rqlite
git commit -m "feat(backend): add fail-closed rqlite client"
```

### Task 2: Pinned isolated three-node CI harness

**Files:**
- Create: `ops/ha/ci-rqlite-cluster.sh`
- Create: `ops/ha/test-ci-rqlite-cluster.sh`
- Create: `.github/workflows/ha-control-plane.yml`
- Modify: `ops/README.md`

**Interfaces:**
- Produces: `ci-rqlite-cluster.sh start|status|stop`; local endpoints `127.0.0.1:4401`, `:4403`, `:4405`; all data below runner temp.
- Consumes: Task 1 tests and pinned archive above.

- [ ] **Step 1: Write the shell contract before the harness**

```bash
test "$(grep -c 'RQLITE_VERSION=10.1.0' ops/ha/ci-rqlite-cluster.sh)" -eq 1
test "$(grep -c '9dca2fc957ee9445bdb94c08ca0ccd1b761d33c7e6fd729c224d1066594a3375' ops/ha/ci-rqlite-cluster.sh)" -eq 1
! grep -Eq 'curl[^|]*\|[[:space:]]*(sh|bash)' ops/ha/ci-rqlite-cluster.sh
```

Also assert three distinct node IDs, one leader/three voters after `start`, `PRAGMA foreign_keys` equals `1` through every node, and that `stop` kills only recorded PIDs under the validated temp root.

- [ ] **Step 2: Run and verify RED**

Run: `bash ops/ha/test-ci-rqlite-cluster.sh`

Expected: FAIL because harness is absent.

- [ ] **Step 3: Implement safe lifecycle**

Use `mktemp -d`, validate its resolved path beneath `${RUNNER_TEMP:-/tmp}`, verify SHA-256 before extraction, bind loopback only, start all three nodes with `-fk`, write one PID per node, poll `/readyz`, verify `PRAGMA foreign_keys=1` on each endpoint, and always trap `stop`. No production IP, credential, SSH or host mount is allowed in this script.

- [ ] **Step 4: Add non-secret HA CI**

```yaml
name: HA control-plane tests
on:
  pull_request:
    paths: ['backend/**', 'ops/ha/**', 'deploy/ha/**', '.github/workflows/ha-control-plane.yml']
  push:
    branches: ['main', 'codex/mobile-4d-deck']
  workflow_dispatch:
permissions:
  contents: read
jobs:
  go-and-rqlite:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4
      - uses: actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff # v5
        with: {go-version-file: backend/go.mod, cache-dependency-path: backend/go.sum}
      - run: cd backend && env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go test ./...
      - run: cd backend && env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go test -race ./...
      - run: bash ops/ha/test-ci-rqlite-cluster.sh
      - run: bash ops/ha/ci-rqlite-cluster.sh start
      - run: cd backend && go test -tags=rqlite_integration ./...
      - if: always()
        run: bash ops/ha/ci-rqlite-cluster.sh stop
```

- [ ] **Step 5: Run lightweight validation**

Run: `bash -n ops/ha/ci-rqlite-cluster.sh && bash -n ops/ha/test-ci-rqlite-cluster.sh`

Expected: PASS. Do not start it locally.

- [ ] **Step 6: Commit, push, run only HA CI and record exact run**

```bash
git add ops/ha .github/workflows/ha-control-plane.yml ops/README.md
git commit -m "test(ha): add pinned three-node rqlite harness"
```

After push, dispatch only `ha-control-plane.yml`. Record exact HEAD/run/result in `CONTEXT_HANDOFF.md` after GREEN; do not invoke Android release or any production workflow.

### Task 3: Checksummed relational schema

**Files:**
- Create: `backend/internal/controlplane/migrations/0001_control_plane.sql`
- Create: `backend/internal/controlplane/migrations.go`
- Create: `backend/internal/controlplane/migrations_test.go`
- Create: `backend/internal/controlplane/types.go`

**Interfaces:**
- Produces: `Migrator.Apply(ctx)`, `Migrator.Verify(ctx)`, `SchemaVersion`; typed payment/provisioning/node/inbox states.
- Consumes: `rqlite.RQLite` and `rqlite.Statement`.

- [ ] **Step 1: Write a failing schema contract test**

Apply migrations twice, require `PRAGMA foreign_keys=1` before the first DDL, inspect `sqlite_master`, and require zero rows from `PRAGMA foreign_key_check`. A disabled foreign-key setting on any configured voter aborts startup/readiness. Assert a changed checksum aborts startup.

```go
wantTables := []string{
    "schema_migrations", "customers", "credentials", "subscription_tokens", "devices",
    "tariff_versions", "orders", "active_order_guards", "payments", "trial_redemptions",
    "idempotency_requests", "nodes", "node_services", "desired_node_state", "outbox_events",
    "node_leases", "cluster_job_leases", "node_apply_receipts", "tombstones", "tombstone_targets",
    "telegram_pollers", "telegram_inbox", "telegram_callbacks",
    "telegram_delivery_outbox", "telegram_bindings", "external_actions", "operations",
    "operation_batches", "cluster_settings", "setting_members", "setting_secrets",
    "principals", "principal_roles", "principal_credentials", "web_sessions",
    "rate_limit_buckets", "import_runs", "import_batches", "backup_watermarks",
    "audit_events", "health_write_canary",
}
```

- [ ] **Step 2: Run on GitHub and verify RED**

Run: `cd backend && go test -tags=rqlite_integration ./internal/controlplane -run TestMigrations -count=1`

Expected: FAIL because migrations are absent.

- [ ] **Step 3: Implement exact keys and constraints**

Use integer UTC seconds for expiry/time and kopecks for amounts. The following payment/idempotency definitions are mandatory parts of the complete schema:

```sql
CREATE TABLE payments (
  payment_id TEXT PRIMARY KEY,
  order_id TEXT NOT NULL UNIQUE REFERENCES orders(order_id) ON DELETE RESTRICT,
  provider TEXT NOT NULL,
  provider_event_id TEXT,
  receipt_ref TEXT,
  amount_minor INTEGER NOT NULL CHECK(amount_minor > 0),
  currency TEXT NOT NULL CHECK(currency = 'RUB'),
  confirmed_at_unix INTEGER NOT NULL,
  UNIQUE(provider, provider_event_id),
  UNIQUE(receipt_ref)
);
CREATE TABLE idempotency_requests (
  scope TEXT NOT NULL, command_type TEXT NOT NULL, idempotency_key TEXT NOT NULL,
  request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
  resource_id TEXT NOT NULL, decision TEXT NOT NULL,
  operation_id TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL CHECK(status IN ('applying','applied')),
  response_json TEXT, created_at_unix INTEGER NOT NULL,
  PRIMARY KEY(scope, command_type, idempotency_key)
);
```

The same migration defines these exact invariants:

- `orders` stores immutable tariff version/amount/currency/duration, `expires_at_unix=created_at_unix+86400`, separate payment/provisioning states, confirmed/result expiry, result generation and operation ID;
- `active_order_guards` has PK `(identity_scope,identity_key_hmac)` and `UNIQUE(order_id,identity_scope)`, allowing one winning order to hold both buyer and customer guards; only a terminal transition or expiry of an unclaimed `created` order may release every guard for that order;
- Telegram buyer HMAC is derived centrally with the cluster HMAC key from raw `from_user.id` only and explicitly excludes bot token/ID, bot name and chat ID, so both bots contend on one guard; every terminal transition deletes all guard scopes for the order;
- `trial_redemptions` stores current plus legacy anchor/DRM HMAC versions and IP-scope HMAC, never raw identities;
- `node_services` separates `desired_target`, `apply_enabled`, `fenced` and `retired`; only audited permanent retirement removes a required target;
- `desired_node_state` stores encrypted payload envelope and SHA-256, not epoch/incarnation; `tombstone_targets` freezes the required service acknowledgements for hard delete;
- `cluster_job_leases` uses DB time and monotonic fences for expiry, external-action and backup workers; `external_actions` stores one durable result/unknown outcome per idempotency key;
- `telegram_pollers` stores durable offset plus fence; inbox payload/callback binding and delivery payload are encrypted, while update/callback/delivery keys are unique HMACs;
- `cluster_settings(name,version,public_json)` holds only validated non-secret scalar/object values; `setting_members(setting_name,member_kind,member_hmac)` normalizes allowlists, `setting_secrets(setting_name,field_name,key_version,nonce,ciphertext)` holds encrypted panel/WB/olcRTC/VK/OTA secrets, `principals` holds identity/revocation epoch, `principal_roles` is default-deny normalized RBAC and `principal_credentials` holds only password hashes or encrypted external credentials. Public value, member/secret reference and audit row change in one version-CAS transaction;
- `operations` and `operation_batches` bind a deterministic input manifest/digest to resumable bounded bulk changes such as endpoint migration and delete-expired; a retry resumes the same operation and a changed digest conflicts;
- `rate_limit_buckets` has actor/IP scope plus bounded window, and `import_runs` plus `import_batches` bind full/delta parent digests and crash-resumable batch commits;
- `backup_watermarks` monotonically records dirty generation and the last verified uploaded generation/object digest; every business transaction advances dirty state in the same commit;
- `health_write_canary` has one row per panel node, so repeated probes cannot grow the table.

Add an `AFTER INSERT` first-writer trigger that aborts an absent/invalid order decision after uniqueness has selected the winner. Add payment/provisioning transition triggers, a final `idempotency_requests.status='applied'` trigger that verifies payment/customer generation/desired/outbox/saved response, and append-only triggers rejecting audit update/delete. Saved idempotency responses contain only stable IDs and redacted values, or an encrypted response envelope bound to the request row; they never contain plaintext `SubToken`, private subscription URL, protocol password or credential. Every zero-row CAS is followed in the same transaction by a DB assertion that raises `ABORT`. Every FK has explicit `ON DELETE`; generations, amounts, durations and limits have CHECKs. Empty provider event/receipt input is normalized to SQL `NULL`, never an empty string. Once a migration checksum has been used by a persistent cluster, it is immutable and every later change uses the next numbered checksummed migration.

- [ ] **Step 4: Seed immutable current tariffs and four nodes**

Seed `tariff_1m_v1` (30 days, 40000 minor RUB), `tariff_2m_v1` (60 days, 80000 minor RUB), and stable node IDs S1–S4. S2/S3/S4 are enabled rqlite voters. S1 is a non-voter with `apply_enabled=0`, `fenced=1`, `retired=0`; its `node_services` remain required desired-state/outbox/tombstone targets so downtime never drops catch-up work. A migration re-run may not modify an existing tariff version.

- [ ] **Step 5: Run schema tests**

Run: `cd backend && go test -tags=rqlite_integration ./internal/controlplane -run 'TestMigration|TestSchema|TestConstraint' -count=1`

Expected: PASS with zero FK violations.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/controlplane
git commit -m "feat(controlplane): add checksummed HA schema"
```

### Task 4: Encrypted credentials and stable identifiers

**Files:**
- Create: `backend/internal/controlplane/crypto.go`
- Create: `backend/internal/controlplane/crypto_test.go`
- Modify: `backend/internal/controlplane/types.go`

**Interfaces:**
- Produces: `SecretBox.Seal(scope, plaintext) Envelope`, `Open(scope, envelope)`, `LookupHMAC`, `NewID(prefix)`, `CanonicalLoginKey`.
- Consumes: distinct 32-byte encryption and HMAC keys decoded at startup.

- [ ] **Step 1: Write failing crypto tests**

Test AES-256-GCM round-trip, random nonces, wrong-key rejection, deterministic HMAC, redacted errors and collision-free prefixed IDs under 100 concurrent goroutines. Also prove that moving an envelope between owners, rows, tables, fields or secret kinds fails authentication, that a missing referenced key version makes readiness red, and that a configured previous key version can decrypt for rotation while every new seal uses only the current version.

- [ ] **Step 2: Run and verify RED**

Run: `cd backend && go test ./internal/controlplane -run 'TestSecret|TestLookup|TestNewID' -count=1`

Expected: FAIL because functions are absent.

- [ ] **Step 3: Implement only standard-library crypto**

```go
type SecretScope struct { OwnerType, OwnerID, Field, Kind string }
type Envelope struct { KeyVersion int; Nonce, Ciphertext []byte }
type SecretBox struct { current int; aeadByVersion map[int]cipher.AEAD; hmacKey []byte }
func (b *SecretBox) LookupHMAC(kind string, plaintext []byte) string {
    mac := hmac.New(sha256.New, b.hmacKey)
    _, _ = mac.Write([]byte(kind + "\x00"))
    _, _ = mac.Write(plaintext)
    return hex.EncodeToString(mac.Sum(nil))
}
```

Canonical AAD is a length-delimited encoding of key version plus every `SecretScope` field. Reject empty scope components and identical encryption/HMAC keys. Do not implement any string formatter that contains nonce, ciphertext or plaintext. Secret import validates the expected owner/field/kind before opening; rotation is a fenced resumable operation and never exposes plaintext outside the owning transaction.

- [ ] **Step 4: Run tests and commit**

Run: `cd backend && go test ./internal/controlplane -run 'TestSecret|TestLookup|TestNewID' -count=1`

```bash
git add backend/internal/controlplane/crypto.go backend/internal/controlplane/crypto_test.go backend/internal/controlplane/types.go
git commit -m "feat(controlplane): protect shared credentials"
```

## Plan 01 acceptance

- GitHub `ha-control-plane.yml` is GREEN on the exact pushed SHA.
- Unit client tests prove no queued writes, no hidden per-result errors and no unknown-outcome auto-retry.
- Migration is repeatable, checksummed and FK-clean on a fresh 3-node cluster.
- Crypto tests show no secret rendering.
- Production remains unchanged; legacy JSON is still the default/live source.
