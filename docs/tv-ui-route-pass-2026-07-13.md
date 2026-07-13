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
- Owner update: Settings will not be part of the app, so do not continue settings subpages. Continue the split-tunnel/per-app route and other non-settings reachable TV routes.
- Real KP1 D-pad/screenshot QA still requires the official runtime `libbox.aar` from a trusted source.

## Continuation update — 2026-07-13 TV rebuild after Settings removal

- Owner explicitly removed Settings from app scope: do not redesign settings root/subpages for TV.
- Current branch: `fix/tv-ui-rebuild-20260713`.
- Latest TV route commits pushed to GitHub:
  - `944ea95` removes Settings entry from TV home; GitHub Actions `29271730214` success.
  - `4226e0f` polishes split tunnel/per-app TV layout; first CI `29272380895` failed on invalid Compose padding.
  - `a82912c` fixes that padding; GitHub Actions `29273231472` success.
  - `a59959c` polishes Trial activation screen for TV; GitHub Actions `29273635961` success.
- Split/per-app now uses the clean dark TV shell, transparent TV topbar, selected count, TV list spacing, and amber focus borders in `AppSelectionCard`. Mobile branch remains on the existing art frame.
- Trial screen now uses a centered clean dark TV panel, sans heading, wider field, and clean CTA. Mobile branch keeps its existing backdrop/scroll/wood behavior.
- `ScanQrActivateScreen` is phone-only: TV has no camera and the new TV home has no scan QR entry. Do not include it in the current TV route scope unless owner changes product direction.
- Graphify was force-updated after the code changes (`graphify update . --force`) and rebuilt 3073 nodes / 5166 edges / 243 communities.
- Do not merge to `main` / OTA / release without explicit owner approval and real-device KP1 verification.
