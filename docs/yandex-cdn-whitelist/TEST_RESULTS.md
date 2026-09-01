# Test results register
Status: target-only by default. The two dated CDN records below are live;
all other unlisted transport, client, subscription, metering, billing and
canary claims remain target-only.

## 2026-08-31/2026-09-01 — isolated XHTTP first-canary

- Test IDs: `T-XHTTP-DIRECT`, `T-XHTTP-CDN`, `T-XHTTP-USER-UNRESTRICTED`,
  `T-XHTTP-USER-WHITELIST-PENDING`.
- Repository checkpoints: lifecycle/recovery
  `819e11ada7f14f4aa6b3bcdbf8cf8cc2e2fd746b`, operator CLI
  `713eb7e0724cd075bfab72420742ffc18dc389a7`, exact-SHA CI policy
  `445b8fa682d128cabd03576050be936b33e658ec`, final Linux CI corrections
  `fcde1cd35c5e96ea99c8afe98ea41c74437c6bac`. Independent final reviews:
  `0 Critical / 0 Important` for every current code scope. Exact-SHA GitHub
  evidence for `fcde1cd35c5e96ea99c8afe98ea41c74437c6bac` completed with `success`:
  `HA immutable panel artifact` run `33464964316`, `Yandex CDN isolated release
  checks` run `33464964334`, `HA DR restore drill` run `33464964361`, `HA
  control-plane checks` run `33464964363`, and `HA S4 network change package`
  run `33464964368`. This closes the repository/CI gate only; the operator
  mobile white-list test remains **PENDING**, and customer-product readiness is
  not claimed.
- Isolated target: a separate Xray `26.7.28` sidecar behind the existing test
  CDN resource. Ordinary x-ui/Xray TCP/UDP `443` and customer traffic were not
  changed. Raw hostname, address, UUID and VLESS Encryption material remain
  outside Git.
- Server result: direct tunnel **PASS** and Yandex CDN tunnel **PASS**.
  Protected evidence run: `20260831T213659Z`.
- Client result without an active mobile white-list: owner-reported connection
  **PASS**, corroborated by the isolated per-user counter increasing by `5844`
  bytes (`8704 -> 14548`). Protected client validation log SHA-256:
  `23842965f2b14df2c0185636d5699ce83e31d8fb8755692f4058657d4c1119ce`.
- Client result with the operator mobile white-list enabled: **PENDING** because
  no white-list was active during the test. At the owner's request, the test
  canary and temporary client key remain active through the morning family
  test. Rotation/removal is deferred until the owner reports the test window
  complete.
- Client compatibility: the tested URI is for a modern Xray client. Production
  MaestroVPN Android/TV `1.0.157` uses pinned sing-box/libbox without XHTTP and
  ML-KEM VLESS Encryption and cannot consume this canary. A current
  `/sub/<token>` wrapper alone does not add that runtime support.
- Recovery contract: restore the external Yandex diagnostic origin first, then
  run local CLI rollback, which verifies that restore. The CLI does not mutate
  Yandex Cloud.
- Durable entitlement identity `wl:<entitlementID>` and the additive white-list
  renderer exist in repository code. Production `/sub/<token>` wiring, durable
  profile/preset/release/edge credential binding, production statistics,
  runtime metering, GB billing, panel projection, Telegram UX, fleet rollout
  and customer cutover remain **NOT IMPLEMENTED / NOT TESTED**.

## 2026-08-31 — live diagnostic Yandex CDN path

- Test IDs: `T-CDN-LOCAL-GET`, `T-CDN-LOCAL-OPTIONS`,
  `T-CDN-S4-H2-GET`, `T-CDN-S4-H2-OPTIONS`, `T-CDN-S4-LITERAL-EDGE`.
- Operator/time: Codex,
  `2026-08-31T18:14:56Z..2026-08-31T18:15:09Z`.
- Repository checkpoint: `d31c36d546ef4e15da6a6a5c829092a3f2f7c981`.
  Protected `EVIDENCE_MANIFEST.json` SHA-256:
  `f50546782b1a548a7d156ed33fada9c0dbd9888f63a3ee2873183c7ee9558416`.
  Raw operational hostname/IP evidence is intentionally outside Git.
- Scope: existing test CDN resource and fallback diagnostic origin only. No
  service, origin, DNS, firewall, database, subscription, client or billing
  mutation was performed.
- Resource console: active resource, issued certificate, HTTP fallback origin,
  client access enabled, CDN/browser cache disabled, allowed methods
  `GET, HEAD, OPTIONS`, zero cached responses and zero 5xx responses in the
  displayed 30-day metrics.
- Local probe: GET body `34` bytes, sender/origin SHA-256 match, HTTP `200`;
  OPTIONS body `4` bytes, HTTP `204`.
- Independent S4 probe: HTTP/2 GET body `31` bytes, sender/origin SHA-256
  match, HTTP `200`; HTTP/2 OPTIONS body `4` bytes, HTTP `204`; literal shared
  edge with CDN SNI/Host, HTTP/2 `200`.
- CDN/origin evidence: `Via: Yandex-CDN`, `CDN-Loop: yandex`, forwarded host
  and protocol present; the fallback origin identified the expected S1 probe.
- Historical result at that checkpoint: diagnostic CDN transport **PASS**;
  VLESS/XHTTP tunnel, mobile-carrier white-list availability, subscription
  import, per-user statistics and billing were **NOT TESTED**. Candidate
  sidecar ports were not listening and no server or CDN mutation was attempted.
  The later first-canary record above supersedes this historical current-state
  snapshot.

## Materialized policy
No unlisted transport/server/client/backup/billing/canary test is claimed
complete. Future records require test ID, release/checksum, isolated target,
timestamps, expectation/outcome, redacted evidence, operator and
pass/fail/inconclusive state. Local docs checks do not prove live behaviour.

## Gates and safety
Ordinary VPN, existing identity, subscription, balances, panel and TV/mobile behaviour remain non-regression boundaries. Work starts only in an isolated branch/process/config/release. Any live inventory, backup/restore, service/origin/firewall/database change, client switch, charging, OTA, reboot or deletion is a stop gate requiring explicit owner approval. Sensitive origin context is referenced by MASTER section, never repeated here.

## Evidence rule
Record source, date, redacted release/checksum and outcome before changing status. WDTT, qWDTT, CSQTT and olcRTC remain deferred.
