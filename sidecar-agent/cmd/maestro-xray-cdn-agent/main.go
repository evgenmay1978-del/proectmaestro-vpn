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
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/agent"
	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/preflight"
	agentserver "github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/server"
	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/xrayclient"
)

const (
	listenAddress      = "0.0.0.0:18443"
	healthAddress      = "127.0.0.1:18444"
	expectedClientName = "maestro-whitelist-controller"
	xrayPIDPath        = "/run/maestro-xray-cdn-pid/xray.pid"
)

func main() {
	if err := run(); err != nil {
		log.Printf("maestro sidecar agent stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 {
		if len(os.Args) != 4 || os.Args[1] != "write-xray-pid" || os.Args[2] != xrayPIDPath {
			return errors.New("sidecar agent: invalid helper invocation")
		}
		return writeXrayPIDFile(os.Args[2], os.Args[3])
	}
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
	preflightConfig, err := preflight.LoadConfig(preflight.RuntimeConfigSource{
		XrayConfigFile: configuration.xrayConfigFile, ActiveOriginsFile: configuration.activeOriginsFile,
		ControllerSourceIPFile: configuration.controllerSourceIPFile,
		RelayCADirectory:       configuration.relayCADirectory, RelayCredentialDirectory: configuration.relayCredentialDirectory,
	}, configuration.releaseID, configuration.configDigest)
	if err != nil {
		return err
	}
	liveSystem, err := preflight.NewLiveSystem(configuration.nftBinary)
	if err != nil {
		return err
	}
	readinessPreflight, err := preflight.NewChecker(preflightConfig, liveSystem, time.Now)
	if err != nil {
		return err
	}
	reconciler, err := agent.NewReconciler(agent.ReconcilerConfig{
		Handler: xray, Store: stateStore, InboundTag: agent.DefaultInboundTag,
		ReleaseID: configuration.releaseID, ConfigDigest: configuration.configDigest,
		ProcessBootID: bootIdentity, Preflight: readinessPreflight, ReceiptTTL: agent.DefaultReceiptTTL,
	})
	if err != nil {
		return err
	}
	healthListener, err := net.Listen("tcp", healthAddress)
	if err != nil {
		return errors.New("sidecar agent: relay health listener failed")
	}
	healthServer := &http.Server{
		Addr: healthAddress, Handler: http.HandlerFunc(relayHealth),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
		IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4 << 10,
	}
	healthErrors := make(chan error, 1)
	go func() {
		healthErrors <- healthServer.Serve(healthListener)
	}()
	defer healthServer.Close()
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
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		return healthServer.Shutdown(shutdownContext)
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("sidecar agent: HTTPS server failed")
	case err := <-healthErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("sidecar agent: relay health server failed")
	}
}

type configuration struct {
	releaseID                string
	configDigest             string
	receiptDirectory         string
	credentialDirectory      string
	xrayPIDFile              string
	xrayConfigFile           string
	activeOriginsFile        string
	controllerSourceIPFile   string
	relayCADirectory         string
	relayCredentialDirectory string
	nftBinary                string
	serverCert               string
	serverKey                string
	controllerCA             string
	xrayClientCert           string
	xrayClientKey            string
	xrayCA                   string
	xrayServerName           string
}

