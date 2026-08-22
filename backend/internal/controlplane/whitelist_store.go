package controlplane

import (
	"context"
	"errors"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const entitlementIDGenerationAttempts = 3

// EnsureWhiteListEntitlement atomically creates or loads the one durable
// white-list billing identity bound to an existing account.
func (s *Service) EnsureWhiteListEntitlement(ctx context.Context, accountID string) (WhiteListEntitlement, error) {
	if s == nil || s.store == nil || s.ids == nil || !validAccountID(accountID) {
		return WhiteListEntitlement{}, errors.New("controlplane: invalid white-list entitlement request")
	}
	for attempt := 0; attempt < entitlementIDGenerationAttempts; attempt++ {
		rawID, err := s.ids.NewID("wl-ent")
		if err != nil {
			return WhiteListEntitlement{}, errors.New("controlplane: generate white-list entitlement identity")
		}
		candidateID, err := whiteListEntitlementIDFromSource(rawID)
		if err != nil {
			return WhiteListEntitlement{}, err
		}
		entitlement, collision, err := s.store.ensureWhiteListEntitlement(ctx, accountID, candidateID)
		if err == nil {
			if collision {
				continue
			}
			return entitlement, nil
		}
		if !unknownWhiteListEntitlementOutcome(err) {
			return WhiteListEntitlement{}, err
		}
		persisted, readErr := s.store.whiteListEntitlementByAccount(ctx, accountID)
		if readErr == nil {
			return persisted, nil
		}
		return WhiteListEntitlement{}, errors.New("controlplane: white-list entitlement outcome is unresolved")
	}
	return WhiteListEntitlement{}, errors.New("controlplane: white-list entitlement identity collision")
}

// WhiteListEntitlementByID resolves the account exclusively from the durable
// binding. Callers cannot provide or replace the account side of the mapping.
func (s *Service) WhiteListEntitlementByID(ctx context.Context, entitlementID string) (WhiteListEntitlement, error) {
	if s == nil || s.store == nil || !validEntitlementID(entitlementID) {
		return WhiteListEntitlement{}, ErrNotFound
	}
	return s.store.whiteListEntitlementByID(ctx, entitlementID)
}

func (s *Store) ensureWhiteListEntitlement(
	ctx context.Context,
	accountID string,
	candidateID string,
) (WhiteListEntitlement, bool, error) {
	results, err := s.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_entitlement_identities(entitlement_id, customer_id, created_at_unix)
SELECT ?, customer_id, ? FROM customers WHERE customer_id = ?
ON CONFLICT DO NOTHING`,
			Args: []any{candidateID, s.clock.Now().Unix(), accountID},
		},
		rqlite.Statement{
			SQL: `SELECT c.customer_id, wei.entitlement_id
FROM customers c
LEFT JOIN whitelist_entitlement_identities wei ON wei.customer_id = c.customer_id
WHERE c.customer_id = ?`,
			Args: []any{accountID},
		},
	)
	if err != nil {
		return WhiteListEntitlement{}, false, err
	}
	if len(results) != 2 {
		return WhiteListEntitlement{}, false, errors.New("controlplane: invalid white-list entitlement response")
	}
	rows := results[1].Rows
	if len(rows) == 0 {
		return WhiteListEntitlement{}, false, ErrNotFound
	}
	if len(rows) != 1 {
		return WhiteListEntitlement{}, false, errors.New("controlplane: invalid white-list entitlement row count")
	}
	row := rows[0]
	persistedAccountID, ok := rowString(row, "customer_id")
	if !ok || persistedAccountID != accountID {
		return WhiteListEntitlement{}, false, errors.New("controlplane: invalid white-list entitlement account")
	}
	rawEntitlementID, exists := row["entitlement_id"]
	if !exists {
		return WhiteListEntitlement{}, false, errors.New("controlplane: incomplete white-list entitlement row")
	}
	if rawEntitlementID == nil {
		return WhiteListEntitlement{}, true, nil
	}
	persistedEntitlementID, ok := rawEntitlementID.(string)
	if !ok {
		return WhiteListEntitlement{}, false, errors.New("controlplane: invalid white-list entitlement identity")
	}
	entitlement, err := whiteListEntitlementFromPersistedIdentity(persistedAccountID, persistedEntitlementID)
	return entitlement, false, err
}

func (s *Store) whiteListEntitlementByID(ctx context.Context, entitlementID string) (WhiteListEntitlement, error) {
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL:  `SELECT customer_id, entitlement_id FROM whitelist_entitlement_identities WHERE entitlement_id = ?`,
		Args: []any{entitlementID},
	})
	if err != nil {
		return WhiteListEntitlement{}, errors.New("controlplane: white-list entitlement lookup unavailable")
	}
	return exactWhiteListEntitlement(results)
}

func (s *Store) whiteListEntitlementByAccount(ctx context.Context, accountID string) (WhiteListEntitlement, error) {
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL:  `SELECT customer_id, entitlement_id FROM whitelist_entitlement_identities WHERE customer_id = ?`,
		Args: []any{accountID},
	})
	if err != nil {
		return WhiteListEntitlement{}, errors.New("controlplane: white-list entitlement lookup unavailable")
	}
	return exactWhiteListEntitlement(results)
}

func exactWhiteListEntitlement(results []rqlite.Result) (WhiteListEntitlement, error) {
	if len(results) != 1 || len(results[0].Rows) == 0 {
		return WhiteListEntitlement{}, ErrNotFound
	}
	if len(results[0].Rows) != 1 {
		return WhiteListEntitlement{}, errors.New("controlplane: invalid white-list entitlement row count")
	}
	row := results[0].Rows[0]
	accountID, accountOK := rowString(row, "customer_id")
	entitlementID, entitlementOK := rowString(row, "entitlement_id")
	if !accountOK || !entitlementOK {
		return WhiteListEntitlement{}, errors.New("controlplane: invalid white-list entitlement row")
	}
	return whiteListEntitlementFromPersistedIdentity(accountID, entitlementID)
}

func whiteListEntitlementIDFromSource(rawID string) (string, error) {
	const sourcePrefix = "wl-ent_"
	if !strings.HasPrefix(rawID, sourcePrefix) {
		return "", errors.New("controlplane: invalid generated white-list entitlement identity")
	}
	entitlementID := entitlementIDPrefix + strings.TrimPrefix(rawID, sourcePrefix)
	if !validEntitlementID(entitlementID) {
		return "", errors.New("controlplane: invalid generated white-list entitlement identity")
	}
	return entitlementID, nil
}

func unknownWhiteListEntitlementOutcome(err error) bool {
	var transportErr *rqlite.TransportError
	return errors.As(err, &transportErr) && transportErr.UnknownOutcome
}
