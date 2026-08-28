package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const runtimeKeyBundleLimit = 64 << 10

type rqliteRuntimeDependencies struct {
	newClient       func(rqlite.Config) (rqlite.RQLite, error)
	loadSecretBox   func(string) (*controlplane.SecretBox, error)
	applyMigrations func(context.Context, rqlite.RQLite) error
	ids             controlplane.IDSource
	clock           controlplane.Clock
}

func productionRQLiteRuntimeDependencies() rqliteRuntimeDependencies {
	return rqliteRuntimeDependencies{
		newClient: func(config rqlite.Config) (rqlite.RQLite, error) {
			return rqlite.New(config)
		},
		loadSecretBox: loadRuntimeSecretBox,
		applyMigrations: func(ctx context.Context, database rqlite.RQLite) error {
			return controlplane.NewMigrator(database).Apply(ctx)
		},
		ids: cryptoRuntimeIDs{}, clock: systemRuntimeClock{},
	}
}

func buildRQLitePanelRuntime(
	ctx context.Context,
	config rqliteRuntimeConfig,
	apiConfig api.Config,
	dependencies rqliteRuntimeDependencies,
) (*panelRuntime, error) {
	if ctx == nil || len(config.Endpoints) != 3 || config.CAFile == "" || config.CertFile == "" ||
		config.KeyFile == "" || config.KeyBundleFile == "" || dependencies.newClient == nil ||
		dependencies.loadSecretBox == nil || dependencies.applyMigrations == nil ||
		dependencies.ids == nil || dependencies.clock == nil {
		return nil, errInvalidRQLiteRuntime
	}
	database, err := dependencies.newClient(rqlite.Config{
		Endpoints: config.Endpoints, CAFile: config.CAFile, CertFile: config.CertFile, KeyFile: config.KeyFile,
		Timeout: 10 * time.Second, MaxResponseBytes: 8 << 20, MaxBackupBytes: 1 << 30,
	})
	if err != nil || database == nil {
		return nil, fmt.Errorf("rqlite runtime: client unavailable: %w", err)
	}
	secrets, err := dependencies.loadSecretBox(config.KeyBundleFile)
	if err != nil || secrets == nil {
		return nil, fmt.Errorf("rqlite runtime: key bundle unavailable: %w", err)
	}
	if err := dependencies.applyMigrations(ctx, database); err != nil {
		return nil, fmt.Errorf("rqlite runtime: schema unavailable: %w", err)
	}
	store, err := controlplane.NewStore(database, secrets, dependencies.clock)
	if err != nil {
		return nil, fmt.Errorf("rqlite runtime: store unavailable: %w", err)
	}
	service, err := controlplane.NewService(store, dependencies.ids, dependencies.clock)
	if err != nil {
		return nil, fmt.Errorf("rqlite runtime: service unavailable: %w", err)
	}
	wbSender, err := api.NewWBRoomSender(service, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("rqlite runtime: WB provider unavailable: %w", err)
	}
	workerID, err := dependencies.ids.NewID("worker")
	if err != nil {
		return nil, fmt.Errorf("rqlite runtime: worker identity unavailable: %w", err)
	}
	business := api.NewServiceBusiness(service, api.ServiceBusinessConfig{
		SubBaseURL: apiConfig.SubBaseURL, SBPPhone: apiConfig.SBPPhone,
		PayURL: apiConfig.PayURL, TrialDays: apiConfig.TrialDays,
		WBRoomSender: wbSender, WorkerID: workerID,
	})
	server := api.NewControlPlane(business, apiConfig)
	return &panelRuntime{mode: "rqlite", business: business, handler: server.Handler()}, nil
}

