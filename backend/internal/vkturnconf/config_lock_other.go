//go:build !linux

package vkturnconf

import (
	"errors"
	"sync"
)

var (
	errConfigFileLocked = errors.New("vkturn: config file is locked")
	portableConfigLocks sync.Map
)

type portableConfigLock struct {
	token chan struct{}
}

func configLockFor(path string) *portableConfigLock {
	created := &portableConfigLock{token: make(chan struct{}, 1)}
	created.token <- struct{}{}
	actual, _ := portableConfigLocks.LoadOrStore(path, created)
	return actual.(*portableConfigLock)
}

func acquireConfigFileLock(path string) (func() error, error) {
	lock := configLockFor(path)
	<-lock.token
	return func() error {
		lock.token <- struct{}{}
		return nil
	}, nil
}

func tryAcquireConfigFileLock(path string) (func() error, error) {
	lock := configLockFor(path)
	select {
	case <-lock.token:
		return func() error {
			lock.token <- struct{}{}
			return nil
		}, nil
	default:
		return nil, errConfigFileLocked
	}
}

// Production persistence runs on Linux, where the parent directory is fsynced.
// Other platforms retain process-local serialization for development builds.
func syncConfigDirectory(string) error { return nil }
