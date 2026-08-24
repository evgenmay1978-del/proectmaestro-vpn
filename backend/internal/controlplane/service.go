package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const sessionTTL = 30 * time.Minute

// IDSource supplies unguessable IDs without global mutable state.
type IDSource interface {
	NewID(prefix string) (string, error)
}

type Service struct {
	store *Store
	ids   IDSource
	clock Clock
}

func NewService(store *Store, ids IDSource, clock Clock) (*Service, error) {
	if store == nil || ids == nil || clock == nil {
		return nil, errors.New("controlplane: incomplete service configuration")
	}
	return &Service{store: store, ids: ids, clock: clock}, nil
}

func (s *Service) CustomerByToken(ctx context.Context, rawToken string) (Customer, error) {
	if rawToken == "" {
		return Customer{}, ErrNotFound
	}
	lookup := s.store.secrets.LookupHMAC("subscription-token", []byte(rawToken))
	return s.store.customerByLookup(ctx, "st.token_hmac", lookup)
}

func (s *Service) CustomerByLogin(ctx context.Context, login string) (Customer, error) {
	canonical, err := CanonicalLoginKey(login)
	if err != nil {
		return Customer{}, ErrNotFound
	}
	lookup := s.store.secrets.LookupHMAC("customer-login", []byte(canonical))
	return s.store.customerByLookup(ctx, "c.login_key_hmac", lookup)
}

func (s *Service) Tariffs(ctx context.Context) ([]Tariff, error) {
	tariffs, err := s.store.tariffs(ctx)
	if err != nil {
		return nil, err
	}
	return append([]Tariff(nil), tariffs...), nil
}

func (s *Service) UpdateSetting(ctx context.Context, update SettingUpdate) (SettingResult, error) {
	if !validSettingUpdate(update) {
		return SettingResult{}, errors.New("controlplane: invalid setting update")
	}
	mutationToken, err := s.ids.NewID("setting-mut")
	if err != nil {
		return SettingResult{}, errors.New("controlplane: generate setting mutation token")
	}
	return s.store.updateSetting(ctx, update, mutationToken)
}

func (s *Service) ApprovedOTA(ctx context.Context) (OTAApproval, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL:  `SELECT public_value_json, generation FROM cluster_settings WHERE setting_key = 'ota'`,
	})
	if err != nil {
		return OTAApproval{}, errors.New("controlplane: approved OTA unavailable")
	}
	row, ok := firstRow(results)
	if !ok {
		return OTAApproval{}, ErrNotFound
	}
	raw, ok := rowString(row, "public_value_json")
	if !ok {
		return OTAApproval{}, errors.New("controlplane: invalid OTA approval")
	}
	var approval OTAApproval
	if err := json.Unmarshal([]byte(raw), &approval); err != nil {
		return OTAApproval{}, errors.New("controlplane: invalid OTA approval")
	}
	approval.Generation, ok = rowInt64(row, "generation")
	if !ok || approval.VersionCode <= 0 || approval.VersionName == "" || approval.APKSize <= 0 ||
		len(approval.SHA256) != 64 || approval.SourceReleaseID == "" {
		return OTAApproval{}, errors.New("controlplane: incomplete OTA approval")
	}
	return approval, nil
}

