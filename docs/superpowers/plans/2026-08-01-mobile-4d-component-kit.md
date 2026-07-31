# Mobile 4D Component Kit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Создать, проверить и запушить 31 самостоятельный PNG-ассет мобильного 4D component kit без изменения Android- и TV-кода.

**Architecture:** Каталожный лист и выбранный Home используются только как визуальные референсы. Каждый итоговый ассет генерируется отдельным вызовом ImageGen на плоском chroma-key фоне; состояния одной геометрической семьи создаются последовательными reference-edits от канонического варианта, после чего фон удаляется локально и результат нормализуется. Финалы проходят автоматическую PNG-проверку и визуальную comparison-board проверку до одного asset-only коммита.

**Tech Stack:** встроенный ImageGen, Python 3, Pillow, `remove_chroma_key.py`, Git.

## Global Constraints

- Точный состав: 31 PNG из `docs/superpowers/specs/2026-08-01-mobile-4d-component-kit-design.md`.
- Финальный каталог: `design/mobile-asset-redraw/kit/`.
- Каталог нельзя резать, растягивать или использовать как источник пикселей.
- Все финалы: RGBA PNG, 8 бит, sRGB без ICC.
- `home_arc_*` и `home_console_*`: ширина ровно 2160 px.
- На ассетах нет подписей, `MaestroVPN`, глаза или межслойных теней.
- Геометрия состояний кнопок должна совпадать пиксель-в-пиксель.
- Не менять Android-код, runtime-атласы, backend, workflows, TV, `tvm_*`, release или OTA.
- Не запускать Gradle/APK локально на слабом компьютере владельца.
- Если chroma-key не даёт чистую альфу, остановиться и получить отдельное разрешение владельца до CLI `gpt-image-1.5` с native transparency.

---

### Task 1: Подготовить чистый staging и зафиксировать входы

**Files:**
- Read: `design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg`
- Read: `design/mobile-asset-redraw/SPEC.md`
- Read: `docs/superpowers/specs/2026-08-01-mobile-4d-component-kit-design.md`
- Reference only: `../../visual-inputs/mobile-4d-kit-catalog.png`
- Create temporarily: `outputs/mobile-4d-kit-staging/`
- Create finally: `design/mobile-asset-redraw/kit/`

**Interfaces:**
- Consumes: утверждённая спецификация и два визуальных референса.
- Produces: пустой staging, пустой финальный каталог и зафиксированный expected-file list из 31 имени.

- [ ] **Step 1: Проверить ветку и чистоту после docs-коммита**

Run:

```powershell
git status --short --branch
git rev-parse HEAD
git diff --check
```

Expected: ветка `codex/mobile-4d-interface`, HEAD `a639fa4` или его подтверждённый потомок, нет неожиданных изменений.

- [ ] **Step 2: Проверить наличие референсов**

Run:

```powershell
Get-Item design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg
Get-Item design/mobile-asset-redraw/SPEC.md
Get-Item ../../visual-inputs/mobile-4d-kit-catalog.png
```

Expected: все три входа существуют и имеют ненулевой размер.

- [ ] **Step 3: Создать staging и финальный каталог**

Run with explicit repository-relative paths only:

```powershell
New-Item -ItemType Directory -Force ../../outputs/mobile-4d-kit-staging
New-Item -ItemType Directory -Force design/mobile-asset-redraw/kit
```

Expected: оба каталога существуют; в Git пока нет PNG.

### Task 2: Сгенерировать 10 ассетов дуги и консоли

**Files:**
- Create: `design/mobile-asset-redraw/kit/home_arc_l.png`
- Create: `design/mobile-asset-redraw/kit/home_arc_c.png`
- Create: `design/mobile-asset-redraw/kit/home_arc_r.png`
- Create: `design/mobile-asset-redraw/kit/separator_gold.png`
- Create: `design/mobile-asset-redraw/kit/center_jewel.png`
- Create: `design/mobile-asset-redraw/kit/rotation_shadow.png`
- Create: `design/mobile-asset-redraw/kit/rotation_highlight.png`
- Create: `design/mobile-asset-redraw/kit/home_console_l.png`
- Create: `design/mobile-asset-redraw/kit/home_console_c.png`
- Create: `design/mobile-asset-redraw/kit/home_console_r.png`

**Interfaces:**
- Consumes: каталог как style/shape reference и выбранный Home как composition/material reference.
- Produces: 10 RGBA PNG; три arc и три console используют общие внутри семьи 2160 px холсты.

