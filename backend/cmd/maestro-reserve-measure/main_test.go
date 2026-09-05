package main

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
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
)

func testConfig(now time.Time) config {
	c := config{SchemaVersion: 1, EntitlementID: "isolated-measure-test", WorkloadDescription: "controlled parallel canary workload",
		FleetDescription: "two isolated active Origins", ValidityReason: "unchanged scoped canary configuration",
		SampleCount: 1000, IntervalMillis: 1000, RequestTimeoutMillis: 200, MaxClockSkewMillis: 10,
		ValidUntilUnix: now.Add(time.Hour).Unix(), Exits: []exitConfig{{"exit-a", []string{"origin-a", "origin-b"}}}}
	for _, id := range []string{"origin-a", "origin-b"} {
		c.Origins = append(c.Origins, originConfig{Expected: binding{
			ActionKey: id + ":1:" + strings.Repeat("a", 64), OriginID: id, ReleaseID: "isolated-release",
			XrayProcessBootID: strings.Repeat("b", 64), ConfigDigest: strings.Repeat("c", 64),
			DesiredGeneration: 1, ManagedUserSetDigest: strings.Repeat("d", 64)}})
	}
	return c
}

func testRound(c config, index int, at time.Time, up, down uint64) round {
	r := round{Index: index, ScheduledAt: at}
	for _, origin := range c.Origins {
		r.Origins = append(r.Origins, observation{Binding: origin.Expected, StartedAt: at,
			FinishedAt: at.Add(100 * time.Millisecond), SampledAt: at.Add(50 * time.Millisecond),
			Counters: map[string]counters{"exit-a": {up, down}}})
	}
	return r
}

func TestAccountAggregateUsesBothDirectionsAndEveryOrigin(t *testing.T) {
	start := time.Unix(2000000, 0)
	c := testConfig(start)
	before := testRound(c, 0, start, 1000, 2000)
	after := testRound(c, 1, start.Add(time.Second), 1100, 2300)
	if err := reduce(c, before, &after); err != nil {
		t.Fatal(err)
	}
	// Two Origins * (100 UP + 300 DOWN), divided by a conservative .9s.
	if after.Rates["exit-a"] != 889 || after.Origins[0].MinimumElapsedNS != 900000000 {
		t.Fatalf("unexpected account aggregate: %+v", after.Rates)
	}
}

func TestRejectsDroppedResetAndChangedBindingWindows(t *testing.T) {
	start := time.Unix(2000000, 0)
	c := testConfig(start)
	tests := map[string]func(*round){
		"missing-origin":     func(r *round) { r.Origins = r.Origins[:1] },
		"missing-exit":       func(r *round) { delete(r.Origins[0].Counters, "exit-a") },
		"decreased-uplink":   func(r *round) { r.Origins[0].Counters["exit-a"] = counters{999, 2300} },
		"decreased-downlink": func(r *round) { r.Origins[0].Counters["exit-a"] = counters{1100, 1999} },
		"boot":               func(r *round) { r.Origins[0].Binding.XrayProcessBootID = strings.Repeat("e", 64) },
		"config":             func(r *round) { r.Origins[0].Binding.ConfigDigest = strings.Repeat("e", 64) },
		"generation":         func(r *round) { r.Origins[0].Binding.DesiredGeneration++ },
		"fleet":              func(r *round) { r.Origins[0].Binding.ManagedUserSetDigest = strings.Repeat("e", 64) },
		"clock":              func(r *round) { r.Origins[0].SampledAt = start },
		"dropped-window":     func(r *round) { r.Index++ },
		"unequal-window":     func(r *round) { r.ScheduledAt = r.ScheduledAt.Add(time.Millisecond) },
		"overlap":            func(r *round) { r.Origins[0].StartedAt = start },
		"overflow":           func(r *round) { r.Origins[0].Counters["exit-a"] = counters{^uint64(0), ^uint64(0)} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			before := testRound(c, 0, start, 1000, 2000)
			after := testRound(c, 1, start.Add(time.Second), 1100, 2300)
			mutate(&after)
			if reduce(c, before, &after) == nil {
				t.Fatal("invalid series accepted")
			}
		})
	}
}

func TestRateCeilingAndOverflow(t *testing.T) {
	for _, input := range []struct {
		delta uint64
		ns    int64
		want  uint64
		fail  bool
	}{
		{1, 3, 333333334, false}, {0, 1, 0, false}, {10, 0, 0, true},
		{^uint64(0), 1, 0, true}, {maximumRate + 1, int64(time.Second), 0, true},
	} {
		got, err := ceilRate(input.delta, input.ns)
		if (err != nil) != input.fail || (!input.fail && got != input.want) {
			t.Fatalf("ceilRate mismatch: got %d, err %v", got, err)
		}
	}
}

