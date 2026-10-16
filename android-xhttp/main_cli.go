//go:build xhttpcli

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Child-process entry point for the authenticated XHTTP client.
//
// Why a separate process: the in-process C-shared library (the JNI bridge in
// native_android.go) crashes the app process with SIGSEGV on the first session start
// (measured 2026-09-26: libbox.so go1.25.11 and libmaestro_xhttp.so go1.26.7 are two Go
// runtimes in one address space — the coexistence the module README called an unproven
// device gate). olcRTC/WDTT already ship as separate PIE executables for the same reason.
//
// Socket bypass: a child process cannot call VpnService.protect(), so the caller must emit
// DIRECT route rules for the CDN edge addresses; this binary therefore reports
// protect=allowed for every socket and relies on those rules (same contract as olcRTC).
//
// Contract with the app: write the payload to an app-private 0600 file, exec this binary
// with -payload and -session, wait for the "ready" line on stdout, then SIGTERM to stop.
func main() {
	payloadPath := flag.String("payload", "", "path to the JSON payload (app-private, 0600)")
	sessionID := flag.Int64("session", 0, "session id (1..2147483647)")
	flag.Parse()
	if *payloadPath == "" || *sessionID <= 0 {
		fmt.Fprintln(os.Stderr, "usage: maestro-xhttp -payload <file> -session <id>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*payloadPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "payload: %v\n", err)
		os.Exit(3)
	}
	// One-shot: the payload carries transport material, so it never stays on disk.
	_ = os.Remove(*payloadPath)

	code := liveEngine.Start(*sessionID, raw, func(int64, int) bool { return true })
	if code != statusOK {
		fmt.Fprintf(os.Stderr, "start failed: status %d\n", code)
		os.Exit(4)
	}
	// The socks listener is up once Start returned; the app keys on this line.
	fmt.Println("ready")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	if code := liveEngine.Stop(*sessionID); code != statusOK {
		fmt.Fprintf(os.Stderr, "stop failed: status %d\n", code)
		os.Exit(5)
	}
}
