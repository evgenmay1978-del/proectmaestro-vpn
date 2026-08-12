package applyagent

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

type fakePayloadOpener struct {
	mu        sync.Mutex
	scopes    []controlplane.DesiredPayloadScope
	documents map[string]controlplane.DesiredPayloadDocument
	failAt    int
	err       error
}

func (o *fakePayloadOpener) OpenDesiredPayload(scope controlplane.DesiredPayloadScope, _ controlplane.Envelope, _ string) (controlplane.DesiredPayloadDocument, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.scopes = append(o.scopes, scope)
	if o.failAt > 0 && len(o.scopes) == o.failAt {
		return controlplane.DesiredPayloadDocument{}, o.err
	}
	return o.documents[scope.CustomerID], nil
}

func (o *fakePayloadOpener) callCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.scopes)
}

func (o *fakePayloadOpener) recordedScopes() []controlplane.DesiredPayloadScope {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]controlplane.DesiredPayloadScope(nil), o.scopes...)
}

func payloadDocument(kind, body string) controlplane.DesiredPayloadDocument {
	raw := json.RawMessage(body)
	digest := sha256.Sum256(raw)
	return controlplane.DesiredPayloadDocument{
		Version:    controlplane.DesiredPayloadVersion,
		Kind:       kind,
		Body:       raw,
		BodySHA256: hex.EncodeToString(digest[:]),
	}
}

func validFakePayloadOpener() *fakePayloadOpener {
	return &fakePayloadOpener{documents: map[string]controlplane.DesiredPayloadDocument{
		"customer-a": payloadDocument("vless", `{"credential":"synthetic-a"}`),
		"customer-b": payloadDocument("vless", `{"credential":"synthetic-b"}`),
	}}
}

func payloadAgentFixture(t *testing.T, verifier LeaseVerifier, driver Driver, state LocalStateStore, opener PayloadOpener) (*Agent, ApplyCommand, ed25519.PrivateKey) {
	t.Helper()
	command, publicKey, privateKey := protocolFixture(t)
	agent, err := NewAgent(AgentConfig{
		NodeID: "node-a", ServiceID: "xui", NodeIncarnation: 3,
		PublicKeys: map[string]ed25519.PublicKey{"dispatcher-key-1": publicKey},
		Verifier: verifier, Driver: driver, State: state, Opener: opener,
		Clock: func() time.Time { return time.Unix(2_000_030, 0) },
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	return agent, command, privateKey
}

func TestAgentOpensEveryEntryBeforeFirstDriverCall(t *testing.T) {
	opener := validFakePayloadOpener()
	var retainedBodies []json.RawMessage
	driver := &fakeDriver{inspectFn: func(snapshot MaterializedSnapshot) (AppliedState, error) {
		if opener.callCount() != len(snapshot.Entries) {
			t.Fatalf("opener calls at first driver call=%d, want %d", opener.callCount(), len(snapshot.Entries))
		}
		for _, entry := range snapshot.Entries {
			retainedBodies = append(retainedBodies, entry.Body)
			if !json.Valid(entry.Body) || entry.BodySHA256 == entry.DesiredSHA256 {
				t.Fatalf("invalid materialized entry: %#v", entry)
			}
		}
		return AppliedState{SnapshotSHA256: snapshot.SnapshotSHA256, Healthy: true}, nil
	}}
	agent, command, privateKey := payloadAgentFixture(t, &fakeLeaseVerifier{}, driver, &fakeStateStore{}, opener)
	if _, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey)); err != nil {
		t.Fatal(err)
	}
	for _, body := range retainedBodies {
		if strings.Trim(string(body), "\x00") != "" {
			t.Fatal("agent retained materialized plaintext after Apply")
		}
	}
}

