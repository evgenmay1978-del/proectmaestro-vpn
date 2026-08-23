//go:build rqlite_integration

package controlplane

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestSchemaSeedsImmutableTariffsAndFourNodes(t *testing.T) {
	ctx, db := mustAppliedSchema(t)

	tariffs := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT tariff_version_id, duration_days, amount_minor, currency
		FROM tariff_versions
		WHERE tariff_version_id IN ('tariff_1m_v1','tariff_2m_v1','tariff_3m_v1','tariff_6m_v1','tariff_12m_v1')
		ORDER BY tariff_version_id
	`})
	if got := fmt.Sprint(tariffs.Rows); got != `[map[amount_minor:480000 currency:RUB duration_days:365 tariff_version_id:tariff_12m_v1] map[amount_minor:40000 currency:RUB duration_days:30 tariff_version_id:tariff_1m_v1] map[amount_minor:80000 currency:RUB duration_days:60 tariff_version_id:tariff_2m_v1] map[amount_minor:120000 currency:RUB duration_days:90 tariff_version_id:tariff_3m_v1] map[amount_minor:240000 currency:RUB duration_days:180 tariff_version_id:tariff_6m_v1]]` {
		t.Fatalf("tariff seeds = %s", got)
	}

	nodes := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT node_id, is_voter FROM nodes ORDER BY node_id
	`})
	if got := fmt.Sprint(nodes.Rows); got != `[map[is_voter:0 node_id:S1] map[is_voter:1 node_id:S2] map[is_voter:1 node_id:S3] map[is_voter:1 node_id:S4]]` {
		t.Fatalf("node seeds = %s", got)
	}

	s1 := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT desired_target, apply_enabled, fenced, retired
		FROM node_services WHERE node_id='S1' AND service_name='maestro-core'
	`})
	if got := fmt.Sprint(s1.Rows); got != `[map[apply_enabled:0 desired_target:1 fenced:1 retired:0]]` {
		t.Fatalf("S1 service seed = %s", got)
	}

	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL:  "UPDATE tariff_versions SET amount_minor=? WHERE tariff_version_id=?",
		Args: []any{1, "tariff_1m_v1"},
	}); err == nil {
		t.Fatal("immutable tariff version accepted an update")
	}
}

func TestDesiredProtocolTagsBindToExactDesiredNode(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	customerID := "topology-customer"
	mustRequest(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO customers(
			customer_id,display_login,login_key_hmac,status,expires_at_unix,
			generation,created_at_unix,updated_at_unix
		) VALUES(?,?,?,?,?,?,?,?)
	`, Args: []any{customerID, "TopologyCustomer", strings.Repeat("ab", 32), "active", 2_100_000, 7, 1_000_000, 1_000_000}})

	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO desired_protocol_tags(customer_id,node_id,service_name,protocol_tag)
		VALUES(?,?,?,?)
	`, Args: []any{customerID, "S2", "maestro-core", "vless"}})

	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: `
			INSERT INTO desired_node_state(
				customer_id,node_id,service_name,generation,desired_envelope,
				desired_sha256,status,updated_at_unix
			) VALUES(?,?,?,?,?,?,?,?)
		`, Args: []any{customerID, "S2", "maestro-core", 7, "synthetic-envelope", repeatHex("2"), "pending", 1_000_000}},
		rqlite.Statement{SQL: `
			INSERT INTO desired_protocol_tags(customer_id,node_id,service_name,protocol_tag)
			VALUES(?,?,?,?)
		`, Args: []any{customerID, "S2", "maestro-core", "vless"}},
	)

	result := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT customer_id,node_id,service_name,protocol_tag
		FROM desired_protocol_tags
		WHERE customer_id=?
	`, Args: []any{customerID}})
	if got := fmt.Sprint(result.Rows); got != `[map[customer_id:topology-customer node_id:S2 protocol_tag:vless service_name:maestro-core]]` {
		t.Fatalf("desired protocol rows = %s", got)
	}

	mustRequest(t, ctx, db, rqlite.Statement{
		SQL:  "DELETE FROM desired_node_state WHERE customer_id=? AND node_id=? AND service_name=?",
		Args: []any{customerID, "S2", "maestro-core"},
	})
	result = mustStrongQuery(t, ctx, db, rqlite.Statement{
		SQL:  "SELECT protocol_tag FROM desired_protocol_tags WHERE customer_id=?",
		Args: []any{customerID},
	})
	if len(result.Rows) != 0 {
		t.Fatalf("desired protocol cascade rows = %#v", result.Rows)
	}
	mustRequest(t, ctx, db, rqlite.Statement{
		SQL:  "DELETE FROM customers WHERE customer_id=?",
		Args: []any{customerID},
	})
}

func TestWhiteListIdentityDoesNotBlockTombstonePurge(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	const (
		customerID    = "whitelist-purge-customer"
		entitlementID = "wl-ent-00000000000000000000000000000001"
		tombstoneID   = "whitelist-purge-tombstone"
	)
	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: `
			INSERT INTO customers(
				customer_id,display_login,login_key_hmac,status,expires_at_unix,
				generation,created_at_unix,updated_at_unix
			) VALUES(?,?,?,?,?,?,?,?)
		`, Args: []any{customerID, "WhitelistPurge", strings.Repeat("cd", 32), "active", 2_100_000, 1, 1_000_000, 1_000_000}},
		rqlite.Statement{SQL: `
			INSERT INTO whitelist_entitlement_identities(entitlement_id,customer_id,created_at_unix)
			VALUES(?,?,?)
		`, Args: []any{entitlementID, customerID, 1_000_000}},
		rqlite.Statement{SQL: `
			INSERT INTO tombstones(tombstone_id,customer_id,generation,reason,created_at_unix)
			VALUES(?,?,1,'test-purge',1)
		`, Args: []any{tombstoneID, customerID}},
		rqlite.Statement{SQL: `
			INSERT INTO tombstone_targets(tombstone_id,node_id,service_name,status,applied_at_unix)
			SELECT ?,node_id,service_name,'applied',1 FROM node_services
			WHERE desired_target=1 AND retired=0
		`, Args: []any{tombstoneID}},
	)

	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "DELETE FROM whitelist_entitlement_identities WHERE entitlement_id=?",
		Args: []any{entitlementID},
	})

	service, _ := testService(t, db)
	if err := service.PurgeTombstone(ctx, TombstonePurgeCommand{
		TombstoneID: tombstoneID, CustomerID: customerID,
	}); err != nil {
		t.Fatalf("PurgeTombstone with white-list identity: %v", err)
	}
	for _, table := range []string{"customers", "whitelist_entitlement_identities"} {
		result := mustStrongQuery(t, ctx, db, rqlite.Statement{
			SQL:  "SELECT COUNT(*) AS row_count FROM " + table + " WHERE customer_id=?",
			Args: []any{customerID},
		})
		if got := fmt.Sprint(result.Rows); got != `[map[row_count:0]]` {
			t.Fatalf("%s rows after purge = %s", table, got)
		}
	}
}

