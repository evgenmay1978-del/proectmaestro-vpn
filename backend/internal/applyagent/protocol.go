package applyagent

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const (
	ProtocolVersion  = 1
	MaxCommandBytes  = 4 << 20
	MaxCommandLifetime = 60 * time.Second
)

var ErrInvalidCommand = errors.New("applyagent: invalid command")

type DesiredEntry struct {
	CustomerID    string                `json:"customer_id"`
	OperationID   string                `json:"operation_id"`
	PayloadKind   string                `json:"payload_kind"`
	Generation    int64                 `json:"generation"`
	Tombstone     bool                  `json:"tombstone"`
	Payload       controlplane.Envelope `json:"payload"`
	PayloadSHA256 string                `json:"payload_sha256"`
}

type DesiredSnapshot struct {
	NodeID             string         `json:"node_id"`
	ServiceID          string         `json:"service_id"`
	TriggerOperationID string         `json:"trigger_operation_id"`
	Entries            []DesiredEntry `json:"entries"`
	SnapshotSHA256     string         `json:"snapshot_sha256"`
}

type ApplyCommand struct {
	Version         int             `json:"version"`
	ClusterEpoch    int64           `json:"cluster_epoch"`
	NodeIncarnation int64           `json:"node_incarnation"`
	LeaseFence      int64           `json:"lease_fence"`
	NodeID          string          `json:"node_id"`
	ServiceID       string          `json:"service_id"`
	HolderID        string          `json:"holder_id"`
	Snapshot        DesiredSnapshot `json:"snapshot"`
	IssuedAtUnix    int64           `json:"issued_at_unix"`
	NotAfterUnix    int64           `json:"not_after_unix"`
}

type SignedCommand struct {
	KeyID     string `json:"key_id"`
	Command   []byte `json:"command"`
	Signature []byte `json:"signature"`
}

type snapshotDigestInput struct {
	NodeID             string         `json:"node_id"`
	ServiceID          string         `json:"service_id"`
	TriggerOperationID string         `json:"trigger_operation_id"`
	Entries            []DesiredEntry `json:"entries"`
}

func NewDesiredSnapshot(nodeID, serviceID, triggerOperationID string, entries []DesiredEntry) (DesiredSnapshot, error) {
	snapshot := DesiredSnapshot{
		NodeID: strings.TrimSpace(nodeID), ServiceID: strings.TrimSpace(serviceID),
		TriggerOperationID: strings.TrimSpace(triggerOperationID),
		Entries: append([]DesiredEntry(nil), entries...),
	}
	sort.Slice(snapshot.Entries, func(i, j int) bool {
		return snapshot.Entries[i].CustomerID < snapshot.Entries[j].CustomerID
	})
	if err := validateSnapshotFields(snapshot); err != nil {
		return DesiredSnapshot{}, err
	}
	digest, err := snapshotDigest(snapshot)
	if err != nil {
		return DesiredSnapshot{}, err
	}
	snapshot.SnapshotSHA256 = digest
	return snapshot, nil
}

func ValidateDesiredSnapshot(snapshot DesiredSnapshot) error {
	if err := validateSnapshotFields(snapshot); err != nil {
		return err
	}
	digest, err := snapshotDigest(snapshot)
	if err != nil || digest != snapshot.SnapshotSHA256 {
		return ErrInvalidCommand
	}
	return nil
}

func validateSnapshotFields(snapshot DesiredSnapshot) error {
	if strings.TrimSpace(snapshot.NodeID) == "" || strings.TrimSpace(snapshot.ServiceID) == "" ||
		strings.TrimSpace(snapshot.TriggerOperationID) == "" {
		return ErrInvalidCommand
	}
	previousCustomer := ""
	for index, entry := range snapshot.Entries {
		if strings.TrimSpace(entry.CustomerID) == "" || strings.TrimSpace(entry.OperationID) == "" ||
			strings.TrimSpace(entry.PayloadKind) == "" || len(entry.PayloadKind) > 1024 || !utf8.ValidString(entry.PayloadKind) ||
			entry.Generation <= 0 || !validSHA256(entry.PayloadSHA256) {
			return ErrInvalidCommand
		}
		if index > 0 && entry.CustomerID <= previousCustomer {
			return ErrInvalidCommand
		}
		previousCustomer = entry.CustomerID
	}
	return nil
}

func snapshotDigest(snapshot DesiredSnapshot) (string, error) {
	payload, err := json.Marshal(snapshotDigestInput{
		NodeID: snapshot.NodeID, ServiceID: snapshot.ServiceID,
		TriggerOperationID: snapshot.TriggerOperationID, Entries: snapshot.Entries,
	})
	if err != nil {
		return "", ErrInvalidCommand
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func SignCommand(command ApplyCommand, keyID string, privateKey ed25519.PrivateKey) (SignedCommand, error) {
	if strings.TrimSpace(keyID) == "" || len(privateKey) != ed25519.PrivateKeySize {
		return SignedCommand{}, ErrInvalidCommand
	}
	if err := validateApplyCommand(command, time.Time{}); err != nil {
		return SignedCommand{}, err
	}
	canonical, err := json.Marshal(command)
	if err != nil || len(canonical) > MaxCommandBytes {
		return SignedCommand{}, ErrInvalidCommand
	}
	return SignedCommand{
		KeyID: keyID, Command: canonical, Signature: ed25519.Sign(privateKey, canonical),
	}, nil
}

func VerifySignedCommand(signed SignedCommand, publicKeys map[string]ed25519.PublicKey, now time.Time) (ApplyCommand, error) {
	if strings.TrimSpace(signed.KeyID) == "" || len(signed.Command) == 0 || len(signed.Command) > MaxCommandBytes ||
		len(signed.Signature) != ed25519.SignatureSize {
		return ApplyCommand{}, ErrInvalidCommand
	}
	publicKey, ok := publicKeys[signed.KeyID]
	if !ok || len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, signed.Command, signed.Signature) {
		return ApplyCommand{}, ErrInvalidCommand
	}
	var command ApplyCommand
	decoder := json.NewDecoder(bytes.NewReader(signed.Command))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		return ApplyCommand{}, ErrInvalidCommand
	}
	canonical, err := json.Marshal(command)
	if err != nil || !bytes.Equal(canonical, signed.Command) {
		return ApplyCommand{}, ErrInvalidCommand
	}
	if err := validateApplyCommand(command, now); err != nil {
		return ApplyCommand{}, err
	}
	return command, nil
}

func validateApplyCommand(command ApplyCommand, now time.Time) error {
	if command.Version != ProtocolVersion || command.ClusterEpoch <= 0 || command.NodeIncarnation <= 0 ||
		command.LeaseFence <= 0 || strings.TrimSpace(command.NodeID) == "" ||
		strings.TrimSpace(command.ServiceID) == "" || strings.TrimSpace(command.HolderID) == "" ||
		command.IssuedAtUnix <= 0 || command.NotAfterUnix <= command.IssuedAtUnix ||
		command.NotAfterUnix-command.IssuedAtUnix > int64(MaxCommandLifetime/time.Second) ||
		command.Snapshot.NodeID != command.NodeID || command.Snapshot.ServiceID != command.ServiceID {
		return ErrInvalidCommand
	}
	if !now.IsZero() && (now.Unix() < command.IssuedAtUnix || now.Unix() > command.NotAfterUnix) {
		return ErrInvalidCommand
	}
	return ValidateDesiredSnapshot(command.Snapshot)
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
