# MaestroVPN HA Operations, DNS, TLS and Cutover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить проверяемые backup/restore, hardened deployment templates, бесплатный SpaceWeb DNS failover, DNS-01 TLS и полный GO/NO-GO cutover набор без выполнения production-переключения.

**Architecture:** Репозиторий сначала строит и испытывает immutable artifacts на изолированном rqlite-кластере. Production workflows существуют только за GitHub protected environments и exact-SHA gates. DNS всегда содержит один ready A-record; TLS заранее валиден на каждом candidate. Cutover начинается с write-freeze и доказанного hard fence, а после первой rqlite business-write rollback остаётся только внутри rqlite truth.

Focused durable-backup references:

- [production backup adapter implementation plan](./2026-08-23-maestrovpn-ha-backup-production-adapters.md)
- [repository-safe operations contract](../../../ops/ha/README.md)
- [exact-SHA GitHub evidence](../../../CONTEXT_HANDOFF.md), recorded only after
  all required runs complete

**Tech Stack:** Go 1.25, Python 3 standard library, Bash, systemd/nginx, rqlite v10.1.0, GnuPG, OpenSSL/certbot DNS-01, GitHub Actions, SpaceWeb JSON-RPC, existing Yandex Object Storage.

## Global Constraints

- Выполнять только после GREEN Plans 01–03 на точном Git SHA.
- Repository-safe scripts/tests/templates разрешены; production deploy/import/DNS/TLS/token/systemd/firewall/OTA mutations требуют отдельного явного approval владельца.
- GitHub environments сейчас отсутствуют, `main` и рабочая ветка не защищены. Production workflows не получают secrets и не могут применяться, пока эти settings не созданы и не проверены.
- Три voter — S2/S3/S4; S1 никогда не становится voter.
- Public client URL остаётся `https://wapmixx.ru:8911`; apex имеет ровно одну A-запись ready panel IP.
- SpaceWeb secret, cluster CA/private keys, bot tokens, backup key и TLS private key не входят в Git/logs/artifacts.
- Production workflows не запускаются из `pull_request`; actions pinned by full SHA; permissions minimal; concurrency prevents overlap.
- Current TV APK/API and OTA parity проверяются без изменения TV UI/assets и без новой Release/OTA.
- Любой недоказанный fence, importer collision, bot-source drift, TLS/DNS/TV/OTA mismatch или Critical/Important review issue означает NO-GO.

---

### Task 14: Authenticated encrypted backup and empty-cluster restore epoch

**Files:**
- Create: `ops/ha/backup-rqlite.sh`
- Create: `ops/ha/restore-rqlite.sh`
- Create: `ops/ha/verify-backup.py`
- Create: `ops/ha/backup_worker.py`
- Create: `ops/ha/tests/test_backup_worker.py`
- Create: `ops/ha/tests/test_verify_backup.py`
- Create: `ops/ha/tests/fixtures/backup-manifest.json`
- Create: `ops/ha/tests/create-fixture-backup.sh`
- Create: `.github/workflows/ha-dr-drill.yml`
- Modify: `deploy/maestro-backup.sh`
- Modify: `deploy/maestro-backup-onchange.path`
- Modify: `deploy/maestro-backup.service`
- Modify: `deploy/maestro-backup.timer`
- Create: `deploy/ha/maestro-backup.service`
- Create: `deploy/ha/maestro-backup.timer`
- Create: `docs/runbook-ha-restore.md`

**Interfaces:**
- Produces: encrypted signed DR bundle, redacted verification report, `backup|verify|restore-drill` commands and monotonic restore epoch.
- Consumes: rqlite `/db/backup?fmt=delete`, existing Yandex Object Storage, offline GPG signing/encryption identities supplied outside Git.

- [ ] **Step 1: Write failing manifest/tamper tests**

Require a canonical manifest containing `format_version`, UTC timestamp, exact Git commit/artifact SHA, rqlite/schema version, cluster epoch, table counts, node inventory, receipt high-watermarks, dirty/verified watermark and SHA-256/size for every bundle member. Test missing/extra member, byte tamper, bad signature, wrong schema, stale epoch, count mismatch, plaintext credential scan and restore into a non-empty target. Worker tests cover burst coalescing, lost wakeup, crash before upload, unknown upload result, crash after upload/before verification, concurrent writes, stale lease handoff, stale temp cleanup/permissions, missing backup capability and a cutover gate proving only one backup implementation is enabled.

- [ ] **Step 2: Run and verify RED**

Run: `python -m unittest ops.ha.tests.test_verify_backup`

Expected: FAIL because verifier/scripts are absent.

- [ ] **Step 3: Implement consistent backup creation**

