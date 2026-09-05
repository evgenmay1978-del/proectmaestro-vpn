package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/agent"
)

type usageApplier struct {
	fakeApplier
	usageKey string
	result   agent.UsageSnapshot
}

func (applier *usageApplier) Usage(_ context.Context, key string) (agent.UsageSnapshot, error) {
	applier.usageKey = key
	return applier.result, nil
}

func TestAuthenticatedUsageReturnsBoundSnapshotAndUnavailableUsers(t *testing.T) {
	serverCA := newCertificateAuthority(t, "server-ca")
	clientCA := newCertificateAuthority(t, "client-ca")
	serverCertificate := newLeafCertificate(t, serverCA, "agent.test", false, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	clientCertificate := newLeafCertificate(t, clientCA, "maestro-whitelist-controller", true, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	desired, err := agent.ParseDesired(canonicalDesired(t))
	if err != nil {
		t.Fatal(err)
	}
	applier := &usageApplier{}
	receipt, err := applier.Apply(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	applier.called = 0
	applier.result = agent.UsageSnapshot{
		Receipt: receipt, SampledAt: receipt.AppliedAt,
		Users:            []agent.UserUsage{{Email: "wl:one:exit-s1", UplinkBytes: 799, DownlinkBytes: 3564}},
		UnavailableUsers: []string{},
	}
	listener := httptest.NewUnstartedServer(NewHandler(applier))
	listener.TLS = ServerTLSConfig(serverCertificate, clientCA.pool, "maestro-whitelist-controller")
	listener.StartTLS()
	defer listener.Close()
	request, err := http.NewRequest(http.MethodGet, listener.URL+"/v1/usage", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(ActionKeyHeader, desired.ActionKey())
	response, err := authenticatedClient(t, serverCA.pool, clientCertificate).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var got agent.UsageSnapshot
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&got) != nil ||
		!reflect.DeepEqual(got, applier.result) || response.Header.Get("Cache-Control") != "no-store" ||
		applier.usageKey != desired.ActionKey() || applier.called != 0 || applier.lookupCalled != 0 {
		t.Fatalf("usage response lost binding/read-only contract: status=%d snapshot=%#v", response.StatusCode, got)
	}
}
