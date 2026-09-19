#!/usr/bin/env python3
"""Synchronize a flat origin from the controller's complete client snapshot.

Required environment: FLAT_CDN_CLIENTS_URL, FLAT_CDN_TOKEN_FILE,
FLAT_CDN_CONFIG, FLAT_CDN_XRAY, FLAT_CDN_UNIT, FLAT_CDN_INBOUND_TAG.
Optional: FLAT_CDN_BACKUP_DIR (default /var/backups/maestro-flat-cdn-sync).
Run only in the approved maintenance window when first installing the timer.
"""

import fcntl
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request


UUID_RE = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
MAX_RESPONSE_BYTES = 1024 * 1024


def log(event, **fields):
    print(json.dumps({"event": event, **fields}), flush=True)


def canonical_uuid(value):
    if not isinstance(value, str) or not UUID_RE.fullmatch(value.lower()):
        raise ValueError("invalid client identifier")
    return value.lower()


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate snapshot field")
        result[key] = value
    return result


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Never forward the origin credential to a redirect destination.
        return None


def fetch_clients(url, token_file):
    parsed = urllib.parse.urlsplit(url)
    if (parsed.scheme != "https" and not (
        parsed.scheme == "http" and parsed.hostname in {"localhost", "127.0.0.1", "::1"}
    )) or parsed.username or parsed.password or not parsed.hostname:
        raise ValueError("invalid controller URL")
    with open(token_file, "rb") as handle:
        info = os.fstat(handle.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077:
            raise PermissionError("token file must be root-only")
        raw_token = handle.read(8193)
    if len(raw_token) > 8192:
        raise ValueError("invalid token file")
    token = raw_token.decode("utf-8").strip()
    if not token or any(character.isspace() for character in token):
        raise ValueError("invalid token file")
    request = urllib.request.Request(url, headers={"Authorization": "Bearer " + token})
    with urllib.request.build_opener(NoRedirect()).open(request, timeout=30) as response:
        if response.status != 200:
            raise ValueError("unexpected controller status")
        payload = response.read(MAX_RESPONSE_BYTES + 1)
    if len(payload) > MAX_RESPONSE_BYTES:
        raise ValueError("controller response too large")
    snapshot = json.loads(payload, object_pairs_hook=unique_object)
    if not isinstance(snapshot, dict) or not isinstance(snapshot.get("clients"), list):
        raise ValueError("invalid snapshot")
    rows = snapshot["clients"]
    if type(snapshot.get("count")) is not int or snapshot["count"] != len(rows):
        raise ValueError("incomplete snapshot")
    wanted = set()
    for row in rows:
        if not isinstance(row, dict):
            raise ValueError("invalid client entry")
        identifier = canonical_uuid(row.get("uuid"))
        if identifier in wanted:
            raise ValueError("duplicate client entry")
        if (type(row.get("expires_at_unix")) is not int
                or type(row.get("available_bytes")) is not int
                or row["available_bytes"] <= 0):
            raise ValueError("invalid entitlement metadata")
        # Entitlement and expiry decisions belong to the controller. Do not
        # silently filter its authoritative set using the origin's clock.
        wanted.add(identifier)
    return wanted


def write_candidate(target, data, info):
    fd, name = tempfile.mkstemp(prefix=".flat-cdn-", suffix=".json", dir=target.parent)
    try:
        with os.fdopen(fd, "wb") as handle:
            handle.write(data)
            os.fchown(handle.fileno(), info.st_uid, info.st_gid)
            os.fchmod(handle.fileno(), stat.S_IMODE(info.st_mode))
            handle.flush()
            os.fsync(handle.fileno())
    except BaseException:
        os.unlink(name)
        raise
    return Path(name)


def restart(unit):
    subprocess.run(["systemctl", "restart", unit], check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=45)
    time.sleep(1)
    subprocess.run(["systemctl", "is-active", "--quiet", unit], check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)


def sync():
    target = Path(os.environ["FLAT_CDN_CONFIG"]).resolve(strict=True)
    xray = os.environ["FLAT_CDN_XRAY"]
    unit = os.environ["FLAT_CDN_UNIT"]
    tag = os.environ["FLAT_CDN_INBOUND_TAG"]
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.@-]*\.service", unit):
        raise ValueError("invalid service unit")
    wanted = fetch_clients(os.environ["FLAT_CDN_CLIENTS_URL"], os.environ["FLAT_CDN_TOKEN_FILE"])
    with target.open("rb") as handle:
        info = os.fstat(handle.fileno())
        original = handle.read()
    config = json.loads(original)
    matches = [inbound for inbound in config["inbounds"] if inbound.get("tag") == tag]
    if len(matches) != 1 or matches[0].get("protocol") != "vless":
        raise ValueError("flat inbound not unique")
    inbound = matches[0]
    clients = inbound["settings"]["clients"]
    if not isinstance(clients, list) or not all(isinstance(client, dict) for client in clients):
        raise ValueError("invalid existing clients")
    current = {canonical_uuid(client.get("id")) for client in clients}
    if len(current) != len(clients):
        raise ValueError("duplicate existing clients")
    static = [client for client in clients if client.get("email") == "flat-owner"]
    static_ids = {canonical_uuid(client["id"]) for client in static}
    effective_wanted = wanted | static_ids
    if effective_wanted == current:
        log("unchanged", clients=len(current), managed=len(wanted), static=len(static))
        return

    inbound["settings"]["clients"] = [
        {"id": identifier, "email": identifier} for identifier in sorted(wanted - static_ids)
    ] + static
    updated = (json.dumps(config, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    candidate = write_candidate(target, updated, info)
    try:
        subprocess.run([xray, "run", "-test", "-config", str(candidate)], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
        if target.read_bytes() != original:
            raise RuntimeError("configuration changed during preparation")
        backup_dir = Path(os.environ.get("FLAT_CDN_BACKUP_DIR", "/var/backups/maestro-flat-cdn-sync"))
        backup_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
        backup_info = backup_dir.stat()
        if backup_info.st_uid != 0 or stat.S_IMODE(backup_info.st_mode) != 0o700:
            raise PermissionError("backup directory must be root-only")
        fd, backup_name = tempfile.mkstemp(prefix=time.strftime("%Y%m%dT%H%M%SZ-", time.gmtime()),
                                         suffix=".json", dir=backup_dir)
        with os.fdopen(fd, "wb") as backup:
            backup.write(original)
            backup.flush()
            os.fsync(backup.fileno())
        if target.read_bytes() != original:
            raise RuntimeError("configuration changed before replacement")
        os.replace(candidate, target)
        try:
            restart(unit)
        except Exception as error:
            if target.read_bytes() != updated:
                log("rollback_conflict", error_type=type(error).__name__)
                raise
            rollback = write_candidate(target, original, info)
            try:
                os.replace(rollback, target)
            finally:
                rollback.unlink(missing_ok=True)
            try:
                restart(unit)
            except Exception as rollback_error:
                log("rollback_failed", error_type=type(rollback_error).__name__)
                raise
            log("rolled_back", error_type=type(error).__name__)
            raise
        log("applied", clients=len(effective_wanted), managed=len(wanted), static=len(static),
            added_count=len(effective_wanted - current), removed_count=len(current - effective_wanted))
    finally:
        candidate.unlink(missing_ok=True)


def main():
    if os.geteuid() != 0:
        raise PermissionError("root required")
    with open("/run/maestro-flat-cdn-sync.lock", "w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            log("already_running")
            return
        sync()


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        log("error", error_type=type(error).__name__)
        sys.exit(1)
