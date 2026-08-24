# Yandex CDN white-list task

Navigation: [requirements](docs/yandex-cdn-whitelist/MASTER_REQUIREMENTS.md), [vocabulary](CONTEXT.md), [SPEC](docs/yandex-cdn-whitelist/SPEC.md), [ADR/Wayfinder map](docs/yandex-cdn-whitelist/ADR_MAP.md), [Definition of Done](docs/yandex-cdn-whitelist/DEFINITION_OF_DONE.md), and [handoff](docs/yandex-cdn-whitelist/HANDOFF.md).

Current execution contract (22.08.2026):

- `codex/yandex-cdn-whitelist-task3-sync` is the sole canonical working/push
  branch; one writer owns it. Alternate branches/worktrees are review-only and
  must never become the handoff or push source.
- The weak local Windows PC is used only for edits, repetition guard, narrow
  Git/diff/static/unit checks. Heavy/full/race/vet/rqlite/Android APK validation
  runs in GitHub Actions against the exact pushed SHA.
- Immutable production Android/TV baseline: version `1.0.157`, tag
  `tv-v1.0.157`, commit
  `9653636863cb65cc2ac95545d953d9c5e06db8bb`, APK SHA-256
  `0c51d1036c76b2d9a7347b59b9f967942159ec27738a2d6bcae099529695a148`.
- The only next Android identity is test-only
  `versionName 1.0.158-task7-test`, `versionCode 1015800`; it is never a
  production tag/release/OTA.
- After every completed top-level task, update `CONTEXT_HANDOFF.md` and the
  redacted baseline manifest, verify them, commit, and push the canonical
  branch so local and GitHub resolve to the same exact SHA.
- Existing owner authorization covers non-production pushes and GitHub Actions
  on the canonical branch only. Merge, tag, release/publish/signing, OTA,
  production deploy/server/client/billing/cutover mutation require a new
  explicit owner approval.

Invariants: ordinary VPN is unchanged; White-List Entitlement defaults OFF and is additive; initial work is isolated; mandatory target is independently reconciled S1/S2/S3/S4 nodes; no production restart/update/migration/firewall/UUID/URI/client/OTA/real charging; no secrets in Git/logs/docs; WDTT/qWDTT/CSQTT/olcRTC are out of scope.

Local checks: `python -m unittest scripts.tests.test_yandex_cdn_docs`; `python scripts/validate_yandex_cdn_docs.py`; `python scripts/render_redacted_baseline.py`.

Stop gates: live inventory, backup/restore, sidecar deployment, CDN origin switch, production Xray/3x-ui/firewall/DB, real-client switch, charging, OTA, reboot, deletion, and push require explicit owner approval. Sensitive owner-supplied test context remains only in MASTER_REQUIREMENTS; derivative docs/logs must cite sections rather than repeat literals.

---

# MaestroVPN — точка входа для Codex и других агентов

Перед любой работой в этом репозитории:

1. Полностью прочитать [`CONTEXT_HANDOFF.md`](CONTEXT_HANDOFF.md).
2. Проверить текущие ветку, HEAD и `git status`; volatile-факты из handoff
   перепроверять, а не принимать навсегда.
3. Прочитать перечисленные в handoff документы именно по текущей задаче.
4. Сохранять чужие tracked/untracked изменения и не выполнять очистку,
   reset, merge, release, OTA или деплой без прямого разрешения владельца.

## Обязательный барьер от повторных ошибок

Перед серверной/сетевой операцией, записью в GitHub, изменением файлов,
долгим сканированием или сборкой, а также перед любой повторной попыткой сначала
выполнить из корня репозитория:

```text
python ops/maestro-repetition-guard.py check --action <действие> --family <способ>
```

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

### Атомарный барьер попытки

`check` всегда выполняется отдельным вызовом инструмента или отдельной командой.
Его запрещено объединять в одной shell-команде с изменением файлов, тестом,
сетевым действием, `git add`, `commit` или `push`. Один успешный `check` разрешает
ровно одно смысловое действие. Сразу после него нужно прочитать код выхода и
вывод; последующая успешная команда не может маскировать предыдущий сбой.

