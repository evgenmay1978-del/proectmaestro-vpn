package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

const whiteListObservationTTLSeconds int64 = 5

// WhiteListAdmissionReserve is an explicit, verified measurement input, not a
// configured default or a rate inferred from missing counters. No production
// measurement provider is installed yet; its zero value denies admission.
type WhiteListAdmissionReserve struct {
	MeasuredP999BytesPerSecond uint64
	MeasuredAtUnix             int64
	ValidUntilUnix             int64
}

func (reserve WhiteListAdmissionReserve) RequiredBytes(nowUnix int64) (int64, error) {
	const maximum = uint64(9223372036854775806)
	if nowUnix <= 0 || reserve.MeasuredAtUnix <= 0 || reserve.MeasuredAtUnix > nowUnix ||
		reserve.ValidUntilUnix <= nowUnix || reserve.ValidUntilUnix > int64(maximum) ||
		reserve.MeasuredP999BytesPerSecond > maximum/5 {
		return 0, ErrUnavailable
	}
	bytes := reserve.MeasuredP999BytesPerSecond * 5
	if bytes < 10_000_000 {
		bytes = 10_000_000
	}
	return int64(bytes), nil
}

// WhiteListOriginObservation contains presence, never byte values. The trusted
// collector calls this only after authenticated mTLS LookupUsage, including a
// real reset=false StatsService query for an empty desired set.
type WhiteListOriginObservation struct {
	Receipt          WhiteListSidecarReceipt
	SampledAt        time.Time
	AvailableUsers   []string
	UnavailableUsers []string
}

func (s *Service) RecordWhiteListOriginObservation(ctx context.Context, observation WhiteListOriginObservation) error {
	if s == nil || s.store == nil || s.store.db == nil || s.clock == nil || ctx == nil {
		return ErrUnavailable
	}
	now := s.clock.Now()
	if observation.SampledAt.Unix() <= 0 || observation.SampledAt.After(now) ||
		now.Unix()-observation.SampledAt.Unix() >= whiteListObservationTTLSeconds {
		return ErrUnavailable
	}
	state, err := s.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		return err
	}
	desired, ok := state.previous[observation.Receipt.OriginID]
	if !ok || ValidateWhiteListSidecarReceipt(desired, observation.Receipt.XrayProcessBootID, observation.Receipt, now) != nil ||
		observation.SampledAt.Before(observation.Receipt.AppliedAt) {
		return ErrUnavailable
	}
	stored, err := s.store.db.QueryLinearizable(ctx, whiteListSidecarReceiptRead(observation.Receipt.ActionKey))
	if err != nil {
		return ErrUnavailable
	}
	receipt, err := whiteListSidecarReceiptFromResults(stored)
	if err != nil || !whiteListSidecarReceiptPersistedEqual(receipt, observation.Receipt) {
		return ErrUnavailable
	}
	if !whiteListObservationCoverage(desired.ManagedUsers, observation.AvailableUsers, observation.UnavailableUsers) {
		return ErrConflict
	}
	available, _ := json.Marshal(append([]string{}, observation.AvailableUsers...))
	unavailable, _ := json.Marshal(append([]string{}, observation.UnavailableUsers...))
	payload, _ := json.Marshal(struct {
		Action                 string
		Boot                   string
		Sampled                int64
		Available, Unavailable []string
	}{
		receipt.ActionKey, receipt.XrayProcessBootID, observation.SampledAt.Unix(), append([]string{}, observation.AvailableUsers...), append([]string{}, observation.UnavailableUsers...),
	})
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	statements := []rqlite.Statement{{SQL: `INSERT INTO whitelist_metering_origin_observations
(origin_id,action_key,sampled_at_unix,checked_at_unix,available_users_json,unavailable_users_json,observation_sha256)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(origin_id) DO UPDATE SET action_key=excluded.action_key,
sampled_at_unix=excluded.sampled_at_unix,checked_at_unix=excluded.checked_at_unix,
available_users_json=excluded.available_users_json,unavailable_users_json=excluded.unavailable_users_json,
observation_sha256=excluded.observation_sha256`, Args: []any{receipt.OriginID, receipt.ActionKey,
		observation.SampledAt.Unix(), now.Unix(), string(available), string(unavailable), hash}}}
	for _, email := range observation.AvailableUsers {
		entitlementID, valid := whiteListMeteringEntitlementID(email, desired.ExitID)
		if !valid {
			return ErrConflict
		}
		statements = append(statements, rqlite.Statement{SQL: `UPDATE whitelist_first_use_admissions SET first_observed_at_unix=?
WHERE entitlement_id=? AND exit_id=? AND origin_id=? AND xray_process_boot_id=? AND first_observed_at_unix=0`,
			Args: []any{observation.SampledAt.Unix(), entitlementID, desired.ExitID, receipt.OriginID, receipt.XrayProcessBootID}})
	}
	// Resolve an unknown transaction result with exact readback, as receipt writes
	// do. Health never calls the billing store or advances a balance watermark.
	_, _ = s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT observation_sha256
