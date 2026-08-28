package controlplane

import "context"

func (s *Service) DisableCustomer(ctx context.Context, command CustomerStateCommand) (Customer, error) {
	return s.mutateCustomer(ctx, customerMutation{
		commandType: "customer.disable", login: command.Login, idempotency: command.IdempotencyKey,
		status: "suspended", tombstone: true,
	})
}

func (s *Service) EnableCustomer(ctx context.Context, command CustomerStateCommand) (Customer, error) {
	return s.mutateCustomer(ctx, customerMutation{
		commandType: "customer.enable", login: command.Login, idempotency: command.IdempotencyKey,
		status: "active",
	})
}

func (s *Service) DeleteCustomer(ctx context.Context, command DeleteCustomerCommand) error {
	_, err := s.mutateCustomer(ctx, customerMutation{
		commandType: "customer.delete", login: command.Login, idempotency: command.IdempotencyKey,
		status: "deleted", tombstone: true,
	})
	return err
}

func (s *Service) ResetDevices(ctx context.Context, command ResetDevicesCommand) error {
	_, err := s.mutateCustomer(ctx, customerMutation{
		commandType: "customer.reset-devices", login: command.Login, idempotency: command.IdempotencyKey,
		resetDevices: true,
	})
	return err
}
