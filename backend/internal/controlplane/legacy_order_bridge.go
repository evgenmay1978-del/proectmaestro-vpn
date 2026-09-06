package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	legacyorder "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type LegacyOrderView struct {
	Order           legacyorder.Order
	CustomerID      string
	HistoricalGrant bool
	InternalOrderID string
	AcceptedAtUnix  int64
}

type legacyOrderSlot struct {
	record                                                           LegacyOrderRecord
	row                                                              LegacyOrderSourceRow
	secretID, secretSHA, encoded, envelopeSHA, sourceSHA, acceptedID string
	acceptedAt                                                       int64
}

type legacyOrderAccess struct {
	value         CustomerAccess
	tokenID       string
	credentialIDs []string
}

func (s *Service) legacyOrderRenewalAccess(ctx context.Context, orderID, customerID string) (*legacyOrderAccess, error) {
	if !strings.HasPrefix(orderID, "legacy-accepted-") {
		return nil, nil
	}
	slot, err := s.loadLegacyOrderKey(ctx, strings.TrimPrefix(orderID, "legacy-accepted-"))
	if err != nil || slot.acceptedID != orderID || slot.record.CustomerID != customerID || slot.row.Order.Status != "pending" || slot.row.Order.Credited {
		return nil, ErrUnavailable
	}
	rows, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT st.token_id,st.token_envelope,cr.credential_id,cr.protocol,cr.secret_envelope
 FROM subscription_tokens st JOIN credentials cr ON cr.customer_id=st.customer_id
 WHERE st.customer_id=? AND st.token_hmac=? AND st.generation=(SELECT max(t.generation) FROM subscription_tokens t WHERE t.customer_id=st.customer_id)
 AND NOT EXISTS(SELECT 1 FROM subscription_tokens t WHERE t.customer_id=st.customer_id AND t.revoked=0 AND t.token_hmac<>st.token_hmac)
 AND cr.generation=(SELECT max(c.generation) FROM credentials c WHERE c.customer_id=cr.customer_id AND c.protocol=cr.protocol) ORDER BY cr.protocol`, Args: []any{customerID, slot.record.CustomerTokenHMAC}})
	if err != nil || len(rows) != 1 || len(rows[0].Rows) == 0 || len(rows[0].Rows) > 16 {
		return nil, ErrUnavailable
	}
	result := &legacyOrderAccess{value: CustomerAccess{Credentials: map[string]string{}}}
	for _, row := range rows[0].Rows {
		tokenID, tOK := rowString(row, "token_id")
		id, iOK := rowString(row, "credential_id")
		protocol, pOK := rowString(row, "protocol")
		if !tOK || !iOK || !pOK || tokenID == "" || id == "" || protocol == "" || (result.tokenID != "" && result.tokenID != tokenID) || result.value.Credentials[protocol] != "" {
			return nil, ErrUnavailable
		}
		result.tokenID = tokenID
		if result.value.SubscriptionToken == "" {
			token, err := s.openCustomerSecret(row, "token_envelope", customerID, "token", "subscription")
			if err != nil || s.store.secrets.LookupHMAC("subscription-token", []byte(token)) != slot.record.CustomerTokenHMAC {
				return nil, ErrUnavailable
			}
			result.value.SubscriptionToken = token
		}
		raw, username, err := s.openCustomerCredential(row, customerID, protocol)
		if err != nil {
			return nil, ErrUnavailable
		}
		result.value.Credentials[protocol] = raw
		result.credentialIDs = append(result.credentialIDs, id)
		if username != "" {
			if result.value.CredentialUsernames == nil {
				result.value.CredentialUsernames = map[string]string{}
			}
			result.value.CredentialUsernames[protocol] = username
		}
	}
	return result, nil
}

func appendLegacyOrderAccessStatements(statements *[]rqlite.Statement, prepared orderRecord, orderID, operationID string) {
	if prepared.LegacyAccess == nil {
		return
	}
	guard := `EXISTS(SELECT 1 FROM orders o JOIN customers c ON c.customer_id=o.customer_id WHERE o.order_id=? AND o.operation_id=? AND o.payment_state='confirmed' AND c.generation=? AND c.status='active')`
	args := []any{orderID, operationID, prepared.CustomerGeneration}
	*statements = append(*statements, rqlite.Statement{SQL: `UPDATE subscription_tokens SET revoked=0,revoked_at_unix=NULL,generation=? WHERE token_id=? AND customer_id=? AND ` + guard, Args: append([]any{prepared.CustomerGeneration, prepared.LegacyAccess.tokenID, prepared.CustomerID}, args...)})
	for _, id := range prepared.LegacyAccess.credentialIDs {
		*statements = append(*statements, rqlite.Statement{SQL: `UPDATE credentials SET enabled=1,generation=?,updated_at_unix=unixepoch() WHERE credential_id=? AND customer_id=? AND ` + guard, Args: append([]any{prepared.CustomerGeneration, id, prepared.CustomerID}, args...)})
	}
}

type legacyOrderSecret struct {
	SecretID       string `json:"secret_id"`
	OwnerType      string `json:"owner_type"`
	OwnerSourceKey string `json:"owner_source_key"`
	Field          string `json:"field"`
	Kind           string `json:"kind"`
	KeyVersion     int    `json:"key_version"`
	NonceB64       string `json:"nonce_b64"`
	CiphertextB64  string `json:"ciphertext_b64"`
	SHA256         string `json:"sha256"`
}

func (s *Service) loadLegacyOrder(ctx context.Context, rawID string) (legacyOrderSlot, error) {
	if rawID == "" || len(rawID) > 4096 || strings.ContainsRune(rawID, 0) {
		return legacyOrderSlot{}, ErrNotFound
	}
	key := s.store.secrets.LookupHMAC(LegacyOrderLookupDomain, []byte(rawID))
	slot, err := s.loadLegacyOrderKey(ctx, key)
	if err == nil && slot.row.Order.ID != rawID {
		return legacyOrderSlot{}, ErrUnavailable
	}
	return slot, err
}

func (s *Service) loadLegacyOrderKey(ctx context.Context, key string) (legacyOrderSlot, error) {
	var slot legacyOrderSlot
	if !legacyBindingSHA(key) {
		return slot, ErrNotFound
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT a.record_secret_id,a.record_sha256,a.source_sha256,a.source_revision,a.customer_id,a.historical_grant,a.accepted_order_id,a.accepted_at_unix,a.cancelled_at_unix,
 i.owner_type,i.owner_source_key,i.field,i.kind,i.key_version,CAST(i.secret_envelope AS TEXT) AS secret_envelope,i.secret_sha256,
 e.target_id,e.canonical_sha256,e.lifecycle FROM imported_legacy_order_aliases a
 LEFT JOIN imported_secrets i ON i.secret_id=a.record_secret_id
 LEFT JOIN imported_entity_state e ON e.entity_kind='encrypted_secret' AND e.source_key=a.record_secret_id
 WHERE a.order_key_hmac=?`, Args: []any{key}})
	if err != nil {
		return slot, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return slot, ErrNotFound
	}
	if row["cancelled_at_unix"] != nil {
		return slot, ErrNotFound
	}
	var fieldOK bool
	slot.secretID, fieldOK = rowString(row, "record_secret_id")
	if !fieldOK {
		return slot, ErrUnavailable
	}
	slot.secretSHA, fieldOK = rowString(row, "record_sha256")
	if !fieldOK {
		return slot, ErrUnavailable
	}
	slot.sourceSHA, fieldOK = rowString(row, "source_sha256")
	if !fieldOK {
		return slot, ErrUnavailable
	}
	slot.encoded, fieldOK = rowString(row, "secret_envelope")
	if !fieldOK || len(slot.encoded) > 256<<10 {
		return slot, ErrUnavailable
	}
	slot.envelopeSHA = LegacyOrderDigest([]byte(slot.encoded))
	for field, want := range map[string]string{"owner_type": "legacy_order", "owner_source_key": key, "field": "source_record", "kind": LegacyOrderRecordKind, "secret_sha256": slot.secretSHA, "target_id": slot.secretID, "canonical_sha256": slot.envelopeSHA, "lifecycle": "active"} {
		got, ok := rowString(row, field)
		if !ok || got != want {
			return slot, ErrUnavailable
		}
	}
	var secret legacyOrderSecret
	if decodeLegacyNodeJSON([]byte(slot.encoded), &secret) != nil || secret.SecretID != slot.secretID || secret.OwnerType != "legacy_order" || secret.OwnerSourceKey != key || secret.Field != "source_record" || secret.Kind != LegacyOrderRecordKind || secret.SHA256 != slot.secretSHA {
		return slot, ErrUnavailable
	}
	version, ok := rowInt64(row, "key_version")
	if !ok || int64(secret.KeyVersion) != version {
		return slot, ErrUnavailable
	}
	nonce, err := base64.StdEncoding.Strict().DecodeString(secret.NonceB64)
	if err != nil || base64.StdEncoding.EncodeToString(nonce) != secret.NonceB64 {
		return slot, ErrUnavailable
	}
	ciphertext, err := base64.StdEncoding.Strict().DecodeString(secret.CiphertextB64)
	if err != nil || base64.StdEncoding.EncodeToString(ciphertext) != secret.CiphertextB64 {
		return slot, ErrUnavailable
	}
	plain, err := s.store.secrets.Open(LegacyOrderRecordScope(key), Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: ciphertext})
	if err != nil {
		return slot, ErrUnavailable
	}
	defer wipeDesiredPayloadBytes(plain)
	if LegacyOrderDigest(plain) != slot.secretSHA || decodeLegacyNodeJSON(plain, &slot.record) != nil || slot.secretID != LegacyOrderRecordID(key, slot.secretSHA) {
		return slot, ErrUnavailable
	}
	slot.row, err = ValidateLegacyOrderRecord(s.store.secrets, slot.record)
	revision, rOK := rowInt64(row, "source_revision")
	historical, hOK := rowInt64(row, "historical_grant")
	customerID, _ := rowString(row, "customer_id")
	if err != nil || slot.record.OrderKeyHMAC != key || !rOK || revision != slot.record.Revision || !hOK || (historical != 0 && historical != 1) || (historical == 1) != (slot.row.Order.Status == "paid" || slot.row.Order.Credited) || customerID != slot.record.CustomerID || slot.sourceSHA != slot.record.OrdersSHA256 {
		return slot, ErrUnavailable
	}
	if row["accepted_order_id"] != nil {
		slot.acceptedID, ok = rowString(row, "accepted_order_id")
		if !ok || slot.acceptedID == "" {
			return slot, ErrUnavailable
		}
		slot.acceptedAt, ok = rowInt64(row, "accepted_at_unix")
		if !ok || slot.acceptedAt < 0 || historical != 0 || customerID == "" {
			return slot, ErrUnavailable
		}
	} else if row["accepted_at_unix"] != nil {
		return slot, ErrUnavailable
	}
	return slot, nil
}

