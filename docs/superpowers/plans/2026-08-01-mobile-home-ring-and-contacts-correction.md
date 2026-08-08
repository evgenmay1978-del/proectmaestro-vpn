# Mobile Home Ring And Contacts Correction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Убрать деревянный провал и нижнее затемнение из кольцевой мозаики, сохранить точную
4D-геометрию и закрепить уже выполненное исправление фирменных контактов тестами.

**Architecture:** Меняется только RGB внутреннего диска трёх `home_ring_*`; внешняя бронза и
каноническая альфа остаются побитно прежними. После RED-проверки исходники проходят штатный
детерминированный atlas pipeline. Контактный production UI меняется только если актуальная
симуляция `6447a9a` докажет ошибку; в любом случае его контракт закрепляется JVM-тестом.

**Tech Stack:** Python/Pillow art guard, pinned Pillow 11.3.0 atlas generator, Kotlin/JUnit,
Jetpack Compose, deterministic phone simulator, Git.

## Task 1: Зафиксировать RED для кольца

- [ ] Заменить устаревший `dark_zone` в `ops/mobile-4d-art-check.py` проверкой непрерывной
  мозаики до нижней бронзы и равномерности яркости нижней/средней зон.
- [ ] Добавить отрицательные synthetic cases в `--selftest`.
- [ ] Запустить `--group ring` на текущем арте и сохранить ожидаемый FAIL.

## Task 2: Исправить три световых варианта

- [ ] Восстановить исходный радиальный рисунок мозаики внутри бронзы, убрать деревянную хорду.
- [ ] Взять точный pre-cap RGB из `03a672a`; не ресемплировать диск по ошибочному диапазону,
  включающему боковые самоцветы.
- [ ] Сохранить каноническую альфу и побитно сохранить наружную бронзу/камни.
- [ ] Синхронно обновить `kit/home_ring_*` и `source/home_ring_*`.

## Task 3: Защитить контакты и manifest

- [ ] Исправить устаревшее ожидание пяти слоёв на восемь в
  `Mobile4DGeneratedAssetsTest`.
- [ ] Добавить JVM-контракт для Telegram/МАКС/WhatsApp, ресурсов, `26 dp`, `10.5 sp` и трёх
  alpha-границ плит из `95b4847`.
- [ ] Добавить RED-проверку полного совпадения manual compositor с восемью слоями manifest;
  исправить пропуск `contacts`.
- [ ] Добавить три состояния строки протокола и отделить визуальные `38 dp` кнопки от её
  интерактивной области `48 dp`.
- [ ] Не добавлять ручные offsets без свежего device screenshot с коммитом `6447a9a` или новее.

## Task 4: Пересобрать и проверить

- [ ] Запустить `mobile-4d-art-check.py --group ring` и `--selftest`.
- [ ] Пересобрать штатный атлас закреплённым toolchain и затем запустить `--check`.
- [ ] Исправить portable font lookup симулятора только если он блокирует визуальную проверку,
  затем построить connected/connecting/disconnected previews.
- [ ] Проверить diff: ноль изменений TV/tvm/backend/release; локальный Gradle/APK не запускать.

## Task 5: Handoff

- [ ] Обновить `CONTEXT_HANDOFF.md` решением владельца и числовой приёмкой.
- [ ] Закоммитить один сфокусированный набор, запушить в `codex/mobile-4d-deck` и сверить SHA
  через `git ls-remote`, чтобы Claude видел результат только через GitHub.
