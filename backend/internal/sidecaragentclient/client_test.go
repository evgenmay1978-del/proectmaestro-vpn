package sidecaragentclient

import (
	"bytes"
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
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientPostsExactActionOverMTLSAndReturnsReceipt(t *testing.T) {
	files, serverTLS := testTLSFiles(t, "agent.test")
	payload, actionKey := testDesired(t)
	receipt := testReceipt(actionKey)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != DesiredPath || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 {
			t.Fatalf("request method=%s path=%s tls=%v", request.Method, request.URL.Path, request.TLS != nil)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		sum := sha256.Sum256(payload)
		if !bytes.Equal(body, payload) || request.Header.Get(ActionKeyHeader) != actionKey || request.Header.Get(DesiredSHA256Header) != hex.EncodeToString(sum[:]) {
			t.Fatalf("request binding mismatch")
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(receipt)
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()

	client, err := New(Config{
		BaseURL: server.URL, ServerName: "agent.test", CAFile: files.ca,
		CertFile: files.cert, KeyFile: files.key, RequestTimeout: time.Second,
		ReceiptLookupTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw, err := client.Post(context.Background(), payload)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if string(raw) != string(mustJSON(t, receipt)) {
		t.Fatalf("receipt=%s", raw)
	}
}

func TestClientRejectsWrongServerHostnameAndMapsStaleGeneration(t *testing.T) {
	files, serverTLS := testTLSFiles(t, "agent.test")
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusConflict)
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	payload, _ := testDesired(t)

	wrong, err := New(Config{
		BaseURL: server.URL, ServerName: "wrong.test", CAFile: files.ca,
		CertFile: files.cert, KeyFile: files.key, RequestTimeout: time.Second,
		ReceiptLookupTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New wrong host: %v", err)
	}
	if _, err := wrong.Post(context.Background(), payload); err == nil || errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("wrong hostname error=%v", err)
	}

	client, err := New(Config{
		BaseURL: server.URL, ServerName: "agent.test", CAFile: files.ca,
		CertFile: files.cert, KeyFile: files.key, RequestTimeout: time.Second,
		ReceiptLookupTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Post(context.Background(), payload); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale generation error=%v", err)
	}
}

func TestClientTimeoutBeforeSendDoesNotAttemptReceiptLookup(t *testing.T) {
	payload, _ := testDesired(t)
	transport := &scriptedTransport{post: func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	}}
	client := newWithHTTPClient("https://agent.test", time.Second, time.Second, &http.Client{Transport: transport})
	if _, err := client.Post(context.Background(), payload); !errors.Is(err, ErrBeforeSend) {
		t.Fatalf("Post error=%v", err)
	}
	if transport.gets.Load() != 0 || transport.posts.Load() != 1 {
		t.Fatalf("posts=%d gets=%d", transport.posts.Load(), transport.gets.Load())
	}
}

func TestClientTimeoutAfterSendLooksUpExactReceiptWithoutBlindResend(t *testing.T) {
	payload, actionKey := testDesired(t)
	receipt := testReceipt(actionKey)
	transport := &scriptedTransport{
		post: func(request *http.Request) (*http.Response, error) {
			if trace := httptrace.ContextClientTrace(request.Context()); trace != nil && trace.WroteRequest != nil {
				trace.WroteRequest(httptrace.WroteRequestInfo{})
			}
			return nil, context.DeadlineExceeded
		},
		get: func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != ReceiptPath || request.Header.Get(ActionKeyHeader) != actionKey {
				t.Fatalf("lookup path=%q key=%q", request.URL.Path, request.Header.Get(ActionKeyHeader))
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(bytes.NewReader(mustJSON(t, receipt))), Request: request,
			}, nil
		},
	}
	client := newWithHTTPClient("https://agent.test", time.Second, time.Second, &http.Client{Transport: transport})
	raw, err := client.Post(context.Background(), payload)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if string(raw) != string(mustJSON(t, receipt)) || transport.posts.Load() != 1 || transport.gets.Load() != 1 {
		t.Fatalf("receipt=%s posts=%d gets=%d", raw, transport.posts.Load(), transport.gets.Load())
	}
}

func TestClientRejectsOversizedDesiredBeforeNetwork(t *testing.T) {
	transport := &scriptedTransport{}
	client := newWithHTTPClient("https://agent.test", time.Second, time.Second, &http.Client{Transport: transport})
	if _, err := client.Post(context.Background(), bytes.Repeat([]byte{'x'}, MaxRequestBytes+1)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Post error=%v", err)
	}
	if transport.posts.Load() != 0 || transport.gets.Load() != 0 {
		t.Fatal("oversized request reached network")
	}
}

func TestClientUnknownPostRecoveryPreservesCallerDeadline(t *testing.T) {
	payload, _ := testDesired(t)
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	transport := &scriptedTransport{
		post: func(request *http.Request) (*http.Response, error) {
			httptrace.ContextClientTrace(request.Context()).WroteRequest(httptrace.WroteRequestInfo{})
			return nil, context.DeadlineExceeded
		},
		get: func(request *http.Request) (*http.Response, error) {
			got, ok := request.Context().Deadline()
			if !ok || !got.Equal(deadline) {
				t.Fatal("unknown POST recovery discarded the caller operation deadline")
			}
			return nil, context.DeadlineExceeded
		},
	}
	client := newWithHTTPClient("https://agent.test", time.Second, 2*time.Minute, &http.Client{Transport: transport})
	if _, err := client.Post(ctx, payload); !errors.Is(err, ErrDeliveryUnknown) {
		t.Fatalf("Post error=%v", err)
	}
	if transport.posts.Load() != 1 || transport.gets.Load() != 1 {
		t.Fatal("recovery must be one receipt read, never a second POST")
	}
}

func TestClientLooksUpTypedUsageOverExistingMTLSBinding(t *testing.T) {
	files, serverTLS := testTLSFiles(t, "agent.test")
	_, actionKey := testDesired(t)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	availableEmail := "wl:wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:exit-s4"
	unavailableEmail := "wl:wl-ent-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:exit-s4"
	want := UsageSnapshot{
		Receipt: Receipt{
			ActionKey: actionKey, OriginID: "origin-s4", ReleaseID: "release-1",
			XrayProcessBootID: "boot-s4", ConfigDigest: strings.Repeat("a", 64),
			DesiredGeneration: 2, ManagedUserSetDigest: testManagedUserSetDigest(t, []string{availableEmail, unavailableEmail}),
			AppliedAt: now.Add(-time.Second), ExpiresAt: now.Add(30 * time.Second),
		},
		SampledAt: now,
		Users: []UsageUser{
			{Email: availableEmail, UplinkBytes: 10, DownlinkBytes: 20},
		},
		UnavailableUsers: []string{unavailableEmail},
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != UsagePath ||
			request.Header.Get(ActionKeyHeader) != actionKey || request.TLS == nil ||
			len(request.TLS.VerifiedChains) == 0 {
			t.Fatalf("usage request method=%s path=%s tls=%v", request.Method, request.URL.Path, request.TLS != nil)
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(want)
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()

	client, err := New(Config{
		BaseURL: server.URL, ServerName: "agent.test", CAFile: files.ca,
		CertFile: files.cert, KeyFile: files.key, RequestTimeout: time.Second,
		ReceiptLookupTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := client.LookupUsage(context.Background(), actionKey)
	if err != nil {
		t.Fatalf("LookupUsage: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usage=%#v, want %#v", got, want)
	}
}

func TestDecodeUsageRejectsMissingCountersAndManagedCoverageDrift(t *testing.T) {
	_, actionKey := testDesired(t)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	availableEmail := "wl:wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:exit-s4"
	otherEmail := "wl:wl-ent-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:exit-s4"
	tests := []struct {
		name         string
		user         map[string]any
		managedUsers []string
	}{
		{
			name:         "missing uplink counter",
			user:         map[string]any{"email": availableEmail, "downlink_bytes": uint64(20)},
			managedUsers: []string{availableEmail},
		},
		{
			name:         "null downlink counter",
			user:         map[string]any{"email": availableEmail, "uplink_bytes": uint64(10), "downlink_bytes": nil},
			managedUsers: []string{availableEmail},
		},
		{
			name:         "managed coverage drift",
			user:         map[string]any{"email": availableEmail, "uplink_bytes": uint64(10), "downlink_bytes": uint64(20)},
			managedUsers: []string{availableEmail, otherEmail},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := testReceipt(actionKey)
			receipt.ManagedUserSetDigest = testManagedUserSetDigest(t, test.managedUsers)
			receipt.AppliedAt = now.Add(-time.Second)
			receipt.ExpiresAt = now.Add(30 * time.Second)
			payload := mustJSON(t, map[string]any{
				"receipt": receipt, "sampled_at": now,
				"users": []map[string]any{test.user}, "unavailable_users": []string{},
			})
			if _, err := decodeUsage(bytes.NewReader(payload), actionKey); !errors.Is(err, ErrDeliveryUnknown) {
				t.Fatalf("decodeUsage error=%v", err)
			}
		})
	}
}

func leaseSnapshotFixture(t *testing.T) UsageSnapshot {
	t.Helper()
	_, action := testDesired(t)
	now := time.Unix(2_000_000, 0).UTC()
	emails := []string{"wl:wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:exit-s1"}
	receipt := testReceipt(action)
	receipt.XrayProcessBootID = strings.Repeat("b", 64)
	receipt.ConfigDigest = strings.Repeat("c", 64)
	receipt.ManagedUserSetDigest = testManagedUserSetDigest(t, emails)
	receipt.AppliedAt = now
	receipt.ExpiresAt = now.Add(30 * time.Second)
	return UsageSnapshot{Receipt: receipt, SampledAt: now, Users: []UsageUser{}, UnavailableUsers: emails,
		LeaseChallenge: &UseLeaseChallenge{Schema: 2, Nonce: strings.Repeat("d", 64), ClockDomain: strings.Repeat("e", 64),
			ReadStartedBoottimeNS: 10_000_000_000, MaxDeadlineBoottimeNS: 15_000_000_000, ManagedUsers: emails}}
}

func TestUseLeaseRequestKeepsOpaqueReadStartAndHonestUnavailableBootstrap(t *testing.T) {
	snapshot := leaseSnapshotFixture(t)
	request, err := NewUseLeaseRequest(snapshot, 2100*time.Millisecond, snapshot.UnavailableUsers)
	if err != nil || request.DeadlineBoottimeNS != 12_100_000_000 || request.ReadStartedBoottimeNS != 10_000_000_000 || request.Nonce != snapshot.LeaseChallenge.Nonce {
		t.Fatal("lease was rebased instead of anchored at agent read start")
	}
	if len(snapshot.Users) != 0 {
		t.Fatal("bootstrap fabricated a real counter")
	}
	for _, budget := range []time.Duration{0, -time.Nanosecond, 5*time.Second + time.Nanosecond} {
		if _, err := NewUseLeaseRequest(snapshot, budget, []string{}); err == nil {
			t.Fatal("unbounded lease budget accepted")
		}
	}
	if _, err := NewUseLeaseRequest(snapshot, time.Second, []string{"wl:other:exit-s1"}); err == nil {
		t.Fatal("foreign email added to challenge")
	}
	for _, change := range []func(*UseLeaseChallenge){func(c *UseLeaseChallenge) { c.MaxDeadlineBoottimeNS++ }, func(c *UseLeaseChallenge) { c.ReadStartedBoottimeNS = 0 }, func(c *UseLeaseChallenge) { c.ManagedUsers = []string{} }, func(c *UseLeaseChallenge) { c.Schema = 1 }} {
		bad := snapshot
		copyChallenge := *snapshot.LeaseChallenge
		bad.LeaseChallenge = &copyChallenge
		change(bad.LeaseChallenge)
		if _, err := decodeUsage(bytes.NewReader(mustJSON(t, bad)), snapshot.Receipt.ActionKey); err == nil {
			t.Fatal("invalid lease challenge accepted")
		}
	}
}

func finalReceiptFixture(t *testing.T) ManagedFinalReceipt {
	t.Helper()
	snapshot := leaseSnapshotFixture(t)
	email := snapshot.UnavailableUsers[0]
	control := ManagedControl{Schema: 2, Operation: "fence", Email: email, BootID: snapshot.Receipt.XrayProcessBootID, ConfigDigest: snapshot.Receipt.ConfigDigest, Generation: 7, ClockDomain: snapshot.LeaseChallenge.ClockDomain}
	up, down := int64(0), int64(19)
	receipt := ManagedReceipt{Schema: 2, State: "fenced", Email: email, BootID: control.BootID, ConfigDigest: control.ConfigDigest, Generation: control.Generation, ObservedAt: snapshot.SampledAt.Format(time.RFC3339Nano), Uplink: &up, Downlink: &down, ClockDomain: control.ClockDomain}
	proof := ManagedFinalReceipt{ActionKey: snapshot.Receipt.ActionKey, OriginID: snapshot.Receipt.OriginID, ReleaseID: snapshot.Receipt.ReleaseID, DesiredGeneration: snapshot.Receipt.DesiredGeneration, ManagedUserSetDigest: snapshot.Receipt.ManagedUserSetDigest, Control: control, Receipt: receipt}
	identity := struct {
		ActionKey            string         `json:"action_key"`
		OriginID             string         `json:"origin_id"`
		ReleaseID            string         `json:"release_id"`
		DesiredGeneration    int64          `json:"desired_generation"`
		ManagedUserSetDigest string         `json:"managed_user_set_digest"`
		Control              ManagedControl `json:"control"`
	}{proof.ActionKey, proof.OriginID, proof.ReleaseID, proof.DesiredGeneration, proof.ManagedUserSetDigest, proof.Control}
	id := sha256.Sum256(mustJSON(t, identity))
	hash := sha256.Sum256(mustJSON(t, proof.Proof()))
	proof.ReceiptID = hex.EncodeToString(id[:])
	proof.ProofSHA256 = hex.EncodeToString(hash[:])
	return proof
}

func TestManagedFinalProofBindsRealCountersAndRejectsFabricatedUnused(t *testing.T) {
	proof := finalReceiptFixture(t)
	if ValidateManagedFinalReceipt(proof) != nil {
		t.Fatal("valid actual final proof rejected")
	}
	for _, change := range []func(*ManagedFinalReceipt){
		func(p *ManagedFinalReceipt) { p.Receipt.State = "fenced_unused" }, func(p *ManagedFinalReceipt) { p.Receipt.Uplink = nil },
		func(p *ManagedFinalReceipt) { p.Control.Generation++ }, func(p *ManagedFinalReceipt) { p.Receipt.BootID = strings.Repeat("f", 64) },
		func(p *ManagedFinalReceipt) { p.Receipt.ObservedAt = "2000-01-01T00:00:00Z" }, func(p *ManagedFinalReceipt) { p.ReceiptID = strings.Repeat("f", 64) },
	} {
		bad := proof
		change(&bad)
		if ValidateManagedFinalReceipt(bad) == nil {
			t.Fatal("changed/partial final proof accepted")
		}
	}
	if validFinalReceiptSet([]ManagedFinalReceipt{proof, proof}) {
		t.Fatal("duplicate final envelope accepted")
	}
}

func TestFinalReceiptReadAndExactACKDoNotDependOnCurrentUsage(t *testing.T) {
	proof := finalReceiptFixture(t)
	transport := &scriptedTransport{
		get: func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != LeaseReceiptsPath || request.Header.Get(ActionKeyHeader) != "" {
				t.Fatal("final receipt recovery depends on current desired")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(mustJSON(t, FinalReceiptPage{Schema: 2, FinalReceipts: []ManagedFinalReceipt{proof}})))}, nil
		},
		post: func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != LeaseAckPath {
				t.Fatal("unexpected receipt mutation path")
			}
			var body struct {
				Schema   int               `json:"schema"`
				Receipts []FinalReceiptACK `json:"receipts"`
			}
			if json.NewDecoder(request.Body).Decode(&body) != nil || body.Schema != 2 || len(body.Receipts) != 1 || body.Receipts[0].ReceiptID != proof.ReceiptID || body.Receipts[0].ProofSHA256 != proof.ProofSHA256 {
				t.Fatal("ACK lost exact immutable proof binding")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"schema":2,"complete":true}`))}, nil
		},
	}
	client := newWithHTTPClient("https://agent.test", time.Second, time.Second, &http.Client{Transport: transport})
	page, err := client.LookupFinalReceipts(context.Background())
	if err != nil || len(page.FinalReceipts) != 1 {
		t.Fatal("historical final receipt unavailable")
	}
	if err := client.AckFinalReceipts(context.Background(), []FinalReceiptACK{{proof.ReceiptID, proof.ProofSHA256}}); err != nil {
		t.Fatal(err)
	}
	if transport.gets.Load() != 1 || transport.posts.Load() != 1 {
		t.Fatal("receipt recovery replayed or polled unrelated endpoints")
	}
}

func TestUseLeaseUnknownDeliveryDoesNotReplayOrManufactureAnotherNonce(t *testing.T) {
	snapshot := leaseSnapshotFixture(t)
	request, err := NewUseLeaseRequest(snapshot, time.Second, snapshot.UnavailableUsers)
	if err != nil {
		t.Fatal(err)
	}
	transport := &scriptedTransport{post: func(actual *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(actual.Body)
		if actual.URL.Path != UseLeasePath || !bytes.Equal(body, mustJSON(t, request)) {
			t.Fatal("lease request changed in transit")
		}
		return nil, errors.New("uncertain transport")
	}}
	client := newWithHTTPClient("https://agent.test", time.Second, time.Second, &http.Client{Transport: transport})
	if _, err := client.PostUseLease(context.Background(), request); !errors.Is(err, ErrDeliveryUnknown) {
		t.Fatal("unknown grant delivery was accepted")
	}
	if transport.posts.Load() != 1 || transport.gets.Load() != 0 {
		t.Fatal("unknown lease automatically replayed or used desired receipt recovery")
	}
}

func TestUseLeaseAcceptsBoundedFullFleetProofsAboveUsageResponseCap(t *testing.T) {
	snapshot := leaseSnapshotFixture(t)
	request, err := NewUseLeaseRequest(snapshot, time.Second, []string{})
	if err != nil {
		t.Fatal(err)
	}
	response := UseLeaseResponse{Schema: 2, Nonce: request.Nonce, Complete: true, Receipts: make([]LeaseReceiptProof, 0, 4096)}
	request.Emails = make([]string, 0, 4096)
	remaining := uint32(900)
	for index := 0; index < 4096; index++ {
		email := "wl:wl-ent-" + hex.EncodeToString([]byte{byte(index >> 8), byte(index)}) + ":exit-s1"
		request.Emails = append(request.Emails, email)
		proof := finalReceiptFixture(t).Proof()
		proof.OriginID = strings.Repeat("o", 256)
		proof.ReleaseID = strings.Repeat("r", 256)
		proof.Control.Operation = "grant"
		proof.Control.Email = email
		proof.Control.DeadlineBoottimeNS = request.DeadlineBoottimeNS
		proof.Receipt.State = "granted"
		proof.Receipt.Email = email
		proof.Receipt.Uplink = nil
		proof.Receipt.Downlink = nil
		proof.Receipt.DeadlineBoottimeNS = request.DeadlineBoottimeNS
		proof.Receipt.LeaseRemainingMS = &remaining
		response.Receipts = append(response.Receipts, proof)
	}
	request.ManagedUserSetDigest = testManagedUserSetDigest(t, request.Emails)
	for index := range response.Receipts {
		response.Receipts[index].ManagedUserSetDigest = request.ManagedUserSetDigest
	}
	body := mustJSON(t, response)
	if len(body) <= maxUsageResponseBytes || len(body) > 16<<20 {
		t.Fatal("fixture must cover the real distinct operation-response bound")
	}
	transport := &scriptedTransport{post: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
	}}
	client := newWithHTTPClient("https://agent.test", time.Second, time.Second, &http.Client{Transport: transport})
	if got, err := client.PostUseLease(context.Background(), request); err != nil || len(got.Receipts) != 4096 {
		t.Fatalf("full fleet proof response: %v", err)
	}
}

func TestFinalPagePreservesExactPendingRequestWithoutNewChallenge(t *testing.T) {
	snapshot := leaseSnapshotFixture(t)
	pending, err := NewUseLeaseRequest(snapshot, time.Second, snapshot.UnavailableUsers)
	if err != nil {
		t.Fatal(err)
	}
	page := FinalReceiptPage{Schema: 2, FinalReceipts: []ManagedFinalReceipt{}, PendingUseLease: &pending}
	transport := &scriptedTransport{get: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(mustJSON(t, page)))}, nil
	}}
	client := newWithHTTPClient("https://agent.test", time.Second, time.Second, &http.Client{Transport: transport})
	got, err := client.LookupFinalReceipts(context.Background())
	if err != nil || got.PendingUseLease == nil || !bytes.Equal(mustJSON(t, *got.PendingUseLease), mustJSON(t, pending)) {
		t.Fatal("durable pending request changed during recovery")
	}
	snapshot.PendingUseLease = &pending
	if validUsageSnapshot(snapshot, snapshot.Receipt.ActionKey) {
		t.Fatal("new challenge accompanied an unresolved operation")
	}
}

type scriptedTransport struct {
	posts atomic.Int32
	gets  atomic.Int32
	post  func(*http.Request) (*http.Response, error)
	get   func(*http.Request) (*http.Response, error)
}

func (transport *scriptedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	switch request.Method {
	case http.MethodPost:
		transport.posts.Add(1)
		if transport.post != nil {
			return transport.post(request)
		}
	case http.MethodGet:
		transport.gets.Add(1)
		if transport.get != nil {
			return transport.get(request)
		}
	}
	return nil, errors.New("unexpected request")
}

type testTLSMaterial struct{ ca, cert, key string }

func testTLSFiles(t *testing.T, serverName string) (testTLSMaterial, *tls.Config) {
	t.Helper()
	caCertificate, caKey, caPEM := testCA(t)
	serverCertificate, _ := testLeaf(t, caCertificate, caKey, serverName, false)
	clientCertificate, clientKey := testLeaf(t, caCertificate, caKey, "maestro-whitelist-controller", true)
	directory := t.TempDir()
	files := testTLSMaterial{
		ca: filepath.Join(directory, "ca.pem"), cert: filepath.Join(directory, "client.pem"),
		key: filepath.Join(directory, "client.key"),
	}
	if err := os.WriteFile(files.ca, caPEM, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	if err := os.WriteFile(files.cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCertificate.Certificate[0]}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	clientKeyDER := x509.MarshalPKCS1PrivateKey(clientKey)
	if err := os.WriteFile(files.key, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: clientKeyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCertificate)
	return files, &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCertificate},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool,
	}
}

func testCA(t *testing.T) (*x509.Certificate, *rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return certificate, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
}

func testLeaf(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey, name string, client bool) (tls.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	usage := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	if client {
		usage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usage,
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}, key
}

func testDesired(t *testing.T) ([]byte, string) {
	t.Helper()
	payload := mustJSON(t, struct {
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
		Version: 1, OriginID: "origin-s4", NodeID: "s4", ReleaseID: "release-1",
		ProfileID: "profile-1", PresetID: "preset-1", ExitID: "exit-s4", Generation: 2,
		ConfigDigest: strings.Repeat("a", 64), ManagedUserSetDigest: strings.Repeat("b", 64),
		StaticUsers: []string{}, ManagedUsers: []string{},
	})
	sum := sha256.Sum256(payload)
	return payload, "s4:2:" + hex.EncodeToString(sum[:])
}

type testReceiptValue struct {
	ActionKey            string    `json:"action_key"`
	OriginID             string    `json:"origin_id"`
	ReleaseID            string    `json:"release_id"`
	XrayProcessBootID    string    `json:"xray_process_boot_id"`
	ConfigDigest         string    `json:"config_digest"`
	DesiredGeneration    int64     `json:"desired_generation"`
	ManagedUserSetDigest string    `json:"managed_user_set_digest"`
	AppliedAt            time.Time `json:"applied_at"`
	ExpiresAt            time.Time `json:"expires_at"`
}

func testReceipt(actionKey string) testReceiptValue {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	return testReceiptValue{
		ActionKey: actionKey, OriginID: "origin-s4", ReleaseID: "release-1", XrayProcessBootID: "boot-s4",
		ConfigDigest: strings.Repeat("a", 64), DesiredGeneration: 2, ManagedUserSetDigest: strings.Repeat("b", 64),
		AppliedAt: now, ExpiresAt: now.Add(30 * time.Second),
	}
}

func testManagedUserSetDigest(t *testing.T, users []string) string {
	t.Helper()
	canonical := append([]string{}, users...)
	sort.Strings(canonical)
	raw := mustJSON(t, canonical)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return raw
}