func TestImportSchemaBindsRunAndBatchDigests(t *testing.T) {
	ctx, db := mustAppliedSchema(t)

	runColumns := schemaColumnNames(t, mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: "PRAGMA table_info(import_runs)"}))
	wantRunColumns := []string{
		"batch_count", "completed_at_unix", "import_run_id", "parent_source_sha256",
		"plan_sha256", "snapshot_kind", "source_sha256", "started_at_unix", "status", "target_sha256",
	}
	if fmt.Sprint(runColumns) != fmt.Sprint(wantRunColumns) {
		t.Fatalf("import_runs columns = %v, want %v", runColumns, wantRunColumns)
	}

	batchColumns := schemaColumnNames(t, mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: "PRAGMA table_info(import_batches)"}))
	wantBatchColumns := []string{
		"applied_at_unix", "batch_digest", "batch_index", "import_run_id", "row_count", "status",
	}
	if fmt.Sprint(batchColumns) != fmt.Sprint(wantBatchColumns) {
		t.Fatalf("import_batches columns = %v, want %v", batchColumns, wantBatchColumns)
	}

	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO import_runs(
			import_run_id,snapshot_kind,source_sha256,plan_sha256,parent_source_sha256,
			batch_count,status,started_at_unix
		) VALUES(?,?,?,?,?,?,?,?)
	`, Args: []any{"bad-full-parent", "full", repeatHex("a"), repeatHex("b"), repeatHex("c"), 1, "applying", 100}})
}

func TestImportSchemaPreservesLegacyBusinessFields(t *testing.T) {
	ctx, db := mustAppliedSchema(t)

	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: `
			INSERT INTO customers(
				customer_id,display_login,login_key_hmac,status,expires_at_unix,
				generation,created_at_unix,updated_at_unix
			) VALUES(?,?,?,?,?,?,?,?)
		`, Args: []any{"legacy-customer", "CaseSensitiveUser", repeatHex("e"), "active", 2_100_000, 7, 1_000_000, 1_000_000}},
		rqlite.Statement{SQL: `
			INSERT INTO subscription_tokens(
				token_id,customer_id,token_hmac,token_envelope,token_sha256,
				generation,revoked,created_at_unix
			) VALUES(?,?,?,?,?,?,?,?)
		`, Args: []any{"legacy-token", "legacy-customer", repeatHex("f"), []byte{1, 2, 3}, repeatHex("c"), 7, 0, 1_000_000}},
		rqlite.Statement{SQL: `
			INSERT INTO orders(
				order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
				amount_minor,currency,duration_days,created_at_unix,expires_at_unix,
				payment_state,provisioning_state,operation_id
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		`, Args: []any{
			"legacy-order", "MCRD1", "customer_login", repeatHex("e"), "legacy-customer", "tariff_1m_v1",
			40_000, "RUB", 30, 1_000_000, 1_086_400, "pending", "pending", "legacy-order-operation",
		}},
	)

	result := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT c.display_login, st.token_envelope,
		       st.token_sha256, o.payment_code
		FROM customers c
		JOIN subscription_tokens st ON st.customer_id = c.customer_id
		JOIN orders o ON o.customer_id = c.customer_id
		WHERE c.customer_id = 'legacy-customer'
	`})
	if got := fmt.Sprint(result.Rows); got != `[map[display_login:CaseSensitiveUser payment_code:MCRD1 token_envelope:AQID token_sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc]]` {
		t.Fatalf("legacy business fields = %s", got)
	}
}

