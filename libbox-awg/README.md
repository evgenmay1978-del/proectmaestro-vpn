# libbox-awg — OFFICIAL AmneziaWG patch for libbox.aar (Path A)

Overlaid by `.github/workflows/libbox-awg.yml` onto a fresh SagerNet/sing-box checkout at
`SINGBOX_REF` (version.properties = v1.14.0-alpha.31) to build an AmneziaWG-enabled `libbox.aar`
**with the OFFICIAL `amnezia-vpn/amneziawg-go v0.2.15` engine**.

## Why this replaced the earlier Leadaxe engine
The first attempt used `Leadaxe/wireguard-go-awg2-lx`. Proven end-to-end (S2→S3, clean datacenter):
the **Leadaxe engine never completes the handshake** with the AmneziaWG kernel server, while the
**official amneziawg-go engine handshakes + passes traffic** (exit IP = the server). So Leadaxe was a
dead end; this patch uses the official engine. Verified with the ported `sb-awg-official` binary
(our exact 1.14 base) before packaging: `received handshake response` + traffic exits via S3.

## What's here (a SEPARATE `awg` endpoint, type `"awg"`)
- `option/awg.go` — `AwgEndpointOptions` (h1-4 STRINGS, jc/jmin/jmax/s1-s4 ints, i1-5, peers[]).
- `protocol/awg/endpoint.go` — the endpoint adapter (builds the wg IPC + device).
- `transport/awg/*.go` — the amneziawg-go ↔ sing-box device bridge (bind/device/tun).
- `include/awg.go` + `include/awg_stub.go` — registration (real under `with_awg`, stub otherwise).
- `constant/proxy.go` + `include/registry.go` — FULL patched v1.14.0-alpha.31 files (add `TypeAwg`
  + `registerAwgEndpoint`). Overwrite the originals (same base version → safe).

These compiled VERBATIM against our 1.14 base (the 1.12→1.14 API delta was nil for them).

## Build = `cp -rv libbox-awg/{option,protocol,transport,include,constant} sing-box/` +
`go get amneziawg-go@v0.2.15 && go mod tidy` + `with_awg` tag + `make lib_install lib_android`.
Artifact `libbox-awg-aar` (NOT `libbox-aar`) → canary-only, never auto-shipped to the fleet.
