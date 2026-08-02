# Mobile Eye Mosaic Seam Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the bright rectangular patch, bronze ghost and black side remnants from closing and closed eyelids while preserving the registered living-eye animation.

**Architecture:** Keep the existing eye fit, aperture contour and local atlas registration unchanged. Change only the mosaic replacement profile so the exact ring pixels fully replace the complete eye-state rectangle outside the animated aperture by the squint phase.

**Tech Stack:** Kotlin, Jetpack Compose Canvas, JUnit4, GitHub Actions `android-test.yml`.

## Global Constraints

- Phone Home only; TV, backend, VPN runtime, Release and OTA remain untouched.
- Do not rescale or re-register the eye, iris, pupil, catchlight or blink frames.
- Open phase keeps zero mosaic replacement; closing reaches full replacement at phase `0.5`.
- Android compile and unit tests run only in GitHub Actions.

---

### Task 1: Replace the complete eyelid frame with registered mosaic

**Files:**
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometryTest.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometry.kt`

**Interfaces:**
- Consumes: `livingEyeMosaicProfile`, `livingEyeEyelidEnvelopeBounds`, `fitLivingEyeLayer`.
- Produces: full-alpha mosaic replacement over the complete `stateBounds`, with the current aperture excluded by `LivingEyeMedallion`.

- [ ] **Step 1: Write the failing regression test**

Change the squint/closed alpha expectations from `0.78f` to `1f`. For phases `0f`, `0.5f`, and `1f`, assert the computed envelope equals all four edges of `fit.stateBounds`.

- [ ] **Step 2: Verify RED on GitHub**

Push the test-only commit and trigger `android-test.yml` through a CI-only draft PR. Expected: `LivingEyeLayerGeometryTest` fails because current alpha is `0.78f` and the current `0.046 * size` envelope does not reach `stateBounds`.

- [ ] **Step 3: Implement the minimal production change**

In `livingEyeMosaicProfile`, ramp `textureAlpha` to `1f` by phase `0.5` and set `envelopeExpansionPx = size`. The existing clamp in `livingEyeEyelidEnvelopeBounds` then returns the complete state rectangle; do not change `fitLivingEyeLayer`, aperture geometry or painter registration.

- [ ] **Step 4: Verify GREEN on GitHub**

Run `android-test.yml` for the implementation commit. Require success for `:app:assembleOtherDebug`, `:app:testOtherDebugUnitTest`, and artifact `maestrovpn-tv-test-apk`.

- [ ] **Step 5: Verify scope and hand off**

Run `git diff --check`, confirm no TV/backend/workflow diff, update `CONTEXT_HANDOFF.md` with tested SHA/run/artifact, close the CI-only PR without merge, and push the documentation checkpoint.

## Self-Review

- The plan covers both screenshot defects: incomplete coverage and translucent ghosting.
- The protected eye registration and all animation inputs remain unchanged.
- No placeholders or unrelated refactors are included.
