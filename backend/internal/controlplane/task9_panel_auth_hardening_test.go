package controlplane_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func TestAuthenticatePasswordHardeningSQLite(t *testing.T) {
	t.Run("owner and admin authenticate as their own principal", func(t *testing.T) {
		fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
		seedF9PanelPrincipal(t, fixture, "owner-1", "owner")
		seedF9PanelPrincipal(t, fixture, "admin-1", "admin")
		insertF9PasswordCredential(t, fixture, "owner-1", "owner-password")
		insertF9PasswordCredential(t, fixture, "admin-1", "admin-password")

		assertF9PasswordPrincipal(t, fixture, "owner-password", "owner-1")
		assertF9PasswordPrincipal(t, fixture, "admin-password", "admin-1")
	})

	t.Run("blank and wrong passwords are unauthenticated", func(t *testing.T) {
		fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
		seedF9PanelPrincipal(t, fixture, "owner-1", "owner")
		insertF9PasswordCredential(t, fixture, "owner-1", "owner-password")

		for _, password := range []string{"", "wrong-password"} {
			if _, err := fixture.service.AuthenticatePassword(context.Background(), password); !errors.Is(err, controlplane.ErrUnauthenticated) {
				t.Fatalf("AuthenticatePassword(%q) error = %v, want ErrUnauthenticated", password, err)
			}
		}
	})

	t.Run("bounded candidate set includes the eighth principal", func(t *testing.T) {
		fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
		for index := 0; index < 8; index++ {
			principalID := fmt.Sprintf("admin-%02d", index)
			password := fmt.Sprintf("password-%02d", index)
			seedF9PanelPrincipal(t, fixture, principalID, "admin")
			insertF9PasswordCredential(t, fixture, principalID, password)
		}

		assertF9PasswordPrincipal(t, fixture, "password-07", "admin-07")
	})

	t.Run("candidate overflow fails closed before authentication", func(t *testing.T) {
		fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
		for index := 0; index < 9; index++ {
			principalID := fmt.Sprintf("admin-%02d", index)
			password := fmt.Sprintf("password-%02d", index)
			seedF9PanelPrincipal(t, fixture, principalID, "admin")
			insertF9PasswordCredential(t, fixture, principalID, password)
		}

		if _, err := fixture.service.AuthenticatePassword(context.Background(), "password-00"); !errors.Is(err, controlplane.ErrUnavailable) {
			t.Fatalf("overflow error = %v, want ErrUnavailable", err)
		}
	})

	t.Run("duplicate passwords across principals fail closed", func(t *testing.T) {
		fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
		for _, principalID := range []string{"owner-1", "admin-1"} {
			role := "admin"
			if principalID == "owner-1" {
				role = "owner"
			}
			seedF9PanelPrincipal(t, fixture, principalID, role)
			insertF9PasswordCredential(t, fixture, principalID, "shared-password")
		}

		if _, err := fixture.service.AuthenticatePassword(context.Background(), "shared-password"); !errors.Is(err, controlplane.ErrUnavailable) {
			t.Fatalf("duplicate-password error = %v, want ErrUnavailable", err)
		}
	})

	t.Run("canceled context fails closed", func(t *testing.T) {
		fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
		seedF9PanelPrincipal(t, fixture, "owner-1", "owner")
		insertF9PasswordCredential(t, fixture, "owner-1", "owner-password")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := fixture.service.AuthenticatePassword(ctx, "owner-password"); !errors.Is(err, controlplane.ErrUnavailable) {
			t.Fatalf("canceled-context error = %v, want ErrUnavailable", err)
		}
	})
}

func assertF9PasswordPrincipal(t *testing.T, fixture *f5SubscriptionFixture, password, wantPrincipalID string) {
	t.Helper()
	session, err := fixture.service.AuthenticatePassword(context.Background(), password)
	if err != nil {
		t.Fatalf("authenticate %s: %v", wantPrincipalID, err)
	}
	authorization, err := fixture.service.Authorize(
		context.Background(),
		session.Cookie.Value,
		session.CSRFToken,
		controlplane.PermissionCustomerRead,
	)
	if err != nil {
		t.Fatalf("authorize %s: %v", wantPrincipalID, err)
	}
	if authorization.PrincipalID != wantPrincipalID {
		t.Fatalf("principal = %q, want %q", authorization.PrincipalID, wantPrincipalID)
	}
}
