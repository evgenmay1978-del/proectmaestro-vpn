# Yandex White-List Android Preparation Design

Date: 2026-08-22
Status: approved by the owner's standing instruction to complete the full production-readiness program
Scope: Task 6 only

## Goal

Prepare MaestroVPN Android for additive Yandex white-list client state and deterministic heartbeat recovery without changing ordinary VPN behavior, TV behavior, assets, runtime feature gates, application version, or OTA state.

## Constraints

- The production compatibility baseline is MaestroVPN 1.0.157.
- The existing VPNService remains the only Android VPN service.
- DefaultNetworkListener remains the single connectivity callback seam.
- Missing, unknown, malformed, or future API fields preserve current behavior.
- TV must neither parse nor display white-list state and must not start a white-list runtime.
- Ordinary subscription profiles and ordinary VPN remain usable during white-list suspension or recovery failure.
- No token, subscription URL, credentials, origin details, payload, or visited destination may enter logs.
- This task creates preparation and testable abstractions. Live probing and OTA remain gated by Task 7 evidence and explicit production approval.

## Considered approaches

### Selected: additive inert capability module

Add a pure optional parser and display projection, a pure watchdog state machine, and a small adapter over the existing DefaultNetworkListener. A device/runtime gate returns no white-list runtime for TV or for absent/non-active policy. The module is dormant unless a later verified public API projection explicitly activates it.

This is the smallest architecture that meets the Task 6 contract while preserving the current fleet.

### Rejected: immediate live heartbeat

Starting real probes before the public API projection, data-plane heartbeat endpoint, routing proof, and fixture harness exist would make failure behavior depend on unverified infrastructure and could produce reconnect storms.

### Rejected: second Android service or VPN tunnel

A separate service would duplicate lifecycle and connectivity ownership, violate the single-VpnService invariant, and create avoidable TV and ordinary-VPN regression risk.

## Public client model

The parser accepts an optional nested white_list object from GET /sub/<token>/info. Its public-safe fields are:

- state
- transport_profile_id
- transport_release_id
- preset
- billing_state
- usage_bytes
- remaining_limit_bytes
- suspension_reason
- edge_ids
- heartbeat_enabled

Opaque identifiers are bounded strings. Byte values are non-negative integers in the signed Android range. Unknown enum values remain displayable as unavailable state but cannot activate the runtime. Secret material and full endpoints are not part of this model.

A missing white_list object returns null. A malformed object fails closed to null. Existing fields such as login, expires, olcrtc, and vk_turn retain their current behavior.

## Mobile-only projection

WhiteListDisplayModel is produced only when isTelevision is false. It contains bounded user-facing state, usage/remaining values, preset, and suspension reason. TV projection always returns null, so no TV composable, navigation, resource, asset, focus, or runtime gate changes are required.

ACTIVE and GRACE may be runtime-eligible only when heartbeat_enabled is true and at least one opaque edge identifier is present. DISABLED, PROVISIONING, SUSPENDED, ERROR, EXPIRED, unknown, or malformed values are never runtime-eligible.

## Watchdog state machine

The watchdog is a pure deterministic reducer. Time, jitter, probing, session cleanup, redial, edge selection, and logging are injected effects.

Default policy:

- heartbeat interval: 25 to 35 seconds
- probe timeout: 9 seconds
- bounded exponential backoff
- no immediate retry loop
- network loss waits without redial
- a new default network cancels stale work, clears the stale session, and requests one controlled redial
- repeated failures first back off, then request a controlled redial, then advance to the next approved edge
- exhausting approved edges emits ordinary-VPN fallback
- success resets the failure counter and schedules the next jittered heartbeat
- stop and process teardown cancel work and clear stale session state

The reducer emits only typed actions. It performs no network or Android calls itself.

## Android adapter

WhiteListNetworkBinding registers an additional listener key with DefaultNetworkListener. The existing listener actor still owns one ConnectivityManager callback and fans events out to consumers. The binding is hosted by the existing VPNService lifecycle abstraction; it never creates another Service or VpnService.

The Task 6 production wiring remains dormant by default. Task 7 may activate it only after fixture-driven API, routing-through-TUN, heartbeat endpoint, and fallback evidence pass.

## Logging and telemetry

WhiteListLogEvent contains only fixed reason codes, state names, failure count, and edge ordinal. Its safe rendering cannot accept arbitrary strings. Tokens, URLs, edge addresses, exception messages, response bodies, and credentials are excluded by type and tested with sentinel secrets.

## Error and fallback behavior

- Missing or malformed white-list data: hide the mobile projection and keep current behavior.
- Unknown state or billing value: display unavailable state and keep runtime disabled.
- Network loss: cancel probe and wait.
- Network replacement: clear stale session and request one controlled redial.
- Probe failure: bounded backoff and failover.
- All edges exhausted: stop white-list recovery and select ordinary-VPN fallback.
- White-list suspension: show the bounded reason on mobile and keep ordinary VPN available.

## Tests

JVM tests must prove:

- absent and unknown fields are backward compatible
- valid ACTIVE mobile data creates the expected display model
- TV never creates a display model or runtime
- zero, negative, oversized, and malformed values fail closed
- safe logs do not contain token, URL, credentials, or exception text
- success schedules a 25 to 35 second jittered heartbeat
- network loss, network replacement, wake, success, and failure transitions are deterministic
- retry delay is bounded and exponential
- edge failover is ordered and finite
- exhausted edges emit ordinary-VPN fallback
- stop clears stale work
- no Android manifest, TV UI/resource, version, or OTA file changes enter the Task 6 commit

## Acceptance

Task 6 is complete when the focused Android unit tests pass, the relevant existing Android tests remain green, the scoped diff proves no TV/asset/runtime-version/OTA changes, and independent review reports no open Critical or Important findings.
