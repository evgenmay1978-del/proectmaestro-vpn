package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestRQLiteRuntimeBuildsServiceBusinessAndControlPlaneHandler(t *testing.T) {
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x31}, 32)}, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	database := &runtimeFakeRQLite{}
	clientCalls := 0
	keyCalls := 0
	migrationCalls := 0
	wantConfig := rqliteRuntimeConfig{
		Endpoints: []string{"https://s2.internal:4001", "https://s3.internal:4001", "https://s4.internal:4001"},
		CAFile:    "/run/maestro/ca.pem", CertFile: "/run/maestro/client.pem",
		KeyFile: "/run/maestro/client.key", KeyBundleFile: "/run/maestro/key-bundle.json",
	}
	runtime, err := buildRQLitePanelRuntime(context.Background(), wantConfig, api.Config{
		SubBaseURL: "https://sub.example", SBPPhone: "+70000000000", PayURL: "https://pay.example", TrialDays: 2,
	}, rqliteRuntimeDependencies{
		newClient: func(config rqlite.Config) (rqlite.RQLite, error) {
			clientCalls++
			if !reflect.DeepEqual(config.Endpoints, wantConfig.Endpoints) || config.CAFile != wantConfig.CAFile ||
				config.CertFile != wantConfig.CertFile || config.KeyFile != wantConfig.KeyFile || config.Timeout <= 0 {
				t.Fatalf("rqlite config=%#v", config)
			}
			return database, nil
		},
		loadSecretBox: func(path string) (*controlplane.SecretBox, error) {
			keyCalls++
			if path != wantConfig.KeyBundleFile {
				t.Fatalf("key bundle path=%q", path)
			}
			return box, nil
		},
		applyMigrations: func(_ context.Context, got rqlite.RQLite) error {
			migrationCalls++
			if got != database {
				t.Fatal("migrations received a different rqlite client")
			}
			return nil
		},
		ids: runtimeTestIDs{}, clock: runtimeTestClock{now: time.Unix(2_000_000, 0)},
	})
	if err != nil {
		t.Fatalf("buildRQLitePanelRuntime: %v", err)
	}
	if clientCalls != 1 || keyCalls != 1 || migrationCalls != 1 {
		t.Fatalf("constructor calls client=%d keys=%d migrations=%d", clientCalls, keyCalls, migrationCalls)
	}
	if runtime == nil || runtime.mode != "rqlite" || runtime.handler == nil || runtime.business == nil {
		t.Fatalf("runtime=%#v", runtime)
	}
	if _, ok := runtime.business.(*api.ServiceBusiness); !ok {
		t.Fatalf("business type=%T, want *api.ServiceBusiness", runtime.business)
	}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	runtime.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "ok "+api.BuildCommit {
		t.Fatalf("health status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestRQLiteRuntimeFailsClosedBeforeHandlerWhenMigrationFails(t *testing.T) {
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x51}, 32)}, bytes.Repeat([]byte{0x62}, 32))
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("schema unavailable")
	runtime, err := buildRQLitePanelRuntime(context.Background(), completeRuntimeConfig(), api.Config{}, rqliteRuntimeDependencies{
		newClient:       func(rqlite.Config) (rqlite.RQLite, error) { return &runtimeFakeRQLite{}, nil },
		loadSecretBox:   func(string) (*controlplane.SecretBox, error) { return box, nil },
		applyMigrations: func(context.Context, rqlite.RQLite) error { return wantErr },
		ids:             runtimeTestIDs{}, clock: runtimeTestClock{now: time.Unix(2_000_000, 0)},
	})
	if runtime != nil || !errors.Is(err, wantErr) {
		t.Fatalf("runtime=%#v error=%v, want nil/%v", runtime, err, wantErr)
	}
}

func completeRuntimeConfig() rqliteRuntimeConfig {
	return rqliteRuntimeConfig{
		Endpoints: []string{"https://s2.internal:4001", "https://s3.internal:4001", "https://s4.internal:4001"},
		CAFile:    "ca.pem", CertFile: "client.pem", KeyFile: "client.key", KeyBundleFile: "keys.json",
	}
}

type runtimeTestIDs struct{ counter int }

func (runtimeTestIDs) NewID(prefix string) (string, error) {
	return prefix + "_00000000000000000000000000000001", nil
}

type runtimeTestClock struct{ now time.Time }

func (clock runtimeTestClock) Now() time.Time { return clock.now }

type runtimeFakeRQLite struct{}

func (*runtimeFakeRQLite) Request(context.Context, rqlite.Consistency, bool, ...rqlite.Statement) ([]rqlite.Result, error) {
	return nil, errors.New("unexpected runtime request")
}

func (*runtimeFakeRQLite) QueryLinearizable(context.Context, ...rqlite.Statement) ([]rqlite.Result, error) {
	return nil, errors.New("unexpected runtime query")
}

func (*runtimeFakeRQLite) QueryStrong(context.Context, ...rqlite.Statement) ([]rqlite.Result, error) {
	return nil, errors.New("unexpected runtime strong query")
}

func (*runtimeFakeRQLite) Backup(context.Context, io.Writer) error {
	return errors.New("unexpected runtime backup")
}
