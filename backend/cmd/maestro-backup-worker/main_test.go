package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/backuprpo"
)

func validConfigJSON(t *testing.T) string {
	t.Helper()
	root := "/var/lib/maestro-test"
	return fmt.Sprintf(`{
        "version": 1,
        "holder_id": "backup-worker-node-a",
        "rqlite_endpoints": ["https://127.0.0.1:4001", "https://10.0.0.2:4001"],
        "rqlite_credentials_file": "%s/rqlite-credentials.json",
        "rqlite_ca_file": "%s/rqlite-ca.pem",
        "rqlite_cert_file": "%s/rqlite-client.pem",
        "rqlite_key_file": "%s/rqlite-client.key",
        "yandex_endpoint": "https://storage.yandexcloud.net",
        "yandex_region": "ru-central1",
        "yandex_bucket": "maestro-ha-backups",
        "yandex_prefix": "control-plane",
        "yandex_credentials_file": "%s/yandex-credentials.json",
        "runtime_dir": "%s/runtime",
        "backup_script_path": "%s/backup-rqlite.sh",
        "verify_script_path": "%s/verify_backup.py",
        "keys_path": "%s/backup-keys.env",
        "gpg_path": "%s/gpg",
        "python_path": "%s/python3",
        "gpg_home": "%s/gnupg",
        "signer_fingerprint": "0123456789ABCDEF0123456789ABCDEF01234567",
        "recipient_fingerprint": "89ABCDEF0123456789ABCDEF0123456789ABCDEF",
        "repository_commit_sha": "0123456789abcdef0123456789abcdef01234567",
        "build_run_id": 12345,
        "capability_evidence_file": "%s/capability.json",
        "lease_ttl_seconds": 60,
        "capability_ttl_seconds": 120,
        "deadline_seconds": 30,
        "command_timeout_seconds": 20,
        "max_transitions": 16,
        "max_response_bytes": 1048576,
        "max_backup_bytes": 8388608,
        "max_image_bytes": 8388608,
        "max_bundle_bytes": 16777216,
        "max_archive_bytes": 33554432,
        "max_extracted_bytes": 67108864
    }`, root, root, root, root, root, root, root, root, root, root, root, root, root)
}

func decodeValidConfig(t *testing.T) workerConfig {
	t.Helper()
	config, err := decodeWorkerConfig(strings.NewReader(validConfigJSON(t)))
	if err != nil {
		t.Fatalf("decode valid config: %v", err)
	}
	return config
}

func TestDecodeWorkerConfigAcceptsStrictVersionOne(t *testing.T) {
	config := decodeValidConfig(t)
	if config.Version != 1 {
		t.Fatalf("version = %d, want 1", config.Version)
	}
	if config.YandexPrefix != "control-plane" {
		t.Fatalf("prefix = %q", config.YandexPrefix)
	}
	if len(config.RQLiteEndpoints) != 2 {
		t.Fatalf("rqlite endpoints = %d, want 2", len(config.RQLiteEndpoints))
	}
	if config.HolderID != "backup-worker-node-a" {
		t.Fatalf("holder id = %q, want stable configured identity", config.HolderID)
	}
}

func TestDecodeWorkerConfigRequiresStrictStableHolderID(t *testing.T) {
	valid := validConfigJSON(t)
	tests := map[string]string{
		"missing": strings.Replace(valid, `        "holder_id": "backup-worker-node-a",
`, "", 1),
		"uppercase":    strings.Replace(valid, "backup-worker-node-a", "backup-worker-Node-a", 1),
		"whitespace":   strings.Replace(valid, "backup-worker-node-a", " backup-worker-node-a", 1),
		"empty suffix": strings.Replace(valid, "backup-worker-node-a", "backup-worker-", 1),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeWorkerConfig(strings.NewReader(raw)); !errors.Is(err, errConfig) {
				t.Fatalf("error = %v, want errConfig", err)
			}
		})
	}
}

func TestDecodeWorkerConfigRejectsStructuralAmbiguity(t *testing.T) {
	valid := validConfigJSON(t)
	tests := map[string]string{
		"duplicate key":       strings.Replace(valid, `"version": 1,`, `"version": 1, "version": 1,`, 1),
		"unknown key":         strings.Replace(valid, `"version": 1,`, `"version": 1, "surprise": true,`, 1),
		"inline secret":       strings.Replace(valid, `"version": 1,`, `"version": 1, "secret_access_key": "must-not-be-inline",`, 1),
		"trailing value":      valid + `{}`,
		"unsupported version": strings.Replace(valid, `"version": 1`, `"version": 2`, 1),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := decodeWorkerConfig(strings.NewReader(raw))
			if !errors.Is(err, errConfig) {
				t.Fatalf("error = %v, want errConfig", err)
			}
		})
	}
}

