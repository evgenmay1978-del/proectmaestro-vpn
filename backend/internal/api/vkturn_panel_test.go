package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnprobe"
	"golang.org/x/crypto/bcrypt"
)

// TestVKTurnRedactedNeverLeaksSecrets is the security-critical assertion for the
// panel editor: the browser-facing view must expose only presence booleans for the
// per-login password and WG private key, never their values — while still returning
// the non-secret fields (server, hashes, peer public key, tunnel address).
func TestVKTurnRedactedNeverLeaksSecrets(t *testing.T) {
	vkStore := vkturnconf.NewInMemory()
	if err := vkStore.Set(validVKTurnConfig()); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	s := New(nil, nil, nil, nil, Config{VKTurn: vkStore})

	r := s.vkTurnRedacted()
	if r["configured"] != true || r["enabled"] != true {
		t.Fatalf("redacted top-level wrong: %#v", r)
	}
	clients, ok := r["clients"].(map[string]any)
	if !ok {
		t.Fatalf("clients missing: %#v", r)
	}
	for _, login := range []string{"wapmix", "wapmixx", "wapmix2"} {
		cl, ok := clients[login].(map[string]any)
		if !ok {
			t.Fatalf("client %q missing: %#v", login, clients)
		}
		if _, present := cl["password"]; present {
			t.Errorf("client %q: plaintext password leaked into redacted view", login)
		}
		if cl["password_set"] != true {
			t.Errorf("client %q: password_set should be true", login)
		}
		wg, ok := cl["wg"].(map[string]any)
		if !ok {
			t.Fatalf("client %q wg missing", login)
		}
		if _, present := wg["private_key"]; present {
			t.Errorf("client %q: WG private_key leaked into redacted view", login)
		}
		if wg["private_key_set"] != true {
			t.Errorf("client %q: private_key_set should be true", login)
		}
		if v, _ := wg["peer_public_key"].(string); v == "" {
			t.Errorf("client %q: peer_public_key (non-secret) should be shown", login)
		}
		if v, _ := wg["local_address"].(string); v == "" {
			t.Errorf("client %q: local_address should be shown", login)
		}
	}
}

// TestVKTurnRedactedUnconfigured reports configured=false (feature off) without panicking
// when the store has no config yet.
func TestVKTurnRedactedUnconfigured(t *testing.T) {
	s := New(nil, nil, nil, nil, Config{VKTurn: vkturnconf.NewInMemory()})
	r := s.vkTurnRedacted()
	if r["configured"] != false {
		t.Fatalf("unconfigured store should report configured=false: %#v", r)
	}
	if _, present := r["clients"]; present {
		t.Fatal("no clients should be exposed when unconfigured")
	}
}

// TestApplyVKTurnEditBlankKeepsSecrets unit-tests the merge: a blank/absent secret keeps
// the existing value; a provided non-secret field replaces it; first-time setup on nil.
func TestApplyVKTurnEditBlankKeepsSecrets(t *testing.T) {
	cur := validVKTurnConfig() // secrets set, server "wdtt.example:443"
	oldHash := cur.VKHashes[0]
	sp := func(s string) *string { return &s }
	var req vkTurnSaveReq
	req.Server = sp("newhost:56000")
	req.Clients = map[string]vkTurnClientReq{}
	for _, l := range []string{"wapmix", "wapmixx", "wapmix2"} {
		c := vkTurnClientReq{Password: sp("")} // blank → keep
		c.WG.PrivateKey = sp("")               // blank → keep
		req.Clients[l] = c
	}
	got := applyVKTurnEdit(cur, req)
	if got.Server != "newhost:56000" || got.VKHashes[0] != oldHash {
		t.Fatalf("non-secret fields not applied: %+v", got)
	}
	if got.Clients["wapmix"].Password != "pass-wapmix" {
		t.Fatalf("blank password overwrote the kept secret: %q", got.Clients["wapmix"].Password)
	}
	if got.Clients["wapmix"].WG.PrivateKey != vkTurnTestKey {
		t.Fatalf("blank private_key overwrote the kept secret")
	}
	// First-time setup: nil cur must not panic and must produce a Clients map.
	fresh := applyVKTurnEdit(nil, vkTurnSaveReq{Server: sp("h:1")})
	if fresh == nil || fresh.Server != "h:1" || fresh.Clients == nil {
		t.Fatalf("first-time merge wrong: %+v", fresh)
	}
}

