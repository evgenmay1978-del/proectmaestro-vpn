package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type panelWhiteListTestBusiness struct {
	panelPermissionBusiness
	commands []panelWhiteListCommand
}

func (b *panelWhiteListTestBusiness) PanelWhiteListBalance(context.Context, string) (panelWhiteListView, error) {
	return panelWhiteListView{RemainingBytes: 3000000000}, nil
}
func (b *panelWhiteListTestBusiness) PanelWhiteListAdmin(_ context.Context, c panelWhiteListCommand) (panelWhiteListView, error) {
	b.commands = append(b.commands, c)
	return panelWhiteListView{Enabled: c.Action == "enable", RemainingBytes: c.GB * 1000000000}, nil
}

func TestPanelCDNActionsRequireSessionCSRFAndOwnerPermission(t *testing.T) {
	for _, action := range []string{"enable", "disable", "credit"} {
		t.Run(action, func(t *testing.T) {
			b := &panelWhiteListTestBusiness{}
			handler := NewControlPlane(b, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
			body := `{"login":"MiXeD","action":"` + action + `","gb":0}`
			if action == "credit" {
				body = `{"login":"MiXeD","action":"credit","gb":137}`
			}
			request := httptest.NewRequest(http.MethodPost, "/mp/api/whitelist", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "manual-"+action)
			denied := httptest.NewRecorder()
			handler.ServeHTTP(denied, request)
			if denied.Code == http.StatusOK || len(b.commands) != 0 {
				t.Fatal("unauthenticated mutation")
			}
			request = httptest.NewRequest(http.MethodPost, "/mp/api/whitelist", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "manual-"+action)
			request.Header.Set("X-CSRF", "panel-csrf")
			request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
			request.AddCookie(&http.Cookie{Name: controlPlanePanelCSRFCookie, Value: "panel-csrf"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || len(b.commands) != 1 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			command := b.commands[0]
			if command.Login != "MiXeD" || command.Actor != "owner" || command.IdempotencyKey != "manual-"+action || b.authorize[len(b.authorize)-1].Permission != "settings.critical" {
				t.Fatalf("lost identity/permission: %#v", command)
			}
		})
	}
}

func TestPanelCDNCommandRejectsPackagesFractionalOrOutOfRangeAmounts(t *testing.T) {
	for _, c := range []panelWhiteListCommand{{Action: "credit", GB: 0}, {Action: "credit", GB: -1}, {Action: "credit", GB: 9223372037}, {Action: "enable", GB: 1}, {Action: "package", GB: 1}} {
		c.Login = "Exact"
		c.IdempotencyKey = "key"
		if validPanelWhiteListCommand(c) {
			t.Fatalf("invalid command: %#v", c)
		}
	}
}

func TestPanelCDNRejectsFractionalWireAndMissingCSRFBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		body string
		csrf bool
	}{
		{`{"login":"Exact","action":"credit","gb":1.5}`, true},
		{`{"login":"Exact","action":"credit","gb":1,"package":"fake"}`, true},
		{`{"login":"Exact","action":"credit","gb":1}`, false},
	} {
		b := &panelWhiteListTestBusiness{}
		handler := NewControlPlane(b, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
		request := httptest.NewRequest(http.MethodPost, "/mp/api/whitelist", strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "wire-amount")
		request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
		request.AddCookie(&http.Cookie{Name: controlPlanePanelCSRFCookie, Value: "panel-csrf"})
		if test.csrf {
			request.Header.Set("X-CSRF", "panel-csrf")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusOK || len(b.commands) != 0 {
			t.Fatalf("invalid mutation accepted: %s", test.body)
		}
	}
}

// The real service resolves an already committed credit. Only its subsequent
// customer read fails, reproducing the retry path after an unknown response.
type panelWhiteListCommittedReadDB struct {
	*exactLoginAPIReadDB
	accountID, entitlementID, requestHash string
	resolvedCredit                        bool
	ensureCalls                           int
	businessReads                         int
}

func (db *panelWhiteListCommittedReadDB) Request(_ context.Context, consistency rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	if consistency != rqlite.Linearizable || !transaction || len(statements) != 3 ||
		!strings.HasPrefix(statements[0].SQL, "INSERT INTO whitelist_entitlement_identities(") ||
		len(statements[0].Args) != 3 || statements[0].Args[2] != db.accountID ||
		!strings.HasPrefix(statements[2].SQL, "SELECT c.customer_id, wei.entitlement_id") {
		return nil, errors.New("unexpected committed-credit fixture write")
	}
	db.ensureCalls++
	return []rqlite.Result{{}, {}, {Rows: []map[string]any{{"customer_id": db.accountID, "entitlement_id": db.entitlementID}}}}, nil
}

func (db *panelWhiteListCommittedReadDB) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	const businessCustomerRead = `SELECT display_login,
       (SELECT COUNT(*) FROM devices d WHERE d.customer_id=customers.customer_id AND d.revoked=0 AND d.last_seen_at_unix>=?) AS device_count,
       COALESCE((SELECT MAX(d.last_seen_at_unix) FROM devices d WHERE d.customer_id=customers.customer_id AND d.revoked=0 AND d.last_seen_at_unix>=?),0) AS last_seen_at_unix
FROM customers WHERE customer_id=? LIMIT 1`
	if len(statements) == 1 && statements[0].SQL == businessCustomerRead {
		args := statements[0].Args
		if len(args) != 3 || args[2] != db.accountID || len(db.rows) != 1 || db.rows[0]["customer_id"] != db.accountID {
			return nil, errors.New("unexpected committed-credit customer read")
		}
		db.businessReads++
		return []rqlite.Result{{Rows: db.rows}}, nil
	}
	if len(statements) == 1 && statements[0].SQL == "SELECT request_hash,status,response_json FROM idempotency_requests WHERE scope=? AND command_type=? AND idempotency_key=?" {
		args := statements[0].Args
		if len(args) != 3 || args[0] != "whitelist_manual_credit:"+db.entitlementID || args[1] != "whitelist_manual_credit" || args[2] != "committed-credit" {
			return nil, errors.New("unexpected committed-credit receipt identity")
		}
		db.resolvedCredit = true
		// A concurrent account removal makes the following real lookup return 404.
		db.exactLoginAPIReadDB.rows = nil
		return []rqlite.Result{{Rows: []map[string]any{{"request_hash": db.requestHash, "status": "applied", "response_json": `{"operation_id":"committed-operation","credit_id":"committed-credit","bytes":3000000000,"purchased_remaining_bytes":3000000000,"projection_version":1}`}}}}, nil
	}
	return db.exactLoginAPIReadDB.QueryLinearizable(ctx, statements...)
}

type panelWhiteListCommittedIDs struct{}

func (panelWhiteListCommittedIDs) NewID(prefix string) (string, error) {
	if prefix != "wl-ent" {
		return "", errors.New("unexpected ID allocation during credit replay")
	}
	return "wl-ent_" + strings.Repeat("a", 32), nil
}

func TestPanelCDNCommittedCreditReadFailureRemainsRetryable(t *testing.T) {
	_, source := newExactLoginBusinessFixture(t, "Exact")
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x51}, 32)}, bytes.Repeat([]byte{0x52}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db := &panelWhiteListCommittedReadDB{exactLoginAPIReadDB: source, accountID: source.rows[0]["customer_id"].(string), entitlementID: "wl-ent-" + strings.Repeat("a", 32)}
	raw, err := json.Marshal(struct {
		Version       int
		EntitlementID string
		GB            int64
		Actor         string
		Kind          string
	}{1, db.entitlementID, 3, box.LookupHMAC("audit-actor", []byte("owner")), "whitelist_manual_credit"})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	db.requestHash = hex.EncodeToString(digest[:])
	clock := exactLoginAPIClock{}
	store, err := controlplane.NewStore(db, box, clock)
	if err != nil {
		t.Fatal(err)
	}
	service, err := controlplane.NewService(store, panelWhiteListCommittedIDs{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	business := NewServiceBusiness(service, ServiceBusinessConfig{Now: clock.Now})
	_, err = business.PanelWhiteListAdmin(context.Background(), panelWhiteListCommand{Login: "Exact", Action: "credit", GB: 3, Actor: "owner", IdempotencyKey: "committed-credit"})
	var httpErr interface{ HTTPStatus() int }
	if !db.resolvedCredit || db.ensureCalls != 1 || db.businessReads != 1 || !errors.Is(err, controlplane.ErrUnavailable) || !errors.As(err, &httpErr) || httpErr.HTTPStatus() != http.StatusServiceUnavailable {
		t.Fatalf("committed credit read was classified as rejection: resolved=%v ensures=%d business_reads=%d err=%v", db.resolvedCredit, db.ensureCalls, db.businessReads, err)
	}
	_, lookupErr := business.PanelWhiteListBalance(context.Background(), "Exact")
	if !errors.Is(lookupErr, controlplane.ErrNotFound) {
		t.Fatalf("fixture did not reproduce the post-commit 404: %v", lookupErr)
	}
}