func rqliteAPIConfigFromEnvironment() api.Config {
	return api.Config{
		AdminToken:         os.Getenv("MAESTRO_ADMIN_TOKEN"),
		PanelPath:          os.Getenv("MAESTRO_PANEL_PATH"),
		SubBaseURL:         env("MAESTRO_SUB_BASE", "https://wapmixx.ru:8910"),
		SBPPhone:           os.Getenv("MAESTRO_SBP_PHONE"),
		PayURL:             os.Getenv("MAESTRO_SBP_PAY_URL"),
		UpdateDir:          env("MAESTRO_UPDATE_DIR", "/var/lib/maestro/update"),
		ReportDir:          env("MAESTRO_REPORT_DIR", "/var/lib/maestro/reports"),
		EnforceDeviceLimit: env("MAESTRO_DEVICE_LIMIT", "on") != "off",
		TrialDays:          atoi(os.Getenv("MAESTRO_TRIAL_DAYS"), 2),
		TrialIPQuota:       atoi(os.Getenv("MAESTRO_TRIAL_IP_QUOTA"), 3),
	}
}

type runtimeKeyBundle struct {
	CurrentKeyVersion int                   `json:"current_key_version"`
	EncryptionKeys    []runtimeVersionedKey `json:"encryption_keys"`
	HMACKeyB64        string                `json:"hmac_key_b64"`
}

type runtimeVersionedKey struct {
	Version int    `json:"version"`
	KeyB64  string `json:"key_b64"`
}

func loadRuntimeSecretBox(path string) (*controlplane.SecretBox, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errInvalidRQLiteRuntime
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("rqlite runtime: invalid key bundle")
	}
	if goruntime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("rqlite runtime: key bundle permissions are too broad")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > runtimeKeyBundleLimit {
		return nil, errors.New("rqlite runtime: invalid key bundle")
	}
	var encoded runtimeKeyBundle
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&encoded); err != nil {
		return nil, errors.New("rqlite runtime: invalid key bundle")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("rqlite runtime: invalid key bundle")
	}
	if encoded.CurrentKeyVersion <= 0 || len(encoded.EncryptionKeys) == 0 || len(encoded.EncryptionKeys) > 16 {
		return nil, errors.New("rqlite runtime: invalid key bundle")
	}
	keys := make(map[int][]byte, len(encoded.EncryptionKeys))
	for _, item := range encoded.EncryptionKeys {
		key, ok := decodeRuntimeKey(item.KeyB64)
		if !ok || item.Version <= 0 {
			zeroRuntimeKeys(keys, nil)
			return nil, errors.New("rqlite runtime: invalid key bundle")
		}
		if _, duplicate := keys[item.Version]; duplicate {
			zeroRuntimeKeys(keys, key)
			return nil, errors.New("rqlite runtime: invalid key bundle")
		}
		keys[item.Version] = key
	}
	if _, ok := keys[encoded.CurrentKeyVersion]; !ok {
		zeroRuntimeKeys(keys, nil)
		return nil, errors.New("rqlite runtime: invalid key bundle")
	}
	hmacKey, ok := decodeRuntimeKey(encoded.HMACKeyB64)
	if !ok {
		zeroRuntimeKeys(keys, hmacKey)
		return nil, errors.New("rqlite runtime: invalid key bundle")
	}
	box, err := controlplane.NewSecretBox(encoded.CurrentKeyVersion, keys, hmacKey)
	zeroRuntimeKeys(keys, hmacKey)
	if err != nil {
		return nil, errors.New("rqlite runtime: invalid key bundle")
	}
	return box, nil
}

func decodeRuntimeKey(encoded string) ([]byte, bool) {
	key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(encoded))
	return key, err == nil && len(key) == 32
}

func zeroRuntimeKeys(keys map[int][]byte, extra []byte) {
	for version, key := range keys {
		for index := range key {
			key[index] = 0
		}
		delete(keys, version)
	}
	for index := range extra {
		extra[index] = 0
	}
}

type cryptoRuntimeIDs struct{}

func (cryptoRuntimeIDs) NewID(prefix string) (string, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "", errors.New("rqlite runtime: empty identifier prefix")
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("rqlite runtime: identifier generation failed")
	}
	return prefix + "_" + hex.EncodeToString(raw), nil
}

type systemRuntimeClock struct{}

func (systemRuntimeClock) Now() time.Time { return time.Now().UTC() }