func loadConfiguration() (configuration, error) {
	configuration := configuration{
		releaseID: os.Getenv("MAESTRO_RELEASE_ID"), configDigest: os.Getenv("MAESTRO_CONFIG_DIGEST"),
		receiptDirectory:         envOr("MAESTRO_RECEIPT_DIRECTORY", "/var/lib/maestro-xray-cdn-agent/receipts"),
		credentialDirectory:      envOr("MAESTRO_CREDENTIAL_DIRECTORY", "/var/lib/maestro-xray-cdn-agent/credentials"),
		xrayPIDFile:              envOr("MAESTRO_XRAY_PID_FILE", xrayPIDPath),
		xrayConfigFile:           envOr("MAESTRO_XRAY_CONFIG_FILE", "/run/maestro-xray-cdn/config.json"),
		activeOriginsFile:        envOr("MAESTRO_ACTIVE_ORIGIN_IPS_FILE", "/etc/maestro-xray-cdn-agent/active-origin-ips.json"),
		controllerSourceIPFile:   envOr("MAESTRO_CONTROLLER_SOURCE_IP_FILE", "/etc/maestro-xray-cdn-agent/controller-source-ip.json"),
		relayCADirectory:         envOr("MAESTRO_RELAY_CA_DIRECTORY", "/etc/maestro-xray-cdn-agent/relay-ca"),
		relayCredentialDirectory: envOr("MAESTRO_RELAY_CREDENTIAL_DIRECTORY", "/var/lib/maestro-xray-cdn-agent/relay-credentials"),
		nftBinary:                envOr("MAESTRO_NFT_BINARY", "/usr/sbin/nft"),
		serverCert:               envOr("MAESTRO_AGENT_SERVER_CERT", "/etc/maestro-xray-cdn-agent/server/server.crt"),
		serverKey:                envOr("MAESTRO_AGENT_SERVER_KEY", "/etc/maestro-xray-cdn-agent/server/server.key"),
		controllerCA:             envOr("MAESTRO_CONTROLLER_CA", "/etc/maestro-xray-cdn-agent/controller-ca/client-ca.crt"),
		xrayClientCert:           envOr("MAESTRO_XRAY_CLIENT_CERT", "/etc/maestro-xray-cdn/api-mtls/sidecar-agent.crt"),
		xrayClientKey:            envOr("MAESTRO_XRAY_CLIENT_KEY", "/etc/maestro-xray-cdn/api-mtls/sidecar-agent.key"),
		xrayCA:                   envOr("MAESTRO_XRAY_API_CA", "/etc/maestro-xray-cdn/api-mtls/server-ca.crt"),
		xrayServerName:           envOr("MAESTRO_XRAY_API_SERVER_NAME", "maestro-xray-api"),
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

func writeXrayPIDFile(path, value string) (returnErr error) {
	if !filepath.IsAbs(path) || value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "+- \t\r\n") {
		return errors.New("sidecar agent: invalid Xray PID")
	}
	pid, err := strconv.ParseUint(value, 10, 31)
	if err != nil || pid == 0 {
		return errors.New("sidecar agent: invalid Xray PID")
	}
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return errors.New("sidecar agent: Xray PID directory unavailable")
	}
	temporary, err := os.CreateTemp(directory, ".xray-pid-*")
	if err != nil {
		return errors.New("sidecar agent: create Xray PID file")
	}
	temporaryPath := temporary.Name()
	defer func() {
		if temporary != nil {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return errors.New("sidecar agent: protect Xray PID file")
	}
	if _, err := temporary.WriteString(strconv.FormatUint(pid, 10) + "\n"); err != nil {
		return errors.New("sidecar agent: write Xray PID file")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sidecar agent: sync Xray PID file")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("sidecar agent: close Xray PID file")
	}
	temporary = nil
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return errors.New("sidecar agent: publish Xray PID file")
	}
	if runtime.GOOS != "windows" {
		directoryHandle, err := os.Open(directory)
		if err != nil {
			return errors.New("sidecar agent: open Xray PID directory")
		}
		defer directoryHandle.Close()
		if err := directoryHandle.Sync(); err != nil {
			return errors.New("sidecar agent: sync Xray PID directory")
		}
	}
	return nil
}

func relayHealth(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || request.URL.Path != "/healthz" {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("X-Maestro-Relay-Health", "exact")
	writer.WriteHeader(http.StatusNoContent)
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
		configuration.xrayConfigFile, configuration.activeOriginsFile, configuration.relayCADirectory,
		configuration.controllerSourceIPFile, configuration.relayCredentialDirectory, configuration.nftBinary,
		configuration.serverCert, configuration.serverKey, configuration.controllerCA,
		configuration.xrayClientCert, configuration.xrayClientKey, configuration.xrayCA,
	} {
		if !filepath.IsAbs(path) {
			return false
		}
	}
	return true
}
