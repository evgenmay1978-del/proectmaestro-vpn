package preflight

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/proxy/vless"
	vlessencoding "github.com/xtls/xray-core/proxy/vless/encoding"
)

type fakeSystem struct {
	firewall  FirewallState
	unhealthy string
}

func (system fakeSystem) Firewall(context.Context) (FirewallState, error) {
	return system.firewall, nil
}

func (system fakeSystem) ProbeRelay(_ context.Context, route Route) error {
	if route.ExitID == system.unhealthy {
		return errors.New("synthetic relay failure")
	}
	return nil
}

func testConfig() Config {
	return Config{
		ReleaseID: "release-12", ConfigDigest: strings.Repeat("a", 64),
		ActiveOriginIPs:    []string{"192.0.2.11", "192.0.2.12", "192.0.2.13", "192.0.2.14"},
		ControllerSourceIP: "192.0.2.10",
		Routes: []Route{
			{ExitID: "exit-s1", Address: "127.0.0.1", Port: 18084, ServerName: "exit-s1.example.test", CAFile: "/etc/maestro/exit-s1.crt", CredentialFile: "/etc/maestro/exit-s1.credential"},
			{ExitID: "exit-s2", Address: "192.0.2.12", Port: 18084, ServerName: "exit-s2.example.test", CAFile: "/etc/maestro/exit-s2.crt", CredentialFile: "/etc/maestro/exit-s2.credential"},
			{ExitID: "exit-s3", Address: "192.0.2.13", Port: 18084, ServerName: "exit-s3.example.test", CAFile: "/etc/maestro/exit-s3.crt", CredentialFile: "/etc/maestro/exit-s3.credential"},
			{ExitID: "exit-s4", Address: "192.0.2.14", Port: 18084, ServerName: "exit-s4.example.test", CAFile: "/etc/maestro/exit-s4.crt", CredentialFile: "/etc/maestro/exit-s4.credential"},
		},
	}
}

func TestCheckerRequiresExactSourceFirewallAndEveryRelayHealth(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	config := testConfig()
	checker, err := NewChecker(config, fakeSystem{firewall: FirewallState{ActiveOriginIPs: config.ActiveOriginIPs, ControllerSourceIPs: []string{config.ControllerSourceIP}}}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	attestation, err := checker.Check(context.Background(), "boot-preflight")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !reflect.DeepEqual(attestation.HealthyExitIDs, []string{"exit-s1", "exit-s2", "exit-s3", "exit-s4"}) ||
		attestation.ExpiresAt.Sub(attestation.CheckedAt) != 30*time.Second {
		t.Fatalf("attestation = %#v", attestation)
	}
	raw, err := json.Marshal(attestation)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(raw), "credential") || strings.Contains(string(raw), "example.test") {
		t.Fatalf("relay material leaked into attestation: %s", raw)
	}

	for name, system := range map[string]fakeSystem{
		"source firewall drift": {firewall: FirewallState{ActiveOriginIPs: append(config.ActiveOriginIPs, "192.0.2.99"), ControllerSourceIPs: []string{config.ControllerSourceIP}}},
		"controller exposure":   {firewall: FirewallState{ActiveOriginIPs: config.ActiveOriginIPs, ControllerSourceIPs: []string{"192.0.2.99"}}},
		"unhealthy exact exit":  {firewall: FirewallState{ActiveOriginIPs: config.ActiveOriginIPs, ControllerSourceIPs: []string{config.ControllerSourceIP}}, unhealthy: "exit-s3"},
	} {
		t.Run(name, func(t *testing.T) {
			checker, err := NewChecker(config, system, func() time.Time { return now })
			if err != nil {
				t.Fatalf("NewChecker: %v", err)
			}
			if _, err := checker.Check(context.Background(), "boot-preflight"); err == nil {
				t.Fatal("unsafe relay preflight accepted")
			}
		})
	}
}