Если действие завершилось неожиданно, инструмент вернул ошибку или владелец
указал, что способ неверен, следующим исполняемым действием обязан быть `fail`.
До `fail` запрещены повтор, альтернативная команда, другой инструмент и новая
мутация. Затем разрешены только безопасная диагностика, `correct`, отдельный
`check` новой семьи и одна исправленная попытка. Переименование `action` не
сбрасывает блокировку: одинаковая корневая причина считается тем же сбоем. При
повторе корневой причины сначала обновить `AGENTS.md` и постоянный навык, и лишь
потом возвращаться к реализации.

Для изменений исходников действуют дополнительные триггеры:

- создание или изменение patch-файла тоже считается мутацией;
- после второго `context mismatch` для одной смысловой правки запрещены новые
  ручные hunks. Считать фактический текущий файл, построить новый желаемый файл
  вне worktree, получить generated diff из сравнения этих двух файлов, проверить
  его отдельно и применить один раз;

#### Windows generated-patch path rule

If a generated patch is needed on Windows, never build it from absolute drive paths and then rewrite quoted `a/C:\...` headers. Create external `old/<repo-relative-path>` and `new/<repo-relative-path>` mirror trees; run `git diff --no-index --src-prefix=a/ --dst-prefix=b/ -- old/<repo-relative-path> new/<repo-relative-path>` from their common parent; inspect headers; then use `git apply --check --recount -p2 <patch>` before one `git apply --recount -p2 <patch>`. After one absolute-path normalization failure, record it and move to this method; do not tune quoting, slash conversion, prefix stripping, or header text.
Before any `git apply` inside an external mirror, run `git rev-parse --show-toplevel`. If Git discovers an ancestor repository above the mirror, do not trust the process working directory: either set `GIT_CEILING_DIRECTORIES` to the verified mirror parent for both check and apply, or run from the discovered top-level with an explicit verified `--directory=<repo-relative-mirror-root>`. Immediately verify that the intended mirror file changed; exit code 0 without the expected bounded diff is a failed attempt.
- для существующего исходника запрещены `--unidiff-zero` и hunks без устойчивого
  контекста функции/типа;
- после применения патча до staging показать целиком изменённую функцию или
  ограниченный участок с обеими структурными границами, затем выполнить
  `git diff --check` и профильную локальную проверку;
- локально видимую структурную или синтаксическую ошибку запрещено отправлять в
  GitHub «для проверки CI»;
- mutation, validation, `git add`, `git commit` и `git push` — пять отдельных
  смысловых действий, каждое со своим отдельным `check` и проверкой результата.

Не выполнять рекурсивные обходы всего `C:\Users\User\Documents\Codex` на слабом
компьютере владельца. Использовать уже зафиксированный список репозиториев и
узкие `rg`/Git-проверки конкретного проекта; тяжёлые сборки выполнять в GitHub
Actions, если handoff не требует другого проверенного места исполнения.

Отвечать владельцу по-русски.

Для текущей переработки мобильного 4D-интерфейса телевизионная часть доступна
только для чтения: не менять `tvm_*`, TV-компоненты, D-pad/focus/Back,
TV-геометрию и ветки `isTv`.

Если состояние проекта, ветка, коммит, PR, выполненные проверки или следующий
шаг материально изменились, перед завершением обновить `CONTEXT_HANDOFF.md`.

### PowerShell regex generation guard

When a generated Python or PowerShell regex contains both quote types, use a single-quoted here-string as the whole-block template. Write it to an external new mirror, inspect it, and generate the patch from old/new mirrors; never embed that regex in a quoted PowerShell expression. Keep synthetic URL/token fixtures in named in-memory test data and keep owner-supplied endpoints or credentials out of derivative docs and secrecy scans.

### PowerShell exact-source transformation guard

For an exact multiline source transformation, represent the input and output as
explicit line arrays or as one single-quoted here-string, then serialize with
`-join "`n"` and BOM-less UTF-8. A PowerShell single-quoted string treats
backslashes literally: `\n` and `\r\n` are text, not line separators. Assert the
exact match/replacement count, inspect the generated mirror, and only then build
the old/new patch; after this root cause, do not tune another escaped fragment.
