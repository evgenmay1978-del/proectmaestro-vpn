package canary

import "context"

type State string

const (
	StateAbsent           State = "ABSENT"
	StatePrepared         State = "PREPARED"
	StateRollbackRequired State = "ROLLBACK_REQUIRED"
	StateCanaryActive     State = "CANARY_ACTIVE"
)

type Stage struct {
	RuntimeID          string
	State              State
	SnapshotSHA256     string
	XraySHA256         string
	ServerConfigSHA256 string
	UnitSHA256         string
}

type ConfigTester interface {
	Test(context.Context, string, string, uint32, uint32) error
}

type ServiceController interface {
	Reload(context.Context) error
	IsActive(context.Context, string) (bool, error)
	Start(context.Context, string) error
	Stop(context.Context, string) error
}

type DiagnosticOrigin interface {
	RestoreAndVerify(context.Context, string, string) error
}

type Store struct {
	impl *storeImpl
}
