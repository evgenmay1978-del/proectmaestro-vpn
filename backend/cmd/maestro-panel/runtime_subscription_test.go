package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func TestRQLiteRuntimeSubscriptionUsesFrozenEnvironmentTopology(t *testing.T) {
	for key, value := range map[string]string{
		"VLESS_SERVER": "s1.runtime.test", "VLESS_PORT": "1443", "VLESS_SNI": "s1-tls.runtime.test", "VLESS_PBK": "s1-public", "VLESS_SID": "1111", "VLESS_FLOW": "fixture-flow", "VLESS_FP": "firefox",
		"HY2_SERVER": "hy2.runtime.test", "S2_HY2_PORT": "2443", "HY2_SNI": "hy2-tls.runtime.test", "HY2_INSECURE": "0",
		"NAIVE_SERVER": "naive.runtime.test", "NAIVE_PORT": "3443", "NAIVE_SNI": "naive-tls.runtime.test",
		"ANYTLS_SERVER": "anytls.runtime.test", "ANYTLS_PORT": "4443", "ANYTLS_SNI": "anytls-tls.runtime.test", "ANYTLS_INSECURE": "0",
		"S3_XUI_BASE_URL": "https://panel3.runtime.test", "S3_VLESS_SERVER": "s3.runtime.test", "S3_VLESS_PORT": "5443", "S3_VLESS_SNI": "s3-tls.runtime.test", "S3_VLESS_PBK": "s3-public", "S3_VLESS_SID": "3333", "S3_VLESS_FLOW": "s3-flow", "S3_VLESS_FP": "safari",
		"S4_XUI_BASE_URL": "https://panel4.runtime.test", "S4_VLESS_SERVER": "s4.runtime.test", "S4_VLESS_PORT": "6443", "S4_VLESS_SNI": "s4-tls.runtime.test", "S4_VLESS_PBK": "s4-public", "S4_VLESS_SID": "4444", "S4_VLESS_FLOW": "s4-flow", "S4_VLESS_FP": "edge",
	} {
		t.Setenv(key, value)
	}
	for _, tc := range []struct {
		name   string
		absent []string
	}{
		{name: "configured"},
		{name: "optional_servers_absent", absent: []string{"NAIVE_SERVER", "ANYTLS_SERVER", "S3_VLESS_SERVER", "S4_VLESS_SERVER"}},
		{name: "node_enablement_absent", absent: []string{"S3_XUI_BASE_URL", "S4_XUI_BASE_URL"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range tc.absent {
				t.Setenv(key, "")
			}
			box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x53}, 32)}, bytes.Repeat([]byte{0x64}, 32))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().Truncate(time.Second)
			database := &runtimeSubscriptionDatabase{runtimeFakeRQLite: &runtimeFakeRQLite{}, t: t, box: box, now: now}
			runtime, err := buildRQLitePanelRuntime(context.Background(), completeRuntimeConfig(), rqliteAPIConfigFromEnvironment(), rqliteRuntimeDependencies{
				newClient:       func(rqlite.Config) (rqlite.RQLite, error) { return database, nil },
				loadSecretBox:   func(string) (*controlplane.SecretBox, error) { return box, nil },
				applyMigrations: func(context.Context, rqlite.RQLite) error { return nil },
				ids:             runtimeTestIDs{}, clock: runtimeTestClock{now: now},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.business.CustomerByToken(context.Background(), "runtime-fixture-token"); err != nil {
				t.Fatal("canonical fixture access failed before subscription rendering")
			}
			request := httptest.NewRequest(http.MethodGet, "/sub/runtime-fixture-token?platform=tv", nil)
			request.Header.Set("User-Agent", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
			response := httptest.NewRecorder()
			runtime.handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("subscription status=%d", response.Code)
			}
			want := subgen.Customer{
				Name: "runtime-fixture-login", DNSFakeIP: true,
				VLESS:  &subgen.VLESSCreds{Server: "s1.runtime.test", Port: 1443, UUID: "runtime-vless", SNI: "s1-tls.runtime.test", PublicKey: "s1-public", ShortID: "1111", Flow: "fixture-flow", Fingerprint: "firefox"},
				Hy2:    &subgen.Hy2Creds{Server: "hy2.runtime.test", Port: 2443, User: "runtime-fixture-login", Pass: "runtime-hy2", SNI: "hy2-tls.runtime.test"},
				Naive:  &subgen.NaiveCreds{Server: "naive.runtime.test", Port: 3443, Username: "runtime-fixture-login", Password: "runtime-naive", SNI: "naive-tls.runtime.test"},
				AnyTLS: &subgen.AnyTLSCreds{Server: "anytls.runtime.test", Port: 4443, Password: "runtime-anytls", SNI: "anytls-tls.runtime.test"},
				VLESS3: &subgen.VLESSCreds{Server: "s3.runtime.test", Port: 5443, UUID: "runtime-vless", SNI: "s3-tls.runtime.test", PublicKey: "s3-public", ShortID: "3333", Flow: "s3-flow", Fingerprint: "safari"},
				VLESS4: &subgen.VLESSCreds{Server: "s4.runtime.test", Port: 6443, UUID: "runtime-vless", SNI: "s4-tls.runtime.test", PublicKey: "s4-public", ShortID: "4444", Flow: "s4-flow", Fingerprint: "edge"},
			}
			if tc.name == "optional_servers_absent" {
				want.Naive, want.AnyTLS = nil, nil
			}
			if tc.name != "configured" {
				want.VLESS3, want.VLESS4 = nil, nil
			}
			wantDocument, err := subgen.GenerateSingbox(want)
			if err != nil {
				t.Fatal(err)
			}
			if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("subscription headers differ from frozen contract")
			}
			if !bytes.Equal(response.Body.Bytes(), wantDocument) {
				t.Error("runtime subscription omitted or changed frozen environment topology")
			}
		})
	}
}