// --- panel session helpers (login → cookie + CSRF, then authenticated POST) ---

type vkTurnProberFunc func(context.Context, []string) vkturnprobe.Result

func (f vkTurnProberFunc) Probe(ctx context.Context, hashes []string) vkturnprobe.Result {
	return f(ctx, hashes)
}

type controlledVKTurnProber struct {
	started chan []string
	release chan vkturnprobe.Result
	calls   atomic.Int32
}

func (p *controlledVKTurnProber) Probe(ctx context.Context, hashes []string) vkturnprobe.Result {
	p.calls.Add(1)
	copyOfHashes := append([]string(nil), hashes...)
	select {
	case p.started <- copyOfHashes:
	case <-ctx.Done():
		return vkturnprobe.Result{Stage: "STARTING", Code: "PROBE_TIMEOUT"}
	}
	select {
	case result := <-p.release:
		return result
	case <-ctx.Done():
		return vkturnprobe.Result{Stage: "STARTING", Code: "PROBE_TIMEOUT"}
	}
}

func newPanelVKTurnServer(t *testing.T, cfg *VKTurnConfig) (*httptest.Server, *vkturnconf.Store) {
	return newPanelVKTurnServerWithProber(t, cfg, nil)
}

func newPanelVKTurnServerWithProber(t *testing.T, cfg *VKTurnConfig, prober VKTurnProber) (*httptest.Server, *vkturnconf.Store) {
	t.Helper()
	vkStore := vkturnconf.NewInMemory()
	if cfg != nil {
		if err := vkStore.Set(cfg); err != nil {
			t.Fatalf("seed store: %v", err)
		}
	}
	return newPanelVKTurnServerWithStoreAndProber(t, vkStore, prober), vkStore
}

func newPanelVKTurnServerWithStoreAndProber(t *testing.T, vkStore *vkturnconf.Store, prober VKTurnProber) *httptest.Server {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("testpw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, nil, nil, Config{
		PanelPath: "/mp/", PanelPasswordHash: string(hash), VKTurn: vkStore, VKTurnProber: prober,
	})
	return httptest.NewServer(s.Handler())
}

func panelLogin(t *testing.T, srv *httptest.Server) (cookie, csrf string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": "testpw"})
	resp, err := http.Post(srv.URL+"/mp/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("panel login status = %d", resp.StatusCode)
	}
	var out struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "mp_session" {
			cookie = c.Value
		}
	}
	if cookie == "" || out.CSRF == "" {
		t.Fatalf("login missing session/csrf: cookie=%q csrf=%q", cookie, out.CSRF)
	}
	return cookie, out.CSRF
}

