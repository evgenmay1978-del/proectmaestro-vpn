# Connected eye glow: outer-edge taper — 2026-09-05

Status: **SOURCE IMPLEMENTED, NOT INSTALLED; focused scripted preview SUCCESS.**
Code SHA is `7dff0773b217f7a634db304147020b1ed0537f02` (review base `279e34f`).
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

Exact-source run `33940310334` and focused job `101236295752` completed SUCCESS.
The manual `mobile-eye-state-preview` task used Python `3.11`, Pillow `11.3.0`,
the existing eye-state suite and lightweight OFF/ON rendering. The ring, runtime
atlas and Android build jobs were all SKIPPED: no Kotlin compilation, APK or
device proof. Existing APK conditions are unchanged.
Artifact `9961583414`, `mobile-eye-state-preview-7dff0773b217f7a634db304147020b1ed0537f02`,
is `6545629` bytes; one download verified ZIP SHA-256
`28bdfd1f47ef6892d70207d51fc15b5063dd37263e2c6058d5a0a870d75cf2a5`.
Only the three PNGs below were extracted and copied unchanged into this document tree.

| Durable PNG | SHA-256 |
| --- | --- |
| [OFF](preview-7dff077/home-disconnected.png) | `1ce90e412bc85ca5c35e9bc73d54961277ef6ac468ed4a97f94f820f3c23422d` |
| [ON](preview-7dff077/home-connected.png) | `afb379b8acde5af3a58110ac870c37f81018317f6aed7b59e2baf454234e44cb` |
| [Comparison](preview-7dff077/home-eye-states-comparison.png) | `8180555feb79fbfb3337e2d8161ce72af5dce45fca5f97d7831cd3cc0d862a49` |

Visual review: the OFF fold extends much farther horizontally than the ON eye;
the legacy gold/brown inner rim remains visible. All three visual conditions
remain open. Scripted states and static buttons are not runtime evidence.
Keep `[skip ci]` on post-preview documentation commits to prevent automatic
push/PR APK builds until images are accepted. Skipped required checks may stay
pending and must never be reported GREEN; workflow_dispatch is not skipped. See
[GitHub skip-workflow documentation](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/skip-workflow-runs).

Full-master imagegen edit was rejected: altered green contrast/cracks and wide fold remained.
The iris candidate was RGB without alpha, with a painted checkerboard; not integrated.
Sclera generation returned HTTP 400 `moderation_blocked` with no artifact; no retry.
No failed artwork was copied here; native crop/clip remains unvalidated, with no runtime edit.
Production Android remains `1.0.157`. Commercial S4 remains on source/package
`b4415daa90c95a38f9a7b9adea7642c66e63a420`; its private working state, service
state and CDN/public-edge gates are unchanged by this documentation/preview checkpoint.
