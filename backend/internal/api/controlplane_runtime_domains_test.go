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
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
)

// The fixture replaces only SQL reads. The real setting AEAD reader, exact-login
// resolver, API account gates and subscription/info materializers all execute.
type runtimeAPIReadDB struct {
	*exactLoginAPIReadDB
	settings map[string]map[string]any
	members  map[string][]map[string]any
	markers  map[string][]map[string]any
	wb       string
	ota      map[string]any
	otaReads int
}

func (db *runtimeAPIReadDB) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	const customerDetailSQL = `SELECT display_login,
       (SELECT COUNT(*) FROM devices d WHERE d.customer_id=customers.customer_id AND d.revoked=0 AND d.last_seen_at_unix>=?) AS device_count,
       COALESCE((SELECT MAX(d.last_seen_at_unix) FROM devices d WHERE d.customer_id=customers.customer_id AND d.revoked=0 AND d.last_seen_at_unix>=?),0) AS last_seen_at_unix
FROM customers WHERE customer_id=? LIMIT 1`
	if len(statements) == 1 && statements[0].SQL == customerDetailSQL {
		args := statements[0].Args
		cutoff := int64(2_000_000) - int64((60*24*time.Hour)/time.Second)
		if len(args) != 3 || args[0] != cutoff || args[1] != cutoff {
			return nil, errors.New("unexpected runtime customer detail binding")
		}
		for _, row := range db.rows {
			if row["customer_id"] == args[2] {
				return []rqlite.Result{{Rows: []map[string]any{row}}}, nil
			}
		}
		return []rqlite.Result{{}}, nil
	}
	const otaSQL = `SELECT c.public_value_json,s.secret_envelope,s.secret_sha256,i.secret_id,i.secret_sha256 AS source_sha256,e.lifecycle FROM cluster_settings c LEFT JOIN setting_secrets s ON s.setting_key=c.setting_key LEFT JOIN imported_secrets i ON i.owner_type='setting' AND i.owner_source_key='ota' AND i.field='secret' AND i.kind='ota' AND i.secret_id LIKE 'runtime-setting-v1:ota:%' LEFT JOIN imported_entity_state e ON e.entity_kind='encrypted_secret' AND e.source_key=i.secret_id AND e.target_id=i.secret_id WHERE c.setting_key='ota'`
	if len(statements) == 1 && statements[0].SQL == otaSQL && len(statements[0].Args) == 0 {
		db.otaReads++
		var rows []map[string]any
		if db.ota != nil {
			rows = []map[string]any{db.ota}
		}
		return []rqlite.Result{{Rows: rows}}, nil
	}
	const runtimeSQL = `SELECT c.generation,s.secret_envelope,s.secret_sha256 FROM cluster_settings c LEFT JOIN setting_secrets s ON s.setting_key=c.setting_key WHERE c.setting_key=?`
	if len(statements) == 3 && statements[0].SQL == runtimeSQL {
		key, ok := statements[0].Args[0].(string)
		if !ok || !reflect.DeepEqual(statements[0].Args, []any{key}) ||
			statements[1].SQL != `SELECT member_key,member_value_json,generation FROM setting_members WHERE setting_key=? ORDER BY member_key` || !reflect.DeepEqual(statements[1].Args, []any{key}) ||
			statements[2].SQL != `SELECT i.secret_id,i.secret_sha256,i.secret_envelope AS source_envelope,i.key_version,e.canonical_sha256 AS source_envelope_sha256,e.lifecycle FROM imported_secrets i LEFT JOIN imported_entity_state e ON e.entity_kind='encrypted_secret' AND e.source_key=i.secret_id AND e.target_id=i.secret_id WHERE i.owner_type='setting' AND i.owner_source_key=? AND i.field='secret' AND i.kind=? AND i.secret_id LIKE ? ORDER BY i.secret_id` || !reflect.DeepEqual(statements[2].Args, []any{key, key, "runtime-setting-v1:" + key + ":%"}) {
			return nil, errors.New("unexpected runtime fixture binding")
		}
		var rows []map[string]any
		if row := db.settings[key]; row != nil {
			rows = []map[string]any{row}
		}
		return []rqlite.Result{{Rows: rows}, {Rows: db.members[key]}, {Rows: db.markers[key]}}, nil
	}
	if len(statements) == 1 && statements[0].SQL == `SELECT secret_envelope FROM setting_secrets WHERE setting_key=?` && reflect.DeepEqual(statements[0].Args, []any{"wbstream"}) {
		return []rqlite.Result{{Rows: []map[string]any{{"secret_envelope": db.wb}}}}, nil
	}
	return db.exactLoginAPIReadDB.QueryLinearizable(ctx, statements...)
}

