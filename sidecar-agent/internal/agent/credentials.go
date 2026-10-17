package agent

import (
	"context"
	"regexp"
)

// CredentialInstall adds secret material only. Desired state and use leases
// remain the exclusive authority to add or enable an Xray user.
type CredentialInstall struct {
	Schema       int    `json:"schema"`
	ReleaseID    string `json:"release_id"`
	ConfigDigest string `json:"config_digest"`
	ManagedEmail string `json:"managed_email"`
	ClientID     string `json:"client_id"`
}

type credentialInstaller interface {
	InstallCredential(context.Context, string, string) error
}

var credentialUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (reconciler *Reconciler) InstallCredential(ctx context.Context, request CredentialInstall) error {
	if reconciler == nil || ctx == nil || !reconciler.managedLeaseEnabled || request.Schema != 1 ||
		!safeEmail(request.ManagedEmail) || !credentialUUID.MatchString(request.ClientID) {
		return ErrConflict
	}
	validEmail := false
	for _, exit := range []string{"exit-s1", "exit-s2", "exit-s3", "exit-s4"} {
		validEmail = validEmail || managedEmailForExit(request.ManagedEmail, exit)
	}
	if !validEmail || request.ReleaseID != reconciler.releaseID || request.ConfigDigest != reconciler.configDigest {
		return ErrConflict
	}
	installer, ok := reconciler.handler.(credentialInstaller)
	if !ok {
		return ErrLeaseUnavailable
	}
	reconciler.mutex.Lock()
	defer reconciler.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return ErrLeaseUnavailable
	}
	return installer.InstallCredential(ctx, request.ManagedEmail, request.ClientID)
}
