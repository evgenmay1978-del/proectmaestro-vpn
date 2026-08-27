import copy, importlib.util, json, pathlib, re, shutil, subprocess, sys, tempfile, unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
DOCS = ROOT / 'docs' / 'yandex-cdn-whitelist'
REQUIRED_DOCS = {
    'MASTER_REQUIREMENTS.md', 'VERIFIED_FACTS.md', 'RESEARCH.md', 'ARCHITECTURE.md',
    'SPEC.md', 'IMPLEMENTATION_PLAN.md', 'TEST_PLAN.md', 'TEST_RESULTS.md',
    'CLIENT_COMPATIBILITY.md', 'TRANSPORT_PRESETS.md', 'EDGE_LIFECYCLE.md',
    'PANEL_INTEGRATION.md', 'TRAFFIC_METERING.md', 'BILLING.md',
    'BILLING_RECONCILIATION.md', 'SECURITY.md', 'PRODUCTION_SAFETY.md',
    'DEPLOYMENT.md', 'ROLLBACK.md', 'HANDOFF.md', 'ADR_MAP.md',
    'DEFINITION_OF_DONE.md', 'TERMINOLOGY.md', 'BASELINE_MANIFEST.json',
}
ADRS = {f'ADR-{n:04d}.md' for n in range(1, 18)}
HEADINGS = ('Problem', 'Constraints', 'Alternatives', 'Trade-offs', 'Risks', 'Compatibility', 'Testing', 'Rollback', 'Decision', 'Evidence')
IPV4 = re.compile(r'(?<![\w.])(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}(?![\w.])')
ADR_TERMS = (
    {'Alternatives':'shared inbound','Decision':'isolated sidecar','Trade-offs':'process','Risks':'configuration drift','Compatibility':'ordinary VPN','Testing':'independent reconciliation','Rollback':'stop the sidecar'},
    {'Alternatives':'panel upgrade','Decision':'round-trip raw fields','Trade-offs':'schema','Risks':'field loss','Compatibility':'ordinary subscription','Testing':'export/import fixture','Rollback':'restore exported config'},
    {'Alternatives':'floating binary','Decision':'pinned checksum','Trade-offs':'upgrade cadence','Risks':'provenance','Compatibility':'config syntax','Testing':'reproduce the binary','Rollback':'prior pinned binary'},
    {'Alternatives':'single universal preset','Decision':'versioned capability preset','Trade-offs':'preset catalog','Risks':'capability mismatch','Compatibility':'client family','Testing':'client matrix','Rollback':'disable the preset'},
    {'Alternatives':'blind retry','Decision':'idle recovery','Trade-offs':'heartbeat','Risks':'idle cutoff','Compatibility':'battery','Testing':'long-lived stream','Rollback':'standard fallback'},
    {'Alternatives':'edit active config','Decision':'immutable release pointer','Trade-offs':'release storage','Risks':'partial reconciliation','Compatibility':'older reconciler','Testing':'selected checksum','Rollback':'previous release pointer'},
    {'Alternatives':'customer UUID','Decision':'opaque stats identity','Trade-offs':'mapping table','Risks':'identity collision','Compatibility':'subscription identity','Testing':'rotation fixture','Rollback':'revoke opaque user'},
    {'Alternatives':'imperative mutation','Decision':'desired-state reconciliation','Trade-offs':'convergence','Risks':'conflicting generation','Compatibility':'ordinary users','Testing':'partial node failure','Rollback':'previous desired generation'},
    {'Alternatives':'first DNS answer','Decision':'approved edge evidence','Trade-offs':'probe window','Risks':'edge churn','Compatibility':'SNI','Testing':'time-separated probes','Rollback':'quarantine the edge'},
    {'Alternatives':'replace subscription','Decision':'additive rendering','Trade-offs':'duplicate control','Risks':'escaping','Compatibility':'ordinary nodes','Testing':'golden subscription','Rollback':'remove only CDN nodes'},
    {'Alternatives':'client counters','Decision':'server-side counters','Trade-offs':'stats polling','Risks':'attribution gap','Compatibility':'third-party clients','Testing':'counter comparison','Rollback':'pause meter posting'},
    {'Alternatives':'monotonic lifetime','Decision':'epoch-scoped delta','Trade-offs':'epoch storage','Risks':'counter reset','Compatibility':'historical usage','Testing':'duplicate sample replay','Rollback':'open a new epoch'},
    {'Alternatives':'direct balance edit','Decision':'immutable ledger entry','Trade-offs':'adjustment rows','Risks':'duplicate charge','Compatibility':'existing wallet','Testing':'idempotency replay','Rollback':'compensating reversal'},
    {'Alternatives':'disable account','Decision':'entitlement-only suspension','Trade-offs':'grace policy','Risks':'existing sessions','Compatibility':'ordinary VPN','Testing':'low-balance transition','Rollback':'resume the entitlement'},
    {'Alternatives':'fixed address list','Decision':'official prefix source','Trade-offs':'atomic allowlist','Risks':'operator lockout','Compatibility':'origin path','Testing':'firewall dry-run','Rollback':'restore prior ruleset'},
    {'Alternatives':'big-bang rollout','Decision':'gated canary','Trade-offs':'observation window','Risks':'abort threshold','Compatibility':'ordinary cohort','Testing':'stage metrics','Rollback':'unpublish the canary'},
    {'Alternatives':'full restore','Decision':'scoped rollback point','Trade-offs':'rollback catalog','Risks':'stale pointer','Compatibility':'ordinary profiles','Testing':'rollback rehearsal','Rollback':'select the prior release'},
)


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    assert spec and spec.loader
    spec.loader.exec_module(module)
    return module


