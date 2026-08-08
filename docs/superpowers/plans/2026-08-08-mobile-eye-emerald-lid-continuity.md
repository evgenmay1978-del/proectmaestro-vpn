# Emerald Eyelid Continuity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the animated eyelids reveal the exact emerald `home_ring` material instead of drawing separate bronze closed-eye rasters.

**Architecture:** `home_ring` is already rendered below `LivingEyeMedallion` and contains the registered closed green relief. The eye renderer will draw live anatomy only inside the animated 70/30 aperture; closing the aperture therefore reveals that same underlying material. The scripted preview and phone simulator must mirror this ownership exactly.

**Tech Stack:** Kotlin/Jetpack Compose Canvas, Python 3/Pillow, unittest, GitHub Actions.

## Global Constraints

- Work only on `codex/mobile-4d-deck` and preserve unrelated changes.
- Do not modify TV, `tvm_*`, backend, API, VPN runtime, signing, release, OTA, or `main`.
- Do not run local Gradle, APK, full atlas generation, or the full phone simulator.
- Preserve the 70/30 aperture, current eye size/registration, blink, gaze, touch, pupil, catchlight, and connected glow.
- Do not build an APK before owner approval of exact OFF/ON GitHub images.

---

### Task 1: RED material-continuity contract

**Files:**
- Modify: `ops/test_mobile_eye_state_preview.py`
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometryTest.kt`

**Interfaces:**
- Consumes: `render_home(state: str, scale: int)`, `_replace_material(reference, scale)`, `render_living_eye_layers(closure, scale)`, and `livingEyeRenderPolicy(closure)`.
- Produces: a failing contract that full closure reveals the registered surround and adds no opaque lid overlay.

- [ ] **Step 1: Replace the obsolete opaque-overlay Python assertion**

Compare `render_home("disconnected", scale=2)` with `_replace_material(load_reference(2), 2)` through an eroded copy of `_contour_mask(2, 0.0)`. Require zero pixel difference inside that mask. Separately require the alpha of `eye + seam` at `closure=1.0` to have no bounding box.

- [ ] **Step 2: Replace the obsolete Kotlin policy assertion**

Rename the test to `fullClosureRevealsRegisteredSurroundAndDisablesAnatomyAndGlow`; assert only that open/near-open anatomy and glow remain enabled and that closure at `0.999f` and `1f` disables both. Remove assertions for `lidCoverageEnabled` and `lidCoverageAlpha`.

- [ ] **Step 3: Run the focused RED test**

Run: `python -m unittest ops.test_mobile_eye_state_preview.MobileEyeStatePreviewGeometryTest`

Expected: FAIL because the current bronze `squint/closed` composite changes pixels and leaves opaque overlay alpha inside the original aperture.

- [ ] **Step 4: Commit the RED contract**

Run:

```powershell
git add ops/test_mobile_eye_state_preview.py app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometryTest.kt
git commit -m "test: require emerald eyelid continuity"
```

### Task 2: Remove separate lid material from every renderer

**Files:**
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeMedallion.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometry.kt`
- Modify: `ops/mobile-eye-state-preview.py`
- Modify: `ops/phone-screen-sim.py`

**Interfaces:**
- Consumes: the existing `livingEyeApertureContour` and registered `home_ring` drawn below the eye.
- Produces: live anatomy clipped by one moving aperture, with no `squint/closed` composite and a contact shadow whose alpha reaches zero at full closure.

- [ ] **Step 1: Remove the Kotlin bronze lid draw path**

Delete the two legacy `ImageBitmap.imageResource` loads, `livingEyeLidCoverageContours`, `drawTexturedLidCoverage`, and the render-policy lid fields. Keep the anatomy clip and change the contact-shadow multiplier so both contour overlays fade by `1f - closure`; the baked emerald master owns the final closed fold.

- [ ] **Step 2: Mirror the same ownership in the lightweight preview**

Delete `SQUINT_EYE_PATH`, `CLOSED_EYE_PATH`, `_lid_coverage_mask`, and the texture composite. At full closure return transparent anatomy/overlay layers; multiply seam alpha by `1.0 - phase`.

- [ ] **Step 3: Mirror the same ownership in the phone simulator**

Remove the coverage mask and `mobile_eye_squint.webp` / `mobile_eye_closed.webp` loop. Keep the animated aperture and fade the transient seam to zero at full closure.

- [ ] **Step 4: Run focused GREEN checks**

Run:

```powershell
python -m unittest ops.test_mobile_eye_state_preview
python -m py_compile ops/mobile-eye-state-preview.py ops/phone-screen-sim.py
git diff --check
```

Expected: all 9 preview tests pass, both Python files compile, and `git diff --check` reports no errors.

- [ ] **Step 5: Commit the minimal implementation**

Run:

```powershell
git add app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeMedallion.kt app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometry.kt ops/mobile-eye-state-preview.py ops/phone-screen-sim.py
git commit -m "fix: unify eyelids with emerald surround"
```

### Task 3: GitHub visual proof and durable handoff

**Files:**
- Modify: `CONTEXT_HANDOFF.md`

**Interfaces:**
- Consumes: the exact pushed branch HEAD and workflow task `mobile-eye-runtime-assets`.
- Produces: one successful GitHub run with OFF/ON and blink-phase images plus a current handoff.

- [ ] **Step 1: Push the exact branch HEAD**

Run: `git push origin codex/mobile-4d-deck`

- [ ] **Step 2: Dispatch the existing exact-artifact helper**

Run `python ops/github-actions-artifact.py mobile-eye-runtime-assets` with a long caller timeout so the already-scripted dispatch/wait/download loop is not terminated locally.

Expected: `completed / success`, exact matching HEAD, and an artifact containing six generated atlas WebP plus `phone-screens`, `owner-home-comparison`, and `owner-eye-blink-phases` PNG/JPEG outputs.

- [ ] **Step 3: Compare the accepted baseline and GitHub output**

Inspect the same viewport and states. OFF must contain only emerald material and its natural fold; ON must retain the large live eye. Reject any bronze oval, iris/pupil in OFF, doubled seam, crop, or layout drift.

- [ ] **Step 4: Update and verify the durable handoff**

Record the exact commits, run ID, artifact ID/SHA-256, checks, visual gate, weak-PC rule, and explicit APK/OTA prohibition in `CONTEXT_HANDOFF.md`. Run `git diff --check` and confirm `git status --short --branch` contains only intended documentation before committing and pushing.

- [ ] **Step 5: Show OFF and ON and stop at owner approval**

Present the exact GitHub-rendered images. Do not integrate atlas pages or start the Android APK workflow until the owner explicitly accepts these states.
