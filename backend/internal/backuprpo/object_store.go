package backuprpo

import (
	"context"
	"errors"
	"io"
	"strings"
)

const MaxObjectBytes int64 = 1 << 30

var (
	ErrInvalidConfig       = errors.New("backuprpo: object store configuration is invalid")
	ErrInvalidRequest      = errors.New("backuprpo: object store request is invalid")
	ErrVersioningRequired  = errors.New("backuprpo: bucket versioning is unavailable")
	ErrStorageUnavailable  = errors.New("backuprpo: object store is unavailable")
	ErrPutOutcomeUnknown   = errors.New("backuprpo: object upload outcome is unknown")
	ErrObjectMismatch      = errors.New("backuprpo: exact object verification failed")
	ErrManifestInvalid     = errors.New("backuprpo: authenticated manifest verification failed")
	ErrReconcileUnresolved = errors.New("backuprpo: unknown upload remains unresolved")
	ErrReconcileAmbiguous  = errors.New("backuprpo: unknown upload reconciliation is ambiguous")
	ErrPaginationInvalid   = errors.New("backuprpo: object version pagination is invalid")
	ErrPaginationLimit     = errors.New("backuprpo: object version pagination limit exceeded")
)

type ObjectStore interface {
	CheckVersioning(context.Context) error
	PutImmutable(context.Context, PutRequest) (VersionID, error)
	GetExact(context.Context, ExactObjectRequest) (Readback, error)
	ReconcileUnknownPut(context.Context, ReconcileRequest) (VersionID, error)
}

type AuthenticatedManifestVerifier interface {
	VerifyAuthenticatedManifest(context.Context, io.Reader, ManifestExpectation) error
}

type ObjectMetadata struct {
	SHA256             string
	SizeBytes          int64
	CapturedGeneration int64
	RestoreEpoch       int64
	AttemptSequence    int64
	BackupID           string
	ManifestVersion    int64
	LeaseFence         int64
}

type PutRequest struct {
	Key      string
	Body     io.ReadSeeker
	Metadata ObjectMetadata
}

type ExactObjectRequest struct {
	Key       string
	VersionID VersionID
	Metadata  ObjectMetadata
}

type ReconcileRequest struct {
	Key      string
	Metadata ObjectMetadata
}

type ManifestExpectation struct {
	Key          string
	VersionID    VersionID
	Metadata     ObjectMetadata
	RestoreEpoch int64
}

type Readback struct {
	VersionID             VersionID
	SHA256                string
	SizeBytes             int64
	RestoreEpoch          int64
	ManifestAuthenticated bool
}

type VersionID struct {
	value string
}

func NewVersionID(value string) (VersionID, error) {
	if !validVersionID(value) {
		return VersionID{}, ErrInvalidRequest
	}
	return VersionID{value: value}, nil
}

func (version VersionID) String() string {
	return version.value
}

func BuildObjectKey(capturedGeneration, attemptSequence int64, backupID string) (string, error) {
	return BuildObjectKeyWithPrefix("backup-rpo", capturedGeneration, attemptSequence, backupID)
}

func BuildObjectKeyWithPrefix(prefix string, capturedGeneration, attemptSequence int64, backupID string) (string, error) {
	if capturedGeneration <= 0 || attemptSequence <= 0 || !canonicalLowerHex(backupID, 32) {
		return "", ErrInvalidRequest
	}
	tail := "g-" + decimal(capturedGeneration) + "/a-" + decimal(attemptSequence) + "-" + backupID + ".tar.gpg"
	key := tail
	if prefix != "" {
		if !validObjectPrefix(prefix) {
			return "", ErrInvalidRequest
		}
		key = prefix + "/" + tail
	}
	if len(key) > 1024 {
		return "", ErrInvalidRequest
	}
	return key, nil
}

func validObjectPrefix(prefix string) bool {
	if prefix == "" {
		return true
	}
	if len(prefix) > 1024 || !asciiAlphaNumeric(prefix[0]) || prefix[len(prefix)-1] == '/' {
		return false
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	for _, character := range prefix {
		if character > 127 || (!asciiAlphaNumeric(byte(character)) && character != '.' && character != '_' && character != '-' && character != '/') {
			return false
		}
	}
	return true
}

func asciiAlphaNumeric(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9')
}

func isValidObjectMetadata(metadata ObjectMetadata) bool {
	return canonicalLowerHex(metadata.SHA256, 64) &&
		metadata.SizeBytes > 0 && metadata.SizeBytes <= MaxObjectBytes &&
		metadata.CapturedGeneration > 0 && metadata.RestoreEpoch > 0 && metadata.AttemptSequence > 0 &&
		canonicalLowerHex(metadata.BackupID, 32) && metadata.ManifestVersion == 2 &&
		metadata.LeaseFence > 0
}

func validBoundKey(prefix, key string, metadata ObjectMetadata) bool {
	expected, err := BuildObjectKeyWithPrefix(prefix, metadata.CapturedGeneration, metadata.AttemptSequence, metadata.BackupID)
	return err == nil && key == expected
}

func validVersionID(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || value[0] == '"' || value[len(value)-1] == '"' {
		return false
	}
	switch strings.ToLower(value) {
	case "latest", "null", "none":
		return false
	}
	if looksLikeMultipartETag(value) {
		return false
	}
	return !foldedHex(value, 32) || value == strings.ToLower(value)
}

func looksLikeMultipartETag(value string) bool {
	if len(value) <= 33 || value[32] != '-' || !foldedHex(value[:32], 32) {
		return false
	}
	for _, character := range value[33:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func foldedHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') &&
			(character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

func canonicalLowerHex(value string, length int) bool {
	return len(value) == length && foldedHex(value, length) && value == strings.ToLower(value)
}
