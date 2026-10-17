package main

import (
	"bufio"
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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
		rqlite.Statement{SQL: `INSERT INTO whitelist_sidecar_exits(exit_id,country_code,country_label,healthy,created_at_unix) VALUES('exit-s1','NL','Netherlands',1,?)`, Args: []any{now.Unix()}},
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
	routeCredential, err := controlplane.NewWhiteListRouteCredential(box, entitlementID, "exit-s1", material)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StoreWhiteListRouteCredential(context.Background(), routeCredential); err != nil {
		t.Fatalf("store route credential: %v", err)
	}
	sender := &publicationReceiptSender{
		now: now, bootID: strings.Repeat("b", 64), receipts: make(map[string][]byte), managedUsers: make(map[string][]string),
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
	// The real runtime caller must discover a paid candidate while Routes is
	// still empty. A direct call to Authorize would hide a bootstrap deadlock.
	reportPath := filepath.Join(t.TempDir(), "reserve.json")
	if err := os.WriteFile(reportPath, []byte(strings.ReplaceAll(runtimeReserveFixture, "exit-nl", "exit-s1")), 0600); err != nil {
		t.Fatal(err)
	}
	collector.reserves = runtimeWhiteListReserveFile(reportPath, clock.Now)
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
	if len(sender.leases) != 3 || len(sender.leases[2].Emails) != 1 || sender.leases[2].Emails[0] != "wl:"+entitlementID+":exit-s1" {
		t.Fatal("actual collector did not authorize exact managed route after debit")
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
	nativeRequest := func(token string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "https://sub.example.invalid/account/whitelist-runtime", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		handler.ServeHTTP(response, request)
		return response
	}
	bareRequest := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sub/"+customer.Access.SubscriptionToken, nil))
		return response
	}
	bareBefore := bareRequest()
	if bareBefore.Code != http.StatusOK {
		t.Fatal("ordinary baseline unavailable")
	}
	nativeResponse := nativeRequest(customer.Access.SubscriptionToken)
	var native api.WhiteListNativeRuntimeView
	if nativeResponse.Code != http.StatusOK || json.Unmarshal(nativeResponse.Body.Bytes(), &native) != nil ||
		native.SchemaVersion != 1 || native.ProjectionVersion != after.Projection.Version || native.DesiredGeneration <= 0 ||
		native.IssuedAtUnix != clock.Now().Unix() || native.FreshUntilUnix <= native.IssuedAtUnix ||
		native.FreshUntilUnix-native.IssuedAtUnix > 5 || len(native.Profiles) != 1 ||
		native.Profiles[0].Address != publicHost || native.Profiles[0].Encryption != encryption ||
		native.Profiles[0].TransportReleaseID != "release-1" || nativeResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("native route did not use actual fresh production publication adapter")
	}
	unknownNative := nativeRequest("unknown-native-test-token")
	if unknownNative.Code != http.StatusNotFound || strings.Contains(unknownNative.Body.String(), "client_id") {
		t.Fatal("native route failed unknown-token isolation")
	}
	sender.receipts = nil
	closed := request()
	if closed.Code != http.StatusServiceUnavailable || strings.Contains(closed.Body.String(), publicHost) {
		t.Fatalf("missing current-boot receipt did not fail closed: status=%d body=%q", closed.Code, closed.Body.String())
	}
	nativeClosed := nativeRequest(customer.Access.SubscriptionToken)
	if nativeClosed.Code != http.StatusServiceUnavailable || strings.Contains(nativeClosed.Body.String(), "profiles") ||
		strings.Contains(nativeClosed.Body.String(), publicHost) {
		t.Fatal("native route served credentials without a current Origin receipt")
	}
	bareAfter := bareRequest()
	if bareAfter.Code != http.StatusOK || bareAfter.Body.String() != bareBefore.Body.String() {
		t.Fatal("native publication failure changed the ordinary bare subscription")
	}
}

type publicationReceiptSender struct {
	now               time.Time
	bootID            string
	receipts          map[string][]byte
	managedUsers      map[string][]string
	countersAvailable bool
	usageSequence     int64
	leases            []sidecaragentclient.UseLeaseRequest
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
	snapshot := sidecaragentclient.UsageSnapshot{SampledAt: sender.now, Users: []sidecaragentclient.UsageUser{}, UnavailableUsers: []string{}}
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
	sender.usageSequence++
	nonce := sha256.Sum256([]byte(actionKey + ":" + strconv.FormatInt(sender.usageSequence, 10)))
	readStart := int64(10*time.Second) + sender.usageSequence*int64(time.Second)
	snapshot.LeaseChallenge = &sidecaragentclient.UseLeaseChallenge{Schema: 2, Nonce: hex.EncodeToString(nonce[:]), ClockDomain: strings.Repeat("d", 64), ReadStartedBoottimeNS: readStart, MaxDeadlineBoottimeNS: readStart + int64(5*time.Second), ManagedUsers: append([]string{}, sender.managedUsers[actionKey]...)}
	return snapshot, nil
}

