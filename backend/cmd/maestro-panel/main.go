// Command maestro-panel is the MaestroVPN TV provisioning + subscription backend.
// It runs on server 1 (alongside 3x-ui) and serves the per-customer sing-box
// subscription the Android TV app polls, plus a token-guarded admin API to
// provision and renew customers across both servers.
//
// Config via env (see README); secrets never come from flags/repo.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/promo"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/provision"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/server2"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/xui"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func main() {
	listen := env("MAESTRO_LISTEN", "127.0.0.1:8910")
	storePath := env("MAESTRO_STORE", "/var/lib/maestro/customers.json")

	st, err := store.Open(storePath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}

	ost, err := order.Open(env("MAESTRO_ORDER_STORE", "/var/lib/maestro/orders.json"))
	if err != nil {
		log.Fatalf("open order store: %v", err)
	}

	// In-app free-trial ledger (anti-abuse). Salt seeds the HMAC over device anchors.
	pst, err := promo.Open(env("MAESTRO_PROMO_FILE", "/var/lib/maestro/trials.json"), env("MAESTRO_TRIAL_SALT", "maestro-trial-v1"))
	if err != nil {
		log.Fatalf("open trial store: %v", err)
	}

	// olcRTC global config (carrier room/key) — hot-swappable so an expired Telemost room
	// propagates without a redeploy (POST /admin/olcrtc/room). Missing file = disabled.
	olc, err := olcconf.Open(env("MAESTRO_OLC_FILE", "/var/lib/maestro/olcrtc.json"))
	if err != nil {
		log.Fatalf("open olcrtc config: %v", err)
	}
	// Seed the olcRTC allowlist from MAESTRO_OLC_LOGINS the FIRST time (empty config) — after
	// that the panel manages it at runtime, so the env is just the bootstrap value.
	if len(olc.Get().Logins) == 0 {
		if err := olc.SetLogins(api.ParseOlcLogins(os.Getenv("MAESTRO_OLC_LOGINS"))); err != nil {
			log.Printf("seed olcrtc logins: %v", err)
		}
	}

	// The provisioner is wired only when its dependencies are configured.
	var prov api.Provisioner
	if env("XUI_BASE_URL", "") != "" && env("S2_PASSWORD", "") != "" {
		xc, err := xui.New(xui.Config{
			BaseURL:  os.Getenv("XUI_BASE_URL"),
			Host:     os.Getenv("XUI_HOST"),
			Token:    os.Getenv("XUI_TOKEN"), // Bearer API token — the real 3x-ui auth (login is CSRF-blocked)
			Username: os.Getenv("XUI_USER"),
			Password: os.Getenv("XUI_PASS"),
			Insecure: env("XUI_INSECURE", "1") == "1",
		})
		if err != nil {
			log.Fatalf("xui client: %v", err)
		}
		s2 := server2.New(server2.Config{
			Host: env("S2_HOST", "85.137.166.237"), User: env("S2_USER", "root"),
			Password: os.Getenv("S2_PASSWORD"), Hy2Port: atoi(os.Getenv("S2_HY2_PORT"), 8443),
			NaivePanelURL:    os.Getenv("NAIVE_PANEL_URL"),
			NaivePanelUser:   os.Getenv("NAIVE_PANEL_USER"),
			NaivePanelPass:   os.Getenv("NAIVE_PANEL_PASS"),
			AnyTLSPort:       atoi(os.Getenv("ANYTLS_PORT"), 8443),
			AnyTLSCert:       env("ANYTLS_CERT", "/etc/sing-box-anytls/cert.pem"),
			AnyTLSKey:        env("ANYTLS_KEY", "/etc/sing-box-anytls/key.pem"),
			AnyTLSService:    env("ANYTLS_SERVICE", "sing-box-anytls"),
			AnyTLSConfigPath: env("ANYTLS_CONFIG", "/etc/sing-box-anytls/config.json"),
		})
		provCfg := provision.Config{
			VLESS: provision.VLESSTmpl{
				InboundID: atoi(os.Getenv("XUI_INBOUND"), 2),
				Server:    env("VLESS_SERVER", "wapmixx.ru"), Port: atoi(os.Getenv("VLESS_PORT"), 443),
				SNI: os.Getenv("VLESS_SNI"), PublicKey: os.Getenv("VLESS_PBK"),
				ShortID: os.Getenv("VLESS_SID"), Flow: env("VLESS_FLOW", "xtls-rprx-vision"),
				Fingerprint: env("VLESS_FP", "chrome"),
			},
			Hy2: provision.Hy2Tmpl{
				Server: env("HY2_SERVER", "wapmix.duckdns.org"), Port: atoi(os.Getenv("S2_HY2_PORT"), 8443),
				SNI: env("HY2_SNI", "wapmix.duckdns.org"), Insecure: env("HY2_INSECURE", "1") == "1",
			},
		}
		// Naive: enabled when the rixxx-panel is reachable + NAIVE_SERVER set.
		if os.Getenv("NAIVE_SERVER") != "" {
			provCfg.Naive = provision.NaiveTmpl{
				Server: os.Getenv("NAIVE_SERVER"), Port: atoi(os.Getenv("NAIVE_PORT"), 443),
				SNI: env("NAIVE_SNI", os.Getenv("NAIVE_SERVER")),
			}
		}
		// AnyTLS: standalone sing-box "anytls" server on SERVER 2 (8443/tcp), managed over
		// SSH alongside hy2/naive (server-side facts live in server2.Config above).
		// Enabled when ANYTLS_SERVER is set.
		if os.Getenv("ANYTLS_SERVER") != "" {
			provCfg.AnyTLS = provision.AnyTLSTmpl{
				Server: os.Getenv("ANYTLS_SERVER"), Port: atoi(os.Getenv("ANYTLS_PORT"), 8443),
				SNI: env("ANYTLS_SNI", os.Getenv("ANYTLS_SERVER")), Insecure: env("ANYTLS_INSECURE", "1") == "1",
			}
		}
		// 3rd node (S3): a SECOND 3x-ui panel serving VLESS-Reality, managed over the
		// public internet via its Bearer API token. Enabled when S3_XUI_BASE_URL + S3_VLESS_SERVER
		// are set; otherwise every S3 code path is a no-op (xui3 stays nil).
		var xc3 provision.NodeClienter
		if env("S3_XUI_BASE_URL", "") != "" && env("S3_VLESS_SERVER", "") != "" {
			x3, err := xui.New(xui.Config{
				BaseURL:  os.Getenv("S3_XUI_BASE_URL"),
				Host:     os.Getenv("S3_XUI_HOST"),
				Token:    os.Getenv("S3_XUI_TOKEN"),
				Insecure: env("S3_XUI_INSECURE", "1") == "1",
			})
			if err != nil {
				log.Fatalf("s3 xui client: %v", err)
			}
			xc3 = x3
			provCfg.VLESS3 = provision.VLESSTmpl{
				InboundID: atoi(os.Getenv("S3_VLESS_INBOUND"), 1),
				Server:    os.Getenv("S3_VLESS_SERVER"), Port: atoi(os.Getenv("S3_VLESS_PORT"), 443),
				SNI: os.Getenv("S3_VLESS_SNI"), PublicKey: os.Getenv("S3_VLESS_PBK"),
				ShortID: os.Getenv("S3_VLESS_SID"), Flow: env("S3_VLESS_FLOW", "xtls-rprx-vision"),
				Fingerprint: env("S3_VLESS_FP", "chrome"),
			}
		}
		pc := provision.New(st, xc, s2, provCfg)
		if xc3 != nil {
			pc.SetS3Node(xc3)
		}
		prov = pc
		log.Printf("provisioning enabled (3x-ui + server2; naive=%v anytls=%v s3=%v)",
			os.Getenv("NAIVE_SERVER") != "", os.Getenv("ANYTLS_SERVER") != "",
			os.Getenv("S3_VLESS_SERVER") != "")
		// Pull each customer's authoritative expiry from whichever panel owns it (3x-ui
		// VLESS and/or the s2 naive panel) into the unified store, advance-only, every
		// 15 min — so a renewal in ANY of the 3 panels propagates to the app + the
		// customer's other protocols. No admin endpoint exposed, no bot change.
		go func() {
			pc.ReconcileExpiries()
			tk := time.NewTicker(15 * time.Minute)
			defer tk.Stop()
			for range tk.C {
				pc.ReconcileExpiries()
			}
		}()
	} else {
		log.Printf("provisioning disabled (set XUI_BASE_URL + S2_PASSWORD to enable); serving subscriptions only")
	}

	srv := &http.Server{
		Addr: listen,
		Handler: api.New(st, prov, ost, pst, api.Config{
			AdminToken: os.Getenv("MAESTRO_ADMIN_TOKEN"),
			// Web admin panel: served under a secret path, guarded by a bcrypt password hash.
			// Both must be set to enable it (env holds the HASH, never the plaintext).
			PanelPath:         os.Getenv("MAESTRO_PANEL_PATH"),
			PanelPasswordHash: os.Getenv("MAESTRO_PANEL_PASSWORD_HASH"),
			PanelPWFile:       env("MAESTRO_PANEL_PW_FILE", "/var/lib/maestro/panel-pw.hash"),
			OlcrtcRoomScript:  env("MAESTRO_OLCRTC_ROOM_SH", "/usr/local/bin/olcrtc-room.sh"),
			OlcHealthFile:     env("MAESTRO_OLC_HEALTH_FILE", "/var/lib/maestro/olcrtc-health.json"),
			SubBaseURL:        env("MAESTRO_SUB_BASE", "https://wapmixx.ru:8910"),
			SBPPhone:          os.Getenv("MAESTRO_SBP_PHONE"),
			PayURL:            os.Getenv("MAESTRO_SBP_PAY_URL"),
			TGBotToken:        os.Getenv("MAESTRO_TG_BOT_TOKEN"),
			TGAdminID:         os.Getenv("MAESTRO_TG_ADMIN_ID"),
			UpdateDir:         env("MAESTRO_UPDATE_DIR", "/var/lib/maestro/update"),
			ReportDir:         env("MAESTRO_REPORT_DIR", "/var/lib/maestro/reports"),
			// Per-account 5-device cap, on by default; MAESTRO_DEVICE_LIMIT=off is a live
			// kill switch (no redeploy) if it ever misbehaves against real customers.
			EnforceDeviceLimit: env("MAESTRO_DEVICE_LIMIT", "on") != "off",
			// In-app free trial (POST /trial): 2 days, soft per-/24 quota of 3 trials per day.
			TrialDays:    atoi(os.Getenv("MAESTRO_TRIAL_DAYS"), 2),
			TrialIPQuota: atoi(os.Getenv("MAESTRO_TRIAL_IP_QUOTA"), 3),
			OLC:          olc,
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("maestro-panel listening on %s (store %s)", listen, storePath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
