# WDTT and olcRTC runtime repair design

Date: 2026-08-13  
Scope: mobile runtime, Maestro panel orchestration, S1/S3 protocol runtime  
Out of scope: TV code/assets, customer dates, payments, bots, DNS, other VPN protocols

## Verified failure boundaries

### WDTT

- The production subscription advertises WDTT to the three allowed mobile accounts on versionCode 156.
- The panel passwords and the WDTT server password database match exactly.
- The server is active and accepts the configured WRAP key, but it has created no device and has received no WireGuard packets.
- Client and server are pinned to upstream commit `8b26530dfe90ff9b6aa3880ba2c1f070e21e2d3a`.
- Upstream head `1ff024899a577cb5db4691e526614619bf5a06a3` adds the Android fix that avoids `NETLINK_ROUTE` interface enumeration denied by some Huawei/Honor ROMs. Server/auth database code is unchanged between these commits.

The repair therefore updates only the pinned mobile WDTT client artifact first. The production server remains unchanged unless exact-version integration evidence later proves a server change is required.

### olcRTC

- The latest panel binary and olcRTC configuration are present.
- Two dedicated rooms exist, but the room orchestration script and health snapshot are absent on the replacement S1.
- S1 has no private SSH key or trusted host key for S3, so it cannot create/restart the per-login exit.
- A panel-only room write would advertise a dead room and is forbidden.

## WDTT design

1. Add a failing repository policy test that requires the pinned upstream revision containing the Huawei/Honor networking fix and require it in Android CI.
2. Update `WDTT_REF` to exact commit `1ff024899a577cb5db4691e526614619bf5a06a3`.
3. Build the WDTT artifact in GitHub Actions. The Android workflow must verify `WDTT_UPSTREAM_COMMIT` and artifact checksums before packaging.
4. Build a mobile-only release candidate with the next versionCode. Do not modify TV branches, TV resources, or TV assets.
5. Validate on the real phone: child emits structured READY, server records one device/handshake without exposing its identifier, public egress works, then reconnect, sleep/wake and Wi-Fi/mobile switching work.
6. Publish production OTA only after those checks pass.

Rollback: retain the previous APK and upstream pin; do not change the server while validating the client fix.

## olcRTC design

1. Harden `ops/olcrtc-room.sh` before deployment:
   - no hard-coded server address;
   - no `StrictHostKeyChecking=no`;
   - S3 host, key and known-hosts paths come from root-owned S1 configuration;
   - perform strict SSH preflight before changing panel state;
   - stage and validate the S3 room configuration, restart the per-login unit, and prove it joined before publishing the room through the panel;
   - restore the previous S3 configuration if the final panel update fails;
   - never print tokens, room keys or customer data.
2. Add deterministic shell integration tests with fake `ssh` and `curl` proving ordering, fail-closed behavior, rollback and secret redaction.
3. Provision one dedicated S1-to-S3 SSH key, verify the S3 host fingerprint through existing trusted infrastructure, add only that public key on S3, and install a strict root-only known-hosts file.
4. Install the reviewed script on S1 with a recoverable backup and root ownership.
5. Install/enable the existing olcRTC health probe so the panel reports the real per-login exit state.
6. Exercise one room update through the panel. Success requires: HTTP success, S3 per-login unit active, health entry current and healthy, subscription/info returns the new room, and real phone traffic passes.

Rollback: restore the previous script/config/unit state from root-only backups; never leave the panel pointing at an unverified exit.

## Acceptance gates

- WDTT remains visible after refresh/restart and carries real traffic on the mobile release candidate.
- olcRTC room update succeeds from the Maestro panel and carries real traffic.
- No secrets, customer identifiers or authenticated URLs enter Git, logs or reports.
- TV behavior and assets are byte-for-byte unchanged.
- Exact-SHA GitHub checks are green before any production deployment or OTA.
- `CURRENT_PRODUCTION_HANDOFF.md` is updated only with anonymized evidence.
