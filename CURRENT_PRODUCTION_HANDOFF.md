# MaestroVPN current production handoff

Last verified: 2026-08-13 (Europe/Moscow)

This file is intentionally anonymized. Never add passwords, tokens, private subscription URLs, customer records, private keys, production addresses, or authenticated panel paths.

## Current source and release state

- GitHub `main` contains merge commit `296079cf819b36087c690b525d8970d6c87a18db` from PR #86.
- The mobile production OTA is version `1.0.156`. The TV code path and TV assets were not changed by the panel/WDTT repair.
- The live Maestro panel on the replacement primary node reports build `296079c`.
- PR #86 rejects test-only `90xxx` values in the WDTT production `min_version_code` field and preserves the previous config on validation failure.

## Live panel verification

Verified after deployment:

- `maestro-panel.service`: active and enabled.
- `/healthz`: `ok 296079c`.
- `/order/tariffs`: HTTP 200.
- No recent panel panic, fatal, or WDTT config parse errors.
- The previous panel binary and the pre-change WDTT config have recoverable root-only backups on the primary node.

## WDTT production state

- Server source is pinned to reviewed upstream commit `8b26530dfe90ff9b6aa3880ba2c1f070e21e2d3a`.
- Build toolchain is the pinned Go `1.26.3`; the installed binary metadata reports the exact clean upstream VCS revision.
- `wdtt.service`: active and enabled.
- Public DTLS/WRAP UDP listener and internal WireGuard UDP listener are present.
- The userspace WireGuard interface is up, IPv4 forwarding is enabled, and managed NAT is active.
- Exactly three existing owner passwords were migrated from the panel config into a root-only `0600` WDTT database. No password or key was printed or committed.
- The live panel config is enabled for production mobile `versionCode 156`, points at the replacement primary node, retains its VK hashes and all three owner secrets, and no longer contains the test gate `90181`.

## Subscription matrix verified

Using the live internal API without printing subscription tokens or credentials:

- All three owner accounts receive `features.vk_turn=true` and a complete WDTT payload on mobile `versionCode 156`.
- All three payloads point to the replacement primary WDTT endpoint.
- The same three accounts receive no WDTT payload on the TV platform.
- This confirms the server-side reason for “appears once, then disappears” is removed: the app version gate is now the real production code rather than `90xxx`.

## Remaining real-device check

The local Linux WDTT client did not reach `READY` within 60 seconds because it remained in VK allocation before contacting the WDTT server; the server journal showed no handshake attempt. Do not misreport this as a server failure or as a successful full data-plane test.

The next required check is on a real Android phone:

1. Refresh the subscription on app `1.0.156`.
2. Confirm WDTT remains visible after repeated refreshes and app restart.
3. Select WDTT and confirm connection, public egress, reconnect, sleep/wake, and Wi-Fi/mobile switching.
4. Then verify a new WDTT device/handshake appears in the primary-node service state without exposing its identifier.
5. Keep TV unchanged and WDTT-free.

## olcRTC and AWG scope

- The latest merged panel binary is deployed, including the current olcRTC panel implementation.
- No olcRTC room was changed during this repair because an authenticated live room-edit test was not performed.
- AWG server credentials and rollout policy were not modified by this panel/WDTT deployment.
- If either protocol is still absent after a fresh subscription on `1.0.156`, diagnose its live subscription payload and runtime separately; do not change WDTT again without new evidence.

## Safety rules for the next session

- Read `AGENTS.md`, this file, and the relevant design/runbook before any mutation.
- Work GitHub-first and reuse the existing topic branch when possible.
- Use `ops/maestro-repetition-guard.py` before every executable action.
- Never repeat a failed command family. Record `fail`, diagnose read-only, record `correct`, check once, then make one corrected attempt.
- Never use `apply_patch` in the Windows worktree where the recorded deny-read ACL failure applies.
- Do not change customer dates, subscriptions, payment state, bot data, DNS, OTA, TV behavior, or protocol policy while validating this deployment.
