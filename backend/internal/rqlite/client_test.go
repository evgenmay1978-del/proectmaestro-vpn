package rqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestRejectsStatementErrorInsideHTTP200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("transaction"); got != "true" {
			t.Errorf("transaction = %q, want true", got)
		}
		_, _ = io.WriteString(w, `{"results":[{"rows_affected":1},{"error":"constraint failed"}]}`)
	}))
	defer srv.Close()

	c := mustClient(t, Config{Endpoints: []string{srv.URL}})
	_, err := c.Request(context.Background(), Linearizable, true,
		Statement{SQL: "INSERT INTO a VALUES(?)", Args: []any{1}},
		Statement{SQL: "UPDATE b SET x=?", Args: []any{2}},
	)
	var stmtErr *StatementError
	if !errors.As(err, &stmtErr) {
		t.Fatalf("error = %#v, want *StatementError", err)
	}
	if stmtErr.Index != 1 || stmtErr.Message != "constraint failed" {
		t.Fatalf("statement error = %#v", stmtErr)
	}
	if strings.Contains(err.Error(), "UPDATE b") {
		t.Fatalf("error leaked SQL: %v", err)
	}
}

func TestRequestUsesUnifiedEndpointAndNeverQueues(t *testing.T) {
	type captured struct {
		method      string
		path        string
		query       url.Values
		contentType string
		body        [][]any
	}
	requests := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body [][]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		requests <- captured{
			method:      r.Method,
			path:        r.URL.Path,
			query:       r.URL.Query(),
			contentType: r.Header.Get("Content-Type"),
			body:        body,
		}
		_, _ = io.WriteString(w, `{"results":[{"rows_affected":1}]}`)
	}))
	defer srv.Close()

	c := mustClient(t, Config{Endpoints: []string{srv.URL}})
	results, err := c.Request(context.Background(), Linearizable, true,
		Statement{SQL: "INSERT INTO customers(id, name) VALUES(?, ?)", Args: []any{7, "alice"}},
	)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if len(results) != 1 || results[0].RowsAffected != 1 {
		t.Fatalf("results = %#v", results)
	}

	got := <-requests
	if got.method != http.MethodPost || got.path != "/db/request" {
		t.Fatalf("request = %s %s", got.method, got.path)
	}
	if got.query.Get("associative") != "true" || got.query.Get("level") != "linearizable" {
		t.Fatalf("query = %v", got.query)
	}
	if got.query.Get("transaction") != "true" {
		t.Fatalf("transaction missing: %v", got.query)
	}
	if got.query.Has("queue") || got.query.Has("redirect") {
		t.Fatalf("unsafe query flags: %v", got.query)
	}
	if got.contentType != "application/json" {
		t.Fatalf("Content-Type = %q", got.contentType)
	}
	if len(got.body) != 1 || len(got.body[0]) != 3 ||
		got.body[0][0] != "INSERT INTO customers(id, name) VALUES(?, ?)" ||
		got.body[0][1] != float64(7) || got.body[0][2] != "alice" {
		t.Fatalf("body = %#v", got.body)
	}
}

func TestRequestOmitsTransactionWhenNotRequested(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("transaction") {
			t.Errorf("unexpected transaction flag: %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"results":[{"rows_affected":1}]}`)
	}))
	defer srv.Close()

	c := mustClient(t, Config{Endpoints: []string{srv.URL}})
	if _, err := c.Request(context.Background(), Linearizable, false,
		Statement{SQL: "UPDATE customers SET active=1"}); err != nil {
		t.Fatalf("Request: %v", err)
	}
}

