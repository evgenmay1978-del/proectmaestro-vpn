# Mobile 4D Arc Rerender Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Заменить три ошибочных позиционных `home_arc_*` на три полноширинных световых варианта одной семисекторной дуги и исправить документацию CI.

**Architecture:** Центральная дуга создаётся один раз по двум референсам владельца. Левый и правый свет получаются отдельными reference-edit вызовами от центральной версии; после удаления chroma-key каноническая alpha центральной версии применяется ко всем трём, а финалы помещаются в одну абсолютную область мастер-холста 2160×4670.

**Tech Stack:** встроенный ImageGen, `remove_chroma_key.py`, Python 3 + Pillow только для детерминированной нормализации/проверки, Git.

## Global Constraints

- Меняются только три `design/mobile-asset-redraw/kit/home_arc_*.png` и документы.
- Финалы: RGBA PNG 2160×4670, 8 бит, sRGB без ICC.
- Семь пустых секторов; текст, иконки, jewel и отдельные separators отсутствуют.
- Alpha L/C/R идентична пиксель-в-пиксель.
- Эталон Home: `design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg`.
- TV, `tvm_*`, backend, workflows, release и OTA не меняются.
- Локальный Gradle/APK запрещён владельцем.
- Push выполняется только в `codex/mobile-4d-deck`, не в PR #74.

---

### Task 1: Зафиксировать ошибку и входы

**Files:**
- Verify: `design/mobile-asset-redraw/kit/home_arc_l.png`
- Verify: `design/mobile-asset-redraw/kit/home_arc_c.png`
- Verify: `design/mobile-asset-redraw/kit/home_arc_r.png`

**Interfaces:**
- Consumes: текущий ошибочный набор.
- Produces: воспроизводимый RED-результат проверки alpha.

- [ ] **Step 1: Запустить Pillow assertion одинаковой full-width alpha**

Ожидаемый результат до исправления: FAIL с `arc L/C/R alpha geometry differs`.

- [ ] **Step 2: Зафиксировать фактические alpha bounds**

Ожидаемый результат: три непересекающиеся трети холста 2160×1080.

### Task 2: Создать каноническую центральную дугу

**Files:**
- Create temporarily: `outputs/mobile-4d-arc-rerender/raw/home_arc_c.png`
- Create temporarily: `outputs/mobile-4d-arc-rerender/rgba/home_arc_c.png`

**Interfaces:**
- Consumes: owner Home и каталог группы 6 как visual reference only.
- Produces: один центрально освещённый arc source без текста и декораторов.

- [ ] **Step 1: Выполнить отдельный ImageGen-вызов**

Prompt обязан потребовать один цельный семисекторный резной веер на идеально
плоском `#ff00ff`, без текста, иконок, jewel, separators, теней на фон и без
использования пикселей каталога.

- [ ] **Step 2: Удалить chroma-key штатным helper**

Run:

```powershell
python C:/Users/User/.codex/skills/.system/imagegen/scripts/remove_chroma_key.py --input outputs/mobile-4d-arc-rerender/raw/home_arc_c.png --out outputs/mobile-4d-arc-rerender/rgba/home_arc_c.png --auto-key border --soft-matte --transparent-threshold 12 --opaque-threshold 220 --despill
```

- [ ] **Step 3: Проверить центральную дугу**

Ожидается чистая alpha, семь пустых секторов и отсутствие magenta fringe.

### Task 3: Создать L/R-варианты без геометрического дрейфа

**Files:**
- Create temporarily: `outputs/mobile-4d-arc-rerender/raw/home_arc_l.png`
- Create temporarily: `outputs/mobile-4d-arc-rerender/raw/home_arc_r.png`
- Create temporarily: `outputs/mobile-4d-arc-rerender/rgba/home_arc_l.png`
- Create temporarily: `outputs/mobile-4d-arc-rerender/rgba/home_arc_r.png`

**Interfaces:**
- Consumes: канонический `home_arc_c`.
- Produces: две reference-edit версии с изменённым только направлением света.

- [ ] **Step 1: Выполнить два отдельных reference-edit вызова**

В каждом prompt повторить: `change lighting only; preserve canvas, silhouette,
seven-sector geometry, carving and joins exactly`.

- [ ] **Step 2: Удалить chroma-key тем же helper**

Ожидается три RGBA source с одинаковой общей композицией.

- [ ] **Step 3: Нормализовать alpha и мастер-координаты**

Использовать alpha канонической `_c` для всех трёх, пропорционально привести
локальный arc к ширине 2160 и поместить в `y=3150..3905` прозрачного холста
2160×4670. Не менять RGB художественно локальным скриптом.

### Task 4: Заменить финалы и выполнить GREEN-проверку

**Files:**
- Modify: `design/mobile-asset-redraw/kit/home_arc_l.png`
- Modify: `design/mobile-asset-redraw/kit/home_arc_c.png`
- Modify: `design/mobile-asset-redraw/kit/home_arc_r.png`

**Interfaces:**
- Consumes: три нормализованных master PNG.
- Produces: финальный L/C/R-набор.

- [ ] **Step 1: Скопировать только три точных файла**

- [ ] **Step 2: Повторить RED assertion**

Ожидается PASS: 2160×4670, identical alpha, alpha bbox ≥90% width и в зоне
протокольной дуги.

- [ ] **Step 3: Проверить RGB-разницу**

Ожидается ненулевая visible RGB difference `_l↔_c` и `_c↔_r` без изменения
alpha.

- [ ] **Step 4: Собрать comparison board**

Показать owner Home и `_c` в одном viewport 390×844; проверить отсутствие
обрезки, текста и лишних слоёв.

### Task 5: Документация, scope и Git

**Files:**
- Modify: `docs/superpowers/specs/2026-08-01-mobile-4d-component-kit-design.md`
- Modify: `docs/superpowers/plans/2026-08-01-mobile-4d-component-kit.md`
- Modify: `design/mobile-asset-redraw/KIT.md`
- Modify: `CONTEXT_HANDOFF.md`

**Interfaces:**
- Consumes: проверенные финалы и подтверждённый Actions run.
- Produces: UTF-8 handoff без ложной гарантии о CI.

- [ ] **Step 1: Исправить старый план**

Отметить positional arc steps как superseded и записать, что path filter
`pull_request` оценивает полный three-dot diff PR.

- [ ] **Step 2: Обновить handoff**

Записать branch/HEAD, три заменённых файла, проверки и правило: PR #74 = CI на
любой push; asset staging = `codex/mobile-4d-deck`.

- [ ] **Step 3: Проверить scope**

Run:

```powershell
git diff --check
git status --short --branch
git diff --name-only 5679391..HEAD
```

Ожидается: три PNG и документы; нулевой diff TV/backend/workflows/release/OTA.

- [ ] **Step 4: Commit и push обходной ветки**

Commit локально, затем push только `codex/mobile-4d-deck`. После push проверить,
что для нового SHA не появился pull-request workflow run.
