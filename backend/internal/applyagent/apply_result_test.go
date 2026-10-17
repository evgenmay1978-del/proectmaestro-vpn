package applyagent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentReturnsPerEntryAppliedResult(t *testing.T) {
	agent, command, privateKey := agentFixture(t, &fakeLeaseVerifier{}, &fakeDriver{}, &fakeStateStore{})
	result, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey))
	if err != nil {
		t.Fatal(err)
	}
	assertAppliedResult(t, command, result)
}

func TestHTTPApplyReturnsPerEntryAppliedResult(t *testing.T) {
	agent, command, privateKey := agentFixture(t, &fakeLeaseVerifier{}, &fakeDriver{}, &fakeStateStore{})
	handler, err := NewHTTPHandler(HTTPConfig{
		Agent:         agent,
		DispatcherSAN: "controlplane-dispatcher",
		Ready:         func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(signedFixture(t, command, privateKey))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/apply", strings.NewReader(string(payload)))
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{DNSNames: []string{"controlplane-dispatcher"}}}}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", response.Code, response.Body.String())
	}
	if body:=response.Body.String();!strings.Contains(body,`"snapshot_sha256"`)||strings.Contains(body,`"SnapshotSHA256"`){
		t.Fatalf("non-canonical JSON response: %s",body)
	}
	var result DispatchResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	assertAppliedResult(t, command, result)
}

func assertAppliedResult(t *testing.T, command ApplyCommand, result DispatchResult) {
	t.Helper()
	if result.SnapshotSHA256 != command.Snapshot.SnapshotSHA256 || len(result.Entries) != len(command.Snapshot.Entries) {
		t.Fatalf("result=%#v", result)
	}
	for index, entry := range command.Snapshot.Entries {
		applied := result.Entries[index]
		if applied.CustomerID != entry.CustomerID ||
			applied.OperationID != entry.OperationID ||
			applied.Generation != entry.Generation ||
			applied.DesiredSHA256 != entry.PayloadSHA256 ||
			applied.ObservedSHA256 == "" {
			t.Fatalf("entry[%d]=%#v want %#v", index, applied, entry)
		}
	}
}