func TestLoadConfigBindsRuntimeRoutesToProtectedCredentials(t *testing.T) {
	directory := t.TempDir()
	raw := []byte(`{"outbounds":[{"protocol":"freedom","tag":"direct"},{"protocol":"blackhole","tag":"block"},{"protocol":"vless","settings":{"vnext":[{"address":"192.0.2.11","port":18084,"users":[{"id":"00000000-0000-4000-8000-000000000041","encryption":"none"}]}]},"streamSettings":{"network":"tcp","security":"tls","tlsSettings":{"serverName":"exit-s1.example.test","allowInsecure":false,"alpn":["h2"]}},"tag":"exit-s1"},{"protocol":"vless","settings":{"vnext":[{"address":"192.0.2.12","port":18084,"users":[{"id":"00000000-0000-4000-8000-000000000042","encryption":"none"}]}]},"streamSettings":{"network":"tcp","security":"tls","tlsSettings":{"serverName":"exit-s2.example.test","allowInsecure":false,"alpn":["h2"]}},"tag":"exit-s2"},{"protocol":"vless","settings":{"vnext":[{"address":"192.0.2.13","port":18084,"users":[{"id":"00000000-0000-4000-8000-000000000043","encryption":"none"}]}]},"streamSettings":{"network":"tcp","security":"tls","tlsSettings":{"serverName":"exit-s3.example.test","allowInsecure":false,"alpn":["h2"]}},"tag":"exit-s3"},{"protocol":"vless","settings":{"vnext":[{"address":"192.0.2.14","port":18084,"users":[{"id":"00000000-0000-4000-8000-000000000044","encryption":"none"}]}]},"streamSettings":{"network":"tcp","security":"tls","tlsSettings":{"serverName":"exit-s4.example.test","allowInsecure":false,"alpn":["h2"]}},"tag":"exit-s4"}]}`)
	xrayConfig := filepath.Join(directory, "config.json")
	originsFile := filepath.Join(directory, "active-origins.json")
	controllerSourceFile := filepath.Join(directory, "controller-source.json")
	credentialDirectory := filepath.Join(directory, "relay-credentials")
	caDirectory := filepath.Join(directory, "relay-ca")
	for _, path := range []string{credentialDirectory, caDirectory} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
	}
	if err := os.WriteFile(xrayConfig, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(originsFile, []byte(`["192.0.2.11","192.0.2.12","192.0.2.13","192.0.2.14"]`), 0o600); err != nil {
		t.Fatalf("write origins: %v", err)
	}
	if err := os.WriteFile(controllerSourceFile, []byte(`"192.0.2.10"`), 0o600); err != nil {
		t.Fatalf("write controller source: %v", err)
	}
	for index, exitID := range []string{"exit-s1", "exit-s2", "exit-s3", "exit-s4"} {
		credential := fmt.Sprintf("00000000-0000-4000-8000-00000000004%d", index+1)
		if err := os.WriteFile(filepath.Join(credentialDirectory, exitID+".credential"), []byte(credential+"\n"), 0o600); err != nil {
			t.Fatalf("write credential: %v", err)
		}
		if err := os.WriteFile(filepath.Join(caDirectory, exitID+".crt"), []byte("test CA placeholder"), 0o600); err != nil {
			t.Fatalf("write CA: %v", err)
		}
	}
	digest := sha256.Sum256(raw)
	config, err := LoadConfig(RuntimeConfigSource{
		XrayConfigFile: xrayConfig, ActiveOriginsFile: originsFile, ControllerSourceIPFile: controllerSourceFile,
		RelayCADirectory: caDirectory, RelayCredentialDirectory: credentialDirectory,
	}, "release-12", hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(config.Routes) != 4 || config.Routes[0].CredentialFile != filepath.Join(credentialDirectory, "exit-s1.credential") {
		t.Fatalf("config routes = %#v", config.Routes)
	}
	if err := os.WriteFile(filepath.Join(credentialDirectory, "exit-s3.credential"), []byte("00000000-0000-4000-8000-000000000099\n"), 0o600); err != nil {
		t.Fatalf("replace credential: %v", err)
	}
	if _, err := LoadConfig(RuntimeConfigSource{
		XrayConfigFile: xrayConfig, ActiveOriginsFile: originsFile, ControllerSourceIPFile: controllerSourceFile,
		RelayCADirectory: caDirectory, RelayCredentialDirectory: credentialDirectory,
	}, "release-12", hex.EncodeToString(digest[:])); err == nil || strings.Contains(err.Error(), "00000000") {
		t.Fatalf("runtime credential mismatch error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(credentialDirectory, "exit-s3.credential"), []byte("00000000-0000-4000-8000-000000000043\n"), 0o600); err != nil {
		t.Fatalf("restore credential: %v", err)
	}
	checker, err := NewChecker(config, fakeSystem{firewall: FirewallState{ActiveOriginIPs: config.ActiveOriginIPs, ControllerSourceIPs: []string{config.ControllerSourceIP}}}, time.Now)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	if err := os.WriteFile(xrayConfig, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("replace runtime config: %v", err)
	}
	if _, err := checker.Check(context.Background(), "boot-runtime-drift"); err == nil {
		t.Fatal("changed runtime config accepted after preflight contract load")
	}
}

func TestParseNFTSourceFirewallRequiresAllowlistThenTerminalDrop(t *testing.T) {
	raw := []byte(`{"nftables":[{"chain":{"family":"inet","table":"maestro_xray_cdn","name":"input","type":"filter","hook":"input","prio":0,"policy":"accept"}},{"set":{"family":"inet","table":"maestro_xray_cdn","name":"active_origins_18084","type":"ipv4_addr","elem":["192.0.2.11","192.0.2.12"]}},{"set":{"family":"inet","table":"maestro_xray_cdn","name":"controller_source_18443","type":"ipv4_addr","elem":["192.0.2.10"]}},{"rule":{"family":"inet","table":"maestro_xray_cdn","chain":"input","expr":[{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":18084}},{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"saddr"}},"right":"@active_origins_18084"}},{"accept":null}]}},{"rule":{"family":"inet","table":"maestro_xray_cdn","chain":"input","expr":[{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":18084}},{"drop":null}]}},{"rule":{"family":"inet","table":"maestro_xray_cdn","chain":"input","expr":[{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":18443}},{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"saddr"}},"right":"@controller_source_18443"}},{"accept":null}]}},{"rule":{"family":"inet","table":"maestro_xray_cdn","chain":"input","expr":[{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":18443}},{"drop":null}]}}]}`)
	firewall, err := ParseNFTSourceFirewall(raw)
	if err != nil {
		t.Fatalf("ParseNFTSourceFirewall: %v", err)
	}
	if !reflect.DeepEqual(firewall.ActiveOriginIPs, []string{"192.0.2.11", "192.0.2.12"}) || !reflect.DeepEqual(firewall.ControllerSourceIPs, []string{"192.0.2.10"}) {
		t.Fatalf("firewall = %#v", firewall)
	}
	if _, err := ParseNFTSourceFirewall([]byte(strings.Replace(string(raw), `{"drop":null}`, `{"accept":null}`, 1))); err == nil {
		t.Fatal("source firewall without terminal port-18084 drop accepted")
	}
	unsafeGeneralAccept := strings.Replace(string(raw), `{"nftables":[`, `{"nftables":[{"rule":{"family":"inet","table":"maestro_xray_cdn","chain":"input","expr":[{"accept":null}]}},`, 1)
	if _, err := ParseNFTSourceFirewall([]byte(unsafeGeneralAccept)); err == nil {
		t.Fatal("source firewall with a general accept rule accepted")
	}
	jumpToAccept := strings.Replace(string(raw), `{"nftables":[`, `{"nftables":[{"chain":{"family":"inet","table":"maestro_xray_cdn","name":"bypass"}},{"rule":{"family":"inet","table":"maestro_xray_cdn","chain":"bypass","expr":[{"accept":null}]}},{"rule":{"family":"inet","table":"maestro_xray_cdn","chain":"input","expr":[{"jump":{"target":"bypass"}}]}},`, 1)
	earlyReturn := strings.Replace(string(raw), `{"nftables":[`, `{"nftables":[{"rule":{"family":"inet","table":"maestro_xray_cdn","chain":"input","expr":[{"return":null}]}},`, 1)
	verdictMapAccept := strings.Replace(string(raw), `{"rule":{"family":"inet","table":"maestro_xray_cdn","chain":"input","expr":[`, `{"rule":{"family":"inet","table":"maestro_xray_cdn","chain":"input","expr":[{"vmap":{"key":{"payload":{"protocol":"tcp","field":"dport"}},"data":{"set":[[18084,{"accept":null}]]}}}]}},{"rule":{"family":"inet","table":"maestro_xray_cdn","chain":"input","expr":[`, 1)
	for name, unsafe := range map[string]string{
		"unhooked chain":           strings.Replace(string(raw), `"hook":"input"`, `"hook":"output"`, 1),
		"controller port exposed":  strings.Replace(string(raw), `{"drop":null}`, `{"accept":null}`, 2),
		"jump to auxiliary accept": jumpToAccept,
		"early return":             earlyReturn,
		"verdict map accept":       verdictMapAccept,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseNFTSourceFirewall([]byte(unsafe)); err == nil {
				t.Fatal("unsafe firewall accepted")
			}
		})
	}
}

