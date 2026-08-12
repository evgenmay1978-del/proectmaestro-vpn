package applyagent

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"time"
)

var ErrStaleFence = errors.New("applyagent: stale fence")

type AppliedState struct { SnapshotSHA256 string; Healthy bool }
type PreparedChange struct { SnapshotSHA256 string }
type EntryMarker struct { Generation int64; PayloadSHA256 string }
type StateMarker struct { SnapshotSHA256 string; ClusterEpoch, NodeIncarnation, LeaseFence int64; HolderID string; Entries map[string]EntryMarker }

type LeaseVerifier interface { VerifyCurrentStrong(context.Context, string, string, string, string, int64, int64, int64) error }
type Driver interface {
	Inspect(context.Context, DesiredSnapshot) (AppliedState, error)
	Prepare(context.Context, DesiredSnapshot) (PreparedChange, error)
	Commit(context.Context, PreparedChange) (AppliedState, error)
	Rollback(context.Context, PreparedChange) error
}
type LocalStateStore interface { Load(context.Context) (StateMarker, error); Store(context.Context, StateMarker) error }
type AgentConfig struct { NodeID, ServiceID string; NodeIncarnation int64; PublicKeys map[string]ed25519.PublicKey; Verifier LeaseVerifier; Driver Driver; State LocalStateStore; Clock func() time.Time }
type Agent struct { cfg AgentConfig; mu sync.Mutex }

func NewAgent(cfg AgentConfig) (*Agent, error) {
	if cfg.NodeID=="" || cfg.ServiceID=="" || cfg.NodeIncarnation<=0 || cfg.Verifier==nil || cfg.Driver==nil || cfg.State==nil || cfg.Clock==nil || len(cfg.PublicKeys)==0 { return nil, ErrInvalidCommand }
	return &Agent{cfg:cfg}, nil
}

func (a *Agent) Apply(ctx context.Context, signed SignedCommand) (DispatchResult, error) {
	a.mu.Lock(); defer a.mu.Unlock()
	cmd, err := VerifySignedCommand(signed, a.cfg.PublicKeys, a.cfg.Clock()); if err!=nil { return DispatchResult{}, err }
	if cmd.NodeID!=a.cfg.NodeID || cmd.ServiceID!=a.cfg.ServiceID || cmd.NodeIncarnation!=a.cfg.NodeIncarnation { return DispatchResult{}, ErrInvalidCommand }
	verify := func() error { return a.cfg.Verifier.VerifyCurrentStrong(ctx, cmd.NodeID, cmd.ServiceID, cmd.HolderID, cmd.Snapshot.SnapshotSHA256, cmd.ClusterEpoch, cmd.NodeIncarnation, cmd.LeaseFence) }
	if err=verify(); err!=nil { return DispatchResult{}, err }
	marker,err:=a.cfg.State.Load(ctx);if err!=nil{return DispatchResult{},err}
	if err=validateMarkerForCommand(marker,cmd);err!=nil{return DispatchResult{},err}
	actual, err := a.cfg.Driver.Inspect(ctx, cmd.Snapshot); if err!=nil { return DispatchResult{}, err }
	if actual.Healthy && actual.SnapshotSHA256==cmd.Snapshot.SnapshotSHA256 {
		if err:=a.storeMarker(ctx,cmd);err!=nil{return DispatchResult{},err}
		return dispatchResult(cmd),nil
	}
	prepared, err := a.cfg.Driver.Prepare(ctx, cmd.Snapshot); if err!=nil { return DispatchResult{}, err }
	if prepared.SnapshotSHA256=="" { prepared.SnapshotSHA256=cmd.Snapshot.SnapshotSHA256 }
	if err=verify(); err!=nil { _=a.cfg.Driver.Rollback(ctx, prepared); return DispatchResult{}, err }
	applied, err := a.cfg.Driver.Commit(ctx, prepared); if err!=nil { _=a.cfg.Driver.Rollback(ctx, prepared); return DispatchResult{}, err }
	if !applied.Healthy || applied.SnapshotSHA256!=cmd.Snapshot.SnapshotSHA256 { _=a.cfg.Driver.Rollback(ctx, prepared); return DispatchResult{}, ErrInvalidCommand }
	if err:=a.storeMarker(ctx,cmd);err!=nil{return DispatchResult{},err}
	return dispatchResult(cmd),nil
}

func validateMarkerForCommand(marker StateMarker,cmd ApplyCommand) error {
	if marker.SnapshotSHA256==""&&len(marker.Entries)==0{return nil}
	if err:=validateStateMarker(marker);err!=nil{return err}
	if marker.ClusterEpoch>cmd.ClusterEpoch||
		(marker.ClusterEpoch==cmd.ClusterEpoch&&marker.LeaseFence>cmd.LeaseFence)||
		marker.NodeIncarnation>cmd.NodeIncarnation{return ErrInvalidCommand}
	for _,entry:=range cmd.Snapshot.Entries {
		previous,exists:=marker.Entries[entry.CustomerID]
		if !exists{continue}
		if entry.Generation<previous.Generation||
			(entry.Generation==previous.Generation&&entry.PayloadSHA256!=previous.PayloadSHA256){return ErrInvalidCommand}
	}
	return nil
}

func dispatchResult(cmd ApplyCommand) DispatchResult {
	entries:=make([]AppliedEntry,0,len(cmd.Snapshot.Entries))
	for _,entry:=range cmd.Snapshot.Entries {
		entries=append(entries,AppliedEntry{
			CustomerID:entry.CustomerID,
			OperationID:entry.OperationID,
			Generation:entry.Generation,
			DesiredSHA256:entry.PayloadSHA256,
			ObservedSHA256:entry.PayloadSHA256,
		})
	}
	return DispatchResult{SnapshotSHA256:cmd.Snapshot.SnapshotSHA256,Entries:entries}
}

func (a *Agent) storeMarker(ctx context.Context, cmd ApplyCommand) error {
	m:=StateMarker{SnapshotSHA256:cmd.Snapshot.SnapshotSHA256,ClusterEpoch:cmd.ClusterEpoch,NodeIncarnation:cmd.NodeIncarnation,LeaseFence:cmd.LeaseFence,HolderID:cmd.HolderID,Entries:map[string]EntryMarker{}}
	for _,e:=range cmd.Snapshot.Entries { m.Entries[e.CustomerID]=EntryMarker{Generation:e.Generation,PayloadSHA256:e.PayloadSHA256} }
	return a.cfg.State.Store(ctx,m)
}
