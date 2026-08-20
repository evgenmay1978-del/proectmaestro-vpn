import importlib.util
import json
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
DOCS = ROOT / 'docs' / 'yandex-cdn-whitelist'
REQUIRED_DOCS = {
    'MASTER_REQUIREMENTS.md', 'VERIFIED_FACTS.md', 'RESEARCH.md', 'ARCHITECTURE.md',
    'SPEC.md', 'IMPLEMENTATION_PLAN.md', 'TEST_PLAN.md', 'TEST_RESULTS.md',
    'CLIENT_COMPATIBILITY.md', 'TRANSPORT_PRESETS.md', 'EDGE_LIFECYCLE.md',
    'PANEL_INTEGRATION.md', 'TRAFFIC_METERING.md', 'BILLING.md',
    'BILLING_RECONCILIATION.md', 'SECURITY.md', 'PRODUCTION_SAFETY.md',
    'DEPLOYMENT.md', 'ROLLBACK.md', 'HANDOFF.md', 'ADR_MAP.md',
}
REQUIRED_ADRS = {f'ADR-{number:04d}.md' for number in range(1, 18)}
ADR_HEADINGS = (
    '## Problem', '## Constraints', '## Alternatives', '## Trade-offs',
    '## Risks', '## Compatibility', '## Testing', '## Rollback',
    '## Decision', '## Evidence',
)


def load_redactor():
    spec = importlib.util.spec_from_file_location(
        'render_redacted_baseline', ROOT / 'scripts' / 'render_redacted_baseline.py'
    )
    module = importlib.util.module_from_spec(spec)
    assert spec and spec.loader
    spec.loader.exec_module(module)
    return module


class YandexCdnDocsTest(unittest.TestCase):
    def test_required_document_inventory_exists(self):
        self.assertTrue(DOCS.is_dir())
        self.assertEqual(REQUIRED_DOCS, {path.name for path in DOCS.iterdir() if path.is_file()})
        self.assertEqual(REQUIRED_ADRS, {path.name for path in (DOCS / 'adr').iterdir() if path.is_file()})

    def test_canonical_documents_are_materialized_target_only(self):
        for name in REQUIRED_DOCS - {'MASTER_REQUIREMENTS.md', 'ADR_MAP.md'}:
            text = (DOCS / name).read_text(encoding='utf-8')
            self.assertGreaterEqual(len(text), 350, name)
            self.assertIn('Status: target-only', text, name)
        for path in (DOCS / 'adr').glob('ADR-*.md'):
            text = path.read_text(encoding='utf-8')
            self.assertIn('Status: proposed', text, path.name)
            for heading in ADR_HEADINGS:
                self.assertIn(heading, text, f'{path.name}: {heading}')

    def test_adr_map_links_every_required_record(self):
        text = (DOCS / 'ADR_MAP.md').read_text(encoding='utf-8')
        self.assertIn('Wayfinder', text)
        for name in sorted(REQUIRED_ADRS):
            self.assertIn(name[:-3], text)

    def test_master_contains_non_regression_invariants(self):
        text = (DOCS / 'MASTER_REQUIREMENTS.md').read_text(encoding='utf-8')
        for invariant in (
            'Белые списки = ВЫКЛЮЧЕНО',
            'Не повредить ни одного уже работающего VPN-подключения',
            'WDTT, qWDTT, CSQTT и ' + 'OLC' + 'TRC сейчас отложены',
            'Real charging включается только после отдельного подтверждения',
        ):
            self.assertIn(invariant, text)

    def test_non_master_docs_keep_olc_rtc_spelling_and_do_not_repeat_sensitive_origin(self):
        for path in [ROOT / 'AGENTS.md', ROOT / 'CONTEXT.md', *DOCS.rglob('*.md')]:
            if path.name == 'MASTER_REQUIREMENTS.md':
                continue
            text = path.read_text(encoding='utf-8')
            self.assertNotIn('OLC' + 'TRC', text, path)
            self.assertNotIn('193.17.183.48', text, path)

    def test_validation_fixtures_use_corrected_olc_rtc_spelling(self):
        obsolete = 'OLC' + 'TRC'
        for path in (ROOT / 'scripts' / 'validate_yandex_cdn_docs.py', ROOT / 'scripts' / 'tests' / 'test_yandex_cdn_docs.py'):
            self.assertNotIn(obsolete, path.read_text(encoding='utf-8'), path)

    def test_validator_accepts_document_tree(self):
        result = subprocess.run(
            [sys.executable, 'scripts/validate_yandex_cdn_docs.py'],
            cwd=ROOT, text=True, capture_output=True, check=False,
        )
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)

    def test_validator_rejects_missing_adr_and_malformed_facts(self):
        with tempfile.TemporaryDirectory() as temp:
            copied = pathlib.Path(temp) / 'docs'
            shutil.copytree(DOCS, copied)
            (copied / 'adr' / 'ADR-0017.md').unlink()
            missing = subprocess.run(
                [sys.executable, 'scripts/validate_yandex_cdn_docs.py', '--docs-root', str(copied)],
                cwd=ROOT, text=True, capture_output=True, check=False,
            )
            self.assertNotEqual(0, missing.returncode)
        with tempfile.TemporaryDirectory() as temp:
            copied = pathlib.Path(temp) / 'docs'
            shutil.copytree(DOCS, copied)
            (copied / 'VERIFIED_FACTS.md').write_text('# Verified facts\n', encoding='utf-8')
            malformed = subprocess.run(
                [sys.executable, 'scripts/validate_yandex_cdn_docs.py', '--docs-root', str(copied)],
                cwd=ROOT, text=True, capture_output=True, check=False,
            )
            self.assertNotEqual(0, malformed.returncode)

    def test_redacted_manifest_emits_json_with_adr_hash_coverage(self):
        result = subprocess.run(
            [sys.executable, 'scripts/render_redacted_baseline.py'],
            cwd=ROOT, text=True, capture_output=True, check=False,
        )
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)
        manifest = json.loads(result.stdout)
        self.assertEqual('local-redacted-baseline', manifest['kind'])
        paths = {row['path'] for row in manifest['files']}
        self.assertIn('docs/yandex-cdn-whitelist/adr/ADR-0001.md', paths)
        self.assertTrue(all(len(row['sha256']) == 64 for row in manifest['files']))

    def test_redacted_manifest_masks_sensitive_values_without_logging_plaintext(self):
        module = load_redactor()
        source = (
            'Authorization: Bearer multi word token value\n'
            'token=secret-value\n'
            'vless://secret@example.invalid:443?security=tls\n'
            'hysteria2://password@example.invalid:443\n'
            'ss://opaque@example.invalid:443\n'
            'uuid=123e4567-e89b-12d3-a456-426614174000\n'
            '-----BEGIN PRIVATE KEY-----\nline-one\nline-two\n-----END PRIVATE KEY-----\n'
        )
        rendered = module.redact_text(source)
        for secret in ('multi word token value', 'secret-value', 'vless://', 'hysteria2://', 'ss://',
                       '123e4567-e89b-12d3-a456-426614174000', 'line-one', 'line-two'):
            self.assertNotIn(secret, rendered)
        self.assertGreaterEqual(rendered.count('<REDACTED>'), 6)


if __name__ == '__main__':
    unittest.main()
