package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

// LegacyPrimaryCustomer contains only the primary authorization read from the
// protected legacy registry. Ordinary credentials and commercial state are not
// imported by this adapter.
type LegacyPrimaryCustomer struct {
	Login         string
	Token         string
	ExpiresAtUnix int64
	Disabled      bool
}

type LegacyPrimarySnapshot struct {
	Customers []LegacyPrimaryCustomer
	// Current rejects a registry replaced while a database update was prepared.
	Current func() bool
}

type LegacyPrimarySource interface {
	ReadLegacyPrimaries(context.Context) (LegacyPrimarySnapshot, error)
}

type legacyPrimaryMirror struct {
	source LegacyPrimarySource
	mu     sync.Mutex
}

// SetLegacyPrimarySource is called once, before the service starts serving.
// A nil source leaves the existing native writer behavior unchanged.
func (s *Service) SetLegacyPrimarySource(source LegacyPrimarySource) {
	if source != nil {
		s.legacyPrimary = &legacyPrimaryMirror{source: source}
	}
}

type legacyPrimaryRow struct {
	Customer
	login, lookup, source, digest, lifecycle string
	tokenID, tokenHMAC, tokenDigest          string
	tokenGeneration, revoked                 int64
}

const legacyPrimaryRowsSQL = `SELECT c.customer_id,c.display_login,c.login_key_hmac,c.status,c.expires_at_unix,c.generation,
e.source_key,e.canonical_sha256,e.lifecycle,
t.token_id,t.token_hmac,t.token_sha256,t.generation AS token_generation,t.revoked
FROM customers c
JOIN imported_entity_state e ON e.entity_kind='customer' AND e.target_id=c.customer_id
LEFT JOIN subscription_tokens t ON t.customer_id=c.customer_id
WHERE e.source_key GLOB ?`

func (s *Service) legacyPrimaryRows(ctx context.Context) ([]legacyPrimaryRow, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: legacyPrimaryRowsSQL, Args: []any{LegacyExactCustomerSourcePrefix + "*"},
	})
	if err != nil || len(results) != 1 {
		return nil, ErrUnavailable
	}
	rows := make([]legacyPrimaryRow, 0, len(results[0].Rows))
	seen := make(map[string]bool)
	for _, values := range results[0].Rows {
		var row legacyPrimaryRow
		textFields := []struct {
			key string
			out *string
		}{
			{"customer_id", &row.ID}, {"display_login", &row.login}, {"login_key_hmac", &row.lookup},
			{"status", &row.Status}, {"source_key", &row.source}, {"canonical_sha256", &row.digest},
			{"lifecycle", &row.lifecycle}, {"token_id", &row.tokenID}, {"token_hmac", &row.tokenHMAC},
			{"token_sha256", &row.tokenDigest},
		}
		for _, item := range textFields {
			value, ok := rowString(values, item.key)
			if !ok || value == "" {
				return nil, ErrUnavailable
			}
			*item.out = value
		}
		integers := []struct {
			key string
			out *int64
		}{
			{"expires_at_unix", &row.ExpiresAtUnix}, {"generation", &row.Generation},
			{"token_generation", &row.tokenGeneration}, {"revoked", &row.revoked},
		}
		for _, item := range integers {
			value, ok := rowInt64(values, item.key)
			if !ok || value < 0 {
				return nil, ErrUnavailable
			}
			*item.out = value
		}
		source, lookup, id, err := s.legacyPrimaryIdentity(row.login)
		if err != nil || source != row.source || lookup != row.lookup || id != row.ID || seen[row.ID] ||
			!customerIdentityDigest(row.digest) || !customerIdentityDigest(row.tokenHMAC) ||
			!customerIdentityDigest(row.tokenDigest) || row.revoked > 1 ||
			(row.lifecycle != "active" && row.lifecycle != "deleted") {
			return nil, ErrUnavailable
		}
		seen[row.ID] = true
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *Service) legacyPrimaryIdentity(login string) (source, lookup, id string, err error) {
	canonical, err := CanonicalLoginKey(login)
	if err != nil {
		return "", "", "", ErrUnavailable
	}
	canonicalHMAC := s.store.secrets.LookupHMAC("customer-login", []byte(canonical))
	lookup = s.store.secrets.LookupHMAC(LegacyExactCustomerLoginHMACDomain, []byte(login))
	source = LegacyExactCustomerSourcePrefix + canonicalHMAC + ":" + lookup
	digest := sha256.Sum256([]byte("maestro-legacy-v1\x00customer\x00" + source))
	return source, lookup, hex.EncodeToString(digest[:]), nil
}

