package applyagent

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sort"
	"strings"
	"time"
)

type Target struct { NodeID string; ServiceID string }
type Trigger struct { Target Target; OperationID string }
type LeaseProof struct { ClusterEpoch, NodeIncarnation, LeaseFence int64; HolderID string }
type SnapshotSource interface { CompleteSnapshot(context.Context, Target, string) (DesiredSnapshot, error) }
type LeaseProvider interface { Acquire(context.Context, Target, string) (LeaseProof, error) }
type AppliedEntry struct {
	CustomerID     string `json:"customer_id"`
	OperationID    string `json:"operation_id"`
	Generation     int64  `json:"generation"`
	DesiredSHA256  string `json:"desired_sha256"`
	ObservedSHA256 string `json:"observed_sha256"`
}
type DispatchResult struct {
	SnapshotSHA256 string         `json:"snapshot_sha256"`
	Entries        []AppliedEntry `json:"entries"`
}
type CommandSender interface { Send(context.Context, Target, SignedCommand) (DispatchResult,error) }
type ReceiptSink interface { CommitApplied(context.Context, Target, LeaseProof, DesiredSnapshot, DispatchResult) error }
type DispatcherConfig struct { Source SnapshotSource; Leases LeaseProvider; Sender CommandSender; Receipts ReceiptSink; KeyID string; PrivateKey ed25519.PrivateKey; Clock func() time.Time }
type Dispatcher struct { cfg DispatcherConfig }

func NewDispatcher(cfg DispatcherConfig) (*Dispatcher,error) {
	if cfg.Source==nil || cfg.Leases==nil || cfg.Sender==nil || cfg.Receipts==nil || strings.TrimSpace(cfg.KeyID)=="" || len(cfg.PrivateKey)!=ed25519.PrivateKeySize || cfg.Clock==nil { return nil,ErrInvalidCommand }
	return &Dispatcher{cfg:cfg},nil
}

func (d *Dispatcher) DispatchBatch(ctx context.Context,triggers []Trigger) error {
	coalesced:=map[Target]string{}
	for _,trigger:=range triggers { if !validTarget(trigger.Target)||strings.TrimSpace(trigger.OperationID)=="" { return ErrInvalidCommand }; coalesced[trigger.Target]=trigger.OperationID }
	targets:=make([]Target,0,len(coalesced)); for target:=range coalesced { targets=append(targets,target) }
	sort.Slice(targets,func(i,j int)bool { if targets[i].NodeID==targets[j].NodeID{return targets[i].ServiceID<targets[j].ServiceID};return targets[i].NodeID<targets[j].NodeID })
	var joined error
	for _,target:=range targets { if err:=d.dispatch(ctx,target,coalesced[target]);err!=nil{joined=errors.Join(joined,err)} }
	return joined
}

func (d *Dispatcher) Sweep(ctx context.Context,targets []Target) error {
	triggers:=make([]Trigger,0,len(targets)); for _,target:=range targets { triggers=append(triggers,Trigger{Target:target,OperationID:"sweep:"+target.NodeID+":"+target.ServiceID}) }
	return d.DispatchBatch(ctx,triggers)
}

func (d *Dispatcher) dispatch(ctx context.Context,target Target,operationID string) error {
	snapshot,err:=d.cfg.Source.CompleteSnapshot(ctx,target,operationID);if err!=nil{return err}
	if snapshot.NodeID!=target.NodeID||snapshot.ServiceID!=target.ServiceID { return ErrInvalidCommand }
	if err:=ValidateDesiredSnapshot(snapshot);err!=nil{return err}
	lease,err:=d.cfg.Leases.Acquire(ctx,target,snapshot.SnapshotSHA256);if err!=nil{return err}
	now:=d.cfg.Clock().Unix()
	command:=ApplyCommand{Version:ProtocolVersion,ClusterEpoch:lease.ClusterEpoch,NodeIncarnation:lease.NodeIncarnation,LeaseFence:lease.LeaseFence,NodeID:target.NodeID,ServiceID:target.ServiceID,HolderID:lease.HolderID,Snapshot:snapshot,IssuedAtUnix:now,NotAfterUnix:now+int64(MaxCommandLifetime/time.Second)}
	signed,err:=SignCommand(command,d.cfg.KeyID,d.cfg.PrivateKey);if err!=nil{return err}
	result,err:=d.cfg.Sender.Send(ctx,target,signed);if err!=nil{return err}
	if err:=validateDispatchResult(snapshot,result);err!=nil{return err}
	return d.cfg.Receipts.CommitApplied(ctx,target,lease,snapshot,result)
}
func validateDispatchResult(snapshot DesiredSnapshot,result DispatchResult)error{
	if result.SnapshotSHA256!=snapshot.SnapshotSHA256||len(result.Entries)!=len(snapshot.Entries){return ErrInvalidCommand}
	byCustomer:=map[string]AppliedEntry{};for _,entry:=range result.Entries{if _,exists:=byCustomer[entry.CustomerID];exists{return ErrInvalidCommand};byCustomer[entry.CustomerID]=entry}
	for _,desired:=range snapshot.Entries{applied,ok:=byCustomer[desired.CustomerID];if !ok||applied.OperationID!=desired.OperationID||applied.Generation!=desired.Generation||applied.DesiredSHA256!=desired.PayloadSHA256||!validSHA256(applied.ObservedSHA256){return ErrInvalidCommand}}
	return nil
}

func validTarget(target Target) bool { return strings.TrimSpace(target.NodeID)!=""&&strings.TrimSpace(target.ServiceID)!="" }
