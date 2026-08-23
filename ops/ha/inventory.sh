#!/usr/bin/env bash
set -euo pipefail

umask 077

usage_error() {
  printf '%s\n' 'inventory: explicit fixture root and output are required' >&2
  exit 3
}

fixture_root=''
output=''

while (( $# > 0 )); do
  case "$1" in
    --fixture-root)
      if (( $# < 2 )) || [[ -n "$fixture_root" ]]; then
        usage_error
      fi
      fixture_root=$2
      shift 2
      ;;
    --output)
      if (( $# < 2 )) || [[ -n "$output" ]]; then
        usage_error
      fi
      output=$2
      shift 2
      ;;
    *)
      usage_error
      ;;
  esac
done

if [[ -z "$fixture_root" || -z "$output" ]]; then
  usage_error
fi

if command -v python3 >/dev/null 2>&1; then
  python_bin=python3
elif command -v python >/dev/null 2>&1; then
  python_bin=python
else
  printf '%s\n' 'inventory: Python 3 is required' >&2
  exit 3
fi

"$python_bin" - "$fixture_root" "$output" <<'PY'
import json
import os
import stat
import sys


MAX_FIXTURE_BYTES = 4096
EXPECTED_FILES = {
    "ha-backup.json": "ha",
    "legacy-backup.json": "legacy",
}


def output_error():
    sys.stderr.write("inventory: output must be a new regular file\n")
    raise SystemExit(3)


def strict_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate key")
        result[key] = value
    return result


def identity(info):
    return info.st_dev, info.st_ino


def content_fingerprint(info):
    return (
        info.st_dev,
        info.st_ino,
        info.st_size,
        info.st_mtime_ns,
        info.st_ctime_ns,
    )


def supports_pinned_directory():
    return (
        hasattr(os, "O_DIRECTORY")
        and os.open in os.supports_dir_fd
        and os.stat in os.supports_dir_fd
        and os.stat in os.supports_follow_symlinks
        and os.listdir in os.supports_fd
    )


def read_fixture(root, root_descriptor, filename, expected_kind):
    flags = (
        os.O_RDONLY
        | getattr(os, "O_NOFOLLOW", 0)
        | getattr(os, "O_BINARY", 0)
    )
    if root_descriptor is None:
        path = os.path.join(root, filename)
        info = os.lstat(path)
        descriptor = os.open(path, flags)
    else:
        info = os.stat(filename, dir_fd=root_descriptor, follow_symlinks=False)
        descriptor = os.open(filename, flags, dir_fd=root_descriptor)

    try:
        opened_info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or not stat.S_ISREG(opened_info.st_mode):
            raise ValueError("fixture is not a regular file")
        if identity(info) != identity(opened_info):
            raise ValueError("fixture identity changed before open")
        if opened_info.st_size <= 0 or opened_info.st_size > MAX_FIXTURE_BYTES:
            raise ValueError("fixture size is outside the allowed range")

        chunks = []
        remaining = MAX_FIXTURE_BYTES + 1
        while remaining:
            chunk = os.read(descriptor, remaining)
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        content = b"".join(chunks)
        final_info = os.fstat(descriptor)
        if content_fingerprint(final_info) != content_fingerprint(opened_info):
            raise ValueError("fixture changed while reading")
    finally:
        os.close(descriptor)

    if not content or len(content) > MAX_FIXTURE_BYTES:
        raise ValueError("fixture size changed while reading")

    document = json.loads(
        content.decode("utf-8", errors="strict"),
        object_pairs_hook=strict_object,
    )
    if not isinstance(document, dict):
        raise ValueError("fixture root must be an object")
    if set(document) != {"enabled", "format_version", "kind"}:
        raise ValueError("fixture keys do not match the contract")
    if type(document["enabled"]) is not bool:
        raise ValueError("enabled must be a boolean")
    if type(document["format_version"]) is not int or document["format_version"] != 1:
        raise ValueError("unsupported fixture format")
    if document["kind"] != expected_kind:
        raise ValueError("fixture kind does not match its binding")
    return document["enabled"]


def read_sources(root):
    root_info = os.lstat(root)
    if not stat.S_ISDIR(root_info.st_mode):
        raise ValueError("fixture root is not a direct directory")

    root_descriptor = None
    try:
        if supports_pinned_directory():
            flags = (
                os.O_RDONLY
                | os.O_DIRECTORY
                | getattr(os, "O_NOFOLLOW", 0)
                | getattr(os, "O_BINARY", 0)
            )
            root_descriptor = os.open(root, flags)
            opened_root_info = os.fstat(root_descriptor)
            if not stat.S_ISDIR(opened_root_info.st_mode):
                raise ValueError("opened fixture root is not a directory")
            if identity(opened_root_info) != identity(root_info):
                raise ValueError("fixture root identity changed before open")
            names = os.listdir(root_descriptor)
        else:
            names = os.listdir(root)
            opened_root_info = os.lstat(root)
            if identity(opened_root_info) != identity(root_info):
                raise ValueError("fixture root identity changed before listing")

        if set(names) != set(EXPECTED_FILES):
            raise ValueError("fixture file set does not match the contract")

        enabled = {
            kind: read_fixture(root, root_descriptor, filename, kind)
            for filename, kind in EXPECTED_FILES.items()
        }
        if root_descriptor is None:
            final_root_info = os.lstat(root)
            if identity(final_root_info) != identity(root_info):
                raise ValueError("fixture root identity changed while reading")
        return enabled
    finally:
        if root_descriptor is not None:
            os.close(root_descriptor)


def build_report(implementation, blocker_codes):
    return {
        "backup_implementation": implementation,
        "blocker_codes": blocker_codes,
        "evidence_class": "FIXTURE",
        "format_version": 1,
        "release_readiness": "NO_GO",
    }


def write_new_output(path, report):
    if os.path.lexists(path):
        output_error()

    encoded = (
        json.dumps(report, ensure_ascii=True, separators=(",", ":"), sort_keys=True)
        + "\n"
    ).encode("ascii")
    flags = (
        os.O_CREAT
        | os.O_EXCL
        | os.O_WRONLY
        | getattr(os, "O_NOFOLLOW", 0)
        | getattr(os, "O_BINARY", 0)
    )
    descriptor = None
    try:
        descriptor = os.open(path, flags, 0o600)
        opened_info = os.fstat(descriptor)
        if not stat.S_ISREG(opened_info.st_mode):
            raise OSError("output is not a regular file")
        if hasattr(os, "fchmod"):
            os.fchmod(descriptor, 0o600)
        view = memoryview(encoded)
        while view:
            written = os.write(descriptor, view)
            if written <= 0:
                raise OSError("short output write")
            view = view[written:]
        os.fsync(descriptor)
    except (OSError, ValueError):
        if descriptor is not None:
            try:
                os.close(descriptor)
            except OSError:
                pass
            descriptor = None
        output_error()
    finally:
        if descriptor is not None:
            os.close(descriptor)


fixture_root, output = sys.argv[1:]

if os.path.lexists(output):
    output_error()

try:
    enabled = read_sources(fixture_root)
except (OSError, UnicodeError, ValueError, json.JSONDecodeError, RecursionError):
    report = build_report("unknown", ["fixture_input_invalid"])
    result = 2
else:
    selected = sorted(kind for kind, is_enabled in enabled.items() if is_enabled)
    if selected == ["ha"] or selected == ["legacy"]:
        report = build_report(selected[0], [])
        result = 0
    elif not selected:
        report = build_report("none", ["backup_implementation_missing"])
        result = 2
    else:
        report = build_report("conflict", ["backup_implementation_conflict"])
        result = 2

write_new_output(output, report)
raise SystemExit(result)
PY
