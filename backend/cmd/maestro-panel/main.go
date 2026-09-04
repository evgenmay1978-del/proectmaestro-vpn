// Command maestro-panel is the MaestroVPN TV provisioning + subscription backend.
// It runs on server 1 (alongside 3x-ui) and serves the per-customer sing-box
// subscription the Android TV app polls, plus a token-guarded admin API to
// provision and renew customers across both servers.
//
// Config via env (see README); secrets never come from flags/repo.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/promo"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/provision"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/server2"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
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

type panelRuntime struct {
	mode                    string
	business                api.Business
	handler                 http.Handler
	background              func(context.Context)
	whiteListSidecarSenders map[string]controlplane.ExternalActionSender
}

type runtimeFactories struct {
	legacy func(context.Context) (*panelRuntime, error)
	rqlite func(context.Context) (*panelRuntime, error)
}

func buildRuntime(ctx context.Context, mode string, factories runtimeFactories) (*panelRuntime, error) {
	switch mode {
	case "", "legacy":
		return factories.legacy(ctx)
	case "rqlite":
		return factories.rqlite(ctx)
	default:
		return nil, fmt.Errorf("unsupported MAESTRO_CONTROL_PLANE mode %q", mode)
	}
}

func main() {
	listen := env("MAESTRO_LISTEN", "127.0.0.1:8910")
	switch mode := strings.TrimSpace(os.Getenv("MAESTRO_CONTROL_PLANE")); mode {
	case "rqlite":
		runtimeConfig, err := readRQLiteRuntimeConfig(os.Getenv)
		if err != nil {
			log.Fatalf("configure rqlite runtime: %v", err)
		}
		sidecarConfig, err := readRuntimeWhiteListSidecarConfig(os.Getenv)
		if err != nil {
			log.Fatalf("configure white-list sidecar runtime: %v", err)
		}
		sidecarSenders, err := buildRuntimeWhiteListSidecarSenders(sidecarConfig, func(config sidecaragentclient.Config) (controlplane.ExternalActionSender, error) {
			return sidecaragentclient.New(config)
		})
		if err != nil {
			log.Fatalf("build white-list sidecar runtime: %v", err)
		}
		runtimeInstance, err := buildRQLitePanelRuntime(
			context.Background(), runtimeConfig, rqliteAPIConfigFromEnvironment(), productionRQLiteRuntimeDependencies(),
			sidecarConfig.Enabled,
		)
		if err != nil {
			log.Fatalf("build rqlite runtime: %v", err)
		}
		runtimeInstance.whiteListSidecarSenders = sidecarSenders
		workerContext, stopWorker := context.WithCancel(context.Background())
		var workerDone chan struct{}
		if runtimeInstance.background != nil {
			workerDone = make(chan struct{})
			go func() {
				defer close(workerDone)
				runtimeInstance.background(workerContext)
			}()
		}
		srv := &http.Server{
			Addr: listen, Handler: runtimeInstance.handler, ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			log.Printf("maestro-panel listening on %s (rqlite control plane)", listen)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("serve: %v", err)
			}
		}()
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		stopWorker()
		if workerDone != nil {
			select {
			case <-workerDone:
			case <-time.After(5 * time.Second):
				log.Printf("white-list renewal worker shutdown timed out")
			}
		}
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownContext)
		return
	case "", "legacy":
		// The unchanged legacy composition root begins below.
	default:
		log.Fatalf("unsupported MAESTRO_CONTROL_PLANE mode %q", mode)
	}

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

	// WDTT/VK TURN secrets are kept in one external root-owned JSON file. No
	// default path: an unset env keeps the feature completely off. A configured
	// but EXISTING invalid/unreadable file aborts startup so credentials can never
	// be partially advertised; a configured-but-absent file starts OFF and is
	// populated from the admin panel (which validates+persists atomically).
	vkTurn, err := vkturnconf.OpenStore(os.Getenv("MAESTRO_VKTURN_FILE"))
	if err != nil {
		log.Fatalf("open vkturn config: %v", err)
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
		// 4th node (S4): another 3x-ui panel serving VLESS-Reality, same wiring as S3.
		// Enabled ONLY when S4_XUI_BASE_URL + S4_VLESS_SERVER are both set; otherwise xui4
		// stays nil and every S4 path is a no-op, so existing customers' configs are
		// byte-for-byte unchanged. ⚠️ Give S4 its OWN S4_VLESS_SNI — sharing one SNI with
		// S1/S3 puts both behind a single DPI counter (see the June-2026 TSPU scheme).
		var xc4 provision.NodeClienter
		if env("S4_XUI_BASE_URL", "") != "" && env("S4_VLESS_SERVER", "") != "" {
			x4, err := xui.New(xui.Config{
				BaseURL:  os.Getenv("S4_XUI_BASE_URL"),
				Host:     os.Getenv("S4_XUI_HOST"),
				Token:    os.Getenv("S4_XUI_TOKEN"),
				Insecure: env("S4_XUI_INSECURE", "1") == "1",
			})
			if err != nil {
				log.Fatalf("s4 xui client: %v", err)
			}
			xc4 = x4
			provCfg.VLESS4 = provision.VLESSTmpl{
				InboundID: atoi(os.Getenv("S4_VLESS_INBOUND"), 1),
				Server:    os.Getenv("S4_VLESS_SERVER"), Port: atoi(os.Getenv("S4_VLESS_PORT"), 443),
				SNI: os.Getenv("S4_VLESS_SNI"), PublicKey: os.Getenv("S4_VLESS_PBK"),
				ShortID: os.Getenv("S4_VLESS_SID"), Flow: env("S4_VLESS_FLOW", "xtls-rprx-vision"),
				Fingerprint: env("S4_VLESS_FP", "chrome"),
			}
		}
		pc := provision.New(st, xc, s2, provCfg)
		if xc3 != nil {
			pc.SetS3Node(xc3)
		}
		if xc4 != nil {
			pc.SetS4Node(xc4)
		}
		prov = pc
		log.Printf("provisioning enabled (3x-ui + server2; naive=%v anytls=%v s3=%v s4=%v)",
			os.Getenv("NAIVE_SERVER") != "", os.Getenv("ANYTLS_SERVER") != "",
			os.Getenv("S3_VLESS_SERVER") != "", os.Getenv("S4_VLESS_SERVER") != "")
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
			OlcWBTokenFile:    env("MAESTRO_OLC_WB_TOKEN_FILE", "/var/lib/maestro/wb.token"),
			SubBaseURL:        env("MAESTRO_SUB_BASE", "https://wapmixx.ru:8910"),
			SBPPhone:          os.Getenv("MAESTRO_SBP_PHONE"),
			PayURL:            os.Getenv("MAESTRO_SBP_PAY_URL"),
			TGBotToken:        os.Getenv("MAESTRO_TG_BOT_TOKEN"),
			TGAdminID:         os.Getenv("MAESTRO_TG_ADMIN_ID"),
			UpdateDir:         env("MAESTRO_UPDATE_DIR", "/var/lib/maestro/update"),
			ReportDir:         env("MAESTRO_REPORT_DIR", "/var/lib/maestro/reports"),
			// Per-account 5-device cap, on by default; MAESTRO_DEVICE_LIMIT=off is a live
			// kill switch (no redeploy) if it ever misbehaves against real customers.
			EnforceDeviceLimit: deviceLimitEnforced(env("MAESTRO_DEVICE_LIMIT", "on")),
			// In-app free trial (POST /trial): 2 days, soft per-/24 quota of 3 trials per day.
			TrialDays:    atoi(os.Getenv("MAESTRO_TRIAL_DAYS"), 2),
			TrialIPQuota: atoi(os.Getenv("MAESTRO_TRIAL_IP_QUOTA"), 3),
			OLC:          olc,
			VKTurn:       vkTurn,
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

type runtimeWhiteListSidecarConfig struct {
	Enabled bool
	Nodes   map[string]sidecaragentclient.Config
}

func readRuntimeWhiteListSidecarConfig(getenv func(string) string) (runtimeWhiteListSidecarConfig, error) {
	if getenv == nil {
		return runtimeWhiteListSidecarConfig{}, errors.New("invalid white-list sidecar runtime configuration")
	}
	switch strings.TrimSpace(getenv("MAESTRO_WHITELIST_SIDECAR_ENABLE")) {
	case "", "0":
		return runtimeWhiteListSidecarConfig{}, nil
	case "1":
	default:
		return runtimeWhiteListSidecarConfig{}, errors.New("invalid white-list sidecar runtime configuration")
	}
	caFile := strings.TrimSpace(getenv("MAESTRO_WHITELIST_SIDECAR_CA_FILE"))
	certFile := strings.TrimSpace(getenv("MAESTRO_WHITELIST_SIDECAR_CERT_FILE"))
	keyFile := strings.TrimSpace(getenv("MAESTRO_WHITELIST_SIDECAR_KEY_FILE"))
	if caFile == "" || certFile == "" || keyFile == "" {
		return runtimeWhiteListSidecarConfig{}, errors.New("invalid white-list sidecar runtime configuration")
	}
	nodes := make(map[string]sidecaragentclient.Config, 4)
	for _, nodeID := range []string{"s1", "s2", "s3", "s4"} {
		prefix := "MAESTRO_WHITELIST_SIDECAR_" + strings.ToUpper(nodeID)
		baseURL := strings.TrimSpace(getenv(prefix + "_URL"))
		serverName := strings.TrimSpace(getenv(prefix + "_SERVER_NAME"))
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || serverName == "" {
			return runtimeWhiteListSidecarConfig{}, errors.New("invalid white-list sidecar runtime configuration")
		}
		nodes[nodeID] = sidecaragentclient.Config{
			BaseURL: baseURL, ServerName: serverName, CAFile: caFile, CertFile: certFile, KeyFile: keyFile,
			RequestTimeout: 5 * time.Second, ReceiptLookupTimeout: 2 * time.Second,
		}
	}
	return runtimeWhiteListSidecarConfig{Enabled: true, Nodes: nodes}, nil
}

func buildRuntimeWhiteListSidecarSenders(
	config runtimeWhiteListSidecarConfig,
	factory func(sidecaragentclient.Config) (controlplane.ExternalActionSender, error),
) (map[string]controlplane.ExternalActionSender, error) {
	if !config.Enabled {
		return nil, nil
	}
	if factory == nil || len(config.Nodes) == 0 {
		return nil, errors.New("invalid white-list sidecar runtime configuration")
	}
	senders := make(map[string]controlplane.ExternalActionSender, len(config.Nodes))
	for nodeID, clientConfig := range config.Nodes {
		sender, err := factory(clientConfig)
		if err != nil {
			return nil, err
		}
		if sender == nil {
			return nil, errors.New("white-list sidecar client is unavailable")
		}
		senders[nodeID] = sender
	}
	return senders, nil
}
