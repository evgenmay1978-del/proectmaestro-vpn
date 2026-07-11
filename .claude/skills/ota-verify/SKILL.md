---
name: ota-verify
description: Проверить OTA-цепочку доставки end-to-end (панель ↔ Яндекс-зеркало ↔ waypoint 107) после релиза или при жалобах «не обновляется». Триггеры — выкатили релиз, «проверь ота», «обновление не приходит», merge в main с app/**.
---

# Проверка OTA-цепочки

Выполни `bash ops/verify-ota.sh` (read-only). Если только что вышел новый релиз и зеркало ещё не синкнулось — `bash ops/verify-ota.sh --sync` (дёргает maestro-update-mirror.service, ждёт, проверяет).

Скрипт проверяет: версию в манифесте панели и на зеркале, доступность APK, waypoint UA<107→107 (⛔ ОБЯЗАТЕЛЬНЫЙ AWG-чекпойнт — если сломан, чинить немедленно).

Дополнительно при разборе жалоб:
- Свежий релиз собрался? `curl -s -H "Authorization: Bearer $(grep -oP 'https://[^:]+:\K[^@]+' /root/.git-credentials|head -1)" https://api.github.com/repos/evgenmay1978-del/proectmaestro-vpn/releases/latest | jq -r .tag_name`
- Размер APK на зеркале == size из манифеста (бит-в-бит).
- Зеркало живо: `journalctl -u maestro-update-mirror --since "1 hour ago"` (403 от GitHub = кто-то сжёг анонимный лимит; скрипт уже ходит с токеном).
- «Скачал, но не ставится» на Android<12 = Shizuku-тупик у версий <138 → ручная переустановка по ссылке бота (memory/fleet-triage-2026-07-10).
