package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

func Apply(ctx context.Context, store ApplyStore, plan ImportPlan, options ApplyOptions) (ApplyResult, error) {
	if len(plan.Blockers) != 0 {
		return ApplyResult{}, ErrBlockedPlan
	}
	if options.RunID == "" || options.BatchSize <= 0 || plan.SourceDigest == "" || plan.PlanDigest == "" {
		return ApplyResult{}, errors.New("invalid apply options or plan digest")
	}
	operations, err := planOperations(plan)
	if err != nil {
		return ApplyResult{}, err
	}
	batches := makeBatches(options.RunID, plan.PlanDigest, operations, options.BatchSize)

	targetBefore, err := store.InspectTarget(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	progress, err := store.BeginOrResume(ctx, ApplyRun{
		RunID:        options.RunID,
		SnapshotKind: plan.SnapshotKind,
		SourceDigest: plan.SourceDigest,
		PlanDigest:   plan.PlanDigest,
		ParentDigest: plan.ParentSourceDigest,
		BatchCount:   len(batches),
	})
	if err != nil {
		return ApplyResult{}, err
	}
	if progress.Completed {
		if targetBefore.BusinessDigest != progress.TargetDigest {
			return ApplyResult{}, ErrRunDigestMismatch
		}
		return ApplyResult{Counts: cloneCounts(plan.Counts), TargetDigest: progress.TargetDigest, AppliedBatches: len(batches)}, nil
	}
	if len(progress.AppliedBatchDigests) == 0 {
		if plan.SnapshotKind == "full" && !targetBefore.Empty {
			return ApplyResult{}, ErrTargetNotEmpty
		}
		if plan.SnapshotKind == "delta" && targetBefore.AppliedSourceDigest != plan.ParentSourceDigest {
			return ApplyResult{}, ErrParentMismatch
		}
	}

	for _, batch := range batches {
		if previous, exists := progress.AppliedBatchDigests[batch.Index]; exists {
			if previous != batch.Digest {
				return ApplyResult{}, ErrRunDigestMismatch
			}
			continue
		}
		receipt, err := store.CommitBatch(ctx, batch)
		if err != nil {
			return ApplyResult{}, err
		}
		if receipt.Index != batch.Index || receipt.Digest != batch.Digest {
			return ApplyResult{}, ErrRunDigestMismatch
		}
	}

	targetAfter, err := store.InspectTarget(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	completion := ApplyCompletion{
		RunID:        options.RunID,
		SourceDigest: plan.SourceDigest,
		PlanDigest:   plan.PlanDigest,
		TargetDigest: targetAfter.BusinessDigest,
	}
	if err := store.Complete(ctx, completion); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Counts: cloneCounts(plan.Counts), TargetDigest: targetAfter.BusinessDigest, AppliedBatches: len(batches)}, nil
}

func makeBatches(runID, planDigest string, operations []ApplyOperation, batchSize int) []ApplyBatch {
	if len(operations) == 0 {
		return nil
	}
	batches := make([]ApplyBatch, 0, (len(operations)+batchSize-1)/batchSize)
	for start := 0; start < len(operations); start += batchSize {
		end := start + batchSize
		if end > len(operations) {
			end = len(operations)
		}
		items := append([]ApplyOperation(nil), operations[start:end]...)
		batches = append(batches, ApplyBatch{
			RunID:      runID,
			PlanDigest: planDigest,
			Index:      len(batches),
			Digest:     digestBatch(items),
			Operations: items,
		})
	}
	return batches
}

func planOperations(plan ImportPlan) ([]ApplyOperation, error) {
	var operations []ApplyOperation
	appendJSON := func(entity, key string, value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode %s operation: %w", entity, err)
		}
		operations = append(operations, ApplyOperation{Entity: entity, Key: key, CanonicalJSON: encoded})
		return nil
	}
	for _, value := range plan.Customers {
		if err := appendJSON("customer", value.SourceKey, value); err != nil {
			return nil, err
		}
	}
	for _, value := range plan.Orders {
		if err := appendJSON("order", value.SourceKey, value); err != nil {
			return nil, err
		}
	}
	for _, value := range plan.Trials {
		if err := appendJSON("trial", value.SourceKey, value); err != nil {
			return nil, err
		}
	}
	for _, value := range plan.BotBindings {
		key := value.BotIdentityHMAC + ":" + fmt.Sprint(value.CredentialVersion)
		if err := appendJSON("bot_binding", key, value); err != nil {
			return nil, err
		}
	}
	for _, value := range plan.Settings {
		if err := appendJSON("setting", value.Key, value); err != nil {
			return nil, err
		}
	}
	for _, value := range plan.Principals {
		if err := appendJSON("principal", value.SourceKey, value); err != nil {
			return nil, err
		}
	}
	for _, value := range plan.EncryptedSecrets {
		if err := appendJSON("encrypted_secret", value.SecretID, value); err != nil {
			return nil, err
		}
	}
	for _, value := range plan.BotPollStates {
		if err := appendJSON("bot_poll_state", value.BotIdentityHMAC, value); err != nil {
			return nil, err
		}
	}
	for _, value := range plan.PendingCallbacks {
		if err := appendJSON("pending_callback", value.CallbackHMAC, value); err != nil {
			return nil, err
		}
	}
	for _, value := range plan.BotCredentialRotations {
		if err := appendJSON("bot_credential_rotation", value.AuditDigest, value); err != nil {
			return nil, err
		}
	}
	for _, deletion := range append(append([]PlannedDelete(nil), plan.Deletes...), plan.CascadeDeletes...) {
		operations = append(operations, ApplyOperation{Entity: deletion.Entity, Key: deletion.SourceKey, Tombstone: true})
	}
	sort.Slice(operations, func(i, j int) bool {
		return operations[i].Entity+"\x00"+operations[i].Key < operations[j].Entity+"\x00"+operations[j].Key
	})
	return operations, nil
}