def renderer():
    return load_module('render_redacted_baseline', ROOT / 'scripts' / 'render_redacted_baseline.py')


def validator():
    return load_module('validate_yandex_cdn_docs', ROOT / 'scripts' / 'validate_yandex_cdn_docs.py')


def sections(text):
    result = {}
    for heading in HEADINGS:
        marker = f'## {heading}\n'
        self_text = text.split(marker, 1)[1]
        result[heading] = self_text.split('\n## ', 1)[0].strip()
    return result


class DocumentationTests(unittest.TestCase):
    def test_inventory_and_adrs(self):
        self.assertEqual(REQUIRED_DOCS, {p.name for p in DOCS.iterdir() if p.is_file()})
        self.assertEqual(ADRS, {p.name for p in (DOCS / 'adr').iterdir() if p.is_file()})

    def test_master_is_verbatim_owner_source(self):
        text = (DOCS / 'MASTER_REQUIREMENTS.md').read_text(encoding='utf8')
        self.assertIn('WDTT, qWDTT, CSQTT и OLCTRC сейчас отложены', text)

    def test_material_docs_and_topic_specific_adrs(self):
        decisions = []
        excluded = {'MASTER_REQUIREMENTS.md', 'ADR_MAP.md', 'DEFINITION_OF_DONE.md', 'TERMINOLOGY.md', 'BASELINE_MANIFEST.json'}
        for name in REQUIRED_DOCS - excluded:
            self.assertIn('Status: target-only', (DOCS / name).read_text(encoding='utf8'))
        for number, vocabulary in enumerate(ADR_TERMS, 1):
            path = DOCS / 'adr' / f'ADR-{number:04d}.md'
            text = path.read_text(encoding='utf8')
            self.assertIn('Status: proposed', text)
            parsed = sections(text)
            for heading, term in vocabulary.items():
                self.assertIn(term.casefold(), parsed[heading].casefold(), f'{path.name} {heading}')
            self.assertIn(vocabulary['Risks'].casefold(), parsed['Evidence'].casefold(), f'{path.name} Evidence')
            decisions.append(parsed['Decision'])
        self.assertEqual(17, len(set(decisions)))

    def test_schema_v4_rollback_is_forward_only_and_snapshot_bound(self):
        text = (DOCS / 'ROLLBACK.md').read_text(encoding='utf8').casefold()
        for required in (
            'schema v4',
            'older v3 binary',
            'must not be started',
            'verified pre-v4 rqlite snapshot',
            'fresh empty cluster',
            'same-turn owner approval',
            'production remains no-go',
        ):
            self.assertIn(required, text)

    def test_no_raw_ipv4_outside_master(self):
        files = [ROOT / 'AGENTS.md', ROOT / 'CONTEXT.md', *DOCS.rglob('*.md'), *(ROOT / 'scripts').rglob('*.py')]
        for path in files:
            if path.name == 'MASTER_REQUIREMENTS.md':
                continue
            self.assertIsNone(IPV4.search(path.read_text(encoding='utf8')), path)


