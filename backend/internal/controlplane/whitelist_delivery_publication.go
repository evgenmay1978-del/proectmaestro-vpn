package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
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

type WhiteListPublicationRoute struct {
	Material     WhiteListClientMaterial
	ExitID       string
	CountryCode  string
	CountryLabel string
}

// WhiteListPublicationDelivery is a side-effect-free view used by the public
// subscription adapter. Material is populated only for a publishable decision.
type WhiteListPublicationDelivery struct {
	Decision        WhiteListPublicationDecision
	Routes          []WhiteListPublicationRoute
	AvailableBytes  int64
	ExpiresAtUnix   int64
	Material        WhiteListClientMaterial
	ExitID          string
	CountryCode     string
	CountryLabel    string
	ReleaseID       string
	ProfileID       string
	PresetID        string
	desiredBindings []WhiteListSidecarDesired
}

// This snapshot belongs to one authorization call. Its immutable origin
// proofs are shared across accounts, while their deadlines are rechecked.
type whiteListPublicationOriginSnapshot struct {
	observations              []whiteListObservedOrigin
	liveReceipts              map[string]WhiteListSidecarReceipt
	observationsValidatedUnix int64
}

func (snapshot *whiteListPublicationOriginSnapshot) observedAt(state whiteListSidecarRuntimeState, now time.Time) ([]whiteListObservedOrigin, error) {
	if snapshot == nil || len(snapshot.observations) == 0 || len(snapshot.observations) != len(state.origins) ||
		snapshot.observationsValidatedUnix <= 0 || snapshot.observationsValidatedUnix > now.Unix() {
		return nil, ErrUnavailable
	}
	for index, observed := range snapshot.observations {
		origin := state.origins[index]
		desired, ok := state.previous[origin.OriginID]
		if !ok || observed.origin.OriginID != origin.OriginID || observed.desired.DesiredSHA256 != desired.DesiredSHA256 ||
			desired.NodeID != origin.NodeID || desired.ReleaseID != origin.ReleaseID || desired.ProfileID != origin.ProfileID ||
			desired.PresetID != origin.PresetID || desired.ConfigDigest != origin.ConfigDigest ||
			ValidateWhiteListSidecarReceipt(desired, observed.receipt.XrayProcessBootID, observed.receipt, now) != nil ||
			observed.sampledAt < observed.receipt.AppliedAt.Unix() || observed.sampledAt > snapshot.observationsValidatedUnix ||
			snapshot.observationsValidatedUnix-observed.sampledAt >= whiteListAccountedObservationTTLSeconds ||
			!whiteListObservationCoverage(desired.ManagedUsers, observed.available, observed.unavailable) {
			return nil, ErrUnavailable
		}
	}
	return snapshot.observations, nil
}

func (s *Service) loadWhiteListPublicationOrigins(ctx context.Context, state whiteListSidecarRuntimeState, resolve func(string) (ExternalActionSender, bool)) (*whiteListPublicationOriginSnapshot, error) {
	observed, err := s.whiteListObservedOriginsFromState(ctx, state)
	if err != nil {
		return nil, err
	}
	snapshot := &whiteListPublicationOriginSnapshot{
		observations:              observed,
		liveReceipts:              make(map[string]WhiteListSidecarReceipt, len(observed)),
		observationsValidatedUnix: s.clock.Now().Unix(),
	}
	for _, origin := range observed {
		sender, ok := resolve(origin.origin.NodeID)
		lookup, lookupOK := sender.(whiteListSidecarReceiptLookup)
		if !ok || !lookupOK {
			return nil, ErrUnavailable
		}
		raw, lookupErr := lookup.LookupReceipt(ctx, origin.desired.Action.ActionKey)
		live, decodeErr := decodeWhiteListSidecarReceipt(raw)
		if lookupErr != nil || decodeErr != nil || !whiteListSidecarReceiptPersistedEqual(origin.receipt, live) ||
			ValidateWhiteListSidecarReceipt(origin.desired, live.XrayProcessBootID, live, s.clock.Now()) != nil {
			return nil, ErrUnavailable
		}
		snapshot.liveReceipts[origin.desired.Action.ActionKey] = live
	}
	return snapshot, nil
}

// WhiteListPublicationDelivery resolves the subscription token through the
// existing durable customer, entitlement, balance, desired-state and receipt
// records. Unknown tokens and missing entitlements are ordinary-only; corrupt
// or incomplete entitled state fails closed.
func (s *Service) WhiteListPublicationDelivery(
	ctx context.Context, rawToken string, now time.Time,
	resolveSender func(string) (ExternalActionSender, bool),
) (WhiteListPublicationDelivery, error) {
	return s.whiteListTokenPublicationDelivery(ctx, rawToken, now, resolveSender, true)
}

