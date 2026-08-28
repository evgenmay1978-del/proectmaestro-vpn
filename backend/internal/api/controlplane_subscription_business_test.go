package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

type subscriptionDocumentStub struct {
	token string
}

func (s *subscriptionDocumentStub) BusinessSubscriptionDocument(_ context.Context, token string) (controlplane.BusinessCustomer, json.RawMessage, error) {
	s.token = token
	return controlplane.BusinessCustomer{
		Customer: controlplane.Customer{Access: controlplane.CustomerAccess{Credentials: map[string]string{"vless": "customer-vless-uuid"}}},
		Login:    "alice",
	}, json.RawMessage(`{"credentials":{"vless":"customer-vless-uuid"}}`), nil
}

func TestServiceBusinessSubscriptionSnapshotRendersConfiguredTopology(t *testing.T) {
	t.Parallel()

	source := &subscriptionDocumentStub{}
	business := &ServiceBusiness{
		cfg: ServiceBusinessConfig{SubscriptionTopology: subgen.Customer{
			VLESS: &subgen.VLESSCreds{Server: "vless.example.test", Port: 443, SNI: "cdn.example.test", PublicKey: "public-key", ShortID: "0123456789abcdef"},
		}},
		subscriptions: source,
	}

	snapshot, err := business.SubscriptionSnapshot(context.Background(), "subscription-token")
	if err != nil {
		t.Fatalf("subscription snapshot: %v", err)
	}
	if source.token != "subscription-token" {
		t.Fatalf("source token = %q", source.token)
	}
	var config struct {
		Outbounds []struct {
			Type   string `json:"type"`
			Server string `json:"server"`
			UUID   string `json:"uuid"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(snapshot.Document, &config); err != nil {
		t.Fatalf("decode snapshot document: %v", err)
	}
	for _, outbound := range config.Outbounds {
		if outbound.Type == "vless" && outbound.Server == "vless.example.test" && outbound.UUID == "customer-vless-uuid" {
			return
		}
	}
	t.Fatalf("snapshot did not use configured topology and canonical access: %s", snapshot.Document)
}