Fetch canonical SQLite only through the rqlite backup API into a service-owned `0700` directory below a fixed systemd `RuntimeDirectory`; files are `0600`. Install a trap before the first secret/file operation, reject `set -x`, clean the validated exact temp path on every exit, and on startup remove only stale service-owned task directories contained by that runtime root. Open the downloaded image read-only with Python `sqlite3` and derive schema/version, cluster epoch, counts, node inventory, receipt high-watermarks and dirty generation from that image; never mix a live pre-read with a later backup manifest. Export required encrypted application key material, hash/sign/encrypt the archive to the existing recipient, then download and verify object hash, signature and embedded image before acknowledgment. Never treat Raft directories as canonical restore. Missing capability or ambiguous/failed encryption/upload/download/verification fails closed.

- [ ] **Step 4: Preserve and prove RPO policy**

Every business mutation advances `backup_watermarks.dirty_generation` in the same cluster transaction. S2/S3/S4 each receive, outside Git, the same narrowly scoped Yandex write/read credential and a dedicated revocable backup-signing subkey (not the offline master); exact fingerprints, ownership and `0600` modes are readiness inputs. Only a capability-ready node may acquire the DB-time monotonic backup lease, so loss of S2 does not lose RPO. The single active worker compares dirty with last verified, coalesces bursts, polls on a short timer and forces at least hourly backup. It advances `verified_generation/object_digest/verified_at` only in a fenced transaction after re-download verification covers the captured dirty generation. Lost wakeups recover from the durable watermark; crash/unknown upload cannot acknowledge. Legacy `maestro-backup-onchange.path`, timer, service and shell are explicit `legacy` only and exit before JSON/file/SSH reads in rqlite mode; HA units `Conflicts=` them, and cutover stops/disables/masks the legacy path before enabling the HA timer. Enforce time retention; verified age over 60 minutes is red. Actual monthly/manual restore uses only reviewed `production-dr-override` on the hardened `maestro-ha-management` runner and CAS-publishes measured RTO/restore epoch into redacted operations status. Reports contain no credentials/private data.

- [ ] **Step 5: Implement fenced empty-cluster restore drill**

Restore only into a freshly created empty three-node test cluster after verifying signature/hashes/schema/counts. Agents/pollers are fake-fenced. After restore, one transaction sets a new `cluster_epoch` strictly greater than the manifest epoch and every recorded receipt/lease epoch; old signed commands and leases must fail. Reconcile desired state before a signed activation record enables fake agents.

- [ ] **Step 6: Run isolated restore test and commit**

Run in HA CI: `bash ops/ha/tests/create-fixture-backup.sh "$RUNNER_TEMP/maestro-dr-fixture.gpg" && bash ops/ha/ci-rqlite-cluster.sh start && bash ops/ha/restore-rqlite.sh --drill "$RUNNER_TEMP/maestro-dr-fixture.gpg"`

Run: `python -m unittest ops.ha.tests.test_verify_backup ops.ha.tests.test_backup_worker && bash -n ops/ha/backup-rqlite.sh ops/ha/restore-rqlite.sh`

```bash
git add ops/ha .github/workflows/ha-dr-drill.yml deploy/ha/maestro-backup.service deploy/ha/maestro-backup.timer deploy/maestro-backup-onchange.path deploy/maestro-backup.service deploy/maestro-backup.timer deploy/maestro-backup.sh docs/runbook-ha-restore.md
git commit -m "feat(ha): add authenticated rqlite disaster recovery"
```

### Task 15: GitHub safety policy, immutable artifacts and hardened node templates

**Files:**
- Create: `ops/test_github_workflow_policy.py`
- Modify: `.github/workflows/ha-control-plane.yml`
- Create: `.github/workflows/ha-build.yml`
- Create: `.github/workflows/ha-deploy.yml`
- Create: `deploy/ha/maestro-ha-runner.service`
- Create: `docs/runbook-ha-management-runner.md`
- Create: `ops/ha/pki-verify.py`
- Create: `ops/ha/tests/test_pki_verify.py`
- Create: `deploy/ha/rqlited@.service`
- Create: `deploy/ha/rqlite-s2.env.example`
- Create: `deploy/ha/rqlite-s3.env.example`
- Create: `deploy/ha/rqlite-s4.env.example`
- Create: `deploy/ha/maestro-panel.service`
- Create: `deploy/ha/maestro-agent.service`
- Create: `deploy/ha/nginx-maestro-panel.conf`
- Create: `deploy/ha/firewall-contract.md`
- Create: `backend/internal/api/probe.go`
- Create: `backend/internal/api/probe_test.go`
- Create: `ops/ha/tests/test_nginx_contract.py`
- Create: `deploy/ha/README.md`
- Create: `ops/ha/deploy-node.sh`
- Create: `ops/ha/tests/test_deploy_node.py`
- Modify: `backend/README.md`
- Modify: `deploy/DEPLOY.md`
- Modify: `ops/README.md`

