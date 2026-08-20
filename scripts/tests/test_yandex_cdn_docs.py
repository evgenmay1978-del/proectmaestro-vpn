import importlib.util
import json
import pathlib
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
    'DEPLOYMENT.md', 'ROLLBACK.md', 'HANDOFF.md',
}

class YandexCdnDocsTest(unittest.TestCase):
    def test_required_document_inventory_exists(self):
        self.assertTrue(DOCS.is_dir())
        self.assertEqual(REQUIRED_DOCS, {path.name for path in DOCS.iterdir() if path.is_file()})

    def test_required_scripts_exist(self):
        self.assertTrue((ROOT / 'scripts' / 'validate_yandex_cdn_docs.py').is_file())
        self.assertTrue((ROOT / 'scripts' / 'render_redacted_baseline.py').is_file())

    def test_master_contains_non_regression_invariants(self):
        text = (DOCS / 'MASTER_REQUIREMENTS.md').read_text(encoding='utf-8')
        for invariant in (
            'Белые списки = ВЫКЛЮЧЕНО',
            'Не повредить ни одного уже работающего VPN-подключения',
            'WDTT, qWDTT, CSQTT и OLCTRC сейчас отложены',
            'Real charging включается только после отдельного подтверждения',
        ):
            self.assertIn(invariant, text)

    def test_validator_accepts_document_tree(self):
        result = subprocess.run(
            [sys.executable, 'scripts/validate_yandex_cdn_docs.py'],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)

    def test_redacted_manifest_emits_json_with_windows_console_encoding(self):
        result = subprocess.run(
            [sys.executable, 'scripts/render_redacted_baseline.py'],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertEqual('local-redacted-baseline', json.loads(result.stdout)['kind'])
    def test_redacted_manifest_masks_sensitive_values(self):
        spec = importlib.util.spec_from_file_location(
            'render_redacted_baseline', ROOT / 'scripts' / 'render_redacted_baseline.py'
        )
        module = importlib.util.module_from_spec(spec)
        assert spec and spec.loader
        spec.loader.exec_module(module)
        source = 'token=secret-value\\nvless://secret@example.invalid:443?security=tls\\nuuid=123e4567-e89b-12d3-a456-426614174000\\n'
        rendered = module.redact_text(source)
        self.assertNotIn('secret-value', rendered)
        self.assertNotIn('vless://', rendered)
        self.assertNotIn('123e4567-e89b-12d3-a456-426614174000', rendered)
        self.assertIn('<REDACTED>', rendered)

if __name__ == '__main__':
    unittest.main()
