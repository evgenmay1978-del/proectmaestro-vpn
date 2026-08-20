import argparse
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]; DEFAULT=ROOT/'docs'/'yandex-cdn-whitelist'
REQ={'MASTER_REQUIREMENTS.md','VERIFIED_FACTS.md','RESEARCH.md','ARCHITECTURE.md','SPEC.md','IMPLEMENTATION_PLAN.md','TEST_PLAN.md','TEST_RESULTS.md','CLIENT_COMPATIBILITY.md','TRANSPORT_PRESETS.md','EDGE_LIFECYCLE.md','PANEL_INTEGRATION.md','TRAFFIC_METERING.md','BILLING.md','BILLING_RECONCILIATION.md','SECURITY.md','PRODUCTION_SAFETY.md','DEPLOYMENT.md','ROLLBACK.md','HANDOFF.md','ADR_MAP.md'}
ADRS={f'ADR-{n:04d}.md' for n in range(1,18)}; LEGACY_OLC_RTC='OLC'+'TRC'; HEAD=('## Problem','## Constraints','## Alternatives','## Trade-offs','## Risks','## Compatibility','## Testing','## Rollback','## Decision','## Evidence')
INV=('Белые списки = ВЫКЛЮЧЕНО','Не повредить ни одного уже работающего VPN-подключения','WDTT, qWDTT, CSQTT и '+LEGACY_OLC_RTC+' сейчас отложены','Real charging включается только после отдельного подтверждения')
def validate(d):
 e=[]; actual={p.name for p in d.iterdir() if p.is_file()} if d.is_dir() else set()
 if actual!=REQ: return ['document inventory']
 ad=d/'adr'; got={p.name for p in ad.iterdir() if p.is_file()} if ad.is_dir() else set()
 if got!=ADRS:e+=['ADR inventory']
 master=(d/'MASTER_REQUIREMENTS.md').read_text(encoding='utf8')
 if not all(x in master for x in INV):e+=['master invariants']
 for n in REQ-{'MASTER_REQUIREMENTS.md','ADR_MAP.md'}:
  t=(d/n).read_text(encoding='utf8')
  if len(t)<350 or 'Status: target-only' not in t:e+=[f'target document: {n}']
 facts=(d/'VERIFIED_FACTS.md').read_text(encoding='utf8')
 if not all(x in facts for x in ('OWNER-PROVIDED ACCEPTANCE CLAIM','CODE/REPO FACT','UNVERIFIED','Source and date')):e+=['facts provenance schema']
 m=(d/'ADR_MAP.md').read_text(encoding='utf8')
 if 'Wayfinder' not in m or any(x[:-3] not in m for x in ADRS):e+=['ADR map']
 for n in ADRS:
  t=(ad/n).read_text(encoding='utf8') if (ad/n).is_file() else ''
  if 'Status: proposed' not in t or any(h not in t for h in HEAD):e+=[f'ADR structure: {n}']
 for p in [ROOT/'AGENTS.md',ROOT/'CONTEXT.md',*d.rglob('*.md')]:
  if p.name=='MASTER_REQUIREMENTS.md':continue
  t=p.read_text(encoding='utf8')
  if LEGACY_OLC_RTC in t:e+=[f'legacy spelling: {p.name}']
  if '193.17.183.48' in t:e+=[f'sensitive literal copied: {p.name}']
 return e
def main():
 a=argparse.ArgumentParser();a.add_argument('--docs-root',type=Path,default=DEFAULT);x=a.parse_args();e=validate(x.docs_root)
 print(('FAILED: '+'; '.join(e)) if e else 'OK: local document inventory, provenance, ADRs and invariants');return bool(e)
if __name__=='__main__':raise SystemExit(main())