func TestWriteUnknownOutcomeIsNotRetriedByClient(t *testing.T) {
	var firstCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("response writer does not support hijacking")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer first.Close()

	var secondCalls atomic.Int32
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondCalls.Add(1)
		_, _ = io.WriteString(w, `{"results":[{"rows_affected":1}]}`)
	}))
	defer second.Close()

	c := mustClient(t, Config{Endpoints: []string{first.URL, second.URL}})
	_, err := c.Request(context.Background(), Linearizable, true,
		Statement{SQL: "INSERT INTO operations(id) VALUES(?)", Args: []any{"op-1"}},
	)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) || !transportErr.UnknownOutcome {
		t.Fatalf("error = %#v, want unknown-outcome TransportError", err)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 0 {
		t.Fatalf("write calls: first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}
}

func TestMutatingRequestRejectsRedirectWithoutReplay(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		_, _ = io.WriteString(w, `{"results":[{"rows_affected":1}]}`)
	}))
	defer target.Close()

	var sourceCalls atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sourceCalls.Add(1)
		w.Header().Set("Location", target.URL+"/db/request")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	c := mustClient(t, Config{Endpoints: []string{source.URL, target.URL}})
	_, err := c.Request(context.Background(), Linearizable, true,
		Statement{SQL: "UPDATE customers SET expires_at=?", Args: []any{123}},
	)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) || transportErr.StatusCode != http.StatusTemporaryRedirect ||
		!transportErr.UnknownOutcome {
		t.Fatalf("error = %#v", err)
	}
	if sourceCalls.Load() != 1 || targetCalls.Load() != 0 {
		t.Fatalf("redirect replayed: source=%d target=%d", sourceCalls.Load(), targetCalls.Load())
	}
}

func TestQueryLinearizableRotatesAfterTransportFailure(t *testing.T) {
	var firstCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstCalls.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer first.Close()

	var secondCalls atomic.Int32
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls.Add(1)
		if r.URL.Query().Get("level") != "linearizable" || r.URL.Query().Has("transaction") {
			t.Errorf("query flags = %v", r.URL.Query())
		}
		_, _ = io.WriteString(w, `{"results":[{"types":{"id":"integer"},"rows":[{"id":7}]}]}`)
	}))
	defer second.Close()

	c := mustClient(t, Config{Endpoints: []string{first.URL, second.URL}})
	results, err := c.QueryLinearizable(context.Background(),
		Statement{SQL: "SELECT id FROM customers WHERE login=?", Args: []any{"alice"}},
	)
	if err != nil {
		t.Fatalf("QueryLinearizable: %v", err)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("read calls: first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}
	if len(results) != 1 || len(results[0].Rows) != 1 || fmt.Sprint(results[0].Rows[0]["id"]) != "7" {
		t.Fatalf("results = %#v", results)
	}
}

func TestQueryStrongSetsStrongLevel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("level"); got != "strong" {
			t.Errorf("level = %q", got)
		}
		_, _ = io.WriteString(w, `{"results":[{"rows":[]}]}`)
	}))
	defer srv.Close()

	c := mustClient(t, Config{Endpoints: []string{srv.URL}})
	if _, err := c.QueryStrong(context.Background(), Statement{SQL: "SELECT 1"}); err != nil {
		t.Fatalf("QueryStrong: %v", err)
	}
}

func TestNewRejectsInvalidTLSMaterialWithoutLeakingPaths(t *testing.T) {
	writeInvalid := func(name string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
			t.Fatalf("write invalid TLS material: %v", err)
		}
		return path
	}

	invalidCA := writeInvalid("private-ca-marker.pem")
	invalidCert := writeInvalid("private-cert-marker.pem")
	invalidKey := writeInvalid("private-key-marker.pem")
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "CA", cfg: Config{Endpoints: []string{"https://db.example.test:4001"}, CAFile: invalidCA}},
		{name: "certificate without key", cfg: Config{Endpoints: []string{"https://db.example.test:4001"}, CertFile: invalidCert}},
		{name: "invalid certificate pair", cfg: Config{Endpoints: []string{"https://db.example.test:4001"}, CertFile: invalidCert, KeyFile: invalidKey}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			if err == nil {
				t.Fatal("New succeeded with invalid TLS material")
			}
			for _, secret := range []string{invalidCA, invalidCert, invalidKey, "private-ca-marker", "private-cert-marker", "private-key-marker"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked TLS path %q: %v", secret, err)
				}
			}
		})
	}
}

