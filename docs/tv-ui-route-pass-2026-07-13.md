# Android TV route layout pass, 2026-07-13

Secrets and temporary connection codes are intentionally excluded.

## Completed before this pass

- Server feature branch: `fix/tv-ui-rebuild-20260713`.
- `6cc4df0`: responsive clean TV home.
- `e408c4c`: shared TV surfaces for `FantasyDialog`, `FantasyListRow`, `FantasyTextField`, and `FantasyToggle`, plus the main handoff.
- Server tree was clean before this route pass; no push, merge, APK install, release, or OTA.

## Route-level changes

- `GlossyButton`: TV no longer renders carved wood; it uses a clean green CTA, restrained scale, and amber D-pad focus. Mobile wood/gradient branches remain unchanged.
- `BuyScreen`: TV headings are clean sans-serif and tariffs use a responsive two-column grid instead of a narrow 560dp vertical strip. Mobile list layout remains unchanged.
- `ClaimScreen`: TV uses a centered 720dp clean panel and a wider input instead of a narrow phone form. Mobile image/background/layout remains in the mobile branch.
- `SettingsScreen`: TV uses a two-column lazy grid with a clean transparent top bar instead of giant full-width rows. Mobile keeps the original scroll column.
- `IosKaringDialog`: TV QR is a compact 210dp clean white card; `tvm_qr_bezel` is no longer rendered on TV. Mobile `frame_qr` composition remains unchanged. TV close CTA is no longer full-dialog width.

## Verification

- `git diff --check` passed locally.
- Exact compile task: `:app:compileOtherDebugKotlin`.
- Result: `BUILD SUCCESSFUL in 1m 34s`, 14 tasks (2 executed, 12 up-to-date).
- The reconstructed local `libbox.aar` remains compile-only. Do not build/install/release an APK from it.

## Next

- Sync these five source files and this document to the server feature branch, normalize Windows CRLF to LF, check the server diff, and commit without push.
- Continue reachable settings subpages and split-tunnel route; shared components improve them, but direct Material/top-bar/fixed-width layout still needs an explicit TV audit.
- Real KP1 D-pad/screenshot QA still requires the official runtime `libbox.aar` from a trusted source.
