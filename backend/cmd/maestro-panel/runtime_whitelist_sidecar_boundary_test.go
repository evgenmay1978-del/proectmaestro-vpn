package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func TestRQLiteBackgroundUsesSidecarSendersOnExistingWorkerPass(t *testing.T) {
	wantSender := runtimeExternalSender{}
	sidecar := &runtimeSidecarBoundaryReconciler{
		calls: make(chan struct{}, 1), wantWorker: "worker-1", wantSender: wantSender,
	}
	renewal := &runtimeBoundaryRenewalReconciler{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runRQLiteReconcilers(ctx, renewal, sidecar, "worker-1", map[string]controlplane.ExternalActionSender{
			"s1": wantSender,
		}, time.Hour)
		close(done)
	}()
	select {
	case <-sidecar.calls:
	case <-time.After(time.Second):
		t.Fatal("sidecar reconciliation did not consume the configured sender")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("combined runtime reconciler did not stop")
	}
	if renewal.calls != 1 {
		t.Fatalf("renewal calls=%d, want the same immediate worker pass", renewal.calls)
	}
}

type runtimeBoundaryRenewalReconciler struct{ calls int }

func (reconciler *runtimeBoundaryRenewalReconciler) ReconcileWhiteListRenewalIntents(context.Context, int) (int64, error) {
	reconciler.calls++
	return 0, nil
}

type runtimeSidecarBoundaryReconciler struct {
	calls      chan struct{}
	wantWorker string
	wantSender controlplane.ExternalActionSender
}

func (reconciler *runtimeSidecarBoundaryReconciler) ReconcileWhiteListSidecarIntents(
	_ context.Context,
	workerID string,
	resolve func(string) (controlplane.ExternalActionSender, bool),
) error {
	if workerID != reconciler.wantWorker {
		return errors.New("unexpected worker")
	}
	sender, ok := resolve("s1")
	if !ok || sender != reconciler.wantSender {
		return errors.New("configured sender was not resolved")
	}
	if _, unexpected := resolve("ordinary"); unexpected {
		return errors.New("unrelated sender resolved")
	}
	reconciler.calls <- struct{}{}
	return nil
}
