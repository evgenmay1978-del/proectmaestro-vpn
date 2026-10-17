package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

// SQL mutation correctness is exercised by the controlplane SQLite tests. This
// read transport supplies authenticated import rows to the real API resolver;
// unsupported queries and every mutation fail instead of inventing a response.
type exactLoginAPIReadDB struct {
	rows     []map[string]any
	access   map[string][]map[string]any
	requests int
}

func (db *exactLoginAPIReadDB) QueryLinearizable(_ context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	if len(statements) != 1 {
		return nil, errors.New("unexpected fixture query batch")
	}
	q := statements[0]
	if strings.HasPrefix(q.SQL, "SELECT c.customer_id,c.display_login,c.login_key_hmac") && strings.Contains(q.SQL, "AS exact_family") && len(q.Args) == 3 {
		family, familyOK := q.Args[0].(string)
		canonical, canonicalOK := q.Args[1].(string)
		exact, exactOK := q.Args[2].(string)
		if !familyOK || !canonicalOK || !exactOK {
			return nil, errors.New("invalid resolver fixture args")
		}
		foundFamily := int64(0)
		var found []map[string]any
		for _, row := range db.rows {
			if source, ok := row["source_key"].(string); ok && strings.HasPrefix(source, strings.TrimSuffix(family, "*")) {
				foundFamily = 1
			}
			if row["login_key_hmac"] == canonical || row["login_key_hmac"] == exact {
				found = append(found, row)
			}
		}
		if len(found) == 0 {
			found = []map[string]any{{"customer_id": nil, "has_token": int64(0)}}
		}
		result := make([]map[string]any, len(found))
		for index, row := range found {
			copyRow := make(map[string]any, len(row)+1)
			for key, value := range row {
				copyRow[key] = value
			}
			copyRow["exact_family"] = foundFamily
			result[index] = copyRow
		}
		return []rqlite.Result{{Rows: result}}, nil
	}
	if strings.HasPrefix(q.SQL, "SELECT customer_id,display_login,status,expires_at_unix,generation,") && strings.Contains(q.SQL, "FROM customers WHERE customer_id=?") && len(q.Args) == 3 {
		for _, row := range db.rows {
			if row["customer_id"] == q.Args[2] {
				return []rqlite.Result{{Rows: []map[string]any{row}}}, nil
			}
		}
		return []rqlite.Result{{}}, nil
	}
	if strings.HasPrefix(q.SQL, "SELECT st.token_envelope, cr.protocol, cr.secret_envelope") && len(q.Args) == 1 {
		id, ok := q.Args[0].(string)
		if !ok {
			return nil, errors.New("invalid access fixture args")
		}
		return []rqlite.Result{{Rows: db.access[id]}}, nil
	}
	return nil, errors.New("unexpected fixture query")
}
func (db *exactLoginAPIReadDB) QueryStrong(context.Context, ...rqlite.Statement) ([]rqlite.Result, error) {
	return nil, errors.New("unexpected weaker read")
}
func (db *exactLoginAPIReadDB) Request(context.Context, rqlite.Consistency, bool, ...rqlite.Statement) ([]rqlite.Result, error) {
	db.requests++
	return nil, errors.New("unexpected API fixture mutation")
}
func (db *exactLoginAPIReadDB) Backup(context.Context, io.Writer) error {
	return errors.New("unexpected backup")
}

type exactLoginAPIClock struct{}

func (exactLoginAPIClock) Now() time.Time { return time.Unix(2_000_000, 0) }

type exactLoginAPIIDs struct{}

func (exactLoginAPIIDs) NewID(prefix string) (string, error) { return prefix + "-fixture", nil }

