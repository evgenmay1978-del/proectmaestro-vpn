package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/runtimefence"
)

const (
	leaseStateFile      = "managed-lease-state.json"
	maxLeaseStateBytes  = 16 << 20
	maxLeaseUsers       = 4096
	maxLeaseUserHistory = 8192
	maxFinalReceipts    = 4096
	maxFinalReceiptPage = 32
)

var (
	ErrLeaseUnavailable = errors.New("sidecar agent: commercial runtime lease unavailable")
	ErrLeasePending     = errors.New("sidecar agent: exact managed operation remains pending")
	ErrLeaseCapacity    = errors.New("sidecar agent: unacknowledged managed state capacity reached")
)

type LeaseChallenge struct {
	Schema                int      `json:"schema"`
	Nonce                 string   `json:"nonce"`
	ClockDomain           string   `json:"clock_domain"`
	ReadStartedBoottimeNS int64    `json:"read_started_boottime_ns"`
	MaxDeadlineBoottimeNS int64    `json:"max_deadline_boottime_ns"`
	ManagedUsers          []string `json:"managed_users"`
}

type LeaseBinding struct {
	ActionKey            string `json:"action_key"`
	OriginID             string `json:"origin_id"`
	ReleaseID            string `json:"release_id"`
	DesiredGeneration    int64  `json:"desired_generation"`
	ManagedUserSetDigest string `json:"managed_user_set_digest"`
}

// Field order is the proof-hash wire contract. Hash the JSON encoding of this
// struct (binding, control, receipt), without receipt_id or proof_sha256.
type LeaseReceiptProof struct {
	LeaseBinding
	Control runtimefence.Control `json:"control"`
	Receipt runtimefence.Receipt `json:"receipt"`
}

type FinalLeaseReceipt struct {
	ReceiptID   string `json:"receipt_id"`
	ProofSHA256 string `json:"proof_sha256"`
	LeaseReceiptProof
}

type LeaseReceiptPage struct {
	Schema               int                 `json:"schema"`
	FinalReceipts        []FinalLeaseReceipt `json:"final_receipts"`
	HasMoreFinalReceipts bool                `json:"has_more_final_receipts"`
	PendingUseLease      *UseLeaseRequest    `json:"pending_use_lease,omitempty"`
}

type LeaseReceiptAck struct {
	Schema   int                   `json:"schema"`
	Receipts []LeaseReceiptAckItem `json:"receipts"`
}

type LeaseReceiptAckItem struct {
	ReceiptID   string `json:"receipt_id"`
	ProofSHA256 string `json:"proof_sha256"`
}

type leaseUserState struct {
	BootID             string       `json:"boot_id"`
	Email              string       `json:"email"`
	Generation         uint64       `json:"generation"`
	ClockDomain        string       `json:"clock_domain"`
	ConfigDigest       string       `json:"config_digest"`
	Binding            LeaseBinding `json:"binding"`
	Phase              string       `json:"phase"`
	DeadlineBoottimeNS int64        `json:"deadline_boottime_ns,omitempty"`
}

type savedLeaseChallenge struct {
	Challenge LeaseChallenge `json:"challenge"`
	Receipt   Receipt        `json:"receipt"`
}

type leaseStep struct {
	Kind    string                `json:"kind"`
	Email   string                `json:"email"`
	BootID  string                `json:"boot_id"`
	Binding LeaseBinding          `json:"binding"`
	Control *runtimefence.Control `json:"control,omitempty"`
}

type pendingLeaseCommand struct {
	Key         string           `json:"key"`
	Request     *UseLeaseRequest `json:"request,omitempty"`
	DesiredJSON json.RawMessage  `json:"desired_json,omitempty"`
	Steps       []leaseStep      `json:"steps"`
	Next        int              `json:"next"`
	Result      UseLeaseResult   `json:"result"`
}

type completedLeaseCommand struct {
	RequestSHA256 string         `json:"request_sha256"`
	Result        UseLeaseResult `json:"result"`
}

type leaseState struct {
	Schema        int                          `json:"schema"`
	Users         map[string]leaseUserState    `json:"users"`
	Challenge     *savedLeaseChallenge         `json:"challenge,omitempty"`
	Pending       *pendingLeaseCommand         `json:"pending,omitempty"`
	Completed     *completedLeaseCommand       `json:"completed,omitempty"`
	FinalReceipts map[string]FinalLeaseReceipt `json:"final_receipts"`
}

func emptyLeaseState() leaseState {
	return leaseState{Schema: 2, Users: map[string]leaseUserState{}, FinalReceipts: map[string]FinalLeaseReceipt{}}
}

func (store *FileStore) loadLeaseState() (leaseState, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	raw, err := readBounded(filepath.Join(store.directory, leaseStateFile), maxLeaseStateBytes)
	if errors.Is(err, ErrNotFound) {
		return emptyLeaseState(), nil
	}
	if err != nil {
		return leaseState{}, err
	}
	var state leaseState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil {
		return leaseState{}, ErrLeaseUnavailable
	}
	canonical, err := json.Marshal(state)
	if err != nil || !bytes.Equal(canonical, raw) || validateLeaseState(state) != nil {
		return leaseState{}, ErrLeaseUnavailable
	}
	return state, nil
}

