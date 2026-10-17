package controlplane_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestServiceBusinessInstalledAppRenewalOrderSQLite(t *testing.T) {
	ctx := context.Background()
	db := newS4CanarySQLite(t)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("apply real migrations: %v", err)
	}
	box, err := controlplane.NewSecretBox(
		1,
		map[int][]byte{1: bytes.Repeat([]byte{0x51}, 32)},
		bytes.Repeat([]byte{0x52}, 32),
	)
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	clock := s4CanaryClock{value: time.Unix(2_000_000, 0).UTC()}
	store, err := controlplane.NewStore(db, box, clock)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ids := &f6SequenceIDs{}
	service, err := controlplane.NewService(store, ids, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	existing, err := service.ProvisionCustomer(ctx, controlplane.ProvisionCustomerCommand{
		Login: "Existing", Days: 30, IdempotencyKey: "seed-existing",
	})
	if err != nil {
		t.Fatalf("seed existing customer: %v", err)
	}
	if existing.Access.SubscriptionToken == "" {
		t.Fatal("seeded existing customer has no subscription token")
	}

	business := api.NewServiceBusiness(service, api.ServiceBusinessConfig{
		SubBaseURL: "https://service.invalid",
	})
	handler := api.NewControlPlane(business, api.Config{AdminToken: "admin-secret"}).Handler()
	order := f6CreateRenewalOrder(t, handler, existing.Access.SubscriptionToken)
	if order.Status != "pending" {
		t.Fatalf("created public status=%q, want pending", order.Status)
	}
	f6AssertOrderCustomer(t, db, order.OrderID, existing.ID)

	pending := f6ReadOrder(t, handler, order.OrderID, http.StatusOK)
	if pending.Status != "pending" || pending.SubURL != "" {
		t.Fatalf("initial poll=%+v, want pending without sub_url", pending)
	}

	claim := httptest.NewRequest(http.MethodPost, "/order/paid-claim", strings.NewReader(
		fmt.Sprintf(`{"order_id":%q}`, order.OrderID),
	))
	claimResponse := httptest.NewRecorder()
	handler.ServeHTTP(claimResponse, claim)
	if claimResponse.Code != http.StatusOK || strings.TrimSpace(claimResponse.Body.String()) != `{"status":"awaiting_confirm"}` {
		t.Fatalf("paid-claim status=%d body=%q, want exact legacy acknowledgement", claimResponse.Code, claimResponse.Body.String())
	}

	confirm := httptest.NewRequest(http.MethodPost, "/admin/order/confirm", strings.NewReader(
		fmt.Sprintf(`{"order_id":%q}`, order.OrderID),
	))
	confirm.Header.Set("Authorization", "Bearer admin-secret")
	confirm.Header.Set("Idempotency-Key", "confirm-renewal")
	confirmResponse := httptest.NewRecorder()
	handler.ServeHTTP(confirmResponse, confirm)
	if confirmResponse.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%q, want 200", confirmResponse.Code, confirmResponse.Body.String())
	}

	pending = f6ReadOrder(t, handler, order.OrderID, http.StatusOK)
	if pending.Status != "pending" || pending.SubURL != "" || pending.PaymentState != "confirmed" {
		t.Fatalf("pre-receipt poll=%+v, want pending with raw confirmed payment state", pending)
	}
	f6ApplyOneDesiredReceipt(t, db, box, order.OrderID, existing.Access)

	paid := f6ReadOrder(t, handler, order.OrderID, http.StatusOK)
	wantSubURL := "https://service.invalid/sub/" + existing.Access.SubscriptionToken
	if paid.Status != "paid" || paid.SubURL != wantSubURL || paid.PaymentState != "confirmed" {
		t.Fatalf("ready poll=%+v, want paid with same subscription URL %q", paid, wantSubURL)
	}
	f6AssertOrderCustomer(t, db, order.OrderID, existing.ID)

	adminOrders, err := business.ListOrders(ctx, api.OrderFilter{})
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	var adminOrder api.OrderView
	for _, candidate := range adminOrders {
		if candidate.OrderID == order.OrderID {
			adminOrder = candidate
			break
		}
	}
	if adminOrder.OrderID == "" || adminOrder.Status != "confirmed" || adminOrder.PaymentState != "confirmed" {
		t.Fatalf("admin order=%+v, want raw confirmed state", adminOrder)
	}

	for _, failure := range []struct {
		name     string
		fragment string
	}{
		{"visibility", "AS receipt_ready"},
		{"customer", "FROM customers WHERE customer_id=? AND status<>'deleted'"},
	} {
		t.Run(failure.name+"-failure-is-503", func(t *testing.T) {
			faultStore, storeErr := controlplane.NewStore(f6QueryFailureDB{RQLite: db, fragment: failure.fragment}, box, clock)
			if storeErr != nil {
				t.Fatalf("fault NewStore: %v", storeErr)
			}
			faultService, serviceErr := controlplane.NewService(faultStore, &f6SequenceIDs{}, clock)
			if serviceErr != nil {
				t.Fatalf("fault NewService: %v", serviceErr)
			}
			faultHandler := api.NewControlPlane(
				api.NewServiceBusiness(faultService, api.ServiceBusinessConfig{SubBaseURL: "https://service.invalid"}),
				api.Config{},
			).Handler()
			f6ReadOrder(t, faultHandler, order.OrderID, http.StatusServiceUnavailable)
		})
	}

	canceled := f6CreateRenewalOrder(t, handler, existing.Access.SubscriptionToken)
	if canceled.OrderID == order.OrderID {
		t.Fatalf("two keyless taps collapsed to order %q", canceled.OrderID)
	}
	f6AssertOrderCustomer(t, db, canceled.OrderID, existing.ID)
	cancel := httptest.NewRequest(http.MethodPost, "/admin/order/cancel", strings.NewReader(
		fmt.Sprintf(`{"order_id":%q}`, canceled.OrderID),
	))
	cancel.Header.Set("Authorization", "Bearer admin-secret")
	cancel.Header.Set("Idempotency-Key", "cancel-renewal")
	cancelResponse := httptest.NewRecorder()
	handler.ServeHTTP(cancelResponse, cancel)
	if cancelResponse.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%q, want 200", cancelResponse.Code, cancelResponse.Body.String())
	}
	f6ReadOrder(t, handler, canceled.OrderID, http.StatusNotFound)

	unknownToken := f6CreateRenewalOrder(t, handler, "unknown-token")
	firstTime := f6CreateFirstTimeOrder(t, handler)
	if unknownToken.OrderID == firstTime.OrderID {
		t.Fatalf("unknown-token and tokenless taps collapsed to order %q", firstTime.OrderID)
	}
	if unknownToken.Status != "pending" || unknownToken.SubURL != "" || firstTime.Status != "pending" || firstTime.SubURL != "" {
		t.Fatalf("first-time public orders unknown=%+v omitted=%+v, want pending without sub_url", unknownToken, firstTime)
	}
	firstTimeRows := db.must(t, rqlite.Statement{SQL: `
SELECT o.order_id,o.customer_id,c.display_login,c.login_key_hmac,
CAST(c.expires_at_unix AS TEXT) AS expires_at_unix,c.status,
CAST((SELECT count(*) FROM desired_node_state d WHERE d.customer_id=c.customer_id) AS TEXT) AS desired_count,
CAST((SELECT count(*) FROM outbox_events e WHERE e.aggregate_id LIKE c.customer_id || ':%') AS TEXT) AS outbox_count
FROM orders o JOIN customers c ON c.customer_id=o.customer_id
WHERE o.order_id IN (?,?) ORDER BY o.order_id`, Args: []any{unknownToken.OrderID, firstTime.OrderID}})
	if len(firstTimeRows) != 1 || len(firstTimeRows[0].Rows) != 2 {
		t.Fatalf("first-time rows=%#v, want two distinct intents", firstTimeRows)
	}
	firstCustomerIDs := map[string]struct{}{}
	firstDisplayLogins := map[string]struct{}{}
	firstCustomers := map[string]controlplane.BusinessCustomer{}
	for _, row := range firstTimeRows[0].Rows {
		customerID, _ := row["customer_id"].(string)
		displayLogin, _ := row["display_login"].(string)
		canonicalLogin, canonicalErr := controlplane.CanonicalLoginKey(displayLogin)
		if canonicalErr != nil || row["login_key_hmac"] != box.LookupHMAC("customer-login", []byte(canonicalLogin)) {
			t.Fatalf("first-time identity row=%#v canonicalErr=%v, want valid unique keyed login", row, canonicalErr)
		}
		if customerID == "" || customerID == existing.ID || row["expires_at_unix"] != "2000000" || row["status"] != "expired" ||
			row["desired_count"] != "0" || row["outbox_count"] != "0" {
			t.Fatalf("first-time row=%#v, want a sealed but inert expired customer without desired/outbox", row)
		}
		decrypted, decryptErr := service.BusinessCustomerByLogin(ctx, displayLogin)
		if decryptErr != nil || decrypted.ID != customerID || decrypted.Access.SubscriptionToken == "" || len(decrypted.Access.Credentials) != 4 {
			t.Fatalf("first-time sealed access customer=%+v err=%v, want exact decryptable customer", decrypted, decryptErr)
		}
		orderID, _ := row["order_id"].(string)
		firstCustomerIDs[customerID] = struct{}{}
		firstDisplayLogins[displayLogin] = struct{}{}
		firstCustomers[orderID] = decrypted
	}
	if len(firstCustomerIDs) != 2 || len(firstDisplayLogins) != 2 {
		t.Fatalf("first-time customers=%#v logins=%#v, want a fresh identity per keyless tap", firstCustomerIDs, firstDisplayLogins)
	}
	firstTimeCustomer, ok := firstCustomers[firstTime.OrderID]
	if !ok {
		t.Fatalf("first-time order %q has no exact customer mapping: %#v", firstTime.OrderID, firstCustomers)
	}
	firstClaim := httptest.NewRequest(http.MethodPost, "/order/paid-claim", strings.NewReader(
		fmt.Sprintf(`{"order_id":%q}`, firstTime.OrderID),
	))
	firstClaimResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstClaimResponse, firstClaim)
	if firstClaimResponse.Code != http.StatusOK || strings.TrimSpace(firstClaimResponse.Body.String()) != `{"status":"awaiting_confirm"}` {
		t.Fatalf("first-time paid-claim status=%d body=%q, want exact legacy acknowledgement", firstClaimResponse.Code, firstClaimResponse.Body.String())
	}
	firstConfirm := httptest.NewRequest(http.MethodPost, "/admin/order/confirm", strings.NewReader(
		fmt.Sprintf(`{"order_id":%q}`, firstTime.OrderID),
	))
	firstConfirm.Header.Set("Authorization", "Bearer admin-secret")
	firstConfirm.Header.Set("Idempotency-Key", "confirm-first-time")
	firstConfirmResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstConfirmResponse, firstConfirm)
	if firstConfirmResponse.Code != http.StatusOK {
		t.Fatalf("first-time confirm status=%d body=%q, want 200", firstConfirmResponse.Code, firstConfirmResponse.Body.String())
	}
	firstPending := f6ReadOrder(t, handler, firstTime.OrderID, http.StatusOK)
	if firstPending.Status != "pending" || firstPending.SubURL != "" || firstPending.PaymentState != "confirmed" {
		t.Fatalf("first-time pre-receipt poll=%+v, want pending with raw confirmed payment state", firstPending)
	}
	f6ApplyOneDesiredReceipt(t, db, box, firstTime.OrderID, firstTimeCustomer.Access)
	firstPaid := f6ReadOrder(t, handler, firstTime.OrderID, http.StatusOK)
	wantFirstSubURL := "https://service.invalid/sub/" + firstTimeCustomer.Access.SubscriptionToken
	if firstPaid.Status != "paid" || firstPaid.SubURL != wantFirstSubURL || firstPaid.PaymentState != "confirmed" {
		t.Fatalf("first-time ready poll=%+v, want paid with minted subscription URL %q", firstPaid, wantFirstSubURL)
	}

	const expiredOrderID = "expired-order-f6"
	db.must(t, rqlite.Statement{SQL: `
INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,operation_id,origin_bot_id,origin_chat_key_hmac)
SELECT ?,?,'legacy-http',?,?,t.tariff_version_id,t.amount_minor,t.currency,t.duration_days,
1000000,1086400,'created','none',NULL,?,'',NULL
FROM tariff_versions t WHERE t.tariff_code='1m' AND t.active=1`, Args: []any{
		expiredOrderID, "expired-code-f6", box.LookupHMAC("order-buyer:legacy-http", []byte(expiredOrderID)),
		existing.ID, "expired-operation-f6",
	}})
	f6ReadOrder(t, handler, expiredOrderID, http.StatusNotFound)
	expiredRows := db.must(t, rqlite.Statement{
		SQL: `SELECT payment_state FROM orders WHERE order_id=?`, Args: []any{expiredOrderID},
	})
	if len(expiredRows) != 1 || len(expiredRows[0].Rows) != 1 || expiredRows[0].Rows[0]["payment_state"] != "expired" {
		t.Fatalf("lazy expiry rows=%#v, want persisted expired state", expiredRows)
	}
}

