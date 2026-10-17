package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	commercialAccountA       = "account-a"
	commercialAccountB       = "account-b"
	commercialTokenA         = "account-a-token"
	commercialCredentialLeak = "credential-secret-must-not-leak"
	commercialNodeLeak       = "node-secret-must-not-leak"
)

// commercialBusinessFake is an executable HTTP-boundary fixture. Task 7 owns
// the durable service proof; these tests require Task 8 to preserve typed
// commands, durable identities, and account ownership at the API port.
type commercialBusinessFake struct {
	Business

	customers map[string]CustomerView
	catalog   CommercialCatalogView
	bindings  map[string]CommercialOrderBindingView
	orders    map[string]CommercialOrderView
	balance   WhiteListBalanceView
	delivery  CommercialDeliveryView

	createCommercialErr error
	claimCommercialErr  error
	balanceErr          error
	deliveryErr         error

	customerTokens         []string
	balanceAccountIDs      []string
	bindingOrderIDs        []string
	createCommercialCalls  []CommercialOrderCommand
	claimCommercialCalls   []CommercialClaimCommand
	confirmCommercialCalls []CommercialOrderDecisionCommand
	rejectCommercialCalls  []CommercialOrderDecisionCommand
	publicationCalls       []CommercialPublicationCommand
	deliveryCalls          []CommercialDeliveryCommand

	claimMutationKeys       map[string]struct{}
	confirmMutationKeys     map[string]struct{}
	publicationMutationKeys map[string]struct{}

	legacyTariffs       []TariffView
	legacyCreateResult  OrderView
	legacyClaimResult   OrderView
	legacyConfirmResult ConfirmPaymentResult
	legacyCancelResult  OrderView
	legacyTariffCalls   int
	legacyCreateCalls   []CreateOrderCommand
	legacyClaimCalls    []ClaimPaymentCommand
	legacyConfirmCalls  []ConfirmPaymentCommand
	legacyCancelCalls   []CancelOrderCommand
	legacyEnableCalls   int
	legacyDisableCalls  int
}

var _ CommercialBusiness = (*commercialBusinessFake)(nil)

type commercialTestError struct {
	status  int
	message string
}

func (err commercialTestError) Error() string   { return err.message }
func (err commercialTestError) HTTPStatus() int { return err.status }

func (b *commercialBusinessFake) CustomerByToken(_ context.Context, token string) (CustomerView, error) {
	b.customerTokens = append(b.customerTokens, token)
	customer, ok := b.customers[token]
	if !ok {
		return CustomerView{}, commercialTestError{status: http.StatusUnauthorized, message: "unknown account token"}
	}
	return customer, nil
}

func (b *commercialBusinessFake) CommercialCatalog(context.Context) (CommercialCatalogView, error) {
	return b.catalog, nil
}

func (b *commercialBusinessFake) CommercialOrderBinding(_ context.Context, orderID string) (CommercialOrderBindingView, error) {
	b.bindingOrderIDs = append(b.bindingOrderIDs, orderID)
	binding, ok := b.bindings[orderID]
	if !ok {
		return CommercialOrderBindingView{}, commercialTestError{status: http.StatusNotFound, message: "order not found"}
	}
	return binding, nil
}

func (b *commercialBusinessFake) CreateCommercialOrder(_ context.Context, command CommercialOrderCommand) (CommercialOrderView, error) {
	b.createCommercialCalls = append(b.createCommercialCalls, command)
	if b.createCommercialErr != nil {
		return CommercialOrderView{}, b.createCommercialErr
	}
	return b.orders[command.ProductID], nil
}

func (b *commercialBusinessFake) ClaimCommercialPayment(_ context.Context, command CommercialClaimCommand) (CommercialOrderView, error) {
	b.claimCommercialCalls = append(b.claimCommercialCalls, command)
	if b.claimCommercialErr != nil {
		return CommercialOrderView{}, b.claimCommercialErr
	}
	if b.claimMutationKeys == nil {
		b.claimMutationKeys = map[string]struct{}{}
	}
	b.claimMutationKeys[command.IdempotencyKey] = struct{}{}
	return b.orders[command.OrderID], nil
}

func (b *commercialBusinessFake) ConfirmCommercialOrder(_ context.Context, command CommercialOrderDecisionCommand) (CommercialOrderView, error) {
	b.confirmCommercialCalls = append(b.confirmCommercialCalls, command)
	if b.confirmMutationKeys == nil {
		b.confirmMutationKeys = map[string]struct{}{}
	}
	b.confirmMutationKeys[command.IdempotencyKey] = struct{}{}
	return b.orders[command.OrderID], nil
}

