package importer

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

var errInvalidImportReceipt = errors.New("invalid import receipt")

type AppliedRunEvidence struct {
	RunID               string
	SnapshotKind        string
	SourceDigest        string
	PlanDigest          string
	ParentDigest        string
	TargetDigest        string
	BatchCount          int
	BatchReceiptDigest  string
	CompletedAtUnix     int64
}

type ImportReceipt struct {
	SchemaVersion          int    `json:"schema_version"`
	RunID                  string `json:"run_id"`
	SnapshotKind           string `json:"snapshot_kind"`
	SourceDigest           string `json:"source_sha256"`
	PlanDigest             string `json:"plan_sha256"`
	ParentDigest           string `json:"parent_source_sha256,omitempty"`
	TargetDigest           string `json:"target_sha256"`
	BatchCount             int    `json:"batch_count"`
	BatchReceiptDigest     string `json:"batch_receipt_sha256"`
	ControlSchemaVersion   int    `json:"control_schema_version"`
	ControlSchemaChecksum  string `json:"control_schema_checksum"`
	TargetConfigDigest     string `json:"target_config_sha256"`
	SignerKeyID            string `json:"signer_key_id"`
	CompletedAtUnix        int64  `json:"completed_at_unix"`
	SignatureB64           string `json:"signature_b64"`
}

type unsignedImportReceipt struct {
	SchemaVersion          int    `json:"schema_version"`
	RunID                  string `json:"run_id"`
	SnapshotKind           string `json:"snapshot_kind"`
	SourceDigest           string `json:"source_sha256"`
	PlanDigest             string `json:"plan_sha256"`
	ParentDigest           string `json:"parent_source_sha256,omitempty"`
	TargetDigest           string `json:"target_sha256"`
	BatchCount             int    `json:"batch_count"`
	BatchReceiptDigest     string `json:"batch_receipt_sha256"`
	ControlSchemaVersion   int    `json:"control_schema_version"`
	ControlSchemaChecksum  string `json:"control_schema_checksum"`
	TargetConfigDigest     string `json:"target_config_sha256"`
	SignerKeyID            string `json:"signer_key_id"`
	CompletedAtUnix        int64  `json:"completed_at_unix"`
}

func SignImportReceipt(
	evidence AppliedRunEvidence,
	schema controlplane.SchemaIdentity,
	targetConfigDigest string,
	privateKey ed25519.PrivateKey,
) (ImportReceipt, []byte, error) {
	if !validAppliedRunEvidence(evidence) || schema.Version <= 0 ||
		!validCanonicalSHA256(schema.Checksum) ||
		!validCanonicalSHA256(targetConfigDigest) ||
		len(privateKey) != ed25519.PrivateKeySize {
		return ImportReceipt{}, nil, errInvalidImportReceipt
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return ImportReceipt{}, nil, errInvalidImportReceipt
	}
	signerKeyID := sha256BytesHex(publicKey)
	receipt := ImportReceipt{
		SchemaVersion:          1,
		RunID:                  evidence.RunID,
		SnapshotKind:           evidence.SnapshotKind,
		SourceDigest:           evidence.SourceDigest,
		PlanDigest:             evidence.PlanDigest,
		ParentDigest:           evidence.ParentDigest,
		TargetDigest:           evidence.TargetDigest,
		BatchCount:             evidence.BatchCount,
		BatchReceiptDigest:     evidence.BatchReceiptDigest,
		ControlSchemaVersion:   schema.Version,
		ControlSchemaChecksum:  schema.Checksum,
		TargetConfigDigest:     targetConfigDigest,
		SignerKeyID:            signerKeyID,
		CompletedAtUnix:        evidence.CompletedAtUnix,
	}
	unsigned, err := canonicalUnsignedReceipt(receipt)
	if err != nil {
		return ImportReceipt{}, nil, errInvalidImportReceipt
	}
	receipt.SignatureB64 = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, unsigned))
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return ImportReceipt{}, nil, errInvalidImportReceipt
	}
	return receipt, encoded, nil
}

