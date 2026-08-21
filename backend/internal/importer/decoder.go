package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var errInvalidSnapshot = errors.New("invalid normalized snapshot JSON")

func DecodeSnapshot(data []byte) (Snapshot, error) {
	var snapshot Snapshot
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, errInvalidSnapshot
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Snapshot{}, errInvalidSnapshot
	}
	if snapshot.FormatVersion != 2 || (snapshot.SnapshotKind != "full" && snapshot.SnapshotKind != "delta") {
		return Snapshot{}, errInvalidSnapshot
	}
	if !validCanonicalSHA256(snapshot.ClusterHMACKeySHA256) {
		return Snapshot{}, errInvalidSnapshot
	}
	if len(snapshot.Trials) > 0 {
		if !validCanonicalSHA256(snapshot.LegacyTrialSaltSHA256) {
			return Snapshot{}, errInvalidSnapshot
		}
	} else if snapshot.LegacyTrialSaltSHA256 != "" {
		return Snapshot{}, errInvalidSnapshot
	}
	if snapshot.CapturedAt.IsZero() || len(snapshot.SourceHashes) == 0 {
		return Snapshot{}, errInvalidSnapshot
	}
	return snapshot, nil
}
