package backuprpo

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

const maximumCommandOutputBytes int64 = 4096

const commandWaitDelay = 250 * time.Millisecond

type osCommandRunner struct{}

func (osCommandRunner) Run(ctx context.Context, spec CommandSpec) error {
	if spec.Path == "" || len(spec.Args) == 0 || spec.Timeout <= 0 {
		return ErrCommandFailed
	}
	if len(spec.ExtraFiles) > 16 {
		return ErrCommandFailed
	}
	for _, file := range spec.ExtraFiles {
		if file == nil {
			return ErrCommandFailed
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	command := exec.CommandContext(runCtx, spec.Path, spec.Args...)
	command.Env = append([]string(nil), spec.Env...)
	command.ExtraFiles = append([]*os.File(nil), spec.ExtraFiles...)
	configureProcessGroup(command)
	command.Cancel = func() error {
		killProcessGroup(command)
		return nil
	}
	command.WaitDelay = commandWaitDelay
	output := &boundedOutputSink{remaining: maximumCommandOutputBytes}
	if spec.Stdin != nil {
		command.Stdin = spec.Stdin
	}
	if spec.Stdout != nil {
		command.Stdout = spec.Stdout
	} else {
		command.Stdout = output
	}
	command.Stderr = output
	if err := command.Run(); err != nil || output.overflowed() {
		return ErrCommandFailed
	}
	return nil
}

type boundedOutputSink struct {
	mu        sync.Mutex
	remaining int64
	overflow  bool
}

func (sink *boundedOutputSink) Write(data []byte) (int, error) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if int64(len(data)) > sink.remaining {
		sink.overflow = true
		sink.remaining = 0
		return len(data), nil
	}
	sink.remaining -= int64(len(data))
	return len(data), nil
}

func (sink *boundedOutputSink) overflowed() bool {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.overflow
}

var _ io.Writer = (*boundedOutputSink)(nil)
