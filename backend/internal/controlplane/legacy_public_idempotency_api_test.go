package controlplane_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func TestServiceBusinessLegacyPublicIdempotencyBridgeUsesCoreHMACSQLite(t *testing.T) {
	ctx := context.Background()
	db := newS4CanarySQLite(t)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("apply real migrations: %v", err)
	}
	box, err := controlplane.NewSecretBox(
		1,
		map[int][]byte{1: bytes.Repeat([]byte{0x41}, 32)},
		bytes.Repeat([]byte{0x42}, 32),
	)
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	clock := s4CanaryClock{value: time.Unix(2_000_000, 0).UTC()}
	store, err := controlplane.NewStore(db, box, clock)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	service, err := controlplane.NewService(store, s4CanaryIDs{}, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	business := api.NewServiceBusiness(service, api.ServiceBusinessConfig{})

	coreKey, err := service.LegacyPublicIdempotencyKey("/claim", "missing", "device-1")
	if err != nil {
		t.Fatalf("core key: %v", err)
	}
	bridgeKey, err := business.LegacyPublicIdempotencyKey("/claim", "missing", "device-1")
	if err != nil {
		t.Fatalf("ServiceBusiness key: %v", err)
	}
	if bridgeKey != coreKey {
		t.Fatalf("ServiceBusiness key=%q, want exact core key %q", bridgeKey, coreKey)
	}

	handler := api.NewControlPlane(business, api.Config{}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"code":"missing","device":"device-1"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("real ServiceBusiness keyless claim status=%d body=%q, want 404 after derivation", response.Code, response.Body.String())
	}
}
