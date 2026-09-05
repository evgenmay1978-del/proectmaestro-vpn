package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
	legacystore "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/xui"
)

type captureXUIClientFunc func(string) (*xui.ExistingClient, error)

func (f captureXUIClientFunc) GetClient(login string) (*xui.ExistingClient, error) { return f(login) }

func captureFixture(t *testing.T) (captureXUIConfig, []legacystore.Customer, []string, []byte) {
	t.Helper()
	directory := t.TempDir()
	cfg := captureXUIConfig{SchemaVersion: 1, Nodes: make([]captureXUINode, 0, 3)}
	for _, node := range []string{"S1", "S3", "S4"} {
		server := strings.ToLower(node) + ".example.test"
		cfg.Nodes = append(cfg.Nodes, captureXUINode{NodeID: node, ExpectedServer: server, Config: xui.Config{BaseURL: "https://" + server + "/panel-base", Host: server, Token: "synthetic-capture-token"}})
	}
	const uuid = "4d29cf7f-8581-4243-baba-d39eb481256c"
	customers := []legacystore.Customer{{Login: "CaptureFixture", SubToken: "synthetic-source-token", Expires: time.Unix(2_100_000, 0).UTC(), Disabled: true,
		VLESS:  &subgen.VLESSCreds{Server: "s1.example.test", Port: 443, UUID: uuid, SNI: "example.test", Fingerprint: "chrome"},
		VLESS3: &subgen.VLESSCreds{Server: "s3.example.test", Port: 443, UUID: uuid, SNI: "example.test", Fingerprint: "chrome"},
		VLESS4: &subgen.VLESSCreds{Server: "s4.example.test", Port: 443, UUID: uuid, SNI: "example.test", Fingerprint: "chrome"}}}
	args := []string{"--customers", filepath.Join(directory, "customers.json"), "--xui-config", filepath.Join(directory, "xui.json"), "--output", filepath.Join(directory, "capture.json")}
	raw := writeCaptureJSON(t, args[1], customers)
	writeCaptureJSON(t, args[3], cfg)
	return cfg, customers, args, raw
}

