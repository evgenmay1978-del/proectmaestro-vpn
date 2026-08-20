import hashlib,json,re
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1];DOCS=ROOT/'docs'/'yandex-cdn-whitelist'
PEM=re.compile(r'-----BEGIN [A-Z0-9][A-Z0-9 ]{0,80}-----[\s\S]*?-----END [A-Z0-9][A-Z0-9 ]{0,80}-----');BEARER=re.compile(r'(?im)^(\s*authorization\s*:\s*bearer\s+)[^\r\n]+');ASSIGN=re.compile(r'(?im)((?:token|password|secret|private[_ -]?key)\s*[:=]\s*)[^\r\n]+');URI=re.compile(r'(?i)(?:vless|vmess|trojan|hysteria2|hysteria|ss|tuic|wireguard|wg|csqtt|wdtt|olcrtc)://[^\s]+');UUID=re.compile(r'\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b',re.I)
def redact_text(v):
 """Return display-safe preview without parsing or emitting plaintext secrets."""
 return UUID.sub('<REDACTED>',URI.sub('<REDACTED>',ASSIGN.sub(r'\1<REDACTED>',BEARER.sub(r'\1<REDACTED>',PEM.sub('<REDACTED>',v)))))
def manifest_paths():return [ROOT/'AGENTS.md',ROOT/'CONTEXT.md']+sorted(DOCS.rglob('*.md'))
def main():
 rows=[]
 for p in manifest_paths():
  d=p.read_text(encoding='utf8');rows+=[{'path':str(p.relative_to(ROOT)).replace('\\','/'),'bytes':len(d.encode()),'sha256':hashlib.sha256(d.encode()).hexdigest(),'preview':redact_text(d[:320])}]
 print(json.dumps({'kind':'local-redacted-baseline','files':rows},ensure_ascii=True,indent=2));return 0
if __name__=='__main__':raise SystemExit(main())
