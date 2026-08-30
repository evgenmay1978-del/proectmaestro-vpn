package controlplane_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestServiceBusinessPanelOperationalStatusIsHonestAndAggregateOnlySQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	owner := seedF9PanelPrincipal(t, fixture, "status-owner", "owner")
	handler := f9PanelHandler(fixture)
	fixture.sqlite.must(t,
		rqlite.Statement{SQL: `INSERT INTO outbox_events(
event_id,aggregate_type,aggregate_id,generation,event_type,payload_envelope,payload_sha256,
status,available_at_unix,attempts,created_at_unix,node_id,service_name,operation_id,event_kind)
VALUES('status-event-pending','customer',?,2,'status.pending',X'01',?,'pending',unixepoch()-120,0,unixepoch()-120,'S4','s4','status-operation','apply')`, Args: []any{fixture.customerID, strings.Repeat("1", 64)}},
		rqlite.Statement{SQL: `INSERT INTO outbox_events(
event_id,aggregate_type,aggregate_id,generation,event_type,payload_envelope,payload_sha256,
status,available_at_unix,attempts,created_at_unix,node_id,service_name,operation_id,event_kind)
VALUES('status-event-failed','customer',?,3,'status.failed',X'02',?,'failed',unixepoch()-60,3,unixepoch()-60,'S4','s4','status-operation-failed','apply')`, Args: []any{fixture.customerID, strings.Repeat("2", 64)}},
		rqlite.Statement{SQL: `INSERT INTO node_apply_receipts(
receipt_id,customer_id,node_id,service_name,generation,desired_sha256,status,error_code,created_at_unix,
cluster_epoch,node_incarnation,lease_fence,operation_id)
VALUES('status-receipt-secret',?,'S4','s4',1,?,'failed','apply_failed',unixepoch()-45,1,1,1,'status-operation-failed')`, Args: []any{fixture.customerID, strings.Repeat("a", 64)}},
		rqlite.Statement{SQL: `INSERT INTO telegram_bot_routes(
bot_identity_hmac,token_fingerprint_hmac,credential_version,schema_fingerprint,updated_at_unix)
VALUES(?,?,1,'status-schema',unixepoch())`, Args: []any{strings.Repeat("b", 64), strings.Repeat("c", 64)}},
		rqlite.Statement{SQL: `INSERT INTO telegram_pollers(
bot_identity_hmac,node_id,lease_token,offset_value,lease_fence,lease_expires_at_unix,updated_at_unix)
VALUES(?,'S4','status-lease-secret',0,1,unixepoch()+600,unixepoch())`, Args: []any{strings.Repeat("b", 64)}},
		rqlite.Statement{SQL: `INSERT INTO telegram_inbox(
bot_id,update_id,update_hmac,payload_envelope,payload_sha256,status,received_at_unix,processed_at_unix)
VALUES('status-bot-secret',1,?,X'03',?,'rejected',unixepoch()-30,unixepoch()-20)`, Args: []any{strings.Repeat("d", 64), strings.Repeat("e", 64)}},
		rqlite.Statement{SQL: `INSERT INTO telegram_delivery_outbox(
delivery_id,bot_id,chat_key_hmac,payload_envelope,payload_sha256,dedupe_key,status,attempts,available_at_unix,created_at_unix)
VALUES('status-delivery-secret','status-bot-secret',?,X'04',?,'status-dedupe-secret','failed',4,unixepoch()-20,unixepoch()-30)`, Args: []any{strings.Repeat("f", 64), strings.Repeat("0", 64)}},
		rqlite.Statement{SQL: `INSERT INTO operations(
operation_id,operation_type,resource_type,resource_id,status,requested_by_hmac,created_at_unix,updated_at_unix)
VALUES('status-operation-secret','status.test','customer',?,'failed',?,unixepoch()-25,unixepoch()-10)`, Args: []any{fixture.customerID, strings.Repeat("9", 64)}},
		rqlite.Statement{SQL: `INSERT INTO cluster_settings(setting_key,public_value_json,generation,updated_at_unix)
VALUES('service_endpoint.s4','{"endpoint":"https://status-origin-secret.invalid"}',1,unixepoch())`},
	)

	response := f9PanelGET(t, handler, "/mp/api/cluster-status", owner)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", response.Code, response.Body.String())
	}
	var body struct {
		Ready          bool   `json:"ready"`
		Quorum         bool   `json:"quorum"`
		ReadReady      bool   `json:"read_ready"`
		WriteReadiness string `json:"write_readiness"`
		DataComplete   bool   `json:"data_complete"`
		Replication    struct {
			State          string `json:"state"`
			DataComplete   bool   `json:"data_complete"`
			LeaderID       string `json:"leader_id"`
			ReachableNodes int    `json:"reachable_nodes"`
			MaxLagEntries  int64  `json:"max_lag_entries"`
		} `json:"replication"`
		Nodes struct {
			Voters               int `json:"voters"`
			EnabledVoters        int `json:"enabled_voters"`
			ActiveServiceTargets int `json:"active_service_targets"`
			FencedServiceTargets int `json:"fenced_service_targets"`
			StaleReceipts        int `json:"stale_receipts"`
		} `json:"nodes"`
		Apply struct {
			Pending          int   `json:"pending"`
			Failed           int   `json:"failed"`
			FailedReceipts   int   `json:"failed_receipts"`
			MaxGenerationLag int64 `json:"max_generation_lag"`
		} `json:"apply"`
		Outbox struct {
			Pending                 int   `json:"pending"`
			Failed                  int   `json:"failed"`
			OldestPendingAgeSeconds int64 `json:"oldest_pending_age_seconds"`
		} `json:"outbox"`
		Telegram struct {
			Routes         int  `json:"routes"`
			ActivePollers  int  `json:"active_pollers"`
			InboxRejected  int  `json:"inbox_rejected"`
			DeliveryFailed int  `json:"delivery_failed"`
			DataComplete   bool `json:"data_complete"`
		} `json:"telegram"`
		DNSTLS struct {
			State        string `json:"state"`
			Targets      int    `json:"targets"`
			DataComplete bool   `json:"data_complete"`
		} `json:"dns_tls"`
		Backup struct {
			State              string `json:"state"`
			DirtyGeneration    int64  `json:"dirty_generation"`
			VerifiedGeneration int64  `json:"verified_generation"`
			GenerationGap      int64  `json:"generation_gap"`
		} `json:"backup"`
		Restore struct {
			State        string `json:"state"`
			Epoch        int64  `json:"epoch"`
			DataComplete bool   `json:"data_complete"`
		} `json:"restore"`
		Failures []struct {
			Component string `json:"component"`
			Count     int    `json:"count"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || !body.Quorum || !body.ReadReady || body.WriteReadiness != "unknown" || body.DataComplete {
		t.Fatalf("top-level status = %+v, want degraded honest read-ready status", body)
	}
	if body.Replication.State != "unknown" || body.Replication.DataComplete || body.Replication.LeaderID != "" || body.Replication.ReachableNodes != 0 || body.Replication.MaxLagEntries != 0 {
		t.Fatalf("replication = %+v, want explicit unknown without invented telemetry", body.Replication)
	}
	if body.Nodes.Voters != 3 || body.Nodes.EnabledVoters != 3 || body.Nodes.ActiveServiceTargets < 4 || body.Nodes.FencedServiceTargets < 1 || body.Nodes.StaleReceipts < 1 {
		t.Fatalf("nodes = %+v, want seeded aggregate topology", body.Nodes)
	}
	if body.Apply.Pending < 1 || body.Apply.Failed != 0 || body.Apply.FailedReceipts < 1 || body.Apply.MaxGenerationLag < 1 {
		t.Fatalf("apply = %+v, want pending desired state and failed receipt", body.Apply)
	}
	if body.Outbox.Pending != 1 || body.Outbox.Failed != 1 || body.Outbox.OldestPendingAgeSeconds < 90 {
		t.Fatalf("outbox = %+v, want one pending and one failed event", body.Outbox)
	}
	if body.Telegram.Routes != 1 || body.Telegram.ActivePollers != 1 || body.Telegram.InboxRejected != 1 || body.Telegram.DeliveryFailed != 1 || body.Telegram.DataComplete {
		t.Fatalf("telegram = %+v, want aggregate-only incomplete telemetry", body.Telegram)
	}
	if body.DNSTLS.State != "unknown" || body.DNSTLS.Targets != 1 || body.DNSTLS.DataComplete {
		t.Fatalf("dns_tls = %+v, want one unprobed target", body.DNSTLS)
	}
	if body.Backup.State != "dirty" || body.Backup.DirtyGeneration != 1 || body.Backup.VerifiedGeneration != 0 || body.Backup.GenerationGap != 1 {
		t.Fatalf("backup = %+v, want initial dirty generation", body.Backup)
	}
	if body.Restore.State != "activated" || body.Restore.Epoch != 1 || body.Restore.DataComplete {
		t.Fatalf("restore = %+v, want current epoch and explicit missing drill telemetry", body.Restore)
	}
	failureCounts := map[string]int{}
	for _, failure := range body.Failures {
		failureCounts[failure.Component] = failure.Count
	}
	for _, component := range []string{"outbox", "apply", "telegram", "operations"} {
		if failureCounts[component] < 1 {
			t.Fatalf("failures = %+v, missing aggregate %q", body.Failures, component)
		}
	}
	for _, secret := range []string{
		"status-event-pending", "status-event-failed", "status-receipt-secret", fixture.customerID,
		"status-bot-secret", "status-lease-secret", "status-delivery-secret", "status-operation-secret",
		"status-origin-secret.invalid", strings.Repeat("b", 64), strings.Repeat("c", 64),
	} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("cluster status leaked secret or durable identifier %q: %s", secret, response.Body.String())
		}
	}
}
