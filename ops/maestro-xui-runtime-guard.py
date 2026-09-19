#!/usr/bin/env python3
"""Compare ordinary VLESS clients with live Xray; repair only stable drift.

Read-only unless --repair is explicit. Never log client identifiers or CLI output.
config.json is NOT runtime truth: native x-ui hot-apply does not rewrite it.
"""

import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import time
import uuid


PANEL = Path('/usr/local/x-ui/x-ui')
CORE = Path('/usr/local/x-ui/bin/xray-linux-amd64')
DB = Path('/etc/x-ui/x-ui.db')
CONFIG = Path('/usr/local/x-ui/bin/config.json')
STATE = Path('/var/lib/maestro-xui-guard/state.json')
PANEL_BUILDS = {
    '4d32ca07539eec2083336a4b75036ebc5cbb97b77cf672efb962e0ed6a22b92b': '3.2.8',
    '87d21f986308592fcc416af317e032bec352a8b40c68482e7100ac9ffe87b92e': '3.4.0',
}
CORE_BUILDS = {
    '402c34d5d537a48c858d6d227c1e660eda8bf8abe9d1258d54faa66b8b263b78': '26.6.1',
    '2745bf12d7217c81769b161e44fd1528c05e8ce79176d59a59a4012f1bdadb6b': '26.6.22',
}
CONFIRM_SECONDS = 120
COOLDOWN_SECONDS = 3600


class Skip(Exception):
    """A fixed, non-sensitive reason that cannot authorize a restart."""


def emit(status, **counts):
    print(json.dumps({'status': status, **counts}, sort_keys=True), flush=True)


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()


def file_hash(path):
    with path.open('rb') as handle:
        result = hashlib.sha256()
        for block in iter(lambda: handle.read(1024 * 1024), b''):
            result.update(block)
        return result.hexdigest()


def command(args, timeout=5):
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
    if result.returncode:
        raise Skip('command-unavailable')
    return result.stdout


def processes():
    pid = int(command(['systemctl', 'show', 'x-ui.service', '-p', 'MainPID', '--value']).strip())
    if pid <= 0 or Path('/proc', str(pid), 'exe').resolve() != PANEL:
        raise Skip('panel-process-unavailable')
    children = []
    for proc in Path('/proc').iterdir():
        if not proc.name.isdigit():
            continue
        try:
            fields = (proc / 'stat').read_text().rsplit(')', 1)[1].split()
            if int(fields[1]) == pid and (proc / 'exe').resolve() == CORE:
                children.append((int(proc.name), fields[19]))
        except (FileNotFoundError, ProcessLookupError):
            continue
    if len(children) != 1:
        raise Skip('core-process-unavailable')
    core_pid, started = children[0]
    args = Path('/proc', str(core_pid), 'cmdline').read_bytes().split(b'\0')
    if b'-c' not in args:
        raise Skip('core-config-unrecognized')
    config_arg = Path(os.fsdecode(args[args.index(b'-c') + 1]))
    if not config_arg.is_absolute():
        config_arg = Path('/proc', str(core_pid), 'cwd').resolve() / config_arg
    if config_arg.resolve() != CONFIG:
        raise Skip('core-config-unrecognized')
    return [pid, core_pid, started]


def effective_stats(conn):
    # GetAllInbounds preloads own rows, then backfills missing emails from
    # settings.clients across ALL inbounds. Normalized links alone are not
    # equivalent: sibling inbounds can share a disabled traffic row.
    all_stats = conn.execute('SELECT inbound_id,email,enable FROM client_traffics').fetchall()
    stats, emails, seen, missing = {}, {}, {}, set()
    for inbound in conn.execute('SELECT id,settings FROM inbounds'):
        iid = inbound['id']
        stats[iid] = [row for row in all_stats if row['inbound_id'] == iid]
        clients = json.loads(inbound['settings']).get('clients', [])
        emails[iid] = [client.get('email', '') for client in clients]
        seen[iid] = {row['email'].lower() for row in stats[iid] if row['email']}
        missing.update(email for email in emails[iid] if email and email.lower() not in seen[iid])
    by_lower = {}
    for row in all_stats:
        if row['email'] not in missing:
            continue
        key = row['email'].lower()
        previous = by_lower.get(key)
        if previous is not None and (previous['email'], previous['enable']) != (row['email'], row['enable']):
            raise Skip('ambiguous-shared-client-stats')
        by_lower[key] = row
    for iid in stats:
        for email in emails[iid]:
            key = email.lower()
            if email and key not in seen[iid] and key in by_lower:
                stats[iid].append(by_lower[key])
                seen[iid].add(key)
    return stats


