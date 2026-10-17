package controlplane

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
)

type whiteListCredentialInstaller interface {
	InstallCredential(context.Context, string, string, string, string) error
}

type whiteListCredentialTemplateSource interface {
	WhiteListCredentialTemplate(context.Context) ([]byte, error)
}

// EnsureWhiteListRouteCredential persists the only random UUID for this
// entitlement/exit. Concurrent callers adopt the committed winner; neither
// retries nor controller restarts replace an existing credential.
func (s *Service) EnsureWhiteListRouteCredential(
	ctx context.Context, entitlementID, exitID string, template WhiteListClientMaterial,
) (WhiteListClientMaterial, error) {
	if s == nil || s.store == nil || s.store.db == nil || s.store.secrets == nil || ctx == nil ||
		!validEntitlementID(entitlementID) || !routeCredentialExit(exitID) || !validRouteCredentialTemplate(template) {
		return WhiteListClientMaterial{}, ErrConflict
	}
	if material, found, err := s.existingWhiteListRouteMaterial(ctx, entitlementID, exitID); err != nil || found {
		if err == nil && !sameRouteTemplate(material, template) {
			err = ErrConflict
		}
		return material, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	random[6] = random[6]&0x0f | 0x40
	random[8] = random[8]&0x3f | 0x80
	encoded := hex.EncodeToString(random[:])
	material := template
	material.ClientID = encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
	plaintext, err := json.Marshal(material)
	if err != nil {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	defer clear(plaintext)
	credential, err := NewWhiteListRouteCredential(s.store.secrets, entitlementID, exitID, plaintext)
	if err != nil {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	if err := s.StoreWhiteListRouteCredential(ctx, credential); err == nil {
		return material, nil
	}
	// INSERT OR IGNORE and the immutable composite key select one durable UUID.
	// Re-read on both conflicts and ambiguous writes instead of generating again.
	winner, found, err := s.existingWhiteListRouteMaterial(ctx, entitlementID, exitID)
	if err != nil || !found {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	if !sameRouteTemplate(winner, template) {
		return WhiteListClientMaterial{}, ErrConflict
	}
	return winner, nil
}

func (s *Service) existingWhiteListRouteMaterial(ctx context.Context, entitlementID, exitID string) (WhiteListClientMaterial, bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, whiteListRouteCredentialRead(entitlementID, exitID))
	if err != nil || len(results) != 1 || len(results[0].Rows) > 1 {
		return WhiteListClientMaterial{}, false, ErrUnavailable
	}
	if len(results[0].Rows) == 0 {
		return WhiteListClientMaterial{}, false, nil
	}
	row := results[0].Rows[0]
	storedEntitlement, entitlementOK := rowString(row, "entitlement_id")
	storedExit, exitOK := rowString(row, "exit_id")
	storedEmail, emailOK := rowString(row, "managed_email")
	if !entitlementOK || !exitOK || !emailOK || storedEntitlement != entitlementID || storedExit != exitID ||
		storedEmail != whiteListManagedEmail(entitlementID, exitID) {
		return WhiteListClientMaterial{}, true, ErrUnavailable
	}
	material, err := s.whiteListClientMaterial(ctx, entitlementID, exitID)
	return material, true, err
}

const whiteListRequiredExitCount = 4

var whiteListRequiredExitIDs = [whiteListRequiredExitCount]string{"exit-s1", "exit-s2", "exit-s3", "exit-s4"}

func routeCredentialExit(exitID string) bool {
	return exitID == "exit-s1" || exitID == "exit-s2" || exitID == "exit-s3" || exitID == "exit-s4"
}

func whiteListRequiredRuntimeExits(exits map[string]WhiteListExit) ([]WhiteListExit, bool) {
	result := make([]WhiteListExit, 0, whiteListRequiredExitCount)
	for _, exitID := range whiteListRequiredExitIDs {
		exit, ok := exits[exitID]
		if !ok || !exit.Healthy {
			return nil, false
		}
		exit.ExitID = exitID
		result = append(result, exit)
	}
	return result, true
}

func whiteListPerRouteChunkBytes(totalBytes int64) (int64, bool) {
	if totalBytes <= 0 || totalBytes%whiteListRequiredExitCount != 0 {
		return 0, false
	}
	return totalBytes / whiteListRequiredExitCount, true
}

func validRouteCredentialTemplate(template WhiteListClientMaterial) bool {
	return template.ClientID == "" && validPublicHost(template.PublicHost) && validSecretPath(template.SecretPath) &&
		len(template.ClientEncryption) <= 8192 && template.ClientEncryptionRole == "CLIENT" &&
		validClientEncryption(template.ClientEncryption) && validClientEncryptionProof(WhiteListCredential{
		ClientEncryption: template.ClientEncryption, ClientEncryptionRole: template.ClientEncryptionRole,
		ClientEncryptionProofRef: template.ClientEncryptionProofRef,
	})
}

func sameRouteTemplate(material, template WhiteListClientMaterial) bool {
	material.ClientID = ""
	return material == template
}

func decodeRouteCredentialTemplate(raw []byte) (WhiteListClientMaterial, error) {
	if len(raw) == 0 || len(raw) > 16384 {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var template WhiteListClientMaterial
	if decoder.Decode(&template) != nil || !validRouteCredentialTemplate(template) {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	return template, nil
}

// prepareWhiteListRouteCredentials runs before desired-state publication. A
// credential-install failure closes only that entitlement for this pass so
// unrelated disables/expiry revocations can still reach their existing agents.
func (s *Service) prepareWhiteListRouteCredentials(
	ctx context.Context, state *whiteListSidecarRuntimeState, resolveSender func(string) (ExternalActionSender, bool),
) error {
	exits, exitsReady := whiteListRequiredRuntimeExits(state.exits)
	if !exitsReady || len(state.origins) == 0 {
		return nil
	}
	releaseID := state.origins[0].ReleaseID
	installers := make(map[string]whiteListCredentialInstaller, len(state.origins))
	var templateSource whiteListCredentialTemplateSource
	for _, origin := range state.origins {
		if !origin.Active || origin.ReleaseID == "" || origin.ReleaseID != releaseID {
			return nil // Existing release-consistency logic closes publication.
		}
		sender, ok := resolveSender(origin.NodeID)
		if !ok || sender == nil {
			continue
		}
		if installer, ok := sender.(whiteListCredentialInstaller); ok {
			installers[origin.OriginID] = installer
		}
		if templateSource == nil {
			templateSource, _ = sender.(whiteListCredentialTemplateSource)
		}
	}
	entitlements := make([]string, 0, len(state.publications))
	for entitlementID := range state.publications {
		entitlements = append(entitlements, entitlementID)
	}
	sort.Strings(entitlements)
	var deferredErr error
	var template WhiteListClientMaterial
	templateLoaded := false
entitlementLoop:
	for _, entitlementID := range entitlements {
		publication := state.publications[entitlementID]
		if !publication.Enabled || publication.PrimaryStatus != "active" || publication.PrimaryExpiresAtUnix <= s.clock.Now().Unix() {
			continue
		}
		balance, err := s.WhiteListBalanceSnapshot(ctx, s.clock.Now().Unix(), entitlementID)
		if err != nil {
			publication.Enabled = false
			state.publications[entitlementID] = publication
			delete(state.credentials, entitlementID)
			deferredErr = ErrUnavailable
			continue
		}
		if balance.AvailableBytes <= 0 || balance.Projection.Pending {
			continue
		}
		for _, exit := range exits {
			exitID := exit.ExitID
			_, stored := state.credentials[entitlementID][exitID]
			if stored && routeCredentialAlreadyDesired(state, entitlementID, exitID) {
				continue
			}
			closeForPass := func() {
				publication.Enabled = false
				state.publications[entitlementID] = publication
				delete(state.credentials, entitlementID)
				deferredErr = ErrUnavailable
			}
			// Existing externally provisioned senders remain supported. New automatic
			// credentials require the authenticated install capability on every Origin.
			if len(installers) != len(state.origins) {
				if !stored {
					closeForPass()
					continue entitlementLoop
				}
				continue
			}
			var material WhiteListClientMaterial
			if stored {
				material, err = s.whiteListClientMaterial(ctx, entitlementID, exitID)
			} else {
				if !templateLoaded {
					if templateSource == nil {
						closeForPass()
						continue entitlementLoop
					}
					raw, readErr := templateSource.WhiteListCredentialTemplate(ctx)
					if readErr == nil {
						template, readErr = decodeRouteCredentialTemplate(raw)
					}
					clear(raw)
					if readErr != nil {
						closeForPass()
						continue entitlementLoop
					}
					templateLoaded = true
				}
				material, err = s.EnsureWhiteListRouteCredential(ctx, entitlementID, exitID, template)
			}
			if err != nil {
				closeForPass()
				continue entitlementLoop
			}
			installed := true
			for _, origin := range state.origins {
				if err := installers[origin.OriginID].InstallCredential(ctx, origin.ReleaseID, origin.ConfigDigest,
					whiteListManagedEmail(entitlementID, exitID), material.ClientID); err != nil {
					installed = false
					break
				}
			}
			if !installed {
				closeForPass()
				continue entitlementLoop
			}
			if state.credentials[entitlementID] == nil {
				state.credentials[entitlementID] = make(map[string]struct{})
			}
			state.credentials[entitlementID][exitID] = struct{}{}
		}
	}
	return deferredErr
}

func routeCredentialAlreadyDesired(state *whiteListSidecarRuntimeState, entitlementID, exitID string) bool {
	email := whiteListManagedEmail(entitlementID, exitID)
	for _, origin := range state.origins {
		previous, ok := state.previous[origin.OriginID]
		if !ok || previous.ReleaseID != origin.ReleaseID || previous.ConfigDigest != origin.ConfigDigest {
			return false
		}
		found := false
		for _, managed := range previous.ManagedUsers {
			found = found || managed == email
		}
		if !found {
			return false
		}
	}
	return len(state.origins) > 0
}
