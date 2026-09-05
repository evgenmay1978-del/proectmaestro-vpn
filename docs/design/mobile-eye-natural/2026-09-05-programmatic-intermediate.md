# Registered eye: intermediate, not accepted for release

The owner explicitly permitted programmatic raster processing. This is a local source candidate,
not an APK/release, runtime screenshot, or completion of the three visual acceptance criteria.

**Latest source:** registered original ocular tissue, described in the final section below.
The analytic spherical sclera and earlier experiments are superseded, not simultaneous options.

- The green master `mobile_eye_surround_c.png`, bronze art, CLOSED state and shared anatomy fit
  remain unchanged. Master SHA-256: `0f5d565c2a269579166723b7b59532cde7032cb0b7ea668847b95c5531f278ca`.
- OPEN and blink now meet the original closed green fold at fixed corners. The old fixed 70/30
  interpolation generated a different closure line and has been replaced by registered samples.
- The renderer no longer draws the old OPEN photograph underneath the anatomical layers: its
  decorative bronze eyelid rim produced a second visible boundary.
- Three small runtime sprites have changed. Sclera keeps original tissue coordinates where usable,
  with local legacy dark-rim removal. Catchlight is softened; the runtime pupil gradient is unchanged.
- Iris uses only the inspected green interior of the previously generated 1254×1254 RGB image
  `exec-9191a7ad-1767-422f-9c1f-9fd987baf0d8.png`: centre (627,627), maximum sampled radius 548.
  Exterior pixels are not sampled. A real alpha channel is constructed inside the 292×292 sprite,
  with centre (146,145), radius 145 and 2.5 px inward feather. The rejected fake-checkerboard
  background itself is not an application asset.
- `ops/mobile-eye-state-preview.py` now composites the actual sclera, iris, pupil, catchlight,
  original closure contour and bronze clip. Its old phone background is only a visual reference;
  pictured WDTT/WEBRTC buttons are not evidence of current capabilities.

Independent visual review confirms the separate bronze rim is gone, the new iris/reflection is
more natural, and CLOSED/outer green remain preserved. **Still open:** sclera looks too flat at the
two side triangles and needs anatomical corner/waterline refinement. All-three visual acceptance,
Android runtime validation and APK release remain explicitly uncompleted.

Bounded local checks passed: preview Python syntax, Kotlin/preview contour equality, fixed corners,
zero anatomy/seam/glow at CLOSED, real alpha and required sprite dimensions, unchanged green-master
SHA, and identical CLOSED preview pixels. `git diff --check` is clean for the changed eye sources.
No local Gradle/Android/APK build or runtime test was performed; geometry JUnit changes await CI.

A subsequent scratch-only native-corner refinement retained unscaled original vascular tissue,
but introduced visible dark patch boundaries. It was not installed in application assets and is
not accepted. The source remains the independently reviewed intermediate above.

One further bounded scratch experiment used analytic globe lighting, upper-lid occlusion, a thin
wet lower margin, sparse branching vessels and high-frequency native tissue detail (without its
old low-frequency shadow patches). Alpha was identical to the intermediate. Full-home inspection
showed that patch boundaries were reduced, but the sampled native tone became too yellow/olive
and the eye still read as graphic rather than convincingly anatomical. This experiment was also
not integrated. No additional variants or image-generation requests were made in that pass.

## Current source: continuous lid shadow, still intermediate

A subsequent focused correction addresses the independently observed pasted-iris lighting:
`LivingEyeMedallion` casts a contour-following upper-lid shadow over **all** anatomy, including the
iris and corneal reflection. It fades through the upper 30% of the opening; a softer lower contact
shadow occupies its final 8%. A thin wet meniscus remains inside the aperture. Both shadows follow
the existing blink contour rather than drawing a new lid/green border. The preview uses the same
band counts, opacity and contour fractions.

Sclera now uses continuous globe lighting with neutral desaturated ivory derived from the original
tissue luminance, not its rejected yellow/olive chroma. Subtle native high-frequency tissue detail
is retained without transplanting the old large dark patches. The alpha mask, accepted iris,
catchlight, green master and CLOSED geometry remain unchanged. Source full-home inspection shows
better lighting integration; the lateral surfaces still appear stylized, so anatomical/all-three
acceptance remains open. No APK or runtime acceptance is implied.

The bounded alpha/geometry/master checks and eye-source `git diff --check` passed again after this
correction. Kotlin compilation/runtime validation still belongs to GitHub CI, not the weak local PC.

### Spherical continuity refinement

The current sclera replaces the broad 382×250 shading ellipsoid with a radius-340 sphere centred
at source (681,1045). Its neutral surface becomes brighter continuously toward the iris boundary
and darker near the canthal tangent. The light direction `(0,-0.16,sqrt(1-0.16^2))` projects near
(681,990), matching the existing corneal highlight rather than introducing another unrelated
light. The sclera's baked meniscus was removed so there is only the shared runtime wet margin.

This pass changes only the sclera raster relative to the preceding source candidate. Iris,
catchlight, alpha, aperture/CLOSED contour, original green and runtime shadow geometry are unchanged.
The actual-source full-home preview shows a stronger, continuous illumination gradient; the
smooth opening and circular iris still look stylized. **All-three visual acceptance remains OPEN.**
No generation, network requests, local builds, installation, commit or publication occurred.

## Latest source: registered original ocular tissue

Inspection of `mobile_eye_open.webp` showed that the native ocular interior already provides
convincing scleral light, vessels and a moist medial corner. That tissue now replaces the analytic
sclera, using one registered crop/warp rather than another green-roll/shader layer. The decorative
bronze perimeter and old iris are not used as an additional rendered layer.

The actual ocular edge was measured on the original asset; the earlier aperture samples included
part of its decoration and were unsuitable as crop boundaries. Source corner/iris-side x anchors
are 385/546/816/911 and map to the unchanged target corner/iris-side anchors. Sampling stays inward
of the native ocular edge and outside the old radius-139 limbus. Existing tissue luminance is kept;
old yellow chroma is desaturated without replacing the native shading with flat gray patches.

Crop review used an **opaque alpha-composited proof**: hidden RGB beneath zero alpha must not be
mistaken for included bronze. Actual-source full-home review shows materially better scleral
volume and corner anatomy, without the prior plain gray triangular areas. Independent all-three
visual acceptance still remains OPEN; this is not an APK or runtime acceptance claim.

Only the sclera and this note changed in this pass. Alpha, iris, catchlight, outside green and
CLOSED/shared contour are unchanged; the existing invariant checks passed. The superseded sphere
candidate remains recoverable in the task scratch `spherical-continuity` directory. No local
heavy build, new generation, server action, commit or CI dispatch was performed.
