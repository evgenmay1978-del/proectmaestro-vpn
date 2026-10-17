"""Strict, no-retry rqlite API used only by the isolated DR restore drill."""

from __future__ import annotations

import http.client
import json
import os
import re
import ssl
import stat
from pathlib import Path
from typing import Any, Callable
from urllib.parse import urlsplit


class RestoreAPIError(RuntimeError):
    pass


_NODES = [
    {"node_id": "S2", "endpoint": "https://127.0.0.1:4401"},
    {"node_id": "S3", "endpoint": "https://127.0.0.1:4403"},
    {"node_id": "S4", "endpoint": "https://127.0.0.1:4405"},
]
_CONFIG_KEYS = {"format_version", "nodes", "ca", "client_cert", "client_key"}
_MAX_RESPONSE = 1_048_576
_MAX_IMAGE = 536_870_912
_EMPTY_QUERY = [
    [
        "SELECT name FROM sqlite_master "
        "WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name"
    ]
]


def _error(code: str) -> RestoreAPIError:
    return RestoreAPIError(f"restore-api:{code}")


def _regular_private_file(value: Any) -> Path:
    if not isinstance(value, str) or not value:
        raise _error("invalid-config")
    path = Path(value)
    try:
        info = path.lstat()
        resolved = path.resolve(strict=True)
    except OSError as exc:
        raise _error("invalid-config") from exc
    expected_mode = 0o666 if os.name == "nt" else 0o600
    if (
        not path.is_absolute()
        or resolved != path
        or not stat.S_ISREG(info.st_mode)
        or path.is_symlink()
        or stat.S_IMODE(info.st_mode) != expected_mode
    ):
        raise _error("invalid-config")
    return path


def _validate_config(config: Any) -> tuple[list[tuple[str, int]], Path, Path, Path]:
    if (
        not isinstance(config, dict)
        or set(config) != _CONFIG_KEYS
        or config.get("format_version") != 1
        or config.get("nodes") != _NODES
    ):
        raise _error("invalid-config")
    endpoints: list[tuple[str, int]] = []
    for expected in _NODES:
        parsed = urlsplit(expected["endpoint"])
        if (
            parsed.scheme != "https"
            or parsed.hostname != "127.0.0.1"
            or parsed.path
            or parsed.query
            or parsed.fragment
            or parsed.port is None
        ):
            raise _error("invalid-config")
        endpoints.append((parsed.hostname, parsed.port))
    ca = _regular_private_file(config["ca"])
    cert = _regular_private_file(config["client_cert"])
    key = _regular_private_file(config["client_key"])
    return endpoints, ca, cert, key


def _default_context(ca: Path, cert: Path, key: Path) -> ssl.SSLContext:
    try:
        context = ssl.create_default_context(ssl.Purpose.SERVER_AUTH, cafile=str(ca))
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.check_hostname = True
        context.verify_mode = ssl.CERT_REQUIRED
        context.load_cert_chain(certfile=str(cert), keyfile=str(key))
        return context
    except (OSError, ssl.SSLError, ValueError) as exc:
        raise _error("invalid-config") from exc


def _read_response(response: Any) -> bytes:
    if response.status != 200:
        raise _error("http-rejected")
    length = response.getheader("Content-Length")
    if length is not None:
        if not length.isascii() or not length.isdigit() or int(length) > _MAX_RESPONSE:
            raise _error("http-rejected")
    body = response.read(_MAX_RESPONSE + 1)
    if len(body) > _MAX_RESPONSE:
        raise _error("http-rejected")
    return body


def _connection(
    factory: Callable[..., Any],
    endpoint: tuple[str, int],
    context: Any,
) -> Any:
    host, port = endpoint
    return factory(host, port, context=context, timeout=10)


def inspect_empty(
    config: dict[str, Any],
    *,
    connection_factory: Callable[..., Any] = http.client.HTTPSConnection,
    context_factory: Callable[[Path, Path, Path], Any] = _default_context,
) -> bool:
    endpoints, ca, cert, key = _validate_config(config)
    context = context_factory(ca, cert, key)
    body = (json.dumps(_EMPTY_QUERY, separators=(",", ":")) + "\n").encode()
    headers = {
        "Content-Type": "application/json",
        "Content-Length": str(len(body)),
        "Accept": "application/json",
    }
    empty = True
    for endpoint in endpoints:
        connection = _connection(connection_factory, endpoint, context)
        try:
            connection.request("POST", "/db/query?level=strong", body=body, headers=headers)
            raw = _read_response(connection.getresponse())
        except RestoreAPIError:
            raise
        except (OSError, http.client.HTTPException) as exc:
            raise _error("inspect-failed") from exc
        finally:
            connection.close()
        try:
            payload = json.loads(raw)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise _error("invalid-response") from exc
        if not isinstance(payload, dict) or set(payload) != {"results"}:
            raise _error("invalid-response")
        results = payload["results"]
        if not isinstance(results, list) or len(results) != 1:
            raise _error("invalid-response")
        result = results[0]
        if (
            not isinstance(result, dict)
            or result.get("columns") != ["name"]
            or result.get("types") != ["text"]
            or not set(result).issubset({"columns", "types", "values"})
        ):
            raise _error("invalid-response")
        values = result.get("values", [])
        if not isinstance(values, list):
            raise _error("invalid-response")
        if values:
            empty = False
    return empty