**Interfaces:**
- Produces: exact-SHA build manifest for panel/agent/bot/importer; protected manual deploy workflow; mTLS profile verifier; dry-run deployment plan; non-secret systemd/nginx/firewall templates.
- Consumes: reviewed Git commit and GitHub artifact digest; never builds a dirty checkout on a server.

- [ ] **Step 1: Write repository policy tests first**

Parse every HA workflow and fail unless marketplace actions use full 40-hex SHA, top/job permissions are explicit, timeouts exist and test jobs unset `MAESTRO_S2_PASS`/`MAESTRO_HY2_PASS`. For a job containing production secrets require an exact environment class, `concurrency.cancel-in-progress: false`, no `pull_request` execution and no artifact containing key/credential paths. Scheduled automatic environments are branch-restricted to protected `main`, have no per-run required reviewer and may be referenced only by the exact scheduled workflow/ref. Manual override/deploy/DR environments require owner reviewer(s) and may never be used by a scheduled job. Policy tests also require every SpaceWeb-mutating DNS/TTL/alias/ACME workflow to use the literal shared concurrency group `maestro-spaceweb-dns-mutations`.

Use the approved pins: checkout `11d5960a326750d5838078e36cf38b85af677262`, setup-go `40f1582b2485089dde7abd97c1529aa768e1baff`, upload-artifact `ea165f8d65b6e75b540449e92b4886f43607fa02`, download-artifact `d3f86a106a0bac45b974a628896c90dbdf5c8093`.

- [ ] **Step 2: Run and verify RED**

Run: `python -m unittest ops.test_github_workflow_policy`

Expected: FAIL until HA workflows/templates meet the policy.

- [ ] **Step 3: Build immutable non-production artifacts**

`ha-build.yml` builds `maestro-panel`, `maestro-agent`, `maestro-bot`, `maestro-import` with `-trimpath` after unit/race/3-node tests. The manifest binds repository, branch, full commit, Go version, binary SHA-256/size and workflow run ID. Upload only binaries/manifests, never `.env`, keys, snapshots or production data. `ha-deploy.yml` is manual-only, exact-artifact, `production-control-plane`, one-node-at-a-time and non-overlapping. Its apply job must run on `[self-hosted,linux,x64,maestro-ha-management]`, fails when runner identity/source is not an exact inventoried S2/S3/S4 management peer, and never runs on GitHub-hosted infrastructure. It cannot apply until Step 7 settings and later owner approval are verified.

- [ ] **Step 4: Add least-privilege service/config templates**

`rqlited@` runs as dedicated `rqlite`, with stable node ID, `-fk` on every voter, separate HTTP/Raft mTLS, state directory and no wildcard public exposure. Read-only inventory selects fixed reachable management addresses: private/overlay when present, otherwise exact S2/S3/S4 public peer IPs protected by mTLS/source allowlist. Empty S2 initializes epoch 0, empty S3/S4 join by exact IDs, then three voters/leader/strong write and `PRAGMA foreign_keys=1` through every node are proven before migrations. `pki-verify.py` requires separate CA/SAN/EKU profiles for rqlite HTTP, Raft, dispatcher, bot gateway, lease-verifier proxy, node status and GitHub probe; private keys stay outside Git. A dedicated Actions runner service on each S2/S3/S4 uses the restricted repository runner group/label `maestro-ha-management`, accepts only protected-main deploy/TLS/DR workflows, never PR jobs, runs as an unprivileged user with ephemeral work cleanup and reaches peers only through existing allowlisted mTLS/SSH management paths. Policy tests fail if a management job uses a hosted label, an unapproved workflow/ref or an unverified runner peer identity. Panel/agent units use dedicated users, `NoNewPrivileges`, `PrivateTmp`, restrictive `UMask`, bounded restarts and explicit writable paths. nginx `:8911` exposes normal panel/client routes without requiring a client cert, but public requests to original `/readyz/read` and `/readyz/write` are denied before the app. It configures optional probe CA verification and requires workflow-probe SAN for `/readyz/probe/read`, `/readyz/probe/write`, `/internal/probe/status`, `/internal/probe/failover-state` and `/internal/probe/operations-state`. Status/failover/operations writes are nonce-bound CASes by service identity; TLS fingerprint/expiry and measured DR RTO/restore epoch feed the redacted owner dashboard. GitHub-hosted jobs never reach private rqlite/agent endpoints. Sensitive locations use a log format excluding `$request_uri`, query, headers/body; canary tests prove absence. Firewall permits rqlite, agent and bot management only among exact S2/S3/S4 peers. Returned S1 gets only dispatcher ingress and outbound quorum-backed lease-verifier proxy after new identity/incarnation; old identity fails closed.

