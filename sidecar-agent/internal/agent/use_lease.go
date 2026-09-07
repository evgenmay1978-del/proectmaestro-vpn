package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/runtimefence"
)

type managedRuntimeController interface {
	ApplyManagedControl(context.Context, runtimefence.Control) (runtimefence.Receipt, error)
}

const managedLeaseWindow = 60 * time.Second

type UseLeaseRequest struct {
	Schema                   int               `json:"schema"`
	ActionKey                string            `json:"action_key"`
	XrayProcessBootID        string            `json:"xray_process_boot_id"`
	ConfigDigest             string            `json:"config_digest"`
	ManagedUserSetDigest     string            `json:"managed_user_set_digest"`
	Nonce                    string            `json:"nonce"`
	ClockDomain              string            `json:"clock_domain"`
	ReadStartedBoottimeNS    int64             `json:"read_started_boottime_ns"`
	DeadlineBoottimeNS       int64             `json:"deadline_boottime_ns"`
	Emails                   []string          `json:"emails"`
	CumulativeByteCeilings   map[string]int64  `json:"cumulative_byte_ceilings,omitempty"`
	ExpectedFenceGenerations map[string]uint64 `json:"expected_fence_generations,omitempty"`
}

type UseLeaseResult struct {
	Schema          int                 `json:"schema"`
	Nonce           string              `json:"nonce"`
	Complete        bool                `json:"complete"`
	NeedsFreshNonce bool                `json:"needs_fresh_nonce"`
	Receipts        []LeaseReceiptProof `json:"receipts"`
}

func validLeaseActionKey(key string) bool {
	return key != "" && len(key) <= 1024 && strings.TrimSpace(key) == key && !strings.ContainsAny(key, "\x00\r\n\t")
}
func validManagedLeaseEmail(email string) bool {
	parts := strings.Split(email, ":")
	return len(email) <= 200 && len(parts) == 3 && managedEmailForExit(email, parts[2]) && supportedExit(parts[2])
}

func validUseLeaseRequest(request UseLeaseRequest) bool {
	if (request.Schema != 2 && request.Schema != 3) || !validLeaseActionKey(request.ActionKey) || !safeIdentifier(request.XrayProcessBootID) || !validDigest(request.ConfigDigest) ||
		!validDigest(request.ManagedUserSetDigest) || !validDigest(request.Nonce) || !validDigest(request.ClockDomain) || request.ReadStartedBoottimeNS <= 0 ||
		request.DeadlineBoottimeNS <= request.ReadStartedBoottimeNS || request.DeadlineBoottimeNS-request.ReadStartedBoottimeNS > int64(managedLeaseWindow) ||
		request.Emails == nil || len(request.Emails) > maxLeaseUsers || !strictlySortedUnique(request.Emails) {
		return false
	}
	if (request.Schema == 2 && (len(request.CumulativeByteCeilings) != 0 || len(request.ExpectedFenceGenerations) != 0)) ||
		(request.Schema == 3 && (len(request.CumulativeByteCeilings) != len(request.Emails) || len(request.ExpectedFenceGenerations) != len(request.Emails))) {
		return false
	}
	for _, email := range request.Emails {
		_, hasFenceGeneration := request.ExpectedFenceGenerations[email]
		if !validManagedLeaseEmail(email) || (request.Schema == 3 && (request.CumulativeByteCeilings[email] <= 0 || !hasFenceGeneration)) {
			return false
		}
	}
	return true
}

func validLeaseControl(control runtimefence.Control) bool {
	if (control.Schema != 2 && control.Schema != 3) || (control.Schema == 2 && control.CumulativeByteCeiling != 0) || !validManagedLeaseEmail(control.Email) || !safeIdentifier(control.BootID) || !validDigest(control.ConfigDigest) || !validDigest(control.ClockDomain) || control.Generation == 0 {
		return false
	}
	switch control.Operation {
	case "grant", "renew":
		return control.DeadlineBoottimeNS > 0 && (control.Schema == 2 || control.CumulativeByteCeiling > 0)
	case "fence":
		return control.DeadlineBoottimeNS == 0 && control.CumulativeByteCeiling == 0
	}
	return false
}

