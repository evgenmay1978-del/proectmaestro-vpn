package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type RQLiteApplyStore struct {
	db  rqlite.RQLite
	now func() time.Time
}

var _ ApplyStore = (*RQLiteApplyStore)(nil)

func NewRQLiteApplyStore(db rqlite.RQLite, now func() time.Time) (*RQLiteApplyStore, error) {
	if db == nil || now == nil {
		return nil, errors.New("rqlite apply store dependencies are required")
	}
	return &RQLiteApplyStore{db: db, now: now}, nil
}

type businessDigestTable struct {
	Name string           `json:"name"`
	Rows []map[string]any `json:"rows"`
}

var businessDigestQueries = []struct {
	name string
	sql  string
}{
	{"customers", "SELECT * FROM customers ORDER BY customer_id"},
	{"credentials", "SELECT * FROM credentials ORDER BY credential_id"},
	{"subscription_tokens", "SELECT * FROM subscription_tokens ORDER BY token_id"},
	{"orders", "SELECT * FROM orders ORDER BY order_id"},
	{"trial_redemptions", "SELECT * FROM trial_redemptions ORDER BY redemption_id"},
	{"desired_node_state", "SELECT * FROM desired_node_state ORDER BY customer_id,node_id,service_name"},
	{"tombstones", "SELECT * FROM tombstones ORDER BY tombstone_id"},
	{"telegram_bot_routes", "SELECT * FROM telegram_bot_routes ORDER BY bot_identity_hmac"},
	{"telegram_bot_credential_rotations", "SELECT * FROM telegram_bot_credential_rotations ORDER BY audit_digest"},
	{"telegram_pollers", "SELECT * FROM telegram_pollers ORDER BY bot_identity_hmac"},
	{"telegram_imported_callbacks", "SELECT * FROM telegram_imported_callbacks ORDER BY callback_hmac"},
	{"telegram_callbacks", "SELECT * FROM telegram_callbacks ORDER BY callback_hmac"},
	{"telegram_bindings", "SELECT * FROM telegram_bindings ORDER BY binding_id"},
	{"cluster_settings", "SELECT * FROM cluster_settings ORDER BY setting_key"},
	{"setting_members", "SELECT * FROM setting_members ORDER BY setting_key,member_key"},
	{"setting_secrets", "SELECT * FROM setting_secrets ORDER BY setting_key"},
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

func (s *RQLiteApplyStore) Complete(ctx context.Context, completion ApplyCompletion) error {
	if completion.RunID == "" || len(completion.SourceDigest) != 64 ||
		len(completion.PlanDigest) != 64 || len(completion.TargetDigest) != 64 {
		return errors.New("invalid import completion")
	}
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
			completion.TargetDigest, s.now().Unix(), completion.RunID,
			completion.SourceDigest, completion.PlanDigest, completion.RunID,
			completion.RunID, completion.RunID, completion.RunID,
		},
	})
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
	statements = append(statements, finishBatchStatement(batch, s.now().Unix()))

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

func (s *RQLiteApplyStore) operationStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	if operation.Tombstone {
		return nil, fmt.Errorf("unsupported import tombstone entity %q", operation.Entity)
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
	case "order":
		return s.orderStatements(batch, operation)
	case "setting":
		return s.settingStatements(batch, operation)
	case "principal":
		return s.principalStatements(batch, operation)
	case "pending_callback":
		return s.pendingCallbackStatements(batch, operation)
	default:
		return nil, fmt.Errorf("unsupported canonical import entity %q", operation.Entity)
	}
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

func (s *RQLiteApplyStore) customerStatements(batch ApplyBatch, operation ApplyOperation) ([]rqlite.Statement, error) {
	var payload customerApplyPayload
	if err := decodeCanonicalOperation(operation.CanonicalJSON, &payload); err != nil {
		return nil, err
	}
	customer := payload.Customer
	secret := payload.IdentitySecret
	if customer.InternalID == "" || customer.DisplayLogin == "" || customer.IdentitySecretRef != secret.SecretID {
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
	return []rqlite.Statement{
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
	}, nil
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
	if paymentState == "created" {
		paymentState = "pending"
	}
	if paymentState == "paid" {
		paymentState = "confirmed"
	}
	provisioningState := order.ProvisioningState
	if provisioningState == "paid" {
		provisioningState = "applied"
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
	operationID := sha256Hex([]byte("import-order-operation\x00" + order.InternalID))
	args := []any{
		order.InternalID, order.PaymentCode, order.BuyerScope, order.BuyerKeyHMAC,
		customerID, order.TariffVersionID, order.AmountMinor, order.Currency,
		order.DurationDays, order.CreatedAtUnix, order.ExpiresAtUnix,
		paymentState, provisioningState, resultExpiry, resultGeneration, operationID,
	}
	args = append(args, batchGateArgs(batch)...)
	return []rqlite.Statement{{
		SQL: `INSERT INTO orders(
    order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
    amount_minor,currency,duration_days,created_at_unix,expires_at_unix,
    payment_state,provisioning_state,result_expires_at_unix,result_generation,operation_id
) SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,? WHERE ` + batchWriteGate + `
ON CONFLICT(order_id) DO UPDATE SET
    payment_state=excluded.payment_state,provisioning_state=excluded.provisioning_state,
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
		SQL: `DELETE FROM principal_roles WHERE principal_id=? AND ` + batchWriteGate,
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
