package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type RQLiteApplyStore struct {
	db              rqlite.RQLite
	now             func() time.Time
	trialProtection *TrialImportProtection
}

// TrialImportProtection contains only the already encrypted legacy lookup
// salt. Plaintext salt is never part of an import plan, report or SQL args.
type TrialImportProtection struct {
	KeyVersion            int
	EncryptedSaltEnvelope string
	SaltSHA256            string
}

const legacyTrialSaltSecretID = "legacy-trial-salt-v1"

var _ ApplyStore = (*RQLiteApplyStore)(nil)

func NewRQLiteApplyStore(db rqlite.RQLite, now func() time.Time) (*RQLiteApplyStore, error) {
	if db == nil || now == nil {
		return nil, errors.New("rqlite apply store dependencies are required")
	}
	return &RQLiteApplyStore{db: db, now: now}, nil
}

func NewRQLiteApplyStoreWithTrialProtection(
	db rqlite.RQLite,
	now func() time.Time,
	protection TrialImportProtection,
) (*RQLiteApplyStore, error) {
	store, err := NewRQLiteApplyStore(db, now)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		KeyVersion    int    `json:"key_version"`
		NonceB64      string `json:"nonce_b64"`
		CiphertextB64 string `json:"ciphertext_b64"`
	}
	if protection.KeyVersion <= 0 || len(protection.SaltSHA256) != 64 ||
		decodeCanonicalOperation([]byte(protection.EncryptedSaltEnvelope), &envelope) != nil ||
		envelope.KeyVersion != protection.KeyVersion || envelope.NonceB64 == "" ||
		envelope.CiphertextB64 == "" {
		return nil, errors.New("invalid protected legacy trial salt")
	}
	copyProtection := protection
	store.trialProtection = &copyProtection
	return store, nil
}