def users_from_db(conn, inbound_id, stats):
    rows = conn.execute(
        'SELECT c.uuid,c.email,c.enable,ci.flow_override FROM clients c '
        'JOIN client_inbounds ci ON ci.client_id=c.id WHERE ci.inbound_id=?',
        (inbound_id,),
    ).fetchall()
    enabled = {row['email']: row['enable'] for row in stats}
    if len(enabled) != len(stats):
        raise Skip('ambiguous-client-stats')
    result = []
    for row in rows:
        if row['enable'] not in (0, 1) or enabled.get(row['email'], 1) not in (0, 1):
            raise Skip('unsupported-client-state')
        if not row['enable'] or not enabled.get(row['email'], 1):
            continue
        # Both supported x-ui versions overwrite c.Flow with flow_override,
        # including an empty override. Traffic/expiry jobs update stats.Enable;
        # GetXrayConfig does not independently compare expiry or byte counters.
        flow = row['flow_override'] or ''
        if flow == 'xtls-rprx-vision-udp443':
            flow = 'xtls-rprx-vision'
        if not row['uuid'] or not row['email']:
            raise Skip('unsupported-client-identity')
        result.append((str(uuid.UUID(row['uuid'])), row['email'], flow))
    if len(set(result)) != len(result):
        raise Skip('duplicate-client-identity')
    return sorted(result)


def snapshot(inbound_ids):
    process = processes()
    config = json.loads(CONFIG.read_text())
    api = config.get('api', {})
    listeners = [i for i in config['inbounds'] if i.get('tag') == api.get('tag')]
    if (len(listeners) != 1 or listeners[0].get('listen') != '127.0.0.1'
            or 'HandlerService' not in api.get('services', [])):
        raise Skip('unsupported-api-binding')
    address = '127.0.0.1:' + str(int(listeners[0]['port']))
    expected, live = {}, {}
    with contextlib.closing(sqlite3.connect('file:' + str(DB) + '?mode=ro', uri=True,
                                           timeout=3)) as conn:
        conn.row_factory = sqlite3.Row
        conn.execute('PRAGMA query_only=ON')
        conn.execute('BEGIN')
        required = {
            'inbounds': {'id', 'tag', 'enable', 'node_id', 'protocol', 'port', 'settings'},
            'clients': {'id', 'uuid', 'email', 'enable'},
            'client_inbounds': {'client_id', 'inbound_id', 'flow_override'},
            'client_traffics': {'inbound_id', 'email', 'enable'},
        }
        for table, columns in required.items():
            actual = {row[1] for row in conn.execute('PRAGMA table_info(' + table + ')')}
            if not columns <= actual:
                raise Skip('unsupported-schema')
        stats = effective_stats(conn)
        for inbound_id in inbound_ids:
            inbound = conn.execute('SELECT * FROM inbounds WHERE id=?', (inbound_id,)).fetchone()
            if (inbound is None or inbound['protocol'] != 'vless'
                    or inbound['enable'] != 1 or inbound['node_id'] is not None):
                raise Skip('unsupported-inbound-scope')
            matches = [i for i in config['inbounds'] if i.get('tag') == inbound['tag']]
            if (len(matches) != 1 or matches[0].get('protocol') != 'vless'
                    or matches[0].get('port') != inbound['port']):
                raise Skip('inbound-definition-changed')
            expected[inbound['tag']] = users_from_db(conn, inbound_id, stats[inbound_id])
    # Release the read transaction before gRPC, so an unavailable API cannot
    # keep a SQLite read lock over an ordinary panel write.
    for tag in expected:
        response = json.loads(command([str(CORE), 'api', 'inbounduser',
                                      '--server=' + address, '-tag=' + tag]))
        users = []
        for user in response.get('users', []):
            account = user.get('account', {})
            if account.get('_TypedMessage_') != 'xray.proxy.vless.Account':
                raise Skip('unsupported-runtime-account')
            users.append((str(uuid.UUID(account['id'])), user.get('email', ''), account.get('flow', '')))
        if len(set(users)) != len(users):
            raise Skip('duplicate-runtime-identity')
        live[tag] = sorted(users)
    if processes() != process:
        raise Skip('process-changed')
    missing = sum(len(set(expected[k]) - set(live[k])) for k in expected)
    extra = sum(len(set(live[k]) - set(expected[k])) for k in expected)
    return {'expected': digest(expected), 'signature': digest([expected, live, process]),
            'missing': missing, 'extra': extra, 'inbounds': len(expected),
            'expected_clients': sum(map(len, expected.values())),
            'live_clients': sum(map(len, live.values()))}


