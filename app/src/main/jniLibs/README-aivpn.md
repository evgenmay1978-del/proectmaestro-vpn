# jniLibs — native helpers fetched at build time

This directory is **gitignored** (`.gitignore` line 13). Native binaries are never committed:
CI downloads or builds them before Gradle runs.

## Contents at build time

| File | Source | Fetched by |
|------|--------|------------|
| `<abi>/libmaestro_xhttp.so` | built from `android-xhttp/` (Go, PIE) | `.github/workflows/android-test.yml`, `.github/workflows/android-cdn-apk-build.yml` |
| `<abi>/libaivpn_core.so` | AIVPN v1.2.0 release APK | Gradle task `fetchAivpnNativeLibs` (`app/build.gradle.kts`); manual: `tools/fetch-aivpn-so.sh` |
| `<abi>/libbox.so` | libbox AAR (see `app/libs/`) | Gradle, from the AAR |

## AIVPN (`libaivpn_core.so`)

The experimental AIVPN tunnel is a separate UDP protocol with a Rust core
(<https://github.com/infosave2007/aivpn>, MIT). The Android app loads it through JNI from the
`aivpn_core` library name; the Kotlin side lives in `app/src/main/java/com/aivpn/client/AivpnJni.kt`
and `app/src/main/java/com/maestrovpn/tv/aivpn/`.

Gradle fetches it automatically. The `fetchAivpnNativeLibs` task in `app/build.gradle.kts`
runs before `preBuild` and before the per-variant JniLibFolders merge of the app module, so
`./gradlew :app:assembleOtherDebug` (what CI runs) always has the binaries in place. It downloads
the pinned APK (`aivpn-android.apk` from release `v1.2.0`) into `app/build/aivpn/`, verifies the
APK sha256, extracts `lib/arm64-v8a/libaivpn_core.so` and `lib/armeabi-v7a/libaivpn_core.so`,
verifies each .so sha256 and writes it into `app/src/main/jniLibs/<abi>/`. It is idempotent: an
existing file whose size and sha256 match the pin is left alone, and a verified APK in
`build/aivpn/` is reused instead of being downloaded again.

A download or hash failure only logs a warning and leaves the directory empty — the build still
succeeds (see "Behaviour without the library" below). CI needs no workflow change for this, and
`.github/workflows/android-test.yml` is deliberately not modified by this feature.

For a manual run (offline builds, local debugging) the shell script does the same job:

```sh
tools/fetch-aivpn-so.sh
```

To reuse an APK you already have:

```sh
AIVPN_APK=/path/to/aivpn-android.apk tools/fetch-aivpn-so.sh
```

The downstream APK size grows by roughly 21 MB (12.9 MB arm64 + 8.4 MB armv7a) because
`packaging.jniLibs.useLegacyPackaging = true` keeps both ABIs uncompressed.

### Pinned hashes (v1.2.0)

- APK: `45370f3d505fbdff71ba6c43c90444041ba52aaf64c80b68a2b22adcd0b5c4e9` (27 674 391 bytes)
- `arm64-v8a/libaivpn_core.so`: `5db4a55b0a83f0778dcb4c779e067455a89131aa0aa7ca00e509d744065959dd` (12 891 696 bytes)
- `armeabi-v7a/libaivpn_core.so`: `64d769da3cc1ac520679db8a9dd0de8729d7b15d9a830132710f77a1f68738df` (8 394 632 bytes)

When bumping the AIVPN release, update the pins in **both** places together — `aivpnReleaseUrl`,
`aivpnReleaseSha256`, `aivpnReleaseBytes`, `aivpnLibSha256` and `aivpnLibBytes` in
`app/build.gradle.kts`, and `RELEASE_URL`, `APK_SHA256`, `sha_for_abi`, `size_for_abi` in
`tools/fetch-aivpn-so.sh` — and refresh this table.

### Behaviour without the library

A build produced without running the fetch step is valid: `AivpnJni` catches the
`System.loadLibrary` failure (`UnsatisfiedLinkError` is an `Error`, not an `Exception`), keeps the
reason in `AivpnJni.loadError` and reports `isAvailable == false`. Settings → "AIVPN (experimental)"
then shows the error banner and refuses to connect; the sing-box path is untouched.