func validateLeaseReceipt(control runtimefence.Control, receipt runtimefence.Receipt) error {
	if !validLeaseControl(control) || receipt.Schema != control.Schema || receipt.Email != control.Email || receipt.BootID != control.BootID || receipt.ConfigDigest != control.ConfigDigest ||
		receipt.Generation != control.Generation || receipt.ClockDomain != control.ClockDomain || receipt.ResetSequence != 0 {
		return ErrLeaseUnavailable
	}
	if receipt.CumulativeByteCeiling != control.CumulativeByteCeiling ||
		(control.Schema == 2 && receipt.CumulativeBytes != nil) ||
		(control.Schema == 3 && (receipt.CumulativeBytes == nil || *receipt.CumulativeBytes < 0)) ||
		(control.Schema == 3 && control.Operation != "fence" && *receipt.CumulativeBytes > control.CumulativeByteCeiling) {
		return ErrLeaseUnavailable
	}
	observed, err := time.Parse(time.RFC3339Nano, receipt.ObservedAt)
	if err != nil || observed.IsZero() {
		return ErrLeaseUnavailable
	}
	if control.Operation == "fence" {
		if receipt.DeadlineBoottimeNS != 0 || receipt.LeaseRemainingMS != nil {
			return ErrLeaseUnavailable
		}
		switch receipt.State {
		case "fenced_unused":
			if receipt.Uplink != nil || receipt.Downlink != nil {
				return ErrLeaseUnavailable
			}
		case "fenced":
			if receipt.Uplink == nil || receipt.Downlink == nil || *receipt.Uplink < 0 || *receipt.Downlink < 0 {
				return ErrLeaseUnavailable
			}
		default:
			return ErrLeaseUnavailable
		}
		return nil
	}
	if receipt.State != "granted" || receipt.DeadlineBoottimeNS != control.DeadlineBoottimeNS || receipt.LeaseRemainingMS == nil || *receipt.LeaseRemainingMS > uint32(managedLeaseWindow/time.Millisecond) || receipt.Uplink != nil || receipt.Downlink != nil {
		return ErrLeaseUnavailable
	}
	return nil
}

func (reconciler *Reconciler) leaseClockNow() (string, int64, error) {
	if reconciler == nil || !reconciler.managedLeaseEnabled || reconciler.leaseClock == nil {
		return "", 0, ErrLeaseUnavailable
	}
	domain, now, err := reconciler.leaseClock()
	if err != nil || !validDigest(domain) || now <= 0 {
		return "", 0, ErrLeaseUnavailable
	}
	return domain, now, nil
}

func (reconciler *Reconciler) LeaseReceipts(ctx context.Context) (LeaseReceiptPage, error) {
	if reconciler == nil || !reconciler.managedLeaseEnabled || ctx == nil || ctx.Err() != nil {
		return LeaseReceiptPage{}, ErrLeaseUnavailable
	}
	reconciler.mutex.Lock()
	defer reconciler.mutex.Unlock()
	state, err := reconciler.store.loadLeaseState()
	if err != nil {
		return LeaseReceiptPage{}, err
	}
	return finalReceiptPage(state), nil
}

func (reconciler *Reconciler) AckLeaseReceipts(ctx context.Context, ack LeaseReceiptAck) error {
	if reconciler == nil || !reconciler.managedLeaseEnabled || ctx == nil || ctx.Err() != nil || ack.Schema != 2 || len(ack.Receipts) == 0 || len(ack.Receipts) > maxFinalReceiptPage {
		return ErrLeaseUnavailable
	}
	reconciler.mutex.Lock()
	defer reconciler.mutex.Unlock()
	state, err := reconciler.store.loadLeaseState()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, item := range ack.Receipts {
		if !validDigest(item.ReceiptID) || !validDigest(item.ProofSHA256) || seen[item.ReceiptID] {
			return ErrConflict
		}
		seen[item.ReceiptID] = true
		if stored, ok := state.FinalReceipts[item.ReceiptID]; ok && stored.ProofSHA256 != item.ProofSHA256 {
			return ErrConflict
		}
	}
	for _, item := range ack.Receipts {
		delete(state.FinalReceipts, item.ReceiptID)
	}
	if boot, err := reconciler.processBootID(); err == nil && safeIdentifier(boot) {
		pruneAcknowledgedLeaseHistory(&state, boot)
	}
	return reconciler.store.saveLeaseState(state)
}

func pruneAcknowledgedLeaseHistory(state *leaseState, currentBoot string) {
	retained := map[string]bool{}
	for _, final := range state.FinalReceipts {
		retained[leaseUserKey(final.Control.BootID, final.Control.Email)] = true
	}
	if state.Pending != nil {
		for _, step := range state.Pending.Steps {
			retained[leaseUserKey(step.BootID, step.Email)] = true
		}
	}
	for key, user := range state.Users {
		if user.BootID != currentBoot && !retained[key] && (user.Phase == "ready" || user.Phase == "fenced" || user.Phase == "removed") {
			delete(state.Users, key)
		}
	}
}