func writeCaptureJSON(t *testing.T, path string, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCaptureXUIUsesExactRequiredNodesAndRetainsSourceHash(t *testing.T) {
	_, customers, args, _ := captureFixture(t)
	single := customers[0]
	single.Login = "CaptureSingle"
	single.SubToken = "synthetic-second-token"
	single.VLESS3 = nil
	single.VLESS4 = nil
	vless := *single.VLESS
	vless.UUID = "727bd93a-4452-4e1a-933f-155c1c03ef54"
	single.VLESS = &vless
	customers = append(customers, single)
	raw := writeCaptureJSON(t, args[1], customers)
	var calls []string
	var firstLookup time.Time
	factory := func(cfg xui.Config) (captureXUIClient, error) {
		return captureXUIClientFunc(func(login string) (*xui.ExistingClient, error) {
			if firstLookup.IsZero() {
				firstLookup = time.Now().UTC()
			}
			calls = append(calls, cfg.Host+"/"+login)
			uuid := customers[0].VLESS.UUID
			if login == single.Login {
				uuid = single.VLESS.UUID
			}
			return &xui.ExistingClient{Email: login, UUID: uuid, SubID: "synthetic-panel-sub-" + login}, nil
		}), nil
	}
	var stdout, stderr bytes.Buffer
	if code := runCaptureXUIWithFactory(args, &stdout, &stderr, factory); code != exitClean {
		t.Fatalf("capture failed: %d %s", code, stderr.String())
	}
	output, err := readRuntimeFile(args[5], maxSnapshotSize, true)
	if err != nil {
		t.Fatal(err)
	}
	var capture importer.LegacyXUICapture
	if strictRuntimeJSON(output, &capture) != nil {
		t.Fatal("capture JSON is invalid")
	}
	if capture.SchemaVersion != 1 || capture.CustomersSHA256 != runtimeSHA256Hex(raw) || capture.CapturedAt.IsZero() || capture.CapturedAt.After(firstLookup) || capture.CompletedAt.Before(firstLookup) || len(capture.Bindings) != 4 {
		t.Fatal("capture omitted source binding or capture interval")
	}
	seen := make(map[string]bool)
	for _, binding := range capture.Bindings {
		key := strings.ToLower(binding.NodeID) + ".example.test/" + binding.Login
		seen[key] = true
		if binding.Server != strings.ToLower(binding.NodeID)+".example.test" || binding.SubID != "synthetic-panel-sub-"+binding.Login {
			t.Fatal("panel/source binding changed")
		}
	}
	if len(calls) != 4 || len(seen) != 4 {
		t.Fatal("missing or extra panel request")
	}
	for _, call := range calls {
		if !seen[call] {
			t.Fatal("request does not correspond to captured node")
		}
	}
	if strings.Contains(stdout.String()+stderr.String(), customers[0].Login) || strings.Contains(stdout.String()+stderr.String(), "synthetic-") {
		t.Fatal("capture printed protected identities")
	}
	if current, err := os.ReadFile(args[1]); err != nil || !bytes.Equal(current, raw) {
		t.Fatal("capture modified source customers")
	}
}

func TestCaptureXUISingleNodeSourceRequiresOnlyItsConfiguredNode(t *testing.T) {
	cfg, customers, args, _ := captureFixture(t)
	cfg.Nodes = cfg.Nodes[:1]
	customers[0].VLESS3, customers[0].VLESS4 = nil, nil
	writeCaptureJSON(t, args[3], cfg)
	writeCaptureJSON(t, args[1], customers)
	calls := 0
	factory := func(config xui.Config) (captureXUIClient, error) {
		if config.Host != "s1.example.test" {
			t.Fatal("single-node capture selected another panel")
		}
		return captureXUIClientFunc(func(login string) (*xui.ExistingClient, error) {
			calls++
			return &xui.ExistingClient{Email: login, UUID: customers[0].VLESS.UUID, SubID: "synthetic-panel-sub"}, nil
		}), nil
	}
	var stdout, stderr bytes.Buffer
	if code := runCaptureXUIWithFactory(args, &stdout, &stderr, factory); code != exitClean || calls != 1 {
		t.Fatalf("single-node capture failed: %d %s", code, stderr.String())
	}
	raw, err := readRuntimeFile(args[5], maxSnapshotSize, true)
	if err != nil {
		t.Fatal(err)
	}
	var capture importer.LegacyXUICapture
	if strictRuntimeJSON(raw, &capture) != nil || len(capture.Bindings) != 1 || capture.Bindings[0].NodeID != "S1" {
		t.Fatal("single-node capture invented additional bindings")
	}
}

func TestCaptureXUIRejectsUnverifiedPanelIdentityWithoutOutput(t *testing.T) {
	for _, failure := range []string{"missing", "email", "uuid", "sub-id", "error"} {
		t.Run(failure, func(t *testing.T) {
			_, customers, args, _ := captureFixture(t)
			factory := func(xui.Config) (captureXUIClient, error) {
				return captureXUIClientFunc(func(login string) (*xui.ExistingClient, error) {
					record := &xui.ExistingClient{Email: login, UUID: customers[0].VLESS.UUID, SubID: "synthetic-panel-sub"}
					switch failure {
					case "missing":
						return nil, nil
					case "email":
						record.Email = "other-private-login"
					case "uuid":
						record.UUID = "other-private-uuid"
					case "sub-id":
						record.SubID = ""
					case "error":
						return nil, errors.New("secret-upstream-url-token")
					}
					return record, nil
				}), nil
			}
			var stdout, stderr bytes.Buffer
			if runCaptureXUIWithFactory(args, &stdout, &stderr, factory) != exitInputSystem {
				t.Fatal("unverified lookup succeeded")
			}
			if _, err := os.Lstat(args[5]); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed capture left output")
			}
			for _, protected := range []string{"CaptureFixture", "other-private", "secret-upstream", "synthetic-"} {
				if strings.Contains(stdout.String()+stderr.String(), protected) {
					t.Fatal("capture leaked protected details")
				}
			}
		})
	}
}