func TestImportSchemaPreservesBotCredentialRoute(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	botIdentity := repeatHex("6")
	tokenFingerprint := repeatHex("7")

	mustRequest(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO telegram_bot_routes(
			bot_identity_hmac,token_fingerprint_hmac,credential_version,
			schema_fingerprint,updated_at_unix
		) VALUES(?,?,?,?,?)
	`, Args: []any{botIdentity, tokenFingerprint, 3, "bot-schema-v1", 1_000_000}})

	result := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT bot_identity_hmac,token_fingerprint_hmac,credential_version,schema_fingerprint
		FROM telegram_bot_routes WHERE bot_identity_hmac=?
	`, Args: []any{botIdentity}})
	if got := fmt.Sprint(result.Rows); got != fmt.Sprintf(
		`[map[bot_identity_hmac:%s credential_version:3 schema_fingerprint:bot-schema-v1 token_fingerprint_hmac:%s]]`,
		botIdentity, tokenFingerprint,
	) {
		t.Fatalf("bot credential route = %s", got)
	}

	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO telegram_bot_routes(
			bot_identity_hmac,token_fingerprint_hmac,credential_version,
			schema_fingerprint,updated_at_unix
		) VALUES(?,?,?,?,?)
		ON CONFLICT(bot_identity_hmac) DO UPDATE SET
			token_fingerprint_hmac=excluded.token_fingerprint_hmac
	`, Args: []any{botIdentity, repeatHex("8"), 3, "bot-schema-v1", 1_000_001}})
}

func TestImportSchemaPreservesHardFencedBotPollState(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	botIdentity := repeatHex("9")
	tokenFingerprint := repeatHex("a")

	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: `
			INSERT INTO telegram_bot_routes(
				bot_identity_hmac,token_fingerprint_hmac,credential_version,
				schema_fingerprint,updated_at_unix
			) VALUES(?,?,?,?,?)
		`, Args: []any{botIdentity, tokenFingerprint, 5, "bot-schema-v1", 1_000_000}},
		rqlite.Statement{SQL: `
			INSERT INTO telegram_pollers(
				bot_identity_hmac,node_id,lease_token,offset_value,lease_fence,
				lease_expires_at_unix,updated_at_unix
			) VALUES(?,NULL,NULL,?,?,0,?)
		`, Args: []any{botIdentity, 42, 11, 1_000_000}},
	)

	result := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT bot_identity_hmac,offset_value,lease_fence
		FROM telegram_pollers WHERE bot_identity_hmac=?
	`, Args: []any{botIdentity}})
	if got := fmt.Sprint(result.Rows); got != fmt.Sprintf(
		`[map[bot_identity_hmac:%s lease_fence:11 offset_value:42]]`, botIdentity,
	) {
		t.Fatalf("hard-fenced poll state = %s", got)
	}

	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE telegram_pollers SET offset_value=? WHERE bot_identity_hmac=?",
		Args: []any{41, botIdentity},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE telegram_pollers SET offset_value=?,lease_fence=? WHERE bot_identity_hmac=?",
		Args: []any{43, 10, botIdentity},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO telegram_pollers(
			bot_identity_hmac,node_id,lease_token,offset_value,lease_fence,
			lease_expires_at_unix,updated_at_unix
		) VALUES(?,NULL,NULL,?,?,0,?)
	`, Args: []any{repeatHex("b"), 1, 1, 1_000_000}})
}

func TestImportSchemaPreservesPendingCallbackState(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	botIdentity := repeatHex("c")
	tokenFingerprint := repeatHex("d")
	callbackHMAC := repeatHex("e")

	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: `
			INSERT INTO telegram_bot_routes(
				bot_identity_hmac,token_fingerprint_hmac,credential_version,
				schema_fingerprint,updated_at_unix
			) VALUES(?,?,?,?,?)
		`, Args: []any{botIdentity, tokenFingerprint, 7, "bot-schema-v1", 1_000_000}},
		rqlite.Statement{SQL: `
			INSERT INTO telegram_imported_callbacks(
				callback_hmac,bot_identity_hmac,order_id,action,state,updated_at_unix
			) VALUES(?,?,?,?,?,?)
		`, Args: []any{callbackHMAC, botIdentity, "legacy-order-callback", "confirm", "pending", 1_000_000}},
	)

	result := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT callback_hmac,bot_identity_hmac,order_id,action,state
		FROM telegram_imported_callbacks WHERE callback_hmac=?
	`, Args: []any{callbackHMAC}})
	if got := fmt.Sprint(result.Rows); got != fmt.Sprintf(
		`[map[action:confirm bot_identity_hmac:%s callback_hmac:%s order_id:legacy-order-callback state:pending]]`,
		botIdentity, callbackHMAC,
	) {
		t.Fatalf("imported callback = %s", got)
	}

	mustRequest(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE telegram_imported_callbacks SET state='in_flight',updated_at_unix=? WHERE callback_hmac=?",
		Args: []any{1_000_001, callbackHMAC},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE telegram_imported_callbacks SET state='pending' WHERE callback_hmac=?",
		Args: []any{callbackHMAC},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE telegram_imported_callbacks SET order_id='different-order' WHERE callback_hmac=?",
		Args: []any{callbackHMAC},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO telegram_imported_callbacks(
			callback_hmac,bot_identity_hmac,order_id,action,state,updated_at_unix
		) VALUES(?,?,?,?,?,?)
	`, Args: []any{repeatHex("0"), repeatHex("f"), "missing-route-order", "confirm", "pending", 1_000_000}})
}

func TestImportSchemaPreservesImmutableBotCredentialRotation(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	botIdentity := repeatHex("8")
	oldFingerprint := repeatHex("2")
	newFingerprint := repeatHex("3")
	auditDigest := repeatHex("4")

	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: `
			INSERT INTO telegram_bot_routes(
				bot_identity_hmac,token_fingerprint_hmac,credential_version,
				schema_fingerprint,updated_at_unix
			) VALUES(?,?,?,?,?)
		`, Args: []any{botIdentity, newFingerprint, 2, "bot-schema-v1", 1_000_000}},
		rqlite.Statement{SQL: `
			INSERT INTO telegram_bot_credential_rotations(
				audit_digest,bot_identity_hmac,old_token_fingerprint_hmac,
				new_token_fingerprint_hmac,old_credential_version,
				new_credential_version,imported_at_unix
			) VALUES(?,?,?,?,?,?,?)
		`, Args: []any{auditDigest, botIdentity, oldFingerprint, newFingerprint, 1, 2, 1_000_000}},
	)

	result := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT audit_digest,bot_identity_hmac,old_token_fingerprint_hmac,
		       new_token_fingerprint_hmac,old_credential_version,new_credential_version
		FROM telegram_bot_credential_rotations WHERE audit_digest=?
	`, Args: []any{auditDigest}})
	if got := fmt.Sprint(result.Rows); got != fmt.Sprintf(
		`[map[audit_digest:%s bot_identity_hmac:%s new_credential_version:2 new_token_fingerprint_hmac:%s old_credential_version:1 old_token_fingerprint_hmac:%s]]`,
		auditDigest, botIdentity, newFingerprint, oldFingerprint,
	) {
		t.Fatalf("credential rotation = %s", got)
	}

	mustRequest(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO telegram_bot_credential_rotations(
			audit_digest,bot_identity_hmac,old_token_fingerprint_hmac,
			new_token_fingerprint_hmac,old_credential_version,new_credential_version,imported_at_unix
		) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(audit_digest) DO UPDATE SET
			bot_identity_hmac=excluded.bot_identity_hmac,
			old_token_fingerprint_hmac=excluded.old_token_fingerprint_hmac,
			new_token_fingerprint_hmac=excluded.new_token_fingerprint_hmac,
			old_credential_version=excluded.old_credential_version,
			new_credential_version=excluded.new_credential_version
	`, Args: []any{auditDigest, botIdentity, oldFingerprint, newFingerprint, 1, 2, 1_000_001}})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE telegram_bot_credential_rotations SET new_token_fingerprint_hmac=? WHERE audit_digest=?",
		Args: []any{repeatHex("5"), auditDigest},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO telegram_bot_credential_rotations(
			audit_digest,bot_identity_hmac,old_token_fingerprint_hmac,
			new_token_fingerprint_hmac,old_credential_version,new_credential_version,imported_at_unix
		) VALUES(?,?,?,?,?,?,?)
	`, Args: []any{repeatHex("6"), botIdentity, oldFingerprint, repeatHex("5"), 1, 3, 1_000_001}})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "DELETE FROM telegram_bot_credential_rotations WHERE audit_digest=?",
		Args: []any{auditDigest},
	})
}

