package applyagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const plaintextSecretMarker = "plaintext-secret-marker"

type retainingPayloadOpener struct {
	returnedBodies [][]byte
	returnError    error
	invalidDigest  bool
}

func (o *retainingPayloadOpener) OpenDesiredPayload(scope controlplane.DesiredPayloadScope, _ controlplane.Envelope, _ string) (controlplane.DesiredPayloadDocument, error) {
	document := payloadDocument(scope.PayloadKind, `{"credential":"`+plaintextSecretMarker+`"}`)
	if o.invalidDigest {
		document.BodySHA256 = strings.Repeat("f", 64)
	}
	o.returnedBodies = append(o.returnedBodies, document.Body)
	return document, o.returnError
}

func retainMaterializedBodies(destination *[][]byte, snapshot MaterializedSnapshot) {
	for _, entry := range snapshot.Entries {
		*destination = append(*destination, entry.Body)
	}
}

func requireZeroedBuffers(t *testing.T, label string, buffers [][]byte) {
	t.Helper()
	if len(buffers) == 0 {
		t.Fatalf("%s retained no buffers", label)
	}
	for index, buffer := range buffers {
		for _, value := range buffer {
			if value != 0 {
				t.Fatalf("%s buffer %d retained plaintext: %q", label, index, string(buffer))
			}
		}
	}
}

func TestAgentWipesOwnedPlaintextOnEveryExit(t *testing.T) {
	markerErr := errors.New("test: marker store failed")
	secondStrongErr := errors.New("test: second strong check failed")
	secretErr := errors.New("driver boundary " + plaintextSecretMarker)

	tests := []struct {
		name          string
		configure     func(*retainingPayloadOpener, *fakeLeaseVerifier, *fakeDriver, *fakeStateStore, *[][]byte)
		wantErr       error
		forbidRaw     error
		wantDriverBuf bool
	}{
		{
			name: "open error with document",
			configure: func(opener *retainingPayloadOpener, _ *fakeLeaseVerifier, _ *fakeDriver, _ *fakeStateStore, _ *[][]byte) {
				opener.returnError = secretErr
			},
			wantErr: ErrPayloadOpen, forbidRaw: secretErr,
		},
		{
			name: "document validation",
			configure: func(opener *retainingPayloadOpener, _ *fakeLeaseVerifier, _ *fakeDriver, _ *fakeStateStore, _ *[][]byte) {
				opener.invalidDigest = true
			},
			wantErr: ErrInvalidCommand,
		},
		{
			name: "inspect error",
			configure: func(_ *retainingPayloadOpener, _ *fakeLeaseVerifier, driver *fakeDriver, _ *fakeStateStore, retained *[][]byte) {
				driver.inspectFn = func(snapshot MaterializedSnapshot) (AppliedState, error) {
					retainMaterializedBodies(retained, snapshot)
					return AppliedState{}, secretErr
				}
			},
			wantErr: ErrDriverInspect, forbidRaw: secretErr, wantDriverBuf: true,
		},
		{
			name: "prepare error",
			configure: func(_ *retainingPayloadOpener, _ *fakeLeaseVerifier, driver *fakeDriver, _ *fakeStateStore, retained *[][]byte) {
				driver.prepareFn = func(snapshot MaterializedSnapshot) (PreparedChange, error) {
					retainMaterializedBodies(retained, snapshot)
					return PreparedChange{}, secretErr
				}
			},
			wantErr: ErrDriverPrepare, forbidRaw: secretErr, wantDriverBuf: true,
		},
		{
			name: "second strong check",
			configure: func(_ *retainingPayloadOpener, verifier *fakeLeaseVerifier, driver *fakeDriver, _ *fakeStateStore, retained *[][]byte) {
				verifier.fn = func(call int, _ string, _, _, _ int64) error {
					if call == 2 {
						return secondStrongErr
					}
					return nil
				}
				driver.prepareFn = func(snapshot MaterializedSnapshot) (PreparedChange, error) {
					retainMaterializedBodies(retained, snapshot)
					return PreparedChange{SnapshotSHA256: snapshot.SnapshotSHA256}, nil
				}
			},
			wantErr: secondStrongErr, wantDriverBuf: true,
		},
		{
			name: "commit error",
			configure: func(_ *retainingPayloadOpener, _ *fakeLeaseVerifier, driver *fakeDriver, _ *fakeStateStore, retained *[][]byte) {
				driver.prepareFn = func(snapshot MaterializedSnapshot) (PreparedChange, error) {
					retainMaterializedBodies(retained, snapshot)
					return PreparedChange{SnapshotSHA256: snapshot.SnapshotSHA256}, nil
				}
				driver.commitFn = func(PreparedChange) (AppliedState, error) {
					return AppliedState{}, secretErr
				}
			},
			wantErr: ErrDriverCommit, forbidRaw: secretErr, wantDriverBuf: true,
		},
		{
			name: "marker failure",
			configure: func(_ *retainingPayloadOpener, _ *fakeLeaseVerifier, driver *fakeDriver, state *fakeStateStore, retained *[][]byte) {
				driver.prepareFn = func(snapshot MaterializedSnapshot) (PreparedChange, error) {
					retainMaterializedBodies(retained, snapshot)
					return PreparedChange{SnapshotSHA256: snapshot.SnapshotSHA256}, nil
				}
				state.storeErr = markerErr
			},
			wantErr: markerErr, wantDriverBuf: true,
		},
		{
			name: "success",
			configure: func(_ *retainingPayloadOpener, _ *fakeLeaseVerifier, driver *fakeDriver, _ *fakeStateStore, retained *[][]byte) {
				driver.prepareFn = func(snapshot MaterializedSnapshot) (PreparedChange, error) {
					retainMaterializedBodies(retained, snapshot)
					return PreparedChange{SnapshotSHA256: snapshot.SnapshotSHA256}, nil
				}
			},
			wantDriverBuf: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opener := &retainingPayloadOpener{}
			verifier := &fakeLeaseVerifier{}
			driver := &fakeDriver{}
			state := &fakeStateStore{}
			var retainedDriverBodies [][]byte
			test.configure(opener, verifier, driver, state, &retainedDriverBodies)
			agent, command, privateKey := payloadAgentFixture(t, verifier, driver, state, opener)
			_, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey))
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Apply error=%v, want %v", err, test.wantErr)
			}
			if test.forbidRaw != nil && (errors.Is(err, test.forbidRaw) || strings.Contains(err.Error(), plaintextSecretMarker)) {
				t.Fatalf("Apply exposed raw secret error: %v", err)
			}
			requireZeroedBuffers(t, "opener", opener.returnedBodies)
			if test.wantDriverBuf {
				requireZeroedBuffers(t, "driver", retainedDriverBodies)
			}
		})
	}
}