func TestAgentPayloadOpenFailureCausesZeroDriverCalls(t *testing.T) {
	secretErr := errors.New("test: synthetic open failure secret-marker")
	opener := validFakePayloadOpener()
	opener.failAt = 2
	opener.err = secretErr
	driver := &fakeDriver{}
	state := &fakeStateStore{}
	agent, command, privateKey := payloadAgentFixture(t, &fakeLeaseVerifier{}, driver, state, opener)
	if _, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey)); !errors.Is(err, ErrPayloadOpen) {
		t.Fatalf("Apply error=%v, want ErrPayloadOpen", err)
	} else if errors.Is(err, secretErr) || strings.Contains(err.Error(), "secret-marker") {
		t.Fatalf("Apply exposed opener error: %v", err)
	}
	if opener.callCount() != 2 || totalDriverCalls(driver) != 0 || state.stores != 0 {
		t.Fatalf("open/driver/store calls=%d/%d/%d, want 2/0/0", opener.callCount(), totalDriverCalls(driver), state.stores)
	}
}

func TestAgentRejectsPayloadKindBodyOrDigestMismatchBeforeDriver(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakePayloadOpener)
	}{
		{name: "kind", mutate: func(opener *fakePayloadOpener) { document := opener.documents["customer-a"]; document.Kind = "hysteria2"; opener.documents["customer-a"] = document }},
		{name: "body", mutate: func(opener *fakePayloadOpener) { document := opener.documents["customer-a"]; document.Body = json.RawMessage(`{"credential":"changed"}`); opener.documents["customer-a"] = document }},
		{name: "digest", mutate: func(opener *fakePayloadOpener) { document := opener.documents["customer-a"]; document.BodySHA256 = strings.Repeat("f", 64); opener.documents["customer-a"] = document }},
	} {
		t.Run(test.name, func(t *testing.T) {
			opener := validFakePayloadOpener()
			test.mutate(opener)
			driver := &fakeDriver{}
			state := &fakeStateStore{}
			agent, command, privateKey := payloadAgentFixture(t, &fakeLeaseVerifier{}, driver, state, opener)
			if _, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey)); !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("Apply error=%v, want ErrInvalidCommand", err)
			}
			if totalDriverCalls(driver) != 0 || state.stores != 0 {
				t.Fatalf("invalid document reached driver/store: %d/%d", totalDriverCalls(driver), state.stores)
			}
		})
	}
}

func TestAgentDerivesExactAADScopeForEveryEntry(t *testing.T) {
	opener := validFakePayloadOpener()
	agent, command, privateKey := payloadAgentFixture(t, &fakeLeaseVerifier{}, &fakeDriver{}, &fakeStateStore{}, opener)
	if _, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey)); err != nil {
		t.Fatal(err)
	}
	scopes := opener.recordedScopes()
	if len(scopes) != len(command.Snapshot.Entries) {
		t.Fatalf("scope count=%d, want %d", len(scopes), len(command.Snapshot.Entries))
	}
	for index, entry := range command.Snapshot.Entries {
		want := controlplane.DesiredPayloadScope{
			NodeID: command.Snapshot.NodeID, ServiceID: command.Snapshot.ServiceID,
			CustomerID: entry.CustomerID, Generation: entry.Generation,
			OperationID: entry.OperationID, Tombstone: entry.Tombstone, PayloadKind: entry.PayloadKind,
		}
		if scopes[index] != want {
			t.Fatalf("scope[%d]=%#v, want %#v", index, scopes[index], want)
		}
	}
}

type materializedOnlyDriver struct{}

func (materializedOnlyDriver) Inspect(context.Context, MaterializedSnapshot) (AppliedState, error) {
	return AppliedState{}, nil
}
func (materializedOnlyDriver) Prepare(context.Context, MaterializedSnapshot) (PreparedChange, error) {
	return PreparedChange{}, nil
}
func (materializedOnlyDriver) Commit(context.Context, PreparedChange) (AppliedState, error) {
	return AppliedState{}, nil
}
func (materializedOnlyDriver) Rollback(context.Context, PreparedChange) error { return nil }

func TestDriverInterfaceAcceptsOnlyMaterializedSnapshot(t *testing.T) {
	var _ Driver = materializedOnlyDriver{}
}

