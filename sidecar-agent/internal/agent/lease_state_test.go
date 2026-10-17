package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLeaseAckRequiresExactProofAndIsAtomicIdempotent(t *testing.T) {
	f := newLeaseFixture(t)
	page, err := f.r.LeaseReceipts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	final := page.FinalReceipts[0]
	bad := LeaseReceiptAck{Schema: 2, Receipts: []LeaseReceiptAckItem{{final.ReceiptID, strings.Repeat("e", 64)}}}
	if err := f.r.AckLeaseReceipts(context.Background(), bad); !errors.Is(err, ErrConflict) {
		t.Fatal("ACK accepted a different final proof")
	}
	after, _ := f.r.LeaseReceipts(context.Background())
	if len(after.FinalReceipts) != 1 {
		t.Fatal("failed ACK deleted final counters")
	}
	ack := LeaseReceiptAck{Schema: 2, Receipts: []LeaseReceiptAckItem{{final.ReceiptID, final.ProofSHA256}}}
	if f.r.AckLeaseReceipts(context.Background(), ack) != nil || f.r.AckLeaseReceipts(context.Background(), ack) != nil {
		t.Fatal("exact retry ACK failed")
	}
	state, err := f.store.loadLeaseState()
	if err != nil || len(state.FinalReceipts) != 0 || state.Users[leaseUserKey(f.boot, "wl:one:exit-s1")].Generation != 1 {
		t.Fatal("ACK removed generation history or retained acknowledged tail")
	}
}

func TestLeaseStateRejectsTamperAndPreservesPrivateAtomicFormat(t *testing.T) {
	f := newLeaseFixture(t)
	path := filepath.Join(f.store.directory, leaseStateFile)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("durable lease state is not private")
	}
	state, err := f.store.loadLeaseState()
	if err != nil {
		t.Fatal(err)
	}
	for id, final := range state.FinalReceipts {
		final.ProofSHA256 = strings.Repeat("d", 64)
		state.FinalReceipts[id] = final
	}
	raw, err := json.Marshal(state)
	if err != nil || os.WriteFile(path, raw, 0o600) != nil {
		t.Fatal("synthetic corruption failed")
	}
	if _, err := f.store.loadLeaseState(); err == nil {
		t.Fatal("tampered final proof loaded")
	}
	before := len(f.handler.controls)
	if _, err := f.r.Refresh(context.Background()); err == nil || len(f.handler.controls) != before {
		t.Fatal("corrupt journal permitted runtime mutation")
	}
}

func TestAcknowledgedOldBootHistoryPrunesOnlyProvenDeniedUsers(t *testing.T) {
	f := newLeaseFixture(t)
	state, err := f.store.loadLeaseState()
	if err != nil {
		t.Fatal(err)
	}
	key := leaseUserKey(f.boot, "wl:one:exit-s1")
	pruneAcknowledgedLeaseHistory(&state, "new-physical-boot")
	if _, ok := state.Users[key]; !ok {
		t.Fatal("unacknowledged old-boot final history was pruned")
	}
	state.FinalReceipts = map[string]FinalLeaseReceipt{}
	user := state.Users[key]
	user.Phase = "unknown"
	state.Users[key] = user
	pruneAcknowledgedLeaseHistory(&state, "new-physical-boot")
	if _, ok := state.Users[key]; !ok {
		t.Fatal("unknown old permission was discarded")
	}
	user.Phase = "fenced"
	state.Users[key] = user
	pruneAcknowledgedLeaseHistory(&state, f.boot)
	if _, ok := state.Users[key]; !ok {
		t.Fatal("current physical sequence was discarded")
	}
	pruneAcknowledgedLeaseHistory(&state, "new-physical-boot")
	if _, ok := state.Users[key]; ok {
		t.Fatal("acknowledged denied old-boot history was retained forever")
	}
}

func TestLeaseBacklogNeverPrunesUnacknowledgedReceipts(t *testing.T) {
	f := newLeaseFixture(t)
	state, err := f.store.loadLeaseState()
	if err != nil {
		t.Fatal(err)
	}
	var sample FinalLeaseReceipt
	for _, final := range state.FinalReceipts {
		sample = final
	}
	state.FinalReceipts = map[string]FinalLeaseReceipt{}
	for i := 1; i <= maxFinalReceipts; i++ {
		final := sample
		final.Control.Generation = uint64(i)
		final.Receipt.Generation = uint64(i)
		final.ReceiptID = leaseOperationID(final.LeaseBinding, final.Control)
		final.ProofSHA256 = leaseHash(final.LeaseReceiptProof)
		state.FinalReceipts[final.ReceiptID] = final
	}
	if err := f.store.saveLeaseState(state); err != nil {
		t.Fatal(err)
	}
	page, err := f.r.LeaseReceipts(context.Background())
	if err != nil || len(page.FinalReceipts) != 32 || !page.HasMoreFinalReceipts {
		t.Fatal("bounded final receipt pagination failed")
	}
	for index, final := range page.FinalReceipts {
		if final.Control.Generation != uint64(index+1) {
			t.Fatal("final receipts reordered cumulative counters by hash")
		}
	}
	command := &pendingLeaseCommand{}
	if err := reserveLeaseControl(&state, command, bindingForDesired(f.desired), "wl:one:exit-s1", f.boot, strings.Repeat("c", 64), strings.Repeat("a", 64), "fence", 0); !errors.Is(err, ErrLeaseCapacity) {
		t.Fatal("full final backlog accepted another drain slot")
	}
	stored, err := f.store.loadLeaseState()
	if err != nil || len(stored.FinalReceipts) != maxFinalReceipts {
		t.Fatal("capacity handling deleted unacknowledged accounting")
	}
}
