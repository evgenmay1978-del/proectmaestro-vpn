# jniLibs — native helpers fetched at build time

This directory is **gitignored** (`.gitignore` line 13). Native binaries are never committed:
CI downloads or builds them before Gradle runs.

## Contents at build time

| File | Source | Fetched by |
|------|--------|------------|
| `<abi>/libmaestro_xhttp.so` | built from `android-xhttp/` (Go, PIE) | `.github/workflows/android-test.yml`, `.github/workflows/android-cdn-apk-build.yml` |
| `<abi>/libaivpn_core.so` | AIVPN v1.2.0 release APK | `tools/fetch-aivpn-so.sh` (also called by `android-test.yml`) |
| `<abi>/libbox.so` | libbox AAR (see `app/libs/`) | Gradle, from the AAR |

## AIVPN (`libaivpn_core.so`)

The experimental AIVPN tunnel is a separate UDP protocol with a Rust core
(<https://github.com/infosave2007/aivpn>, MIT). The Android app loads it through JNI from the
`aivpn_core` library name; the Kotlin side lives in `app/src/main/java/com/aivpn/client/AivpnJni.kt`
and `app/src/main/java/com/maestrovpn/tv/aivpn/`.

Fetch it locally with:

```sh
tools/fetch-aivpn-so.sh
```

The script downloads the pinned APK
(`aivpn-android.apk` from release `v1.2.0`), verifies the APK sha256, extracts
`lib/arm64-v8a/libaivpn_core.so` and `lib/armeabi-v7a/libaivpn_core.so`, verifies each .so sha256
and copies it into `app/src/main/jniLibs/<abi>/`. It is idempotent: files whose sha256 already
matches the pin are left alone. To reuse an APK you already have:

```sh
AIVPN_APK=/path/to/aivpn-android.apk tools/fetch-aivpn-so.sh
```

The downstream APK size grows by roughly 21 MB (12.9 MB arm64 + 8.4 MB armv7a) because
`packaging.jniLibs.useLegacyPackaging = true` keeps both ABIs uncompressed.

### Pinned hashes (v1.2.0)

- APK: `45370f3d505fbdff71ba6c43c90444041ba52aaf64c80b68a2b22adcd0b5c4e9` (27 674 391 bytes)
- `arm64-v8a/libaivpn_core.so`: `5db4a55b0a83f0778dcb4c779e067455a89131aa0aa7ca00e509d744065959dd` (12 891 696 bytes)
- `armeabi-v7a/libaivpn_core.so`: `64d769da3cc1ac520679db8a9dd0de8729d7b15d9a830132710f77a1f68738df` (8 394 632 bytes)

When bumping the AIVPN release, update `RELEASE_URL`, `APK_SHA256`, `sha_for_abi` and
`size_for_abi` in `tools/fetch-aivpn-so.sh` together, and refresh this table.

### Behaviour without the library

A build produced without running the fetch step is valid: `AivpnJni` catches the
`System.loadLibrary` failure (`UnsatisfiedLinkError` is an `Error`, not an `Exception`), keeps the
reason in `AivpnJni.loadError` and reports `isAvailable == false`. Settings → "AIVPN (experimental)"
then shows the error banner and refuses to connect; the sing-box path is untouched.