func TestCaptureXUIRejectsSourceDriftAndPreflightsBindings(t *testing.T) {
	for _, failure := range []string{"source-drift", "source-server", "distinct-uuid", "missing-node", "unsafe-https", "legacy-login", "duplicate-node"} {
		t.Run(failure, func(t *testing.T) {
			cfg, customers, args, raw := captureFixture(t)
			switch failure {
			case "source-server":
				customers[0].VLESS4.Server = "other.example.test"
				writeCaptureJSON(t, args[1], customers)
			case "distinct-uuid":
				customers[0].VLESS4.UUID = "different"
				writeCaptureJSON(t, args[1], customers)
			case "missing-node":
				cfg.Nodes = cfg.Nodes[:2]
				writeCaptureJSON(t, args[3], cfg)
			case "unsafe-https":
				cfg.Nodes[0].Config.Insecure = true
				writeCaptureJSON(t, args[3], cfg)
			case "legacy-login":
				cfg.Nodes[0].Config.Username = "private-admin"
				writeCaptureJSON(t, args[3], cfg)
			case "duplicate-node":
				cfg.Nodes[2] = cfg.Nodes[0]
				writeCaptureJSON(t, args[3], cfg)
			}
			calls := 0
			factory := func(xui.Config) (captureXUIClient, error) {
				calls++
				return captureXUIClientFunc(func(login string) (*xui.ExistingClient, error) {
					if failure == "source-drift" {
						if err := os.WriteFile(args[1], append(append([]byte{}, raw...), '\n'), 0o600); err != nil {
							return nil, err
						}
					}
					return &xui.ExistingClient{Email: login, UUID: customers[0].VLESS.UUID, SubID: "synthetic-panel-sub"}, nil
				}), nil
			}
			var stderr bytes.Buffer
			if runCaptureXUIWithFactory(args, &bytes.Buffer{}, &stderr, factory) != exitInputSystem {
				t.Fatal("invalid capture succeeded")
			}
			if failure != "source-drift" && calls != 0 {
				t.Fatal("source/config validation occurred after creating a remote client")
			}
			if _, err := os.Lstat(args[5]); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected capture left output")
			}
		})
	}
}

func TestCaptureXUIConfigRequiresProtectedHTTPSBearerTargets(t *testing.T) {
	cfg, _, _, _ := captureFixture(t)
	if !validCaptureXUIConfig(cfg) {
		t.Fatal("valid HTTPS config rejected")
	}
	for _, change := range []func(*captureXUINode){
		func(n *captureXUINode) { n.Config.BaseURL = "http://example.test/api" },
		func(n *captureXUINode) { n.Config.BaseURL = "https://user:secret@example.test/api" },
		func(n *captureXUINode) { n.Config.BaseURL = "https://example.test/api?token=secret" },
		func(n *captureXUINode) { n.Config.BaseURL = "https://example.test/api#secret" },
		func(n *captureXUINode) { n.Config.Token = "" },
		func(n *captureXUINode) { n.Config.Password = "secret" },
		func(n *captureXUINode) { n.ExpectedServer = "" },
	} {
		copyCfg := cfg
		copyCfg.Nodes = append([]captureXUINode{}, cfg.Nodes...)
		change(&copyCfg.Nodes[0])
		if validCaptureXUIConfig(copyCfg) {
			t.Fatal("unprotected/misbound target accepted")
		}
	}
}

func TestCaptureXUIExistingOutputAndArgumentErrorsDoNotLeak(t *testing.T) {
	_, _, args, _ := captureFixture(t)
	original := []byte("existing-protected-output")
	if err := os.WriteFile(args[5], original, 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	factory := func(xui.Config) (captureXUIClient, error) { calls++; return nil, errors.New("private-error") }
	var stdout, stderr bytes.Buffer
	if runCaptureXUIWithFactory(args, &stdout, &stderr, factory) != exitInputSystem || calls != 0 {
		t.Fatal("existing destination triggered requests")
	}
	current, err := os.ReadFile(args[5])
	if err != nil || !reflect.DeepEqual(current, original) {
		t.Fatal("existing output changed")
	}
	if runCaptureXUIWithFactory([]string{"--unknown-secret-argument"}, &stdout, &stderr, factory) != exitInputSystem {
		t.Fatal("unknown flag accepted")
	}
	if strings.Contains(stdout.String()+stderr.String(), "secret-argument") {
		t.Fatal("flag parser echoed a protected value")
	}
}

func TestCaptureXUICancellationStopsPublicationWithoutWaitingForGET(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	client := captureXUIClientFunc(func(string) (*xui.ExistingClient, error) {
		close(entered)
		<-release
		return &xui.ExistingClient{Email: "late-private-result"}, nil
	})
	go func() {
		defer close(returned)
		record, err := lookupCaptureXUI(ctx, client, "synthetic-login")
		if err == nil || record != nil {
			t.Error("canceled lookup returned a usable result")
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("lookup did not start")
	}
	cancel()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("capture waited for a canceled GET")
	}
	called := false
	_, err := lookupCaptureXUI(ctx, captureXUIClientFunc(func(string) (*xui.ExistingClient, error) { called = true; return nil, nil }), "synthetic-login")
	if !errors.Is(err, context.Canceled) || called {
		t.Fatal("canceled command started another GET")
	}
}
