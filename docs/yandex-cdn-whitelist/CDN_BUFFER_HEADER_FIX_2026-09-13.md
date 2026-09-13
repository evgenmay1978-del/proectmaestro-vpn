# CDN downstream delay and isolated Remnawave trial — 2026-09-13

Latest owner confirmation: ping is present and YouTube, Instagram, Telegram and WhatsApp work. Keep the header-forwarding change; do not expand production changes on this evidence. Long-term stability is not established. Earlier four-client production observation did fail with independently recorded credit exhaustion/fencing, so this must not be labelled complete elimination of the random incident.

## Evidence and minimal change

The owner explicitly authorized connecting with the three supplied Akonit profiles. On the same S1 and official Xray 26.7.28 used for our failing scenario, both individual profiles passed 24/24 HTTP/HTTPS requests over 120 seconds; the native auto-balancer passed 60/60 over 300 seconds. For this controlled comparison only proxy/balancer routes were retained, with no direct fallback. Temporary private configurations and clients were removed. No Akonit credentials, endpoints or raw profiles were copied into Git or Maestro subscriptions. This was bounded server-side observation, not a mobile-carrier or long-term availability guarantee.

An independently installed Remnawave/stock-Xray path reproduced the original symptom: at 10:32 UTC HTTP 200, HTTPS 204, HTTP 200, then HTTPS `curl 56` after 13.180 seconds. Native quota was later observed ACTIVE with only 15,739 of 209,715,200 bytes used. Client-only `cMaxReuseTimes=1`, `maxConnections=1`, and official prerelease Xray 26.9.9 with upstream PR6632 also failed. These variants must not be repeated unchanged.

The 11:27 UTC metadata trace localized the delay between ingress output and the client: Xray and Nginx sent a downstream chunk immediately; the client received 2,791 data bytes promptly, but the final 59 bytes only 11.03 seconds later, after the upstream closed. No packet capture drops occurred. This delayed completion of the inner TLS handshake, followed by peer close. The trace does not establish a universal cause for every owner-reported disconnect.

Nginx 1.24.0 already had `proxy_buffering off`, `proxy_request_buffering off`, HTTP/1.1 upstream and 3600-second read/send timeouts. Direct Xray returned `X-Accel-Buffering: no`, but ingress removed it. Nginx documents this default for `X-Accel-*` headers. Adding the following directive only to the isolated XHTTP location preserved it:

```nginx
proxy_pass_header X-Accel-Buffering;
```

Server-only `noSSEHeader=true` had previously failed with the same 11-second delay; actual response headers confirmed that setting was applied. With header forwarding added, the unchanged 26.7.28 client passed all 60 requests from 11:36:35 to 11:41:36 UTC. Request 25 downloaded 21,164,807 bytes in 4.941 seconds; ZIP CRC verification passed, SHA256 `8195d909f1109b8f3d99eefe401a3c451d7bf4af71f24d3815420f77e5dd2a40`. Subsequent HTTPS requests also passed. Small downstream tails were then delivered promptly.

At 11:42:19 UTC the isolated Xray profile was restored exactly to its pre-noSSE backup, preserving only the Nginx header change. Actual ingress headers again showed `Content-Type: text/event-stream` together with `X-Accel-Buffering: no`; the bounded confirmation is recorded below when complete. The source template scopes this directive to XHTTP locations, excluding cabinet/subscription handlers. No new tests or automatic CI were launched.

## Production observation after header change

After restoring the original trial profile, another 24/24 requests passed over 120.05 seconds (11:43:16–11:45:16 UTC). Only header forwarding differed from the original failing path.

The same directive was then added to the four existing exact/prefix XHTTP locations. Syntax passed as www-data; all seven protected running service states/PIDs were unchanged. Ingress master remained 1074065. Live config SHA256: `7fee7db3153aeb2109be48211cdc41f21250d401ec110b67477caaeddedc552c`. Immediate prior backup: `/opt/maestro-remna-trial/ingress-before-cdn-buffer-fix.conf`; its hash is the earlier isolated-header hash below. No cabinet/subscription handler changed.

Four persistent clients used existing owner profiles 4–7, starting 11:48:55–57 UTC. Profile5 failed on request4 at11:49:11 (`curl35`); profiles4/6/7 failed on request9 at11:49:36–37 (`curl52`). The planned large download was not reached. All temporary clients/configs were removed. Watchdog correlation: at11:49:12 exit-s2 had zero internal byte credit with45.32 seconds of lease remaining; at11:49:33 all four routes of that owner were fenced while the other four stayed active; at11:49:40 all eight were ready rather than active. No service restart or collector error was recorded in that window. Paid balance and controller fencing reason still require authoritative rqlite inspection; zero internal credit alone does not establish purchased-balance exhaustion.

## Installed isolated trial; do not reinstall

