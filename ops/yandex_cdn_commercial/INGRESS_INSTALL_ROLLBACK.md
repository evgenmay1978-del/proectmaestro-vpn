# Isolated S4 HTTP ingress

This component preserves the existing private-canary XHTTP path on `18081`
while routing a separate secret commercial path to `28081` through one
restricted listener on `28080`. It never authorizes a CDN switch by itself.

## Current evidence (2026-09-05)

The custom `maestro-cdn-ingress.service` is ACTIVE/enabled, PID `646960`,
owning TCP `28080`; the unknown local path returns `404`. The intended-user
parser passed. Config SHA-256 is
`43291883decf99bcdc1bffbb2172a44fbc37a1f585da2371fa722e0953e2e739`, unit
SHA-256 `3fda8a55077918cd7e0e78a214df8b4c14d025ffdc0732f621534c16d02fd68d`,
and installed binary SHA-256
`1f16b72bea2f44e5d04fe6cf9e3e4b0dec53a82c50c7c1533c302a8ecaeccacf`.
The two official packages below were installed once and never reinstalled.
Default nginx remains administrator-masked/inactive; ordinary/private hashes,
unit/PIDs, SSH and TCP/UDP `443` owners are unchanged. No public `28080`
allowance, CDN switch or private-canary probe occurred.

Isolated direct authenticated traffic PASS: existing synthetic generation 4
through loopback SOCKS -> ingress `28080` -> commercial XHTTP/ML-KEM `28081`
-> `exit-s4` relay -> HTTPS egress-check service (ipify); curl exit `0`, `468 ms`, S4 egress
match. The owned test client stopped, ordinary/private file and service
baselines were unchanged, and no desired POST or service/config/firewall/CDN
mutation ran. This is not CDN/public-edge, counters, or general device proof;
those gates and deliberate rollback remain open.

The first traffic preflight failed before client startup or traffic because
saved checksum paths are root-relative but SSH cwd was `/root`. Preserve that
failed evidence; it did not prove file drift. The reviewed correction checks
the manifest using subprocess cwd `/` in a separately named helper with fresh
exclusive evidence. Syntax and bounded independent review passed before the
successful direct proof.

The initial install reached custom-unit start and `404`, then rolled back both
newly owned units after raw ss column padding changed. A separate resume failed
before start because stateless nft output still included changing fail2ban SSH
ban members. Both failed evidence sets remain intact. A new reviewed resume
with separate evidence passed; it did not replay installation.

## Input and request-preservation contract

- Private input is `/opt/maestro-xray-cdn/config.json`: exactly one VLESS XHTTP
  inbound on `18081`, clear HTTP transport, with `xhttpSettings.host` and
  `xhttpSettings.path`.
- Commercial input is the protected seven-key `runtime-material.json`; only
  `public_host` and `secret_path` are rendered.
- Both inputs must name the same DNS host. Paths are canonicalized only by
  removing one terminal slash and must be different, non-overlapping
  namespaces.
- `proxy_pass` has no URI suffix and there is no rewrite, so method, original
  URI, query, and GET body are forwarded unchanged. Every unrelated path gets
  `404`.
- Rendering refuses an existing stage and untrusted, linked, writable, or
  over-permissive inputs. It prints only the rendered SHA-256.

## Protected staging

Run `render_ingress.py` as root with explicit absolute protected input paths,
the committed template, and a fresh stage directly beneath a root-owned parent
that `www-data` can search. Do not stage below a root-only backup directory.
The result is one `root:www-data` `0640` `nginx.conf` inside a
`root:www-data` `0750` stage. Rendering does not install a package, unit,
firewall rule, or CDN setting.

## Controlled first-install sequence

1. Require reviewed source, refreshed pinned management and console access,
   ordinary listener/unit baseline, free TCP `28080`, verified backup/restore,
   and the recorded current CDN origin. Preserve SSH `22` and private
   `18081/18082` throughout.
2. Require `nginx`, `nginx-common`, and the default `nginx.service` to be
   absent/inactive. Also require
   `/var/lib/systemd/deb-systemd-helper-masked/nginx.service` to be absent. If
   any package, unit, helper-owned mask state, override, or prior admin mask is
   present, stop instead of inferring ownership.
