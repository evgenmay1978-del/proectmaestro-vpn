package controlplane

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const DesiredPayloadVersion = 1

const (
	desiredPayloadUnavailableError     = "controlplane: desired payload protector is unavailable"
	invalidDesiredPayloadScopeError    = "controlplane: invalid desired payload scope"
	invalidDesiredPayloadEnvelopeError = "controlplane: invalid desired payload envelope"
	desiredPayloadAuthenticationError  = "controlplane: desired payload authentication failed"
	invalidDesiredPayloadDocumentError = "controlplane: invalid desired payload document"
)

type DesiredPayloadScope struct {
	NodeID, ServiceID, CustomerID, OperationID, PayloadKind string
	Generation                                              int64
	Tombstone                                               bool
}

type DesiredPayloadDocument struct {
	Version    int             `json:"version"`
	Kind       string          `json:"kind"`
	Body       json.RawMessage `json:"body,omitempty"`
	BodySHA256 string          `json:"body_sha256"`
}

func (b *SecretBox) SealDesiredPayload(scope DesiredPayloadScope, body any) (Envelope, string, error) {
	if b == nil {
		return Envelope{}, "", errors.New(desiredPayloadUnavailableError)
	}
	aad, err := desiredPayloadAAD(b.current, scope)
	if err != nil {
		return Envelope{}, "", err
	}
	aead, err := b.currentAEAD()
	if err != nil {
		return Envelope{}, "", errors.New(desiredPayloadUnavailableError)
	}
	canonicalBody, err := canonicalDesiredPayloadBody(body, scope.Tombstone)
	if err != nil {
		return Envelope{}, "", err
	}
	document := DesiredPayloadDocument{Version: DesiredPayloadVersion, Kind: scope.PayloadKind, Body: canonicalBody, BodySHA256: desiredPayloadDigest(canonicalBody)}
	plaintext, err := json.Marshal(document)
	if err != nil {
		return Envelope{}, "", errors.New(invalidDesiredPayloadDocumentError)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, "", errors.New(desiredPayloadUnavailableError)
	}
	envelope := Envelope{KeyVersion: b.current, Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plaintext, aad)}
	digest, err := desiredEnvelopeSHA256(envelope)
	if err != nil {
		return Envelope{}, "", err
	}
	return envelope, digest, nil
}

func (b *SecretBox) OpenDesiredPayload(scope DesiredPayloadScope, envelope Envelope, envelopeSHA256 string) (DesiredPayloadDocument, error) {
	if b == nil {
		return DesiredPayloadDocument{}, errors.New(desiredPayloadUnavailableError)
	}
	actualDigest, err := desiredEnvelopeSHA256(envelope)
	if err != nil || !equalDesiredPayloadDigest(actualDigest, envelopeSHA256) {
		return DesiredPayloadDocument{}, errors.New(invalidDesiredPayloadEnvelopeError)
	}
	aead, ok := b.aeadByVersion[envelope.KeyVersion]
	if !ok {
		return DesiredPayloadDocument{}, errors.New(invalidDesiredPayloadEnvelopeError)
	}
	return openDesiredPayloadDocument(aead, scope, envelope)
}

type desiredPayloadAEAD interface {
	NonceSize() int
	Overhead() int
	Open([]byte, []byte, []byte, []byte) ([]byte, error)
}

func openDesiredPayloadDocument(aead desiredPayloadAEAD, scope DesiredPayloadScope, envelope Envelope) (DesiredPayloadDocument, error) {
	aad, err := desiredPayloadAAD(envelope.KeyVersion, scope)
	if err != nil {
		return DesiredPayloadDocument{}, err
	}
	if len(envelope.Nonce) != aeadNonceSize(aead) || len(envelope.Ciphertext) < aeadOverhead(aead) {
		return DesiredPayloadDocument{}, errors.New(invalidDesiredPayloadEnvelopeError)
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return DesiredPayloadDocument{}, errors.New(desiredPayloadAuthenticationError)
	}
	defer wipeDesiredPayloadBytes(plaintext)
	document, err := decodeDesiredPayloadDocument(plaintext)
	if err != nil {
		return DesiredPayloadDocument{}, err
	}
	if document.Version != DesiredPayloadVersion || document.Kind != scope.PayloadKind {
		return DesiredPayloadDocument{}, errors.New(invalidDesiredPayloadDocumentError)
	}
	if len(document.Body) == 0 {
		if document.Body != nil || scope.Tombstone || !equalDesiredPayloadDigest(desiredPayloadDigest(nil), document.BodySHA256) {
			return DesiredPayloadDocument{}, errors.New(invalidDesiredPayloadDocumentError)
		}
	} else {
		canonicalBody, err := canonicalDesiredPayloadJSON(document.Body)
		if err != nil || !bytes.Equal(canonicalBody, document.Body) || !equalDesiredPayloadDigest(desiredPayloadDigest(document.Body), document.BodySHA256) {
			return DesiredPayloadDocument{}, errors.New(invalidDesiredPayloadDocumentError)
		}
		if scope.Tombstone && !bytes.Equal(document.Body, []byte(`{"tombstone":true}`)) {
			return DesiredPayloadDocument{}, errors.New(invalidDesiredPayloadDocumentError)
		}
	}
	canonicalDocument, err := json.Marshal(document)
	if err != nil || !bytes.Equal(canonicalDocument, plaintext) {
		return DesiredPayloadDocument{}, errors.New(invalidDesiredPayloadDocumentError)
	}
	return document, nil
}

func wipeDesiredPayloadBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func desiredPayloadAAD(version int, scope DesiredPayloadScope) ([]byte, error) {
	if version <= 0 {
		return nil, errors.New(invalidDesiredPayloadScopeError)
	}
	fields := []string{"maestrovpn:desired:v1", strconv.Itoa(version), scope.NodeID, scope.ServiceID, scope.CustomerID, strconv.FormatInt(scope.Generation, 10), scope.OperationID, strconv.FormatBool(scope.Tombstone), scope.PayloadKind}
	var out bytes.Buffer
	for _, field := range fields {
		if field == "" || len(field) > maxSecretScopePart || !utf8.ValidString(field) {
			return nil, errors.New(invalidDesiredPayloadScopeError)
		}
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(field)))
		_, _ = out.Write(length[:])
		_, _ = out.WriteString(field)
	}
	return out.Bytes(), nil
}

func canonicalDesiredPayloadBody(body any, tombstone bool) (json.RawMessage, error) {
	if body == nil && !tombstone {
		return nil, nil
	}
	if tombstone {
		return json.RawMessage(`{"tombstone":true}`), nil
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New(invalidDesiredPayloadDocumentError)
	}
	canonical, err := canonicalDesiredPayloadJSON(encoded)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(canonical), nil
}

func canonicalDesiredPayloadJSON(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New(invalidDesiredPayloadDocumentError)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New(invalidDesiredPayloadDocumentError)
	}
	if err := requireDesiredPayloadEOF(decoder); err != nil {
		return nil, err
	}
	canonicalValue, err := canonicalDesiredPayloadValue(value)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(canonicalValue)
	if err != nil {
		return nil, errors.New(invalidDesiredPayloadDocumentError)
	}
	return canonical, nil
}

func canonicalDesiredPayloadValue(value any) (any, error) {
	switch value := value.(type) {
	case json.Number:
		return canonicalDesiredPayloadNumber(value)
	case []any:
		for index := range value {
			canonical, err := canonicalDesiredPayloadValue(value[index])
			if err != nil {
				return nil, err
			}
			value[index] = canonical
		}
		return value, nil
	case map[string]any:
		for key, nested := range value {
			canonical, err := canonicalDesiredPayloadValue(nested)
			if err != nil {
				return nil, err
			}
			value[key] = canonical
		}
		return value, nil
	default:
		return value, nil
	}
}

func canonicalDesiredPayloadNumber(number json.Number) (json.Number, error) {
	raw := string(number)
	negative := false
	if raw[0] == '-' {
		negative = true
		raw = raw[1:]
	}
	exponent := int64(0)
	if index := strings.IndexAny(raw, "eE"); index >= 0 {
		parsed, err := strconv.ParseInt(raw[index+1:], 10, 64)
		if err != nil {
			return "", errors.New(invalidDesiredPayloadDocumentError)
		}
		exponent = parsed
		raw = raw[:index]
	}
	parts := strings.Split(raw, ".")
	digits := strings.TrimLeft(strings.Join(parts, ""), "0")
	if digits == "" {
		return json.Number("0"), nil
	}
	scale := int64(0)
	if len(parts) == 2 {
		scale = int64(len(parts[1]))
	}
	scale -= exponent
	for scale > 0 && strings.HasSuffix(digits, "0") {
		digits = strings.TrimSuffix(digits, "0")
		scale--
	}
	if scale > 4096 || scale < -4096 || int64(len(digits))+absDesiredPayloadInt64(scale) > 4096 {
		return "", errors.New(invalidDesiredPayloadDocumentError)
	}
	if scale <= 0 {
		digits += strings.Repeat("0", int(-scale))
	} else if scale >= int64(len(digits)) {
		digits = "0." + strings.Repeat("0", int(scale)-len(digits)) + digits
	} else {
		index := len(digits) - int(scale)
		digits = digits[:index] + "." + digits[index:]
	}
	if negative {
		digits = "-" + digits
	}
	return json.Number(digits), nil
}

func absDesiredPayloadInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func decodeDesiredPayloadDocument(plaintext []byte) (DesiredPayloadDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	var document DesiredPayloadDocument
	if err := decoder.Decode(&document); err != nil {
		return DesiredPayloadDocument{}, errors.New(invalidDesiredPayloadDocumentError)
	}
	if err := requireDesiredPayloadEOF(decoder); err != nil {
		return DesiredPayloadDocument{}, err
	}
	return document, nil
}

func requireDesiredPayloadEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New(invalidDesiredPayloadDocumentError)
	}
	return nil
}

func desiredEnvelopeSHA256(envelope Envelope) (string, error) {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", errors.New(invalidDesiredPayloadEnvelopeError)
	}
	return desiredPayloadDigest(encoded), nil
}

func desiredPayloadDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func equalDesiredPayloadDigest(expected, actual string) bool {
	return len(expected) == len(actual) && subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func aeadNonceSize(aead interface{ NonceSize() int }) int { return aead.NonceSize() }

func aeadOverhead(aead interface{ Overhead() int }) int { return aead.Overhead() }