func TestImportEntityRegistryAndDeleteReceiptsAreFailClosed(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	runID := "schema-delete-registry-run"
	batchDigest := repeatHex("9")
	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: `
			INSERT INTO import_runs(
				import_run_id,snapshot_kind,source_sha256,plan_sha256,parent_source_sha256,
				target_sha256,batch_count,status,started_at_unix,completed_at_unix
			) VALUES(?,?,?,?,NULL,NULL,1,'applying',?,NULL)
		`, Args: []any{runID, "full", repeatHex("1"), repeatHex("2"), 1_000_000}},
		rqlite.Statement{SQL: `
			INSERT INTO import_batches(
				import_run_id,batch_index,batch_digest,row_count,status,applied_at_unix
			) VALUES(?,?,?,1,'applying',NULL)
		`, Args: []any{runID, 0, batchDigest}},
	)

	insertState := func(sourceKey, targetID, digest string) {
		t.Helper()
		mustRequest(t, ctx, db, rqlite.Statement{SQL: `
			INSERT INTO imported_entity_state(
				entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix
			) VALUES('encrypted_secret',?,?,?,'active',?)
		`, Args: []any{sourceKey, targetID, digest, 1_000_000}})
	}
	deleteState := func(sourceKey string) {
		t.Helper()
		mustRequest(t, ctx, db, rqlite.Statement{
			SQL:  "UPDATE imported_entity_state SET lifecycle='deleted',updated_at_unix=? WHERE entity_kind='encrypted_secret' AND source_key=?",
			Args: []any{1_000_001, sourceKey},
		})
	}
	insertReceipt := func(sourceKey, targetID, digest string) rqlite.Statement {
		t.Helper()
		return rqlite.Statement{SQL: `
			INSERT INTO import_delete_receipts(
				entity_kind,source_key,target_id,expected_prior_digest,lifecycle,tombstone_id,
				import_run_id,batch_index,batch_digest,imported_at_unix
			) VALUES('encrypted_secret',?,?,?,'deleted',NULL,?,?,?,?)
		`, Args: []any{sourceKey, targetID, digest, runID, 0, batchDigest, 1_000_001}}
	}

	insertState("schema-secret-one", "schema-target-one", repeatHex("a"))
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE imported_entity_state SET target_id=? WHERE entity_kind='encrypted_secret' AND source_key=?",
		Args: []any{"substituted-target", "schema-secret-one"},
	})
	deleteState("schema-secret-one")
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE imported_entity_state SET lifecycle='active' WHERE entity_kind='encrypted_secret' AND source_key=?",
		Args: []any{"schema-secret-one"},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE imported_entity_state SET canonical_sha256=? WHERE entity_kind='encrypted_secret' AND source_key=?",
		Args: []any{repeatHex("b"), "schema-secret-one"},
	})
	exactReceipt := insertReceipt("schema-secret-one", "schema-target-one", repeatHex("a"))
	mustRequest(t, ctx, db, exactReceipt)
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE import_delete_receipts SET imported_at_unix=? WHERE entity_kind='encrypted_secret' AND source_key=?",
		Args: []any{1_000_002, "schema-secret-one"},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "DELETE FROM import_delete_receipts WHERE entity_kind='encrypted_secret' AND source_key=?",
		Args: []any{"schema-secret-one"},
	})

	insertState("schema-secret-two", "schema-target-two", repeatHex("b"))
	deleteState("schema-secret-two")
	mustRequestFail(t, ctx, db, insertReceipt("schema-secret-two", "schema-target-two", repeatHex("c")))

	insertState("schema-secret-three", "schema-target-three", repeatHex("c"))
	deleteState("schema-secret-three")
	mustRequestFail(t, ctx, db, insertReceipt("schema-secret-three", "substituted-target", repeatHex("c")))
}

func TestCustomerDeleteReceiptRequiresImportedAccessRows(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	runID := "schema-customer-delete-access-run"
	batchDigest := repeatHex("8")
	customerID := "schema-customer-delete-access-id"
	sourceKey := "schema-customer-delete-access"
	priorDigest := repeatHex("7")
	tombstoneID := "schema-customer-delete-access-tombstone"
	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: `
			INSERT INTO import_runs(
				import_run_id,snapshot_kind,source_sha256,plan_sha256,parent_source_sha256,
				target_sha256,batch_count,status,started_at_unix,completed_at_unix
			) VALUES(?,'delta',?,?,?,NULL,1,'applying',?,NULL)
		`, Args: []any{runID, repeatHex("d"), repeatHex("e"), repeatHex("f"), 1_000_000}},
		rqlite.Statement{SQL: `
			INSERT INTO import_batches(
				import_run_id,batch_index,batch_digest,row_count,status,applied_at_unix
			) VALUES(?,?,?,1,'applying',NULL)
		`, Args: []any{runID, 0, batchDigest}},
		rqlite.Statement{SQL: `
			INSERT INTO customers(
				customer_id,display_login,login_key_hmac,status,expires_at_unix,
				generation,created_at_unix,updated_at_unix
			) VALUES(?,?,?,'deleted',0,2,?,?)
		`, Args: []any{customerID, "Schema Delete", repeatHex("4"), 1_000_000, 1_000_001}},
		rqlite.Statement{SQL: `
			INSERT INTO imported_entity_state(
				entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix
			) VALUES('customer',?,?,?,'deleted',?)
		`, Args: []any{sourceKey, customerID, priorDigest, 1_000_001}},
		rqlite.Statement{SQL: `
			INSERT INTO tombstones(tombstone_id,customer_id,generation,reason,created_at_unix)
			VALUES(?,?,2,'legacy_import_delete',?)
		`, Args: []any{tombstoneID, customerID, 1_000_001}},
		rqlite.Statement{SQL: `
			INSERT INTO tombstone_targets(tombstone_id,node_id,service_name,status,applied_at_unix)
			SELECT ?,node_id,service_name,'pending',NULL FROM node_services
			WHERE desired_target=1 AND retired=0
		`, Args: []any{tombstoneID}},
	)
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO import_delete_receipts(
			entity_kind,source_key,target_id,expected_prior_digest,lifecycle,tombstone_id,
			import_run_id,batch_index,batch_digest,imported_at_unix
		) VALUES('customer',?,?,?,'deleted',?,?,?,?,?)
	`, Args: []any{sourceKey, customerID, priorDigest, tombstoneID, runID, 0, batchDigest, 1_000_001}})
}

func TestImportSchemaPreservesImmutableStandaloneSecretEnvelope(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	secretID := "secret-schema-standalone"
	envelope := `{"key_version":1,"nonce_b64":"AAECAwQFBgcICQoL","ciphertext_b64":"c3ludGhldGljLWVuY3J5cHRlZA=="}`
	secretSHA := repeatHex("a")

	mustRequest(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO imported_secrets(
			secret_id,owner_type,owner_source_key,field,kind,key_version,
			secret_envelope,secret_sha256,imported_at_unix
		) VALUES(?,?,?,?,?,?,?,?,?)
	`, Args: []any{
		secretID, "legacy_service", "schema-s3-wb", "token", "bearer", 1,
		envelope, secretSHA, 1_000_000,
	}})

	result := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT owner_type,owner_source_key,field,kind,key_version,secret_envelope,secret_sha256
		FROM imported_secrets WHERE secret_id=?
	`, Args: []any{secretID}})
	if got := fmt.Sprint(result.Rows); got != fmt.Sprintf(
		`[map[field:token key_version:1 kind:bearer owner_source_key:schema-s3-wb owner_type:legacy_service secret_envelope:%s secret_sha256:%s]]`,
		envelope, secretSHA,
	) {
		t.Fatalf("standalone secret envelope = %s", got)
	}

	mustRequest(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO imported_secrets(
			secret_id,owner_type,owner_source_key,field,kind,key_version,
			secret_envelope,secret_sha256,imported_at_unix
		) VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(secret_id) DO UPDATE SET secret_envelope=excluded.secret_envelope
	`, Args: []any{
		secretID, "legacy_service", "schema-s3-wb", "token", "bearer", 1,
		envelope, secretSHA, 1_000_000,
	}})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE imported_secrets SET secret_sha256=? WHERE secret_id=?",
		Args: []any{repeatHex("b"), secretID},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "DELETE FROM imported_secrets WHERE secret_id=?",
		Args: []any{secretID},
	})
}