func VerifyImportReceipt(data []byte, publicKey ed25519.PublicKey) (ImportReceipt, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return ImportReceipt{}, errInvalidImportReceipt
	}
	var receipt ImportReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return ImportReceipt{}, errInvalidImportReceipt
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ImportReceipt{}, errInvalidImportReceipt
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(canonical, data) {
		return ImportReceipt{}, errInvalidImportReceipt
	}
	if receipt.SchemaVersion != 1 ||
		receipt.SignerKeyID != sha256BytesHex(publicKey) ||
		!validImportReceiptFields(receipt) {
		return ImportReceipt{}, errInvalidImportReceipt
	}
	signature, ok := decodeCanonicalReceiptBase64(receipt.SignatureB64)
	if !ok || len(signature) != ed25519.SignatureSize {
		return ImportReceipt{}, errInvalidImportReceipt
	}
	unsigned, err := canonicalUnsignedReceipt(receipt)
	if err != nil || !ed25519.Verify(publicKey, unsigned, signature) {
		return ImportReceipt{}, errInvalidImportReceipt
	}
	return receipt, nil
}

func canonicalUnsignedReceipt(receipt ImportReceipt) ([]byte, error) {
	return json.Marshal(unsignedImportReceipt{
		SchemaVersion:          receipt.SchemaVersion,
		RunID:                  receipt.RunID,
		SnapshotKind:           receipt.SnapshotKind,
		SourceDigest:           receipt.SourceDigest,
		PlanDigest:             receipt.PlanDigest,
		ParentDigest:           receipt.ParentDigest,
		TargetDigest:           receipt.TargetDigest,
		BatchCount:             receipt.BatchCount,
		BatchReceiptDigest:     receipt.BatchReceiptDigest,
		ControlSchemaVersion:   receipt.ControlSchemaVersion,
		ControlSchemaChecksum:  receipt.ControlSchemaChecksum,
		TargetConfigDigest:     receipt.TargetConfigDigest,
		SignerKeyID:            receipt.SignerKeyID,
		CompletedAtUnix:        receipt.CompletedAtUnix,
	})
}

func validAppliedRunEvidence(evidence AppliedRunEvidence) bool {
	return evidence.RunID != "" &&
		(evidence.SnapshotKind == "full" || evidence.SnapshotKind == "delta") &&
		validCanonicalSHA256(evidence.SourceDigest) &&
		validCanonicalSHA256(evidence.PlanDigest) &&
		validCanonicalSHA256(evidence.TargetDigest) &&
		validCanonicalSHA256(evidence.BatchReceiptDigest) &&
		evidence.BatchCount >= 0 && evidence.CompletedAtUnix >= 0 &&
		((evidence.SnapshotKind == "full" && evidence.ParentDigest == "") ||
			(evidence.SnapshotKind == "delta" && validCanonicalSHA256(evidence.ParentDigest)))
}

func validImportReceiptFields(receipt ImportReceipt) bool {
	return validAppliedRunEvidence(AppliedRunEvidence{
		RunID:              receipt.RunID,
		SnapshotKind:       receipt.SnapshotKind,
		SourceDigest:       receipt.SourceDigest,
		PlanDigest:         receipt.PlanDigest,
		ParentDigest:       receipt.ParentDigest,
		TargetDigest:       receipt.TargetDigest,
		BatchCount:         receipt.BatchCount,
		BatchReceiptDigest: receipt.BatchReceiptDigest,
		CompletedAtUnix:    receipt.CompletedAtUnix,
	}) && receipt.ControlSchemaVersion > 0 &&
		validCanonicalSHA256(receipt.ControlSchemaChecksum) &&
		validCanonicalSHA256(receipt.TargetConfigDigest) &&
		validCanonicalSHA256(receipt.SignerKeyID)
}

func decodeCanonicalReceiptBase64(value string) ([]byte, bool) {
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	return decoded, err == nil && base64.StdEncoding.EncodeToString(decoded) == value
}

func sha256BytesHex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