// All service/crypto/rendering code is real; this double replaces only the
// remote SQL read boundary and rejects every mutation through runtimeFakeRQLite.
type runtimeSubscriptionDatabase struct {
	*runtimeFakeRQLite
	t   *testing.T
	box *controlplane.SecretBox
	now time.Time
}

func (database *runtimeSubscriptionDatabase) QueryLinearizable(_ context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	if len(statements) != 1 {
		return nil, fmt.Errorf("unexpected fixture query batch")
	}
	statement := statements[0]
	switch {
	case strings.Contains(statement.SQL, "WHERE st.token_hmac"):
		want := database.box.LookupHMAC("subscription-token", []byte("runtime-fixture-token"))
		if len(statement.Args) != 1 || statement.Args[0] != want {
			return nil, fmt.Errorf("unexpected token lookup")
		}
		return []rqlite.Result{{Rows: []map[string]any{{"customer_id": "runtime-customer", "status": "active", "expires_at_unix": database.now.Add(24 * time.Hour).Unix(), "generation": 7}}}}, nil
	case strings.Contains(statement.SQL, "SELECT display_login"):
		if len(statement.Args) != 1 || statement.Args[0] != "runtime-customer" {
			return nil, fmt.Errorf("unexpected login lookup")
		}
		return []rqlite.Result{{Rows: []map[string]any{{"display_login": "runtime-fixture-login"}}}}, nil
	case strings.Contains(statement.SQL, "SELECT st.token_envelope"):
		if len(statement.Args) != 1 || statement.Args[0] != "runtime-customer" {
			return nil, fmt.Errorf("unexpected access lookup")
		}
		var rows []map[string]any
		for _, credential := range []struct{ protocol, raw string }{{"anytls", "runtime-anytls"}, {"hysteria2", "runtime-hy2"}, {"naive", "runtime-naive"}, {"vless", "runtime-vless"}} {
			rows = append(rows, map[string]any{"protocol": credential.protocol, "token_envelope": database.seal("token", "subscription", "runtime-fixture-token"), "secret_envelope": database.seal("credential", credential.protocol, credential.raw)})
		}
		return []rqlite.Result{{Rows: rows}}, nil
	default:
		return nil, fmt.Errorf("unexpected fixture query")
	}
}

func (database *runtimeSubscriptionDatabase) seal(field, kind, raw string) string {
	database.t.Helper()
	envelope, err := database.box.Seal(controlplane.SecretScope{OwnerType: "customer", OwnerID: "runtime-customer", Field: field, Kind: kind}, []byte(raw))
	if err != nil {
		database.t.Fatal(err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		database.t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}
