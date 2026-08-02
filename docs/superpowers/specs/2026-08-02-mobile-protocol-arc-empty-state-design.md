# Mobile Protocol Arc Empty-State Design

**Decision date:** 2026-08-02
**Scope:** phone Home only; TV, shared group loading, VPN runtime, backend, Release and OTA are excluded.

## Confirmed failure

The owner installed artifact `8832259523` from run `30743893059`, built from `6dcae22`.
That predates the mosaic replacement in `0a116af`, so it cannot verify the eyelid fix.

The same screenshot exposes an independent current-branch defect: all protocol sectors are empty
except a centered locked `WEBRTC`.

The exact data path is:

1. `SFANavigation` passes `emptyList()` while no selector group is available.
2. `orderedHomeProtocols(emptyList())` currently returns only `olcrtc`.
3. `arcSectorCells(1)` intentionally centers a one-item list.
4. `homeProtocolLabel("olcrtc")` renders that item as `WEBRTC`.

The existing JVM test explicitly blesses this broken empty-state, while the Pillow simulator always
draws seven hard-coded protocols and therefore cannot expose the runtime-empty case.

## Approaches considered

1. **Phone-only deterministic empty fallback (selected).** When the runtime list is empty, return
   the existing owner-approved `HOME_PROTOCOL_ORDER`. Preserve the current ordering/filtering for
   every non-empty list. This directly prevents the visible collapse and changes one pure helper.
2. Retry or observe offline profile loading in shared `GroupsViewModel`. This may improve the data
   source, but it changes shared TV/VPN-adjacent behavior and still cannot guarantee a list when a
   profile file is temporarily unavailable.
3. Cache the last non-empty list in Compose. This adds state, does not help a fresh cold start, and
   makes the rendering result depend on navigation history.

## Design

`orderedHomeProtocols(protocols)` keeps its current behavior for non-empty inputs: known runtime
tags follow the approved order, unknown runtime tags remain after them, and `olcrtc` remains last
and unique.

For the one proven failure input, `protocols.isEmpty()`, it returns all seven already-defined tags:

```text
auto, vless, hysteria2, anytls, naive, vk-turn, olcrtc
```

No new protocol, callback, state holder, asset or UI layer is introduced. If the selector is truly
unavailable, existing callbacks already no-op safely because `SFANavigation` has no `selectGroup`;
the change only keeps the approved phone Home labels and cell registration visible.

## Verification

- A JVM regression test must fail first because current code returns only `[olcrtc]`.
- The same test must pass after the minimal helper change.
- Existing non-empty, unknown-tag, WDTT and single-`olcrtc` ordering tests remain unchanged.
- GitHub Actions must pass `:app:assembleOtherDebug` and `:app:testOtherDebugUnitTest` and expose a
  real `maestrovpn-tv-test-apk` artifact.
- The artifact must include both this fix and tested mosaic commit `0a116af`.
- No TV, shared `GroupsViewModel`, backend, workflow, Release or OTA diff is allowed.
