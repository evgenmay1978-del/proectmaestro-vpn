# WDTT provider bridge and fail-closed recovery design

Date: 2026-08-15
Scope: MaestroVPN mobile WDTT runtime, the pinned native WDTT client build, and WDTT panel orchestration
Out of scope: Android TV behavior/assets, customer dates, payments, bots, DNS, other VPN protocols, SSH authentication policy, OTA publication

## Goal

Make WDTT observable and usable end to end on the owner's Android phone: MaestroVPN must either establish the complete `phone -> VK TURN -> S1 -> Internet` path or report the exact safe failure stage. A panel room/hash change must not destroy the last known-good configuration.

## Verified evidence

1. Production advertises WDTT only to the three approved mobile aliases. TV receives no WDTT payload.
2. S1 has the WDTT service, public UDP listener, internal WireGuard listener, interface, forwarding and NAT. The server has not observed a phone handshake during the failed attempts, so the current failure is before S1.
3. MaestroVPN pins native client commit `1ff024899a577cb5db4691e526614619bf5a06a3`. That client supports the VKCalls anonymous path, typed failure stages, structured events, and stdin control.
4. `WdttManager` was introduced before the pin update and only recognizes `__WDTT_EVENT__|READY|`. It merely logs every `ERROR`, stats and captcha line.
5. The native client prints `CAPTCHA_SOLVE|<mode>|<redirect>|<session>` and blocks until stdin receives `CAPTCHA_RESULT|<result>`. MaestroVPN has no parser, UI bridge or writer for this protocol.
6. MaestroVPN kills a start attempt after 30 seconds. The native fallback can wait through multiple automatic attempts, two 10-second WebView attempts and a 60-second manual attempt. The contracts are incompatible.
7. The pinned native client creates `tls-client` HTTP clients without an explicit root pool. The current VK-TURN ecosystem has reproduced Android trust failures after a VK certificate-chain change. This is a targeted risk, not yet proven as the owner's exact phone error because MaestroVPN currently hides that error.
8. The current panel immediately stores a syntactically valid hash list. It does not distinguish candidate, active and last known-good provider state.

## Rejected directions

- Do not replace the protocol with Lionheart/Wildberries TURN. Lionheart is archived after provider `denied-peer-ip` restrictions made arbitrary VPS relay nonviable.
- Do not switch wholesale to another VK-TURN core. That risks WRAP/server incompatibility and changes too many variables at once.
- Do not disable TLS verification, use `InsecureSkipVerify`, scrape browser cookies, export authenticated sessions or add a server-side browser bot.
- Do not declare success from a visible WDTT chip, active systemd service, open UDP port or a native `READY` line alone.

## Chosen architecture

### 1. Deterministic native patch boundary

Keep the reviewed upstream commit as the immutable base. Store a small Maestro-owned patch set and its tests in the repository. `wdtt-bin.yml` must:

1. checkout the exact upstream commit;
2. verify the base revision;
3. run `git apply --check` on the audited patch set;
4. apply it once;
5. run focused Go tests;
6. build both Android ABIs;
7. publish the base revision, patch-set digest and binary checksums.

Android packaging must reject an artifact when any of those provenance values differ.

The native patch adds only:

- an explicit verified CA pool that combines the available system roots with a pinned official Mozilla/curl CA bundle;
- typed stage/error events without credentials, room hashes, URLs, tokens or device identifiers;
- a deterministic provider-only probe mode used by tests and panel orchestration;
- a readiness event emitted only after at least one active carrier worker exists.

Invalid certificates must still fail. No TLS bypass is permitted.

### 2. Mobile event and control bridge

Introduce a pure parser with these externally visible states:

`STARTING`, `DNS`, `TLS`, `VK_AUTH`, `CAPTCHA_REQUIRED`, `TURN_ALLOCATED`, `DTLS`, `WRAP`, `WIREGUARD`, `READY`, `FAILED`, `STOPPED`.

The parser accepts only the exact structured prefix and the documented legacy `CAPTCHA_SOLVE` line. Unknown/malformed lines are ignored and cannot mark the tunnel ready. Error text is mapped to a fixed safe code; raw provider bodies and secrets never reach UI or logs.

`WdttManager` owns one synchronized stdin writer for the child lifetime. It can send only documented commands (`STOP`, `CAPTCHA_RESULT`). It exposes the current safe state and last safe error to the mobile ViewModel.

Start waiting becomes state-aware:

- ordinary provider stages have bounded deadlines;
- an explicit captcha request pauses the ordinary deadline and starts a bounded interactive deadline;
- process exit, malformed fatal event, user cancellation or deadline expiry stops and reaps the child;
- only typed `READY` plus a live child allows selector activation.

### 3. Mobile captcha fallback

