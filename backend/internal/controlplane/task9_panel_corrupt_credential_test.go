package controlplane_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestAuthenticatePasswordCorruptCredentialFailsClosedUnavailableSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	seedF9PanelPrincipal(t, fixture, "owner-1", "owner")
	fixture.sqlite.must(t, rqlite.Statement{
		SQL: `INSERT INTO principal_credentials(
			credential_id,principal_id,credential_type,verifier_envelope,verifier_sha256,active,created_at_unix
		) VALUES(?,?,'password',?,?,1,?)`,
		Args: []any{
			"credential-owner-1",
			"owner-1",
			"not-valid-base64!",
			strings.Repeat("0", 64),
			fixture.startedAt.Unix(),
		},
	})

	if _, err := fixture.service.AuthenticatePassword(context.Background(), "owner-password"); !errors.Is(err, controlplane.ErrUnavailable) {
		t.Fatalf("corrupt credential error = %v, want ErrUnavailable", err)
	}
}
