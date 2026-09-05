package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

// WhiteListClientMaterial is the protected client half of one durable
// entitlement/exit route credential.
type WhiteListClientMaterial struct {
	PublicHost               string `json:"public_host"`
	SecretPath               string `json:"secret_path"`
	ClientID                 string `json:"client_id"`
	ClientEncryption         string `json:"client_encryption"`
	ClientEncryptionRole     string `json:"client_encryption_role"`
	ClientEncryptionProofRef string `json:"client_encryption_proof_ref"`
}

// WhiteListPublicationDelivery is a side-effect-free view used by the public
// subscription adapter. Material is populated only for a publishable decision.
type WhiteListPublicationDelivery struct {
	Decision        WhiteListPublicationDecision
	Material        WhiteListClientMaterial
	ExitID          string
	CountryCode     string
	CountryLabel    string
	ReleaseID       string
	ProfileID       string
	PresetID        string
	desiredBindings []WhiteListSidecarDesired
}

// WhiteListPublicationDelivery resolves the subscription token through the
// existing durable customer, entitlement, balance, desired-state and receipt
// records. Unknown tokens and missing entitlements are ordinary-only; corrupt
// or incomplete entitled state fails closed.
func (s *Service) WhiteListPublicationDelivery(
	ctx context.Context, rawToken string, now time.Time,
	resolveSender func(string) (ExternalActionSender, bool),
) (WhiteListPublicationDelivery, error) {
	closed := func(verdict WhiteListPublicationVerdict) WhiteListPublicationDelivery {
		return WhiteListPublicationDelivery{Decision: closedWhiteListPublication(verdict)}
	}
	if s == nil || s.store == nil || s.store.db == nil || s.store.secrets == nil ||
		ctx == nil || strings.TrimSpace(rawToken) == "" || now.Unix() <= 0 {
		return closed(WhiteListPublicationNoEntitlement), nil
	}
	customer, err := s.CustomerByToken(ctx, rawToken)
	if errors.Is(err, ErrNotFound) {
		return closed(WhiteListPublicationNoEntitlement), nil
	}
	if err != nil {
		return WhiteListPublicationDelivery{}, ErrUnavailable
	}
	entitlement, err := s.WhiteListEntitlementByAccountID(ctx, customer.ID)
	if errors.Is(err, ErrNotFound) {
		return closed(WhiteListPublicationNoEntitlement), nil
	}
	if err != nil {
		return WhiteListPublicationDelivery{}, ErrUnavailable
	}
	entitlementID := entitlement.EntitlementID()
	return s.whiteListPublicationForEntitlement(ctx, entitlementID, now, resolveSender, true)
}

