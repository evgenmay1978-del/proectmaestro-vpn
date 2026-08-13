# Mobile 154 protocol recovery handoff - 2026-08-13

Branch: codex/mobile-protocols-wdtt-awg-olc
Base: origin/codex/mobile-4d-deck at 71b7f0c59e0c8fa6be36d1bc78f66b4753324a26
RED commit: 8c9839a0b3dc6897b83a81c394c5a8c7c92ee2e0

## User-visible failures and proven causes

- Mobile 154 dropped WDTT because the phone arc trusted only selector tags returned at runtime. The WDTT tag is vk-turn and is owner-only.
- AWG uses the runtime tag awg. The fleet libbox workflow already builds with with_awg; the backend still version-gates credentials so unsupported clients never receive the endpoint.
- The carved arc has seven physical cells. Any eighth protocol was silently discarded by getOrNull; the corrected mobile arc is horizontally swipeable and keeps every protocol reachable.
- Maestro global Telemost room updates failed because the panel passed room plus telemost, while ops/olcrtc-room.sh global mode accepts exactly one room argument. Per-login updates still use login, room and optional provider.
- Global wbstream is invalid because wbstream rooms are per-login; the panel now rejects that request before invoking the script.

## Safety boundaries

- WDTT and AWG are forced into the mobile selector only for logins wapmix, wapmixx, and wapmix2; ordinary customers do not receive private owner protocols.
- olcRTC keeps its existing credential lock and request-support behavior.
- TV UI and TV assets are untouched.
- No server, DNS, OTA, subscription, bot, customer, payment, or production state was changed in this branch.

## Verification and repetition guard

- RED tests cover owner ordering, AWG label, overflow placement, ordinary-account isolation, global Telemost argv, global wbstream rejection, and per-login provider argv.
- Local Go execution is not authoritative on this workstation: go is unavailable and the cached toolchain is incomplete. Use exact-SHA GitHub Actions.
- Do not use local apply_patch in this worktree: Windows deny-read ACL has failed repeatedly.
- Do not create patches with PowerShell text redirection or Set-Content: prior attempts produced encoding and context failures.
- Use generated UTF-8 byte patches outside the worktree, inspect them, run standalone git apply --check --recount, then apply once.
- GitHub CLI is absent; use the connected GitHub app or API for PRs and workflow evidence.

## Next infrastructure phase

After this mobile and panel branch is green and reviewed, resume the HA single-organism plan from the authoritative HA branch and plans. Production remains NO-GO until CI, restore rehearsal, signed desired-state boundary, idempotency, and rollback gates are all green. S1 is intentionally out of scope for this mobile fix.