func (store *FileStore) saveLeaseState(state leaseState) error {
	if validateLeaseState(state) != nil {
		return ErrLeaseUnavailable
	}
	raw, err := json.Marshal(state)
	if err != nil || len(raw) > maxLeaseStateBytes {
		return ErrLeaseCapacity
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return store.atomicWrite(leaseStateFile, raw)
}

func validateLeaseState(state leaseState) error {
	if state.Schema != 2 || state.Users == nil || state.FinalReceipts == nil || len(state.Users) > maxLeaseUserHistory || len(state.FinalReceipts) > maxFinalReceipts {
		return ErrLeaseCapacity
	}
	for key, user := range state.Users {
		if key != leaseUserKey(user.BootID, user.Email) || !safeIdentifier(user.BootID) || !validManagedLeaseEmail(user.Email) || user.Generation == 0 ||
			!validDigest(user.ClockDomain) || !validDigest(user.ConfigDigest) || !validLeaseBinding(user.Binding) {
			return ErrLeaseUnavailable
		}
		switch user.Phase {
		case "unknown", "ready", "active", "fenced", "removed":
		default:
			return ErrLeaseUnavailable
		}
		if user.Phase == "active" && user.DeadlineBoottimeNS <= 0 {
			return ErrLeaseUnavailable
		}
	}
	for id, final := range state.FinalReceipts {
		if id != final.ReceiptID || final.Control.Operation != "fence" || validateLeaseReceipt(final.Control, final.Receipt) != nil ||
			!validLeaseBinding(final.LeaseBinding) || final.ReceiptID != leaseOperationID(final.LeaseBinding, final.Control) || final.ProofSHA256 != leaseHash(final.LeaseReceiptProof) {
			return ErrLeaseUnavailable
		}
	}
	if state.Challenge != nil {
		c := state.Challenge.Challenge
		if c.Schema != 2 || !validDigest(c.Nonce) || !validDigest(c.ClockDomain) || c.ReadStartedBoottimeNS <= 0 ||
			c.MaxDeadlineBoottimeNS <= c.ReadStartedBoottimeNS || c.MaxDeadlineBoottimeNS-c.ReadStartedBoottimeNS > int64(5_000_000_000) ||
			c.ManagedUsers == nil || len(c.ManagedUsers) > maxLeaseUsers || !strictlySortedUnique(c.ManagedUsers) || !state.Challenge.Receipt.ReadyAt(state.Challenge.Receipt.AppliedAt) {
			return ErrLeaseUnavailable
		}
	}
	if state.Pending != nil {
		p := state.Pending
		if !validDigest(p.Key) || p.Next < 0 || p.Next > len(p.Steps) || len(p.Steps) > maxLeaseUsers*3 {
			return ErrLeaseUnavailable
		}
		if p.Request != nil && (p.Key != leaseHash(*p.Request) || !validUseLeaseRequest(*p.Request)) {
			return ErrLeaseUnavailable
		}
		if p.Request == nil {
			if _, err := ParseDesired(p.DesiredJSON); err != nil {
				return ErrLeaseUnavailable
			}
		} else if len(p.DesiredJSON) != 0 {
			return ErrLeaseUnavailable
		}
		for _, step := range p.Steps {
			user, ok := state.Users[leaseUserKey(step.BootID, step.Email)]
			if !ok || !validLeaseBinding(step.Binding) {
				return ErrLeaseUnavailable
			}
			switch step.Kind {
			case "control":
				if step.Control == nil || !validLeaseControl(*step.Control) || step.Control.BootID != step.BootID || step.Control.Email != step.Email || step.Control.Generation > user.Generation {
					return ErrLeaseUnavailable
				}
				if p.Request == nil && step.Control.Operation != "fence" {
					return ErrLeaseUnavailable
				}
			case "remove", "add":
				if step.Control != nil {
					return ErrLeaseUnavailable
				}
			default:
				return ErrLeaseUnavailable
			}
		}
	}
	if state.Completed != nil && (!validDigest(state.Completed.RequestSHA256) || state.Completed.Result.Schema != 2 || !validDigest(state.Completed.Result.Nonce)) {
		return ErrLeaseUnavailable
	}
	return nil
}

func leaseUserKey(boot, email string) string { return boot + "\x00" + email }
func leaseHash(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func leaseOperationID(binding LeaseBinding, control runtimefence.Control) string {
	return leaseHash(struct {
		LeaseBinding
		Control runtimefence.Control `json:"control"`
	}{binding, control})
}
func bindingForDesired(desired Desired) LeaseBinding {
	return LeaseBinding{desired.ActionKey(), desired.OriginID, desired.ReleaseID, desired.Generation, desired.ManagedUserSetDigest}
}
func validLeaseBinding(binding LeaseBinding) bool {
	return validLeaseActionKey(binding.ActionKey) && safeIdentifier(binding.OriginID) && safeIdentifier(binding.ReleaseID) && binding.DesiredGeneration > 0 && validDigest(binding.ManagedUserSetDigest)
}

func finalReceiptPage(state leaseState) LeaseReceiptPage {
	ids := make([]string, 0, len(state.FinalReceipts))
	for id := range state.FinalReceipts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := state.FinalReceipts[ids[i]].Control, state.FinalReceipts[ids[j]].Control
		if left.BootID != right.BootID {
			return left.BootID < right.BootID
		}
		if left.Email != right.Email {
			return left.Email < right.Email
		}
		if left.Generation != right.Generation {
			return left.Generation < right.Generation
		}
		return ids[i] < ids[j]
	})
	page := LeaseReceiptPage{Schema: 2, FinalReceipts: make([]FinalLeaseReceipt, 0, maxFinalReceiptPage), HasMoreFinalReceipts: len(ids) > maxFinalReceiptPage}
	if state.Pending != nil && state.Pending.Request != nil {
		request := *state.Pending.Request
		request.Emails = append([]string{}, request.Emails...)
		page.PendingUseLease = &request
	}
	if len(ids) > maxFinalReceiptPage {
		ids = ids[:maxFinalReceiptPage]
	}
	for _, id := range ids {
		page.FinalReceipts = append(page.FinalReceipts, state.FinalReceipts[id])
	}
	return page
}
