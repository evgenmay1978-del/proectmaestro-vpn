package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const whiteListSidecarApplyAction = "whitelist_sidecar_apply"

type WhiteListExit struct {
	ExitID       string
	CountryCode  string
	CountryLabel string
	Healthy      bool
}

type WhiteListOrigin struct {
	OriginID     string
	NodeID       string
	ReleaseID    string
	ProfileID    string
	PresetID     string
	ConfigDigest string
	Active       bool
	StaticUsers  []string
}

type WhiteListManagedRoute struct {
	EntitlementID string
	ExitID        string
}

type WhiteListRouteMatrixEntry struct {
	OriginID           string
	NodeID             string
	EntitlementID      string
	ExitID             string
	CountryCode        string
	PublicCountryLabel string
}

type WhiteListSidecarDesired struct {
	OriginID             string
	NodeID               string
	ReleaseID            string
	ProfileID            string
	PresetID             string
	ExitID               string
	Generation           int64
	ConfigDigest         string
	ManagedUserSetDigest string
	DesiredSHA256        string
	StaticUsers          []string
	ManagedUsers         []string
	PayloadJSON          []byte
	Action               ExternalActionCommand
}

type whiteListSidecarPayload struct {
	Version              int      `json:"version"`
	OriginID             string   `json:"origin_id"`
	NodeID               string   `json:"node_id"`
	ReleaseID            string   `json:"release_id"`
	ProfileID            string   `json:"profile_id"`
	PresetID             string   `json:"preset_id"`
	ExitID               string   `json:"exit_id"`
	Generation           int64    `json:"generation"`
	ConfigDigest         string   `json:"config_digest"`
	ManagedUserSetDigest string   `json:"managed_user_set_digest"`
	StaticUsers          []string `json:"static_users"`
	ManagedUsers         []string `json:"managed_users"`
}

func BuildWhiteListRouteMatrix(
	origins []WhiteListOrigin, routes []WhiteListManagedRoute, exit WhiteListExit,
) ([]WhiteListRouteMatrixEntry, error) {
	if exit.ExitID == "" || exit.CountryCode == "" || exit.CountryLabel == "" {
		return nil, errors.New("controlplane: invalid white-list exit")
	}
	orderedOrigins := append([]WhiteListOrigin(nil), origins...)
	sort.Slice(orderedOrigins, func(i, j int) bool { return orderedOrigins[i].OriginID < orderedOrigins[j].OriginID })
	orderedRoutes := append([]WhiteListManagedRoute(nil), routes...)
	sort.Slice(orderedRoutes, func(i, j int) bool {
		if orderedRoutes[i].EntitlementID == orderedRoutes[j].EntitlementID {
			return orderedRoutes[i].ExitID < orderedRoutes[j].ExitID
		}
		return orderedRoutes[i].EntitlementID < orderedRoutes[j].EntitlementID
	})
	result := make([]WhiteListRouteMatrixEntry, 0, len(orderedOrigins)*len(orderedRoutes))
	for _, origin := range orderedOrigins {
		if !origin.Active {
			continue
		}
		if origin.OriginID == "" || origin.NodeID == "" {
			return nil, errors.New("controlplane: invalid white-list origin")
		}
		for _, route := range orderedRoutes {
			if route.EntitlementID == "" || route.ExitID != exit.ExitID {
				return nil, errors.New("controlplane: invalid white-list managed route")
			}
			result = append(result, WhiteListRouteMatrixEntry{
				OriginID: origin.OriginID, NodeID: origin.NodeID, EntitlementID: route.EntitlementID,
				ExitID: exit.ExitID, CountryCode: exit.CountryCode, PublicCountryLabel: exit.CountryLabel,
			})
		}
	}
	return result, nil
}

