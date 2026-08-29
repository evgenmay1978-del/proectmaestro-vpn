package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

// Clock keeps durable time decisions deterministic and testable.
type Clock interface {
	Now() time.Time
}

// Store is the only Task 5 boundary that talks to rqlite.
type Store struct {
	db      rqlite.RQLite
	secrets *SecretBox
	clock   Clock
}

func NewStore(db rqlite.RQLite, secrets *SecretBox, clock Clock) (*Store, error) {
	if db == nil || secrets == nil || clock == nil {
		return nil, errors.New("controlplane: incomplete store configuration")
	}
	return &Store{db: db, secrets: secrets, clock: clock}, nil
}

func (s *Store) customerByLookup(ctx context.Context, column, lookup string) (Customer, error) {
	if lookup == "" {
		return Customer{}, ErrNotFound
	}
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT c.customer_id, c.status, c.expires_at_unix, c.generation
FROM customers c
JOIN subscription_tokens st ON st.customer_id = c.customer_id
WHERE ` + column + ` = ? AND st.revoked = 0
LIMIT 1`,
		Args: []any{lookup},
	})
	if err != nil {
		return Customer{}, errors.New("controlplane: customer lookup unavailable")
	}
	row, ok := firstRow(results)
	if !ok {
		return Customer{}, ErrNotFound
	}
	customer := Customer{}
	if customer.ID, ok = rowString(row, "customer_id"); !ok {
		return Customer{}, errors.New("controlplane: invalid customer row")
	}
	customer.Status, _ = rowString(row, "status")
	customer.ExpiresAtUnix, ok = rowInt64(row, "expires_at_unix")
	if !ok {
		return Customer{}, errors.New("controlplane: invalid customer row")
	}
	generation, ok := rowInt64(row, "generation")
	if !ok {
		return Customer{}, errors.New("controlplane: invalid customer row")
	}
	customer.Generation = generation
	return customer, nil
}

func (s *Store) tariffs(ctx context.Context) ([]Tariff, error) {
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT tariff_version_id, tariff_code, duration_days, amount_minor, currency
FROM tariff_versions WHERE active = 1 ORDER BY duration_days, tariff_version_id`})
	if err != nil {
		return nil, errors.New("controlplane: tariff catalog unavailable")
	}
	if len(results) != 1 {
		return nil, errors.New("controlplane: invalid tariff response")
	}
	tariffs := make([]Tariff, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		id, okID := rowString(row, "tariff_version_id")
		code, okCode := rowString(row, "tariff_code")
		days, okDays := rowInt64(row, "duration_days")
		amount, okAmount := rowInt64(row, "amount_minor")
		currency, okCurrency := rowString(row, "currency")
		if !okID || !okCode || !okDays || !okAmount || !okCurrency || days <= 0 || amount <= 0 || currency != "RUB" {
			return nil, errors.New("controlplane: invalid tariff row")
		}
		tariffs = append(tariffs, Tariff{
			VersionID: id, Code: code, DurationDays: int(days), AmountMinor: amount, Currency: currency,
		})
	}
	return tariffs, nil
}

func validSettingUpdate(update SettingUpdate) bool {
	if strings.TrimSpace(update.Key) == "" || update.ExpectedGeneration < 0 || !json.Valid([]byte(update.PublicValueJSON)) {
		return false
	}
	if update.Key != "olcrtc" {
		return len(update.TargetPayloads) == 0
	}
	targets := make(map[string]struct{}, len(update.TargetMembers))
	for _, login := range update.TargetMembers {
		canonical, err := CanonicalLoginKey(login)
		if err != nil || login != canonical {
			return false
		}
		if _, duplicate := targets[canonical]; duplicate {
			return false
		}
		targets[canonical] = struct{}{}
	}
	if len(update.TargetPayloads) != len(targets) {
		return false
	}
	for login, payload := range update.TargetPayloads {
		canonical, err := CanonicalLoginKey(login)
		if err != nil || login != canonical || !json.Valid([]byte(payload)) {
			return false
		}
		if _, targeted := targets[canonical]; !targeted {
			return false
		}
	}
	return true
}

