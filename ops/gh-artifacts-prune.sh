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
#     ⭐ Проверено 2026-07-30 отдельно, потому что owner прямо сказал «107 версию не трожь»:
#     APK waypoint'а для старых клиентов — это ассет РЕЛИЗА `tv-v1.0.107`
#     (MaestroVPN-TV-1.0.107-debug.apk, 82 МБ, релиз от 27.06.2026), а среди артефактов
#     Actions совпадений со «107» РОВНО НОЛЬ. Этот скрипт работает только с артефактами,
#     до релизов не дотягивается ни при каких флагах. Сам waypoint включён через
#     MAESTRO_UPDATE_WAYPOINTS в /etc/maestro-panel.env.
#     ⛔ НИКОГДА не удалять релиз tv-v1.0.107 и его ассеты — это единственный мост, по
#     которому клиенты с versionCode < 107 вообще способны обновиться.
#
# Использование:
#   ops/gh-artifacts-prune.sh --dry-run   # показать, что было бы удалено
#   ops/gh-artifacts-prune.sh             # удалить
set -euo pipefail

REPO=evgenmay1978-del/proectmaestro-vpn
# Сколько последних артефактов каждого имени оставлять. Переопределяется переменной:
#   KEEP=0 ops/gh-artifacts-prune.sh   # снести ВСЕ сборочные APK
# KEEP=0 безопасен: откат живёт в GitHub Releases (у 1.0.150/151/152 есть ассеты-APK),
# а сборочные артефакты — лишь снимки прогонов.
KEEP="${KEEP:-3}"
# Только сборочные APK. Всё, чего нет в этом списке, скрипт не трогает.
PRUNE_NAMES="maestrovpn-tv-test-apk maestrovpn-tv-debug-apk maestrovpn-tv-olcrtc-canary maestrovpn-tv-awg-canary-apk maestrovpn-tv-stopfix-apk unit-test-report"

# ─── режим --deps (добавлен 2026-07-29) ────────────────────────────────────────
# Зачем: чистка ОДНИХ APK не спасает. 2026-07-29 после полного прогона скрипта
# осталось 3.43 ГБ, из них 2.2 ГБ — СТАРЫЕ КОПИИ зависимостей: libbox-aar лежал в
# 11 экземплярах (1.03 ГБ), wdtt-bin в 17 (353 МБ). Лимит артефактов на приватных
# репо бесплатного плана — 500 МБ, поэтому выгрузка APK падала «quota has been hit»,
# а шаг стоит с continue-on-error и показывал success. Симптом: сборка зелёная,
# артефакта нет.
#
# ⛔ ЛОВУШКИ (почему нельзя просто добавить эти имена в PRUNE_NAMES):
#  1. Сборки СКАЧИВАЮТ зависимости из ПОСЛЕДНЕГО УСПЕШНОГО прогона своего workflow
#     (`gh run download -n libbox-aar` в android.yml/android-test.yml, шаг «Fetch the
#     NORMAL libbox.aar»). Удалишь артефакт ИМЕННО того прогона — сборка умрёт на
#     шаге 9, до единой скомпилированной строки. Поэтому ниже прогон-источник
#     вычисляется через API и защищается ПОИМЕННО, а не «по свежести даты»:
#     самый новый артефакт может принадлежать неуспешному прогону.
#  2. KEEP_DEPS=2 — источник плюс один запас на откат. Меньше ставить нельзя:
#     останешься без пути назад, если новый libbox окажется битым.
#  3. libbox-mieru-aar / libbox-awg-aar / olcrtc-aar — экспериментальные ветки
#     движка, их прогоны-источники ищутся так же, по своим workflow.
DEPS_NAMES="libbox-aar libbox-mieru-aar libbox-awg-aar wdtt-bin olcrtc-bin olcrtc-aar"
KEEP_DEPS="${KEEP_DEPS:-2}"
# имя артефакта -> workflow, чей последний успешный прогон является источником
dep_workflow() {
  case "$1" in
    libbox-aar|libbox-mieru-aar|libbox-awg-aar) echo libbox.yml ;;
    wdtt-bin) echo wdtt-bin.yml ;;
    olcrtc-bin|olcrtc-aar) echo olcrtc-bin.yml ;;
    *) echo '' ;;
  esac
}

DRY=0; DEPS=0
for a in "$@"; do
  case "$a" in
    --dry-run) DRY=1 ;;
    --deps) DEPS=1 ;;
    *) echo "неизвестный аргумент: $a" >&2; exit 2 ;;
  esac
done

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
if [ "$DEPS" = "1" ]; then
  echo "-- режим --deps: старые копии зависимостей (оставляем $KEEP_DEPS + прогон-источник) --"
  for name in $DEPS_NAMES; do
    wf=$(dep_workflow "$name")
    # Прогон-источник: последний УСПЕШНЫЙ прогон нужного workflow. Именно его
    # артефакт качают сборки, и он защищён независимо от даты.
    src=$(gh run list --workflow="$wf" --status=success --limit 1 --json databaseId -q '.[0].databaseId' 2>/dev/null || echo '')
    keep_ids=''
    if [ -n "$src" ]; then
      keep_ids=$(gh api "repos/$REPO/actions/runs/$src/artifacts" \
        -q ".artifacts[] | select(.name==\"$name\") | .id" 2>/dev/null | tr '\n' ' ')
    fi
    rows=$(awk -F'\t' -v n="$name" '$1==n {print $3"\t"$2"\t"$4}' /tmp/gh-artifacts.$$ | sort -r)
    [ -z "$rows" ] && continue
    victims=$(printf '%s\n' "$rows" | tail -n +$((KEEP_DEPS+1)))
    # выкидываем из списка на удаление всё, что принадлежит прогону-источнику
    for kid in $keep_ids; do
      victims=$(printf '%s\n' "$victims" | awk -F'\t' -v k="$kid" '$2!=k')
    done
    victims=$(printf '%s\n' "$victims" | awk 'NF')
    [ -z "$victims" ] && { echo "  $name: чистить нечего (источник run $src)"; continue; }
    cnt=$(printf '%s\n' "$victims" | wc -l)
    bytes=$(printf '%s\n' "$victims" | awk -F'\t' '{s+=$3} END {print s+0}')
    echo "  $name: под удаление $cnt шт, $(awk -v b="$bytes" 'BEGIN{printf "%.0f", b/1048576}') МБ (источник run $src защищён)"
    total_del=$((total_del+cnt)); total_freed=$((total_freed+bytes))
    [ "$DRY" = "1" ] && continue
    printf '%s\n' "$victims" | while IFS=$'\t' read -r _created id _size; do
      gh api -X DELETE "repos/$REPO/actions/artifacts/$id" >/dev/null 2>&1 || echo "    не удалось удалить $id"
    done
  done
fi
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
