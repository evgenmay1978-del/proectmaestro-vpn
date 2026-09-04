package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestWhiteListPublicationDeliveryReadsCurrentDurableStateBySubscriptionToken(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	now := service.clock.Now()
	customer := seedIntegrityCustomer(t, service)
	access, err := service.customerAccess(ctx, customer.ID)
	if err != nil {
		t.Fatalf("customerAccess: %v", err)
	}
	entitlement, err := service.EnsureWhiteListEntitlement(ctx, customer.ID)
	if err != nil {
		t.Fatalf("EnsureWhiteListEntitlement: %v", err)
	}
	entitlementID := entitlement.EntitlementID()
	origin := WhiteListOrigin{
		OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1",
		ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("a"),
		Active: true, StaticUsers: []string{"static-s2@example.invalid"},
	}
	exit := WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true}
	seedWhiteListSidecarInventory(t, db, []WhiteListOrigin{origin}, exit)
	materialBytes, err := json.Marshal(WhiteListClientMaterial{
		PublicHost: "cdn.example.invalid", SecretPath: "/static/main/video/segment.ts/opaque",
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := NewWhiteListRouteCredential(service.store.secrets, entitlementID, exit.ExitID, materialBytes)
	if err != nil {
		t.Fatalf("NewWhiteListRouteCredential: %v", err)
	}
	if err := service.StoreWhiteListRouteCredential(ctx, credential); err != nil {
		t.Fatalf("StoreWhiteListRouteCredential: %v", err)
	}
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO whitelist_balance_projections(
entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix
) VALUES(?,NULL,0,1000000000,0,0,1,0,?,?)`, Args: []any{entitlementID, now.Unix(), now.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_publication_controls(
control_id,entitlement_id,version,enabled,source,source_topup_order_id,
operation_id,request_hash,created_at_unix
) VALUES(?,?,2,1,'ADMIN_ENABLE',NULL,?,?,?)`, Args: []any{
			"wlpub-admin:" + entitlementID, entitlementID, "op-enable:" + entitlementID,
			testDigest("b"), now.Unix(),
		}},
	)
	sender := &desiredReceiptSender{now: now, bootID: "boot-s2"}
	resolveSender := func(nodeID string) (ExternalActionSender, bool) {
		return sender, nodeID == origin.NodeID
	}
	if err := service.ReconcileWhiteListSidecarIntents(ctx, "worker-s2", resolveSender); err != nil {
		t.Fatalf("ReconcileWhiteListSidecarIntents: %v", err)
	}

	got, err := service.WhiteListPublicationDelivery(ctx, access.SubscriptionToken, now, resolveSender)
	if err != nil {
		t.Fatalf("WhiteListPublicationDelivery: %v", err)
	}
	if got.Decision.Verdict != WhiteListPublicationPublishable || got.Decision.ProjectionVersion != 1 ||
		got.Decision.DesiredGeneration != 1 || got.Material.ClientID != "11111111-1111-4111-8111-111111111111" ||
		got.CountryCode != "NL" || got.CountryLabel != "Netherlands" || got.ReleaseID != origin.ReleaseID {
		t.Fatalf("publication delivery = %#v", got)
	}

	sender.receipt = nil
	restarted, err := service.WhiteListPublicationDelivery(ctx, access.SubscriptionToken, now, resolveSender)
	if err != nil || restarted.Decision.Verdict == WhiteListPublicationPublishable {
		t.Fatalf("receipt from a prior Xray boot remained publishable: %#v, %v", restarted, err)
	}

	closed, err := service.WhiteListPublicationDelivery(ctx, "unknown-token", now, resolveSender)
	if err != nil || closed.Decision.Verdict != WhiteListPublicationNoEntitlement {
		t.Fatalf("unknown token decision = %#v, %v", closed, err)
	}
}

func TestWhiteListClientMaterialRejectsNonClientCredential(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	customer := seedIntegrityCustomer(t, service)
	entitlement, err := service.EnsureWhiteListEntitlement(ctx, customer.ID)
	if err != nil {
		t.Fatal(err)
	}
	exit := WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true}
	seedWhiteListSidecarInventory(t, db, []WhiteListOrigin{{
		OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1", ProfileID: "profile-1",
		PresetID: "preset-1", ConfigDigest: testDigest("a"), Active: true,
	}}, exit)
	badBytes, err := json.Marshal(WhiteListClientMaterial{
		PublicHost: "cdn.example.invalid", SecretPath: "/static/main/video/segment.ts/opaque",
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "none",
		ClientEncryptionRole:     "SERVER",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := NewWhiteListRouteCredential(service.store.secrets, entitlement.EntitlementID(), exit.ExitID, badBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StoreWhiteListRouteCredential(ctx, credential); err != nil {
		t.Fatal(err)
	}
	if _, err := service.whiteListClientMaterial(ctx, entitlement.EntitlementID(), exit.ExitID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("non-client route credential accepted: %v", err)
	}
}