def inspect_restored(
    config: dict[str, Any],
    manifest: dict[str, Any],
    *,
    connection_factory: Callable[..., Any] = http.client.HTTPSConnection,
    context_factory: Callable[[Path, Path, Path], Any] = _default_context,
) -> None:
    endpoints, ca, cert, key = _validate_config(config)
    if not isinstance(manifest, dict):
        raise _error("invalid-manifest")
    schema = manifest.get("schema")
    counts = manifest.get("table_counts")
    if not isinstance(schema, dict) or not isinstance(counts, list):
        raise _error("invalid-manifest")
    migrations = schema.get("migrations")
    if (
        set(schema) != {"version", "checksum", "migrations"}
        or not isinstance(schema.get("version"), int)
        or not isinstance(schema.get("checksum"), str)
        or not re.fullmatch(r"[a-f0-9]{64}", schema["checksum"])
        or not isinstance(migrations, list)
        or len(migrations) != schema["version"]
        or not migrations
    ):
        raise _error("invalid-manifest")
    expected_migrations = []
    for expected_version, item in enumerate(migrations, start=1):
        if (
            not isinstance(item, dict)
            or set(item) != {"version", "checksum"}
            or item.get("version") != expected_version
            or not isinstance(item.get("checksum"), str)
            or not re.fullmatch(r"[a-f0-9]{64}", item["checksum"])
        ):
            raise _error("invalid-manifest")
        expected_migrations.append([expected_version, item["checksum"]])

    queries: list[list[str]] = [
        ["SELECT version,checksum FROM schema_migrations ORDER BY version"]
    ]
    expected_results: list[dict[str, Any]] = [
        {
            "columns": ["version", "checksum"],
            "types": ["integer", "text"],
            "values": expected_migrations,
        }
    ]
    previous = ""
    for item in counts:
        if (
            not isinstance(item, dict)
            or set(item) != {"table", "count"}
            or not isinstance(item.get("table"), str)
            or not re.fullmatch(r"[a-z_][a-z0-9_]*", item["table"])
            or item["table"] <= previous
            or not isinstance(item.get("count"), int)
            or isinstance(item["count"], bool)
            or item["count"] < 0
        ):
            raise _error("invalid-manifest")
        previous = item["table"]
        queries.append([f'SELECT COUNT(*) AS row_count FROM "{item["table"]}"'])
        expected_results.append(
            {"columns": ["row_count"], "types": ["integer"], "values": [[item["count"]]]}
        )

    body = (json.dumps(queries, separators=(",", ":")) + "\n").encode()
    headers = {
        "Content-Type": "application/json",
        "Content-Length": str(len(body)),
        "Accept": "application/json",
    }
    context = context_factory(ca, cert, key)
    for endpoint in endpoints:
        connection = _connection(connection_factory, endpoint, context)
        try:
            connection.request("POST", "/db/query?level=strong", body=body, headers=headers)
            raw = _read_response(connection.getresponse())
        except RestoreAPIError:
            raise
        except (OSError, http.client.HTTPException) as exc:
            raise _error("readback-failed") from exc
        finally:
            connection.close()
        try:
            payload = json.loads(raw)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise _error("readback-failed") from exc
        if payload != {"results": expected_results}:
            raise _error("readback-mismatch")


def load_sqlite(
    config: dict[str, Any],
    image_path: Path | str,
    *,
    connection_factory: Callable[..., Any] = http.client.HTTPSConnection,
    context_factory: Callable[[Path, Path, Path], Any] = _default_context,
) -> None:
    endpoints, ca, cert, key = _validate_config(config)
    image = _regular_private_file(str(image_path))
    try:
        size = image.stat().st_size
        if size <= 16 or size > _MAX_IMAGE:
            raise _error("invalid-image")
        content = image.read_bytes()
    except OSError as exc:
        raise _error("invalid-image") from exc
    if len(content) != size or not content.startswith(b"SQLite format 3\x00"):
        raise _error("invalid-image")
    context = context_factory(ca, cert, key)
    connection = _connection(connection_factory, endpoints[0], context)
    sent = False
    try:
        sent = True
        connection.request(
            "POST",
            "/db/load",
            body=content,
            headers={
                "Content-Type": "application/octet-stream",
                "Content-Length": str(size),
                "Accept": "application/json",
            },
        )
        raw = _read_response(connection.getresponse())
    except RestoreAPIError:
        raise
    except (OSError, http.client.HTTPException) as exc:
        raise _error("unknown-load-outcome" if sent else "load-failed") from exc
    finally:
        connection.close()
    try:
        payload = json.loads(raw)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise _error("load-rejected") from exc
    if payload != {"results": []}:
        raise _error("load-rejected")
