package applyagent

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

type PayloadOpener interface {
	OpenDesiredPayload(controlplane.DesiredPayloadScope, controlplane.Envelope, string) (controlplane.DesiredPayloadDocument, error)
}

type MaterializedEntry struct {
	CustomerID, OperationID, PayloadKind string
	Generation                           int64
	Tombstone                            bool
	Body                                 json.RawMessage
	DesiredSHA256, BodySHA256             string
}

type MaterializedSnapshot struct {
	NodeID, ServiceID, TriggerOperationID, SnapshotSHA256 string
	Entries                                               []MaterializedEntry
}

func materializeSnapshot(opener PayloadOpener, encrypted DesiredSnapshot) (MaterializedSnapshot, error) {
	if opener == nil {
		return MaterializedSnapshot{}, ErrInvalidCommand
	}
	materialized := MaterializedSnapshot{
		NodeID: encrypted.NodeID, ServiceID: encrypted.ServiceID,
		TriggerOperationID: encrypted.TriggerOperationID, SnapshotSHA256: encrypted.SnapshotSHA256,
		Entries: make([]MaterializedEntry, 0, len(encrypted.Entries)),
	}
	for _, entry := range encrypted.Entries {
		scope := controlplane.DesiredPayloadScope{
			NodeID: encrypted.NodeID, ServiceID: encrypted.ServiceID,
			CustomerID: entry.CustomerID, OperationID: entry.OperationID,
			PayloadKind: entry.PayloadKind, Generation: entry.Generation, Tombstone: entry.Tombstone,
		}
		document, err := opener.OpenDesiredPayload(scope, entry.Payload, entry.PayloadSHA256)
		if err != nil {
			wipeBytes(document.Body)
			wipeMaterializedSnapshot(&materialized)
			return MaterializedSnapshot{}, ErrPayloadOpen
		}
		if !validMaterializedDocument(scope, document) {
			wipeBytes(document.Body)
			wipeMaterializedSnapshot(&materialized)
			return MaterializedSnapshot{}, ErrInvalidCommand
		}
		body := append(json.RawMessage(nil), document.Body...)
		wipeBytes(document.Body)
		materialized.Entries = append(materialized.Entries, MaterializedEntry{
			CustomerID: entry.CustomerID, OperationID: entry.OperationID, PayloadKind: entry.PayloadKind,
			Generation: entry.Generation, Tombstone: entry.Tombstone,
			Body: body,
			DesiredSHA256: entry.PayloadSHA256, BodySHA256: document.BodySHA256,
		})
	}
	return materialized, nil
}

func validMaterializedDocument(scope controlplane.DesiredPayloadScope, document controlplane.DesiredPayloadDocument) bool {
	if document.Version != controlplane.DesiredPayloadVersion || document.Kind != scope.PayloadKind || !validSHA256(document.BodySHA256) {
		return false
	}
	digest := sha256.Sum256(document.Body)
	actual := hex.EncodeToString(digest[:])
	if subtle.ConstantTimeCompare([]byte(actual), []byte(document.BodySHA256)) != 1 {
		return false
	}
	if len(document.Body) == 0 {
		return document.Body == nil && !scope.Tombstone
	}
	if !json.Valid(document.Body) {
		return false
	}
	return !scope.Tombstone || bytes.Equal(document.Body, []byte(`{"tombstone":true}`))
}

func wipeMaterializedSnapshot(snapshot *MaterializedSnapshot) {
	if snapshot == nil {
		return
	}
	for index := range snapshot.Entries {
		wipeBytes(snapshot.Entries[index].Body)
		snapshot.Entries[index].Body = nil
	}
	snapshot.Entries = nil
}

func wipeBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
