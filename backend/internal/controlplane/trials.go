package controlplane

import "context"

func (s *Service) RedeemTrial(ctx context.Context, command RedeemTrialCommand) (Customer, error) {
	return s.mutateCustomer(ctx, customerMutation{
		commandType: "trial.redeem", login: command.Login, idempotency: command.IdempotencyKey,
		days: command.Days, status: "active", allowCreate: true,
		trialAnchor: command.Anchor, trialDevice: command.DRMIdentity,
	})
}
