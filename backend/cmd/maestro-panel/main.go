// Command maestro-panel is the MaestroVPN TV provisioning + subscription backend.
// It runs on server 1 (alongside 3x-ui) and serves the per-customer sing-box
// subscription the Android TV app polls.
//
// Config via env:
//
//	MAESTRO_LISTEN     listen address (default 127.0.0.1:8910)
//	MAESTRO_STORE      customer store path (default /var/lib/maestro/customers.json)
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
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

	srv := &http.Server{
		Addr:              listen,
		Handler:           api.New(st).Handler(),
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
