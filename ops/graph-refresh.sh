#!/usr/bin/env bash
# Принудительно пересобрать граф кода и ПРОВЕРИТЬ, что выход обновился.
#
# Зачем нужен отдельный сторож, если есть git-хук post-commit:
# 2026-07-29 обнаружено, что хук срабатывает, но результата не даёт. В логе
# /root/.cache/graphify-rebuild.log видна гонка:
#     [graphify hook] 1 file(s) changed - rebuilding graph...
#     [graphify watch] Rebuild already in progress - changes queued.
#     [graphify watch] No code-graph topology changes detected; outputs left untouched.
# Очередь сравнивается против УСТАРЕВШЕГО снимка, поэтому правки проглатываются: в main
# приехали 11 новых Kotlin-файлов, а graph.json остался от 28.07 (3407 узлов вместо 3567).
# Классическое «механизм ≠ результат»: хук настроен, а граф не ведётся.
#
# ⛔ ЛОВУШКИ:
#  1. Подкоманда сборки — `graphify update <путь>`, НЕ `graphify build`. «build» будет
#     принят за ПУТЬ, скрипт полезет в ./build (там PNG от phone-screen-sim) и потребует
#     LLM-ключ для семантического разбора картинок.
#  2. `--no-label` у `update` НЕ существует (это опция cluster-only) — вернёт
#     «unknown update option» и молча ничего не соберёт.
#  3. GRAPHIFY_FORCE=1 обязателен: без него сборка откажется перезаписать graph.json,
#     если узлов стало меньше (бывает после удаления кода) — и опять «результата нет».
#  4. Проверяем РЕЗУЛЬТАТ, но правильным инвариантом: **graph.json не должен быть СТАРШЕ
#     самого свежего файла кода**. Первая версия скрипта требовала, чтобы mtime всегда
#     РОС — и падала на втором прогоне подряд, когда пересобирать нечего. Ложная тревога
#     хуже отсутствия сторожа: её быстро начинают игнорировать, и настоящее отставание
#     графа пройдёт незамеченным.
set -euo pipefail

REPO="${1:-/root/maestrovpn-tv}"
GFY=/root/.local/bin/graphify
OUT="$REPO/graphify-out/graph.json"

[ -x "$GFY" ] || { echo "graph-refresh: нет $GFY" >&2; exit 1; }

before=0; [ -f "$OUT" ] && before=$(stat -c %Y "$OUT")
nodes_before=$(python3 -c "
import json,sys
try: print(len(json.load(open('$OUT')).get('nodes',[])))
except Exception: print(0)" 2>/dev/null || echo 0)

GRAPHIFY_FORCE=1 "$GFY" update "$REPO" 2>&1 | tail -4

after=0; [ -f "$OUT" ] && after=$(stat -c %Y "$OUT")
nodes_after=$(python3 -c "
import json,sys
try: print(len(json.load(open('$OUT')).get('nodes',[])))
except Exception: print(0)" 2>/dev/null || echo 0)

if [ ! -f "$OUT" ]; then
  echo "graph-refresh: ⛔ graph.json не создан. Смотреть /root/.cache/graphify-rebuild.log" >&2
  exit 1
fi

# Инвариант: граф не старше самого свежего ОТСЛЕЖИВАЕМОГО файла кода. Берём только
# то, что под git (иначе build/ с картинками симулятора вечно будет «свежее» графа).
newest_src=$(cd "$REPO" && git ls-files -z -- '*.kt' '*.java' '*.go' '*.py' '*.sh' 2>/dev/null \
  | xargs -0 -r stat -c %Y 2>/dev/null | sort -rn | head -1)
newest_src=${newest_src:-0}

if [ "$after" -lt "$newest_src" ]; then
  echo "graph-refresh: ⛔ ГРАФ ОТСТАЁТ: graph.json ($after) старше свежайшего исходника ($newest_src)." >&2
  echo "  Это симптом гонки post-commit-хука. Лог: /root/.cache/graphify-rebuild.log" >&2
  exit 1
fi

if [ "$after" -gt "$before" ]; then
  echo "graph-refresh: ✅ граф пересобран, узлов $nodes_before → $nodes_after"
else
  echo "graph-refresh: ✅ граф уже актуален ($nodes_after узлов, пересобирать нечего)"
fi