func (*publicationReceiptSender) LookupFinalReceipts(context.Context) (sidecaragentclient.FinalReceiptPage, error) {
	return sidecaragentclient.FinalReceiptPage{Schema: 2, FinalReceipts: []sidecaragentclient.ManagedFinalReceipt{}}, nil
}
func (*publicationReceiptSender) AckFinalReceipts(context.Context, []sidecaragentclient.FinalReceiptACK) error {
	return errors.New("unexpected final receipt ACK")
}
func (sender *publicationReceiptSender) PostUseLease(_ context.Context, request sidecaragentclient.UseLeaseRequest) (sidecaragentclient.UseLeaseResponse, error) {
	if request.Schema != 2 || request.XrayProcessBootID != sender.bootID || request.DeadlineBoottimeNS <= request.ReadStartedBoottimeNS || request.DeadlineBoottimeNS-request.ReadStartedBoottimeNS > int64(5*time.Second) {
		return sidecaragentclient.UseLeaseResponse{}, errors.New("invalid fixture use lease")
	}
	sender.leases = append(sender.leases, request)
	return sidecaragentclient.UseLeaseResponse{Schema: 2, Nonce: request.Nonce, Complete: true, Receipts: []sidecaragentclient.LeaseReceiptProof{}}, nil
}

// publicationSQLite replaces only rqlite's network transport. Production
// migrations, constraints, storage services and the public renderer are real.
type publicationSQLite struct {
	command  *exec.Cmd
	input    io.WriteCloser
	output   io.ReadCloser
	scanner  *bufio.Scanner
	serial   chan struct{}
	stopped  chan struct{}
	exited   chan struct{}
	stopOnce sync.Once
	cancel   context.CancelFunc
}

const publicationSQLiteMaxFrame = 8 << 20

func newPublicationSQLite(t *testing.T) *publicationSQLite {
	t.Helper()
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal("Python SQLite is required for publication integration test")
	}
	path := filepath.Join(t.TempDir(), "publication.sqlite")
	lifetime, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	command := exec.CommandContext(lifetime, python, "-u", "-c", publicationSQLiteProgram, path)
	command.WaitDelay = time.Second
	input, err := command.StdinPipe()
	if err != nil {
		cancel()
		t.Fatalf("publication SQLite stdin: %v", err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		cancel()
		t.Fatalf("publication SQLite stdout: %v", err)
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		input.Close()
		output.Close()
		cancel()
		t.Fatalf("start publication SQLite: %v", err)
	}
	db := &publicationSQLite{
		command: command, input: input, output: output, scanner: bufio.NewScanner(output),
		serial: make(chan struct{}, 1), stopped: make(chan struct{}), exited: make(chan struct{}), cancel: cancel,
	}
	db.scanner.Buffer(make([]byte, 4096), publicationSQLiteMaxFrame)
	go func() {
		_ = command.Wait()
		close(db.exited)
	}()
	// Registered after TempDir, so the child is reaped before SQLite files are removed.
	t.Cleanup(db.close)
	startup, stopStartup := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopStartup()
	ready, err := db.exchange(startup, nil)
	if err != nil || string(ready) != `{"ready": true}` {
		db.stop()
		t.Fatalf("publication SQLite startup handshake: %v", err)
	}
	return db
}

func (db *publicationSQLite) stop() {
	db.stopOnce.Do(func() {
		close(db.stopped)
		db.cancel()
		_ = db.command.Process.Kill()
		_ = db.input.Close()
		_ = db.output.Close()
	})
}

func (db *publicationSQLite) close() {
	db.stop()
	<-db.exited
}

