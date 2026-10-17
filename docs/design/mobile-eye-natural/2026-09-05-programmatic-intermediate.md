# Registered eye: intermediate, not accepted for release

The owner explicitly permitted programmatic raster processing. This is a local source candidate,
not an APK/release, runtime screenshot, or completion of the three visual acceptance criteria.

**Latest source — WIP, visually unaccepted:** registered original ocular tissue with
58 upper / 18 lower lid-attached follicles, dark tapered bodies, short muted shaft
ridges and proximal curves crossing the ocular edge. Kotlin and Pillow share the
authored geometry. The owner rejected the prior dark-fan phone-scale visibility;
the subsequent outside-only dense candidate also remained insufficient at390dp.
The current aperture-crossing candidate has been rendered but is not accepted as
anatomically convincing or as satisfying all three visual requirements.

Current preview background is the owner's unchanged September reference
`design/mobile-4d-references/10-owner-installed-home-2026-09-01.jpg`, not the
August screenshot with baked WDTT/WEBRTC cards. It contains no invented CDN card.
The analytic spherical sclera and earlier experiments below are superseded
history, not simultaneous options. No APK or device visual proof is claimed.

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

## Eyelashes: same moving lid edges, unchanged master

The owner identified the missing lashes in the actual-source preview. A separate procedural layer
now adds 29 upper and 12 shorter, lighter lower lashes. Authored unequal follicle spacing, length,
sweep and opacity avoid identical radial spokes. Each hair is a narrow cubic ribbon tapering to a
point, rooted directly on the existing interpolated lid contour. A half-width muted shaft reflection
keeps dark hairs legible against the dark green relief, without a bright lash outline. Upper lashes
roll down with closure; the lower row becomes less visible under the closed upper lid. The two aperture corners,
green-master file, sclera/iris/catchlight assets and common anatomy transform are unchanged.

The overlay is drawn after lid contact shadows and clipped by the bronze socket, not the eye
opening. CLOSED still draws no ocular anatomy or glow; only the lashes now cover small parts of the
original green fold. The preview reads the follicle specifications from the Kotlin source and
uses the same cubic geometry with small-socket antialiasing. The existing closed-surround check
retains pixel identity everywhere outside the explicit new lash support.

New actual-source artifacts belong in task scratch `eye-programmatic-v1/eyelashes-preview`.
They remain labelled scripted previews, not Android runtime captures. Geometry assertions cover
root attachment during OPEN/half/CLOSED, deterministic irregularity and the sparser lower row.
All 11 focused Pillow preview tests passed locally in 20.103 seconds, including retained fixed-corner,
zero CLOSED anatomy/glow and outside-owned-pixel assertions. Full-home OPEN/CLOSED previews were rendered
from these sources and inspected; lashes remain deliberately understated on the textured green material.
Android compilation/device appearance and owner all-three visual acceptance remain pending;
no local Gradle/Go/heavy tests, APK, server, Git write or CI action are part of this pass.

### Home-size lash visibility correction

Root review found that the first lash profile still disappeared at normal Home size. Its upper
root width was only 0.51–0.73 dp and the shaft tapered below that; the half-width reflection lost
further coverage during sampling over the master's high-frequency green relief. The lashes were
already after contact shadows, outside the aperture clip and inside the bronze socket, so changing
clip/order or repainting the master would not address that cause.

The corrected authored profile increases upper roots to 1.02–1.40 dp, with unequal lengths and
stronger lateral curls; a few closer follicle pairs break the regular spacing. Lower roots remain
thinner (0.57–0.73 dp) and the lower row retains 12 hairs versus 29 above. The muted reflection is
not brightened. The cubic tip now turns sideways after its arch, rather than reading as a nearly
straight shaft when enlarged. Roots still use the same exact moving lid contour, including CLOSED;
no anatomy asset, green-master pixel, layer ordering or clipping contract changed. Kotlin and Pillow
share the revised cubic control factors; the preview consumes follicle values directly from Kotlin.
A new full-Home comparison and
enlarged two-state eye crop are rendered to scratch `eye-programmatic-v1/eyelashes-visible-preview`.

### Latest lash style: dark chestnut, fanned roots, no reflected wire

The thickened reflected profile above was rejected in detail review: the bright inner ribbon and
near-vertical root tangents resembled a pale wire fence. The current candidate removes the entire
half-width reflection and draws a single dark-chestnut (`#281910`) tapered shaft. Width is reduced
16%, not increased for visibility. Fan inclination now starts at the root and varies across the
lid; an independent authored sweep bends the mid-shaft rather than merely hooking a straight tip.
Unequal close pairs alternate with wider gaps; paired hairs differ in length and curl. The lateral
row fans toward the outer corner. Lower hairs remain fewer, shorter and lighter in opacity.

Kotlin and Pillow use the same fan/curl factors and the same runtime follicle specifications. The
master, aperture corners, blink attachment, bronze clip and test oracles are unchanged. This single
style candidate is rendered to scratch `eye-programmatic-v1/eyelashes-dark-fan-preview`, including
a two-state enlarged crop derived directly from its actual-source Home pixels. This is still a
scripted source preview, not an Android screenshot or a claim of owner anatomical acceptance.
