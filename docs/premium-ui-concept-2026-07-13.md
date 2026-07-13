# MaestroVPN TV premium UI concept (2026-07-13)

Status: approved visual direction, implementation is a future task.

## Canonical references

- `docs/design/maestrovpn-premium-tv-concept-2026-07-13.png` - approved full-screen TV concept.
- `docs/design/maestrovpn-mv-logo-reference-2026-07-13.jpg` - exact M/V logo reference supplied by the owner.

These files are the source of truth for the premium redesign. Do not replace the logo with an invented M or add surrounding shapes.

## Required UI content

- Keep the left pane with MaestroVPN branding, connection status, connect button, and account information.
- Keep all real actions: Buy subscription, Enter code, Applications / VPN tunnel, Share / Connect iPhone, Update application, and Check connection.
- Keep support actions: full phone number `8 977 811-65-64`, Telegram, WhatsApp, and MAX.
- Keep all eight protocols visible: Auto, VLESS, Hysteria2, Naive, AnyTLS, vless-s3, AWG, and OLC RTC.
- Settings must not appear anywhere in the TV application.

## Visual direction

- Dark graphite premium 3D interface with restrained smoked-glass panels.
- Exact gold M with green V near the MaestroVPN name.
- Large exact M/V logo as a low-contrast embossed graphite background relief.
- Champagne-gold D-pad focus rim; green is reserved for active/success state.
- No neon gamer look, excessive glow, cheap plastic effect, or invented branding.
- Everything must fit a 1920x1080 TV safe area; labels and phone number must be fully readable from a distance.

## Future implementation tasks

1. Recreate the approved concept in Jetpack Compose using the existing TV screen behavior and navigation.
2. Prepare the logo/background assets from the exact supplied M/V reference without altering its silhouette.
3. Preserve current actions, protocol fallback behavior, OTA flow, and subscription state handling.
4. Build and install on the KP1 Android TV over cable.
5. Test every D-pad focus transition and action, all eight protocol labels, support links, phone number fit, and 1920x1080 layout.
6. Capture real-device screenshots and compare them with the approved concept before release.
7. Update release notes, persistent memory, and Graphify after implementation.
