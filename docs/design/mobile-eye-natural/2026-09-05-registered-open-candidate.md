# Registered open-eye candidate — 2026-09-05

Status: **PREVIEW ONLY — not accepted, not integrated into runtime.** This is a
documentation checkpoint while authenticated CDN management UI access remains
unconfirmed. Commercial delivery remains the primary task. No Kotlin, runtime
asset, TV component, APK, workflow, service or CDN configuration changed.

## Provenance and references

- Source review revision: `43d9ad4fc7b17117bb6326739a4bf62a8cc9d710`.
- [Candidate PNG](2026-09-05-registered-open-candidate.png), copied unchanged;
  the original generated file is retained separately.
- Candidate SHA-256:
  `8b8d247a2cc2b684437a14c2e74c3253e2d1454c993ffd2a4f8c5d3de2c3fefb`.
- The single image-generation reference/edit target was
  `design/mobile-asset-redraw/materials/mobile_eye_surround_c.png`, SHA-256
  `0f5d565c2a269579166723b7b59532cde7032cb0b7ea668847b95c5531f278ca`.
- Runtime ring source inspected alongside it:
  `design/mobile-asset-redraw/source/home_ring_c.png`, SHA-256
  `25cdba7899da5088f4a892b12227669ce745f5368fa2eb1af21cc78f3b9fc5bf`.
- Production remains `1.0.157`; `1.0.158-task7-test` is not production. This
  source review does not identify either screenshot's installed source SHA.

## Confirmed source facts and visual limits

`TvHomeScreen` selects `Mobile4DHome` only for mobile. TV uses `TvEskizHome`, not
`LivingEyeMedallion`. Mobile draws the ring from generated runtime atlas pages
`atlas_{l,c,r}_{07,08}.webp`, below the live-eye Canvas; it does not load the
design `home_ring` PNG directly. Ring and live eye share the scene placement and
parallax owner.

`LivingEyeMedallion` draws `mobile_eye_open`, sclera, animated iris, pupil and
catchlight inside the aperture from `LivingEyeLayerGeometry`. The existing
`mobile_eye_closed` and `mobile_eye_squint` files are not used by this compositor.
Full closure disables live anatomy and reveals the baked green surround below.
The aperture currently uses source-space contours, a 70/30 closure interpolation
and an anatomy fit separate from the ring master. Thus the moving aperture and
the baked closed fold have separate geometric definitions. This explains a
possible visible discontinuity; it does not establish the correct replacement
coordinates or prove a particular numeric offset is wrong.

Current bitmap review found that the new candidate depicts one opening,
qualitatively retains the edge canthi, has a realistic iris, and has no separate
bronze lid plate. However, generation also changed green texture around the
opening. Exact material continuity and aperture registration have not passed a
runtime composite review. The candidate contains a fixed iris, pupil and
reflection, so it is not a drop-in underlay beneath the existing animated layers.

All three owner acceptance conditions remain open together: anatomically
realistic eye; green lids matching the original surround; no visible insertion
boundary. A promising standalone image does not close them.

## Smallest next step, not an implementation approval

Keep the original green surround/ring and its single placement owner unchanged.
Prepare an anatomy-only candidate without a fixed iris, pupil or catchlight for
one registered composite; preserve the existing blink, gaze and animated-layer
behavior. Register the existing aperture mechanism to the original closed seam
and common canthi before choosing numeric changes. Do not leave the old
gold-rimmed open-eye base underneath a replacement. No new renderer is proposed.

The existing artifact-only manual task is `mobile-eye-runtime-assets` in
`.github/workflows/android-test.yml`; it skips the Android APK job. Its Python
`ops/phone-screen-sim.py` preview covers OFF/CONNECTING/ON and five closure phases,
not actual Compose/device execution or animated gaze. Any later approved run
must match the exact intended source SHA. No workflow was dispatched for this
concept PNG, and no atlas regeneration is needed merely to archive it.

## Exact built-in generation prompt

One reference image was supplied: `materials/mobile_eye_surround_c.png` above.
The prompt was:

```text
Use case: precise-object-edit. Preview-only MaestroVPN OPEN eye state, matching the provided CLOSED state. Input image 1 is the EDIT TARGET and strict registration/color/material reference, not a loose inspiration. Produce ONE square image with the exact same camera, framing, spherical emerald surround, outer silhouette, scale, dark emerald relief texture and delicate aged-gold veins. Change ONLY the central closed eye fold into the corresponding anatomically realistic open human eye with a natural emerald iris, small black pupil, off-white softly veined sclera and subtle single corneal reflection. The existing long curved closed seam runs from the left corner near the horizontal middle to the right corner, dipping at center. Preserve those SAME two canthi/endpoints in the OPEN state: no second shorter eye pasted inside that seam. Upper and lower emerald eyelids should meet the eyeball physically with a thin dark wet contact edge, upper lid naturally overlapping the iris. Opening raises the central upper edge and lowers the lower edge slightly, with realistic almond-shaped anatomy, not a circular hole. Do not leave a separate black smile/closed slit below or behind the new open eye. Keep surrounding embossed green material practically identical; no gold/bronze separate lid plates, no human skin patch, no eyelash crown, no neon green glow, no visible oval mask, no halo, no collage boundary, no new frame, no text. Match original lighting and fine texture across the junction so the eye feels embedded in the original emerald surface, not floating on top. Preserve the rest of the image tightly and do not redesign the ornament.
```
