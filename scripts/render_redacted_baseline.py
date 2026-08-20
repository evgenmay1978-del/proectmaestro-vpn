import hashlib
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PATTERNS = (
    re.compile(r"(?i)((?:token|password|secret|private[_ -]?key|authorization)\s*[:=]\s*)[^\s]+"),
    re.compile(r"(?i)(?:vless|hysteria2|trojan|ss)://[^\s]+"),
    re.compile(r"\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b", re.I),
)

def redact_text(value: str) -> str:
    value = PATTERNS[0].sub(r"\1<REDACTED>", value)
    value = PATTERNS[1].sub("<REDACTED>", value)
    return PATTERNS[2].sub("<REDACTED>", value)

def main() -> int:
    files = [ROOT / "AGENTS.md", ROOT / "CONTEXT.md"]
    files += sorted((ROOT / "docs" / "yandex-cdn-whitelist").glob("*.md"))
    rows = []
    for path in files:
        data = path.read_text(encoding="utf-8")
        rows.append({
            "path": str(path.relative_to(ROOT)).replace("\\", "/"),
            "bytes": len(data.encode("utf-8")),
            "sha256": hashlib.sha256(data.encode("utf-8")).hexdigest(),
            "preview": redact_text(data[:320]),
        })
    print(json.dumps({"kind": "local-redacted-baseline", "files": rows}, ensure_ascii=True, indent=2))
    return 0

if __name__ == "__main__":
    raise SystemExit(main())