# Production fleet inventory for commercial white-list rollout

Status: read-only inventory; mutation `NO_GO`

Snapshot refreshed: 2026-09-05. This document records sanitized inventory and
bounded isolated commercial rollout evidence. It contains no host address, credential,
UUID, subscription URL, customer record, certificate material, or configuration
body.

## Repository checkpoint

- Canonical branch: `codex/yandex-cdn-whitelist-task3-sync`.
- Current installed source/package checkpoint:
  `b4415daa90c95a38f9a7b9adea7642c66e63a420`; later documentation HEAD is
  separate from package and deployed identities. Scoped reviews PASS. Exact
  Yandex run `33928873964` has all six jobs GREEN, including Android
  `101204799664` refreshed 2026-09-05; immutable `33928874093`/job
  `101203186787` and network `33928874014` are GREEN. No reruns were requested.
  Artifact `9957942956`, size `54141763`, archive SHA-256
  `7a74ff26f181c44456493958577005f938b417cd2eca501dab60376989736b7b`, is
  staged on S4 with nine members verified and manifest SHA-256
  `a687657c43a20d77512a26ac73821c161513ddaebdb4dda6af466bde2855add5`.
  Shipped upgrade plan PASS binds release
  `b4415daa90c9-s4commercial-bc65f9447a8e945a`, config SHA-256
  `5fd67f3bf80fc3a7c0bfae1de32d421b66ee11ac95bff8ea3f851ca71d052f38`,
  runtime-input SHA-256
  `bc65f9447a8e945a4b7b8686859f54e7f05408f6477b2171047724ed3dde3f34`.
  Locked shipped apply EXIT 0: ACTIVE with all six checks true. Console and
  fresh preflight passed; exact change sheet was transferred before apply.
  Post-upgrade ordinary/private saved hashes, units/PIDs and strict SSH PASS.
  Synthetic add 2/revoke 3/resume 4 and GET-only receipt recovery PASS; current
  desired generation 4 is resumed. No nginx/firewall/CDN/private-canary mutation
  occurred in this upgrade.
- Retained previous commercial code checkpoint:
  `3603f11bbc35a4a9d708c41db1bc13f0d2907805`. The targeted permission
  regression is green; exact-SHA runs are Yandex CDN release `33923773188`, HA
  immutable artifact `33923773196`, and HA S4 network `33923773238`; all are
  terminal green. Commercial package job `101188536793` produced artifact
  `9956162284`, size `54137499`, archive SHA-256
  `8274c235b47b54ad0bfa2e76f6c7f2dabf8a34476bdd0f0cceb421fc86a99bc5`;
  its exact direct S4 fetch, member verification, and operator `plan` passed.
  The plan bound manifest SHA-256
  `623fa69d3ebe00dca99595d73d5287c9b63b7a6346def2ea4252d441fb10268c`,
  config SHA-256
  `8ff75a7e4718610cf20866aadc7e6903d69362d7455d60735ddef47daa41e7a1`,
  and runtime-input SHA-256
  `afb832ce9e32f3a4b088222989f3e24d46a2c4d3efbb458e214c286925cbb928`
  to release `3603f11bbc35-s4commercial-afb832ce9e32f3a4`.
- Canonical ingress source checkpoint
  `aad63b52c74aedd2c568f0ed4a6a9f912e31e262`, with sealed-workflow correction
  `26895992db384d8275c36720a622d96862505f69`, has scoped review and isolated
  real nginx parser PASS. Both are b441 ancestors. The isolated custom unit is
  now ACTIVE/enabled, PID `646960`, listener `28080`, unknown local path `404`.
  Config SHA-256 `43291883decf99bcdc1bffbb2172a44fbc37a1f585da2371fa722e0953e2e739`,
  unit SHA-256 `3fda8a55077918cd7e0e78a214df8b4c14d025ffdc0732f621534c16d02fd68d`,
  and binary SHA-256 `1f16b72bea2f44e5d04fe6cf9e3e4b0dec53a82c50c7c1533c302a8ecaeccacf`
  match reviewed inputs. Official nginx/nginx-common `1.24.0-2ubuntu7.17` were
  installed once; default nginx stays masked/inactive. No public commercial
  allowance or CDN switch exists.
