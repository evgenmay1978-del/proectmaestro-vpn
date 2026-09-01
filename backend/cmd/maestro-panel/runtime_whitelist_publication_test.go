package main

import (
	"context"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
)

type runtimePublicationSource struct{}

func (runtimePublicationSource) WhiteListPublication(context.Context, string, time.Time) (api.WhiteListPublicationSnapshot, error) {
	return api.WhiteListPublicationSnapshot{}, nil
}

func TestRuntimeWhiteListPublicationDefaultsOff(t *testing.T) {
	if runtimeWhiteListPublicationSource() != nil {
		t.Fatal("publication source enabled by default")
	}
}

func TestRuntimeWhiteListPublicationExplicitInjection(t *testing.T) {
	source := runtimePublicationSource{}
	if runtimeWhiteListPublicationSourceFrom(source, false) != nil {
		t.Fatal("disabled publication source was injected")
	}
	if runtimeWhiteListPublicationSourceFrom(source, true) != source {
		t.Fatal("enabled publication source was not injected")
	}
}