The owner's later brief recurrence report near12:08 UTC was followed by the explicit clarification that ping and all four named applications work. Watchdog records12:06–12:09 show eight active routes, remaining leases44–59 seconds, positive byte credit and no fencing/errors. Do not attribute this later report to the earlier11:49 fence. Authoritative rqlite inspection confirmed positive paid balance, active primary subscription, no uncovered/pending balance and no unapplied debit receipts; aggregate reservations are much smaller than the paid balance. Customer identifiers, exact financial figures and raw query responses are intentionally excluded here.

The controller's durable refill selection is evaluated before fresh settlement; sequential passes can take much longer than the nominal2-second tick. SQL refill is not a runtime grant: a later successful PostUseLease is required. These are code-backed contributing risks, not a proved historical verdict for the observed fence. Publication reasons are not persisted by this path. Do not disable leases, blindly enlarge timeouts/budgets or replay failed experiments. The isolated trial remains separate and capped; no customer migration was made.

Owner approved one capped trial on existing hosts, not customer migration. S1 runs native Docker 29.1.3/Compose 2.40.3 with Remnawave backend 3.4.4 (512 MiB), PostgreSQL 18.4 (128 MiB), Valkey 9 (32 MiB). S4 runs Remnawave node 3.4.1 (256 MiB), stock Xray 26.7.28. Container swap and Docker automatic restarts are disabled; PM2 has its own process supervision. No NET_ADMIN or host networking is used.

Backend API is loopback 38000; DB/Valkey are not published. Node API 32222 is source-restricted to S1 in a dedicated Docker forwarding chain. Trial inbound is loopback 38081, reached through one separate ingress location. Preserve S1 FORWARD ACCEPT/WDTT and S4 FORWARD DROP. Installation upgraded no existing packages and changed no protected VPN service PIDs or pre-existing non-fail2ban firewall rules at its observation checkpoint.

Root-only state is under `/opt/maestro-remna-trial` on S1/S4. Native objects have already been created: profile, node, squad, host and one synthetic owner user; user expiry is 2026-09-14T09:44:45.261Z, limit 200 MiB, NO_RESET. Do not replay POST creation. APIs use loopback trusted-proxy headers; admin JWT additionally requires `X-Remnawave-Client-Type: browser`. Users have numeric IDs, while profile/node/squad/host IDs are UUIDs. Native JSON subscription is an array. Stock-generated ML-KEM encryption/decryption pair and native JSON were verified without publishing secrets.

Installed image digests:

- backend: `sha256:63ef481550bbf49dabfa514c95d94109619cc85607730b308f7ad0b9b5599f06`
- postgres: `sha256:a02db8cac496f15b094798a38254f14d6e00741f709360e5e00bb6668ea31636`
- valkey: `sha256:a0dbf4c1d5708782907c10e2c72deff317518518b5288a58416981d9db95d30b`
- node: `sha256:0cdf386dd49f360fc885bb34bde21132e478e40f0deac62d616086ec0fa9257e`

The separate client `/opt/maestro-remna-trial/client-xray-26.9.9` did not replace any installed server/client binary. Its official archive SHA256 was checked against `1eb9175d0f0a8f8149c9230a7fc5ae66ce332ed20a53155ce61fe62e3f58b7df`; it did not fix the observed delay.

## Recovery and continuation

Original ingress backup: S4 `/opt/maestro-remna-trial/ingress-before.conf`. Before header forwarding: `ingress-before-pass-buffering.conf`. Current isolated header-change hash: `c0f3e0a7c077543222b1d0e45e2c573cff0009abb75cd5395afb887ec1c31f51`. Preserve root:www-data 0640 on the live file; replacing it as root:root previously prevented the www-data master from reloading. Syntax-check as www-data and send HUP to the actual ingress master; the unit has no ExecReload. Existing master at this checkpoint: 1074065.

Private original trial-profile backup: S1 `profile-before-nosse.json`; restored through native PATCH. Panel installation snapshots: S1 `/var/backups/maestro-remna-trial-l_t3tzw0`; node snapshots: S4 `/var/backups/maestro-remna-trial-ag08znye`. Roll back only scoped configuration/container changes; do not overwrite current customer databases, remove Docker or restore entire firewall tables over later changes. Existing controller cee1642, ordinary VPN, customer subscriptions, billing and watchdogs remain separate. S4 rqlite must remain stopped.

Sources: [Nginx hidden response headers](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_hide_header), [explicit header forwarding](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_pass_header), [Xray 26.7.28 downstream headers/flush](https://github.com/XTLS/Xray-core/blob/v26.7.28/transport/internet/splithttp/hub.go), [upstream replay fix](https://github.com/XTLS/Xray-core/pull/6632), [Remnawave backend 3.4.4](https://github.com/remnawave/backend/tree/3.4.4), [node 3.4.1](https://github.com/remnawave/node/tree/3.4.1).