// WhiteListNativePublicationDelivery uses the same fresh all-Origin admission
// path as internal runtime authorization, then resolves its client material.
// It never derives an admission deadline from the stable subscription expiry.
func (s *Service) WhiteListNativePublicationDelivery(
	ctx context.Context, rawToken string, now time.Time,
	resolveSender func(string) (ExternalActionSender, bool),
) (WhiteListPublicationDelivery, error) {
	return s.whiteListTokenPublicationDelivery(ctx, rawToken, now, resolveSender, false)
}

func (s *Service) whiteListTokenPublicationDelivery(
	ctx context.Context, rawToken string, now time.Time,
	resolveSender func(string) (ExternalActionSender, bool), stableSubscription bool,
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
	delivery, err := s.whiteListPublicationForEntitlement(ctx, entitlementID, now, resolveSender, stableSubscription)
	if err != nil || stableSubscription || delivery.Decision.Verdict != WhiteListPublicationPublishable {
		return delivery, err
	}
	if err := s.fillWhiteListPublicationMaterials(ctx, entitlementID, delivery.Routes); err != nil {
		return WhiteListPublicationDelivery{}, err
	}
	for _, route := range delivery.Routes {
		if route.ExitID == delivery.ExitID {
			delivery.Material = route.Material
			break
		}
	}
	return delivery, nil
}

// Both internal runtime use and public token delivery resolve this same actual
// all-Origin receipt, observation, debit and admission evidence. Internal use
// never substitutes desired/readiness TTLs for fresh metering authority.
func (s *Service) whiteListPublicationForEntitlement(
	ctx context.Context, entitlementID string, now time.Time,
	resolveSender func(string) (ExternalActionSender, bool), includeMaterial bool,
) (WhiteListPublicationDelivery, error) {
	if s == nil || s.store == nil || s.store.db == nil || s.store.secrets == nil || ctx == nil || !validEntitlementID(entitlementID) || now.Unix() <= 0 {
		return WhiteListPublicationDelivery{}, ErrUnavailable
	}
	state, err := s.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		return WhiteListPublicationDelivery{}, err
	}
	return s.whiteListPublicationForEntitlementFromState(ctx, entitlementID, now, resolveSender, includeMaterial, state)
}