def save_state(state):
    STATE.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = STATE.with_suffix('.tmp')
    with temporary.open('w') as handle:
        os.chmod(temporary, 0o600)
        json.dump(state, handle, sort_keys=True)
        handle.flush()
        os.fsync(handle.fileno())
    temporary.replace(STATE)


def run(args):
    if file_hash(PANEL) not in PANEL_BUILDS or file_hash(CORE) not in CORE_BUILDS:
        raise Skip('unsupported-build')
    current = snapshot(args.inbound_ids)
    counts = {k: v for k, v in current.items() if k not in ('expected', 'signature')}
    if not args.repair:
        emit('observed-drift' if current['missing'] or current['extra'] else 'consistent', **counts)
        return
    state = json.loads(STATE.read_text()) if STATE.exists() else {'version': 1}
    if not isinstance(state, dict) or state.get('version') != 1:
        raise Skip('invalid-state')
    if (not isinstance(state.get('candidate', {}), dict)
            or not isinstance(state.get('last_attempt', 0), (int, float))
            or not isinstance(state.get('candidate', {}).get('since', 0), (int, float))):
        raise Skip('invalid-state')
    if not current['missing'] and not current['extra']:
        state.pop('candidate', None)
        save_state(state)
        emit('consistent', **counts)
        return
    now = time.time()
    if state.get('attempted_expected') == current['expected']:
        emit('repair-already-attempted', **counts)
        return
    if now - state.get('last_attempt', 0) < COOLDOWN_SECONDS:
        emit('cooldown', **counts)
        return
    candidate = state.get('candidate', {})
    if candidate.get('signature') != current['signature'] or now < candidate.get('since', now):
        state['candidate'] = {'signature': current['signature'], 'since': now}
        save_state(state)
        emit('drift-pending', **counts)
        return
    if now - candidate['since'] < CONFIRM_SECONDS:
        emit('drift-pending', **counts)
        return
    if snapshot(args.inbound_ids)['signature'] != current['signature']:
        state.pop('candidate', None)
        save_state(state)
        emit('drift-changed')
        return
    # Latch BEFORE restart, including timeout/failure. No restart loop on an
    # unresolved generation; a new expected client set can retry after cooldown.
    state.update(last_attempt=now, attempted_expected=current['expected'])
    state.pop('candidate', None)
    save_state(state)
    command(['systemctl', 'restart', 'x-ui.service'], timeout=30)
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        time.sleep(2)
        try:
            after = snapshot(args.inbound_ids)
            if not after['missing'] and not after['extra']:
                emit('repaired', inbounds=after['inbounds'], live_clients=after['live_clients'])
                return
        except (Skip, OSError, ValueError, sqlite3.Error, subprocess.SubprocessError):
            pass
    emit('repair-unconfirmed')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inbound-ids', required=True,
                        type=lambda text: sorted({int(x) for x in text.split(',')}))
    parser.add_argument('--repair', action='store_true')
    args = parser.parse_args()
    os.umask(0o077)
    try:
        with contextlib.ExitStack() as stack:
            # Use the SAME flock inode as the completed rotation helper. The
            # existing file is not a busy marker and must never be unlinked.
            for name in ('maestro-ordinary-vless-rotation.lock', 'maestro-xui-guard.lock'):
                handle = stack.enter_context(open('/run/' + name, 'a'))
                try:
                    fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
                except BlockingIOError:
                    emit('operation-busy')
                    return
            run(args)
    except Skip as exc:
        emit(str(exc))
    except (OSError, ValueError, KeyError, TypeError, IndexError, AttributeError, sqlite3.Error,
            subprocess.SubprocessError):
        # Never print exception text: it may contain JSON, tags, or CLI output.
        emit('observation-unavailable')


if __name__ == '__main__':
    main()