func (s *Service) LegacyOrderByID(ctx context.Context, rawID string) (LegacyOrderView, error) {
	slot, err := s.loadLegacyOrder(ctx, rawID)
	if err != nil {
		return LegacyOrderView{}, err
	}
	return s.legacyOrderView(ctx, slot)
}

func (s *Service) legacyOrderView(ctx context.Context, slot legacyOrderSlot) (LegacyOrderView, error) {
	view := LegacyOrderView{Order: slot.row.Order, CustomerID: slot.record.CustomerID, HistoricalGrant: slot.row.Order.Status == "paid" || slot.row.Order.Credited, InternalOrderID: slot.acceptedID, AcceptedAtUnix: slot.acceptedAt}
	if slot.acceptedID == "" {
		return view, nil
	}
	current, err := s.queryOrder(ctx, slot.acceptedID)
	if err != nil || current.CustomerID != slot.record.CustomerID || current.View.AmountMinor != int64(view.Order.Rub)*100 || current.View.DurationSeconds != int64(view.Order.Days)*86400 {
		return LegacyOrderView{}, ErrUnavailable
	}
	switch current.View.PaymentState {
	case PaymentClaimed: // An accepted owner decision is never aged from source CreatedAt.
	case PaymentConfirmed:
		visibility, err := s.LegacyOrderVisibility(ctx, slot.acceptedID)
		if err != nil {
			return LegacyOrderView{}, err
		}
		view.Order.Status, view.Order.Credited = visibility, true
		if visibility == "paid" {
			access, err := s.customerAccess(ctx, slot.record.CustomerID)
			if err != nil {
				return LegacyOrderView{}, err
			}
			view.Order.SubToken = access.SubscriptionToken
		}
	case PaymentCanceled, PaymentExpired:
		return LegacyOrderView{}, ErrNotFound
	default:
		return LegacyOrderView{}, ErrUnavailable
	}
	return view, nil
}

