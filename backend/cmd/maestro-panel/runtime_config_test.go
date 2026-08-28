package main

import (
	"context"
	"reflect"
	"testing"
)

func TestRQLiteModeHasNoLegacyStoreProvisionerExecOrSSH(t *testing.T) {
	environment := completeRQLiteEnvironment()
	legacyStoreOpens := 0
	legacyProvisioners := 0
	execCalls := 0
	sshCalls := 0
	rqliteCalls := 0

	wantRuntime := &panelRuntime{}
	got, err := buildConfiguredRuntime(context.Background(), mapGetenv(environment), configuredRuntimeFactories{
		legacy: func(context.Context) (*panelRuntime, error) {
			legacyStoreOpens++
			legacyProvisioners++
			execCalls++
			sshCalls++
			return &panelRuntime{}, nil
		},
		rqlite: func(_ context.Context, config rqliteRuntimeConfig) (*panelRuntime, error) {
			rqliteCalls++
			if want := []string{"https://s2.internal:4001", "https://s3.internal:4001", "https://s4.internal:4001"}; !reflect.DeepEqual(config.Endpoints, want) {
				t.Fatalf("rqlite endpoints = %v, want %v", config.Endpoints, want)
			}
			if config.CAFile != "/run/maestro/ca.pem" ||
				config.CertFile != "/run/maestro/client.pem" ||
				config.KeyFile != "/run/maestro/client.key" ||
				config.KeyBundleFile != "/run/maestro/key-bundle.json" {
				t.Fatalf("unexpected rqlite TLS/crypto config: %#v", config)
			}
			return wantRuntime, nil
		},
	})
	if err != nil {
		t.Fatalf("buildConfiguredRuntime: %v", err)
	}
	if got != wantRuntime {
		t.Fatal("buildConfiguredRuntime returned the wrong rqlite runtime")
	}
	if rqliteCalls != 1 {
		t.Fatalf("rqlite factory calls = %d, want 1", rqliteCalls)
	}
	if legacyStoreOpens != 0 || legacyProvisioners != 0 || execCalls != 0 || sshCalls != 0 {
		t.Fatalf("rqlite mode reached legacy paths: store=%d provisioner=%d exec=%d ssh=%d",
			legacyStoreOpens, legacyProvisioners, execCalls, sshCalls)
	}
}

func TestRQLiteModeRejectsIncompleteTLSCryptoConfiguration(t *testing.T) {
	for _, missing := range []string{
		"MAESTRO_RQLITE_ENDPOINTS",
		"MAESTRO_RQLITE_CA_FILE",
		"MAESTRO_RQLITE_CERT_FILE",
		"MAESTRO_RQLITE_KEY_FILE",
		"MAESTRO_RQLITE_KEY_BUNDLE_FILE",
	} {
		t.Run(missing, func(t *testing.T) {
			environment := completeRQLiteEnvironment()
			delete(environment, missing)
			factoryCalls := 0
			factory := func(context.Context) (*panelRuntime, error) {
				factoryCalls++
				return &panelRuntime{}, nil
			}

			_, err := buildConfiguredRuntime(context.Background(), mapGetenv(environment), configuredRuntimeFactories{
				legacy: factory,
				rqlite: func(context.Context, rqliteRuntimeConfig) (*panelRuntime, error) {
					factoryCalls++
					return &panelRuntime{}, nil
				},
			})
			if err == nil {
				t.Fatalf("missing %s did not fail startup", missing)
			}
			if factoryCalls != 0 {
				t.Fatalf("missing %s constructed %d runtime(s), want 0", missing, factoryCalls)
			}
		})
	}
}

func TestConfiguredRuntimeDefaultsToLegacyOnly(t *testing.T) {
	legacyCalls := 0
	rqliteCalls := 0
	wantRuntime := &panelRuntime{}

	got, err := buildConfiguredRuntime(context.Background(), mapGetenv(nil), configuredRuntimeFactories{
		legacy: func(context.Context) (*panelRuntime, error) {
			legacyCalls++
			return wantRuntime, nil
		},
		rqlite: func(context.Context, rqliteRuntimeConfig) (*panelRuntime, error) {
			rqliteCalls++
			return &panelRuntime{}, nil
		},
	})
	if err != nil {
		t.Fatalf("buildConfiguredRuntime: %v", err)
	}
	if got != wantRuntime || legacyCalls != 1 || rqliteCalls != 0 {
		t.Fatalf("runtime=%p calls legacy=%d rqlite=%d, want %p/1/0", got, legacyCalls, rqliteCalls, wantRuntime)
	}
}

func completeRQLiteEnvironment() map[string]string {
	return map[string]string{
		"MAESTRO_CONTROL_PLANE":          "rqlite",
		"MAESTRO_RQLITE_ENDPOINTS":       "https://s2.internal:4001, https://s3.internal:4001,https://s4.internal:4001",
		"MAESTRO_RQLITE_CA_FILE":         "/run/maestro/ca.pem",
		"MAESTRO_RQLITE_CERT_FILE":       "/run/maestro/client.pem",
		"MAESTRO_RQLITE_KEY_FILE":        "/run/maestro/client.key",
		"MAESTRO_RQLITE_KEY_BUNDLE_FILE": "/run/maestro/key-bundle.json",
	}
}

func mapGetenv(environment map[string]string) func(string) string {
	return func(key string) string {
		return environment[key]
	}
}