func runtimeAPIFixture(t *testing.T) (*ServiceBusiness, *runtimeAPIReadDB, controlplane.BusinessCustomer, olcconf.Config, vkturnconf.Config) {
	t.Helper()
	_, base := newExactLoginBusinessFixture(t, "wapmix", "wapmixx", "wapmix2")
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x51}, 32)}, bytes.Repeat([]byte{0x52}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db := &runtimeAPIReadDB{exactLoginAPIReadDB: base, settings: map[string]map[string]any{}, members: map[string][]map[string]any{}, markers: map[string][]map[string]any{}}
	clock := exactLoginAPIClock{}
	store, err := controlplane.NewStore(db, box, clock)
	if err != nil {
		t.Fatal(err)
	}
	service, err := controlplane.NewService(store, exactLoginAPIIDs{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	olc := olcconf.Config{Enabled: true, Provider: "telemost", Transport: "vp8channel", Room: "https://telemost.yandex.ru/j/global-fixture", Key: strings.Repeat("a", 64),
		Logins: []string{"wapmix", "wapmixx", "wapmix2"}, Rooms: map[string]olcconf.RoomKey{
			"wapmix":  {Room: "fixture-room-one", Key: strings.Repeat("b", 64), Provider: "wbstream"},
			"wapmixx": {Room: "https://telemost.yandex.ru/j/fixture-two", Key: strings.Repeat("c", 64)},
			"wapmix2": {Provider: "wbstream"}, // Global fallback remains ineligible for the transport.
		}}
	vk := vkturnconf.Config{Enabled: true, MinVersionCode: 107, Server: "turn.example.test:443", VKHashes: []string{"fixture-hash"}, Clients: map[string]vkturnconf.Client{}}
	for _, login := range vkturnconf.AllowedLogins() {
		vk.Clients[login] = vkturnconf.Client{Password: "password-" + login, WG: subgen.VKTurnCreds{PrivateKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), PeerPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), LocalAddress: "10.8.0.2/32"}}
	}
	for key, config := range map[string]any{"olcrtc": olc, "vkturn": vk} {
		rawConfig, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		doc := controlplane.LegacyRuntimeSettingDocument{SchemaVersion: 1, SettingKey: key, CapsuleSHA256: strings.Repeat("a", 64), CustomersSHA256: strings.Repeat("b", 64), ConfigJSON: rawConfig, Members: []controlplane.LegacyRuntimeMember{}}
		for _, row := range base.rows {
			login := row["display_login"].(string)
			member := controlplane.LegacyRuntimeMember{Login: login, CustomerSourceKey: row["source_key"].(string), CustomerID: row["customer_id"].(string), CustomerSHA256: row["canonical_sha256"].(string), LoginHMAC: row["login_key_hmac"].(string), MemberHMAC: box.LookupHMAC("setting-member:"+key, []byte(login))}
			doc.Members = append(doc.Members, member)
			db.members[key] = append(db.members[key], map[string]any{"member_key": member.MemberHMAC, "member_value_json": `{"enabled":true}`, "generation": int64(7)})
		}
		plain, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := box.Seal(controlplane.SecretScope{OwnerType: "setting", OwnerID: key, Field: "secret", Kind: key}, plain)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(plain)
		digest := hex.EncodeToString(sum[:])
		db.settings[key] = map[string]any{"generation": int64(7), "secret_envelope": base64.StdEncoding.EncodeToString(raw), "secret_sha256": digest}
		id := "runtime-setting-v1:" + key + ":" + doc.CapsuleSHA256
		source, _ := json.Marshal(map[string]any{"secret_id": id, "owner_type": "setting", "owner_source_key": key, "field": "secret", "kind": key, "key_version": envelope.KeyVersion, "nonce_b64": base64.StdEncoding.EncodeToString(envelope.Nonce), "ciphertext_b64": base64.StdEncoding.EncodeToString(envelope.Ciphertext), "sha256": digest})
		sourceSHA := sha256.Sum256(source)
		db.markers[key] = []map[string]any{{"secret_id": id, "secret_sha256": digest, "lifecycle": "active", "source_envelope": string(source), "key_version": int64(envelope.KeyVersion), "source_envelope_sha256": hex.EncodeToString(sourceSHA[:])}}
	}
	wb, err := box.Seal(controlplane.SecretScope{OwnerType: "setting", OwnerID: "wbstream", Field: "secret", Kind: "wbstream"}, []byte("protected-wb-account-token"))
	if err != nil {
		t.Fatal(err)
	}
	wbJSON, _ := json.Marshal(wb)
	db.wb = base64.StdEncoding.EncodeToString(wbJSON)
	customer, err := service.BusinessCustomerByLogin(context.Background(), "wapmix")
	if err != nil {
		t.Fatal(err)
	}
	business := NewServiceBusiness(service, ServiceBusinessConfig{Now: clock.Now, SubscriptionTopology: subscriptionRequestTopology()})
	business.subscriptionStates = nil
	business.subscriptions = subscriptionRequestSource{customer: customer}
	return business, db, customer, olc, vk
}

