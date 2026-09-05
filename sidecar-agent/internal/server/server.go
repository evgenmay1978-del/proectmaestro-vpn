package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/agent"
)

const (
	DesiredPath         = "/v1/desired"
	ReceiptPath         = "/v1/receipt"
	UsagePath           = "/v1/usage"
	ActionKeyHeader     = "X-Maestro-Action-Key"
	DesiredSHA256Header = "X-Maestro-Desired-SHA256"
	MaxRequestBytes     = agent.MaxDesiredBytes
)

type Applier interface {
	Apply(context.Context, agent.Desired) (agent.Receipt, error)
	LookupReceipt(context.Context, string) (agent.Receipt, error)
}

type usageReader interface {
	Usage(context.Context, string) (agent.UsageSnapshot, error)
}

func NewHandler(applier Applier) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(DesiredPath, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if applier == nil || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		defer request.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(request.Body, MaxRequestBytes+1))
		if err != nil {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(raw) > MaxRequestBytes {
			response.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		desired, err := agent.ParseDesired(raw)
		if err != nil {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		digest := sha256.Sum256(raw)
		if !equalHeader(request.Header.Get(DesiredSHA256Header), hex.EncodeToString(digest[:])) ||
			!equalHeader(request.Header.Get(ActionKeyHeader), desired.ActionKey()) {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		receipt, err := applier.Apply(request.Context(), desired)
		if err != nil {
			if errors.Is(err, agent.ErrStaleGeneration) || errors.Is(err, agent.ErrConflict) {
				response.WriteHeader(http.StatusConflict)
				return
			}
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		encoded, err := json.Marshal(receipt)
		if err != nil {
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(encoded)
	}))
	mux.Handle(ReceiptPath, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if applier == nil || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		actionKey := request.Header.Get(ActionKeyHeader)
		if actionKey == "" || strings.TrimSpace(actionKey) != actionKey || strings.ContainsAny(actionKey, "\x00\r\n\t") {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		receipt, err := applier.LookupReceipt(request.Context(), actionKey)
		if err != nil {
			if errors.Is(err, agent.ErrNotFound) {
				response.WriteHeader(http.StatusNotFound)
				return
			}
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		encoded, err := json.Marshal(receipt)
		if err != nil {
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(encoded)
	}))
	mux.Handle(UsagePath, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if applier == nil || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		actionKey := request.Header.Get(ActionKeyHeader)
		if actionKey == "" || strings.TrimSpace(actionKey) != actionKey || strings.ContainsAny(actionKey, "\x00\r\n\t") {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		reader, ok := applier.(usageReader)
		if !ok {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
		defer cancel()
		snapshot, err := reader.Usage(ctx, actionKey)
		if err != nil {
			if errors.Is(err, agent.ErrNotFound) {
				response.WriteHeader(http.StatusNotFound)
				return
			}
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(encoded)
	}))
	return mux
}

func ServerTLSConfig(certificate tls.Certificate, clientCA *x509.CertPool, clientName string) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCA,
		NextProtos:   []string{"h2", "http/1.1"},
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) != 1 || len(state.VerifiedChains) == 0 || clientName == "" {
				return errors.New("sidecar server: invalid client certificate")
			}
			if err := state.PeerCertificates[0].VerifyHostname(clientName); err != nil {
				return errors.New("sidecar server: invalid client certificate name")
			}
			return nil
		},
	}
}

func LoadServerTLSConfig(certFile, keyFile, clientCAFile, clientName string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" || clientCAFile == "" || clientName == "" {
		return nil, errors.New("sidecar server: incomplete mTLS configuration")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, errors.New("sidecar server: load server certificate")
	}
	caBytes, err := os.ReadFile(clientCAFile)
	if err != nil {
		return nil, errors.New("sidecar server: load client CA")
	}
	clientCA := x509.NewCertPool()
	if !clientCA.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("sidecar server: parse client CA")
	}
	return ServerTLSConfig(certificate, clientCA, clientName), nil
}

func equalHeader(actual, expected string) bool {
	return actual != "" && strings.TrimSpace(actual) == actual && len(actual) == len(expected) &&
		subtleEqual(actual, expected)
}

func subtleEqual(left, right string) bool {
	leftDigest := sha256.Sum256([]byte(left))
	rightDigest := sha256.Sum256([]byte(right))
	return leftDigest == rightDigest
}