VKCalls remains the first path. Interactive captcha is opened only when the child explicitly requests it.

A mobile-only WebView surface:

- accepts only HTTPS and the exact VK identity host allowlist;
- blocks redirects, downloads, file access and navigation outside the allowlist;
- never exposes cookies, local storage or the session token to logs or repository files;
- returns only the success result expected by the child;
- sends `CAPTCHA_RESULT|error:cancelled` or `error:timeout` on cancellation/failure;
- is unreachable on TV.

The child stays alive while this surface is displayed. Closing the VPN flow closes the WebView and the child together.

### 4. Panel candidate/active room state

The panel accepts a full VK invite or bare hash and normalizes it before validation. A change is written first as a candidate. The active/last-known-good list is not overwritten until the provider-only probe proves:

1. DNS and verified TLS;
2. VK call availability;
3. TURN credential allocation;
4. at least one usable relay address.

Probe output contains only stage, safe error code, timestamp and success/failure. It never returns credentials, hashes, provider URLs or customer data.

On success the candidate is promoted atomically. On failure the previous active list remains served to phones. Up to four active hashes may be health-scored and rotated with bounded backoff; retry storms and simultaneous probes are prevented by one lease/lock.

Panel status distinguishes `candidate`, `active`, `last-known-good`, `checking` and `failed`. The existing three-account allowlist and blank-secret-preservation behavior remain unchanged.

### 5. End-to-end readiness

Application success requires all of:

1. native carrier worker ready;
2. local relay bound at the configured address;
3. sing-box/WireGuard outbound selected;
4. S1 observes a new authenticated device/handshake count;
5. bidirectional byte counters increase;
6. a public HTTPS egress check succeeds through WDTT.

Only anonymized counts and stage results may be recorded. Failed egress keeps the release at `NO-GO` even if every earlier stage is green.

## Error handling

- `TLS_TRUST_FAILED`: stop; never retry with verification disabled.
- `VK_CALL_UNAVAILABLE`: reject the candidate and retain last known-good.
- `VK_CAPTCHA_REQUIRED`: request the mobile WebView once; no server-side solver.
- `VK_RATE_LIMITED`: bounded exponential backoff with jitter; no immediate loop.
- `TURN_ALLOCATION_FAILED`, `DTLS_FAILED`, `WRAP_AUTH_FAILED`, `WG_HANDSHAKE_FAILED`, `EGRESS_FAILED`: stop at the exact stage and preserve other protocols.
- Three consecutive failures invalidate only volatile TURN credentials, not the saved room or subscription.

## Test strategy

TDD is mandatory for every production change.

1. Go tests: CA pool, invalid-certificate rejection, safe event payloads, provider-probe stage transitions and secret-redaction markers.
2. JVM tests: event parsing, malformed input, captcha request/result, cancellation, timeout, process exit, writer synchronization and TV denial.
3. Backend tests: URL/hash normalization, candidate isolation, atomic promotion, failed-probe rollback, four-hash rotation, concurrent probe lock and redacted responses.
4. Workflow policy tests: exact base commit, exact patch digest, CA-bundle digest, both ABIs and artifact provenance.
5. Exact-SHA GitHub checks: focused tests, Android unit tests, APK assembly, backend test/race/vet, WDTT native build and secret/scope audit.
6. Physical-phone acceptance: refresh/restart visibility, connect/egress, reconnect, sleep/wake, Wi-Fi/mobile switching and server-side handshake/counter proof.

## Rollout and rollback

1. No production server change is required for the first patched-client candidate.
2. Build an artifact-only normal monotonic mobile candidate in GitHub Actions. Do not create a Release or OTA.
3. Install without deleting application data and execute the physical-phone matrix.
4. If any gate fails, keep the current production APK and server configuration; use the surfaced stage as the next single hypothesis.
5. OTA is a separate owner-authorized action only after the complete data path passes.

## Safety invariants

- TV source, resources, assets, D-pad/focus/Back and TV runtime remain unchanged and WDTT-free.
- SSH root password access and public-key access policy remain unchanged.
- No customer, payment, bot, DNS or other protocol state changes.
- No secrets, hashes, provider tokens, private subscription URLs or authenticated panel URLs in Git, CI artifacts, logs or handoffs.
- Heavy Android/native builds run in GitHub Actions, not on the owner's computer.

## Acceptance criteria

- A captcha request can be completed or cancelled without a silent hang.
- TLS verification remains enabled and works with the required current VK certificate chain.
- Every failed start identifies one safe terminal stage.
- A bad/new room cannot replace the last known-good room.
- WDTT carries real bidirectional phone traffic through S1 and survives the lifecycle matrix.
- Exact-SHA CI is green, independent review has no Critical/Important findings, TV diff is empty, and the owner accepts the physical-phone result before OTA.
