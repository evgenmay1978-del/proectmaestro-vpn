import hashlib,json,re
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1];DOCS=ROOT/'docs'/'yandex-cdn-whitelist';PEM=re.compile(r'-----BEGIN [A-Z0-9][A-Z0-9 ]{0,80}-----[\s\S]*?(?:-----END [A-Z0-9][A-Z0-9 ]{0,80}-----|\Z)');BEARER=re.compile(r'(?is)(authorization\s*[:=]\s*["\']?bearer\s+)[^"\'\r\n]+');URI=re.compile(r'(?i)(?:vless|vmess|trojan|hysteria2|hysteria|ss|tuic|wireguard|wg|csqtt|wdtt|olcrtc)://[^\s]+');UUID=re.compile(r'\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b',re.I);ASSIGN=re.compile(r'(?im)((?:token|password|secret|private[_ -]?key)\s*[:=]\s*)[^\r\n]+')
def redact_text(v):return UUID.sub('<REDACTED>',URI.sub('<REDACTED>',ASSIGN.sub(r'\1<REDACTED>',BEARER.sub(r'\1<REDACTED>',PEM.sub('<REDACTED>',v)))))
def safe_preview(v,limit=320):return redact_text(v)[:limit]
def paths():return [ROOT/'AGENTS.md',ROOT/'CONTEXT.md']+sorted(DOCS.rglob('*.md'))
def build_manifest():
 rows=[]
 for p in paths():
  b=p.read_bytes();rows+=[{'path':str(p.relative_to(ROOT)).replace('\\','/'),'bytes':len(b),'sha256':hashlib.sha256(b).hexdigest(),'preview':safe_preview(b.decode('utf8'))}]
 return {'kind':'local-redacted-baseline','files':rows}
def validate_manifest(m):
 expected=[str(p.relative_to(ROOT)).replace('\\','/')for p in paths()]
 if not isinstance(m,dict)or m.get('kind')!='local-redacted-baseline'or not isinstance(m.get('files'),list):return False
 rows=m['files'];got=[r.get('path')for r in rows if isinstance(r,dict)]
 if got!=expected or len(set(got))!=len(got):return False
 for r,p in zip(rows,paths()):
  b=p.read_bytes()
  if set(r)!={'path','bytes','sha256','preview'}or not isinstance(r['bytes'],int)or not isinstance(r['preview'],str)or r['bytes']!=len(b)or r['sha256']!=hashlib.sha256(b).hexdigest():return False
 return True
def main():print(json.dumps(build_manifest(),ensure_ascii=True,indent=2));return 0
if __name__=='__main__':raise SystemExit(main())
