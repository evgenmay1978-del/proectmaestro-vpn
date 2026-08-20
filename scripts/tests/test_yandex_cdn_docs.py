import importlib.util, json, pathlib, re, shutil, subprocess, sys, tempfile, unittest
ROOT=pathlib.Path(__file__).resolve().parents[2]; DOCS=ROOT/'docs'/'yandex-cdn-whitelist'
REQUIRED_DOCS={'MASTER_REQUIREMENTS.md','VERIFIED_FACTS.md','RESEARCH.md','ARCHITECTURE.md','SPEC.md','IMPLEMENTATION_PLAN.md','TEST_PLAN.md','TEST_RESULTS.md','CLIENT_COMPATIBILITY.md','TRANSPORT_PRESETS.md','EDGE_LIFECYCLE.md','PANEL_INTEGRATION.md','TRAFFIC_METERING.md','BILLING.md','BILLING_RECONCILIATION.md','SECURITY.md','PRODUCTION_SAFETY.md','DEPLOYMENT.md','ROLLBACK.md','HANDOFF.md','ADR_MAP.md','DEFINITION_OF_DONE.md','TERMINOLOGY.md'}
ADRS={f'ADR-{n:04d}.md' for n in range(1,18)}; HEAD=('## Problem','## Constraints','## Alternatives','## Trade-offs','## Risks','## Compatibility','## Testing','## Rollback','## Decision','## Evidence')
IPV4=re.compile(r'(?<![\w.])(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}(?![\w.])')
def module():
 s=importlib.util.spec_from_file_location('render',ROOT/'scripts'/'render_redacted_baseline.py');m=importlib.util.module_from_spec(s);assert s and s.loader;s.loader.exec_module(m);return m
class T(unittest.TestCase):
 def test_inventory_and_adrs(self):
  self.assertEqual(REQUIRED_DOCS,{p.name for p in DOCS.iterdir() if p.is_file()});self.assertEqual(ADRS,{p.name for p in (DOCS/'adr').iterdir() if p.is_file()})
 def test_master_is_verbatim_owner_source(self):
  t=(DOCS/'MASTER_REQUIREMENTS.md').read_text(encoding='utf8');self.assertIn('WDTT, qWDTT, CSQTT и OLCTRC сейчас отложены',t)
 def test_material_docs_and_distinct_adrs(self):
  decisions=[]
  for n in REQUIRED_DOCS-{'MASTER_REQUIREMENTS.md','ADR_MAP.md','DEFINITION_OF_DONE.md','TERMINOLOGY.md'}:
   self.assertIn('Status: target-only',(DOCS/n).read_text(encoding='utf8'))
  for p in (DOCS/'adr').glob('ADR-*.md'):
   x=p.read_text(encoding='utf8');self.assertIn('Status: proposed',x);[self.assertIn(h,x) for h in HEAD];decisions.append(x.split('## Decision',1)[1].split('## Evidence',1)[0].strip())
  self.assertEqual(17,len(set(decisions)))
 def test_no_raw_ipv4_outside_master(self):
  files=[ROOT/'AGENTS.md',ROOT/'CONTEXT.md',*DOCS.rglob('*.md'),*(ROOT/'scripts').rglob('*.py')]
  for p in files:
   if p.name=='MASTER_REQUIREMENTS.md':continue
   self.assertIsNone(IPV4.search(p.read_text(encoding='utf8')),p)
 def test_validator_rejects_missing_and_extra_docs(self):
  with tempfile.TemporaryDirectory() as q:
   d=pathlib.Path(q)/'docs';shutil.copytree(DOCS,d);(d/'adr'/'ADR-0017.md').unlink();r=subprocess.run([sys.executable,'scripts/validate_yandex_cdn_docs.py','--docs-root',str(d)],cwd=ROOT,text=True,capture_output=True);self.assertNotEqual(0,r.returncode)
   (d/'unexpected.md').write_text('x',encoding='utf8');r=subprocess.run([sys.executable,'scripts/validate_yandex_cdn_docs.py','--docs-root',str(d)],cwd=ROOT,text=True,capture_output=True);self.assertNotEqual(0,r.returncode)
 def test_manifest_schema_hashes_and_tamper(self):
  m=module();manifest=m.build_manifest();self.assertTrue(m.validate_manifest(manifest));manifest['files'][0]['sha256']='0'*64;self.assertFalse(m.validate_manifest(manifest))
 def test_redactor_redacts_before_preview_inline_bearer_and_open_pem(self):
  m=module();secret='very secret token beyond preview';source='{"Authorization":"Bearer '+secret+'"}\n-----BEGIN PRIVATE KEY-----\nmismatch -----END CERTIFICATE-----\nprivate material beyond cutoff\n-----END PRIVATE KEY-----'+'x'*400
  preview=m.safe_preview(source,320);self.assertEqual(320,len(preview));self.assertNotIn(secret,preview);self.assertNotIn('private material',preview);self.assertIn('<REDACTED>',preview)
if __name__=='__main__':unittest.main()
