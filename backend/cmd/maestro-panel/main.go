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
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
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
		})
		prov = provision.New(st, xc, s2, provision.Config{
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
		})
		log.Printf("provisioning enabled (3x-ui + server2)")
	} else {
		log.Printf("provisioning disabled (set XUI_BASE_URL + S2_PASSWORD to enable); serving subscriptions only")
	}

	srv := &http.Server{
		Addr: listen,
		Handler: api.New(st, prov, ost, api.Config{
			AdminToken: os.Getenv("MAESTRO_ADMIN_TOKEN"),
			SubBaseURL: env("MAESTRO_SUB_BASE", "https://wapmixx.ru:8910"),
			SBPPhone:   os.Getenv("MAESTRO_SBP_PHONE"),
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
