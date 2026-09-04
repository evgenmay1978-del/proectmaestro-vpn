# Production fleet inventory for commercial white-list rollout

Status: read-only inventory; mutation `NO_GO`

Snapshot date: 2026-09-04. This document records only the sanitized facts from
the bounded production read-only pass. It contains no host address, credential,
UUID, subscription URL, customer record, certificate material, or configuration
body.

## Repository checkpoint

- Canonical branch: `codex/yandex-cdn-whitelist-task3-sync`.
- Repository checkpoint: `ac8c6bf160d1adcd08b9e9046c9577d47ce9cdcf`.
- Exact-SHA GitHub checks are green at that checkpoint: S4 package run
  `33848098013`, HA artifact run `33848098068`, and Yandex CDN release run
  `33848098064`.
- Green repository checks prove the checked-in implementation only. They do not
  prove a production artifact-to-host binding, a node certificate, a usable
  provider console, a backup restore, a firewall change, or a rollback time.

## Evidence method and limits

The read-only pass used strict pinned key-only SSH and only bounded facts from
`systemctl show`/`is-active`, filtered socket ownership, executable paths through
`/proc`, filesystem capacity, time synchronization, default-route count, and
aggregate firewall/backup indicators. It did not read configuration bodies,
secrets, subscriptions, customer data, payment state, or bot tokens.

The provider login required CAPTCHA and the direct VNC link was unauthorized;
therefore current independent console recovery is not proven. The stale local
`s1` alias still identifies the deleted old host and must never be used. Only the
owner-authoritative current S1 pin and hostname may identify S1.

## Sanitized host facts

| Node | Intended/live role observed | Ordinary services observed | Candidate ports | Other read-only facts | Mutation blockers |
| --- | --- | --- | --- | --- | --- |
| S4 | Existing x-ui/VPN node and private CDN canary node; Ubuntu 24.04 x64 | x-ui and Hysteria active | `18081/18082` owned by existing `maestro-xray-cdn.service` and must be preserved; commercial candidate `28081/28082`; `18084/18443` were clear in the read-only snapshot; all four require a fresh preflight | Time synchronization, default route, disk, and failed-unit checks sane | Current console, verified service backup and restore, node certificate, reviewed firewall plan, immutable commercial artifact binding, and demonstrated rollback under five minutes are not proven |
| S2 | Multi-protocol and bot node; Ubuntu 24.04 x64 | Hysteria, nginx, Caddy, and `vpn_bot` active | `18081/18082/18084/18443` clear | Time/default-route/failed-unit checks sane; root filesystem about 70% used with about 2.8 GiB free; both systemd-networkd and networking active, so network ownership is unresolved | Verified service backup/restore and the remaining mutation preconditions are not proven; unresolved network ownership is an additional stop gate |
| S3 | x-ui/VPN node; Ubuntu 22.04 x64 | x-ui active with child Xray under protected `/usr/local/x-ui` | `18081/18082/18084/18443` clear | Time/default-route/failed-unit checks sane; identity continuity only; UFW inactive; both systemd-networkd and networking active | Verified backup/restore and current console proof are not proven; network ownership and reviewed firewall change remain stop gates |
| Current S1 | Replacement public control plane; owner-authoritative pin and hostname matched; Ubuntu 24.04 x64 | maestro-panel, x-ui/Xray, Hysteria, and nginx active | `18081/18082/18084/18443` clear | Time synchronization, default route, disk, and failed-unit checks sane | Current console path, backup/restore, node certificate, immutable deploy digest binding, firewall plan, and rollback under five minutes are not proven |

## Required mutation gate

Every row must be `GREEN` for the exact node and exact change package before a
single mutation. Evidence from one node cannot satisfy another node.

| Preconditions for each host | Current state |
| --- | --- |
| Exact reviewed Task 15 SHA and all required exact-SHA CI | `NOT_PROVEN` for a Task 15 mutation package |
| Current console recovery path exercised | `NOT_PROVEN` |
| Authoritative host identity and role rechecked immediately before change | Read-only snapshot exists; fresh pre-change proof still required |
| Ordinary service/unit/path/listener inventory unchanged | Read-only snapshot exists; fresh pre-change proof still required |
| Candidate-port ownership matches this inventory | Read-only snapshot exists; fresh pre-change proof still required |
| Service/data backup plus exact restore command proved on an isolated restore target | `NOT_PROVEN` |
| Immutable artifact manifest and digest bound to staged bytes on this node | `NOT_PROVEN` |
| Node mTLS certificate identity, trust chain, expiry, and key permissions | `NOT_PROVEN` |
| Reviewed exact firewall plan and rollback ruleset | `NOT_PROVEN` |
| Deliberate rollback restored the isolated baseline in under five minutes | `NOT_PROVEN` |

The fleet verdict is therefore `PRODUCTION MUTATION NO_GO` on S4, S2, S3, and
current S1.

## Facts versus design assumptions

Confirmed facts are limited to the tables above and the exact repository run
IDs. The following remain design targets, not live facts: one common active
Origin group, a complete managed desired set on every Origin, mTLS controller
reachability, exact four-rule firewall convergence, selected-exit country
truth, commercial balance enforcement, automatic customer refresh, and live
delivery through both bots and the channel.

Production Android remains `1.0.157`. `1.0.158-task7-test` remains test-only and
must not be installed, promoted, released, or published as OTA. OLCRTC and WDTT
remain frozen.
