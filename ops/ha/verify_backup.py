"""Fail-closed offline verifier for synthetic MaestroVPN rqlite backups."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import stat
import subprocess
from typing import Any, Callable


class BackupVerificationError(Exception):
    """A fixed, redacted backup verification failure."""


_ERROR = {
    "input": "backup-verification:invalid-input",
    "members": "backup-verification:invalid-members",
    "manifest": "backup-verification:invalid-manifest",
    "sqlite": "backup-verification:invalid-sqlite",
    "signature": "backup-verification:invalid-signature",
    "leak": "backup-verification:forbidden-content",
}

_BASE_METADATA_KEYS = {
    "format_version",
    "repository_commit_sha",
    "workflow_run_id",
    "rqlite_version",
    "created_at_utc",
    "signing_key_fingerprint",
    "recipient_key_fingerprint",
    "nodes",
}
_V2_BINDING_KEYS = {
    "backup_id",
    "attempt_sequence",
    "captured_generation",
    "lease_fence",
    "object_key",
}
_DERIVED_MANIFEST_KEYS = {
    "schema",
    "source",
    "image",
    "members",
    "table_counts",
    "receipts",
}


def _metadata_keys(format_version: Any) -> set[str] | None:
    if type(format_version) is not int:
        return None
    if format_version == 1:
        return set(_BASE_METADATA_KEYS)
    if format_version == 2:
        return set(_BASE_METADATA_KEYS | _V2_BINDING_KEYS)
    return None
_EXACT_FILES = {
    "control-plane.sqlite3",
    "application-keys.json",
    "manifest.json",
    "manifest.sig",
}
RunGPG = Callable[[list[str]], tuple[int, str, str]]


def _fail(code: str) -> None:
    raise BackupVerificationError(_ERROR[code])


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _canonical(value: Any) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")


def _strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            _fail("manifest")
        result[key] = value
    return result


def _canonical_hex(value: Any, length: int) -> bool:
    if not isinstance(value, str) or len(value) != length or value != value.lower():
        return False
    try:
        bytes.fromhex(value)
    except ValueError:
        return False
    return True




def _object_key(value: Any) -> bool:
    if (
        not isinstance(value, str)
        or not 0 < len(value) <= 1_024
        or value.startswith("/")
        or "\\" in value
        or not all(0x21 <= ord(character) <= 0x7E for character in value)
    ):
        return False
    parts = value.split("/")
    return all(part not in {"", ".", ".."} for part in parts)


def _bound_object_key(
    value: Any,
    *,
    backup_id: str,
    captured_generation: int,
    attempt_sequence: int,
) -> bool:
    tail = f"g-{captured_generation}/a-{attempt_sequence}-{backup_id}.tar.gpg"
    return _object_key(value) and (value == tail or value.endswith("/" + tail))


def _exact_keys(value: Any, keys: set[str]) -> bool:
    return isinstance(value, dict) and set(value) == keys


def _open_image(path: Path) -> sqlite3.Connection:
    try:
        uri = f"{path.resolve().as_uri()}?mode=ro&immutable=1"
        connection = sqlite3.connect(uri, uri=True)
        connection.row_factory = sqlite3.Row
        return connection
    except (OSError, sqlite3.Error, ValueError):
        _fail("sqlite")


def _single_row(connection: sqlite3.Connection, sql: str) -> sqlite3.Row:
    try:
        rows = connection.execute(sql).fetchall()
    except sqlite3.Error:
        _fail("sqlite")
    if len(rows) != 1:
        _fail("sqlite")
    return rows[0]


def _inspect_image(path: Path) -> dict[str, Any]:
    connection = _open_image(path)
    try:
        integrity = connection.execute("PRAGMA integrity_check").fetchall()
        if len(integrity) != 1 or integrity[0][0] != "ok":
            _fail("sqlite")
        if connection.execute("PRAGMA foreign_key_check").fetchall():
            _fail("sqlite")

        migrations = connection.execute(
            "SELECT version,checksum FROM schema_migrations ORDER BY version"
        ).fetchall()
        if not migrations:
            _fail("sqlite")
        migration_items = []
        for expected, row in enumerate(migrations, start=1):
            version, checksum = int(row["version"]), row["checksum"]
            if version != expected or not _canonical_hex(checksum, 64):
                _fail("sqlite")
            migration_items.append({"version": version, "checksum": checksum})
        identity = hashlib.sha256(_canonical(migration_items)[:-1]).hexdigest()

        restore = _single_row(
            connection,
            "SELECT cluster_id,restore_epoch FROM cluster_restore_state "
            "WHERE singleton_id=1",
        )
        cluster_id, epoch = restore["cluster_id"], int(restore["restore_epoch"])
        if not _canonical_hex(cluster_id, 64) or epoch <= 0:
            _fail("sqlite")

        names = [
            row[0]
            for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type='table' "
                "AND name NOT LIKE 'sqlite_%' ORDER BY name"
            )
        ]
        counts = []
        for name in names:
            quoted = '"' + name.replace('"', '""') + '"'
            count = int(connection.execute(f"SELECT COUNT(*) FROM {quoted}").fetchone()[0])
            counts.append({"table": name, "count": count})

        import_high = connection.execute(
            "SELECT COALESCE(MAX(completed_at_unix),0) FROM import_runs"
        ).fetchone()[0]
        batch_row = connection.execute(
            "SELECT COALESCE(MAX(batch_index),0),"
            "COALESCE(MAX(applied_at_unix),0) FROM import_batches"
        ).fetchone()
        watermark = connection.execute(
            "SELECT COALESCE(MAX(created_at_unix),0) FROM backup_watermarks"
        ).fetchone()[0]
        return {
            "schema": {
                "version": len(migration_items),
                "checksum": identity,
                "migrations": migration_items,
            },
            "source": {"cluster_id": cluster_id, "restore_epoch": epoch},
            "table_counts": counts,
            "receipts": {
                "import_completed_at_high_watermark": int(import_high or 0),
                "batch_index_high_watermark": int(batch_row[0] or 0),
                "batch_applied_at_high_watermark": int(batch_row[1] or 0),
                "backup_created_at_high_watermark": int(watermark or 0),
            },
        }
    except (sqlite3.Error, TypeError, ValueError, OverflowError):
        _fail("sqlite")
    finally:
        connection.close()


def _validate_metadata(metadata: Any) -> None:
    if not isinstance(metadata, dict):
        _fail("input")
    format_version = metadata.get("format_version")
    keys = _metadata_keys(format_version)
    if keys is None or not _exact_keys(metadata, keys):
        _fail("input")
    if not _canonical_hex(metadata["repository_commit_sha"], 40):
        _fail("input")
    if type(metadata["workflow_run_id"]) is not int or metadata["workflow_run_id"] <= 0:
        _fail("input")
    if not isinstance(metadata["rqlite_version"], str) or not metadata["rqlite_version"]:
        _fail("input")
    created = metadata["created_at_utc"]
    if not isinstance(created, str) or len(created) != 20 or not created.endswith("Z"):
        _fail("input")
    for field in ("signing_key_fingerprint", "recipient_key_fingerprint"):
        value = metadata[field]
        if not isinstance(value, str) or len(value) != 40 or not value.isalnum() or value != value.upper():
            _fail("input")
    if metadata["nodes"] != ["S2", "S3", "S4"]:
        _fail("input")
    if format_version == 2:
        if (
            not _canonical_hex(metadata["backup_id"], 32)
            or type(metadata["attempt_sequence"]) is not int
            or metadata["attempt_sequence"] <= 0
            or type(metadata["captured_generation"]) is not int
            or metadata["captured_generation"] < 0
            or type(metadata["lease_fence"]) is not int
            or metadata["lease_fence"] <= 0
            or not _bound_object_key(
                metadata["object_key"],
                backup_id=metadata["backup_id"],
                captured_generation=metadata["captured_generation"],
                attempt_sequence=metadata["attempt_sequence"],
            )
        ):
            _fail("input")


def build_manifest(image_path: Path | str, keys_path: Path | str, metadata: dict[str, Any]) -> dict[str, Any]:
    image, keys = Path(image_path), Path(keys_path)
    _validate_metadata(metadata)
    for path in (image, keys):
        try:
            mode = path.lstat().st_mode
        except OSError:
            _fail("input")
        if not stat.S_ISREG(mode) or stat.S_ISLNK(mode):
            _fail("input")
    if image.name != "control-plane.sqlite3" or keys.name != "application-keys.json":
        _fail("input")
    inspected = _inspect_image(image)
    image_item = {"filename": image.name, "size": image.stat().st_size, "sha256": _sha256(image)}
    members = sorted(
        [
            {"filename": image.name, "size": image.stat().st_size, "sha256": _sha256(image)},
            {"filename": keys.name, "size": keys.stat().st_size, "sha256": _sha256(keys)},
        ],
        key=lambda item: item["filename"],
    )
    return {
        **metadata,
        **inspected,
        "image": image_item,
        "members": members,
    }


def _default_gpg(command: list[str]) -> tuple[int, str, str]:
    completed = subprocess.run(
        command,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=30,
    )
    return completed.returncode, completed.stdout, completed.stderr


def _verify_signature(
    manifest: Path,
    signature: Path,
    fingerprint: str,
    gpg_home: Path,
    run_gpg: RunGPG,
) -> None:
    try:
        mode = gpg_home.stat().st_mode
    except OSError:
        _fail("signature")
    if not stat.S_ISDIR(mode) or (os.name != "nt" and stat.S_IMODE(mode) != 0o700):
        _fail("signature")
    command = [
        "gpg",
        "--homedir",
        str(gpg_home),
        "--batch",
        "--no-tty",
        "--status-fd",
        "1",
        "--verify",
        str(signature),
        str(manifest),
    ]
    try:
        code, stdout, _stderr = run_gpg(command)
    except Exception:
        _fail("signature")
    valid = []
    for line in stdout.splitlines():
        fields = line.split()
        if len(fields) >= 3 and fields[0:2] == ["[GNUPG:]", "VALIDSIG"]:
            valid.append(fields[2])
    if code != 0 or valid != [fingerprint]:
        _fail("signature")


def verify_bundle(
    directory: Path | str,
    trusted_signer_fingerprint: str,
    gpg_home: Path | str,
    *,
    run_gpg: RunGPG = _default_gpg,
) -> dict[str, Any]:
    root, home = Path(directory), Path(gpg_home)
    if not isinstance(trusted_signer_fingerprint, str) or len(trusted_signer_fingerprint) != 40:
        _fail("signature")
    try:
        entries = list(root.iterdir())
    except OSError:
        _fail("members")
    names = set()
    for entry in entries:
        if entry.resolve() == home.resolve() and entry.is_dir() and not entry.is_symlink():
            continue
        if entry.name in names or entry.name not in _EXACT_FILES:
            _fail("members")
        names.add(entry.name)
        try:
            mode = entry.lstat().st_mode
        except OSError:
            _fail("members")
        if not stat.S_ISREG(mode) or stat.S_ISLNK(mode):
            _fail("members")
    if names != _EXACT_FILES:
        _fail("members")

    manifest_path = root / "manifest.json"
    try:
        raw = manifest_path.read_bytes()
        manifest = json.loads(raw, object_pairs_hook=_strict_object)
    except BackupVerificationError:
        raise
    except (OSError, UnicodeDecodeError, json.JSONDecodeError):
        _fail("manifest")
    metadata_keys = _metadata_keys(
        manifest.get("format_version") if isinstance(manifest, dict) else None
    )
    if (
        raw != _canonical(manifest)
        or metadata_keys is None
        or not _exact_keys(manifest, metadata_keys | _DERIVED_MANIFEST_KEYS)
    ):
        _fail("manifest")

    metadata = {key: manifest[key] for key in metadata_keys}
    _validate_metadata(metadata)
    _verify_signature(
        manifest_path,
        root / "manifest.sig",
        trusted_signer_fingerprint,
        home,
        run_gpg,
    )
    expected = build_manifest(
        root / "control-plane.sqlite3",
        root / "application-keys.json",
        metadata,
    )
    if manifest != expected:
        _fail("manifest")
    if metadata["format_version"] == 1:
        return {
            "binding_status": "legacy-unbound",
            "format_version": 1,
            "rpo_eligible": False,
            "status": "verified",
        }
    return {
        "backup_id": metadata["backup_id"],
        "attempt_sequence": metadata["attempt_sequence"],
        "binding_status": "signed-attempt",
        "captured_generation": metadata["captured_generation"],
        "format_version": 2,
        "lease_fence": metadata["lease_fence"],
        "object_key": metadata["object_key"],
        "rpo_eligible": False,
        "status": "verified",
    }


def _main() -> int:
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    build = sub.add_parser("build")
    build.add_argument("--image", required=True)
    build.add_argument("--keys", required=True)
    build.add_argument("--metadata", required=True)
    verify = sub.add_parser("verify")
    verify.add_argument("--directory", required=True)
    verify.add_argument("--signer", required=True)
    verify.add_argument("--gpg-home", required=True)
    args = parser.parse_args()
    try:
        if args.command == "build":
            metadata = json.loads(Path(args.metadata).read_text(encoding="utf-8"))
            print(_canonical(build_manifest(args.image, args.keys, metadata)).decode(), end="")
        else:
            print(json.dumps(verify_bundle(args.directory, args.signer, args.gpg_home), separators=(",", ":")))
    except (BackupVerificationError, OSError, json.JSONDecodeError) as error:
        print(str(error))
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(_main())
