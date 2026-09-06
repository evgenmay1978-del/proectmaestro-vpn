package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/agent"
)

const CredentialPath = "/v1/credentials"

type credentialInstaller interface {
	InstallCredential(context.Context, agent.CredentialInstall) error
}

func credentialHandler(applier Applier) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		if request.Method != http.MethodPost {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if applier == nil || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		installer, ok := applier.(credentialInstaller)
		if !ok {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, 4096)
		var command agent.CredentialInstall
		if !readCanonicalLeaseBody(response, request, &command) {
			return
		}
		if err := installer.InstallCredential(request.Context(), command); err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, agent.ErrConflict) {
				status = http.StatusConflict
			}
			response.WriteHeader(status)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})
}
