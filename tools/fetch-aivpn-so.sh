#!/usr/bin/env bash
# Fetch the AIVPN native core (libaivpn_core.so) from the pinned upstream release.
#
# The .so files are NOT committed: app/src/main/jniLibs/ is gitignored, and the
# AIVPN tunnel is an experimental protocol that must not bloat the repository.
# CI and local builds call this script before Gradle; without the library the app
# still builds and runs - AivpnJni.isAvailable stays false and the AIVPN screen
# reports a missing library instead of crashing.
#
# Pin: aivpn v1.2.0 (github.com/infosave2007/aivpn). Bump RELEASE_URL,
# APK_SHA256 and the per-ABI hashes below together.
#
# Usage:
#   tools/fetch-aivpn-so.sh                                   # download the pinned APK
#   AIVPN_APK=/path/aivpn-android.apk tools/fetch-aivpn-so.sh # reuse a local APK
#
# Idempotent: an .so whose sha256 already matches is left untouched.
set -euo pipefail

RELEASE_URL="https://github.com/infosave2007/aivpn/releases/download/v1.2.0/aivpn-android.apk"
APK_SHA256="45370f3d505fbdff71ba6c43c90444041ba52aaf64c80b68a2b22adcd0b5c4e9"
APK_SIZE=27674391

ABIS=(arm64-v8a armeabi-v7a)

# Expected hash and size of lib/<abi>/libaivpn_core.so inside the pinned APK.
sha_for_abi() {
    case "$1" in
        arm64-v8a)   echo "5db4a55b0a83f0778dcb4c779e067455a89131aa0aa7ca00e509d744065959dd" ;;
        armeabi-v7a) echo "64d769da3cc1ac520679db8a9dd0de8729d7b15d9a830132710f77a1f68738df" ;;
        *) echo "" ;;
    esac
}
size_for_abi() {
    case "$1" in
        arm64-v8a)   echo "12891696" ;;
        armeabi-v7a) echo "8394632" ;;
        *) echo "" ;;
    esac
}

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)
jni_root="$repo_root/app/src/main/jniLibs"
cache_dir="${AIVPN_CACHE_DIR:-$repo_root/.gradle/aivpn-cache}"

log() { printf '[fetch-aivpn-so] %s\n' "$*"; }
die() { printf '[fetch-aivpn-so] error: %s\n' "$*" >&2; exit 1; }

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | cut -d' ' -f1
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 "$1" | sed 's/^.*= //'
    else
        die "no sha256 tool: install coreutils (sha256sum), perl (shasum) or openssl"
    fi
}

file_size() {
    if stat -c %s "$1" >/dev/null 2>&1; then stat -c %s "$1"; else stat -f %z "$1"; fi
}

verify_apk() {
    local apk="$1" actual size
    actual=$(sha256_of "$apk")
    if [ "$actual" != "$APK_SHA256" ]; then
        die "APK sha256 mismatch: got $actual, expected $APK_SHA256"
    fi
    size=$(file_size "$apk")
    if [ "$size" != "$APK_SIZE" ]; then
        die "APK size mismatch: got $size bytes, expected $APK_SIZE"
    fi
}

obtain_apk() {
    if [ -n "${AIVPN_APK:-}" ]; then
        [ -f "$AIVPN_APK" ] || die "AIVPN_APK does not exist: $AIVPN_APK"
        log "using local APK $AIVPN_APK"
        APK="$AIVPN_APK"
        verify_apk "$APK"
        return
    fi

    mkdir -p "$cache_dir"
    APK="$cache_dir/aivpn-android-v1.2.0.apk"
    if [ -f "$APK" ] && [ "$(sha256_of "$APK")" = "$APK_SHA256" ]; then
        log "cached APK matches the pinned sha256: $APK"
        return
    fi

    local tmp="$APK.part"
    rm -f "$tmp"
    log "downloading $RELEASE_URL"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 30 -o "$tmp" "$RELEASE_URL" \
            || die "download failed: $RELEASE_URL"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$tmp" "$RELEASE_URL" || die "download failed: $RELEASE_URL"
    else
        die "neither curl nor wget is available"
    fi
    verify_apk "$tmp"
    mv -f "$tmp" "$APK"
    log "APK verified: sha256 $APK_SHA256"
}

extract_so() {
    local apk="$1" abi="$2" out="$3" entry="lib/$abi/libaivpn_core.so"
    if command -v unzip >/dev/null 2>&1; then
        unzip -p "$apk" "$entry" >"$out"
    elif command -v python3 >/dev/null 2>&1; then
        python3 - "$apk" "$entry" "$out" <<'PY'
import sys, zipfile
apk, entry, out = sys.argv[1], sys.argv[2], sys.argv[3]
with zipfile.ZipFile(apk) as zf, open(out, "wb") as fh:
    fh.write(zf.read(entry))
PY
    else
        die "neither unzip nor python3 is available"
    fi
    [ -s "$out" ] || die "could not extract $entry from $apk"
}

mkdir -p "$jni_root"
APK=""
obtain_apk

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/aivpn-so.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT

installed=0
skipped=0
for abi in "${ABIS[@]}"; do
    expected_sha=$(sha_for_abi "$abi")
    expected_size=$(size_for_abi "$abi")
    [ -n "$expected_sha" ] || die "unsupported ABI: $abi"

    dest_dir="$jni_root/$abi"
    dest="$dest_dir/libaivpn_core.so"

    if [ -f "$dest" ] && [ "$(sha256_of "$dest")" = "$expected_sha" ]; then
        log "$abi: libaivpn_core.so already up to date, skipping"
        skipped=$((skipped + 1))
        continue
    fi

    work="$tmp_dir/libaivpn_core-$abi.so"
    extract_so "$APK" "$abi" "$work"

    actual_sha=$(sha256_of "$work")
    actual_size=$(file_size "$work")
    if [ "$actual_sha" != "$expected_sha" ] || [ "$actual_size" != "$expected_size" ]; then
        die "$abi: extracted libaivpn_core.so mismatch (sha256 $actual_sha size $actual_size, expected sha256 $expected_sha size $expected_size)"
    fi

    mkdir -p "$dest_dir"
    rm -f "$dest"
    cp -f "$work" "$dest"
    log "$abi: installed $dest (sha256 $actual_sha, $actual_size bytes)"
    installed=$((installed + 1))
done

log "done: installed=$installed skipped=$skipped (jniLibs root: $jni_root)"