- The preceding immutable artifact
  `maestro-xray-cdn-commercial-dbbd950ab556b92b103cd51f5a4b2686acb74ef5`
  has ID `9955259827`, size `54137499` bytes, and archive SHA-256
  `64b41f53592fc3a3b5c82da45028f851a85811ea11725ed713b16ce67b484981`.
  S4 independently matched the fetched artifact and its manifest SHA-256
  `b005eba27a5ab5a95038dae7ce5dc26e440fb42b599af8e38b31abceb14246e4`.
  The shipped operator `plan` passed with config SHA-256
  `8ff75a7e4718610cf20866aadc7e6903d69362d7455d60735ddef47daa41e7a1`
  and runtime-input SHA-256
  `57a9aeba7bca9f07fdac0c72311baeba9d2709f24b2bc4a5378b22c8ba1043d5`.
- Its first real S4 apply failed because the agent-owned `runtime/relay-ca`
  directory was not traversable by the Xray service identity. The operator
  recovered to `ABSENT`; the commercial firewall delta was exactly rolled
  back; and the full ordinary/private baseline passed. This immutable failed
  artifact is evidence only and must not be modified or reapplied.
- Green repository checks prove the checked-in implementation only. They do not
  prove a production artifact-to-host binding, a node certificate, a usable
  provider console, a backup restore, a firewall change, or a rollback time.

## Evidence method and limits

The read-only pass used strict pinned key-only SSH and only bounded facts from
`systemctl show`/`is-active`, filtered socket ownership, executable paths through
`/proc`, filesystem capacity, time synchronization, default-route count, and
aggregate firewall/backup indicators. It did not read configuration bodies,
secrets, subscriptions, customer data, payment state, or bot tokens.

The encrypted QEMU noVNC recovery console passed before the `3603f11...`
installation, then expired with login CAPTCHA and direct VNC Unauthorized.
The owner has now authorized continuation; product-panel authentication is
restored without CAPTCHA. The official QEMU console is now connected/encrypted
with the Ubuntu tty1 login visible. Fresh preconditions still precede the
bounded upgrade; authenticated panel access alone is insufficient console proof.
Strict SSH remains available.
The stale local `s1` alias identifies the deleted old host and must
never be used. Current S1 uses its previously trusted pin and owner-authoritative
hostname; no TOFU, password, new key, or port opening was used.

## Sanitized host facts

| Node | Intended/live role observed | Ordinary services observed | Candidate ports | Other read-only facts | Mutation blockers |
| --- | --- | --- | --- | --- | --- |
| S4 | Existing x-ui/VPN and private CDN canary node; Ubuntu 24.04 x64 | Ordinary/private hashes, units/PIDs and strict SSH PASS; b441 and isolated ingress ACTIVE | Private `18081/18082`; commercial Xray `28081/18084`, loopback API `28082`, agent `18443`, loopback health `18444`; ingress `28080` without public allowance | Console, preflight, b441/synthetic receipts, corrected ingress resume and isolated direct traffic PASS; default nginx masked; static firewall policy unchanged | GET-only recovery PASS; unknown-outcome fault injection, CDN/public-edge/counters/client proof and deliberate rollback/re-apply open |
| S2 | Multi-protocol and bot node; Ubuntu 24.04 x64 | Hysteria, nginx, Caddy, and `vpn_bot` active | `18081/18082/18084/18443/18444` require a fresh preflight | Time/default-route/failed-unit checks sane; root filesystem about 70% used with about 2.8 GiB free; both systemd-networkd and networking active, so network ownership is unresolved | Verified service backup/restore and the remaining mutation preconditions are not proven; unresolved network ownership is an additional stop gate |
| S3 | x-ui/VPN node; Ubuntu 22.04 x64 | x-ui active with child Xray under protected `/usr/local/x-ui` | `18081/18082/18084/18443/18444` require a fresh preflight | Time/default-route/failed-unit checks sane; identity continuity only; UFW inactive; both systemd-networkd and networking active | Verified backup/restore and current console proof are not proven; network ownership and reviewed firewall change remain stop gates |
| Current S1 | Replacement public control plane; owner-authoritative pin and hostname matched; Ubuntu 24.04 x64 | maestro-panel, x-ui/Xray, Hysteria, and nginx active | `18081/18082/18084/18443/18444` require a fresh preflight | Strict pinned key-only SSH PASS; controller-source mTLS leaf with exact `maestro-whitelist-controller` SAN staged in a protected backup; live services unchanged | Console path, backup/restore, node deploy digest binding, firewall plan, and rollback under five minutes are not proven |

## Required mutation gate

Every row must be `GREEN` for the exact node and exact change package before a
single mutation. Evidence from one node cannot satisfy another node.

