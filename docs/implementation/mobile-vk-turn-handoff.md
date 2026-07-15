# Mobile VK TURN / WDTT integration — in-flight handoff

Updated: 2026-07-15. Status: application/backend/build implementation completed in the working
tree; CI and real transport canary remain. **Not deployed**, no OTA, no live services changed.

## Owner-approved scope

- Mobile Android only. Android TV must never receive, display, cache, or start WDTT.
- Exact login allowlist: `wapmix`, `wapmixx`, `wapmix2`.
- Delivery must be a normal in-place MaestroVPN OTA; no manual reinstall.
- Manual selector fallback only; never add to the `auto` urltest pool.
- Existing clients and protocols must remain untouched until isolated phone E2E proof and explicit
  owner approval for release/deploy.

## Architecture decision

Pinned upstream: `amurcanov/proxy-turn-vk-android` commit
`8b26530dfe90ff9b6aa3880ba2c1f070e21e2d3a` (GPL-3.0).

The upstream `libclient.so` is a standalone Go executable, not JNI. It listens on local UDP
`127.0.0.1:9000`, obtains VK Calls TURN credentials, creates TURN/DTLS/WRAP sessions to the WDTT
server, and forwards WireGuard UDP packets. Upstream Kotlin then starts its own WireGuard
`VpnService`; Maestro must **not** do that. Maestro keeps its single libbox `VPNService` and emits a
sing-box WireGuard outbound whose endpoint is `127.0.0.1:9000`. `WdttManager` only owns the child
process.

Server defaults: WDTT/DTLS `56000/udp`, internal userspace WG `56001/udp`, network
`10.66.66.0/24`, MTU 1280. Client control is stdin/stdout; `WDTT_EVENTS=1` enables structured
`__WDTT_EVENT__|...` lines. The server's generated-password limit is 10, sufficient for the three
approved logins but not a future fleet rollout. It has no SIGHUP config reload; external password
changes require a future API/reload patch. Never use the master password in the APK.

## Safety gates (all required)

1. `MaestroSub.withDevice()` appends `platform=mobile|tv` using the system form factor.
2. Backend eligibility requires: enabled + fully configured + explicit `platform=mobile` + SFA
   version at/above the configured minimum + active account + exact case-sensitive login allowlist.
3. Old clients and requests with missing/unknown platform fail closed.
4. `WdttManager.ensureStarted()` must reject TV immediately before process spawn; UI hiding is not
   considered a safety boundary.
5. Feature remains disabled when its JSON config, native binary, server, or any credential is
   absent.

## Critical unresolved verification

The WDTT child is a separate process and cannot directly call Maestro's `VpnService.protect()`.
Its VK API/TURN sockets are captured by the app TUN unless carrier routes are forced DIRECT.
Before any canary is called working, verify on a real phone from route logs that VK signalling,
DNS, and every selected TURN relay IP bypass the `vk-turn` outbound. A routing loop here is the
primary integration risk.

## Anti-loop hardening (2026-07-15 follow-up)

- `WdttVpnPolicy` detects the structured top-level `vk-turn` outbound instead of matching raw text.
- When a mobile VPN profile contains `vk-turn`, `BoxService` excludes MaestroVPN's own Android
  package/UID from the TUN. This lets the standalone WDTT child reach VK signalling, DNS and TURN
  using the underlying network even though it cannot call `VpnService.protect()` itself.
- Existing per-app include/exclude semantics are preserved for every profile without `vk-turn`;
  TV is still hard-gated and never receives this override.
- Conditional DIRECT rules cover the VK/OK carrier domains, known VK address ranges and bootstrap
  DNS addresses. Real-phone route logs must still prove every dynamically selected relay bypasses
  the tunnel before the canary can be called working.
- Verification after this follow-up: full backend `go test ./...`, Android
  `testDebugUnitTest`, and `git diff --check` all pass locally.

## Build/delivery

`version.properties` pins `WDTT_REF` and `WDTT_GO_VERSION`. `.github/workflows/wdtt-bin.yml`
checks out that exact commit and builds Android PIE executables for arm64-v8a and armeabi-v7a.
Artifacts contain binaries, SHA-256 manifest, and upstream commit marker. Android workflows verify
the marker and checksums before placing `libwdtt.so` into `jniLibs`.

## Required completion sequence

1. Finish config/subgen/app selection integration and all fail-closed tests.
2. Create a feature branch, run backend Go tests and Android unit/compile CI. The `codex` user has
   verified write access to `.git`; GitHub CLI authorization is still required before push/PR.
3. Build WDTT server from the same pin and deploy only to an isolated canary host/service after a
   backup/rollback plan. Do not touch existing S1/S2/S3 VPN processes.
4. Provision unique passwords/WG peers for the three approved logins.
5. Install the test artifact on a **phone**, prove TV absence separately, then prove DTLS, WG
   handshake, public egress, reconnect, sleep/wake, Wi-Fi/LTE switch, and no routing loop.
6. Field-test in a VK-whitelist region. Only after evidence and owner approval: deploy backend,
   then merge/release OTA and verify update over the existing installed app.

## Current working-tree result

- Mobile/TV marker and migration for already-installed profiles: `DeviceFormFactor`, `MaestroSub`,
  `Application`. The `/info` suffix is inserted before query parameters, fixing the historical
  malformed `?device=.../info` pattern for the new platform gate.
- Fail-closed backend: `backend/internal/{api,vkturnconf,subgen}`. `/sub` emits `vk-turn` only for
  an eligible mobile request; `/info` returns child parameters. Config file must contain exactly
  `wapmix`, `wapmixx`, `wapmix2`.
- App runtime: `WdttManager` (mobile + API 28 hard gates, exact upstream argv, structured READY,
  cache validation, orphan cleanup); selection/watchdog/stop wiring in `GroupsViewModel` and
  `BoxService`; TV filtered both in navigation and TV home rendering.
- Build: pinned WDTT client + Linux server artifacts with SHA-256 verification; release/test APK
  workflows fetch the pinned client as `libwdtt.so`.
- Tests passed locally: `go test ./internal/subgen ./internal/vkturnconf`; `git diff --check`; YAML
  parse for all touched workflows; JSON parse for the example config. Full API tests require the
  unavailable `x/crypto` module; Android compile/tests require the unavailable Gradle distribution.
- Graphify updated from this working tree and query `WdttManager VKTurn vkturnconf mobile platform
  GroupsViewModel` resolves the new runtime chain.

## Environment blockers recorded for continuation

- Verified 2026-07-15: `/srv/maestrovpn-tv` and `.git` are owned by `codex:codex`, and the `codex`
  user has write access. Do not request `chown`; local branch and commit are available now.
- `gh auth status` reports no authenticated GitHub CLI session. Per publish workflow, do not push
  or create a draft PR until authentication is restored. The connected GitHub reader was used only
  to audit upstream sources.
- Do not treat this as shipped or working: no `wdtt-server`, VK room hash, per-login password/WG
  identity, test APK, phone installation, DTLS handshake, public egress, or whitelist-region proof
  exists yet.

## GitHub authorization update

- Owner completed GitHub CLI device authorization from mobile on 2026-07-15; host-side
  `sudo -u codex -H gh auth status` showed account `evgenmay1978-del` with `repo` and `workflow`
  scopes. Never copy the resulting token into chat or project files.
- The current Telegram Codex sandbox still mounts `.git` read-only, so the anti-loop follow-up
  cannot be committed from this already-running session. Commit/push must be performed from a new
  writable Codex session or the owner's root terminal while executing Git as user `codex` from
  `/srv/maestrovpn-tv`.
