package applyagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func TestSignedStatusRequiresFreshStrongLeaseProof(t *testing.T) {
	publicKey,privateKey,err:=ed25519.GenerateKey(rand.Reader);if err!=nil{t.Fatal(err)}
	marker:=StateMarker{SnapshotSHA256:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",ClusterEpoch:7,NodeIncarnation:3,LeaseFence:11,HolderID:"dispatcher-a",Entries:map[string]EntryMarker{}}
	state:=&fakeStateStore{marker:marker}
	verifier:=&fakeLeaseVerifier{}
	signer,err:=NewStatusSigner(StatusSignerConfig{NodeID:"node-a",ServiceID:"xui",State:state,Verifier:verifier,KeyID:"status-key-1",PrivateKey:privateKey,Clock:func()time.Time{return time.Unix(2_000_000,0)}})
	if err!=nil{t.Fatalf("NewStatusSigner: %v",err)}
	signed,err:=signer.Sign(context.Background(),"nonce-1");if err!=nil{t.Fatalf("Sign: %v",err)}
	status,err:=VerifySignedStatus(signed,map[string]ed25519.PublicKey{"status-key-1":publicKey},"nonce-1",time.Unix(2_000_010,0));if err!=nil{t.Fatalf("VerifySignedStatus: %v",err)}
	if status.SnapshotSHA256!=marker.SnapshotSHA256||verifier.calls!=1{t.Fatalf("status=%#v verifier.calls=%d",status,verifier.calls)}
}

func TestStatusNoQuorumProducesNoSignedResult(t *testing.T) {
	_,privateKey,_:=ed25519.GenerateKey(rand.Reader)
	state:=&fakeStateStore{marker:StateMarker{SnapshotSHA256:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",ClusterEpoch:7,NodeIncarnation:3,LeaseFence:11,HolderID:"dispatcher-a",Entries:map[string]EntryMarker{}}}
	verifier:=&fakeLeaseVerifier{fn:func(int,string,int64,int64,int64)error{return errNoQuorum}}
	signer,_:=NewStatusSigner(StatusSignerConfig{NodeID:"node-a",ServiceID:"xui",State:state,Verifier:verifier,KeyID:"status-key-1",PrivateKey:privateKey,Clock:func()time.Time{return time.Unix(2_000_000,0)}})
	if _,err:=signer.Sign(context.Background(),"nonce-1");!errors.Is(err,errNoQuorum){t.Fatalf("Sign error=%v, want no quorum",err)}
}
