package applyagent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"time"
)

type AgentStatus struct { Nonce string `json:"nonce"`; NodeID string `json:"node_id"`; ServiceID string `json:"service_id"`; Ready bool `json:"ready"`; ClusterEpoch int64 `json:"cluster_epoch"`; NodeIncarnation int64 `json:"node_incarnation"`; LeaseFence int64 `json:"lease_fence"`; SnapshotSHA256 string `json:"snapshot_sha256"`; IssuedAtUnix int64 `json:"issued_at_unix"`; NotAfterUnix int64 `json:"not_after_unix"` }
type SignedStatus struct { KeyID string `json:"key_id"`; Status []byte `json:"status"`; Signature []byte `json:"signature"` }
type StatusSignerConfig struct { NodeID,ServiceID string; State LocalStateStore; Verifier LeaseVerifier; KeyID string; PrivateKey ed25519.PrivateKey; Clock func()time.Time }
type StatusSigner struct { cfg StatusSignerConfig }

func NewStatusSigner(cfg StatusSignerConfig)(*StatusSigner,error){if cfg.NodeID==""||cfg.ServiceID==""||cfg.State==nil||cfg.Verifier==nil||cfg.KeyID==""||len(cfg.PrivateKey)!=ed25519.PrivateKeySize||cfg.Clock==nil{return nil,ErrInvalidCommand};return &StatusSigner{cfg:cfg},nil}
func (s *StatusSigner) Sign(ctx context.Context,nonce string)(SignedStatus,error){
	if strings.TrimSpace(nonce)==""{return SignedStatus{},ErrInvalidCommand}
	marker,err:=s.cfg.State.Load(ctx);if err!=nil{return SignedStatus{},err}
	if marker.ClusterEpoch<=0||marker.NodeIncarnation<=0||marker.LeaseFence<=0||marker.HolderID==""||!validSHA256(marker.SnapshotSHA256){return SignedStatus{},ErrInvalidCommand}
	if err:=s.cfg.Verifier.VerifyCurrentStrong(ctx,s.cfg.NodeID,s.cfg.ServiceID,marker.HolderID,marker.SnapshotSHA256,marker.ClusterEpoch,marker.NodeIncarnation,marker.LeaseFence);err!=nil{return SignedStatus{},err}
	now:=s.cfg.Clock().Unix();status:=AgentStatus{Nonce:nonce,NodeID:s.cfg.NodeID,ServiceID:s.cfg.ServiceID,Ready:true,ClusterEpoch:marker.ClusterEpoch,NodeIncarnation:marker.NodeIncarnation,LeaseFence:marker.LeaseFence,SnapshotSHA256:marker.SnapshotSHA256,IssuedAtUnix:now,NotAfterUnix:now+30}
	canonical,err:=json.Marshal(status);if err!=nil{return SignedStatus{},ErrInvalidCommand};return SignedStatus{KeyID:s.cfg.KeyID,Status:canonical,Signature:ed25519.Sign(s.cfg.PrivateKey,canonical)},nil
}
func VerifySignedStatus(signed SignedStatus,keys map[string]ed25519.PublicKey,nonce string,now time.Time)(AgentStatus,error){
	key,ok:=keys[signed.KeyID];if !ok||len(key)!=ed25519.PublicKeySize||!ed25519.Verify(key,signed.Status,signed.Signature){return AgentStatus{},ErrInvalidCommand}
	var status AgentStatus;if err:=json.Unmarshal(signed.Status,&status);err!=nil{return AgentStatus{},ErrInvalidCommand};canonical,_:=json.Marshal(status)
	if !bytes.Equal(canonical,signed.Status)||status.Nonce!=nonce||!status.Ready||status.NotAfterUnix-status.IssuedAtUnix>30||now.Unix()<status.IssuedAtUnix||now.Unix()>status.NotAfterUnix{return AgentStatus{},ErrInvalidCommand};return status,nil
}