func (b *commercialBusinessFake) RejectCommercialOrder(_ context.Context, command CommercialOrderDecisionCommand) (CommercialOrderView, error) {
	b.rejectCommercialCalls = append(b.rejectCommercialCalls, command)
	return b.orders[command.OrderID], nil
}

func (b *commercialBusinessFake) WhiteListBalance(_ context.Context, accountID string) (WhiteListBalanceView, error) {
	b.balanceAccountIDs = append(b.balanceAccountIDs, accountID)
	if b.balanceErr != nil {
		return WhiteListBalanceView{}, b.balanceErr
	}
	view := b.balance
	view.AccountID = accountID
	return view, nil
}

func (b *commercialBusinessFake) SetWhiteListPublication(_ context.Context, command CommercialPublicationCommand) (CommercialPublicationView, error) {
	b.publicationCalls = append(b.publicationCalls, command)
	if b.publicationMutationKeys == nil {
		b.publicationMutationKeys = map[string]struct{}{}
	}
	mutationKey := command.AccountID + ":" + command.IdempotencyKey
	b.publicationMutationKeys[mutationKey] = struct{}{}
	return CommercialPublicationView{
		AccountID:   command.AccountID,
		Enabled:     command.Enabled,
		Version:     7,
		OperationID: "publication-operation-1",
		AuditID:     "publication-audit-1",
	}, nil
}

func (b *commercialBusinessFake) SubscriptionDelivery(_ context.Context, command CommercialDeliveryCommand) (CommercialDeliveryView, error) {
	b.deliveryCalls = append(b.deliveryCalls, command)
	if b.deliveryErr != nil {
		return CommercialDeliveryView{}, b.deliveryErr
	}
	return b.delivery, nil
}

func (b *commercialBusinessFake) Tariffs(context.Context) ([]TariffView, error) {
	b.legacyTariffCalls++
	return b.legacyTariffs, nil
}

func (b *commercialBusinessFake) CreateOrder(_ context.Context, command CreateOrderCommand) (OrderView, error) {
	b.legacyCreateCalls = append(b.legacyCreateCalls, command)
	return b.legacyCreateResult, nil
}

func (b *commercialBusinessFake) MarkPaymentClaimed(_ context.Context, command ClaimPaymentCommand) (OrderView, error) {
	b.legacyClaimCalls = append(b.legacyClaimCalls, command)
	return b.legacyClaimResult, nil
}

func (b *commercialBusinessFake) ConfirmPayment(_ context.Context, command ConfirmPaymentCommand) (ConfirmPaymentResult, error) {
	b.legacyConfirmCalls = append(b.legacyConfirmCalls, command)
	return b.legacyConfirmResult, nil
}

func (b *commercialBusinessFake) CancelOrder(_ context.Context, command CancelOrderCommand) (OrderView, error) {
	b.legacyCancelCalls = append(b.legacyCancelCalls, command)
	return b.legacyCancelResult, nil
}

func (b *commercialBusinessFake) EnableCustomer(context.Context, CustomerStateCommand) (CustomerView, error) {
	b.legacyEnableCalls++
	return CustomerView{}, errors.New("ordinary access enable must not be called")
}

func (b *commercialBusinessFake) DisableCustomer(context.Context, CustomerStateCommand) (CustomerView, error) {
	b.legacyDisableCalls++
	return CustomerView{}, errors.New("ordinary access disable must not be called")
}