- [ ] **Step 5: Add runtime preflight and rollback boundary**

The deploy helper verifies OS/arch/disk/time sync, exact artifact digest, rqlite archive SHA-256, CA/SAN/EKU, node ID/incarnation, membership, ports, config syntax and current readiness. It installs one candidate at a time into a versioned directory, atomically switches a symlink and keeps last-good binary/config. It does not bootstrap/join/start/enable services or alter firewall without `--apply`, protected-environment proof, expected current state and explicit node allowlist. Binary rollback never changes rqlite data/schema/epoch.

- [ ] **Step 6: Verify templates and commit**

Run: `python -m unittest ops.test_github_workflow_policy ops.ha.tests.test_deploy_node ops.ha.tests.test_nginx_contract && cd backend && go test ./internal/api -run 'TestProbe|TestSensitiveRouteLogs|TestReturnedS1LeaseVerifier' -count=1`

Run: `bash -n ops/ha/deploy-node.sh`

Run in Linux CI: `systemd-analyze verify deploy/ha/*.service deploy/ha/*.timer`

```bash
git add .github/workflows/ha-control-plane.yml .github/workflows/ha-build.yml .github/workflows/ha-deploy.yml ops deploy/ha backend/internal/api/probe.go backend/internal/api/probe_test.go backend/README.md deploy/DEPLOY.md
git commit -m "build(ha): add immutable artifacts and hardened templates"
```

- [ ] **Step 7: Record external GitHub settings as a blocking gate**

Read-only verify protected `main`; automatic environments `production-dns-auto` and `production-tls-auto` restricted to that branch with no required reviewer; manual `production-control-plane`, `production-dns-override`, `production-tls-override`, `production-dr-override` with owner reviewer(s); and runner group `maestro-ha-management` restricted to this repository and exact protected workflows. Verify automatic jobs cannot reference manual environments, management jobs cannot land on hosted/unknown runners, and manual dispatch cannot reference automatic secrets. Creating/changing settings or enrolling runners is an external mutation requiring separate owner approval. Until every restriction and shared SpaceWeb lock is verified, apply jobs fail before secrets and cutover is NO-GO.

### Task 16: SpaceWeb active-only DNS failover, dry-run first

**Files:**
- Create: `ops/ha/spaceweb_dns.py`
- Create: `ops/ha/failover.py`
- Create: `ops/ha/tests/test_spaceweb_dns.py`
- Create: `ops/ha/tests/test_failover.py`
- Create: `ops/ha/public-inventory.json`
- Create: `.github/workflows/ha-dns-failover.yml`
- Create: `docs/runbook-ha-dns.md`

**Interfaces:**
- Produces: SpaceWeb `info`/`editMain` JSON-RPC adapter; hysteresis decision; redacted dry-run/apply report.
- Consumes: `POST https://api.sweb.ru/domains/dns`, protected `SPACEWEB_TOKEN`, cluster-backed failover state and quorum-signed candidate status.

- [ ] **Step 1: Write fixture tests for the exact mutation boundary**

Mock JSON-RPC and require `Authorization: Bearer` only at the official endpoint. Inventory the complete apex answer/control set and reject any AAAA, CNAME, ANAME, ALIAS/provider flattening, wildcard shadow or multiple/missing A record as NO-GO; an unmanaged IPv6/flattened route must never bypass the active A. A mutation target must be exactly one of `{85.137.166.237,46.30.42.151,89.125.19.95}`. The current source may additionally be inventoried S1 `194.48.141.106` only for the one-time frozen pre-activation transition. Reject unready candidate, no quorum, stale signed status, unexpected read-before-write value and secret/token/header/body in errors/reports.

- [ ] **Step 2: Write hysteresis/failure tests**

Use the authenticated public probe API's failover-state CAS, keyed by cluster state version, to retain active target, consecutive failures/successes, last change, irreversible cutover marker and cooldown; the runner never reads/writes rqlite directly. Require at least three scheduled active failures plus three consecutive candidate successes; failback requires six successes and a longer cooldown. Simulate delayed/missed schedules, conflicting run, stale CAS, post-write mismatch, ambiguous provider response and rollback failure. No quorum always returns `NO_CHANGE` and does not reset counters from workflow-local state.

- [ ] **Step 3: Run and verify RED**

Run: `python -m unittest ops.ha.tests.test_spaceweb_dns ops.ha.tests.test_failover`

Expected: FAIL because clients/state machine are absent.

- [ ] **Step 4: Implement dry-run as the default**

