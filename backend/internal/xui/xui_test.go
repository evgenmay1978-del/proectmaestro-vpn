package xui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
