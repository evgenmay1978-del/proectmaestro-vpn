package importer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync"
	"testing"
)

var errSyntheticAfterCommit = errors.New("synthetic transport failure after commit")

type memoryApplyStore struct {
	mu              sync.Mutex
	runs            map[string]ApplyRun
	progress        map[string]RunProgress
	state           map[string][]byte
	commitCounts    map[int]int
	failAfterCommit int
	failureUsed     bool
	appliedSource   string
}

func newMemoryApplyStore() *memoryApplyStore {
	return &memoryApplyStore{
		runs:            make(map[string]ApplyRun),
		progress:        make(map[string]RunProgress),
		state:           make(map[string][]byte),
		commitCounts:    make(map[int]int),
		failAfterCommit: -1,
	}
}

func (s *memoryApplyStore) InspectTarget(context.Context) (TargetState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return TargetState{
		Empty:               len(s.state) == 0,
		BusinessDigest:      digestMemoryState(s.state),
		AppliedSourceDigest: s.appliedSource,
	}, nil
}

func (s *memoryApplyStore) BeginOrResume(_ context.Context, run ApplyRun) (RunProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.runs[run.RunID]; ok {
		if previous.PlanDigest != run.PlanDigest || previous.SourceDigest != run.SourceDigest {
			return RunProgress{}, ErrRunDigestMismatch
		}
		return cloneProgress(s.progress[run.RunID]), nil
	}
	s.runs[run.RunID] = run
	progress := RunProgress{AppliedBatchDigests: make(map[int]string)}
	s.progress[run.RunID] = progress
	return cloneProgress(progress), nil
}

func (s *memoryApplyStore) CommitBatch(_ context.Context, batch ApplyBatch) (BatchReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	progress, ok := s.progress[batch.RunID]
	if !ok {
		return BatchReceipt{}, fmt.Errorf("unknown run %q", batch.RunID)
	}
	if previous, ok := progress.AppliedBatchDigests[batch.Index]; ok {
		if previous != batch.Digest {
			return BatchReceipt{}, ErrRunDigestMismatch
		}
		return BatchReceipt{Index: batch.Index, Digest: batch.Digest, AlreadyApplied: true}, nil
	}
	for _, operation := range batch.Operations {
		key := operation.Entity + "\x00" + operation.Key
		if operation.Tombstone {
			delete(s.state, key)
			continue
		}
		s.state[key] = append([]byte(nil), operation.CanonicalJSON...)
	}
	progress.AppliedBatchDigests[batch.Index] = batch.Digest
	s.progress[batch.RunID] = progress
	s.commitCounts[batch.Index]++
	receipt := BatchReceipt{Index: batch.Index, Digest: batch.Digest}
	if s.failAfterCommit == batch.Index && !s.failureUsed {
		s.failureUsed = true
		return receipt, errSyntheticAfterCommit
	}
	return receipt, nil
}

func (s *memoryApplyStore) Complete(_ context.Context, completion ApplyCompletion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	progress, ok := s.progress[completion.RunID]
	if !ok {
		return fmt.Errorf("unknown run %q", completion.RunID)
	}
	if len(progress.AppliedBatchDigests) != s.runs[completion.RunID].BatchCount {
		return fmt.Errorf("incomplete run %q", completion.RunID)
	}
	progress.Completed = true
	progress.TargetDigest = completion.TargetDigest
	s.progress[completion.RunID] = progress
	s.appliedSource = completion.SourceDigest
	return nil
}

func cloneProgress(progress RunProgress) RunProgress {
	clone := progress
	clone.AppliedBatchDigests = make(map[int]string, len(progress.AppliedBatchDigests))
	for index, digest := range progress.AppliedBatchDigests {
		clone.AppliedBatchDigests[index] = digest
	}
	return clone
}