func panelPost(t *testing.T, srv *httptest.Server, path, cookie, csrf string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mp/"+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF", csrf)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "mp_session", Value: cookie})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestPanelVKTurnSaveMergeToggleAndCSRF(t *testing.T) {
	srv, vkStore := newPanelVKTurnServer(t, validVKTurnConfig()) // enabled, secrets set
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)
	oldHash := vkStore.Get().VKHashes[0]

	// Save with BLANK secrets + changed server/hash/min-version → secrets kept, fields applied.
	clients := map[string]any{}
	addrs := map[string]string{"wapmix": "10.77.0.2/32", "wapmixx": "10.77.0.3/32", "wapmix2": "10.77.0.4/32"}
	for l, a := range addrs {
		clients[l] = map[string]any{"password": "", "wg": map[string]any{
			"private_key": "", "peer_public_key": vkTurnTestKey, "local_address": a,
		}}
	}
	resp := panelPost(t, srv, "api/vkturn", cookie, csrf, map[string]any{
		"min_version_code": 156, "server": "newhost:56000", "clients": clients,
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("save status = %d: %s", resp.StatusCode, b)
	}
	_ = resp.Body.Close()
	got := vkStore.Get()
	if got.Server != "newhost:56000" || got.VKHashes[0] != oldHash || got.MinVersionCode != 156 {
		t.Fatalf("save did not apply public fields: %+v", got)
	}
	if got.Clients["wapmix"].Password != "pass-wapmix" || got.Clients["wapmix"].WG.PrivateKey != vkTurnTestKey {
		t.Fatalf("blank secret overwrote kept value: %+v", got.Clients["wapmix"])
	}
	if !got.Enabled {
		t.Fatal("save must NOT flip enabled (no enabled field sent)")
	}

	// Master switch OFF.
	resp2 := panelPost(t, srv, "api/vkturn/enabled", cookie, csrf, map[string]any{"enabled": false})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("toggle status = %d", resp2.StatusCode)
	}
	_ = resp2.Body.Close()
	if vkStore.Get().Enabled {
		t.Fatal("toggle did not disable the transport")
	}

	// Missing CSRF on a write → 403 (proves the mutating routes are CSRF-guarded).
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mp/api/vkturn/enabled", strings.NewReader(`{"enabled":true}`))
	req.AddCookie(&http.Cookie{Name: "mp_session", Value: cookie})
	r3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r3.Body.Close()
	if r3.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF must be 403, got %d", r3.StatusCode)
	}
}

func TestPanelVKTurnRejectsDirectHashSaveBypass(t *testing.T) {
	srv, vkStore := newPanelVKTurnServer(t, validVKTurnConfig())
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)
	oldHash := vkStore.Get().VKHashes[0]

	resp := panelPost(t, srv, "api/vkturn", cookie, csrf, map[string]any{
		"vk_hashes": []string{"candidateMustBeProbed"},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("direct active-room mutation must be rejected, got %d", resp.StatusCode)
	}
	if got := vkStore.Get().VKHashes[0]; got != oldHash {
		t.Fatalf("rejected direct save changed active hash: got %q", got)
	}
}

func TestPanelVKTurnCandidateIsolatedUntilProbeThenPromoted(t *testing.T) {
	prober := &controlledVKTurnProber{
		started: make(chan []string, 1),
		release: make(chan vkturnprobe.Result, 1),
	}
	srv, vkStore := newPanelVKTurnServerWithProber(t, validVKTurnConfig(), prober)
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)
	oldHash := vkStore.Get().VKHashes[0]
	const candidate = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq"

	type asyncResult struct {
		resp *http.Response
		err  error
	}
	responseCh := make(chan asyncResult, 1)
	go func() {
		body, _ := json.Marshal(map[string]any{
			"vk_hashes": []string{"https://vk.ru/call/join/" + candidate},
		})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mp/api/vkturn/candidate", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF", csrf)
		req.AddCookie(&http.Cookie{Name: "mp_session", Value: cookie})
		resp, err := http.DefaultClient.Do(req)
		responseCh <- asyncResult{resp: resp, err: err}
	}()

	select {
	case hashes := <-prober.started:
		if len(hashes) != 1 || hashes[0] != candidate {
			t.Fatalf("prober received %#v", hashes)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("candidate probe did not start")
	}
	during := vkStore.Get()
	if during.VKHashes[0] != oldHash || len(during.CandidateVKHashes) != 1 || during.CandidateVKHashes[0] != candidate || during.ProbeStatus != vkturnconf.ProbeStatusChecking {
		t.Fatalf("candidate was not isolated from active config: %+v", during)
	}

	second := panelPost(t, srv, "api/vkturn/candidate", cookie, csrf, map[string]any{
		"vk_hashes": []string{"secondConcurrentCandidate"},
	})
	_ = second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second concurrent candidate status = %d, want 409", second.StatusCode)
	}
	if calls := prober.calls.Load(); calls != 1 {
		t.Fatalf("concurrent request started %d probes, want 1", calls)
	}

	prober.release <- vkturnprobe.Result{OK: true, Stage: "TURN_ALLOCATED", Code: "OK"}
	select {
	case result := <-responseCh:
		if result.err != nil {
			t.Fatal(result.err)
		}
		defer func() { _ = result.resp.Body.Close() }()
		if result.resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(result.resp.Body)
			t.Fatalf("candidate status = %d: %s", result.resp.StatusCode, b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("candidate request did not finish")
	}
	after := vkStore.Get()
	if len(after.VKHashes) != 1 || after.VKHashes[0] != candidate || len(after.CandidateVKHashes) != 0 || after.LastKnownGoodVKHashes[0] != candidate || after.ProbeStatus != vkturnconf.ProbeStatusActive {
		t.Fatalf("successful candidate was not promoted atomically: %+v", after)
	}
}

