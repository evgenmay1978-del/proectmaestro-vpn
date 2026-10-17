#!/bin/sh
# Кто на какой версии приложения — перепись флота по подпискам, а не по приборам и не по IP.
#
# ЗАЧЕМ ИМЕННО ТАК (ловушки, на которых я уже обжигался 30.07.2026):
#  1. По IP считать НЕЛЬЗЯ: мобильные адреса скачут, один телефон за сутки даёт до пяти IP —
#     получается пятикратный перебор. Считаем по sub-токену = по подписке = по клиенту.
#  2. По телеметрии `hello` считать НЕЛЬЗЯ: она шлётся ОДИН раз на versionCode, поэтому показывает
#     не текущую версию, а последнюю, чей первый запуск отчитался. Текущую версию даёт только UA
#     в nginx-логе.
#  3. Приборы ниже 138 в /sub часто не ходят вовсе — их видно лишь по /update/update.json, и там
#     нет ни device=, ни токена. Такие подписки в этот отчёт не попадут: см. блок «без подписки».
#  4. Мои собственные пробы с 193.17.183.48 (это актуальный S1) остаются в логах НАВСЕГДА и создают
#     фантомные «живые приборы на vc84». Исключаем жёстко.
#
# Граница 138 не случайна: в сборках ниже сломан САМ установщик внутри APK (фоновый startActivity
# глушится, сессии копятся) — с сервера это не чинится ничем, лечит только ручная установка.
#
# Usage: ops/fleet-versions.sh [дней_логов]   (по умолчанию текущий + вчерашний access.log)
set -u
STORE=${MAESTRO_STORE:-/var/lib/maestro/customers.json}
S1_IP=193.17.183.48
BROKEN_INSTALLER_BELOW=138
LATEST=$(curl -s --max-time 10 https://storage.yandexcloud.net/maestro-apk/update.json 2>/dev/null |
         jq -r '.version_code // empty')
[ -n "$LATEST" ] || LATEST=0

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

cat /var/log/nginx/access.log /var/log/nginx/access.log.1 2>/dev/null |
  grep -v "^$S1_IP " | grep "GET /sub/" |
  sed -E 's#^[0-9.]+ .*GET /sub/([a-f0-9]+).*SFA/1\.0\.([0-9]+).*#\1\t\2#' |
  grep -P '^[a-f0-9]+\t[0-9]+$' | sort -u > "$TMP/tokver.tsv"

# Самая СВЕЖАЯ версия среди приборов подписки. ⛔ Порядок условий важен: обращение к mx[$1] внутри
# сравнения само создаёт ключ, поэтому проверку «ключа ещё нет» пишем ПЕРВОЙ (иначе всегда 0).
awk -F'\t' '{ if (!($1 in mx) || $2+0 > mx[$1]) mx[$1]=$2+0 } END { for (t in mx) printf "%d\t%s\n", mx[t], t }' \
  "$TMP/tokver.tsv" | sort -n > "$TMP/tokmax.tsv"

echo "актуальная версия на зеркале: vc=$LATEST · подписок замечено за сутки: $(wc -l < "$TMP/tokmax.tsv")"

STORE="$STORE" LATEST="$LATEST" BROKEN="$BROKEN_INSTALLER_BELOW" python3 - "$TMP/tokmax.tsv" <<'PY'
import json, os, sys
rows = [l.rstrip("\n").split("\t") for l in open(sys.argv[1])]
latest, broken = int(os.environ["LATEST"]), int(os.environ["BROKEN"])
try:
    cust = json.load(open(os.environ["STORE"]))
except Exception as e:
    print(f"  ⚠️ база клиентов недоступна ({e}) — покажу только токены"); cust = []
by_tok = {c.get("sub_token"): c for c in cust if c.get("sub_token")}

def dump(title, keep):
    sel = [(int(v), t) for v, t in rows if keep(int(v))]
    print(f"\n=== {title}: {len(sel)} ===")
    for vc, tok in sorted(sel):
        c = by_tok.get(tok)
        if c is None:
            print(f"  vc={vc:<4} токен {tok[:12]}…  ⚠️ подписки в базе НЕТ (удалённый клиент?)")
            continue
        flag = "  ⛔ОТКЛЮЧЕНА" if c.get("disabled") else ""
        trial = "  (триал)" if str(c.get("login", "")).startswith("trial-") else ""
        print(f"  vc={vc:<4} {c.get('login','?'):<22} до {str(c.get('expires',''))[:10]}"
              f"  приборов:{len(c.get('devices') or [])}{trial}{flag}")

dump(f"НИЖЕ {broken} — лечит ТОЛЬКО ручная установка", lambda v: v < broken)
dump(f"{broken}–{latest-1} — догонят сами", lambda v: broken <= v < latest)
dump(f"на {latest} — актуальны", lambda v: v >= latest)
PY
