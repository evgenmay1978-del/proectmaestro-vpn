package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	maxRuntimePrivateFile = int64(1 << 20)
	maxRuntimeCertFile    = int64(4 << 20)
	maxRuntimeKeyVersions = 32
)

var errInvalidProductionRuntime = errors.New("invalid production import runtime")

type targetConfig struct {
	SchemaVersion  int           `json:"schema_version"`
	Voters         []targetVoter `json:"voters"`
	CAFile         string        `json:"ca_file"`
	CertFile       string        `json:"cert_file"`
	KeyFile        string        `json:"key_file"`
	TimeoutSeconds int           `json:"timeout_seconds"`
}

type targetVoter struct {
	NodeID string `json:"node_id"`
	URL    string `json:"url"`
}

type keyBundle struct {
	SchemaVersion     int            `json:"schema_version"`
	CurrentKeyVersion int            `json:"current_key_version"`
	EncryptionKeys   []versionedKey `json:"encryption_keys"`
	HMACKeyB64        string         `json:"hmac_key_b64"`
}

type versionedKey struct {
	Version int    `json:"version"`
	KeyB64  string `json:"key_b64"`
}

type loadedKeyBundle struct {
	CurrentKeyVersion int
	EncryptionKeys    map[int][]byte
	HMACKey            []byte
}

type receiptSigningKey struct {
	SchemaVersion int    `json:"schema_version"`
	SeedB64       string `json:"seed_b64"`
}

type applyRuntime struct {
	Store              *importer.RQLiteApplyStore
	Schema             controlplane.SchemaIdentity
	TargetConfigSHA256 string
	Signer             ed25519.PrivateKey
	SignerKeyID        string
}

type applyRuntimeConfig struct {
	TargetConfigFile    string
	KeyBundleFile       string
	LegacyTrialSaltFile string
	ReceiptSigningFile  string
	Protection          importer.SnapshotProtection
}

type schemaIdentityVerifier interface {
	VerifyIdentity(context.Context) (controlplane.SchemaIdentity, error)
}

type productionRuntimeDependencies struct {
	now         func() time.Time
	newRQLite   func(rqlite.Config) (rqlite.RQLite, error)
	newVerifier func(rqlite.RQLite) schemaIdentityVerifier
}

func productionApplyRuntimeFactory(ctx context.Context, config applyRuntimeConfig) (*applyRuntime, error) {
	return buildProductionApplyRuntime(ctx, config, productionRuntimeDependencies{
		now: time.Now,
		newRQLite: func(config rqlite.Config) (rqlite.RQLite, error) {
			return rqlite.New(config)
		},
		newVerifier: func(db rqlite.RQLite) schemaIdentityVerifier {
			return controlplane.NewMigrator(db)
		},
	})
}