func (s *Service) whiteListPublicationForEntitlementFromState(
	ctx context.Context, entitlementID string, now time.Time,
	resolveSender func(string) (ExternalActionSender, bool), includeMaterial bool, state whiteListSidecarRuntimeState,
	shared ...*whiteListPublicationOriginSnapshot,
) (WhiteListPublicationDelivery, error) {
	closed := func(verdict WhiteListPublicationVerdict) WhiteListPublicationDelivery {
		return WhiteListPublicationDelivery{Decision: closedWhiteListPublication(verdict)}
	}
	if s == nil || s.store == nil || s.store.db == nil || s.store.secrets == nil || ctx == nil || !validEntitlementID(entitlementID) || now.Unix() <= 0 ||
		len(shared) > 1 || (len(shared) == 1 && shared[0] == nil) {
		return WhiteListPublicationDelivery{}, ErrUnavailable
	}
	publication, ok := state.publications[entitlementID]
	if !ok {
		return closed(WhiteListPublicationNoEntitlement), nil
	}
	facts, err := s.whiteListRuntimePublicationFacts(ctx, now.Unix(), entitlementID, publication)
	if err != nil {
		return WhiteListPublicationDelivery{}, err
	}

	releaseID, profileID, presetID, referenceExitID := "", "", "", ""
	managedUserSetDigest := ""
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
			referenceExitID, generation = current.ExitID, current.Generation
			managedUserSetDigest = current.ManagedUserSetDigest
		} else if current.ReleaseID != releaseID || current.ProfileID != profileID || current.PresetID != presetID ||
			current.ExitID != referenceExitID || current.Generation != generation ||
			current.ManagedUserSetDigest != managedUserSetDigest {
			releaseExact = false
		}
		desired = append(desired, current)
	}
	exitIDs, routeSetExact := whiteListPublicationRouteExitIDs(entitlementID, desired)
	referenceExitFound := false
	routes := make([]WhiteListPublicationRoute, 0, len(exitIDs))
	credentialsUsable := routeSetExact
	for _, exitID := range exitIDs {
		exit, exitOK := state.exits[exitID]
		_, credentialOK := state.credentials[entitlementID][exitID]
		if !exitOK || !exit.Healthy || !credentialOK {
			credentialsUsable = false
		}
		if exitID == referenceExitID {
			referenceExitFound = true
		}
		routes = append(routes, WhiteListPublicationRoute{
			ExitID: exitID, CountryCode: exit.CountryCode, CountryLabel: exit.CountryLabel,
		})
	}
	facts.ReleaseBindingExact = releaseExact && routeSetExact && referenceExitFound && len(desired) == len(state.origins)
	facts.CredentialUsable = credentialsUsable
	facts.DesiredGeneration = generation
	stableSubscription := includeMaterial && len(shared) == 0

	receiptStatements := make([]rqlite.Statement, 0, len(desired))
	for _, current := range desired {
		receiptStatements = append(receiptStatements, whiteListSidecarReceiptRead(current.Action.ActionKey))
	}
	receiptsFreshUntil := int64(0)
	receiptSetReady := !stableSubscription && len(receiptStatements) > 0 && resolveSender != nil
	validatedReceipts := make(map[string]WhiteListSidecarReceipt, len(desired))
	if receiptSetReady {
		var results []rqlite.Result
		if len(shared) == 0 {
			var queryErr error
			results, queryErr = s.store.db.QueryLinearizable(ctx, receiptStatements...)
			if queryErr != nil || len(results) != len(desired) {
				return WhiteListPublicationDelivery{}, ErrUnavailable
			}
		}
		for index := range desired {
			var stored, live WhiteListSidecarReceipt
			var storedErr, lookupErr, liveErr error
			checkedAt := now
			if len(shared) == 1 {
				observations, observationErr := shared[0].observedAt(state, s.clock.Now())
				if observationErr != nil || len(observations) != len(desired) {
					return WhiteListPublicationDelivery{}, ErrUnavailable
				}
				stored = observations[index].receipt
				var found bool
				live, found = shared[0].liveReceipts[desired[index].Action.ActionKey]
				if !found {
					return WhiteListPublicationDelivery{}, ErrUnavailable
				}
				checkedAt = s.clock.Now()
			} else {
				stored, storedErr = whiteListSidecarReceiptFromResults(results[index : index+1])
			}
			sender, senderOK := resolveSender(desired[index].NodeID)
			lookup, lookupOK := sender.(whiteListSidecarReceiptLookup)
			if storedErr != nil || !senderOK || !lookupOK {
				receiptSetReady = false
				break
			}
			if len(shared) == 0 {
				var raw []byte
				raw, lookupErr = lookup.LookupReceipt(ctx, desired[index].Action.ActionKey)
				live, liveErr = decodeWhiteListSidecarReceipt(raw)
			}
			if lookupErr != nil || liveErr != nil || !whiteListSidecarReceiptPersistedEqual(stored, live) ||
				ValidateWhiteListSidecarReceipt(desired[index], live.XrayProcessBootID, live, checkedAt) != nil {
				receiptSetReady = false
				break
			}
			validatedReceipts[desired[index].OriginID] = live
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
		if includeMaterial {
			// Subscription pulls happen independently of the five-second metering
			// cadence. Current live receipts plus positive allocations for this
			// exact process boot are enough to show links; the agent's short use
			// lease still gates every forwarded byte.
			facts.AdmissionFreshUntilUnix = s.whiteListPublicationByteBudgetFreshUntil(
				ctx, entitlementID, routes, state, validatedReceipts, now,
			)
		} else {
			meteringReady := len(routes) == whiteListRequiredPublicationRouteCount
			observedThroughInitialized := false
			for index := range routes {
				observedThrough, admissionFreshUntil := s.whiteListMeteringPublicationReadyFromState(
					ctx, entitlementID, routes[index].ExitID, state.previous, state, shared...,
				)
				if admissionFreshUntil <= now.Unix() {
					meteringReady = false
					break
				}
				if !observedThroughInitialized || observedThrough < facts.ObservedThroughUnix {
					facts.ObservedThroughUnix = observedThrough
					observedThroughInitialized = true
				}
				if facts.AdmissionFreshUntilUnix == 0 || admissionFreshUntil < facts.AdmissionFreshUntilUnix {
					facts.AdmissionFreshUntilUnix = admissionFreshUntil
				}
			}
			if !meteringReady {
				facts.ObservedThroughUnix = 0
				facts.AdmissionFreshUntilUnix = 0
			}
		}
	}
	decision := EvaluateWhiteListPublication(facts)
	if stableSubscription {
		decision = evaluateWhiteListSubscriptionPublication(facts)
	}
	if decision.Verdict != WhiteListPublicationPublishable {
		return WhiteListPublicationDelivery{Decision: decision}, nil
	}
	var referenceRoute WhiteListPublicationRoute
	if includeMaterial {
		if err := s.fillWhiteListPublicationMaterials(ctx, entitlementID, routes); err != nil {
			return WhiteListPublicationDelivery{}, err
		}
	}
	for _, route := range routes {
		if route.ExitID == referenceExitID {
			referenceRoute = route
			break
		}
	}
	return WhiteListPublicationDelivery{
		Decision: decision, Routes: routes, AvailableBytes: facts.AvailableBytes,
		ExpiresAtUnix: facts.PrimaryExpiresAtUnix,
		Material: referenceRoute.Material, ExitID: referenceRoute.ExitID,
		CountryCode: referenceRoute.CountryCode, CountryLabel: referenceRoute.CountryLabel,
		ReleaseID: releaseID, ProfileID: profileID, PresetID: presetID,
		desiredBindings: desired,
	}, nil
}

