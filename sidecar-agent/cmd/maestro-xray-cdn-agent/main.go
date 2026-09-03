package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/agent"
	agentserver "github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/server"
	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/xrayclient"
)

const (
	listenAddress      = "0.0.0.0:18443"
	expectedClientName = "maestro-whitelist-controller"
)

func main() {
	if err := run(); err != nil {
		log.Printf("maestro sidecar agent stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := loadConfiguration()
	if err != nil {
		return err
	}
	stateStore, err := agent.NewFileStore(configuration.receiptDirectory, 64)
	if err != nil {
		return err
	}
	xray, err := xrayclient.New(xrayclient.Config{
		Address: "127.0.0.1:18082", ServerName: configuration.xrayServerName,
		ClientCertFile: configuration.xrayClientCert, ClientKeyFile: configuration.xrayClientKey,
		CAFile: configuration.xrayCA,
	}, xrayclient.DirectoryCredentials{Directory: configuration.credentialDirectory})
	if err != nil {
		return err
	}
	defer xray.Close()
	bootIdentity := func() (string, error) {
		return xrayProcessBootID(configuration.xrayPIDFile)
	}
	reconciler, err := agent.NewReconciler(agent.ReconcilerConfig{
		Handler: xray, Store: stateStore, InboundTag: agent.DefaultInboundTag,
		ReleaseID: configuration.releaseID, ConfigDigest: configuration.configDigest,
		ProcessBootID: bootIdentity, ReceiptTTL: agent.DefaultReceiptTTL,
	})
	if err != nil {
		return err
	}
	if bootID, bootErr := bootIdentity(); bootErr == nil {
		if err := stateStore.InvalidateReceiptsExceptBoot(bootID); err != nil {
			return err
		}
	}
	recoveryContext, cancelRecovery := context.WithTimeout(context.Background(), 5*time.Second)
	_, recoveryErr := reconciler.Recover(recoveryContext)
	cancelRecovery()
	if recoveryErr != nil && !errors.Is(recoveryErr, agent.ErrNotFound) {
		log.Print("maestro sidecar agent startup reconciliation is pending")
	}

	tlsConfig, err := agentserver.LoadServerTLSConfig(
		configuration.serverCert, configuration.serverKey, configuration.controllerCA, expectedClientName,
	)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return errors.New("sidecar agent: listen failed")
	}
	server := &http.Server{
		Addr: listenAddress, Handler: agentserver.NewHandler(reconciler), TLSConfig: tlsConfig,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go refreshLoop(ctx, reconciler)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(tls.NewListener(listener, tlsConfig))
	}()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("sidecar agent: HTTPS server failed")
	}
}

type configuration struct {
	releaseID           string
	configDigest        string
	receiptDirectory    string
	credentialDirectory string
	xrayPIDFile         string
	serverCert          string
	serverKey           string
	controllerCA        string
	xrayClientCert      string
	xrayClientKey       string
	xrayCA              string
	xrayServerName      string
}

func loadConfiguration() (configuration, error) {
	configuration := configuration{
		releaseID: os.Getenv("MAESTRO_RELEASE_ID"), configDigest: os.Getenv("MAESTRO_CONFIG_DIGEST"),
		receiptDirectory:    envOr("MAESTRO_RECEIPT_DIRECTORY", "/var/lib/maestro-xray-cdn-agent/receipts"),
		credentialDirectory: envOr("MAESTRO_CREDENTIAL_DIRECTORY", "/var/lib/maestro-xray-cdn-agent/credentials"),
		xrayPIDFile:         envOr("MAESTRO_XRAY_PID_FILE", "/run/maestro-xray-cdn/xray.pid"),
		serverCert:          envOr("MAESTRO_AGENT_SERVER_CERT", "/etc/maestro-xray-cdn-agent/server/server.crt"),
		serverKey:           envOr("MAESTRO_AGENT_SERVER_KEY", "/etc/maestro-xray-cdn-agent/server/server.key"),
		controllerCA:        envOr("MAESTRO_CONTROLLER_CA", "/etc/maestro-xray-cdn-agent/controller-ca/client-ca.crt"),
		xrayClientCert:      envOr("MAESTRO_XRAY_CLIENT_CERT", "/etc/maestro-xray-cdn/api-mtls/sidecar-agent.crt"),
		xrayClientKey:       envOr("MAESTRO_XRAY_CLIENT_KEY", "/etc/maestro-xray-cdn/api-mtls/sidecar-agent.key"),
		xrayCA:              envOr("MAESTRO_XRAY_API_CA", "/etc/maestro-xray-cdn/api-mtls/server-ca.crt"),
		xrayServerName:      envOr("MAESTRO_XRAY_API_SERVER_NAME", "maestro-xray-api"),
	}
	if configuration.releaseID == "" || !validDigest(configuration.configDigest) || !allAbsolute(configuration) {
		return configuration, errors.New("sidecar agent: invalid environment configuration")
	}
	return configuration, nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func refreshLoop(ctx context.Context, reconciler *agent.Reconciler) {
	ticker := time.NewTicker(agent.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshContext, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := reconciler.Refresh(refreshContext)
			cancel()
			if err != nil && !errors.Is(err, agent.ErrNotFound) {
				log.Print("maestro sidecar agent readiness refresh failed")
			}
		}
	}
}

func xrayProcessBootID(pidFile string) (string, error) {
	hostBoot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", errors.New("sidecar agent: host boot identity unavailable")
	}
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		return "", errors.New("sidecar agent: Xray PID unavailable")
	}
	pid := strings.TrimSpace(string(pidBytes))
	if pid == "" || strings.ContainsAny(pid, "/\\\x00\r\n\t ") {
		return "", errors.New("sidecar agent: invalid Xray PID")
	}
	stat, err := os.ReadFile(filepath.Join("/proc", pid, "stat"))
	if err != nil {
		return "", errors.New("sidecar agent: Xray start identity unavailable")
	}
	closing := strings.LastIndex(string(stat), ") ")
	if closing < 0 {
		return "", errors.New("sidecar agent: invalid Xray process identity")
	}
	fields := strings.Fields(string(stat)[closing+2:])
	if len(fields) <= 19 {
		return "", errors.New("sidecar agent: invalid Xray process identity")
	}
	identity := strings.TrimSpace(string(hostBoot)) + "\x00" + pid + "\x00" + fields[19]
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:]), nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func allAbsolute(configuration configuration) bool {
	for _, path := range []string{
		configuration.receiptDirectory, configuration.credentialDirectory, configuration.xrayPIDFile,
		configuration.serverCert, configuration.serverKey, configuration.controllerCA,
		configuration.xrayClientCert, configuration.xrayClientKey, configuration.xrayCA,
	} {
		if !filepath.IsAbs(path) {
			return false
		}
	}
	return true
}
