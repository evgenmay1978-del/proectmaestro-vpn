package applyagent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func protocolFixture(t *testing.T) (ApplyCommand, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	snapshot, err := NewDesiredSnapshot("node-a", "xui", "operation-trigger", []DesiredEntry{
		{
			CustomerID: "customer-b", OperationID: "operation-b", PayloadKind: "vless", Generation: 4,
			Payload: controlplane.Envelope{KeyVersion: 2, Nonce: []byte("nonce-b"), Ciphertext: []byte("cipher-b")},
			PayloadSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
		{
			CustomerID: "customer-a", OperationID: "operation-a", PayloadKind: "vless", Generation: 3,
			Payload: controlplane.Envelope{KeyVersion: 2, Nonce: []byte("nonce-a"), Ciphertext: []byte("cipher-a")},
			PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	})
	if err != nil {
		t.Fatalf("NewDesiredSnapshot: %v", err)
	}
	return ApplyCommand{
		Version: 1, ClusterEpoch: 7, NodeIncarnation: 3, LeaseFence: 11,
		NodeID: "node-a", ServiceID: "xui", HolderID: "dispatcher-a",
		Snapshot: snapshot, IssuedAtUnix: 2_000_000, NotAfterUnix: 2_000_060,
	}, publicKey, privateKey
}

func TestCanonicalCommandSignatureRejectsFieldMutation(t *testing.T) {
	command, publicKey, privateKey := protocolFixture(t)
	signed, err := SignCommand(command, "dispatcher-key-1", privateKey)
	if err != nil {
		t.Fatalf("SignCommand: %v", err)
	}
	if _, err := VerifySignedCommand(signed, map[string]ed25519.PublicKey{
		"dispatcher-key-1": publicKey,
	}, time.Unix(2_000_030, 0)); err != nil {
		t.Fatalf("VerifySignedCommand valid command: %v", err)
	}

	var mutated ApplyCommand
	if err := json.Unmarshal(signed.Command, &mutated); err != nil {
		t.Fatalf("Unmarshal signed command: %v", err)
	}
	mutated.NodeID = "node-b"
	mutatedBytes, err := json.Marshal(mutated)
	if err != nil {
		t.Fatalf("Marshal mutated command: %v", err)
	}
	signed.Command = mutatedBytes
	if _, err := VerifySignedCommand(signed, map[string]ed25519.PublicKey{
		"dispatcher-key-1": publicKey,
	}, time.Unix(2_000_030, 0)); err == nil {
		t.Fatal("field mutation retained a valid command signature")
	}
}

func TestSnapshotCanonicalizationSortsEntriesAndBindsAggregateHash(t *testing.T) {
	command, _, _ := protocolFixture(t)
	if got := []string{command.Snapshot.Entries[0].CustomerID, command.Snapshot.Entries[1].CustomerID};
		got[0] != "customer-a" || got[1] != "customer-b" {
		t.Fatalf("canonical entry order=%v", got)
	}
	if err := ValidateDesiredSnapshot(command.Snapshot); err != nil {
		t.Fatalf("ValidateDesiredSnapshot valid snapshot: %v", err)
	}
	command.Snapshot.Entries[0].Generation++
	if err := ValidateDesiredSnapshot(command.Snapshot); err == nil {
		t.Fatal("entry mutation retained aggregate snapshot hash")
	}
}

func TestSignedCommandRejectsUnknownKeyAndInvalidLifetime(t *testing.T) {
	command, publicKey, privateKey := protocolFixture(t)
	signed, err := SignCommand(command, "dispatcher-key-1", privateKey)
	if err != nil {
		t.Fatalf("SignCommand: %v", err)
	}
	if _, err := VerifySignedCommand(signed, map[string]ed25519.PublicKey{
		"other-key": publicKey,
	}, time.Unix(2_000_030, 0)); err == nil {
		t.Fatal("unknown signing key was accepted")
	}
	if _, err := VerifySignedCommand(signed, map[string]ed25519.PublicKey{
		"dispatcher-key-1": publicKey,
	}, time.Unix(2_000_061, 0)); err == nil {
		t.Fatal("expired apply command was accepted")
	}

	command.NotAfterUnix = command.IssuedAtUnix + 61
	signed, err = SignCommand(command, "dispatcher-key-1", privateKey)
	if err == nil {
		if _, verifyErr := VerifySignedCommand(signed, map[string]ed25519.PublicKey{
			"dispatcher-key-1": publicKey,
		}, time.Unix(2_000_030, 0)); verifyErr == nil {
			t.Fatal("command lifetime over 60 seconds was accepted")
		}
	}
}
