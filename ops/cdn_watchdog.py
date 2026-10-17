#!/usr/bin/env python3
"""Passive CDN evidence only: no restart, lease request, reset, or traffic probe."""
import argparse
import collections
import ctypes
import datetime
import hashlib
import http.client
import json
import logging
import logging.handlers
import os
from pathlib import Path
import re
import shlex
import signal
import socket
import ssl
import struct
import subprocess
import threading
import time
import urllib.parse

LOG_DIR = Path("/var/log/maestro-cdn-watch")
STOP = threading.Event()
CHILDREN = []
PACKETS = collections.Counter()
PACKET_LOCK = threading.Lock()
JOURNAL_ERRORS = re.compile(r"error|failed|failure|deferred|unavailable|timeout|deadline|broken pipe|oom|killed process", re.I)
LOGGER = logging.getLogger("cdn-watch")
NODE = ""

def emit(kind, **fields):
    record = {"at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "node": NODE, "kind": kind}
    record.update(fields)
    LOGGER.info(json.dumps(record, ensure_ascii=True, separators=(",", ":")))

def redact(text):
    text = re.sub(r"(?i)bearer\s+\S+", "Bearer <redacted>", str(text))
    text = re.sub(r"(?i)(?:https?|vless|ss|trojan)://\S+", "<url>", text)
    text = re.sub(r"(?i)(?:token|password|secret|private.?key|authorization|client.?id|uuid)\s*[:=]\s*\S+", "<credential-field>", text)
    text = re.sub(r"(?i)(?:email|login|account|customer|username|nickname)\s*[:=]?\s+[\w@.+-]+", "<identity>", text)
    text = re.sub(r"wl:[^\s>]+", "<managed-user>", text)
    text = re.sub(r"[A-Za-z0-9_+/=-]{24,}", "<opaque>", text)
    text = re.sub(r"\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?", "<ip>", text)
    text = re.sub(r"\[[0-9A-Fa-f:]+\](?::\d+)?", "<ip>", text)
    text = re.sub(r"\b[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+", "<host>", text)
    text = re.sub(r"(?<![A-Za-z0-9])/[^\s<>]{8,}", "<path>", text)
    return text[:1000]

def command(args, timeout=5):
    return subprocess.run(args, capture_output=True, check=True, timeout=timeout).stdout

def unit_state(unit):
    raw = command(["systemctl", "show", unit, "-p", "MainPID", "-p", "ActiveState",
                   "-p", "SubState", "-p", "NRestarts", "-p", "EnvironmentFiles"]).decode()
    return dict(line.split("=", 1) for line in raw.splitlines() if "=" in line)

def environment(unit):
    state = unit_state(unit)
    values = {}
    for path in re.findall(r"(/\S+?)\s+\(ignore_errors=(?:yes|no)\)", state.get("EnvironmentFiles", "")):
        for line in Path(path).read_text().splitlines():
            if not line.strip() or line.lstrip().startswith("#"):
                continue
            key, value = line.split("=", 1)
            words = shlex.split(value)
            if len(words) <= 1:
                values[key] = words[0] if words else ""
    return values

def journal_loop(units):
    while not STOP.is_set():
        child = None
        try:
            args = ["journalctl", "--follow", "--since=-10s", "--no-pager", "-o", "json"]
            for unit in units:
                args += ["-u", unit]
            child = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            CHILDREN.append(child)
            for raw in child.stdout:
                if STOP.is_set():
                    break
                if len(raw) > 65536:
                    emit("journal_record_oversized")
                    continue
                row = json.loads(raw)
                msg = row.get("MESSAGE", "")
                if isinstance(msg, str) and (int(row.get("PRIORITY", "6")) <= 4 or JOURNAL_ERRORS.search(msg)):
                    emit("service_log", unit=row.get("_SYSTEMD_UNIT", ""),
                         journal_time_us=row.get("__REALTIME_TIMESTAMP"),
                         priority=row.get("PRIORITY"), message=redact(msg))
        except Exception as error:
            if not STOP.is_set():
                emit("collector_error", collector="journal", error_type=type(error).__name__)
        finally:
            if child is not None and child.poll() is None:
                child.terminate()
        STOP.wait(5)

def packet_filter(receiver):
    # Classic socket BPF, on AF_PACKET/SOCK_DGRAM network-layer bytes.
    # Retain only IPv4 TCP lifecycle packets for the two CDN ports and HTTP
    # response prefixes. Request bodies/URLs and ordinary VPN ports are dropped.
    instructions = [
        ("", 0x30, 0, 0, 0), ("", 0x54, 0, 0, 0xf0),
        ("", 0x15, 0, "drop", 0x40), ("", 0x30, 0, 0, 9),
        ("", 0x15, 0, "drop", 6), ("", 0x28, 0, 0, 6),
        ("", 0x45, "drop", 0, 0x1fff), ("", 0xb1, 0, 0, 0),
        ("", 0x48, 0, 0, 0), ("", 0x15, "flags", 0, 28080),
        ("", 0x15, "flags", 0, 28081), ("", 0x48, 0, 0, 2),
        ("", 0x15, "flags", 0, 28080), ("", 0x15, 0, "drop", 28081),
        ("flags", 0x50, 0, 0, 13), ("", 0x45, "keep", 0, 7),
        ("", 0x48, 0, 0, 0), ("", 0x15, "body", 0, 28080),
        ("", 0x15, 0, "drop", 28081),
        ("body", 0x03, 0, 0, 0), ("", 0x50, 0, 0, 12),
        ("", 0x54, 0, 0, 0xf0), ("", 0x74, 0, 0, 2),
        ("", 0x61, 0, 0, 0), ("", 0x0c, 0, 0, 0),
        ("", 0x07, 0, 0, 0), ("", 0x40, 0, 0, 0),
        ("", 0x15, "keep", "drop", 0x48545450),
        ("keep", 0x06, 0, 0, 192), ("drop", 0x06, 0, 0, 0),
    ]
    class Filter(ctypes.Structure):
        _fields_ = [("code", ctypes.c_ushort), ("jt", ctypes.c_ubyte),
                    ("jf", ctypes.c_ubyte), ("k", ctypes.c_uint32)]
    class Program(ctypes.Structure):
        _fields_ = [("length", ctypes.c_ushort), ("filters", ctypes.POINTER(Filter))]
    labels = {label: i for i, (label, *_) in enumerate(instructions) if label}
    compiled = []
    for i, (_, code, yes, no, value) in enumerate(instructions):
        yes = labels[yes] - i - 1 if isinstance(yes, str) else yes
        no = labels[no] - i - 1 if isinstance(no, str) else no
        if not (0 <= yes <= 255 and 0 <= no <= 255):
            raise ValueError("filter jump")
        compiled.append(Filter(code, yes, no, value))
    filters = (Filter * len(compiled))(*compiled)
    program = Program(len(compiled), filters)
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.setsockopt(receiver.fileno(), socket.SOL_SOCKET, 26,
                       ctypes.byref(program), ctypes.sizeof(program)) != 0:
        raise OSError(ctypes.get_errno(), "attach capture filter")

def packet_loop():
    while not STOP.is_set():
        receiver = None
        try:
            receiver = socket.socket(socket.AF_PACKET, socket.SOCK_DGRAM, socket.htons(3))
            packet_filter(receiver)
            receiver.settimeout(1)
            last_stats = time.monotonic()
            emit("packet_observer_ready", ports=[28080, 28081], payload_saved=False,
                 implementation="native-filtered-socket")
            while not STOP.is_set():
                if time.monotonic() - last_stats >= 30:
                    captured, dropped = struct.unpack("II", receiver.getsockopt(263, 6, 8))
                    emit("packet_capture_stats", captured_packets=captured, dropped_packets=dropped)
                    last_stats = time.monotonic()
                try:
                    ip = receiver.recv(192)
                except socket.timeout:
                    continue
                if len(ip) < 20 or ip[0] >> 4 != 4 or ip[9] != 6:
                    continue
                tcp = ip[(ip[0] & 15) * 4:]
                if len(tcp) < 20:
                    continue
                source, destination = struct.unpack("!HH", tcp[:4])
                if not {source, destination}.intersection((28080, 28081)):
                    continue
                flags = tcp[13]
                layer = "ingress" if 28080 in (source, destination) else "xray"
                side = "server" if source in (28080, 28081) else "peer"
                status = re.match(rb"HTTP/1\.[01] ([1-5]\d\d)", tcp[(tcp[12] >> 4) * 4:])
                with PACKET_LOCK:
                    for flag, name in ((2, "syn"), (1, "fin"), (4, "rst")):
                        if flags & flag:
                            PACKETS[layer + "." + side + "." + name] += 1
                    if status:
                        code = int(status[1])
                        PACKETS[layer + ".http." + str(code)] += 1
                if status and int(status[1]) >= 400:
                    emit("http_error", layer=layer, status=int(status[1]))
        except Exception as error:
            if not STOP.is_set():
                emit("collector_error", collector="packet", error_type=type(error).__name__,
                     errno=getattr(error, "errno", None))
        finally:
            if receiver is not None:
                receiver.close()
        STOP.wait(5)

def varint(raw, position):
    value = shift = 0
    while position < len(raw) and shift < 70:
        byte = raw[position]
        position += 1
        value |= (byte & 127) << shift
        if byte < 128:
            return value, position
        shift += 7
    raise ValueError("varint")

def fields(raw):
    position = 0
    while position < len(raw):
        tag, position = varint(raw, position)
        wire = tag & 7
        if wire == 0:
            value, position = varint(raw, position)
        elif wire == 2:
            length, position = varint(raw, position)
            value = raw[position:position + length]
            if len(value) != length:
                raise ValueError("protobuf length")
            position += length
        else:
            raise ValueError("protobuf wire")
        yield tag >> 3, value

def counters(env):
    name = env["MAESTRO_XRAY_API_SERVER_NAME"]
    if not re.fullmatch(r"[A-Za-z0-9.-]+", name) or env["MAESTRO_XRAY_API_ADDRESS"] != "127.0.0.1:28082":
        raise ValueError("API binding")
    message = b"\x0a\x0auser>>>wl:"
    frame = b"\x00" + len(message).to_bytes(4, "big") + message
    args = ["curl", "--silent", "--show-error", "--http2", "--noproxy", "*", "--max-time", "3",
            "--max-filesize", "1048576", "--resolve", name + ":28082:127.0.0.1",
            "--cacert", env["MAESTRO_XRAY_API_CA"], "--cert", env["MAESTRO_XRAY_CLIENT_CERT"],
            "--key", env["MAESTRO_XRAY_CLIENT_KEY"], "-H", "Content-Type: application/grpc",
            "-H", "TE: trailers", "--data-binary", "@-",
            "https://" + name + ":28082/xray.app.stats.command.StatsService/QueryStats"]
    result = subprocess.run(args, input=frame, capture_output=True, timeout=4, check=True)
    raw = result.stdout
    if not 5 <= len(raw) <= 1048576 or raw[0] != 0:
        raise ValueError("grpc frame")
    length = int.from_bytes(raw[1:5], "big")
    payload = raw[5:5 + length]
    if len(payload) != length:
        raise ValueError("grpc payload")
    values = {}
    for field, item in fields(payload):
        if field != 1:
            continue
        row = dict(fields(item))
        match = re.fullmatch(r"user>>>(wl:[^:]+:exit-s[1-4])>>>traffic>>>(uplink|downlink)",
                             row.get(1, b"").decode())
        if match:
            values.setdefault(match[1], {})[match[2]] = row.get(2, 0)
    return {email: pair for email, pair in values.items() if len(pair) == 2}

def rqlite_status(env):
    context = ssl.create_default_context(cafile=env["MAESTRO_RQLITE_CA_FILE"])
    context.load_cert_chain(env["MAESTRO_RQLITE_CERT_FILE"], env["MAESTRO_RQLITE_KEY_FILE"])
    output = []
    for index, endpoint in enumerate(env["MAESTRO_RQLITE_ENDPOINTS"].split(",")):
        target = urllib.parse.urlsplit(endpoint)
        if target.scheme != "https" or target.port != 4001 or target.username or target.path not in ("", "/"):
            raise ValueError("rqlite endpoint")
        connection = http.client.HTTPSConnection(target.hostname, target.port, context=context, timeout=3)
        started = time.monotonic()
        try:
            connection.request("GET", "/status")
            response = connection.getresponse()
            raw = response.read(262145)
            if response.status != 200 or len(raw) > 262144:
                raise ValueError("rqlite response")
            value = json.loads(raw).get("store", {})
            raft = value.get("raft", {})
            output.append({"endpoint": index, "seconds": round(time.monotonic() - started, 3),
                           "ready": value.get("ready"), "state": raft.get("state"),
                           "applied_index": raft.get("applied_index"), "commit_index": raft.get("commit_index"),
                           "fsm_pending": raft.get("fsm_pending")})
        except Exception as error:
            output.append({"endpoint": index, "error_type": type(error).__name__,
                           "seconds": round(time.monotonic() - started, 3)})
        finally:
            connection.close()
    return output

def main():
    global NODE
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--node", choices=("S1", "S2", "S3", "S4"), required=True)
    NODE = parser.parse_args().node
    os.umask(0o077)
    LOG_DIR.mkdir(mode=0o700, exist_ok=True)
    handler = logging.handlers.RotatingFileHandler(LOG_DIR / "events.jsonl", maxBytes=8 << 20,
                                                   backupCount=3, encoding="utf-8")
    handler.setFormatter(logging.Formatter("%(message)s"))
    LOGGER.addHandler(handler)
    LOGGER.setLevel(logging.INFO)
    signal.signal(signal.SIGTERM, lambda *_: STOP.set())
    signal.signal(signal.SIGINT, lambda *_: STOP.set())
    units = {
        "S1": ["maestro-panel.service", "maestro-cdn-controller.service", "x-ui.service", "nginx.service"],
        "S2": ["maestro-vless-s2.service", "maestro-xray-cdn-commercial.service",
               "maestro-xray-cdn-commercial-agent.service", "maestro-cdn-rqlite-s2.service"],
        "S3": ["x-ui.service", "maestro-xray-cdn-commercial.service",
               "maestro-xray-cdn-commercial-agent.service", "maestro-cdn-rqlite-s3.service"],
        "S4": ["maestro-cdn-ingress.service", "maestro-xray-cdn-commercial.service",
               "maestro-xray-cdn-commercial-agent.service", "x-ui.service"],
    }[NODE]
    owner_unit = "maestro-cdn-controller.service" if NODE == "S1" else "maestro-xray-cdn-commercial-agent.service"
    threading.Thread(target=journal_loop, args=(units,), daemon=True).start()
    if NODE == "S4":
        threading.Thread(target=packet_loop, daemon=True).start()
    emit("started", version=1, limits={"file_bytes": 8 << 20, "backup_files": 3},
         actions="read-only; no usage challenge, reset, restart, or traffic probe")
    env = {}
    values = {}
    previous_units = {}
    previous_routes = None
    previous_counters = {}
    error_file = None
    error_offset = 0
    error_identity = None
    last_summary = last_inventory = last_counters = 0.0
    while not STOP.is_set():
        now = time.monotonic()
        try:
            if now - last_inventory >= 10:
                for unit in units:
                    status = unit_state(unit)
                    safe = {key: status.get(key) for key in ("ActiveState", "SubState", "MainPID", "NRestarts")}
                    if previous_units.get(unit) != safe:
                        emit("unit_state", unit=unit, **safe)
                        previous_units[unit] = safe
                env = environment(owner_unit)
                if NODE != "S1":
                    config = json.loads(Path(env["MAESTRO_XRAY_CONFIG_FILE"]).read_text())
                    log_path = config.get("log", {}).get("error")
                    error_file = Path(log_path) if log_path and log_path != "none" else None
                last_inventory = now
            if NODE == "S4":
                if now - last_counters >= 5:
                    values = counters(env)
                    for email, pair in values.items():
                        old = previous_counters.get(email)
                        if old and any(pair[k] < old[k] for k in pair):
                            emit("counter_decrease", route=hashlib.sha256(email.encode()).hexdigest()[:16],
                                 exit=email.rsplit(":", 1)[1])
                    for email in previous_counters.keys() - values.keys():
                        emit("counter_pair_disappeared", route=hashlib.sha256(email.encode()).hexdigest()[:16],
                             exit=email.rsplit(":", 1)[1])
                    previous_counters = values
                    last_counters = now
                state = json.loads((Path(env["MAESTRO_RECEIPT_DIRECTORY"]) / "managed-lease-state.json").read_text())
                boot_now = time.clock_gettime_ns(time.CLOCK_BOOTTIME)
                rows = []
                signature = []
                for user in sorted(state.get("users", {}).values(), key=lambda row: row["email"]):
                    email = user["email"]
                    pair = values.get(email)
                    total = sum(pair.values()) if pair else None
                    remaining = (user.get("deadline_boottime_ns", 0) - boot_now) / 1e9
                    credit = user.get("cumulative_byte_ceiling", 0) - total if total is not None else None
                    name = hashlib.sha256(email.encode()).hexdigest()[:16]
                    row = {"route": name, "exit": email.rsplit(":", 1)[1], "phase": user["phase"],
                           "generation": user["generation"], "last_fence": user.get("last_fenced_generation"),
                           "remaining_s": round(remaining, 2), "counters": pair, "remaining_byte_credit": credit}
                    rows.append(row)
                    band = "expired" if remaining <= 0 else "under5" if remaining <= 5 else "under15" if remaining <= 15 else "normal"
                    signature.append((name, user["phase"], user["generation"], band, credit is not None and credit <= 0))
                signature = (tuple(signature), bool(state.get("pending")), len(state.get("final_receipts", {})))
                if signature != previous_routes or now - last_summary >= 30:
                    emit("routes", rows=rows[:128], truncated=len(rows) > 128,
                         pending=bool(state.get("pending")), finals=len(state.get("final_receipts", {})))
                    previous_routes = signature
            if error_file and error_file.exists():
                info = error_file.stat()
                identity = (info.st_dev, info.st_ino)
                if identity != error_identity or info.st_size < error_offset:
                    error_offset = info.st_size if error_identity is None else 0
                    error_identity = identity
                with error_file.open("rb") as source:
                    source.seek(error_offset)
                    raw = source.read(65536)
                complete = raw.rfind(b"\n") + 1
                if complete == 0 and len(raw) == 65536:
                    emit("xray_log_record_oversized")
                    error_offset += len(raw)
                else:
                    error_offset += complete
                for line in raw[:complete].decode(errors="replace").splitlines():
                    if "X-Forwarded-For" in line:
                        with PACKET_LOCK:
                            PACKETS["forwarded_header_ignored"] += 1
                    elif line.strip():
                        emit("xray_log", message=redact(line))
            if now - last_summary >= 30:
                with PACKET_LOCK:
                    packet_counts = dict(PACKETS)
                    PACKETS.clear()
                disk = os.statvfs(LOG_DIR)
                summary = {"loadavg": list(os.getloadavg()), "disk_available_bytes": disk.f_bavail * disk.f_frsize}
                if NODE == "S4":
                    summary["packet_counts"] = packet_counts
                    summary["counter_pairs"] = len(values)
                elif NODE == "S1":
                    summary["rqlite"] = rqlite_status(env)
                emit("heartbeat", **summary)
                last_summary = now
        except Exception as error:
            emit("collector_error", collector="snapshot", error_type=type(error).__name__)
            STOP.wait(4)
        STOP.wait(1)
    for child in CHILDREN:
        if child.poll() is None:
            child.terminate()
    emit("stopped")

if __name__ == "__main__":
    main()
