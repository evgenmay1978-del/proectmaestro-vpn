// Package whitelistfixture creates persisted control-plane entitlements for
// tests through the real Service and Store APIs. Production packages must not
// import testsupport.
package whitelistfixture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

var identitySequence atomic.Uint64

type fixtureClock struct{}

func (fixtureClock) Now() time.Time { return time.Unix(2_000_000, 0).UTC() }

type fixtureIDs struct{ value uint64 }

func (source fixtureIDs) NewID(prefix string) (string, error) {
	if prefix != "wl-ent" || source.value == 0 {
		return "", errors.New("whitelistfixture: unexpected id request")
	}
	return fmt.Sprintf("wl-ent_%032x", source.value), nil
}

type fixtureDB struct{ accountID string }

func (db *fixtureDB) Request(
	_ context.Context,
	level rqlite.Consistency,
	transaction bool,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	if level != rqlite.Linearizable || !transaction || len(statements) != 3 ||
		len(statements[0].Args) != 3 || len(statements[1].Args) != 1 || len(statements[2].Args) != 1 {
		return nil, errors.New("whitelistfixture: unexpected persistence request")
	}
	dirtySQL := strings.ToLower(statements[1].SQL)
	if !strings.Contains(dirtySQL, "update backup_rpo_state") || !strings.Contains(dirtySQL, "changes() > 0") {
		return nil, errors.New("whitelistfixture: unexpected dirty-generation statement")
	}
	entitlementID, idOK := statements[0].Args[0].(string)
	insertAccountID, insertOK := statements[0].Args[2].(string)
	selectAccountID, selectOK := statements[2].Args[0].(string)
	if !idOK || !insertOK || !selectOK || insertAccountID != db.accountID || selectAccountID != db.accountID {
		return nil, errors.New("whitelistfixture: unexpected persistence binding")
	}
	return []rqlite.Result{
		{RowsAffected: 1},
		{RowsAffected: 1},
		{Rows: []map[string]any{{"customer_id": db.accountID, "entitlement_id": entitlementID}}},
	}, nil
}

func (*fixtureDB) QueryLinearizable(context.Context, ...rqlite.Statement) ([]rqlite.Result, error) {
	return nil, errors.New("whitelistfixture: unexpected linearizable query")
}

func (*fixtureDB) QueryStrong(context.Context, ...rqlite.Statement) ([]rqlite.Result, error) {
	return nil, errors.New("whitelistfixture: unexpected strong query")
}

func (*fixtureDB) Backup(context.Context, io.Writer) error {
	return errors.New("whitelistfixture: unexpected backup")
}

// NewPersisted returns a fixture whose opaque identity has passed through the
// same atomic Service.EnsureWhiteListEntitlement boundary used in production.
func NewPersisted(accountID string) (controlplane.WhiteListEntitlement, error) {
	encryptionKey := bytes.Repeat([]byte{0x61}, 32)
	hmacKey := bytes.Repeat([]byte{0x62}, 32)
	secrets, err := controlplane.NewSecretBox(1, map[int][]byte{1: encryptionKey}, hmacKey)
	if err != nil {
		return controlplane.WhiteListEntitlement{}, err
	}
	clock := fixtureClock{}
	store, err := controlplane.NewStore(&fixtureDB{accountID: accountID}, secrets, clock)
	if err != nil {
		return controlplane.WhiteListEntitlement{}, err
	}
	service, err := controlplane.NewService(store, fixtureIDs{value: identitySequence.Add(1)}, clock)
	if err != nil {
		return controlplane.WhiteListEntitlement{}, err
	}
	return service.EnsureWhiteListEntitlement(context.Background(), accountID)
}

type testingT interface {
	Helper()
	Fatalf(string, ...any)
}

// MustPersisted is the testing convenience wrapper around NewPersisted.
func MustPersisted(t testingT, accountID string) controlplane.WhiteListEntitlement {
	t.Helper()
	entitlement, err := NewPersisted(accountID)
	if err != nil {
		t.Fatalf("NewPersisted(%q): %v", accountID, err)
	}
	return entitlement
}