FROM whitelist_metering_origin_observations WHERE origin_id=?`, Args: []any{receipt.OriginID}})
	row, ok := firstRow(results)
	got, _ := rowString(row, "observation_sha256")
	if err != nil || !ok || got != hash {
		return ErrUnavailable
	}
	return nil
}

func whiteListObservationCoverage(managed, available, unavailable []string) bool {
	if !whiteListMeteringManagedUsersCanonical(managed) || !whiteListMeteringManagedUsersCanonical(available) ||
		!whiteListMeteringManagedUsersCanonical(unavailable) {
		return false
	}
	union := append(append([]string{}, available...), unavailable...)
	sort.Strings(union)
	return whiteListMeteringManagedUsersCanonical(union) && whiteListStringsEqual(managed, union)
}

type whiteListObservedOrigin struct {
	origin      WhiteListOrigin
	desired     WhiteListSidecarDesired
	receipt     WhiteListSidecarReceipt
	sampledAt   int64
	hash        string
	available   []string
	unavailable []string
}

// Loads every active origin. A latest aggregate balance timestamp is not an
// origin health proof. Rechecking current config/desired/receipt also prevents
// an old observation from surviving a rollout or a known process boot change.
func (s *Service) whiteListObservedOrigins(ctx context.Context) ([]whiteListObservedOrigin, error) {
	state, err := s.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		return nil, err
	}
	if len(state.origins) == 0 {
		return nil, ErrUnavailable
	}
	now := s.clock.Now()
	observed := make([]whiteListObservedOrigin, 0, len(state.origins))
	for _, origin := range state.origins {
		desired, ok := state.previous[origin.OriginID]
		if !ok || desired.NodeID != origin.NodeID || desired.ReleaseID != origin.ReleaseID ||
			desired.ProfileID != origin.ProfileID || desired.PresetID != origin.PresetID || desired.ConfigDigest != origin.ConfigDigest {
			return nil, ErrUnavailable
		}
		results, err := s.store.db.QueryLinearizable(ctx, whiteListSidecarReceiptRead(desired.Action.ActionKey),
			rqlite.Statement{SQL: `SELECT action_key,sampled_at_unix,observation_sha256,
