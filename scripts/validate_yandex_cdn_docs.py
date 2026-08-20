import argparse,re
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1];DEFAULT=ROOT/'docs'/'yandex-cdn-whitelist'
REQ={'MASTER_REQUIREMENTS.md','VERIFIED_FACTS.md','RESEARCH.md','ARCHITECTURE.md','SPEC.md','IMPLEMENTATION_PLAN.md','TEST_PLAN.md','TEST_RESULTS.md','CLIENT_COMPATIBILITY.md','TRANSPORT_PRESETS.md','EDGE_LIFECYCLE.md','PANEL_INTEGRATION.md','TRAFFIC_METERING.md','BILLING.md','BILLING_RECONCILIATION.md','SECURITY.md','PRODUCTION_SAFETY.md','DEPLOYMENT.md','ROLLBACK.md','HANDOFF.md','ADR_MAP.md','DEFINITION_OF_DONE.md','TERMINOLOGY.md'};ADRS={f'ADR-{i:04d}.md'for i in range(1,18)};H=('## Problem','## Constraints','## Alternatives','## Trade-offs','## Risks','## Compatibility','## Testing','## Rollback','## Decision','## Evidence');IP=re.compile(r'(?<![\w.])(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}(?![\w.])')
def validate(d):
 e=[]
 if {p.name for p in d.iterdir()if p.is_file()}!=REQ:return['document inventory']
 a=d/'adr'
 if {p.name for p in a.iterdir()if p.is_file()}!=ADRS:e+=['ADR inventory']
 for p in [ROOT/'AGENTS.md',ROOT/'CONTEXT.md',*d.rglob('*.md'),*(ROOT/'scripts').rglob('*.py')]:
  if p.name=='MASTER_REQUIREMENTS.md':continue
  if IP.search(p.read_text(encoding='utf8')):e+=[f'raw IPv4 policy: {p.name}']
 for n in ADRS:
  t=(a/n).read_text(encoding='utf8')
  if 'Status: proposed'not in t or any(x not in t for x in H):e+=[f'ADR structure: {n}']
 f=(d/'VERIFIED_FACTS.md').read_text(encoding='utf8')
 if not all(x in f for x in ('OWNER-PROVIDED ACCEPTANCE CLAIM','CODE/REPO FACT','UNVERIFIED','Source and date')):e+=['facts provenance']
 return e
def main():
 p=argparse.ArgumentParser();p.add_argument('--docs-root',type=Path,default=DEFAULT);e=validate(p.parse_args().docs_root);print('FAILED: '+'; '.join(e)if e else 'OK: docs policy');return bool(e)
if __name__=='__main__':raise SystemExit(main())
