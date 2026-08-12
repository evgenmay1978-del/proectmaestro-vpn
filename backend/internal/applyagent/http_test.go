package applyagent

import (
	"crypto/x509"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplyRequiresExactDispatcherCertificateIdentity(t *testing.T) {
	verifier:=&fakeLeaseVerifier{}; driver:=&fakeDriver{}
	agent,_,_:=agentFixture(t,verifier,driver,&fakeStateStore{})
	handler,err:=NewHTTPHandler(HTTPConfig{Agent:agent,DispatcherSAN:"controlplane-dispatcher",Ready:func()bool{return true}})
	if err!=nil { t.Fatalf("NewHTTPHandler: %v",err) }
	request:=httptest.NewRequest(http.MethodPost,"/v1/apply",strings.NewReader(`{}`))
	response:=httptest.NewRecorder();handler.ServeHTTP(response,request)
	if response.Code!=http.StatusUnauthorized { t.Fatalf("status=%d, want 401",response.Code) }
	if totalDriverCalls(driver)!=0 { t.Fatalf("unauthenticated apply reached driver: %d",totalDriverCalls(driver)) }
}

func TestApplyRejectsBodyOverLimitBeforeDriver(t *testing.T) {
	verifier:=&fakeLeaseVerifier{};driver:=&fakeDriver{}
	agent,_,_:=agentFixture(t,verifier,driver,&fakeStateStore{})
	handler,_:=NewHTTPHandler(HTTPConfig{Agent:agent,DispatcherSAN:"controlplane-dispatcher",Ready:func()bool{return true}})
	request:=httptest.NewRequest(http.MethodPost,"/v1/apply",strings.NewReader(strings.Repeat("x",int(MaxHTTPBodyBytes+1))))
	request.TLS=&tls.ConnectionState{PeerCertificates:[]*x509.Certificate{{DNSNames:[]string{"controlplane-dispatcher"}}}}
	response:=httptest.NewRecorder();handler.ServeHTTP(response,request)
	if response.Code!=http.StatusRequestEntityTooLarge { t.Fatalf("status=%d, want 413",response.Code) }
	if totalDriverCalls(driver)!=0 { t.Fatalf("oversized apply reached driver: %d",totalDriverCalls(driver)) }
}

func TestHealthRoutesAndUnknownPathDoNotRedirect(t *testing.T) {
	agent,_,_:=agentFixture(t,&fakeLeaseVerifier{},&fakeDriver{},&fakeStateStore{})
	handler,_:=NewHTTPHandler(HTTPConfig{Agent:agent,DispatcherSAN:"controlplane-dispatcher",Ready:func()bool{return false}})
	for path,want:=range map[string]int{"/livez":http.StatusOK,"/readyz":http.StatusServiceUnavailable,"/unknown/":http.StatusNotFound} {
		response:=httptest.NewRecorder();handler.ServeHTTP(response,httptest.NewRequest(http.MethodGet,path,nil))
		if response.Code!=want { t.Fatalf("%s status=%d, want %d",path,response.Code,want) }
	}
}
