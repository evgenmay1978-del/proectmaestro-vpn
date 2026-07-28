#!/usr/bin/env bash
# Освободить квоту хранилища артефактов GitHub Actions, удалив СТАРЫЕ сборочные APK.
#
# Зачем: артефакты копятся по 130–340 МБ за прогон и не истекают сами достаточно быстро.
# 2026-07-28 квота была выбрана полностью (403 артефакта, 38.6 ГБ) — и это ломало ВСЁ:
# шаг `Upload test APK` падал с "Artifact storage quota has been hit", а в релизном
# android.yml такой же upload стоит ПЕРЕД `gh release create`, то есть релиз не публиковался
# бы вообще. Симптом обманчив: сборка при этом зелёная, падает только выгрузка.
#
# ⛔ ЛОВУШКИ (не удалять руками, использовать этот скрипт):
#  1. НИКОГДА не удалять libbox-aar / libbox-*-aar / wdtt-bin / olcrtc-bin / olcrtc-aar —
#     их СКАЧИВАЮТ последующие сборки (`gh run download -n libbox-aar`). Удалишь свежий
#     libbox-aar → android.yml и android-test.yml перестанут собираться совсем.
#     Скрипт трогает только имена из PRUNE_NAMES, всё остальное игнорируется по умолчанию.
#  2. Оставляем KEEP последних артефактов каждого имени — на случай отката/сравнения.
#  3. APK релизов лежат в GitHub Releases, а не в артефактах: удаление сборочных APK
#     НЕ трогает то, что раздаётся флоту по OTA.
#
# Использование:
#   ops/gh-artifacts-prune.sh --dry-run   # показать, что было бы удалено
#   ops/gh-artifacts-prune.sh             # удалить
set -euo pipefail

REPO=evgenmay1978-del/proectmaestro-vpn
KEEP=3
# Только сборочные APK. Всё, чего нет в этом списке, скрипт не трогает.
PRUNE_NAMES="maestrovpn-tv-test-apk maestrovpn-tv-debug-apk maestrovpn-tv-olcrtc-canary maestrovpn-tv-awg-canary maestrovpn-tv-stopfix-apk unit-test-report"

DRY=0; [ "${1:-}" = "--dry-run" ] && DRY=1

echo "== чистка артефактов $REPO (оставляем $KEEP последних каждого имени) =="

gh api "repos/$REPO/actions/artifacts" --paginate \
  -q '.artifacts[] | select(.expired==false) | "\(.name)\t\(.id)\t\(.created_at)\t\(.size_in_bytes)"' \
  > /tmp/gh-artifacts.$$

total_freed=0
total_del=0
for name in $PRUNE_NAMES; do
  # Сортируем по дате создания, новые сверху; хвост после KEEP — под удаление.
  ids=$(awk -F'\t' -v n="$name" '$1==n {print $3"\t"$2"\t"$4}' /tmp/gh-artifacts.$$ | sort -r | tail -n +$((KEEP+1)))
  [ -z "$ids" ] && continue
  cnt=$(printf '%s\n' "$ids" | wc -l)
  bytes=$(printf '%s\n' "$ids" | awk -F'\t' '{s+=$3} END {print s+0}')
  echo "  $name: под удаление $cnt шт, $(awk -v b="$bytes" 'BEGIN{printf "%.2f", b/1073741824}') ГБ"
  total_del=$((total_del+cnt)); total_freed=$((total_freed+bytes))
  [ "$DRY" = "1" ] && continue
  printf '%s\n' "$ids" | while IFS=$'\t' read -r _created id _size; do
    gh api -X DELETE "repos/$REPO/actions/artifacts/$id" >/dev/null 2>&1 || echo "    не удалось удалить $id"
  done
done
rm -f /tmp/gh-artifacts.$$

echo "  ИТОГО: $total_del артефактов, $(awk -v b="$total_freed" 'BEGIN{printf "%.2f", b/1073741824}') ГБ"
[ "$DRY" = "1" ] && echo "  (dry-run — ничего не удалено)"

if [ "$DRY" != "1" ]; then
  # Проверяем РЕЗУЛЬТАТ, а не факт вызова. Квота пересчитывается на стороне GitHub
  # с задержкой (6–12 ч по их доке), но число живых артефактов видно сразу.
  left=$(gh api "repos/$REPO/actions/artifacts" --paginate -q '.artifacts[] | select(.expired==false) | .size_in_bytes' | awk '{s+=$1; n++} END {printf "%d|%.2f", n, s/1073741824}')
  echo "  осталось живых: ${left%|*} шт, ${left#*|} ГБ"
  echo "  ⚠️ libbox-aar/wdtt-bin/olcrtc-* сохранены:"
  gh api "repos/$REPO/actions/artifacts" --paginate \
    -q '.artifacts[] | select(.expired==false) | select(.name|test("libbox|wdtt|olcrtc")) | "     \(.name) \(.created_at[:10])"' | sort -u | head -8
fi