// Both internal runtime use and public token delivery resolve this same actual
// all-Origin receipt, observation, debit and admission evidence. Internal use
// never substitutes desired/readiness TTLs for fresh metering authority.
func (s *Service) whiteListPublicationForEntitlement(
	ctx context.Context, entitlementID string, now time.Time,
	resolveSender func(string) (ExternalActionSender, bool), includeMaterial bool,
) (WhiteListPublicationDelivery, error) {
	closed := func(verdict WhiteListPublicationVerdict) WhiteListPublicationDelivery {
		return WhiteListPublicationDelivery{Decision: closedWhiteListPublication(verdict)}
	}
	if s == nil || s.store == nil || s.store.db == nil || s.store.secrets == nil || ctx == nil || !validEntitlementID(entitlementID) || now.Unix() <= 0 {
		return WhiteListPublicationDelivery{}, ErrUnavailable
	}
	state, err := s.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		return WhiteListPublicationDelivery{}, err
	}
	publication, ok := state.publications[entitlementID]
	if !ok {
		return closed(WhiteListPublicationNoEntitlement), nil
	}
	facts, err := s.whiteListRuntimePublicationFacts(ctx, now.Unix(), entitlementID, publication)
	if err != nil {
		return WhiteListPublicationDelivery{}, err
	}

	releaseID, profileID, presetID, exitID := "", "", "", ""
	generation := int64(0)
	releaseExact := len(state.origins) > 0
	desired := make([]WhiteListSidecarDesired, 0, len(state.origins))
	for _, origin := range state.origins {
		current, found := state.previous[origin.OriginID]
		if !found || current.ReleaseID != origin.ReleaseID || current.ProfileID != origin.ProfileID ||
			current.PresetID != origin.PresetID || current.ConfigDigest != origin.ConfigDigest {
			releaseExact = false
			continue
		}
		if releaseID == "" {
			releaseID, profileID, presetID = current.ReleaseID, current.ProfileID, current.PresetID
			exitID, generation = current.ExitID, current.Generation
		} else if current.ReleaseID != releaseID || current.ProfileID != profileID || current.PresetID != presetID ||
			current.ExitID != exitID || current.Generation != generation {
			releaseExact = false
		}
		desired = append(desired, current)
	}
	facts.ReleaseBindingExact = releaseExact && len(desired) == len(state.origins)
	exit, exitOK := state.exits[exitID]
	_, credentialOK := state.credentials[entitlementID][exitID]
	facts.CredentialUsable = exitOK && exit.Healthy && credentialOK
	facts.DesiredGeneration = generation

	receiptStatements := make([]rqlite.Statement, 0, len(desired))
	for _, current := range desired {
		receiptStatements = append(receiptStatements, whiteListSidecarReceiptRead(current.Action.ActionKey))
	}
	receiptsFreshUntil := int64(0)
	receiptSetReady := len(receiptStatements) > 0 && resolveSender != nil
	if receiptSetReady {
		results, queryErr := s.store.db.QueryLinearizable(ctx, receiptStatements...)
		if queryErr != nil || len(results) != len(desired) {
			return WhiteListPublicationDelivery{}, ErrUnavailable
		}
		for index, result := range results {
			stored, storedErr := whiteListSidecarReceiptFromResults([]rqlite.Result{result})
			sender, senderOK := resolveSender(desired[index].NodeID)
			lookup, lookupOK := sender.(whiteListSidecarReceiptLookup)
			if storedErr != nil || !senderOK || !lookupOK {
				receiptSetReady = false
				break
			}
			raw, lookupErr := lookup.LookupReceipt(ctx, desired[index].Action.ActionKey)
			live, liveErr := decodeWhiteListSidecarReceipt(raw)
			if lookupErr != nil || liveErr != nil || !whiteListSidecarReceiptPersistedEqual(stored, live) ||
				ValidateWhiteListSidecarReceipt(desired[index], live.XrayProcessBootID, live, now) != nil {
				receiptSetReady = false
				break
			}
			expiresAt := live.ExpiresAt.Unix()
			if receiptsFreshUntil == 0 || expiresAt < receiptsFreshUntil {
				receiptsFreshUntil = expiresAt
			}
		}
	}
	facts.ReceiptSetReady = receiptSetReady
	facts.ReceiptsFreshUntilUnix = receiptsFreshUntil
	facts.ApprovedNodeCount = len(desired)
	if facts.ReleaseBindingExact && facts.CredentialUsable && receiptSetReady {
		facts.ObservedThroughUnix, facts.AdmissionFreshUntilUnix = s.whiteListMeteringPublicationReady(
			ctx, entitlementID, exitID, state.previous,
		)
	}
	decision := EvaluateWhiteListPublication(facts)
	if decision.Verdict != WhiteListPublicationPublishable {
		return WhiteListPublicationDelivery{Decision: decision}, nil
	}
	var material WhiteListClientMaterial
	if includeMaterial {
		material, err = s.whiteListClientMaterial(ctx, entitlementID, exitID)
		if err != nil {
			return WhiteListPublicationDelivery{}, err
		}
	}
	return WhiteListPublicationDelivery{
		Decision: decision, Material: material, ExitID: exitID,
		CountryCode: exit.CountryCode, CountryLabel: exit.CountryLabel,
		ReleaseID: releaseID, ProfileID: profileID, PresetID: presetID,
		desiredBindings: desired,
	}, nil
}

func (s *Service) whiteListClientMaterial(ctx context.Context, entitlementID, exitID string) (WhiteListClientMaterial, error) {
	results, err := s.store.db.QueryLinearizable(ctx, whiteListRouteCredentialRead(entitlementID, exitID))
	row, ok := firstRow(results)
	if err != nil || !ok {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	encoded, ok := whiteListRowBytes(row, "credential_envelope")
	if !ok {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	var envelope Envelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	plaintext, err := s.store.secrets.Open(WhiteListRouteCredentialScope(entitlementID, exitID), envelope)
	if err != nil {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	var material WhiteListClientMaterial
	if err := decoder.Decode(&material); err != nil {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	credential := WhiteListCredential{
		ClientID: material.ClientID, ClientEncryption: material.ClientEncryption,
		ClientEncryptionRole:     material.ClientEncryptionRole,
		ClientEncryptionProofRef: material.ClientEncryptionProofRef,
	}
	if !validPublicHost(material.PublicHost) || !validSecretPath(material.SecretPath) ||
		!validWhiteListCredential(credential) || len(material.ClientEncryption) > 8192 {
		return WhiteListClientMaterial{}, ErrUnavailable
	}
	return material, nil
}