func TestProbeRelayAuthenticatesVLESSAndValidatesTLSIdentityAndALPN(t *testing.T) {
	const serverName = "exit-s1.example.test"
	const acceptedCredential = "00000000-0000-4000-8000-000000000031"
	for _, test := range []struct {
		name       string
		serverName string
		credential string
		serverALPN string
		wantError  bool
	}{
		{name: "exact", serverName: serverName, credential: acceptedCredential, serverALPN: "h2"},
		{name: "wrong SNI", serverName: "wrong.example.test", credential: acceptedCredential, serverALPN: "h2", wantError: true},
		{name: "wrong credential", serverName: serverName, credential: "00000000-0000-4000-8000-000000000032", serverALPN: "h2", wantError: true},
		{name: "wrong ALPN", serverName: serverName, credential: acceptedCredential, serverALPN: "http/1.1", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			address, caPEM, closeServer := startRelayProbeServer(t, serverName, test.serverALPN, acceptedCredential)
			defer closeServer()
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				t.Fatalf("SplitHostPort: %v", err)
			}
			caFile := filepath.Join(t.TempDir(), "relay-ca.crt")
			credentialFile := filepath.Join(t.TempDir(), "relay.credential")
			if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
				t.Fatalf("write CA: %v", err)
			}
			if err := os.WriteFile(credentialFile, []byte(test.credential+"\n"), 0o600); err != nil {
				t.Fatalf("write credential: %v", err)
			}
			route := Route{
				ExitID: "exit-s1", Address: host, Port: mustPort(t, port), ServerName: test.serverName,
				CAFile: caFile, CredentialFile: credentialFile,
			}
			err = probeRelay(context.Background(), route)
			if (err != nil) != test.wantError {
				t.Fatalf("probeRelay error = %v, wantError=%v", err, test.wantError)
			}
		})
	}
}

