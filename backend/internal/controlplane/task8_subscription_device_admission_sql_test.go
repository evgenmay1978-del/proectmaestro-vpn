package controlplane_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func TestSubscriptionDeviceAdmissionHealthyLimitSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	var first f5HTTPResult
	for index := 1; index <= 5; index++ {
		result := f5SubscriptionGET(t, fixture.handler, fixture.path(""), fmt.Sprintf("device-%d", index), "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
		if result.status != http.StatusOK {
			t.Fatalf("device %d status=%d body=%q, want 200", index, result.status, result.body)
		}
		if index == 1 {
			first = result
		}
	}
	if got := fixture.deviceCount(t); got != 5 {
		t.Fatalf("committed device count=%d, want 5", got)
	}

	blocked := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "device-6", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if blocked.status != http.StatusForbidden {
		t.Fatalf("sixth device status=%d body=%q, want 403", blocked.status, blocked.body)
	}
	if got := fixture.deviceCount(t); got != 5 {
		t.Fatalf("blocked device changed count to %d, want 5", got)
	}

	repeat := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "device-1", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if repeat.status != http.StatusOK || !bytes.Equal(repeat.body, first.body) {
		t.Fatalf("committed repeat status=%d byte_equal=%v", repeat.status, bytes.Equal(repeat.body, first.body))
	}
	if got := fixture.deviceCount(t); got != 5 {
		t.Fatalf("committed repeat changed count to %d, want 5", got)
	}
}

func TestSubscriptionInfoAndLegacyNoDeviceDoNotAdmitSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)

	withoutDevice := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "", "curl/8")
	if withoutDevice.status != http.StatusOK {
		t.Fatalf("no-device base status=%d body=%q, want 200", withoutDevice.status, withoutDevice.body)
	}
	invalidDevice := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "invalid/device", "curl/8")
	if invalidDevice.status != http.StatusOK {
		t.Fatalf("invalid-device base status=%d body=%q, want 200", invalidDevice.status, invalidDevice.body)
	}
	info := f5SubscriptionGET(t, fixture.handler, fixture.path("/info"), "info-must-not-admit", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if info.status != http.StatusOK {
		t.Fatalf("info status=%d body=%q, want 200", info.status, info.body)
	}
	if got := fixture.deviceCount(t); got != 0 {
		t.Fatalf("no-device/invalid/info requests committed %d devices, want 0", got)
	}
}

func TestSubscriptionCacheNoQuorumSeparatesCommittedAndNewDevicesSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	warm := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "committed-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if warm.status != http.StatusOK {
		t.Fatalf("warm status=%d body=%q", warm.status, warm.body)
	}
	fixture.database.setUnavailable(true)

	cached := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "committed-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if cached.status != http.StatusOK || cached.contentType != warm.contentType || !bytes.Equal(cached.body, warm.body) {
		t.Fatalf("cached committed status=%d content_type=%q byte_equal=%v", cached.status, cached.contentType, bytes.Equal(cached.body, warm.body))
	}
	newDevice := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "uncommitted-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if newDevice.status != http.StatusServiceUnavailable {
		t.Fatalf("new device during outage status=%d body=%q, want 503", newDevice.status, newDevice.body)
	}

	emptyCacheHandler := fixture.newHandler()
	emptyCache := f5SubscriptionGET(t, emptyCacheHandler, fixture.path(""), "committed-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if emptyCache.status != http.StatusServiceUnavailable {
		t.Fatalf("new ServiceBusiness empty cache status=%d body=%q, want 503", emptyCache.status, emptyCache.body)
	}
	if got := fixture.deviceCount(t); got != 1 {
		t.Fatalf("outage requests changed device count to %d, want 1", got)
	}
}

func TestSubscriptionCacheTTLBoundarySQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	warm := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "ttl-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if warm.status != http.StatusOK {
		t.Fatalf("warm status=%d body=%q", warm.status, warm.body)
	}
	fixture.database.setUnavailable(true)

	fixture.clock.set(fixture.startedAt.Add(60 * time.Minute))
	exact := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "ttl-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if exact.status != http.StatusOK || !bytes.Equal(exact.body, warm.body) {
		t.Fatalf("cache at exactly 60m status=%d byte_equal=%v, want cached 200", exact.status, bytes.Equal(exact.body, warm.body))
	}

	fixture.clock.set(fixture.startedAt.Add(60*time.Minute + time.Second))
	stale := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "ttl-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if stale.status != http.StatusServiceUnavailable {
		t.Fatalf("cache over 60m status=%d body=%q, want 503", stale.status, stale.body)
	}
}

func TestSubscriptionCacheKeepsRenderVariantsIsolatedSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	const modernUA = "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)"
	const stockUA = "curl/8"

	modern := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "variant-device", modernUA)
	links := f5SubscriptionGET(t, fixture.handler, fixture.path("?format=links"), "variant-device", stockUA)
	stock := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "variant-device", stockUA)
	for name, result := range map[string]f5HTTPResult{"modern": modern, "links": links, "stock": stock} {
		if result.status != http.StatusOK {
			t.Fatalf("%s warm status=%d body=%q", name, result.status, result.body)
		}
	}
	if modern.contentType != "application/json" || stock.contentType != "application/json" || links.contentType != "text/plain; charset=utf-8" {
		t.Fatalf("variant content types modern=%q stock=%q links=%q", modern.contentType, stock.contentType, links.contentType)
	}
	modernHasNaive := f5HasOutboundType(t, modern.body, "naive")
	stockHasNaive := f5HasOutboundType(t, stock.body, "naive")
	if !modernHasNaive || stockHasNaive {
		t.Fatalf("UA variant did not preserve Naive gate: modern_has=%v stock_has=%v", modernHasNaive, stockHasNaive)
	}
	decodedLinks, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(links.body)))
	if err != nil || !bytes.Contains(decodedLinks, []byte("naive+https://")) {
		t.Fatalf("links variant is not the expected independent share-link document: decoded=%q err=%v", decodedLinks, err)
	}
	if bytes.Equal(modern.body, stock.body) || bytes.Equal(modern.body, links.body) || bytes.Equal(stock.body, links.body) {
		t.Fatal("base/links/UA variants collapsed to the same cached bytes")
	}

	fixture.database.setUnavailable(true)
	for _, test := range []struct {
		name, suffix, userAgent string
		want                    f5HTTPResult
	}{
		{name: "modern", userAgent: modernUA, want: modern},
		{name: "links", suffix: "?format=links", userAgent: stockUA, want: links},
		{name: "stock", userAgent: stockUA, want: stock},
	} {
		got := f5SubscriptionGET(t, fixture.handler, fixture.path(test.suffix), "variant-device", test.userAgent)
		if got.status != http.StatusOK || got.contentType != test.want.contentType || !bytes.Equal(got.body, test.want.body) {
			t.Fatalf("%s cached status=%d content_type=%q byte_equal=%v", test.name, got.status, got.contentType, bytes.Equal(got.body, test.want.body))
		}
	}
}

func TestSubscriptionExpiredCachedRouteSemanticsSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	databaseNow := f5DatabaseNow(t, fixture)
	fixture.sqlite.must(t, rqlite.Statement{SQL: `UPDATE customers SET expires_at_unix=?,updated_at_unix=? WHERE customer_id=?`, Args: []any{
		databaseNow - 1, fixture.startedAt.Unix(), fixture.customerID,
	}})

	warmInfo := f5SubscriptionGET(t, fixture.handler, fixture.path("/info"), "ignored-info-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if warmInfo.status != http.StatusOK {
		t.Fatalf("expired warm info status=%d body=%q", warmInfo.status, warmInfo.body)
	}
	var info struct {
		Active   bool `json:"active"`
		DaysLeft int  `json:"days_left"`
	}
	if err := json.Unmarshal(warmInfo.body, &info); err != nil || info.Active || info.DaysLeft != 0 {
		t.Fatalf("expired info=%+v body=%q err=%v, want active=false days_left=0", info, warmInfo.body, err)
	}
	if got := fixture.deviceCount(t); got != 0 {
		t.Fatalf("expired /info committed %d devices, want 0", got)
	}

	fixture.database.setUnavailable(true)
	cachedInfo := f5SubscriptionGET(t, fixture.handler, fixture.path("/info"), "another-info-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if cachedInfo.status != http.StatusOK || !bytes.Equal(cachedInfo.body, warmInfo.body) {
		t.Fatalf("expired cached info status=%d byte_equal=%v", cachedInfo.status, bytes.Equal(cachedInfo.body, warmInfo.body))
	}
	for _, suffix := range []string{"", "/helpers", "?format=links"} {
		result := f5SubscriptionGET(t, fixture.handler, fixture.path(suffix), "expired-device", "curl/8")
		if result.status != http.StatusPaymentRequired {
			t.Fatalf("expired cached %q status=%d body=%q, want 402", suffix, result.status, result.body)
		}
	}
	if got := fixture.deviceCount(t); got != 0 {
		t.Fatalf("expired cached routes committed %d devices, want 0", got)
	}
}

