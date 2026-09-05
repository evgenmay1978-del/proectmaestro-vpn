package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/shadowbilling"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
)

func TestPublicSubscriptionUsesProductionPublicationAdapterWithRealStorage(t *testing.T) {
	const publicHost = "cdn.example.invalid"
	now := time.Unix(2_000_000, 0).UTC()
	database := newPublicationSQLite(t)
	if err := controlplane.NewMigrator(database).Apply(context.Background()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x53}, 32)}, bytes.Repeat([]byte{0x64}, 32))
	if err != nil {
		t.Fatal(err)
	}
	clock := &runtimeTestClock{now: now}
	store, err := controlplane.NewStore(database, box, clock)
	if err != nil {
		t.Fatal(err)
	}
	service, err := controlplane.NewService(store, cryptoRuntimeIDs{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	customer, err := service.ProvisionCustomer(context.Background(), controlplane.ProvisionCustomerCommand{
		Login: "Publication", Days: 30, IdempotencyKey: "publication-integration",
	})
	if err != nil {
		t.Fatalf("provision customer: %v", err)
	}
	entitlement, err := service.EnsureWhiteListEntitlement(context.Background(), customer.ID)
	if err != nil {
		t.Fatalf("ensure entitlement: %v", err)
	}
	entitlementID := entitlement.EntitlementID()
	const periodID = "publication-paid-period"
	const accessOrderID = "publication-paid-access-order"
	database.must(t,
		rqlite.Statement{SQL: `INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES('s4','S4',0,1,?)`, Args: []any{now.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_sidecar_exits(exit_id,country_code,country_label,healthy,created_at_unix) VALUES('exit-nl','NL','Netherlands',1,?)`, Args: []any{now.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_sidecar_origins(origin_id,node_id,release_id,profile_id,preset_id,config_digest,active,created_at_unix) VALUES('origin-s4','s4','release-1','profile-1','preset-1',?,1,?)`, Args: []any{strings.Repeat("a", 64), now.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,result_generation,operation_id)
VALUES(?,'AA1122334455','publication-integration',?,?,'tariff_1m_v1',40000,'RUB',30,?,?,'confirmed','applied','confirmed',?,?,1,?)`,
			Args: []any{accessOrderID, strings.Repeat("c", 64), customer.ID, now.Unix() - 100,
				now.Unix() - 100 + 86400, now.Unix() - 50, now.Unix() + 30*86400, "publication-paid-operation"}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_billing_periods(
period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,included_grant_bytes,access_order_id,created_at_unix)
VALUES(?,?,0,?,?,0,?,?)`, Args: []any{periodID, entitlementID, now.Unix() - 100, now.Unix() + 86400, accessOrderID, now.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_balance_projections(entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix) VALUES(?,?,0,1000000000,0,0,1,0,0,?)`, Args: []any{entitlementID, periodID, now.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_publication_controls(control_id,entitlement_id,version,enabled,source,source_topup_order_id,operation_id,request_hash,created_at_unix) VALUES(?,?,2,1,'ADMIN_ENABLE',NULL,?,?,?)`, Args: []any{"wlpub-admin:" + entitlementID, entitlementID, "op-enable:" + entitlementID, strings.Repeat("b", 64), now.Unix()}},
	)
	encryption := "mlkem768x25519plus.native.0rtt." + strings.Repeat("Wlpa", 394) + "Wlo"
	proofDigest := sha256.Sum256([]byte("maestrovpn:vlessenc-client:v1\x00CLIENT\x00" + encryption))
	material, err := json.Marshal(controlplane.WhiteListClientMaterial{
		PublicHost: publicHost, SecretPath: "/static/main/video/segment.ts/opaque",
		ClientID: "11111111-1111-4111-8111-111111111111", ClientEncryption: encryption,
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:" + hex.EncodeToString(proofDigest[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	routeCredential, err := controlplane.NewWhiteListRouteCredential(box, entitlementID, "exit-nl", material)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StoreWhiteListRouteCredential(context.Background(), routeCredential); err != nil {
		t.Fatalf("store route credential: %v", err)
	}
	sender := &publicationReceiptSender{
		now: now, bootID: "boot-s4", receipts: make(map[string][]byte), managedUsers: make(map[string][]string),
	}
	meteringStore, err := shadowbilling.NewDurableStore(database)
	if err != nil {
		t.Fatal(err)
	}
	collector := &runtimeWhiteListMeteringCollector{
		control: service, store: meteringStore, workerID: "worker-s4",
		senders: map[string]controlplane.ExternalActionSender{"s4": sender},
	}
	// Use the production collector and durable services: an aggregate balance
	// watermark alone is not an authenticated per-origin admission/debit proof.
	if err := collector.runPass(context.Background()); err != nil {
		t.Fatalf("bootstrap empty desired and collector observation: %v", err)
	}
	if err := service.AuthorizeWhiteListMeteringAdmission(context.Background(), entitlementID, "exit-nl", controlplane.WhiteListAdmissionReserve{
		MeasuredP999BytesPerSecond: 2_000_000, MeasuredAtUnix: now.Unix(), ValidUntilUnix: now.Unix() + 20,
	}); err != nil {
		t.Fatalf("authorize measured first-use reserve: %v", err)
	}
	if err := collector.runPass(context.Background()); err != nil {
		t.Fatalf("provision admitted managed desired: %v", err)
	}
	before, err := service.WhiteListBalanceSnapshot(context.Background(), clock.Now().Unix(), entitlementID)
	if err != nil || before.Projection.FreshThroughUnix != 0 || before.Projection.LifetimeConsumedBytes != 0 {
		t.Fatalf("bootstrap fabricated metering freshness: %#v, %v", before, err)
	}
	clock.now = now.Add(time.Second)
	sender.now, sender.countersAvailable = clock.Now(), true
	if err := collector.runPass(context.Background()); err != nil {
		t.Fatalf("observe and debit the actual first cumulative: %v", err)
	}
	after, err := service.WhiteListBalanceSnapshot(context.Background(), clock.Now().Unix(), entitlementID)
	if err != nil || after.Projection.Pending || after.AvailableBytes != 1_000_000_000-42 ||
		after.Projection.LifetimeConsumedBytes != 42 || after.Projection.Version != before.Projection.Version+1 ||
		after.Projection.FreshThroughUnix != clock.Now().Unix() {
		t.Fatalf("first cumulative lacks its actual applied debit: %#v, %v", after, err)
	}
	source := runtimeWhiteListPublicationSource(
		service, true, map[string]controlplane.ExternalActionSender{"s4": sender},
	)

	t.Setenv("VLESS_SERVER", "ordinary.example.invalid")
	businessConfig := rqliteServiceBusinessConfig(
		api.Config{SubBaseURL: "https://sub.example.invalid"}, nil, "worker-s4", source,
	)
	businessConfig.Now = clock.Now
	businessConfig.WhiteListPublicationTimeout = 30 * time.Second
	handler := api.NewControlPlane(api.NewServiceBusiness(service, businessConfig), api.Config{}).Handler()
	request := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sub/"+customer.Access.SubscriptionToken+"?format=links", nil))
		return response
	}
	publishedResponse := request()
	published, decodeErr := base64.StdEncoding.Strict().DecodeString(publishedResponse.Body.String())
	if publishedResponse.Code != http.StatusOK || decodeErr != nil {
		t.Fatalf("published subscription status=%d decode=%v body=%q", publishedResponse.Code, decodeErr, publishedResponse.Body.String())
	}
	if !strings.Contains(string(published), publicHost) || !strings.Contains(string(published), "type=xhttp") || !strings.Contains(string(published), encryption) {
		t.Fatalf("public production adapter omitted CDN node: %q", published)
	}
	sender.receipts = nil
	closed := request()
	if closed.Code != http.StatusServiceUnavailable || strings.Contains(closed.Body.String(), publicHost) {
		t.Fatalf("missing current-boot receipt did not fail closed: status=%d body=%q", closed.Code, closed.Body.String())
	}
}

type publicationReceiptSender struct {
	now               time.Time
	bootID            string
	receipts          map[string][]byte
	managedUsers      map[string][]string
	countersAvailable bool
}

func (sender *publicationReceiptSender) Post(_ context.Context, request []byte) ([]byte, error) {
	var payload struct {
		OriginID             string   `json:"origin_id"`
		NodeID               string   `json:"node_id"`
		ReleaseID            string   `json:"release_id"`
		Generation           int64    `json:"generation"`
		ConfigDigest         string   `json:"config_digest"`
		ManagedUserSetDigest string   `json:"managed_user_set_digest"`
		ManagedUsers         []string `json:"managed_users"`
	}
	if err := json.Unmarshal(request, &payload); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(request)
	actionKey := payload.NodeID + ":" + strconv.FormatInt(payload.Generation, 10) + ":" + hex.EncodeToString(digest[:])
	receipt := controlplane.WhiteListSidecarReceipt{
		ActionKey: actionKey, OriginID: payload.OriginID, ReleaseID: payload.ReleaseID,
		XrayProcessBootID: sender.bootID, ConfigDigest: payload.ConfigDigest,
		DesiredGeneration: payload.Generation, ManagedUserSetDigest: payload.ManagedUserSetDigest,
		AppliedAt: sender.now, ExpiresAt: sender.now.Add(30 * time.Second),
	}
	raw, err := json.Marshal(receipt)
	if err == nil {
		sender.receipts[actionKey] = append([]byte(nil), raw...)
		sender.managedUsers[actionKey] = append([]string{}, payload.ManagedUsers...)
	}
	return raw, err
}

func (sender *publicationReceiptSender) LookupReceipt(_ context.Context, actionKey string) ([]byte, error) {
	raw := sender.receipts[actionKey]
	if len(raw) == 0 {
		return nil, controlplane.ErrUnavailable
	}
	return append([]byte(nil), raw...), nil
}

func (sender *publicationReceiptSender) LookupUsage(ctx context.Context, actionKey string) (sidecaragentclient.UsageSnapshot, error) {
	raw, err := sender.LookupReceipt(ctx, actionKey)
	if err != nil {
		return sidecaragentclient.UsageSnapshot{}, err
	}
	snapshot := sidecaragentclient.UsageSnapshot{SampledAt: sender.now}
	if err := json.Unmarshal(raw, &snapshot.Receipt); err != nil {
		return sidecaragentclient.UsageSnapshot{}, err
	}
	for _, email := range sender.managedUsers[actionKey] {
		if !sender.countersAvailable {
			snapshot.UnavailableUsers = append(snapshot.UnavailableUsers, email)
			continue
		}
		snapshot.Users = append(snapshot.Users, sidecaragentclient.UsageUser{Email: email, UplinkBytes: 19, DownlinkBytes: 23})
	}
	return snapshot, nil
}

// publicationSQLite replaces only rqlite's network transport. Production
// migrations, constraints, storage services and the public renderer are real.
type publicationSQLite struct {
	python string
	path   string
}

func newPublicationSQLite(t *testing.T) *publicationSQLite {
	t.Helper()
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal("Python SQLite is required for publication integration test")
	}
	return &publicationSQLite{python: python, path: filepath.Join(t.TempDir(), "publication.sqlite")}
}

func (db *publicationSQLite) Request(ctx context.Context, _ rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.execute(ctx, transaction, statements...)
}

func (db *publicationSQLite) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.execute(ctx, false, statements...)
}

func (db *publicationSQLite) QueryStrong(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.execute(ctx, false, statements...)
}

func (*publicationSQLite) Backup(context.Context, io.Writer) error {
	return errors.New("backup is outside the publication integration fixture")
}

func (db *publicationSQLite) must(t *testing.T, statements ...rqlite.Statement) {
	t.Helper()
	if _, err := db.execute(context.Background(), true, statements...); err != nil {
		t.Fatalf("SQLite fixture: %v", err)
	}
}

func (db *publicationSQLite) execute(ctx context.Context, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	payload := make([]map[string]any, 0, len(statements))
	for _, statement := range statements {
		args := make([]any, len(statement.Args))
		for index, arg := range statement.Args {
			if blob, ok := arg.([]byte); ok {
				args[index] = map[string]string{"blob": base64.StdEncoding.EncodeToString(blob)}
			} else {
				args[index] = arg
			}
		}
		payload = append(payload, map[string]any{"sql": statement.SQL, "args": args})
	}
	input, err := json.Marshal(map[string]any{"transaction": transaction, "statements": payload})
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, db.python, "-c", publicationSQLiteProgram, db.path)
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("publication SQLite process: %w", err)
	}
	var response struct {
		Results []rqlite.Result `json:"results"`
		Error   string          `json:"error"`
		Index   int             `json:"index"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("decode publication SQLite response: %w", err)
	}
	if response.Error != "" {
		return nil, &rqlite.StatementError{Index: response.Index, Message: response.Error}
	}
	return response.Results, nil
}

const publicationSQLiteProgram = `
import base64, json, sqlite3, sys
payload = json.load(sys.stdin)
connection = sqlite3.connect(sys.argv[1], isolation_level=None)
connection.row_factory = sqlite3.Row
connection.create_function("unixepoch", 0, lambda: 2000000)
connection.execute("PRAGMA foreign_keys=ON")
results = []
index = -1
def value(item):
    return base64.b64encode(item).decode() if isinstance(item, bytes) else item
try:
    if payload["transaction"]:
        connection.execute("BEGIN IMMEDIATE")
    for index, statement in enumerate(payload["statements"]):
        args = [base64.b64decode(arg["blob"]) if isinstance(arg, dict) and "blob" in arg else arg for arg in statement.get("args") or []]
        cursor = connection.execute(statement["sql"], args)
        results.append({"Rows": [{key: value(row[key]) for key in row.keys()} for row in cursor.fetchall()], "RowsAffected": max(cursor.rowcount, 0)})
    if payload["transaction"]:
        connection.commit()
    print(json.dumps({"results": results}))
except sqlite3.Error as error:
    if payload["transaction"]:
        connection.rollback()
    print(json.dumps({"error": str(error), "index": index}))
finally:
    connection.close()
`