func (s *Service) fillWhiteListPublicationMaterials(ctx context.Context, entitlementID string, routes []WhiteListPublicationRoute) error {
	seenCountries := make(map[string]struct{}, len(routes))
	seenLabels := make(map[string]struct{}, len(routes))
	seenClientIDs := make(map[string]struct{}, len(routes))
	for index := range routes {
		material, err := s.whiteListClientMaterial(ctx, entitlementID, routes[index].ExitID)
		if err != nil || routes[index].CountryCode == "" || routes[index].CountryLabel == "" {
			return ErrUnavailable
		}
		routes[index].Material = material
		_, countryExists := seenCountries[routes[index].CountryCode]
		_, labelExists := seenLabels[routes[index].CountryLabel]
		_, clientExists := seenClientIDs[material.ClientID]
		if countryExists || labelExists || clientExists {
			return ErrUnavailable
		}
		seenCountries[routes[index].CountryCode] = struct{}{}
		seenLabels[routes[index].CountryLabel] = struct{}{}
		seenClientIDs[material.ClientID] = struct{}{}
	}
	return nil
}

func (s *Service) whiteListPublicationByteBudgetFreshUntil(
	ctx context.Context, entitlementID string, routes []WhiteListPublicationRoute,
	state whiteListSidecarRuntimeState, receipts map[string]WhiteListSidecarReceipt, now time.Time,
) int64 {
	if len(routes) != whiteListRequiredPublicationRouteCount || len(receipts) != len(state.origins) {
		return 0
	}
	freshUntil := now.Unix() + whiteListObservationFreshnessSeconds
	for _, route := range routes {
		period, available, periodEndsAt, err := s.whiteListAdmissionBaseFromState(ctx, entitlementID, route.ExitID, state)
		if err != nil || available <= 0 {
			return 0
		}
		if periodEndsAt < freshUntil {
			freshUntil = periodEndsAt
		}
		for _, origin := range state.origins {
			receipt, ok := receipts[origin.OriginID]
			if !ok {
				return 0
			}
			row, err := s.whiteListByteAllocationRow(ctx, entitlementID, route.ExitID, origin.OriginID, receipt.XrayProcessBootID)
			boundPeriod, periodOK := rowString(row, "billing_period_id")
			outstanding, outstandingOK := rowInt64(row, "outstanding_bytes")
			if err != nil || !periodOK || !outstandingOK || boundPeriod != period || outstanding <= 0 {
				return 0
			}
			if receipt.ExpiresAt.Unix() < freshUntil {
				freshUntil = receipt.ExpiresAt.Unix()
			}
		}
	}
	if freshUntil <= now.Unix() {
		return 0
	}
	return freshUntil
}

const whiteListRequiredPublicationRouteCount = 4

func whiteListPublicationRouteExitIDs(entitlementID string, desired []WhiteListSidecarDesired) ([]string, bool) {
	required := []string{"exit-s1", "exit-s2", "exit-s3", "exit-s4"}
	prefix := "wl:" + entitlementID + ":"
	if len(desired) == 0 {
		return nil, false
	}
	for _, binding := range desired {
		exitIDs := make([]string, 0, whiteListRequiredPublicationRouteCount)
		for _, managedUser := range binding.ManagedUsers {
			if !strings.HasPrefix(managedUser, prefix) {
				continue
			}
			exitID := strings.TrimPrefix(managedUser, prefix)
			if exitID == "" || managedUser != whiteListManagedEmail(entitlementID, exitID) {
				return nil, false
			}
			exitIDs = append(exitIDs, exitID)
		}
		sort.Strings(exitIDs)
		if !whiteListStringsEqual(exitIDs, required) {
			return nil, false
		}
	}
	return required, true
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
