package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func nativePublicationFixture(now time.Time) WhiteListPublicationSnapshot {
	node := subgen.WhiteListNode{
		Protocol: "vless", Network: "xhttp", Address: "cdn.example.invalid", Port: 443,
		TLS: true, ServerName: "cdn.example.invalid", Host: "cdn.example.invalid",
		Path: "/static/main/video/segment.ts/native", Mode: "packet-up", UplinkHTTPMethod: "GET", UplinkDataPlacement: "body",
		ClientID:   "11111111-1111-4111-8111-111111111111",
		Encryption: "mlkem768x25519plus.native.0rtt." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 1184)),
		Security:   "tls", ALPN: []string{"h2"}, Fingerprint: "firefox",
		Extra: url.QueryEscape(`{"sessionIDPlacement":"query","sessionIDKey":"auth","sessionIDLength":16,"seqPlacement":"query","seqKey":"chunk_id","uplinkHTTPMethod":"GET","uplinkDataPlacement":"body"}`),
		Label: "Maestro CDN Netherlands", TransportProfileID: "profile-1", TransportReleaseID: "release-1", CompatibilityPresetID: "preset-1",
	}
	return WhiteListPublicationSnapshot{Verdict: WhiteListPublishable, Nodes: []subgen.WhiteListNode{node},
		ProjectionVersion: 2, DesiredGeneration: 3, FreshThrough: time.Unix(now.Unix()+5, 0)}
}

func nativeErrorStatus(err error) int {
	var typed interface{ HTTPStatus() int }
	if errors.As(err, &typed) {
		return typed.HTTPStatus()
	}
	return 0
}

func TestNativeRuntimeLeaseRoundsDownActualRemainingTime(t *testing.T) {
	now := time.Unix(2000000, 100000000)
	view, err := nativeWhiteListRuntimeView(nativePublicationFixture(now), now)
	if err != nil {
		t.Fatal(err)
	}
	if view.SchemaVersion != 1 || view.IssuedAtUnix != 2000001 || view.FreshUntilUnix != 2000005 ||
		view.ProjectionVersion != 2 || view.DesiredGeneration != 3 || len(view.Profiles) != 1 {
		t.Fatal("native lease or provenance mismatch")
	}
	if time.Duration(view.FreshUntilUnix-view.IssuedAtUnix)*time.Second > nativePublicationFixture(now).FreshThrough.Sub(now) {
		t.Fatal("integer lease extended actual freshness")
	}
}

func TestNativeRuntimeRejectsEveryClosedOrIncompletePublication(t *testing.T) {
	now := time.Unix(2000000, 100000000)
	for verdict, status := range map[WhiteListPublicationVerdict]int{
		WhiteListNoEntitlement: 403, WhiteListNoBalance: 403, WhiteListPrimaryExpired: 403, WhiteListPublicationDisabled: 403,
		WhiteListProjectionStale: 503, WhiteListProjectionPending: 503, WhiteListReleaseMismatch: 503,
		WhiteListSidecarUnavailable: 503, WhiteListPublicationVerdict("UNKNOWN"): 503,
	} {
		t.Run(string(verdict), func(t *testing.T) {
			publication := nativePublicationFixture(now)
			publication.Verdict = verdict
			view, err := nativeWhiteListRuntimeView(publication, now)
			if nativeErrorStatus(err) != status || len(view.Profiles) != 0 {
				t.Fatal("closed verdict leaked profile")
			}
		})
	}
	for name, mutate := range map[string]func(*WhiteListPublicationSnapshot){
		"no-freshness":     func(p *WhiteListPublicationSnapshot) { p.FreshThrough = time.Time{} },
		"expired":          func(p *WhiteListPublicationSnapshot) { p.FreshThrough = now },
		"fractional-only":  func(p *WhiteListPublicationSnapshot) { p.FreshThrough = time.Unix(now.Unix()+1, 0) },
		"overlong":         func(p *WhiteListPublicationSnapshot) { p.FreshThrough = now.Add(6 * time.Second) },
		"no-projection":    func(p *WhiteListPublicationSnapshot) { p.ProjectionVersion = 0 },
		"no-generation":    func(p *WhiteListPublicationSnapshot) { p.DesiredGeneration = 0 },
		"no-nodes":         func(p *WhiteListPublicationSnapshot) { p.Nodes = nil },
		"invalid-material": func(p *WhiteListPublicationSnapshot) { p.Nodes[0].Encryption = "none" },
	} {
		t.Run(name, func(t *testing.T) {
			publication := nativePublicationFixture(now)
			mutate(&publication)
			view, err := nativeWhiteListRuntimeView(publication, now)
			if nativeErrorStatus(err) != 503 || len(view.Profiles) != 0 {
				t.Fatal("incomplete publication leaked profile")
			}
		})
	}
}

