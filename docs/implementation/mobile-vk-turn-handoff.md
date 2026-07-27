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

## Isolated server placement assessment (2026-07-15)

- Selected S1 for the first WDTT canary: it has 3 CPU cores and about 9.9 GiB disk free, while S3
  has only one core and S2 carries the denser Hy2/Caddy/AnyTLS/bot stack. The canary must use its
  own directory, process, UDP ports and strict CPU/RAM limits; no existing service may be edited or
  restarted.
- Live S1 snapshot before any change: load `0.40/0.27/0.37`, 722 MiB available RAM, 9.9 GiB free.
- The mandatory root-request script is prepared at
  `/home/codex/maestro-context/root-request/draft.sh` (SHA-256
  `a33c803b95f23a9e7ab191d3faddadae8eadd8b7356d07cd999c8eff56076dc8`). It only installs and
  statically inspects the pinned artifact; it intentionally creates no service/listener and changes
  no firewall/backend/subscription state. It has not been approved or executed.
- No production change occurred. The owner must separately approve the exact broker request before
  the artifact is staged.

## Telegram root-broker delivery failure (2026-07-15 09:20 UTC)

- Owner confirmed that no **Разрешить / Отклонить** buttons were delivered. The stage directory,
  WDTT process, and UDP 56000/56001 listeners remain absent; staging did not run.
- The canonical script was recovered and syntax-checked at `/tmp/wdtt-root-request-draft.sh`; its
  SHA-256 is still `a33c803b95f23a9e7ab191d3faddadae8eadd8b7356d07cd999c8eff56076dc8`.
- Current managed Telegram Codex turns mount `/home/codex/maestro-context/root-request/` read-only.
  Re-submission fails with `Read-only file system`, so this turn cannot trigger the broker button.
  Do not claim the request is pending merely because a prior watched draft disappeared.
- Required infrastructure fix: allow the `codex` sandbox to write only the broker inbox directory,
  then copy the exact canonical script there and verify that the owner actually receives buttons.

## Owner approval policy for Telegram Codex (2026-07-15)

- Owner wants routine development to proceed autonomously: repository edits, tests, builds, CI,
  commits/pushes on feature branches, read-only diagnostics, memory and graph maintenance.
- Ask in this Telegram chat only for materially serious operations: root execution, production
  deploy/restart/configuration, OTA/main release, destructive cleanup, credential/security changes,
  or anything that can interrupt paying clients. State risk and rollback before asking.
- The bridge should run Codex as the unprivileged `codex` OS user with normal filesystem/network
  access for its owned development and context paths. Root remains unavailable except through the
  hash-bound **Разрешить / Отклонить** broker. Do not grant Codex a root shell or weaken the broker.
- First phone-terminal remediation attempt was corrupted by wrapped/pasted prose and shell text;
  terminal returned `command not found`. No bridge change is verified. Continue with short,
  one-command diagnostics before issuing any further mutation.
- Read-only grep then found neither `danger-full-access` nor
  `dangerously-bypass-approvals-and-sandbox` in `bridge_bot.py`; the launch arguments are formed
  elsewhere or indirectly. No mutation occurred. Inspect the actual Codex/runuser construction
  before designing the patch.
- At 2026-07-15 10:11 UTC the managed sandbox was corrected to make only the broker inbox writable.
  Codex itself re-submitted the canonical stage-only script to
  `/home/codex/maestro-context/root-request/draft.sh`; syntax check passed and SHA-256 is the
  expected `a33c803b95f23a9e7ab191d3faddadae8eadd8b7356d07cd999c8eff56076dc8`.
  This submission is not execution: wait for the actual broker buttons and verify the root audit
  result before claiming staging occurred.
- Owner approved request `a33c803b95f2`; it failed safely before installation because root's
  `mktemp` directory was mode `0700`, so the deliberately unprivileged `codex` GitHub downloader
  could not traverse it (`permission denied` extracting `WDTT_UPSTREAM_COMMIT`). No staged directory
  or service/listener was created.
- Corrected the script by assigning only the ephemeral download directory to `codex` after identity
  prechecks. Syntax and secret checks passed; resubmitted SHA-256
  `f9ac6cbfcfc0d54bf2ca47855fc811eb5be9b3afcc57a68154cfcc7c4f3cc01b` at 10:14 UTC.
