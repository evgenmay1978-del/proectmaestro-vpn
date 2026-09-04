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
	if runtimeWhiteListPublicationSource(nil, false, nil) != nil {
		t.Fatal("publication source enabled by default")
	}
}

func TestRuntimeWhiteListPublicationUsesExistingSidecarGate(t *testing.T) {
	service := &controlplane.Service{}
	if runtimeWhiteListPublicationSource(service, false, nil) != nil {
		t.Fatal("disabled sidecar gate injected publication source")
	}
	if runtimeWhiteListPublicationSource(service, true, nil) != nil {
		t.Fatal("publication source enabled without live sidecar receipt lookup")
	}
	senders := map[string]controlplane.ExternalActionSender{"s2": runtimePublicationSender{}}
	if runtimeWhiteListPublicationSource(service, true, senders) == nil {
		t.Fatal("enabled sidecar gate did not inject production publication source")
	}
}

type runtimePublicationSender struct{}

func (runtimePublicationSender) Post(context.Context, []byte) ([]byte, error) { return nil, nil }