func TestImportSchemaPreservesProtectedLegacyTrialIdentity(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	secretID := "schema-legacy-trial-salt-v1"
	envelope := `{"key_version":1,"nonce_b64":"AAECAwQFBgcICQoL","ciphertext_b64":"cHJvdGVjdGVkLXRyaWFsLXNhbHQ="}`
	saltSHA := repeatHex("8")
	legacyHMAC := repeatHex("2")
	currentHMAC := repeatHex("3")

	mustRequest(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO imported_secrets(
			secret_id,owner_type,owner_source_key,field,kind,key_version,
			secret_envelope,secret_sha256,imported_at_unix
		) VALUES(?,?,?,?,?,?,?,?,?)
	`, Args: []any{
		secretID, "trial_lookup", "schema-legacy", "salt", "hmac-key", 1,
		envelope, saltSHA, 1_000_000,
	}})
	mustRequest(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO imported_trial_identities(
			source_key,legacy_anchor_hmac,current_hmac,used,expires_at_unix,
			lookup_secret_id,imported_at_unix
		) VALUES(?,?,?,?,?,?,?)
	`, Args: []any{
		"schema-trial-source", legacyHMAC, currentHMAC, 0, 2_000_000,
		secretID, 1_000_000,
	}})

	result := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT legacy_anchor_hmac,current_hmac,used,expires_at_unix,lookup_secret_id
		FROM imported_trial_identities WHERE source_key=?
	`, Args: []any{"schema-trial-source"}})
	if len(result.Rows) != 1 ||
		result.Rows[0]["legacy_anchor_hmac"] != legacyHMAC ||
		result.Rows[0]["current_hmac"] != currentHMAC ||
		fmt.Sprint(result.Rows[0]["used"]) != "0" ||
		fmt.Sprint(result.Rows[0]["expires_at_unix"]) != "2000000" ||
		result.Rows[0]["lookup_secret_id"] != secretID {
		t.Fatalf("protected trial identity = %#v", result.Rows)
	}

	mustRequest(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE imported_trial_identities SET used=1 WHERE source_key=?",
		Args: []any{"schema-trial-source"},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE imported_trial_identities SET used=0 WHERE source_key=?",
		Args: []any{"schema-trial-source"},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE imported_trial_identities SET legacy_anchor_hmac=? WHERE source_key=?",
		Args: []any{repeatHex("4"), "schema-trial-source"},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE imported_trial_identities SET current_hmac=? WHERE source_key=?",
		Args: []any{repeatHex("5"), "schema-trial-source"},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  "UPDATE imported_trial_identities SET expires_at_unix=? WHERE source_key=?",
		Args: []any{2_000_001, "schema-trial-source"},
	})
}

func schemaColumnNames(t *testing.T, result rqlite.Result) []string {
	t.Helper()
	columns := make([]string, 0, len(result.Rows))
	for _, row := range result.Rows {
		name, ok := row["name"].(string)
		if !ok || name == "" {
			t.Fatalf("malformed PRAGMA table_info row: %#v", row)
		}
		columns = append(columns, name)
	}
	sort.Strings(columns)
	return columns
}

func TestConstraintPaymentsAndIdempotencyFailClosed(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: `
			INSERT INTO customers(
				customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix
			) VALUES(?,?,?,?,?,?,?,?)
		`, Args: []any{"constraint-customer", "ConstraintCustomer", repeatHex("a"), "active", 200000, 1, 100000, 100000}},
	)

	insertOrder := func(orderID string, created int64) {
		t.Helper()
		mustRequest(t, ctx, db, rqlite.Statement{SQL: `
			INSERT INTO orders(
				order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
				amount_minor,currency,duration_days,created_at_unix,expires_at_unix,
				payment_state,provisioning_state,operation_id
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		`, Args: []any{
			orderID, "CODE-" + orderID, "telegram", repeatHex("b"), "constraint-customer", "tariff_1m_v1",
			40000, "RUB", 30, created, created + 86400,
			string(PaymentPending), string(ProvisioningPending), "operation-" + orderID,
		}})
	}
	insertOrder("constraint-order-1", 100001)
	insertOrder("constraint-order-2", 100002)
	insertOrder("constraint-order-3", 100003)

	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO payments(payment_id,order_id,provider,amount_minor,currency,confirmed_at_unix)
		VALUES(?,?,?,?,?,?)
	`, Args: []any{"bad-amount", "constraint-order-1", "manual", 0, "RUB", 100010}})
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO payments(payment_id,order_id,provider,amount_minor,currency,confirmed_at_unix)
		VALUES(?,?,?,?,?,?)
	`, Args: []any{"bad-currency", "constraint-order-1", "manual", 40000, "USD", 100010}})

	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: "UPDATE orders SET payment_state='confirmed', decision='confirmed', confirmed_at_unix=? WHERE order_id=?", Args: []any{100010, "constraint-order-1"}},
		rqlite.Statement{SQL: `
			INSERT INTO payments(payment_id,order_id,provider,provider_event_id,receipt_ref,amount_minor,currency,confirmed_at_unix)
			VALUES(?,?,?,?,?,?,?,?)
		`, Args: []any{"payment-1", "constraint-order-1", "manual", nil, nil, 40000, "RUB", 100010}},
		rqlite.Statement{SQL: "UPDATE orders SET payment_state='confirmed', decision='confirmed', confirmed_at_unix=? WHERE order_id=?", Args: []any{100011, "constraint-order-2"}},
		rqlite.Statement{SQL: `
			INSERT INTO payments(payment_id,order_id,provider,provider_event_id,receipt_ref,amount_minor,currency,confirmed_at_unix)
			VALUES(?,?,?,?,?,?,?,?)
		`, Args: []any{"payment-2", "constraint-order-2", "manual", "provider-event", nil, 40000, "RUB", 100011}},
	)

	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: "UPDATE orders SET payment_state='confirmed', decision='confirmed', confirmed_at_unix=? WHERE order_id=?", Args: []any{100012, "constraint-order-3"}},
	)
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO payments(payment_id,order_id,provider,provider_event_id,amount_minor,currency,confirmed_at_unix)
		VALUES(?,?,?,?,?,?,?)
	`, Args: []any{"payment-3", "constraint-order-3", "manual", "provider-event", 40000, "RUB", 100012}})

	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO idempotency_requests(
			scope,command_type,idempotency_key,request_hash,resource_id,decision,
			operation_id,status,response_json,created_at_unix
		) VALUES(?,?,?,?,?,?,?,?,?,?)
	`, Args: []any{"owner", "confirm", "key-1", "short", "payment-1", "payment_confirmed", "idem-op-1", "applied", `{}`, 100020}})
}

func TestConstraintAuditIsAppendOnlyAndForeignKeysDeclareDeleteActions(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	mustRequest(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,created_at_unix)
		VALUES(?,?,?,?,?,?)
	`, Args: []any{"audit-1", repeatHex("c"), "test", "schema", repeatHex("d"), 100030}})
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: "UPDATE audit_events SET action='changed' WHERE event_id='audit-1'"})
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: "DELETE FROM audit_events WHERE event_id='audit-1'"})

	tables := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name
	`})
	for _, row := range tables.Rows {
		name, ok := row["name"].(string)
		if !ok {
			t.Fatalf("malformed table row: %#v", row)
		}
		foreignKeys := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: fmt.Sprintf("PRAGMA foreign_key_list(%q)", name)})
		for _, fk := range foreignKeys.Rows {
			if fk["on_delete"] == "NO ACTION" {
				t.Fatalf("table %s has FK without explicit ON DELETE: %#v", name, fk)
			}
		}
	}
}

func mustAppliedSchema(t *testing.T) (context.Context, rqlite.RQLite) {
	t.Helper()
	db := mustIntegrationRQLite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return ctx, db
}

func mustStrongQuery(t *testing.T, ctx context.Context, db rqlite.RQLite, statement rqlite.Statement) rqlite.Result {
	t.Helper()
	results, err := db.QueryStrong(ctx, statement)
	if err != nil {
		t.Fatalf("QueryStrong: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("QueryStrong returned %d results, want 1", len(results))
	}
	return results[0]
}

func mustRequest(t *testing.T, ctx context.Context, db rqlite.RQLite, statements ...rqlite.Statement) {
	t.Helper()
	if _, err := db.Request(ctx, rqlite.Linearizable, true, statements...); err != nil {
		t.Fatalf("Request: %v", err)
	}
}

func mustRequestFail(t *testing.T, ctx context.Context, db rqlite.RQLite, statement rqlite.Statement) {
	t.Helper()
	if _, err := db.Request(ctx, rqlite.Linearizable, true, statement); err == nil {
		t.Fatalf("Request unexpectedly accepted %s", statement.SQL)
	}
}

func repeatHex(value string) string {
	return strings.Repeat(value, 64)
}

func TestBackupRPOSchemaFreezesDurableColumnsAndSeed(t *testing.T) {
	ctx, db := mustAppliedSchema(t)

	stateColumns := schemaColumnNames(t, mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: "PRAGMA table_info(backup_rpo_state)"}))
	wantStateColumns := []string{
		"dirty_generation",
		"last_attempt_sequence",
		"phase",
		"restore_epoch",
		"singleton_id",
		"updated_at_unix",
		"verified_at_unix",
		"verified_backup_id",
		"verified_generation",
		"verified_manifest_version",
		"verified_object_key",
		"verified_object_sha256",
		"verified_object_version",
		"verified_size_bytes",
	}
	if fmt.Sprint(stateColumns) != fmt.Sprint(wantStateColumns) {
		t.Fatalf("backup_rpo_state columns = %v, want %v", stateColumns, wantStateColumns)
	}

	attemptColumns := schemaColumnNames(t, mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: "PRAGMA table_info(backup_rpo_attempts)"}))
	wantAttemptColumns := []string{
		"adapter_contract_version",
		"attempt_sequence",
		"backup_id",
		"capability_evidence_sha256",
		"capability_expires_at_unix",
		"capability_generation",
		"captured_generation",
		"created_at_unix",
		"failure_code",
		"lease_fence",
		"lease_holder_id",
		"lease_token",
		"manifest_version",
		"object_key",
		"object_sha256",
		"object_size_bytes",
		"object_version",
		"phase",
		"restore_epoch",
		"updated_at_unix",
	}
	if fmt.Sprint(attemptColumns) != fmt.Sprint(wantAttemptColumns) {
		t.Fatalf("backup_rpo_attempts columns = %v, want %v", attemptColumns, wantAttemptColumns)
	}

	indexes := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: "PRAGMA index_list(backup_rpo_attempts)"})
	hasRestoreScopedAttemptSequence := false
	for _, index := range indexes.Rows {
		if fmt.Sprint(index["unique"]) != "1" {
			continue
		}
		indexName, ok := index["name"].(string)
		if !ok || indexName == "" {
			t.Fatalf("malformed backup_rpo_attempts index row: %#v", index)
		}
		indexInfo := mustStrongQuery(t, ctx, db, rqlite.Statement{
			SQL: fmt.Sprintf("PRAGMA index_info(%q)", indexName),
		})
		indexColumns := make([]string, 0, len(indexInfo.Rows))
		for _, column := range indexInfo.Rows {
			columnName, ok := column["name"].(string)
			if !ok || columnName == "" {
				t.Fatalf("malformed backup_rpo_attempts index column: %#v", column)
			}
			indexColumns = append(indexColumns, columnName)
		}
		if fmt.Sprint(indexColumns) == "[restore_epoch attempt_sequence]" {
			hasRestoreScopedAttemptSequence = true
			break
		}
	}
	if !hasRestoreScopedAttemptSequence {
		t.Fatal("backup_rpo_attempts lacks UNIQUE(restore_epoch,attempt_sequence)")
	}

	leaseColumns := schemaColumnNames(t, mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: "PRAGMA table_info(cluster_job_leases)"}))
	wantLeaseColumns := []string{
		"acquired_at_unix",
		"capability_evidence_sha256",
		"capability_expires_at_unix",
		"capability_generation",
		"expires_at_unix",
		"holder_id",
		"job_name",
		"lease_fence",
		"lease_token",
		"restore_epoch",
	}
	if fmt.Sprint(leaseColumns) != fmt.Sprint(wantLeaseColumns) {
		t.Fatalf("cluster_job_leases columns = %v, want %v", leaseColumns, wantLeaseColumns)
	}

	seed := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT backup.restore_epoch, backup.dirty_generation,
		       backup.verified_generation, backup.last_attempt_sequence,
		       backup.phase
		FROM backup_rpo_state AS backup
		JOIN cluster_restore_state AS restore
		  ON restore.restore_epoch = backup.restore_epoch
		WHERE backup.singleton_id = 1
		  AND backup.dirty_generation = 1
		  AND backup.verified_generation = 0
		  AND backup.last_attempt_sequence = 0
		  AND backup.phase = 'dirty'
		  AND backup.verified_backup_id IS NULL
		  AND backup.verified_object_key IS NULL
		  AND backup.verified_object_sha256 IS NULL
		  AND backup.verified_object_version IS NULL
		  AND backup.verified_size_bytes IS NULL
		  AND backup.verified_manifest_version IS NULL
		  AND backup.verified_at_unix IS NULL
	`})
	if got := fmt.Sprint(seed.Rows); got != "[map[dirty_generation:1 last_attempt_sequence:0 phase:dirty restore_epoch:1 verified_generation:0]]" {
		t.Fatalf("backup RPO seed = %s", got)
	}
}