func (s *Service) readLegacyPrimary(ctx context.Context) (LegacyPrimarySnapshot, map[string]LegacyPrimaryCustomer, error) {
	snapshot, err := s.legacyPrimary.source.ReadLegacyPrimaries(ctx)
	if err != nil || snapshot.Current == nil || !snapshot.Current() {
		return LegacyPrimarySnapshot{}, nil, ErrUnavailable
	}
	byLogin := make(map[string]LegacyPrimaryCustomer, len(snapshot.Customers))
	tokens := make(map[string]bool, len(snapshot.Customers))
	for _, customer := range snapshot.Customers {
		if _, err := CanonicalLoginKey(customer.Login); err != nil || customer.Token == "" ||
			customer.ExpiresAtUnix < 0 || tokens[customer.Token] {
			return LegacyPrimarySnapshot{}, nil, ErrUnavailable
		}
		if _, duplicate := byLogin[customer.Login]; duplicate {
			return LegacyPrimarySnapshot{}, nil, ErrUnavailable
		}
		byLogin[customer.Login], tokens[customer.Token] = customer, true
	}
	return snapshot, byLogin, nil
}

// refreshLegacyPrimary resolves a single exact legacy login or token. Missing
// records close their existing native identity; they never fall back to a stale
// import or create a customer under a normalized/case-insensitive login.
func (s *Service) refreshLegacyPrimary(ctx context.Context, login, token string) error {
	if s.legacyPrimary == nil {
		return nil
	}
	s.legacyPrimary.mu.Lock()
	defer s.legacyPrimary.mu.Unlock()
	snapshot, byLogin, err := s.readLegacyPrimary(ctx)
	if err != nil {
		return err
	}
	customer, found := byLogin[login]
	if token != "" {
		for _, candidate := range byLogin {
			if candidate.Token == token {
				customer, found = candidate, true
				break
			}
		}
	}
	rows, err := s.legacyPrimaryRows(ctx)
	if err != nil {
		return err
	}
	tokenHMAC := s.store.secrets.LookupHMAC("subscription-token", []byte(token))
	for _, row := range rows {
		if (found && row.login == customer.Login) || (!found && (row.login == login || (token != "" && row.tokenHMAC == tokenHMAC))) {
			if err := s.updateLegacyPrimary(ctx, snapshot, row, customer, found); err != nil {
				return err
			}
			if !found {
				return ErrNotFound
			}
			return nil
		}
	}
	if !found {
		return ErrNotFound
	}
	return s.createLegacyPrimary(ctx, snapshot, customer)
}

