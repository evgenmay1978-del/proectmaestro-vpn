package importer

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrRunDigestMismatch = errors.New("import run digest mismatch")
	ErrTargetNotEmpty    = errors.New("full import requires an empty business target")
	ErrParentMismatch    = errors.New("delta parent digest does not match applied source")
	ErrBlockedPlan       = errors.New("import plan contains blockers")
)

type Snapshot struct {
	FormatVersion          int                           `json:"format_version"`
	SnapshotKind           string                        `json:"snapshot_kind"`
	ParentSourceDigest     string                        `json:"parent_source_digest,omitempty"`
	CapturedAt             time.Time                     `json:"captured_at"`
	SourceHashes           map[string]string             `json:"source_hashes"`
	Customers              []LegacyCustomer              `json:"customers"`
	Orders                 []LegacyOrder                 `json:"orders"`
	Trials                 []LegacyTrial                 `json:"trials"`
	BotBindings            []LegacyBotBinding            `json:"bot_bindings"`
	Settings               []LegacySetting               `json:"settings"`
	Principals             []LegacyPrincipal             `json:"principals"`
	EncryptedSecrets       []LegacyEncryptedSecret       `json:"encrypted_secrets"`
	Deletes                []LegacyDelete                `json:"deletes,omitempty"`
	BotPollStates          []LegacyBotPollState          `json:"bot_poll_states"`
	PendingCallbacks       []LegacyCallback              `json:"pending_callbacks"`
	BotCredentialRotations []LegacyBotCredentialRotation `json:"bot_credential_rotations,omitempty"`
}

type LegacyCustomer struct {
	SourceKey                 string `json:"source_key"`
	Login                     string `json:"login"`
	LoginKeyHMAC              string `json:"login_key_hmac"`
	UUIDHMAC                  string `json:"uuid_hmac"`
	SubIDHMAC                 string `json:"sub_id_hmac"`
	TokenHMAC                 string `json:"token_hmac"`
	CredentialFingerprintHMAC string `json:"credential_fingerprint_hmac"`
	IdentitySecretRef         string `json:"identity_secret_ref"`
	ProtocolTags              []string `json:"protocol_tags"`
	NodeIDs                   []string `json:"node_ids"`
	ExpiresAtUnix             int64  `json:"expires_at_unix"`
	Generation                int64  `json:"generation"`
	Status                    string `json:"status"`
}

type LegacyOrder struct {
	SourceKey                   string `json:"source_key"`
	CustomerSourceKey           string `json:"customer_source_key"`
	BuyerScope                  string `json:"buyer_scope"`
	BuyerKeyHMAC                string `json:"buyer_key_hmac"`
	TariffVersionID             string `json:"tariff_version_id"`
	AmountMinor                 int64  `json:"amount_minor"`
	Currency                    string `json:"currency"`
	DurationDays                int64  `json:"duration_days"`
	PaymentCode                 string `json:"payment_code"`
	CreatedAtUnix               int64  `json:"created_at_unix"`
	State                       string `json:"state"`
	Credited                    bool   `json:"credited"`
	StoredCustomerExpiresAtUnix int64  `json:"stored_customer_expires_at_unix"`
	ResultGeneration            int64  `json:"result_generation"`
}

type LegacyTrial struct {
	SourceKey        string `json:"source_key"`
	LegacyAnchorHMAC string `json:"legacy_anchor_hmac"`
	CurrentHMAC      string `json:"current_hmac"`
	Used             bool   `json:"used"`
	ExpiresAtUnix    int64  `json:"expires_at_unix"`
}

type LegacyBotBinding struct {
	BotIdentityHMAC     string `json:"bot_identity_hmac"`
	TokenFingerprintHMAC string `json:"token_fingerprint_hmac"`
	CredentialVersion  int    `json:"credential_version"`
	SchemaFingerprint  string `json:"schema_fingerprint"`
}

type LegacySetting struct {
	Key             string          `json:"key"`
	PublicValueJSON json.RawMessage `json:"public_value_json"`
	Generation      int64           `json:"generation"`
	SecretRef       string          `json:"secret_ref,omitempty"`
}