func (reconciler *Reconciler) saveLeaseChallengeLocked(desired Desired, receipt Receipt, domain string, started int64) (*LeaseChallenge, error) {
	currentDomain, now, err := reconciler.leaseClockNow()
	if err != nil || currentDomain != domain || now < started || started > math.MaxInt64-int64(managedLeaseWindow) || now >= started+int64(managedLeaseWindow) {
		return nil, ErrLeaseUnavailable
	}
	state, err := reconciler.store.loadLeaseState()
	if err != nil {
		return nil, err
	}
	if state.Pending != nil {
		return nil, nil
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrLeaseUnavailable
	}
	challenge := LeaseChallenge{Schema: 2, Nonce: hex.EncodeToString(nonce), ClockDomain: domain, ReadStartedBoottimeNS: started, MaxDeadlineBoottimeNS: started + int64(managedLeaseWindow), ManagedUsers: append([]string{}, desired.ManagedUsers...)}
	state.Challenge = &savedLeaseChallenge{Challenge: challenge, Receipt: receipt}
	if err := reconciler.store.saveLeaseState(state); err != nil {
		return nil, err
	}
	return &challenge, nil
}

func (reconciler *Reconciler) UseLease(ctx context.Context, request UseLeaseRequest) (UseLeaseResult, error) {
	if reconciler == nil || !reconciler.managedLeaseEnabled || ctx == nil || !validUseLeaseRequest(request) {
		return UseLeaseResult{}, ErrLeaseUnavailable
	}
	request = cloneUseLeaseRequest(request)
	reconciler.mutex.Lock()
	defer reconciler.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return UseLeaseResult{}, err
	}
	state, err := reconciler.store.loadLeaseState()
	if err != nil {
		return UseLeaseResult{}, err
	}
	requestHash := leaseHash(request)
	if state.Pending != nil {
		if state.Pending.Request == nil || state.Pending.Key != requestHash {
			return UseLeaseResult{}, ErrConflict
		}
		result, err := reconciler.executeLeasePendingLocked(ctx, &state)
		result = reconciler.currentLeaseResult(result, request)
		if !result.Complete && err == nil {
			err = ErrLeaseUnavailable
		}
		return result, err
	}
	if state.Completed != nil && state.Completed.Result.Nonce == request.Nonce {
		if state.Completed.RequestSHA256 != requestHash {
			return UseLeaseResult{}, ErrConflict
		}
		result := reconciler.currentLeaseResult(state.Completed.Result, request)
		if !result.Complete {
			return result, ErrLeaseUnavailable
		}
		return result, nil
	}
	desired, err := reconciler.store.LoadDesired()
	if err != nil {
		return UseLeaseResult{}, err
	}
	domain, now, err := reconciler.leaseClockNow()
	if err != nil {
		return UseLeaseResult{}, err
	}
	boot, err := reconciler.processBootID()
	if err != nil {
		return UseLeaseResult{}, ErrLeaseUnavailable
	}
	if desired.ActionKey() != request.ActionKey || desired.ConfigDigest != request.ConfigDigest || desired.ManagedUserSetDigest != request.ManagedUserSetDigest ||
		boot != request.XrayProcessBootID || domain != request.ClockDomain || state.Challenge == nil {
		return UseLeaseResult{}, ErrConflict
	}
	challenge := state.Challenge
	if challenge.Challenge.Nonce != request.Nonce || challenge.Challenge.ClockDomain != domain || challenge.Challenge.ReadStartedBoottimeNS != request.ReadStartedBoottimeNS ||
		request.DeadlineBoottimeNS > challenge.Challenge.MaxDeadlineBoottimeNS || challenge.Receipt.ActionKey != request.ActionKey || challenge.Receipt.XrayProcessBootID != boot ||
		challenge.Receipt.ConfigDigest != request.ConfigDigest || challenge.Receipt.ManagedUserSetDigest != request.ManagedUserSetDigest || !challenge.Receipt.ReadyAt(reconciler.now()) ||
		now < request.ReadStartedBoottimeNS || (len(request.Emails) > 0 && now >= request.DeadlineBoottimeNS) {
		return UseLeaseResult{}, ErrLeaseUnavailable
	}
	if !setsEqual(stringSet(challenge.Challenge.ManagedUsers), stringSet(desired.ManagedUsers)) {
		return UseLeaseResult{}, ErrConflict
	}
	managed := stringSet(desired.ManagedUsers)
	for _, email := range request.Emails {
		if _, ok := managed[email]; !ok {
			return UseLeaseResult{}, ErrConflict
		}
	}
	if len(request.Emails) > 0 && len(state.FinalReceipts) > 0 {
		return UseLeaseResult{}, ErrLeasePending
	}
	for _, user := range state.Users {
		if user.BootID != boot && (user.Phase == "active" || user.Phase == "unknown") {
			return UseLeaseResult{}, ErrLeaseUnavailable
		}
	}
	binding, ok := reconciler.preflight.(runtimeBindingReader)
	if !ok || binding.ValidateRuntimeBinding(reconciler.releaseID, reconciler.configDigest) != nil {
		return UseLeaseResult{}, ErrLeaseUnavailable
	}
	pending, err := reconciler.planUseLeaseLocked(&state, desired, request, now)
	if err != nil {
		return UseLeaseResult{}, err
	}
	state.Pending = pending
	// Consuming the nonce and persisting the complete operation/generation plan
	// are one durable replace, before the first possibly uncertain runtime RPC.
	state.Challenge = nil
	if err := reconciler.store.saveLeaseState(state); err != nil {
		return UseLeaseResult{}, err
	}
	result, err := reconciler.executeLeasePendingLocked(ctx, &state)
	result = reconciler.currentLeaseResult(result, request)
	if !result.Complete && err == nil {
		err = ErrLeaseUnavailable
	}
	return result, err
}

