package api

import (
	"net/http"
	"strings"
)

type controlPlanePublicIdempotencyKeyDeriver interface {
	LegacyPublicIdempotencyKey(route string, values ...string) (string, error)
}

func (b *ServiceBusiness) LegacyPublicIdempotencyKey(route string, values ...string) (string, error) {
	if err := b.available(); err != nil {
		return "", err
	}
	key, err := b.service.LegacyPublicIdempotencyKey(route, values...)
	if err != nil {
		return "", businessError(err)
	}
	return key, nil
}

func (s *ControlPlaneServer) controlPlanePublicIdempotencyKey(
	w http.ResponseWriter, r *http.Request, route string, values ...string,
) (string, bool) {
	if key := r.Header.Get("Idempotency-Key"); strings.TrimSpace(key) != "" {
		return key, true
	}
	deriver, ok := s.business.(controlPlanePublicIdempotencyKeyDeriver)
	if !ok {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return "", false
	}
	key, err := deriver.LegacyPublicIdempotencyKey(route, values...)
	if err != nil || strings.TrimSpace(key) == "" {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return "", false
	}
	return key, true
}