func TestDesiredChangeDuringPrepareRejectsOldSnapshotBeforeSwap(t *testing.T) {
	changedDesired := errors.New("test: desired snapshot changed")
	verifier := &fakeLeaseVerifier{fn: func(call int, _ string, _, _, _ int64) error {
		if call == 2 {
			return changedDesired
		}
		return nil
	}}
	driver := &fakeDriver{}
	state := &fakeStateStore{}
	opener := validFakePayloadOpener()
	agent, command, privateKey := payloadAgentFixture(t, verifier, driver, state, opener)
	if _, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey)); !errors.Is(err, changedDesired) {
		t.Fatalf("Apply error=%v, want changed desired", err)
	}
	if driver.prepareCalls.Load() != 1 || driver.commitCalls.Load() != 0 || driver.rollbackCalls.Load() != 1 || state.stores != 0 {
		t.Fatalf("prepare/commit/rollback/store=%d/%d/%d/%d, want 1/0/1/0", driver.prepareCalls.Load(), driver.commitCalls.Load(), driver.rollbackCalls.Load(), state.stores)
	}
}

func TestSecondStrongCheckUsesOriginalSignedSnapshotDigest(t *testing.T) {
	var digests []string
	verifier := &fakeLeaseVerifier{fn: func(_ int, digest string, _, _, _ int64) error {
		digests = append(digests, digest)
		return nil
	}}
	agent, command, privateKey := payloadAgentFixture(t, verifier, &fakeDriver{}, &fakeStateStore{}, validFakePayloadOpener())
	if _, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey)); err != nil {
		t.Fatal(err)
	}
	if len(digests) != 2 || digests[0] != command.Snapshot.SnapshotSHA256 || digests[1] != command.Snapshot.SnapshotSHA256 {
		t.Fatalf("strong-check digests=%v, want original signed digest twice", digests)
	}
}

func TestSecondStrongCheckFailureRollsBackWithoutMarkerOrReceipt(t *testing.T) {
	wantErr := errors.New("test: second strong check failed")
	verifier := &fakeLeaseVerifier{fn: func(call int, _ string, _, _, _ int64) error {
		if call == 2 {
			return wantErr
		}
		return nil
	}}
	driver := &fakeDriver{}
	state := &fakeStateStore{}
	agent, command, privateKey := payloadAgentFixture(t, verifier, driver, state, validFakePayloadOpener())
	result, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Apply error=%v, want %v", err, wantErr)
	}
	if result.SnapshotSHA256 != "" || len(result.Entries) != 0 || driver.rollbackCalls.Load() != 1 || driver.commitCalls.Load() != 0 || state.stores != 0 {
		t.Fatalf("result=%#v rollback/commit/store=%d/%d/%d", result, driver.rollbackCalls.Load(), driver.commitCalls.Load(), state.stores)
	}
}

func TestAgentRejectsUnsignedPayloadKindMutationBeforeOpenOrDriver(t *testing.T) {
	opener := validFakePayloadOpener()
	driver := &fakeDriver{}
	agent, command, privateKey := payloadAgentFixture(t, &fakeLeaseVerifier{}, driver, &fakeStateStore{}, opener)
	signed := signedFixture(t, command, privateKey)
	var mutated ApplyCommand
	if err := json.Unmarshal(signed.Command, &mutated); err != nil {
		t.Fatal(err)
	}
	mutated.Snapshot.Entries[0].PayloadKind = "hysteria2"
	mutatedBytes, err := json.Marshal(mutated)
	if err != nil {
		t.Fatal(err)
	}
	signed.Command = mutatedBytes
	if _, err := agent.Apply(context.Background(), signed); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("Apply error=%v, want ErrInvalidCommand", err)
	}
	if opener.callCount() != 0 || totalDriverCalls(driver) != 0 {
		t.Fatalf("unsigned kind mutation reached opener/driver: %d/%d", opener.callCount(), totalDriverCalls(driver))
	}
}