func (reconciler *Reconciler) currentLeaseResult(result UseLeaseResult, request UseLeaseRequest) UseLeaseResult {
	if !result.Complete || len(request.Emails) == 0 {
		return result
	}
	domain, now, err := reconciler.leaseClockNow()
	boot, bootErr := reconciler.processBootID()
	desired, desiredErr := reconciler.store.LoadDesired()
	state, stateErr := reconciler.store.loadLeaseState()
	live := err == nil && bootErr == nil && desiredErr == nil && stateErr == nil && domain == request.ClockDomain && boot == request.XrayProcessBootID && now >= request.ReadStartedBoottimeNS && now < request.DeadlineBoottimeNS &&
		desired.ActionKey() == request.ActionKey && desired.ConfigDigest == request.ConfigDigest && desired.ManagedUserSetDigest == request.ManagedUserSetDigest && len(result.Receipts) == len(request.Emails)
	proofs := map[string]LeaseReceiptProof{}
	for _, proof := range result.Receipts {
		if _, duplicate := proofs[proof.Control.Email]; duplicate {
			live = false
		}
		proofs[proof.Control.Email] = proof
	}
	for _, email := range request.Emails {
		user, exists := state.Users[leaseUserKey(boot, email)]
		proof, hasProof := proofs[email]
		if !exists || !hasProof || user.Phase != "active" || user.ConfigDigest != request.ConfigDigest || user.ClockDomain != domain || user.Binding != bindingForDesired(desired) ||
			user.DeadlineBoottimeNS != request.DeadlineBoottimeNS || proof.LeaseBinding != user.Binding || proof.Control.Generation != user.Generation ||
			proof.Control.DeadlineBoottimeNS != request.DeadlineBoottimeNS || proof.Control.Operation == "fence" || proof.Control.Schema != request.Schema ||
			proof.Control.CumulativeByteCeiling != request.CumulativeByteCeilings[email] ||
			(request.Schema == 3 && (user.BudgetSchema != 3 || user.CumulativeByteCeiling != request.CumulativeByteCeilings[email] || user.LastFencedGeneration != request.ExpectedFenceGenerations[email])) ||
			validateLeaseReceipt(proof.Control, proof.Receipt) != nil {
			live = false
		}
	}
	if !live {
		result.Complete = false
		result.NeedsFreshNonce = true
	}
	return result
}