type f6OrderResponse struct {
	OrderID          string `json:"order_id"`
	Status           string `json:"status"`
	SubURL           string `json:"sub_url"`
	PaymentState     string `json:"payment_state"`
	ResultGeneration int64  `json:"result_generation"`
}

func f6CreateRenewalOrder(t *testing.T, handler http.Handler, token string) f6OrderResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/order", strings.NewReader(
		fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, token),
	))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("create renewal status=%d body=%q, want 200", response.Code, response.Body.String())
	}
	return f6DecodeOrder(t, response)
}

func f6CreateFirstTimeOrder(t *testing.T, handler http.Handler) f6OrderResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/order", strings.NewReader(`{"tariff":"1m"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("create first-time status=%d body=%q, want 200", response.Code, response.Body.String())
	}
	return f6DecodeOrder(t, response)
}

func f6ReadOrder(t *testing.T, handler http.Handler, orderID string, wantStatus int) f6OrderResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/order/"+orderID, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("read %s status=%d body=%q, want %d", orderID, response.Code, response.Body.String(), wantStatus)
	}
	if wantStatus != http.StatusOK {
		return f6OrderResponse{}
	}
	return f6DecodeOrder(t, response)
}

func f6DecodeOrder(t *testing.T, response *httptest.ResponseRecorder) f6OrderResponse {
	t.Helper()
	var order f6OrderResponse
	if err := json.Unmarshal(response.Body.Bytes(), &order); err != nil {
		t.Fatalf("decode order response %q: %v", response.Body.String(), err)
	}
	return order
}

