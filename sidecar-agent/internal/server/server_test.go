package server

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
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/agent"
)

type fakeApplier struct {
	called int
}

func (fake *fakeApplier) Apply(_ context.Context, desired agent.Desired) (agent.Receipt, error) {
	fake.called++
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	return agent.Receipt{
		ActionKey: desired.ActionKey(), OriginID: desired.OriginID, ReleaseID: desired.ReleaseID,
		XrayProcessBootID: "boot-a", ConfigDigest: desired.ConfigDigest,
		DesiredGeneration: desired.Generation, ManagedUserSetDigest: desired.ManagedUserSetDigest,
		AppliedAt: now, ExpiresAt: now.Add(30 * time.Second),
	}, nil
}

func canonicalDesired(t *testing.T) []byte {
	t.Helper()
	managed := []string{"wl:one:exit-s1"}
	digest, err := agent.ManagedUserSetDigest(managed)
	if err != nil {
		t.Fatalf("ManagedUserSetDigest: %v", err)
	}
	raw, err := json.Marshal(struct {
		Version              int      `json:"version"`
		OriginID             string   `json:"origin_id"`
		NodeID               string   `json:"node_id"`
		ReleaseID            string   `json:"release_id"`
		ProfileID            string   `json:"profile_id"`
		PresetID             string   `json:"preset_id"`
		ExitID               string   `json:"exit_id"`
		Generation           int64    `json:"generation"`
		ConfigDigest         string   `json:"config_digest"`
		ManagedUserSetDigest string   `json:"managed_user_set_digest"`
		StaticUsers          []string `json:"static_users"`
		ManagedUsers         []string `json:"managed_users"`
	}{
		Version: 1, OriginID: "origin-s1", NodeID: "node-s1", ReleaseID: "release-12",
		ProfileID: "profile-xhttp", PresetID: "preset-packet-up", ExitID: "exit-s1",
		Generation: 1, ConfigDigest: strings.Repeat("a", 64), ManagedUserSetDigest: digest,
		StaticUsers: []string{"canary:fixed"}, ManagedUsers: managed,
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return raw
}

func TestMTLSServerRejectsUntrustedWrongNameExpiredPlaintextAndInvalidBodies(t *testing.T) {
	serverCA := newCertificateAuthority(t, "server-ca")
	clientCA := newCertificateAuthority(t, "client-ca")
	serverCertificate := newLeafCertificate(t, serverCA, "agent.test", false, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	applier := &fakeApplier{}
	listener := httptest.NewUnstartedServer(NewHandler(applier))
	listener.TLS = ServerTLSConfig(serverCertificate, clientCA.pool, "maestro-whitelist-controller")
	listener.StartTLS()
	defer listener.Close()

	validClient := newLeafCertificate(t, clientCA, "maestro-whitelist-controller", true, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	wrongName := newLeafCertificate(t, clientCA, "someone-else", true, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	expired := newLeafCertificate(t, clientCA, "maestro-whitelist-controller", true, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	unknownCA := newCertificateAuthority(t, "unknown-ca")
	unknown := newLeafCertificate(t, unknownCA, "maestro-whitelist-controller", true, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	raw := canonicalDesired(t)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	desired, err := agent.ParseDesired(raw)
	if err != nil {
		t.Fatalf("ParseDesired: %v", err)
	}
	validHeaders := map[string]string{DesiredSHA256Header: digest, ActionKeyHeader: desired.ActionKey()}
	for name, test := range map[string]struct {
		certificate tlsCertificate
		body        []byte
		headers     map[string]string
	}{
		"unknown ca":        {certificate: unknown, body: raw, headers: validHeaders},
		"wrong client name": {certificate: wrongName, body: raw, headers: validHeaders},
		"expired cert":      {certificate: expired, body: raw, headers: validHeaders},
	} {
		t.Run(name, func(t *testing.T) {
			client := authenticatedClient(t, serverCA.pool, test.certificate)
			if response, err := postDesired(client, listener.URL, test.body, test.headers); err == nil {
				response.Body.Close()
				t.Fatalf("TLS request unexpectedly reached HTTP: %s", response.Status)
			}
		})
	}

	client := authenticatedClient(t, serverCA.pool, validClient)
	for name, test := range map[string]struct {
		body    []byte
		headers map[string]string
		status  int
	}{
		"non-canonical json": {body: append(append([]byte(nil), raw...), '\n'), headers: validHeaders, status: http.StatusBadRequest},
		"digest mismatch":    {body: raw, headers: map[string]string{DesiredSHA256Header: strings.Repeat("0", 64), ActionKeyHeader: desired.ActionKey()}, status: http.StatusBadRequest},
		"action mismatch":    {body: raw, headers: map[string]string{DesiredSHA256Header: digest, ActionKeyHeader: "wrong"}, status: http.StatusBadRequest},
		"oversized":          {body: []byte(strings.Repeat("x", MaxRequestBytes+1)), headers: validHeaders, status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			response, err := postDesired(client, listener.URL, test.body, test.headers)
			if err != nil {
				t.Fatalf("postDesired: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
		})
	}

	address := listener.Listener.Addr().String()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("Dial plaintext: %v", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(connection, "GET / HTTP/1.1\r\nHost: agent.test\r\n\r\n"); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	buffer := make([]byte, 16)
	count, _ := connection.Read(buffer)
	if count == 0 || !strings.HasPrefix(string(buffer[:count]), "HTTP/1.0 400") {
		t.Fatalf("plaintext HTTP was not explicitly rejected: %q", buffer[:count])
	}
	if applier.called != 0 {
		t.Fatalf("invalid requests reached applier %d times", applier.called)
	}
}

func TestMTLSServerAcceptsCanonicalDesiredAndReturnsExactReceipt(t *testing.T) {
	serverCA := newCertificateAuthority(t, "server-ca")
	clientCA := newCertificateAuthority(t, "client-ca")
	serverCertificate := newLeafCertificate(t, serverCA, "agent.test", false, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	clientCertificate := newLeafCertificate(t, clientCA, "maestro-whitelist-controller", true, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	applier := &fakeApplier{}
	listener := httptest.NewUnstartedServer(NewHandler(applier))
	listener.TLS = ServerTLSConfig(serverCertificate, clientCA.pool, "maestro-whitelist-controller")
	listener.StartTLS()
	defer listener.Close()

	raw := canonicalDesired(t)
	desired, err := agent.ParseDesired(raw)
	if err != nil {
		t.Fatalf("ParseDesired: %v", err)
	}
	sum := sha256.Sum256(raw)
	response, err := postDesired(authenticatedClient(t, serverCA.pool, clientCertificate), listener.URL, raw, map[string]string{
		DesiredSHA256Header: hex.EncodeToString(sum[:]), ActionKeyHeader: desired.ActionKey(),
	})
	if err != nil {
		t.Fatalf("postDesired: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || applier.called != 1 {
		t.Fatalf("status=%d calls=%d", response.StatusCode, applier.called)
	}
	var receipt agent.Receipt
	if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if receipt.ActionKey != desired.ActionKey() || receipt.DesiredGeneration != 1 {
		t.Fatalf("receipt = %#v", receipt)
	}
}

type certificateAuthority struct {
	certificate *x509.Certificate
	key         *rsa.PrivateKey
	pool        *x509.CertPool
}

type tlsCertificate = tls.Certificate

func newCertificateAuthority(t *testing.T, name string) certificateAuthority {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate CA: %v", err)
	}
	certificate, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatalf("ParseCertificate CA: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	return certificateAuthority{certificate: certificate, key: key, pool: pool}
}

func newLeafCertificate(t *testing.T, ca certificateAuthority, name string, client bool, notBefore, notAfter time.Time) tlsCertificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	usage := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	if client {
		usage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(notAfter.UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usage}
	raw, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("CreateCertificate leaf: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}
}

func authenticatedClient(t *testing.T, roots *x509.CertPool, certificate tlsCertificate) *http.Client {
	t.Helper()
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "agent.test",
		Certificates: []tls.Certificate{certificate},
	}}}
}

func postDesired(client *http.Client, baseURL string, body []byte, headers map[string]string) (*http.Response, error) {
	request, err := http.NewRequest(http.MethodPost, baseURL+DesiredPath, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	return client.Do(request)
}
