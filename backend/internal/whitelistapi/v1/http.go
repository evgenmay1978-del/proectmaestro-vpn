package v1

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
)

var ErrNotFound = errors.New("whitelistapi/v1: not found")

// Reader is deliberately read-only. Adapters cannot post balance changes,
// mutate subscriptions, control Xray, or suspend ordinary VPN access through
// this interface.
type Reader interface {
	Entitlement(context.Context, string) (Entitlement, error)
	Health(context.Context, string) (Health, error)
	Usage(context.Context, string) (Usage, error)
	Ledger(context.Context, string, PageRequest) (Page[LedgerEntry], error)
	Audit(context.Context, string, PageRequest) (Page[AuditRecord], error)
}

type Config struct {
	BearerToken string
	Reader      Reader
}

type handler struct {
	reader       Reader
	wantAuthHash [sha256.Size]byte
}

// NewHandler constructs a private read model. It does not register the handler
// on Maestro's public mux; callers must mount it on a loopback-only listener.
func NewHandler(config Config) (http.Handler, error) {
	if config.Reader == nil || !validBearerToken(config.BearerToken) {
		return nil, errors.New("whitelistapi/v1: incomplete private handler config")
	}
	return &handler{
		reader:       config.Reader,
		wantAuthHash: sha256.Sum256([]byte("Bearer " + config.BearerToken)),
	}, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	accountID, resource, ok := parseRoute(r)
	if !ok || !isLoopbackPeer(r.RemoteAddr) {
		http.NotFound(w, r)
		return
	}
	if !h.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	var data any
	var err error
	switch resource {
	case "entitlement":
		if len(r.URL.Query()) != 0 {
			writeError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		var value Entitlement
		value, err = h.reader.Entitlement(r.Context(), accountID)
		if err == nil {
			err = value.validateForAccount(accountID)
		}
		data = value
	case "health":
		if len(r.URL.Query()) != 0 {
			writeError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		var value Health
		value, err = h.reader.Health(r.Context(), accountID)
		if err == nil {
			err = value.validateForAccount(accountID)
		}
		data = value
	case "usage":
		if len(r.URL.Query()) != 0 {
			writeError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		var value Usage
		value, err = h.reader.Usage(r.Context(), accountID)
		if err == nil {
			err = value.validateForAccount(accountID)
		}
		data = value
	case "ledger":
		page, pageErr := parsePageRequest(r)
		if pageErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		var value Page[LedgerEntry]
		value, err = h.reader.Ledger(r.Context(), accountID, page)
		if err == nil {
			err = validateLedgerPage(value, accountID)
		}
		data = value
	case "audit":
		page, pageErr := parsePageRequest(r)
		if pageErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		var value Page[AuditRecord]
		value, err = h.reader.Audit(r.Context(), accountID, page)
		if err == nil {
			err = validateAuditPage(value, accountID)
		}
		data = value
	}

	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if errors.Is(err, errInvalidContract) {
		writeError(w, http.StatusBadGateway, "invalid_upstream")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeData(w, data)
}

func (h *handler) authorized(r *http.Request) bool {
	got := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	return subtle.ConstantTimeCompare(got[:], h.wantAuthHash[:]) == 1
}

func parseRoute(r *http.Request) (string, string, bool) {
	escaped := r.URL.EscapedPath()
	prefix := BasePath + "/accounts/"
	if strings.Contains(escaped, "%") || !strings.HasPrefix(escaped, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(escaped, prefix), "/")
	if len(parts) != 2 || !validOpaqueID(parts[0]) {
		return "", "", false
	}
	switch parts[1] {
	case "entitlement", "health", "usage", "ledger", "audit":
		return parts[0], parts[1], true
	default:
		return "", "", false
	}
}

func parsePageRequest(r *http.Request) (PageRequest, error) {
	query := r.URL.Query()
	for key := range query {
		if key != "limit" && key != "cursor" {
			return PageRequest{}, errors.New("unsupported query")
		}
	}
	page := PageRequest{Limit: DefaultPageSize}
	if values, ok := query["limit"]; ok {
		if len(values) != 1 {
			return PageRequest{}, errors.New("duplicate limit")
		}
		limit, err := strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > MaxPageSize {
			return PageRequest{}, errors.New("invalid limit")
		}
		page.Limit = limit
	}
	if values, ok := query["cursor"]; ok {
		if len(values) != 1 || !validOptionalCursor(values[0]) {
			return PageRequest{}, errors.New("invalid cursor")
		}
		page.Cursor = values[0]
	}
	return page, nil
}

func isLoopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func validBearerToken(value string) bool {
	if len(value) < 32 || len(value) > 512 || value != strings.TrimSpace(value) {
		return false
	}
	for _, char := range value {
		if char <= 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func writeData(w http.ResponseWriter, data any) {
	payload, err := json.Marshal(struct {
		APIVersion string `json:"api_version"`
		Data       any    `json:"data"`
	}{APIVersion: Version, Data: data})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encoding_failed")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func writeError(w http.ResponseWriter, status int, code string) {
	type errorBody struct {
		Code string `json:"code"`
	}
	payload, _ := json.Marshal(struct {
		APIVersion string    `json:"api_version"`
		Error      errorBody `json:"error"`
	}{APIVersion: Version, Error: errorBody{Code: code}})
	writeJSON(w, status, payload)
}

func writeJSON(w http.ResponseWriter, status int, payload []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