func TestPanelVKTurnCandidateFailureRetainsActiveAndRedactsStatus(t *testing.T) {
	const candidate = "candidateProviderFailure"
	prober := vkTurnProberFunc(func(context.Context, []string) vkturnprobe.Result {
		return vkturnprobe.Result{OK: false, Stage: "TLS", Code: "TLS_TRUST_FAILED"}
	})
	srv, vkStore := newPanelVKTurnServerWithProber(t, validVKTurnConfig(), prober)
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)
	oldHash := vkStore.Get().VKHashes[0]

	resp := panelPost(t, srv, "api/vkturn/candidate", cookie, csrf, map[string]any{
		"vk_hashes": []string{candidate},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("failed candidate status = %d: %s", resp.StatusCode, b)
	}
	after := vkStore.Get()
	if after.VKHashes[0] != oldHash || after.LastKnownGoodVKHashes[0] != oldHash || len(after.CandidateVKHashes) != 0 || after.ProbeStatus != vkturnconf.ProbeStatusFailed || after.ProbeErrorCode != "TLS_TRUST_FAILED" {
		t.Fatalf("failed candidate changed active/LKG or lost safe status: %+v", after)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{oldHash, candidate, "pass-wapmix"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("candidate response leaked protected value %q", forbidden)
		}
	}
}

func TestVKTurnRedactedContainsOnlyRoomCountsAndSafeProbeState(t *testing.T) {
	vkStore := vkturnconf.NewInMemory()
	if err := vkStore.Set(validVKTurnConfig()); err != nil {
		t.Fatal(err)
	}
	const candidate = "candidateRedactionMarker"
	if _, err := vkStore.StageCandidate([]string{candidate}, time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	s := New(nil, nil, nil, nil, Config{VKTurn: vkStore})
	out := s.vkTurnRedacted()
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	activeHash := validVKTurnConfig().VKHashes[0]
	for _, forbidden := range []string{activeHash, candidate, "pass-wapmix", "vk_hashes", "candidate_vk_hashes", "last_known_good_vk_hashes"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("redacted status leaked %q: %s", forbidden, encoded)
		}
	}
	if out["probe_status"] != string(vkturnconf.ProbeStatusChecking) || out["active_count"] != 1 || out["candidate_count"] != 1 || out["last_known_good_count"] != 1 {
		t.Fatalf("redacted probe status/counts missing: %#v", out)
	}
}

func TestPanelVKTurnCandidateRequiresCSRFAndMissingRunnerFailsClosed(t *testing.T) {
	srv, vkStore := newPanelVKTurnServer(t, validVKTurnConfig())
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)
	oldHash := vkStore.Get().VKHashes[0]

	missingCSRF := panelPost(t, srv, "api/vkturn/candidate", cookie, "", map[string]any{
		"vk_hashes": []string{"candidateWithoutCSRF"},
	})
	_ = missingCSRF.Body.Close()
	if missingCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("candidate without CSRF status = %d", missingCSRF.StatusCode)
	}

	resp := panelPost(t, srv, "api/vkturn/candidate", cookie, csrf, map[string]any{
		"vk_hashes": []string{"candidateWithoutRunner"},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("missing runner should be a recorded failed candidate, got %d", resp.StatusCode)
	}
	after := vkStore.Get()
	if after.VKHashes[0] != oldHash || after.ProbeStatus != vkturnconf.ProbeStatusFailed || after.ProbeErrorCode != "PROBE_UNAVAILABLE" {
		t.Fatalf("missing runner did not fail closed: %+v", after)
	}
}


