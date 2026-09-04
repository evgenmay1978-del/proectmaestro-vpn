package controlplane

import (
	"context"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestReconcileWhiteListSidecarIntentsUnpublishesEnabledZeroBalanceBeforeRemovalRealStore(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	now := service.clock.Now()
	customer := seedIntegrityCustomer(t, service)
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
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO whitelist_balance_projections(
entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix
) VALUES(?,NULL,0,0,0,0,1,0,?,?)`, Args: []any{entitlementID, now.Unix(), now.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_publication_controls(
control_id,entitlement_id,version,enabled,source,source_topup_order_id,
operation_id,request_hash,created_at_unix
) VALUES(?,?,2,1,'ADMIN_ENABLE',NULL,?,?,?)`, Args: []any{
			"wlpub-admin:" + entitlementID, entitlementID, "op-enable:" + entitlementID,
			testDigest("b"), now.Unix(),
		}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_route_credentials(
entitlement_id,exit_id,managed_email,credential_envelope,created_at_unix
) VALUES(?,?,?,?,?)`, Args: []any{
			entitlementID, exit.ExitID, "wl:" + entitlementID + ":" + exit.ExitID,
			[]byte("sealed-route-credential"), now.Unix(),
		}},
	)
	previous, err := BuildWhiteListSidecarDesired(nil, []WhiteListOrigin{origin}, []WhiteListManagedRoute{{
		EntitlementID: entitlementID, ExitID: exit.ExitID,
	}}, exit)
	if err != nil || len(previous) != 1 {
		t.Fatalf("BuildWhiteListSidecarDesired: %#v, %v", previous, err)
	}
	if _, err := service.PersistWhiteListSidecarDesired(ctx, previous[0]); err != nil {
		t.Fatalf("PersistWhiteListSidecarDesired: %v", err)
	}

	sender := &desiredReceiptSender{now: now, bootID: "boot-s2"}
	err = service.ReconcileWhiteListSidecarIntents(ctx, "worker-s2", func(nodeID string) (ExternalActionSender, bool) {
		return sender, nodeID == origin.NodeID
	})
	if err != nil {
		t.Fatalf("ReconcileWhiteListSidecarIntents: %v", err)
	}
	state, err := service.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		t.Fatalf("loadWhiteListSidecarRuntimeState: %v", err)
	}
	managed, _, err := whiteListPreviousManagedState(state.previous)
	if err != nil {
		t.Fatalf("whiteListPreviousManagedState: %v", err)
	}
	if len(managed) != 0 {
		t.Fatalf("latest managed entitlements = %#v, want zero after fail-closed unpublish", managed)
	}
	if sender.posts != 1 || state.previous[origin.OriginID].Generation != previous[0].Generation+1 {
		t.Fatalf("removal posts/generation = %d/%d, want 1/%d", sender.posts, state.previous[origin.OriginID].Generation, previous[0].Generation+1)
	}
}
