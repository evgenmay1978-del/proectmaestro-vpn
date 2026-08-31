// Package canary creates a strictly local, pre-candidate XHTTP configuration
// snapshot. It deliberately has no release, activation, or subscription links.
package canary

import (
	"bytes"
	"crypto/mlkem"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
)

const maxJSONBytes = 64 << 10

type Request struct {
	SchemaVersion            int    `json:"schema_version"`
	PublicHost               string `json:"public_host"`
	DiagnosticProbeURL       string `json:"diagnostic_probe_url"`
	DiagnosticResponseSHA256 string `json:"diagnostic_response_sha256"`
}

type Material struct {
	ClientID             string
	ClientEmail          string
	ServerDecryption     string
	ClientEncryption     string
	PairTranscriptSHA256 string
	SecretPath           string
}

type Snapshot struct {
	SchemaVersion int              `json:"schema_version"`
	Request       Request          `json:"request"`
	Material      snapshotMaterial `json:"material"`
	Provenance    XrayProvenance   `json:"xray_provenance"`
}

type snapshotMaterial struct {
	ClientID             string `json:"client_id"`
	ClientEmail          string `json:"client_email"`
	ServerDecryption     string `json:"server_decryption"`
	ClientEncryption     string `json:"client_encryption"`
	PairTranscriptSHA256 string `json:"pair_transcript_sha256"`
	SecretPath           string `json:"secret_path"`
}

type codedError struct{ code string }

func (e codedError) Error() string { return e.code }
func invalid(code string) error    { return codedError{code} }

func ParseRequest(raw []byte) (Request, error) {
	var request Request
	if err := decodeCanonical(raw, &request); err != nil {
		return Request{}, err
	}
	if err := validateRequest(request); err != nil {
		return Request{}, err
	}
	return request, nil
}

func NewSnapshot(request Request, material Material) (Snapshot, error) {
	if err := validateRequest(request); err != nil {
		return Snapshot{}, err
	}
	stored := snapshotMaterial{material.ClientID, material.ClientEmail, material.ServerDecryption, material.ClientEncryption, material.PairTranscriptSHA256, material.SecretPath}
	if err := validateMaterial(stored); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{SchemaVersion: 1, Request: request, Material: stored, Provenance: pinnedProvenance()}, nil
}

func ParseSnapshot(raw []byte) (Snapshot, error) {
	var snapshot Snapshot
	if err := decodeCanonical(raw, &snapshot); err != nil {
		return Snapshot{}, err
	}
	if snapshot.SchemaVersion != 1 {
		return Snapshot{}, invalid("snapshot_schema_invalid")
	}
	if err := validateRequest(snapshot.Request); err != nil {
		return Snapshot{}, err
	}
	if err := validateMaterial(snapshot.Material); err != nil {
		return Snapshot{}, err
	}
	if snapshot.Provenance != pinnedProvenance() {
		return Snapshot{}, invalid("xray_provenance_invalid")
	}
	return snapshot, nil
}

func (s Snapshot) CanonicalJSON() []byte {
	raw, _ := json.Marshal(s)
	return append([]byte(nil), raw...)
}
func (s Snapshot) SHA256() string {
	sum := sha256.Sum256(s.CanonicalJSON())
	return hex.EncodeToString(sum[:])
}

