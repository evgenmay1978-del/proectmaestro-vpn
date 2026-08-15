# MaestroVPN — точка входа для Codex и других агентов

Перед любой работой в этом репозитории:

1. Полностью прочитать [`CONTEXT_HANDOFF.md`](CONTEXT_HANDOFF.md).
2. Для production, серверов, ботов, OTA и протоколов полностью прочитать
   [`CURRENT_PRODUCTION_HANDOFF.md`](CURRENT_PRODUCTION_HANDOFF.md). Это
   обезличенный материальный checkpoint; его volatile-факты также перепроверять.
3. Проверить текущие ветку, HEAD и `git status`; volatile-факты из handoff
   перепроверять, а не принимать навсегда.
4. Прочитать перечисленные в handoff документы именно по текущей задаче.
5. Сохранять чужие tracked/untracked изменения и не выполнять очистку,
   reset, merge, release, OTA или деплой без прямого разрешения владельца.

## Долговременная память проекта

- Git-история, запушенные ветки и проверенные документы репозитория — долговременная
  память проекта. Чат и незапушенное состояние компьютера дают только ориентиры.
  Каждый материальный checkpoint документировать, коммитить, отправлять на GitHub и
  подтверждать точным удалённым SHA.
- Загружать контекст постепенно: сначала `AGENTS.md` и handoff, затем документы
  текущей задачи, после этого только нужные ссылки, данные и скрипты. Не дублировать
  весь проект в чате или нескольких документах.
- Стабильную процедуру, знания и повторяемые скрипты хранить в skill; актуальное
  внешнее состояние получать через API/MCP/connector. Не сохранять в skill секреты
  или volatile production-факты.
- Изменения памяти проходят те же барьеры качества: подтверждение источников,
  просмотр diff, узкие проверки и CI, когда меняется исполняемое поведение. При
  конфликте сводить правила в один авторитетный источник и удалять устаревшие дубли.
- Диагностический, исполнительный и meta-loop должны иметь явные границы и условия
  остановки. Новое постоянное правило добавлять только после воспроизводимого
  результата или повторившейся подтверждённой ошибки, а не по единичной догадке.
- Пока guard не использует межпроцессную блокировку, его ledger — single-writer:
  не запускать guarded-команды параллельно из root и subagents в одном worktree.
  Сериализовать действия либо использовать отдельные worktree и ledger. Исчезновение
  correction/history считать ledger race: остановить конкурентов, проверить журнал и
  один раз восстановить запись; бизнес-команду повторно не выполнять.
- Таймаут-тест, который запускает дочерний test binary, после первого расхождения
  между обычным и `-race` запуском запрещено чинить расширением wall-clock порога.
  Вынести проверяемый цикл за детерминированный injected seam и моделировать deadline
  через контекст; реальные subprocess-тесты оставить только для границ exec/stdin/
  stdout/exit. Новый exact-SHA CI допустим только после такой структурной коррекции.

## Обязательный барьер от повторных ошибок

Перед серверной/сетевой операцией, записью в GitHub, изменением файлов,
долгим сканированием или сборкой, а также перед любой повторной попыткой сначала
выполнить из корня репозитория:

```text
python ops/maestro-repetition-guard.py check --action <действие> --family <способ>
```

Перед формированием каждой исполняемой команды сначала сопоставить ей один semantic action и проверить, что непосредственно предыдущее отдельное действие guard вернуло `ALLOW`/`ALLOW_CORRECTED` именно для него. Если такого подтверждения нет, команду, включая read-only `git apply --check`, не запускать. Любой случай запуска без непосредственного guard считать сбоем процесса и до продолжения обновить это правило.

An ALLOW is single-use and is not transferable to another command family; if the executable command differs, stop and run a fresh matching check.
If a generated full-file transformation reports one anchor mismatch, do not tune or retry string anchors. Read the exact current bounded line, replace that entire line through a structural generator, and validate the complete replacement before diffing.
For Python validation executed inside an old/new patch mirror, set `PYTHONDONTWRITEBYTECODE=1` before the first run. Never clean generated `__pycache__` with `Remove-Item`: Windows policy rejects both recursive and exact-file deletion. After a read-only exact-path check proves the cache is inside the staging mirror, move the whole directory to a named quarantine path outside both mirror sides, verify the source is absent, and do not retry deletion.

Browser bridge rule: `@browser` selects the Codex in-app browser; it does not attach Yandex Browser or transfer its authenticated session. An authenticated external browser is usable only when the ChatGPT browser extension is installed and connected through Settings -> Computer use. After `Browser is not available: extension`, record `fail` and do not call `get("extension")` again until the owner explicitly confirms that the extension is connected. Never substitute the in-app browser and never inspect cookies, profiles, passwords, local storage, or session stores.

`action` и `family` — только короткие смысловые идентификаторы, например
`s1-key-login` и `openssh-key-probe`. Команды, пути с секретами, пароли, ключи,
токены, URL подписок и данные клиентов передавать в guard запрещено.

После первого неожиданного сбоя или замечания владельца, что выбран неверный
способ, немедленно записать `fail` и остановить это действие. Повтор той же
команды, смена флагов, другой клиент или импровизированная альтернатива также
запрещены, пока отдельно не установлена причина и не зарегистрирован иной способ
через `correct`. Перед ним снова выполнить `check`; после доказанного результата
записать `success`. Точные формы команд и коды выхода описаны в
[`ops/README.md`](ops/README.md). Если guard отсутствует, повреждён или сам
возвращает блокировку, production- и внешние действия запрещены.

Не выполнять рекурсивные обходы всего `C:\Users\User\Documents\Codex` на слабом
компьютере владельца. Использовать уже зафиксированный список репозиториев и
узкие `rg`/Git-проверки конкретного проекта; тяжёлые сборки выполнять в GitHub
Actions, если handoff не требует другого проверенного места исполнения.

Для тестовых shell-fixtures под `/bin/sh`/dash запрещены составные `case`-glob
шаблоны с заключёнными в кавычки shell-операторами. После первой ошибки
`dash`/`sh -n` такой шаблон не править повторно: заменить условие простым
предикатом через stdin и `grep -F`, затем отдельно проверить синтаксис в Linux
GitHub Actions до запуска тестов.

Отвечать владельцу по-русски.


Если два свежих exact-SHA запуска узкого backend-изменения успешно проходят
format, `go test ./...`, `go test -race ./...` и `go vet ./...`, но оба падают
в одном и том же независимом integration harness, запрещено создавать новые
пустые checkpoint-коммиты или перезапускать тот же workflow. Зафиксировать
повторяющийся root cause, считать узкий backend gate доказанным, а harness —
отдельным блокером его owning-задачи; следующий запуск допустим только после
материального исправления harness или workflow.

Для текущей переработки мобильного 4D-интерфейса телевизионная часть доступна
только для чтения: не менять `tvm_*`, TV-компоненты, D-pad/focus/Back,
TV-геометрию и ветки `isTv`.

Если состояние UI/дизайна, ветка, коммит, PR, выполненные проверки или следующий
шаг материально изменились, перед завершением обновить `CONTEXT_HANDOFF.md`.
Если материально изменились production, серверы, боты, OTA или протоколы,
обновить `CURRENT_PRODUCTION_HANDOFF.md` только обезличенными фактами.