func startRelayProbeServer(t *testing.T, serverName, alpn, credential string) (string, []byte, func()) {
	t.Helper()
	certificate, caPEM := relayTestCertificate(t, serverName)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, NextProtos: []string{alpn},
	})
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	account, err := (&vless.Account{Id: credential, Encryption: "none"}).AsAccount()
	if err != nil {
		listener.Close()
		t.Fatalf("VLESS account: %v", err)
	}
	validator := new(vless.MemoryValidator)
	if err := validator.Add(&protocol.MemoryUser{Email: "relay:exit-s1", Account: account}); err != nil {
		listener.Close()
		t.Fatalf("validator.Add: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
		_, request, _, _, decodeErr := vlessencoding.DecodeRequestHeader(false, nil, connection, validator)
		if decodeErr != nil || request.Address.String() != "127.0.0.1" || request.Port != 18444 {
			return
		}
		if encodeErr := vlessencoding.EncodeResponseHeader(connection, request, &vlessencoding.Addons{}); encodeErr != nil {
			return
		}
		_, _ = io.WriteString(connection, "HTTP/1.1 204 No Content\r\nX-Maestro-Relay-Health: exact\r\nContent-Length: 0\r\n\r\n")
	}()
	return listener.Addr().String(), caPEM, func() {
		_ = listener.Close()
		<-done
	}
}

func TestRelayTestCertificateValidAtRuntimeClock(t *testing.T) {
	const serverName = "relay-runtime.example.test"
	certificate, caPEM := relayTestCertificate(t, serverName)
	if len(certificate.Certificate) != 1 {
		t.Fatalf("certificate chain length = %d, want 1", len(certificate.Certificate))
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate leaf: %v", err)
	}
	caBlock, remainder := pem.Decode(caPEM)
	if caBlock == nil || caBlock.Type != "CERTIFICATE" || len(remainder) != 0 {
		t.Fatal("decode CA certificate")
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate CA: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{
		DNSName:     serverName,
		Roots:       roots,
		CurrentTime: time.Now().UTC(),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("verify fixture at runtime clock: %v", err)
	}
}

func relayTestCertificate(t *testing.T, serverName string) (tls.Certificate, []byte) {
	t.Helper()
	now := time.Now().UTC()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey CA: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "relay-test-ca"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate CA: %v", err)
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey server: %v", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: serverName}, DNSNames: []string{serverName},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate server: %v", err)
	}
	serverKeyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: serverKeyDER}),
	)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	return certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}

func mustPort(t *testing.T, value string) int {
	t.Helper()
	var port int
	if _, err := fmt.Sscanf(value, "%d", &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return port
}
