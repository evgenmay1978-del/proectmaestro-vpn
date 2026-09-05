package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// WhiteListNativeBusiness is optional; the frozen Business and ordinary
// subscription adapter do not acquire a new requirement.
type WhiteListNativeBusiness interface {
	WhiteListNativeRuntime(context.Context, string) (WhiteListNativeRuntimeView, error)
}

type WhiteListNativeRuntimeView struct {
	SchemaVersion     int                             `json:"schema_version"`
	IssuedAtUnix      int64                           `json:"issued_at_unix"`
	FreshUntilUnix    int64                           `json:"fresh_until_unix"`
	ProjectionVersion int64                           `json:"projection_version"`
	DesiredGeneration int64                           `json:"desired_generation"`
	Profiles          []subgen.WhiteListNativeProfile `json:"profiles"`
}

func (b *ServiceBusiness) WhiteListNativeRuntime(ctx context.Context, token string) (WhiteListNativeRuntimeView, error) {
	if _, err := b.CustomerByToken(ctx, token); err != nil {
		return WhiteListNativeRuntimeView{}, err
	}
	if b.cfg.WhiteListPublicationSource == nil || ctx == nil || ctx.Err() != nil {
		return WhiteListNativeRuntimeView{}, businessError(controlplane.ErrUnavailable)
	}
	// This is deliberately independent of both the ordinary subscription cache
	// and the commercial balance verdict. The actual source checks all Origins.
	timed, cancel := context.WithTimeout(ctx, b.cfg.WhiteListPublicationTimeout)
	defer cancel()
	publication, err := b.cfg.WhiteListPublicationSource.WhiteListPublication(timed, token, b.requestNow())
	if err != nil || timed.Err() != nil {
		return WhiteListNativeRuntimeView{}, businessError(controlplane.ErrUnavailable)
	}
	return nativeWhiteListRuntimeView(publication, b.requestNow())
}

func nativeWhiteListRuntimeView(publication WhiteListPublicationSnapshot, now time.Time) (WhiteListNativeRuntimeView, error) {
	closed := WhiteListNativeRuntimeView{}
	switch publication.Verdict {
	case WhiteListPublishable:
	case WhiteListNoEntitlement, WhiteListNoBalance, WhiteListPrimaryExpired, WhiteListPublicationDisabled:
		return closed, businessError(controlplane.ErrForbidden)
	default:
		return closed, businessError(controlplane.ErrUnavailable)
	}
	// Integer-second leases must round conservatively: rounding the issued time
	// down could let a fast client extend the true deadline by almost a second.
	issued := now.Unix()
	if now.Nanosecond() != 0 {
		issued++
	}
	fresh := publication.FreshThrough.Unix()
	if now.Unix() <= 0 || publication.ProjectionVersion <= 0 || publication.DesiredGeneration <= 0 ||
		publication.FreshThrough.IsZero() || !publication.FreshThrough.After(now) ||
		publication.FreshThrough.After(now.Add(5*time.Second)) || fresh <= issued || fresh-issued > 5 {
		return closed, businessError(controlplane.ErrUnavailable)
	}
	profiles, err := subgen.NativeWhiteListProfiles(publication.Nodes)
	if err != nil {
		return closed, businessError(controlplane.ErrUnavailable)
	}
	view := WhiteListNativeRuntimeView{SchemaVersion: 1, IssuedAtUnix: issued, FreshUntilUnix: fresh,
		ProjectionVersion: publication.ProjectionVersion, DesiredGeneration: publication.DesiredGeneration, Profiles: profiles}
	encoded, err := json.Marshal(view)
	if err != nil || len(encoded)+1 > 64<<10 {
		return closed, businessError(controlplane.ErrUnavailable)
	}
	return view, nil
}

func (s *ControlPlaneServer) handleControlPlaneWhiteListNativeRuntime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(header, prefix) ||
		strings.TrimSpace(strings.TrimPrefix(header, prefix)) == "" {
		writeControlPlaneJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.URL.RawQuery != "" {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	native, ok := s.business.(WhiteListNativeBusiness)
	if !ok {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	view, err := native.WhiteListNativeRuntime(r.Context(), strings.TrimSpace(strings.TrimPrefix(header, prefix)))
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}
