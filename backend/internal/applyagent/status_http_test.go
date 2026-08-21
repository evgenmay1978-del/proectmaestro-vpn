package applyagent

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStatusRouteRequiresMTLSNonceAndReturnsVerifiableStatus(t *testing.T) {
	publicKey,privateKey,_:=ed25519.GenerateKey(rand.Reader)
	state:=&fakeStateStore{marker:StateMarker{SnapshotSHA256:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",ClusterEpoch:7,NodeIncarnation:3,LeaseFence:11,HolderID:"dispatcher-a",Entries:map[string]EntryMarker{}}}
	statusSigner,_:=NewStatusSigner(StatusSignerConfig{NodeID:"node-a",ServiceID:"xui",State:state,Verifier:&fakeLeaseVerifier{},KeyID:"status-key-1",PrivateKey:privateKey,Clock:func()time.Time{return time.Unix(2_000_000,0)}})
	agent,_,_:=agentFixture(t,&fakeLeaseVerifier{},&fakeDriver{},&fakeStateStore{})
	handler,err:=NewHTTPHandler(HTTPConfig{Agent:agent,Status:statusSigner,DispatcherSAN:"controlplane-dispatcher",Ready:func()bool{return true}});if err!=nil{t.Fatal(err)}
	request:=httptest.NewRequest(http.MethodGet,"/v1/status?nonce=nonce-1",nil);request.TLS=&tls.ConnectionState{PeerCertificates:[]*x509.Certificate{{DNSNames:[]string{"controlplane-dispatcher"}}}}
	response:=httptest.NewRecorder();handler.ServeHTTP(response,request)
	if response.Code!=http.StatusOK{t.Fatalf("status=%d body=%s",response.Code,response.Body.String())}
	var signed SignedStatus;if err:=json.Unmarshal(response.Body.Bytes(),&signed);err!=nil{t.Fatal(err)}
	if _,err:=VerifySignedStatus(signed,map[string]ed25519.PublicKey{"status-key-1":publicKey},"nonce-1",time.Unix(2_000_010,0));err!=nil{t.Fatalf("VerifySignedStatus: %v",err)}
}
