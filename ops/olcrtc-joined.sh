#!/bin/sh
set -eu

[ "$#" -eq 1 ] || exit 64
unit=$1
case "$unit" in
  olcrtc-*.service) ;;
  *) exit 64 ;;
esac

pid=$(systemctl show -p MainPID --value "$unit" 2>/dev/null) || exit 1
case "$pid" in
  ''|*[!0-9]*) exit 1 ;;
esac
[ "$pid" -gt 0 ] || exit 1

ss -H -tnp state established 2>/dev/null | awk -v needle="pid=$pid," '
function peer_address(endpoint, value) {
  value = tolower(endpoint)
  if (value ~ /^\[/) {
    sub(/^\[/, "", value)
    sub(/\]:[0-9]+$/, "", value)
    return value
  }
  sub(/:[0-9]+$/, "", value)
  return value
}
function is_public(addr, octets) {
  sub(/^::ffff:/, "", addr)
  if (addr == "" || addr == "*" || addr == "0.0.0.0" || addr == "::" || addr == "::1") return 0
  if (addr ~ /^127\./ || addr ~ /^169\.254\./ || addr ~ /^10\./ || addr ~ /^192\.168\./) return 0
  if (addr ~ /^172\./) {
    split(addr, octets, ".")
    if ((octets[2] + 0) >= 16 && (octets[2] + 0) <= 31) return 0
  }
  if (addr ~ /^fe[89ab]/ || addr ~ /^f[cd]/) return 0
  return 1
}
index($0, needle) {
  if (is_public(peer_address($4))) found = 1
}
END { exit found ? 0 : 1 }
'