- Owner approved `f9ac6cbfcfc0`; root audit returned code 0. Both Android binaries and
  `wdtt-server` passed the artifact manifest, the staged pin is
  `8b26530dfe90ff9b6aa3880ba2c1f070e21e2d3a`, and an independent staged checksum check passed.
  `/opt/maestro-wdtt-canary/wdtt-server` exists, but no WDTT process and no UDP 56000/56001
  listener exist. Stage-only gate is complete; production/backend/OTA/VPN services were untouched.
- Binary help confirms activation flags: `-config-dir` (default `/etc/wdtt`), `-listen` (default
  `0.0.0.0:56000`), `-wg-port` (default `56001`), `-password`, plus optional Telegram bot/admin
  flags. Next gate is a separately approved isolated activation with generated credentials,
  resource limits, startup verification, and automatic rollback, followed by real-phone E2E.

## Disabled service preparation request (2026-07-15)

- Reverified the staged executable and checksum; it remains stopped. No UDP 56000/56001 listener
  was observed from the host checks available before submitting the request.
- Exact pinned upstream source retrieval was unavailable from the managed network, so activation
  was deliberately not attempted from binary inference alone.
- Submitted root request SHA-256
  `0daf0c1d4377bae29b696e3f7a4870451b37cd4df3920bca7f3c9b5d9861e4ab`. It backs up any prior
  canary unit/config, creates root-only `/etc/maestro-wdtt-canary`, installs a resource-limited
  systemd unit, then explicitly disables/stops it and removes the activation environment file.
- The request creates no credential, starts no process, opens no port, changes no firewall/NAT,
  and restarts no production service. After broker approval, verify the root audit and stopped /
  disabled / no-listener state before designing the separate activation request.
- Follow-up verification at 2026-07-15 11:02 UTC confirms the broker consumed the draft and the
  disabled contour was installed: `/etc/systemd/system/maestro-wdtt-canary.service` exists,
  `/etc/maestro-wdtt-canary` is mode 0700, and the required `activate.env` is absent. No exact-path
  WDTT process exists and `/proc/net/udp{,6}` contains no UDP 56000/56001 socket. The managed
  sandbox cannot query systemd D-Bus, so disabled/inactive is established by the missing mandatory
  ConditionPath/EnvironmentFile plus the independent process/socket checks, not by `systemctl`.
- This is preparation only, not activation or deployment. Production/backend/OTA/firewall and live
  VPN processes remain untouched. The next serious gate is a separately approved activation with
  generated server-side credentials and explicit NAT/firewall rollback, then real-phone E2E.

## Canary activation request (2026-07-15)

- Owner explicitly authorized starting the isolated canary, with the requirement that all existing
  protocols remain working.
- Submitted idempotent root request SHA-256
  `9e718523e4a2c43c3c2d1426dd223a1f498992b20de706c4c467d0ef9132de34`. Before starting it verifies
  the pinned binary/checksum, free UDP ports, stopped state, and an already-ACCEPT host INPUT policy;
  it refuses to edit the INPUT firewall.
- It backs up IPv4/IPv6 iptables, `net.ipv4.ip_forward`, the canary unit/config, all active services,
  and all existing TCP/UDP listeners. It generates a new 32-byte master password directly into a
  root-only environment file (never printed or embedded), starts only `maestro-wdtt-canary`, and
  requires both UDP 56000/56001 plus generated password/WG state.
- Existing active services and listeners must remain a superset of the pre-start snapshot. Any
  failure triggers automatic stop/disable, secret removal, iptables restore and `ip_forward`
  restore. On success the canary deliberately remains disabled at boot pending real-phone E2E.
- This request has only been submitted; activation must not be claimed until the owner approves the
  broker button and the root result plus independent process/socket/protocol checks pass.
- Post-approval verification at 2026-07-15 11:22 UTC shows activation did **not** occur. The draft
  was consumed, but no `activation-backup-*` directory was created, `activate.env`,
  `passwords.json`, and `wg-keys.dat` are absent, no exact-path WDTT process exists, and
  `/proc/net/udp{,6}` has no UDP 56000/56001 sockets. This is consistent with broker rejection
  before execution (the broker policy rejects network/firewall operations, and the request included
  iptables backup/rollback). Do not resubmit the same request.
- The failure was fail-safe: no canary or credential remains and no production change is evidenced.
  Activation now requires a supervised root path explicitly capable of network/NAT changes, or a
  revised architecture that avoids host firewall/NAT mutation; it cannot honestly be reported as
  ready through the current restricted broker.

## Isolated Docker canary and verified APK update (2026-07-15 17:15 UTC)

