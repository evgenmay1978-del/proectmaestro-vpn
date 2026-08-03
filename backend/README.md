# maestro-panel (backend)

Provisioning + combined-subscription + billing for MaestroVPN TV.
Runs on server 1 (alongside 3x-ui). Go.

- `internal/subgen` — renders the per-customer sing-box config. На 2026-08-02 это
  **шесть outbounds** — VLESS/Reality на трёх узлах (`vless` S1, `vless-s3`, `vless-s4`),
  Hysteria2, Naive, AnyTLS — плюс `auto` (urltest) и `select`, ПЛЮС секция `endpoints`
  с AmneziaWG. ⚠️ Число растёт с числом узлов, не заучивать.
  ⛔ Узел добавляется в ДВА места: `GenerateSingbox` (наше приложение) И `ShareLinks`
  (iPhone/Karing, base64-список ссылок). Добавленный только в первое невидим для всех
  не-Android клиентов — это пинит `TestS4ReachesAppAndKaring`. **done + tested.**

Next: customer store, server-2 provisioning (naive+hy2 over ssh), 3x-ui client
(VLESS via API, operator-configured creds), subscription HTTP endpoint, payment.