type LegacyPrincipal struct {
	SourceKey           string   `json:"source_key"`
	LoginKeyHMAC        string   `json:"login_key_hmac"`
	Status              string   `json:"status"`
	Roles               []string `json:"roles"`
	CredentialSecretRef string   `json:"credential_secret_ref"`
}

type LegacyEncryptedSecret struct {
	SecretID       string `json:"secret_id"`
	OwnerType      string `json:"owner_type"`
	OwnerSourceKey string `json:"owner_source_key"`
	Field          string `json:"field"`
	Kind           string `json:"kind"`
	KeyVersion     int    `json:"key_version"`
	NonceB64       string `json:"nonce_b64"`
	CiphertextB64  string `json:"ciphertext_b64"`
	SHA256         string `json:"sha256"`
}

type LegacyDelete struct {
	Entity              string `json:"entity"`
	SourceKey           string `json:"source_key"`
	ExpectedPriorDigest string `json:"expected_prior_digest,omitempty"`
	Explicit            bool   `json:"explicit"`
}

type LegacyBotPollState struct {
	BotIdentityHMAC            string `json:"bot_identity_hmac"`
	CurrentTokenFingerprintHMAC string `json:"current_token_fingerprint_hmac"`
	CredentialVersion         int    `json:"credential_version"`
	NextUpdateID              int64  `json:"next_update_id"`
	CapturedFence             uint64 `json:"captured_fence"`
}

type LegacyCallback struct {
	BotIdentityHMAC       string `json:"bot_identity_hmac"`
	TokenFingerprintHMAC string `json:"token_fingerprint_hmac"`
	CredentialVersion    int    `json:"credential_version"`
	CallbackHMAC         string `json:"callback_hmac"`
	OrderID              string `json:"order_id"`
	Action               string `json:"action"`
	State                string `json:"state"`
}

type LegacyBotCredentialRotation struct {
	BotIdentityHMAC        string `json:"bot_identity_hmac"`
	OldTokenFingerprintHMAC string `json:"old_token_fingerprint_hmac"`
	NewTokenFingerprintHMAC string `json:"new_token_fingerprint_hmac"`
	OldCredentialVersion  int    `json:"old_credential_version"`
	NewCredentialVersion  int    `json:"new_credential_version"`
	AuditDigest           string `json:"audit_digest"`
}

type PlanOptions struct {
	Namespace             string
	SupportedBotSchemas   []string
	SupportedProtocolTags []string
	SupportedNodeIDs      []string
	ParentSnapshot        *Snapshot
	AppliedParentDigest   string
}

type PlannedCustomer struct {
	InternalID                string `json:"internal_id"`
	SourceKey                 string `json:"source_key"`
	DisplayLogin              string `json:"display_login"`
	LoginKeyHMAC              string `json:"login_key_hmac"`
	UUIDHMAC                  string `json:"uuid_hmac"`
	SubIDHMAC                 string `json:"sub_id_hmac"`
	TokenHMAC                 string `json:"token_hmac"`
	CredentialFingerprintHMAC string `json:"credential_fingerprint_hmac"`
	IdentitySecretRef         string `json:"identity_secret_ref"`
	ProtocolTags              []string `json:"protocol_tags"`
	NodeIDs                   []string `json:"node_ids"`
	ExpiresAtUnix             int64  `json:"expires_at_unix"`
	Generation                int64  `json:"generation"`
	Status                    string `json:"status"`
}

type PlannedOrder struct {
	InternalID          string   `json:"internal_id"`
	SourceKey           string   `json:"source_key"`
	CustomerInternalID  string   `json:"customer_internal_id,omitempty"`
	CustomerSourceKey   string   `json:"customer_source_key"`
	BuyerScope          string   `json:"buyer_scope"`
	BuyerKeyHMAC        string   `json:"buyer_key_hmac"`
	TariffVersionID     string   `json:"tariff_version_id"`
	AmountMinor         int64    `json:"amount_minor"`
	Currency            string   `json:"currency"`
	DurationDays        int64    `json:"duration_days"`
	PaymentCode         string   `json:"payment_code"`
	CreatedAtUnix       int64    `json:"created_at_unix"`
	ExpiresAtUnix       int64    `json:"expires_at_unix"`
	PaymentState        string   `json:"payment_state"`
	ProvisioningState   string   `json:"provisioning_state"`
	ImportState         string   `json:"import_state"`
	ResultExpiresAtUnix int64    `json:"result_expires_at_unix"`
	ResultGeneration    int64    `json:"result_generation"`
	AuditMarkers        []string `json:"audit_markers"`
}

