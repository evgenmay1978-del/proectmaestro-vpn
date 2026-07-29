# Premium Mobile Subscription Flows Design

## Goal

Bring the phone-only purchase, claim, and trial screens into the approved
“Emerald Relic” system without changing navigation, ViewModels, API calls,
payment behavior, or Android TV rendering.

## Visual system

- Background: the existing dark walnut `mobile_surface`.
- Structure: centred aged-gold framed panels using the existing mobile premium kit.
- Titles: restrained Playfair gold; body copy uses the existing readable application face.
- Actions: emerald-accented framed controls with a minimum 48 dp touch target.
- Signature: each flow reads as one carved relic panel rather than a stack of generic cards.
- QR payment: remains black on white for scan reliability, surrounded by a quiet gold frame.

## Screens

### Claim and trial

Phone branches use `MobilePremiumScreen`, `MobilePremiumPanel`,
`MobilePremiumTextField`, `MobilePremiumButton`, and `MobilePremiumError`.
Existing text entry, focus, busy-state locking, callbacks, and completion navigation remain.
TV branches retain the current D-pad layout and controls.

### Tariff selection

Phone tariffs become full-width premium rows inside a centred panel. The tariff name
and price remain the primary information; tapping a row invokes the same `buy(key)`.
TV keeps the existing two-column tariff grid.

### Payment and state screens

Amount, QR, order code, open-payment action, and “Я оплатил” remain in the current
order. Loading, confirmation, activation, completion, and error states use the shared
premium loading/error language on phones. The QR itself is never tinted or placed on
a translucent background.

## Accessibility and responsive behavior

- Interactive phone controls retain standard Button semantics and 48 dp minimum targets.
- Content scrolls vertically on short phones.
- Compact/regular/expanded widths use the existing `MobilePremiumLayout`.
- Busy controls remain disabled, and errors remain visible text rather than decoration only.

## Boundaries

No changes to ViewModels, routes, endpoints, polling, payment URLs, pricing data,
profile creation, VPN runtime, Android TV visuals, or the living eye.

## Verification

Add phone Compose flow tests for visible titles, enabled/disabled actions, tariff
selection, and payment-state content. Run those tests and `assembleOtherDebug` in
GitHub CI because the local checkout lacks the generated `app/libs/libbox.aar`.
