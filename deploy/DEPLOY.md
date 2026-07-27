# Deploy maestro-panel (server 1, 194.48.141.106)

Production step — run as root on server 1. This brings `/sub/<token>` and `/claim`
online so the TV app can provision + auto-update.

## Quick path (everything pre-filled)
```bash
bash /root/maestrovpn-tv/deploy/install.sh
```
Reads the 3x-ui Bearer token (from `/root/vpn_bot/.env`) + server-2 password (from
`/root/.ssh/.s2pass`) server-side, writes `/etc/maestro-panel.env` with the discovered
Reality params (SNI `www.intel.com`, pbk/sid from inbound :443), builds the binary,
installs the systemd unit, starts it, and prints the admin token. Then do **step 4 (TLS)**.
The manual steps below are the same, broken out.

## 1. Build the binary
```bash
cd /root/maestrovpn-tv/backend
/usr/local/go/bin/go build -o /usr/local/bin/maestro-panel ./cmd/maestro-panel
```

## 2. Configure
```bash
cp /root/maestrovpn-tv/deploy/maestro-panel.env.example /etc/maestro-panel.env
chmod 600 /etc/maestro-panel.env
# edit /etc/maestro-panel.env and fill the __SET_ME__ / __..._ values:
#   MAESTRO_ADMIN_TOKEN  ->  openssl rand -hex 24
#   XUI_TOKEN            ->  grep PANEL_API_TOKEN /root/vpn_bot/.env
#   S2_PASSWORD          ->  the server-2 root password
#   VLESS_SNI/PBK/SID    ->  from the 3x-ui :443 inbound (panel → inbound → Reality settings)
```

## 3. Install + start
```bash
cp /root/maestrovpn-tv/deploy/maestro-panel.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now maestro-panel
systemctl status maestro-panel --no-pager
curl -s http://127.0.0.1:8910/healthz   # -> ok
```

## 4. TLS exposure (required — MAESTRO_SUB_BASE must be HTTPS-reachable by the TV)
The panel listens on `127.0.0.1:8910` only. Expose it over TLS using the existing
`wapmixx.ru` certificate, then set `MAESTRO_SUB_BASE` to that public URL:
- **Reverse proxy (recommended):** nginx/caddy `https://wapmixx.ru:8910` → `127.0.0.1:8910`.
- Keep `/admin/*` NOT publicly exposed (it's token-guarded, but bind admin to localhost
  or allowlist) — only `/sub/` and `/claim` need to be public.

## 5. Provision a customer + hand the code to the customer
```bash
TOK=$(grep MAESTRO_ADMIN_TOKEN /etc/maestro-panel.env | cut -d= -f2)
curl -s -X POST http://127.0.0.1:8910/admin/provision \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"login":"MAESTRO-TEST","days":30}'
# In the app: "Ввести код подписки" → MAESTRO-TEST → /claim → sub URL → auto-updating profile.
```

Note: provisioning calls 3x-ui (Bearer) + server-2 (SSH) — verify both reachable from
server 1 first. Existing 3x-ui/naive customers are never touched (the app uses its own
inbound clients + the `mtv_` naive prefix).

---

# Инфраструктурная автоматика флота (добавлено 2026-07-27)

Файлы ниже были заведены после аудита серверов и **уже стоят в бою**. Здесь они лежат, чтобы
пережить потерю машины: до этого они существовали ровно в одном экземпляре — на самом сервере.
Копии сверены с боевыми побайтно в день добавления.

## Что где стоит

| файл в репо | куда ставится | сервер | что делает |
|---|---|---|---|
| `deploy/certbot-deploy-reload-cert-consumers.sh` | `/etc/letsencrypt/renewal-hooks/deploy/10-reload-cert-consumers.sh` | S1 | после обновления серта перечитывает его nginx'ом и панелью x-ui |
| `deploy/s3-panel-tunnel.service` | `/etc/systemd/system/` | S1 | ssh-туннель `127.0.0.1:14798 → S3:47989`, чтобы nginx отдавал панель S3 по TLS |
| `deploy/nginx-olcbox-api.conf` | `/etc/nginx/sites-available/olcbox-api` | S1 | сайт на :9443 — olcbox API + проксирование панели S3 |
| `deploy/sync-anytls-cert.sh` + `.service` + `.timer` | `/usr/local/bin/` и `/etc/systemd/system/` | **S2** | ежедневно переносит обновлённый caddy'ем серт в sing-box-anytls |
| `deploy/maestro-backup.sh` | `/usr/local/bin/` | S1 | часовой бэкап control-plane; с 27.07 забирает и базу панели **S3** |
| `ops/disable-xui-sub.sh` | запускается с S1 | S1+S3 | выключает неиспользуемую подписку x-ui на :2096 (есть `--dry-run`) |

## ⛔ Ловушки, стоившие времени — не «чинить» обратно

1. **Перезагружать x-ui только `kill -HUP <MainPID>`.** В юните стоит `ExecReload=kill -USR1`, а USR1
   у этого бинаря означает «restarting xray-core», то есть обрыв ВСЕХ клиентских соединений
   (в момент проверки их было 206). SIGHUP перезапускает только веб-часть панели.
   `systemctl kill` без `--kill-whom=main` тоже заденет xray.
2. **Обновление серта ≠ его применение.** certbot исправно продлевал `wapmixx.ru`, а панель три месяца
   отдавала майский серт, потому что читает файлы один раз при старте. Отсюда deploy-хук.
3. **Серт для AnyTLS брать НОВЕЙШИЙ из хранилища caddy.** Там лежит и мёртвая копия ZeroSSL —
   «первый попавшийся» установит именно её.
4. **Сертификат Let's Encrypt на голый IP** выдаётся только профилем `shortlived` — 160 часов.
   Поэтому панель S3 отдаётся через туннель под сертом `wapmixx.ru`, а не своим.
5. **Первый запрос после `systemctl reload nginx` может вернуть 404** — старый воркер ещё дослуживает.
   Не диагностировать по одному запросу.

## Проверка, что всё живо

```bash
# сертификаты: что реально отдаётся клиентам (а не что лежит на диске)
echo | openssl s_client -connect wapmixx.ru:2053 -servername wapmixx.ru 2>/dev/null | openssl x509 -noout -enddate
echo | openssl s_client -connect 85.137.166.237:8443 -servername wapmix.duckdns.org 2>/dev/null | openssl x509 -noout -enddate

# панель S3 через TLS S1 + туннель
curl -sk -o /dev/null -w '%{http_code}\n' https://wapmixx.ru:9443/<webBasePath>/

# бэкап: в архиве должно быть 9 файлов, включая базу панели S3
journalctl -u maestro-backup --since -2h | grep -E 'S3 panel|uploaded'

# синк серта AnyTLS (на S2): штатный ответ — «cert unchanged»
ssh root@85.137.166.237 systemctl start sync-anytls-cert.service && \
ssh root@85.137.166.237 journalctl -u sync-anytls-cert -n 3 --no-pager
```
