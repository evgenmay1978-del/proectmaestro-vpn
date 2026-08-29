# MaestroVPN TV — architecture & plan

> ⛔ **ЭТО ДОКУМЕНТ ПЛАНИРОВАНИЯ ОТ 2026-06-14, А НЕ ОПИСАНИЕ ТЕКУЩЕГО СОСТОЯНИЯ.**
> Часть описанного здесь так и не была сделана, а часть цифр устарела в разы. Читая его,
> легко принять ПЛАН за СДЕЛАННОЕ — на этом 2026-08-02 уже подорвались (строки про
> «server2 VLESS/Reality» ниже описывают невыполненный план, и по ним было сделано неверное
> утверждение о флоте).
>
> **Актуальная карта флота, протоколов и доступов:** `memory/ORIENT.md`.
> **Живое состояние одной командой:** `sh /root/maestrotv-ops/orient.sh`.
> **Статус задач:** `memory/OPEN.md`.
>
> **Авторитетный S1 checkpoint владельца (29.08.2026):** единственный актуальный
> S1 — `193.17.183.48` (`ubuntu24`, SSH host key
> `SHA256:nz7FYGv3rSajprEtnn4nPm+XDIVScfo2iBN8dlrNhfU`). Сверка 2026-08-02,
> клиентские числа и прежние topology notes ниже — исторический snapshot, а не
> команда для обращения к старому узлу.

Own-brand **Android TV** VPN client. Headline feature: **the client never touches
keys** — the app fetches its config from the backend and auto-updates when the
key rotates. Built so a non-technical customer needs to do nothing after install.

## Decisions (2026-06-14)

- **Engine: sing-box** (single core), consumed as a library (`libbox` AAR). Covers
  VLESS/Reality/VMess/Trojan/Shadowsocks and does failover (`urltest`) natively.
  Base: fork/adapt **sing-box-for-android (sfa)** — native Kotlin, already has
  auto-updating *remote profiles* (= the auto-key mechanism), TV-adaptable.
- **Backends (both, with failover):**
  - Server 1 — current box `193.17.183.48` (`ubuntu24`): **3x-ui** (Xray VLESS/Reality).
    Has subscription-URL support → the natural auto-key source.
  - Server 2 — `85.137.166.237`: **NaiveProxy**, ⚠️ has existing production users —
    touch carefully, never disrupt them.
  - **NaiveProxy caveat:** sing-box/Xray have NO naive *client* (naive is a
    separate Chromium-based protocol). Plan (a, chosen): add a VLESS/Reality
    inbound to server 2 *alongside* naive so ONE core covers both servers and
    existing naive users are untouched. Plan (b, fallback): bundle a separate
    naive client = two cores, much more work.
    > ⛔ **ПЛАН (a) ТАК И НЕ ВЫПОЛНЕН.** Проверено 2026-08-02: на S2 нет ни `xray`, ни `x-ui`;
    > он отдаёт только NaiveProxy :443, Hysteria2 :8443/udp и AnyTLS :8443/tcp. VLESS там НЕТ.
    > Вместо этого вторым и третьим VLESS-узлами стали S3 (46.30.42.151) и S4 (89.125.19.95).
