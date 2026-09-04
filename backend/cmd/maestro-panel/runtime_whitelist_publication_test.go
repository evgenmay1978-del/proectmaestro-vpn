package main

import (
	"context"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

type runtimePublicationSource struct{}

func (runtimePublicationSource) WhiteListPublication(context.Context, string, time.Time) (api.WhiteListPublicationSnapshot, error) {
	return api.WhiteListPublicationSnapshot{}, nil
}

func TestRuntimeWhiteListPublicationDefaultsOff(t *testing.T) {
	if runtimeWhiteListPublicationSource(nil, false) != nil {
		t.Fatal("publication source enabled by default")
	}
}

func TestRuntimeWhiteListPublicationUsesExistingSidecarGate(t *testing.T) {
	service := &controlplane.Service{}
	if runtimeWhiteListPublicationSource(service, false) != nil {
		t.Fatal("disabled sidecar gate injected publication source")
	}
	if runtimeWhiteListPublicationSource(service, true) == nil {
		t.Fatal("enabled sidecar gate did not inject production publication source")
	}
}