func TestRuntimeDomainsAuthenticateAndMaterializeExactClientGates(t *testing.T) {
	for _, tc := range []struct {
		name, login, platform, ua string
		active, olc, vk           bool
	}{
		{"mobile", "wapmix", "mobile", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)", true, true, true},
		{"tv", "wapmix", "tv", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)", true, true, false},
		{"old", "wapmix", "mobile", "SFA/1.0.106 (106; sing-box 1.14)", true, true, false},
		{"stock", "wapmix", "mobile", "curl/8", true, true, false},
		{"unknown-platform", "wapmix", "", "SFA/1.0.157 (157; sing-box 1.14)", true, true, false},
		{"case-sibling", "WAPMIX", "mobile", "SFA/1.0.157 (157; sing-box 1.14)", true, false, false},
		{"global-not-dedicated", "wapmix2", "mobile", "SFA/1.0.157 (157; sing-box 1.14)", true, false, true},
		{"expired", "wapmix", "mobile", "SFA/1.0.157 (157; sing-box 1.14)", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			business, db, customer, olc, vk := runtimeAPIFixture(t)
			if tc.login != "WAPMIX" {
				var err error
				customer, err = business.service.BusinessCustomerByLogin(context.Background(), tc.login)
				if err != nil {
					t.Fatal(err)
				}
			}
			customer.Login = tc.login
			if !tc.active {
				customer.ExpiresAtUnix = 1
			}
			business.subscriptions = subscriptionRequestSource{customer: customer}
			handler := NewControlPlane(business, Config{}).Handler()
			request := httptest.NewRequest(http.MethodGet, "/sub/fixture-token/info?platform="+tc.platform, nil)
			request.Header.Set("User-Agent", tc.ua)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("info status=%d", response.Code)
			}
			var info map[string]json.RawMessage
			if json.Unmarshal(response.Body.Bytes(), &info) != nil {
				t.Fatal("invalid info JSON")
			}
			if (info["olcrtc"] != nil) != tc.olc || (info["vk_turn"] != nil) != tc.vk {
				t.Fatal("account/platform gates differ")
			}
			if tc.olc {
				var value map[string]string
				_ = json.Unmarshal(info["olcrtc"], &value)
				room, key, _ := olc.RoomFor(tc.login)
				if value["room"] != room || value["key"] != key || value["provider"] != olc.ProviderFor(tc.login) {
					t.Fatal("OLC tuple changed")
				}
			}
			if tc.vk {
				var value map[string]any
				_ = json.Unmarshal(info["vk_turn"], &value)
				if value["password"] != vk.Clients[tc.login].Password || value["server"] != vk.Server {
					t.Fatal("VK identity changed")
				}
			}
			if bytes.Contains(response.Body.Bytes(), []byte("protected-wb-account-token")) {
				t.Fatal("WB account token reached client")
			}
			if tc.active {
				request = httptest.NewRequest(http.MethodGet, "/sub/fixture-token?platform="+tc.platform, nil)
				request.Header.Set("User-Agent", tc.ua)
				response = httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("subscription status=%d", response.Code)
				}
				var document struct {
					Outbounds []struct {
						Tag string `json:"tag"`
					}
					Endpoints []struct {
						Tag string `json:"tag"`
					}
				}
				if json.Unmarshal(response.Body.Bytes(), &document) != nil {
					t.Fatal("invalid subscription")
				}
				hasOLC, hasVK := false, false
				for _, v := range document.Outbounds {
					hasOLC = hasOLC || v.Tag == "olcrtc"
				}
				for _, v := range document.Endpoints {
					hasVK = hasVK || v.Tag == "vk-turn"
				}
				if hasOLC != tc.olc || hasVK != tc.vk {
					t.Fatal("info/subscription gates differ")
				}
			}
			if db.requests != 0 {
				t.Fatal("runtime read mutated data")
			}
		})
	}
}

