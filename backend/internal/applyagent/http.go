package applyagent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const MaxHTTPBodyBytes int64 = 4 << 20

type HTTPConfig struct { Agent *Agent; Status *StatusSigner; DispatcherSAN string; Ready func() bool }
type HTTPHandler struct { cfg HTTPConfig }

func NewHTTPHandler(cfg HTTPConfig) (*HTTPHandler,error) {
	if cfg.Agent==nil || strings.TrimSpace(cfg.DispatcherSAN)=="" || cfg.Ready==nil { return nil,ErrInvalidCommand }
	return &HTTPHandler{cfg:cfg},nil
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter,r *http.Request) {
	switch r.URL.Path {
	case "/livez":
		if r.Method!=http.MethodGet { w.WriteHeader(http.StatusMethodNotAllowed);return };w.WriteHeader(http.StatusOK)
	case "/readyz":
		if r.Method!=http.MethodGet { w.WriteHeader(http.StatusMethodNotAllowed);return };if !h.cfg.Ready(){w.WriteHeader(http.StatusServiceUnavailable);return};w.WriteHeader(http.StatusOK)
	case "/v1/apply":
		h.apply(w,r)
	case "/v1/status":
		h.status(w,r)
	default:
		http.NotFound(w,r)
	}
}

func (h *HTTPHandler) apply(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodPost { w.WriteHeader(http.StatusMethodNotAllowed);return }
	if !hasExactPeerSAN(r,h.cfg.DispatcherSAN) { w.WriteHeader(http.StatusUnauthorized);return }
	if r.ContentLength>MaxHTTPBodyBytes { w.WriteHeader(http.StatusRequestEntityTooLarge);return }
	r.Body=http.MaxBytesReader(w,r.Body,MaxHTTPBodyBytes)
	decoder:=json.NewDecoder(r.Body);decoder.DisallowUnknownFields()
	var signed SignedCommand
	if err:=decoder.Decode(&signed);err!=nil { var maxErr *http.MaxBytesError;if errors.As(err,&maxErr){w.WriteHeader(http.StatusRequestEntityTooLarge)}else{w.WriteHeader(http.StatusBadRequest)};return }
	if err:=decoder.Decode(&struct{}{});!errors.Is(err,io.EOF) { w.WriteHeader(http.StatusBadRequest);return }
	result,err:=h.cfg.Agent.Apply(r.Context(),signed);if err!=nil { w.WriteHeader(http.StatusConflict);return }
	w.Header().Set("Content-Type","application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (h *HTTPHandler) status(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodGet { w.WriteHeader(http.StatusMethodNotAllowed);return }
	if !hasExactPeerSAN(r,h.cfg.DispatcherSAN) { w.WriteHeader(http.StatusUnauthorized);return }
	if h.cfg.Status==nil { w.WriteHeader(http.StatusServiceUnavailable);return }
	signed,err:=h.cfg.Status.Sign(r.Context(),r.URL.Query().Get("nonce"));if err!=nil { w.WriteHeader(http.StatusServiceUnavailable);return }
	w.Header().Set("Content-Type","application/json")
	_ = json.NewEncoder(w).Encode(signed)
}

func hasExactPeerSAN(r *http.Request,want string) bool {
	if r.TLS==nil || len(r.TLS.PeerCertificates)!=1 { return false }
	for _,name:=range r.TLS.PeerCertificates[0].DNSNames { if name==want{return true} }
	return false
}
