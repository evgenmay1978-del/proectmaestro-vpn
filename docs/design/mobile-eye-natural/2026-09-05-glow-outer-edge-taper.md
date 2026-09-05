# Connected eye glow: outer-edge taper — 2026-09-05

Status: **SOURCE IMPLEMENTED, NOT INSTALLED; focused GitHub preview pending.**
Source baseline is `279e34f16c24326bb416e7ff8c801282f0279f90`.
The [registered artwork candidate](2026-09-05-registered-open-candidate.md)
remains unaccepted and is not integrated. All three artwork acceptance criteria
remain open. This narrow glow fix does not establish screenshot causation.

## Confirmed defect and bounded correction

`LivingEyeMedallion` ended its circular radial glow at nonzero alpha: connected
strength `0.7` times maximum alpha `0.22` gave `0.154` at the outer boundary.
The lightweight preview reproduced that hard cutoff as alpha `39`.

The Kotlin change preserves the existing transparent inner region and piecewise
brightness ramp through radius fraction `0.98`, where relative alpha is `5/6`.
Only the final 2% tapers continuously to transparent at `1.0`; connection glow
is retained. `ops/mobile-eye-state-preview.py` mirrors the same radial stops.
No blink/gaze, aperture, ring placement, runtime atlas, artwork or TV change was
made; no new renderer, full simulator or atlas generation was added.

One new behavioral pixel test observed RED (`39 != 0`) before the correction.
The corrected boundary/glow-preservation test and existing closed-material
invariance test both passed locally in under one second. Independent review of
the four source/workflow files returned CLEAN. These are source/pixel-test
results, not Android compilation, installed-device or full visual acceptance.

## Exact-SHA image gate, without an APK

The existing `android-test.yml` now offers manual task `mobile-eye-state-preview`.
Its isolated 10-minute job checks out `github.sha`, uses Python `3.11` and pinned
Pillow `11.3.0`, runs the existing eye-state suite, renders lightweight OFF/ON,
and uploads three PNGs as `mobile-eye-state-preview-${github.sha}`. Existing
APK job conditions are unchanged. No dispatch or image artifact is claimed yet.

This source commit and later post-preview documentation commits must retain
`[skip ci]` to prevent automatic push/PR APK builds until the images are accepted.
Only the focused task may then be dispatched manually against the verified
commit; require its run head SHA to equal that commit. The marker applies to
push/pull-request runs, not workflow_dispatch; skipped required checks may stay
pending and must never be reported GREEN. See the official
[GitHub skip-workflow documentation](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/skip-workflow-runs).

Next: inspect the exact-SHA lightweight images, then decide the next eye step.
Production Android remains `1.0.157`. Commercial S4 remains on source/package
`b4415daa90c95a38f9a7b9adea7642c66e63a420`; its private working state, service
state and CDN/public-edge gates are unchanged by this source-only checkpoint.