func f6AssertOrderCustomer(t *testing.T, db *s4CanarySQLite, orderID, wantCustomerID string) {
	t.Helper()
	results := db.must(t, rqlite.Statement{
		SQL: `SELECT customer_id FROM orders WHERE order_id=?`, Args: []any{orderID},
	})
	if len(results) != 1 || len(results[0].Rows) != 1 || results[0].Rows[0]["customer_id"] != wantCustomerID {
		t.Fatalf("order %s rows=%#v, want customer_id=%q", orderID, results, wantCustomerID)
	}
}

func f6ApplyOneDesiredReceipt(
	t *testing.T,
	db *s4CanarySQLite,
	box *controlplane.SecretBox,
	orderID string,
	wantAccess controlplane.CustomerAccess,
) {
	t.Helper()
	results := db.must(t, rqlite.Statement{SQL: `
SELECT d.customer_id,d.node_id,d.service_name,d.generation,d.desired_sha256,d.desired_envelope,d.operation_id
FROM desired_node_state d
JOIN orders o ON o.customer_id=d.customer_id AND o.result_generation=d.generation
JOIN node_services ns ON ns.node_id=d.node_id AND ns.service_name=d.service_name
JOIN nodes n ON n.node_id=d.node_id
WHERE o.order_id=? AND ns.desired_target=1 AND ns.apply_enabled=1 AND ns.fenced=0 AND ns.retired=0
AND n.enabled=1 LIMIT 1`, Args: []any{orderID}})
	if len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("desired receipt target rows=%#v, want one eligible target", results)
	}
	row := results[0].Rows[0]
	encodedEnvelope, envelopeOK := row["desired_envelope"].(string)
	envelopeBytes, decodeErr := base64.StdEncoding.DecodeString(encodedEnvelope)
	var envelope controlplane.Envelope
	if !envelopeOK || decodeErr != nil || json.Unmarshal(envelopeBytes, &envelope) != nil {
		t.Fatalf("desired envelope row=%#v decodeErr=%v, want encoded envelope", row, decodeErr)
	}
	generation, generationOK := row["generation"].(float64)
	nodeID, nodeOK := row["node_id"].(string)
	serviceName, serviceOK := row["service_name"].(string)
	customerID, customerOK := row["customer_id"].(string)
	operationID, operationOK := row["operation_id"].(string)
	digest, digestOK := row["desired_sha256"].(string)
	if !generationOK || !nodeOK || !serviceOK || !customerOK || !operationOK || !digestOK {
		t.Fatalf("desired scope row=%#v, want complete scope", row)
	}
	document, openErr := box.OpenDesiredPayload(controlplane.DesiredPayloadScope{
		NodeID: nodeID, ServiceID: serviceName, CustomerID: customerID, Generation: int64(generation),
		OperationID: operationID, PayloadKind: "customer-active",
	}, envelope, digest)
	var body struct {
		Access controlplane.CustomerAccess `json:"access"`
	}
	if openErr != nil || json.Unmarshal(document.Body, &body) != nil ||
		body.Access.SubscriptionToken != wantAccess.SubscriptionToken || !reflect.DeepEqual(body.Access.Credentials, wantAccess.Credentials) {
		t.Fatalf("desired access=%+v openErr=%v, want exact canonical access %+v", body.Access, openErr, wantAccess)
	}
	db.must(t, rqlite.Statement{SQL: `
INSERT INTO node_apply_receipts(
receipt_id,customer_id,node_id,service_name,generation,desired_sha256,status,observed_sha256,error_code,applied_at_unix,created_at_unix)
VALUES (?,?,?,?,?,?,'applied',?,NULL,2000000,2000000)`, Args: []any{
		"receipt-f6-" + orderID, row["customer_id"], row["node_id"], row["service_name"], row["generation"],
		row["desired_sha256"], row["desired_sha256"],
	}})
}

type f6SequenceIDs struct{ next int }

func (ids *f6SequenceIDs) NewID(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s-f6-%d", prefix, ids.next), nil
}

type f6QueryFailureDB struct {
	rqlite.RQLite
	fragment string
}

func (db f6QueryFailureDB) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	for _, statement := range statements {
		if strings.Contains(statement.SQL, db.fragment) {
			return nil, errors.New("injected F6 query failure")
		}
	}
	return db.RQLite.QueryLinearizable(ctx, statements...)
}
