# Test results register
Status: target-only by default. The dated diagnostic CDN record below is live;
all other unlisted transport, client, subscription, metering, billing and
canary claims remain target-only.

## 2026-08-31 — live diagnostic Yandex CDN path

- Scope: existing test CDN resource and fallback diagnostic origin only. No
  service, origin, DNS, firewall, database, subscription, client or billing
  mutation was performed.
- Resource console: active resource, issued certificate, HTTP fallback origin,
  client access enabled, CDN/browser cache disabled, allowed methods
  `GET, HEAD, OPTIONS`, zero cached responses and zero 5xx responses in the
  displayed 30-day metrics.
- Local probe: GET body `29` bytes, sender/origin SHA-256 match, HTTP `200`;
  OPTIONS body `4` bytes, HTTP `204`.
- Independent S4 probe: HTTP/2 GET body `31` bytes, sender/origin SHA-256
  match, HTTP `200`; HTTP/2 OPTIONS body `4` bytes, HTTP `204`; literal shared
  edge with CDN SNI/Host, HTTP/2 `200`.
- CDN/origin evidence: `Via: Yandex-CDN`, `CDN-Loop: yandex`, forwarded host
  and protocol present; the fallback origin identified the expected S1 probe.
- Result: diagnostic CDN transport **PASS**. VLESS/XHTTP tunnel, mobile-carrier
  white-list availability, subscription import, per-user statistics and
  billing remain **NOT TESTED**. Candidate sidecar ports were not listening and
  no server or CDN mutation was attempted.

## Materialized policy
No unlisted transport/server/client/backup/billing/canary test is claimed
complete. Future records require test ID, release/checksum, isolated target,
timestamps, expectation/outcome, redacted evidence, operator and
pass/fail/inconclusive state. Local docs checks do not prove live behaviour.

## Gates and safety
Ordinary VPN, existing identity, subscription, balances, panel and TV/mobile behaviour remain non-regression boundaries. Work starts only in an isolated branch/process/config/release. Any live inventory, backup/restore, service/origin/firewall/database change, client switch, charging, OTA, reboot or deletion is a stop gate requiring explicit owner approval. Sensitive origin context is referenced by MASTER section, never repeated here.

## Evidence rule
Record source, date, redacted release/checksum and outcome before changing status. WDTT, qWDTT, CSQTT and olcRTC remain deferred.
