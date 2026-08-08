# Mobile Eye Single-Mosaic Aperture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans task-by-task.

**Goal:** Make the eye and emerald mosaic read as one continuous material throughout blinking.

**Architecture:** Keep the registered 4D `ring` as the only mosaic layer. Clip the existing open-eye anatomy to a dynamic aperture; closing the aperture reveals the untouched mosaic below. Remove runtime squint/closed crossfades and the repeated ring painter.

**Tech Stack:** Kotlin, Jetpack Compose Canvas, JUnit4, Pillow simulator, GitHub Actions `android-test.yml`.

## Constraints

- Mobile Home only; no TV, backend, VPN runtime, workflow, Release or OTA changes.
- Preserve eye fit, gaze, pupil, catchlight, touch behavior and timing.
- Do not run Gradle or build APK locally.
- Keep obsolete bitmap files until their auxiliary consumers are audited.

### Task 1: Lock the aperture contract with RED tests

**Files:**
- Modify: `LivingEyeLayerGeometryTest.kt`

- [x] Replace obsolete mosaic-overlay tests with common-X, 70/30 seam and zero-height closed-aperture assertions.
- [x] Update contact-shadow expectation to `3 px / 0.18 alpha`.
- [x] Push a test-only commit and verify the targeted test fails in a CI-only draft PR.

### Task 2: Implement one mosaic and one aperture

**Files:**
- Modify: `LivingEyeLayerGeometry.kt`
- Modify: `LivingEyeMedallion.kt`
- Modify: `Mobile4DHome.kt`

- [x] Interpolate upper/lower sources on one sorted union X-grid.
- [x] Move upper 70% and lower 30% toward one shared seam.
- [x] Draw open-eye anatomy only inside the current aperture; skip all eye pixels at full closure.
- [x] Remove runtime squint/closed crossfades and repeated `ring` painter.
- [x] Use a thin deep-emerald contact seam without changing fit or animation clocks.

### Task 3: Update deterministic visual QA

**Files:**
- Modify: `ops/phone-screen-sim.py`

- [x] Render every state from `mobile_eye_open.webp` clipped by the same union-X aperture.
- [x] Generate a 0/25/50/75/100% blink close-up sheet.
- [x] Assert full closure leaves no eye-layer alpha and the base mosaic stays unchanged outside the aperture.

### Task 4: Verify and build on GitHub

- [x] Run Python simulator, visual checks, `py_compile`, `git diff --check`, and scoped source searches.
- [x] Push implementation; require GitHub Android compile/unit success and APK artifact.
- [x] Record tested SHA, run ID, artifact ID and direct GitHub link in handoff docs.
- [x] Close the CI-only PR without merge; do not touch `main`, Release or OTA.

## Self-review gates

- No second mosaic draw call remains in `LivingEyeMedallion`.
- No `mobile_eye_squint/closed` runtime reference remains.
- Open and closed contours share exactly the same X-grid.
- Closed upper/lower Y coordinates match within `0.001 px`.
- Simulator and Kotlin use the same `0.70` seam share and shadow width.
- Only allowed mobile UI/docs/ops files differ from the base.