func validateRequest(request Request) error {
	if request.SchemaVersion != 1 || !safeHost(request.PublicHost) {
		return invalid("request_host_invalid")
	}
	u, err := url.ParseRequestURI(request.DiagnosticProbeURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Host != request.PublicHost ||
		u.Path == "" || u.RawPath != "" || strings.Contains(request.DiagnosticProbeURL, "%") || strings.ContainsAny(u.Path, "\\\r\n") ||
		strings.Contains(u.Path, "//") || path.Clean(u.Path) != u.Path || !strings.HasPrefix(u.Path, "/") {
		return invalid("request_url_invalid")
	}
	if !validDigest(request.DiagnosticResponseSHA256) {
		return invalid("request_digest_invalid")
	}
	return nil
}

func validateMaterial(material snapshotMaterial) error {
	if !validUUID(material.ClientID) {
		return invalid("material_client_id_invalid")
	}
	if !safeEmail(material.ClientEmail) {
		return invalid("material_email_invalid")
	}
	if !validVLESSPair(material.ServerDecryption, material.ClientEncryption) {
		return invalid("material_vless_pair_invalid")
	}
	if !safePath(material.SecretPath) {
		return invalid("material_secret_path_invalid")
	}
	if !validDigest(material.PairTranscriptSHA256) || subtle.ConstantTimeCompare([]byte(material.PairTranscriptSHA256), []byte(pairTranscript(material))) != 1 {
		return invalid("material_pair_evidence_invalid")
	}
	return nil
}

func pairTranscript(material snapshotMaterial) string {
	raw, _ := json.Marshal(struct {
		ClientID         string `json:"client_id"`
		ClientEmail      string `json:"client_email"`
		ServerDecryption string `json:"server_decryption"`
		ClientEncryption string `json:"client_encryption"`
		SecretPath       string `json:"secret_path"`
	}{material.ClientID, material.ClientEmail, material.ServerDecryption, material.ClientEncryption, material.SecretPath})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func decodeCanonical(raw []byte, destination any) error {
	if len(raw) == 0 || len(raw) > maxJSONBytes {
		return invalid("json_size_invalid")
	}
	if !utf8.Valid(raw) {
		return invalid("json_utf8_invalid")
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalid("json_invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalid("json_trailing_data")
	}
	canonical, err := json.Marshal(destination)
	if err != nil || !bytes.Equal(raw, canonical) {
		return invalid("json_not_canonical")
	}
	return nil
}

func rejectDuplicateKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := scanValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return invalid("json_trailing_data")
	}
	return nil
}
func scanValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return invalid("json_invalid")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return invalid("json_invalid")
			}
			text, ok := key.(string)
			if !ok {
				return invalid("json_invalid")
			}
			if _, exists := seen[text]; exists {
				return invalid("json_duplicate_key")
			}
			seen[text] = struct{}{}
			if err := scanValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanValue(decoder); err != nil {
				return err
			}
		}
	default:
		return invalid("json_invalid")
	}
	if _, err := decoder.Token(); err != nil {
		return invalid("json_invalid")
	}
	return nil
}

func safeHost(host string) bool {
	if len(host) == 0 || len(host) > 253 || host != strings.ToLower(host) || net.ParseIP(host) != nil || strings.Contains(host, "..") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				return false
			}
		}
	}
	return strings.Contains(host, ".")
}
func safeEmail(email string) bool {
	return len(email) > 2 && len(email) <= 254 && !strings.ContainsAny(email, "\r\n\x00") && strings.Count(email, "@") == 1
}
func safePath(path string) bool {
	if len(path) < 2 || len(path) > 256 || !strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.ContainsAny(path, "?#\\\r\n\x00") {
		return false
	}
	for _, c := range path {
		if !(c == '/' || c == '-' || c == '_' || c == '.' || c == '~' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, c := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return value != "00000000-0000-0000-0000-000000000000"
}
func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func decodeVLESSMaterial(value, prefix string, expectedLength int) ([]byte, bool) {
	if !strings.HasPrefix(value, prefix) {
		return nil, false
	}
	material := strings.TrimPrefix(value, prefix)
	decoded, err := base64.RawURLEncoding.DecodeString(material)
	if err != nil || len(decoded) != expectedLength {
		return nil, false
	}
	return decoded, true

}

func validVLESSPair(serverDecryption, clientEncryption string) bool {
	serverSeed, ok := decodeVLESSMaterial(serverDecryption, "mlkem768x25519plus.native.600s.", 64)
	if !ok {
		return false
	}
	clientPublicKey, ok := decodeVLESSMaterial(clientEncryption, "mlkem768x25519plus.native.0rtt.", 1184)
	if !ok {
		return false
	}
	key, err := mlkem.NewDecapsulationKey768(serverSeed)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(key.EncapsulationKey().Bytes(), clientPublicKey) == 1
}
