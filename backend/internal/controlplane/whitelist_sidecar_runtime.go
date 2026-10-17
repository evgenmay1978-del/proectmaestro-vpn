package controlplane

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

// ReconcileWhiteListSidecarIntents consumes the durable white-list publication
// controls through the complete fail-closed publication decision. A closed
// decision is converted to a revoke before a removal generation is sent. An
// enabled control is publishable only after the resulting generation is ready
// on every active Origin.
func (s *Service) ReconcileWhiteListSidecarIntents(
	ctx context.Context,
	workerID string,
	resolveSender func(string) (ExternalActionSender, bool),
) (resultErr error) {
	if s == nil || s.store == nil || s.store.db == nil || ctx == nil ||
		strings.TrimSpace(workerID) == "" || resolveSender == nil {
		return ErrConflict
	}
	state, err := s.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		return err
	}
	credentialErr := s.prepareWhiteListRouteCredentials(ctx, &state, resolveSender)
	defer func() {
		if resultErr == nil {
			resultErr = credentialErr
		}
	}()
	previousRoutes, previousExit, err := whiteListPreviousManagedRoutes(state.previous)
	if err != nil {
		return err
	}
	previousEntitlements := make(map[string]struct{}, len(previousRoutes))
	for _, route := range previousRoutes {
		previousEntitlements[route.EntitlementID] = struct{}{}
	}
	exits, exitsReady := whiteListRequiredRuntimeExits(state.exits)
	now := s.clock.Now()
	releaseID := ""
	releaseBindingExact := true
	approvedNodeCount := 0
	for _, origin := range state.origins {
		if !origin.Active {
			continue
		}
		approvedNodeCount++
		if releaseID == "" {
			releaseID = origin.ReleaseID
		}
		if origin.ReleaseID == "" || origin.ReleaseID != releaseID {
			releaseBindingExact = false
		}
	}
	if approvedNodeCount == 0 {
		releaseBindingExact = false
	}
	factsByEntitlement := make(map[string]WhiteListPublicationFacts, len(state.publications))
	decisions := make(map[string]WhiteListPublicationDecision, len(state.publications))
	provisioning := make(map[string]bool)
	targetEntitlements := make([]string, 0, len(state.publications))
	for entitlementID, publication := range state.publications {
		facts, factsErr := s.whiteListRuntimePublicationFacts(ctx, now.Unix(), entitlementID, publication)
		if factsErr != nil {
			return factsErr
		}
		facts.ReleaseBindingExact = releaseBindingExact
		facts.CredentialUsable = exitsReady && whiteListRuntimeCredentialUsable(state.credentials[entitlementID], state.exits)
		facts.DesiredGeneration = 1
		facts.ReceiptSetReady = approvedNodeCount > 0
		facts.ReceiptsFreshUntilUnix = now.Unix() + 1
		facts.ApprovedNodeCount = approvedNodeCount
		if facts.CredentialUsable {
			ready := true
			for _, exit := range exits {
				through, until := s.whiteListMeteringPublicationReadyFromState(
					ctx, entitlementID, exit.ExitID, state.previous, state,
				)
				if until <= now.Unix() {
					ready = false
					break
				}
				if facts.ObservedThroughUnix == 0 || (through > 0 && through < facts.ObservedThroughUnix) {
					facts.ObservedThroughUnix = through
				}
				if facts.AdmissionFreshUntilUnix == 0 || until < facts.AdmissionFreshUntilUnix {
					facts.AdmissionFreshUntilUnix = until
				}
			}
			if !ready {
				facts.ObservedThroughUnix, facts.AdmissionFreshUntilUnix = 0, 0
			}
		}
		decision := EvaluateWhiteListPublication(facts)
		factsByEntitlement[entitlementID] = facts
		decisions[entitlementID] = decision
		_, wasManaged := previousEntitlements[entitlementID]
		if wasManaged && decision.Verdict == WhiteListPublicationProjectionStale &&
			facts.AvailableBytes > 0 && releaseBindingExact && facts.CredentialUsable {
			// Keep the paid route installed while its short observation lease is
			// renewed. The agent still denies traffic when the use lease expires;
			// removing the user here would restart the same bootstrap cycle.
			provisioning[entitlementID] = true
			targetEntitlements = append(targetEntitlements, entitlementID)
			continue
		}
		if decision.Verdict == WhiteListPublicationPublishable {
			targetEntitlements = append(targetEntitlements, entitlementID)
			continue
		}
		if releaseBindingExact && facts.CredentialUsable {
			allAwaiting := true
			for _, exit := range exits {
				if _, _, ready := s.whiteListMeteringReadinessFromState(ctx, entitlementID, exit.ExitID, true, nil, state); !ready {
					allAwaiting = false
					break
				}
			}
			if allAwaiting {
				provisioning[entitlementID] = true
				targetEntitlements = append(targetEntitlements, entitlementID)
			}
		}
	}
	sort.Strings(targetEntitlements)
	if len(targetEntitlements) == 0 && len(previousEntitlements) == 0 {
		return nil
	}
	selectedExit, err := whiteListSelectedRuntimeExit(targetEntitlements, previousExit, state.credentials, state.exits)
	if err != nil {
		return err
	}
	for entitlementID := range previousEntitlements {
		decision, ok := decisions[entitlementID]
		if ok && (decision.Verdict == WhiteListPublicationPublishable || provisioning[entitlementID]) {
			continue
		}
		if !ok {
			decision = closedWhiteListPublication(WhiteListPublicationNoEntitlement)
		}
		intent, changed, deriveErr := DeriveWhiteListPublicationIntent(entitlementID, true, decision)
		if deriveErr != nil || !changed || intent.Action != WhiteListPublicationRevoke {
			return ErrConflict
		}
	}
	for index := range state.origins {
		if previous, ok := state.previous[state.origins[index].OriginID]; ok {
			state.origins[index].StaticUsers = append([]string{}, previous.StaticUsers...)
		}
	}
	routes := make([]WhiteListManagedRoute, 0, len(targetEntitlements)*whiteListRequiredExitCount)
	for _, entitlementID := range targetEntitlements {
		for _, exit := range exits {
			routes = append(routes, WhiteListManagedRoute{EntitlementID: entitlementID, ExitID: exit.ExitID})
		}
	}
	result, err := s.ReconcileWhiteListSidecarGeneration(
		ctx, state.previous, state.origins, routes, selectedExit, workerID, resolveSender,
	)
	if err != nil {
		return err
	}
	if len(targetEntitlements) == 0 {
		return nil
	}
	for _, entitlementID := range targetEntitlements {
		if provisioning[entitlementID] {
			if !result.Ready {
				return ErrUnavailable
			}
			continue
		}
		facts := factsByEntitlement[entitlementID]
		facts.ReleaseBindingExact = releaseBindingExact && result.ReleaseID == releaseID
		facts.CredentialUsable = whiteListRuntimeCredentialUsable(state.credentials[entitlementID], state.exits)
		facts.DesiredGeneration = result.Generation
		facts.ReceiptSetReady = result.Ready
		facts.ReceiptsFreshUntilUnix = result.FreshUntil.Unix()
		facts.ApprovedNodeCount = len(result.Receipts)
		decision := EvaluateWhiteListPublication(facts)
		if decision.Verdict != WhiteListPublicationPublishable {
			return ErrUnavailable
		}
		_, wasManaged := previousEntitlements[entitlementID]
		intent, changed, deriveErr := DeriveWhiteListPublicationIntent(entitlementID, wasManaged, decision)
		if deriveErr != nil || (!wasManaged && (!changed || intent.Action != WhiteListPublicationEnable)) {
			return ErrConflict
		}
	}
	return nil
}

