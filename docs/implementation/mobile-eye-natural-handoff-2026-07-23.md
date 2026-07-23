# Mobile living eye — handoff (2026-07-23)

## Owner decision

The three supplied frames are the only visual source of truth:

- `docs/design/mobile-eye-natural/source/open.png`
- `docs/design/mobile-eye-natural/source/closed.png`
- `docs/design/mobile-eye-natural/source/squint.png`

The previous reptile/cat-eye insert is obsolete. The phone must use this exact
gold/emerald human eye. TV, VPN transport, backend, OTA and release behaviour
are outside this change.

Disconnected is a motionless closed eye. Connected opens through the supplied
squint frame and becomes alive. Every later blink follows:

`open → squint → closed → squint → open`

The top hero remains fixed. Only the lower revolver menu scrolls.

## Runtime implementation

`LivingEyeMedallion.kt` renders these independent layers:

1. exact open-state patch;
2. stationary reconstructed sclera;
3. pupil-free iris translated together with the pupil;
4. procedural round pupil;
5. corneal catchlight, translated by only 8% of gaze;
6. aligned squint/closed state patches above the anatomy during lid motion.

The source is a flattened JPEG-in-PNG and contains neither sclera hidden behind
the iris nor the upper part of the iris hidden by the lid. Those areas were
reconstructed deterministically from the supplied open frame. To keep those
reconstructions invisible, runtime gaze is limited to ±7 source pixels
horizontally and ±4 vertically.

Coordinates, masks, source alignment and reconstruction classifications are in
`docs/design/mobile-eye-natural/asset_metadata.json`.

## Natural-motion constants

- normal blink close: 92 ms;
- closed hold: 26 ms;
- normal blink opening: 205 ms;
- connection opening: 430 ms via the squint state;
- disconnection closing: 250 ms via the squint state;
- blink interval: log-normal, median 4.2 s, clamped to 1.5–9.5 s;
- double blink chance: 10%;
- normal blink globe shift: 1.2 px medial and 2 px down;
- fixation: 0.8–3.5 s;
- saccade: 42–44 ms;
- rare microsaccade: up to 0.7 source pixel;
- pupil light-response latency: 230 ms;
- pupil constriction: 420 ms;
- redilation: 980 ms;
- idle pupil hippus: irregular ±2.5%.

Do not replace this with smooth perpetual roaming, a sine-wave pupil or an
upward Bell movement during ordinary short blinks.

## Touch behaviour

The eye tracks a finger while it is down inside the medallion. The parent
pointer observer does not consume events, so the transparent connect button
continues to toggle VPN normally.

## Assets

Runtime resources:

- `mobile_home_scene.webp` — fixed open-frame hero blended into the preserved
  lower revolver scene;
- `mobile_eye_open.webp`
- `mobile_eye_squint.webp`
- `mobile_eye_closed.webp`
- `mobile_eye_sclera.webp`
- `mobile_eye_iris.webp`
- `mobile_eye_catchlight.webp`

Regenerate the fixed scene and aligned state patches with:

```bash
python3 ops/mobile-eye-natural-assets.py
```

The script does not regenerate the reconstructed anatomy layers; they are
committed outputs whose origin and limits are recorded in the metadata.

Visual QA:

- `docs/design/mobile-eye-natural/runtime-preview.png`
- `docs/design/mobile-eye-natural/blink-gaze-preview.gif`
- `docs/design/mobile-eye-natural/contact_sheet.png`

## Status/protocol collision

`PhoneStatusRow` is one centred `Column` with an 8 dp gap. Status and active
protocol are separate rows; the protocol row is centred, horizontally padded
and may wrap to two lines. Do not place them again as sibling children of a
plain `Box`, which caused the previous overlap.

## Release boundary

Compile and package through the manually dispatched
`.github/workflows/android-test.yml` on the test branch only. Do not merge to
`main`, run `android.yml`, publish a release, change OTA or deploy to users
without a separate explicit owner instruction.

## Verified test build

The natural-eye implementation was compiled successfully on the test branch:

- branch: `codex/mobile-eye-revolver-preview`;
- source commit: `5666224e3a96c9be399670532d23c85b31177a8c`;
- workflow run: `#200`, ID `30032956940`;
- run URL:
  `https://github.com/evgenmay1978-del/proectmaestro-vpn/actions/runs/30032956940`;
- artifact: `maestrovpn-tv-test-apk`, ID `8574127969`;
- artifact ZIP SHA-256:
  `1b4b49355046364a8726d34a13652025c87e12833130003b6038befe81a4a3b7`;
- APK SHA-256:
  `bd85b12f356a55f5b99b50cea9fc9d1607143e0b541ffc61d7eb310e9dcce151`;
- signing certificate SHA-256:
  `17:B9:58:45:8A:6C:52:D9:06:2B:33:36:25:50:3B:01:25:66:38:44:54:0A:C2:4E:0D:E4:86:25:92:D5:9F:F7`.

All build and upload steps completed successfully. The APK ZIP integrity check
passed and the package contains all six new runtime resources:
`mobile_eye_open`, `mobile_eye_squint`, `mobile_eye_closed`,
`mobile_eye_sclera`, `mobile_eye_iris` and `mobile_eye_catchlight`.

Do not poll the public workflow HTML page to determine the result: it was
several runs behind because of caching. Query the Actions API/connector first,
then inspect the run jobs and artifact by ID. For a future build, prefer
`workflow_dispatch` with ref `codex/mobile-eye-revolver-preview`; use a
test-workflow marker commit only when dispatch is unavailable.
