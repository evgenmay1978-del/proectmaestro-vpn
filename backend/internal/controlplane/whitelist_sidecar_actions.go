package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type WhiteListSidecarGenerationResult struct {
	Generation int64
	ReleaseID  string
	Ready      bool
	FreshUntil time.Time
	Desired    []WhiteListSidecarDesired
	Receipts   []WhiteListSidecarReceipt
}

type whiteListSidecarReceiptLookup interface {
	LookupReceipt(context.Context, string) ([]byte, error)
}

func (s *Service) ExecuteWhiteListSidecarAction(
	ctx context.Context, desired WhiteListSidecarDesired, workerID string, sender ExternalActionSender,
) (WhiteListSidecarReceipt, error) {
	if ctx == nil || sender == nil || strings.TrimSpace(workerID) == "" ||
		validateWhiteListSidecarDesired(desired) != nil {
		return WhiteListSidecarReceipt{}, ErrConflict
	}
	if _, err := s.PersistWhiteListSidecarDesired(ctx, desired); err != nil {
		return WhiteListSidecarReceipt{}, fmt.Errorf("controlplane: persist white-list sidecar desired: %w", err)
	}
	result, executeErr := s.ExecuteExternalAction(ctx, desired.Action, workerID, sender)
	response := result.Response
	if executeErr != nil || result.State == "unknown" {
		lookup, ok := sender.(whiteListSidecarReceiptLookup)
		if !ok {
			if executeErr != nil {
				return WhiteListSidecarReceipt{}, executeErr
			}
			return WhiteListSidecarReceipt{}, ErrUnavailable
		}
		lookupContext := context.WithoutCancel(ctx)
		var err error
		response, err = lookup.LookupReceipt(lookupContext, desired.Action.ActionKey)
		if err != nil {
			if executeErr != nil {
				return WhiteListSidecarReceipt{}, executeErr
			}
			return WhiteListSidecarReceipt{}, ErrUnavailable
		}
	} else if result.State != "succeeded" {
		return WhiteListSidecarReceipt{}, ErrUnavailable
	}
	receipt, err := decodeWhiteListSidecarReceipt(response)
	if err != nil || receipt.ActionKey != desired.Action.ActionKey {
		return WhiteListSidecarReceipt{}, ErrConflict
	}
	stored, err := s.RecordWhiteListSidecarReceipt(ctx, desired, receipt.XrayProcessBootID, receipt)
	if err != nil {
		return WhiteListSidecarReceipt{}, fmt.Errorf("controlplane: record white-list sidecar receipt: %w", err)
	}
	return stored, nil
}

func (s *Service) ReconcileWhiteListSidecarGeneration(
	ctx context.Context, previous map[string]WhiteListSidecarDesired, origins []WhiteListOrigin,
	routes []WhiteListManagedRoute, exit WhiteListExit, workerID string,
	resolveSender func(string) (ExternalActionSender, bool),
) (WhiteListSidecarGenerationResult, error) {
	if ctx == nil || !exit.Healthy || strings.TrimSpace(workerID) == "" || resolveSender == nil {
		return WhiteListSidecarGenerationResult{}, ErrConflict
	}
	releaseID := ""
	active := 0
	for _, origin := range origins {
		if !origin.Active {
			continue
		}
		active++
		if releaseID == "" {
			releaseID = origin.ReleaseID
		}
		if origin.ReleaseID == "" || origin.ReleaseID != releaseID {
			return WhiteListSidecarGenerationResult{}, ErrConflict
		}
	}
	if active == 0 {
		return WhiteListSidecarGenerationResult{}, ErrConflict
	}
	desired, err := BuildWhiteListSidecarDesired(previous, origins, routes, exit)
	if err != nil || len(desired) != active {
		return WhiteListSidecarGenerationResult{}, ErrConflict
	}
	generation := desired[0].Generation
	for _, target := range desired {
		if target.Generation != generation || target.ReleaseID != releaseID {
			return WhiteListSidecarGenerationResult{}, ErrConflict
		}
		if _, ok := resolveSender(target.NodeID); !ok {
			return WhiteListSidecarGenerationResult{}, ErrUnavailable
		}
	}

	receipts := make([]WhiteListSidecarReceipt, 0, len(desired))
	bootIDs := make(map[string]string, len(desired))
	for _, target := range desired {
		sender, _ := resolveSender(target.NodeID)
		receipt, err := s.ExecuteWhiteListSidecarAction(ctx, target, workerID, sender)
		if err != nil {
			return WhiteListSidecarGenerationResult{}, fmt.Errorf("controlplane: deliver white-list sidecar desired: %w", err)
		}
		receipts = append(receipts, receipt)
		bootIDs[target.OriginID] = receipt.XrayProcessBootID
	}
	ready, err := s.EvaluateWhiteListSidecarReadiness(ctx, bootIDs, receipts, exit.ExitID)
	if err != nil {
		return WhiteListSidecarGenerationResult{}, err
	}
	freshUntil := time.Time{}
	for _, receipt := range receipts {
		if freshUntil.IsZero() || receipt.ExpiresAt.Before(freshUntil) {
			freshUntil = receipt.ExpiresAt
		}
	}
	return WhiteListSidecarGenerationResult{
		Generation: generation, ReleaseID: releaseID, Ready: ready, FreshUntil: freshUntil,
		Desired:  append([]WhiteListSidecarDesired(nil), desired...),
		Receipts: append([]WhiteListSidecarReceipt(nil), receipts...),
	}, nil
}

func decodeWhiteListSidecarReceipt(raw []byte) (WhiteListSidecarReceipt, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return WhiteListSidecarReceipt{}, errors.New("controlplane: invalid white-list sidecar receipt")
	}
	var receipt WhiteListSidecarReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return WhiteListSidecarReceipt{}, errors.New("controlplane: invalid white-list sidecar receipt")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return WhiteListSidecarReceipt{}, errors.New("controlplane: invalid white-list sidecar receipt")
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(canonical, raw) {
		return WhiteListSidecarReceipt{}, errors.New("controlplane: invalid white-list sidecar receipt")
	}
	return receipt, nil
}