func TestPanelVKTurnRejectsCanaryVersionCodeAndKeepsPrevious(t *testing.T) {
	srv, vkStore := newPanelVKTurnServer(t, validVKTurnConfig())
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)

	resp := panelPost(t, srv, "api/vkturn", cookie, csrf, map[string]any{"min_version_code": 90181})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("canary versionCode must be rejected by the production panel, got %d", resp.StatusCode)
	}
	if got := vkStore.Get().MinVersionCode; got != 200 {
		t.Fatalf("rejected canary versionCode clobbered the live config: got %d want 200", got)
	}
}

func TestPanelVKTurnRejectsPartialAndGuardsUnconfiguredToggle(t *testing.T) {
	srv, _ := newPanelVKTurnServer(t, nil) // unconfigured (in-memory, empty)
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)

	// A partial first-time save fails validation → 400 (fail-closed, nothing persisted).
	resp := panelPost(t, srv, "api/vkturn", cookie, csrf, map[string]any{"server": "h:1"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("partial save should be 400, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Enabling before a full config exists → 400 with an actionable message.
	resp2 := panelPost(t, srv, "api/vkturn/enabled", cookie, csrf, map[string]any{"enabled": true})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("toggle on unconfigured should be 400, got %d", resp2.StatusCode)
	}
	_ = resp2.Body.Close()

	// Unauthenticated GET → 401 (session required even to read the redacted view).
	r3, err := http.Get(srv.URL + "/mp/api/vkturn")
	if err != nil {
		t.Fatal(err)
	}
	_ = r3.Body.Close()
	if r3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET should be 401, got %d", r3.StatusCode)
	}
}
func TestApplyVKTurnEditCannotMutateActiveWithFullVKCallURL(t *testing.T) {
	cur := validVKTurnConfig()
	oldHash := cur.VKHashes[0]
	const hash = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq"
	got := applyVKTurnEdit(cur, vkTurnSaveReq{
		VKHashes: []string{"https://vk.ru/call/join/" + hash},
	})
	if len(got.VKHashes) != 1 || got.VKHashes[0] != oldHash {
		t.Fatalf("full save bypass changed active hash: %#v", got.VKHashes)
	}
}

func TestPanelVKTurnCanBootstrapDisabledBaseBeforeVerifiedCandidate(t *testing.T) {
	prober := vkTurnProberFunc(func(context.Context, []string) vkturnprobe.Result {
		return vkturnprobe.Result{OK: true, Stage: "TURN_ALLOCATED", Code: "OK"}
	})
	srv, vkStore := newPanelVKTurnServerWithProber(t, nil, prober)
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)

	clients := map[string]any{}
	for i, login := range []string{"wapmix", "wapmixx", "wapmix2"} {
		clients[login] = map[string]any{
			"password": "bootstrap-" + login,
			"wg": map[string]any{
				"private_key":    vkTurnTestKey,
				"peer_public_key": vkTurnTestKey,
				"local_address":   "10.88.0." + string(rune('2'+i)) + "/32",
			},
		}
	}
	baseResp := panelPost(t, srv, "api/vkturn", cookie, csrf, map[string]any{
		"min_version_code": 156,
		"server":           "bootstrap.example:56000",
		"clients":          clients,
	})
	defer func() { _ = baseResp.Body.Close() }()
	if baseResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(baseResp.Body)
		t.Fatalf("bootstrap base status = %d: %s", baseResp.StatusCode, body)
	}
	base := vkStore.Get()
	if base == nil || base.Enabled || len(base.VKHashes) != 0 {
		t.Fatalf("bootstrap base must stay disabled without an active room: %+v", base)
	}

	const candidate = "bootstrapCandidateHash"
	candidateResp := panelPost(t, srv, "api/vkturn/candidate", cookie, csrf, map[string]any{
		"vk_hashes": []string{candidate},
	})
	defer func() { _ = candidateResp.Body.Close() }()
	if candidateResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(candidateResp.Body)
		t.Fatalf("bootstrap candidate status = %d: %s", candidateResp.StatusCode, body)
	}
	after := vkStore.Get()
	if after.Enabled || after.ProbeStatus != vkturnconf.ProbeStatusActive || !reflect.DeepEqual(after.VKHashes, []string{candidate}) {
		t.Fatalf("bootstrap candidate was not promoted while disabled: %+v", after)
	}
}