func (s *Service) ListLegacyOrders(ctx context.Context) ([]LegacyOrderView, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT order_key_hmac FROM imported_legacy_order_aliases WHERE cancelled_at_unix IS NULL ORDER BY order_key_hmac LIMIT 4097`})
	if err != nil || len(results) != 1 || len(results[0].Rows) > 4096 {
		return nil, ErrUnavailable
	}
	views := make([]LegacyOrderView, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		key, ok := rowString(row, "order_key_hmac")
		if !ok {
			return nil, ErrUnavailable
		}
		slot, err := s.loadLegacyOrderKey(ctx, key)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		view, err := s.legacyOrderView(ctx, slot)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *Service) CancelLegacyOrder(ctx context.Context, rawID, actor string) error {
	if strings.TrimSpace(actor) == "" {
		return ErrForbidden
	}
	slot, err := s.loadLegacyOrder(ctx, rawID)
	if err != nil {
		return err
	}
	if slot.row.Order.Status == "paid" || slot.row.Order.Credited {
		return ErrConflict
	}
	if slot.acceptedID != "" {
		_, err = s.CancelOrder(ctx, CancelOrderCommand{OrderID: slot.acceptedID, IdempotencyKey: "legacy-import-cancel:" + slot.record.OrderKeyHMAC, Actor: actor, Channel: "legacy-import"})
		if err != nil {
			return err
		}
	}
	statements := []rqlite.Statement{{SQL: `UPDATE imported_legacy_order_aliases SET cancelled_at_unix=unixepoch() WHERE order_key_hmac=? AND record_secret_id=? AND historical_grant=0 AND cancelled_at_unix IS NULL AND (accepted_order_id IS NULL OR EXISTS(SELECT 1 FROM orders WHERE order_id=accepted_order_id AND payment_state='canceled'))`, Args: []any{slot.record.OrderKeyHMAC, slot.secretID}}, backupRPODirtyGenerationStatement(s.clock.Now().Unix()),
		{SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,created_at_unix) SELECT ?,?,'legacy-order.cancel','order',?,unixepoch() WHERE EXISTS(SELECT 1 FROM imported_legacy_order_aliases WHERE order_key_hmac=? AND cancelled_at_unix IS NOT NULL) ON CONFLICT(event_id) DO NOTHING`, Args: []any{"legacy-cancel-" + slot.record.OrderKeyHMAC, s.auditActor(actor), s.auditResource(rawID), slot.record.OrderKeyHMAC}}}
	_, writeErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	_, err = s.loadLegacyOrder(ctx, rawID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if writeErr != nil && isUnknownWrite(writeErr) {
		return ErrUnavailable
	}
	return ErrConflict
}

