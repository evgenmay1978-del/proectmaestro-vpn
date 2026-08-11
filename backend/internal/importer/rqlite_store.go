package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

var errRQLiteApplyStoreNotReady = errors.New("rqlite apply store run lifecycle is not ready")

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

func (*RQLiteApplyStore) InspectTarget(context.Context) (TargetState, error) {
	return TargetState{}, errRQLiteApplyStoreNotReady
}

func (*RQLiteApplyStore) BeginOrResume(context.Context, ApplyRun) (RunProgress, error) {
	return RunProgress{}, errRQLiteApplyStoreNotReady
}

func (*RQLiteApplyStore) Complete(context.Context, ApplyCompletion) error {
	return errRQLiteApplyStoreNotReady
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
	case "customer":
		return s.customerStatements(batch, operation)
	case "order":
		return s.orderStatements(batch, operation)
	case "setting":
		return s.settingStatements(batch, operation)
	case "principal":
		return s.principalStatements(batch, operation)
	default:
		return nil, fmt.Errorf("unsupported canonical import entity %q", operation.Entity)
	}
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
	default:
		return 0, false
	}
}
