package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion     = 1
	ManagedPrefix     = "wl:"
	DefaultInboundTag = "maestro-cdn-in"
	DefaultReceiptTTL = 30 * time.Second
	RefreshInterval   = 10 * time.Second
	MaxDesiredBytes   = 1 << 20
)

var (
	ErrConflict        = errors.New("sidecar agent: desired state conflicts with stored generation")
	ErrInvalidDesired  = errors.New("sidecar agent: invalid desired state")
	ErrNotFound        = errors.New("sidecar agent: state not found")
	ErrStaleGeneration = errors.New("sidecar agent: stale desired generation")
)

// Desired is byte-for-byte compatible with Task 11's canonical
// whiteListSidecarPayload. The request SHA and action key are derived locally
// and are deliberately excluded from the wire object.
type Desired struct {
	Version              int      `json:"version"`
	OriginID             string   `json:"origin_id"`
	NodeID               string   `json:"node_id"`
	ReleaseID            string   `json:"release_id"`
	ProfileID            string   `json:"profile_id"`
	PresetID             string   `json:"preset_id"`
	ExitID               string   `json:"exit_id"`
	Generation           int64    `json:"generation"`
	ConfigDigest         string   `json:"config_digest"`
	ManagedUserSetDigest string   `json:"managed_user_set_digest"`
	StaticUsers          []string `json:"static_users"`
	ManagedUsers         []string `json:"managed_users"`

	canonicalJSON []byte
	desiredSHA256 string
}

type Receipt struct {
	ActionKey            string    `json:"action_key"`
	OriginID             string    `json:"origin_id"`
	ReleaseID            string    `json:"release_id"`
	XrayProcessBootID    string    `json:"xray_process_boot_id"`
	ConfigDigest         string    `json:"config_digest"`
	DesiredGeneration    int64     `json:"desired_generation"`
	ManagedUserSetDigest string    `json:"managed_user_set_digest"`
	AppliedAt            time.Time `json:"applied_at"`
	ExpiresAt            time.Time `json:"expires_at"`
}

func ParseDesired(raw []byte) (Desired, error) {
	if len(raw) == 0 || len(raw) > MaxDesiredBytes || !utf8.Valid(raw) {
		return Desired{}, ErrInvalidDesired
	}
	var desired Desired
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&desired); err != nil {
		return Desired{}, ErrInvalidDesired
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Desired{}, ErrInvalidDesired
	}
	canonical, err := json.Marshal(desired)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Desired{}, ErrInvalidDesired
	}
	desired.canonicalJSON = append([]byte(nil), raw...)
	digest := sha256.Sum256(raw)
	desired.desiredSHA256 = hex.EncodeToString(digest[:])
	if err := desired.validate(); err != nil {
		return Desired{}, err
	}
	return desired, nil
}

func ManagedUserSetDigest(users []string) (string, error) {
	if users == nil {
		return "", ErrInvalidDesired
	}
	encoded, err := json.Marshal(users)
	if err != nil {
		return "", ErrInvalidDesired
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (desired Desired) DesiredSHA256() string {
	return desired.desiredSHA256
}

func (desired Desired) ActionKey() string {
	if desired.NodeID == "" || desired.Generation < 1 || desired.desiredSHA256 == "" {
		return ""
	}
	return desired.NodeID + ":" + strconv.FormatInt(desired.Generation, 10) + ":" + desired.desiredSHA256
}

func (desired Desired) CanonicalJSON() []byte {
	return append([]byte(nil), desired.canonicalJSON...)
}

func (receipt Receipt) ReadyAt(now time.Time) bool {
	now = now.UTC()
	return receipt.ActionKey != "" && receipt.OriginID != "" && receipt.ReleaseID != "" &&
		receipt.XrayProcessBootID != "" && validDigest(receipt.ConfigDigest) &&
		receipt.DesiredGeneration > 0 && validDigest(receipt.ManagedUserSetDigest) &&
		!receipt.AppliedAt.IsZero() && !receipt.ExpiresAt.IsZero() &&
		!receipt.AppliedAt.After(now) && receipt.ExpiresAt.After(now) &&
		receipt.ExpiresAt.After(receipt.AppliedAt)
}

func (desired Desired) validate() error {
	canonical, err := json.Marshal(desired)
	digest := sha256.Sum256(canonical)
	if err != nil || !bytes.Equal(canonical, desired.canonicalJSON) ||
		hex.EncodeToString(digest[:]) != desired.desiredSHA256 {
		return ErrInvalidDesired
	}
	if desired.Version != SchemaVersion || !safeIdentifier(desired.OriginID) ||
		!safeIdentifier(desired.NodeID) || !safeIdentifier(desired.ReleaseID) ||
		!safeIdentifier(desired.ProfileID) || !safeIdentifier(desired.PresetID) ||
		!supportedExit(desired.ExitID) || desired.Generation < 1 ||
		!validDigest(desired.ConfigDigest) || !validDigest(desired.ManagedUserSetDigest) ||
		desired.StaticUsers == nil || desired.ManagedUsers == nil ||
		!strictlySortedUnique(desired.StaticUsers) || !strictlySortedUnique(desired.ManagedUsers) {
		return ErrInvalidDesired
	}
	for _, email := range desired.StaticUsers {
		if strings.HasPrefix(email, ManagedPrefix) || !safeEmail(email) {
			return ErrInvalidDesired
		}
	}
	for _, email := range desired.ManagedUsers {
		if !managedEmailForExit(email, desired.ExitID) {
			return ErrInvalidDesired
		}
	}
	managedDigest, err := ManagedUserSetDigest(desired.ManagedUsers)
	if err != nil || managedDigest != desired.ManagedUserSetDigest {
		return ErrInvalidDesired
	}
	return nil
}

func managedEmailForExit(email, exitID string) bool {
	if !safeEmail(email) || !strings.HasPrefix(email, ManagedPrefix) {
		return false
	}
	parts := strings.Split(email, ":")
	return len(parts) == 3 && parts[0] == "wl" && parts[1] != "" && parts[2] == exitID
}

func supportedExit(exitID string) bool {
	switch exitID {
	case "exit-s1", "exit-s2", "exit-s3", "exit-s4":
		return true
	default:
		return false
	}
}

func strictlySortedUnique(values []string) bool {
	return sort.SliceIsSorted(values, func(i, j int) bool { return values[i] < values[j] }) &&
		!hasDuplicate(values)
}

func hasDuplicate(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return true
		}
	}
	return false
}

func safeIdentifier(value string) bool {
	return value != "" && len(value) <= 256 && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "\x00\r\n\t")
}

func safeEmail(value string) bool {
	return safeIdentifier(value) && !strings.ContainsAny(value, " /\\")
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func receiptFor(desired Desired, bootID string, appliedAt time.Time, ttl time.Duration) (Receipt, error) {
	if bootID == "" || ttl <= 0 || desired.ActionKey() == "" {
		return Receipt{}, fmt.Errorf("sidecar agent: cannot build receipt")
	}
	appliedAt = appliedAt.UTC().Truncate(time.Second)
	return Receipt{
		ActionKey: desired.ActionKey(), OriginID: desired.OriginID, ReleaseID: desired.ReleaseID,
		XrayProcessBootID: bootID, ConfigDigest: desired.ConfigDigest,
		DesiredGeneration: desired.Generation, ManagedUserSetDigest: desired.ManagedUserSetDigest,
		AppliedAt: appliedAt, ExpiresAt: appliedAt.Add(ttl),
	}, nil
}
