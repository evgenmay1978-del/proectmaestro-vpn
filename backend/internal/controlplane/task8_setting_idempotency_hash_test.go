package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func expectedSettingRequestHash(t *testing.T, update SettingUpdate) string {
	t.Helper()
	payload, err := json.Marshal(struct {
		Key                string   `json:"key"`
		ExpectedGeneration int64    `json:"expected_generation"`
		PublicValueJSON    string   `json:"public_value_json"`
		Members            []string `json:"members,omitempty"`
		CommandType        string   `json:"command_type"`
	}{update.Key, update.ExpectedGeneration, update.PublicValueJSON, update.Members, update.CommandType})
	if err != nil {
		t.Fatalf("encode expected setting request: %v", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
