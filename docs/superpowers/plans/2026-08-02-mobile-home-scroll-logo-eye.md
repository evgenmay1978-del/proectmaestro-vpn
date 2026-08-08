# Mobile Home Scroll, Logo And Eye Integration — Implementation Plan

> **Execution:** use `subagent-driven-development`; one implementer at a time, then scoped review.

**Goal:** исправить отрыв текста от резного арта при прокрутке, убрать пересечение статуса с
медальоном, удалить пояснение поддержки, привести титул к эталону и визуально встроить полностью
живой глаз в мозаику без изменения его анатомии.

**Architecture:** верх Home остаётся фиксированным. Один `ScrollState`, созданный в
`Mobile4DHome`, управляет Compose-декой и пиксельным переносом только atlas-слоёв `contacts`,
`arc`, `console`. Рантайм z-order сохраняется. Eye assets и animation state machine не меняются;
добавляются только кодовые occlusion/contact shadows внутри существующего clip.

**Global constraints:**

- только мобильный Home;
- не менять `TvHomeScreen.kt`, `TvEskizHome.kt`, `TvEskizSpec.kt`, `SFANavigation.kt`, `tvm_*`,
  TV tests/simulators, D-pad/focus/Back;
- не менять backend, VPN runtime, API, release, OTA и workflows;
- не запускать локальный Gradle/APK;
- не добавлять старый flattened Home, полноэкранные маски или второй UI поверх первого;
- глаз сохраняет blink/squint/gaze/pupil/touch/catchlight и три VPN-состояния;
- каждый task заканчивается лёгкими проверками и отдельным commit; GitHub Actions запускается
  один раз после итогового push.

## Task 1: RED-контракты новой геометрии и прокрутки

**Files:**

- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayoutTest.kt`
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeControlDeckContractTest.kt`
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DSceneModelTest.kt`
- Modify: `app/src/test/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometryTest.kt`

Add failing expectations for:

- deck top `434`, status `434…456`, protocol `456…476`, phone `478…516`;
- primary bottom `864` and required scroll at 390×844;
- no `supportNote` contract;
- exact fixed-vs-scroll layer ownership and equal `-scrollPx` only for `console/contacts/arc`;
- static lower-deck art shift `+25 dp`;
- eye registration unchanged and integration profile present without changing fit scale.

Do not run Gradle locally. Record RED by showing current production constants contradict these
expectations; the first GitHub unit execution is the executable gate.

## Task 2: Один scroll owner, новая вертикаль и исправленный титул

**Files:**

- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DHome.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeControlDeck.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/PhoneHomeReferenceLayout.kt`

Implement:

1. Create one remembered deck `ScrollState` in `Mobile4DHome` and pass it to scene and deck.
2. Classify `console`, `contacts`, `arc` as deck-scrolling relief layers. In the existing Canvas,
   keep z-order and add `+25 dp - scrollState.value` only to these layers, clipped below `434 dp`.
3. Remove the deck-owned `rememberScrollState()` and use the passed instance for the real
   Compose scroll container.
4. Apply the approved bounds; remove support-note UI/constant/tag.
5. Move contact interiors, protocol cells, buy and console controls by the same `+25 dp` as art.
6. Change title target to `38 sp` / `3.5 sp`, retaining the three-layer carved treatment.
7. Remove only imports and helpers orphaned by this task.

Verification: static source checks, contract-value inspection and `git diff --check`; no Gradle.

## Task 3: Физически встроить живой глаз, не менять анатомию

**Files:**

- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeMedallion.kt`
- Modify: `app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometry.kt` only if
  a pure integration-profile helper is needed
- Modify: `ops/mobile-eye-natural-assets.py`
- Modify: relevant JVM/Python guard tests

Implement permanent inner occlusion shadow and restrained state-aligned eyelid contact shadow
inside the current bronze clip. Keep every animation coroutine, gaze/pupil value, aperture point,
asset registration and state mapping unchanged. Make the old eye rebuild script refuse any output
with a non-transparent background outside the eyelid contour; it must not recreate the obsolete
elliptical backing.

Verification: eye alpha/registration guard, Python tests and three-state simulator render.

## Task 4: Синхронизировать simulator и визуальную приёмку

**Files:**

- Modify: `ops/phone-screen-sim.py`
- Modify: `ops/README.md`
- Modify: `design-qa.md`

Mirror the exact layout, title, eye-integration profile and fixed/scroll layer classification.
Generate normal three-state previews plus a scrolled connected preview that proves fixed logo/eye
and equal movement of lower art and labels. Run `python ops/phone-screen-sim.py` and inspect the
comparison board against `04-owner-selected-home-2026-07-31.jpg` and the owner device screenshot.

## Task 5: Scope gates, documentation and GitHub APK

**Files:**

- Modify: `CLAUDE_MOBILE_REBUILD.md`
- Modify: `CONTEXT_HANDOFF.md`
- Modify only other mobile documentation made stale by Tasks 1–4

Run lightweight gates:

- `git diff --check`;
- searches for support copy, independent deck scroll owner, old revolver/flattened Home;
- zero diff for forbidden TV/backend/release/OTA paths;
- Python simulator and relevant Python tests;
- independent task reviews and final whole-branch review.

Then commit, push only to `codex/mobile-4d-deck`, verify remote SHA, dispatch/observe
`android-test.yml`, verify both `assembleOtherDebug` and `testOtherDebugUnitTest`, verify a real
APK artifact exists, and give the owner its GitHub artifact link. No merge, release or OTA.