- **Auto-auth (zero-config for the client):** per-customer **subscription URL**
  (a 3x-ui sub link returning BOTH servers' configs). The app polls it and
  re-applies → key rotation is invisible to the client. The URL is provisioned by
  the OWNER at install time (owner sideloads / pushes via cloud per customer).
- **Distribution:** owner installs the app per customer (sideload / cloud) — NOT
  Google Play. So **no in-app billing**; payment is handled by the owner (СБП),
  out of band. (Can add an in-app СБП+Telegram flow later if wanted.)
- **No local Android SDK** on the build box → APK is built by **GitHub Actions CI**
  (same model as the old olcrtc app).

## Data flow

```
owner provisions customer → per-customer subscription URL (3x-ui sub link)
        │
        ▼
Android TV app (first run) ── stores sub URL ──► polls it (e.g. every N min)
        │                                              │
        ▼                                              ▼
 sing-box core (urltest selector)            config updated → key rotation transparent
   ├── server1 VLESS/Reality (193.17.183.48)
   └── server2 VLESS/Reality (85.137.166.237, added alongside naive)   ← ⛔ НЕ СДЕЛАНО, см. выше
       (фактически: S3 46.30.42.151 и S4 89.125.19.95 — оба VLESS/Reality :443)
        │
        ▼
   VpnService tun → internet
```

## Roadmap

1. **Engine integration** — get a buildable sing-box `libbox` for Android (prebuilt
   AAR vs gomobile build in CI). Decide fork-sfa vs thin-custom-app + AAR.
2. **TV app shell** — Compose-for-TV (or sfa's UI), VpnService, D-pad UX, brand.
3. **Auto-subscription** — fetch + parse the sub link (both servers), sing-box
   urltest failover, periodic auto-refresh.
4. **Zero-config provisioning** — how the owner binds a customer (baked URL /
   activation code / device-bound register-and-approve).
5. **Server 2 VLESS inbound** — add carefully on 85.137.166.237 (don't disrupt naive).
   ⛔ ОТМЕНЁН: вместо этого подняты отдельные узлы S3 и S4.
6. **CI** — GitHub Actions → signed APK; owner-distributable.
7. (later) in-app СБП payment, multi-server picker, update channel.

## Server 2 study (85.137.166.237) — read-only, 2026-06-14

- Ubuntu 24.04, 2 cores / 1.9 GB / ~3 GB free, up 36 days. No docker.
- **NaiveProxy = Caddy `forward_proxy`** on `:443` (TCP + QUIC/H3), domain
  `wapmix.duckdns.org`, basic_auth, **14 existing users** (DO NOT disrupt),
  `probe_resistance`/`hide_ip`/`hide_via`.
- Managed by **rixxx-panel** (`/opt/naiveproxy-panel`, node on `:3000`,
  `panel/data/{config,users}.json`) which regenerates the Caddyfile.
  - **REST API** (the provisioning backend): `/api/login`, `/api/naive/users`
    (GET/POST), `/api/hy2/users`, `/api/config`, `/api/status`, ...
  - Stack flags: `naive: true`, `hy2: false` — **the panel can also run
    Hysteria2** on `:443/udp` (same Caddy cert). Currently off.
    > ⚠️ Речь о флаге в rixxx-панели. Hysteria2 у нас поднят ОТДЕЛЬНОЙ инсталляцией
    > (юнит `hysteria-server.service`, :8443/udp) и раздаётся всем клиентам — см. раздел
    > «Hysteria2 — INSTALLED & TESTED» ниже, он верен.
- Also present (not app-related): **frps** (FRP server :7000, allowPorts
  20000-21999), nginx :8080.

### Refined engine plan (one core, both servers)

sing-box covers BOTH servers with NO second client:
- Server 1: **VLESS/Reality** (3x-ui).
- Server 2: either **`http` outbound** (HTTPS-CONNECT to the Caddy naive proxy
  with basic_auth — works today, no server change, but no naive obfuscation) OR
  **enable Hysteria2** on server 2 (sing-box-native, real QUIC obfuscation — a
  small, panel-supported change that does not touch the 14 naive users).
- `urltest` selector → automatic failover between servers.
- Provisioning: the rixxx-panel API issues server-2 creds; 3x-ui issues server-1
  configs. A thin per-customer "subscription" combines both for the app.

## Historical Server 1 study (old S1 `194.48.141.106`, `wapmixx.ru`) — 2026-06-14

> **Superseded 29.08.2026:** this evidence describes the permanently retired
> predecessor only. It is not an operational target; use the owner-authoritative
> S1 checkpoint above for every current or future action.

- **3x-ui v3.3.0.** VLESS inbounds: `:443` ("moy2") + `:8443` ("VLESS STABIL").
  ⚠️ Замер 14.06 был 133 и 7 клиентов. Пересчёт 2026-08-02: **69 и 2**. Цифры в этом файле
  не поддерживаются — брать их командой, а не отсюда.
- **Subscription server ON**: `subPort 2096`, `subPath /sub/`, `/json/`. So each
  client already has `https://wapmixx.ru:2096/sub/<subId>` that serves the current
  config — **this IS the auto-key backend** (rotate the key in 3x-ui → the sub
  updates → the app re-pulls). Panel on :2053, domain `wapmixx.ru`.

## v1 scope (simplest path that solves the client's pain)

The auto-key backend already exists (3x-ui subscription). v1 = an Android TV app
that consumes a per-customer 3x-ui subscription URL:

1. App provisioned at install with `https://wapmixx.ru:2096/sub/<subId>` (owner
   bakes/enters it once per customer — zero client input).
2. App polls the sub URL → parses VLESS config(s) → sing-box connects → re-polls
   periodically so key rotation is invisible. TV-first UI, MaestroVPN brand.
3. Distribution: owner sideloads / cloud-pushes. No Google Play, no in-app billing.

v2: add server 2 (enable Hysteria2 via rixxx-panel) + a thin subscription
aggregator that merges server-1 (VLESS) + server-2 (hy2) into one app
subscription with `urltest` failover; optional in-app СБП payment.

## FULL SPEC (owner, 2026-06-14)

App (Android TV, branded):
- **In-app purchase + renewal** of a subscription.
- **3 protocols in ONE subscription: Hysteria2 + VLESS + Naive**, switchable in-app.
  (Hy2 not yet on the server — enable via rixxx-panel, don't touch the 14 naive users.)
- **Auto-start on device boot** (BOOT_COMPLETED → VpnService reconnect).
- **Per-app split tunnel** — choose which apps route via VPN vs direct.
- **4 protocols: Hysteria2 + VLESS + Naive + Mieru** (`мейру` = Mieru, enfein/mieru),
  switchable in-app.

### Engine for 4 disparate protocols

sing-box natively does VLESS + Hy2 only. Naive and **Mieru** need extra clients.
Clean design: **sing-box owns the tun + routing + per-app split-tunnel + the
outbound selector, chaining to a local SOCKS5 proxy for each non-native protocol.**
Protocol switch = sing-box selector. This is the v2rayNG/Hiddify plugin pattern.
- **naive**: a bundled `libnaive` exec'd as a child process serving local SOCKS5.
- **mieru (Phase 2, 2026-06-17)**: runs **IN-PROCESS** — bound into the SAME gomobile
  library as libbox (`io.nekohasekai.mierubridge.*`, one shared Go runtime), so the
  Android 12+ phantom-process killer can't reap it and it works on every ABI. See
  `mieru-bridge/bridge.go` + `MieruHelper.kt`. The bundled `libmieru.so` exec binary
  is kept only as a fallback (also the panic-isolation tier — a one-runtime panic is
  process-fatal). A separate gomobile lib was rejected (2nd Go runtime = go.Seq
  collision + signal-handler crashes).
- Outbounds: VLESS (native), Hy2 (native), Naive (socks→naive child), Mieru (socks→in-process bridge).

Backend ("maestro-panel") — needed:
- Accounts + expiry; purchase/renewal via **СБП + Telegram-approve** (reuse vpn_bot).
- On approve/renew: provision the same customer in **3x-ui** (VLESS client, via its API)
  + **rixxx-panel** (Naive user, + Hy2 user once enabled), one shared expiry.
- Serve the app **one combined subscription** = all 3 protocol configs for that customer.

## Hysteria2 — INSTALLED & TESTED (2026-06-14, server 2, safe)

- `hysteria v2.9.2`, **independent install** (NOT via the rixxx-panel 443-coexistence
  mode — to avoid touching naive). Own everything:
  - listen **`:8443/udp`** (Caddy keeps 443 untouched — naive's 14 users unaffected,
    verified before/after; UFW additively allows 8443/udp).
  - self-signed EC cert `/etc/hysteria/server.{crt,key}` → **app uses `insecure: true`
    or pins SHA256 `A9E737A916426B8B7C5DCF1F43F9C0A8914459F3E4FB3BBC5DE79C752D63F4C7`**.
  - auth `userpass` (per-customer); systemd `hysteria-server`; masquerade→bing.
  - test creds root-only in `/root/hysteria-app-creds.txt` (server 2).
- **Verified:** curl through it → HTTP 204, exit IP 85.137.166.237. Works.
- App Hy2 outbound: `server: wapmix.duckdns.org:8443`, `auth: <user>:<pass>`,
  `tls.insecure: true` (or pinSHA256), QUIC.
- The backend manages real Hy2 users by editing `/etc/hysteria/config.yaml`
  `auth.userpass` + `systemctl reload/restart hysteria-server` (own, not the panel).

## Build order

1. **Study 3x-ui v3.3.0 API** (create/extend client) + rixxx-panel API → the
   provisioning primitives.  ← start here
2. **maestro-panel backend**: accounts, provision-across-both-servers, combined
   subscription endpoint, СБП/Telegram-approve (reuse vpn_bot + olcrtc panel patterns).
3. **Enable Hysteria2** on server 2 via rixxx-panel (careful; naive users untouched).
4. **App**: fork sfa/Hiddify (sing-box + subscription + split-tunnel) → brand + TV UI
   + protocol switcher + autostart + purchase/renewal screens (talk to maestro-panel).
5. **CI** → signed APK; owner-distributable.

## Open items to confirm with owner

- "мейру" interpreted as the 3x-ui Xray protocol (VLESS/Reality). Confirm.
- Plan (a) for naive (add VLESS to server 2) vs (b) bundle naive client.
- Provisioning UX: activation code vs device-bound vs baked-per-customer URL.