func (reconciler *Reconciler) planUseLeaseLocked(state *leaseState, desired Desired, request UseLeaseRequest, now int64) (*pendingLeaseCommand, error) {
	// Fence epochs bind credit to the final receipt whose unused reservation
	// may already have been returned. Reject stale credit before reserving any
	// fresh runtime generation, even when the request carries a fresh nonce.
	if !matchesByteFenceGenerations(state, request) {
		return nil, ErrConflict
	}
	command := &pendingLeaseCommand{Key: leaseHash(request), Request: &request, Result: UseLeaseResult{Schema: request.Schema, Nonce: request.Nonce, Receipts: []LeaseReceiptProof{}}}
	authorized := stringSet(request.Emails)
	rearm := map[string]bool{}
	fence := map[string]bool{}
	for _, email := range desired.ManagedUsers {
		user, exists := state.Users[leaseUserKey(request.XrayProcessBootID, email)]
		if !exists {
			return nil, ErrLeaseUnavailable
		}
		_, allow := authorized[email]
		if allow && request.Schema == 2 && user.BudgetSchema == 3 {
			return nil, ErrLeaseUnavailable
		}
		if allow && user.Phase != "ready" && !(user.Phase == "active" && now < user.DeadlineBoottimeNS) {
			rearm[email] = true
			fence[email] = true
		}
		if allow && request.Schema == 3 && user.Phase == "active" && user.BudgetSchema != 3 {
			rearm[email] = true
			fence[email] = true
		}
		if !allow && (user.Phase == "active" || user.Phase == "unknown") {
			fence[email] = true
		}
	}
	if len(fence) > 0 {
		// Rearming is a deny-only operation. All final tails must be ingested and
		// acknowledged before a genuinely fresh usage nonce can authorize grants.
		command.Result.NeedsFreshNonce = true
		for _, email := range desired.ManagedUsers {
			if !fence[email] {
				continue
			}
			key := leaseUserKey(request.XrayProcessBootID, email)
			user := state.Users[key]
			if err := reserveLeaseControl(state, command, user.Binding, email, request.XrayProcessBootID, request.ClockDomain, request.ConfigDigest, "fence", 0); err != nil {
				return nil, err
			}
			if rearm[email] {
				command.Steps = append(command.Steps, leaseStep{Kind: "remove", Email: email, BootID: request.XrayProcessBootID, Binding: bindingForDesired(desired)}, leaseStep{Kind: "add", Email: email, BootID: request.XrayProcessBootID, Binding: bindingForDesired(desired)})
			}
		}
		return command, nil
	}
	for _, email := range request.Emails {
		user := state.Users[leaseUserKey(request.XrayProcessBootID, email)]
		operation := "grant"
		if user.Phase == "active" {
			operation = "renew"
		}
		if err := reserveLeaseControl(state, command, bindingForDesired(desired), email, request.XrayProcessBootID, request.ClockDomain, request.ConfigDigest, operation, request.DeadlineBoottimeNS); err != nil {
			return nil, err
		}
	}
	return command, nil
}

func reserveLeaseControl(state *leaseState, command *pendingLeaseCommand, binding LeaseBinding, email, boot, domain, digest, operation string, deadline int64) error {
	key := leaseUserKey(boot, email)
	user, exists := state.Users[key]
	if !exists {
		if len(state.Users) >= maxLeaseUserHistory {
			return ErrLeaseCapacity
		}
		user = leaseUserState{BootID: boot, Email: email, ClockDomain: domain, ConfigDigest: digest, Binding: binding}
	}
	if user.Generation == math.MaxUint64 || user.ClockDomain != domain || user.ConfigDigest != digest {
		return ErrLeaseUnavailable
	}
	schema, ceiling := 2, int64(0)
	if operation == "fence" {
		// A possibly accepted budgeted grant keeps its schema even after an
		// unknown RPC result or restart, so cleanup retains the actual byte tail.
		if user.BudgetSchema == 3 {
			schema = 3
		}
	} else if command.Request != nil {
		schema = command.Request.Schema
		ceiling = command.Request.CumulativeByteCeilings[email]
		if user.BudgetSchema == 3 && schema != 3 {
			return ErrLeaseUnavailable
		}
		expectedFence, hasFenceGeneration := command.Request.ExpectedFenceGenerations[email]
		if schema == 3 && (!hasFenceGeneration || expectedFence != user.LastFencedGeneration || ceiling <= 0 || ceiling < user.CumulativeBytes ||
			(operation == "renew" && ceiling < user.CumulativeByteCeiling)) {
			return ErrLeaseUnavailable
		}
	}
	control := runtimefence.Control{Schema: schema, Operation: operation, Email: email, BootID: boot, ConfigDigest: digest, Generation: user.Generation + 1, ClockDomain: domain, DeadlineBoottimeNS: deadline, CumulativeByteCeiling: ceiling}
	if !validLeaseControl(control) || !validLeaseBinding(binding) {
		return ErrLeaseUnavailable
	}
	user.Generation++
	user.Phase = "unknown"
	user.ReadinessActionKey = ""
	if schema == 3 {
		user.BudgetSchema = 3
		if operation != "fence" {
			user.CumulativeByteCeiling = ceiling
		}
	}
	state.Users[key] = user
	command.Steps = append(command.Steps, leaseStep{Kind: "control", Email: email, BootID: boot, Binding: binding, Control: &control})
	if operation == "fence" {
		count := len(state.FinalReceipts)
		for _, step := range command.Steps {
			if step.Control != nil && step.Control.Operation == "fence" {
				count++
			}
		}
		if count > maxFinalReceipts {
			return ErrLeaseCapacity
		}
	}
	return nil
}

