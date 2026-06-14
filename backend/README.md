# maestro-panel (backend)

Provisioning + combined-subscription + billing for MaestroVPN TV.
Runs on server 1 (alongside 3x-ui). Go.

- `internal/subgen` — renders the per-customer sing-box config (all 4 protocols:
  VLESS/Reality + Hysteria2 native; Naive + Mieru via local SOCKS helpers;
  selector + urltest; tun + route for split tunnel). **done + tested.**

Next: customer store, server-2 provisioning (naive+hy2 over ssh), 3x-ui client
(VLESS via API, operator-configured creds), subscription HTTP endpoint, payment.
