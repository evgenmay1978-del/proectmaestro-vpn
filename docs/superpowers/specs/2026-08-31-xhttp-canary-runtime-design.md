# XHTTP Canary Runtime Design

Status: approved-spec decomposition for implementation. The owner already approved the binding Yandex CDN White-List specification and authorized autonomous staged production work after its gates; this document narrows the next slice without changing that scope.

## Goal

Create the missing executable boundary between an immutable MaestroVPN transport release and one isolated, reversible VLESS/XHTTP canary. The slice must produce a server config containing exactly one canary identity and a matching protected client profile, validate both with the pinned Xray binary, activate only the isolated sidecar, and retain a deterministic rollback to the diagnostic origin.

## Chosen approach

Use the existing release package only for pure candidate verification and config materialization, then add one dedicated first-canary runtime staging controller and one purpose-built canary command. The existing activation store is not used for this first activation: a freshly prepared canary has no published predecessor, its sealed release directory has no loadable transport snapshot after restart, and the current journal does not transactionally bind the runtime config to the active pointer. Those gaps are explicit later durable-release work, not assumptions hidden inside this slice.

The first-canary controller therefore implements the narrow state machine `ABSENT -> PREPARED -> CANARY_ACTIVE -> ABSENT`. Immutable sealed evidence remains root-only. UUID, VLESS encryption material, secret path and client URI remain outside Git in root-owned regular files with mode `0600`; only the already-verified executable and materialized server config are copied into a non-writable runtime stage readable by the dedicated service group.

Two alternatives were rejected:

- Editing the live Xray or x-ui configuration directly is shorter, but it is not reproducible, cannot prove which client/server pair was deployed and risks the ordinary VPN listener.
- Wiring panel, billing, Telegram and Android before the first tunnel smoke would delay the decisive transport proof and mix unrelated failure boundaries.

## Pinned compatibility boundary

The first live canary uses a separate immutable Xray `26.7.28` sidecar binary,
matching the existing `xray>=26.7.28` transport contract. The source tag is
commit `5ca6f4b7d4dc20a881d4330e498892697627ec0c`; the official
`Xray-linux-64.zip` release asset is pinned by SHA-256
`8195d909f1109b8f3d99eefe401a3c451d7bf4af71f24d3815420f77e5dd2a40`.
Its extracted binary digest is recorded before candidate construction.
Official source at that tag confirms that inbound VLESS users contain identity
only, server `decryption` is global, client `encryption` belongs only to the
outbound, and XHTTP accepts the required padding, session, sequence and XMUX
fields. The older Xray binary owned by x-ui remains untouched.

The runtime rejects any other Xray version, commit, release-asset digest or
extracted-binary digest until separately admitted. Android/TV version
`1.0.157`, OLCRTC and WDTT are unchanged.

## Components

1. `release.CanaryRuntimeBundle` holds a transport snapshot, server decryption, exactly one canary UUID/counter identity, client encryption, the selected Xray identity and a canonical pair commitment. Parsing is strict, bounded and rejects unknown fields, duplicate JSON keys, unsafe paths, non-canonical JSON, wrong roles and unbound material.
2. Release materialization remains a pure operation: it verifies the candidate and transport digest, inserts exactly one inbound client `{id,email}`, installs the complete approved XHTTP settings at the server root, and produces the matching protected client config and URI from the same bundle. Client encryption is never written into inbound settings.
3. `maestro-xray-cdn-canary prepare` reads only root-owned regular `0600` inputs, verifies the pinned binary and sealed evidence, and creates `/opt/maestro-xray-cdn/runtime/<runtime-id>/` owned by `root:maestro-xray-cdn`. Runtime directories and the copied Xray executable are mode `0550`; the materialized server config at `/run/maestro-xray-cdn/config.json` is `root:maestro-xray-cdn` mode `0640`. No runtime path is group-writable. Protected client outputs stay `root:root` mode `0600`. The command runs the staged pinned binary's `run -test` and emits only stable reason codes and non-secret digests.
4. `activate` accepts only the verified `PREPARED` runtime, records the before-state as `ABSENT` together with a non-secret receipt for the current diagnostic CDN origin, and starts only `maestro-xray-cdn.service` from the verified runtime stage. It never publishes through the existing activation store and never touches the ordinary x-ui/Xray listener.
5. `rollback` stops the isolated sidecar, restores the recorded diagnostic CDN origin, verifies that restoration, and returns the controller to `ABSENT`. Staged runtime and protected client artifacts are retained as evidence; they are not treated as active after rollback.

## Data flow

Pinned Xray `vlessenc` output is captured once by the bundle generator; its ML-KEM server/client pair, binary identity and canary UUID are committed together. `prepare` verifies that commitment and the caller-supplied immutable transport snapshot against the sealed release manifest, materializes the protected client output plus the service-readable server runtime stage, and runs offline config tests. It then records `PREPARED` without changing any listener or CDN resource.

Only after those checks may `activate` transition `PREPARED -> CANARY_ACTIVE` and expose the sidecar on the dedicated canary port. The Yandex test resource is switched only for the bounded test window, direct and CDN tunnel smokes run, and `rollback` always performs `CANARY_ACTIVE -> ABSENT` by stopping the sidecar and restoring the diagnostic origin. The runtime stage is evidence, not a substitute for the missing durable release loader or an activation-store predecessor.

## Failure and rollback rules

- Fail closed before writing if any ownership, mode, inode, release, transport, pair, binary or config check fails.
- Never echo UUID, encryption/decryption material, secret path, client URI or raw operational endpoint.
- Use atomic same-directory replacement and fsync for every generated file; refuse symlinks and pre-existing unsafe targets. Enforce `root:root 0600` for secrets and client outputs, `root:maestro-xray-cdn 0550` for runtime directories and the copied executable, and `root:maestro-xray-cdn 0640` for the materialized server config. Nothing is group-writable.
- A failed Xray config test, sidecar health check, direct tunnel, CDN tunnel, metering identity check or resource verification triggers sidecar stop plus verified restoration of the recorded diagnostic origin and state `ABSENT`.
- `rollback` is valid for the first canary without a previous published release. Durable activation-store integration is forbidden until a protected transport-snapshot loader, a real predecessor, and transactional config/pointer/journal coupling exist.
- No customer subscription, charge, Telegram notification, Android/TV release or final customer-traffic cutover is part of this slice.

## Verification

Repository tests must prove strict bundle parsing, one-client materialization, no client encryption in inbound config, exact advanced XHTTP shape, deterministic redacted output, atomic protected writes, exact owner/mode contracts, pinned-binary invocation, `ABSENT -> PREPARED -> CANARY_ACTIVE -> ABSENT`, activation without an activation-store predecessor, and rollback to the diagnostic origin. Linux CI then runs race/vet/unit and fixture replay. The live gate requires: pinned binary/config GREEN, ordinary listeners unchanged, direct VLESS/XHTTP data transfer, Yandex CDN data transfer with GET body, observable per-client counter identity, and successful restoration of the diagnostic path.