func (s *Store) updateSetting(ctx context.Context, update SettingUpdate, mutationToken string) (SettingResult, error) {
	if !validSettingUpdate(update) || mutationToken == "" {
		return SettingResult{}, errors.New("controlplane: invalid setting update")
	}
	now := s.clock.Now().Unix()
	next := update.ExpectedGeneration + 1
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO cluster_settings(setting_key, public_value_json, generation, updated_at_unix, last_mutation_token)
SELECT ?, ?, ?, ?, ?
WHERE COALESCE((SELECT generation FROM cluster_settings WHERE setting_key = ?), 0) = ?
ON CONFLICT(setting_key) DO UPDATE SET public_value_json = excluded.public_value_json,
generation = excluded.generation, updated_at_unix = excluded.updated_at_unix,
last_mutation_token = excluded.last_mutation_token
WHERE cluster_settings.generation = ?
RETURNING generation`,
		Args: []any{update.Key, update.PublicValueJSON, next, now, mutationToken, update.Key, update.ExpectedGeneration, update.ExpectedGeneration},
	}, backupRPOSettingDirtyGenerationStatement(now, update.Key, next, mutationToken), {
		SQL: `DELETE FROM setting_members WHERE setting_key = ? AND EXISTS
(SELECT 1 FROM cluster_settings WHERE setting_key = ? AND generation = ? AND last_mutation_token = ?)`,
		Args: []any{update.Key, update.Key, next, mutationToken},
	}}
	if len(update.Members) > 0 {
		values := make([]string, 0, len(update.Members))
		args := make([]any, 0, len(update.Members)*7)
		for _, member := range update.Members {
			canonical, err := CanonicalLoginKey(member)
			if err != nil {
				return SettingResult{}, errors.New("controlplane: invalid setting member")
			}
			memberHMAC := s.secrets.LookupHMAC("setting-member:"+update.Key, []byte(canonical))
			values = append(values, `SELECT ?, ?, ?, ? WHERE EXISTS
(SELECT 1 FROM cluster_settings WHERE setting_key = ? AND generation = ? AND last_mutation_token = ?)`)
			args = append(args, update.Key, memberHMAC, `{"enabled":true}`, next, update.Key, next, mutationToken)
		}
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO setting_members(setting_key, member_key, member_value_json, generation) ` +
				strings.Join(values, " UNION ALL ") + ` ON CONFLICT(setting_key, member_key) DO UPDATE SET
member_value_json = excluded.member_value_json, generation = excluded.generation`,
			Args: args,
		})
	} else {
		statements = append(statements, rqlite.Statement{
			SQL:  `SELECT 1 FROM cluster_settings WHERE setting_key = ? AND generation = ? AND last_mutation_token = ?`,
			Args: []any{update.Key, next, mutationToken},
		})
	}
	if update.Secret != nil {
		envelopeBytes, err := json.Marshal(update.Secret)
		if err != nil {
			return SettingResult{}, errors.New("controlplane: encode protected envelope")
		}
		digest := sha256.Sum256(envelopeBytes)
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO setting_secrets(setting_key, secret_envelope, secret_sha256, key_version, updated_at_unix)
SELECT ?, ?, ?, ?, ? WHERE EXISTS
(SELECT 1 FROM cluster_settings WHERE setting_key = ? AND generation = ? AND last_mutation_token = ?)
ON CONFLICT(setting_key) DO UPDATE SET secret_envelope = excluded.secret_envelope,
secret_sha256 = excluded.secret_sha256, key_version = excluded.key_version, updated_at_unix = excluded.updated_at_unix`,
			Args: []any{update.Key, envelopeBytes, hex.EncodeToString(digest[:]), update.Secret.KeyVersion, now, update.Key, next, mutationToken},
		})
	} else {
		statements = append(statements, rqlite.Statement{
			SQL: `DELETE FROM setting_secrets WHERE setting_key = ? AND EXISTS
(SELECT 1 FROM cluster_settings WHERE setting_key = ? AND generation = ? AND last_mutation_token = ?)`,
			Args: []any{update.Key, update.Key, next, mutationToken},
		})
	}
	actorHMAC := s.secrets.LookupHMAC("audit-actor", []byte(update.Actor))
	resourceHMAC := s.secrets.LookupHMAC("audit-resource", []byte(update.Key))
	statements = append(statements, rqlite.Statement{
		SQL: `INSERT INTO audit_events(event_id, actor_hmac, action, resource_type, resource_id_hmac, created_at_unix)
SELECT ?, ?, 'setting.update', 'cluster_setting', ?, ? WHERE EXISTS
(SELECT 1 FROM cluster_settings WHERE setting_key = ? AND generation = ? AND last_mutation_token = ?)
ON CONFLICT(event_id) DO NOTHING`,
		Args: []any{auditID("setting", update.Key, next, now), actorHMAC, resourceHMAC, now, update.Key, next, mutationToken},
	})
	results, err := s.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil {
		return SettingResult{}, errors.New("controlplane: setting update unavailable")
	}
	if len(results) == 0 {
		return SettingResult{}, ErrConflict
	}
	row, ok := firstRow(results[:1])
	if !ok {
		return SettingResult{}, ErrConflict
	}
	generation, ok := rowInt64(row, "generation")
	if !ok || generation != next {
		return SettingResult{}, ErrConflict
	}
	return SettingResult{Generation: generation}, nil
}

func firstRow(results []rqlite.Result) (map[string]any, bool) {
	if len(results) == 0 || len(results[0].Rows) == 0 {
		return nil, false
	}
	return results[0].Rows[0], true
}

func rowString(row map[string]any, key string) (string, bool) {
	value, ok := row[key]
	if !ok {
		return "", false
	}
	result, ok := value.(string)
	return result, ok && result != ""
}

func rowInt64(row map[string]any, key string) (int64, bool) {
	value, ok := row[key]
	if !ok {
		return 0, false
	}
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int32:
		return int64(number), true
	case int64:
		return number, true
	case float64:
		if number != float64(int64(number)) {
			return 0, false
		}
		return int64(number), true
	case json.Number:
		parsed, err := number.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(number, 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func auditID(kind, key string, generation, now int64) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d", kind, key, generation, now)))
	return "audit_" + hex.EncodeToString(digest[:16])
}
