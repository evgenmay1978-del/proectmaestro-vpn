# Isolated WDTT canary

This contour exists only to prove the pinned WDTT transport without allowing the
upstream server to touch S1 host networking.

## Boundary

- `wdtt-server` runs in Docker's private network namespace with only
  `CAP_NET_ADMIN` and `/dev/net/tun` inside that namespace.
- No Docker port is published and host networking is forbidden.
- The server's `wdtt0`, IP forwarding and iptables rules therefore remain inside
  the container.
- A small unprivileged Python relay binds host UDP 56000 and forwards DTLS packets
  to the container IP. Internal WireGuard UDP 56001 is never exposed on the host.
- The server starts without a master password. Three random generated passwords
  are preloaded in root-only `passwords.json`, one each for `wapmix`, `wapmixx`,
  and `wapmix2`.
- Container limits: no restart policy, at most 0.5 CPU, 256 MiB RAM and 128 PIDs.

## Mandatory activation checks

1. Verify the artifact SHA-256 and upstream commit marker.
2. Verify Docker bridge egress already exists; do not create a new Docker network.
3. Refuse activation if host UDP 56000 is occupied.
4. Snapshot active production services and listeners.
5. Start the container with no `--network host` and no `-p`/`--publish` option.
6. Resolve the container bridge IP, then start the unprivileged relay.
7. Require container health, relay READY, UDP 56000 on the host, and UDP 56000/56001
   only inside the container.
8. Require every pre-existing production service and listener to remain present.
9. On any failure, stop the relay, remove the container, and remove generated
   canary credentials. Do not restart or edit production services.

This is still not a release gate. After Linux client proof, a real Android phone
must pass DTLS/WireGuard, public egress, reconnect, sleep/wake, Wi-Fi/LTE switching,
and route-log verification. Android TV must independently show no WDTT data or UI.