- [ ] **Step 1: Сгенерировать каждый ассет отдельным ImageGen-вызовом**

Use this fixed prompt scaffold. For every call, replace the three brace variables with the exact row from the manifest immediately below:

```text
Use case: stylized-concept
Asset type: premium mobile game-style UI component
Input image 1: owner-selected MaestroVPN Home, composition and material reference only.
Input image 2: component catalog, shape and family reference only; do not crop, trace, upscale, or reuse its pixels.
Primary request: generate {asset} as a standalone production asset.
Subject: {content}. {position}.
Style/medium: dark carved walnut, aged bronze and restrained antique gold, subtle emerald accents, physically convincing embossed relief matching the references.
Composition: isolated component, centered in its required full-width coordinate canvas, generous transparent safety margin.
Background: perfectly flat solid #ff00ff chroma-key, no gradient, no texture, no floor, no reflection, no shadow on the background.
Constraints: no text, no letters, no numbers, no eye, no extra buttons, no watermark; crisp antialiased edges; no #ff00ff inside the component.
```

Exact prompt manifest:

| asset | content | position |
|---|---|---|
| `home_arc_l.png` | left positional section of the lower radial protocol arc, empty carved trapezoidal bays and aged bronze frame | content occupies only the left third of a full-width transparent coordinate canvas |
| `home_arc_c.png` | central positional section of the lower radial protocol arc, empty carved bays and central upper mount | content occupies only the center third of the same full-width coordinate canvas |
| `home_arc_r.png` | right positional section of the lower radial protocol arc, empty carved trapezoidal bays and aged bronze frame | content occupies only the right third of the same full-width coordinate canvas |
| `separator_gold.png` | one reusable thin radial aged-gold divider with a pointed ornamental base | isolated and centered with generous padding |
| `center_jewel.png` | one faceted emerald gemstone in a small symmetric aged-bronze mount | isolated and centered with generous padding |
| `rotation_shadow.png` | only a soft concave semicircular shadow matching the lower protocol arc | spans the full-width arc coordinate canvas; no wood or metal geometry |
| `rotation_highlight.png` | only a restrained warm-gold curved specular highlight matching the lower protocol arc | spans the full-width arc coordinate canvas; no wood or metal geometry |
| `home_console_l.png` | left positional wing of the lower action console with one empty inset action bay and ornate outer corner | content occupies only the left third of a full-width transparent coordinate canvas |
| `home_console_c.png` | central circular medallion base of the lower action console, empty center, symmetric bronze carving and emerald mounts | content occupies only the center third of the same full-width coordinate canvas |
| `home_console_r.png` | right positional wing of the lower action console with one empty inset action bay and ornate outer corner | content occupies only the right third of the same full-width coordinate canvas |
For `rotation_shadow` and `rotation_highlight`, require only the effect and prohibit base wood/metal geometry. For `separator_gold` and `center_jewel`, require isolated reusable components.

Save every returned source immediately under the exact path `../../outputs/mobile-4d-kit-staging/raw/{asset}` using the manifest name. Expected: 10 individual generated source images; no sprite sheet and no embedded labels.

- [ ] **Step 2: Удалить chroma-key и нормализовать размеры**

Run `remove_chroma_key.py` separately for each generated file after defining the exact input and output paths from its manifest name:

```powershell
$assetName = 'home_arc_l.png'
$rawPath = "../../outputs/mobile-4d-kit-staging/raw/$assetName"
$rgbaPath = "../../outputs/mobile-4d-kit-staging/rgba/$assetName"
python C:/Users/User/.codex/skills/.system/imagegen/scripts/remove_chroma_key.py --input $rawPath --out $rgbaPath --auto-key border --soft-matte --transparent-threshold 12 --opaque-threshold 220 --despill
```


Repeat with each exact filename from the Task 2 manifest; do not use globs when copying finals.
Normalize arc/console/effect canvases to 2160 px width with proportional high-quality resampling and transparent padding; keep all three positional parts of each family on identical canvas dimensions.

Expected: clean alpha, no magenta edge, no baked text.

- [ ] **Step 3: Проверить первую семью до продолжения**

Run a Pillow inspection that asserts `mode == "RGBA"`, width `2160` for `home_arc_*` and `home_console_*`, `icc_profile` absent, non-empty alpha bounds, and transparent corner pixels.