type nativeHTTPFixture struct {
	Business
	view  WhiteListNativeRuntimeView
	err   error
	token string
	calls int
}

func (b *nativeHTTPFixture) WhiteListNativeRuntime(_ context.Context, token string) (WhiteListNativeRuntimeView, error) {
	b.token, b.calls = token, b.calls+1
	return b.view, b.err
}

func TestNativeRuntimeHTTPRequiresHeaderIdentityAndNeverCaches(t *testing.T) {
	now := time.Unix(2000000, 0)
	view, err := nativeWhiteListRuntimeView(nativePublicationFixture(now), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, method, query, authorization string
		duplicate                          bool
		status                             int
	}{
		{"allowed", "GET", "", "Bearer native-test-token", false, 200},
		{"trimmed-existing-contract", "GET", "", "Bearer  native-test-token  ", false, 200},
		{"missing", "GET", "", "", false, 401},
		{"blank", "GET", "", "Bearer ", false, 401},
		{"other-scheme", "GET", "", "Basic fixture", false, 401},
		{"duplicate", "GET", "", "Bearer native-test-token", true, 401},
		{"query-token", "GET", "?token=native-test-token", "", false, 401},
		{"query-account", "GET", "?account=other", "Bearer native-test-token", false, 400},
		{"post", "POST", "", "Bearer native-test-token", false, 405},
		{"head", "HEAD", "", "Bearer native-test-token", false, 405},
	} {
		t.Run(test.name, func(t *testing.T) {
			business := &nativeHTTPFixture{view: view}
			handler := NewControlPlane(business, Config{}).Handler()
			request := httptest.NewRequest(test.method, "https://sub.example.invalid/account/whitelist-runtime"+test.query, nil)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			if test.duplicate {
				request.Header.Add("Authorization", "Bearer second-test-token")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("ETag") != "" {
				t.Fatalf("unexpected status/cache: %d", response.Code)
			}
			if test.status == 200 {
				if business.calls != 1 || business.token != "native-test-token" {
					t.Fatal("wrong header account forwarded")
				}
				var actual WhiteListNativeRuntimeView
				if json.Unmarshal(response.Body.Bytes(), &actual) != nil || len(actual.Profiles) != 1 {
					t.Fatal("typed response missing")
				}
			} else if business.calls != 0 || strings.Contains(response.Body.String(), "client_id") {
				t.Fatal("rejected request reached material provider")
			}
		})
	}
}

func TestNativeRuntimeHTTPPreservesUnknownAndUnavailableErrors(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{
		{businessError(controlplane.ErrNotFound), 404}, {businessError(controlplane.ErrForbidden), 403},
		{businessError(controlplane.ErrUnavailable), 503},
	} {
		business := &nativeHTTPFixture{err: test.err}
		request := httptest.NewRequest(http.MethodGet, "/account/whitelist-runtime", nil)
		request.Header.Set("Authorization", "Bearer native-test-token")
		response := httptest.NewRecorder()
		NewControlPlane(business, Config{}).Handler().ServeHTTP(response, request)
		if response.Code != test.status || strings.Contains(response.Body.String(), "profiles") {
			t.Fatal("error contract mismatch")
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/account/whitelist-runtime", nil)
	request.Header.Set("Authorization", "Bearer native-test-token")
	response := httptest.NewRecorder()
	NewControlPlane(&dispatchBusiness{}, Config{}).Handler().ServeHTTP(response, request)
	if response.Code != 503 {
		t.Fatal("optional native business absence did not fail closed")
	}
}
