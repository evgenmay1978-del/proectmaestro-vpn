package controlplane_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestServiceBusinessPanelSessionStatusSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	owner := seedF9PanelPrincipal(t, fixture, "owner-1", "owner")
	handler := f9PanelHandler(fixture)

	t.Run("invalid session is unauthorized", func(t *testing.T) {
		invalid := owner
		invalid.Cookie.Value = "invalid-session"
		response := f9PanelGET(t, handler, "/mp/api/customers", invalid)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	t.Run("bad csrf is forbidden", func(t *testing.T) {
		badCSRF := owner
		badCSRF.CSRFToken = "bad-csrf"
		response := f9PanelGET(t, handler, "/mp/api/customers", badCSRF)
		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	t.Run("revoked session is unauthorized", func(t *testing.T) {
		if err := fixture.service.RevokeSessions(context.Background(), "owner-1", "owner-1"); err != nil {
			t.Fatalf("revoke sessions: %v", err)
		}
		response := f9PanelGET(t, handler, "/mp/api/customers", owner)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})
}

func TestAuthenticatePasswordChecksOwnerAndAdminCredentialsSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	seedF9PanelPrincipal(t, fixture, "owner-1", "owner")
	seedF9PanelPrincipal(t, fixture, "admin-1", "admin")
	insertF9PasswordCredential(t, fixture, "owner-1", "owner-password")
	insertF9PasswordCredential(t, fixture, "admin-1", "admin-password")

	session, err := fixture.service.AuthenticatePassword(context.Background(), "admin-password")
	if err != nil {
		t.Fatalf("authenticate delegated admin: %v", err)
	}
	authorization, err := fixture.service.Authorize(
		context.Background(), session.Cookie.Value, session.CSRFToken, controlplane.PermissionCustomerRead,
	)
	if err != nil {
		t.Fatalf("authorize delegated admin: %v", err)
	}
	if authorization.PrincipalID != "admin-1" {
		t.Fatalf("principal = %q, want admin-1", authorization.PrincipalID)
	}
}

func TestServiceBusinessPanelResponsesAreSecretFreeSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	owner := seedF9PanelPrincipal(t, fixture, "owner-1", "owner")
	handler := f9PanelHandler(fixture)

	for _, path := range []string{"/mp/api/customers", "/mp/api/customer?login=" + fixture.customerID} {
		response := f9PanelGET(t, handler, path, owner)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, body = %s", path, response.Code, response.Body.String())
		}
		body := response.Body.String()
		if containsTask9AnyFold(body, fixture.token, `"sub_url"`) {
			t.Fatalf("GET %s leaked a subscription secret: %s", path, body)
		}
	}
}

func insertF9PasswordCredential(t *testing.T, fixture *f5SubscriptionFixture, principalID, password string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	envelope, err := fixture.box.Seal(controlplane.SecretScope{
		OwnerType: "principal",
		OwnerID:   principalID,
		Field:     "password",
		Kind:      "bcrypt",
	}, hash)
	if err != nil {
		t.Fatalf("seal password credential: %v", err)
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal password envelope: %v", err)
	}
	digest := sha256.Sum256(envelopeBytes)
	fixture.sqlite.must(t, rqlite.Statement{
		SQL: `INSERT INTO principal_credentials(
			credential_id,principal_id,credential_type,verifier_envelope,verifier_sha256,active,created_at_unix
		) VALUES(?,?,'password',?,?,1,?)`,
		Args: []any{
			"credential-" + principalID,
			principalID,
			base64.StdEncoding.EncodeToString(envelopeBytes),
			hex.EncodeToString(digest[:]),
			fixture.startedAt.Unix(),
		},
	})
}

func containsTask9AnyFold(value string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(strings.ToLower(value), strings.ToLower(needle)) {
			return true
		}
	}
	return false
}