func TestSubscriptionDeviceAdmissionKillSwitchAndUnlimitedSQLite(t *testing.T) {
	t.Run("kill switch bypasses without recording", func(t *testing.T) {
		fixture := newF5SubscriptionFixture(t, func(string) int { return -1 })
		for index := 1; index <= 8; index++ {
			result := f5SubscriptionGET(t, fixture.handler, fixture.path(""), fmt.Sprintf("kill-switch-%d", index), "curl/8")
			if result.status != http.StatusOK {
				t.Fatalf("kill-switch device %d status=%d body=%q", index, result.status, result.body)
			}
		}
		if got := fixture.deviceCount(t); got != 0 {
			t.Fatalf("kill switch recorded %d devices, want 0", got)
		}
	})

	t.Run("zero means unlimited and records", func(t *testing.T) {
		fixture := newF5SubscriptionFixture(t, func(string) int { return 0 })
		for index := 1; index <= 8; index++ {
			result := f5SubscriptionGET(t, fixture.handler, fixture.path(""), fmt.Sprintf("unlimited-%d", index), "curl/8")
			if result.status != http.StatusOK {
				t.Fatalf("unlimited device %d status=%d body=%q", index, result.status, result.body)
			}
		}
		if got := fixture.deviceCount(t); got != 8 {
			t.Fatalf("unlimited committed device count=%d, want 8", got)
		}
	})
}

func TestSubscriptionStrongRevocationInvalidatesCachedDeviceSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	warm := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "revoked-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if warm.status != http.StatusOK {
		t.Fatalf("warm status=%d body=%q", warm.status, warm.body)
	}
	fixture.sqlite.must(t, rqlite.Statement{SQL: `UPDATE subscription_tokens SET revoked=1,revoked_at_unix=? WHERE customer_id=? AND revoked=0`, Args: []any{
		fixture.startedAt.Unix(), fixture.customerID,
	}})

	strongProbe := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "revocation-probe", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if strongProbe.status == http.StatusOK {
		t.Fatalf("strong revoked-token probe reused cached authorization: body=%q", strongProbe.body)
	}
	if got := fixture.deviceCount(t); got != 1 {
		t.Fatalf("revocation probe changed device count to %d, want 1", got)
	}

	fixture.database.setUnavailable(true)
	stale := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "revoked-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if stale.status != http.StatusServiceUnavailable {
		t.Fatalf("revoked cached device during outage status=%d body=%q, want 503", stale.status, stale.body)
	}
}

type f5SubscriptionFixture struct {
	sqlite     *s4CanarySQLite
	database   *f5ToggleRQLite
	box        *controlplane.SecretBox
	clock      *f5MutableClock
	service    *controlplane.Service
	config     api.ServiceBusinessConfig
	handler    http.Handler
	startedAt  time.Time
	customerID string
	token      string
}

func newF5SubscriptionFixture(t *testing.T, deviceLimitFor func(string) int) *f5SubscriptionFixture {
	return newF5SubscriptionFixtureWithIDs(t, deviceLimitFor, &f5UniqueIDs{})
}