func TestAgentRedactsEveryPlaintextAwareDriverError(t *testing.T) {
	secretErr := errors.New("driver returned " + plaintextSecretMarker)
	tests := []struct {
		name      string
		configure func(*fakeDriver)
		want      error
	}{
		{name: "inspect", configure: func(driver *fakeDriver) { driver.inspectFn = func(MaterializedSnapshot) (AppliedState, error) { return AppliedState{}, secretErr } }, want: ErrDriverInspect},
		{name: "prepare", configure: func(driver *fakeDriver) { driver.prepareFn = func(MaterializedSnapshot) (PreparedChange, error) { return PreparedChange{}, secretErr } }, want: ErrDriverPrepare},
		{name: "commit", configure: func(driver *fakeDriver) { driver.commitFn = func(PreparedChange) (AppliedState, error) { return AppliedState{}, secretErr } }, want: ErrDriverCommit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			driver := &fakeDriver{}
			test.configure(driver)
			agent, command, privateKey := payloadAgentFixture(t, &fakeLeaseVerifier{}, driver, &fakeStateStore{}, &retainingPayloadOpener{})
			_, err := agent.Apply(context.Background(), signedFixture(t, command, privateKey))
			if !errors.Is(err, test.want) || errors.Is(err, secretErr) || strings.Contains(err.Error(), plaintextSecretMarker) {
				t.Fatalf("Apply error=%v, want safe %v without marker", err, test.want)
			}
		})
	}
}

func TestRetainingOpenerBodyIsValidJSON(t *testing.T) {
	opener := &retainingPayloadOpener{}
	document, err := opener.OpenDesiredPayload(controlplane.DesiredPayloadScope{PayloadKind: "vless"}, controlplane.Envelope{}, "")
	if err != nil || !json.Valid(document.Body) {
		t.Fatalf("test opener document invalid: %v", err)
	}
}