func buildProductionApplyRuntime(
	ctx context.Context,
	config applyRuntimeConfig,
	dependencies productionRuntimeDependencies,
) (*applyRuntime, error) {
	if dependencies.now == nil || dependencies.newRQLite == nil || dependencies.newVerifier == nil {
		return nil, errInvalidProductionRuntime
	}
	target, targetDigest, err := loadTargetConfig(config.TargetConfigFile, dependencies.now())
	if err != nil {
		return nil, errInvalidProductionRuntime
	}
	bundle, err := loadKeyBundle(config.KeyBundleFile)
	if err != nil {
		return nil, errInvalidProductionRuntime
	}
	keysZeroed := false
	defer func() {
		if !keysZeroed {
			bundle.zero()
		}
	}()

	var rawTrialSalt []byte
	if config.Protection.HasTrials {
		rawTrialSalt, err = readRuntimeFile(config.LegacyTrialSaltFile, maxRuntimePrivateFile, true)
		if err != nil {
			return nil, errInvalidProductionRuntime
		}
	} else if config.LegacyTrialSaltFile != "" {
		return nil, errInvalidProductionRuntime
	}
	saltZeroed := false
	defer func() {
		if !saltZeroed {
			zero(rawTrialSalt)
		}
	}()

	signer, signerKeyID, err := loadReceiptSigningKey(config.ReceiptSigningFile)
	if err != nil {
		return nil, errInvalidProductionRuntime
	}
	box, err := controlplane.NewSecretBox(
		bundle.CurrentKeyVersion,
		bundle.EncryptionKeys,
		bundle.HMACKey,
	)
	if err != nil {
		return nil, errInvalidProductionRuntime
	}
	trialProtection, err := importer.ValidateSnapshotProtection(
		config.Protection,
		box,
		bundle.HMACKey,
		rawTrialSalt,
	)
	if err != nil {
		return nil, errInvalidProductionRuntime
	}
	bundle.zero()
	keysZeroed = true
	zero(rawTrialSalt)
	saltZeroed = true

	endpoints := make([]string, len(target.Voters))
	for index, voter := range target.Voters {
		endpoints[index] = voter.URL
	}
	db, err := dependencies.newRQLite(rqlite.Config{
		Endpoints:        endpoints,
		CAFile:           target.CAFile,
		CertFile:         target.CertFile,
		KeyFile:          target.KeyFile,
		Timeout:          time.Duration(target.TimeoutSeconds) * time.Second,
		MaxResponseBytes: 8 << 20,
		MaxBackupBytes:   4 << 30,
	})
	if err != nil || db == nil {
		return nil, errInvalidProductionRuntime
	}
	verifier := dependencies.newVerifier(db)
	if verifier == nil {
		return nil, errInvalidProductionRuntime
	}
	schema, err := verifier.VerifyIdentity(ctx)
	if err != nil {
		return nil, errInvalidProductionRuntime
	}
	var store *importer.RQLiteApplyStore
	if trialProtection == nil {
		store, err = importer.NewRQLiteApplyStore(db, dependencies.now)
	} else {
		store, err = importer.NewRQLiteApplyStoreWithTrialProtection(db, dependencies.now, *trialProtection)
	}
	if err != nil {
		return nil, errInvalidProductionRuntime
	}
	versions, err := store.ReadReferencedKeyVersions(ctx)
	if err != nil || box.ReadyForVersions(versions...) != nil {
		return nil, errInvalidProductionRuntime
	}
	return &applyRuntime{
		Store:              store,
		Schema:             schema,
		TargetConfigSHA256: targetDigest,
		Signer:             signer,
		SignerKeyID:        signerKeyID,
	}, nil
}

