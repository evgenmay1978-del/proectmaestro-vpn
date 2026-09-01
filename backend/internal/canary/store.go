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

type ServiceInspection struct {
	LoadState     string
	UnitFileState string
	FragmentPath  string
	DropInPaths   []string
	User          string
	Group         string
	ExecStart     []string
}

type ServiceInspector interface {
	Inspect(context.Context, string) (ServiceInspection, error)
	IsActive(context.Context, string) (bool, error)
}

type ConfigTester interface {
	ServiceInspector
	Test(context.Context, string, string, uint32, uint32) error
}

type ServiceController interface {
	ServiceInspector
	Reload(context.Context) error
	Start(context.Context, string) error
	Stop(context.Context, string) error
}

type DiagnosticRestorationVerifier interface {
	VerifyRestored(ctx context.Context, diagnosticProbeURL, diagnosticResponseSHA256 string) error
}

type Store struct {
	impl *storeImpl
}
