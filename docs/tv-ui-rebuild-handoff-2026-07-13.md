# Android TV UI rebuild handoff

This is the current TV-only handoff. Secrets, SSH passwords, and temporary ADB pairing data are deliberately excluded.

## Scope and acceptance

- Rebuild all Android TV screens; do not change mobile UI.
- Real KP1 screenshots and D-pad behavior are authoritative.
- KP1 runtime: 1920x1080, density 320, Compose 960x540dp, fontScale 1.0, fullscreen. The defects are layout/design defects, not density or inset defects.
- Do not merge to `main`, build OTA, or release without explicit owner approval.

## Current source state

- Canonical server repo: `/root/maestrovpn-tv`.
- Server feature branch: `fix/tv-ui-rebuild-20260713`.
- Server commit: `6cc4df0 feat(tv): start clean responsive UI rebuild`.
- That commit replaces the absolute 1920x1080 home artboard, offsets, `tvm_*` bitmap panels, and old bitmap medallion with a responsive Compose home.
- Initial home compile passed: `BUILD SUCCESSFUL in 4m 4s` (14 tasks).
- Server `main` is untouched. No APK install, merge, release, or OTA was performed.
- Push to `evgenmay1978-del/proectmaestro-vpn` was rejected twice by external-destination policy, including after explicit owner approval. Do not circumvent through another channel.

Local clone: `C:\Users\maest\Documents\Codex\2026-07-13\new-chat\repo`. It has TV-only WIP over local `main`; unrelated generated schema files must not be modified.

## Local tools and paths

- Android SDK: `C:\Users\maest\AppData\Local\Android\Sdk`.
- ADB: `C:\Users\maest\AppData\Local\Android\Sdk\platform-tools\adb.exe`.
- APK Analyzer: `C:\Users\maest\AppData\Local\Android\Sdk\cmdline-tools\latest\bin\apkanalyzer.bat`.
- `repo/local.properties` points to that SDK.
- Android SDK Platform 36 rev.2 and Build Tools 36.0.0 are installed.
- Gradle 9.3.1: `work\gradle-9.3.1`, transferred from server cache. Direct local wrapper download from services.gradle.org/GitHub timed out/reset; do not repeat.
- Dex2jar 2.0: `work\dex-tools-2.0\dex2jar-2.0`, transferred through server. Direct local GitHub download reset; do not repeat.
- Server lacks Android SDK, `local.properties`, and `app/libs/libbox.aar`; server Gradle fails before Kotlin with `SDK location not found`. Do not retry without SDK.
- Credential/token discovery was rejected by policy. Do not repeat or circumvent.

## Compile-only libbox

- Read-only USB ADB pulled installed APK 1.0.143 to `work\maestrovpn-installed-143.apk` (119848478 bytes). Device/app data was not modified.
- APK Analyzer located `io.nekohasekai.libbox.Libbox` in `classes20.dex`.
- `repo\app\libs\libbox.aar` was reconstructed from `io/nekohasekai/libbox/**` plus `go/**` for Kotlin compile-check only.
- Never use this reconstructed AAR for an installed APK, runtime, release, or OTA.
- Failed approaches not to repeat: full unfiltered classes20 caused unrelated serialization/Compose errors; filtering without `go/**` caused `Seq$Proxy` errors; anonymous official artifact download returned HTTP 401.
- Required official runtime artifact: run `28528349440`, artifact `8014876973`, name `libbox-aar`, digest `sha256:4c0ae536e74029df6ddab580d1f08f8614633967ab9482bb2ce46323e4c45592`.

## Shared TV component pass

- `FantasyListRow.kt` now gives TV clean dark-green rows/background, amber focus, white/muted text, and no press-scale. It also fixes the unfinished WIP brace so phone-only lighting remains inside the mobile `else` branch.
- `FantasyDialog.kt` gives TV a responsive clean panel capped at 78%/760dp with no carved wood or diamond ornament.
- `FantasyToggle.kt` gives TV clean switches and D-pad-aware segmented controls.
- `FantasyTextField.kt` gives TV a clean surface and amber focus border.
- All four changes are guarded by `rememberIsTv()`; the existing mobile fantasy assets, colors, and dimensions remain in the mobile branches.
- Compile verification passed with `:app:compileOtherDebugKotlin`: `BUILD SUCCESSFUL in 1m 9s`, 14 tasks.
- The alias `:app:compileDebugKotlin` is ambiguous because this project has product flavors and fails before Kotlin. Use `:app:compileOtherDebugKotlin`; do not retry the alias.

## Remaining route work

- Reachable route audit includes home, buy, claim, share QR, split tunnel, settings root/app/core/service/profile override/remote.
- Split list loads after about six seconds; use `outputs/tv-split-real.png`, not `tv-split.png`.
- Next, migrate reachable TV routes that still contain direct Material or fixed-width layouts, then compile after each coherent group.
- Real APK/D-pad QA remains blocked until the official runtime `libbox.aar` is obtained through a trusted route.
