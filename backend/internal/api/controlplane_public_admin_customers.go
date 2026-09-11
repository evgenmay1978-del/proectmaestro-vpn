package api

import (
	"net/http"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

// handleControlPlaneCustomers exposes the existing lightweight customer page to
// Bearer-authenticated bot administrators. Detailed usage, balance, and history
// remain separate lookups so listing a page does not fan out per customer.
func (s *ControlPlaneServer) handleControlPlaneCustomers(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	limit, cursor, valid := s.controlPlanePanelPage(r, "customers")
	if !valid {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pagination"})
		return
	}
	customers, err := s.business.ListCustomers(r.Context(), CustomerFilter{
		Limit: limit + 1, AfterLogin: cursor.Key, AfterCustomerID: cursor.ID,
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	nextCursor := ""
	if len(customers) > limit {
		customers = customers[:limit]
		last := customers[len(customers)-1]
		if last.CustomerID == "" {
			writeControlPlaneBusinessError(w, businessError(controlplane.ErrUnavailable))
			return
		}
		encodedCursor, encoded := s.encodeControlPlanePanelCursor(controlPlanePanelCursor{
			Kind: "customers", Key: last.Login, ID: last.CustomerID,
		})
		if !encoded {
			writeControlPlaneBusinessError(w, businessError(controlplane.ErrUnavailable))
			return
		}
		nextCursor = encodedCursor
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{
		"customers": toPanelCustomers(customers), "next_cursor": nextCursor,
	})
}

// handleControlPlaneCustomerWhiteListBalance exposes the existing single-customer
// CDN balance lookup without adding balance work to paginated customer rows.
func (s *ControlPlaneServer) handleControlPlaneCustomerWhiteListBalance(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	business, ok := s.business.(panelWhiteListBusiness)
	if !ok {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	view, err := business.PanelWhiteListBalance(r.Context(), strings.TrimSpace(r.URL.Query().Get("login")))
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

// handleControlPlaneCustomerWhiteListCredit is a Bearer-authenticated adapter
// over the existing audited, idempotent administrative credit command.
func (s *ControlPlaneServer) handleControlPlaneCustomerWhiteListCredit(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Login string `json:"login"`
		GB    int64  `json:"gb"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	business, ok := s.business.(panelWhiteListBusiness)
	if !ok {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	command := panelWhiteListCommand{
		Login:          strings.TrimSpace(request.Login),
		Action:         "credit",
		GB:             request.GB,
		Actor:          "admin",
		IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")),
	}
	if !validPanelWhiteListCommand(command) {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid login or whole GB amount"})
		return
	}
	view, err := business.PanelWhiteListAdmin(r.Context(), command)
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}