func (s *Service) CreateSession(ctx context.Context, principalID string) (SessionResult, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT principal_id, status, revocation_epoch FROM principals
WHERE principal_id = ? AND status = 'active' LIMIT 1`,
		Args: []any{principalID},
	})
	if err != nil {
		return SessionResult{}, errors.New("controlplane: principal lookup unavailable")
	}
	row, ok := firstRow(results)
	if !ok {
		return SessionResult{}, ErrForbidden
	}
	status, _ := rowString(row, "status")
	epoch, epochOK := rowInt64(row, "revocation_epoch")
	if status != "active" || !epochOK {
		return SessionResult{}, ErrForbidden
	}
	rawSession, err := s.ids.NewID("session")
	if err != nil {
		return SessionResult{}, errors.New("controlplane: generate session")
	}
	rawCSRF, err := s.ids.NewID("csrf")
	if err != nil {
		return SessionResult{}, errors.New("controlplane: generate CSRF token")
	}
	now := s.clock.Now()
	expires := now.Add(sessionTTL)
	sessionHMAC := s.store.secrets.LookupHMAC("web-session", []byte(rawSession))
	csrfHMAC := s.store.secrets.LookupHMAC("web-csrf", []byte(rawCSRF))
	_, err = s.store.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL: `INSERT INTO web_sessions(session_hmac, csrf_hmac, principal_id, revocation_epoch,
created_at_unix, expires_at_unix, revoked_at_unix) VALUES (?, ?, ?, ?, ?, ?, NULL)`,
		Args: []any{sessionHMAC, csrfHMAC, principalID, epoch, now.Unix(), expires.Unix()},
	})
	if err != nil {
		return SessionResult{}, errors.New("controlplane: create session unavailable")
	}
	return SessionResult{
		Cookie: http.Cookie{
			Name: "maestro_session", Value: rawSession, Path: "/", MaxAge: int(sessionTTL.Seconds()),
			Expires: expires, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
		},
		CSRFToken: rawCSRF,
		ExpiresAt: expires,
	}, nil
}

func (s *Service) Authorize(ctx context.Context, rawSession, rawCSRF string, permission Permission) (Authorization, error) {
	if rawSession == "" || rawCSRF == "" {
		return Authorization{}, ErrForbidden
	}
	sessionHMAC := s.store.secrets.LookupHMAC("web-session", []byte(rawSession))
	csrfHMAC := s.store.secrets.LookupHMAC("web-csrf", []byte(rawCSRF))
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT p.principal_id, pr.role_name FROM web_sessions ws
JOIN principals p ON p.principal_id = ws.principal_id
JOIN principal_roles pr ON pr.principal_id = p.principal_id
WHERE ws.session_hmac = ? AND ws.csrf_hmac = ? AND ws.revoked_at_unix IS NULL
AND ws.expires_at_unix > ? AND ws.revocation_epoch = p.revocation_epoch AND p.status = 'active'
ORDER BY pr.role_name`,
		Args: []any{sessionHMAC, csrfHMAC, s.clock.Now().Unix()},
	})
	if err != nil {
		return Authorization{}, ErrForbidden
	}
	if len(results) != 1 {
		return Authorization{}, ErrForbidden
	}
	for _, row := range results[0].Rows {
		principalID, okID := rowString(row, "principal_id")
		role, okRole := rowString(row, "role_name")
		role = strings.ToLower(strings.TrimSpace(role))
		if okID && okRole && roleAllows(role, permission) {
			return Authorization{PrincipalID: principalID, Role: role}, nil
		}
	}
	return Authorization{}, ErrForbidden
}

func (s *Service) RevokeSessions(ctx context.Context, principalID, actor string) error {
	now := s.clock.Now().Unix()
	actorHMAC := s.store.secrets.LookupHMAC("audit-actor", []byte(actor))
	resourceHMAC := s.store.secrets.LookupHMAC("audit-resource", []byte(principalID))
	statements := []rqlite.Statement{{
		SQL:  `UPDATE principals SET revocation_epoch = revocation_epoch + 1 WHERE principal_id = ? RETURNING revocation_epoch`,
		Args: []any{principalID},
	}, backupRPODirtyGenerationStatement(now), {
		SQL: `UPDATE web_sessions SET revoked_at_unix = ?
WHERE principal_id = ? AND revoked_at_unix IS NULL
	AND EXISTS (SELECT 1 FROM principals WHERE principal_id = ? AND revocation_epoch > 0)`,
		Args: []any{now, principalID, principalID},
	}, {
		SQL: `INSERT INTO audit_events(event_id, actor_hmac, action, resource_type, resource_id_hmac, created_at_unix)
SELECT ?, ?, 'session.revoke_all', 'principal', ?, ?
WHERE EXISTS (SELECT 1 FROM principals WHERE principal_id = ? AND revocation_epoch > 0)
ON CONFLICT(event_id) DO NOTHING`,
		Args: []any{auditID("revoke", principalID, 0, now), actorHMAC, resourceHMAC, now, principalID},
	}}
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil {
		return errors.New("controlplane: revoke sessions unavailable")
	}
	if len(results) != len(statements) {
		return errors.New("controlplane: invalid revoke sessions result")
	}
	row, ok := firstRow(results[:1])
	if !ok {
		return ErrNotFound
	}
	epoch, ok := rowInt64(row, "revocation_epoch")
	if !ok || epoch <= 0 {
		return errors.New("controlplane: invalid revoke sessions result")
	}
	return nil
}

func roleAllows(role string, permission Permission) bool {
	switch role {
	case "owner":
		return permission == PermissionCustomerRead || permission == PermissionProvision ||
			permission == PermissionPaymentDecide || permission == PermissionCriticalSettings
	case "admin":
		return permission == PermissionCustomerRead || permission == PermissionProvision
	default:
		return false
	}
}