func digestMemoryState(state map[string][]byte) string {
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		value := state[key]
		_, _ = fmt.Fprintf(hash, "%d:%s:%d:", len(key), key, len(value))
		_, _ = hash.Write(value)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func plannedFixture(t *testing.T, name string, options PlanOptions) ImportPlan {
	t.Helper()
	plan, report := Plan(decodeFixture(t, name), options)
	if len(report.Blockers) != 0 {
		t.Fatalf("Plan(%s) blockers: %#v", name, report.Blockers)
	}
	return plan
}

func applyFixture(t *testing.T, store ApplyStore, plan ImportPlan, runID string) ApplyResult {
	t.Helper()
	result, err := Apply(context.Background(), store, plan, ApplyOptions{RunID: runID, BatchSize: 1})
	if err != nil {
		t.Fatalf("Apply(%s): %v", runID, err)
	}
	return result
}

func preparedDelta(t *testing.T, base Snapshot, basePlan ImportPlan) Snapshot {
	t.Helper()
	delta := decodeFixture(t, "full-then-delta/delta.json")
	delta.ParentSourceDigest = basePlan.SourceDigest
	delta.Deletes[0].ExpectedPriorDigest = canonicalLegacyDigest(base.Customers[1])
	return delta
}

func TestApplyTwiceHasSameCountsAndDigest(t *testing.T) {
	plan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
	store := newMemoryApplyStore()
	first := applyFixture(t, store, plan, "same-run")
	second := applyFixture(t, store, plan, "same-run")
	if first.TargetDigest != second.TargetDigest || !reflect.DeepEqual(first.Counts, second.Counts) {
		t.Fatalf("repeat changed result: first=%#v second=%#v", first, second)
	}
	for index, count := range store.commitCounts {
		if count != 1 {
			t.Fatalf("batch %d committed %d times", index, count)
		}
	}
}

func TestCrashAtEveryBatchBoundaryResumesSameDigest(t *testing.T) {
	plan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
	referenceStore := newMemoryApplyStore()
	reference := applyFixture(t, referenceStore, plan, "reference")
	batchIndexes := make([]int, 0, len(referenceStore.commitCounts))
	for index := range referenceStore.commitCounts {
		batchIndexes = append(batchIndexes, index)
	}
	sort.Ints(batchIndexes)
	if len(batchIndexes) < 2 {
		t.Fatalf("fixture produced only %d batch(es)", len(batchIndexes))
	}
	for _, boundary := range batchIndexes {
		t.Run(fmt.Sprintf("after-batch-%d", boundary), func(t *testing.T) {
			store := newMemoryApplyStore()
			store.failAfterCommit = boundary
			_, err := Apply(context.Background(), store, plan, ApplyOptions{RunID: "resume-run", BatchSize: 1})
			if !errors.Is(err, errSyntheticAfterCommit) {
				t.Fatalf("first Apply error = %v", err)
			}
			resumed := applyFixture(t, store, plan, "resume-run")
			if resumed.TargetDigest != reference.TargetDigest {
				t.Fatalf("resumed digest=%s reference=%s", resumed.TargetDigest, reference.TargetDigest)
			}
			for index, count := range store.commitCounts {
				if count != 1 {
					t.Fatalf("batch %d committed %d times", index, count)
				}
			}
		})
	}
}

func TestDifferentDigestCannotResumePartialRun(t *testing.T) {
	firstPlan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
	secondPlan := plannedFixture(t, "full-then-delta/final-full.json", testPlanOptions())
	store := newMemoryApplyStore()
	store.failAfterCommit = 0
	_, err := Apply(context.Background(), store, firstPlan, ApplyOptions{RunID: "fixed-run", BatchSize: 1})
	if !errors.Is(err, errSyntheticAfterCommit) {
		t.Fatalf("first Apply error = %v", err)
	}
	_, err = Apply(context.Background(), store, secondPlan, ApplyOptions{RunID: "fixed-run", BatchSize: 1})
	if !errors.Is(err, ErrRunDigestMismatch) {
		t.Fatalf("different digest resume error = %v", err)
	}
}

func TestDeltaRequiresExactAppliedParentDigest(t *testing.T) {
	base := decodeFixture(t, "full-then-delta/base-full.json")
	basePlan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
	delta := preparedDelta(t, base, basePlan)
	options := testPlanOptions()
	options.ParentSnapshot = &base
	options.AppliedParentDigest = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	_, report := Plan(delta, options)
	if len(report.Blockers) != 1 || report.Blockers[0].Code != "delta_parent_digest_mismatch" {
		t.Fatalf("delta parent blockers = %#v", report.Blockers)
	}
}

func TestDeltaDeleteRequiresExactPriorDigestAndSupportedEntity(t *testing.T) {
	base := decodeFixture(t, "full-then-delta/base-full.json")
	basePlan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
	options := testPlanOptions()
	options.ParentSnapshot = &base
	options.AppliedParentDigest = basePlan.SourceDigest

	delta := preparedDelta(t, base, basePlan)
	delta.Deletes[0].ExpectedPriorDigest = ""
	if _, report := Plan(delta, options); !hasBlockerCode(report.Blockers, "delta_delete_prior_digest_mismatch") {
		t.Fatalf("missing prior digest blockers = %#v", report.Blockers)
	}

	delta = preparedDelta(t, base, basePlan)
	delta.Deletes[0].ExpectedPriorDigest = fmt.Sprintf("%064x", 1)
	if _, report := Plan(delta, options); !hasBlockerCode(report.Blockers, "delta_delete_prior_digest_mismatch") {
		t.Fatalf("mismatched prior digest blockers = %#v", report.Blockers)
	}

	delta = preparedDelta(t, base, basePlan)
	delta.Deletes[0].Entity = "order"
	if _, report := Plan(delta, options); !hasBlockerCode(report.Blockers, "unsupported_delta_delete_entity") {
		t.Fatalf("unsupported delete blockers = %#v", report.Blockers)
	}

	delta = preparedDelta(t, base, basePlan)
	delta.Deletes[0].SourceKey = "unknown-customer"
	if _, report := Plan(delta, options); !hasBlockerCode(report.Blockers, "delta_delete_source_missing") {
		t.Fatalf("unknown delete source blockers = %#v", report.Blockers)
	}
}

func TestDeltaDeleteRejectsCollisionsAndGenerationOverflow(t *testing.T) {
	base := decodeFixture(t, "full-then-delta/base-full.json")
	basePlan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
	options := testPlanOptions()
	options.ParentSnapshot = &base
	options.AppliedParentDigest = basePlan.SourceDigest

	duplicate := preparedDelta(t, base, basePlan)
	duplicate.Deletes = append(duplicate.Deletes, duplicate.Deletes[0])
	if _, report := Plan(duplicate, options); !hasBlockerCode(report.Blockers, "delta_delete_collision") {
		t.Fatalf("duplicate delete blockers = %#v", report.Blockers)
	}

	upsertAndDelete := preparedDelta(t, base, basePlan)
	upsertAndDelete.Customers = append(upsertAndDelete.Customers, base.Customers[1])
	if _, report := Plan(upsertAndDelete, options); !hasBlockerCode(report.Blockers, "delta_delete_collision") {
		t.Fatalf("upsert/delete blockers = %#v", report.Blockers)
	}

	overflowBase := decodeFixture(t, "full-then-delta/base-full.json")
	overflowBase.Customers[1].Generation = math.MaxInt64
	overflowPlan := plannedFixtureFromSnapshot(t, overflowBase, testPlanOptions())
	overflowOptions := testPlanOptions()
	overflowOptions.ParentSnapshot = &overflowBase
	overflowOptions.AppliedParentDigest = overflowPlan.SourceDigest
	overflow := preparedDelta(t, overflowBase, overflowPlan)
	if _, report := Plan(overflow, overflowOptions); !hasBlockerCode(report.Blockers, "delta_delete_generation_overflow") {
		t.Fatalf("generation overflow blockers = %#v", report.Blockers)
	}
}

func TestDeltaDeleteCarriesTypedCustomerAndDerivedSecretProof(t *testing.T) {
	base := decodeFixture(t, "full-then-delta/base-full.json")
	basePlan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
	delta := preparedDelta(t, base, basePlan)
	options := testPlanOptions()
	options.ParentSnapshot = &base
	options.AppliedParentDigest = basePlan.SourceDigest

	plan, report := Plan(delta, options)
	if len(report.Blockers) != 0 || len(plan.Deletes) != 1 || len(plan.CascadeDeletes) != 1 {
		t.Fatalf("typed deletes = %#v / %#v / %#v", plan.Deletes, plan.CascadeDeletes, report.Blockers)
	}
	customerDelete := plan.Deletes[0]
	if customerDelete.TargetID != deterministicID(options.Namespace, "customer", "customer-beta") ||
		customerDelete.ExpectedPriorDigest != canonicalLegacyDigest(base.Customers[1]) ||
		customerDelete.PriorGeneration != 1 || customerDelete.NextGeneration != 2 ||
		customerDelete.TombstoneID == "" || !customerDelete.Tombstone {
		t.Fatalf("customer delete proof = %#v", customerDelete)
	}
	secretDelete := plan.CascadeDeletes[0]
	if secretDelete.Entity != "encrypted_secret" || secretDelete.SourceKey != "secret-beta" ||
		secretDelete.TargetID != "secret-beta" ||
		secretDelete.ExpectedPriorDigest != canonicalLegacyDigest(base.EncryptedSecrets[1]) ||
		secretDelete.TombstoneID != "" || secretDelete.Tombstone {
		t.Fatalf("secret delete proof = %#v", secretDelete)
	}

	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	want := map[string]PlannedDelete{
		"customer\x00customer-beta":             customerDelete,
		"encrypted_secret\x00secret-beta": secretDelete,
	}
	for _, operation := range operations {
		key := operation.Entity + "\x00" + operation.Key
		expected, exists := want[key]
		if !exists {
			continue
		}
		if !operation.Tombstone || len(operation.CanonicalJSON) == 0 {
			t.Fatalf("typed delete operation missing proof: %#v", operation)
		}
		var decoded PlannedDelete
		if err := json.Unmarshal(operation.CanonicalJSON, &decoded); err != nil {
			t.Fatalf("decode typed delete operation: %v", err)
		}
		if !reflect.DeepEqual(decoded, expected) {
			t.Fatalf("typed delete operation = %#v, want %#v", decoded, expected)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing typed delete operations = %#v", want)
	}
}

func plannedFixtureFromSnapshot(t *testing.T, snapshot Snapshot, options PlanOptions) ImportPlan {
	t.Helper()
	plan, report := Plan(snapshot, options)
	if len(report.Blockers) != 0 {
		t.Fatalf("Plan(snapshot) blockers: %#v", report.Blockers)
	}
	return plan
}

func TestFullThenDeltaEqualsFreshFinalFullDigest(t *testing.T) {
	base := decodeFixture(t, "full-then-delta/base-full.json")
	basePlan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
	store := newMemoryApplyStore()
	applyFixture(t, store, basePlan, "base-full")

	delta := preparedDelta(t, base, basePlan)
	options := testPlanOptions()
	options.ParentSnapshot = &base
	options.AppliedParentDigest = basePlan.SourceDigest
	deltaPlan, report := Plan(delta, options)
	if len(report.Blockers) != 0 {
		t.Fatalf("delta blockers: %#v", report.Blockers)
	}
	afterDelta := applyFixture(t, store, deltaPlan, "delta")

	finalPlan := plannedFixture(t, "full-then-delta/final-full.json", testPlanOptions())
	fresh := applyFixture(t, newMemoryApplyStore(), finalPlan, "fresh-final")
	if afterDelta.TargetDigest != fresh.TargetDigest {
		t.Fatalf("full+delta digest=%s fresh final=%s", afterDelta.TargetDigest, fresh.TargetDigest)
	}
}

func TestConcurrentResumeAppliesEachBatchOnce(t *testing.T) {
	plan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
	store := newMemoryApplyStore()
	results := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := Apply(context.Background(), store, plan, ApplyOptions{RunID: "concurrent", BatchSize: 1})
			results <- err
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Apply: %v", err)
		}
	}
	for index, count := range store.commitCounts {
		if count != 1 {
			t.Fatalf("batch %d committed %d times", index, count)
		}
	}
}

func TestDeltaWithoutExplicitDeleteMarkerBlocks(t *testing.T) {
	base := decodeFixture(t, "full-then-delta/base-full.json")
	basePlan := plannedFixture(t, "full-then-delta/base-full.json", testPlanOptions())
	delta := preparedDelta(t, base, basePlan)
	delta.Deletes = nil
	options := testPlanOptions()
	options.ParentSnapshot = &base
	options.AppliedParentDigest = basePlan.SourceDigest
	_, report := Plan(delta, options)
	if len(report.Blockers) != 1 || report.Blockers[0].Code != "delta_missing_delete_marker" {
		t.Fatalf("implicit deletion blockers = %#v", report.Blockers)
	}
}