// ConfirmLegacyOrder is called only from the authenticated owner route. Paid
// or already-credited source records have no path to a new durable grant.
// Acceptance and grant are separate durable steps so an uncertain write or
// process restart resumes the same internal order with the same decision key.
func (s *Service) ConfirmLegacyOrder(ctx context.Context, rawID, actor string) (LegacyOrderView, ConfirmPaymentResult, error) {
	if strings.TrimSpace(actor) == "" {
		return LegacyOrderView{}, ConfirmPaymentResult{}, ErrForbidden
	}
	slot, err := s.loadLegacyOrder(ctx, rawID)
	if err != nil {
		return LegacyOrderView{}, ConfirmPaymentResult{}, err
	}
	if slot.row.Order.Status == "paid" || slot.row.Order.Credited {
		view, err := s.LegacyOrderByID(ctx, rawID)
		return view, ConfirmPaymentResult{}, err
	}
	if slot.record.CustomerID == "" {
		return LegacyOrderView{}, ConfirmPaymentResult{}, ErrConflict
	}
	if slot.acceptedID == "" {
		if err := s.acceptLegacyOrder(ctx, slot, actor); err != nil {
			return LegacyOrderView{}, ConfirmPaymentResult{}, err
		}
		slot, err = s.loadLegacyOrder(ctx, rawID)
		if err != nil || slot.acceptedID == "" {
			return LegacyOrderView{}, ConfirmPaymentResult{}, ErrUnavailable
		}
	}
	key := slot.record.OrderKeyHMAC
	result, err := s.ConfirmPayment(ctx, ConfirmPaymentCommand{OrderID: slot.acceptedID, IdempotencyKey: "legacy-import-confirm:" + key, PaymentReference: "legacy-import:" + key, Provider: "manual-sbp", TariffVersionID: legacyOrderTariffID(slot.row.Order), Actor: actor, Channel: "legacy-import", SourceEventID: key})
	if err != nil {
		return LegacyOrderView{}, ConfirmPaymentResult{}, err
	}
	view, err := s.LegacyOrderByID(ctx, rawID)
	return view, result, err
}

