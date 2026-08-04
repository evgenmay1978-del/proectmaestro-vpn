# MaestroVPN — точка входа для Codex и других агентов

Перед любой работой в этом репозитории:

1. Полностью прочитать [`CONTEXT_HANDOFF.md`](CONTEXT_HANDOFF.md), начиная с верхнего LIVE-раздела.
2. Полностью прочитать [`docs/agent-working-contract.md`](docs/agent-working-contract.md).
3. Проверить текущие ветку, HEAD и `git status`; volatile-факты из handoff
   перепроверять, а не принимать навсегда.
4. Прочитать перечисленные в handoff документы именно по текущей задаче.
5. Сохранять чужие tracked/untracked изменения и не выполнять очистку,
   reset, merge, release, OTA или деплой без прямого разрешения владельца.

Отвечать владельцу по-русски.

Для активной задачи Mobile Home обязательно прочитать:

- [task handoff](docs/handoffs/2026-08-04-mobile-deck-layer-order.md);
- [утверждённую spec](docs/superpowers/specs/2026-08-04-mobile-deck-foreground-scroll-design.md);
- [TDD implementation plan](docs/superpowers/plans/2026-08-04-mobile-deck-foreground-scroll.md);
- [сохранённый QA baseline](docs/evidence/2026-08-04-owner-home-scroll-proof-qa.svg).

Для текущей переработки мобильного 4D-интерфейса телевизионная часть доступна
только для чтения: не менять `tvm_*`, TV-компоненты, D-pad/focus/Back,
TV-геометрию и ветки `isTv`.

В активной Mobile Home-задаче работа ведётся только через GitHub. На Windows
запрещено изменять проект, запускать Git/Gradle, тесты, simulator и APK-сборку.
Разрешён только тестовый workflow `.github/workflows/android-test.yml`;
`.github/workflows/android.yml`, `main`, Release и OTA не трогать.

Если состояние проекта, ветка, коммит, PR, выполненные проверки, неудача или
следующий шаг материально изменились, перед завершением обновить task handoff и
верхний LIVE-раздел `CONTEXT_HANDOFF.md`. Любая test APK остаётся тестовой:
релизный барьер снимает только отдельная фраза владельца «добро на обновление».