func loadTargetConfig(path string, now time.Time) (targetConfig, string, error) {
	data, err := readRuntimeFile(path, maxRuntimePrivateFile, true)
	if err != nil {
		return targetConfig{}, "", errInvalidProductionRuntime
	}
	var config targetConfig
	if strictRuntimeJSON(data, &config) != nil {
		return targetConfig{}, "", errInvalidProductionRuntime
	}
	if config.SchemaVersion != 1 || len(config.Voters) != 3 ||
		config.TimeoutSeconds < 1 || config.TimeoutSeconds > 30 ||
		config.CAFile == "" || config.CertFile == "" || config.KeyFile == "" {
		return targetConfig{}, "", errInvalidProductionRuntime
	}
	required := map[string]bool{"S2": false, "S3": false, "S4": false}
	origins := make(map[string]struct{}, len(config.Voters))
	for index := range config.Voters {
		voter := &config.Voters[index]
		if _, ok := required[voter.NodeID]; !ok || required[voter.NodeID] {
			return targetConfig{}, "", errInvalidProductionRuntime
		}
		parsed, err := url.Parse(voter.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
			parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			return targetConfig{}, "", errInvalidProductionRuntime
		}
		origin := parsed.String()
		if _, duplicate := origins[origin]; duplicate {
			return targetConfig{}, "", errInvalidProductionRuntime
		}
		origins[origin] = struct{}{}
		required[voter.NodeID] = true
		voter.URL = origin
	}
	for _, present := range required {
		if !present {
			return targetConfig{}, "", errInvalidProductionRuntime
		}
	}
	sort.Slice(config.Voters, func(left, right int) bool {
		return config.Voters[left].NodeID < config.Voters[right].NodeID
	})

	caPEM, err := readRuntimeFile(config.CAFile, maxRuntimeCertFile, false)
	if err != nil {
		return targetConfig{}, "", errInvalidProductionRuntime
	}
	clientPEM, err := readRuntimeFile(config.CertFile, maxRuntimeCertFile, false)
	if err != nil {
		return targetConfig{}, "", errInvalidProductionRuntime
	}
	keyPEM, err := readRuntimeFile(config.KeyFile, maxRuntimePrivateFile, true)
	if err != nil {
		return targetConfig{}, "", errInvalidProductionRuntime
	}
	defer zero(keyPEM)
	caCertificates, err := parseRuntimeCertificates(caPEM)
	if err != nil {
		return targetConfig{}, "", errInvalidProductionRuntime
	}
	pair, err := tls.X509KeyPair(clientPEM, keyPEM)
	if err != nil || len(pair.Certificate) == 0 {
		return targetConfig{}, "", errInvalidProductionRuntime
	}
	clientCertificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || !certificateValidAt(clientCertificate, now) ||
		!hasClientAuthUsage(clientCertificate) {
		return targetConfig{}, "", errInvalidProductionRuntime
	}
	roots := x509.NewCertPool()
	caDigests := make([]string, 0, len(caCertificates))
	for _, certificate := range caCertificates {
		if !certificate.IsCA || !certificateValidAt(certificate, now) {
			return targetConfig{}, "", errInvalidProductionRuntime
		}
		roots.AddCert(certificate)
		caDigests = append(caDigests, runtimeSHA256Hex(certificate.Raw))
	}
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		certificate, err := x509.ParseCertificate(der)
		if err != nil {
			return targetConfig{}, "", errInvalidProductionRuntime
		}
		intermediates.AddCert(certificate)
	}
	if _, err := clientCertificate.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return targetConfig{}, "", errInvalidProductionRuntime
	}
	sort.Strings(caDigests)
	canonical := struct {
		SchemaVersion       int           `json:"schema_version"`
		Voters              []targetVoter `json:"voters"`
		CACertificateSHA256 []string      `json:"ca_certificate_sha256"`
		ClientCertSHA256    string        `json:"client_certificate_sha256"`
		TimeoutSeconds      int           `json:"timeout_seconds"`
	}{
		SchemaVersion:       config.SchemaVersion,
		Voters:              config.Voters,
		CACertificateSHA256: caDigests,
		ClientCertSHA256:    runtimeSHA256Hex(clientCertificate.Raw),
		TimeoutSeconds:      config.TimeoutSeconds,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return targetConfig{}, "", errInvalidProductionRuntime
	}
	return config, runtimeSHA256Hex(encoded), nil
}