func TestPanelHTMLUsesVerifiedWriteOnlyCandidateFlow(t *testing.T) {
	if !strings.Contains(panelHTML, "api/vkturn/candidate") || !strings.Contains(panelHTML, "Проверить и применить") {
		t.Fatal("WDTT panel does not expose the verified candidate action")
	}
	for _, forbidden := range []string{"o.vk_hashes", `id="w_hashes"`, "vk_hashes:hashes,clients"} {
		if strings.Contains(panelHTML, forbidden) {
			t.Fatalf("WDTT panel still contains direct active-room mutation marker %q", forbidden)
		}
	}
}

func TestPanelVKTurnCandidateMapsInvalidAndPersistenceErrors(t *testing.T) {
	checkingPath := filepath.Join(t.TempDir(), "checking.json")
	checkingStore, err := vkturnconf.OpenStore(checkingPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkingStore.Set(validVKTurnConfig()); err != nil {
		t.Fatal(err)
	}
	if _, err := checkingStore.StageCandidate([]string{"candidate-room-a"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	restarted, err := vkturnconf.OpenStore(checkingPath)
	if err != nil {
		t.Fatal(err)
	}
	invalidCalls := atomic.Int32{}
	invalidServer := newPanelVKTurnServerWithStoreAndProber(t, restarted, vkTurnProberFunc(func(context.Context, []string) vkturnprobe.Result {
		invalidCalls.Add(1)
		return vkturnprobe.Result{OK: true, Stage: "TURN_ALLOCATED", Code: "OK"}
	}))
	defer invalidServer.Close()
	cookie, csrf := panelLogin(t, invalidServer)
	invalidResp := panelPost(t, invalidServer, "api/vkturn/candidate", cookie, csrf, map[string]any{"vk_hashes": []string{"short"}})
	defer func() { _ = invalidResp.Body.Close() }()
	if invalidResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid candidate during recoverable persisted checking state = %d, want 400", invalidResp.StatusCode)
	}
	if invalidCalls.Load() != 0 {
		t.Fatal("invalid candidate reached provider probe")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "vkturn.json")
	persistStore, err := vkturnconf.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistStore.Set(validVKTurnConfig()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	persistServer := newPanelVKTurnServerWithStoreAndProber(t, persistStore, nil)
	defer persistServer.Close()
	persistCookie, persistCSRF := panelLogin(t, persistServer)
	persistResp := panelPost(t, persistServer, "api/vkturn/candidate", persistCookie, persistCSRF, map[string]any{"vk_hashes": []string{"candidate-room-b"}})
	defer func() { _ = persistResp.Body.Close() }()
	if persistResp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("candidate persistence failure = %d, want 500", persistResp.StatusCode)
	}
	if got := persistStore.Get(); got == nil || got.ProbeStatus != vkturnconf.ProbeStatusActive {
		t.Fatalf("persistence failure changed live state: %+v", got)
	}
}

func TestPanelHTMLSeparatesActiveRoomFromLastProbeResult(t *testing.T) {
	for _, required := range []string{"активная комната есть", "активной комнаты нет", "последняя проверка: ошибка"} {
		if !strings.Contains(panelHTML, required) {
			t.Fatalf("WDTT panel lacks distinct status marker %q", required)
		}
	}
	if strings.Contains(panelHTML, "комната: ошибка") {
		t.Fatal("WDTT panel still conflates a failed probe with active-room health")
	}
}