CAST(available_users_json AS TEXT) AS available_users,CAST(unavailable_users_json AS TEXT) AS unavailable_users
FROM whitelist_metering_origin_observations WHERE origin_id=?`, Args: []any{origin.OriginID}})
		if err != nil || len(results) != 2 {
			return nil, ErrUnavailable
		}
		receipt, err := whiteListSidecarReceiptFromResults(results[:1])
		if err != nil || ValidateWhiteListSidecarReceipt(desired, receipt.XrayProcessBootID, receipt, now) != nil {
			return nil, ErrUnavailable
		}
		row, ok := firstRow(results[1:])
		action, _ := rowString(row, "action_key")
		sampled, sampledOK := rowInt64(row, "sampled_at_unix")
		hash, _ := rowString(row, "observation_sha256")
		available, _ := rowString(row, "available_users")
		unavailable, _ := rowString(row, "unavailable_users")
		item := whiteListObservedOrigin{origin: origin, desired: desired, receipt: receipt, sampledAt: sampled, hash: hash}
		if !ok || !sampledOK || action != receipt.ActionKey || sampled < receipt.AppliedAt.Unix() || sampled > now.Unix() ||
			now.Unix()-sampled >= whiteListObservationTTLSeconds || json.Unmarshal([]byte(available), &item.available) != nil ||
			json.Unmarshal([]byte(unavailable), &item.unavailable) != nil ||
			!whiteListObservationCoverage(desired.ManagedUsers, item.available, item.unavailable) {
			return nil, ErrUnavailable
		}
		observed = append(observed, item)
	}
	return observed, nil
}

func (s *Service) whiteListAdmissionBase(ctx context.Context, entitlementID, exitID string) (string, int64, error) {
	if s == nil || s.store == nil || s.clock == nil || ctx == nil || !validEntitlementID(entitlementID) || !validWhiteListID(exitID) {
		return "", 0, ErrUnavailable
	}
	state, err := s.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		return "", 0, err
	}
	publication, ok := state.publications[entitlementID]
	if !ok || !publication.Enabled || (publication.Source != WhiteListActivationConfirmedGBPurchase && publication.Source != WhiteListActivationAdminEnable) ||
		publication.PrimaryStatus != "active" || publication.PrimaryExpiresAtUnix <= s.clock.Now().Unix() {
		return "", 0, ErrUnavailable
	}
	if _, ok := state.credentials[entitlementID][exitID]; !ok || !state.exits[exitID].Healthy {
		return "", 0, ErrUnavailable
	}
	now := s.clock.Now().Unix()
	results, err := s.store.db.QueryLinearizable(ctx, whiteListMeteringPeriodRead(entitlementID, now))
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		return "", 0, ErrUnavailable
	}
	period, ok := rowString(results[0].Rows[0], "period_id")
	if !ok {
		return "", 0, ErrUnavailable
	}
	balance, err := s.WhiteListBalanceSnapshot(ctx, now, entitlementID)
	if err != nil || balance.Projection.Pending || balance.Projection.Version <= 0 || balance.AvailableBytes <= 0 {
		return "", 0, ErrUnavailable
	}
	return period, balance.AvailableBytes, nil
}

// AuthorizeWhiteListMeteringAdmission is a narrow internal entry point for a
// verified reserve provider. It does not publish a subscription. Historical
// desired/source rows and immutable admission bindings forbid restarting grace.
func (s *Service) AuthorizeWhiteListMeteringAdmission(ctx context.Context, entitlementID, exitID string, reserve WhiteListAdmissionReserve) error {
	if s == nil || s.clock == nil {
		return ErrUnavailable
	}
	now := s.clock.Now().Unix()
	required, err := reserve.RequiredBytes(now)
	if err != nil {
		return err
	}
	period, available, err := s.whiteListAdmissionBase(ctx, entitlementID, exitID)
	if err != nil || available < required {
		return ErrUnavailable
	}
	origins, err := s.whiteListObservedOrigins(ctx)
	if err != nil {
		return err
	}
	statements := make([]rqlite.Statement, 0, len(origins))
	for _, origin := range origins {
		if origin.desired.ExitID != exitID {
			return ErrUnavailable
		}
		statements = append(statements, rqlite.Statement{SQL: `INSERT INTO whitelist_first_use_admissions
(entitlement_id,exit_id,origin_id,xray_process_boot_id,admitted_action_key,billing_period_id,admitted_at_unix,
zero_start_authorized,first_observed_at_unix,reserve_bytes,reserve_measured_at_unix,reserve_until_unix)
SELECT ?,?,?,?,?,?,?,1,0,?,?,? WHERE EXISTS(SELECT 1 FROM whitelist_metering_origin_observations WHERE origin_id=? AND observation_sha256=?)
AND NOT EXISTS(SELECT 1 FROM whitelist_first_use_admissions
WHERE entitlement_id=? AND exit_id=? AND origin_id=? AND xray_process_boot_id=?)`, Args: []any{
			entitlementID, exitID, origin.origin.OriginID, origin.receipt.XrayProcessBootID, origin.receipt.ActionKey, period, now,
			required, reserve.MeasuredAtUnix, reserve.ValidUntilUnix, origin.origin.OriginID, origin.hash,
			entitlementID, exitID, origin.origin.OriginID, origin.receipt.XrayProcessBootID}})
		// Do not run INSERT triggers on an existing lifetime: only the reserve
		// lease changes. The immutable first-observed/period boundary remains.
		statements = append(statements, rqlite.Statement{SQL: `UPDATE whitelist_first_use_admissions