func loadKeyBundle(path string) (loadedKeyBundle, error) {
	data, err := readRuntimeFile(path, maxRuntimePrivateFile, true)
	if err != nil {
		return loadedKeyBundle{}, errInvalidProductionRuntime
	}
	var encoded keyBundle
	if strictRuntimeJSON(data, &encoded) != nil ||
		encoded.SchemaVersion != 1 || encoded.CurrentKeyVersion <= 0 ||
		len(encoded.EncryptionKeys) == 0 || len(encoded.EncryptionKeys) > maxRuntimeKeyVersions {
		return loadedKeyBundle{}, errInvalidProductionRuntime
	}
	loaded := loadedKeyBundle{
		CurrentKeyVersion: encoded.CurrentKeyVersion,
		EncryptionKeys:    make(map[int][]byte, len(encoded.EncryptionKeys)),
	}
	ok := false
	defer func() {
		if !ok {
			loaded.zero()
		}
	}()
	for _, item := range encoded.EncryptionKeys {
		key, valid := decodeRuntimeBase64(item.KeyB64)
		if !valid || item.Version <= 0 || len(key) != 32 {
			zero(key)
			return loadedKeyBundle{}, errInvalidProductionRuntime
		}
		if _, duplicate := loaded.EncryptionKeys[item.Version]; duplicate {
			zero(key)
			return loadedKeyBundle{}, errInvalidProductionRuntime
		}
		loaded.EncryptionKeys[item.Version] = key
	}
	if _, present := loaded.EncryptionKeys[loaded.CurrentKeyVersion]; !present {
		return loadedKeyBundle{}, errInvalidProductionRuntime
	}
	loaded.HMACKey, ok = decodeRuntimeBase64(encoded.HMACKeyB64)
	if !ok || len(loaded.HMACKey) != 32 {
		zero(loaded.HMACKey)
		return loadedKeyBundle{}, errInvalidProductionRuntime
	}
	for _, key := range loaded.EncryptionKeys {
		if bytes.Equal(key, loaded.HMACKey) {
			return loadedKeyBundle{}, errInvalidProductionRuntime
		}
	}
	ok = true
	return loaded, nil
}

func (bundle *loadedKeyBundle) zero() {
	if bundle == nil {
		return
	}
	for version, key := range bundle.EncryptionKeys {
		zero(key)
		delete(bundle.EncryptionKeys, version)
	}
	zero(bundle.HMACKey)
	bundle.HMACKey = nil
}

func loadReceiptSigningKey(path string) (ed25519.PrivateKey, string, error) {
	data, err := readRuntimeFile(path, maxRuntimePrivateFile, true)
	if err != nil {
		return nil, "", errInvalidProductionRuntime
	}
	var encoded receiptSigningKey
	if strictRuntimeJSON(data, &encoded) != nil || encoded.SchemaVersion != 1 {
		return nil, "", errInvalidProductionRuntime
	}
	seed, ok := decodeRuntimeBase64(encoded.SeedB64)
	if !ok || len(seed) != ed25519.SeedSize {
		zero(seed)
		return nil, "", errInvalidProductionRuntime
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	zero(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return privateKey, runtimeSHA256Hex(publicKey), nil
}

func strictRuntimeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errInvalidProductionRuntime
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errInvalidProductionRuntime
	}
	return nil
}

func readRuntimeFile(path string, limit int64, private bool) ([]byte, error) {
	if path == "" {
		return nil, errInvalidProductionRuntime
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, errInvalidProductionRuntime
	}
	if private && info.Mode().Perm()&0o077 != 0 {
		return nil, errInvalidProductionRuntime
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errInvalidProductionRuntime
	}
	defer file.Close()
	reader := &io.LimitedReader{R: file, N: limit + 1}
	data, err := io.ReadAll(reader)
	if err != nil || int64(len(data)) > limit {
		zero(data)
		return nil, errInvalidProductionRuntime
	}
	return data, nil
}

func parseRuntimeCertificates(data []byte) ([]*x509.Certificate, error) {
	var certificates []*x509.Certificate
	rest := data
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errInvalidProductionRuntime
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errInvalidProductionRuntime
		}
		certificates = append(certificates, certificate)
		rest = next
	}
	if len(certificates) == 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errInvalidProductionRuntime
	}
	return certificates, nil
}

func certificateValidAt(certificate *x509.Certificate, now time.Time) bool {
	return certificate != nil && !now.Before(certificate.NotBefore) && !now.After(certificate.NotAfter)
}

func hasClientAuthUsage(certificate *x509.Certificate) bool {
	for _, usage := range certificate.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth {
			return true
		}
	}
	return false
}

func decodeRuntimeBase64(value string) ([]byte, bool) {
	if value == "" {
		return nil, false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != value {
		return nil, false
	}
	return decoded, true
}

func runtimeSHA256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