class RedactionTests(unittest.TestCase):
    def assert_preview_redacts(self, source, *plaintext):
        preview = renderer().safe_preview(source, 320)
        self.assertLessEqual(len(preview), 320)
        self.assertIn('<REDACT', preview)
        for value in plaintext:
            self.assertNotIn(value, preview)

    def test_redacts_sensitive_syntax_before_truncating(self):
        self.assert_preview_redacts('x' * 300 + '{"password":"visible-prefix-and-rest"}', 'visible-prefix')
        self.assert_preview_redacts('Authorization: Bearer inline-bearer-value', 'inline-bearer-value')
        self.assert_preview_redacts('Bearer bare-bearer-value follows text', 'bare-bearer-value')
        self.assert_preview_redacts('private_key:\n  multiline-first\n  multiline-second\npublic: ok', 'multiline-first', 'multiline-second')
        credential_url = 'https' + '://' + 'user:credential@' + 'example.' + 'test/path?token=query-value'
        self.assert_preview_redacts(credential_url, 'user:credential', 'query-value')
        address = '203.0.' + '113.8'
        host_port = 'cdn.' + 'example.' + 'test' + ':' + '443'
        self.assert_preview_redacts(f'origin={address} next={host_port}', address, host_port)

    def test_public_evidence_url_preserves_markdown_and_sensitive_url_redacts(self):
        module = renderer()
        public_host = 'github.' + 'com'
        public_url = 'https' + '://' + public_host + '/example/project/issues/1'
        public_source = f'[evidence]({public_url}), next.'
        self.assertEqual(public_source, module.safe_preview(public_source))
        private_host = 'private-gateway.' + 'example.' + 'test'
        private_url = 'https' + '://' + private_host + '/private-path'
        private_source = f'endpoint `({private_url})`, next.'
        self.assertEqual('endpoint `(<REDACTED>)`, next.', module.safe_preview(private_source))

    def test_allowlisted_public_url_rejects_query_and_fragment(self):
        module = renderer()
        public_host = 'github.' + 'com'
        public_url = 'https' + '://' + public_host + '/example/project/issues/1'
        signed_url = public_url + '?' + 'X-Amz-' + 'Signature=' + 'synthetic'
        fragment_url = public_url + '#' + 'access_' + 'token=' + 'synthetic'
        source = f'signed [{signed_url}], fragment ({fragment_url}).'
        self.assertEqual('signed [<REDACTED>], fragment (<REDACTED>).', module.safe_preview(source))

    def test_pem_requires_the_matching_punctuated_end_label(self):
        delayed = ('-----BEGIN OPENSSH PRIVATE KEY (TEST)-----\nfirst-private\n'
                   '-----END CERTIFICATE-----\nstill-private\n'
                   '-----END OPENSSH PRIVATE KEY (TEST)-----\npublic-tail')
        self.assert_preview_redacts(delayed, 'first-private', 'still-private')
        missing = '-----BEGIN PGP PRIVATE KEY BLOCK,TEST-----\nmissing-end-private\n' + 'z' * 400
        self.assert_preview_redacts(missing, 'missing-end-private')