SET reserve_bytes=?,reserve_measured_at_unix=?,reserve_until_unix=?
WHERE entitlement_id=? AND exit_id=? AND origin_id=? AND xray_process_boot_id=? AND billing_period_id=?
AND EXISTS(SELECT 1 FROM whitelist_metering_origin_observations WHERE origin_id=? AND observation_sha256=?)`, Args: []any{
			required, reserve.MeasuredAtUnix, reserve.ValidUntilUnix, entitlementID, exitID, origin.origin.OriginID,
			origin.receipt.XrayProcessBootID, period, origin.origin.OriginID, origin.hash}})
	}
	_, _ = s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	for _, origin := range origins {
		row, err := s.whiteListAdmissionRow(ctx, entitlementID, exitID, origin)
		if err != nil {
			return err
		}
		gotPeriod, _ := rowString(row, "billing_period_id")
		gotReserve, _ := rowInt64(row, "reserve_bytes")
		measured, _ := rowInt64(row, "reserve_measured_at_unix")
		until, _ := rowInt64(row, "reserve_until_unix")
		if gotPeriod != period || gotReserve != required || measured != reserve.MeasuredAtUnix || until != reserve.ValidUntilUnix {
			return ErrUnavailable
		}
	}
	return nil
}

func (s *Service) whiteListAdmissionRow(ctx context.Context, entitlementID, exitID string, origin whiteListObservedOrigin) (map[string]any, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT billing_period_id,admitted_at_unix,zero_start_authorized,
first_observed_at_unix,reserve_bytes,reserve_measured_at_unix,reserve_until_unix FROM whitelist_first_use_admissions
WHERE entitlement_id=? AND exit_id=? AND origin_id=? AND xray_process_boot_id=?`, Args: []any{entitlementID, exitID, origin.origin.OriginID, origin.receipt.XrayProcessBootID}})
	row, ok := firstRow(results)
	if err != nil || !ok {
		return nil, ErrUnavailable
	}
	return row, nil
}

// allowAwaiting is used only for sidecar provisioning. Publication always needs
// every origin's real, fully debited cumulative history in this exact period.
func (s *Service) whiteListMeteringAdmissionReady(ctx context.Context, entitlementID, exitID string, allowAwaiting bool) (int64, bool) {
	period, available, err := s.whiteListAdmissionBase(ctx, entitlementID, exitID)
	if err != nil {
		return 0, false
	}
	origins, err := s.whiteListObservedOrigins(ctx)
	if err != nil {
		return 0, false
	}
	through := int64(0)
	now := s.clock.Now().Unix()
	email := whiteListManagedEmail(entitlementID, exitID)
	for _, origin := range origins {
		if origin.desired.ExitID != exitID {
			return 0, false
		}
		row, err := s.whiteListAdmissionRow(ctx, entitlementID, exitID, origin)
		if err != nil {
			return 0, false
		}
		boundPeriod, _ := rowString(row, "billing_period_id")
		reserve, _ := rowInt64(row, "reserve_bytes")
		until, _ := rowInt64(row, "reserve_until_unix")
		observed, _ := rowInt64(row, "first_observed_at_unix")
		zeroStart, _ := rowInt64(row, "zero_start_authorized")
		admitted, _ := rowInt64(row, "admitted_at_unix")
		if boundPeriod != period || reserve < 10_000_000 || available < reserve || until <= now || zeroStart != 1 {
			return 0, false
		}
		if observed == 0 && allowAwaiting {
			continue
		}
		if observed == 0 || !whiteListContainsUser(origin.available, email) {
			return 0, false
		}
		accounted, err := s.whiteListOriginAccountedThrough(ctx, entitlementID, exitID, period, admitted, origin)
		if err != nil || accounted < origin.sampledAt {
			return 0, false
		}
		if through == 0 || accounted < through {
			through = accounted
		}
	}
	return through, true
}

func whiteListContainsUser(users []string, email string) bool {
	index := sort.SearchStrings(users, email)
	return index < len(users) && users[index] == email
}