func (reconciler *Reconciler) executeLeasePendingLocked(ctx context.Context, state *leaseState) (UseLeaseResult, error) {
	controller, ok := reconciler.handler.(managedRuntimeController)
	if !ok || state.Pending == nil {
		return UseLeaseResult{}, ErrLeaseUnavailable
	}
	pending := state.Pending
	if pending.Request == nil {
		desired, err := ParseDesired(pending.DesiredJSON)
		if err != nil || desired.ReleaseID != reconciler.releaseID || desired.ConfigDigest != reconciler.configDigest {
			return pending.Result, ErrLeaseUnavailable
		}
		if err := reconciler.store.SaveDesired(desired); err != nil {
			return pending.Result, err
		}
	}
	for pending.Next < len(pending.Steps) {
		if err := ctx.Err(); err != nil {
			return pending.Result, err
		}
		step := pending.Steps[pending.Next]
		boot, err := reconciler.processBootID()
		if err != nil || boot != step.BootID {
			return pending.Result, ErrLeaseUnavailable
		}
		key := leaseUserKey(step.BootID, step.Email)
		user := state.Users[key]
		switch step.Kind {
		case "control":
			control := *step.Control
			if control.ConfigDigest != reconciler.configDigest {
				return pending.Result, ErrLeaseUnavailable
			}
			if control.Operation != "fence" {
				if control.Schema == 3 && (pending.Request == nil || pending.Request.ExpectedFenceGenerations[step.Email] != user.LastFencedGeneration) {
					return pending.Result, ErrConflict
				}
				domain, now, clockErr := reconciler.leaseClockNow()
				if clockErr != nil || domain != control.ClockDomain {
					return pending.Result, ErrLeaseUnavailable
				}
				if now >= control.DeadlineBoottimeNS {
					// An expired exact tuple cannot ever authorize again. Preserve its
					// reserved sequence/unknown state for a later real drain, not regrant.
					pending.Result.NeedsFreshNonce = true
					pending.Next++
					if err := reconciler.store.saveLeaseState(*state); err != nil {
						return pending.Result, err
					}
					continue
				}
			}
			rpcContext, cancel := context.WithTimeout(ctx, 3*time.Second)
			receipt, rpcErr := controller.ApplyManagedControl(rpcContext, control)
			cancel()
			if validateLeaseReceipt(control, receipt) != nil {
				return pending.Result, ErrLeasePending
			}
			if control.Schema == 3 {
				if *receipt.CumulativeBytes < user.CumulativeBytes {
					return pending.Result, ErrLeasePending
				}
				user.BudgetSchema = 3
				user.CumulativeBytes = *receipt.CumulativeBytes
			}
			proof := LeaseReceiptProof{LeaseBinding: step.Binding, Control: control, Receipt: receipt}
			pending.Result.Receipts = append(pending.Result.Receipts, proof)
			user.Binding = step.Binding
			user.DeadlineBoottimeNS = control.DeadlineBoottimeNS
			if control.Operation == "fence" {
				if control.Schema == 3 {
					if control.Generation < user.LastFencedGeneration {
						return pending.Result, ErrConflict
					}
					user.LastFencedGeneration = control.Generation
				}
				user.Phase = "fenced"
				final := FinalLeaseReceipt{ReceiptID: leaseOperationID(step.Binding, control), ProofSHA256: leaseHash(proof), LeaseReceiptProof: proof}
				if old, exists := state.FinalReceipts[final.ReceiptID]; exists && old.ProofSHA256 != final.ProofSHA256 {
					return pending.Result, ErrConflict
				}
				state.FinalReceipts[final.ReceiptID] = final
			} else {
				user.Phase = "active"
				if rpcErr != nil {
					pending.Result.NeedsFreshNonce = true
				}
			}
			state.Users[key] = user
		case "remove":
			if user.Phase != "fenced" && user.Phase != "removed" {
				return pending.Result, ErrLeaseUnavailable
			}
			users, err := reconciler.handler.ListUsers(ctx, reconciler.inboundTag)
			if err != nil {
				return pending.Result, ErrLeasePending
			}
			if _, exists := stringSet(users)[step.Email]; exists {
				if err := reconciler.handler.RemoveUser(ctx, reconciler.inboundTag, step.Email); err != nil {
					return pending.Result, ErrLeasePending
				}
			}
			user.Phase = "removed"
			state.Users[key] = user
		case "add":
			if user.Phase != "fenced" && user.Phase != "removed" {
				return pending.Result, ErrLeaseUnavailable
			}
			users, err := reconciler.handler.ListUsers(ctx, reconciler.inboundTag)
			if err != nil {
				return pending.Result, ErrLeasePending
			}
			if _, exists := stringSet(users)[step.Email]; !exists {
				if err := reconciler.handler.AddUser(ctx, reconciler.inboundTag, step.Email); err != nil {
					return pending.Result, ErrLeasePending
				}
			}
			matches, err := reconciler.handler.ManagedUserAccountMatches(ctx, reconciler.inboundTag, step.Email)
			if err != nil || !matches {
				return pending.Result, ErrLeaseUnavailable
			}
			user.Phase = "ready"
			user.Binding = step.Binding
			user.DeadlineBoottimeNS = 0
			state.Users[key] = user
		default:
			return pending.Result, ErrLeaseUnavailable
		}
		pending.Next++
		// A successful drain and its exact proof reach stable storage before a
		// following RemoveUser, even if this operation later fails/restarts.
		if err := reconciler.store.saveLeaseState(*state); err != nil {
			return pending.Result, err
		}
	}
	pending.Result.Complete = !pending.Result.NeedsFreshNonce
	result := pending.Result
	if pending.Request != nil {
		state.Completed = &completedLeaseCommand{RequestSHA256: pending.Key, Result: result}
	}
	state.Pending = nil
	if err := reconciler.store.saveLeaseState(*state); err != nil {
		return result, err
	}
	if !result.Complete {
		return result, ErrLeaseUnavailable
	}
	return result, nil
}

