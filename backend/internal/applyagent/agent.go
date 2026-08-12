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
type StateMarker struct { SnapshotSHA256 string; Entries map[string]EntryMarker }

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

func (a *Agent) Apply(ctx context.Context, signed SignedCommand) (AppliedState, error) {
	a.mu.Lock(); defer a.mu.Unlock()
	cmd, err := VerifySignedCommand(signed, a.cfg.PublicKeys, a.cfg.Clock()); if err!=nil { return AppliedState{}, err }
	if cmd.NodeID!=a.cfg.NodeID || cmd.ServiceID!=a.cfg.ServiceID || cmd.NodeIncarnation!=a.cfg.NodeIncarnation { return AppliedState{}, ErrInvalidCommand }
	verify := func() error { return a.cfg.Verifier.VerifyCurrentStrong(ctx, cmd.NodeID, cmd.ServiceID, cmd.HolderID, cmd.Snapshot.SnapshotSHA256, cmd.ClusterEpoch, cmd.NodeIncarnation, cmd.LeaseFence) }
	if err=verify(); err!=nil { return AppliedState{}, err }
	actual, err := a.cfg.Driver.Inspect(ctx, cmd.Snapshot); if err!=nil { return AppliedState{}, err }
	if actual.Healthy && actual.SnapshotSHA256==cmd.Snapshot.SnapshotSHA256 { return actual, a.storeMarker(ctx, cmd) }
	prepared, err := a.cfg.Driver.Prepare(ctx, cmd.Snapshot); if err!=nil { return AppliedState{}, err }
	if prepared.SnapshotSHA256=="" { prepared.SnapshotSHA256=cmd.Snapshot.SnapshotSHA256 }
	if err=verify(); err!=nil { _=a.cfg.Driver.Rollback(ctx, prepared); return AppliedState{}, err }
	applied, err := a.cfg.Driver.Commit(ctx, prepared); if err!=nil { _=a.cfg.Driver.Rollback(ctx, prepared); return AppliedState{}, err }
	if !applied.Healthy || applied.SnapshotSHA256!=cmd.Snapshot.SnapshotSHA256 { _=a.cfg.Driver.Rollback(ctx, prepared); return AppliedState{}, ErrInvalidCommand }
	return applied, a.storeMarker(ctx, cmd)
}

func (a *Agent) storeMarker(ctx context.Context, cmd ApplyCommand) error {
	m:=StateMarker{SnapshotSHA256:cmd.Snapshot.SnapshotSHA256,Entries:map[string]EntryMarker{}}
	for _,e:=range cmd.Snapshot.Entries { m.Entries[e.CustomerID]=EntryMarker{Generation:e.Generation,PayloadSHA256:e.PayloadSHA256} }
	return a.cfg.State.Store(ctx,m)
}
