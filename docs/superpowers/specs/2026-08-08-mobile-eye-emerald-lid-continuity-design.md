# Emerald Eyelid Continuity Design

## Goal

The animated eye must read as one object with its surrounding relief. Upper and lower eyelids use the exact registered emerald `home_ring` material; no bronze or gold closed-eye raster may be drawn over it.

## Confirmed root cause

`LivingEyeMedallion`, the lightweight preview, and the full phone simulator currently composite `mobile_eye_squint` and `mobile_eye_closed` inside moving coverage polygons. Those legacy rasters are predominantly bronze/brown, while `mobile_eye_surround_c.png` and the generated `home_ring` centre are emerald. The overlay therefore creates the visibly separate bronze oval rejected by the owner.

## Considered approaches

1. **Reveal the registered surround (selected).** Keep the animated 70/30 aperture, but draw anatomy only inside that aperture. As it closes, the already-rendered `home_ring` becomes visible. This gives pixel-identical material and automatically follows L/C/R lighting and parallax without a duplicate asset.
2. Create new L/C/R moving green lid textures. This duplicates registered material, adds atlas/memory cost, and can produce seams during light blending.
3. Recolour the legacy bronze frames. Their metallic relief remains different, so this cannot satisfy the one-piece requirement.

## Selected behavior

- `Stopped` and `Stopping`: anatomy and glow are absent. The registered emerald closed-eye relief in `home_ring` is fully visible.
- `Starting`: the same live aperture opens halfway; no state-frame swap occurs.
- `Started`: the aperture is open; blink, gaze, touch response, iris, pupil, catchlight, and connection glow remain unchanged.
- The upper contour still travels 70% and the lower contour 30%.
- The runtime contact-shadow overlay fades to zero at full closure so the closed fold already present in the emerald master is not darkened or doubled.
- `mobile_eye_squint` and `mobile_eye_closed` may remain as historical resources, but no active Home renderer or QA renderer may composite them.

## Ownership and scope

`home_ring` remains the single owner of the bronze frame and emerald eye-surround. `LivingEyeMedallion` owns only live anatomy, aperture motion, transient aperture shadow, gaze/touch/pupil/catchlight, and connected glow. No TV, backend, VPN runtime, signing, release, OTA, or unrelated Home geometry changes are allowed.

## Verification contract

- A RED test must show that the current fully closed frame differs from the material-only emerald baseline inside the original open aperture.
- GREEN requires the fully closed rendered Home to be pixel-identical to that baseline in the aperture, with no active overlay alpha there.
- Connected output must still differ from closed output and preserve the accepted open-eye geometry.
- The same rule must be mirrored in Kotlin, the lightweight scripted preview, and the full phone simulator.
- Heavy atlas generation and full simulator run only in GitHub Actions. The weak local computer runs only focused Python tests, `py_compile`, and static diff checks.

## Acceptance gate

Show the owner exact OFF and ON images from one successful GitHub Actions run. Do not build an APK until those images are accepted. Do not perform OTA without a separate explicit instruction.