class ManifestTests(unittest.TestCase):
    def make_root(self, base):
        root = pathlib.Path(base)
        (root / 'docs' / 'yandex-cdn-whitelist' / 'adr').mkdir(parents=True)
        (root / 'docs' / 'superpowers' / 'plans').mkdir(parents=True)
        for relative, text in (
            ('AGENTS.md', 'agents\n'), ('CONTEXT.md', 'context\n'),
            ('CONTEXT_HANDOFF.md', 'handoff\n'),
            ('docs/superpowers/plans/2026-08-20-yandex-cdn-whitelist.md', 'plan\n'),
            ('docs/yandex-cdn-whitelist/MASTER_REQUIREMENTS.md', 'master\n'),
            ('docs/yandex-cdn-whitelist/SPEC.md', 'spec\n'),
            ('docs/yandex-cdn-whitelist/adr/ADR-0001.md', 'adr\n'),
        ):
            (root / relative).write_text(text, encoding='utf8')
        (root / 'docs' / 'yandex-cdn-whitelist' / 'BASELINE_MANIFEST.json').write_text('{}\n', encoding='utf8')
        return root

    def test_manifest_has_exact_sorted_canonical_rows_and_excludes_itself(self):
        module = renderer()
        with tempfile.TemporaryDirectory() as temp:
            root = self.make_root(temp)
            manifest = module.build_manifest(root)
            paths = [row['path'] for row in manifest['files']]
            self.assertEqual(sorted(paths), paths)
            self.assertEqual(['kind', 'files'], list(manifest))
            self.assertNotIn('docs/yandex-cdn-whitelist/BASELINE_MANIFEST.json', paths)
            self.assertIn('docs/superpowers/plans/2026-08-20-yandex-cdn-whitelist.md', paths)
            self.assertTrue(module.validate_manifest(manifest, root))

    def test_manifest_is_identical_for_lf_and_crlf_copies(self):
        module = renderer()
        with tempfile.TemporaryDirectory() as temp:
            base = pathlib.Path(temp)
            lf_root = self.make_root(base / 'lf')
            for path in module.canonical_paths(lf_root):
                path.write_bytes(path.read_bytes().replace(b'\r\n', b'\n'))
            crlf_root = base / 'crlf'
            shutil.copytree(lf_root, crlf_root)
            for path in module.canonical_paths(crlf_root):
                path.write_bytes(path.read_bytes().replace(b'\n', b'\r\n'))
            lf_manifest = module.build_manifest(lf_root)
            crlf_manifest = module.build_manifest(crlf_root)
            self.assertEqual(lf_manifest, crlf_manifest)
            self.assertTrue(module.validate_manifest(lf_manifest, crlf_root))
            agents = next(row for row in lf_manifest['files'] if row['path'] == 'AGENTS.md')
            self.assertEqual(len(b'agents\n'), agents['bytes'])

    def test_manifest_rejects_every_shape_and_content_tamper(self):
        module = renderer()
        with tempfile.TemporaryDirectory() as temp:
            root = self.make_root(temp)
            original = module.build_manifest(root)
            mutations = []
            scalar = copy.deepcopy(original); scalar['files'][0] = 'row'; mutations.append(scalar)
            missing = copy.deepcopy(original); missing['files'].pop(); mutations.append(missing)
            extra = copy.deepcopy(original); extra['files'].append(copy.deepcopy(extra['files'][0])); extra['files'][-1]['path'] = 'extra.md'; mutations.append(extra)
            duplicate = copy.deepcopy(original); duplicate['files'].append(copy.deepcopy(duplicate['files'][0])); mutations.append(duplicate)
            for field, value in (('bytes', -1), ('sha256', '0' * 64), ('preview', 'forged')):
                changed = copy.deepcopy(original); changed['files'][0][field] = value; mutations.append(changed)
            for changed in mutations:
                self.assertFalse(module.validate_manifest(changed, root))

    def test_validator_loads_persisted_manifest_and_detects_source_tamper(self):
        with tempfile.TemporaryDirectory() as temp:
            temp_root = pathlib.Path(temp)
            shutil.copytree(DOCS, temp_root / 'docs' / 'yandex-cdn-whitelist')
            for name in ('AGENTS.md', 'CONTEXT.md', 'CONTEXT_HANDOFF.md'):
                shutil.copy2(ROOT / name, temp_root / name)
            plan_relative = pathlib.Path('docs/superpowers/plans/2026-08-20-yandex-cdn-whitelist.md')
            (temp_root / plan_relative).parent.mkdir(parents=True)
            shutil.copy2(ROOT / plan_relative, temp_root / plan_relative)
            command = [sys.executable, 'scripts/validate_yandex_cdn_docs.py', '--root', str(temp_root), '--docs-root', str(temp_root / 'docs' / 'yandex-cdn-whitelist')]
            good = subprocess.run(command, cwd=ROOT, text=True, capture_output=True)
            self.assertEqual(0, good.returncode, good.stdout + good.stderr)
            (temp_root / 'CONTEXT.md').write_text('tampered\n', encoding='utf8')
            bad = subprocess.run(command, cwd=ROOT, text=True, capture_output=True)
            self.assertNotEqual(0, bad.returncode)
            self.assertIn('baseline manifest', bad.stdout)