func (reconciler *Reconciler) settleLeasePendingForReconcileLocked(ctx context.Context) error {
	state, err := reconciler.store.loadLeaseState()
	if err != nil {
		return err
	}
	if state.Pending == nil {
		return nil
	}
	if state.Pending.Request != nil {
		denyOnly := true
		for _, step := range state.Pending.Steps[state.Pending.Next:] {
			if step.Control != nil && step.Control.Operation != "fence" {
				denyOnly = false
			}
		}
		if denyOnly {
			_, err := reconciler.executeLeasePendingLocked(ctx, &state)
			if errors.Is(err, ErrLeaseUnavailable) && state.Pending == nil {
				return nil
			}
			return err
		}
		request := state.Pending.Request
		domain, now, err := reconciler.leaseClockNow()
		if err != nil || domain != request.ClockDomain || now < request.DeadlineBoottimeNS {
			return ErrLeasePending
		}
		// Never replay a backend grant from Apply/Refresh/Recover. Only already
		// expired tuples can be retired without RPC; unknown users still drain.
		for _, step := range state.Pending.Steps[state.Pending.Next:] {
			if step.Kind != "control" || step.Control.Operation == "fence" {
				return ErrLeasePending
			}
		}
		result := state.Pending.Result
		result.Complete = false
		result.NeedsFreshNonce = true
		state.Completed = &completedLeaseCommand{RequestSHA256: state.Pending.Key, Result: result}
		state.Pending = nil
		return reconciler.store.saveLeaseState(state)
	}
	_, err = reconciler.executeLeasePendingLocked(ctx, &state)
	return err
}

