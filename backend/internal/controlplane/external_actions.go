package controlplane

import (
	"context"
	"errors"
)

type ExternalActionCommand struct {
	Type              string
	ResourceID        string
	ActionKey         string
	ReplacesActionKey string
	// ReplayResourceID and ReplayRequest are exact legacy binding aliases. New rows and provider calls use only ResourceID and Request.
	ReplayResourceID string
	ReplayRequest    []byte
	WorkerID         string
	LeaseToken       string
	LeaseFence       int64
	Request          []byte
}

func externalActionReplayAliasValid(command ExternalActionCommand) bool {
	hasResource := command.ReplayResourceID != ""
	hasRequest := command.ReplayRequest != nil
	return hasResource == hasRequest && (!hasRequest || len(command.ReplayRequest) > 0)
}

type ExternalActionResult struct {
	ID       string
	State    string
	Response []byte
}

type ExternalActionCrashPoint string

const (
	CrashBeforeAttemptMarker ExternalActionCrashPoint = "before-attempt-marker"
	CrashAfterAttemptMarker  ExternalActionCrashPoint = "after-attempt-marker"
	CrashAfterProviderPost   ExternalActionCrashPoint = "after-provider-post"
	CrashBeforeResultCommit  ExternalActionCrashPoint = "before-result-commit"
)

type ExternalActionPersistence interface {
	Prepare(context.Context, ExternalActionCommand) (ExternalActionResult, error)
	StartAttempt(context.Context, ExternalActionCommand) (ExternalActionResult, error)
	Finish(context.Context, ExternalActionCommand, []byte) (ExternalActionResult, error)
	MarkUnknown(context.Context, ExternalActionCommand) (ExternalActionResult, error)
}

type externalActionNotSentPersistence interface {
	MarkNotSent(context.Context, ExternalActionCommand) (ExternalActionResult, error)
}

type externalActionDefinitelyNotSent interface {
	DefinitelyNotSent() bool
}

type ExternalActionSender interface {
	Post(context.Context, []byte) ([]byte, error)
}

type ExternalActionExecutor struct {
	store  ExternalActionPersistence
	sender ExternalActionSender
}

func NewExternalActionExecutor(store ExternalActionPersistence, sender ExternalActionSender) *ExternalActionExecutor {
	return &ExternalActionExecutor{store: store, sender: sender}
}

func (e *ExternalActionExecutor) Execute(
	ctx context.Context,
	command ExternalActionCommand,
	hook func(ExternalActionCrashPoint) error,
) (ExternalActionResult, error) {
	if e == nil || e.store == nil || e.sender == nil || command.Type == "" || command.ResourceID == "" ||
		command.ActionKey == "" || command.WorkerID == "" || command.LeaseToken == "" || command.LeaseFence <= 0 ||
		!externalActionReplayAliasValid(command) {
		return ExternalActionResult{}, errors.New("controlplane: invalid external action")
	}
	prepared, err := e.store.Prepare(ctx, command)
	if err != nil {
		return ExternalActionResult{}, err
	}
	switch prepared.State {
	case "succeeded", "unknown", "failed":
		return prepared, nil
	case "attempt_started":
		return e.store.MarkUnknown(ctx, command)
	case "pending":
	default:
		return ExternalActionResult{}, errors.New("controlplane: invalid external action state")
	}
	if err := externalActionHook(hook, CrashBeforeAttemptMarker); err != nil {
		return prepared, err
	}
	started, err := e.store.StartAttempt(ctx, command)
	if err != nil {
		return ExternalActionResult{}, err
	}
	if started.State != "attempt_started" {
		return ExternalActionResult{}, errors.New("controlplane: external action was not durably started")
	}
	if err := externalActionHook(hook, CrashAfterAttemptMarker); err != nil {
		return started, err
	}
	response, err := e.sender.Post(ctx, append([]byte(nil), command.Request...))
	if err != nil {
		if externalActionWasDefinitelyNotSent(err) {
			store, ok := e.store.(externalActionNotSentPersistence)
			if !ok {
				return ExternalActionResult{}, err
			}
			pending, markErr := store.MarkNotSent(ctx, command)
			if markErr != nil {
				return ExternalActionResult{}, markErr
			}
			return pending, err
		}
		unknown, markErr := e.store.MarkUnknown(ctx, command)
		if markErr != nil {
			return ExternalActionResult{}, markErr
		}
		return unknown, err
	}
	if err := externalActionHook(hook, CrashAfterProviderPost); err != nil {
		return started, err
	}
	if err := externalActionHook(hook, CrashBeforeResultCommit); err != nil {
		return started, err
	}
	return e.store.Finish(ctx, command, response)
}

func externalActionWasDefinitelyNotSent(err error) bool {
	var definite externalActionDefinitelyNotSent
	return errors.As(err, &definite) && definite.DefinitelyNotSent()
}

func externalActionHook(hook func(ExternalActionCrashPoint) error, point ExternalActionCrashPoint) error {
	if hook == nil {
		return nil
	}
	return hook(point)
}