func legacyOrderTariffID(order legacyorder.Order) string {
	raw, _ := json.Marshal(struct {
		Code      string
		Days, Rub int
	}{order.Tariff, order.Days, order.Rub})
	return "legacy-terms-" + LegacyOrderDigest(raw)
}

func (s *Service) acceptLegacyOrder(ctx context.Context, slot legacyOrderSlot, actor string) error {
	order := slot.row.Order
	key := slot.record.OrderKeyHMAC
	orderID := "legacy-accepted-" + key
	operationID := "legacy-accept-" + key
	tariffID := legacyOrderTariffID(order)
	// A unique archived terms code avoids manufacturing a past tariff version
	// and cannot alter today's default price or active tariff menu.
	termsCode := "legacy-terms:" + tariffID
	guard := `EXISTS(SELECT 1 FROM imported_legacy_order_aliases a JOIN imported_secrets i ON i.secret_id=a.record_secret_id JOIN imported_entity_state e ON e.entity_kind='encrypted_secret' AND e.source_key=i.secret_id WHERE a.order_key_hmac=? AND a.record_secret_id=? AND a.record_sha256=? AND a.source_revision=? AND a.accepted_order_id IS NULL AND a.cancelled_at_unix IS NULL AND a.historical_grant=0 AND a.customer_id=? AND CAST(i.secret_envelope AS TEXT)=? AND e.canonical_sha256=? AND e.lifecycle='active')`
	guardArgs := []any{key, slot.secretID, slot.secretSHA, slot.record.Revision, slot.record.CustomerID, slot.encoded, slot.envelopeSHA}
	statements := []rqlite.Statement{{SQL: `INSERT INTO tariff_versions(tariff_version_id,tariff_code,duration_days,amount_minor,currency,active,created_at_unix) SELECT ?,?,?,?,'RUB',0,unixepoch() WHERE ` + guard + ` ON CONFLICT(tariff_version_id) DO NOTHING`, Args: append([]any{tariffID, termsCode, order.Days, int64(order.Rub) * 100}, guardArgs...)},
		{SQL: `INSERT INTO orders(order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,provisioning_state,operation_id)
 SELECT ?,?,'legacy-import',?,c.customer_id,t.tariff_version_id,t.amount_minor,t.currency,t.duration_days,unixepoch(),unixepoch()+86400,'payment_claimed','none',?
 FROM customers c JOIN tariff_versions t ON t.tariff_version_id=? JOIN imported_entity_state ce ON ce.entity_kind='customer' AND ce.source_key=? AND ce.target_id=c.customer_id AND ce.lifecycle='active'
 WHERE c.customer_id=? AND c.login_key_hmac=? AND c.status IN ('active','expired','suspended') AND t.amount_minor=? AND t.currency='RUB' AND t.duration_days=? AND ` + guard,
			Args: append([]any{orderID, order.Code, key, operationID, tariffID, slot.record.CustomerSourceKey, slot.record.CustomerID, slot.record.CustomerLoginHMAC, int64(order.Rub) * 100, order.Days}, guardArgs...)},
		{SQL: `UPDATE imported_legacy_order_aliases SET accepted_order_id=?,accepted_at_unix=(SELECT created_at_unix FROM orders WHERE order_id=?) WHERE order_key_hmac=? AND record_secret_id=? AND record_sha256=? AND source_revision=? AND accepted_order_id IS NULL AND historical_grant=0 AND EXISTS(SELECT 1 FROM orders WHERE order_id=?)`, Args: []any{orderID, orderID, key, slot.secretID, slot.secretSHA, slot.record.Revision, orderID}},
		backupRPODirtyGenerationStatement(s.clock.Now().Unix()),
		{SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,created_at_unix) SELECT ?,?,'legacy-order.accept','order',?,unixepoch() WHERE EXISTS(SELECT 1 FROM imported_legacy_order_aliases WHERE order_key_hmac=? AND accepted_order_id=?)`, Args: []any{operationID, s.auditActor(actor), s.auditResource(orderID), key, orderID}}}
	_, writeErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	resolved, readErr := s.loadLegacyOrder(ctx, order.ID)
	if readErr == nil && resolved.acceptedID == orderID && resolved.secretID == slot.secretID && resolved.secretSHA == slot.secretSHA {
		return nil
	}
	if writeErr != nil && isUnknownWrite(writeErr) {
		return ErrUnavailable
	}
	if readErr != nil && !errors.Is(readErr, ErrNotFound) {
		return ErrUnavailable
	}
	return ErrConflict
}