Expected: 10/10 PASS. If edge spill remains, retry one targeted ImageGen edit; do not switch to CLI transparency without owner approval.

### Task 3: Сгенерировать 15 элементов конструктора кнопок

**Files:**
- Create: `design/mobile-asset-redraw/kit/button_default_l.png`
- Create: `design/mobile-asset-redraw/kit/button_default_c.png`
- Create: `design/mobile-asset-redraw/kit/button_default_r.png`
- Create: `design/mobile-asset-redraw/kit/button_pressed_l.png`
- Create: `design/mobile-asset-redraw/kit/button_pressed_c.png`
- Create: `design/mobile-asset-redraw/kit/button_pressed_r.png`
- Create: `design/mobile-asset-redraw/kit/button_selected_l.png`
- Create: `design/mobile-asset-redraw/kit/button_selected_c.png`
- Create: `design/mobile-asset-redraw/kit/button_selected_r.png`
- Create: `design/mobile-asset-redraw/kit/button_disabled_l.png`
- Create: `design/mobile-asset-redraw/kit/button_disabled_c.png`
- Create: `design/mobile-asset-redraw/kit/button_disabled_r.png`
- Create: `design/mobile-asset-redraw/kit/button_glow.png`
- Create: `design/mobile-asset-redraw/kit/button_highlight.png`
- Create: `design/mobile-asset-redraw/kit/button_shadow.png`

**Interfaces:**
- Consumes: Task 2 material language; each `default` part becomes the reference-edit source for its three state variants.
- Produces: 12 geometry-compatible 3-slice parts and 3 effect-only overlays.

- [ ] **Step 1: Сгенерировать три канонических default-части**

Use the Task 2 scaffold with these invariants repeated in every prompt:

```text
Three-slice button constructor part; empty interior; no label and no icon.
The left and right parts contain end-cap ornament; the center part has perfectly matching straight join edges and is horizontally tileable.
Dark walnut inset with aged bronze/gold carved border; reference-faithful, not modern UI.
```

Expected: `button_default_l/c/r` with compatible height and join edges.

- [ ] **Step 2: Создать девять state variants reference-edit вызовами**

For every default part, run separate edits for pressed, selected, and disabled:

```text
Change only the interaction state. Preserve canvas, silhouette, dimensions, join edges, ornament geometry and transparent alpha shape exactly.
PRESSED: recessed depth, darker inner walnut, reduced upper highlight.
SELECTED: same geometry with restrained emerald inset glow and fine green accent, no solid green fill.
DISABLED: same geometry, desaturated bronze and walnut, reduced contrast, still readable.
No text, no icon, no extra object, flat #ff00ff background.
```

Expected: 9 additional sources with matching geometry.

- [ ] **Step 3: Сгенерировать три effect-only ассета**

Generate `button_glow`, `button_highlight`, and `button_shadow` separately. Each contains only the named effect on flat #ff00ff, aligned to the canonical center-part footprint, without button wood or border.

Expected: reusable transparent effect overlays.

- [ ] **Step 4: Удалить chroma-key и проверить геометрию**

Apply the same chroma-key command as Task 2. Normalize all 12 button parts so the four states of each positional part have identical dimensions. Compare alpha masks per positional part; any differing alpha pixel outside a 1 px antialias tolerance is a FAIL and requires a targeted edit/regeneration.

Expected: 15/15 PASS and stable three-slice joins.

### Task 4: Сгенерировать 3 статусные панели и 3 контактные иконки

**Files:**
- Create: `design/mobile-asset-redraw/kit/connected_panel.png`
- Create: `design/mobile-asset-redraw/kit/connecting_panel.png`
- Create: `design/mobile-asset-redraw/kit/error_panel.png`
- Create: `design/mobile-asset-redraw/kit/telegram.png`
- Create: `design/mobile-asset-redraw/kit/whatsapp.png`
- Create: `design/mobile-asset-redraw/kit/max.png`

**Interfaces:**
- Consumes: the same walnut/bronze/emerald visual language.
- Produces: three geometry-compatible empty status panels and three standalone recognizable contact glyphs.

- [ ] **Step 1: Сгенерировать canonical connected panel**

Prompt invariants:

```text
Empty compact status panel with aged bronze carved frame and dark walnut inset.
Connected state uses one restrained emerald indicator/accent.
No text, no letters, no numbers, no logo, no icon label.
Flat #ff00ff chroma-key background.
```