- Authoritative feature head is `8459cdfd2e17077d11e4b27718d8254cf4da52ae` on
  `feat/mobile-vk-turn-wdtt`; draft PR remains #70. Relevant follow-ups are `34d9250` (mobile chip
  now says `WDTT`), `f137095` (portable canary-image checksum) and `8459cdf` (Android builds fail
  when WDTT binaries are missing).
- WDTT artifact run `29434834841` passed. Android test run `29435034770` passed with the WDTT fetch
  step mandatory. Final APK SHA-256 is
  `e11a840c5007b27799438339d0fc7d3f9d859964f1fdd0f00179cb8702e648e9`; ZIP inspection proves
  `libwdtt.so` and `libolcrtc.so` for both `arm64-v8a` and `armeabi-v7a`.
- Test-only Yandex object (not referenced by OTA):
  `https://storage.yandexcloud.net/maestro-apk/tests/MaestroVPN-WDTT-29435034770.apk`.
- The first APK run `29434257547` was correctly rejected after manual inspection: WDTT was absent
  because the artifact manifest named `wdtt/wdtt-canary-image.tar.gz` while Actions flattened the
  download. The old non-fatal consumer step hid that failure. Never distribute that APK.
- Isolated container `maestro-wdtt-canary-isolated` is active with no host network and no Docker
  published port. It owns `wdtt0`, DTLS UDP 56000 and WG UDP 56001 only in its private namespace.
  Root-only canary credentials live in `/var/lib/maestro-wdtt-canary-isolated`, one random password
  for each exact login.
- Unprivileged transient unit `maestro-wdtt-canary-relay.service` is active on host UDP 56000 and
  forwards only to `172.17.0.2:56000`. The old direct-host `maestro-wdtt-canary.service` is still
  disabled/inactive and must never be started.
- UFW has no UDP 56000 allow rule, so external TURN traffic cannot reach the relay yet. Docker added
  only its standard raw-table isolation rule for destination `172.17.0.2`; no WDTT host NAT or
  FORWARD rule was installed. Production critical services remain active and `systemctl --failed`
  is empty.
- Backend, subscriptions, OTA manifest and Android TV delivery are unchanged. No client receives
  WDTT yet. Real E2E remains blocked on two owner inputs: an active `vk.com/call/join/...` hash and
  a phone visible/authorized in `adb devices -l`.
- Next exact sequence: obtain hash + ADB phone; record UFW rollback and open only UDP 56000; prove
  Linux client over real VK TURN; provision exact phone device/WG identity; stage three-login
  backend config; verify mobile issue and TV absence; install APK; prove handshake, public egress,
  reconnect, sleep/wake, Wi-Fi/LTE and no routing loop. Do not enable backend or release OTA before
  this evidence and explicit owner approval.

## External VK TURN proof (2026-07-16 01:46 MSK)

- Owner supplied active call hash
  `RpZRQ0n6LR2jDmR23N7fFO2j6qW--BfUJkTz9TfvDm8`.
- Pinned Linux client from Actions run `29434834841` passed its artifact manifest and upstream
  marker. Client SHA-256:
  `52b570ffbba6221f581c998776e93dff0f263dbe8fc5e5bf0bb44119d3b3402a`.
- UFW UDP 56000 was opened only for the supervised proof. From external S2, VKCalls returned two
  real VK TURN relays, all 9 DTLS workers reached READY, and WireGuard config delivery succeeded.
- The disposable `linux-canary-proof` device binding was removed from canary state, the isolated
  container was reloaded at the same `172.17.0.2` address, and the relay plus all production
  services remained active. No production VPN service was restarted.
- UFW UDP 56000 was closed again. Temporary client, credentials copy, scripts and artifact download
  were removed from S1/S2. Root-only proof summary remains at
  `/var/lib/maestro-wdtt-canary-isolated/proof-20260716.txt`.
- Current blocker: `adb devices -l` shows no phone. Next: reconnect/authorize the phone, reopen only
  UDP 56000 for that test, provision the real device identity, stage the three exact logins, then
  perform the full phone E2E. Backend, subscriptions, OTA and Android TV remain unchanged.

## Live three-login backend and visible selector (2026-07-15)

- Root cause of the missing button was operational, not Android UI: the live `maestro-panel`
  binary dated 2026-07-10 did not contain `MAESTRO_VKTURN_FILE`, and no private WDTT config was
  loaded. The APK alone could therefore never receive a `vk-turn` selector item.