class SecrecyScanTests(unittest.TestCase):
    def test_generic_scan_includes_handoff_and_derivative_scripts(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            docs = root / 'docs' / 'yandex-cdn-whitelist'
            scripts_dir = root / 'scripts' / 'nested'
            docs.mkdir(parents=True); scripts_dir.mkdir(parents=True)
            (root / 'AGENTS.md').write_text('safe\n', encoding='utf8')
            (root / 'CONTEXT.md').write_text('safe\n', encoding='utf8')
            (root / 'CONTEXT_HANDOFF.md').write_text('password = "example-sensitive"\n', encoding='utf8')
            (docs / 'MASTER_REQUIREMENTS.md').write_text('excluded owner source\n', encoding='utf8')
            (scripts_dir / 'tool.py').write_text('token = "example-sensitive"\n', encoding='utf8')
            errors = validator().scan_secrecy(root, docs)
            self.assertTrue(any('CONTEXT_HANDOFF.md' in error for error in errors), errors)
            self.assertTrue(any('tool.py' in error for error in errors), errors)


    def test_endpoint_scan_covers_handoff_docs_scripts_and_manifest(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            docs = root / 'docs' / 'yandex-cdn-whitelist'
            scripts_dir = root / 'scripts' / 'nested'
            docs.mkdir(parents=True); scripts_dir.mkdir(parents=True)
            address = '198.51.' + '100.42'
            hostname = 'edge.' + 'example.' + 'test'
            host_port = hostname + ':443'
            endpoint = 'https' + '://' + hostname + '/path'
            (root / 'AGENTS.md').write_text('safe\n', encoding='utf8')
            (root / 'CONTEXT.md').write_text('safe\n', encoding='utf8')
            (root / 'CONTEXT_HANDOFF.md').write_text(f'origin = {address}\n', encoding='utf8')
            (docs / 'MASTER_REQUIREMENTS.md').write_text('excluded source\n', encoding='utf8')
            (docs / 'DERIVATIVE.md').write_text(f'origin hostname: {hostname}\n', encoding='utf8')
            (docs / 'BASELINE_MANIFEST.json').write_text(json.dumps({'preview': endpoint}), encoding='utf8')
            (scripts_dir / 'tool.py').write_text(f'endpoint = "{host_port}"\n', encoding='utf8')
            errors = validator().scan_secrecy(root, docs)
            for name in ('CONTEXT_HANDOFF.md', 'DERIVATIVE.md', 'BASELINE_MANIFEST.json', 'tool.py'):
                self.assertTrue(any(name in error for error in errors), (name, errors))


    def test_exact_tracked_plan_path_scans_endpoint_and_credential(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            docs = root / 'docs' / 'yandex-cdn-whitelist'
            plan = root / 'docs' / 'superpowers' / 'plans' / '2026-08-20-yandex-cdn-whitelist.md'
            docs.mkdir(parents=True); plan.parent.mkdir(parents=True)
            (root / 'AGENTS.md').write_text('safe\n', encoding='utf8')
            (root / 'CONTEXT.md').write_text('safe\n', encoding='utf8')
            (root / 'CONTEXT_HANDOFF.md').write_text('safe\n', encoding='utf8')
            (docs / 'MASTER_REQUIREMENTS.md').write_text('excluded source\n', encoding='utf8')
            hostname = 'private-plan.' + 'example.' + 'test'
            endpoint = 'https' + '://' + hostname + '/path\n'
            credential_key = 'pass' + 'word'
            credential = credential_key + ' = "' + 'synthetic-value' + '"\n'
            for label, payload in (('endpoint', endpoint), ('credential', credential)):
                with self.subTest(label=label):
                    plan.write_text(payload, encoding='utf8')
                    errors = validator().scan_secrecy(root, docs)
                    self.assertTrue(any(plan.name in error for error in errors), errors)


    def test_bare_hostname_rejected_but_public_evidence_host_allowed(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            docs = root / 'docs' / 'yandex-cdn-whitelist'
            docs.mkdir(parents=True)
            bare_host = 'production-node.' + 'example.' + 'test'
            public_host = 'github.' + 'com'
            public_url = 'https' + '://' + public_host + '/example/project'
            (root / 'AGENTS.md').write_text('safe\n', encoding='utf8')
            (root / 'CONTEXT.md').write_text('safe\n', encoding='utf8')
            (root / 'CONTEXT_HANDOFF.md').write_text('safe\n', encoding='utf8')
            (docs / 'MASTER_REQUIREMENTS.md').write_text('excluded source\n', encoding='utf8')
            (docs / 'BARE.md').write_text(f'{bare_host}\n', encoding='utf8')
            (docs / 'PUBLIC.md').write_text(f'[evidence]({public_url}).\n', encoding='utf8')
            errors = validator().scan_secrecy(root, docs)
            self.assertTrue(any('BARE.md' in error for error in errors), errors)
            self.assertFalse(any('PUBLIC.md' in error for error in errors), errors)

    def test_python_attributes_and_public_github_api_are_not_endpoints(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            docs = root / "docs" / "yandex-cdn-whitelist"
            scripts_dir = root / "scripts"
            docs.mkdir(parents=True); scripts_dir.mkdir()
            (root / "AGENTS.md").write_text("safe\n", encoding="utf8")
            (root / "CONTEXT.md").write_text("safe\n", encoding="utf8")
            (root / "CONTEXT_HANDOFF.md").write_text("safe\n", encoding="utf8")
            (docs / "MASTER_REQUIREMENTS.md").write_text("excluded source\n", encoding="utf8")
            public_api = "https" + "://" + "api.github." + "com"
            python_source = (
                "PATTERN = re.compile(\n"
                "    r'safe'\n"
                ")\n"
                "def guard(node):\n"
                "    return node." + "test\n"
                f'API = "{public_api}"\n'
            )
            (scripts_dir / "tool.py").write_text(python_source, encoding="utf8")
            errors = validator().scan_secrecy(root, docs)
            self.assertFalse(any("tool.py" in error for error in errors), errors)

    def test_re_compile_call_cannot_hide_same_line_secret(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            docs = root / "docs" / "yandex-cdn-whitelist"
            scripts_dir = root / "scripts"
            docs.mkdir(parents=True); scripts_dir.mkdir()
            (root / "AGENTS.md").write_text("safe\n", encoding="utf8")
            (root / "CONTEXT.md").write_text("safe\n", encoding="utf8")
            (root / "CONTEXT_HANDOFF.md").write_text("safe\n", encoding="utf8")
            (docs / "MASTER_REQUIREMENTS.md").write_text("excluded source\n", encoding="utf8")
            secret_key = "to" + "ken"
            secret_value = "synthetic-secret-value"
            cases = (
                ("before", f'{secret_key} = "{secret_value}"; re.compile("safe")\n'),
                ("after", f're.compile("safe"); {secret_key} = "{secret_value}"\n'),
            )
            for label, python_source in cases:
                with self.subTest(label=label):
                    (scripts_dir / "tool.py").write_text(python_source, encoding="utf8")
                    errors = validator().scan_secrecy(root, docs)
                    self.assertTrue(any("tool.py" in error for error in errors), errors)

    def test_re_compile_call_cannot_hide_secret_argument(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            docs = root / "docs" / "yandex-cdn-whitelist"
            scripts_dir = root / "scripts"
            docs.mkdir(parents=True); scripts_dir.mkdir()
            (root / "AGENTS.md").write_text("safe\n", encoding="utf8")
            (root / "CONTEXT.md").write_text("safe\n", encoding="utf8")
            (root / "CONTEXT_HANDOFF.md").write_text("safe\n", encoding="utf8")
            (docs / "MASTER_REQUIREMENTS.md").write_text("excluded source\n", encoding="utf8")
            synthetic_token = "github_" + "pat_" + ("A" * 82)
            python_source = f'LEAK = re.compile("{synthetic_token}")\n'
            (scripts_dir / "tool.py").write_text(python_source, encoding="utf8")

            errors = validator().scan_secrecy(root, docs)

            self.assertTrue(any("tool.py" in error for error in errors), errors)

    def test_indented_and_commented_secret_assignments_are_scanned(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            docs = root / "docs" / "yandex-cdn-whitelist"
            scripts_dir = root / "scripts"
            docs.mkdir(parents=True); scripts_dir.mkdir()
            (root / "AGENTS.md").write_text("safe\n", encoding="utf8")
            (root / "CONTEXT.md").write_text("safe\n", encoding="utf8")
            (root / "CONTEXT_HANDOFF.md").write_text("safe\n", encoding="utf8")
            (docs / "MASTER_REQUIREMENTS.md").write_text("excluded source\n", encoding="utf8")
            secret_key = "pass" + "word"
            secret_value = "synthetic-secret-value"
            cases = (
                ("indented", f'if True:\n    {secret_key} = "{secret_value}"\n'),
                ("inline-comment", f'{secret_key} = "{secret_value}"  # fixture\n'),
            )
            for label, python_source in cases:
                with self.subTest(label=label):
                    (scripts_dir / "tool.py").write_text(python_source, encoding="utf8")
                    errors = validator().scan_secrecy(root, docs)
                    self.assertTrue(any("tool.py" in error for error in errors), errors)




if __name__ == '__main__':
    unittest.main()