3. Create and record the administrator mask
   `/etc/systemd/system/nginx.service -> /dev/null`, run `systemctl
   daemon-reload`, and prove the default unit is masked and inactive. With the
   helper-owned masked-state file absent, the package helper does not own and
   cannot remove this administrator mask. Record ownership immediately after
   exclusive mask creation. On any later first-install failure, stop the newly
   owned default nginx even if the custom unit was not yet installed, and stop
   the custom unit if owned. Restore the mask only if absent, never overwrite
   an unexpected file/link, and verify masked/inactive after daemon reload.
   Record actual stop/mask results rather than inferring a successful rollback.
4. Inspect and require an exact transaction containing only
   `nginx=1.24.0-2ubuntu7.17` and `nginx-common=1.24.0-2ubuntu7.17`, with no
   recommends, upgrades, removals, substitutes, or extra packages. Install
   only those two hash-verified local debs with dpkg after exact dependency
   simulation; no downloads or global apt restart hooks. Never invoke package
   hooks manually or replay this step after packages/assets exist.
5. Immediately prove the admin mask still targets `/dev/null`, the default
   unit remains masked/inactive, the helper-owned masked-state file did not
   pre-exist this transaction, and the exact pre-install listeners on `80` and
   `443` are unchanged. Any mismatch triggers stop/rollback before installing
   the custom unit.
6. Render a fresh protected stage. Prove the config and all parents are
   readable/searchable by `www-data`. Install only absent targets:
   `/etc/maestro-cdn-ingress` as `root:www-data` `0750`, its `nginx.conf` as
   `root:www-data` `0640`, and the committed custom unit as `root:root` `0644`.
7. Create the custom unit's runtime/state directories, run the official nginx
   parser as `www-data`, then start only `maestro-cdn-ingress.service`. Prove it
   owns `28080`, not `80`, `443`, or `18081`; verify process, config digest,
   both upstreams, and unchanged ordinary/private baseline.
8. Before any CDN change, apply and verify only the separately reviewed UFW
   `28080` delta with its exact rollback. Preserve SSH and private-canary rules.
9. Only after transport and rollback gates pass, switch the origin of the same
   existing paid CDN resource. Preserve domain/certificate, Host, GET body,
   query behavior, caching-off, and transformation settings. Creating another
   paid resource is not authorized.

## Existing-install resume after a recorded validation failure

Do not rerun the installer over existing packages, paths or evidence. A separate
reviewed resume must use a fresh exclusive evidence directory, bind the original
failure/rollback record, and prove exact package versions, binary/config/unit
hashes, protected ownership, default mask, ordinary baseline, inactive/disabled
custom unit with PID zero and free `28080` before enabling only that unit.
If a previous resume failed in preflight, require its recorded reason and
`start_attempted=false`; retain all prior files. Roll back only the owned custom
unit if this resume attempted a start.

Compare ss protocol/state/local/peer/process ownership after splitting columns;
ignore padding and live queue occupancy only. Stateless nft output still
contains dynamic security-set members. The proven exception normalizes only
IPv4 members in exactly one `table inet f2b-table` / `set addr-set-sshd` /
`type ipv4_addr` block, validates each address, and preserves the set name/type
and every other rule/static set. Require the verified fail2ban process and an
active sshd jail; the current checkpoint PID is `594973`. Any other schema,
static-policy or service drift stops the resume. Never disable fail2ban or
restore a historical ban list to pass this comparison.

## Minimal rollback — failure rollback proved; deliberate gate open

1. If switched, restore the previous origin first and verify it while the proxy
   remains available through propagation.
2. Stop/disable only `maestro-cdn-ingress.service`; restore/remove only the
   exact owned `28080` firewall delta. Prove SSH, private `18081/18082`, and all
   ordinary services/listeners unchanged.
3. Retain the protected stage, package receipts, stopped custom unit, and
   failed evidence. Do not purge packages, broadly delete files, restart
   ordinary services, rotate keys, or modify the private subscription.
4. Keep the recorded default-unit admin mask. Any later cleanup/unmask is a
   separate scoped action after proving the default service remains disabled.
5. If the CDN was never switched, leave its current origin unchanged and roll
   back only the owned listener/rule changes that actually occurred. Record
   elapsed time; do not claim the five-minute gate without a real exercise.