func newF5SubscriptionFixtureWithIDs(t *testing.T, deviceLimitFor func(string) int, ids controlplane.IDSource) *f5SubscriptionFixture {
	t.Helper()
	ctx := context.Background()
	sqlite := newS4CanarySQLite(t)
	database := &f5ToggleRQLite{inner: sqlite}
	if err := controlplane.NewMigrator(database).Apply(ctx); err != nil {
		t.Fatalf("apply real migrations: %v", err)
	}
	box, err := controlplane.NewSecretBox(
		1,
		map[int][]byte{1: bytes.Repeat([]byte{0x71}, 32)},
		bytes.Repeat([]byte{0x72}, 32),
	)
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	startedAt := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	clock := &f5MutableClock{value: startedAt}
	store, err := controlplane.NewStore(database, box, clock)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	service, err := controlplane.NewService(store, ids, clock)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	const customerID = "f5-customer"
	sqlite.must(t, rqlite.Statement{SQL: `INSERT INTO node_services(node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix)
VALUES ('S4','s4',1,1,0,0,2000000)`})
	seedS4CanaryCustomer(t, sqlite, box, customerID, "F5 User", "f5-operation", "f5-envelope", strings.Repeat("a", 64))
	sqlite.must(t, rqlite.Statement{SQL: `UPDATE customers SET expires_at_unix=?,updated_at_unix=? WHERE customer_id=?`, Args: []any{
		startedAt.Add(24 * time.Hour).Unix(), startedAt.Unix(), customerID,
	}})
	f5SeedCredential(t, sqlite, box, customerID, "naive", "f5-naive-password")

	fixture := &f5SubscriptionFixture{
		sqlite: sqlite, database: database, box: box, clock: clock, service: service,
		startedAt: startedAt, customerID: customerID, token: customerID + "-token",
		config: api.ServiceBusinessConfig{
			DeviceLimitFor:       deviceLimitFor,
			Now:                  clock.Now,
			SubscriptionCacheTTL: 60 * time.Minute,
			SubscriptionTopology: subgen.Customer{
				VLESS: &subgen.VLESSCreds{
					Server: "s1.example.test", Port: 443, SNI: "s1.example.test",
					PublicKey: "f5-public-key", ShortID: "0123456789abcdef", Flow: "xtls-rprx-vision", Fingerprint: "chrome",
				},
				Naive: &subgen.NaiveCreds{Server: "naive.example.test", Port: 443, SNI: "naive.example.test"},
			},
		},
	}
	fixture.handler = fixture.newHandler()
	return fixture
}

func (fixture *f5SubscriptionFixture) newHandler() http.Handler {
	business := api.NewServiceBusiness(fixture.service, fixture.config)
	return api.NewControlPlane(business, api.Config{EnforceDeviceLimit: true}).Handler()
}

func (fixture *f5SubscriptionFixture) path(suffix string) string {
	return "/sub/" + fixture.token + suffix
}

