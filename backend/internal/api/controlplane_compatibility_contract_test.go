package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlPlaneOTAUsesFrozenAPKByteRangeAndPathContract(t *testing.T) {
	updateDir := t.TempDir()
	apkName := "1.0.157.apk"
	apk := []byte("0123456789abcdef")
	if err := os.WriteFile(filepath.Join(updateDir, apkName), apk, 0o600); err != nil {
		t.Fatal(err)
	}
	business := &compatibilityBusiness{dispatchBusiness: &dispatchBusiness{}}
	server := NewControlPlane(business, Config{UpdateDir: updateDir})
	handler := server.Handler()

	manifestResponse := httptest.NewRecorder()
	handler.ServeHTTP(manifestResponse, httptest.NewRequest(http.MethodGet, "/update/update.json", nil))
	if manifestResponse.Code != http.StatusOK {
		t.Fatalf("manifest status=%d", manifestResponse.Code)
	}
	var manifest OTAManifestView
	if err := json.Unmarshal(manifestResponse.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.URL != "/update/"+apkName {
		t.Fatalf("manifest URL=%q", manifest.URL)
	}

	apkRequest := httptest.NewRequest(http.MethodGet, manifest.URL, nil)
	apkRequest.Header.Set("Range", "bytes=3-7")
	apkResponse := httptest.NewRecorder()
	handler.ServeHTTP(apkResponse, apkRequest)
	if apkResponse.Code != http.StatusPartialContent {
		t.Fatalf("APK range status=%d body=%q", apkResponse.Code, apkResponse.Body.String())
	}
	if got := apkResponse.Body.Bytes(); !bytes.Equal(got, apk[3:8]) {
		t.Fatalf("APK range bytes=%q want=%q", got, apk[3:8])
	}
	if got := apkResponse.Header().Get("Content-Type"); got != "application/vnd.android.package-archive" {
		t.Fatalf("APK content type=%q", got)
	}
	if got := apkResponse.Header().Get("Content-Range"); got != "bytes 3-7/16" {
		t.Fatalf("APK content range=%q", got)
	}

	traversalRequest := httptest.NewRequest(http.MethodGet, "/update/%2e%2e/escape.apk", nil)
	traversalResponse := httptest.NewRecorder()
	server.handleControlPlaneOTA(traversalResponse, traversalRequest)
	if traversalResponse.Code != http.StatusNotFound {
		t.Fatalf("traversal status=%d", traversalResponse.Code)
	}
}

func TestControlPlaneBackfillAndMigrationPreserveFrozenBodies(t *testing.T) {
	tests := []struct {
		name, path, body, service string
		wantLogins                []string
		migration                 bool
	}{
		{name: "anytls empty", path: "/admin/backfill-anytls", service: "anytls"},
		{name: "s3 empty", path: "/admin/backfill-s3", service: "s3"},
		{name: "s4 empty", path: "/admin/backfill-s4", service: "s4"},
		{name: "s4 canary", path: "/admin/backfill-s4", body: `{"logins":["alice","bob"]}`, service: "s4", wantLogins: []string{"alice", "bob"}},
		{name: "migration empty", path: "/admin/migrate-anytls-s2", migration: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			business := &compatibilityBusiness{dispatchBusiness: &dispatchBusiness{}}
			handler := NewControlPlane(business, Config{AdminToken: "admin-secret"}).Handler()
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer admin-secret")
			request.Header.Set("Idempotency-Key", "compatibility-"+strings.ReplaceAll(test.name, " ", "-"))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if test.migration {
				if len(business.migrations) != 1 || business.migrations[0].Endpoint != "" {
					t.Fatalf("migration commands=%#v", business.migrations)
				}
				return
			}
			if len(business.reconciles) != 1 || business.reconciles[0].Service != test.service {
				t.Fatalf("reconcile commands=%#v", business.reconciles)
			}
			raw, err := json.Marshal(business.reconciles[0])
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			got := stringSliceField(fields["Logins"])
			if strings.Join(got, ",") != strings.Join(test.wantLogins, ",") {
				t.Fatalf("canary logins=%v want=%v command=%s", got, test.wantLogins, raw)
			}
		})
	}

	for _, test := range []struct{ name, path, body string }{
		{name: "anytls unknown field", path: "/admin/backfill-anytls", body: `{"login":"alice"}`},
		{name: "s4 malformed", path: "/admin/backfill-s4", body: `{"logins":`},
		{name: "s4 trailing document", path: "/admin/backfill-s4", body: `{"logins":[]} {}`},
		{name: "migration unknown field", path: "/admin/migrate-anytls-s2", body: `{"unexpected":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			business := &compatibilityBusiness{dispatchBusiness: &dispatchBusiness{}}
			handler := NewControlPlane(business, Config{AdminToken: "admin-secret"}).Handler()
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer admin-secret")
			request.Header.Set("Idempotency-Key", "invalid-"+strings.ReplaceAll(test.name, " ", "-"))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if len(business.reconciles) != 0 || len(business.migrations) != 0 {
				t.Fatal("malformed compatibility body reached Business")
			}
		})
	}
}

func TestControlPlaneReportValidatesAndPersistsUnderReportDir(t *testing.T) {
	reportDir := filepath.Join(t.TempDir(), "reports")
	handler := NewControlPlane(&compatibilityBusiness{dispatchBusiness: &dispatchBusiness{}}, Config{ReportDir: reportDir}).Handler()

	valid := `{"kind":"error","v":"1.0.157","vc":157,"device":"fixture","api":35,"id":"../../escape","msg":"hello\nworld","stack":"trace"}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/report", strings.NewReader(valid)))
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid report status=%d body=%q", response.Code, response.Body.String())
	}
	entries, err := os.ReadDir(reportDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].IsDir() || !strings.HasPrefix(entries[0].Name(), "reports-") || !strings.HasSuffix(entries[0].Name(), ".jsonl") {
		t.Fatalf("report files=%v", entries)
	}
	raw, err := os.ReadFile(filepath.Join(reportDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("report lines=%d", len(lines))
	}
	var record map[string]any
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatal(err)
	}
	if record["id"] != "../../escape" || record["msg"] != "hello world" {
		t.Fatalf("sanitized report=%#v", record)
	}

	for _, test := range []struct {
		name, method, body string
		want               int
	}{
		{name: "method", method: http.MethodGet, want: http.StatusMethodNotAllowed},
		{name: "malformed", method: http.MethodPost, body: "{", want: http.StatusBadRequest},
		{name: "oversize", method: http.MethodPost, body: `{"stack":"` + strings.Repeat("x", 65<<10) + `"}`, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, err := os.ReadFile(filepath.Join(reportDir, entries[0].Name()))
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, "/report", strings.NewReader(test.body)))
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d", response.Code, test.want)
			}
			after, err := os.ReadFile(filepath.Join(reportDir, entries[0].Name()))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("rejected report changed the append log")
			}
		})
	}
}

type compatibilityBusiness struct {
	*dispatchBusiness
	reconciles []ReconcileServicesCommand
	migrations []MigrateEndpointCommand
}

func (business *compatibilityBusiness) ApprovedOTA(context.Context) (OTAManifestView, error) {
	business.called("approved_ota")
	return OTAManifestView{Version: "1.0.157", URL: "/update/1.0.157.apk", SHA256: "fixture-sha"}, nil
}

func (business *compatibilityBusiness) ReconcileServices(_ context.Context, command ReconcileServicesCommand) (OperationView, error) {
	business.reconciles = append(business.reconciles, command)
	return OperationView{ID: command.IdempotencyKey, State: "accepted"}, nil
}

func (business *compatibilityBusiness) MigrateServiceEndpoint(_ context.Context, command MigrateEndpointCommand) (OperationView, error) {
	business.migrations = append(business.migrations, command)
	return OperationView{ID: command.IdempotencyKey, State: "accepted"}, nil
}

func stringSliceField(value any) []string {
	values, _ := value.([]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, _ := value.(string)
		result = append(result, text)
	}
	return result
}