func TestValidateWorkerConfigRejectsUnsafeValues(t *testing.T) {
	tests := map[string]func(*workerConfig){
		"http rqlite":       func(c *workerConfig) { c.RQLiteEndpoints = []string{"http://127.0.0.1:4001"} },
		"public rqlite":     func(c *workerConfig) { c.RQLiteEndpoints = []string{"https://8.8.8.8:4001"} },
		"hostname rqlite":   func(c *workerConfig) { c.RQLiteEndpoints = []string{"https://localhost:4001"} },
		"unpinned yandex":   func(c *workerConfig) { c.YandexEndpoint = "https://example.invalid" },
		"unsafe prefix":     func(c *workerConfig) { c.YandexPrefix = "../escape/" },
		"relative path":     func(c *workerConfig) { c.RuntimeDir = "runtime" },
		"invalid signer":    func(c *workerConfig) { c.SignerFingerprint = strings.ToLower(c.SignerFingerprint) },
		"zero timeout":      func(c *workerConfig) { c.DeadlineSeconds = 0 },
		"unbounded timeout": func(c *workerConfig) { c.DeadlineSeconds = 86401 },
		"zero limit":        func(c *workerConfig) { c.MaxBundleBytes = 0 },
		"unbounded limit":   func(c *workerConfig) { c.MaxBundleBytes = 1 << 41 },
		"duplicate endpoint": func(c *workerConfig) {
			c.RQLiteEndpoints = []string{"https://127.0.0.1:4001", "https://127.0.0.1:4001"}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			config := decodeValidConfig(t)
			mutate(&config)
			if err := validateWorkerConfig(config); !errors.Is(err, errConfig) {
				t.Fatalf("error = %v, want errConfig", err)
			}
		})
	}
}

func TestValidateWorkerConfigEnforcesTimeoutLeaseOrdering(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*workerConfig)
		wantErr bool
	}{
		{
			name: "command equals deadline",
			mutate: func(config *workerConfig) {
				config.CommandTimeoutSeconds = config.DeadlineSeconds
			},
		},
		{
			name: "adjacent deadline and equal lease capability",
			mutate: func(config *workerConfig) {
				config.CommandTimeoutSeconds = 59
				config.DeadlineSeconds = 59
				config.LeaseTTLSeconds = 60
				config.CapabilityTTLSeconds = 60
			},
		},
		{
			name: "command exceeds deadline",
			mutate: func(config *workerConfig) {
				config.CommandTimeoutSeconds = config.DeadlineSeconds + 1
			},
			wantErr: true,
		},
		{
			name: "deadline equals lease",
			mutate: func(config *workerConfig) {
				config.DeadlineSeconds = config.LeaseTTLSeconds
			},
			wantErr: true,
		},
		{
			name: "lease exceeds capability",
			mutate: func(config *workerConfig) {
				config.LeaseTTLSeconds = config.CapabilityTTLSeconds + 1
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := decodeValidConfig(t)
			test.mutate(&config)
			err := validateWorkerConfig(config)
			if test.wantErr && !errors.Is(err, errConfig) {
				t.Fatalf("error = %v, want errConfig", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}

type staticWorker struct {
	result backuprpo.Result
}

func (worker staticWorker) Run(context.Context) backuprpo.Result { return worker.result }

func TestExecuteUsesFixedRedactedResults(t *testing.T) {
	secret := "credential-secret-must-not-appear"
	valid := decodeValidConfig(t)
	tests := []struct {
		name       string
		args       []string
		loadErr    error
		buildErr   error
		result     backuprpo.Result
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "verified", args: []string{"--config", "/etc/maestro/worker.json"}, result: backuprpo.Result{Code: backuprpo.ResultVerified}, wantCode: 0, wantStdout: "backup-worker:ok\n"},
		{name: "noop", args: []string{"--config", "/etc/maestro/worker.json"}, result: backuprpo.Result{Code: backuprpo.ResultNoop}, wantCode: 0, wantStdout: "backup-worker:ok\n"},
		{name: "usage", args: []string{"/etc/maestro/worker.json"}, wantCode: 2, wantStderr: "backup-worker:config\n"},
		{name: "config", args: []string{"--config", "/etc/maestro/worker.json"}, loadErr: fmt.Errorf("%w: %s", errConfig, secret), wantCode: 2, wantStderr: "backup-worker:config\n"},
		{name: "unsafe build", args: []string{"--config", "/etc/maestro/worker.json"}, buildErr: fmt.Errorf("%w: %s", errUnsafeRuntime, secret), wantCode: 3, wantStderr: "backup-worker:unsafe-runtime\n"},
		{name: "operational build", args: []string{"--config", "/etc/maestro/worker.json"}, buildErr: errors.New(secret), wantCode: 1, wantStderr: "backup-worker:operational\n"},
		{name: "unsafe result", args: []string{"--config", "/etc/maestro/worker.json"}, result: backuprpo.Result{Code: backuprpo.ResultUnsafeRuntime}, wantCode: 3, wantStderr: "backup-worker:unsafe-runtime\n"},
		{name: "operational result", args: []string{"--config", "/etc/maestro/worker.json"}, result: backuprpo.Result{Code: backuprpo.ResultStaleLease}, wantCode: 1, wantStderr: "backup-worker:operational\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			dependencies := runtimeDependencies{
				load: func(string) (workerConfig, error) { return valid, test.loadErr },
				build: func(context.Context, workerConfig) (oneShotWorker, error) {
					if test.buildErr != nil {
						return nil, test.buildErr
					}
					return staticWorker{result: test.result}, nil
				},
			}
			code := execute(context.Background(), test.args, &stdout, &stderr, dependencies)
			if code != test.wantCode {
				t.Fatalf("exit code = %d, want %d", code, test.wantCode)
			}
			if stdout.String() != test.wantStdout || stderr.String() != test.wantStderr {
				t.Fatalf("stdout/stderr = %q/%q, want %q/%q", stdout.String(), stderr.String(), test.wantStdout, test.wantStderr)
			}
			if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
				t.Fatal("secret-bearing error reached output")
			}
		})
	}
}
