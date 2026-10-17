package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/agent"
)

type leaseApplier struct {
	fakeApplier
	useCalls, tailCalls, ackCalls int
	request                       agent.UseLeaseRequest
	ack                           agent.LeaseReceiptAck
	result                        agent.UseLeaseResult
	page                          agent.LeaseReceiptPage
	err                           error
}

func (a *leaseApplier) UseLease(_ context.Context, r agent.UseLeaseRequest) (agent.UseLeaseResult, error) {
	a.useCalls++
	a.request = r
	return a.result, a.err
}
func (a *leaseApplier) LeaseReceipts(context.Context) (agent.LeaseReceiptPage, error) {
	a.tailCalls++
	return a.page, a.err
}
func (a *leaseApplier) AckLeaseReceipts(_ context.Context, ack agent.LeaseReceiptAck) error {
	a.ackCalls++
	a.ack = ack
	return a.err
}

func leaseHTTPRequest(method, path string, raw []byte, authenticated bool) *http.Request {
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if authenticated {
		r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{new(x509.Certificate)}}}
	}
	return r
}

func TestLeaseHTTPPreservesExpiredReceiptAndIndependentTailPage(t *testing.T) {
	request := agent.UseLeaseRequest{Schema: 2, ActionKey: "node:1:" + strings.Repeat("d", 64), XrayProcessBootID: "physical-boot", ConfigDigest: strings.Repeat("a", 64),
		ManagedUserSetDigest: strings.Repeat("b", 64), Nonce: strings.Repeat("c", 64), ClockDomain: strings.Repeat("e", 64), ReadStartedBoottimeNS: 1, DeadlineBoottimeNS: 2, Emails: []string{"wl:one:exit-s1"}}
	a := &leaseApplier{result: agent.UseLeaseResult{Schema: 2, Nonce: request.Nonce, Complete: false, NeedsFreshNonce: true, Receipts: []agent.LeaseReceiptProof{}}, err: agent.ErrLeaseUnavailable}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	NewHandler(a).ServeHTTP(response, leaseHTTPRequest(http.MethodPost, UseLeasePath, raw, true))
	var result agent.UseLeaseResult
	if response.Code != http.StatusServiceUnavailable || json.Unmarshal(response.Body.Bytes(), &result) != nil || !reflect.DeepEqual(result, a.result) ||
		!reflect.DeepEqual(a.request, request) || a.useCalls != 1 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("HTTP error discarded validated operation result")
	}
	a.err = nil
	a.page = agent.LeaseReceiptPage{Schema: 2, FinalReceipts: []agent.FinalLeaseReceipt{}, HasMoreFinalReceipts: true, PendingUseLease: &request}
	response = httptest.NewRecorder()
	NewHandler(a).ServeHTTP(response, leaseHTTPRequest(http.MethodGet, LeaseReceiptsPath, nil, true))
	var page agent.LeaseReceiptPage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || !reflect.DeepEqual(page, a.page) || a.tailCalls != 1 || a.lookupCalled != 0 || a.called != 0 {
		t.Fatal("final tail retrieval depended on desired readiness or mutated ordinary state")
	}
}

func TestLeaseHTTPAckHasExactProofAndExplicitSuccess(t *testing.T) {
	a := &leaseApplier{}
	ack := agent.LeaseReceiptAck{Schema: 2, Receipts: []agent.LeaseReceiptAckItem{{ReceiptID: strings.Repeat("a", 64), ProofSHA256: strings.Repeat("b", 64)}}}
	raw, _ := json.Marshal(ack)
	response := httptest.NewRecorder()
	NewHandler(a).ServeHTTP(response, leaseHTTPRequest(http.MethodPost, LeaseAckPath, raw, true))
	if response.Code != http.StatusOK || response.Body.String() != `{"schema":2,"complete":true}` || !reflect.DeepEqual(a.ack, ack) || a.ackCalls != 1 {
		t.Fatal("ACK wire lost exact proof or completion")
	}
	a.err = agent.ErrConflict
	response = httptest.NewRecorder()
	NewHandler(a).ServeHTTP(response, leaseHTTPRequest(http.MethodPost, LeaseAckPath, raw, true))
	if response.Code != http.StatusConflict {
		t.Fatal("mismatched ACK did not conflict")
	}
}

func TestLeaseHTTPRejectsUnauthenticatedAliasedAndOversizeCommands(t *testing.T) {
	for _, test := range []struct {
		name, method, path string
		body               []byte
		authenticated      bool
		status             int
	}{
		{"unauth-use", http.MethodPost, UseLeasePath, []byte(`{}`), false, http.StatusUnauthorized},
		{"unauth-tail", http.MethodGet, LeaseReceiptsPath, nil, false, http.StatusUnauthorized},
		{"unauth-ack", http.MethodPost, LeaseAckPath, []byte(`{}`), false, http.StatusUnauthorized},
		{"wrong-method", http.MethodGet, UseLeasePath, nil, true, http.StatusMethodNotAllowed},
		{"aliased-ack", http.MethodPost, LeaseAckPath, []byte(`{"schema":2,"SCHEMA":2,"receipts":[]}`), true, http.StatusBadRequest},
		{"trailing", http.MethodPost, LeaseAckPath, []byte(`{"schema":2,"receipts":[]} {}`), true, http.StatusBadRequest},
		{"unknown", http.MethodPost, UseLeasePath, []byte(`{"arbitrary_control":"grant"}`), true, http.StatusBadRequest},
		{"oversize", http.MethodPost, UseLeasePath, bytes.Repeat([]byte{' '}, MaxRequestBytes+1), true, http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := &leaseApplier{}
			response := httptest.NewRecorder()
			NewHandler(a).ServeHTTP(response, leaseHTTPRequest(test.method, test.path, test.body, test.authenticated))
			if response.Code != test.status || a.useCalls+a.tailCalls+a.ackCalls != 0 {
				t.Fatal("invalid request reached a lease mutation")
			}
		})
	}
	response := httptest.NewRecorder()
	NewHandler(&fakeApplier{}).ServeHTTP(response, leaseHTTPRequest(http.MethodGet, LeaseReceiptsPath, nil, true))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("upstream-only applier exposed commercial lease capability")
	}
}