type whiteListSidecarRuntimeState struct {
	origins      []WhiteListOrigin
	previous     map[string]WhiteListSidecarDesired
	publications map[string]whiteListRuntimePublication
	credentials  map[string]map[string]struct{}
	exits        map[string]WhiteListExit
}

type whiteListRuntimePublication struct {
	Enabled              bool
	Source               WhiteListActivationSource
	PrimaryStatus        string
	PrimaryExpiresAtUnix int64
}

func (s *Service) whiteListRuntimePublicationFacts(
	ctx context.Context,
	nowUnix int64,
	entitlementID string,
	publication whiteListRuntimePublication,
) (WhiteListPublicationFacts, error) {
	facts := WhiteListPublicationFacts{
		NowUnix:          nowUnix,
		ActivationSource: publication.Source, ActivationEntitlementID: entitlementID,
		EntitlementID: entitlementID, EntitlementState: EntitlementDisabled,
		PrimaryStatus: publication.PrimaryStatus, PrimaryExpiresAtUnix: publication.PrimaryExpiresAtUnix,
	}
	if !publication.Enabled {
		return facts, nil
	}
	snapshot, err := s.WhiteListBalanceSnapshot(ctx, nowUnix, entitlementID)
	if err != nil {
		return WhiteListPublicationFacts{}, err
	}
	facts.EntitlementState = EntitlementActive
	facts.ProjectionVersion = snapshot.Projection.Version
	facts.ProjectionPending = snapshot.Projection.Pending
	facts.AvailableBytes = snapshot.AvailableBytes
	// Metering readiness is resolved by the caller against its exact desired
	// exit/generation; neither balance freshness nor another credential suffices.
	return facts, nil
}

