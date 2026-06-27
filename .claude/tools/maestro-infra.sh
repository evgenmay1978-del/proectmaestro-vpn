#!/bin/bash
# Maestro INFRASTRUCTURE MAP — probes S1/S2/S3 live and writes a current "where is
# everything + how loaded is each node" inventory. Generated from the servers themselves
# so it can NEVER go stale. Run by a timer + on demand; the orientation hook surfaces a digest.
set +e
OUT=/root/.claude/maestro-infra.md
TS=$(date -u +%Y-%m-%dT%H:%MZ)
PORTS='2080|8080|8443|8444|8910|8911|443|80|7000|7500|1080'
SVCS='maestro|nginx|xray|x-ui|hysteria|naive|sing-box|awg|amneziawg|caddy|frp|bot|vpn|trojan'

probe() { # $1=label $2=sshprefix("" for local)
  local L="$1" S="$2"
  echo "### $L"
  $S bash -s <<'RPROBE' 2>/dev/null
    echo "  host: $(hostname) | cores: $(nproc) | $(free -m | awk '/Mem/{printf "RAM %sMB tot / %sMB avail", $2,$7}')"
    echo "  load:$(uptime | grep -oE 'load average:.*') | disk /: $(df -h / | awk 'NR==2{print $3"/"$2" ("$5")"}')"
    echo "  swap: $(free -m | awk '/Swap/{print $3"MB/"$2"MB used"}')"
    echo -n "  TCP listen: "; ss -ltn 2>/dev/null | awk 'NR>1{print $4}' | grep -oE '[0-9]+$' | sort -un | tr '\n' ' '; echo
    echo -n "  UDP listen: "; ss -lun 2>/dev/null | awk 'NR>1{print $4}' | grep -oE '[0-9]+$' | sort -un | tr '\n' ' '; echo
    echo -n "  VPN/panel procs: "; pgrep -a -f 'xray|hysteria|naive|sing-box|awg|caddy|frp|maestro|x-ui' 2>/dev/null | awk '{print $2}' | xargs -n1 basename 2>/dev/null | sort -u | tr '\n' ' '; echo
    echo -n "  running services: "; systemctl list-units --type=service --state=running --no-legend 2>/dev/null | awk '{print $1}' | grep -iE 'maestro|nginx|xray|x-ui|hysteria|naive|sing-box|awg|amneziawg|caddy|frp|bot|trojan' | tr '\n' ' '; echo
RPROBE
  echo
}

{
echo "# Maestro infrastructure map (auto-probed $TS) — the single-organism view"
echo "Regenerate: bash /root/.claude/maestro-infra.sh. Ports legend: 443=VLESS/TLS, 8443=Trojan/Hy2,"
echo "443udp=AmneziaWG(S3), 2080=Naive, 8910=panel(loopback), 8911=nginx public, 7000/7500=frp."
echo
probe "S1 — wapmixx.ru / 194.48.141.106 (NL) — panel+nginx+OTA+some VPN; MY dev box (don't overload)" ""
PW2=$(cat /root/.s2pass 2>/dev/null); PW3=$(cat /root/.s3pass 2>/dev/null)
[ -n "$PW2" ] && probe "S2 — 85.137.166.237 (CZ) — Naive/Hy2/panel-replica/bot" "sshpass -p $PW2 ssh -o StrictHostKeyChecking=no -o ConnectTimeout=8 root@85.137.166.237"
[ -n "$PW3" ] && probe "S3 — 46.30.42.151 (NL) — 3x-ui(VLESS/Trojan)+AnyTLS/Hy2+AmneziaWG(awg0:443udp)" "sshpass -p $PW3 ssh -o StrictHostKeyChecking=no -o ConnectTimeout=8 root@46.30.42.151"
echo "LOAD-PLACEMENT RULE: put heavy builds/probes on the LEAST-loaded node with RAM headroom (check 'load' + 'RAM avail' above), NOT reflexively on S1. S1 RAM is only ~1.9GB → big Go/gvisor builds are memory-tight there."
} > "$OUT" 2>&1
cat "$OUT"
