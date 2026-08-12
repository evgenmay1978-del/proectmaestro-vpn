package applyagent

import (
	"context"
	"crypto/ed25519"
	"sync"
	"testing"
	"time"
)

type fakeSnapshotSource struct { mu sync.Mutex; calls map[Target]int; snapshot DesiredSnapshot }
func (s *fakeSnapshotSource) CompleteSnapshot(_ context.Context, target Target, _ string) (DesiredSnapshot, error) { s.mu.Lock(); defer s.mu.Unlock(); s.calls[target]++; return s.snapshot, nil }
type fakeLeaseProvider struct { calls int }
func (p *fakeLeaseProvider) Acquire(_ context.Context, target Target, snapshotSHA256 string) (LeaseProof, error) { p.calls++; return LeaseProof{ClusterEpoch:7,NodeIncarnation:3,LeaseFence:11,HolderID:"dispatcher-a"},nil }
type fakeCommandSender struct { mu sync.Mutex; calls map[Target]int }
func (s *fakeCommandSender) Send(_ context.Context, target Target, _ SignedCommand) error { s.mu.Lock(); defer s.mu.Unlock(); s.calls[target]++; return nil }

func dispatcherFixture(t *testing.T) (*Dispatcher,*fakeSnapshotSource,*fakeLeaseProvider,*fakeCommandSender) {
	t.Helper()
	command, _, privateKey := protocolFixture(t)
	source:=&fakeSnapshotSource{calls:map[Target]int{},snapshot:command.Snapshot}
	leases:=&fakeLeaseProvider{}
	sender:=&fakeCommandSender{calls:map[Target]int{}}
	dispatcher,err:=NewDispatcher(DispatcherConfig{Source:source,Leases:leases,Sender:sender,KeyID:"dispatcher-key-1",PrivateKey:ed25519.PrivateKey(privateKey),Clock:func()time.Time{return time.Unix(2_000_000,0)}})
	if err!=nil { t.Fatalf("NewDispatcher: %v",err) }
	return dispatcher,source,leases,sender
}

func TestTwoS2EventsCoalesceIntoOneFullSnapshotCommand(t *testing.T) {
	dispatcher,source,leases,sender:=dispatcherFixture(t)
	target:=Target{NodeID:"node-a",ServiceID:"xui"}
	err:=dispatcher.DispatchBatch(context.Background(),[]Trigger{{Target:target,OperationID:"operation-1"},{Target:target,OperationID:"operation-2"}})
	if err!=nil { t.Fatalf("DispatchBatch: %v",err) }
	if source.calls[target]!=1 || leases.calls!=1 || sender.calls[target]!=1 { t.Fatalf("source/lease/send=%d/%d/%d, want 1/1/1",source.calls[target],leases.calls,sender.calls[target]) }
}

func TestFullSweepAppliesDriftWithoutOutboxRow(t *testing.T) {
	dispatcher,source,_,sender:=dispatcherFixture(t)
	target:=Target{NodeID:"node-a",ServiceID:"xui"}
	if err:=dispatcher.Sweep(context.Background(),[]Target{target}); err!=nil { t.Fatalf("Sweep: %v",err) }
	if source.calls[target]!=1 || sender.calls[target]!=1 { t.Fatalf("source/send=%d/%d, want 1/1",source.calls[target],sender.calls[target]) }
}
