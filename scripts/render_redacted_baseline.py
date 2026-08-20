import hashlib
import json
import re
from pathlib import Path
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[1]
DOCS_REL = Path('docs/yandex-cdn-whitelist')
MANIFEST_REL = DOCS_REL / 'BASELINE_MANIFEST.json'
TRACKED_PLAN_REL = Path('docs/superpowers/plans/2026-08-20-yandex-cdn-whitelist.md')
ROOT_GOVERNANCE = ('AGENTS.md', 'CONTEXT.md', 'CONTEXT_HANDOFF.md')
SENSITIVE_KEY = r'(?:authorization|access[_ -]?token|auth[_ -]?token|refresh[_ -]?token|token|password|passwd|secret|client[_ -]?secret|api[_ -]?key|private[_ -]?key|credential)'

PUBLIC_EVIDENCE_HOSTS = frozenset({'github.com', 'githubusercontent.com', 'raw.githubusercontent.com', 'objects.githubusercontent.com'})
PEM = re.compile(r'-----BEGIN (?P<label>[A-Z0-9][A-Z0-9 ._+,:/()\-]{0,120})-----[\s\S]*?(?:-----END (?P=label)-----|\Z)')
JSON_SECRET = re.compile(rf'(?is)(?P<prefix>["\']{SENSITIVE_KEY}["\']\s*:\s*)(?P<quote>["\'])(?:\\.|(?!(?P=quote))[\s\S])*(?P=quote)')
ASSIGN_SECRET = re.compile(rf'(?im)(?P<prefix>\b{SENSITIVE_KEY}\b\s*[:=]\s*)(?P<value>[^\r\n]*(?:\r?\n[ \t]+[^\r\n]*)*)')
BEARER = re.compile(r'(?i)\bbearer[ \t]+[A-Za-z0-9._~+/=\-]+')
URL = re.compile(r'(?i)\b[a-z][a-z0-9+.-]{1,20}://[^\s<>"\']+')
URL_CREDENTIALS = re.compile(r'(?i)\b[^\s/@:]+:[^\s/@]+@(?:[a-z0-9-]+\.)*[a-z0-9-]+')
QUERY_SECRET = re.compile(rf'(?i)([?&]{SENSITIVE_KEY}=)[^&#\s]+')
UUID = re.compile(r'\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b', re.I)
IPV4 = re.compile(r'(?<![\w.])(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}(?![\w.])')
HOST_PORT = re.compile(r'(?i)\b(?:[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?):\d{1,5}\b')
HOSTNAME = re.compile(r'(?i)\b(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+(?:[a-z]{2,63})\b')
URL_TRAILING_PUNCTUATION = ".,;:!?)]}`"


def split_url_token(value):
    core = value
    while core and core[-1] in URL_TRAILING_PUNCTUATION:
        core = core[:-1]
    return core, value[len(core):]


def is_public_evidence_host(value):
    return value.lower().rstrip('.') in PUBLIC_EVIDENCE_HOSTS


def is_public_evidence_url(value):
    core, _ = split_url_token(value)
    try:
        parsed = urlsplit(core)
        port = parsed.port
    except ValueError:
        return False
    if parsed.scheme.lower() != 'https' or not parsed.hostname or port is not None:
        return False
    if parsed.username is not None or parsed.password is not None or parsed.query or parsed.fragment:
        return False
    if not is_public_evidence_host(parsed.hostname):
        return False
    return True


def redact_text(value):
    value = PEM.sub('<REDACTED>', value)
    value = JSON_SECRET.sub(lambda match: match.group('prefix') + match.group('quote') + '<REDACTED>' + match.group('quote'), value)
    value = BEARER.sub('Bearer <REDACTED>', value)
    public_urls = {}

    def redact_url(match):
        token = match.group(0)
        core, suffix = split_url_token(token)
        if is_public_evidence_url(core):
            placeholder = f'PUBLIC_EVIDENCE_URL_{len(public_urls)}'
            public_urls[placeholder] = core
            return placeholder + suffix
        return '<REDACTED>' + suffix

    value = URL.sub(redact_url, value)
    value = ASSIGN_SECRET.sub(lambda match: match.group('prefix') + '<REDACTED>', value)
    value = URL_CREDENTIALS.sub('<REDACTED>', value)
    value = QUERY_SECRET.sub(lambda match: match.group(1) + '<REDACTED>', value)
    value = UUID.sub('<REDACTED>', value)
    value = IPV4.sub('<REDACTED>', value)
    value = HOST_PORT.sub('<REDACTED>', value)
    value = HOSTNAME.sub(lambda match: match.group(0) if is_public_evidence_host(match.group(0)) else '<REDACTED>', value)
    for placeholder, url in public_urls.items():
        value = value.replace(placeholder, url)
    return value


def safe_preview(value, limit=320):
    return redact_text(value)[:limit]


def canonical_bytes(raw):
    """Normalize UTF-8 text to LF before size, hash, and preview."""
    return raw.replace(b'\r\n', b'\n').replace(b'\r', b'\n')


def canonical_paths(root=ROOT):
    root = Path(root).resolve()
    docs = root / DOCS_REL
    paths = [root / name for name in ROOT_GOVERNANCE]
    paths.append(root / TRACKED_PLAN_REL)
    paths.extend(path for path in docs.rglob('*.md') if path != root / MANIFEST_REL)
    if any(not path.is_file() for path in paths):
        raise FileNotFoundError('canonical baseline path is missing')
    return sorted(paths, key=lambda path: path.relative_to(root).as_posix())


def build_manifest(root=ROOT):
    root = Path(root).resolve()
    rows = []
    for path in canonical_paths(root):
        raw = canonical_bytes(path.read_bytes())
        rows.append({'path': path.relative_to(root).as_posix(), 'bytes': len(raw), 'sha256': hashlib.sha256(raw).hexdigest(), 'preview': safe_preview(raw.decode('utf8'))})
    return {'kind': 'local-redacted-baseline', 'files': rows}


def validate_manifest(manifest, root=ROOT):
    try:
        if not isinstance(manifest, dict) or list(manifest) != ['kind', 'files']:
            return False
        if manifest['kind'] != 'local-redacted-baseline' or not isinstance(manifest['files'], list):
            return False
        expected = build_manifest(root)
        rows = manifest['files']
        if any(not isinstance(row, dict) for row in rows):
            return False
        if any(list(row) != ['path', 'bytes', 'sha256', 'preview'] for row in rows):
            return False
        if any(isinstance(row['bytes'], bool) or not isinstance(row['bytes'], int) for row in rows):
            return False
        paths = [row['path'] for row in rows]
        if any(not isinstance(path, str) for path in paths) or len(paths) != len(set(paths)):
            return False
        return manifest == expected
    except (KeyError, OSError, TypeError, UnicodeError):
        return False


def main():
    print(json.dumps(build_manifest(), ensure_ascii=True, indent=2))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