func newExactLoginBusinessFixture(t *testing.T, logins ...string) (*ServiceBusiness, *exactLoginAPIReadDB) {
	t.Helper()
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x51}, 32)}, bytes.Repeat([]byte{0x52}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db := &exactLoginAPIReadDB{access: make(map[string][]map[string]any)}
	for _, login := range logins {
		canonical, err := controlplane.CanonicalLoginKey(login)
		if err != nil {
			t.Fatal(err)
		}
		exact := box.LookupHMAC(controlplane.LegacyExactCustomerLoginHMACDomain, []byte(login))
		source := controlplane.LegacyExactCustomerSourcePrefix + box.LookupHMAC("customer-login", []byte(canonical)) + ":" + exact
		mapped := sha256.Sum256([]byte("maestro-legacy-v1\x00customer\x00" + source))
		id := hex.EncodeToString(mapped[:])
		db.rows = append(db.rows, map[string]any{"customer_id": id, "display_login": login, "login_key_hmac": exact, "status": "active", "expires_at_unix": int64(3_000_000), "generation": int64(1), "source_key": source, "target_id": id, "canonical_sha256": strings.Repeat("a", 64), "lifecycle": "active", "has_token": int64(1)})
		seal := func(field, kind string) string {
			envelope, err := box.Seal(controlplane.SecretScope{OwnerType: "customer", OwnerID: id, Field: field, Kind: kind}, []byte("fixture-"+field+"-"+login))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			return base64.StdEncoding.EncodeToString(raw)
		}
		db.access[id] = []map[string]any{{"token_envelope": seal("token", "subscription"), "protocol": "vless", "secret_envelope": seal("credential", "vless")}}
	}
	clock := exactLoginAPIClock{}
	store, err := controlplane.NewStore(db, box, clock)
	if err != nil {
		t.Fatal(err)
	}
	service, err := controlplane.NewService(store, exactLoginAPIIDs{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	return NewServiceBusiness(service, ServiceBusinessConfig{}), db
}

func TestAPIPurchaseKeepsExactLoginBinding(t *testing.T) {
	business, db := newExactLoginBusinessFixture(t, "Alice", "alice")
	ctx := context.Background()
	var bindings []legacyOrderIdentityBinding
	for _, login := range []string{"Alice", "alice"} {
		binding, err := business.legacyOrderIdentity(ctx, CreateOrderCommand{Login: login})
		if err != nil || binding.LoginIdentity == nil || !binding.LoginIdentity.ExactLegacy() || binding.LoginIdentity.Login() != login || binding.CustomerID != binding.LoginIdentity.CustomerID() || binding.HMAC != binding.LoginIdentity.PurchaseIdentityHMAC() {
			t.Fatalf("exact purchase binding: %v", err)
		}
		bindings = append(bindings, binding)
	}
	if bindings[0].CustomerID == bindings[1].CustomerID || bindings[0].HMAC == bindings[1].HMAC {
		t.Fatal("distinct login orders collapsed")
	}
	if _, err := business.legacyOrderIdentity(ctx, CreateOrderCommand{Login: "ALICE"}); !errors.Is(err, controlplane.ErrConflict) {
		t.Fatalf("unknown purchase casing: %v", err)
	}
	ordinary, _ := newExactLoginBusinessFixture(t)
	binding, err := ordinary.legacyOrderIdentity(ctx, CreateOrderCommand{Login: " NewCustomer "})
	want, wantErr := ordinary.service.PurchaseOrderIdentityHMAC(controlplane.PurchaseOrderIdentityLogin, "newcustomer")
	if err != nil || wantErr != nil || binding.HMAC != want || binding.CustomerID != "" || binding.LoginIdentity.Login() != "newcustomer" {
		t.Fatalf("ordinary purchase compatibility: %v", err)
	}
	if db.requests != 0 {
		t.Fatal("identity lookup mutated storage")
	}
}

func TestAPIRoomMembersResolveHashesWithoutRehashingThem(t *testing.T) {
	business, db := newExactLoginBusinessFixture(t, "Alice", "alice")
	value := json.RawMessage(`{"rooms":{"Alice":{"room":"upper","provider":"wbstream"},"alice":{"room":"lower","provider":"wbstream"}}}`)
	resolved, identities, err := business.resolveOLCRTCSetting(context.Background(), value)
	if err != nil {
		t.Fatal(err)
	}
	members := make(map[string]json.RawMessage)
	for _, identity := range identities {
		members[identity.SettingMemberHMAC("olcrtc")] = json.RawMessage(`{"enabled":true}`)
	}
	logins, err := resolvedOLCRTCMembers(members, identities)
	if err != nil || !reflect.DeepEqual(logins, []string{"Alice", "alice"}) {
		t.Fatalf("hash-to-exact-room binding: %v", err)
	}
	login, target, err := olcrtcGrantTargetValueForIdentity(resolved, identities["Alice"].Login(), false)
	if err != nil || login != "Alice" || !strings.Contains(string(target), `"upper"`) || strings.Contains(string(target), `"lower"`) {
		t.Fatal("grant addressed another exact room")
	}
	members[strings.Repeat("f", 64)] = json.RawMessage(`{"enabled":true}`)
	if _, err := resolvedOLCRTCMembers(members, identities); !errors.Is(err, controlplane.ErrConflict) {
		t.Fatal("unbound persisted hash was dropped")
	}
	if _, _, err := business.resolveOLCRTCSetting(context.Background(), json.RawMessage(`{"rooms":{"ALICE":{"room":"wrong","provider":"wbstream"}}}`)); !errors.Is(err, controlplane.ErrConflict) {
		t.Fatal("unknown room casing was selected")
	}
	if db.requests != 0 {
		t.Fatal("room identity parsing mutated storage")
	}
}

func TestAPIWBProviderAndAssignmentUseSameExactIdentity(t *testing.T) {
	business, _ := newExactLoginBusinessFixture(t, "Alice", "alice")
	runner := &wbActionRunnerSpy{result: controlplane.ExternalActionResult{ID: "fixture-action", State: "succeeded", Response: []byte(`{"room":"new-room"}`)}}
	assigner := &wbRoomAssignerSpy{}
	business.externalActions, business.wbSender, business.wbRooms, business.workerID = runner, wbSenderStub{}, assigner, "fixture-worker"
	if _, err := business.RequestWBRoom(context.Background(), RequestWBRoomCommand{Login: "Alice", ActionKey: "action", IdempotencyKey: "assignment"}); err != nil {
		t.Fatal(err)
	}
	if runner.command.ResourceID != "Alice" || string(runner.command.Request) != `{"login":"Alice"}` || runner.command.ReplayResourceID != "" || assigner.login != "Alice" {
		t.Fatal("provider/assignment identities diverged")
	}
	if _, err := business.RequestWBRoom(context.Background(), RequestWBRoomCommand{Login: "ALICE", ActionKey: "other-action", IdempotencyKey: "other-assignment"}); !errors.Is(err, controlplane.ErrConflict) {
		t.Fatalf("unknown provider target: %v", err)
	}
	if runner.calls != 1 {
		t.Fatal("unknown casing reached provider")
	}
}