func TestRuntimeDomainsKeepAuthenticatedProjectionInCacheAndRejectDrift(t *testing.T) {
	business, db, customer, _, _ := runtimeAPIFixture(t)
	now := time.Unix(2_000_000, 0)
	source := newSubscriptionReviewSource(now, 1, true, "11111111-1111-4111-8111-111111111111")
	source.state.Customer = customer
	source.state.SettingsGeneration = 7
	business.subscriptionStates = source
	options := subscriptionRenderOptions{Endpoint: subscriptionEndpointInfo, Platform: "mobile", UserAgent: "SFA/1.0.157 (157; sing-box 1.14)"}
	warm, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options)
	if err != nil || len(warm.RuntimeInfo) == 0 || warm.RuntimeIdentity.Digest == "" {
		t.Fatalf("warm runtime projection: %v", err)
	}
	want := append([]byte(nil), warm.RuntimeInfo...)
	warm.RuntimeInfo[0] = '!'
	source.snapshotErr = controlplane.ErrUnavailable
	cached, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options)
	if err != nil || !cached.Cached || !bytes.Equal(cached.RuntimeInfo, want) {
		t.Fatal("outage lost exact copied projection")
	}
	other := options
	other.Platform = "tv"
	if _, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", other); !errors.Is(err, controlplane.ErrUnavailable) {
		t.Fatal("mobile cache reused for TV")
	}
	source.snapshotErr = nil
	db.members["olcrtc"] = db.members["olcrtc"][:2]
	if _, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options); !errors.Is(err, controlplane.ErrUnavailable) {
		t.Fatal("member drift fell back to cached credentials")
	}
	source.snapshotErr = controlplane.ErrUnavailable
	if _, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options); !errors.Is(err, controlplane.ErrUnavailable) {
		t.Fatal("invalidated runtime credentials resurrected")
	}
}

func TestRuntimeDomainsRedactionAndExistingWBSecretReader(t *testing.T) {
	business, _, _, _, _ := runtimeAPIFixture(t)
	for _, key := range []string{"olcrtc", "vkturn"} {
		value, err := business.service.ReadLegacyRuntimeSetting(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		var view any
		if key == "olcrtc" {
			view, err = runtimeOLCView(value)
		} else {
			view, err = runtimeVKView(value)
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(view)
		for _, secret := range []string{strings.Repeat("b", 64), "password-wapmix", "private_key", "protected-wb-account-token"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("runtime panel view exposed secret")
			}
		}
	}
	called := false
	sender, err := NewWBRoomSender(business.service, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		called = true
		if request.Method != http.MethodPost || request.URL.String() != wbRoomProviderURL || request.Header.Get("Authorization") != "Bearer protected-wb-account-token" {
			t.Fatal("existing WB contract changed")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"roomId":"fixture-room"}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sender.Post(context.Background(), []byte(`{"login":"wapmix"}`)); err != nil || !called {
		t.Fatalf("protected WB materialization: %v", err)
	}
}

func TestRuntimeDomainsOTAAbsenceRequiresAuthenticatedEvidence(t *testing.T) {
	business, db, _, _, _ := runtimeAPIFixture(t)
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x51}, 32)}, bytes.Repeat([]byte{0x52}, 32))
	if err != nil {
		t.Fatal(err)
	}
	evidence := controlplane.LegacyRuntimeOTAAbsence{SchemaVersion: 1, ObservedAt: time.Unix(2_000_000, 0), Directory: "/var/lib/maestro/ota", State: "absent", RuntimeCapsuleSHA256: strings.Repeat("c", 64),
		Process: controlplane.LegacyRuntimeProcess{PID: 12, StartTicks: 34, ExecutableSHA256: strings.Repeat("a", 64), EnvironmentSHA256: strings.Repeat("b", 64), CustomersSHA256: strings.Repeat("d", 64)}}
	plain, _ := json.Marshal(evidence)
	sum := sha256.Sum256(plain)
	digest := hex.EncodeToString(sum[:])
	envelope, err := box.Seal(controlplane.SecretScope{OwnerType: "setting", OwnerID: "ota", Field: "secret", Kind: "ota"}, plain)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(envelope)
	public, _ := json.Marshal(map[string]string{"state": "absent", "source_sha256": digest})
	db.ota = map[string]any{"public_value_json": string(public), "secret_envelope": base64.StdEncoding.EncodeToString(raw), "secret_sha256": digest, "source_sha256": digest, "secret_id": "runtime-setting-v1:ota:" + digest, "lifecycle": "active"}
	handler := NewControlPlane(business, Config{UpdateDir: t.TempDir()}).Handler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/update/update.json", nil))
	if response.Code != http.StatusNotFound || db.otaReads != 1 {
		t.Fatalf("authenticated absence status=%d reads=%d", response.Code, db.otaReads)
	}
	db.ota["secret_envelope"] = db.settings["olcrtc"]["secret_envelope"]
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/update/update.json", nil))
	if response.Code != http.StatusServiceUnavailable || db.otaReads != 2 {
		t.Fatalf("forged absence status=%d reads=%d", response.Code, db.otaReads)
	}
	if db.requests != 0 {
		t.Fatal("OTA absence lookup mutated data")
	}
}