// The first cumulative interval must debit all bytes, not EPOCH_STARTED's
// discarded baseline. The current accounting adapter cannot produce this yet.
// Keeping the proof in existing immutable source/outbox/receipt rows avoids a
// second authoritative 'accounted' flag. Crossing periods remains closed until
// the accounting layer supplies an explicit boundary sample contract.
func (s *Service) whiteListOriginAccountedThrough(ctx context.Context, entitlementID, exitID, period string, admitted int64, origin whiteListObservedOrigin) (int64, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `WITH history AS (SELECT source.sampled_at_unix,source.billing_period_id,
source.counter_generation,source.sample_sequence,event.uplink_bytes,event.downlink_bytes,
interval.uplink_delta_bytes,interval.downlink_delta_bytes,receipt.status
FROM whitelist_commercial_metering_sources AS source
JOIN whitelist_meter_epochs AS epoch ON epoch.meter_epoch=source.meter_epoch
JOIN whitelist_metering_events AS event ON event.event_id=source.event_id
LEFT JOIN whitelist_metering_intervals AS interval ON interval.event_id=source.event_id
LEFT JOIN whitelist_commercial_debit_outbox AS outbox ON outbox.event_id=source.event_id
 AND outbox.entitlement_id=source.entitlement_id AND outbox.billing_period_id=source.billing_period_id
 AND outbox.meter_epoch=source.meter_epoch AND outbox.source_sha256=source.source_sha256
LEFT JOIN idempotency_requests AS receipt ON receipt.scope=? AND receipt.command_type=?
 AND receipt.idempotency_key=outbox.receipt_key AND receipt.request_hash=outbox.request_hash
 AND receipt.resource_id=outbox.entitlement_id AND receipt.status='applied'
WHERE source.entitlement_id=? AND source.exit_id=? AND source.origin_id=? AND epoch.xray_process_boot_id=?
 AND source.route_xray_identity=? AND epoch.origin_id=source.origin_id
 AND epoch.reset_sequence=0
 AND EXISTS(SELECT 1 FROM whitelist_billing_periods AS current_period WHERE current_period.period_id=?
 AND current_period.entitlement_id=source.entitlement_id AND current_period.starts_at_unix<=? AND ?<current_period.ends_at_unix)
)
SELECT MAX(sampled_at_unix) AS accounted_through,
MIN(CASE WHEN billing_period_id=? AND status='applied' AND sampled_at_unix>=?
 AND counter_generation='1' THEN 1 ELSE 0 END) AS history_valid,
SUM(CASE WHEN counter_generation='1' AND sample_sequence='1'
 AND uplink_bytes=uplink_delta_bytes AND downlink_bytes=downlink_delta_bytes THEN 1 ELSE 0 END) AS full_initial_intervals
FROM history`, Args: []any{
		whitelistmetering.CommercialDebitReceiptScope, whitelistmetering.CommercialDebitReceiptCommand,
		entitlementID, exitID, origin.origin.OriginID, origin.receipt.XrayProcessBootID,
		whiteListManagedEmail(entitlementID, exitID), period, s.clock.Now().Unix(), s.clock.Now().Unix(), period, admitted}})
	row, ok := firstRow(results)
	through, throughOK := rowInt64(row, "accounted_through")
	valid, _ := rowInt64(row, "history_valid")
	initial, _ := rowInt64(row, "full_initial_intervals")
	if err != nil || !ok || !throughOK || through <= 0 || valid != 1 || initial != 1 {
		return 0, ErrUnavailable
	}
	return through, nil
}

// EnsureWhiteListMeteringBootstrap is invoked only by the explicitly enabled
// collector. It installs no managed users, merely a current empty desired set
// that can produce an authenticated StatsService health observation.
func (s *Service) EnsureWhiteListMeteringBootstrap(ctx context.Context, workerID string, resolve func(string) (ExternalActionSender, bool)) error {
	if s == nil || s.store == nil || s.clock == nil || ctx == nil || workerID == "" || resolve == nil {
		return ErrUnavailable
	}
	state, err := s.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		return err
	}
	if len(state.previous) != 0 || len(state.origins) == 0 {
		return nil
	}
	selected := ""
	for entitlementID := range state.publications {
		for exitID := range state.credentials[entitlementID] {
			if _, _, err := s.whiteListAdmissionBase(ctx, entitlementID, exitID); err != nil {
				continue
			}
			if selected != "" && selected != exitID {
				return ErrUnavailable
			}
			selected = exitID
		}
	}
	if selected == "" {
		return nil
	}
	_, err = s.ReconcileWhiteListSidecarGeneration(ctx, state.previous, state.origins, nil, state.exits[selected], workerID, resolve)
	return err
}