func (db *publicationSQLite) exchange(ctx context.Context, input []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		db.stop()
		return nil, ctx.Err()
	case <-db.stopped:
		return nil, errors.New("publication SQLite fixture is closed")
	case db.serial <- struct{}{}:
	}
	defer func() { <-db.serial }()
	if err := ctx.Err(); err != nil {
		db.stop()
		return nil, err
	}
	select {
	case <-db.stopped:
		return nil, errors.New("publication SQLite fixture is closed")
	default:
	}
	type reply struct {
		output []byte
		err    error
	}
	completed := make(chan reply, 1)
	go func() {
		if input != nil {
			written, err := db.input.Write(input)
			if err == nil && written != len(input) {
				err = io.ErrShortWrite
			}
			if err != nil {
				completed <- reply{err: err}
				return
			}
		}
		if !db.scanner.Scan() {
			err := db.scanner.Err()
			if err == nil {
				err = io.EOF
			}
			completed <- reply{err: err}
			return
		}
		completed <- reply{output: append([]byte(nil), db.scanner.Bytes()...)}
	}()
	select {
	case <-ctx.Done():
		db.stop()
		<-completed
		return nil, ctx.Err()
	case <-db.stopped:
		<-completed
		return nil, errors.New("publication SQLite fixture is closed")
	case result := <-completed:
		if err := ctx.Err(); err != nil {
			db.stop()
			return nil, err
		}
		if result.err != nil {
			db.stop()
			return nil, fmt.Errorf("publication SQLite process: %w", result.err)
		}
		return result.output, nil
	}
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
	if len(input) >= publicationSQLiteMaxFrame {
		db.stop()
		return nil, errors.New("publication SQLite request exceeds frame limit")
	}
	output, err := db.exchange(ctx, append(input, '\n'))
	if err != nil {
		return nil, err
	}
	var response struct {
		Results []rqlite.Result `json:"results"`
		Error   string          `json:"error"`
		Index   int             `json:"index"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		db.stop()
		return nil, fmt.Errorf("decode publication SQLite response: %w", err)
	}
	if response.Error != "" {
		return nil, &rqlite.StatementError{Index: response.Index, Message: response.Error}
	}
	return response.Results, nil
}

func TestPublicationSQLiteTransactionFailureRollsBackBeforeNextRequest(t *testing.T) {
	db := newPublicationSQLite(t)
	db.must(t, rqlite.Statement{SQL: `CREATE TABLE fixture(id INTEGER PRIMARY KEY, data BLOB NOT NULL)`})
	_, err := db.Request(context.Background(), rqlite.Strong, true,
		rqlite.Statement{SQL: `INSERT INTO fixture(id,data) VALUES(1,?)`, Args: []any{[]byte{1, 2, 3}}},
		rqlite.Statement{SQL: `INSERT INTO fixture(id,data) VALUES(1,?)`, Args: []any{[]byte{4}}},
	)
	var statementError *rqlite.StatementError
	if !errors.As(err, &statementError) || statementError.Index != 1 {
		t.Fatalf("transaction constraint error: %v", err)
	}
	result, err := db.QueryStrong(context.Background(), rqlite.Statement{SQL: `SELECT COUNT(*) AS count FROM fixture`})
	if err != nil || len(result) != 1 || len(result[0].Rows) != 1 || result[0].Rows[0]["count"] != float64(0) {
		t.Fatalf("failed transaction leaked into next request: %#v, %v", result, err)
	}
	db.must(t, rqlite.Statement{SQL: `INSERT INTO fixture(id,data) VALUES(1,?)`, Args: []any{[]byte{1, 2, 3}}})
	result, err = db.QueryStrong(context.Background(), rqlite.Statement{SQL: `SELECT data FROM fixture WHERE id=1`})
	if err != nil || len(result) != 1 || len(result[0].Rows) != 1 || result[0].Rows[0]["data"] != "AQID" {
		t.Fatalf("next transaction/blob result: %#v, %v", result, err)
	}
}

func TestPublicationSQLiteCancellationPoisonsProcess(t *testing.T) {
	db := newPublicationSQLite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := db.QueryStrong(ctx, rqlite.Statement{SQL: `WITH RECURSIVE counter(value) AS (
SELECT 1 UNION ALL SELECT value+1 FROM counter WHERE value<1000000000
) SELECT sum(value) FROM counter`})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled SQLite request: %v", err)
	}
	if _, err := db.QueryStrong(context.Background(), rqlite.Statement{SQL: `SELECT 1`}); err == nil {
		t.Fatal("cancelled SQLite child silently resumed")
	}
}

const publicationSQLiteProgram = `
import base64, json, sqlite3, sys
MAX_FRAME = 8 * 1024 * 1024
connection = sqlite3.connect(sys.argv[1], isolation_level=None)
connection.row_factory = sqlite3.Row
connection.create_function("unixepoch", 0, lambda: 2000000)
connection.execute("PRAGMA foreign_keys=ON")
def value(item):
    return base64.b64encode(item).decode() if isinstance(item, bytes) else item
print(json.dumps({"ready": True}), flush=True)
try:
    while True:
        request = sys.stdin.buffer.readline(MAX_FRAME + 1)
        if not request:
            break
        if len(request) > MAX_FRAME or not request.endswith(b"\n"):
            raise ValueError("request exceeds frame limit")
        payload = json.loads(request)
        results = []
        index = -1
        try:
            if payload["transaction"]:
                connection.execute("BEGIN IMMEDIATE")
            for index, statement in enumerate(payload["statements"]):
                args = [base64.b64decode(arg["blob"]) if isinstance(arg, dict) and "blob" in arg else arg for arg in statement.get("args") or []]
                cursor = connection.execute(statement["sql"], args)
                results.append({"Rows": [{key: value(row[key]) for key in row.keys()} for row in cursor.fetchall()], "RowsAffected": max(cursor.rowcount, 0)})
            if payload["transaction"]:
                connection.commit()
            response = {"results": results}
        except sqlite3.Error as error:
            if payload["transaction"]:
                connection.rollback()
            response = {"error": str(error), "index": index}
        output = json.dumps(response)
        if len(output.encode("utf-8")) + 1 > MAX_FRAME:
            raise ValueError("response exceeds frame limit")
        print(output, flush=True)
finally:
    connection.close()
`
