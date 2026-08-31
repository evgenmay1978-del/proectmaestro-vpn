# XHTTP Canary Runtime Design

Status: approved-spec decomposition for implementation. The owner already approved the binding Yandex CDN White-List specification and authorized autonomous staged production work after its gates; this document narrows the next slice without changing that scope.

## Goal

Create the missing executable boundary between an immutable MaestroVPN transport release and one isolated, reversible VLESS/XHTTP canary. The slice must produce a server config containing exactly one canary identity and a matching protected client profile, validate both with the pinned Xray binary, activate only the isolated sidecar, and retain a deterministic rollback to the diagnostic origin.

## Chosen approach

Use the existing release and activation packages, extended by one root-owned runtime bundle and one purpose-built canary command. This keeps immutable release artifacts public and reviewable while UUID, VLESS encryption material, secret path and client URI remain outside Git in mode `0600` runtime files.

Two alternatives were rejected:

- Editing the live Xray or x-ui configuration directly is shorter, but it is not reproducible, cannot prove which client/server pair was deployed and risks the ordinary VPN listener.
- Wiring panel, billing, Telegram and Android before the first tunnel smoke would delay the decisive transport proof and mix unrelated failure boundaries.

## Pinned compatibility boundary

The first live canary uses the Xray binary already present on the isolated target, version `26.6.22`, source tag commit `b99c3e56574fb0317608c49dd1dd9af816db7a9e`, binary SHA-256 `2745bf12d7217c81769b161e44fd1528c05e8ce79176d59a59a4012f1bdadb6b`. Official source at that tag confirms that inbound VLESS users contain identity only, server `decryption` is global, client `encryption` belongs only to the outbound, and XHTTP accepts the required padding, session, sequence and XMUX fields.

The runtime rejects any other Xray version, commit or binary digest until separately admitted. Android/TV version `1.0.157`, OLCRTC and WDTT are unchanged.

## Components

1. `release.CanaryRuntimeBundle` holds a transport snapshot, server decryption, exactly one canary UUID/counter identity, client encryption, the selected Xray identity and a canonical pair commitment. Parsing is strict, bounded and rejects unknown fields, duplicate JSON keys, unsafe paths, non-canonical JSON, wrong roles and unbound material.
2. Release materialization verifies the sealed candidate and transport digest, inserts one inbound client `{id,email}`, installs the complete approved XHTTP settings at the server root, and produces the matching client profile from the same bundle. Client encryption is never written into inbound settings.
3. `maestro-xray-cdn-canary prepare` reads only root-owned regular `0600` inputs, writes runtime outputs atomically as `0600`, runs the pinned binary's `run -test`, and emits only stable reason codes and non-secret digests.
4. `activate` publishes the already-validated candidate through the existing activation store and starts/reloads only `maestro-xray-cdn.service`. `rollback` restores the previous immutable pointer and the diagnostic-origin routing. Neither command touches the ordinary x-ui/Xray listener.

## Data flow

Pinned Xray `vlessenc` output is captured once by the bundle generator; its ML-KEM server/client pair, binary identity and canary UUID are committed together. `prepare` verifies that commitment, reconstructs the immutable transport snapshot, verifies it against the sealed release manifest, materializes server and client outputs, and runs offline config tests. Only after those checks may `activate` expose the sidecar on the dedicated canary port. The Yandex test resource is then switched for the bounded test window, direct and CDN tunnel smokes run, and the resource is restored or rolled back on any failed gate.

## Failure and rollback rules

- Fail closed before writing if any ownership, mode, inode, release, transport, pair, binary or config check fails.
- Never echo UUID, encryption/decryption material, secret path, client URI or raw operational endpoint.
- Use atomic same-directory replacement and fsync for runtime files; refuse symlinks and pre-existing unsafe targets.
- A failed Xray config test, sidecar health check, direct tunnel, CDN tunnel, metering identity check or resource verification triggers sidecar stop plus restoration of the recorded diagnostic origin.
- No customer subscription, charge, Telegram notification, Android/TV release or final customer-traffic cutover is part of this slice.

## Verification

Repository tests must prove strict bundle parsing, one-client materialization, no client encryption in inbound config, exact advanced XHTTP shape, deterministic redacted output, atomic protected writes, pinned-binary invocation, activation and rollback. Linux CI then runs race/vet/unit and fixture replay. The live gate requires: pinned binary/config GREEN, ordinary listeners unchanged, direct VLESS/XHTTP data transfer, Yandex CDN data transfer with GET body, observable per-client counter identity, and successful restoration of the diagnostic path.