`spaceweb_dns.py` first calls `info`, normalizes every apex address/alias mechanism and emits a redacted plan. Mutation requires `--apply`, expected state version/current IP, target HA IP, signed decision ID and environment proof. Rollback policy is epoch-bound: before the irreversible first-business-write marker, S1 is an allowed rollback target only while a global write freeze and hard fence are strongly proven; after that marker the rollback allowlist contains only currently write-ready S2/S3/S4. After `editMain`, bounded readback may retry the same idempotent decision. For the initial S1-to-HA cutover, mismatch/timeout/ambiguous response after the marker means freeze, NO-GO and manual recovery among HA targets; it must never write S1 automatically. Tests `TestInitialCutoverAmbiguityNeverRestoresS1AfterMarker`, `TestPostMarkerRollbackAllowlistContainsOnlyReadyHA` and `TestPreMarkerS1RollbackRequiresFreeze` cover the boundary. Never manufacture a second A. TTL/alias changes are staged operations under the shared SpaceWeb lock with provider-minimum discovery and one previous-TTL wait.

- [ ] **Step 5: Combine black-box and quorum-signed health**

A candidate is eligible only when direct-IP TLS with SNI `wapmixx.ru`, mTLS `/readyz/probe/read`, `/readyz/probe/write`, redacted nonce status, tariff/approved OTA fixture and a secret canary `/sub` all pass. Canary URLs are never echoed into commands, summaries or logs. The workflow verifies a fresh quorum-backed status through this public probe channel and CASes decision state through the same API; private rqlite/agent addresses are unreachable. The active may be declared failed only by the same bounded probes. GitHub delay + detection + old TTL/cache + client retry is reported as measured best-effort RTO, never instant failover.

- [ ] **Step 6: Add protected scheduled/manual workflow**

Use `schedule: '*/5 * * * *'` and a separate manual override job, with no PR/push apply. The scheduled job requires exact protected `main`, `environment: production-dns-auto` (no required reviewer), mTLS probe identity, full-SHA checkout and short timeout. Manual dispatch uses only `production-dns-override` with required reviewer. Both jobs and every other SpaceWeb DNS/TTL/alias/ACME mutation use exactly `concurrency.group: maestro-spaceweb-dns-mutations` with `cancel-in-progress: false`. The workflow produces only a redacted decision summary and may apply only after Task 15 environment policy is externally verified.

- [ ] **Step 7: Run tests/policy and commit**

Run: `python -m unittest ops.ha.tests.test_spaceweb_dns ops.ha.tests.test_failover ops.test_github_workflow_policy`

```bash
git add ops/ha/spaceweb_dns.py ops/ha/failover.py ops/ha/public-inventory.json ops/ha/tests .github/workflows/ha-dns-failover.yml docs/runbook-ha-dns.md
git commit -m "feat(ha): add guarded SpaceWeb DNS failover"
```

### Task 17: DNS-01 TLS renewal and atomic candidate installation

**Files:**
- Create: `ops/ha/tls_dns_hook.py`
- Create: `ops/ha/tls_install.py`
- Create: `ops/ha/tests/test_tls_dns_hook.py`
- Create: `ops/ha/tests/test_tls_install.py`
- Create: `.github/workflows/ha-tls-renewal.yml`
- Create: `docs/runbook-ha-tls.md`

**Interfaces:**
- Produces: exact TXT create/propagate/cleanup hooks and validate/stage/install/rollback flow for S2/S3/S4.
- Consumes: protected SpaceWeb/ACME/deployment credentials; certificate exactly for `wapmixx.ru`.

- [ ] **Step 1: Write DNS-01 hook tests**

Allow only `_acme-challenge.wapmixx.ru` TXT and the exact challenge value. Require read-before-write, propagation through at least two independent recursive resolvers, bounded timeout, cleanup of only the created value and redacted output. Every create/cleanup holds `maestro-spaceweb-dns-mutations`, so it cannot overlap failover, apex TTL or S1 alias mutations. A failed cleanup makes the workflow red and records a safe manual-cleanup instruction without the token.

- [ ] **Step 2: Write certificate installation tests**

Validate SAN hostname, validity window, full chain, private-key match, minimum remaining lifetime and exact PEM modes. Stage on one candidate, run nginx config test, atomically install, reload once, then verify direct-IP TLS with SNI and `/livez`. On any failure restore last-good key/fullchain/config and reload once. Never place the private key in GitHub artifacts, cache, command arguments or logs.

- [ ] **Step 3: Run and verify RED**

Run: `python -m unittest ops.ha.tests.test_tls_dns_hook ops.ha.tests.test_tls_install`

Expected: FAIL because hook/installer are absent.

- [ ] **Step 4: Implement DNS-01 and sequential distribution**

