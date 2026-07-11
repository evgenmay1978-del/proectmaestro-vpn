# MaestroVPN — правила проекта (мини, по-делу)

Отвечать по-русски. Ты владеешь проектом: ориентируйся из памяти/графа, не заставляй владельца объяснять заново.

## ⛔ Жёсткие правила
1. **Живые платящие клиенты на S1/S2/S3 (+ LAN роутера владельца) — никогда не вредить.** Перед любым рестартом/правкой прода — подумай, кого заденет.
2. **«Готово/LIVE» только после USER-FACING проверки end-to-end** (публичный edge, реальный OTA-манифест+зеркало, живой трафик) — «скомпилилось/задеплоилось» не считается.
3. **UI = пиксель-в-пиксель из эскизов владельца.** Ничего не выдумывать. Перед правкой ТВ/телефон-раскладки — PIL-сим по числам из Kotlin (`ops/tv-eskiz-pipeline.py`, скилл app-screen-render), смотреть глазами ДО кода.
4. **Не частить OTA-релизами** — churn стирает прогресс загрузок у медленных клиентов. Копи правки, выкатывай одним релизом.

## Сборка и релиз (НЕТ локального Android SDK!)
- Компайл-гейт: workflow `android-test.yml` — только ручной dispatch: `POST /actions/workflows/android-test.yml/dispatches {"ref":"<ветка>"}` с токеном из `/root/.git-credentials`.
- ⚠️ **Push в `main` с изменениями `app/**` = АВТОРЕЛИЗ**: `android.yml` собирает APK, публикует release `--latest`, зеркало (таймер ~15 мин) раскатывает OTA на флот. Мержи в main = решение выкатить.
- OTA-цепочка после релиза проверяется `ops/verify-ota.sh` (манифест vc / sha / размер / waypoint UA105→107 / latest.apk).
- Версия = номер CI-рана. Подпись флота фикс. — см. memory/update-delivery-ru.
- **Все запросы к api.github.com — ТОЛЬКО с токеном** (анонимный лимит 60/час на IP сжигается опросами и ломает зеркало).

## Бэкенд / панель (S1 = этот бокс)
- Деплой панели: `ops/deploy-panel.sh` (build → mv → restart → healthz). Дрейф: `ops/deploy-status.sh`.
- `/admin/*` живёт на 127.0.0.1:8910; наружу (8911) проксируются только 2 IP-pinned эндпоинта для S2-бота.
- Даты подписок = «единый организм»: hourly `maestro-dates-reconcile.timer` + прямой синк из S2-бота. Правки дат руками — через `/admin/renew|set-expiry`, не в сторах напрямую.

## Ориентация и навигация
- Память: `/root/.claude/projects/-root-maestrovpn-tv/memory/MEMORY.md` (индекс) + state `/root/.claude/maestro-state.md` (in-flight, ДЕРЖАТЬ СВЕЖИМ) + инфра-карта `maestro-infra.md`.
- Код: граф `graphify-out/` (`graphify query "<терм>"`), god-nodes: SFANavHost, DashboardViewModel, BoxService, GenerateSingbox, Provisioner. Не читать 340 файлов подряд.
- Тяжёлые сборки — на наименее загруженной ноде (карта в ориентации), не рефлекторно на S1.
- Телеметрия флота: `/var/lib/maestro/reports/*.jsonl` (hello/crash) + nginx-лог. При жалобах — сначала чек-лист memory/fleet-triage-2026-07-10.

## Стиль кода/работы
- Kotlin/Compose: краткие правила — memory/compose-ui-craft-rules. Фокус на ТВ = чёткая рамка, НИКОГДА не blur-glow/state-layer; кропы эскиза НЕ масштабировать.
- Ассеты-герои — lossless webp; арт-битмапы рисовать с FilterQuality.High.
- Скрипты автоматизации кладём в `ops/` (правило «можно кодом — делай кодом»), деплой-юниты в `deploy/`.
- Секреты: /etc/maestro-panel.env, /root/.git-credentials, /root/.s2pass|.s3pass, /root/.routerpass — не печатать значения в вывод.
