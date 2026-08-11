package importer

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

func Digest(plan ImportPlan) string {
	copy := plan
	copy.PlanDigest = ""
	copy.Blockers = nil
	encoded, err := json.Marshal(copy)
	if err != nil {
		return ""
	}
	return sha256Hex(encoded)
}

func digestSnapshot(snapshot Snapshot) string {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return ""
	}
	return sha256Hex(encoded)
}

func digestBatch(operations []ApplyOperation) string {
	encoded, err := json.Marshal(operations)
	if err != nil {
		return ""
	}
	return sha256Hex(encoded)
}

func deterministicID(namespace, entity, sourceKey string) string {
	return sha256Hex([]byte(namespace + "\x00" + entity + "\x00" + sourceKey))
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("%x", sum[:])
}