| Preconditions for each host | Current state |
| --- | --- |
| Exact reviewed Task 15 SHA and all required exact-SHA CI | Installed b441 review/CI GREEN; previous `3603f11...` identity retained separately |
| Current console recovery path exercised | S4 official QEMU console connected/encrypted PASS after session restoration; other nodes not proven |
| Authoritative host identity and role rechecked immediately before change | Read-only snapshot exists; fresh pre-change proof still required |
| Ordinary service/unit/path/listener inventory unchanged | Read-only snapshot exists; fresh pre-change proof still required |
| Candidate-port ownership matches this inventory | Read-only snapshot exists; fresh pre-change proof still required |
| Service/data backup plus exact restore command proved on an isolated restore target | S4 `GREEN`; other nodes not proven |
| Immutable artifact manifest and digest bound to staged bytes on this node | Installed b441 artifact `9957942956`, nine members and exact shipped plan/apply identities PASS; `3603f11...` retained; `dbbd950...` failed evidence only |
| Node mTLS certificate identity, trust chain, expiry, and key permissions | S4 inputs `GREEN`; S1 controller-source leaf staged only; other node inputs not proven |
| Reviewed exact firewall plan and rollback ruleset | S4 corrected delta committed after strict SSH and independent observer; ordinary/private baseline unchanged |
| Deliberate rollback restored the isolated baseline in under five minutes | `NOT_PROVEN` |

Full fleet/customer promotion remains `NO_GO`. A bounded S4 operation may
proceed under standing authorization once its current node-specific
preconditions are GREEN. S2, S3 and current S1 each require their own fresh
gates in the approved rollout order; an S4 result cannot authorize those nodes.

## Facts versus design assumptions

Confirmed facts are limited to the tables above and the exact repository run
IDs. The following remain design targets, not live facts: one common active
Origin group, a complete managed desired set on every Origin, mTLS controller
reachability, exact four-rule firewall convergence, selected-exit country
truth, commercial balance enforcement, automatic customer refresh, and live
delivery through both bots and the channel. Reviewed source for the isolated
two-path ingress is installed and its local unmatched-path rejection is proved.
Direct authenticated traffic PASS used the existing synthetic generation 4
identity through loopback SOCKS -> ingress `28080` -> commercial XHTTP/ML-KEM
`28081` -> `exit-s4` relay -> HTTPS egress-check service (ipify); curl exit `0`, `468 ms`,
S4 egress match. The owned client stopped; ordinary/private file and service
baselines remained unchanged, with no desired POST or service/config/firewall/CDN
mutation. This does not prove CDN/public-edge, counters or general device
behavior. Deliberate firewall/CDN rollback remains open; CDN stays `NO_GO`.

The original direct-traffic preflight stopped before client startup or traffic:
root-relative checksum paths were checked from SSH cwd `/root`. This was not
proven content drift. Failed evidence is retained; a separate helper/evidence
family corrected only the checksum subprocess cwd to `/`, passed scoped review
and syntax validation, and produced the direct PASS above.

The first ingress attempt rolled back only the newly owned units after a false
raw-ss padding mismatch. The first existing-install resume then stopped before
starting because fail2ban legitimately changed SSH ban members. Both failures
are retained. The corrected resume completed with identical socket ownership,
ordinary/private hashes and unit/PIDs. Only IPv4 members in exact
f2b-table/addr-set-sshd/ipv4_addr are normalized; every other rule/static set is
compared exactly, with fail2ban PID `594973` and sshd jail active. No security
service was disabled, old ban list restored, package reinstalled or private
canary probed.

Read-only identity discovery is complete for both existing bots: current S1
`@MaestroSecureVPN_bot` / `vpnbot.service` and S2 `@MaestroSecureNaive_bot` /
`vpn_bot.service`. Each was active with one process, webhook absent and pending
count zero. Channel `@maestrovpn` is configuration-backed; publishing rights
and commercial customer delivery remain unproven. No messages or bot-state
writes were performed.

Synthetic generation 1 is GET-only recovery and must never be replayed. Reviewed
helpers executed on S4 after shipped b441 ACTIVE/baseline proof, preserving the
previous `3603f11...` proof byte-identical. Nine protected files were transferred
to S1. Add 2/revoke 3/resume 4 each returned POST/receipt GET 200 with exact
release/config/boot/generation/managed-set digest and freshness at most 30 s.
GET-only resume recovery returned 200 with no POST and zero replayed requests.
Current desired generation 4 is resumed. No lost response was simulated;
unknown-outcome fault injection remains open. Exact phase timings are in
`COMMERCIAL_CANARY_EVIDENCE.md`.

Production Android remains `1.0.157`. `1.0.158-task7-test` remains test-only and
must not be installed, promoted, released, or published as OTA. OLCRTC and WDTT
remain frozen.
