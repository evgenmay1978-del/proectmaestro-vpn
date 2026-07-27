#!/bin/bash
# disable-xui-sub — выключить встроенный сервис подписки 3x-ui (порт 2096) на S1 и S3.
#
# ЗАЧЕМ. x-ui поднимает свою «подписку» на 2096 БЕЗ TLS (subCertFile/subKeyFile пустые) и отдаёт её
# в интернет открытым текстом. Нашим клиентам она не нужна: панель выдаёт ссылки через nginx по TLS
# (SUB_BASE=https://wapmixx.ru:8911/...). Проверено 27.07.2026: за 7 суток обращений к подписке x-ui
# на S1 — 4 (все мои пробы), на S3 — 0. Порт торчит зря.
#
# ЗАПУСК:
#   disable-xui-sub.sh --dry-run   — только показать, что сейчас (ничего не меняет)
#   disable-xui-sub.sh             — выключить и проверить
#
# ⚠️ ЛОВУШКА, из-за которой здесь SIGHUP, а не systemctl reload:
# в x-ui.service стоит ExecReload=kill -USR1, а USR1 у этого бинаря = «restarting xray-core», то есть
# обрыв ВСЕХ клиентских соединений. SIGHUP перезапускает только веб-часть панели (проверено 27.07:
# pid xray не менялся, соединения не падали).
#
# ⚠️ ЛОВУШКА №2 (наступил на неё при первой версии скрипта): передавать тело функции на удалённый хост
# через НЕэкранированный heredoc нельзя — bash раскроет все $(...) ЛОКАЛЬНО, и на S3 уедут значения S1.
# Поэтому тело лежит в переменной из heredoc с кавычками <<'EOS' и уходит как есть.
#
# ОТКАТ: то же самое со значением 'true' + SIGHUP, либо галка Subscription в UI панели.
# Копия базы делается автоматически рядом с ней: x-ui.db.bak-subdisable-<timestamp>.
set -u
S3_HOST="${S3_HOST:-46.30.42.151}"
DRY=0; [ "${1:-}" = "--dry-run" ] && DRY=1

WORKER=$(cat <<'EOS'
DB=/etc/x-ui/x-ui.db
if [ ! -f "$DB" ]; then echo "  нет $DB — пропускаю"; exit 0; fi
CUR=$(sqlite3 "$DB" "SELECT COALESCE((SELECT value FROM settings WHERE key='subEnable'),'(по умолчанию true)');")
LISTEN=$(ss -tlnp 2>/dev/null | grep -c ':2096')
echo "  сейчас: subEnable=$CUR, слушателей на 2096: $LISTEN"
if [ "${DRY:-0}" = "1" ]; then exit 0; fi
cp -a "$DB" "$DB.bak-subdisable-$(date +%s)"
sqlite3 "$DB" "INSERT INTO settings(key,value) SELECT 'subEnable','false'
               WHERE NOT EXISTS(SELECT 1 FROM settings WHERE key='subEnable');
               UPDATE settings SET value='false' WHERE key='subEnable';"
MP=$(systemctl show x-ui -p MainPID --value)
XB=$(pgrep -f 'xray-linux' | head -1)
kill -HUP "$MP" 2>/dev/null || { echo "  не смог послать SIGHUP (pid=$MP)"; exit 1; }
sleep 6
XA=$(pgrep -f 'xray-linux' | head -1)
echo "  стало: subEnable=$(sqlite3 "$DB" "SELECT value FROM settings WHERE key='subEnable';")"
echo "  слушателей на 2096: $(ss -tlnp 2>/dev/null | grep -c ':2096') (ожидаем 0)"
if [ "$XB" = "$XA" ]; then echo "  xray pid $XA не менялся — клиенты не задеты ✅"; else echo "  ⚠️ xray перезапустился ($XB → $XA)"; fi
echo "  панель жива: $(curl -sk -o /dev/null -w '%{http_code}' --max-time 8 https://127.0.0.1:2053/ 2>/dev/null || curl -s -o /dev/null -w '%{http_code}' --max-time 8 http://127.0.0.1:47989/ 2>/dev/null)"
EOS
)

echo "=== S1 (эта машина) ==="
DRY="$DRY" bash -c "DRY=$DRY; $WORKER"

echo "=== S3 ($S3_HOST) ==="
printf 'DRY=%s\n%s\n' "$DRY" "$WORKER" | ssh -o BatchMode=yes -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=15 "root@$S3_HOST" 'bash -s'