func whiteListRuntimeCredentialUsable(available map[string]struct{}, exits map[string]WhiteListExit) bool {
	required, ok := whiteListRequiredRuntimeExits(exits)
	if !ok {
		return false
	}
	for _, exit := range required {
		if _, exists := available[exit.ExitID]; !exists {
			return false
		}
	}
	return true
}

func (s *Service) loadWhiteListSidecarRuntimeState(ctx context.Context) (whiteListSidecarRuntimeState, error) {
	primaryErr := s.refreshLegacyPrimaryRuntime(ctx)
	results, err := s.store.db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT origin_id,node_id,release_id,profile_id,preset_id,config_digest,active
FROM whitelist_sidecar_origins WHERE active=1 ORDER BY origin_id`},
		rqlite.Statement{SQL: `SELECT desired.origin_id,desired.desired_generation,desired.node_id,
desired.release_id,desired.profile_id,desired.preset_id,desired.exit_id,desired.config_digest,
desired.managed_user_set_digest,desired.desired_sha256,desired.payload_json,desired.action_type,desired.action_key
FROM whitelist_sidecar_desired AS desired
JOIN (
 SELECT origin_id,MAX(desired_generation) AS desired_generation
 FROM whitelist_sidecar_desired GROUP BY origin_id
) AS latest ON latest.origin_id=desired.origin_id AND latest.desired_generation=desired.desired_generation
JOIN whitelist_sidecar_origins AS origin ON origin.origin_id=desired.origin_id AND origin.active=1
ORDER BY desired.origin_id`},
		rqlite.Statement{SQL: `SELECT control.entitlement_id,control.enabled,control.source,
customer.status AS primary_status,customer.expires_at_unix AS primary_expires_at_unix
FROM whitelist_publication_controls AS control
JOIN (
 SELECT entitlement_id,MAX(version) AS version
 FROM whitelist_publication_controls GROUP BY entitlement_id
) AS latest ON latest.entitlement_id=control.entitlement_id AND latest.version=control.version
JOIN whitelist_entitlement_identities AS entitlement ON entitlement.entitlement_id=control.entitlement_id
JOIN customers AS customer ON customer.customer_id=entitlement.customer_id
ORDER BY control.entitlement_id`},
		rqlite.Statement{SQL: `SELECT entitlement_id,exit_id FROM whitelist_route_credentials ORDER BY entitlement_id,exit_id`},
		rqlite.Statement{SQL: `SELECT exit_id,country_code,country_label,healthy FROM whitelist_sidecar_exits ORDER BY exit_id`},
	)
	if err != nil || len(results) != 5 {
		return whiteListSidecarRuntimeState{}, ErrUnavailable
	}
	state := whiteListSidecarRuntimeState{
		previous: make(map[string]WhiteListSidecarDesired), publications: make(map[string]whiteListRuntimePublication),
		credentials: make(map[string]map[string]struct{}), exits: make(map[string]WhiteListExit),
	}
	for _, row := range results[0].Rows {
		originID, originOK := rowString(row, "origin_id")
		nodeID, nodeOK := rowString(row, "node_id")
		releaseID, releaseOK := rowString(row, "release_id")
		profileID, profileOK := rowString(row, "profile_id")
		presetID, presetOK := rowString(row, "preset_id")
		configDigest, digestOK := rowString(row, "config_digest")
		active, activeOK := rowInt64(row, "active")
		if !originOK || !nodeOK || !releaseOK || !profileOK || !presetOK || !digestOK ||
			!activeOK || active != 1 {
			return whiteListSidecarRuntimeState{}, ErrUnavailable
		}
		state.origins = append(state.origins, WhiteListOrigin{
			OriginID: originID, NodeID: nodeID, ReleaseID: releaseID, ProfileID: profileID,
			PresetID: presetID, ConfigDigest: configDigest, Active: true,
		})
	}
	for _, row := range results[1].Rows {
		desired, decodeErr := whiteListRuntimeDesiredFromRow(row)
		if decodeErr != nil {
			return whiteListSidecarRuntimeState{}, decodeErr
		}
		state.previous[desired.OriginID] = desired
	}
	for _, row := range results[2].Rows {
		entitlementID, entitlementOK := rowString(row, "entitlement_id")
		enabled, enabledOK := rowInt64(row, "enabled")
		source, sourceOK := rowString(row, "source")
		primaryStatus, primaryStatusOK := rowString(row, "primary_status")
		primaryExpiresAtUnix, primaryExpiresOK := rowInt64(row, "primary_expires_at_unix")
		if !entitlementOK || !validWhiteListID(entitlementID) || !enabledOK || (enabled != 0 && enabled != 1) {
			return whiteListSidecarRuntimeState{}, ErrUnavailable
		}
		if !sourceOK || !validWhiteListPublicationSource(source) || !primaryStatusOK || primaryStatus == "" || !primaryExpiresOK {
			return whiteListSidecarRuntimeState{}, ErrUnavailable
		}
		state.publications[entitlementID] = whiteListRuntimePublication{
			Enabled: enabled == 1, Source: WhiteListActivationSource(source),
			PrimaryStatus: primaryStatus, PrimaryExpiresAtUnix: primaryExpiresAtUnix,
		}
		if primaryErr != nil {
			// Retain enough state to revoke an existing route. A failed registry
			// read must not authorize admission or renew a stale primary account.
			publication := state.publications[entitlementID]
			publication.PrimaryStatus = "suspended"
			state.publications[entitlementID] = publication
		}
	}
	for _, row := range results[3].Rows {
		entitlementID, entitlementOK := rowString(row, "entitlement_id")
		exitID, exitOK := rowString(row, "exit_id")
		if !entitlementOK || !validWhiteListID(entitlementID) || !exitOK || exitID == "" {
			return whiteListSidecarRuntimeState{}, ErrUnavailable
		}
		if state.credentials[entitlementID] == nil {
			state.credentials[entitlementID] = make(map[string]struct{})
		}
		state.credentials[entitlementID][exitID] = struct{}{}
	}
	for _, row := range results[4].Rows {
		exitID, exitOK := rowString(row, "exit_id")
		countryCode, codeOK := rowString(row, "country_code")
		countryLabel, labelOK := rowString(row, "country_label")
		healthy, healthyOK := rowInt64(row, "healthy")
		if !exitOK || !codeOK || !labelOK || !healthyOK || (healthy != 0 && healthy != 1) {
			return whiteListSidecarRuntimeState{}, ErrUnavailable
		}
		state.exits[exitID] = WhiteListExit{
			ExitID: exitID, CountryCode: countryCode, CountryLabel: countryLabel, Healthy: healthy == 1,
		}
	}
	return state, nil
}

func whiteListRuntimeDesiredFromRow(row map[string]any) (WhiteListSidecarDesired, error) {
	originID, originOK := rowString(row, "origin_id")
	nodeID, nodeOK := rowString(row, "node_id")
	releaseID, releaseOK := rowString(row, "release_id")
	profileID, profileOK := rowString(row, "profile_id")
	presetID, presetOK := rowString(row, "preset_id")
	exitID, exitOK := rowString(row, "exit_id")
	configDigest, configOK := rowString(row, "config_digest")
	managedDigest, managedOK := rowString(row, "managed_user_set_digest")
	desiredSHA, desiredOK := rowString(row, "desired_sha256")
	actionType, typeOK := rowString(row, "action_type")
	actionKey, actionOK := rowString(row, "action_key")
	generation, generationOK := rowInt64(row, "desired_generation")
	payloadJSON, payloadOK := whiteListRowBytes(row, "payload_json")
	if !originOK || !nodeOK || !releaseOK || !profileOK || !presetOK || !exitOK || !configOK ||
		!managedOK || !desiredOK || !typeOK || !actionOK || !generationOK || !payloadOK {
		return WhiteListSidecarDesired{}, ErrUnavailable
	}
	var payload whiteListSidecarPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return WhiteListSidecarDesired{}, ErrUnavailable
	}
	desired := WhiteListSidecarDesired{
		OriginID: originID, NodeID: nodeID, ReleaseID: releaseID, ProfileID: profileID,
		PresetID: presetID, ExitID: exitID, Generation: generation, ConfigDigest: configDigest,
		ManagedUserSetDigest: managedDigest, DesiredSHA256: desiredSHA,
		StaticUsers: append([]string{}, payload.StaticUsers...), ManagedUsers: append([]string{}, payload.ManagedUsers...),
		PayloadJSON: append([]byte(nil), payloadJSON...),
		Action: ExternalActionCommand{
			Type: actionType, ResourceID: originID, ActionKey: actionKey, Request: append([]byte(nil), payloadJSON...),
		},
	}
	if err := validateWhiteListSidecarDesired(desired); err != nil {
		return WhiteListSidecarDesired{}, ErrUnavailable
	}
	return desired, nil
}

func whiteListPreviousManagedRoutes(previous map[string]WhiteListSidecarDesired) ([]WhiteListManagedRoute, string, error) {
	exitID := ""
	var canonical []string
	for _, desired := range previous {
		if exitID == "" {
			exitID = desired.ExitID
		} else if exitID != desired.ExitID {
			return nil, "", ErrConflict
		}
		users := append([]string{}, desired.ManagedUsers...)
		sort.Strings(users)
		if canonical == nil {
			canonical = users
		} else if !whiteListStringsEqual(canonical, users) {
			return nil, "", ErrConflict
		}
	}
	routes := make([]WhiteListManagedRoute, 0, len(canonical))
	for _, user := range canonical {
		entitlementID, routeExitID, ok := whiteListMeteringManagedRouteIdentity(user)
		if !ok {
			return nil, "", ErrConflict
		}
		routes = append(routes, WhiteListManagedRoute{EntitlementID: entitlementID, ExitID: routeExitID})
	}
	return routes, exitID, nil
}

func whiteListPreviousManagedState(previous map[string]WhiteListSidecarDesired) (map[string]struct{}, string, error) {
	routes, exitID, err := whiteListPreviousManagedRoutes(previous)
	if err != nil {
		return nil, "", err
	}
	managed := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		managed[route.EntitlementID] = struct{}{}
	}
	return managed, exitID, nil
}

func whiteListSelectedRuntimeExit(
	entitlementIDs []string,
	previousExit string,
	credentials map[string]map[string]struct{},
	exits map[string]WhiteListExit,
) (WhiteListExit, error) {
	required, ok := whiteListRequiredRuntimeExits(exits)
	if !ok {
		return WhiteListExit{}, ErrUnavailable
	}
	for _, entitlementID := range entitlementIDs {
		for _, exit := range required {
			if _, exists := credentials[entitlementID][exit.ExitID]; !exists {
				return WhiteListExit{}, ErrUnavailable
			}
		}
	}
	for _, exit := range required {
		if exit.ExitID == previousExit {
			return exit, nil
		}
	}
	return required[0], nil
}