func BuildWhiteListSidecarDesired(
	previous map[string]WhiteListSidecarDesired, origins []WhiteListOrigin,
	routes []WhiteListManagedRoute, exit WhiteListExit,
) ([]WhiteListSidecarDesired, error) {
	if _, err := BuildWhiteListRouteMatrix(origins, routes, exit); err != nil {
		return nil, err
	}
	managedUsers := make([]string, 0, len(routes))
	seenManaged := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		email := whiteListManagedEmail(route.EntitlementID, route.ExitID)
		if _, exists := seenManaged[email]; exists {
			continue
		}
		seenManaged[email] = struct{}{}
		managedUsers = append(managedUsers, email)
	}
	sort.Strings(managedUsers)
	managedDigest, err := whiteListCanonicalDigest(managedUsers)
	if err != nil {
		return nil, err
	}
	orderedOrigins := append([]WhiteListOrigin(nil), origins...)
	sort.Slice(orderedOrigins, func(i, j int) bool { return orderedOrigins[i].OriginID < orderedOrigins[j].OriginID })
	result := make([]WhiteListSidecarDesired, 0, len(orderedOrigins))
	for _, origin := range orderedOrigins {
		if !origin.Active {
			continue
		}
		if origin.ReleaseID == "" || origin.ProfileID == "" || origin.PresetID == "" ||
			!whiteListDigestValid(origin.ConfigDigest) {
			return nil, errors.New("controlplane: invalid white-list sidecar origin")
		}
		staticUsers := whiteListSortedUnique(origin.StaticUsers)
		generation := int64(1)
		if prior, ok := previous[origin.OriginID]; ok {
			if prior.Generation < 1 {
				return nil, errors.New("controlplane: invalid previous white-list desired generation")
			}
			generation = prior.Generation
			if !whiteListDesiredSemanticallyEqual(prior, origin, exit.ExitID, staticUsers, managedUsers, managedDigest) {
				generation++
			}
		}
		payload := whiteListSidecarPayload{
			Version: 1, OriginID: origin.OriginID, NodeID: origin.NodeID,
			ReleaseID: origin.ReleaseID, ProfileID: origin.ProfileID, PresetID: origin.PresetID,
			ExitID: exit.ExitID, Generation: generation, ConfigDigest: origin.ConfigDigest,
			ManagedUserSetDigest: managedDigest, StaticUsers: staticUsers,
			ManagedUsers: append([]string(nil), managedUsers...),
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return nil, errors.New("controlplane: encode white-list sidecar desired")
		}
		desiredDigest := sha256.Sum256(payloadJSON)
		desiredSHA := hex.EncodeToString(desiredDigest[:])
		actionKey := origin.NodeID + ":" + strconv.FormatInt(generation, 10) + ":" + desiredSHA
		result = append(result, WhiteListSidecarDesired{
			OriginID: origin.OriginID, NodeID: origin.NodeID, ReleaseID: origin.ReleaseID,
			ProfileID: origin.ProfileID, PresetID: origin.PresetID, ExitID: exit.ExitID,
			Generation: generation, ConfigDigest: origin.ConfigDigest,
			ManagedUserSetDigest: managedDigest, DesiredSHA256: desiredSHA,
			StaticUsers: staticUsers, ManagedUsers: append([]string(nil), managedUsers...),
			PayloadJSON: payloadJSON,
			Action: ExternalActionCommand{
				Type: whiteListSidecarApplyAction, ResourceID: origin.OriginID,
				ActionKey: actionKey, Request: append([]byte(nil), payloadJSON...),
			},
		})
	}
	return result, nil
}

func whiteListDesiredSemanticallyEqual(
	prior WhiteListSidecarDesired, origin WhiteListOrigin, exitID string,
	staticUsers, managedUsers []string, managedDigest string,
) bool {
	return prior.NodeID == origin.NodeID && prior.ReleaseID == origin.ReleaseID &&
		prior.ProfileID == origin.ProfileID && prior.PresetID == origin.PresetID &&
		prior.ExitID == exitID && prior.ConfigDigest == origin.ConfigDigest &&
		prior.ManagedUserSetDigest == managedDigest &&
		whiteListStringsEqual(prior.StaticUsers, staticUsers) &&
		whiteListStringsEqual(prior.ManagedUsers, managedUsers)
}

func whiteListCanonicalDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("controlplane: encode white-list canonical value")
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func whiteListSortedUnique(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if value == "" || (write > 0 && result[write-1] == value) {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func whiteListStringsEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func whiteListDigestValid(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// PersistWhiteListSidecarDesired first durably prepares the protected external
// action, then binds an immutable desired generation to that action.
func (s *Service) PersistWhiteListSidecarDesired(
	ctx context.Context, desired WhiteListSidecarDesired,
) (ExternalActionResult, error) {
	actions, err := NewRQLiteExternalActions(s)
	if err != nil {
		return ExternalActionResult{}, err
	}
	actionResult, err := actions.Prepare(ctx, desired.Action)
	if err != nil {
		return ExternalActionResult{}, err
	}
	statements, err := whiteListSidecarDesiredStatements(desired, s.clock.Now().Unix())
	if err != nil {
		return ExternalActionResult{}, err
	}
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	read := whiteListSidecarDesiredRead(desired)
	results, readErr := s.store.db.QueryLinearizable(ctx, read)
	if readErr != nil {
		return ExternalActionResult{}, ErrUnavailable
	}
	if !whiteListSidecarDesiredMatches(results, desired) {
		if requestErr == nil {
			return ExternalActionResult{}, ErrConflict
		}
		return ExternalActionResult{}, ErrUnavailable
	}
	return actionResult, nil
}

func whiteListSidecarDesiredStatements(desired WhiteListSidecarDesired, now int64) ([]rqlite.Statement, error) {
	if desired.OriginID == "" || desired.NodeID == "" || desired.ExitID == "" || desired.Generation < 1 ||
		!whiteListDigestValid(desired.ConfigDigest) || !whiteListDigestValid(desired.ManagedUserSetDigest) ||
		!whiteListDigestValid(desired.DesiredSHA256) || len(desired.PayloadJSON) == 0 ||
		desired.Action.Type != whiteListSidecarApplyAction || desired.Action.ResourceID != desired.OriginID ||
		desired.Action.ActionKey != desired.NodeID+":"+strconv.FormatInt(desired.Generation, 10)+":"+desired.DesiredSHA256 ||
		string(desired.Action.Request) != string(desired.PayloadJSON) {
		return nil, errors.New("controlplane: invalid white-list sidecar desired")
	}
	return []rqlite.Statement{
		{SQL: `SELECT action_id FROM external_actions WHERE action_type=? AND idempotency_key=?`, Args: []any{
			desired.Action.Type, desired.Action.ActionKey,
		}},
		{SQL: `INSERT OR IGNORE INTO whitelist_sidecar_desired(
origin_id,desired_generation,node_id,release_id,profile_id,preset_id,exit_id,config_digest,
managed_user_set_digest,desired_sha256,payload_json,action_type,action_key,created_at_unix)
SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,? FROM external_actions
WHERE action_type=? AND idempotency_key=?`, Args: []any{
			desired.OriginID, desired.Generation, desired.NodeID, desired.ReleaseID, desired.ProfileID,
			desired.PresetID, desired.ExitID, desired.ConfigDigest, desired.ManagedUserSetDigest,
			desired.DesiredSHA256, desired.PayloadJSON, desired.Action.Type, desired.Action.ActionKey, now,
			desired.Action.Type, desired.Action.ActionKey,
		}},
	}, nil
}

func whiteListSidecarDesiredRead(desired WhiteListSidecarDesired) rqlite.Statement {
	return rqlite.Statement{SQL: `SELECT origin_id,desired_generation,node_id,release_id,profile_id,preset_id,
exit_id,config_digest,managed_user_set_digest,desired_sha256,payload_json,action_type,action_key
FROM whitelist_sidecar_desired WHERE origin_id=? AND desired_generation=?`, Args: []any{
		desired.OriginID, desired.Generation,
	}}
}

func whiteListSidecarDesiredMatches(results []rqlite.Result, desired WhiteListSidecarDesired) bool {
	row, ok := firstRow(results)
	if !ok {
		return false
	}
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
	payload, payloadOK := whiteListRowBytes(row, "payload_json")
	return originOK && nodeOK && releaseOK && profileOK && presetOK && exitOK && configOK && managedOK &&
		desiredOK && typeOK && actionOK && generationOK && payloadOK &&
		originID == desired.OriginID && nodeID == desired.NodeID && releaseID == desired.ReleaseID &&
		profileID == desired.ProfileID && presetID == desired.PresetID && exitID == desired.ExitID &&
		configDigest == desired.ConfigDigest && managedDigest == desired.ManagedUserSetDigest &&
		desiredSHA == desired.DesiredSHA256 && actionType == desired.Action.Type &&
		actionKey == desired.Action.ActionKey && generation == desired.Generation &&
		string(payload) == string(desired.PayloadJSON)
}
