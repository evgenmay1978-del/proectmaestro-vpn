#!/usr/bin/env python3
"""Render a new protected ingress stage; never install or operate services."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys


class Refused(Exception):
    pass


def require(condition, code):
    if not condition:
        raise Refused(code)


def absolute_path(value):
    path = Path(value)
    require(path.is_absolute() and str(path) == value and ".." not in path.parts,
            "input_path_not_canonical")
    return path


def trusted_parents(path, reader_gid=None):
    for parent in (path, *path.parents):
        info = parent.lstat()
        require(stat.S_ISDIR(info.st_mode) and info.st_uid == 0
                and not info.st_mode & 0o022, "untrusted_parent")
        if reader_gid is not None:
            search = 0o010 if info.st_gid == reader_gid else 0o001
            require(bool(info.st_mode & search), "stage_parent_not_searchable")


def read_checked(path, private):
    trusted_parents(path.parent)
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, "rb") as stream:
        info = os.fstat(stream.fileno())
        forbidden = 0o027 if private else 0o022
        require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1
                and info.st_uid == 0 and not info.st_mode & forbidden
                and 0 < info.st_size <= 1024 * 1024, "input_file_not_protected")
        payload = stream.read(1024 * 1024 + 1)
        require(len(payload) <= 1024 * 1024, "input_file_too_large")
    return payload.decode("utf-8")


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate_json_key")
        result[key] = value
    return result


def read_json(path):
    return json.loads(read_checked(path, private=True), object_pairs_hook=unique_object)


def hostname(value):
    require(isinstance(value, str) and 1 <= len(value) <= 253, "hostname_invalid")
    host = value.lower()
    labels = host.split(".")
    label = re.compile(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?")
    require(len(labels) >= 2 and all(label.fullmatch(item) for item in labels)
            and not labels[-1].isdigit(), "hostname_invalid")
    return host


def route_base(value):
    require(isinstance(value, str) and 1 < len(value) <= 1024, "xhttp_path_invalid")
    require(re.fullmatch(r"/[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*/?", value)
            is not None, "xhttp_path_invalid")
    base = value[:-1] if value.endswith("/") else value
    require(all(segment not in {".", ".."} for segment in base[1:].split("/")),
            "xhttp_path_not_canonical")
    return base


def render(canary, material, template):
    require(isinstance(canary, dict) and isinstance(canary.get("inbounds"), list),
            "canary_schema_invalid")
    inbounds = [item for item in canary["inbounds"] if isinstance(item, dict)
                and isinstance(item.get("streamSettings"), dict)
                and item["streamSettings"].get("network") == "xhttp"]
    require(len(inbounds) == 1, "canary_xhttp_not_unique")
    inbound = inbounds[0]
    require(inbound.get("protocol") == "vless" and type(inbound.get("port")) is int
            and inbound["port"] == 18081, "canary_port_or_protocol_mismatch")
    stream = inbound["streamSettings"]
    require(stream.get("security") == "none"
            and isinstance(stream.get("xhttpSettings"), dict), "canary_schema_invalid")
    xhttp = stream["xhttpSettings"]
    expected = {"active_origin_ips", "controller_source_ip", "managed_credentials",
                "public_host", "relay_routes", "secret_path", "server_decryption"}
    require(isinstance(material, dict) and set(material) == expected,
            "commercial_schema_invalid")
    host = hostname(xhttp.get("host"))
    require(host == hostname(material.get("public_host")), "shared_host_mismatch")
    private = route_base(xhttp.get("path"))
    commercial = route_base(material.get("secret_path"))
    require(private != commercial and not private.startswith(commercial + "/")
            and not commercial.startswith(private + "/"), "route_paths_overlap")
    replacements = {"<PUBLIC_HOST>": (host, 1), "<PRIVATE_XHTTP_PATH>": (private, 2),
                    "<COMMERCIAL_XHTTP_PATH>": (commercial, 2)}
    for placeholder, (value, count) in replacements.items():
        require(template.count(placeholder) == count, "template_placeholder_mismatch")
        template = template.replace(placeholder, value)
    require("<" not in template and ">" not in template, "template_placeholder_unresolved")
    return template.encode("utf-8")


def prepare_stage(path, payload, gid):
    trusted_parents(path.parent, reader_gid=gid)
    require(not os.path.lexists(path), "stage_already_exists")
    os.mkdir(path, 0o700)
    os.chown(path, 0, gid)
    os.chmod(path, 0o750)
    descriptor = os.open(path / "nginx.conf", os.O_WRONLY | os.O_CREAT | os.O_EXCL
                         | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(payload)
        stream.flush()
        os.fchown(stream.fileno(), 0, gid)
        os.fchmod(stream.fileno(), 0o640)
        os.fsync(stream.fileno())
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--canary-config", default="/opt/maestro-xray-cdn/config.json")
    parser.add_argument("--commercial-material", required=True)
    parser.add_argument("--template", required=True)
    parser.add_argument("--stage-dir", required=True)
    args = parser.parse_args()
    try:
        require(sys.platform == "linux" and os.geteuid() == 0, "linux_root_required")
        import grp
        gid = grp.getgrnam("www-data").gr_gid
        os.umask(0o077)
        payload = render(read_json(absolute_path(args.canary_config)),
                         read_json(absolute_path(args.commercial_material)),
                         read_checked(absolute_path(args.template), private=False))
        prepare_stage(absolute_path(args.stage_dir), payload, gid)
    except Refused as error:
        print("REFUSED " + str(error), file=sys.stderr)
        return 1
    except (OSError, ValueError, KeyError, TypeError):
        print("REFUSED protected_input_or_stage_error", file=sys.stderr)
        return 1
    print("STAGED config_sha256=" + hashlib.sha256(payload).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