func TestBasicAuthIsSentButCredentialsNeverLeak(t *testing.T) {
	const username = "control-private-user"
	const password = "control-private-password"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPassword, ok := r.BasicAuth()
		if !ok || gotUser != username || gotPassword != password {
			t.Errorf("Basic Auth missing or incorrect")
		}
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := mustClient(t, Config{Endpoints: []string{srv.URL}, Username: username, Password: password})
	_, err := c.Request(context.Background(), Linearizable, true,
		Statement{SQL: "UPDATE customers SET active=1"},
	)
	if err == nil {
		t.Fatal("Request succeeded on HTTP 401")
	}
	if strings.Contains(err.Error(), username) || strings.Contains(err.Error(), password) {
		t.Fatalf("error leaked credentials: %v", err)
	}
}

func TestNewRejectsCredentialsInEndpointWithoutLeakingThem(t *testing.T) {
	const secret = "endpoint-private-password"
	_, err := New(Config{Endpoints: []string{"https://control-user:" + secret + "@db.example.test:4001"}})
	if err == nil {
		t.Fatal("New accepted credentials in endpoint URL")
	}
	if strings.Contains(err.Error(), "control-user") || strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked endpoint credentials: %v", err)
	}
}

func TestRequestCapsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"rows":[{"value":"`+strings.Repeat("x", 512)+`"}]}]}`)
	}))
	defer srv.Close()

	c := mustClient(t, Config{Endpoints: []string{srv.URL}, MaxResponseBytes: 128})
	_, err := c.Request(context.Background(), Linearizable, false, Statement{SQL: "SELECT value"})
	var transportErr *TransportError
	if !errors.As(err, &transportErr) || !transportErr.UnknownOutcome {
		t.Fatalf("error = %#v, want unknown-outcome TransportError", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("x", 32)) {
		t.Fatalf("error leaked response body: %v", err)
	}
}

func TestRequestHonorsContextCancellation(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer srv.Close()

	c := mustClient(t, Config{Endpoints: []string{srv.URL}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Request(ctx, Linearizable, false, Statement{SQL: "SELECT 1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %#v, want context.Canceled", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("canceled request reached server %d time(s)", calls.Load())
	}
}

func TestBackupStreamsDeleteModeDatabase(t *testing.T) {
	const backup = "SQLite format 3\x00test-backup"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/db/backup" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("fmt") != "delete" || r.URL.Query().Has("redirect") {
			t.Errorf("query = %v", r.URL.Query())
		}
		if user, password, ok := r.BasicAuth(); !ok || user != "backup-user" || password != "backup-password" {
			t.Error("backup Basic Auth missing")
		}
		_, _ = io.WriteString(w, backup[:8])
		_, _ = io.WriteString(w, backup[8:])
	}))
	defer srv.Close()

	c := mustClient(t, Config{
		Endpoints:      []string{srv.URL},
		Username:       "backup-user",
		Password:       "backup-password",
		MaxBackupBytes: 1024,
	})
	var out bytes.Buffer
	if err := c.Backup(context.Background(), &out); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if out.String() != backup {
		t.Fatalf("backup = %q", out.String())
	}
}

func TestBackupCapsStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("b", 64))
	}))
	defer srv.Close()

	c := mustClient(t, Config{Endpoints: []string{srv.URL}, MaxBackupBytes: 16})
	var out bytes.Buffer
	err := c.Backup(context.Background(), &out)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("error = %#v, want *TransportError", err)
	}
	if out.Len() > 17 {
		t.Fatalf("backup writer received %d bytes past configured cap", out.Len())
	}
}

func mustClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Second
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}
