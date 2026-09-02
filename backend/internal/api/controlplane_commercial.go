package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

const (
	CommercialOrderFamilyAccess         = "ACCESS"
	CommercialOrderFamilyWhiteListTopUp = "WHITELIST_TOP_UP"
	WhiteListPublicationDisabled        = WhiteListPublicationVerdict("DISABLED")
)

// CommercialBusiness is an optional extension of the frozen Business port.
// Keeping it separate preserves existing adapters while exposing Task 8.
type CommercialBusiness interface {
	CustomerByToken(context.Context, string) (CustomerView, error)
	CommercialCatalog(context.Context) (CommercialCatalogView, error)
	CommercialOrderBinding(context.Context, string) (CommercialOrderBindingView, error)
	CreateCommercialOrder(context.Context, CommercialOrderCommand) (CommercialOrderView, error)
	ClaimCommercialPayment(context.Context, CommercialClaimCommand) (CommercialOrderView, error)
	ConfirmCommercialOrder(context.Context, CommercialOrderDecisionCommand) (CommercialOrderView, error)
	RejectCommercialOrder(context.Context, CommercialOrderDecisionCommand) (CommercialOrderView, error)
	WhiteListBalance(context.Context, string) (WhiteListBalanceView, error)
	SetWhiteListPublication(context.Context, CommercialPublicationCommand) (CommercialPublicationView, error)
	SubscriptionDelivery(context.Context, CommercialDeliveryCommand) (CommercialDeliveryView, error)
}

type CommercialCatalogView struct {
	Access   TariffView              `json:"access"`
	Products []CommercialProductView `json:"products"`
}

type CommercialProductView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Bytes       int64  `json:"bytes"`
	Unit        string `json:"unit"`
}

type CommercialOrderBindingView struct {
	OrderID   string
	Family    string
	AccountID string
}

type CommercialOrderCommand struct {
	AccountID      string
	ProductID      string
	IdempotencyKey string
}

type CommercialClaimCommand struct {
	AccountID      string
	OrderID        string
	IdempotencyKey string
}

type CommercialOrderDecisionCommand struct {
	OrderID        string
	Actor          string
	IdempotencyKey string
}

type CommercialOrderView struct {
	AccountID     string `json:"-"`
	OrderID       string `json:"order_id"`
	PaymentCode   string `json:"payment_code,omitempty"`
	ProductID     string `json:"product_id"`
	AmountMinor   int64  `json:"amount_minor"`
	Currency      string `json:"currency"`
	Bytes         int64  `json:"bytes"`
	PaymentState  string `json:"payment_state"`
	ExpiresAtUnix int64  `json:"expires_at_unix,omitempty"`
}

type WhiteListBalanceView struct {
	AccountID               string `json:"-"`
	IncludedRemainingBytes  int64  `json:"included_remaining_bytes"`
	PurchasedRemainingBytes int64  `json:"purchased_remaining_bytes"`
	AvailableBytes          int64  `json:"available_bytes"`
	PeriodEndsAtUnix        int64  `json:"period_ends_at_unix"`
	PrimaryAccessState      string `json:"primary_access_state"`
	PublicationVerdict      string `json:"publication_verdict"`
}

type CommercialPublicationCommand struct {
	AccountID      string
	Enabled        bool
	Actor          string
	IdempotencyKey string
}

type CommercialPublicationView struct {
	AccountID   string `json:"-"`
	Enabled     bool   `json:"enabled"`
	Version     int64  `json:"version"`
	OperationID string `json:"operation_id"`
	AuditID     string `json:"audit_id"`
}

type CommercialDeliveryCommand struct {
	AccountID      string
	Client         string
	IdempotencyKey string
}

type CommercialDeliveryView struct {
	AccountID string `json:"-"`
	Client    string `json:"client"`
	Format    string `json:"format"`
	URL       string `json:"url"`
}