type PlannedPrincipal struct {
	InternalID          string   `json:"internal_id"`
	SourceKey           string   `json:"source_key"`
	LoginKeyHMAC        string   `json:"login_key_hmac"`
	Status              string   `json:"status"`
	Roles               []string `json:"roles"`
	CredentialSecretRef string   `json:"credential_secret_ref"`
}

type PlannedDelete struct {
	Entity              string `json:"entity"`
	SourceKey           string `json:"source_key"`
	TargetID            string `json:"target_id"`
	ExpectedPriorDigest string `json:"expected_prior_digest"`
	PriorGeneration     int64  `json:"prior_generation,omitempty"`
	NextGeneration      int64  `json:"next_generation,omitempty"`
	TombstoneID         string `json:"tombstone_id,omitempty"`
	Tombstone           bool   `json:"tombstone"`
}

type ImportPlan struct {
	FormatVersion    int                       `json:"format_version"`
	SnapshotKind     string                    `json:"snapshot_kind"`
	ParentSourceDigest string                  `json:"parent_source_digest,omitempty"`
	SourceDigest     string                    `json:"source_digest"`
	PlanDigest       string                    `json:"plan_digest"`
	Customers        []PlannedCustomer         `json:"customers"`
	Orders           []PlannedOrder            `json:"orders"`
	Trials           []LegacyTrial             `json:"trials"`
	BotBindings      []LegacyBotBinding        `json:"bot_bindings"`
	Settings         []LegacySetting           `json:"settings"`
	Principals       []PlannedPrincipal        `json:"principals"`
	EncryptedSecrets []LegacyEncryptedSecret   `json:"encrypted_secrets"`
	Deletes          []PlannedDelete           `json:"deletes"`
	CascadeDeletes   []PlannedDelete           `json:"cascade_deletes,omitempty"`
	BotPollStates    []LegacyBotPollState       `json:"bot_poll_states"`
	PendingCallbacks []LegacyCallback           `json:"pending_callbacks"`
	BotCredentialRotations []LegacyBotCredentialRotation `json:"bot_credential_rotations"`
	Counts           map[string]int            `json:"counts"`
	Blockers         []Blocker                 `json:"-"`
}

type Blocker struct {
	Code      string `json:"code"`
	Entity    string `json:"entity,omitempty"`
	SourceKey string `json:"source_key,omitempty"`
}

type Report struct {
	SourceDigest string         `json:"source_digest"`
	PlanDigest   string         `json:"plan_digest"`
	Counts       map[string]int `json:"counts"`
	Blockers     []Blocker      `json:"blockers"`
}

type TargetState struct {
	Empty               bool
	BusinessDigest      string
	AppliedSourceDigest string
}

type ApplyRun struct {
	RunID        string
	SnapshotKind string
	SourceDigest string
	PlanDigest   string
	ParentDigest string
	BatchCount   int
}

type RunProgress struct {
	New                 bool
	AppliedBatchDigests map[int]string
	Completed           bool
	TargetDigest        string
}

type ApplyOperation struct {
	Entity        string
	Key           string
	Tombstone     bool
	CanonicalJSON []byte
}

type ApplyBatch struct {
	RunID      string
	PlanDigest string
	Index      int
	Digest     string
	Operations []ApplyOperation
}

type BatchReceipt struct {
	Index          int
	Digest         string
	AlreadyApplied bool
}

type ApplyCompletion struct {
	RunID        string
	SourceDigest string
	PlanDigest   string
	TargetDigest string
}

type ApplyStore interface {
	InspectTarget(context.Context) (TargetState, error)
	BeginOrResume(context.Context, ApplyRun) (RunProgress, error)
	CommitBatch(context.Context, ApplyBatch) (BatchReceipt, error)
	Complete(context.Context, ApplyCompletion) error
}

type ApplyOptions struct {
	RunID     string
	BatchSize int
}

type ApplyResult struct {
	Counts        map[string]int
	TargetDigest  string
	AppliedBatches int
}
