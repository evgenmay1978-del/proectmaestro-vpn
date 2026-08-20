import argparse
import importlib.util
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEFAULT = ROOT / 'docs' / 'yandex-cdn-whitelist'
REQ = {'MASTER_REQUIREMENTS.md','VERIFIED_FACTS.md','RESEARCH.md','ARCHITECTURE.md','SPEC.md','IMPLEMENTATION_PLAN.md','TEST_PLAN.md','TEST_RESULTS.md','CLIENT_COMPATIBILITY.md','TRANSPORT_PRESETS.md','EDGE_LIFECYCLE.md','PANEL_INTEGRATION.md','TRAFFIC_METERING.md','BILLING.md','BILLING_RECONCILIATION.md','SECURITY.md','PRODUCTION_SAFETY.md','DEPLOYMENT.md','ROLLBACK.md','HANDOFF.md','ADR_MAP.md','DEFINITION_OF_DONE.md','TERMINOLOGY.md','BASELINE_MANIFEST.json'}
ADRS = {f'ADR-{i:04d}.md' for i in range(1, 18)}
HEADINGS = ('## Problem','## Constraints','## Alternatives','## Trade-offs','## Risks','## Compatibility','## Testing','## Rollback','## Decision','## Evidence')
IPV4 = re.compile(r'(?<![\w.])(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}(?![\w.])')
SECRET_PATTERNS = (
    re.compile(r'(?im)^\s*["\']?(?:token|password|passwd|secret|client[_ -]?secret|api[_ -]?key|private[_ -]?key|credential)["\']?\s*[:=]\s*["\'][^"\'\r\n]{4,}["\']\s*[,};]?\s*$'),
    re.compile(r'(?im)^\s*(?:authorization\s*[:=]\s*)?bearer\s+[A-Za-z0-9._~+/=\-]{8,}\s*$'),
    re.compile(r'(?im)^\s*-----BEGIN [^\r\n]*PRIVATE KEY[^\r\n]*-----\s*$'),
    re.compile(r'(?im)^\s*(?:(?:[-*]\s*)|(?:(?:url|uri|endpoint|origin)\s*[:=]\s*["\']?))?https?://[^/\s:@]+:[^/\s@]+@[^\r\n]*$'),
    re.compile(r'(?im)^\s*(?:(?:[-*]\s*)|(?:(?:url|uri|endpoint|origin)\s*[:=]\s*["\']?))?https?://[^\r\n]*[?&](?:token|password|secret|api[_ -]?key)=[^&#\s]+[^\r\n]*$'),
)
def renderer_module():
    path = Path(__file__).with_name('render_redacted_baseline.py')
    spec = importlib.util.spec_from_file_location('render_redacted_baseline_for_validator', path)
    module = importlib.util.module_from_spec(spec)
    assert spec and spec.loader
    spec.loader.exec_module(module)
    return module


def secrecy_paths(root, docs_root):
    candidates = [root / 'AGENTS.md', root / 'CONTEXT.md', root / 'CONTEXT_HANDOFF.md']
    candidates.extend(path for path in docs_root.rglob('*.md') if path.name != 'MASTER_REQUIREMENTS.md')
    scripts = root / 'scripts'
    if scripts.is_dir():
        candidates.extend(path for path in scripts.rglob('*') if path.is_file() and path.suffix.lower() in {'.py', '.sh', '.ps1', '.json', '.yaml', '.yml'})
        repro = scripts / 'repro'
        if repro.is_dir():
            candidates.extend(path for path in repro.rglob('*') if path.is_file())
    return sorted({path.resolve() for path in candidates if path.is_file()})


def scan_secrecy(root, docs_root):
    errors = []
    for path in secrecy_paths(Path(root), Path(docs_root)):
        text = path.read_text(encoding='utf8')
        if any(pattern.search(text) for pattern in SECRET_PATTERNS):
            errors.append(f'sensitive literal: {path.relative_to(root)}')
    return errors


def validate(docs_root, root=ROOT):
    root = Path(root).resolve(); docs_root = Path(docs_root).resolve(); errors = []
    if {path.name for path in docs_root.iterdir() if path.is_file()} != REQ:
        errors.append('document inventory')
    adr_root = docs_root / 'adr'
    if not adr_root.is_dir() or {path.name for path in adr_root.iterdir() if path.is_file()} != ADRS:
        errors.append('ADR inventory')
    raw_ip_paths = [root / 'AGENTS.md', root / 'CONTEXT.md', *docs_root.rglob('*.md'), *(root / 'scripts').rglob('*.py')]
    for path in raw_ip_paths:
        if path.name != 'MASTER_REQUIREMENTS.md' and path.is_file() and IPV4.search(path.read_text(encoding='utf8')):
            errors.append(f'raw IPv4 policy: {path.name}')
    for name in ADRS:
        path = adr_root / name
        if path.is_file():
            text = path.read_text(encoding='utf8')
            if 'Status: proposed' not in text or any(heading not in text for heading in HEADINGS):
                errors.append(f'ADR structure: {name}')
    facts = docs_root / 'VERIFIED_FACTS.md'
    if facts.is_file() and not all(term in facts.read_text(encoding='utf8') for term in ('OWNER-PROVIDED ACCEPTANCE CLAIM','CODE/REPO FACT','UNVERIFIED','Source and date')):
        errors.append('facts provenance')
    errors.extend(scan_secrecy(root, docs_root))
    manifest_path = docs_root / 'BASELINE_MANIFEST.json'
    try:
        manifest = json.loads(manifest_path.read_text(encoding='utf8'))
    except (OSError, UnicodeError, json.JSONDecodeError):
        errors.append('baseline manifest: unreadable')
    else:
        if not renderer_module().validate_manifest(manifest, root):
            errors.append('baseline manifest: mismatch')
    return errors


def main():
    parser = argparse.ArgumentParser(); parser.add_argument('--root', type=Path, default=ROOT); parser.add_argument('--docs-root', type=Path)
    args = parser.parse_args(); docs_root = args.docs_root or args.root / 'docs' / 'yandex-cdn-whitelist'
    errors = validate(docs_root, args.root)
    print('FAILED: ' + '; '.join(errors) if errors else 'OK: docs policy')
    return bool(errors)


if __name__ == '__main__':
    raise SystemExit(main())