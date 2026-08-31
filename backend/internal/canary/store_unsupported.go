//go:build !linux

package canary

import "context"

type storeImpl struct{}

func NewStore() (*Store, error) {
	return nil, invalid("unsupported_platform")
}

func (*Store) Status(context.Context) (Stage, error) {
	return Stage{}, invalid("unsupported_platform")
}

func (*Store) Prepare(context.Context, Snapshot, []byte, Artifacts, ConfigTester) (Stage, error) {
	return Stage{}, invalid("unsupported_platform")
}

func (*Store) Activate(context.Context, string, ServiceController) error {
	return invalid("unsupported_platform")
}

func (*Store) RollbackToAbsence(context.Context, string, ServiceController, DiagnosticOrigin) error {
	return invalid("unsupported_platform")
}
