package controlplane

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	secretKeyBytes     = 32
	maxSecretScopePart = 1024
	maxLoginKeyBytes   = 128
	maxIDPrefixBytes   = 24
)

// SecretBox protects durable credentials with versioned AES-256-GCM keys and a
// distinct stable HMAC key used only for lookup identifiers.
type SecretBox struct {
	current       int
	aeadByVersion map[int]cipher.AEAD
	hmacKey       []byte
}

// NewSecretBox constructs a fail-closed credential protector. Previous
// encryption versions may be supplied for reads; all new seals use current.
func NewSecretBox(current int, encryptionKeys map[int][]byte, hmacKey []byte) (*SecretBox, error) {
	if current <= 0 || len(hmacKey) != secretKeyBytes {
		return nil, errors.New("controlplane: invalid secret key configuration")
	}
	aeadByVersion := make(map[int]cipher.AEAD, len(encryptionKeys))
	for version, key := range encryptionKeys {
		if version <= 0 || len(key) != secretKeyBytes || bytes.Equal(key, hmacKey) {
			return nil, errors.New("controlplane: invalid secret key configuration")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, errors.New("controlplane: invalid secret key configuration")
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, errors.New("controlplane: invalid secret key configuration")
		}
		aeadByVersion[version] = aead
	}
	if _, ok := aeadByVersion[current]; !ok {
		return nil, errors.New("controlplane: current secret key version is unavailable")
	}
	return &SecretBox{
		current:       current,
		aeadByVersion: aeadByVersion,
		hmacKey:       append([]byte(nil), hmacKey...),
	}, nil
}

// Seal encrypts plaintext with a fresh nonce and scope-bound authenticated
// data. Plaintext is never retained by SecretBox.
func (b *SecretBox) Seal(scope SecretScope, plaintext []byte) (Envelope, error) {
	if b == nil {
		return Envelope{}, errors.New("controlplane: secret box is unavailable")
	}
	aead, err := b.currentAEAD()
	if err != nil {
		return Envelope{}, err
	}
	aad, err := secretAAD(b.current, scope)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, errors.New("controlplane: generate secret nonce")
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	return Envelope{
		KeyVersion: b.current,
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}, nil
}

func (b *SecretBox) currentAEAD() (cipher.AEAD, error) {
	aead, ok := b.aeadByVersion[b.current]
	if !ok {
		return nil, errors.New("controlplane: current secret key version is unavailable")
	}
	return aead, nil
}

// Open authenticates both the envelope and its exact owning scope.
func (b *SecretBox) Open(scope SecretScope, envelope Envelope) ([]byte, error) {
	if b == nil {
		return nil, errors.New("controlplane: secret box is unavailable")
	}
	aead, ok := b.aeadByVersion[envelope.KeyVersion]
	if !ok {
		return nil, errors.New("controlplane: referenced secret key version is unavailable")
	}
	aad, err := secretAAD(envelope.KeyVersion, scope)
	if err != nil {
		return nil, err
	}
	if len(envelope.Nonce) != aead.NonceSize() || len(envelope.Ciphertext) < aead.Overhead() {
		return nil, errors.New("controlplane: invalid secret envelope")
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return nil, errors.New("controlplane: secret authentication failed")
	}
	return plaintext, nil
}

// ReadyForVersions fails readiness when any durable envelope references a key
// version that this process cannot decrypt.
func (b *SecretBox) ReadyForVersions(versions ...int) error {
	if b == nil {
		return errors.New("controlplane: secret box is unavailable")
	}
	for _, version := range versions {
		if version <= 0 {
			return errors.New("controlplane: invalid referenced secret key version")
		}
		if _, ok := b.aeadByVersion[version]; !ok {
			return errors.New("controlplane: referenced secret key version is unavailable")
		}
	}
	return nil
}

// LookupHMAC creates a deterministic, kind-separated lookup key without
// revealing the source value.
func (b *SecretBox) LookupHMAC(kind string, plaintext []byte) string {
	if b == nil || len(b.hmacKey) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, b.hmacKey)
	_, _ = mac.Write([]byte(kind + "\x00"))
	_, _ = mac.Write(plaintext)
	return hex.EncodeToString(mac.Sum(nil))
}

// NewID returns a cryptographically random identifier with a validated,
// readable resource prefix.
func NewID(prefix string) (string, error) {
	if !validIDPrefix(prefix) {
		return "", errors.New("controlplane: invalid identifier prefix")
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", errors.New("controlplane: generate identifier")
	}
	return prefix + "_" + hex.EncodeToString(random), nil
}

// CanonicalLoginKey normalizes a login for stable keyed lookup. Display casing
// remains a separate presentation concern.
func CanonicalLoginKey(login string) (string, error) {
	canonical := strings.ToLower(strings.TrimSpace(login))
	if canonical == "" || len(canonical) > maxLoginKeyBytes || !utf8.ValidString(canonical) {
		return "", errors.New("controlplane: invalid login key")
	}
	for _, r := range canonical {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", errors.New("controlplane: invalid login key")
		}
	}
	return canonical, nil
}

func secretAAD(keyVersion int, scope SecretScope) ([]byte, error) {
	parts := []string{scope.OwnerType, scope.OwnerID, scope.Field, scope.Kind}
	for _, part := range parts {
		if part == "" || len(part) > maxSecretScopePart || !utf8.ValidString(part) {
			return nil, errors.New("controlplane: invalid secret scope")
		}
	}
	size := len("maestro-secret-v1") + 8
	for _, part := range parts {
		size += 4 + len(part)
	}
	aad := make([]byte, 0, size)
	aad = append(aad, "maestro-secret-v1"...)
	var number [8]byte
	binary.BigEndian.PutUint64(number[:], uint64(keyVersion))
	aad = append(aad, number[:]...)
	for _, part := range parts {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(part)))
		aad = append(aad, length[:]...)
		aad = append(aad, part...)
	}
	return aad, nil
}

func validIDPrefix(prefix string) bool {
	if len(prefix) == 0 || len(prefix) > maxIDPrefixBytes {
		return false
	}
	for i, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		case i > 0 && r == '-':
		default:
			return false
		}
	}
	return true
}
