from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]; DOCS=ROOT/"docs"/"yandex-cdn-whitelist"
REQUIRED={"MASTER_REQUIREMENTS.md","VERIFIED_FACTS.md","RESEARCH.md","ARCHITECTURE.md","SPEC.md","IMPLEMENTATION_PLAN.md","TEST_PLAN.md","TEST_RESULTS.md","CLIENT_COMPATIBILITY.md","TRANSPORT_PRESETS.md","EDGE_LIFECYCLE.md","PANEL_INTEGRATION.md","TRAFFIC_METERING.md","BILLING.md","BILLING_RECONCILIATION.md","SECURITY.md","PRODUCTION_SAFETY.md","DEPLOYMENT.md","ROLLBACK.md","HANDOFF.md"}
def main():
 actual={p.name for p in DOCS.iterdir() if p.is_file()} if DOCS.is_dir() else set()
 if actual!=REQUIRED: print("FAILED: document inventory"); return 1
 text=(DOCS/"MASTER_REQUIREMENTS.md").read_text(encoding="utf-8")
 required=("Белые списки = ВЫКЛЮЧЕНО","Не повредить ни одного уже работающего VPN-подключения","WDTT, qWDTT, CSQTT и OLCTRC сейчас отложены","Real charging включается только после отдельного подтверждения")
 if not all(x in text for x in required): print("FAILED: master invariants"); return 1
 print("OK: local document inventory and invariants")
 return 0
if __name__=="__main__": raise SystemExit(main())
