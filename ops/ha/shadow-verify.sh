#!/usr/bin/env bash
set -euo pipefail

legacy_file=''
candidate_file=''
salt_file=''

while [[ $# -gt 0 ]]; do
  case "$1" in
    --legacy)
      [[ $# -ge 2 ]] || exit 3
      legacy_file=$2
      shift 2
      ;;
    --candidate)
      [[ $# -ge 2 ]] || exit 3
      candidate_file=$2
      shift 2
      ;;
    --salt-file)
      [[ $# -ge 2 ]] || exit 3
      salt_file=$2
      shift 2
      ;;
    *)
      exit 3
      ;;
  esac
done

if [[ -z "$legacy_file" || -z "$candidate_file" || -z "$salt_file" ]]; then
  exit 3
fi
if [[ ! -f "$legacy_file" || ! -r "$legacy_file" || ! -f "$candidate_file" || ! -r "$candidate_file" || ! -f "$salt_file" || ! -r "$salt_file" ]]; then
  exit 3
fi

python3 - "$legacy_file" "$candidate_file" "$salt_file" <<'PY'
import hashlib
import hmac
import json
import os
import re
import stat
import sys

HEX64 = re.compile(r"^[0-9a-f]{64}$")
TOP_KEYS = {
    "schema_version",
    "customers",
    "orders",
    "settings_fingerprint",
    "principals_fingerprint",
    "ota_manifest",
}
CUSTOMER_KEYS = {
    "identity_hmac",
    "expires_at_unix",
    "generation",
    "protocol_tags",
    "nodes",
    "maestro_url_shape",
    "karing_url_shape",
}
ORDER_KEYS = {"order_hmac", "state", "result_expires_at_unix"}
OTA_KEYS = {"version_code", "version_name", "apk_sha256", "apk_size"}


class InvalidExport(Exception):
    pass


def require_exact_keys(value, expected):
    if not isinstance(value, dict) or set(value) != expected:
        raise InvalidExport()


def require_hex64(value):
    if not isinstance(value, str) or not HEX64.fullmatch(value):
        raise InvalidExport()


def require_nonnegative_integer(value):
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise InvalidExport()


def require_string_list(value):
    if not isinstance(value, list) or any(not isinstance(item, str) or not item for item in value):
        raise InvalidExport()
    if len(set(value)) != len(value):
        raise InvalidExport()


def validate_customer(customer):
    require_exact_keys(customer, CUSTOMER_KEYS)
    require_hex64(customer["identity_hmac"])
    require_nonnegative_integer(customer["expires_at_unix"])
    require_nonnegative_integer(customer["generation"])
    require_string_list(customer["protocol_tags"])
    require_string_list(customer["nodes"])
    maestro_shape = customer["maestro_url_shape"]
    karing_shape = customer["karing_url_shape"]
    if not isinstance(maestro_shape, str) or not maestro_shape.startswith("maestro://") or "{opaque-token}" not in maestro_shape:
        raise InvalidExport()
    if not isinstance(karing_shape, str) or not karing_shape.startswith("https://") or "{opaque-token}" not in karing_shape:
        raise InvalidExport()


def validate_order(order):
    require_exact_keys(order, ORDER_KEYS)
    require_hex64(order["order_hmac"])
    if not isinstance(order["state"], str) or not order["state"]:
        raise InvalidExport()
    require_nonnegative_integer(order["result_expires_at_unix"])


def load_export(path):
    info = os.stat(path, follow_symlinks=False)
    if not stat.S_ISREG(info.st_mode) or info.st_size <= 0 or info.st_size > 16 * 1024 * 1024:
        raise InvalidExport()
    with open(path, "r", encoding="utf-8") as handle:
        value = json.load(handle)
        if handle.read(1):
            raise InvalidExport()
    require_exact_keys(value, TOP_KEYS)
    if value["schema_version"] != 1:
        raise InvalidExport()
    if not isinstance(value["customers"], list) or not isinstance(value["orders"], list):
        raise InvalidExport()
    for customer in value["customers"]:
        validate_customer(customer)
    for order in value["orders"]:
        validate_order(order)
    require_hex64(value["settings_fingerprint"])
    require_hex64(value["principals_fingerprint"])
    ota = value["ota_manifest"]
    require_exact_keys(ota, OTA_KEYS)
    require_nonnegative_integer(ota["version_code"])
    require_nonnegative_integer(ota["apk_size"])
    require_hex64(ota["apk_sha256"])
    if not isinstance(ota["version_name"], str) or not ota["version_name"]:
        raise InvalidExport()
    return value


def load_salt(path):
    info = os.stat(path, follow_symlinks=False)
    if not stat.S_ISREG(info.st_mode) or info.st_size <= 0 or info.st_size > 4096:
        raise InvalidExport()
    if stat.S_IMODE(info.st_mode) & 0o077:
        raise InvalidExport()
    with open(path, "rb") as handle:
        salt = handle.read(4097)
    if not salt or len(salt) > 4096:
        raise InvalidExport()
    return salt


def index_unique(rows, key):
    result = {}
    for row in rows:
        identity = row[key]
        if identity in result:
            raise InvalidExport()
        result[identity] = row
    return result


def canonical_customer(customer):
    return {
        "expires_at_unix": customer["expires_at_unix"],
        "generation": customer["generation"],
        "protocol_tags": sorted(customer["protocol_tags"]),
        "nodes": sorted(customer["nodes"]),
        "maestro_url_shape": customer["maestro_url_shape"],
        "karing_url_shape": customer["karing_url_shape"],
    }


def canonical_order(order):
    return {
        "state": order["state"],
        "result_expires_at_unix": order["result_expires_at_unix"],
    }


def subject(salt, raw):
    return hmac.new(salt, raw.encode("utf-8"), hashlib.sha256).hexdigest()


def append_difference(differences, salt, field, raw_subject):
    differences.append({"field": field, "subject": subject(salt, raw_subject)})


def compare_index(differences, salt, label, left, right, canonical):
    if len(left) != len(right):
        append_difference(differences, salt, label + ".count", label)
    for identity in sorted(set(left) | set(right)):
        if identity not in left or identity not in right:
            append_difference(differences, salt, label + ".presence", identity)
            continue
        left_value = canonical(left[identity])
        right_value = canonical(right[identity])
        for field in sorted(left_value):
            if left_value[field] != right_value[field]:
                append_difference(differences, salt, label + "." + field, identity)


def compare(legacy, candidate, salt):
    differences = []
    legacy_customers = index_unique(legacy["customers"], "identity_hmac")
    candidate_customers = index_unique(candidate["customers"], "identity_hmac")
    compare_index(differences, salt, "customers", legacy_customers, candidate_customers, canonical_customer)
    legacy_orders = index_unique(legacy["orders"], "order_hmac")
    candidate_orders = index_unique(candidate["orders"], "order_hmac")
    compare_index(differences, salt, "orders", legacy_orders, candidate_orders, canonical_order)
    for field in ("settings_fingerprint", "principals_fingerprint", "ota_manifest"):
        if legacy[field] != candidate[field]:
            append_difference(differences, salt, field, field)
    differences.sort(key=lambda item: (item["field"], item["subject"]))
    return differences


def main():
    if len(sys.argv) != 4:
        return 3
    try:
        salt = load_salt(sys.argv[3])
        legacy = load_export(sys.argv[1])
        candidate = load_export(sys.argv[2])
        differences = compare(legacy, candidate, salt)
    except (InvalidExport, OSError, UnicodeError, json.JSONDecodeError, ValueError, TypeError):
        print("shadow-verify: invalid or unavailable explicit input", file=sys.stderr)
        return 3
    report = {"differences": differences, "status": "mismatch" if differences else "match"}
    json.dump(report, sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return 2 if differences else 0


raise SystemExit(main())
PY