func (s *RQLiteApplyStore) ReadAppliedRunEvidence(
	ctx context.Context,
	runID string,
) (AppliedRunEvidence, error) {
	if s == nil || s.db == nil || runID == "" {
		return AppliedRunEvidence{}, errors.New("invalid applied-run evidence request")
	}
	results, err := s.db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT import_run_id,snapshot_kind,source_sha256,plan_sha256,
parent_source_sha256,target_sha256,batch_count,status,completed_at_unix
FROM import_runs WHERE import_run_id=?`, Args: []any{runID}},
		rqlite.Statement{SQL: `SELECT batch_index,batch_digest,status
FROM import_batches WHERE import_run_id=? ORDER BY batch_index`, Args: []any{runID}},
	)
	if err != nil || len(results) != 2 || len(results[0].Rows) != 1 {
		return AppliedRunEvidence{}, errors.New("applied-run evidence is unavailable")
	}
	row := results[0].Rows[0]
	actualRunID, runOK := row["import_run_id"].(string)
	kind, kindOK := row["snapshot_kind"].(string)
	source, sourceOK := row["source_sha256"].(string)
	plan, planOK := row["plan_sha256"].(string)
	parent, parentOK := nullableApplyString(row["parent_source_sha256"])
	target, targetOK := row["target_sha256"].(string)
	status, statusOK := row["status"].(string)
	batchCount, batchOK := applyRowInt(row["batch_count"])
	completedAt, completedOK := applyRowInt(row["completed_at_unix"])
	if !runOK || actualRunID != runID || !kindOK || !sourceOK || !planOK ||
		!parentOK || !targetOK || !statusOK || status != "applied" ||
		!batchOK || batchCount < 0 || int64(int(batchCount)) != batchCount ||
		!completedOK || completedAt < 0 {
		return AppliedRunEvidence{}, errors.New("invalid applied-run evidence")
	}
	if len(results[1].Rows) != int(batchCount) {
		return AppliedRunEvidence{}, errors.New("applied-run batch evidence count mismatch")
	}
	type canonicalBatchEvidence struct {
		Index  int    `json:"index"`
		Digest string `json:"digest"`
	}
	batches := make([]canonicalBatchEvidence, len(results[1].Rows))
	for index, batchRow := range results[1].Rows {
		rowIndex, indexOK := applyRowInt(batchRow["batch_index"])
		digest, digestOK := batchRow["batch_digest"].(string)
		batchStatus, statusOK := batchRow["status"].(string)
		if !indexOK || rowIndex != int64(index) || !digestOK ||
			!validCanonicalSHA256(digest) || !statusOK || batchStatus != "applied" {
			return AppliedRunEvidence{}, errors.New("invalid applied-run batch evidence")
		}
		batches[index] = canonicalBatchEvidence{Index: index, Digest: digest}
	}
	encoded, err := json.Marshal(batches)
	if err != nil {
		return AppliedRunEvidence{}, errors.New("cannot encode applied-run batch evidence")
	}
	evidence := AppliedRunEvidence{
		RunID:              actualRunID,
		SnapshotKind:       kind,
		SourceDigest:       source,
		PlanDigest:         plan,
		ParentDigest:       parent,
		TargetDigest:       target,
		BatchCount:         int(batchCount),
		BatchReceiptDigest: sha256Hex(encoded),
		CompletedAtUnix:    completedAt,
	}
	if !validAppliedRunEvidence(evidence) {
		return AppliedRunEvidence{}, errors.New("invalid applied-run evidence")
	}
	return evidence, nil
}

func (s *RQLiteApplyStore) ReadReferencedKeyVersions(ctx context.Context) ([]int, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("rqlite apply store is unavailable")
	}
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT key_version FROM (
    SELECT i.key_version AS key_version
    FROM imported_secrets i
    WHERE NOT EXISTS(
        SELECT 1 FROM imported_entity_state state
        WHERE state.entity_kind='encrypted_secret'
          AND state.target_id=i.secret_id
          AND state.lifecycle='deleted'
    )
    UNION
    SELECT key_version FROM setting_secrets
) ORDER BY key_version`})
	if err != nil {
		return nil, errors.New("cannot read referenced secret key versions")
	}
	if len(results) != 1 {
		return nil, errors.New("invalid referenced secret key version result count")
	}
	seen := make(map[int]struct{}, len(results[0].Rows))
	versions := make([]int, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		if _, encodedAsText := row["key_version"].(string); encodedAsText {
			return nil, errors.New("invalid referenced secret key version")
		}
		value, ok := applyRowInt(row["key_version"])
		if !ok || value <= 0 || int64(int(value)) != value {
			return nil, errors.New("invalid referenced secret key version")
		}
		version := int(value)
		if _, duplicate := seen[version]; duplicate {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	sort.Ints(versions)
	return versions, nil
}

type businessDigestTable struct {
	Name string           `json:"name"`
	Rows []map[string]any `json:"rows"`
}

var businessDigestQueries = []struct {
	name string
	sql  string
}{
	{"customers", `SELECT c.* FROM customers c WHERE NOT EXISTS(
SELECT 1 FROM imported_entity_state s WHERE s.entity_kind='customer'
AND s.target_id=c.customer_id AND s.lifecycle='deleted') ORDER BY c.customer_id`},
	{"credentials", `SELECT c.* FROM credentials c WHERE NOT EXISTS(
SELECT 1 FROM imported_entity_state s WHERE s.entity_kind='customer'
AND s.target_id=c.customer_id AND s.lifecycle='deleted') ORDER BY c.credential_id`},
	{"subscription_tokens", `SELECT t.* FROM subscription_tokens t WHERE NOT EXISTS(
SELECT 1 FROM imported_entity_state s WHERE s.entity_kind='customer'
AND s.target_id=t.customer_id AND s.lifecycle='deleted') ORDER BY t.token_id`},
	{"orders", "SELECT * FROM orders ORDER BY order_id"},
	{"trial_redemptions", "SELECT * FROM trial_redemptions ORDER BY redemption_id"},
	{"desired_node_state", `SELECT d.* FROM desired_node_state d WHERE NOT EXISTS(
SELECT 1 FROM imported_entity_state s WHERE s.entity_kind='customer'
AND s.target_id=d.customer_id AND s.lifecycle='deleted') ORDER BY d.customer_id,d.node_id,d.service_name`},
	{"desired_protocol_tags", `SELECT p.* FROM desired_protocol_tags p WHERE NOT EXISTS(
SELECT 1 FROM imported_entity_state s WHERE s.entity_kind='customer'
AND s.target_id=p.customer_id AND s.lifecycle='deleted') ORDER BY p.customer_id,p.node_id,p.service_name,p.protocol_tag`},
	{"telegram_bot_routes", "SELECT * FROM telegram_bot_routes ORDER BY bot_identity_hmac"},
	{"telegram_bot_credential_rotations", "SELECT * FROM telegram_bot_credential_rotations ORDER BY audit_digest"},
	{"telegram_pollers", "SELECT * FROM telegram_pollers ORDER BY bot_identity_hmac"},
	{"telegram_imported_callbacks", "SELECT * FROM telegram_imported_callbacks ORDER BY callback_hmac"},
	{"telegram_callbacks", "SELECT * FROM telegram_callbacks ORDER BY callback_hmac"},
	{"telegram_bindings", "SELECT * FROM telegram_bindings ORDER BY binding_id"},
	{"cluster_settings", "SELECT * FROM cluster_settings ORDER BY setting_key"},
	{"setting_members", "SELECT * FROM setting_members ORDER BY setting_key,member_key"},
	{"setting_secrets", "SELECT * FROM setting_secrets ORDER BY setting_key"},
	{"imported_secrets", `SELECT i.* FROM imported_secrets i WHERE NOT EXISTS(
SELECT 1 FROM imported_entity_state s WHERE s.entity_kind='encrypted_secret'
AND s.target_id=i.secret_id AND s.lifecycle='deleted') ORDER BY i.secret_id`},
	{"imported_trial_identities", "SELECT * FROM imported_trial_identities ORDER BY source_key"},
	{"principals", "SELECT * FROM principals ORDER BY principal_id"},
	{"principal_roles", "SELECT * FROM principal_roles ORDER BY principal_id,role_name"},
	{"principal_credentials", "SELECT * FROM principal_credentials ORDER BY credential_id"},
}

func (s *RQLiteApplyStore) InspectTarget(ctx context.Context) (TargetState, error) {
	statements := make([]rqlite.Statement, 0, len(businessDigestQueries)+1)
	for _, query := range businessDigestQueries {
		statements = append(statements, rqlite.Statement{SQL: query.sql})
	}
	statements = append(statements, rqlite.Statement{SQL: `SELECT source_sha256 FROM import_runs
WHERE status='applied' ORDER BY completed_at_unix DESC,import_run_id DESC LIMIT 1`})
	results, err := s.db.QueryLinearizable(ctx, statements...)
	if err != nil {
		return TargetState{}, err
	}
	if len(results) != len(statements) {
		return TargetState{}, errors.New("invalid business digest result count")
	}
	tables := make([]businessDigestTable, len(businessDigestQueries))
	empty := true
	for index, query := range businessDigestQueries {
		rows := results[index].Rows
		if rows == nil {
			rows = []map[string]any{}
		}
		if len(rows) != 0 {
			empty = false
		}
		tables[index] = businessDigestTable{Name: query.name, Rows: rows}
	}
	encoded, err := json.Marshal(tables)
	if err != nil {
		return TargetState{}, errors.New("cannot encode canonical business digest")
	}
	state := TargetState{Empty: empty, BusinessDigest: sha256Hex(encoded)}
	latest := results[len(results)-1].Rows
	if len(latest) > 1 {
		return TargetState{}, errors.New("ambiguous applied import source")
	}
	if len(latest) == 1 {
		source, ok := latest[0]["source_sha256"].(string)
		if !ok || source == "" {
			return TargetState{}, errors.New("invalid applied import source")
		}
		state.AppliedSourceDigest = source
	}
	return state, nil
}

func (s *RQLiteApplyStore) ReadShadowProjection(ctx context.Context, expectedSourceDigest string) (ShadowProjection, error) {
	if !validShadowHex64(expectedSourceDigest) {
		return ShadowProjection{}, ErrShadowExportUnavailable
	}
	statements := make([]rqlite.Statement, 0, len(businessDigestQueries)+6)
	for _, query := range businessDigestQueries {
		statements = append(statements, rqlite.Statement{SQL: query.sql})
	}
	statements = append(statements,
		rqlite.Statement{SQL: `SELECT import_run_id,source_sha256,target_sha256,batch_count,status,
(SELECT COUNT(*) FROM import_batches b
 WHERE b.import_run_id=r.import_run_id AND b.status='applied') AS applied_batch_count
FROM import_runs r WHERE source_sha256=?
ORDER BY completed_at_unix DESC,import_run_id`, Args: []any{expectedSourceDigest}},
		rqlite.Statement{SQL: `SELECT c.customer_id,c.login_key_hmac,c.status,c.expires_at_unix,c.generation,
EXISTS(SELECT 1 FROM credentials e WHERE e.customer_id=c.customer_id AND e.enabled=1) AS credential_enabled,
(SELECT COUNT(*) FROM credentials e WHERE e.customer_id=c.customer_id AND e.enabled=1) AS credential_count,
EXISTS(SELECT 1 FROM subscription_tokens t WHERE t.customer_id=c.customer_id AND t.revoked=0) AS token_active,
(SELECT COUNT(*) FROM subscription_tokens t WHERE t.customer_id=c.customer_id AND t.revoked=0) AS token_count
FROM customers c WHERE c.status='active' AND NOT EXISTS(
 SELECT 1 FROM imported_entity_state s WHERE s.entity_kind='customer'
 AND s.target_id=c.customer_id AND s.lifecycle='deleted')
ORDER BY c.customer_id`},
		rqlite.Statement{SQL: `SELECT d.customer_id,d.node_id,d.service_name,d.generation,d.status,p.protocol_tag
FROM desired_node_state d
JOIN customers c ON c.customer_id=d.customer_id AND c.status='active'
JOIN desired_protocol_tags p ON p.customer_id=d.customer_id AND p.node_id=d.node_id AND p.service_name=d.service_name
WHERE d.service_name='maestro-core' AND NOT EXISTS(
 SELECT 1 FROM imported_entity_state s WHERE s.entity_kind='customer'
 AND s.target_id=d.customer_id AND s.lifecycle='deleted')
ORDER BY d.customer_id,d.node_id,p.protocol_tag`},
		rqlite.Statement{SQL: `SELECT order_id,payment_state,provisioning_state,
COALESCE(result_expires_at_unix,0) AS result_expires_at_unix
FROM orders ORDER BY order_id`},
		rqlite.Statement{SQL: `SELECT c.setting_key,c.public_value_json,c.generation,
s.secret_sha256,s.key_version
FROM cluster_settings c LEFT JOIN setting_secrets s ON s.setting_key=c.setting_key
ORDER BY c.setting_key`},
		rqlite.Statement{SQL: `SELECT p.principal_id,p.login_key_hmac,p.status,r.role_name,
(SELECT COUNT(*) FROM principal_credentials c WHERE c.principal_id=p.principal_id AND c.active=1) AS credential_count,
(SELECT c.verifier_sha256 FROM principal_credentials c
 WHERE c.principal_id=p.principal_id AND c.active=1 ORDER BY c.credential_id LIMIT 1) AS verifier_sha256,
(SELECT CAST(json_extract(c.verifier_envelope,'$.key_version') AS INTEGER) FROM principal_credentials c
 WHERE c.principal_id=p.principal_id AND c.active=1 ORDER BY c.credential_id LIMIT 1) AS verifier_key_version
FROM principals p LEFT JOIN principal_roles r ON r.principal_id=p.principal_id
ORDER BY p.principal_id,r.role_name`},
	)
	results, err := s.db.QueryLinearizable(ctx, statements...)
	if err != nil || len(results) != len(statements) {
		return ShadowProjection{}, ErrShadowExportUnavailable
	}

	tables := make([]businessDigestTable, len(businessDigestQueries))
	tableCounts := make(map[string]int, len(businessDigestQueries))
	for index, query := range businessDigestQueries {
		rows := results[index].Rows
		if rows == nil {
			rows = []map[string]any{}
		}
		tables[index] = businessDigestTable{Name: query.name, Rows: rows}
		tableCounts[query.name] = len(rows)
	}
	encoded, err := json.Marshal(tables)
	if err != nil {
		return ShadowProjection{}, ErrShadowExportUnavailable
	}
	businessDigest := sha256Hex(encoded)
	offset := len(businessDigestQueries)
	runRows := results[offset].Rows
	if len(runRows) != 1 {
		return ShadowProjection{}, ErrShadowExportUnavailable
	}
	run := runRows[0]
	sourceDigest, sourceOK := shadowRowString(run, "source_sha256")
	targetDigest, targetOK := shadowRowString(run, "target_sha256")
	status, statusOK := shadowRowString(run, "status")
	batchCount, batchOK := applyRowInt(run["batch_count"])
	appliedCount, appliedOK := applyRowInt(run["applied_batch_count"])
	if !sourceOK || sourceDigest != expectedSourceDigest || !targetOK || targetDigest != businessDigest ||
		!statusOK || status != "applied" || !batchOK || batchCount < 0 || !appliedOK || appliedCount != batchCount {
		return ShadowProjection{}, ErrShadowExportUnavailable
	}
	projection := ShadowProjection{
		SourceDigest: sourceDigest, TargetDigest: targetDigest, RunApplied: true,
		BatchCount: batchCount, AppliedBatchCount: appliedCount,
	}

	customerRows := results[offset+1].Rows
	if len(customerRows) != tableCounts["customers"] {
		return ShadowProjection{}, ErrShadowExportUnavailable
	}
	customers := make(map[string]*ShadowProjectionCustomer, len(customerRows))
	for _, row := range customerRows {
		internalID, idOK := shadowRowString(row, "customer_id")
		loginHMAC, loginOK := shadowRowString(row, "login_key_hmac")
		customerStatus, customerStatusOK := shadowRowString(row, "status")
		expiresAt, expiryOK := applyRowInt(row["expires_at_unix"])
		generation, generationOK := applyRowInt(row["generation"])
		credentialEnabled, credentialEnabledOK := applyRowInt(row["credential_enabled"])
		credentialCount, credentialCountOK := applyRowInt(row["credential_count"])
		tokenActive, tokenActiveOK := applyRowInt(row["token_active"])
		tokenCount, tokenCountOK := applyRowInt(row["token_count"])
		if !idOK || !loginOK || !customerStatusOK || customerStatus != "active" || !expiryOK || expiresAt < 0 ||
			!generationOK || generation < 0 || !credentialEnabledOK || credentialEnabled != 1 ||
			!credentialCountOK || credentialCount != 1 || !tokenActiveOK || tokenActive != 1 ||
			!tokenCountOK || tokenCount != 1 {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		if _, exists := customers[internalID]; exists {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		projection.Customers = append(projection.Customers, ShadowProjectionCustomer{
			InternalID: internalID, LoginKeyHMAC: loginHMAC, Status: customerStatus,
			ExpiresAtUnix: expiresAt, Generation: generation, CredentialEnabled: true,
		})
		customers[internalID] = &projection.Customers[len(projection.Customers)-1]
	}

	topologyRows := results[offset+2].Rows
	if len(topologyRows) != tableCounts["desired_protocol_tags"] {
		return ShadowProjection{}, ErrShadowExportUnavailable
	}
	type shadowNodeTags map[string]map[string]struct{}
	topology := make(map[string]shadowNodeTags, len(customers))
	tuples := make(map[string]struct{}, len(topologyRows))
	for _, row := range topologyRows {
		customerID, customerOK := shadowRowString(row, "customer_id")
		nodeID, nodeOK := shadowRowString(row, "node_id")
		service, serviceOK := shadowRowString(row, "service_name")
		tag, tagOK := shadowRowString(row, "protocol_tag")
		generation, generationOK := applyRowInt(row["generation"])
		desiredStatus, desiredStatusOK := shadowRowString(row, "status")
		customer, exists := customers[customerID]
		if !customerOK || !nodeOK || !serviceOK || service != "maestro-core" || !tagOK ||
			!generationOK || !exists || generation != customer.Generation || !desiredStatusOK ||
			(desiredStatus != "pending" && desiredStatus != "applying" && desiredStatus != "applied" && desiredStatus != "failed") {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		tuple := customerID + "\x00" + nodeID + "\x00" + tag
		if _, duplicate := tuples[tuple]; duplicate {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		tuples[tuple] = struct{}{}
		if topology[customerID] == nil {
			topology[customerID] = make(shadowNodeTags)
		}
		if topology[customerID][nodeID] == nil {
			topology[customerID][nodeID] = make(map[string]struct{})
		}
		topology[customerID][nodeID][tag] = struct{}{}
	}
	if tableCounts["desired_node_state"] != 0 {
		nodeCount := 0
		for _, nodes := range topology {
			nodeCount += len(nodes)
		}
		if nodeCount != tableCounts["desired_node_state"] {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
	}
	for index := range projection.Customers {
		customer := &projection.Customers[index]
		nodes := topology[customer.InternalID]
		if len(nodes) == 0 {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		var expectedTags []string
		for nodeID, tagSet := range nodes {
			tags := shadowSetKeys(tagSet)
			if expectedTags == nil {
				expectedTags = tags
			} else if !equalShadowStrings(expectedTags, tags) {
				return ShadowProjection{}, ErrShadowExportUnavailable
			}
			customer.Nodes = append(customer.Nodes, nodeID)
		}
		customer.ProtocolTags = expectedTags
		customer.Nodes, err = canonicalShadowSet(customer.Nodes)
		if err != nil {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
	}

	orderRows := results[offset+3].Rows
	if len(orderRows) != tableCounts["orders"] {
		return ShadowProjection{}, ErrShadowExportUnavailable
	}
	orderIDs := make(map[string]struct{}, len(orderRows))
	for _, row := range orderRows {
		orderID, orderOK := shadowRowString(row, "order_id")
		payment, paymentOK := shadowRowString(row, "payment_state")
		provisioning, provisioningOK := shadowRowString(row, "provisioning_state")
		resultExpiry, resultOK := applyRowInt(row["result_expires_at_unix"])
		if !orderOK || !paymentOK || !provisioningOK || !resultOK || resultExpiry < 0 {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		if _, exists := orderIDs[orderID]; exists {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		orderIDs[orderID] = struct{}{}
		projection.Orders = append(projection.Orders, ShadowProjectionOrder{
			InternalID: orderID, PaymentState: payment, ProvisioningState: provisioning,
			ResultExpiresAtUnix: resultExpiry,
		})
	}

	settingRows := results[offset+4].Rows
	if len(settingRows) != tableCounts["cluster_settings"] || tableCounts["setting_members"] != 0 {
		return ShadowProjection{}, ErrShadowExportUnavailable
	}
	settingKeys := make(map[string]struct{}, len(settingRows))
	for _, row := range settingRows {
		key, keyOK := shadowRowString(row, "setting_key")
		publicJSON, publicOK := shadowRowString(row, "public_value_json")
		generation, generationOK := applyRowInt(row["generation"])
		if !keyOK || !publicOK || !generationOK || generation < 0 {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		if _, exists := settingKeys[key]; exists {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		settingKeys[key] = struct{}{}
		setting := ShadowProjectionSetting{Key: key, PublicValueJSON: json.RawMessage(publicJSON), Generation: generation}
		if secretSHA, exists := nullableApplyString(row["secret_sha256"]); !exists {
			return ShadowProjection{}, ErrShadowExportUnavailable
		} else if secretSHA != "" {
			keyVersion, keyVersionOK := applyRowInt(row["key_version"])
			if !keyVersionOK || keyVersion <= 0 || keyVersion > int64(^uint(0)>>1) {
				return ShadowProjection{}, ErrShadowExportUnavailable
			}
			setting.SecretSHA256, setting.SecretKeyVersion = secretSHA, int(keyVersion)
		} else if row["key_version"] != nil {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		projection.Settings = append(projection.Settings, setting)
	}

	principalRows := results[offset+5].Rows
	principals := make(map[string]*ShadowProjectionPrincipal)
	rolePairs := make(map[string]struct{}, len(principalRows))
	for _, row := range principalRows {
		principalID, principalOK := shadowRowString(row, "principal_id")
		loginHMAC, loginOK := shadowRowString(row, "login_key_hmac")
		principalStatus, principalStatusOK := shadowRowString(row, "status")
		role, roleOK := shadowRowString(row, "role_name")
		credentialCount, countOK := applyRowInt(row["credential_count"])
		verifierSHA, verifierOK := shadowRowString(row, "verifier_sha256")
		verifierVersion, versionOK := applyRowInt(row["verifier_key_version"])
		if !principalOK || !loginOK || !principalStatusOK || !roleOK || !countOK || credentialCount != 1 ||
			!verifierOK || !versionOK || verifierVersion <= 0 || verifierVersion > int64(^uint(0)>>1) {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		principal := principals[principalID]
		if principal == nil {
			projection.Principals = append(projection.Principals, ShadowProjectionPrincipal{
				InternalID: principalID, LoginKeyHMAC: loginHMAC, Status: principalStatus,
				VerifierSHA256: verifierSHA, VerifierKeyVersion: int(verifierVersion), CredentialActive: true,
			})
			principal = &projection.Principals[len(projection.Principals)-1]
			principals[principalID] = principal
		} else if principal.LoginKeyHMAC != loginHMAC || principal.Status != principalStatus ||
			principal.VerifierSHA256 != verifierSHA || principal.VerifierKeyVersion != int(verifierVersion) {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		pair := principalID + "\x00" + role
		if _, duplicate := rolePairs[pair]; duplicate {
			return ShadowProjection{}, ErrShadowExportUnavailable
		}
		rolePairs[pair] = struct{}{}
		principal.Roles = append(principal.Roles, role)
	}
	if len(principals) != tableCounts["principals"] {
		return ShadowProjection{}, ErrShadowExportUnavailable
	}
	return projection, nil
}

func shadowRowString(row map[string]any, key string) (string, bool) {
	value, ok := row[key].(string)
	return value, ok && value != ""
}

func shadowSetKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	result, _ = canonicalShadowSet(result)
	return result
}

func equalShadowStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *RQLiteApplyStore) BeginOrResume(ctx context.Context, run ApplyRun) (RunProgress, error) {
	if run.RunID == "" || (run.SnapshotKind != "full" && run.SnapshotKind != "delta") ||
		len(run.SourceDigest) != 64 || len(run.PlanDigest) != 64 || run.BatchCount < 0 ||
		(run.SnapshotKind == "full" && run.ParentDigest != "") ||
		(run.SnapshotKind == "delta" && len(run.ParentDigest) != 64) {
		return RunProgress{}, errors.New("invalid import run")
	}
	var parent any
	if run.ParentDigest != "" {
		parent = run.ParentDigest
	}
	results, writeErr := s.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL: `INSERT INTO import_runs(
    import_run_id,snapshot_kind,source_sha256,plan_sha256,parent_source_sha256,
    target_sha256,batch_count,status,started_at_unix,completed_at_unix
) SELECT ?,?,?,?,?,NULL,?,'applying',?,NULL
WHERE NOT EXISTS(
    SELECT 1 FROM import_runs WHERE source_sha256=? AND plan_sha256=?
) ON CONFLICT(import_run_id) DO NOTHING`,
		Args: []any{
			run.RunID, run.SnapshotKind, run.SourceDigest, run.PlanDigest, parent,
			run.BatchCount, s.now().Unix(), run.SourceDigest, run.PlanDigest,
		},
	})
	created := writeErr == nil && len(results) == 1 && results[0].RowsAffected == 1

	readResults, readErr := s.db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT import_run_id,snapshot_kind,source_sha256,plan_sha256,
parent_source_sha256,target_sha256,batch_count,status
FROM import_runs WHERE import_run_id=?`, Args: []any{run.RunID}},
		rqlite.Statement{SQL: `SELECT batch_index,batch_digest,status FROM import_batches
WHERE import_run_id=? ORDER BY batch_index`, Args: []any{run.RunID}},
	)
	if readErr != nil {
		if writeErr != nil {
			return RunProgress{}, writeErr
		}
		return RunProgress{}, readErr
	}
	if len(readResults) != 2 || len(readResults[0].Rows) != 1 {
		if writeErr != nil {
			return RunProgress{}, writeErr
		}
		return RunProgress{}, ErrRunDigestMismatch
	}
	row := readResults[0].Rows[0]
	if !runRowMatches(row, run) {
		return RunProgress{}, ErrRunDigestMismatch
	}
	status, _ := row["status"].(string)
	target, targetOK := nullableApplyString(row["target_sha256"])
	if !targetOK || (status != "applying" && status != "applied") ||
		(status == "applied" && target == "") {
		return RunProgress{}, ErrRunDigestMismatch
	}
	progress := RunProgress{
		New: created, AppliedBatchDigests: make(map[int]string),
		Completed: status == "applied", TargetDigest: target,
	}
	for _, batchRow := range readResults[1].Rows {
		index, indexOK := applyRowInt(batchRow["batch_index"])
		digest, digestOK := batchRow["batch_digest"].(string)
		batchStatus, statusOK := batchRow["status"].(string)
		if !indexOK || index < 0 || !digestOK || !statusOK {
			return RunProgress{}, ErrRunDigestMismatch
		}
		if batchStatus == "applied" {
			progress.AppliedBatchDigests[int(index)] = digest
		}
	}
	return progress, nil
}

func runRowMatches(row map[string]any, run ApplyRun) bool {
	runID, runIDOK := row["import_run_id"].(string)
	kind, kindOK := row["snapshot_kind"].(string)
	source, sourceOK := row["source_sha256"].(string)
	plan, planOK := row["plan_sha256"].(string)
	parent, parentOK := nullableApplyString(row["parent_source_sha256"])
	count, countOK := applyRowInt(row["batch_count"])
	return runIDOK && kindOK && sourceOK && planOK && parentOK && countOK &&
		runID == run.RunID && kind == run.SnapshotKind && source == run.SourceDigest &&
		plan == run.PlanDigest && parent == run.ParentDigest && count == int64(run.BatchCount)
}

func backupRPODirtyGenerationStatement(updatedAtUnix int64) rqlite.Statement {
	return rqlite.Statement{
		SQL: `UPDATE backup_rpo_state AS b
SET dirty_generation = b.dirty_generation + 1,
phase = 'dirty',
	updated_at_unix = ?
WHERE b.singleton_id = 1
	AND changes() > 0
	AND EXISTS (
		SELECT 1 FROM cluster_restore_state AS cr
		WHERE cr.singleton_id = 1 AND cr.activated = 1
			AND cr.restore_epoch = b.restore_epoch
	)
RETURNING dirty_generation`,
		Args: []any{updatedAtUnix},
	}
}

func (s *RQLiteApplyStore) Complete(ctx context.Context, completion ApplyCompletion) error {
	if completion.RunID == "" || len(completion.SourceDigest) != 64 ||
		len(completion.PlanDigest) != 64 || len(completion.TargetDigest) != 64 {
		return errors.New("invalid import completion")
	}
	nowUnix := s.now().Unix()
	_, writeErr := s.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL: `UPDATE import_runs SET status='applied',target_sha256=?,completed_at_unix=?
WHERE import_run_id=? AND source_sha256=? AND plan_sha256=? AND status='applying'
AND (SELECT COUNT(*) FROM import_batches WHERE import_run_id=? )=batch_count
AND (SELECT COUNT(*) FROM import_batches WHERE import_run_id=? AND status='applied')=batch_count
AND (batch_count=0 OR (
    (SELECT MIN(batch_index) FROM import_batches WHERE import_run_id=? AND status='applied')=0
    AND (SELECT MAX(batch_index) FROM import_batches WHERE import_run_id=? AND status='applied')=batch_count-1
))`,
		Args: []any{
			completion.TargetDigest, nowUnix, completion.RunID,
			completion.SourceDigest, completion.PlanDigest, completion.RunID,
			completion.RunID, completion.RunID, completion.RunID,
		},
	}, backupRPODirtyGenerationStatement(nowUnix))
	results, readErr := s.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT source_sha256,plan_sha256,target_sha256,status
FROM import_runs WHERE import_run_id=?`, Args: []any{completion.RunID},
	})
	if readErr != nil {
		if writeErr != nil {
			return writeErr
		}
		return readErr
	}
	if len(results) != 1 || len(results[0].Rows) != 1 {
		if writeErr != nil {
			return writeErr
		}
		return ErrRunDigestMismatch
	}
	row := results[0].Rows[0]
	source, sourceOK := row["source_sha256"].(string)
	plan, planOK := row["plan_sha256"].(string)
	target, targetOK := row["target_sha256"].(string)
	status, statusOK := row["status"].(string)
	if !sourceOK || !planOK || !targetOK || !statusOK ||
		source != completion.SourceDigest || plan != completion.PlanDigest ||
		target != completion.TargetDigest || status != "applied" {
		return ErrRunDigestMismatch
	}
	return nil
}

func nullableApplyString(value any) (string, bool) {
	if value == nil {
		return "", true
	}
	text, ok := value.(string)
	return text, ok
}

func (s *RQLiteApplyStore) CommitBatch(ctx context.Context, batch ApplyBatch) (BatchReceipt, error) {
	if batch.RunID == "" || batch.PlanDigest == "" || batch.Index < 0 ||
		batch.Digest == "" || len(batch.Operations) == 0 || digestBatch(batch.Operations) != batch.Digest {
		return BatchReceipt{}, errors.New("invalid import batch")
	}
	previous, exists, err := s.readBatchReceipt(ctx, batch.RunID, batch.Index)
	if err != nil {
		return BatchReceipt{}, err
	}
	if exists {
		if previous.Digest != batch.Digest || !previous.AlreadyApplied {
			return BatchReceipt{}, ErrRunDigestMismatch
		}
		return previous, nil
	}

	statements := []rqlite.Statement{beginBatchStatement(batch)}
	for _, operation := range batch.Operations {
		operationStatements, err := s.operationStatements(batch, operation)
		if err != nil {
			return BatchReceipt{}, err
		}
		statements = append(statements, operationStatements...)
	}
	nowUnix := s.now().Unix()
	statements = append(statements, finishBatchStatement(batch, nowUnix), backupRPODirtyGenerationStatement(nowUnix))

	_, writeErr := s.db.Request(ctx, rqlite.Linearizable, true, statements...)
	resolved, exists, readErr := s.readBatchReceipt(ctx, batch.RunID, batch.Index)
	if readErr == nil && exists && resolved.Digest == batch.Digest && resolved.AlreadyApplied {
		resolved.AlreadyApplied = writeErr != nil
		return resolved, nil
	}
	if readErr == nil && exists && resolved.Digest != batch.Digest {
		return BatchReceipt{}, ErrRunDigestMismatch
	}
	if writeErr != nil {
		return BatchReceipt{}, writeErr
	}
	if readErr != nil {
		return BatchReceipt{}, readErr
	}
	return BatchReceipt{}, errors.New("import batch receipt was not committed")
}

func (s *RQLiteApplyStore) readBatchReceipt(ctx context.Context, runID string, index int) (BatchReceipt, bool, error) {
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT batch_index,batch_digest,status FROM import_batches
WHERE import_run_id=? AND batch_index=?`,
		Args: []any{runID, index},
	})
	if err != nil {
		return BatchReceipt{}, false, err
	}
	if len(results) != 1 {
		return BatchReceipt{}, false, errors.New("invalid import batch receipt result")
	}
	if len(results[0].Rows) == 0 {
		return BatchReceipt{}, false, nil
	}
	if len(results[0].Rows) != 1 {
		return BatchReceipt{}, false, errors.New("ambiguous import batch receipt")
	}
	row := results[0].Rows[0]
	digest, digestOK := row["batch_digest"].(string)
	status, statusOK := row["status"].(string)
	rowIndex, indexOK := applyRowInt(row["batch_index"])
	if !digestOK || !statusOK || !indexOK || rowIndex < 0 {
		return BatchReceipt{}, false, errors.New("invalid import batch receipt row")
	}
	return BatchReceipt{
		Index: int(rowIndex), Digest: digest, AlreadyApplied: status == "applied",
	}, true, nil
}

func beginBatchStatement(batch ApplyBatch) rqlite.Statement {
	return rqlite.Statement{
		SQL: `INSERT INTO import_batches(
    import_run_id,batch_index,batch_digest,row_count,status,applied_at_unix
)
SELECT ?,?,?,?,'applying',NULL
WHERE EXISTS(
    SELECT 1 FROM import_runs
    WHERE import_run_id=? AND plan_sha256=? AND status='applying'
) AND NOT EXISTS(
    SELECT 1 FROM import_batches WHERE import_run_id=? AND batch_index=?
)`,
		Args: []any{
			batch.RunID, batch.Index, batch.Digest, len(batch.Operations),
			batch.RunID, batch.PlanDigest, batch.RunID, batch.Index,
		},
	}
}

func finishBatchStatement(batch ApplyBatch, nowUnix int64) rqlite.Statement {
	return rqlite.Statement{
		SQL: `UPDATE import_batches SET status='applied',applied_at_unix=?
WHERE import_run_id=? AND batch_index=? AND batch_digest=? AND status='applying'`,
		Args: []any{nowUnix, batch.RunID, batch.Index, batch.Digest},
	}
}

const batchWriteGate = `EXISTS(
    SELECT 1 FROM import_batches
    WHERE import_run_id=? AND batch_index=? AND batch_digest=? AND status='applying'
)`

func batchGateArgs(batch ApplyBatch) []any {
	return []any{batch.RunID, batch.Index, batch.Digest}
}

func entityStateUpsertStatement(
	batch ApplyBatch,
	entity, sourceKey, targetID, digest string,
	nowUnix int64,
) rqlite.Statement {
	return rqlite.Statement{
		SQL: `INSERT INTO imported_entity_state(
    entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix
) SELECT ?,?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(entity_kind,source_key) DO UPDATE SET
    target_id=excluded.target_id,canonical_sha256=excluded.canonical_sha256,
    lifecycle=excluded.lifecycle,updated_at_unix=excluded.updated_at_unix`,
		Args: append([]any{
			entity, sourceKey, targetID, digest, "active", nowUnix,
		}, batchGateArgs(batch)...),
	}
}

func (s *RQLiteApplyStore) operationStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	if operation.Tombstone {
		switch operation.Entity {
		case "customer":
			return s.customerDeleteStatements(batch, operation)
		case "encrypted_secret":
			return s.encryptedSecretDeleteStatements(batch, operation)
		default:
			return nil, fmt.Errorf("unsupported import tombstone entity %q", operation.Entity)
		}
	}
	switch operation.Entity {
	case "bot_binding":
		return s.botRouteStatements(batch, operation)
	case "bot_credential_rotation":
		return s.botCredentialRotationStatements(batch, operation)
	case "bot_poll_state":
		return s.botPollStateStatements(batch, operation)
	case "customer":
		return s.customerStatements(batch, operation)
	case "encrypted_secret":
		return s.encryptedSecretStatements(batch, operation)
	case "order":
		return s.orderStatements(batch, operation)
	case "setting":
		return s.settingStatements(batch, operation)
	case "principal":
		return s.principalStatements(batch, operation)
	case "trial":
		return s.trialStatements(batch, operation)
	case "pending_callback":
		return s.pendingCallbackStatements(batch, operation)
	default:
		return nil, fmt.Errorf("unsupported canonical import entity %q", operation.Entity)
	}
}

func (s *RQLiteApplyStore) customerDeleteStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var deletion PlannedDelete
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &deletion); err != nil {
		return nil, err
	}
	wantTombstoneID := sha256Hex([]byte("import-tombstone\x00" + deletion.TargetID + "\x00" +
		strconv.FormatInt(deletion.NextGeneration, 10)))
	if deletion.Entity != "customer" || operation.Key != deletion.SourceKey || deletion.SourceKey == "" ||
		deletion.TargetID == "" || !validCanonicalSHA256(deletion.ExpectedPriorDigest) ||
		deletion.PriorGeneration < 0 || deletion.NextGeneration <= deletion.PriorGeneration ||
		deletion.NextGeneration != deletion.PriorGeneration+1 || !deletion.Tombstone ||
		deletion.TombstoneID != wantTombstoneID || !validCanonicalSHA256(deletion.TombstoneID) {
		return nil, errors.New("invalid canonical customer delete")
	}
	nowUnix := s.now().Unix()
	gate := batchGateArgs(batch)
	return []rqlite.Statement{
		{
			SQL: `UPDATE imported_entity_state SET lifecycle='deleted',updated_at_unix=?
WHERE entity_kind=? AND source_key=? AND target_id=? AND canonical_sha256=? AND lifecycle='active'
  AND ` + batchWriteGate,
			Args: append([]any{nowUnix, "customer", deletion.SourceKey, deletion.TargetID, deletion.ExpectedPriorDigest}, gate...),
		},
		{
			SQL: `UPDATE customers SET status='deleted',generation=?,updated_at_unix=?
WHERE customer_id=? AND generation=? AND status<>'deleted' AND ` + batchWriteGate,
			Args: append([]any{deletion.NextGeneration, nowUnix, deletion.TargetID, deletion.PriorGeneration}, gate...),
		},
		{
			SQL: `UPDATE credentials SET generation=?,enabled=0,updated_at_unix=?
WHERE customer_id=? AND generation=? AND enabled<>0 AND ` + batchWriteGate,
			Args: append([]any{deletion.NextGeneration, nowUnix, deletion.TargetID, deletion.PriorGeneration}, gate...),
		},
		{
			SQL: `UPDATE subscription_tokens SET generation=?,revoked=1,revoked_at_unix=?
WHERE customer_id=? AND generation=? AND revoked<>1 AND ` + batchWriteGate,
			Args: append([]any{deletion.NextGeneration, nowUnix, deletion.TargetID, deletion.PriorGeneration}, gate...),
		},
		{
			SQL: `INSERT INTO tombstones(tombstone_id,customer_id,generation,reason,created_at_unix)
SELECT ?,?,?,?,? WHERE ` + batchWriteGate,
			Args: append([]any{
				deletion.TombstoneID, deletion.TargetID, deletion.NextGeneration,
				"legacy_import_delete", nowUnix,
			}, gate...),
		},
		{
			SQL: `INSERT INTO tombstone_targets(tombstone_id,node_id,service_name,status,applied_at_unix)
SELECT ?,node_id,service_name,'pending',NULL FROM node_services
WHERE desired_target=1 AND retired=0 AND ` + batchWriteGate,
			Args: append([]any{deletion.TombstoneID}, gate...),
		},
		{
			SQL: `INSERT INTO import_delete_receipts(
    entity_kind,source_key,target_id,expected_prior_digest,lifecycle,tombstone_id,
    import_run_id,batch_index,batch_digest,imported_at_unix
) SELECT ?,?,?,?,'deleted',?,?,?,?,? WHERE ` + batchWriteGate,
			Args: append([]any{
				"customer", deletion.SourceKey, deletion.TargetID, deletion.ExpectedPriorDigest,
				deletion.TombstoneID, batch.RunID, batch.Index, batch.Digest, nowUnix,
			}, gate...),
		},
	}, nil
}

func (s *RQLiteApplyStore) encryptedSecretDeleteStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var deletion PlannedDelete
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &deletion); err != nil {
		return nil, err
	}
	if deletion.Entity != "encrypted_secret" || operation.Key != deletion.SourceKey ||
		deletion.SourceKey == "" || deletion.TargetID != deletion.SourceKey ||
		!validCanonicalSHA256(deletion.ExpectedPriorDigest) || deletion.PriorGeneration != 0 ||
		deletion.NextGeneration != 0 || deletion.TombstoneID != "" || deletion.Tombstone {
		return nil, errors.New("invalid canonical encrypted-secret delete")
	}
	nowUnix := s.now().Unix()
	gate := batchGateArgs(batch)
	return []rqlite.Statement{
		{
			SQL: `UPDATE imported_entity_state SET lifecycle='deleted',updated_at_unix=?
WHERE entity_kind=? AND source_key=? AND target_id=? AND canonical_sha256=? AND lifecycle='active'
  AND ` + batchWriteGate,
			Args: append([]any{
				nowUnix, "encrypted_secret", deletion.SourceKey, deletion.TargetID,
				deletion.ExpectedPriorDigest,
			}, gate...),
		},
		{
			SQL: `INSERT INTO import_delete_receipts(
    entity_kind,source_key,target_id,expected_prior_digest,lifecycle,tombstone_id,
    import_run_id,batch_index,batch_digest,imported_at_unix
) SELECT ?,?,?,?,'deleted',NULL,?,?,?,? WHERE ` + batchWriteGate,
			Args: append([]any{
				"encrypted_secret", deletion.SourceKey, deletion.TargetID, deletion.ExpectedPriorDigest,
				batch.RunID, batch.Index, batch.Digest, nowUnix,
			}, gate...),
		},
	}, nil
}

func validCanonicalSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func (s *RQLiteApplyStore) botRouteStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var route LegacyBotBinding
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &route); err != nil {
		return nil, err
	}
	if len(route.BotIdentityHMAC) != 64 || len(route.TokenFingerprintHMAC) != 64 ||
		route.CredentialVersion <= 0 || route.SchemaFingerprint == "" {
		return nil, errors.New("invalid canonical bot credential route")
	}
	gate := batchGateArgs(batch)
	return []rqlite.Statement{{
		SQL: `INSERT INTO telegram_bot_routes(
    bot_identity_hmac,token_fingerprint_hmac,credential_version,schema_fingerprint,updated_at_unix
) SELECT ?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(bot_identity_hmac) DO UPDATE SET
    token_fingerprint_hmac=excluded.token_fingerprint_hmac,
    credential_version=excluded.credential_version,
    schema_fingerprint=excluded.schema_fingerprint,
    updated_at_unix=CASE
        WHEN excluded.credential_version > telegram_bot_routes.credential_version
        THEN excluded.updated_at_unix
        ELSE telegram_bot_routes.updated_at_unix
    END`,
		Args: append([]any{
			route.BotIdentityHMAC, route.TokenFingerprintHMAC, route.CredentialVersion,
			route.SchemaFingerprint, s.now().Unix(),
		}, gate...),
	}}, nil
}

func (s *RQLiteApplyStore) botPollStateStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var state LegacyBotPollState
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &state); err != nil {
		return nil, err
	}
	const maxSQLiteInteger = uint64(1<<63 - 1)
	if len(state.BotIdentityHMAC) != 64 || len(state.CurrentTokenFingerprintHMAC) != 64 ||
		state.CredentialVersion <= 0 || state.NextUpdateID < 0 || state.CapturedFence > maxSQLiteInteger {
		return nil, errors.New("invalid canonical bot poll state")
	}
	gate := batchGateArgs(batch)
	return []rqlite.Statement{{
		SQL: `INSERT INTO telegram_pollers(
    bot_identity_hmac,node_id,lease_token,offset_value,lease_fence,
    lease_expires_at_unix,updated_at_unix
) SELECT CASE WHEN EXISTS(
    SELECT 1 FROM telegram_bot_routes
    WHERE bot_identity_hmac=? AND token_fingerprint_hmac=? AND credential_version=?
) THEN ? ELSE NULL END,NULL,NULL,?,?,0,?
WHERE ` + batchWriteGate + `
ON CONFLICT(bot_identity_hmac) DO UPDATE SET
    node_id=NULL,lease_token=NULL,
    offset_value=excluded.offset_value,lease_fence=excluded.lease_fence,
    lease_expires_at_unix=0,
    updated_at_unix=CASE
        WHEN excluded.offset_value > telegram_pollers.offset_value OR
             excluded.lease_fence > telegram_pollers.lease_fence
        THEN excluded.updated_at_unix
        ELSE telegram_pollers.updated_at_unix
    END`,
		Args: append([]any{
			state.BotIdentityHMAC, state.CurrentTokenFingerprintHMAC, state.CredentialVersion,
			state.BotIdentityHMAC, state.NextUpdateID, int64(state.CapturedFence), s.now().Unix(),
		}, gate...),
	}}, nil
}

func (s *RQLiteApplyStore) pendingCallbackStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var callback LegacyCallback
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &callback); err != nil {
		return nil, err
	}
	if len(callback.CallbackHMAC) != 64 || len(callback.BotIdentityHMAC) != 64 ||
		len(callback.TokenFingerprintHMAC) != 64 || callback.CredentialVersion <= 0 ||
		callback.OrderID == "" || callback.Action == "" ||
		(callback.State != "pending" && callback.State != "in_flight") {
		return nil, errors.New("invalid canonical pending callback")
	}
	gate := batchGateArgs(batch)
	return []rqlite.Statement{{
		SQL: `INSERT INTO telegram_imported_callbacks(
    callback_hmac,bot_identity_hmac,order_id,action,state,updated_at_unix
) SELECT ?,CASE WHEN EXISTS(
    SELECT 1 FROM telegram_bot_routes
    WHERE bot_identity_hmac=? AND token_fingerprint_hmac=? AND credential_version=?
) THEN ? ELSE NULL END,?,?,?,?
WHERE ` + batchWriteGate + `
ON CONFLICT(callback_hmac) DO UPDATE SET
    bot_identity_hmac=excluded.bot_identity_hmac,
    order_id=excluded.order_id,action=excluded.action,state=excluded.state,
    updated_at_unix=CASE
        WHEN excluded.state <> telegram_imported_callbacks.state
        THEN excluded.updated_at_unix
        ELSE telegram_imported_callbacks.updated_at_unix
    END`,
		Args: append([]any{
			callback.CallbackHMAC,
			callback.BotIdentityHMAC, callback.TokenFingerprintHMAC, callback.CredentialVersion,
			callback.BotIdentityHMAC, callback.OrderID, callback.Action, callback.State, s.now().Unix(),
		}, gate...),
	}}, nil
}

func (s *RQLiteApplyStore) botCredentialRotationStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var rotation LegacyBotCredentialRotation
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &rotation); err != nil {
		return nil, err
	}
	if len(rotation.AuditDigest) != 64 || len(rotation.BotIdentityHMAC) != 64 ||
		len(rotation.OldTokenFingerprintHMAC) != 64 || len(rotation.NewTokenFingerprintHMAC) != 64 ||
		rotation.OldTokenFingerprintHMAC == rotation.NewTokenFingerprintHMAC ||
		rotation.OldCredentialVersion <= 0 || rotation.NewCredentialVersion <= rotation.OldCredentialVersion {
		return nil, errors.New("invalid canonical bot credential rotation")
	}
	gate := batchGateArgs(batch)
	return []rqlite.Statement{{
		SQL: `INSERT INTO telegram_bot_credential_rotations(
    audit_digest,bot_identity_hmac,old_token_fingerprint_hmac,
    new_token_fingerprint_hmac,old_credential_version,new_credential_version,imported_at_unix
) SELECT ?,?,?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(audit_digest) DO UPDATE SET
    bot_identity_hmac=excluded.bot_identity_hmac,
    old_token_fingerprint_hmac=excluded.old_token_fingerprint_hmac,
    new_token_fingerprint_hmac=excluded.new_token_fingerprint_hmac,
    old_credential_version=excluded.old_credential_version,
    new_credential_version=excluded.new_credential_version,
    imported_at_unix=telegram_bot_credential_rotations.imported_at_unix`,
		Args: append([]any{
			rotation.AuditDigest, rotation.BotIdentityHMAC,
			rotation.OldTokenFingerprintHMAC, rotation.NewTokenFingerprintHMAC,
			rotation.OldCredentialVersion, rotation.NewCredentialVersion, s.now().Unix(),
		}, gate...),
	}}, nil
}

func (s *RQLiteApplyStore) encryptedSecretStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var secret LegacyEncryptedSecret
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &secret); err != nil {
		return nil, err
	}
	if secret.SecretID == "" || secret.OwnerType == "" || secret.OwnerSourceKey == "" ||
		secret.Field == "" || secret.Kind == "" || secret.KeyVersion <= 0 ||
		secret.NonceB64 == "" || secret.CiphertextB64 == "" || len(secret.SHA256) != 64 {
		return nil, errors.New("invalid canonical encrypted secret")
	}
	envelope, err := json.Marshal(secret)
	if err != nil {
		return nil, errors.New("cannot encode protected standalone envelope")
	}
	nowUnix := s.now().Unix()
	gate := batchGateArgs(batch)
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO imported_secrets(
    secret_id,owner_type,owner_source_key,field,kind,key_version,
    secret_envelope,secret_sha256,imported_at_unix
) SELECT ?,?,?,?,?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(secret_id) DO UPDATE SET
    owner_type=excluded.owner_type,owner_source_key=excluded.owner_source_key,
    field=excluded.field,kind=excluded.kind,key_version=excluded.key_version,
    secret_envelope=excluded.secret_envelope,secret_sha256=excluded.secret_sha256,
    imported_at_unix=imported_secrets.imported_at_unix`,
		Args: append([]any{
			secret.SecretID, secret.OwnerType, secret.OwnerSourceKey, secret.Field, secret.Kind,
			secret.KeyVersion, string(envelope), secret.SHA256, nowUnix,
		}, gate...),
	}}
	statements = append(statements, entityStateUpsertStatement(batch, "encrypted_secret", secret.SecretID, secret.SecretID, canonicalLegacyDigest(secret), nowUnix))
	return statements, nil
}

func (s *RQLiteApplyStore) trialStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	if s.trialProtection == nil {
		return nil, errors.New("protected legacy trial salt is required")
	}
	var trial LegacyTrial
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &trial); err != nil {
		return nil, err
	}
	if trial.SourceKey == "" || operation.Key != trial.SourceKey ||
		len(trial.LegacyAnchorHMAC) != 64 || len(trial.CurrentHMAC) != 64 ||
		trial.ExpiresAtUnix < 0 {
		return nil, errors.New("invalid canonical trial identity")
	}
	used := 0
	if trial.Used {
		used = 1
	}
	protection := s.trialProtection
	nowUnix := s.now().Unix()
	gate := batchGateArgs(batch)
	return []rqlite.Statement{{
		SQL: `INSERT INTO imported_secrets(
    secret_id,owner_type,owner_source_key,field,kind,key_version,
    secret_envelope,secret_sha256,imported_at_unix
) SELECT ?,?,?,?,?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(secret_id) DO UPDATE SET
    owner_type=excluded.owner_type,owner_source_key=excluded.owner_source_key,
    field=excluded.field,kind=excluded.kind,key_version=excluded.key_version,
    secret_envelope=excluded.secret_envelope,secret_sha256=excluded.secret_sha256,
    imported_at_unix=imported_secrets.imported_at_unix`,
		Args: append([]any{
			legacyTrialSaltSecretID, "trial_lookup", "legacy", "salt", "hmac-key",
			protection.KeyVersion, protection.EncryptedSaltEnvelope, protection.SaltSHA256, nowUnix,
		}, gate...),
	}, {
		SQL: `INSERT INTO imported_trial_identities(
    source_key,legacy_anchor_hmac,current_hmac,used,expires_at_unix,
    lookup_secret_id,imported_at_unix
) SELECT ?,?,?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(source_key) DO UPDATE SET
    legacy_anchor_hmac=excluded.legacy_anchor_hmac,current_hmac=excluded.current_hmac,
    used=excluded.used,expires_at_unix=excluded.expires_at_unix,
    lookup_secret_id=excluded.lookup_secret_id,
    imported_at_unix=imported_trial_identities.imported_at_unix`,
		Args: append([]any{
			trial.SourceKey, trial.LegacyAnchorHMAC, trial.CurrentHMAC, used,
			trial.ExpiresAtUnix, legacyTrialSaltSecretID, nowUnix,
		}, gate...),
	}}, nil
}

func sqlPlaceholders(count int) string {
	placeholders := make([]string, count)
	for index := range placeholders {
		placeholders[index] = "?"
	}
	return strings.Join(placeholders, ",")
}

func (s *RQLiteApplyStore) customerStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var payload customerApplyPayload
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &payload); err != nil {
		return nil, err
	}
	customer := payload.Customer
	secret := payload.IdentitySecret
	if customer.InternalID == "" || customer.DisplayLogin == "" || customer.IdentitySecretRef != secret.SecretID ||
		len(customer.ProtocolTags) == 0 || len(customer.NodeIDs) == 0 {
		return nil, errors.New("invalid canonical customer operation")
	}
	envelope, err := json.Marshal(secret)
	if err != nil {
		return nil, errors.New("cannot encode protected customer envelope")
	}
	nowUnix := s.now().Unix()
	enabled := 1
	revoked := 0
	var revokedAt any
	if customer.Status != "active" {
		enabled = 0
		revoked = 1
		revokedAt = nowUnix
	}
	gate := batchGateArgs(batch)
	credentialID := sha256Hex([]byte("credential\x00" + customer.InternalID + "\x00" + secret.Kind))
	tokenID := sha256Hex([]byte("subscription-token\x00" + customer.InternalID))
	statements := []rqlite.Statement{
		{
			SQL: `INSERT INTO customers(
    customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix
) SELECT ?,?,?,?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(customer_id) DO UPDATE SET
    display_login=excluded.display_login,login_key_hmac=excluded.login_key_hmac,
    status=excluded.status,expires_at_unix=excluded.expires_at_unix,
    generation=excluded.generation,updated_at_unix=excluded.updated_at_unix
WHERE customers.generation <= excluded.generation`,
			Args: append([]any{
				customer.InternalID, customer.DisplayLogin, customer.LoginKeyHMAC, customer.Status,
				customer.ExpiresAtUnix, customer.Generation, nowUnix, nowUnix,
			}, gate...),
		},
		{
			SQL: `INSERT INTO credentials(
    credential_id,customer_id,protocol,secret_envelope,secret_sha256,generation,enabled,created_at_unix,updated_at_unix
) SELECT ?,?,?,?,?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(credential_id) DO UPDATE SET
    secret_envelope=excluded.secret_envelope,secret_sha256=excluded.secret_sha256,
    generation=excluded.generation,enabled=excluded.enabled,updated_at_unix=excluded.updated_at_unix`,
			Args: append([]any{
				credentialID, customer.InternalID, secret.Kind, string(envelope), secret.SHA256,
				customer.Generation, enabled, nowUnix, nowUnix,
			}, gate...),
		},
		{
			SQL: `INSERT INTO subscription_tokens(
    token_id,customer_id,token_hmac,token_envelope,token_sha256,generation,revoked,created_at_unix,revoked_at_unix
) SELECT ?,?,?,?,?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(token_id) DO UPDATE SET
    token_hmac=excluded.token_hmac,token_envelope=excluded.token_envelope,
    token_sha256=excluded.token_sha256,generation=excluded.generation,
    revoked=excluded.revoked,revoked_at_unix=excluded.revoked_at_unix`,
			Args: append([]any{
				tokenID, customer.InternalID, customer.TokenHMAC, string(envelope), secret.SHA256,
				customer.Generation, revoked, nowUnix, revokedAt,
			}, gate...),
		},
	}
	protocolDeleteArgs := []any{customer.InternalID, "maestro-core"}
	for _, protocolTag := range customer.ProtocolTags {
		protocolDeleteArgs = append(protocolDeleteArgs, protocolTag)
	}
	protocolDeleteArgs = append(protocolDeleteArgs, gate...)
	statements = append(statements, rqlite.Statement{
		SQL: `DELETE FROM desired_protocol_tags
WHERE customer_id=? AND service_name=? AND protocol_tag NOT IN (` + sqlPlaceholders(len(customer.ProtocolTags)) + `)
  AND ` + batchWriteGate,
		Args: protocolDeleteArgs,
	})
	nodeDeleteArgs := []any{customer.InternalID, "maestro-core"}
	for _, nodeID := range customer.NodeIDs {
		nodeDeleteArgs = append(nodeDeleteArgs, nodeID)
	}
	nodeDeleteArgs = append(nodeDeleteArgs, gate...)
	statements = append(statements, rqlite.Statement{
		SQL: `DELETE FROM desired_node_state
WHERE customer_id=? AND service_name=? AND node_id NOT IN (` + sqlPlaceholders(len(customer.NodeIDs)) + `)
  AND ` + batchWriteGate,
		Args: nodeDeleteArgs,
	})
	for _, nodeID := range customer.NodeIDs {
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO desired_node_state(
    customer_id,node_id,service_name,generation,desired_envelope,desired_sha256,status,updated_at_unix
) SELECT ?,?,?,?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(customer_id,node_id,service_name) DO UPDATE SET
    generation=excluded.generation,desired_envelope=excluded.desired_envelope,
    desired_sha256=excluded.desired_sha256,status='pending',updated_at_unix=excluded.updated_at_unix
WHERE desired_node_state.generation <= excluded.generation`,
			Args: append([]any{
				customer.InternalID, nodeID, "maestro-core", customer.Generation,
				string(envelope), secret.SHA256, "pending", nowUnix,
			}, gate...),
		})
		for _, protocolTag := range customer.ProtocolTags {
			statements = append(statements, rqlite.Statement{
				SQL: `INSERT INTO desired_protocol_tags(customer_id,node_id,service_name,protocol_tag)
SELECT ?,?,?,? WHERE EXISTS(
    SELECT 1 FROM desired_node_state
    WHERE customer_id=? AND node_id=? AND service_name=? AND generation=?
) AND ` + batchWriteGate + `
ON CONFLICT(customer_id,node_id,service_name,protocol_tag) DO NOTHING`,
				Args: append([]any{
					customer.InternalID, nodeID, "maestro-core", protocolTag,
					customer.InternalID, nodeID, "maestro-core", customer.Generation,
				}, gate...),
			})
		}
	}
	statements = append(statements,
		entityStateUpsertStatement(batch, "customer", customer.SourceKey, customer.InternalID, plannedCustomerSourceDigest(customer), nowUnix),
		entityStateUpsertStatement(batch, "encrypted_secret", secret.SecretID, secret.SecretID, canonicalLegacyDigest(secret), nowUnix),
	)
	return statements, nil
}

func (s *RQLiteApplyStore) orderStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var payload orderApplyPayload
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &payload); err != nil {
		return nil, err
	}
	order := payload.Order
	if order.InternalID == "" || order.PaymentCode == "" || order.TariffVersionID == "" ||
		order.AmountMinor <= 0 || order.DurationDays <= 0 || order.CreatedAtUnix < 0 {
		return nil, errors.New("invalid canonical order operation")
	}
	paymentState := order.PaymentState
	switch paymentState {
	case "created", "pending":
		paymentState = "created"
	case "payment_claimed", "claimed":
		paymentState = "payment_claimed"
	case "paid":
		paymentState = "confirmed"
	}
	provisioningState := order.ProvisioningState
	if provisioningState == "paid" || provisioningState == "applied" {
		provisioningState = "ready"
	}
	var customerID any
	if order.CustomerInternalID != "" {
		customerID = order.CustomerInternalID
	}
	var resultExpiry, resultGeneration any
	if order.ResultExpiresAtUnix > 0 {
		resultExpiry = order.ResultExpiresAtUnix
	}
	if order.ResultGeneration > 0 {
		resultGeneration = order.ResultGeneration
	}
	var decision, confirmedAt any
	if paymentState == "confirmed" {
		decision = "confirmed"
		confirmedAt = order.CreatedAtUnix
	}
	operationID := sha256Hex([]byte("import-order-operation\x00" + order.InternalID))
	args := []any{
		order.InternalID, order.PaymentCode, order.BuyerScope, order.BuyerKeyHMAC,
		customerID, order.TariffVersionID, order.AmountMinor, order.Currency,
		order.DurationDays, order.CreatedAtUnix, order.ExpiresAtUnix,
		paymentState, provisioningState, decision, confirmedAt, resultExpiry, resultGeneration, operationID,
	}
	args = append(args, batchGateArgs(batch)...)
	return []rqlite.Statement{{
		SQL: `INSERT INTO orders(
    order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
    amount_minor,currency,duration_days,created_at_unix,expires_at_unix,
    payment_state,provisioning_state,decision,confirmed_at_unix,
    result_expires_at_unix,result_generation,operation_id
) SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(order_id) DO UPDATE SET
    payment_state=excluded.payment_state,provisioning_state=excluded.provisioning_state,
    decision=excluded.decision,confirmed_at_unix=excluded.confirmed_at_unix,
    result_expires_at_unix=excluded.result_expires_at_unix,
    result_generation=excluded.result_generation`,
		Args: args,
	}}, nil
}

func (s *RQLiteApplyStore) settingStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var payload settingApplyPayload
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &payload); err != nil {
		return nil, err
	}
	if payload.Setting.Key == "" || len(payload.Setting.PublicValueJSON) == 0 {
		return nil, errors.New("invalid canonical setting operation")
	}
	nowUnix := s.now().Unix()
	gate := batchGateArgs(batch)
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO cluster_settings(setting_key,public_value_json,generation,updated_at_unix)
SELECT ?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(setting_key) DO UPDATE SET
    public_value_json=excluded.public_value_json,generation=excluded.generation,
    updated_at_unix=excluded.updated_at_unix
WHERE cluster_settings.generation <= excluded.generation`,
		Args: append([]any{
			payload.Setting.Key, string(payload.Setting.PublicValueJSON), payload.Setting.Generation, nowUnix,
		}, gate...),
	}}
	if payload.Secret == nil {
		return statements, nil
	}
	envelope, err := json.Marshal(payload.Secret)
	if err != nil {
		return nil, errors.New("cannot encode protected setting envelope")
	}
	statements = append(statements, rqlite.Statement{
		SQL: `INSERT INTO setting_secrets(setting_key,secret_envelope,secret_sha256,key_version,updated_at_unix)
SELECT ?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(setting_key) DO UPDATE SET
    secret_envelope=excluded.secret_envelope,secret_sha256=excluded.secret_sha256,
    key_version=excluded.key_version,updated_at_unix=excluded.updated_at_unix`,
		Args: append([]any{
			payload.Setting.Key, string(envelope), payload.Secret.SHA256, payload.Secret.KeyVersion, nowUnix,
		}, gate...),
	})
	return statements, nil
}

func (s *RQLiteApplyStore) principalStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var payload principalApplyPayload
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &payload); err != nil {
		return nil, err
	}
	principal := payload.Principal
	secret := payload.CredentialSecret
	if principal.InternalID == "" || principal.CredentialSecretRef != secret.SecretID {
		return nil, errors.New("invalid canonical principal operation")
	}
	envelope, err := json.Marshal(secret)
	if err != nil {
		return nil, errors.New("cannot encode protected principal envelope")
	}
	nowUnix := s.now().Unix()
	gate := batchGateArgs(batch)
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO principals(principal_id,login_key_hmac,status,revocation_epoch,created_at_unix)
SELECT ?,?,?,0,? WHERE ` + batchWriteGate + `
ON CONFLICT(principal_id) DO UPDATE SET
    login_key_hmac=excluded.login_key_hmac,status=excluded.status`,
		Args: append([]any{principal.InternalID, principal.LoginKeyHMAC, principal.Status, nowUnix}, gate...),
	}, {
		SQL:  `DELETE FROM principal_roles WHERE principal_id=? AND ` + batchWriteGate,
		Args: append([]any{principal.InternalID}, gate...),
	}}
	for _, role := range principal.Roles {
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO principal_roles(principal_id,role_name,granted_at_unix)
SELECT ?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(principal_id,role_name) DO NOTHING`,
			Args: append([]any{principal.InternalID, role, nowUnix}, gate...),
		})
	}
	credentialID := sha256Hex([]byte("principal-credential\x00" + principal.InternalID))
	statements = append(statements, rqlite.Statement{
		SQL: `INSERT INTO principal_credentials(
    credential_id,principal_id,credential_type,verifier_envelope,verifier_sha256,active,created_at_unix
) SELECT ?,?,'password',?,?,1,? WHERE ` + batchWriteGate + `
ON CONFLICT(credential_id) DO UPDATE SET
    verifier_envelope=excluded.verifier_envelope,verifier_sha256=excluded.verifier_sha256,
    active=excluded.active`,
		Args: append([]any{
			credentialID, principal.InternalID, string(envelope), secret.SHA256, nowUnix,
		}, gate...),
	})
	return statements, nil
}

func decodeCanonicalOperation(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid canonical import operation")
	}
	return nil
}

func applyRowInt(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), typed == float64(int64(typed))
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}
