# Remnawave owner-only CDN trial Implementation Plan

> For agentic workers: execute the approved existing-host rollout in this task. Root is the only production/guard-ledger writer; delegated work is bounded source research and review.

**Goal:** One isolated capped owner profile on native Remnawave/stock Xray, without customer migration.

**Architecture:** S1 runs panel/PostgreSQL/Valkey; S4 runs the isolated node. Existing CDN ingress gets one separate trial path to a loopback-only node inbound. Existing commercial runtime, ordinary VPN, billing and subscriptions stay intact.

**Tech Stack:** Ubuntu 24.04 native Docker 29.1.3 + Compose 2.40.3; Remnawave backend 3.4.4, node 3.4.1, stock Xray 26.7.28, PostgreSQL 18.4, official Valkey 9-alpine image resolved to its pulled digest.

## Global constraints

- Owner explicitly confirmed this trial and then directed use of existing servers only; no new VPS purchase or tariff change.
- Low free RAM is a known trial constraint, not a production capacity pass. Initial hard caps: S1 backend 512 MiB, DB 128 MiB, Valkey 32 MiB; S4 node 256 MiB. No container swap or automatic restart loops. If these fail, stop the new component before reconsidering limits.
- Keep current S1 FORWARD ACCEPT and wdtt0 unchanged using Docker's documented ip-forward-no-drop option. Snapshot existing firewall rules and service PIDs before installation. Do not flush or wholesale restore firewall tables over later changes.
- Node has no NET_ADMIN and no host network. Its API is mTLS/JWT and restricted to S1 through Docker's forwarding filter; trial inbound host publication is 127.0.0.1:38081 only. Backend API is 127.0.0.1:38000 only; database/metrics are not publicly published.
- All credentials/configurations stay in root-only /opt/maestro-remna-trial, never Git/chat. Unique Compose resources use mvpn-remna-trial names.
- No tests beyond the owner-authorized connection/load scenario and necessary startup/configuration observations; no automatic CI test run.

## Task 1 — bounded native runtime

- [x] Confirm versions, official API/config contract, live resources and kernel cgroup capabilities.
- [x] Create `ops/remna-trial/panel.compose.yml` and `node.compose.yml` from the pinned official files, with the limits and bindings above.
- [x] Snapshot firewall/forward policies and protected service PIDs on S1/S4. Install only native Docker/Compose dependencies without unrelated upgrades or automatic restarts of existing services. Validate Docker configuration before starting any trial containers.
- [x] Pull the pinned official images; record actual image digests. Generate fresh panel DB/app/admin secrets server-side. Start S1 stack once; inspect health, actual RSS and any OOM flag. Failure stops the trial stack, not existing services.

## Task 2 — one native profile

- [x] Use loopback API with trusted proxy headers: auth/status → auth/register → keygen. Save admin credentials and node SECRET_KEY privately. Start the capped S4 node, with API source restriction installed before port publication.
- [x] Create profile, node, squad, host and one owner-trial user via native APIs; store returned IDs after each successful POST, never blindly retry POST. User limit 209715200 bytes, NO_RESET, expiry 24 hours. Use fresh trial credentials and the verified current GET/body/query XHTTP contract.
- [x] Add only the unique trial ingress location after protected config backup and syntax check; gracefully reload only ingress. Obtain native JSON and verify ML-KEM encryption did not silently become none, UUID/extra/mode match, and actual core version is 26.7.28.

## Task 3 — result and containment

- [ ] Through the actual CDN path, verify repeated Internet requests without switching, a continuous/idle distinction and one bounded download over 16 MiB. Inspect native counters/quota, trial process continuity and protected existing PIDs. A target website's 503 is not a transport verdict.
- [ ] Provide only the private owner profile when usable; distinguish server-client evidence from INCY/device confirmation. No customer cutover on this approval.
- [ ] Record actual results and rollback. Stop/remove only trial containers/path on failure; preserve existing data and logs. Docker/package removal is not required for rollback. Update both handoffs and push a redacted skip-CI checkpoint.

Sources: https://github.com/remnawave/backend/tree/3.4.4 ; https://github.com/remnawave/node/tree/3.4.1 ; https://docs.docker.com/engine/network/packet-filtering-firewalls/ .
