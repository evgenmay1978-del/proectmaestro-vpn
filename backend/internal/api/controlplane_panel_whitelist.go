package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

// Optional capability leaves the legacy Business contract unchanged.
type panelWhiteListBusiness interface {
	PanelWhiteListBalance(context.Context, string) (panelWhiteListView, error)
	PanelWhiteListAdmin(context.Context, panelWhiteListCommand) (panelWhiteListView, error)
}

type panelWhiteListCommand struct {
	Login          string `json:"login"`
	Action         string `json:"action"`
	GB             int64  `json:"gb"`
	Actor          string `json:"-"`
	IdempotencyKey string `json:"-"`
}

type panelWhiteListView struct {
	PublicationVerdict string `json:"publication_verdict"`
	Enabled            bool   `json:"enabled"`
	PrimaryActive      bool   `json:"primary_active"`
	RemainingBytes     int64  `json:"remaining_bytes,string"`
	IncludedBytes      int64  `json:"included_bytes,string"`
	PurchasedBytes     int64  `json:"purchased_bytes,string"`
	PeriodEndsAtUnix   int64  `json:"period_ends_at_unix"`
}

func (b *ServiceBusiness) PanelWhiteListBalance(ctx context.Context, login string) (panelWhiteListView, error) {
	view := panelWhiteListView{PublicationVerdict: string(WhiteListNoEntitlement)}
	customer, err := b.CustomerByLogin(ctx, login)
	if err != nil {
		return view, err
	}
	view.PrimaryActive = customer.Active
	entitlement, err := b.service.WhiteListEntitlementByAccountID(ctx, customer.CustomerID)
	if errors.Is(err, controlplane.ErrNotFound) {
		return view, nil
	}
	if err != nil {
		return view, businessError(err)
	}
	snapshot, err := b.service.WhiteListBalanceSnapshot(ctx, b.requestNow().Unix(), entitlement.EntitlementID())
	if err != nil {
		return view, businessError(err)
	}
	publication, err := b.service.WhiteListPublicationState(ctx, entitlement.EntitlementID())
	if err != nil {
		return view, businessError(err)
	}
	view.Enabled = publication.Enabled
	view.PublicationVerdict = string(b.whiteListBalanceVerdict(ctx, customer.CustomerID, publication.Enabled, snapshot))
	view.PrimaryActive = snapshot.PrimaryActive
	view.RemainingBytes = snapshot.AvailableBytes
	view.IncludedBytes = snapshot.Projection.IncludedRemainingBytes
	view.PurchasedBytes = snapshot.Projection.PurchasedRemainingBytes
	view.PeriodEndsAtUnix = snapshot.PeriodEndsUnix
	return view, nil
}

func (b *ServiceBusiness) PanelWhiteListAdmin(ctx context.Context, command panelWhiteListCommand) (panelWhiteListView, error) {
	var empty panelWhiteListView
	if !validPanelWhiteListCommand(command) {
		return empty, businessError(controlplane.ErrConflict)
	}
	customer, err := b.CustomerByLogin(ctx, command.Login)
	if err != nil {
		return empty, err
	}
	entitlement, err := b.service.EnsureWhiteListEntitlement(ctx, customer.CustomerID)
	if err != nil {
		return empty, businessError(err)
	}
	switch command.Action {
	case "credit":
		_, err = b.service.CreditWhiteListManualGB(ctx, controlplane.CreditWhiteListManualGBCommand{EntitlementID: entitlement.EntitlementID(), GB: command.GB, IdempotencyKey: command.IdempotencyKey, Actor: command.Actor})
	case "enable", "disable":
		_, err = b.service.SetWhiteListPublication(ctx, controlplane.SetWhiteListPublicationCommand{EntitlementID: entitlement.EntitlementID(), Enabled: command.Action == "enable", IdempotencyKey: command.IdempotencyKey, Actor: command.Actor, Channel: "panel-admin", SourceEventID: command.IdempotencyKey})
	}
	if err != nil {
		return empty, businessError(err)
	}
	view, readErr := b.PanelWhiteListBalance(ctx, command.Login)
	if readErr != nil {
		// The mutation already succeeded. A subsequent read failure cannot be
		// reported as a definitive rejection that permits a new credit key.
		return empty, businessError(controlplane.ErrUnavailable)
	}
	return view, nil
}

func validPanelWhiteListCommand(command panelWhiteListCommand) bool {
	if command.Login == "" || command.IdempotencyKey == "" {
		return false
	}
	switch command.Action {
	case "enable", "disable":
		return command.GB == 0
	case "credit":
		return command.GB >= 1 && command.GB <= 9223372036
	default:
		return false
	}
}

func (s *ControlPlaneServer) handleControlPlanePanelWhiteList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeControlPlaneJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	permission := "customer.read"
	if r.Method == http.MethodPost {
		permission = "settings.critical"
	}
	principal, ok := s.controlPlanePanelGuardPermission(w, r, permission, r.Method == http.MethodPost)
	if !ok {
		return
	}
	business, ok := s.business.(panelWhiteListBusiness)
	if !ok {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	var view panelWhiteListView
	var err error
	if r.Method == http.MethodGet {
		view, err = business.PanelWhiteListBalance(r.Context(), r.URL.Query().Get("login"))
	} else {
		var command panelWhiteListCommand
		if !decodeControlPlaneBody(w, r, &command) {
			return
		}
		command.Actor = principal.ID
		command.IdempotencyKey = r.Header.Get("Idempotency-Key")
		if !validPanelWhiteListCommand(command) {
			writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid CDN action or whole GB amount"})
			return
		}
		view, err = business.PanelWhiteListAdmin(r.Context(), command)
	}
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}