func (fixture *f5SubscriptionFixture) deviceCount(t *testing.T) int64 {
	t.Helper()
	results := fixture.sqlite.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM devices WHERE customer_id=? AND revoked=0`, Args: []any{fixture.customerID}})
	if len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("device count rows=%#v", results)
	}
	value, ok := f5Int64(results[0].Rows[0]["n"])
	if !ok {
		t.Fatalf("device count value=%#v", results[0].Rows[0]["n"])
	}
	return value
}

func f5SeedCredential(t *testing.T, db *s4CanarySQLite, box *controlplane.SecretBox, customerID, protocol, plaintext string) {
	t.Helper()
	envelope, err := box.Seal(controlplane.SecretScope{
		OwnerType: "customer", OwnerID: customerID, Field: "credential", Kind: protocol,
	}, []byte(plaintext))
	if err != nil {
		t.Fatalf("seal %s credential: %v", protocol, err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal %s credential: %v", protocol, err)
	}
	db.must(t, rqlite.Statement{SQL: `INSERT INTO credentials(
credential_id,customer_id,protocol,secret_envelope,secret_sha256,generation,enabled,created_at_unix,updated_at_unix)
VALUES (?,?,?,?,?,1,1,2000000,2000000)`, Args: []any{
		customerID + "-" + protocol + "-credential", customerID, protocol, encoded, strings.Repeat("b", 64),
	}})
}

type f5HTTPResult struct {
	status      int
	contentType string
	body        []byte
}

func f5SubscriptionGET(t *testing.T, handler http.Handler, path, device, userAgent string) f5HTTPResult {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if device != "" {
		request.Header.Set("X-Device-Id", device)
	}
	if userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return f5HTTPResult{
		status: response.Code, contentType: response.Header().Get("Content-Type"),
		body: append([]byte(nil), response.Body.Bytes()...),
	}
}

type f5MutableClock struct {
	mu    sync.RWMutex
	value time.Time
}

func (clock *f5MutableClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.value
}

func (clock *f5MutableClock) set(value time.Time) {
	clock.mu.Lock()
	clock.value = value
	clock.mu.Unlock()
}

type f5UniqueIDs struct {
	mu   sync.Mutex
	next uint64
}

func (ids *f5UniqueIDs) NewID(prefix string) (string, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.next++
	return fmt.Sprintf("%s-%032x", prefix, ids.next), nil
}

type f5ToggleRQLite struct {
	inner             rqlite.RQLite
	mu                sync.RWMutex
	down              bool
	beforeRequest     func()
	beforeRequestOnce sync.Once
	rewriteRequest    func([]rqlite.Statement) []rqlite.Statement
	rewriteStrong     func(int, []rqlite.Result) []rqlite.Result
	strongCalls       int
}

func (database *f5ToggleRQLite) setUnavailable(unavailable bool) {
	database.mu.Lock()
	database.down = unavailable
	database.mu.Unlock()
}

func (database *f5ToggleRQLite) unavailable() bool {
	database.mu.RLock()
	defer database.mu.RUnlock()
	return database.down
}

func (database *f5ToggleRQLite) Request(ctx context.Context, consistency rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	if database.unavailable() {
		return nil, errors.New("F5 SQLite fixture: rqlite unavailable")
	}
	if database.beforeRequest != nil {
		database.beforeRequestOnce.Do(database.beforeRequest)
	}
	if database.unavailable() {
		return nil, errors.New("F5 SQLite fixture: rqlite unavailable")
	}
	if database.rewriteRequest != nil {
		statements = database.rewriteRequest(statements)
	}
	return database.inner.Request(ctx, consistency, transaction, statements...)
}

func (database *f5ToggleRQLite) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	if database.unavailable() {
		return nil, errors.New("F5 SQLite fixture: rqlite unavailable")
	}
	return database.inner.QueryLinearizable(ctx, statements...)
}

func (database *f5ToggleRQLite) QueryStrong(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	if database.unavailable() {
		return nil, errors.New("F5 SQLite fixture: rqlite unavailable")
	}
	results, err := database.inner.QueryStrong(ctx, statements...)
	if err != nil {
		return nil, err
	}
	database.strongCalls++
	if database.rewriteStrong != nil {
		results = database.rewriteStrong(database.strongCalls, results)
	}
	return results, nil
}

func (database *f5ToggleRQLite) Backup(ctx context.Context, writer io.Writer) error {
	if database.unavailable() {
		return errors.New("F5 SQLite fixture: rqlite unavailable")
	}
	return database.inner.Backup(ctx, writer)
}

func f5HasOutboundType(t *testing.T, body []byte, outboundType string) bool {
	t.Helper()
	var document struct {
		Outbounds []struct {
			Type string `json:"type"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode subscription JSON: %v", err)
	}
	for _, outbound := range document.Outbounds {
		if outbound.Type == outboundType {
			return true
		}
	}
	return false
}

func f5Int64(value any) (int64, bool) {
	switch actual := value.(type) {
	case int64:
		return actual, true
	case int:
		return int64(actual), true
	case float64:
		return int64(actual), actual == float64(int64(actual))
	case json.Number:
		parsed, err := actual.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
