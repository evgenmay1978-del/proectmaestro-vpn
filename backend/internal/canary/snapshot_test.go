package canary_test

import (
	"bytes"
	"crypto/mlkem"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/canary"
)

func testRequest() canary.Request {
	return canary.Request{
		SchemaVersion:            1,
		PublicHost:               "cdn.example.invalid",
		DiagnosticProbeURL:       "https://cdn.example.invalid/health",
		DiagnosticResponseSHA256: strings.Repeat("a", 64),
	}
}

func testMaterial() canary.Material {
	seed := bytes.Repeat([]byte{0x42}, 64)
	key, err := mlkem.NewDecapsulationKey768(seed)
	if err != nil {
		panic(err)
	}
	material := canary.Material{
		ClientID:         "123e4567-e89b-12d3-a456-426614174000",
		ClientEmail:      "canary@example.invalid",
		ServerDecryption: "mlkem768x25519plus.native.600s." + base64.RawURLEncoding.EncodeToString(seed),
		ClientEncryption: "mlkem768x25519plus.native.0rtt." + base64.RawURLEncoding.EncodeToString(key.EncapsulationKey().Bytes()),
		SecretPath:       "/xhttp-canary",
	}
	material.PairTranscriptSHA256 = pairDigest(material)
	return material
}

func pairDigest(material canary.Material) string {
	raw, err := json.Marshal(struct {
		ClientID         string `json:"client_id"`
		ClientEmail      string `json:"client_email"`
		ServerDecryption string `json:"server_decryption"`
		ClientEncryption string `json:"client_encryption"`
		SecretPath       string `json:"secret_path"`
	}{material.ClientID, material.ClientEmail, material.ServerDecryption, material.ClientEncryption, material.SecretPath})
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func TestSnapshotCanonicalRoundTrip(t *testing.T) {
	snapshot, err := canary.NewSnapshot(testRequest(), testMaterial())
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	raw := snapshot.CanonicalJSON()
	parsed, err := canary.ParseSnapshot(raw)
	if err != nil {
		t.Fatalf("ParseSnapshot: %v", err)
	}
	if got := string(parsed.CanonicalJSON()); got != string(raw) {
		t.Fatalf("canonical round trip changed bytes: %s", got)
	}
	if got := parsed.SHA256(); len(got) != 64 || got != snapshot.SHA256() {
		t.Fatalf("unexpected snapshot digest: %q", got)
	}

	raw[0] = '!'
	if string(snapshot.CanonicalJSON()) == string(raw) {
		t.Fatal("CanonicalJSON exposed mutable backing bytes")
	}
}

func TestParseRequestRejectsUntrustedJSON(t *testing.T) {
	valid, err := json.Marshal(testRequest())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		raw  []byte
	}{
		{"unknown", append([]byte(`{"unexpected":true,`), valid[1:]...)},
		{"duplicate", []byte(`{"schema_version":1,"schema_version":1,"public_host":"cdn.example.invalid","diagnostic_probe_url":"https://cdn.example.invalid/health","diagnostic_response_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)},
		{"trailing", append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{"oversize", []byte(`{"schema_version":1,"public_host":"` + strings.Repeat("a", 65536) + `","diagnostic_probe_url":"https://cdn.example.invalid/health","diagnostic_response_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)},
		{"invalid utf8", append([]byte{0xff}, valid...)},
		{"unsafe host", []byte(`{"schema_version":1,"public_host":"cdn.example.invalid:443","diagnostic_probe_url":"https://cdn.example.invalid/health","diagnostic_response_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)},
		{"unsafe url", []byte(`{"schema_version":1,"public_host":"cdn.example.invalid","diagnostic_probe_url":"http://cdn.example.invalid/health","diagnostic_response_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)},
		{"escaped dot", []byte(`{"schema_version":1,"public_host":"cdn.example.invalid","diagnostic_probe_url":"https://cdn.example.invalid/%2e%2e/health","diagnostic_response_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)},
		{"escaped slash", []byte(`{"schema_version":1,"public_host":"cdn.example.invalid","diagnostic_probe_url":"https://cdn.example.invalid/a%2fb","diagnostic_response_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)},
		{"escaped backslash", []byte(`{"schema_version":1,"public_host":"cdn.example.invalid","diagnostic_probe_url":"https://cdn.example.invalid/a%5cb","diagnostic_response_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)},
		{"escaped newline", []byte(`{"schema_version":1,"public_host":"cdn.example.invalid","diagnostic_probe_url":"https://cdn.example.invalid/a%0ab","diagnostic_response_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := canary.ParseRequest(tc.raw); err == nil {
				t.Fatal("ParseRequest accepted untrusted JSON")
			}
		})
	}
}

func TestNewSnapshotRejectsInvalidMaterialAndPins(t *testing.T) {
	cases := []struct {
		name   string
		modify func(*canary.Material)
	}{
		{"malformed uuid", func(m *canary.Material) { m.ClientID = "not-a-uuid" }},
		{"server encryption role", func(m *canary.Material) { m.ServerDecryption = "none" }},
		{"client encryption role", func(m *canary.Material) { m.ClientEncryption = "none" }},
		{"swapped server encryption role", func(m *canary.Material) { m.ServerDecryption = m.ClientEncryption }},
		{"swapped client encryption role", func(m *canary.Material) { m.ClientEncryption = m.ServerDecryption }},
		{"wrong algorithm", func(m *canary.Material) { m.ClientEncryption = "x25519.native.0rtt." + strings.Repeat("A", 1579) }},
		{"unsafe client encryption material", func(m *canary.Material) { m.ClientEncryption = "mlkem768x25519plus.native.0rtt.not+base64url" }},
		{"short server encryption material", func(m *canary.Material) { m.ServerDecryption = "mlkem768x25519plus.native.600s.short" }},
		{"truncated client material", func(m *canary.Material) { m.ClientEncryption = m.ClientEncryption[:len(m.ClientEncryption)-1] }},
		{"mismatched pair", func(m *canary.Material) {
			m.ClientEncryption = "mlkem768x25519plus.native.0rtt." + strings.Repeat("A", 1579)
			m.PairTranscriptSHA256 = pairDigest(*m)
		}},
		{"mixed pair evidence", func(m *canary.Material) { m.ClientEmail = "other@example.invalid" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			material := testMaterial()
			tc.modify(&material)
			if _, err := canary.NewSnapshot(testRequest(), material); err == nil {
				t.Fatal("NewSnapshot accepted invalid material")
			}
		})
	}

	snapshot, err := canary.NewSnapshot(testRequest(), testMaterial())
	if err != nil {
		t.Fatal(err)
	}
	raw := snapshot.CanonicalJSON()
	if !strings.Contains(string(raw), `"version":"26.7.28"`) {
		t.Fatal("snapshot omitted pinned provenance")
	}
	for _, substitution := range []struct{ old, replacement string }{
		{`"version":"26.7.28"`, `"version":"26.7.29"`},
		{`"source_archive_sha256":"f7e2426b267f24aabdc72868bf85ebe100df9cce50ed90595a5c959ad188bf70"`, `"source_archive_sha256":"` + strings.Repeat("b", 64) + `"`},
		{`"binary_sha256":"64d46afb80adea1bf97a0d467e83f4a9ac1ebd0995891e84bca3f1a1d1affb1d"`, `"binary_sha256":"` + strings.Repeat("c", 64) + `"`},
	} {
		mutated := []byte(strings.Replace(string(raw), substitution.old, substitution.replacement, 1))
		if string(mutated) == string(raw) {
			t.Fatal("pin substitution did not alter fixture")
		}
		if _, err := canary.ParseSnapshot(mutated); err == nil {
			t.Fatal("ParseSnapshot accepted substituted provenance")
		}
	}
}
