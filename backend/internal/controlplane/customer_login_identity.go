package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	LegacyExactCustomerSourcePrefix    = "s1:customer-exact-v1:"
	LegacyExactCustomerLoginHMACDomain = "legacy-customer-login-exact-v1"
)

// ParseLegacyExactCustomerSource decodes the authenticated import wire identity.
// The canonical digest indexes a family; it never chooses one family member.
func ParseLegacyExactCustomerSource(source string) (canonicalHMAC, exactHMAC string, ok bool) {
	if !strings.HasPrefix(source, LegacyExactCustomerSourcePrefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(source, LegacyExactCustomerSourcePrefix), ":")
	if len(parts) != 2 || !customerIdentityDigest(parts[0]) || !customerIdentityDigest(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func customerIdentityDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// CustomerLoginIdentity is an immutable, database-bound lookup proof. Callers
// cannot construct an authoritative identity from an ID or a supplied digest.
type CustomerLoginIdentity struct {
	box                                              *SecretBox
	login, display, lookup, canonicalHMAC, exactHMAC string
	source, digest, lifecycle                        string
	customer                                         Customer
	exists, exact, hasToken                          bool
}

func (i CustomerLoginIdentity) Exists() bool       { return i.box != nil && i.exists }
func (i CustomerLoginIdentity) CustomerID() string { return i.customer.ID }

// Login is the original login for an exact import, and the canonical member key
// for ordinary customers. It is also the stable setting/room membership input.
func (i CustomerLoginIdentity) Login() string      { return i.login }
func (i CustomerLoginIdentity) LookupHMAC() string { return i.lookup }
func (i CustomerLoginIdentity) ExactLegacy() bool  { return i.exact }
func (i CustomerLoginIdentity) SettingMemberHMAC(settingKey string) string {
	if i.box == nil {
		return ""
	}
	return i.box.LookupHMAC("setting-member:"+settingKey, []byte(i.login))
}
func (i CustomerLoginIdentity) PurchaseIdentityHMAC() string {
	if i.box == nil {
		return ""
	}
	domain := "legacy-order-identity:login"
	if i.exact {
		domain = "legacy-order-identity:login-exact-v1"
	}
	return i.box.LookupHMAC(domain, []byte(i.login))
}

func (s *Service) ResolveCustomerLogin(ctx context.Context, raw string) (CustomerLoginIdentity, error) {
	identity, err := s.store.resolveCustomerLogin(ctx, raw)
	if err == nil && identity.lifecycle == "deleted" {
		return CustomerLoginIdentity{}, ErrNotFound
	}
	return identity, err
}

// resolveCustomerLogin retains a deleted import's immutable identity only so a
// mutation can resolve its already-applied receipt before rejecting a new write.
func (s *Store) resolveCustomerLogin(ctx context.Context, raw string) (CustomerLoginIdentity, error) {
	canonical, err := CanonicalLoginKey(raw)
	if err != nil {
		return CustomerLoginIdentity{}, ErrNotFound
	}
	i := CustomerLoginIdentity{box: s.secrets, login: canonical}
	i.canonicalHMAC = s.secrets.LookupHMAC("customer-login", []byte(canonical))
	i.exactHMAC = s.secrets.LookupHMAC(LegacyExactCustomerLoginHMACDomain, []byte(raw))
	i.lookup = i.canonicalHMAC
	family := LegacyExactCustomerSourcePrefix + i.canonicalHMAC + ":*"
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT c.customer_id,c.display_login,c.login_key_hmac,c.status,c.expires_at_unix,c.generation,
e.source_key,e.target_id,e.canonical_sha256,e.lifecycle,
EXISTS(SELECT 1 FROM imported_entity_state f WHERE f.entity_kind='customer' AND f.source_key GLOB ?) AS exact_family,
EXISTS(SELECT 1 FROM subscription_tokens st WHERE st.customer_id=c.customer_id AND st.revoked=0) AS has_token
FROM (SELECT 1) seed
LEFT JOIN customers c ON c.login_key_hmac IN (?,?)
LEFT JOIN imported_entity_state e ON e.entity_kind='customer' AND e.target_id=c.customer_id
LIMIT 3`, Args: []any{family, i.canonicalHMAC, i.exactHMAC},
	})
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		return CustomerLoginIdentity{}, ErrUnavailable
	}
	row := results[0].Rows[0]
	familyValue, familyOK := rowInt64(row, "exact_family")
	hasToken, tokenOK := rowInt64(row, "has_token")
	if !familyOK || familyValue < 0 || familyValue > 1 || !tokenOK || hasToken < 0 || hasToken > 1 {
		return CustomerLoginIdentity{}, ErrUnavailable
	}
	idValue, idPresent := row["customer_id"]
	if !idPresent {
		return CustomerLoginIdentity{}, ErrUnavailable
	}
	if idValue == nil {
		if familyValue == 1 {
			return CustomerLoginIdentity{}, ErrConflict
		}
		return i, nil
	}
	id, idOK := rowString(row, "customer_id")
	display, displayOK := rowString(row, "display_login")
	lookup, lookupOK := rowString(row, "login_key_hmac")
	status, statusOK := rowString(row, "status")
	expires, expiresOK := rowInt64(row, "expires_at_unix")
	generation, generationOK := rowInt64(row, "generation")
	if !idOK || id == "" || !displayOK || !lookupOK || !statusOK || !expiresOK || expires < 0 || !generationOK || generation < 0 {
		return CustomerLoginIdentity{}, ErrUnavailable
	}
	switch status {
	case "active", "suspended", "expired", "deleted":
	default:
		return CustomerLoginIdentity{}, ErrUnavailable
	}
	if displayCanonical, displayErr := CanonicalLoginKey(display); displayErr != nil || displayCanonical != canonical {
		return CustomerLoginIdentity{}, ErrUnavailable
	}
	i.customer = Customer{ID: id, Status: status, ExpiresAtUnix: expires, Generation: generation}
	i.display, i.lookup, i.exists, i.hasToken = display, lookup, true, hasToken == 1
	if sourceValue, present := row["source_key"]; !present {
		return CustomerLoginIdentity{}, ErrUnavailable
	} else if sourceValue != nil {
		source, sourceOK := rowString(row, "source_key")
		target, targetOK := rowString(row, "target_id")
		digest, digestOK := rowString(row, "canonical_sha256")
		lifecycle, lifecycleOK := rowString(row, "lifecycle")
		if !sourceOK || source == "" || !targetOK || target != id || !digestOK || !customerIdentityDigest(digest) || !lifecycleOK || (lifecycle != "active" && lifecycle != "deleted") {
			return CustomerLoginIdentity{}, ErrUnavailable
		}
		i.source, i.digest, i.lifecycle = source, digest, lifecycle
	}
	if lookup == i.exactHMAC {
		canonicalPart, exactPart, valid := ParseLegacyExactCustomerSource(i.source)
		mapped := sha256.Sum256([]byte("maestro-legacy-v1\x00customer\x00" + i.source))
		if !valid || canonicalPart != i.canonicalHMAC || exactPart != i.exactHMAC || id != hex.EncodeToString(mapped[:]) || display != raw || familyValue != 1 {
			return CustomerLoginIdentity{}, ErrUnavailable
		}
		i.login, i.exact = raw, true
	} else if lookup != i.canonicalHMAC || familyValue != 0 || strings.HasPrefix(i.source, LegacyExactCustomerSourcePrefix) {
		return CustomerLoginIdentity{}, ErrUnavailable
	}
	return i, nil
}

// guardSQL repeats the read proof in the caller's write transaction. Generation
// CAS remains the caller's responsibility; this predicate binds the identity.
func (i CustomerLoginIdentity) guardSQL() (string, []any) {
	if i.box == nil || i.lifecycle == "deleted" {
		return "0=1", nil
	}
	family := LegacyExactCustomerSourcePrefix + i.canonicalHMAC + ":*"
	guard := `NOT EXISTS(SELECT 1 FROM imported_entity_state f WHERE f.entity_kind='customer' AND f.source_key GLOB ?)`
	args := []any{family}
	if i.exact {
		guard = `NOT EXISTS(SELECT 1 FROM customers canonical WHERE canonical.login_key_hmac=?)`
		args = []any{i.canonicalHMAC}
	}
	if !i.exists {
		return guard + ` AND NOT EXISTS(SELECT 1 FROM customers c WHERE c.login_key_hmac IN (?,?))`, append(args, i.canonicalHMAC, i.exactHMAC)
	}
	guard += ` AND EXISTS(SELECT 1 FROM customers c WHERE c.customer_id=? AND c.login_key_hmac=? AND c.display_login=?)`
	args = append(args, i.customer.ID, i.lookup, i.display)
	if i.source == "" {
		return guard + ` AND NOT EXISTS(SELECT 1 FROM imported_entity_state e WHERE e.entity_kind='customer' AND e.target_id=?)`, append(args, i.customer.ID)
	}
	return guard + ` AND EXISTS(SELECT 1 FROM imported_entity_state e WHERE e.entity_kind='customer' AND e.source_key=? AND e.target_id=? AND e.canonical_sha256=? AND e.lifecycle='active')`, append(args, i.source, i.customer.ID, i.digest)
}