func commercialRequest(t *testing.T, handler http.Handler, method, path, body, bearer, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeCommercialJSON[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
	if decoder.Decode(&struct{}{}) == nil {
		t.Fatalf("response contains more than one JSON value: %q", response.Body.String())
	}
	return value
}

func newCommercialBusinessFake() *commercialBusinessFake {
	return &commercialBusinessFake{
		customers: map[string]CustomerView{
			commercialTokenA: {
				CustomerID: commercialAccountA,
				Login:      "alice",
				Active:     true,
				Expires:    time.Unix(2_000_000_000, 0).UTC(),
			},
		},
		catalog: CommercialCatalogView{
			Access: TariffView{ID: "month", Days: 30, RUB: 400},
			Products: []CommercialProductView{
				{ID: "wl-gb-5-v1", Kind: "WHITELIST_BYTES", AmountMinor: 10_000, Currency: "RUB", Bytes: 5_000_000_000, Unit: "GB_DECIMAL"},
				{ID: "wl-gb-20-v1", Kind: "WHITELIST_BYTES", AmountMinor: 30_000, Currency: "RUB", Bytes: 20_000_000_000, Unit: "GB_DECIMAL"},
				{ID: "wl-gb-50-v1", Kind: "WHITELIST_BYTES", AmountMinor: 60_000, Currency: "RUB", Bytes: 50_000_000_000, Unit: "GB_DECIMAL"},
				{ID: "wl-gb-100-v1", Kind: "WHITELIST_BYTES", AmountMinor: 100_000, Currency: "RUB", Bytes: 100_000_000_000, Unit: "GB_DECIMAL"},
			},
		},
		bindings: map[string]CommercialOrderBindingView{},
		orders: map[string]CommercialOrderView{
			"wl-gb-5-v1": {
				OrderID: "topup-order-1", AccountID: commercialAccountA,
				ProductID: "wl-gb-5-v1", AmountMinor: 10_000, Currency: "RUB",
				Bytes: 5_000_000_000, PaymentState: "created",
			},
			"topup-order-1": {
				OrderID: "topup-order-1", AccountID: commercialAccountA,
				ProductID: "wl-gb-5-v1", AmountMinor: 10_000, Currency: "RUB",
				Bytes: 5_000_000_000, PaymentState: "payment_claimed",
			},
		},
		balance: WhiteListBalanceView{
			IncludedRemainingBytes:  1_250_000_000,
			PurchasedRemainingBytes: 7_500_000_000,
			AvailableBytes:          8_750_000_000,
			PeriodEndsAtUnix:        2_000_000_000,
			PrimaryAccessState:      "active",
			PublicationVerdict:      "PUBLISHABLE",
		},
		legacyTariffs: []TariffView{{ID: "month", Days: 30, RUB: 400}},
		legacyCreateResult: OrderView{
			OrderID: "legacy-order-1", Code: "LEGACY", RUB: 400, Days: 30,
			Tariff: "month", SBPPhone: "+70000000000", PayURL: "https://pay.example/order", Status: "created",
		},
		legacyClaimResult: OrderView{OrderID: "legacy-order-1", Status: "payment_claimed"},
		legacyConfirmResult: ConfirmPaymentResult{
			Order: OrderView{OrderID: "legacy-order-1", Status: "paid"},
		},
		legacyCancelResult: OrderView{OrderID: "legacy-order-1", Status: "canceled"},
	}
}

func TestCommercialCatalogReturnsExactAccessAndDecimalGBProducts(t *testing.T) {
	business := newCommercialBusinessFake()
	response := commercialRequest(t, NewControlPlane(business, Config{}).Handler(), http.MethodGet, "/order/catalog", "", "", "")

	if response.Code != http.StatusOK {
		t.Fatalf("GET /order/catalog status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	got := decodeCommercialJSON[CommercialCatalogView](t, response)
	if !reflect.DeepEqual(got, business.catalog) {
		t.Fatalf("catalog = %#v, want %#v", got, business.catalog)
	}
	if len(got.Products) != 4 {
		t.Fatalf("catalog product count = %d, want 4", len(got.Products))
	}
}

func TestCommercialProductOrderResolvesTokenAndDispatchesExactProduct(t *testing.T) {
	business := newCommercialBusinessFake()
	handler := NewControlPlane(business, Config{}).Handler()
	response := commercialRequest(t, handler, http.MethodPost, "/order", `{"product_id":"wl-gb-5-v1","sub_token":"account-a-token"}`, "", "create-key-1")

	if response.Code != http.StatusOK {
		t.Fatalf("POST /order status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	wantCommand := CommercialOrderCommand{AccountID: commercialAccountA, ProductID: "wl-gb-5-v1", IdempotencyKey: "create-key-1"}
	if !reflect.DeepEqual(business.customerTokens, []string{commercialTokenA}) {
		t.Fatalf("customer token lookups = %#v, want [%q]", business.customerTokens, commercialTokenA)
	}
	if !reflect.DeepEqual(business.createCommercialCalls, []CommercialOrderCommand{wantCommand}) {
		t.Fatalf("commercial create calls = %#v, want %#v", business.createCommercialCalls, []CommercialOrderCommand{wantCommand})
	}
	got := decodeCommercialJSON[CommercialOrderView](t, response)
	want := business.orders["wl-gb-5-v1"]
	want.AccountID = ""
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commercial order = %#v, want redacted %#v", got, want)
	}
}

func TestCommercialProductOrderRejectsExpiredPrimaryWithoutLeakingErrorDetails(t *testing.T) {
	business := newCommercialBusinessFake()
	business.customers[commercialTokenA] = CustomerView{
		CustomerID: commercialAccountA, Login: "alice", Active: false,
		Expires: time.Unix(1_600_000_000, 0).UTC(),
	}
	business.createCommercialErr = commercialTestError{
		status:  http.StatusConflict,
		message: "primary access expired; token=" + commercialCredentialLeak + "; node=" + commercialNodeLeak,
	}
	response := commercialRequest(t, NewControlPlane(business, Config{}).Handler(), http.MethodPost, "/order", `{"product_id":"wl-gb-5-v1","sub_token":"account-a-token"}`, "", "expired-key-1")

	if response.Code != http.StatusConflict {
		t.Fatalf("expired-primary order status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	if len(business.createCommercialCalls) != 1 || business.createCommercialCalls[0].AccountID != commercialAccountA {
		t.Fatalf("expired-primary create calls = %#v, want one account-bound call", business.createCommercialCalls)
	}
	if strings.Contains(response.Body.String(), commercialCredentialLeak) || strings.Contains(response.Body.String(), commercialNodeLeak) {
		t.Fatalf("expired-primary error leaked sensitive details: %s", response.Body.String())
	}
	if got := decodeCommercialJSON[map[string]string](t, response)["error"]; got != "request rejected" {
		t.Fatalf("expired-primary error = %q, want %q", got, "request rejected")
	}
}

func TestCommercialPaidClaimReplaysSameAccountBoundCommand(t *testing.T) {
	business := newCommercialBusinessFake()
	business.bindings["topup-order-1"] = CommercialOrderBindingView{
		OrderID: "topup-order-1", Family: "WHITELIST_TOP_UP", AccountID: commercialAccountA,
	}
	handler := NewControlPlane(business, Config{}).Handler()
	first := commercialRequest(t, handler, http.MethodPost, "/order/topup-order-1/paid-claim", `{}`, commercialTokenA, "claim-key-1")
	second := commercialRequest(t, handler, http.MethodPost, "/order/topup-order-1/paid-claim", `{}`, commercialTokenA, "claim-key-1")

	if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Body.String() != second.Body.String() {
		t.Fatalf("duplicate paid claim = (%d, %q), (%d, %q); want identical 200 replay", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	wantCommand := CommercialClaimCommand{AccountID: commercialAccountA, OrderID: "topup-order-1", IdempotencyKey: "claim-key-1"}
	if !reflect.DeepEqual(business.claimCommercialCalls, []CommercialClaimCommand{wantCommand, wantCommand}) {
		t.Fatalf("claim commands = %#v, want two exact replay commands", business.claimCommercialCalls)
	}
	if len(business.claimMutationKeys) != 1 {
		t.Fatalf("durable claim mutation keys = %d, want 1", len(business.claimMutationKeys))
	}
	if !reflect.DeepEqual(business.bindingOrderIDs, []string{"topup-order-1", "topup-order-1"}) {
		t.Fatalf("durable binding lookups = %#v, want one per replay", business.bindingOrderIDs)
	}
}

func TestCommercialBalanceReturnsExactRedactedAccountProjection(t *testing.T) {
	business := newCommercialBusinessFake()
	response := commercialRequest(t, NewControlPlane(business, Config{}).Handler(), http.MethodGet, "/account/whitelist-balance", "", commercialTokenA, "")

	if response.Code != http.StatusOK {
		t.Fatalf("balance status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	got := decodeCommercialJSON[map[string]any](t, response)
	want := map[string]any{
		"included_remaining_bytes":  float64(1_250_000_000),
		"purchased_remaining_bytes": float64(7_500_000_000),
		"available_bytes":           float64(8_750_000_000),
		"period_ends_at_unix":       float64(2_000_000_000),
		"primary_access_state":      "active",
		"publication_verdict":       "PUBLISHABLE",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("balance JSON = %#v, want exact redacted projection %#v", got, want)
	}
	if !reflect.DeepEqual(business.customerTokens, []string{commercialTokenA}) || !reflect.DeepEqual(business.balanceAccountIDs, []string{commercialAccountA}) {
		t.Fatalf("balance binding = tokens %#v accounts %#v, want token A and account A", business.customerTokens, business.balanceAccountIDs)
	}
	if strings.Contains(response.Body.String(), commercialTokenA) || strings.Contains(response.Body.String(), "nodes") || strings.Contains(response.Body.String(), "credentials") {
		t.Fatalf("balance JSON exposed account or publication internals: %s", response.Body.String())
	}
}

func TestCommercialSubscriptionDeliveryIsBoundToAuthenticatedAccount(t *testing.T) {
	business := newCommercialBusinessFake()
	business.delivery = CommercialDeliveryView{
		AccountID: commercialAccountA,
		Client:    "incy",
		Format:    "INCY_ONE_TAP",
		URL:       "https://subscription.example/account-a",
	}
	response := commercialRequest(t, NewControlPlane(business, Config{}).Handler(), http.MethodPost, "/account/subscription-delivery", `{"client":"incy"}`, commercialTokenA, "delivery-key-1")

	if response.Code != http.StatusOK {
		t.Fatalf("delivery status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	wantCommand := CommercialDeliveryCommand{AccountID: commercialAccountA, Client: "incy", IdempotencyKey: "delivery-key-1"}
	if !reflect.DeepEqual(business.deliveryCalls, []CommercialDeliveryCommand{wantCommand}) {
		t.Fatalf("delivery calls = %#v, want %#v", business.deliveryCalls, []CommercialDeliveryCommand{wantCommand})
	}
	got := decodeCommercialJSON[map[string]any](t, response)
	want := map[string]any{
		"client": "incy", "format": "INCY_ONE_TAP", "url": "https://subscription.example/account-a",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delivery JSON = %#v, want %#v", got, want)
	}
}

func TestCommercialSubscriptionDeliveryDeniesCrossAccountResultAndRedactsSecrets(t *testing.T) {
	business := newCommercialBusinessFake()
	business.delivery = CommercialDeliveryView{
		AccountID: commercialAccountB,
		Client:    "happ",
		Format:    "TYPED_DESCRIPTOR",
		URL:       "https://subscription.example/" + commercialCredentialLeak,
	}
	response := commercialRequest(t, NewControlPlane(business, Config{}).Handler(), http.MethodPost, "/account/subscription-delivery", `{"client":"happ"}`, commercialTokenA, "delivery-key-2")

	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-account delivery status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
	if len(business.deliveryCalls) != 1 || business.deliveryCalls[0].AccountID != commercialAccountA {
		t.Fatalf("cross-account delivery calls = %#v, want authenticated account A", business.deliveryCalls)
	}
	if strings.Contains(response.Body.String(), commercialCredentialLeak) || strings.Contains(response.Body.String(), commercialAccountB) {
		t.Fatalf("cross-account delivery leaked protected data: %s", response.Body.String())
	}
}

func TestCommercialProfileBindsOnlyAnAuthenticatedBearerAndReturnsOnlyLogin(t *testing.T) {
	business := newCommercialBusinessFake()
	business.customers[commercialTokenA] = CustomerView{CustomerID: commercialAccountA, Login: "maestro-login"}
	response := commercialRequest(t, NewControlPlane(business, Config{}).Handler(), http.MethodGet, "/account/profile", "", commercialTokenA, "")
	if response.Code != http.StatusOK {
		t.Fatalf("profile status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	got := decodeCommercialJSON[map[string]any](t, response)
	if !reflect.DeepEqual(got, map[string]any{"login": "maestro-login"}) {
		t.Fatalf("profile JSON = %#v", got)
	}
	if strings.Contains(response.Body.String(), commercialTokenA) || strings.Contains(response.Body.String(), commercialAccountA) {
		t.Fatalf("profile leaked bearer or account identity: %s", response.Body.String())
	}
}

func TestCommercialErrorsNeverExposeCredentialNodeOrTokenMaterial(t *testing.T) {
	business := newCommercialBusinessFake()
	business.balanceErr = commercialTestError{
		status:  http.StatusServiceUnavailable,
		message: "token=" + commercialTokenA + " credential=" + commercialCredentialLeak + " node=" + commercialNodeLeak,
	}
	response := commercialRequest(t, NewControlPlane(business, Config{}).Handler(), http.MethodGet, "/account/whitelist-balance", "", commercialTokenA, "")

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("redacted error status = %d, want %d; body=%s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	for _, forbidden := range []string{commercialTokenA, commercialCredentialLeak, commercialNodeLeak} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("commercial error leaked %q in %s", forbidden, response.Body.String())
		}
	}
	if got := decodeCommercialJSON[map[string]string](t, response); !reflect.DeepEqual(got, map[string]string{"error": "unavailable"}) {
		t.Fatalf("commercial error JSON = %#v, want generic unavailable", got)
	}
}