Run certbot/ACME DNS-01 only on `[self-hosted,linux,x64,maestro-ha-management]` after verifying the runner's exact S2/S3/S4 peer identity; policy rejects a hosted runner. Pass SpaceWeb token only through process environment to the redacting client. Install locally when selected node is target and distribute sequentially to the other exact peers over the already allowlisted authenticated management channel, never through an artifact. Prove each candidate before moving on, then CAS-publish only public certificate fingerprint/expiry to operations status. Failure leaves every last-good certificate untouched and alerts before the safety window.

- [ ] **Step 5: Add protected renewal workflow**

Split the weekly scheduled and manual jobs. Scheduled renewal runs only from exact protected `main` in `production-tls-auto` without per-run reviewer; manual retry/override uses only reviewed `production-tls-override`. Both require `[self-hosted,linux,x64,maestro-ha-management]`, verified runner peer identity, `contents: read`, full-SHA actions, bounded timeout and literal `concurrency.group: maestro-spaceweb-dns-mutations`, `cancel-in-progress: false`. A PR runs fixture tests only on GitHub-hosted infrastructure and never sees secrets; protected jobs emit only hostname, public certificate fingerprint/expiry and per-node success/rollback state.

- [ ] **Step 6: Run policy/tests and commit**

Run: `python -m unittest ops.ha.tests.test_tls_dns_hook ops.ha.tests.test_tls_install ops.test_github_workflow_policy`

```bash
git add ops/ha/tls_dns_hook.py ops/ha/tls_install.py ops/ha/tests .github/workflows/ha-tls-renewal.yml docs/runbook-ha-tls.md
git commit -m "feat(ha): add DNS-01 TLS candidate renewal"
```

### Task 18: Full fault/compatibility gates and cutover/rollback/S1-return runbooks

**Files:**
- Create: `ops/ha/fault-matrix.sh`
- Create: `ops/ha/inventory.sh`
- Create: `ops/ha/fence-audit.sh`
- Create: `ops/ha/ota-parity.py`
- Create: `ops/ha/tests/test_inventory.py`
- Create: `ops/ha/tests/test_fence_audit.py`
- Create: `ops/ha/tests/test_ota_parity.py`
- Create: `docs/runbook-ha-cutover.md`
- Create: `docs/runbook-ha-rollback.md`
- Create: `docs/runbook-ha-s1-return.md`
- Modify: `docs/runbook-s1-recovery.md`
- Modify: `.github/workflows/ha-control-plane.yml`
- Modify: `CONTEXT_HANDOFF.md`

**Interfaces:**
- Produces: redacted inventory/fence evidence, repeatable 24-scenario fault matrix, immutable GO/NO-GO report and operator runbooks.
- Consumes: only fixtures in CI; production reads/changes require later approvals named in each runbook stage.

- [ ] **Step 1: Make inventory and fencing fail closed**

`inventory.sh` accepts exact explicit hosts/sources and records redacted hashes/counts for S1 JSON/orders/trials/settings/x-ui and the current apex-backed VLESS endpoint, S2 bot DB/config plus per-token getMe bot identity/offset/in-flight callbacks/paid claims, S2 Naive users inside/outside the marker, S3 olcRTC/S3-S4 x-ui, both full bot checkouts/dependency locks/schemas/systemd, every installed writer/backup unit, ports/firewall paths, complete apex A/AAAA/CNAME/ANAME/ALIAS/flattening state, TLS, key-capability fingerprints and disk. Missing/inconsistent source is a blocker, not a warning.

`fence-audit.sh` requires evidence for: S1 removed from public control-plane ingress; old panel/admin/API/agent credentials revoked; new cluster CA not trusted by old S1 identity; S1 management/SSH paths blocked; each old bot poller stopped and its final stable-bot-identity offset/callback/claim capture signed before any token rotation; old reconcilers, olcRTC scripts and legacy backup units unable to read/write; only one backup implementation enabled; sensitive-route log canaries absent. S1 merely being offline never counts. If provider control cannot prevent an old writer returning, exit NO-GO.

- [ ] **Step 2: Encode all mandatory fault scenarios**

`fault-matrix.sh` runs only against isolated rqlite/fake agents and records exact seed/SHA/results for:

1. 100 identical confirms, saved-result restart and zero secret in replay rows;
2. same idempotency key/different hash plus provider-event/receipt reuse;
3. two paid orders, confirm-vs-cancel and latest receipt generation semantics;
4. cross-bot active guard, 24-hour unclaimed expiry and retained `payment_claimed`;
5. leader kill before/after commit and transport-unknown outcome recovery;
6. one voter down versus global no-quorum 503 and bounded stale `/sub`;
7. expiry lease handoff/crash/renew race with stale-fence zero side effect;
8. bot crash at every poll/inbox/callback state and duplicate update;
9. same-bot token rotation preserving offset/fence/callbacks and one poller;
10. imported legacy/replacement callbacks and paid claims with no miss/double confirm;
11. owner/client delivery ambiguous send without duplicate business command;
12. agent stale epoch/incarnation/fence, no-quorum and crash before receipt;
13. S1-down create/renew/expire/delete and later exact catch-up/tombstones;
14. returned-S1 new lease-verifier identity succeeds while old identity is denied;
15. S2 full-snapshot validate/swap/fsync/one reload/last-good rollback;
16. Naive imported-user adoption, zero-unowned gate and unrelated byte preservation;
17. S3 olcRTC full snapshot, grant removal, health rollback and no shell/SSH;
18. importer collision, pending+credited, full/delta batch crash/resume/delete digest;
19. settings/secret AAD/RBAC/session revocation and missing-key readiness;
20. backup dirty watermark, lost wakeup, node failover, cleanup and empty restore epoch;
21. WB external action crash boundaries, stale lease and at-most-one POST per key;
22. URL variants, S1 endpoint migration, current APK fallback and log redaction;
23. DNS alternate-address rejection, hysteresis, initial ambiguity and post-marker no-S1 rollback;
24. TLS install/rollback, disk-full readiness and exact TV/API/OTA compatibility parity.

- [ ] **Step 3: Freeze current TV/API and OTA parity**

Read-only inventory first records exact installed production TV APK versionCode/versionName/size/SHA-256 and matching source/release provenance; absence of a match is NO-GO. Run that exact APK contract fixture against `/claim`, `/sub`, `/update` in legacy and HA adapters. `ota-parity.py` compares approved cluster, panel, exact GitHub Release and Yandex mirror on all four fields and has no sync/upload/delete mode. Assert zero diff for `TvEskizHome.kt`, `TvEskizSpec.kt`, all `tvm_*`, TV D-pad/focus/Back and geometry. Do not dispatch `android.yml` or publish OTA.

- [ ] **Step 4: Write the staged cutover runbook**

The runbook requires signed evidence and a human checkpoint at each boundary:

1. protect `main`, split automatic/manual environments, shared SpaceWeb lock; require GREEN exact-SHA CI/review with Critical=0 and Important=0;
2. prepare three-voter rqlite, TLS, panels, authenticated probe/lease-verifier and agents in non-public shadow/canary-only mode;
3. under the SpaceWeb lock establish exactly `s1-vless.wapmixx.ru -> 194.48.141.106`, reject alternate apex paths, retain S2/S3/S4 fallbacks and record signed provider/TTL-wait proof; S1 itself may remain down;
4. take read-only consistent inventory and full backup; run full importer dry-run until blockers=0 and shadow credential/expiry/settings/principal/bot digest diff=0;
5. begin and prove a global write freeze across legacy app API, panel/admin, both bot pollers, reconcilers, olcRTC and backup writers;
6. after stopping each old poller, capture and sign its stable getMe bot identity, final offset, pending/in-flight callbacks and paid claims; if hard-fence rotation is required, CAS old-to-new token fingerprint only after verifying the same bot identity;
7. independently prove the full fence matrix, stop/disable legacy backup units, then take the final backup and apply the crash-resumable explicit delta/import including bot capture; reconcile the linearizable final business digest;
8. while still pre-activation/frozen, run `MigrateServiceEndpoint(s1-vless, wapmixx.ru -> s1-vless.wapmixx.ru)` and record the transformed digest proving credential-byte preservation, zero newly generated apex:443 VLESS and usable S2/S3/S4 fallback for every legacy client;
9. enable new agents canary-only; prove S2 Naive adoption has zero unowned imported users/unrelated-byte drift, S3 olcRTC receipts match and no legacy shell/SSH path is callable;
10. prove canonical `/sub`, owner dashboard, mTLS probes, redacted logs, backup watermark and every node receipt without accepting public writes;
11. after a separate explicit canary approval, atomically record the irreversible cutover marker with the first live owner canary command, then execute create/renew/paid-claim/confirm through cluster truth;
12. prove one payment, one expiry delta/generation, `receipt.generation >= result_generation`, canonical `/sub`, verified backup and no duplicate bot/order side effect;
13. execute TLS/DNS dry-run and, only after separate explicit production approval, switch the single apex A to a write-ready HA target; bounded mismatch/ambiguity freezes and never restores S1 after the marker;
14. start new pollers from imported stable-identity offsets in callback-replacement mode and prove no captured paid claim/callback is missed or duplicated;
15. before general bot reopening, run an allowlisted production canary through each live bot configuration (or the same buyer sequentially after terminal completion): tariff/order, «Я оплатил», one owner claim, owner confirm, one payment/expiry/generation, query-free Maestro URL and exact Karing links; cross-bot concurrency must not create a second order/claim;
16. retire callback-replacement mode only with zero pending/in-flight imported work, gradually reopen app/panel/admin/bots, continuously observe quorum/lag/outbox/DNS/TLS/backup dashboard, and keep S1 apply fenced until its separate return runbook passes.