func TestNearestRankAndNoInactiveOrPartialReport(t *testing.T) {
	start := time.Unix(2000000, 0)
	c := testConfig(start)
	baseline := testRound(c, 0, start, 0, 0)
	final := testRound(c, 1000, start.Add(1000*time.Second), 1, 1)
	values := make([]uint64, 1000)
	for i := range values {
		values[i] = uint64(i + 1)
	}
	rates := map[string][]uint64{"exit-a": values}
	result, err := buildReport(c, baseline, final, rates, final.ScheduledAt)
	if err != nil || len(result.Measurements) != 1 || result.Measurements[0].Rate != 999 {
		t.Fatalf("nearest rank mismatch: %+v, %v", result, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), `"measured_p999_bytes_per_second":999`) || result.Unit != "BYTES_PER_SECOND" || result.Basis != "UPLINK_PLUS_DOWNLINK" {
		t.Fatal("report contract drift")
	}
	rates["exit-a"] = values[:999]
	if _, err := buildReport(c, baseline, final, rates, final.ScheduledAt); err == nil {
		t.Fatal("partial series accepted")
	}
	rates["exit-a"] = make([]uint64, 1000)
	if _, err := buildReport(c, baseline, final, rates, final.ScheduledAt); err == nil {
		t.Fatal("zero traffic accepted")
	}
	rates["exit-a"] = values
	final.Origins[1].Counters["exit-a"] = counters{}
	if _, err := buildReport(c, baseline, final, rates, final.ScheduledAt); err == nil {
		t.Fatal("inactive Origin accepted")
	}
}

func TestConfigurationHasExplicitIsolatedScopeAndBounds(t *testing.T) {
	now := time.Now()
	if err := testConfig(now).validate(now); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*config){
		"ordinary-account": func(c *config) { c.EntitlementID = "customer-1" },
		"sample-count":     func(c *config) { c.SampleCount = 999 },
		"timeout":          func(c *config) { c.RequestTimeoutMillis = 900 },
		"expiry":           func(c *config) { c.ValidUntilUnix = now.Unix() },
		"duplicate-origin": func(c *config) { c.Origins[1] = c.Origins[0] },
		"unknown-origin":   func(c *config) { c.Exits[0].OriginIDs[0] = "unknown" },
		"unmapped-origin":  func(c *config) { c.Exits[0].OriginIDs = c.Exits[0].OriginIDs[:1] },
		"description":      func(c *config) { c.WorkloadDescription = "" },
	} {
		t.Run(name, func(t *testing.T) {
			c := testConfig(now)
			mutate(&c)
			if c.validate(now) == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

// Exercise the real exported mTLS client and HTTP decoder, not a substitute
// provider. The server is a local authenticated fixture and creates no traffic.
func TestRealMTLSLookupPreservesBindingAndMissingCounterFailure(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "measurement.test"},
		DNSNames: []string{"measurement.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	directory := t.TempDir()
	certPath, keyPath := filepath.Join(directory, "test.crt"), filepath.Join(directory, "test.key")
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	c := testConfig(time.Now())
	c.Origins = c.Origins[:1]
	c.Exits[0].OriginIDs = []string{"origin-a"}
	email := "wl:" + c.EntitlementID + ":exit-a"
	// SHA256 of the exact canonical complete managed set, including the account.
	usersJSON, _ := json.Marshal([]string{email})
	usersHash := sha256Sum(usersJSON)
	c.Origins[0].Expected.ManagedUserSetDigest = usersHash
	var missing atomic.Bool
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != sidecaragentclient.UsagePath ||
			r.Header.Get(sidecaragentclient.ActionKeyHeader) != c.Origins[0].Expected.ActionKey || len(r.TLS.PeerCertificates) != 1 {
			t.Error("unexpected request or missing mTLS identity")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		b, now := c.Origins[0].Expected, time.Now()
		snapshot := sidecaragentclient.UsageSnapshot{Receipt: sidecaragentclient.Receipt{
			ActionKey: b.ActionKey, OriginID: b.OriginID, ReleaseID: b.ReleaseID, XrayProcessBootID: b.XrayProcessBootID,
			ConfigDigest: b.ConfigDigest, DesiredGeneration: b.DesiredGeneration, ManagedUserSetDigest: b.ManagedUserSetDigest,
			AppliedAt: now.Add(-time.Second), ExpiresAt: now.Add(30 * time.Second)}, SampledAt: now,
			Users: []sidecaragentclient.UsageUser{{Email: email, UplinkBytes: 7, DownlinkBytes: 11}}, UnavailableUsers: []string{}}
		if missing.Load() {
			snapshot.Users = []sidecaragentclient.UsageUser{}
			snapshot.UnavailableUsers = []string{email}
		}
		if err := json.NewEncoder(w).Encode(snapshot); err != nil {
			t.Error("fixture response failed")
		}
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	client, err := sidecaragentclient.New(sidecaragentclient.Config{BaseURL: server.URL, ServerName: "measurement.test",
		CAFile: certPath, CertFile: certPath, KeyFile: keyPath, RequestTimeout: c.timeout(), ReceiptLookupTimeout: c.timeout()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := sampleRound(context.Background(), c, []*sidecaragentclient.Client{client}, 0, time.Now())
	if err != nil || result.Origins[0].Counters["exit-a"] != (counters{7, 11}) {
		t.Fatalf("real lookup failed: %v", err)
	}
	missing.Store(true)
	if _, err := sampleRound(context.Background(), c, []*sidecaragentclient.Client{client}, 1, time.Now()); err == nil {
		t.Fatal("unavailable pair became zero")
	}
}

func sha256Sum(data []byte) string {
	// Kept local to the fixture; source report digests use the same standard hash.
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