- Full backend `go test ./...` passed and the feature-head backend was built. The first production
  attempt failed on an old systemd EnvironmentFile value that is valid for systemd but not shell
  syntax; the transactional script restored the old binary/env/config and `maestro-panel` stayed
  active. The corrected health check does not source that file.
- The pinned client provisioned and server-verified one password/WG peer for each exact login:
  `wapmix` = `10.66.66.2/32`, `wapmixx` = `10.66.66.3/32`, `wapmix2` = `10.66.66.4/32`.
  Secrets remain only in root-owned canary/backend files.
- Live `/etc/maestro-vkturn.json` is mode 0600, enabled with minimum test version code `90181`,
  the supplied VK hash, and exactly the three allowed logins. UFW UDP 56000 is open with comment
  `maestro-wdtt-mobile`; rollback is `ufw --force delete allow 56000/udp`.
- Post-deploy live subscription checks passed for all three mobile logins and proved TV absence.
  `vk-turn` is manual-selector-only. In the app it is the last horizontal protocol chip, after
  `olcRTC`, labelled `WDTT`.
- `maestro-panel`, relay/container and production services are active; failed units are empty.
  Temporary download/build/provision scripts were removed. OTA/main remains unchanged.
- Remaining gate: the phone is still absent from `adb devices -l`. Fully restart/refresh the test
  app, confirm the `WDTT` chip, then complete real-phone tunnel, egress, reconnect, sleep/wake,
  Wi-Fi/LTE and routing-loop tests before any OTA release.
## Task stopped after real-phone failure (2026-07-15 22:11 MSK)

- Owner decision: stop WDTT until Claude takes over. No merge, deploy, enablement, or OTA release.
- Phone proof: the fresh profile exposed the `WDTT` selector but made all selectors unusable. The
  previous profile was restored and `Auto` again has an active Android VPN with HTTPS reachability.
  Exact libbox validation/runtime incompatibility remains unresolved.
- Live containment: backend config `enabled=false`; relay/container stopped; UDP 56000 closed;
  all three allowlisted logins return subscriptions without `vk-turn` for mobile and TV.
- Branch `feat/mobile-vk-turn-wdtt` at `8459cdf` is not in `main`; PR #70 remains draft; production
  OTA remains 1.0.145 and does not reference the WDTT test APK.
- Rollback backup: `/var/backups/maestro-panel/wdtt-stop-20260715T190827Z`.

## Exact-libbox evidence checkpoint (2026-07-16 07:09 UTC)

- Read-only Git verification: feature remains
  `feat/mobile-vk-turn-wdtt@8459cdfd2e17077d11e4b27718d8254cf4da52ae` and matches its remote;
  main remains clean at `43244f263756e8831fa56eebfc9a9f3fbf08db82`; pinned upstream remains
  clean/detached at `8b26530dfe90ff9b6aa3880ba2c1f070e21e2d3a`. PR #70 is open, draft and
  not merged.
- The known failing-test artifact `MaestroVPN-WDTT-29435034770.apk` was downloaded again and
  verified: 130282395 bytes, SHA-256
  `e11a840c5007b27799438339d0fc7d3f9d859964f1fdd0f00179cb8702e648e9`.
- Packaged `libbox.so` SHA-256 is now fixed for both ABIs: arm64
  `0fde946570493e8a881f7e4087a0cb831e34eb263c7bd47ad26046dcf367cab3`, armv7
  `5e8ad42a5e54d51d2c630f67cb4e4d8c9bb0327c6f91858ad423452029d2f095`. The feature
  source declares `SINGBOX_REF=v1.14.0-alpha.31` and references
  `fc25cedc25e9b1fcecb4f1589453cb381d518031`, but binary-to-source provenance remains unproven.
- `go test ./internal/subgen ./internal/vkturnconf` passes on the exact feature HEAD. These tests
  prove generator/config structure only; they do not invoke packaged `Libbox.checkConfig`.
- Live containment was reverified: panel active, WDTT flag false, canary and relay inactive, no
  UDP 56000/56001 listener, no running container, no failed units, OTA still 1.0.145/145.
- Remaining blockers are the absent byte-exact/structural failing profile, first libbox
  error/stage and authorized ARM phone. Do not change the generator yet. Next safe test is an
  offline exact-binary matrix without VPN/child/network: baseline → WDTT outbound → manual
  selector → DIRECT/DNS rules → full generated profile. Check package bypass separately through
  `OverrideOptions`; it is not part of JSON.
- No runtime, production config, service, port, container, merge or OTA state changed during this
  checkpoint.
