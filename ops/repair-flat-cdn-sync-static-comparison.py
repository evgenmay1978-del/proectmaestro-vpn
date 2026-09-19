#!/usr/bin/env python3
"""Repair the deployed flat-CDN comparison without touching Xray or its config.

Requires the observed SHA-256 of the existing sync script. The previous script
is backed up with its mode/owner; no service is started, stopped or restarted.
"""

import argparse
import ast
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time


OLD_COMPARE = """    if wanted == current:
        log({"event": "unchanged", "clients": len(wanted)})
"""
NEW_COMPARE = """    # Static clients are retained below, so include them in the no-change check.
    effective_wanted = sorted(set(wanted) | {
        str(client["id"]).strip().lower() for client in STATIC_CLIENTS
    })
    if effective_wanted == current:
        log({"event": "unchanged", "clients": len(current)})
"""
OLD_DRY_RUN = '        log({"event": "dry_run", "added": sorted(set(wanted) - set(current)), "removed": sorted(set(current) - set(wanted))})'
NEW_DRY_RUN = '        log({"event": "dry_run", "added_count": len(set(effective_wanted) - set(current)), "removed_count": len(set(current) - set(effective_wanted))})'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", default="/usr/local/sbin/maestro-flat-cdn-sync")
    parser.add_argument("--expected-sha256", required=True)
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    target = Path(args.target).resolve(strict=True)
    original = target.read_bytes()
    before = hashlib.sha256(original).hexdigest()
    if before != args.expected_sha256:
        raise SystemExit("source digest changed; inspect the deployed script first")
    source = original.decode("utf-8")
    if source.count(OLD_COMPARE) != 1 or source.count(OLD_DRY_RUN) != 1:
        raise SystemExit("source anchors differ; no changes made")
    updated = source.replace(OLD_COMPARE, NEW_COMPARE).replace(OLD_DRY_RUN, NEW_DRY_RUN)
    ast.parse(updated, filename=str(target))
    encoded = updated.encode("utf-8")
    after = hashlib.sha256(encoded).hexdigest()
    if not args.apply:
        print(json.dumps({"event": "prepared", "before": before, "after": after}))
        return
    active = subprocess.run(
        ["systemctl", "is-active", "maestro-flat-cdn-sync.service"],
        capture_output=True, text=True, timeout=10,
    ).stdout.strip()
    if active not in ("inactive", "failed"):
        raise SystemExit("sync is running; let its current run finish before applying")

    info = target.stat()
    backup_dir = Path("/root/flat-cdn-sync-backups")
    backup_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    backup = backup_dir / ("sync-before-static-compare-" + time.strftime("%Y%m%dT%H%M%SZ", time.gmtime()) + ".py")
    if backup.exists():
        raise SystemExit("backup already exists; no changes made")
    shutil.copy2(target, backup)
    os.chmod(backup, 0o600)
    fd, temp_name = tempfile.mkstemp(prefix=".flat-cdn-sync-", dir=target.parent)
    try:
        with os.fdopen(fd, "wb") as out:
            os.fchmod(out.fileno(), info.st_mode & 0o7777)
            os.fchown(out.fileno(), info.st_uid, info.st_gid)
            out.write(encoded)
            out.flush()
            os.fsync(out.fileno())
        if target.read_bytes() != original:
            raise SystemExit("source changed before replacement; no changes made")
        os.replace(temp_name, target)
    finally:
        if os.path.exists(temp_name):
            os.unlink(temp_name)
    if hashlib.sha256(target.read_bytes()).hexdigest() != after:
        raise SystemExit("installed script digest mismatch; backup retained")
    print(json.dumps({"event": "installed", "before": before, "after": after,
                      "backup": str(backup), "services_restarted": False}))


if __name__ == "__main__":
    main()
