package applyagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

type selectiveSnapshotSource struct { failed Target }
func (s selectiveSnapshotSource) CompleteSnapshot(_ context.Context,target Target,operationID string)(DesiredSnapshot,error){
	if target==s.failed{return DesiredSnapshot{},errors.New("test: target unavailable")}
	return NewDesiredSnapshot(target.NodeID,target.ServiceID,operationID,[]DesiredEntry{{CustomerID:"customer-a",OperationID:"operation-a",PayloadKind:"vless",Generation:3,Payload:controlplane.Envelope{KeyVersion:1,Nonce:[]byte("nonce"),Ciphertext:[]byte("cipher")},PayloadSHA256:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}})
}
type resultSender struct { mu sync.Mutex; calls []Target }
func (s *resultSender) Send(_ context.Context,target Target,signed SignedCommand)(DispatchResult,error){
	s.mu.Lock();defer s.mu.Unlock();s.calls=append(s.calls,target)
	var command ApplyCommand;if err:=json.Unmarshal(signed.Command,&command);err!=nil{return DispatchResult{},err}
	entries:=make([]AppliedEntry,0,len(command.Snapshot.Entries));for _,entry:=range command.Snapshot.Entries{entries=append(entries,AppliedEntry{CustomerID:entry.CustomerID,OperationID:entry.OperationID,Generation:entry.Generation,DesiredSHA256:entry.PayloadSHA256,ObservedSHA256:entry.PayloadSHA256})}
	return DispatchResult{SnapshotSHA256:command.Snapshot.SnapshotSHA256,Entries:entries},nil
}
type recordingReceiptSink struct { mu sync.Mutex; calls int }
func (s *recordingReceiptSink) CommitApplied(_ context.Context,_ Target,_ LeaseProof,_ DesiredSnapshot,_ DispatchResult)error{s.mu.Lock();defer s.mu.Unlock();s.calls++;return nil}

func TestUnavailableTargetDoesNotBlockOtherTargets(t *testing.T){
	_,privateKey,_:=ed25519.GenerateKey(rand.Reader);bad:=Target{NodeID:"node-a",ServiceID:"xui"};good:=Target{NodeID:"node-b",ServiceID:"xui"}
	sender:=&resultSender{};sink:=&recordingReceiptSink{}
	dispatcher,err:=NewDispatcher(DispatcherConfig{Source:selectiveSnapshotSource{failed:bad},Leases:&fakeLeaseProvider{},Sender:sender,Receipts:sink,KeyID:"dispatcher-key-1",PrivateKey:privateKey,Clock:func()time.Time{return time.Unix(2_000_000,0)}});if err!=nil{t.Fatal(err)}
	err=dispatcher.DispatchBatch(context.Background(),[]Trigger{{Target:bad,OperationID:"bad-op"},{Target:good,OperationID:"good-op"}})
	if err==nil{t.Fatal("target failure was not reported")}
	if len(sender.calls)!=1||sender.calls[0]!=good{t.Fatalf("successful target calls=%v, want [%v]",sender.calls,good)}
}

func TestDispatcherCommitsPerEntryReceiptsAfterSend(t *testing.T){
	_,privateKey,_:=ed25519.GenerateKey(rand.Reader);target:=Target{NodeID:"node-b",ServiceID:"xui"};sender:=&resultSender{};sink:=&recordingReceiptSink{}
	dispatcher,err:=NewDispatcher(DispatcherConfig{Source:selectiveSnapshotSource{},Leases:&fakeLeaseProvider{},Sender:sender,Receipts:sink,KeyID:"dispatcher-key-1",PrivateKey:privateKey,Clock:func()time.Time{return time.Unix(2_000_000,0)}});if err!=nil{t.Fatal(err)}
	if err:=dispatcher.DispatchBatch(context.Background(),[]Trigger{{Target:target,OperationID:"operation-1"}});err!=nil{t.Fatal(err)}
	if sink.calls!=1{t.Fatalf("receipt commits=%d, want 1",sink.calls)}
}
