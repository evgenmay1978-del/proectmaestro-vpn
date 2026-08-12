package applyagent

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errNoQuorum = errors.New("test: no quorum")

type fakeLeaseVerifier struct {
	mu    sync.Mutex
	calls int
	fn    func(call int, snapshotSHA256 string, epoch, incarnation, fence int64) error
}

func (v *fakeLeaseVerifier) VerifyCurrentStrong(_ context.Context, _, _, _, snapshotSHA256 string, epoch, incarnation, fence int64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	if v.fn != nil {
		return v.fn(v.calls, snapshotSHA256, epoch, incarnation, fence)
	}
	return nil
}

type fakeDriver struct {
	inspectCalls  atomic.Int32
	prepareCalls  atomic.Int32
	commitCalls   atomic.Int32
	rollbackCalls atomic.Int32
	inspectFn     func(DesiredSnapshot) (AppliedState, error)
	prepareFn     func(DesiredSnapshot) (PreparedChange, error)
	commitFn      func(PreparedChange) (AppliedState, error)
	rollbackFn    func(PreparedChange) error
}

func (d *fakeDriver) Inspect(_ context.Context, snapshot DesiredSnapshot) (AppliedState, error) {
	d.inspectCalls.Add(1)
	if d.inspectFn != nil {
		return d.inspectFn(snapshot)
	}
	return AppliedState{}, nil
}

func (d *fakeDriver) Prepare(_ context.Context, snapshot DesiredSnapshot) (PreparedChange, error) {
	d.prepareCalls.Add(1)
	if d.prepareFn != nil {
		return d.prepareFn(snapshot)
	}
	return PreparedChange{}, nil
}

func (d *fakeDriver) Commit(_ context.Context, prepared PreparedChange) (AppliedState, error) {
	d.commitCalls.Add(1)
	if d.commitFn != nil {
		return d.commitFn(prepared)
	}
	return AppliedState{SnapshotSHA256: prepared.SnapshotSHA256, Healthy: true}, nil
}

func (d *fakeDriver) Rollback(_ context.Context, prepared PreparedChange) error {
	d.rollbackCalls.Add(1)
	if d.rollbackFn != nil {
		return d.rollbackFn(prepared)
	}
	return nil
}

type fakeStateStore struct {
	mu       sync.Mutex
	marker   StateMarker
	loadErr  error
	storeErr error
	stores   int
}

func (s *fakeStateStore) Load(context.Context) (StateMarker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStateMarker(s.marker), s.loadErr
}

func (s *fakeStateStore) Store(_ context.Context, marker StateMarker) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stores++
	if s.storeErr != nil {
		err := s.storeErr
		s.storeErr = nil
		return err
	}
	s.marker = cloneStateMarker(marker)
	return nil
}

func cloneStateMarker(marker StateMarker) StateMarker {
	cloned := marker
	cloned.Entries = make(map[string]EntryMarker, len(marker.Entries))
	for key, value := range marker.Entries {
		cloned.Entries[key] = value
	}
	return cloned
}

func agentFixture(t *testing.T, verifier LeaseVerifier, driver Driver, state LocalStateStore) (*Agent, ApplyCommand, ed25519.PrivateKey) {
	t.Helper()
	command, publicKey, privateKey := protocolFixture(t)
	agent, err := NewAgent(AgentConfig{
		NodeID: "node-a", ServiceID: "xui", NodeIncarnation: 3,
		PublicKeys: map[string]ed25519.PublicKey{"dispatcher-key-1": publicKey},
		Verifier: verifier, Driver: driver, State: state,
		Clock: func() time.Time { return time.Unix(2_000_030, 0) },
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	return agent, command, privateKey
}

func signedFixture(t *testing.T, command ApplyCommand, privateKey ed25519.PrivateKey) SignedCommand {
	t.Helper()
	signed, err := SignCommand(command, "dispatcher-key-1", privateKey)
	if err != nil {
		t.Fatalf("SignCommand: %v", err)
	}
	return signed
}

func totalDriverCalls(driver *fakeDriver) int32 {
	return driver.inspectCalls.Load() + driver.prepareCalls.Load() + driver.commitCalls.Load() + driver.rollbackCalls.Load()
}

func TestAgentRejectsWrongNodeServiceEpochOrIncarnation(t *testing.T) {
	verifier := &fakeLeaseVerifier{}
	driver := &fakeDriver{}
	agent, command, privateKey := agentFixture(t, verifier, driver, &fakeStateStore{})
	command.NodeIncarnation++
	if _, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey)); err == nil {
		t.Fatal("wrong node incarnation was accepted")
	}
	if totalDriverCalls(driver) != 0 || verifier.calls != 0 {
		t.Fatalf("identity mismatch reached verifier/driver: verifier=%d driver=%d", verifier.calls, totalDriverCalls(driver))
	}
}

