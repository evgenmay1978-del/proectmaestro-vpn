package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const vlessRotationInputLimit = 64 << 10

var errVLESSRotation = errors.New("credential rotation preparation failed")

type vlessRotationInput struct {
	KeyBundleFile        string                `json:"key_bundle_file"`
	CustomerID           string                `json:"customer_id"`
	CredentialID         string                `json:"credential_id"`
	CustomerGeneration   int64                 `json:"customer_generation"`
	CredentialGeneration int64                 `json:"credential_generation"`
	OldUUID              string                `json:"old_uuid"`
	OldSecretSHA256      string                `json:"old_secret_sha256"`
	OldEnvelope          controlplane.Envelope `json:"old_envelope"`
}

type vlessRotationPlan struct {
	Version                      int                   `json:"version"`
	CustomerID                   string                `json:"customer_id"`
	CredentialID                 string                `json:"credential_id"`
	ExpectedCustomerGeneration   int64                 `json:"expected_customer_generation"`
	ExpectedCredentialGeneration int64                 `json:"expected_credential_generation"`
	OldUUID                      string                `json:"old_uuid"`
	NewUUID                      string                `json:"new_uuid"`
	OldSecretSHA256              string                `json:"old_secret_sha256"`
	NewSecretSHA256              string                `json:"new_secret_sha256"`
	OldEnvelope                  controlplane.Envelope `json:"old_envelope"`
	NewEnvelope                  controlplane.Envelope `json:"new_envelope"`
}

// This mode exits before any server/database initialization. It only writes an offline plan.
func runVLESSRotationTool(args []string) int {
	if len(args) != 2 || prepareVLESSRotation(args[0], args[1]) != nil {
		fmt.Println(`{"ready":false}`)
		return 1
	}
	fmt.Println(`{"ready":true}`)
	return 0
}

func prepareVLESSRotation(inputPath, outputPath string) error {
	info, err := rotationPrivateFile(inputPath, vlessRotationInputLimit)
	if err != nil || info.Mode().Perm() != 0o600 || rotationPrivateParents(outputPath) != nil {
		return errVLESSRotation
	}
	file, err := os.Open(inputPath)
	if err != nil {
		return errVLESSRotation
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errVLESSRotation
	}
	raw, err := io.ReadAll(io.LimitReader(file, vlessRotationInputLimit+1))
	defer clear(raw)
	if err != nil || len(raw) != int(info.Size()) || len(raw) > vlessRotationInputLimit {
		return errVLESSRotation
	}
	var input vlessRotationInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF ||
		!rotationIdentifier(input.CustomerID) || !rotationIdentifier(input.CredentialID) ||
		input.CustomerGeneration <= 0 || input.CustomerGeneration == math.MaxInt64 ||
		input.CredentialGeneration <= 0 || input.CredentialGeneration == math.MaxInt64 ||
		input.OldEnvelope.KeyVersion <= 0 || len(input.OldEnvelope.Nonce) != 12 ||
		len(input.OldEnvelope.Ciphertext) != 36+16 || !rotationUUID(input.OldUUID) {
		return errVLESSRotation
	}
	expectedDigest, err := hex.DecodeString(input.OldSecretSHA256)
	if err != nil || len(expectedDigest) != sha256.Size || hex.EncodeToString(expectedDigest) != input.OldSecretSHA256 {
		return errVLESSRotation
	}
	keyInfo, err := rotationPrivateFile(input.KeyBundleFile, runtimeKeyBundleLimit)
	if err != nil {
		return errVLESSRotation
	}
	box, err := loadRuntimeSecretBox(input.KeyBundleFile)
	keyAfter, statErr := rotationPrivateFile(input.KeyBundleFile, runtimeKeyBundleLimit)
	if err != nil || statErr != nil || !os.SameFile(keyInfo, keyAfter) ||
		keyInfo.Size() != keyAfter.Size() || !keyInfo.ModTime().Equal(keyAfter.ModTime()) ||
		box.ReadyForVersions(input.OldEnvelope.KeyVersion) != nil {
		return errVLESSRotation
	}
	scope := controlplane.SecretScope{OwnerType: "customer", OwnerID: input.CustomerID, Field: "credential", Kind: "vless"}
	oldPlain, err := box.Open(scope, input.OldEnvelope)
	defer clear(oldPlain)
	if err != nil || subtle.ConstantTimeCompare(oldPlain, []byte(input.OldUUID)) != 1 {
		return errVLESSRotation
	}
	oldDigest := sha256.Sum256(oldPlain)
	if subtle.ConstantTimeCompare(oldDigest[:], expectedDigest) != 1 {
		return errVLESSRotation
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errVLESSRotation
	}
	defer clear(random[:])
	random[6] = random[6]&0x0f | 0x40
	random[8] = random[8]&0x3f | 0x80
	newUUID := fmt.Sprintf("%x-%x-%x-%x-%x", random[:4], random[4:6], random[6:8], random[8:10], random[10:])
	if newUUID == input.OldUUID {
		return errVLESSRotation
	}
	newPlain := []byte(newUUID)
	defer clear(newPlain)
	newEnvelope, err := box.Seal(scope, newPlain)
	if err != nil || newEnvelope.KeyVersion < input.OldEnvelope.KeyVersion {
		return errVLESSRotation
	}
	verified, err := box.Open(scope, newEnvelope)
	defer clear(verified)
	if err != nil || subtle.ConstantTimeCompare(verified, newPlain) != 1 {
		return errVLESSRotation
	}
	newDigest := sha256.Sum256(newPlain)
	plan := vlessRotationPlan{
		Version: 1, CustomerID: input.CustomerID, CredentialID: input.CredentialID,
		ExpectedCustomerGeneration: input.CustomerGeneration, ExpectedCredentialGeneration: input.CredentialGeneration,
		OldUUID: input.OldUUID, NewUUID: newUUID,
		OldSecretSHA256: input.OldSecretSHA256, NewSecretSHA256: hex.EncodeToString(newDigest[:]),
		OldEnvelope: input.OldEnvelope, NewEnvelope: newEnvelope,
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	defer clear(encoded)
	if err != nil {
		return errVLESSRotation
	}
	output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errVLESSRotation
	}
	complete := false
	defer func() {
		output.Close()
		if !complete {
			os.Remove(outputPath)
		}
	}()
	outInfo, err := output.Stat()
	if err != nil || !legacyPrimaryRootOwned(outInfo) || outInfo.Mode().Perm() != 0o600 {
		return errVLESSRotation
	}
	if _, err := output.Write(encoded); err != nil || output.Sync() != nil || output.Close() != nil {
		return errVLESSRotation
	}
	complete = true
	return nil
}

func rotationPrivateFile(path string, limit int64) (os.FileInfo, error) {
	if rotationPrivateParents(path) != nil {
		return nil, errVLESSRotation
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || !legacyPrimaryRootOwned(info) ||
		(info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o400) || info.Size() <= 0 || info.Size() > limit {
		return nil, errVLESSRotation
	}
	return info, nil
}

func rotationPrivateParents(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errVLESSRotation
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || !legacyPrimaryRootOwned(info) || info.Mode().Perm()&0o022 != 0 {
			return errVLESSRotation
		}
		if filepath.Dir(parent) == parent {
			return nil
		}
	}
}

func rotationIdentifier(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) < 0
}

func rotationUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(compact)
	return err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == compact
}