Any failed checkpoint preserves write-freeze and returns NO-GO. Pre-activation import/migration writes are discardable only while the legacy truth remains frozen and the irreversible marker is absent; resuming legacy then requires a signed operator checkpoint. After the marker there is no S1/JSON fallback and no improvised writer.

- [ ] **Step 5: Write rollback boundaries and S1 return**

Before the irreversible marker, old routing may return only while global write-freeze and hard fence are proven; the pre-activation cluster is discarded rather than reverse-imported. The marker is committed atomically with the first live rqlite business command. After it, JSON/SQLite is forensic stale data and DNS rollback targets are only write-ready S2/S3/S4; rollback means previous binary on the same rqlite cluster or write-freeze plus verified rqlite export. Never dual-write, import stale JSON back or automatically write apex to S1.

Returned S1 remains isolated for forensic backup; old panel/bot/reconciler/backup stay disabled. Issue a new agent identity, increment incarnation and narrow ACLs to dispatcher ingress plus outbound mTLS lease-verifier only. Send full desired snapshot+tombstones, preserve exact login/UUID/SubID/SubToken/credentials/expiry, require fresh quorum fence verification immediately before each swap, verify hashes/receipts and only then enable it as a VPN desired target. The old identity must fail every route. S1 runs no rqlite member and never becomes a voter.

- [ ] **Step 6: Run complete repository gates**

Run in `ha-control-plane.yml` on the exact pushed SHA:

```bash
cd backend
env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go test -race -count=1 ./...
env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go vet ./...
go build -trimpath ./cmd/maestro-panel ./cmd/maestro-agent ./cmd/maestro-bot ./cmd/maestro-import
cd ..
bash ops/ha/fault-matrix.sh
python -m unittest discover -s ops/ha/tests
python -m unittest ops.test_github_workflow_policy ops.test_dates_reconcile_mode_guard
```

Run the existing Android unit/compile test workflow only, verify exact SHA/artifact and zero TV protected-file diff. Do not run release/OTA or production DNS/TLS/deploy workflows.

- [ ] **Step 7: Independent review and immutable NO-GO/GO report**

Require independent code/security review with zero Critical/Important. Generate a report containing exact commit/run IDs, schema/import/shadow digests, backup signature/restore epoch, fence evidence hashes, candidate TLS fingerprints, DNS dry-run/rollback, bot-source manifest, TV/OTA parity and owner canary evidence. Before a separately approved production canary, status must remain `NO-GO (repository implementation only)` even if every fixture test is green.

- [ ] **Step 8: Commit documentation and tests**

```bash
git add ops/ha .github/workflows/ha-control-plane.yml docs/runbook-ha-cutover.md docs/runbook-ha-rollback.md docs/runbook-ha-s1-return.md docs/runbook-s1-recovery.md CONTEXT_HANDOFF.md
git commit -m "docs(ha): add verified cutover and rollback gates"
```

## Plan 04 acceptance

- Signed/encrypted backup verifies and restores into an empty three-node cluster with a strictly newer epoch.
- Durable dirty-watermark backup survives S2 loss, acknowledges only a re-downloaded verified object and conflicts/fences every legacy JSON/SSH backup unit.
- Scheduled DNS/TLS jobs use branch-restricted no-reviewer automatic environments; all manual overrides require reviewers and every SpaceWeb mutation shares one lock.
- Public automation reaches only mTLS probe/CAS routes; original readyz, rqlite, agents and bot management stay private, and sensitive URLs never enter logs or summaries.
- S1 alias/endpoint migration precedes the irreversible owner marker; after it, DNS rollback can select only write-ready S2/S3/S4 and returned S1 uses a new lease-verifier identity.
- Immutable artifacts/templates/policy tests are green; no server builds a dirty checkout.
- DNS/TLS tools are dry-run/fixture-tested, single-A and fail-closed; protected workflows cannot expose secrets to PR code.
- All 24 required fault/security/compatibility scenarios are represented in exact-SHA CI evidence.
- Full bot source, importer, shadow, hard fence, backup/restore, TLS, DNS, TV/OTA and owner canary gates are explicit and independently reviewable.
- Repository implementation alone never reports production GO and performs no production mutation.
- TV UI/assets and published OTA remain byte-for-byte untouched.