- [ ] **Step 2: Создать connecting и error reference-edits**

Preserve exact panel geometry. Change only state accent: amber/gold for connecting, ruby red for error. Do not add words or symbols.

Expected: three status panels with identical alpha geometry.

- [ ] **Step 3: Сгенерировать три иконки отдельно**

Prompt each as a standalone 192×192-target glyph, no enclosing button and no text. Preserve recognizable Telegram paper plane, WhatsApp handset-in-chat-circle, and MAX contact mark while rendering them as aged gold/bronze glyphs consistent with the owner reference.

Expected: three visually distinct icons without labels.

- [ ] **Step 4: Удалить chroma-key и проверить**

Apply the same alpha cleanup. Normalize icons to 192×192 transparent canvases without enlarging catalog pixels. Assert identical status panel dimensions and alpha masks.

Expected: 6/6 PASS.

### Task 5: Выполнить полную автоматическую и визуальную приёмку

**Files:**
- Verify: `design/mobile-asset-redraw/kit/*.png`
- Create temporarily: `outputs/mobile-4d-kit-comparison.png`

**Interfaces:**
- Consumes: all 31 final candidates.
- Produces: machine-readable PASS evidence and one non-committed comparison board.

- [ ] **Step 1: Проверить точный inventory**

Expected sorted names are copied verbatim from the design spec. Assert exactly 31 PNG, no missing and no extra files.

- [ ] **Step 2: Проверить PNG metadata и alpha**

For each file, Pillow must assert:

```python
assert image.format == "PNG"
assert image.mode == "RGBA"
assert image.info.get("icc_profile") is None
assert image.getchannel("A").getbbox() is not None
assert image.getpixel((0, 0))[3] == 0
```

Also assert 2160 px width for `home_arc_*` and `home_console_*`, and 192×192 for the three contact icons.

- [ ] **Step 3: Проверить геометрию семей**

Compare canvas sizes and alpha masks for:

- four states of each `button_*_l`, `button_*_c`, `button_*_r` family;
- `connected_panel`, `connecting_panel`, `error_panel`;
- the three arc canvases;
- the three console canvases.

Expected: exact family canvas match; state masks exact except allowed 1 px antialias tolerance documented in the check output.

- [ ] **Step 4: Собрать comparison board**

Create a non-committed board with the source catalog on the left and all generated finals grouped on the right. Inspect at 100% and 400% for wrong labels, clipped ornaments, inconsistent metal, halos, black/magenta fringes, broken joins, accidental backgrounds and unreadable icons.

Expected: no catalog pixels reused; generated families visibly match the same material language.

- [ ] **Step 5: Проверить Git scope**

Run:

```powershell
git status --short
git diff --check
git diff --name-only a639fa4..HEAD
```

Expected before commit: only 31 PNG under `design/mobile-asset-redraw/kit/` are untracked; no Android, TV, backend, workflow, release or OTA changes.

### Task 6: Создать asset-only коммит и отправить ветку

**Files:**
- Add: `design/mobile-asset-redraw/kit/*.png`
- Verify only: `docs/superpowers/specs/2026-08-01-mobile-4d-component-kit-design.md`
- Verify only: `docs/superpowers/plans/2026-08-01-mobile-4d-component-kit.md`

**Interfaces:**
- Consumes: полностью проверенный набор Task 5.
- Produces: один asset-only commit и обновлённая ветка `origin/codex/mobile-4d-interface`.

- [ ] **Step 1: Добавить только точный каталог kit**

Run:

```powershell
git add -- design/mobile-asset-redraw/kit
git diff --cached --check
git diff --cached --name-only
```

Expected: ровно 31 PNG, без иных staged-файлов.

- [ ] **Step 2: Создать коммит**

Run:

```powershell
git commit -m "design: add mobile 4D component kit"
```

Expected: один новый asset-only commit.

- [ ] **Step 3: Проверить финальную область изменений**

Run:

```powershell
git status --short --branch
git diff --check origin/codex/mobile-4d-interface..HEAD
git diff --name-only origin/codex/mobile-4d-interface..HEAD
```

Expected: docs spec, implementation plan and 31 kit PNG; нулевой diff приложения и TV.

- [ ] **Step 4: Push без merge/release/OTA**

Run:

```powershell
git push origin codex/mobile-4d-interface
```

Expected: remote branch advances; draft PR #74 receives docs and asset kit. No APK workflow is required for files under `design/`.
