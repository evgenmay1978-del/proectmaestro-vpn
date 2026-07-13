# Android TV UI real-device audit — 2026-07-13

Canonical handoff: `/root/.claude/projects/-root-maestrovpn-tv/memory/tv-ui-real-device-audit-2026-07-13.md`.

## Current owner decision

The owner rejects the graphics of **all Android TV screens**: distorted and shifted geometry, overlays, visual overload, inconsistent spacing and styles. A full TV-only redesign is required. The mobile UI must not be changed.

Owner update later on 2026-07-13: Settings is not needed and will not be in the app. Treat settings root and settings subpages as old audit evidence only, not as current redesign targets.

Previous OTA 1.0.143 delivery remains a technical fact, but it is not visual acceptance. PIL simulation is not acceptance either; the real KP1 runtime is the ground truth. Do not merge or release without explicit owner approval and real-device verification of every reachable TV screen.

## Proven device facts

- USB ADB: `GZ25020642003456`, SEI700BKP/KP1, Android 14.
- Package `com.maestrovpn.tv` 1.0.143.
- Physical display 1920×1080, density 320, Compose 960×540dp, fontScale 1.0, landscape, full-screen without cutout/inset.
- The issue is therefore not resolution, density, font scale, orientation or system insets. It is the actual Compose design/layout and the mismatch between the simplistic PIL compositor and runtime behavior.

## Audited surfaces

Home, Buy, Claim, Share QR, Split tunnel, Settings root, App settings, Core, Service, Profile override and Remote control were opened on the real device. Split tunnel needs about six seconds before its app rows appear. Trial/Scan QR were conditional on subscription state. Log/Groups/Profile routes exist in `SFANavigation.kt` but are not normally reachable from TV home or are state-dependent.

The home presents about 20 focusable targets at once. Buy wastes most of the canvas. Claim and Share are narrow phone-like surfaces. Settings pages mix incompatible Material, framed wood, ornate and sparse styles. The required solution is one dedicated TV layout/component system while preserving mobile branches unchanged.

## Dirty-tree warning

`app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/TvEskizHome.kt` currently contains an unverified modern-layout WIP (roughly +428/-380). It has no reliable build result and the remote copy likely misses the `heightIn` import. There is no commit, push or OTA. Inspect `git status` and the diff before any work; do not assume the WIP is approved or compilable.

Local audit screenshots are under `C:\Users\maest\Documents\Codex\2026-07-13\new-chat\outputs\`. Use `tv-split-real.png`; `tv-split.png` is an accidental repeat of Claim caused by Back closing the keyboard first.

No credentials or temporary ADB pairing data are recorded here.
## 2026-07-13 release 1.0.144
- Released main e8f95f9 as tv-v1.0.144, version_code 144.
- OTA mirror update.json points to MaestroVPN-TV-1.0.144-debug.apk, sha256 49cb4f7f997c6c7d8cb2543e13205e6603a200608557bacbf0745c14e6877fc5.
- KP1 test path used Yandex; direct S1 :18080 was blocked externally.
- Health 20:07Z: S1/S2/S3 failed units: none. Reports today are hello launches, not crashes.
- UI scope today: Settings removed from TV home; phone/protocols fit on KP1. Premium polish deferred.