func TestAgentRejectsOldFenceEvenBeforeSeeingNewCommand(t *testing.T) {
	verifier := &fakeLeaseVerifier{fn: func(_ int, _ string, _, _, fence int64) error {
		if fence < 12 {
			return ErrStaleFence
		}
		return nil
	}}
	driver := &fakeDriver{}
	agent, command, privateKey := agentFixture(t, verifier, driver, &fakeStateStore{})
	if _, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("Apply old fence error=%v, want ErrStaleFence", err)
	}
	if totalDriverCalls(driver) != 0 {
		t.Fatalf("old fence reached driver: calls=%d", totalDriverCalls(driver))
	}
}

func TestNoQuorumCausesZeroDriverCalls(t *testing.T) {
	verifier := &fakeLeaseVerifier{fn: func(int, string, int64, int64, int64) error { return errNoQuorum }}
	driver := &fakeDriver{}
	agent, command, privateKey := agentFixture(t, verifier, driver, &fakeStateStore{})
	if _, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey)); !errors.Is(err, errNoQuorum) {
		t.Fatalf("Apply no quorum error=%v, want %v", err, errNoQuorum)
	}
	if totalDriverCalls(driver) != 0 {
		t.Fatalf("no quorum reached driver: calls=%d", totalDriverCalls(driver))
	}
}

func TestFenceIsRecheckedImmediatelyBeforeSwap(t *testing.T) {
	verifier := &fakeLeaseVerifier{fn: func(call int, _ string, _, _, _ int64) error {
		if call == 2 {
			return ErrStaleFence
		}
		return nil
	}}
	driver := &fakeDriver{}
	agent, command, privateKey := agentFixture(t, verifier, driver, &fakeStateStore{})
	if _, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("Apply second fence check error=%v, want ErrStaleFence", err)
	}
	if driver.prepareCalls.Load() != 1 || driver.commitCalls.Load() != 0 || driver.rollbackCalls.Load() != 1 {
		t.Fatalf("prepare/commit/rollback=%d/%d/%d, want 1/0/1",
			driver.prepareCalls.Load(), driver.commitCalls.Load(), driver.rollbackCalls.Load())
	}
}

func TestCrashAfterSideEffectBeforeReceiptRetriesAsHashNoOp(t *testing.T) {
	verifier := &fakeLeaseVerifier{}
	state := &fakeStateStore{storeErr: errors.New("test: fsync failed")}
	var currentHash string
	driver := &fakeDriver{
		inspectFn: func(DesiredSnapshot) (AppliedState, error) {
			return AppliedState{SnapshotSHA256: currentHash, Healthy: true}, nil
		},
		commitFn: func(prepared PreparedChange) (AppliedState, error) {
			currentHash = prepared.SnapshotSHA256
			return AppliedState{SnapshotSHA256: currentHash, Healthy: true}, nil
		},
	}
	agent, command, privateKey := agentFixture(t, verifier, driver, state)
	signed := signedFixture(t, command, privateKey)
	if _, err := agent.Apply(context.Background(), signed); err == nil {
		t.Fatal("state fsync failure was hidden")
	}
	if _, err := agent.Apply(context.Background(), signed); err != nil {
		t.Fatalf("hash no-op retry: %v", err)
	}
	if driver.commitCalls.Load() != 1 {
		t.Fatalf("commit calls=%d, want 1", driver.commitCalls.Load())
	}
}

func TestConcurrentApplyIsSerializedPerService(t *testing.T) {
	verifier := &fakeLeaseVerifier{}
	var active atomic.Int32
	var maximum atomic.Int32
	driver := &fakeDriver{prepareFn: func(snapshot DesiredSnapshot) (PreparedChange, error) {
		current := active.Add(1)
		for {
			old := maximum.Load()
			if current <= old || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		return PreparedChange{SnapshotSHA256: snapshot.SnapshotSHA256}, nil
	}}
	agent, command, privateKey := agentFixture(t, verifier, driver, &fakeStateStore{})
	signed := signedFixture(t, command, privateKey)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := agent.Apply(context.Background(), signed)
			errs <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Apply: %v", err)
		}
	}
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrent prepares=%d, want 1", maximum.Load())
	}
}