// refreshLegacyPrimaryRuntime only refreshes identities already admitted to the
// native database. New legacy accounts are created on their first lookup.
func (s *Service) refreshLegacyPrimaryRuntime(ctx context.Context) error {
	if s.legacyPrimary == nil {
		return nil
	}
	s.legacyPrimary.mu.Lock()
	defer s.legacyPrimary.mu.Unlock()
	snapshot, byLogin, err := s.readLegacyPrimary(ctx)
	if err != nil {
		return err
	}
	rows, err := s.legacyPrimaryRows(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		customer, found := byLogin[row.login]
		if err := s.updateLegacyPrimary(ctx, snapshot, row, customer, found); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	if !snapshot.Current() {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) updateLegacyPrimary(ctx context.Context, snapshot LegacyPrimarySnapshot, row legacyPrimaryRow, customer LegacyPrimaryCustomer, found bool) error {
	status, expires, revoked := "deleted", row.ExpiresAtUnix, int64(1)
	identityChanged := false
	if found {
		digest := sha256.Sum256([]byte(customer.Token))
		identityChanged = row.tokenHMAC != s.store.secrets.LookupHMAC("subscription-token", []byte(customer.Token)) ||
			row.tokenDigest != hex.EncodeToString(digest[:]) || row.lifecycle != "active"
		if !identityChanged {
			status, expires, revoked = "active", customer.ExpiresAtUnix, 0
			if customer.Disabled {
				status = "suspended"
			}
		}
	}
	if row.Status != status || row.ExpiresAtUnix != expires || row.revoked != revoked {
		if !snapshot.Current() {
			return ErrUnavailable
		}
		now := s.clock.Now().Unix()
		statements := []rqlite.Statement{{
			SQL: `UPDATE customers SET status=?,expires_at_unix=?,generation=generation+1,updated_at_unix=max(updated_at_unix,?)
WHERE customer_id=? AND display_login=? AND login_key_hmac=? AND generation=? AND status=? AND expires_at_unix=?
AND EXISTS(SELECT 1 FROM imported_entity_state e WHERE e.entity_kind='customer' AND e.target_id=customers.customer_id AND e.source_key=? AND e.canonical_sha256=? AND e.lifecycle=?)
AND (SELECT COUNT(*) FROM subscription_tokens t WHERE t.customer_id=customers.customer_id)=1
AND EXISTS(SELECT 1 FROM subscription_tokens t WHERE t.customer_id=customers.customer_id AND t.token_id=? AND t.token_hmac=? AND t.token_sha256=? AND t.generation=? AND t.revoked=?)`,
			Args: []any{status, expires, now, row.ID, row.login, row.lookup, row.Generation, row.Status, row.ExpiresAtUnix,
				row.source, row.digest, row.lifecycle, row.tokenID, row.tokenHMAC, row.tokenDigest, row.tokenGeneration, row.revoked},
		}, {
			SQL: `UPDATE subscription_tokens SET revoked=?,revoked_at_unix=CASE WHEN ?=0 THEN NULL ELSE max(created_at_unix,?) END
WHERE token_id=? AND customer_id=? AND changes()=1`,
			Args: []any{revoked, revoked, now, row.tokenID, row.ID},
		}, backupRPODirtyGenerationStatement(now)}
		results, err := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
		if err != nil || len(results) != len(statements) || results[0].RowsAffected != 1 || results[1].RowsAffected != 1 {
			return ErrUnavailable
		}
	}
	if !snapshot.Current() {
		return ErrUnavailable
	}
	if identityChanged {
		return ErrConflict
	}
	return nil
}

func (s *Service) createLegacyPrimary(ctx context.Context, snapshot LegacyPrimarySnapshot, customer LegacyPrimaryCustomer) error {
	source, lookup, id, err := s.legacyPrimaryIdentity(customer.Login)
	if err != nil {
		return err
	}
	canonicalHMAC, _, _ := ParseLegacyExactCustomerSource(source)
	tokenIDHash := sha256.Sum256([]byte("subscription-token\x00" + id))
	tokenID := hex.EncodeToString(tokenIDHash[:])
	token, err := s.sealCustomerSecret(id, tokenID, "token", "subscription", customer.Token)
	if err != nil {
		return ErrUnavailable
	}
	tokenHMAC := s.store.secrets.LookupHMAC("subscription-token", []byte(customer.Token))
	// This binding authenticates the local identity, without claiming a migration
	// batch or replacing the original proof of an already imported customer.
	proof, _ := json.Marshal([]string{"legacy-primary-file-v1", source, tokenHMAC, token.Digest})
	digest := sha256.Sum256(proof)
	status := "active"
	if customer.Disabled {
		status = "suspended"
	}
	now := s.clock.Now().Unix()
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix)
SELECT ?,?,?,?,?,1,?,? WHERE NOT EXISTS(SELECT 1 FROM customers WHERE customer_id=? OR login_key_hmac IN (?,?))
AND NOT EXISTS(SELECT 1 FROM imported_entity_state WHERE entity_kind='customer' AND (source_key=? OR target_id=?))
AND NOT EXISTS(SELECT 1 FROM subscription_tokens WHERE token_hmac=?)`,
		Args: []any{id, customer.Login, lookup, status, customer.ExpiresAtUnix, now, now, id, canonicalHMAC, lookup, source, id, tokenHMAC},
	}, {
		SQL: `INSERT INTO subscription_tokens(token_id,customer_id,token_hmac,token_envelope,token_sha256,generation,revoked,created_at_unix)
SELECT ?,?,?,?,?,1,0,? WHERE changes()=1`,
		Args: []any{tokenID, id, tokenHMAC, token.Envelope, token.Digest, now},
	}, {
		SQL: `INSERT INTO imported_entity_state(entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix)
SELECT 'customer',?,?,?,'active',? WHERE changes()=1`,
		Args: []any{source, id, hex.EncodeToString(digest[:]), now},
	}, backupRPODirtyGenerationStatement(now)}
	if !snapshot.Current() {
		return ErrUnavailable
	}
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil || len(results) != len(statements) || results[0].RowsAffected != 1 || results[1].RowsAffected != 1 || results[2].RowsAffected != 1 || !snapshot.Current() {
		return ErrUnavailable
	}
	return nil
}

// Native application views need the legacy token, while /sub remains served by
// the ordinary writer. No ordinary credentials are minted or re-enabled here.
func (s *Service) legacyPrimaryTokenAccess(ctx context.Context, customerID string) (CustomerAccess, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT t.token_envelope,t.token_hmac,t.token_sha256,c.display_login,e.source_key FROM subscription_tokens t
JOIN customers c ON c.customer_id=t.customer_id
JOIN imported_entity_state e ON e.entity_kind='customer' AND e.target_id=t.customer_id
WHERE t.customer_id=? AND t.revoked=0 AND e.lifecycle='active' AND e.source_key GLOB ?`,
		Args: []any{customerID, LegacyExactCustomerSourcePrefix + "*"},
	})
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		return CustomerAccess{}, ErrUnavailable
	}
	row := results[0].Rows[0]
	login, loginOK := rowString(row, "display_login")
	source, sourceOK := rowString(row, "source_key")
	tokenHMAC, hmacOK := rowString(row, "token_hmac")
	tokenDigest, digestOK := rowString(row, "token_sha256")
	expectedSource, _, expectedID, identityErr := s.legacyPrimaryIdentity(login)
	if !loginOK || !sourceOK || !hmacOK || !digestOK || identityErr != nil || source != expectedSource || customerID != expectedID {
		return CustomerAccess{}, ErrUnavailable
	}
	token, err := s.openCustomerSecret(row, "token_envelope", customerID, "token", "subscription")
	if err != nil || token == "" {
		return CustomerAccess{}, ErrUnavailable
	}
	digest := sha256.Sum256([]byte(token))
	if tokenHMAC != s.store.secrets.LookupHMAC("subscription-token", []byte(token)) || tokenDigest != hex.EncodeToString(digest[:]) {
		return CustomerAccess{}, ErrUnavailable
	}
	return CustomerAccess{SubscriptionToken: token, Credentials: map[string]string{}}, nil
}
