# MaestroVPN HA production readiness audit - 2026-08-30

Status: **PRODUCTION NO-GO**.

This checkpoint supersedes the incomplete 2026-08-12 identity inventory. It
records redacted production observations only. Green repository CI, server
access and a decryptable legacy backup do not authorize deployment, canary or
traffic cutover.

## Scope and safety boundary

- The owner explicitly confirmed ownership and access for S1-S4.
- SSH used key-only authentication and strict ED25519 host-key checking. S1,
  S2 and S4 passed the applicable identity gate; S3 remains continuity-only.
- No customer rows, credentials, private keys, bot tokens, subscription URLs,
  raw database contents or environment values were emitted by the bounded
  commands or recorded in this audit. The S2 verifier necessarily read the
  selected backup locally; its contents were not returned to Codex.
- No service was installed, enabled, disabled, restarted or reloaded. No
  firewall, DNS, TLS, OTA, customer, payment or VPN configuration was changed.
- The S2 legacy backup verifier used only its documented temporary workspace,
  downloaded/decrypted one backup, checked it and cleaned up. It did not restore
  production state.
- OLCRTC and WDTT remained frozen.

## Identity evidence

- S1: the owner-provided ED25519 fingerprint matched; dedicated pin and key-only
  login succeeded.
- S2: the existing exact ED25519 pin was revalidated; key-only login succeeded.
- S4: the dedicated ED25519 pin and SSH config were revalidated; key-only login
  succeeded.
- S3: no prior local, S2 or S4 pin existed. The live ED25519 fingerprint matched
  from the local runner and trusted S2. A dedicated continuity pin was created
  only after that two-path match and passed strict key-only login. S4 could not
  reach S3 TCP/22. This is continuity evidence only; authoritative out-of-band
  S3 identity attestation is still required before any mutation.

## Production observations

### S1 - current public control plane

- System is running with zero failed services; reboot is pending.
- `maestro-panel`, x-ui, nginx, Hysteria and the VPN Telegram bot are active.
- Public health is healthy and reports build commit `296079c`.
- The stamp resolves to full commit
  `296079cf819b36087c690b525d8970d6c87a18db`, reachable as
  `tv-v1.0.157~3` from the remote `tv-v1.0.157` tag. Its backend tree
  `762dac2a4b7a7edf1cfa0821bf9d6bbe8ec4500a` matches the tagged release.
- Accepted current code is `d7cfec12eb8656ea821d855bdb552a172cbf5fd6`
  with backend tree `af0f9aa7b46ae3cfd9b605306169b0833e06b746`; it is a
  different backend state.
- The health stamp is not a binary digest. No immutable `maestro-panel` artifact
  manifest/digest was found in repository workflows, so binary-level provenance
  remains incomplete and a later exact-artifact redeploy is still required.
- HA agent, rqlite state, HA backup worker/config/state and root AWS credentials
  were not observed.

### S2 - current multi-protocol and bot host

- System is running with zero failed services; reboot is pending.
- Caddy, nginx, Hysteria Server, sing-box AnyTLS and `vpn_bot.service` are active.
- Expected public transports, including TCP/8443, are listening.
- Root filesystem free space is in the 25-49% bucket.
- Bounded unit/path checks did not detect HA panel, HA agent, rqlite or
  `maestro-ha-backup.service/.timer`.
- Legacy AWS/GPG material and self-restore helper are present. Default verify
  selected and downloaded an object, decrypted/read the archive, opened
  `customers.json`, observed the orders file and returned `ok` for x-ui SQLite.
- Backup age/RPO, VersionId, signature authenticity, recipient binding, isolated
  empty-cluster restore, restore epoch and measured RTO remain unobserved.

### S3 - current x-ui node

- System is running with zero failed services; reboot is pending.
- One CPU, less than 2 GiB memory and at least 50% root free space.
- x-ui is active; nginx is inactive; public TCP/UDP 443 are listening.
- Bounded unit/path checks did not detect HA panel, HA agent, rqlite, backup
  worker/config/state or customer stores.

### S4 - current x-ui/Hysteria node

- One CPU, less than 2 GiB memory and at least 50% root free space; reboot is
  pending.
- x-ui and Hysteria are active; TCP/UDP 443 and TCP/2096 are listening.
- Bounded unit/path checks did not detect HA panel, HA agent, rqlite, backup
  worker/config/state or customer stores.
- System is degraded: `ifup@eth0.service` and `networking.service` fail with
  `Address already assigned`.
- Root cause is confirmed: active `systemd-networkd` owns an UP `eth0` with a
  default route, while enabled ifupdown also declares a static `eth0` address
  and gateway. No network change was made.
- S4 cannot reach S3 TCP/22; the HA east-west port matrix is not proven.

## Repository and operations evidence

- Accepted code SHA: `d7cfec12eb8656ea821d855bdb552a172cbf5fd6`.
- Exact-SHA Yandex CDN, HA control-plane and HA DR workflows are green.
- Documentation HEAD before this report:
  `e1a3547c222fbdcaebb6395443bfc9505bf1a322`.
- `ops/ha/inventory.sh` is fixture-only, not a live production collector.
- Missing production runbooks: `runbook-ha-management-runner.md`,
  `runbook-ha-restore.md`, `runbook-ha-dns.md`, `runbook-ha-tls.md`,
  `runbook-ha-cutover.md`, `runbook-ha-rollback.md`,
  `runbook-ha-s1-return.md`, and `deploy/ha/README.md`.

## Blocking gates

1. Complete authoritative out-of-band S3 identity attestation; the live two-path
   fingerprint match permits continuity checks only, not mutation.
2. Establish reproducible S1 build provenance and immutable exact-SHA artifacts.
3. Implement/review missing HA runbooks and the tooling they name.
4. Prove authenticated/versioned backup, bounded RPO and isolated empty-cluster
   restore before import or cutover.
5. Resolve S4 double network management through a separately approved,
   console-recoverable maintenance procedure.
6. Prove the full S2-S4 east-west firewall/listener matrix.
7. Deploy S2-S4 rqlite/TLS/panel/probe/agent only as non-public shadow
   components and only in a separate owner-approved, console-recoverable change
   window; prove quorum, rejoin, parity and duplicate prevention.
8. Complete freeze, fencing, final backup, delta import and reconciliation before
   requesting canary approval; production backup, fencing and import mutations
   require separately owner-approved change windows.
9. Canary and final apex traffic cutover remain separate owner approvals.

Until these gates pass, S1 remains the live control plane and S2-S4 retain their
current VPN roles. This audit is not permission to mutate live customer truth.