func TestBackupRPOSchemaRejectsInconsistentStateAndUnfencedAttempts(t *testing.T) {
	ctx, db := mustAppliedSchema(t)

	type rejection struct {
		name      string
		statement rqlite.Statement
	}

	backupID := strings.Repeat("a", 32)
	verifiedArgs := func(dirtyGeneration, verifiedGeneration int64) []any {
		return []any{
			dirtyGeneration,
			verifiedGeneration,
			int64(1),
			backupID,
			fmt.Sprintf("backups/g-%d/a-1-%s.tar.gpg", verifiedGeneration, backupID),
			repeatHex("a"),
			"version-1",
			int64(4_096),
			int64(2),
			int64(1_000_000),
		}
	}
	verifiedStatement := func(args []any) rqlite.Statement {
		return rqlite.Statement{SQL: `
			UPDATE backup_rpo_state
			SET dirty_generation=?,
			    verified_generation=?,
			    last_attempt_sequence=?,
			    verified_backup_id=?,
			    verified_object_key=?,
			    verified_object_sha256=?,
			    verified_object_version=?,
			    verified_size_bytes=?,
			    verified_manifest_version=?,
			    verified_at_unix=?
			WHERE singleton_id=1
		`, Args: args}
	}

	stateCases := []rejection{
		{
			name: "second-singleton-row",
			statement: rqlite.Statement{SQL: `
				INSERT INTO backup_rpo_state(
					singleton_id,restore_epoch,dirty_generation,verified_generation,
					last_attempt_sequence,phase,updated_at_unix
				) VALUES(2,1,1,0,0,'dirty',1000000)
			`},
		},
		{
			name:      "verified-generation-exceeds-dirty",
			statement: verifiedStatement(verifiedArgs(1, 2)),
		},
		{
			name:      "invalid-state-phase",
			statement: rqlite.Statement{SQL: "UPDATE backup_rpo_state SET phase='invalid' WHERE singleton_id=1"},
		},
		{
			name:      "negative-dirty-generation",
			statement: rqlite.Statement{SQL: "UPDATE backup_rpo_state SET dirty_generation=-1 WHERE singleton_id=1"},
		},
		{
			name:      "negative-verified-generation",
			statement: rqlite.Statement{SQL: "UPDATE backup_rpo_state SET verified_generation=-1 WHERE singleton_id=1"},
		},
		{
			name:      "negative-last-attempt-sequence",
			statement: rqlite.Statement{SQL: "UPDATE backup_rpo_state SET last_attempt_sequence=-1 WHERE singleton_id=1"},
		},
		{
			name:      "zero-restore-epoch",
			statement: rqlite.Statement{SQL: "UPDATE backup_rpo_state SET restore_epoch=0 WHERE singleton_id=1"},
		},
		{
			name:      "negative-restore-epoch",
			statement: rqlite.Statement{SQL: "UPDATE backup_rpo_state SET restore_epoch=-1 WHERE singleton_id=1"},
		},
		{
			name:      "zero-updated-at",
			statement: rqlite.Statement{SQL: "UPDATE backup_rpo_state SET updated_at_unix=0 WHERE singleton_id=1"},
		},
		{
			name:      "negative-updated-at",
			statement: rqlite.Statement{SQL: "UPDATE backup_rpo_state SET updated_at_unix=-1 WHERE singleton_id=1"},
		},
	}

	fullTuple := verifiedArgs(2, 1)
	partialFields := []struct {
		name  string
		index int
	}{
		{name: "verified-generation", index: 1},
		{name: "verified-backup-id", index: 3},
		{name: "verified-object-key", index: 4},
		{name: "verified-object-sha256", index: 5},
		{name: "verified-object-version", index: 6},
		{name: "verified-size-bytes", index: 7},
		{name: "verified-manifest-version", index: 8},
		{name: "verified-at-unix", index: 9},
	}
	for _, field := range partialFields {
		args := append([]any(nil), fullTuple...)
		args[field.index] = nil
		stateCases = append(stateCases, rejection{
			name:      "partial-tuple-missing-" + field.name,
			statement: verifiedStatement(args),
		})
	}
	for _, testCase := range []struct {
		name  string
		index int
		value any
	}{
		{name: "uppercase-verified-sha256", index: 5, value: strings.Repeat("A", 64)},
		{name: "short-verified-sha256", index: 5, value: strings.Repeat("a", 63)},
		{name: "long-verified-sha256", index: 5, value: strings.Repeat("a", 65)},
		{name: "nonhex-verified-sha256", index: 5, value: strings.Repeat("g", 64)},
		{name: "whitespace-verified-sha256", index: 5, value: " " + strings.Repeat("a", 63)},
		{name: "empty-verified-object-version", index: 6, value: ""},
		{name: "whitespace-verified-object-version", index: 6, value: " version-1"},
		{name: "latest-verified-object-version", index: 6, value: "latest"},
		{name: "uppercase-latest-verified-object-version", index: 6, value: "LATEST"},
		{name: "null-literal-verified-object-version", index: 6, value: "null"},
		{name: "none-literal-verified-object-version", index: 6, value: "none"},
		{name: "zero-verified-size", index: 7, value: int64(0)},
		{name: "negative-verified-size", index: 7, value: int64(-1)},
		{name: "zero-verified-manifest-version", index: 8, value: int64(0)},
		{name: "negative-verified-manifest-version", index: 8, value: int64(-1)},
		{name: "wrong-verified-manifest-version", index: 8, value: int64(1)},
		{name: "zero-verified-at", index: 9, value: int64(0)},
		{name: "negative-verified-at", index: 9, value: int64(-1)},
		{name: "zero-last-attempt-with-verified-tuple", index: 2, value: int64(0)},
		{name: "negative-last-attempt-with-verified-tuple", index: 2, value: int64(-1)},
	} {
		args := append([]any(nil), fullTuple...)
		args[testCase.index] = testCase.value
		stateCases = append(stateCases, rejection{
			name:      testCase.name,
			statement: verifiedStatement(args),
		})
	}
	for _, testCase := range stateCases {
		t.Run("state/"+testCase.name, func(t *testing.T) {
			mustRequestFail(t, ctx, db, testCase.statement)
		})
	}

	const attemptSQL = `
		INSERT INTO backup_rpo_attempts(
			restore_epoch,attempt_sequence,phase,backup_id,captured_generation,
			object_key,object_sha256,object_version,object_size_bytes,
			manifest_version,adapter_contract_version,capability_generation,
			capability_evidence_sha256,capability_expires_at_unix,
			lease_holder_id,lease_token,lease_fence,failure_code,
			created_at_unix,updated_at_unix
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`
	attemptArgs := func(sequence int64, hexDigit string) []any {
		attemptBackupID := strings.Repeat(hexDigit, 32)
		return []any{
			int64(1),
			sequence,
			"applied",
			attemptBackupID,
			int64(1),
			fmt.Sprintf("backups/g-1/a-%d-%s.tar.gpg", sequence, attemptBackupID),
			strings.Repeat(hexDigit, 64),
			fmt.Sprintf("version-%d", sequence),
			int64(4_096),
			int64(2),
			"yandex-s3-v1",
			int64(1),
			repeatHex("c"),
			int64(2_000_000),
			"node-s2",
			fmt.Sprintf("lease-token-%d", sequence),
			int64(1),
			nil,
			int64(1_000_000),
			int64(1_000_001),
		}
	}
	attemptStatement := func(args []any) rqlite.Statement {
		return rqlite.Statement{SQL: attemptSQL, Args: args}
	}

	mustRequest(t, ctx, db, attemptStatement(attemptArgs(1, "a")))

	attemptCases := []rejection{
		{
			name:      "globally-duplicate-attempt-sequence",
			statement: attemptStatement(attemptArgs(1, "b")),
		},
	}
	nextSequence := int64(10)
	addAttemptCase := func(name string, index int, value any) {
		args := attemptArgs(nextSequence, "b")
		nextSequence++
		args[index] = value
		attemptCases = append(attemptCases, rejection{
			name:      name,
			statement: attemptStatement(args),
		})
	}
	addAttemptCase("invalid-attempt-phase", 2, "invalid")
	addAttemptCase("null-object-version-for-applied", 7, nil)
	addAttemptCase("empty-object-version-for-applied", 7, "")
	addAttemptCase("whitespace-object-version-for-applied", 7, " version-1")
	addAttemptCase("latest-object-version-for-applied", 7, "latest")
	addAttemptCase("uppercase-latest-object-version-for-applied", 7, "LATEST")
	addAttemptCase("null-literal-object-version-for-applied", 7, "null")
	addAttemptCase("none-literal-object-version-for-applied", 7, "none")
	addAttemptCase("uppercase-object-sha256", 6, strings.Repeat("B", 64))
	addAttemptCase("short-object-sha256", 6, strings.Repeat("b", 63))
	addAttemptCase("long-object-sha256", 6, strings.Repeat("b", 65))
	addAttemptCase("nonhex-object-sha256", 6, strings.Repeat("g", 64))
	addAttemptCase("whitespace-object-sha256", 6, " " + strings.Repeat("b", 63))
	addAttemptCase("uppercase-capability-sha256", 12, strings.Repeat("C", 64))
	addAttemptCase("short-capability-sha256", 12, strings.Repeat("c", 63))
	addAttemptCase("long-capability-sha256", 12, strings.Repeat("c", 65))
	addAttemptCase("nonhex-capability-sha256", 12, strings.Repeat("g", 64))
	addAttemptCase("whitespace-capability-sha256", 12, " " + strings.Repeat("c", 63))
	addAttemptCase("wrong-adapter-contract", 10, "aws-s3-v1")
	addAttemptCase("empty-adapter-contract", 10, "")
	addAttemptCase("manifest-v1", 9, int64(1))
	addAttemptCase("zero-restore-epoch", 0, int64(0))
	addAttemptCase("negative-restore-epoch", 0, int64(-1))
	addAttemptCase("zero-attempt-sequence", 1, int64(0))
	addAttemptCase("negative-attempt-sequence", 1, int64(-1))
	addAttemptCase("zero-captured-generation", 4, int64(0))
	addAttemptCase("negative-captured-generation", 4, int64(-1))
	addAttemptCase("zero-object-size", 8, int64(0))
	addAttemptCase("negative-object-size", 8, int64(-1))
	addAttemptCase("zero-manifest-version", 9, int64(0))
	addAttemptCase("negative-manifest-version", 9, int64(-1))
	addAttemptCase("zero-capability-generation", 11, int64(0))
	addAttemptCase("negative-capability-generation", 11, int64(-1))
	addAttemptCase("zero-capability-expiry", 13, int64(0))
	addAttemptCase("negative-capability-expiry", 13, int64(-1))
	addAttemptCase("empty-lease-holder", 14, "")
	addAttemptCase("empty-lease-token", 15, "")
	addAttemptCase("zero-lease-fence", 16, int64(0))
	addAttemptCase("negative-lease-fence", 16, int64(-1))
	addAttemptCase("zero-created-at", 18, int64(0))
	addAttemptCase("negative-created-at", 18, int64(-1))
	addAttemptCase("zero-updated-at", 19, int64(0))
	addAttemptCase("negative-updated-at", 19, int64(-1))

	for _, testCase := range attemptCases {
		t.Run("attempt/"+testCase.name, func(t *testing.T) {
			mustRequestFail(t, ctx, db, testCase.statement)
		})
	}

	const leaseSQL = `
		INSERT INTO cluster_job_leases(
			job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,
			restore_epoch,lease_fence,capability_generation,
			capability_evidence_sha256,capability_expires_at_unix
		) VALUES(?,?,?,?,?,?,?,?,?,?)
	`
	leaseArgs := func() []any {
		return []any{
			"backup-rpo",
			"node-s2",
			"backup-rpo-lease-token",
			int64(1_000_000),
			int64(1_000_100),
			int64(1),
			int64(1),
			int64(1),
			repeatHex("d"),
			int64(1_000_200),
		}
	}
	leaseStatement := func(args []any) rqlite.Statement {
		return rqlite.Statement{SQL: leaseSQL, Args: args}
	}
	leaseCases := make([]rejection, 0, 16)
	addLeaseCase := func(name string, index int, value any) {
		args := leaseArgs()
		args[index] = value
		leaseCases = append(leaseCases, rejection{
			name:      name,
			statement: leaseStatement(args),
		})
	}
	addLeaseCase("zero-restore-epoch", 5, int64(0))
	addLeaseCase("negative-restore-epoch", 5, int64(-1))
	addLeaseCase("zero-lease-fence", 6, int64(0))
	addLeaseCase("negative-lease-fence", 6, int64(-1))
	addLeaseCase("zero-capability-generation", 7, int64(0))
	addLeaseCase("negative-capability-generation", 7, int64(-1))
	addLeaseCase("uppercase-capability-sha256", 8, strings.Repeat("D", 64))
	addLeaseCase("short-capability-sha256", 8, strings.Repeat("d", 63))
	addLeaseCase("long-capability-sha256", 8, strings.Repeat("d", 65))
	addLeaseCase("nonhex-capability-sha256", 8, strings.Repeat("g", 64))
	addLeaseCase("whitespace-capability-sha256", 8, " " + strings.Repeat("d", 63))
	addLeaseCase("zero-capability-expiry", 9, int64(0))
	addLeaseCase("negative-capability-expiry", 9, int64(-1))
	for _, testCase := range leaseCases {
		t.Run("lease/"+testCase.name, func(t *testing.T) {
			mustRequestFail(t, ctx, db, testCase.statement)
		})
	}
}