func (reconciler *Reconciler) convergeCommercial(ctx context.Context, desired, previous Desired) error {
	current, err := reconciler.handler.ListUsers(ctx, reconciler.inboundTag)
	if err != nil {
		return ErrLeaseUnavailable
	}
	if err := verifyStaticUsers(current, desired.StaticUsers); err != nil {
		return err
	}
	state, err := reconciler.store.loadLeaseState()
	if err != nil {
		return err
	}
	if state.Pending != nil {
		return ErrLeasePending
	}
	domain, now, err := reconciler.leaseClockNow()
	if err != nil {
		return err
	}
	boot, err := reconciler.processBootID()
	if err != nil {
		return ErrLeaseUnavailable
	}
	managed, expected := managedSet(current), stringSet(desired.ManagedUsers)
	readinessOnly := len(state.FinalReceipts) == 0 && readinessGenerationOnly(previous, desired)
	readinessChanged := false
	retiring := managedSet(current)
	for _, email := range previous.ManagedUsers {
		retiring[email] = struct{}{}
	}
	for _, user := range state.Users {
		if user.BootID == boot && user.Phase != "removed" {
			retiring[user.Email] = struct{}{}
		}
	}
	command := &pendingLeaseCommand{Key: leaseHash(struct{ Action, Boot string }{desired.ActionKey(), boot}), DesiredJSON: desired.CanonicalJSON(), Result: UseLeaseResult{Schema: 2, Receipts: []LeaseReceiptProof{}}}
	for _, email := range sortedDifference(retiring, expected) {
		user, exists := state.Users[leaseUserKey(boot, email)]
		binding := user.Binding
		if !exists {
			if _, ok := stringSet(previous.ManagedUsers)[email]; !ok {
				return ErrLeaseUnavailable
			}
			binding = bindingForDesired(previous)
		}
		if err := reserveLeaseControl(&state, command, binding, email, boot, domain, reconciler.configDigest, "fence", 0); err != nil {
			return err
		}
		command.Steps = append(command.Steps, leaseStep{Kind: "remove", Email: email, BootID: boot, Binding: binding})
	}
	for _, email := range desired.ManagedUsers {
		if !validManagedLeaseEmail(email) {
			return ErrInvalidDesired
		}
		_, present := managed[email]
		user, tracked := state.Users[leaseUserKey(boot, email)]
		matches := false
		if present {
			matches, err = reconciler.handler.ManagedUserAccountMatches(ctx, reconciler.inboundTag, email)
			if err != nil {
				return ErrLeaseUnavailable
			}
		}
		preserveBinding := user.Binding.ActionKey == desired.ActionKey() ||
			(len(state.FinalReceipts) == 0 && user.ReadinessActionKey == desired.ActionKey()) ||
			(readinessOnly && (user.Binding == bindingForDesired(previous) || user.ReadinessActionKey == previous.ActionKey()))
		if present && tracked && matches && preserveBinding && (user.Phase == "ready" || (user.Phase == "active" && now < user.DeadlineBoottimeNS)) {
			// A readiness generation carries no new runtime authority. Keep the
			// prior binding for authentic final tails until a subsequent successful
			// grant/renew adopts the new desired binding; lease and byte cap stay put.
			if user.Binding.ActionKey != desired.ActionKey() && user.ReadinessActionKey != desired.ActionKey() {
				user.ReadinessActionKey = desired.ActionKey()
				state.Users[leaseUserKey(boot, email)] = user
				readinessChanged = true
			}
			continue
		}
		binding := bindingForDesired(desired)
		if tracked {
			binding = user.Binding
		}
		if err := reserveLeaseControl(&state, command, binding, email, boot, domain, reconciler.configDigest, "fence", 0); err != nil {
			return err
		}
		if present {
			command.Steps = append(command.Steps, leaseStep{Kind: "remove", Email: email, BootID: boot, Binding: binding})
		}
		command.Steps = append(command.Steps, leaseStep{Kind: "add", Email: email, BootID: boot, Binding: bindingForDesired(desired)})
	}
	if len(command.Steps) > 0 {
		state.Pending = command
		state.Challenge = nil
		if err := reconciler.store.saveLeaseState(state); err != nil {
			return err
		}
		if _, err := reconciler.executeLeasePendingLocked(ctx, &state); err != nil {
			return err
		}
	} else {
		// Record continuity before SaveDesired so a repeated action or a crash
		// between desired and receipt persistence cannot force an extra fence.
		if readinessChanged {
			if err := reconciler.store.saveLeaseState(state); err != nil {
				return err
			}
		}
		if err := reconciler.store.SaveDesired(desired); err != nil {
			return err
		}
	}
	final, err := reconciler.handler.ListUsers(ctx, reconciler.inboundTag)
	if err != nil || verifyStaticUsers(final, desired.StaticUsers) != nil || !setsEqual(managedSet(final), expected) {
		return ErrLeaseUnavailable
	}
	return nil
}
