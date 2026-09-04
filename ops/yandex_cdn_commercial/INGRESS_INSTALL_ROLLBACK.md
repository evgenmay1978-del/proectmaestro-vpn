# Isolated S4 HTTP ingress

This component preserves the existing private-canary XHTTP path on `18081`
while routing a separate secret commercial path to `28081` through one
restricted listener on `28080`. It never authorizes a CDN switch by itself.

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

## Controlled install sequence — not yet executed

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
   cannot remove this administrator mask.
4. Inspect and require an exact transaction containing only
   `nginx=1.24.0-2ubuntu7.17` and `nginx-common=1.24.0-2ubuntu7.17`, with no
   recommends, upgrades, removals, substitutes, or extra packages. Install
   only that transaction; never invoke package hooks manually.
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

## Minimal rollback — not yet executed

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