func (s *ControlPlaneServer) handleControlPlaneCommercialCatalog(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	if s.commercial == nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	view, err := s.commercial.CommercialCatalog(r.Context())
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneCreateCommercialOrder(
	w http.ResponseWriter,
	r *http.Request,
	productID string,
	subToken string,
) {
	if s.commercial == nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	customer, err := s.commercial.CustomerByToken(r.Context(), strings.TrimSpace(subToken))
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	idempotencyKey, ok := s.controlPlanePublicIdempotencyKey(w, r, "/order", customer.CustomerID, productID)
	if !ok {
		return
	}
	view, err := s.commercial.CreateCommercialOrder(r.Context(), CommercialOrderCommand{
		AccountID: customer.CustomerID, ProductID: strings.TrimSpace(productID), IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	if view.AccountID != "" && view.AccountID != customer.CustomerID {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	view.AccountID = ""
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneCommercialPaidClaim(w http.ResponseWriter, r *http.Request, orderID string) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	customer, ok := s.controlPlaneCommercialCustomer(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !decodeControlPlanePublicMutation(w, r, &request) {
		return
	}
	idempotencyKey, ok := s.controlPlanePublicIdempotencyKey(w, r, "/order/<id>/paid-claim", orderID)
	if !ok {
		return
	}
	binding, err := s.commercial.CommercialOrderBinding(r.Context(), orderID)
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	if binding.OrderID != orderID || binding.AccountID != customer.CustomerID {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	switch binding.Family {
	case CommercialOrderFamilyWhiteListTopUp:
		view, claimErr := s.commercial.ClaimCommercialPayment(r.Context(), CommercialClaimCommand{
			AccountID: customer.CustomerID, OrderID: orderID, IdempotencyKey: idempotencyKey,
		})
		if claimErr != nil {
			writeControlPlaneCommercialError(w, claimErr)
			return
		}
		view.AccountID = ""
		writeControlPlaneJSON(w, http.StatusOK, view)
	case CommercialOrderFamilyAccess:
		view, claimErr := s.business.MarkPaymentClaimed(r.Context(), ClaimPaymentCommand{OrderID: orderID, IdempotencyKey: idempotencyKey})
		if claimErr != nil {
			writeControlPlaneCommercialError(w, claimErr)
			return
		}
		writeControlPlaneJSON(w, http.StatusOK, view)
	default:
		writeControlPlaneJSON(w, http.StatusConflict, map[string]string{"error": "request rejected"})
	}
}

func (s *ControlPlaneServer) handleControlPlaneCommercialBalance(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	customer, ok := s.controlPlaneCommercialCustomer(w, r)
	if !ok {
		return
	}
	view, err := s.commercial.WhiteListBalance(r.Context(), customer.CustomerID)
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	if view.AccountID != "" && view.AccountID != customer.CustomerID {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	view.AccountID = ""
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneCommercialDelivery(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	customer, ok := s.controlPlaneCommercialCustomer(w, r)
	if !ok {
		return
	}
	var request struct {
		Client string `json:"client"`
	}
	if !decodeControlPlanePublicMutation(w, r, &request) {
		return
	}
	client := strings.ToLower(strings.TrimSpace(request.Client))
	if client != "incy" && client != "happ" {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	idempotencyKey, ok := s.controlPlanePublicIdempotencyKey(w, r, "/account/subscription-delivery", customer.CustomerID, client)
	if !ok {
		return
	}
	view, err := s.commercial.SubscriptionDelivery(r.Context(), CommercialDeliveryCommand{
		AccountID: customer.CustomerID, Client: client, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	if view.AccountID != "" && view.AccountID != customer.CustomerID {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	view.AccountID = ""
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneCommercialAdminOrder(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/order/"), "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "confirm" && parts[1] != "reject") {
		s.controlPlaneNotFound(w, r)
		return
	}
	var request struct{}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	if s.commercial == nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	binding, err := s.commercial.CommercialOrderBinding(r.Context(), parts[0])
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	command := CommercialOrderDecisionCommand{
		OrderID: parts[0], Actor: "admin", IdempotencyKey: r.Header.Get("Idempotency-Key"),
	}
	if binding.Family == CommercialOrderFamilyWhiteListTopUp {
		var view CommercialOrderView
		if parts[1] == "confirm" {
			view, err = s.commercial.ConfirmCommercialOrder(r.Context(), command)
		} else {
			view, err = s.commercial.RejectCommercialOrder(r.Context(), command)
		}
		if err != nil {
			writeControlPlaneCommercialError(w, err)
			return
		}
		view.AccountID = ""
		writeControlPlaneJSON(w, http.StatusOK, view)
		return
	}
	if binding.Family != CommercialOrderFamilyAccess {
		writeControlPlaneJSON(w, http.StatusConflict, map[string]string{"error": "request rejected"})
		return
	}
	if parts[1] == "confirm" {
		view, confirmErr := s.business.ConfirmPayment(r.Context(), ConfirmPaymentCommand{
			OrderID: parts[0], Actor: "admin", IdempotencyKey: command.IdempotencyKey,
		})
		if confirmErr != nil {
			writeControlPlaneCommercialError(w, confirmErr)
			return
		}
		writeControlPlaneJSON(w, http.StatusOK, view)
		return
	}
	view, rejectErr := s.business.CancelOrder(r.Context(), CancelOrderCommand{
		OrderID: parts[0], Actor: "admin", IdempotencyKey: command.IdempotencyKey,
	})
	if rejectErr != nil {
		writeControlPlaneCommercialError(w, rejectErr)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneCommercialPublication(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/accounts/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "whitelist-publication" {
		s.controlPlaneNotFound(w, r)
		return
	}
	var request struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	if s.commercial == nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	view, err := s.commercial.SetWhiteListPublication(r.Context(), CommercialPublicationCommand{
		AccountID: parts[0], Enabled: request.Enabled, Actor: "admin", IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	view.AccountID = ""
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) controlPlaneCommercialCustomer(w http.ResponseWriter, r *http.Request) (CustomerView, bool) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) || strings.TrimSpace(strings.TrimPrefix(header, prefix)) == "" {
		writeControlPlaneJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return CustomerView{}, false
	}
	if s.commercial == nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return CustomerView{}, false
	}
	view, err := s.commercial.CustomerByToken(r.Context(), strings.TrimSpace(strings.TrimPrefix(header, prefix)))
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return CustomerView{}, false
	}
	return view, true
}

func writeControlPlaneCommercialError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	type statusError interface{ HTTPStatus() int }
	var typed statusError
	if errors.As(err, &typed) {
		status = typed.HTTPStatus()
	}
	message := "request rejected"
	switch status {
	case http.StatusBadRequest:
		message = "invalid request"
	case http.StatusUnauthorized:
		message = "unauthorized"
	case http.StatusForbidden:
		message = "forbidden"
	case http.StatusNotFound:
		message = "not found"
	case http.StatusServiceUnavailable:
		message = "unavailable"
	}
	writeControlPlaneJSON(w, status, map[string]string{"error": message})
}
