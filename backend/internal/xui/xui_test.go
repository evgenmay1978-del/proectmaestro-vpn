package xui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLoginAndAddClient(t *testing.T) {
	var sawHost, sawLogin, sawAdd bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "wapmixx.ru" {
			sawHost = true
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/login"):
			sawLogin = true
			_ = r.ParseForm()
			if r.Form.Get("username") != "admin" {
				w.Write([]byte(`{"success":false,"msg":"bad creds"}`))
				return
			}
			w.Write([]byte(`{"success":true,"msg":"ok"}`))
		case strings.HasSuffix(r.URL.Path, "/clients/add"):
			sawAdd = true
			var body struct {
				Client     map[string]any `json:"client"`
				InboundIDs []int          `json:"inboundIds"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Client["email"] != "cust1" || len(body.InboundIDs) != 1 || body.InboundIDs[0] != 2 {
				t.Errorf("clients/add bad body: %+v", body)
			}
			w.Write([]byte(`{"success":true}`))
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, Host: "wapmixx.ru", Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Login(); err != nil {
		t.Fatalf("Login: %v", err)
	}
	err = c.AddClient(2, VLESSClient{ID: "uuid-1", Email: "cust1", Flow: "xtls-rprx-vision",
		Enable: true, SubID: "subtok1"})
	if err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	if !sawHost || !sawLogin || !sawAdd {
		t.Fatalf("missing calls: host=%v login=%v add=%v", sawHost, sawLogin, sawAdd)
	}
}

func TestLoginBadCreds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"success":false,"msg":"Incorrect username or password"}`))
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Host: "wapmixx.ru", Username: "x", Password: "y"})
	if err := c.Login(); err == nil {
		t.Fatal("expected login error for bad creds")
	}
}

func trustedReadOnlyTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	c, err := NewReadOnly(Config{BaseURL: server.URL, Host: "panel.example.test", Token: "synthetic-read-token"})
	if err != nil {
		t.Fatal(err)
	}
	guard, ok := c.http.Transport.(*readOnlyTransport)
	if !ok {
		t.Fatal("read-only transport guard missing")
	}
	// Trust only httptest's supplied certificate pool. Keep the production
	// wrapper, redirect policy and TLS verification; never use Insecure=true.
	guard.base = server.Client().Transport
	return c
}

func TestReadOnlyAuthenticatedHTTPSRejectsEveryRedirectBeforeDestination(t *testing.T) {
	var destinationCalls atomic.Int32
	target := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationCalls.Add(1)
		_, _ = w.Write([]byte(`{"success":true,"obj":{"email":"fixture","uuid":"fixture-uuid","subId":"fixture-sub-id"}}`))
	})
	httpsDestination := httptest.NewTLSServer(target)
	defer httpsDestination.Close()
	httpDestination := httptest.NewServer(target)
	defer httpDestination.Close()
	for _, location := range []string{httpsDestination.URL + "/redirected", httpDestination.URL + "/downgraded", "/same-origin-redirect"} {
		for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
			t.Run(http.StatusText(code)+"/"+location, func(t *testing.T) {
				var sourceCalls atomic.Int32
				source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sourceCalls.Add(1)
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer synthetic-read-token" || r.Host != "panel.example.test" {
						t.Error("initial HTTPS request lost its authenticated binding")
					}
					w.Header().Set("Location", location)
					w.WriteHeader(code)
					// A redirect cannot be mistaken for a successful lookup merely
					// because its body contains a valid-looking client record.
					_, _ = w.Write([]byte(`{"success":true,"obj":{"email":"fixture","uuid":"fixture-uuid","subId":"fixture-sub-id"}}`))
				}))
				defer source.Close()
				c := trustedReadOnlyTestClient(t, source)
				if record, err := c.GetClient("fixture"); err == nil || record != nil {
					t.Fatal("redirect was followed or accepted as a client")
				}
				if sourceCalls.Load() != 1 || destinationCalls.Load() != 0 {
					t.Fatal("authenticated capture reached a redirect destination")
				}
			})
		}
	}
}

func TestReadOnlyRequestBoundaryRejectsWritesAndForeignOrigins(t *testing.T) {
	var sourceCalls, destinationCalls atomic.Int32
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceCalls.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer synthetic-read-token" {
			t.Error("unexpected authenticated request")
		}
		_, _ = w.Write([]byte(`{"success":true,"obj":{"email":"fixture","uuid":"fixture-uuid","subId":"fixture-sub-id"}}`))
	}))
	defer source.Close()
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer destination.Close()
	c := trustedReadOnlyTestClient(t, source)
	if record, err := c.GetClient("fixture"); err != nil || record == nil || record.UUID != "fixture-uuid" {
		t.Fatal("verified same-origin GET failed")
	}
	if err := c.AddClient(1, VLESSClient{Email: "fixture"}); err == nil {
		t.Fatal("read-only client provisioned a user")
	}
	if err := c.DelClient("fixture"); err == nil {
		t.Fatal("read-only client removed a user")
	}
	if err := c.Login(); err != nil {
		t.Fatal("Bearer login should require no request")
	}
	for _, requestCase := range []struct{ method, target string }{
		{http.MethodGet, destination.URL + "/foreign"},
		{http.MethodGet, strings.Replace(source.URL, "https:", "http:", 1) + "/downgrade"},
		{http.MethodPost, source.URL + "/mutate"},
	} {
		request, err := http.NewRequest(requestCase.method, requestCase.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "panel.example.test"
		request.Header.Set("Authorization", "Bearer synthetic-read-token")
		response, err := c.http.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		if err == nil {
			t.Fatal("request escaped read-only origin/method boundary")
		}
	}
	if sourceCalls.Load() != 1 || destinationCalls.Load() != 0 {
		t.Fatal("rejected method/origin reached a server")
	}
}

func TestNewReadOnlyRequiresVerifiedHTTPSBearerConfig(t *testing.T) {
	base := Config{BaseURL: "https://panel.example.test/base", Token: "synthetic-read-token"}
	for _, change := range []func(*Config){
		func(c *Config) { c.BaseURL = "http://panel.example.test/base" },
		func(c *Config) { c.BaseURL = "https://user:secret@panel.example.test/base" },
		func(c *Config) { c.BaseURL += "?token=secret" },
		func(c *Config) { c.BaseURL += "#secret" },
		func(c *Config) { c.Insecure = true },
		func(c *Config) { c.Token = "" },
		func(c *Config) { c.Username = "admin" },
		func(c *Config) { c.Password = "secret" },
	} {
		cfg := base
		change(&cfg)
		if c, err := NewReadOnly(cfg); err == nil || c != nil {
			t.Fatal("unsafe read-only client configuration accepted")
		}
	}
}
